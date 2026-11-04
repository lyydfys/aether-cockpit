package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 网络连通诊断：`GET /api/network/diag?host=…&port=…`
 * （`gateway.go:1213` → `netdiag.go:81-99`；桌面 `channelnet.js:113-129`）。
 *
 * 本地校验规则与服务端一致（`netdiag.go:82-95`）：host 只收域名/IP，含 `/ \ ? & @` 或空格直接拒绝；
 * 端口可省略（默认 443）。
 */
class NetDiagTest {
    @Test
    fun requiresSession() {
        assertTrue(probeNeedsSession("/api/network/diag"))
    }

    @Test
    fun plainDomainDefaultsToPort443() {
        val input = parseNetDiagInput("  api.example.com ")
        assertEquals("", input.error)
        assertEquals("api.example.com", input.host)
        assertEquals("443", input.port)
    }

    @Test
    fun hostWithPortSplitsPort() {
        val input = parseNetDiagInput("1.2.3.4:8443")
        assertEquals("", input.error)
        assertEquals("1.2.3.4", input.host)
        assertEquals("8443", input.port)
    }

    @Test
    fun ipv6IsSupportedWithAndWithoutBrackets() {
        // 带方括号 + 端口
        val bracketed = parseNetDiagInput("[2001:db8::1]:8443")
        assertEquals("", bracketed.error)
        assertEquals("2001:db8::1", bracketed.host)
        assertEquals("8443", bracketed.port)
        // 裸 IPv6：多冒号不当端口，整段就是 host
        val bare = parseNetDiagInput("2001:db8::1")
        assertEquals("", bare.error)
        assertEquals("2001:db8::1", bare.host)
        assertEquals("443", bare.port)
        // 方括号后跟非端口 → 本地报错
        assertTrue(parseNetDiagInput("[::1]abc").error.isNotEmpty())
    }

    @Test
    fun illegalCharactersAreRejectedLocally() {
        // 与服务端同一个字符集（netdiag.go:88）
        for (raw in listOf(
            "http://api.example.com",
            "api.example.com/path",
            "api example.com",
            "api?x=1",
            "user@api.example.com",
            "api\\example.com",
            "a&b",
        )) {
            val input = parseNetDiagInput(raw)
            assertTrue("应拒绝：$raw", input.error.isNotEmpty())
            assertTrue("应说明规则：$raw", input.error.contains("host") || input.error.contains("空格"))
        }
    }

    @Test
    fun emptyInputSaysWhatToFill() {
        for (raw in listOf("", "   ")) {
            val input = parseNetDiagInput(raw)
            assertTrue(input.error.contains("先填上游域名"))
            assertFalse(input.error.contains("host 只能是"))
        }
    }

    @Test
    fun badPortsAreRejected() {
        assertTrue(parseNetDiagInput("api.example.com:0").error.contains("端口"))
        assertTrue(parseNetDiagInput("api.example.com:70000").error.contains("端口"))
        // 非数字后缀不当端口，整段当 host 交给服务端（服务端会按域名/IP 校验）
        val notPort = parseNetDiagInput("api.example.com:abc")
        assertEquals("api.example.com:abc", notPort.host)
        assertEquals("443", notPort.port)
    }

    @Test
    fun parsesFullDiagnosis() {
        val report = parseNetDiag(
            """
            {"host":"api.example.com","port":"443",
             "system_dns":["1.2.3.4","5.6.7.8"],
             "doh":{"cloudflare":["1.2.3.4"],"google":["1.2.3.4"]},
             "doh_errors":{"quad9":"timeout"},
             "ips":[{"ip":"1.2.3.4","ok":true,"colo":"HKG","tcp_ms":120,"tls_ms":300,
                     "ttfb_ms":480,"http_status":404,"tls_version":"TLS 1.3","alpn":"h2","verdict":"可用"},
                    {"ip":"5.6.7.8","ok":false,"tcp_ms":0,"tcp_error":"i/o timeout","verdict":"不可用"}],
             "summary":"2 个 IP：1 可用 / 1 不可用","elapsed_ms":1234}
            """.trimIndent(),
        )
        assertEquals("api.example.com", report.host)
        assertEquals("443", report.port)
        assertEquals(listOf("1.2.3.4", "5.6.7.8"), report.systemDns)
        assertEquals(2, report.doh.size)
        assertEquals("timeout", report.dohErrors["quad9"])
        assertEquals(2, report.ips.size)
        assertTrue(report.ips[0].ok)
        assertEquals(120L, report.ips[0].tcpMs)
        assertEquals("TLS 1.3", report.ips[0].tlsVersion)
        assertFalse(report.ips[1].ok)
        assertEquals("i/o timeout", report.ips[1].tcpError)
        assertEquals(1234L, report.elapsedMs)
    }

