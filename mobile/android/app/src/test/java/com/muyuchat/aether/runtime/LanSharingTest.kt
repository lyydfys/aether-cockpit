package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 局域网共享的数据层合同（Go 侧对应 `internal/mobile.RuntimeConfig.ListenHost`
 * 与 `RuntimeStatus.lanEnabled/lanUrls`）。
 *
 * 口径（用户拍板）：
 *  - 默认关闭 → 只绑 127.0.0.1；
 *  - 打开 → 绑 0.0.0.0，对话接口(/v1)与**管理接口(/api)**同时对该网络开放，
 *    但管理接口仍然要求 App 内会话凭据（鉴权由 Go 强制，这里只管数据搬运）。
 */
class LanSharingTest {

    @Test
    fun listenHostSwitchesBetweenLoopbackAndAllInterfaces() {
        assertEquals("127.0.0.1", lanListenHost(false))
        assertEquals("0.0.0.0", lanListenHost(true))
        assertEquals(LOOPBACK_LISTEN_HOST, lanListenHost(false))
        assertEquals(LAN_LISTEN_HOST, lanListenHost(true))
    }

    @Test
    fun runtimeConfigCarriesTheChosenListenHost() {
        val off = buildRuntimeConfig("/d", "/c", "/t", listenHost = lanListenHost(false))
        val on = buildRuntimeConfig("/d", "/c", "/t", listenHost = lanListenHost(true))
        assertTrue(off.contains("\"listenHost\":\"127.0.0.1\""))
        assertTrue(on.contains("\"listenHost\":\"0.0.0.0\""))
    }

    @Test
    fun lanAddressHintIsOnlySentWhenKnown() {
        val without = buildRuntimeConfig("/d", "/c", "/t", listenHost = lanListenHost(true))
        assertTrue(!without.contains("lanAddressHint"))

        val withHint = buildRuntimeConfig(
            "/d", "/c", "/t",
            listenHost = lanListenHost(true),
            lanAddressHint = lanAddressHint(listOf("192.168.3.24")),
        )
        assertTrue(withHint.contains("\"lanAddressHint\":\"192.168.3.24\""))
    }

    @Test
    fun onlyRouteableLanIpv4CountsAsHint() {
        assertTrue(isRouteableLanIpv4("192.168.3.24"))
        assertTrue(isRouteableLanIpv4("10.0.0.2"))
        assertFalse(isRouteableLanIpv4("127.0.0.1"))
        assertFalse(isRouteableLanIpv4("0.0.0.0"))
        assertFalse(isRouteableLanIpv4("169.254.1.1"))
        assertFalse(isRouteableLanIpv4("::1"))
        assertFalse(isRouteableLanIpv4("example.com"))
        assertFalse(isRouteableLanIpv4("192.168.3.24/24"))
        assertFalse(isRouteableLanIpv4("192.168.3.999"))
        assertFalse(isRouteableLanIpv4("  "))

        assertEquals("", lanAddressHint(emptyList()))
        assertEquals(
            "192.168.3.24,10.0.0.2",
            lanAddressHint(listOf("192.168.3.24", " 127.0.0.1 ", "10.0.0.2", "192.168.3.24")),
        )
    }

    @Test
    fun preferenceParsingFailsClosed() {
        // 缺文件 / 坏 JSON / 非布尔值一律回落关闭（安全默认），绝不猜"开"。
        assertFalse(parseLanSharing(null))
        assertFalse(parseLanSharing(""))
        assertFalse(parseLanSharing("   "))
        assertFalse(parseLanSharing("not json"))
        assertFalse(parseLanSharing("{}"))
        assertFalse(parseLanSharing("""{"lanSharing":"true"}"""))
        assertFalse(parseLanSharing("""{"lanSharing":1}"""))
        assertTrue(parseLanSharing("""{"lanSharing":true}"""))
        assertFalse(parseLanSharing("""{"lanSharing":false}"""))
    }

    @Test
    fun preferenceJsonRoundTrips() {
        assertEquals(true, parseLanSharing(buildLanSharingJson(true)))
        assertEquals(false, parseLanSharing(buildLanSharingJson(false)))
    }

    @Test
    fun runtimeSnapshotParsesLanFields() {
        val snapshot = RuntimeSnapshot.parse(
            """
            {"state":"running","addr":"0.0.0.0:37521","startedAt":1,"adminSession":"a","callerKey":"c",
             "lanEnabled":true,"lanUrls":["http://192.168.1.23:37521","http://10.0.0.2:37521"]}
            """.trimIndent(),
        )
        assertTrue(snapshot.running)
        assertTrue(snapshot.lanEnabled)
        assertEquals(
            listOf("http://192.168.1.23:37521", "http://10.0.0.2:37521"),
            snapshot.lanUrls,
        )
    }

    @Test
    fun runtimeSnapshotDefaultsWhenLanFieldsAreAbsent() {
        val snapshot = RuntimeSnapshot.parse(
            """{"state":"running","addr":"127.0.0.1:37521","startedAt":1}""",
        )
        assertFalse(snapshot.lanEnabled)
        assertTrue(snapshot.lanUrls.isEmpty())
    }

    @Test
    fun runtimeSnapshotToleratesWrongLanUrlTypes() {
        // 服务端若给了空串/非字符串项，只丢那一项，不整包失败。
        val snapshot = RuntimeSnapshot.parse(
            """{"state":"running","lanEnabled":true,"lanUrls":["http://192.168.1.23:1",""]}""",
        )
        assertEquals(listOf("http://192.168.1.23:1"), snapshot.lanUrls)
        val notArray = RuntimeSnapshot.parse("""{"state":"running","lanUrls":"http://x:1"}""")
        assertTrue(notArray.lanUrls.isEmpty())
    }

    @Test
    fun gatewayStatusDerivesLanV1UrlsAndLoopbackFlag() {
        val running = GatewayStatus(
            state = GatewayState.RUNNING,
            addr = "0.0.0.0:37521",
            lanEnabled = true,
            lanUrls = listOf("http://192.168.1.23:37521/"),
        )
        assertTrue(running.listeningBeyondLoopback)
        assertEquals(listOf("http://192.168.1.23:37521/v1"), running.lanV1Urls)

        val loopback = GatewayStatus(state = GatewayState.RUNNING, addr = "127.0.0.1:37521")
        assertFalse(loopback.listeningBeyondLoopback)
        assertTrue(loopback.lanV1Urls.isEmpty())
        // 没开 LAN 但 addr 已是具体网卡地址时也算"超出回环"。
        assertTrue(
            GatewayStatus(state = GatewayState.RUNNING, addr = "192.168.1.23:37521")
                .listeningBeyondLoopback,
        )
    }

    @Test
    fun emptyLanUrlsMeansAddressUnknown() {
        val status = GatewayStatus(
            state = GatewayState.RUNNING,
            addr = "0.0.0.0:37521",
            lanEnabled = true,
            lanUrls = emptyList(),
        )
        // 页面据此显示「未获取到局域网地址」，不能自己编一个。
        assertTrue(status.lanV1Urls.isEmpty())
        assertTrue(status.listeningBeyondLoopback)
    }
}
