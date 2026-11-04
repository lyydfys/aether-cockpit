package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 应用内 WebView 登录窗的两条安全判定（纯函数，真机逻辑见 `OnboardWebLoginDialog.kt`）。
 *
 * 除了"正常地址放行"，重点锁**伪装域名**：`ecosystem.127.0.0.1.com`、
 * `127.0.0.1.evil.com` 这类必须拦；非 http(s) scheme（`intent://`、`file://`、`javascript:`）也一律拦。
 */
class OnboardWebLoginUrlTest {
    private val providerHosts = setOf("accounts.google.com")

    @Test
    fun callbackUrlOnlyMatchesLoopbackWithTheGatewayPath() {
        assertTrue(isOauthCallbackUrl("http://127.0.0.1:8765/oauth/callback"))
        assertTrue("带查询串也要认（?code=…）", isOauthCallbackUrl("http://127.0.0.1:8765/oauth/callback?code=abc&state=x"))
        assertTrue(isOauthCallbackUrl("http://localhost:8765/oauth/callback"))
        assertTrue("大小写不敏感", isOauthCallbackUrl("HTTP://127.0.0.1:8765/oauth/callback"))

        assertFalse("路径不对", isOauthCallbackUrl("http://127.0.0.1:8765/oauth/other"))
        assertFalse("不是回环", isOauthCallbackUrl("http://evil.com/oauth/callback"))
        assertFalse("后缀伪装", isOauthCallbackUrl("http://127.0.0.1.evil.com/oauth/callback"))
        assertFalse("前缀伪装", isOauthCallbackUrl("http://evil-127.0.0.1.com/oauth/callback"))
        assertFalse("空串", isOauthCallbackUrl(""))
        assertFalse("非 http scheme", isOauthCallbackUrl("javascript:alert(1)"))
    }

    @Test
    fun allowedUrlAcceptsProviderDomainAndItsSubdomains() {
        assertTrue(isAllowedAuthUrl("https://accounts.google.com/o/oauth2/v2/auth?x=1", providerHosts))
        assertTrue("大小写不敏感", isAllowedAuthUrl("HTTPS://Accounts.Google.COM/o/oauth2/auth", providerHosts))
        assertTrue("端口不影响 host 判定", isAllowedAuthUrl("https://accounts.google.com:8443/auth", providerHosts))
        // 授权域的父域 / 子域：子域放行（同一站点），父域不放行
        assertTrue(isAllowedAuthUrl("https://sub.accounts.google.com/auth", providerHosts))
        assertFalse(isAllowedAuthUrl("https://google.com/auth", providerHosts))
    }

    @Test
    fun allowedUrlAcceptsLoopbackButNotLookalikes() {
        assertTrue(isAllowedAuthUrl("http://127.0.0.1:8765/oauth/callback", providerHosts))
        assertTrue(isAllowedAuthUrl("http://localhost:1234/whatever", providerHosts))
        assertFalse("后缀伪装（127.0.0.1 不是纯域名后缀）", isAllowedAuthUrl("http://127.0.0.1.evil.com/x", providerHosts))
        assertFalse("前缀伪装", isAllowedAuthUrl("http://evil-127.0.0.1.com/x", providerHosts))
        assertFalse("相似域名不算", isAllowedAuthUrl("https://evil-accounts.google.com/x", providerHosts))
    }

    @Test
    fun dangerousSchemesAreRejectedEvenOnWhitelistedHosts() {
        for (url in listOf(
            "intent://accounts.google.com/x#Intent;scheme=https;end",
            "file:///sdcard/secret.txt",
            "javascript:alert(document.cookie)",
            "data:text/html,<h1>x</h1>",
            "about:blank",
            "content://com.example/x",
        )) {
            assertFalse("必须拦：$url", isAllowedAuthUrl(url, providerHosts))
        }
    }

    @Test
    fun malformedInputNeverThrows() {
        for (url in listOf("", "   ", "not a url", "http://", "://x", "https:///nohost")) {
            assertFalse("不抛异常且拒绝：$url", isAllowedAuthUrl(url, providerHosts))
            assertFalse("不抛异常且拒绝：$url", isOauthCallbackUrl(url))
        }
        // 空 host 白名单时，只有回环地址还能放行
        assertEquals("127.0.0.1", authUrlHost("http://127.0.0.1:8765/oauth/callback"))
        assertNull(authUrlHost("not a url"))
        assertTrue(isAllowedAuthUrl("http://127.0.0.1:1/x", emptySet()))
        assertFalse(isAllowedAuthUrl("https://accounts.google.com/x", emptySet()))
    }
}
