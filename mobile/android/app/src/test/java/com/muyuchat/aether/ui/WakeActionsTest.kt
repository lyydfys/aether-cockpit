package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.WakeTask
import com.muyuchat.aether.runtime.buildWakeUpdateJson
import com.muyuchat.aether.runtime.wakeUpdatePath
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 唤醒页三个动作的纯逻辑（桌面 `internal/web/ui2/js/views/scheduler.js`）：
 *
 * - 启停：`POST /api/wake/{id}`，请求体**只能**是 `{"enabled":…}`（`wake.go:408-415` 全指针字段，
 *   缺省=不修改；多带 `model`/`every_minutes` 会把排期与模型一起覆盖）；
 * - 立即唤醒：`POST /api/wake/{id}/run`（异步，结果下次刷新才显示）；
 * - 删除：二次确认文案逐字来自 `scheduler.js:427-435`。
 */
class WakeActionsTest {
    private fun task(
        enabled: Boolean = true,
        running: Boolean = false,
        lastOk: Boolean = false,
        lastResult: String = "",
    ) = WakeTask(
        id = "wake-1",
        accountId = "acc-1",
        providerId = "codex",
        label = "主力号",
        model = "gpt-5-codex",
        everyMinutes = 60,
        maxTokens = 512,
        enabled = enabled,
        createdAt = 0L,
        lastRun = 0L,
        lastModel = "",
        lastResult = lastResult,
        lastOk = lastOk,
        failures = 0,
        nextRun = 0L,
        running = running,
    )

    @Test
    fun enabledOnlyBodyNeverCarriesScheduleOrModel() {
        // 逐字断言：局部更新只允许带 enabled 一个键
        assertEquals("""{"enabled":true}""", buildWakeUpdateJson(true))
        assertEquals("""{"enabled":false}""", buildWakeUpdateJson(false))
        for (enabled in listOf(true, false)) {
            val body = buildWakeUpdateJson(enabled)
            assertTrue("不能带 model：$body", !body.contains("model"))
            assertTrue("不能带 every_minutes：$body", !body.contains("every_minutes"))
            assertTrue("不能带 max_tokens：$body", !body.contains("max_tokens"))
        }
    }

    @Test
    fun updatePathEncodesTheTaskId() {
        assertEquals("/api/wake/wake-1", wakeUpdatePath("wake-1"))
        // id 里出现斜杠不能拼出子路径
        assertEquals("/api/wake/a%2Fb", wakeUpdatePath("a/b"))
    }

    @Test
    fun deleteConfirmCopyIsVerbatimFromDesktop() {
        val confirm = wakeDeleteConfirmText()
        assertEquals("删除这条唤醒任务？", confirm.title)
        assertEquals("只删除定时计划，不影响账号。", confirm.body)
        assertEquals("删除", confirm.confirmLabel)
    }

    @Test
    fun statusChipFollowsDesktopPriority() {
        // running 最高优先：即使上次成功也显示「执行中」
        assertEquals("执行中", wakeStatusChip(task(running = true, lastOk = true)).label)
        // 其次 last_ok
        assertEquals("上次成功", wakeStatusChip(task(lastOk = true)).label)
        // 再次 last_result 非空
        assertEquals("上次失败", wakeStatusChip(task(lastResult = "timeout")).label)
        // 都没有 → 未执行
        assertEquals("未执行", wakeStatusChip(task()).label)
    }

    @Test
    fun statusChipToneMatchesDesktopColors() {
        assertEquals(WakeChipTone.RUNNING, wakeStatusChip(task(running = true)).tone)
        assertEquals(WakeChipTone.OK, wakeStatusChip(task(lastOk = true)).tone)
        assertEquals(WakeChipTone.FAILED, wakeStatusChip(task(lastResult = "bad gateway")).tone)
        assertEquals(WakeChipTone.IDLE, wakeStatusChip(task()).tone)
    }

    @Test
    fun statusChipIsIndependentFromEnabledFlag() {
        // 停用的任务也可能「上次成功」——chip 与开关是两条独立信息
        assertEquals("上次成功", wakeStatusChip(task(enabled = false, lastOk = true)).label)
        assertEquals("未执行", wakeStatusChip(task(enabled = false)).label)
    }
}