    @Test
    fun toleratesEmptyAndPartialPayloads() {
        val empty = parseNetDiag("""{"host":"a.com"}""")
        assertTrue(empty.ips.isEmpty())
        assertTrue(empty.systemDns.isEmpty())
        assertEquals("443", empty.port)
        // 缺 ip 的条目丢弃
        assertTrue(parseNetDiag("""{"ips":[{"ok":true}]}""").ips.isEmpty())
        // 负数耗时夹成 0
        val clamped = parseNetDiag("""{"ips":[{"ip":"1.1.1.1","tcp_ms":-5}]}""").ips[0]
        assertEquals(0L, clamped.tcpMs)
    }

    @Test
    fun brokenBodiesAreInvalidResponse() {
        for (body in listOf("not json", "", "<html>400</html>", "[1,2]")) {
            val error = runCatching { parseNetDiag(body) }.exceptionOrNull()
            assertTrue("body=<$body> 应抛 ManagementApiException，实际=${error?.javaClass?.name}", error is ManagementApiException)
            assertEquals(
                "body=<$body> 的错误码应是 invalid_response",
                "invalid_response",
                (error as ManagementApiException).code,
            )
        }
    }

    @Test
    fun stepsCoverDnsDohAndEachIp() {
        val report = parseNetDiag(
            """
            {"host":"a.com","port":"443","system_dns":["1.1.1.1"],
             "doh":{"cloudflare":["1.1.1.1"]},"doh_errors":{"quad9":"timeout"},
             "ips":[{"ip":"1.1.1.1","ok":true,"tcp_ms":10},
                    {"ip":"2.2.2.2","ok":false,"tcp_error":"refused"}]}
            """.trimIndent(),
        )
        val steps = netDiagSteps(report)
        // 顺序固定：系统 DNS → DoH 对照 → 每个 IP 一跳
        assertEquals("步数应为 DNS + DoH + 2 个 IP：$steps", 4, steps.size)
        assertEquals("第 0 步应是系统 DNS：$steps", "系统 DNS", steps[0].label)
        assertTrue("系统 DNS 应判成功（system_dns 非空且无 error）：$steps", steps[0].ok)
        assertEquals("第 1 步应是 DoH 对照：$steps", "DoH 对照", steps[1].label)
        assertTrue("DoH 至少有一个解析成功就判成功：$steps", steps[1].ok)
        assertTrue("DoH 明细应同时含成功与失败解析器：${steps[1].detail}", steps[1].detail.contains("timeout"))
        assertEquals("第 2 步应是第一个 IP 的 TCP：$steps", "TCP 1.1.1.1", steps[2].label)
        assertTrue("1.1.1.1 是 ok:true：$steps", steps[2].ok)
        assertTrue("成功那一跳的明细应是耗时：${steps[2].detail}", steps[2].detail.contains("10ms"))
        assertEquals("第 3 步应是第二个 IP 的 TCP：$steps", "TCP 2.2.2.2", steps[3].label)
        assertFalse("2.2.2.2 是 ok:false：$steps", steps[3].ok)
        // 失败原因必须进 detail，而不是丢失（桌面 channelnet.js:163 的 chip 也会写它）
        assertTrue("失败那一跳的明细应含 tcp_error：${steps[3].detail}", steps[3].detail.contains("refused"))
    }

    @Test
    fun dnsFailureIsReportedNotSwallowed() {
        val report = parseNetDiag("""{"host":"a.com","system_dns_error":"no such host","ips":[]}""")
        val steps = netDiagSteps(report)
        assertEquals("系统 DNS", steps[0].label)
        assertFalse(steps[0].ok)
        assertEquals("no such host", steps[0].detail)
    }

    @Test
    fun ipLineAndVerdictMatchDesktopShape() {
        val ok = NetDiagIp(
            ip = "1.2.3.4", ok = true, colo = "HKG", tcpMs = 120, tcpError = "",
            tlsMs = 300, tlsError = "", ttfbMs = 480, httpStatus = 404, httpError = "",
            tlsVersion = "TLS 1.3", alpn = "h2", verdict = "可用",
        )
        assertEquals("TCP 120ms · TLS 300ms · 首字节 480ms · TLS 1.3 · h2", netDiagIpLine(ok))
        assertEquals("可用", netDiagIpVerdict(ok))
        // 无耗时时不编 0ms
        assertEquals("—", netDiagIpLine(ok.copy(tcpMs = 0, tlsMs = 0, ttfbMs = 0, tlsVersion = "", alpn = "")))
        // 失败原因优先级：tcp > tls > http > verdict
        assertEquals("refused", netDiagIpVerdict(ok.copy(ok = false, tcpError = "refused")))
        assertEquals("tls handshake", netDiagIpVerdict(ok.copy(ok = false, tcpError = "", tlsError = "tls handshake")))
        assertEquals("500", netDiagIpVerdict(ok.copy(ok = false, tcpError = "", tlsError = "", httpError = "500")))
        assertEquals("不可用", netDiagIpVerdict(ok.copy(ok = false, tcpError = "", tlsError = "", httpError = "", verdict = "")))
    }

    @Test
    fun runningCopyWarnsAboutLongWait() {
        assertTrue(NET_DIAG_RUNNING_COPY.contains("25"))
        assertTrue(NET_DIAG_RUNNING_COPY.contains("诊断中"))
    }
}
