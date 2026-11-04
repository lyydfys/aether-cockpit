package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 唤醒队列的纯逻辑。
 *
 * 口径红线（与页面一致）：`enabled` 只表示"这条任务被启用"，**不表示它一定会按时跑**——
 * 移动端的调度是进程内定时器，系统省电策略随时会停掉它。所以这里所有文案函数
 * 都不许输出"一定会执行"这类意思。
 */
class WakeApiTest {
    @Test
    fun parsesTasksWithRuntimeState() {
        val body = """
            {"now":1800000000,"tasks":[
              {"id":"w1","account_id":"acc_1","provider_id":"openai","label":"主账号","model":"gpt-5",
               "every_minutes":300,"max_tokens":8,"enabled":true,"created_at":1799990000,
               "last_run":1799995000,"last_model":"gpt-5","last_result":"ok","last_ok":true,
               "failures":0,"next_run":1800001000,"running":false}
            ]}
        """.trimIndent()
        val list = parseWakeTasks(body)
        assertEquals(1800000000L, list.now)
        assertEquals(1, list.tasks.size)
        val t = list.tasks.single()
        assertEquals("w1", t.id)
        assertEquals("acc_1", t.accountId)
        assertEquals("主账号", t.label)
        assertEquals("gpt-5", t.model)
        assertEquals(300, t.everyMinutes)
        assertTrue(t.enabled)
        assertTrue(t.lastOk)
        assertEquals(1799995000L, t.lastRun)
        assertEquals(1800001000L, t.nextRun)
        assertFalse(t.running)
    }

    @Test
    fun dropsRowsWithoutId() {
        val list = parseWakeTasks("""{"now":1,"tasks":[{"account_id":"a"},{"id":"ok"}]}""")
        assertEquals(listOf("ok"), list.tasks.map { it.id })
    }

    @Test
    fun emptyQueueIsNormal() {
        val list = parseWakeTasks("""{"tasks":[],"now":0}""")
        assertTrue(list.tasks.isEmpty())
    }

    @Test
    fun intervalLabelIsHuman() {
        assertEquals("每 15 分钟", wakeIntervalLabel(15))
        assertEquals("每 1 小时", wakeIntervalLabel(60))
        assertEquals("每 5 小时", wakeIntervalLabel(300))
        assertEquals("每 2 天", wakeIntervalLabel(2_880))
        // 非法/缺失间隔如实说，不猜。
        assertEquals("间隔未知", wakeIntervalLabel(0))
        assertEquals("间隔未知", wakeIntervalLabel(-30))
    }

    @Test
    fun nextRunIsRelativeAndNeverNegative() {
        val now = 1_800_000_000L
        assertEquals("约 5 分钟后", wakeNextRunLabel(now + 300, now))
        assertEquals("约 2 小时后", wakeNextRunLabel(now + 7_200, now))
        assertEquals("即将到期", wakeNextRunLabel(now - 10, now))
        assertEquals("未安排", wakeNextRunLabel(0, now))
    }

    @Test
    fun runAckParsesThe202Body() {
        // POST /api/wake/{id}/run 是异步的：服务端只回 202 {"started":true}，
        // 页面必须说"已提交"，不能说"已完成"。
        assertTrue(parseWakeRunAck("""{"started":true}"""))
        assertFalse(parseWakeRunAck("""{"started":false}"""))
        val error = runCatching { parseWakeRunAck("not-json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }

    @Test
    fun enabledLabelDoesNotPromiseDelivery() {
        // 只有这两个值；不允许出现"定时执行""保证执行"。
        assertEquals("已启用（尽力而为）", wakeEnabledLabel(true))
        assertEquals("已停用", wakeEnabledLabel(false))
    }

    @Test
    fun lastResultLabelIsHonest() {
        assertEquals("从未运行", wakeLastRunLabel(0L, true, ""))
        assertTrue(wakeLastRunLabel(1_800_000_000L, true, "ok").startsWith("上次成功"))
        assertTrue(wakeLastRunLabel(1_800_000_000L, false, "429").startsWith("上次失败"))
        assertTrue(wakeLastRunLabel(1_800_000_000L, false, "429").contains("429"))
    }
}
