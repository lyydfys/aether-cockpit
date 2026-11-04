package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道启停。
 *
 * 契约（`provider_switch_api.go`）：`POST /api/provider/on|off` + `{"id":…}`，
 * 成功回渠道列表（`respondProviders` 直接转发 `handleProviders` 的字节，
 * 与 `GET /api/providers` 同构）；只改 `DisabledProviders`，**不碰账号**。
 */
class ChannelToggleApiTest {
    @Test
    fun pathFollowsAction() {
        assertEquals("/api/provider/on", channelTogglePath(true))
        assertEquals("/api/provider/off", channelTogglePath(false))
    }

    @Test
    fun jsonCarriesOnlyTheId() {
        assertEquals("""{"id":"antigravity"}""", buildChannelToggleJson("antigravity"))
        // 不能夹带其它字段（尤其不能把凭据塞进 body）
        assertTrue(!buildChannelToggleJson("x").contains("key"))
    }

    @Test
    fun invalidIdsAreRejectedBeforeAnyRequest() {
        assertEquals("channel_id_empty", validateChannelId(""))
        assertEquals("channel_id_empty", validateChannelId("   "))
        assertEquals("channel_id_invalid", validateChannelId("a b"))
        assertEquals("channel_id_invalid", validateChannelId("../etc/passwd"))
        assertEquals("channel_id_invalid", validateChannelId("a?b"))
        assertNull(validateChannelId("minimax-code-global"))
    }

    @Test
    fun labelAndCopiesFollowState() {
        // off=true 表示当前已停用 → 按钮应是「启用」
        assertEquals("启用", channelToggleLabel(true))
        assertEquals("停用", channelToggleLabel(false))
        assertTrue(channelToggleConfirmTitle(true, "Gemini").contains("启用"))
        assertTrue(channelToggleConfirmTitle(false, "Gemini").contains("停用"))
        // 停用必须说清"模型会路由失败"
        assertTrue(channelToggleConfirmBody(false).contains("路由失败"))
        // 启用要说明账号冷却不会因此解除
        assertTrue(channelToggleConfirmBody(true).contains("冷却"))
    }

    @Test
    fun ackMustBeJsonObject() {
        parseChannelToggleAck("""{"providers":[{"id":"x"}]}""")
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseChannelToggleAck(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun resultEchoesWhatWasAsked() {
        val result = ChannelToggleResult(providerId = "antigravity", enabled = false)
        assertEquals("antigravity", result.providerId)
        assertTrue(!result.enabled)
        // 默认没有就地列表 → 调用方回退刷新
        assertNull(result.providers)
    }

    @Test
    fun ackAlsoCarriesTheFreshProviderList() {
        // respondProviders 直接把 handleProviders 的字节回给客户端，结构与 GET /api/providers 同构。
        // 有了它就能就地更新列表，不必整页刷新（整页刷新会走 Loading，把用户刚点的行甩走）。
        val body = """{"providers":[{"id":"cursor","name":"Cursor","description":"","brandColor":"",
            "provider":"cursor","ready":true,"off":true,"accountCount":0,"readyCount":0,"accounts":[]}]}"""
        val providers = parseProviders(body)
        assertEquals(1, providers.size)
        assertEquals("cursor", providers[0].id)
        assertTrue(providers[0].off)
        // 解析不出列表时调用方要能回退（因此 toggle() 里用 runCatching 兜住）
        assertTrue(runCatching { parseProviders("not-json") }.isFailure)
    }
}
