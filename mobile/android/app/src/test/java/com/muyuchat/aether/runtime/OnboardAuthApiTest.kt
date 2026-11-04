package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 手机端原生接入的纯逻辑合同。
 *
 * 每条都对着桌面 `js/views/onboard.js` 的具体行：
 *  - 方式判定 `onboard.js:207-216`
 *  - 文案表 `onboard.js:9-24`
 *  - 手动凭据表单 `onboard.js:297-354`
 *  - Token/JSON 粘贴 `onboard.js:356-452`
 */
class OnboardAuthApiTest {

    private fun provider(
        id: String,
        auths: List<String> = emptyList(),
        oauth: Boolean = false,
        schema: List<ProviderFormField> = emptyList(),
    ) = ProviderSummary(
        id = id,
        name = id,
        description = "",
        accountCount = 0,
        readyCount = 0,
        ready = false,
        off = false,
        brandColor = "",
        supportedAuths = auths,
        authFormSchema = schema,
        hasOAuth = oauth,
    )

    @Test
    fun methodsMirrorDesktopOnboardRules() {
        // supported_auths 打底；空则兜底 manual_input（onboard.js:216）
        assertEquals(listOf("manual_input"), onboardMethods(provider("custom-x")))
        assertEquals(
            listOf("oauth_pkce", "manual_input"),
            onboardMethods(provider("claude-sub", listOf("oauth_pkce", "manual_input"))),
        )
        // id 前缀补设备码（onboard.js:208-215）
        assertTrue(onboardMethods(provider("qoder-cn")).contains("qoder_device"))
        assertTrue(onboardMethods(provider("trae-cn")).contains("trae_device"))
        assertTrue(onboardMethods(provider("workbuddy-cn")).contains("workbuddy_device"))
        assertTrue(onboardMethods(provider("codebuddy-cn")).contains("codebuddy_device"))
        assertTrue(onboardMethods(provider("minimax-code-cn")).contains("minimax_device"))
        assertTrue(onboardMethods(provider("zed")).contains("zed_device"))
        assertTrue(onboardMethods(provider("kiro")).contains("kiro_device"))
        assertTrue(onboardMethods(provider("windsurf")).contains("windsurf_login"))
        // 前缀不匹配就不补：`qoder`（无连字符）不算
        assertEquals(listOf("manual_input"), onboardMethods(provider("qoder")))
        // 设备码方法只补一次
        assertEquals(1, onboardMethods(provider("zed", listOf("zed_device"))).count { it == "zed_device" })
    }

    @Test
    fun deviceKindMapsToServerRouteSegment() {
        assertEquals("trae", onboardDeviceKind("trae_device"))
        assertEquals("kiro", onboardDeviceKind("kiro_device"))
        assertEquals("zed", onboardDeviceKind("zed_device"))
        assertEquals("codebuddy", onboardDeviceKind("codebuddy_device"))
        assertEquals("workbuddy", onboardDeviceKind("workbuddy_device"))
        assertEquals("qoder", onboardDeviceKind("qoder_device"))
        assertEquals("minimax", onboardDeviceKind("minimax_device"))
        // 非设备码方式没有路由段
        assertNull(onboardDeviceKind("oauth_pkce"))
        assertNull(onboardDeviceKind("device_code"))
        assertNull(onboardDeviceKind("windsurf_login"))
    }

    @Test
    fun mobileCannotDoDesktopOnlyMethods() {
        val claude = provider("claude", listOf("local_import", "oauth_pkce", "manual_input"), oauth = true)
        assertTrue(onboardMethodBlockedBy("local_import", claude)!!.contains("桌面"))
        assertTrue(onboardMethodBlockedBy("webview_login", claude)!!.contains("WebView"))
        // windsurf_login 已开：手机上就是一个 POST（邮箱 + 密码 → token），不再拦。
        assertNull(onboardMethodBlockedBy("windsurf_login", claude))
        assertNull(onboardMethodBlockedBy("windsurf_login", provider("windsurf")))
        // 手机端能做的三种不受影响
        assertNull(onboardMethodBlockedBy("oauth_pkce", claude))
        assertNull(onboardMethodBlockedBy("manual_input", claude))
        assertNull(onboardMethodBlockedBy("token_json", claude))
    }

    @Test
    fun oauthMethodsNeedManifestOAuth() {
        val noOAuth = provider("plain", listOf("oauth_pkce", "device_code"))
        assertTrue(onboardMethodBlockedBy("oauth_pkce", noOAuth)!!.contains("OAuth"))
        assertTrue(onboardMethodBlockedBy("device_code", noOAuth)!!.contains("OAuth"))
        val withOAuth = noOAuth.copy(hasOAuth = true)
        assertNull(onboardMethodBlockedBy("oauth_pkce", withOAuth))
        assertNull(onboardMethodBlockedBy("device_code", withOAuth))
    }

    @Test
    fun tokenKeyFollowsDesktopPriority() {
        // onboard.js:421-428
        assertEquals(
            "api_key",
            onboardTokenKey(
                listOf(
                    ProviderFormField("access_token", "AT", "text"),
                    ProviderFormField("api_key", "Key", "password"),
                ),
            ),
        )
        assertEquals("access_token", onboardTokenKey(listOf(ProviderFormField("access_token", "AT", "text"))))
        assertEquals("token", onboardTokenKey(listOf(ProviderFormField("token", "T", "text"))))
        assertEquals("cookie", onboardTokenKey(listOf(ProviderFormField("cookie", "C", "textarea"))))
        // 没命中白名单就取第一个 password 字段
        assertEquals("session_key", onboardTokenKey(listOf(ProviderFormField("session_key", "S", "password"))))
        // 什么都没有时回落 access_token
        assertEquals("access_token", onboardTokenKey(emptyList()))
    }

