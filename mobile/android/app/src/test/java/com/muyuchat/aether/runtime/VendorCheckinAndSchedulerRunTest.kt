package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 两处新入口的数据层：
 * 1. 厂商级「立即签到」`POST /api/usage/{vendor}-checkin`
 *    （`gateway.go:1011-1014` → `checkin_api.go:180`），回包 `[{user,outcome,credit,streak,msg,again}]`；
 * 2. 按渠道跑调度 `POST /api/scheduler/run/{渠道 id}`
 *    （`gateway.go:1116` → `scheduler_api.go:65`），回包 `{queued,skipped,note}`。
 */
class VendorCheckinAndSchedulerRunTest {

    /* ---------------- 厂商签到 ---------------- */

    @Test
    fun parsesVendorCheckinResults() {
        val results = parseVendorCheckins(
            """
            [{"user":"alice","outcome":"claimed","credit":1.5,"streak":3},
             {"user":"bob","outcome":"done","again":true,"msg":"今天已签"},
             {"user":"carol","outcome":"failed","msg":"网络超时"}]
            """.trimIndent(),
        )
        assertEquals(3, results.size)
        assertEquals("alice", results[0].user)
        assertEquals("claimed", results[0].outcome)
        assertEquals(1.5, results[0].credit, 1e-9)
        assertEquals(3, results[0].streak)
        assertEquals(true, results[1].again)
        assertEquals("今天已签", results[1].message)
        assertEquals("failed", results[2].outcome)
    }

    @Test
    fun emptyVendorCheckinIsExplainedNotCelebrated() {
        // 空数组 = 该厂商没有可用账号，不能显示成"全部成功"。
        assertTrue(parseVendorCheckins("[]").isEmpty())
        val summary = vendorCheckinSummary(emptyList())
        assertTrue(summary.contains("没有可签到的账号"))
        assertTrue(!summary.contains("本次领到 0"))
    }

    @Test
    fun summarySplitsTheThreeOutcomes() {
        val results = parseVendorCheckins(
            """
            [{"user":"a","outcome":"claimed"},{"user":"b","outcome":"claimed"},
             {"user":"c","outcome":"done"},{"user":"d","outcome":"failed"}]
            """.trimIndent(),
        )
        val summary = vendorCheckinSummary(results)
        assertTrue(summary.contains("4 个账号"))
        assertTrue(summary.contains("本次领到 2"))
        assertTrue(summary.contains("今天已签 1"))
        assertTrue(summary.contains("失败 1"))
    }

    @Test
    fun outcomeLabelsMatchProviderConstants() {
        // internal/provider/contract.go:188-192
        assertEquals("本次领到", checkinOutcomeLabel("claimed"))
        assertEquals("今天已签", checkinOutcomeLabel("done"))
        assertEquals("失败", checkinOutcomeLabel("failed"))
        // 大写也认；未知值原样回显，不硬翻成"成功"。
        assertEquals("本次领到", checkinOutcomeLabel("CLAIMED"))
        assertEquals("weird", checkinOutcomeLabel("weird"))
        assertEquals("无结论", checkinOutcomeLabel(""))
    }

    @Test
    fun creditFormattingDropsTrailingZeros() {
        assertEquals("1", formatCheckinCredit(1.0))
        assertEquals("1.5", formatCheckinCredit(1.5))
        assertEquals("0.25", formatCheckinCredit(0.25))
        assertEquals("0", formatCheckinCredit(0.0))
        // 负数/NaN 不是"领到"，按 0 显示。
        assertEquals("0", formatCheckinCredit(-1.0))
        assertEquals("0", formatCheckinCredit(Double.NaN))
    }

    @Test
    fun lineIncludesCreditStreakAndMessage() {
        val line = vendorCheckinLine(
            VendorCheckinResult(
                user = "alice",
                outcome = "claimed",
                credit = 1.5,
                streak = 3,
                message = "第 3 天",
                again = false,
            ),
        )
        assertTrue(line.contains("alice"))
        assertTrue(line.contains("本次领到"))
        assertTrue(line.contains("+1.5"))
        assertTrue(line.contains("连续 3 天"))
        assertTrue(line.contains("第 3 天"))
        // 没有领取额/连续天数时不出现 "+0"/"连续 0 天"。
        val plain = vendorCheckinLine(VendorCheckinResult("bob", "failed", 0.0, 0, "", false))
        assertTrue(!plain.contains("+0"))
        assertTrue(!plain.contains("连续"))
    }

    @Test
    fun vendorPathsMatchGatewayRoutes() {
        // gateway.go:1011-1014
        assertEquals("/api/usage/trae-checkin", CheckinVendor.TRAE.apiPath)
        assertEquals("/api/usage/qoder-checkin", CheckinVendor.QODER.apiPath)
        assertEquals("/api/usage/minimax-checkin", CheckinVendor.MINIMAX.apiPath)
        assertEquals("/api/usage/workbuddy-checkin", CheckinVendor.WORKBUDDY.apiPath)
        // 渠道范围与服务端 vendorProviders（checkin_api.go:11-16）逐项一致。
        assertEquals(listOf("trae-cn", "trae-solo-cn"), CheckinVendor.TRAE.providers)
        assertEquals(listOf("qoder-cn", "qoder-intl"), CheckinVendor.QODER.providers)
        assertEquals(listOf("minimax-code", "minimax-code-global"), CheckinVendor.MINIMAX.providers)
        assertEquals(listOf("workbuddy-cn"), CheckinVendor.WORKBUDDY.providers)
    }

    @Test
    fun malformedCheckinBodyIsAnExplicitError() {
        for (body in listOf("not json", "", "{}")) {
            assertTrue(runCatching { parseVendorCheckins(body) }.exceptionOrNull() is ManagementApiException)
        }
    }

    /* ---------------- 按渠道跑调度 ---------------- */

    @Test
    fun parsesSchedulerRunAck() {
        val ack = parseSchedulerRunAck("""{"queued":3,"skipped":2,"note":"已入队 3 个账号；今日已完成 2 个，已跳过"}""")
        assertEquals(3, ack.queued)
        assertEquals(2, ack.skipped)
        assertTrue(ack.note.contains("已入队 3"))
        // 负数/缺字段夹到 0，不显示"入队 -1 个"。
        val empty = parseSchedulerRunAck("""{}""")
        assertEquals(0, empty.queued)
        assertEquals(0, empty.skipped)
        assertEquals("", empty.note)
    }

    @Test
    fun ackTextPrefersServerNoteThenFallsBack() {
        // 服务端 note 知道"为什么是 0"，优先用它。
        assertEquals(
            "今天已经完成，本次跳过 2 个账号",
            schedulerRunAckText(SchedulerRunAck(0, 2, "今天已经完成，本次跳过 2 个账号")),
        )
        // 没有 note 时按计数兜一句，且区分"没有可入队"与"全部跳过"。
        assertEquals("当前没有可入队的账号", schedulerRunAckText(SchedulerRunAck(0, 0, "")))
        assertEquals("今天已完成，本次跳过 2 个账号", schedulerRunAckText(SchedulerRunAck(0, 2, "")))
        assertEquals(
            "已入队 3 个账号；今日已完成 2 个，已跳过",
            schedulerRunAckText(SchedulerRunAck(3, 2, "")),
        )
        assertEquals("已入队 3 个账号", schedulerRunAckText(SchedulerRunAck(3, 0, "")))
    }

    @Test
    fun malformedAckIsAnExplicitError() {
        for (body in listOf("not json", "", "[]")) {
            assertTrue(runCatching { parseSchedulerRunAck(body) }.exceptionOrNull() is ManagementApiException)
        }
    }
}
