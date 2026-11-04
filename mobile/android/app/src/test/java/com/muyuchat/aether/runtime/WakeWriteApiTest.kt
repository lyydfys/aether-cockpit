package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 唤醒任务新建 + 签到汇总。
 *
 * 契约（已对过 handler）：
 * - `POST /api/wake` `{account_id, model, every_minutes, max_tokens, enabled?}`；
 *   **不需要凭据**——服务端从账号池按 `account_id` 自己找账号。
 * - 间隔合法区间：15 分钟 ~ 7 天（`wakeMinInterval`/`wakeMaxInterval`）。
 * - `GET /api/scheduler/checkin-summary?since=` → `{generated_at,since,counts{...},items[...]}`。
 */
class WakeWriteApiTest {
    @Test
    fun createBodyCarriesOnlyDeclaredFields() {
        val json = JSONObject(
            buildWakeCreateJson(
                WakeDraft(accountId = "acc-1", model = "glm-5.3-flash", everyMinutes = 120, maxTokens = 32),
            ),
        )
        assertEquals("acc-1", json.getString("account_id"))
        assertEquals("glm-5.3-flash", json.getString("model"))
        assertEquals(120, json.getInt("every_minutes"))
        assertEquals(32, json.getInt("max_tokens"))
        assertEquals(4, json.length())
    }

    @Test
    fun intervalMustStayInServerRange() {
        assertNull(validateWakeDraft(WakeDraft("a", "", 15, 32)).everyMinutes)
        assertNull(validateWakeDraft(WakeDraft("a", "", 7 * 24 * 60, 32)).everyMinutes)
        assertEquals(
            "间隔需在 15 分钟到 7 天之间",
            validateWakeDraft(WakeDraft("a", "", 14, 32)).everyMinutes,
        )
        assertEquals(
            "间隔需在 15 分钟到 7 天之间",
            validateWakeDraft(WakeDraft("a", "", 7 * 24 * 60 + 1, 32)).everyMinutes,
        )
    }

    @Test
    fun accountIsRequiredAndTokensAreClamped() {
        assertEquals("请选择账号", validateWakeDraft(WakeDraft("  ", "", 60, 32)).accountId)
        // max_tokens 服务端上限 64；本地先夹紧，别等 400
        val draft = WakeDraft("a", "", 60, 999).normalized()
        assertEquals(64, draft.maxTokens)
        // 0/负数回落到默认 16
        assertEquals(16, WakeDraft("a", "", 60, 0).normalized().maxTokens)
    }

    @Test
    fun intervalLabelIsPlainLanguage() {
        assertEquals("每 15 分钟", wakeIntervalLabel(15))
        assertEquals("每 2 小时", wakeIntervalLabel(120))
        assertEquals("每天", wakeIntervalLabel(24 * 60))
        assertEquals("每 3 天", wakeIntervalLabel(3 * 24 * 60))
        assertEquals("每 7 天", wakeIntervalLabel(7 * 24 * 60))
    }

    @Test
    fun parsesCheckinSummary() {
        val summary = parseCheckinSummary(
            """{"generated_at":1793730000,"since":1793726400,
                "counts":{"new_success":2,"already":1,"failed":1,"unavailable":0,"not_run":0,"no_account":1,"pending":3},
                "items":[
                  {"provider_id":"workbuddy-cn","provider_name":"WorkBuddy CN","task_id":"t1","title":"签到",
                   "status":"success","accounts":2,"new_success":2,"already":0,"failed":0,"unavailable":0},
                  {"provider_id":"antigravity","provider_name":"Antigravity","task_id":"t2","title":"签到",
                   "status":"failed","accounts":1,"new_success":0,"already":0,"failed":1,"unavailable":0,
                   "failures":[{"account_id":"a1","label":"本机","message":"i/o timeout","at":1793729999}]}]}""",
        )
        assertEquals(2, summary.counts.newSuccess)
        assertEquals(1, summary.counts.failed)
        assertEquals(3, summary.counts.pending)
        assertEquals(2, summary.items.size)
        val failed = summary.items[1]
        assertEquals("failed", failed.status)
        assertEquals(1, failed.failures.size)
        assertEquals("i/o timeout", failed.failures[0].message)
    }

    @Test
    fun summaryLineUsesExplicitScope() {
        val summary = parseCheckinSummary(
            """{"counts":{"new_success":3,"already":2,"failed":1,"unavailable":0,"not_run":1,"no_account":2,"pending":0}}""",
        )
        val line = checkinSummaryLine(summary)
        // 口径必须写清楚，不能只写"完成 3"
        assertTrue(line.contains("本次"))
        assertTrue(line.contains("3"))
        assertTrue(line.contains("失败 1"))
        // 没有任何可统计项时如实说明
        assertTrue(checkinSummaryLine(parseCheckinSummary("""{"counts":{}}""")).contains("暂无"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseCheckinSummary(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
