package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.OnboardAuthSession
import com.muyuchat.aether.runtime.OnboardDeviceSession
import com.muyuchat.aether.runtime.ProviderFormField
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 接入弹窗 footer 主按钮的判定（`onboardPrimaryAction`）。
 *
 * 线上 bug：主提交原来放在弹窗**滚动内容区的最底部**，长表单（手动凭据 / 设备码 / 浏览器授权）
 * 会把按钮滚出屏幕，用户只看到「关闭」，以为功能坏了。现在统一由 footer 渲染，这里锁行为：
 *  - 每种接入方式在该阶段有且只有一个主动作；
 *  - 必填缺失 → 置灰 **且给原因**（不是让用户点了才报错）；
 *  - busy → 置灰，文案换成「进行中」。
 */
class OnboardPrimaryActionTest {
    private fun state(
        active: String,
        busy: Boolean = false,
        tokenInput: String = "",
        schema: List<ProviderFormField> = emptyList(),
        values: Map<String, String> = emptyMap(),
        email: String = "",
        password: String = "",
        auth: OnboardAuthSession? = null,
        device: OnboardDeviceSession? = null,
    ) = OnboardConnectState(
        providerId = "p",
        providerName = "P",
        active = active,
        busy = busy,
        tokenInput = tokenInput,
        manualSchema = schema,
        manualValues = values,
        windsurfEmail = email,
        windsurfPassword = password,
        auth = auth,
        device = device,
    )

    private fun required(key: String, label: String) =
        ProviderFormField(key = key, label = label, placeholder = "", type = "text", required = true)

    private fun optional(key: String, label: String) =
        ProviderFormField(key = key, label = label, placeholder = "", type = "text", required = false)

    @Test
    fun tokenJsonNeedsAnInputAndSaysWhyItIsDisabled() {
        val empty = onboardPrimaryAction(state("token_json"))
        assertEquals(OnboardPrimaryKind.SubmitTokenJson, empty.kind)
        assertEquals("导入并接入", empty.label)
        assertFalse("空输入必须置灰", empty.enabled)
        assertNotNull("置灰要给原因", empty.hint)

        val filled = onboardPrimaryAction(state("token_json", tokenInput = "  sk-abc  "))
        assertTrue(filled.enabled)
        assertNull("可点的时候不需要提示", filled.hint)
    }

    @Test
    fun manualFormOnlyRequiresTheSchemaRequiredFields() {
        val schema = listOf(required("key", "Access Token"), optional("project", "GCP Project"))
        val missing = onboardPrimaryAction(state("manual_input", schema = schema))
        assertFalse(missing.enabled)
        assertTrue("原因里要点名缺哪个字段", missing.hint!!.contains("Access Token"))
        assertFalse("选填字段不该出现在原因里", missing.hint.contains("GCP Project"))

        // 必填填好、选填留空 → 可以提交
        val ok = onboardPrimaryAction(
            state("manual_input", schema = schema, values = mapOf("key" to "tok")),
        )
        assertTrue(ok.enabled)
        assertNull(ok.hint)

        // 只有选填字段的渠道：空表单也允许提交
        val optionalOnly = onboardPrimaryAction(
            state("manual_input", schema = listOf(optional("project", "GCP Project"))),
        )
        assertTrue(optionalOnly.enabled)
    }

    @Test
    fun windsurfNeedsBothEmailAndPassword() {
        assertFalse(onboardPrimaryAction(state("windsurf_login", email = "a@b.c")).enabled)
        assertFalse(onboardPrimaryAction(state("windsurf_login", password = "pw")).enabled)
        val ok = onboardPrimaryAction(state("windsurf_login", email = "a@b.c", password = "pw"))
        assertTrue(ok.enabled)
        assertEquals("登录并接入", ok.label)
    }

    @Test
    fun oauthWalksThroughStartCallbackAndCommit() {
        // 尚未开始
        assertEquals(OnboardPrimaryKind.StartAuth, onboardPrimaryKind(state("oauth_pkce")))
        assertEquals("开始授权", onboardPrimaryAction(state("oauth_pkce")).label)

        // 已拿到 authorize_url：可以「我已授权，继续」
        val pending = OnboardAuthSession(
            id = "s1",
            providerId = "claude",
            method = "oauth_pkce",
            status = "pending",
            authorizeUrl = "https://example.com/authorize",
        )
        assertEquals(OnboardPrimaryKind.SubmitCallback, onboardPrimaryKind(state("oauth_pkce", auth = pending)))
        assertEquals("我已授权，继续", onboardPrimaryAction(state("oauth_pkce", auth = pending)).label)

        // 授权成功：下一步是入库
        val success = pending.copy(status = "success")
        assertEquals(OnboardPrimaryKind.CommitAuth, onboardPrimaryKind(state("oauth_pkce", auth = success)))
        assertEquals("完成接入", onboardPrimaryAction(state("oauth_pkce", auth = success)).label)

        // 设备码已展示（无 authorize_url、无 success）：正文里的「打开授权页面」是主操作，footer 不抢
        val deviceCode = pending.copy(authorizeUrl = "", userCode = "ABCD-1234", status = "pending")
        val none = onboardPrimaryAction(state("device_code", auth = deviceCode))
        assertEquals(OnboardPrimaryKind.None, none.kind)
        assertFalse("没有主动作时 footer 只留关闭", none.visible)
    }

