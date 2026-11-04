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
        assertEquals("263 ms", fields["总耗时"])
        assertEquals("12", fields["输入 Token"])
        assertEquals("34", fields["输出 Token"])
        assertEquals("—", fields["总费用"])
        assertTrue("detail must show a timestamp", fields["时间"]?.isNotBlank() == true)
    }

    @Test
    fun detailSectionsFollowDesktopOrder() {
        // 桌面 logs.js:286-319：请求 → 路由与渠道 → 耗时 → Token 明细 → 计费详情。
        assertEquals(
            listOf("请求", "路由与渠道", "耗时", "Token 明细", "计费详情"),
            requestDetailSections(row).map { it.title },
        )
    }

    @Test
    fun detailFallsBackWhenFieldsAreMissing() {
        val empty = row.copy(timeMs = 0, status = 0, ms = 0, inputTokens = 0, outputTokens = 0)
        val fields = requestDetailFields(empty).associate { it.label to it.value }
        assertEquals("—", fields["时间"])
        assertEquals("—", fields["总耗时"])
        assertEquals("0", fields["输入 Token"])
        assertEquals("0", fields["输出 Token"])
        assertEquals("（与模型同名）", fields["请求模型"])
    }

    @Test
    fun failedRowsExplainByTypeWithoutRawUpstreamText() {
        assertTrue(requestErrorExplanation(row)?.contains("鉴权") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "rate"))?.contains("限流") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "shape"))?.contains("拒绝") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "internal"))?.contains("内部") == true)
        assertTrue(requestErrorExplanation(row.copy(errorType = "other"))?.contains("失败") == true)
        assertEquals(null, requestErrorExplanation(row.copy(status = 200, errorType = "")))
        assertTrue(requestErrorExplanation(row.copy(errorType = "", status = 503))?.contains("上游") == true)
    }

    @Test
    fun detailNeverExposesPayloadOrSecrets() {
        // 会话 ID 按桌面 logs.js:292 展示；这里仍然禁掉的只剩正文与凭据类字样。
        val labels = requestDetailFields(row).map { it.label }.joinToString("/")
        for (forbidden in listOf("正文", "caller", "密钥", "Authorization", "api_key")) {
            assertFalse("detail label exposes $forbidden", labels.contains(forbidden, ignoreCase = true))
        }
    }
}
