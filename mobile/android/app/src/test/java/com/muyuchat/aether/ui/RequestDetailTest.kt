package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RequestRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RequestDetailTest {
    private val row = RequestRow(
        id = "1-1",
        timeMs = 1_700_000_000_000,
        model = "claude-sub/claude-sonnet-4-6",
        providerName = "",
        status = 401,
        errorType = "auth",
        inputTokens = 12,
        outputTokens = 34,
        ms = 263,
        cost = 0.0,
    )

    @Test
    fun detailFieldsCoverSafeLedgerColumns() {
        val fields = requestDetailFields(row).associate { it.label to it.value }
        assertEquals("claude-sub/claude-sonnet-4-6", fields["模型"])
        assertEquals("未记录渠道", fields["渠道"])
        assertEquals("401", fields["状态"])
        assertEquals("263 ms", fields["耗时"])
        assertEquals("12 输入 / 34 输出", fields["Token"])
        assertEquals("$0", fields["成本"])
        assertTrue("detail must show a timestamp", fields["时间"]?.isNotBlank() == true)
    }

    @Test
    fun detailFallsBackWhenFieldsAreMissing() {
        val empty = row.copy(timeMs = 0, status = 0, ms = 0, inputTokens = 0, outputTokens = 0)
        val fields = requestDetailFields(empty).associate { it.label to it.value }
        assertEquals("—", fields["时间"])
        assertEquals("—", fields["状态"])
        assertEquals("—", fields["耗时"])
        assertEquals("0 输入 / 0 输出", fields["Token"])
    }

    @Test
    fun failedRowsExplainByTypeWithoutRawUpstreamText() {
        assertTrue(requestErrorExplanation(row)?.contains("鉴权") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "rate"))?.contains("限流") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "shape"))?.contains("格式") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "other"))?.contains("失败") == true)
        assertEquals(null, requestErrorExplanation(row.copy(status = 200, errorType = "")))
        assertTrue(requestErrorExplanation(row.copy(errorType = ""))?.contains("401") == true)
    }

    @Test
    fun detailNeverExposesPayloadOrSecrets() {
        val labels = requestDetailFields(row).map { it.label }.joinToString("/")
        for (forbidden in listOf("正文", "会话", "caller", "密钥", "Authorization")) {
            assertFalse("detail label exposes $forbidden", labels.contains(forbidden, ignoreCase = true))
        }
    }
}
