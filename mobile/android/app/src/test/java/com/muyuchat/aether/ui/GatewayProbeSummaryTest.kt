package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 探针区块标题右侧的端点计数。
 *
 * 只按 [ProbeRow.attention] 判定「需注意」，不估算、不把失败抹平成 0。
 */
class GatewayProbeSummaryTest {

    @Test
    fun countsEndpointsAndFlagsAttention() {
        assertEquals("4 个端点", probeRowsSummary(listOf(ok("/readyz"), ok("/version"), ok("/metrics"), ok("/api/runtime"))))
        assertEquals(
            "4 个端点 · 1 项需注意",
            probeRowsSummary(listOf(ok("/readyz"), attention("/version"), ok("/metrics"), ok("/api/runtime"))),
        )
    }

    @Test
    fun everyEndpointNeedingAttentionIsCounted() {
        assertEquals(
            "2 个端点 · 2 项需注意",
            probeRowsSummary(listOf(attention("/healthz"), attention("/api/health"))),
        )
    }

    @Test
    fun emptyListDoesNotPretendToHaveEndpoints() {
        // 空列表不返回「共 0 条」这种废话，直接说 0 个端点。
        assertEquals("0 个端点", probeRowsSummary(emptyList()))
    }

    private fun ok(path: String) = row(path = path, attention = false)

    private fun attention(path: String) = row(path = path, attention = true)

    private fun row(path: String, attention: Boolean) = ProbeRow(
        path = path,
        httpStatus = if (attention) 500 else 200,
        badge = if (attention) "异常" else "正常",
        attention = attention,
        code = "",
        message = "",
        fields = emptyList(),
    )
}