    @Test
    fun plainTokenBecomesSingleCredential() {
        val schema = listOf(ProviderFormField("api_key", "API Key", "password", required = true))
        val items = onboardParseTokenInput("  sk-abc123  ", schema)
        assertEquals(1, items.size)
        assertEquals("sk-abc123", items[0]["api_key"])
        assertTrue(onboardParseTokenInput("   ", schema).isEmpty())
    }

    @Test
    fun jsonTokenAcceptsWrapperAndAliases() {
        // {tokens:{...}} 包装 + accessToken 别名（onboard.js:430-452）
        val wrapped = onboardParseTokenInput(
            """{"tokens":{"accessToken":"at-1","refreshToken":"rt-1"}}""",
            emptyList(),
        )
        assertEquals(1, wrapped.size)
        assertEquals("at-1", wrapped[0]["access_token"])
        assertEquals("rt-1", wrapped[0]["refresh_token"])
        assertEquals("at-1", wrapped[0]["accessToken"])

        // 平铺 + apiKey 别名 + 未知嵌套对象整体字符串化
        val flat = onboardParseTokenInput(
            """{"apiKey":"k-1","account_id":"a-1","meta":{"tier":"pro"}}""",
            emptyList(),
        )
        assertEquals("k-1", flat[0]["api_key"])
        assertEquals("a-1", flat[0]["account_id"])
        assertEquals("""{"tier":"pro"}""", flat[0]["meta"])

        // token 没有 access_token 时补一份（onboard.js:450）
        val single = onboardParseTokenInput("""{"token":"t-9"}""", emptyList())
        assertEquals("t-9", single[0]["access_token"])
    }

    @Test
    fun jsonArrayYieldsOneCredentialPerItem() {
        val items = onboardParseTokenInput(
            """[{"apiKey":"k1"},{"apiKey":"k2"}]""",
            emptyList(),
        )
        assertEquals(2, items.size)
        assertEquals("k1", items[0]["api_key"])
        assertEquals("k2", items[1]["api_key"])
    }

    @Test
    fun malformedJsonIsRejectedWithServerStyleMessage() {
        val error = runCatching { onboardParseTokenInput("{not json", emptyList()) }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertTrue((error as ManagementApiException).message!!.contains("JSON"))
    }

    @Test
    fun missingRequiredFieldsAreNamedByLabel() {
        val schema = listOf(
            ProviderFormField("api_key", "API Key", "password", required = true),
            ProviderFormField("base_url", "Base URL", "text", required = false),
        )
        assertEquals(listOf("API Key"), onboardMissingRequired(emptyMap(), schema))
        assertTrue(onboardMissingRequired(mapOf("api_key" to "k"), schema).isEmpty())
        assertTrue(onboardMissingRequired(mapOf("api_key" to "  "), schema).isNotEmpty())
    }

    @Test
    fun manualSchemaFallsBackToDesktopDefault() {
        val custom = provider("custom-x")
        assertEquals(listOf("api_key", "base_url"), onboardManualSchema(custom).map { it.key })
        val declared = provider(
            "deepseek",
            schema = listOf(ProviderFormField("api_key", "API Key", "password", required = true)),
        )
        assertEquals(listOf("api_key"), onboardManualSchema(declared).map { it.key })
    }

    @Test
    fun authSessionParsesWrappedAndBareShapes() {
        // handleAuthStart 回 {"session":{...}}（auth_api.go:77）
        val wrapped = parseAuthSession(
            """
            {"session":{"id":"s1","provider_id":"claude-sub","method":"oauth_pkce","status":"pending",
             "authorize_url":"https://x/authorize","redirect_uri":"http://127.0.0.1:4321/oauth/callback",
             "user_code":"AB-12","verification_uri":"https://x/device","interval":5}}
            """.trimIndent(),
        )
        assertEquals("s1", wrapped.id)
        assertEquals("pending", wrapped.status)
        assertTrue(wrapped.authorizeUrl.startsWith("https://x/authorize"))
        assertEquals("AB-12", wrapped.userCode)
        assertEquals(5, wrapped.intervalSeconds)
        assertTrue(wrapped.isTerminal.not())
        // 裸对象也接受
        assertEquals("s2", parseAuthSession("""{"id":"s2","status":"success"}""").id)
        assertTrue(parseAuthSession("""{"id":"s3","status":"success"}""").isSuccess)
        // 缺 id 直接报格式错误，不返回半截对象
        val error = runCatching { parseAuthSession("""{"status":"pending"}""") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
    }

    @Test
    fun deviceSessionAndPollParseServerShapes() {
        val session = parseDeviceSession(
            """{"id":"d1","user_code":"CODE-9","verification_uri":"https://v","interval":3}""",
        )
        assertEquals("d1", session.id)
        assertEquals("CODE-9", session.userCode)
        assertTrue(session.hasUserCode)
        val login = parseDeviceSession("""{"id":"d2","login_url":"https://login"}""")
        assertTrue(!login.hasUserCode)
        assertEquals("https://login", login.loginUrl)

        assertTrue(parseDevicePoll("""{"state":"done"}""").isDone)
        assertTrue(parseDevicePoll("""{"state":"pending"}""").isDone.not())
        assertTrue(parseDevicePoll("""{"state":"expired","error":"超时"}""").isFailed)
        assertEquals("超时", parseDevicePoll("""{"state":"expired","error":"超时"}""").error)
        // 缺 state 当 unknown（按失败处理，不装成功）
        assertTrue(parseDevicePoll("""{}""").isFailed)
    }
}
