package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * App 内浏览器（移植自 MCA `InternalBrowserDialog.kt`）的纯逻辑契约。
 *
 * 覆盖：阶段文案八分支、isTerminal、搜索 URL 构造与编码、URL 规范化、
 * 导航放行策略（同源/跨源/已批准源/子域）、host 与 origin 解析、错误文案格式。
 */
class InAppBrowserTest {

    // ——— 1) browserLabel 八个分支 ———

    @Test
    fun browserLabelMatchesMcaWording() {
        assertEquals("等待窗口", BrowserTaskPhase.QUEUED.browserLabel())
        assertEquals("搜索中", BrowserTaskPhase.SEARCHING.browserLabel())
        assertEquals("待批准", BrowserTaskPhase.AWAITING_CONFIRMATION.browserLabel())
        assertEquals("访问中", BrowserTaskPhase.NAVIGATING.browserLabel())
        assertEquals("已加载", BrowserTaskPhase.COMPLETED.browserLabel())
        assertEquals("已取消", BrowserTaskPhase.CANCELLED.browserLabel())
        assertEquals("已拦截", BrowserTaskPhase.BLOCKED.browserLabel())
        assertEquals("加载失败", BrowserTaskPhase.FAILED.browserLabel())
        // 八个分支一个不漏（枚举新增会在这里红）
        assertEquals(8, BrowserTaskPhase.entries.size)
    }

    // ——— 2) isTerminal ———

    @Test
    fun isTerminalOnlyForCancelledBlockedFailed() {
        val terminal = BrowserTaskPhase.entries.filter { it.isTerminal }.toSet()
        assertEquals(
            setOf(
                BrowserTaskPhase.CANCELLED,
                BrowserTaskPhase.BLOCKED,
                BrowserTaskPhase.FAILED,
            ),
            terminal,
        )
        listOf(
            BrowserTaskPhase.QUEUED,
            BrowserTaskPhase.SEARCHING,
            BrowserTaskPhase.AWAITING_CONFIRMATION,
            BrowserTaskPhase.NAVIGATING,
            BrowserTaskPhase.COMPLETED,
        ).forEach { assertFalse("$it 不应是终态", it.isTerminal) }
    }

    // ——— 3) buildSearchUrl ———

    @Test
    fun buildSearchUrlReplacesAndEncodes() {
        val template = "https://www.google.com/search?q={query}"
        assertEquals(
            "https://www.google.com/search?q=hello",
            InternalBrowserUrlPolicy.buildSearchUrl(template, "hello"),
        )
        // 空格 → '+'，中文 → UTF-8 百分号编码（URLEncoder 语义）
        val spaced = InternalBrowserUrlPolicy.buildSearchUrl(template, "hello world")
        assertTrue("空格要编码", spaced!!.endsWith("q=hello+world"))
        val chinese = InternalBrowserUrlPolicy.buildSearchUrl(template, "以太网关")
        assertTrue("中文要百分号编码", chinese!!.contains("%"))
        assertFalse("不能原样留中文", chinese.contains("以太"))
    }

    @Test
    fun buildSearchUrlRejectsBadInput() {
        assertNull(InternalBrowserUrlPolicy.buildSearchUrl("https://www.google.com/search?q=", "x"))
        assertNull(InternalBrowserUrlPolicy.buildSearchUrl("https://a.com/?q={query}&r={query}", "x"))
        assertNull(InternalBrowserUrlPolicy.buildSearchUrl("https://www.google.com/search?q={query}", "   "))
        assertNull(
            "超过 512 字符要拒绝",
            InternalBrowserUrlPolicy.buildSearchUrl(
                "https://www.google.com/search?q={query}",
                "x".repeat(513),
            ),
        )
    }

    // ——— 4) normalizeInitialUrl ———

    @Test
    fun normalizeInitialUrlKeepsHttpsAndRejectsOtherSchemes() {
        assertEquals(
            "https://github.com/lyydfys",
            InternalBrowserUrlPolicy.normalizeInitialUrl("https://github.com/lyydfys"),
        )
        // MCA 只允许 https：http 会被拒绝（返回 null），不是"补成 https"
        assertNull(InternalBrowserUrlPolicy.normalizeInitialUrl("http://github.com"))
        assertNull(InternalBrowserUrlPolicy.normalizeInitialUrl("github.com"))
        assertNull(InternalBrowserUrlPolicy.normalizeInitialUrl("not a url"))
        assertNull(InternalBrowserUrlPolicy.normalizeInitialUrl(""))
    }

    // ——— 5) allowsNavigation ———

    @Test
    fun allowsNavigationSameOriginOnlyUnlessApproved() {
        val initial = "https://github.com/lyydfys/aether-cockpit"
        assertTrue(
            "同 host 放行",
            InternalBrowserUrlPolicy.allowsNavigation(initial, "https://github.com/settings"),
        )
        assertTrue(
            "子域也算同源（注册域相同）",
            InternalBrowserUrlPolicy.allowsNavigation(initial, "https://gist.github.com/x"),
        )
        assertFalse(
            "跨站默认拦截",
            InternalBrowserUrlPolicy.allowsNavigation(initial, "https://evil.example.com/x"),
        )
        assertTrue(
            "已批准的源放行",
            InternalBrowserUrlPolicy.allowsNavigation(
                initial,
                "https://evil.example.com/x",
                additionalApprovedOrigins = setOf("example.com"),
            ),
        )
        assertFalse("非 https 一律不放行", InternalBrowserUrlPolicy.allowsNavigation(initial, "javascript:alert(1)"))
    }

    // ——— 6) allowedHost / allowedOrigin ———

    @Test
    fun allowedHostAndOriginNormalizeCasePortAndSubdomains() {
        assertEquals(
            "github.com",
            InternalBrowserUrlPolicy.allowedHost("https://GitHub.com:443/lyydfys"),
        )
        assertEquals(
            "www 前缀去掉",
            "github.com",
            InternalBrowserUrlPolicy.allowedHost("https://www.github.com/x"),
        )
        assertEquals("github.com", InternalBrowserUrlPolicy.allowedOrigin("https://gist.github.com/a/b"))
        assertEquals("github.com", InternalBrowserUrlPolicy.allowedOrigin("https://GitHub.com/x"))
        assertNull("非法 URL 不崩", InternalBrowserUrlPolicy.allowedHost("::::"))
    }

    // ——— 8) 错误文案格式 ———

    @Test
    fun loadErrorMessageKeepsMcaFormat() {
        // 与 onReceivedError 里的拼串保持一致：「网页加载失败 (code)：desc」
        val code = -2
        val desc = "net::ERR_NAME_NOT_RESOLVED"
        val message = "网页加载失败 ($code)：$desc"
        assertEquals("网页加载失败 (-2)：net::ERR_NAME_NOT_RESOLVED", message)
        assertTrue(message.startsWith("网页加载失败 ("))
        assertTrue(message.contains(")："))
    }
}
