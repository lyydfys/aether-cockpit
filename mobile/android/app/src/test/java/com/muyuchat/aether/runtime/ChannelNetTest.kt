package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道网络只读汇总：`GET /api/channels/net`
 * （`gateway.go:1205` → `channel_net.go:306-324`；桌面形态 `channelnet.js:63-77` 是单渠道抽屉）。
 *
 * 回包：`{"channels":[{provider_id,provider_name,base_url,prewarm_sec,max_inflight,
 * stall_timeout_ms,inflight}]}`；`0` 表示"跟随默认"，不是"关掉"。
 */
class ChannelNetTest {
    @Test
    fun requiresSession() {
        assertTrue(probeNeedsSession("/api/channels/net"))
    }

    @Test
    fun parsesRows() {
        val rows = parseChannelNetRows(
            """
            {"channels":[
              {"provider_id":"codex","provider_name":"Codex","base_url":"https://api.example.com",
               "prewarm_sec":30,"max_inflight":4,"stall_timeout_ms":1500,"inflight":2},
              {"provider_id":"claude-sub","prewarm_sec":0,"max_inflight":0,"stall_timeout_ms":0,"inflight":0}]}
            """.trimIndent(),
        )
        assertEquals(2, rows.size)
        val first = rows[0]
        assertEquals("codex", first.providerId)
        assertEquals("Codex", first.providerName)
        assertEquals("https://api.example.com", first.baseUrl)
        assertEquals(30, first.prewarmSec)
        assertEquals(4, first.maxInflight)
        assertEquals(1500, first.stallTimeoutMs)
        assertEquals(2, first.inflight)
        assertEquals(0, rows[1].inflight)
    }

    @Test
    fun toleratesEmptyAndMissingFields() {
        assertTrue(parseChannelNetRows("""{"channels":[]}""").isEmpty())
        // 没有 channels 键 → 空列表（不是崩溃）
        assertTrue(parseChannelNetRows("{}").isEmpty())
        // 缺 provider_id 的行丢弃
        assertTrue(parseChannelNetRows("""{"channels":[{"provider_name":"X"}]}""").isEmpty())
        // 缺数值字段按 0（跟随默认）
        val row = parseChannelNetRows("""{"channels":[{"provider_id":"p"}]}""").singleton()
        assertEquals("", row.providerName)
        assertEquals(0, row.prewarmSec)
        assertEquals(0, row.maxInflight)
        assertEquals(0, row.stallTimeoutMs)
        assertEquals(0, row.inflight)
        // 负数夹成 0
        val clamped = parseChannelNetRows("""{"channels":[{"provider_id":"p","inflight":-5}]}""").singleton()
        assertEquals(0, clamped.inflight)
    }

    @Test
    fun brokenBodiesAreInvalidResponse() {
        for (body in listOf("not json", "", "<html>401</html>", "[1,2]")) {
            val error = runCatching { parseChannelNetRows(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun policyLineMarksDefaultsInsteadOfZero() {
        val configured = ChannelNetRow("p", "P", "", 30, 4, 1500, 0)
        assertEquals("预热 30s · 并发 4 · 停顿 1500ms", channelNetPolicyLine(configured))
        // 未配置的项写「默认」，不写 0
        val fallback = ChannelNetRow("p", "P", "", 0, 0, 0, 0)
        assertEquals("预热 默认 · 并发 默认", channelNetPolicyLine(fallback))
        // 只配了预热的中间态
        assertEquals(
            "预热 30s · 并发 默认",
            channelNetPolicyLine(fallback.copy(prewarmSec = 30)),
        )
    }

    @Test
    fun inflightLabelMatchesDesktopChip() {
        assertEquals("在途 2", channelNetInflightLabel(ChannelNetRow("p", "P", "", 0, 0, 0, 2)))
        assertEquals("在途 0", channelNetInflightLabel(ChannelNetRow("p", "P", "", 0, 0, 0, 0)))
    }

    private fun List<ChannelNetRow>.singleton(): ChannelNetRow {
        assertEquals(1, size)
        return first()
    }
}
