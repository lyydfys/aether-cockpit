package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 分组就是鉴权面：匿名组（/healthz、/api/health）一个字节的凭据都不发，
 * 会话组（/readyz、/version、/metrics、/api/runtime）必须带 Bearer。
 * HTTP 层不在这里起服务，只测"发不发凭据"这个纯逻辑 + 各端点的 JSON 形状。
 */
class ProbeApiTest {
    private val session = "sess-abc-123"

    private fun runningStatus() = GatewayStatus(
        state = GatewayState.RUNNING,
        addr = "127.0.0.1:8787",
        adminSession = session,
    )

    @Test
    fun anonymousEndpointsSendNoAuthorization() {
        val status = runningStatus()
        for (path in listOf("/healthz", "/api/health")) {
            assertFalse("$path 不该要会话凭据", probeNeedsSession(path))
            assertNull("$path 不该带 Authorization", probeAuthorization(path, status))
        }
        // 没有会话凭据时，匿名端点照样不发头。
        assertNull(probeAuthorization("/api/health", status.copy(adminSession = "")))
        // 反过来确认 readyz/version/metrics 不在匿名组里：它们都要凭据。
        for (path in listOf("/readyz", "/version", "/metrics")) {
            assertNotNull("$path 不能被当成匿名端点", probeAuthorization(path, status))
        }
    }

    @Test
    fun sessionEndpointsAlwaysSendBearer() {
        val status = runningStatus()
        for (path in listOf("/readyz", "/version", "/metrics", "/api/runtime")) {
            assertTrue("$path 需要会话凭据", probeNeedsSession(path))
            assertEquals("Bearer $session", probeAuthorization(path, status))
        }
    }

    /** 报告的结构本身就是分组：匿名报告装不下 readyz/version/metrics。 */
    @Test
    fun reportShapesMatchTheAuthSurface() {
        assertEquals(listOf("health", "healthz"), reportFields(AnonymousProbeReport::class.java))
        assertEquals(listOf("metrics", "readyz", "runtime", "version"), reportFields(SessionProbeReport::class.java))
    }

    /** 只取真实属性名；Compose 编译器可能塞 `$stable` 之类的合成静态字段。 */
    private fun reportFields(type: Class<*>): List<String> = type.declaredFields
        .filterNot { it.isSynthetic || it.name.startsWith("$") }
        .map { it.name }
        .sorted()

    @Test
    fun parsesPlainTextHealthz() {
        val probe = parseHealthz("ok\n")
        assertEquals("ok", probe.status)
        assertTrue(probe.plainText)
    }

    /**
     * 移动运行时在 Gateway 之前注册了自己的 /healthz，回的是 JSON 而不是 `ok\n`；
     * 两种形状都必须是"活着"，其它内容一律当格式错误。
     */
    @Test
    fun parsesJsonHealthzAndRejectsOtherBodies() {
        val probe = parseHealthz("""{"status":"ok"}""")
        assertEquals("ok", probe.status)
        assertFalse(probe.plainText)

        for (body in listOf("", "  \n ", "not ok", """{"status":"down"}""", """{"status":123}""")) {
            assertInvalid { parseHealthz(body) }
        }
    }

    @Test
    fun parsesHealthFromApiHealth() {
        val health = parseHealth(
            """
            {"status":"ok","uptime_sec":31,"providers":3,"accounts":5,"version":"0.1.0","revision":"abc1234"}
            """.trimIndent(),
        )
        assertEquals("ok", health.status)
        assertEquals(31, health.uptimeSec)
        assertEquals(3, health.providers)
        assertEquals(5, health.accounts)
        assertEquals("0.1.0", health.version)
        assertEquals("abc1234", health.revision)
    }

    @Test
    fun parsesReadyzOk() {
        val probe = parseReadyz(
            """
            {"ready":true,"models":4,"providers":2,"providers_in_use":2,
             "accounts":3,"usable_accounts":3,"uptime_sec":42,"reasons":[]}
            """.trimIndent(),
        )
        assertEquals(200, probe.httpStatus)
        assertTrue(probe.ready)
        assertEquals(4, probe.models)
        assertEquals(2, probe.providers)
        assertEquals(2, probe.providersInUse)
        assertEquals(3, probe.accounts)
        assertEquals(3, probe.usableAccounts)
        assertEquals(42, probe.uptimeSec)
        assertTrue(probe.reasons.isEmpty())
    }

