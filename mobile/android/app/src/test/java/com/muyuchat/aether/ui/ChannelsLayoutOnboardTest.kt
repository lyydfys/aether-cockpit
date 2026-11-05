package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ProviderSummary
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度页结构（桌面 `channels.js:183-192`）与接入页文案（桌面 `onboard.js:11-26,209`）的纯逻辑。
 */
class ChannelsLayoutOnboardTest {
    // 账号区布局固定单列（用户要求去掉网格），原 accountGridColumns / accountLayoutLabel /
    // showAddChannelCard 三条测试随实现一并删除，不再为已移除的布局档位立契约。

    @Test
    fun qoderDeviceLoginOnlyForQoderProviders() {
        // 桌面 onboard.js:209：/^qoder-/ 的渠道额外挂 qoder_device
        assertTrue(hasQoderDeviceLogin("qoder-cn"))
        assertTrue(hasQoderDeviceLogin("qoder-intl"))
        assertTrue(hasQoderDeviceLogin("  Qoder-CN "))
        assertFalse(hasQoderDeviceLogin("claude-sub"))
        assertFalse(hasQoderDeviceLogin("qoder"))
        assertFalse(hasQoderDeviceLogin(""))
    }

    @Test
    fun providerMetaShowsAccountCountAndAvailableMethods() {
        // 卡片副标题现在按「手机端真正能做的方式」拼（旧文案「可用：账号包导入」已废）：
        // supported_auths 为空 → 兜底 manual_input（onboard.js:216），再加常驻的 Token / JSON。
        assertEquals("已有 0 个账号 · 可用：手动凭据 / Token / JSON", onboardProviderMeta(provider("claude-sub"), 0))
        // qoder- 前缀：设备码那条由 id 补进来（onboard.js:209）
        val qoder = provider("qoder-cn", auths = listOf("oauth_pkce", "manual_input"))
        assertEquals(
            "已有 2 个账号 · 可用：手动凭据 / Qoder 登录 / Token / JSON",
            onboardProviderMeta(qoder, 2),
        )
        // 声明了 OAuth 参数时浏览器授权才可用（onboardMethodBlockedBy）
        val withOAuth = provider("claude-sub", auths = listOf("oauth_pkce", "manual_input")).copy(hasOAuth = true)
        assertEquals(
            "已有 0 个账号 · 可用：浏览器授权 / 手动凭据 / Token / JSON",
            onboardProviderMeta(withOAuth, 0),
        )
    }

    @Test
    fun accountOnlyChannelsSaySoBeforeUserSendsARequest() {
        // cursor / windsurf / zed 只有账号/额度/登录能力：接入页要提前说清"不转发对话"，
        // 别等用户发请求才撞 503/400。按钮不禁用——接入后能看额度，本身有意义。
        val hint = onboardChatUnsupportedHint()
        assertTrue("要点明只支持账号与额度", hint.contains("账号") && hint.contains("额度"))
        assertTrue("要点明不支持对话转发", hint.contains("对话转发"))
        assertTrue("措辞要说明是'暂不支持'而不是'禁止'", hint.contains("暂不支持"))

        // 默认值：老构造点 / 老服务端没有 chat 字段时按"支持"处理。
        assertTrue(provider("claude-sub").chat)
        assertFalse(provider("cursor").copy(chat = false).chat)
    }

    @Test
    fun methodLabelTableCoversDesktopKeys() {
        // 桌面 onboard.js:11-26 的 14 个键一个不少（文案逐字相同）
        val expected = mapOf(
            "local_import" to "本机导入",
            "oauth_pkce" to "浏览器授权",
            "device_code" to "设备码",
            "manual_input" to "手动凭据",
            "webview_login" to "浏览器登录",
            "minimax_device" to "MiniMax 设备码",
            "qoder_device" to "Qoder 登录",
            "workbuddy_device" to "WorkBuddy 登录",
            "codebuddy_device" to "CodeBuddy 登录",
            "zed_device" to "Zed 登录",
            "kiro_device" to "Kiro 登录",
            "trae_device" to "Trae 登录",
            "windsurf_login" to "Windsurf 登录",
            "token_json" to "Token / JSON",
        )
        assertEquals(expected, com.muyuchat.aether.runtime.ONBOARD_METHOD_LABELS)
    }

    private fun provider(id: String, auths: List<String> = emptyList()): ProviderSummary = ProviderSummary(
        id = id,
        name = id,
        description = "",
        accountCount = 0,
        readyCount = 0,
        ready = false,
        off = false,
        brandColor = "",
        supportedAuths = auths,
    )
}
