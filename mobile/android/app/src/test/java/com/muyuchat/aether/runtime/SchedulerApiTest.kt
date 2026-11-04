package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 签到调度的纯解析/文案逻辑。
 *
 * 口径红线（与页面一致）：
 * - 调度器没接线时服务端回 `{enabled:false, reason:"调度器未启动"}`——页面要**原样**显示
 *   reason，不能伪造一个空任务列表；
 * - 手动触发是**入队**不是执行完，文案必须说「已入队」。
 */
class SchedulerApiTest {
    @Test
    fun parsesEnabledStateWithJobs() {
        val body = """
            {"enabled":true,
             "info":{"jobs":2,"completed_today":1,"uptime_sec":3600,"wake_count":0,"next_fire_at":1800000000},
             "jobs":[
               {"provider_id":"kiro","task_id":"daily","title":"每日签到","cron":"0 9 * * *",
                "next_run":1800000000,"last_run":1799990000,"last_result":"ok",
                "pending":0,"today_done":1,"today_total":1},
               {"provider_id":"qoder","task_id":"daily","title":"签到","cron":"0 9 * * *",
                "next_run":1800006000,"pending":2,"today_done":0,"today_total":1}
             ]}
        """.trimIndent()
        val state = parseSchedulerState(body)
        assertTrue(state.enabled)
        assertNull(state.reason)
        assertEquals(2, state.jobs.size)
        assertEquals(2, state.info?.jobs)
        assertEquals(1, state.info?.completedToday)
        val kiro = state.jobs.first { it.providerId == "kiro" }
        assertEquals("每日签到", kiro.title)
        assertEquals(1, kiro.todayDone)
        assertEquals(1, kiro.todayTotal)
        assertEquals("ok", kiro.lastResult)
        val qoder = state.jobs.first { it.providerId == "qoder" }
        assertEquals(2, qoder.pending)
        assertEquals(0L, qoder.lastRun)
    }

    @Test
    fun parsesDisabledStateWithReasonVerbatim() {
        val state = parseSchedulerState("""{"enabled":false,"reason":"调度器未启动"}""")
        assertEquals(false, state.enabled)
        assertEquals("调度器未启动", state.reason)
        assertTrue("未接线时不应伪造任务", state.jobs.isEmpty())
    }

    @Test
    fun parsesRunResultAndKeepsServerNote() {
        val r = parseSchedulerRunResult("""{"queued":3,"skipped":1,"note":"已入队 3 个账号；今日已完成 1 个，已跳过"}""")
        assertEquals(3, r.queued)
        assertEquals(1, r.skipped)
        assertTrue(r.note.contains("已入队"))
    }

    @Test
    fun runResultWithoutNoteFallsBackToHonestText() {
        // 服务端没给 note 时，页面也不能说"已完成"。
        val r = parseSchedulerRunResult("""{"queued":0,"skipped":0}""")
        assertTrue(r.note.isNotBlank())
        assertTrue("不能出现'已完成'这种假成功", !r.note.contains("已完成"))
        assertTrue(r.note.contains("入队") || r.note.contains("没有"))
    }

    @Test
    fun jobsWithoutProviderIdAreDropped() {
        val state = parseSchedulerState(
            """{"enabled":true,"info":{},"jobs":[{"title":"无渠道"},{"provider_id":"ok","task_id":"t"}]}""",
        )
        assertEquals(listOf("ok"), state.jobs.map { it.providerId })
    }

    @Test
    fun summaryLineIsHonestAboutTodoAndDone() {
        val state = parseSchedulerState(
            """{"enabled":true,"info":{"jobs":3,"completed_today":2},"jobs":[]}""",
        )
        assertEquals("今日完成 2 / 3 个任务", schedulerSummaryLine(state))
        val disabled = parseSchedulerState("""{"enabled":false,"reason":"调度器未启动"}""")
        assertEquals("调度器未启动", schedulerSummaryLine(disabled))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseSchedulerState(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