    /** 503 携带同一份 JSON：要能读出来当"未就绪"，不能当成网络失败。 */
    @Test
    fun parsesReadyzNotReadyFrom503() {
        val probe = parseReadyz(
            """
            {"ready":false,"models":0,"providers":1,"providers_in_use":0,"accounts":0,
             "usable_accounts":0,"uptime_sec":7,"reasons":["还没有任何账号：先在渠道页添加账号"]}
            """.trimIndent(),
            httpStatus = 503,
        )
        assertEquals(503, probe.httpStatus)
        assertFalse(probe.ready)
        assertEquals(0, probe.usableAccounts)
        assertEquals(1, probe.providers)
        assertEquals(1, probe.reasons.size)
        assertTrue(probe.reasons[0].contains("账号"))
    }

    @Test
    fun parsesVersionBuildInfo() {
        val info = parseVersion(
            """
            {"version":"0.1.0","go":"go1.24.0","revision":"abc1234","build_time":"2026-11-03T10:00:00Z",
             "build_time_source":"ldflags","uptime_sec":128}
            """.trimIndent(),
        )
        assertEquals("0.1.0", info.version)
        assertEquals("go1.24.0", info.go)
        assertEquals("abc1234", info.revision)
        assertEquals("2026-11-03T10:00:00Z", info.buildTime)
        assertEquals("ldflags", info.buildTimeSource)
        assertEquals(128, info.uptimeSec)
    }

    @Test
    fun parsesRuntime() {
        val runtime = parseRuntime(
            """
            {"uptime_sec":75,"go_version":"go1.24.0","goroutines":23,"heap_mb":11,
             "state_records":9,"models":6,"tun":true,"public_addr":"http://127.0.0.1:8787"}
            """.trimIndent(),
        )
        assertEquals(75, runtime.uptimeSec)
        assertEquals("go1.24.0", runtime.goVersion)
        assertEquals(23, runtime.goroutines)
        assertEquals(11, runtime.heapMb)
        assertEquals(9, runtime.stateRecords)
        assertEquals(6, runtime.models)
        assertTrue(runtime.tun)
        assertEquals("http://127.0.0.1:8787", runtime.publicAddr)
    }

    /** 缺字段要抛 invalid_response，不能静默返回一个字段全为默认值的空对象。 */
    @Test
    fun missingFieldsAreInvalidResponses() {
        // 少了 accounts
        assertInvalid {
            parseReadyz(
                """{"ready":true,"models":1,"providers":1,"providers_in_use":1,"usable_accounts":1,"uptime_sec":1,"reasons":[]}""",
            )
        }
        // reasons 不是数组
        assertInvalid {
            parseReadyz(
                """{"ready":true,"models":1,"providers":1,"providers_in_use":1,"accounts":1,"usable_accounts":1,"uptime_sec":1,"reasons":"none"}""",
            )
        }
        // version 只剩两个字段
        assertInvalid { parseVersion("""{"version":"0.1.0","go":"go1.24.0"}""") }
        // /api/health 只剩 status
        assertInvalid { parseHealth("""{"status":"ok"}""") }
        assertInvalid { parseRuntime("""{"uptime_sec":1,"go_version":"go1.24.0","goroutines":3}""") }
        // 全是空白的指标文本
        assertInvalid { parseMetrics(" \n \n") }
        // 完全不是 JSON
        assertInvalid { parseHealth("not json") }
    }

    @Test
    fun parsesMetricsDigest() {
        val digest = parseMetrics(
            """
            # HELP aether_uptime_seconds Seconds since the gateway started.
            # TYPE aether_uptime_seconds gauge
            aether_uptime_seconds 12.34

            aether_models 4
            aether_accounts 3
            aether_providers 2
            """.trimIndent(),
        )
        // 空行不计，其余 6 行都算非空行
        assertEquals(6, digest.lineCount)
        assertEquals(5, digest.preview.size)
        assertEquals("# HELP aether_uptime_seconds Seconds since the gateway started.", digest.preview[0])
        assertEquals("aether_uptime_seconds 12.34", digest.preview[2])
        assertTrue(digest.charCount > 0)
    }

    private fun assertInvalid(block: () -> Unit) {
        val error = parseErrorOf(block)
        assertNotNull("期望抛 ManagementApiException，但没抛", error)
        assertEquals("invalid_response", error?.code)
    }

    private fun parseErrorOf(block: () -> Unit): ManagementApiException? =
        runCatching(block).exceptionOrNull() as? ManagementApiException
}
