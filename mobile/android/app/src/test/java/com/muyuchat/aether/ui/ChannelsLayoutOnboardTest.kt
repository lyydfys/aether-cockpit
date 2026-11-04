package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度页结构（桌面 `channels.js:183-192`）与接入页文案（桌面 `onboard.js:11-26,209`）的纯逻辑。
 */
class ChannelsLayoutOnboardTest {
    @Test
    fun gridFallsBackToOneColumnAtLargerFontScale() {
        // 桌面 channels.js:183：list 一列、grid 两列
        assertEquals(1, accountGridColumns(AccountsLayout.LIST, 1.0f))
        assertEquals(2, accountGridColumns(AccountsLayout.GRID, 1.0f))
        assertEquals(2, accountGridColumns(AccountsLayout.GRID, 1.29f))
        // 1.3× 及以上字号退回单列，避免两列把额度条/按钮挤坏
        assertEquals(1, accountGridColumns(AccountsLayout.GRID, 1.3f))
        assertEquals(1, accountGridColumns(AccountsLayout.GRID, 2.0f))
    }

    @Test
    fun layoutLabelsMatchDesktopToggle() {
        assertEquals("列表", accountLayoutLabel(AccountsLayout.LIST))
        assertEquals("网格", accountLayoutLabel(AccountsLayout.GRID))
    }

    /** 桌面 `channels.js:186`：`filter.layout !== 'list' && list.length < 3` 才补虚线卡。 */
    @Test
    fun addChannelCardOnlyInGridWithOneOrTwoAccounts() {
        // 网格 + 1/2 个账号 → 出现
        assertTrue(showAddChannelCard(AccountsLayout.GRID, 1.0f, 1))
        assertTrue(showAddChannelCard(AccountsLayout.GRID, 1.0f, 2))
        // 0 个走空态、3 个以上网格已填满 → 不出现
        assertFalse(showAddChannelCard(AccountsLayout.GRID, 1.0f, 0))
        assertFalse(showAddChannelCard(AccountsLayout.GRID, 1.0f, 3))
        assertFalse(showAddChannelCard(AccountsLayout.GRID, 1.0f, 9))
        // 列表布局永远不出现（桌面 list 时没有虚线卡）
        assertFalse(showAddChannelCard(AccountsLayout.LIST, 1.0f, 1))
        assertFalse(showAddChannelCard(AccountsLayout.LIST, 1.0f, 2))
        // 大字号退单列时也不出现（此时等价于 list）
        assertFalse(showAddChannelCard(AccountsLayout.GRID, 1.3f, 1))
        assertFalse(showAddChannelCard(AccountsLayout.GRID, 2.0f, 2))
    }

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
    fun providerMetaShowsAccountCountAndAvailableMethod() {
        assertEquals("已有 2 个账号 · 可用：Qoder 登录", onboardProviderMeta("qoder-cn", 2))
        assertEquals("已有 0 个账号 · 可用：账号包导入", onboardProviderMeta("claude-sub", 0))
    }

    @Test
    fun methodsLineUsesDesktopLabels() {
        val line = onboardMethodsLine("claude-sub")
        for (label in listOf("本机导入", "浏览器授权", "设备码", "手动凭据")) {
            assertTrue("$label in $line", line.contains(label))
        }
        assertTrue(line.startsWith("桌面端支持的接入方式："))
        // Qoder 走设备码那条
        assertTrue(onboardMethodsLine("qoder-cn").contains("Qoder 登录"))
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
        assertEquals(expected, ONBOARD_METHOD_LABELS)
    }

    @Test
    fun unsupportedHintNamesWhatPhonesCannotDo() {
        val hint = onboardUnsupportedHint()
        assertTrue(hint.contains("Qoder 设备码登录"))
        assertTrue(hint.contains("账号包导入"))
        assertTrue(hint.contains("桌面端"))
    }
}
