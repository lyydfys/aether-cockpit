package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Trae 四渠道（`trae-cn` / `trae-intl` / `trae-solo-cn` / `trae-solo`）：手机端**无法完成**网页授权。
 *
 * 原因（Go 侧已核实）：授权码必须由 Trae 桌面客户端回传（`login_channel=native_ide` /
 * `auth_type=local`），浏览器/WebView 只能拿到 `isRedirect`。所以：
 *  - 判定按 **provider id 前缀 `trae-`**（不看错误文案，那太脆）；
 *  - 登录类方式一律 blocked → 页面不渲染"点了必失败"的按钮；
 *  - 主入口落到「手动凭据 · Token」（唯一真能走通的路），并把原因写清楚。
 */
class OnboardTraeDesktopLoginTest {
    private val traeIds = listOf("trae-cn", "trae-intl", "trae-solo-cn", "trae-solo")

    @Test
    fun traePrefixChannelsRequireTheDesktopClient() {
        traeIds.forEach { id ->
            assertTrue("$id 应判为需桌面端", onboardLoginDesktopRequired(id))
        }
        // 前缀判定：大小写与首尾空格不敏感
        assertTrue(onboardLoginDesktopRequired("TRAE-CN"))
        assertTrue(onboardLoginDesktopRequired("  trae-solo  "))
        // 非 Trae 渠道不受影响（含"名字里带 trae 但不是 trae- 前缀"的）
        assertFalse(onboardLoginDesktopRequired("claude-sub"))
        assertFalse(onboardLoginDesktopRequired("cursor"))
        assertFalse(onboardLoginDesktopRequired("windsurf"))
        assertFalse(onboardLoginDesktopRequired("zed"))
        assertFalse(onboardLoginDesktopRequired("trae"))
        assertFalse(onboardLoginDesktopRequired(""))
    }

    @Test
    fun everyLoginMethodIsBlockedOnTrae() {
        val loginMethods = listOf(
            "oauth_pkce",
            "device_code",
            "windsurf_login",
            "qoder_device",
            "trae_device",
            "kiro_device",
            "zed_device",
        )
        traeIds.forEach { id ->
            loginMethods.forEach { method ->
                assertNotNull("$id 的 $method 必须被拦", onboardLoginMethodBlockedReason(method, id))
            }
        }
    }

    @Test
    fun tokenAndManualPathsStayAvailableOnTrae() {
        // 这两条不需要回调，是 Trae 真正能走通的接入路径，不能被拦。
        traeIds.forEach { id ->
            assertNull(onboardLoginMethodBlockedReason("manual_input", id))
            assertNull(onboardLoginMethodBlockedReason("token_json", id))
            assertNull(onboardLoginMethodBlockedReason("local_import", id))
        }
    }

    @Test
    fun nonTraeChannelsKeepTheirLoginMethods() {
        assertNull(onboardLoginMethodBlockedReason("oauth_pkce", "claude-sub"))
        assertNull(onboardLoginMethodBlockedReason("device_code", "claude-sub"))
        assertNull(onboardLoginMethodBlockedReason("oauth_pkce", "cursor"))
        assertNull(onboardLoginMethodBlockedReason("windsurf_login", "windsurf"))
    }

    @Test
    fun traeNoteSaysWhatWhyAndHow() {
        val note = onboardTraeDesktopLoginNote()
        assertTrue("说清不能做什么", note.contains("手机端无法完成"))
        assertTrue("说清为什么（桌面客户端回传）", note.contains("桌面客户端回传"))
        assertTrue("指向可行路径", note.contains("手动凭据") && note.contains("Token"))
        assertTrue("说清凭据怎么来", note.contains("access token"))
        // 逐字保留用户的原始措辞
        assertTrue(note.startsWith("手机端无法完成 Trae 网页授权：授权码需要由 Trae 桌面客户端回传。"))
    }

    @Test
    fun blockedMethodsProduceNoLoginButtonAndFallBackToToken() {
        // 复刻 `OnboardConnectController.open()` 的选项装配
        fun options(providerId: String, methods: List<String>): List<OnboardMethodOption> =
            methods.map {
                OnboardMethodOption(
                    id = it,
                    label = it,
                    blockedReason = onboardLoginMethodBlockedReason(it, providerId),
                )
            } + OnboardMethodOption("token_json", "Token / JSON")

        val trae = options("trae-cn", listOf("oauth_pkce", "device_code", "manual_input"))
        val enabledIds = trae.filter { it.enabled }.map { it.id }
        assertEquals("Trae 只应剩下手动凭据与 Token", listOf("manual_input", "token_json"), enabledIds)
        assertTrue(
            "登录类方法必须全部置灰",
            trae.filter { it.id != "manual_input" && it.id != "token_json" }.none { it.enabled },
        )
        // 主入口 = 第一个可用项；这里就是手动凭据（Token 紧随其后）
        assertEquals("manual_input", trae.first { it.enabled }.id)

        // 对照：非 Trae 渠道的 PKCE 仍然可用
        val claude = options("claude-sub", listOf("oauth_pkce", "manual_input"))
        assertTrue("oauth_pkce" in claude.filter { it.enabled }.map { it.id })
        assertTrue(claude.first { it.id == "oauth_pkce" }.enabled)
    }

    @Test
    fun traeEntryPointResolvesToTokenSubmission() {
        // 接入弹窗里，Trae 落到 token_json 时 footer 主按钮应是「导入并接入」而不是任何登录动作。
        val state = OnboardConnectState(
            providerId = "trae-solo",
            providerName = "Trae Solo",
            active = "token_json",
        )
        val action = onboardPrimaryAction(state)
        assertEquals(OnboardPrimaryKind.SubmitTokenJson, action.kind)
        assertEquals("导入并接入", action.label)
        assertFalse("Token 为空时置灰", action.enabled)
        // 明确不是任何登录按钮
        assertTrue(action.kind != OnboardPrimaryKind.StartAuth)
        assertTrue(action.kind != OnboardPrimaryKind.StartDevice)
    }
}