    @Test
    fun channelDeviceLoginOnlyStartsOnce() {
        assertEquals(OnboardPrimaryKind.StartDevice, onboardPrimaryKind(state("trae_device")))
        assertEquals("开始登录", onboardPrimaryAction(state("trae_device")).label)

        val started = state("trae_device", device = OnboardDeviceSession(id = "d1", loginUrl = "https://x/login"))
        assertEquals(OnboardPrimaryKind.None, onboardPrimaryKind(started))
    }

    @Test
    fun busyAlwaysDisablesAndSwitchesToProgressLabel() {
        val cases = listOf(
            "token_json" to "提交中…",
            "manual_input" to "提交中…",
            "windsurf_login" to "登录中…",
            "oauth_pkce" to "处理中…",
            "trae_device" to "处理中…",
        )
        for ((active, label) in cases) {
            // 即使字段都填好，busy 时也必须置灰
            val action = onboardPrimaryAction(
                state(
                    active,
                    busy = true,
                    tokenInput = "sk-abc",
                    schema = listOf(required("key", "Access Token")),
                    values = mapOf("key" to "tok"),
                    email = "a@b.c",
                    password = "pw",
                ),
            )
            assertFalse("busy 时必须置灰：$active", action.enabled)
            assertEquals("busy 文案：$active", label, action.label)
        }
    }

    // ---- 登录按钮文案：要跟"在哪打开"对齐，不能一个词打天下 ----

    @Test
    fun inAppLoginCopySaysItStaysInsideTheApp() {
        assertEquals("在应用内登录", onboardAuthButtonLabel(OnboardAuthActionKind.InAppLogin))
        // 应用内打开不需要"将在浏览器中打开"这种提醒
        assertNull(onboardAuthButtonHint(OnboardAuthActionKind.InAppLogin))
    }

    @Test
    fun reopenedInAppLoginSaysReopen() {
        assertEquals("重新打开登录页", onboardAuthButtonLabel(OnboardAuthActionKind.InAppLoginReopen))
        assertNull(onboardAuthButtonHint(OnboardAuthActionKind.InAppLoginReopen))
    }

    @Test
    fun externalBrowserCopyWarnsBeforeLeavingTheApp() {
        assertEquals("打开授权页面", onboardAuthButtonLabel(OnboardAuthActionKind.ExternalBrowser))
        assertEquals("将在浏览器中打开", onboardAuthButtonHint(OnboardAuthActionKind.ExternalBrowser))
    }

    @Test
    fun desktopOnlyCopyDoesNotOfferAFailingButton() {
        assertEquals("该渠道需在桌面端登录", onboardAuthButtonLabel(OnboardAuthActionKind.DesktopOnly))
        val hint = onboardAuthButtonHint(OnboardAuthActionKind.DesktopOnly)
        assertNotNull("桌面端场景要给替代路径", hint)
        assertTrue("替代路径应指向 Token / JSON", hint!!.contains("Token / JSON"))
    }

    @Test
    fun theVagueOpenLoginPageWordingIsGoneForEveryScenario() {
        val labels = OnboardAuthActionKind.values().map { onboardAuthButtonLabel(it) }
        assertFalse("不许再出现笼统的「打开登录页面」", labels.any { it.contains("打开登录页面") })
        assertTrue("四种场景都不能是空文案", labels.none { it.isBlank() })
        assertEquals("四种场景必须各不相同（不能一个词打天下）", labels.size, labels.toSet().size)
    }

    @Test
    fun actionKindRoutesPkceInAppAndDeviceCodesToBrowser() {
        // PKCE 走本机回环 → 应用内
        assertEquals(OnboardAuthActionKind.InAppLogin, onboardAuthActionKind("oauth_pkce"))
        assertEquals(
            OnboardAuthActionKind.InAppLoginReopen,
            onboardAuthActionKind("oauth_pkce", reopened = true),
        )
        // 设备码 / 渠道私有设备码 → 外部浏览器
        assertEquals(OnboardAuthActionKind.ExternalBrowser, onboardAuthActionKind("device_code"))
        assertEquals(OnboardAuthActionKind.ExternalBrowser, onboardAuthActionKind("trae_device"))
        assertEquals(OnboardAuthActionKind.ExternalBrowser, onboardAuthActionKind("windsurf_login"))
        // 手机端做不了 → 桌面端，不给会失败的按钮
        assertEquals(
            OnboardAuthActionKind.DesktopOnly,
            onboardAuthActionKind("oauth_pkce", blockedReason = "需要桌面端浏览器"),
        )
    }
}
