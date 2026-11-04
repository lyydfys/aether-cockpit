package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「读取额度」诊断日志的格式与脱敏。
 *
 * 这三行只为回答一个问题：**请求发出去了吗、回包到了吗**（真机上出现过"按钮进了读取中、
 * 但既没有最近读数也没有读取失败"的情况）。所以日志必须可读，同时**绝不能**带出
 * 完整账号 id、URL、凭据、响应体。
 */
class ChannelsQuotaReadLogTest {
    /** 真实的账号 id 是长串（UUID 形态），用长 id 才能验证"真的被截断"。 */
    private val fullId = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

    @Test
    fun maskedIdKeepsOnlySixCharsAndNeverLeaksTheFullId() {
        val line = quotaReadLogLine(QuotaReadLog.Start(fullId))
        assertEquals("start account=a1b2c3…", line)
        // 完整 id 与"第 7 位往后"的内容都不许出现
        assertFalse(line, line.contains(fullId))
        assertFalse(line, line.contains("a1b2c3d4"))
        assertEquals("a1b2c3…", maskAccountId(fullId))
        // 空/空白 id 不会拼出 "account=null"
        assertEquals("…", maskAccountId(""))
        assertEquals("…", maskAccountId("   "))
    }

    @Test
    fun logLinesCarryNoUrlOrCredentialWords() {
        val lines = listOf(
            quotaReadLogLine(QuotaReadLog.Start(fullId)),
            quotaReadLogLine(QuotaReadLog.Ok(fullId, elapsedMs = 1234L, hasReadAt = true)),
            quotaReadLogLine(
                QuotaReadLog.Fail(fullId, code = "http_500", elapsedMs = 90_000L, errorType = "java.net.SocketTimeoutException"),
            ),
            // 故意喂进"看起来像凭据/URL"的输入：拼出来的日志里也不许出现这些字样
            quotaReadLogLine(
                QuotaReadLog.Fail("token-bearer-abc123", "Bearer sk-live-xyz", 1L, "io.IOException"),
            ),
            quotaReadLogLine(QuotaReadLog.Start("https://example.com/secret")),
        )
        for (line in lines) {
            val lower = line.lowercase()
            assertFalse("line=$line 不含 http", lower.contains("http"))
            assertFalse("line=$line 不含 bearer", lower.contains("bearer"))
            assertFalse("line=$line 不含 token", lower.contains("token"))
            assertFalse("line=$line 不含 sk-", lower.contains("sk-"))
            // 日志里只有"键=值"与中文，没有响应体/JSON 的花括号
            assertFalse("line=$line 不含 JSON", line.contains("{") || line.contains("}"))
        }
    }

    @Test
    fun eachBranchLogsItsOwnKeywordAndFields() {
        // start
        assertTrue(quotaReadLogLine(QuotaReadLog.Start(fullId)).startsWith("start account="))
        // ok：耗时 + 有没有读到（readAt=有/无）
        assertEquals("ok account=a1b2c3… ms=1234 readAt=有", quotaReadLogLine(QuotaReadLog.Ok(fullId, 1234L, true)))
        assertEquals("ok account=a1b2c3… ms=90 readAt=无", quotaReadLogLine(QuotaReadLog.Ok(fullId, 90L, false)))
        // fail：固定 kind=QUOTA + code + 耗时；没有异常时不带 type
        assertEquals(
            "fail account=a1b2c3… kind=QUOTA code=network_error ms=90000",
            quotaReadLogLine(QuotaReadLog.Fail(fullId, "network_error", 90_000L, null)),
        )
        assertTrue(
            quotaReadLogLine(QuotaReadLog.Fail(fullId, "status_502", 5L, "SocketTimeoutException"))
                .contains("type=SocketTimeoutException"),
        )
        // 客户端把 http_<status> 记成 status_<status>：信息保留、字样不带
        assertTrue(
            quotaReadLogLine(QuotaReadLog.Fail(fullId, "http_500", 1L, null)).contains("code=status_500"),
        )
    }
}
