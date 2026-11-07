package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.OnboardAuthSession
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 登录/接入结果面板的纯函数（`OnboardResultPanel`）。
 *
 * 线上投诉「登录成功没有明显的提示」：过去成功后只把接入弹窗底部的 `statusLine`
 * 改成「账号已接入」。这里锁住新行为：
 *  - 标题按接入方式如实措辞（登录类说「登录成功/失败」，Token/手动凭据说「接入成功/失败」）；
 *  - 「复制失败原因」只含渠道/方式/原因/错误码，绝不含 token、密码、设备码；
 *  - 服务端给的错误原文优先，取不到才把状态码翻译成人话。
 */
class OnboardResultPanelTest {
    private fun success(method: String = "workbuddy_device") = OnboardResultPanel(
        kind = OnboardResultKind.SUCCESS,
        providerName = "WorkBuddy Intl",
        method = method,
        accountLabel = "主力号",
        addedAccounts = 1,
        accountCount = 3,
    )

    private fun failure(
        method: String = "workbuddy_device",
        reason: String = "登录会话已过期，请重新开始登录",
        code: String = "expired",
    ) = OnboardResultPanel(
        kind = OnboardResultKind.FAILURE,
        providerName = "WorkBuddy Intl",
        method = method,
        accountLabel = "主力号",
        reason = reason,
        code = code,
        retry = OnboardPrimaryKind.StartDevice,
    )

    private fun auth(status: String, error: String = "") = OnboardAuthSession(
        id = "s1",
        providerId = "workbuddy-intl",
        method = "device_code",
        status = status,
        error = error,
    )

    @Test
    fun loginSuccessIsTitledLoginSucceeded() {
        val panel = success()
        assertTrue(panel.success)
        assertEquals("登录成功", onboardResultTitle(panel))
    }

    @Test
    fun loginFailureIsTitledLoginFailed() {
        assertFalse(failure().success)
        assertEquals("登录失败", onboardResultTitle(failure()))
    }

    @Test
    fun tokenAndManualAreCalledImportNotLogin() {
        assertEquals("接入成功", onboardResultTitle(success("token_json")))
        assertEquals("接入失败", onboardResultTitle(failure("manual_input")))
    }

    @Test
    fun loginFlowCoversOauthDeviceAndChannelLogins() {
        assertTrue(onboardResultLoginFlow("oauth_pkce"))
        assertTrue(onboardResultLoginFlow("device_code"))
        assertTrue(onboardResultLoginFlow("windsurf_login"))
        assertTrue(onboardResultLoginFlow("workbuddy_device"))
        assertTrue(onboardResultLoginFlow("trae_device"))
        assertFalse(onboardResultLoginFlow("token_json"))
        assertFalse(onboardResultLoginFlow("manual_input"))
    }

    @Test
    fun copyTextCarriesChannelMethodReasonAndCode() {
        val text = onboardResultCopyText(failure())
        assertTrue(text.contains("WorkBuddy Intl"))
        assertTrue(text.contains("登录失败"))
        assertTrue(text.contains("WorkBuddy 登录"))
        assertTrue(text.contains("登录会话已过期，请重新开始登录"))
        assertTrue(text.contains("expired"))
        assertTrue(text.contains("主力号"))
        assertFalse("末尾不该留换行", text.endsWith("\n"))
    }

    @Test
    fun copyTextFallsBackWhenReasonIsBlank() {
        val text = onboardResultCopyText(failure(reason = "", code = ""))
        assertTrue(text.contains("未知原因"))
        assertFalse("没有错误码就不该出现这一行", text.contains("错误码："))
    }

    @Test
    fun copyTextNeverContainsCredentialFields() {
        // 面板里根本没有可放凭据的字段；这条测试守住"将来也不加"这个约定。
        val text = onboardResultCopyText(failure())
        assertFalse(text.contains("token", ignoreCase = true))
        assertFalse(text.contains("password", ignoreCase = true))
        assertFalse(text.contains("api_key", ignoreCase = true))
    }

    @Test
    fun authTerminalReasonPrefersServerMessage() {
        assertEquals("code 已过期", onboardAuthTerminalReason(auth("error", error = "code 已过期")))
        assertEquals("授权已取消", onboardAuthTerminalReason(auth("cancelled")))
        assertEquals("授权已过期，请重新发起", onboardAuthTerminalReason(auth("expired")))
        assertEquals("授权失败", onboardAuthTerminalReason(auth("error")))
        assertEquals("授权未完成（pending）", onboardAuthTerminalReason(auth("pending")))
    }

    @Test
    fun deviceFailureReasonPrefersServerMessage() {
        assertEquals(
            "device flow denied",
            onboardDeviceFailureReason("failed", "device flow denied"),
        )
        assertEquals("登录会话已过期，请重新开始登录", onboardDeviceFailureReason("expired", ""))
        assertEquals("登录失败", onboardDeviceFailureReason("failed", ""))
        assertEquals("登录未完成（unknown）", onboardDeviceFailureReason("unknown", ""))
    }
}
