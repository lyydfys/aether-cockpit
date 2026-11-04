package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.SessionModelUsage
import com.muyuchat.aether.runtime.SessionTranscript
import com.muyuchat.aether.runtime.SessionTranscriptPart
import com.muyuchat.aether.runtime.SessionUsageView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话详情的纯格式化（桌面 `sessions.js:403-478`）。
 *
 * 关键口径：
 * - 「合计 Token」= input + cache_read + cache_write + output（`:445`）；
 * - 「输入」chip 含缓存（`:450`）；
 * - 有未计价请求时写「部分未计价」而不是假费用（`:453`）；
 * - 正文每条截断 6000 字（`:472`），角色缺省「记录」（`:469`）。
 */
class SessionsDetailFormatTest {
    private fun usage(
        input: Long = 1000L,
        cacheRead: Long = 2000L,
        cacheWrite: Long = 500L,
        output: Long = 300L,
        cost: Double = 0.1234,
        unpriced: Boolean = false,
        requests: Int = 42,
        resume: String = "codex resume abc",
        models: List<SessionModelUsage> = listOf(SessionModelUsage("gpt-5-codex", 1000, 300, 2000, 500, 0.1)),
    ) = SessionUsageView(
        key = "codex:abc",
        id = "abc",
        agent = "codex",
        name = "Codex CLI",
        title = "修编译错误",
        cwd = "/repo",
        start = 1762200000L,
        last = 1762290000L,
        input = input,
        output = output,
        cacheRead = cacheRead,
        cacheWrite = cacheWrite,
        cost = cost,
        priced = true,
        unpriced = unpriced,
        requests = requests,
        messages = 18,
        toolCalls = 7,
        models = models,
        resume = resume,
        wsl = "",
        deletable = true,
        gateway = false,
    )

    @Test
    fun usageLineMatchesDesktopChips() {
        val line = sessionDetailUsageLine(usage())
        assertTrue(line, line.contains("合计 3800 Token"))
        assertTrue(line, line.contains("输入 3500"))
        assertTrue(line, line.contains("缓存 2000"))
        assertTrue(line, line.contains("输出 300"))
        assertTrue(line, line.contains("\$0.1234"))
        assertTrue(line, line.contains("42 次请求"))
        assertFalse(line, line.contains("部分未计价"))
    }

    @Test
    fun unpricedUsageSaysSoInsteadOfFakeCost() {
        val line = sessionDetailUsageLine(usage(unpriced = true))
        assertTrue(line, line.contains("部分未计价"))
        assertFalse("未计价时不要同时给一个估算数字：$line", line.contains("\$0.1234"))
    }

    @Test
    fun emptyTranscriptCopyDependsOnSource() {
        // gateway：网关自己的会话没有落文件（桌面 sessions.js:463）
        val gateway = sessionDetailEmptyCopy("gateway")
        assertEquals("这里没有可读的正文", gateway.first)
        assertTrue(gateway.second.contains("没有落文件"))
        // 其他来源：文件为空或格式不同
        val file = sessionDetailEmptyCopy("file")
        assertEquals("这里没有可读的正文", file.first)
        assertTrue(file.second.contains("为空或格式不同"))
    }

    @Test
    fun transcriptBodyIsCappedAndTrimmed() {
        val long = "x".repeat(7000)
        val part = SessionTranscriptPart(role = "user", text = "  $long  ", at = 0L)
        assertEquals(6000, sessionTranscriptBody(part).length)
        assertEquals("短内容", sessionTranscriptBody(SessionTranscriptPart("user", "  短内容 ", 0L)))
        // 自定义上限
        assertEquals(10, sessionTranscriptBody(part, limit = 10).length)
    }

    @Test
    fun footerReportsCountAndTruncation() {
        assertEquals("共 3 段", sessionTranscriptFooter(3, cut = false))
        assertTrue(sessionTranscriptFooter(3, cut = true).contains("服务端已截断"))
    }

    @Test
    fun titleFallsBackToIdThenKey() {
        assertEquals("修编译错误", sessionDetailTitle(SessionDetailUiState.Ready("codex:abc", usage(), emptyList(), "file", false)))
        // 没有标题时用品名/ id
        assertEquals(
            "abc",
            sessionDetailTitle(SessionDetailUiState.Ready("codex:abc", usage().copy(title = ""), emptyList(), "file", false)),
        )
        assertEquals("abc", sessionDetailTitle(SessionDetailUiState.Loading("codex:abc")))
        assertEquals("abc", sessionDetailTitle(SessionDetailUiState.Failed("codex:abc", "x", "y")))
        assertEquals("", sessionDetailTitle(SessionDetailUiState.Idle))
    }

    @Test
    fun roleLineShowsRelativeTimeWhenKnown() {
        val part = SessionTranscriptPart(role = "user", text = "hi", at = System.currentTimeMillis() / 1000L - 120L)
        assertTrue(sessionTranscriptRoleLine(part).startsWith("user ·"))
        assertTrue(sessionTranscriptRoleLine(part).contains("分钟前"))
        // 没有时间就只显示角色，不显示「时间未知」
        val noTime = SessionTranscriptPart(role = "assistant", text = "hi", at = 0L)
        assertEquals("assistant", sessionTranscriptRoleLine(noTime))
    }

    @Test
    fun transcriptModelKeepsPartsInOrder() {
        val transcript = SessionTranscript(
            parts = listOf(
                SessionTranscriptPart("user", "第一条", 1L),
                SessionTranscriptPart("assistant", "第二条", 2L),
            ),
            source = "file",
            cut = false,
        )
        assertEquals(listOf("第一条", "第二条"), transcript.parts.map { it.text })
    }
}
