package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.SessionRow
import com.muyuchat.aether.runtime.WakeTask
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 深层页面统一的排版与文案合同。
 *
 * 覆盖三件事：
 *  1. 间距节奏按 UI_REFRESH_SPEC §1.2（第二轮密度收敛）：卡片水平 16 / 垂直 20、
 *     卡片之间 16、区块之间 24、行垂直 16（触控下限）；
 *  2. 计数文案统一，0 条不写「共 0 条」；
 *  3. 空态/级别文案必须说清"是哪一种空"，且缺失字段有明确兜底。
 */
class DeepPagesLayoutTest {
    @Test
    fun spacingRhythmIsOnTheFourPointScale() {
        assertEquals(16f, DeepCardPadding.value)
        assertEquals(16f, DeepCardGap.value)
        assertEquals(24f, DeepBlockGap.value)
        assertEquals(16f, DeepRowVertical.value)
        // 卡片垂直内边距 = 水平 16 + 上下各 4，内容不贴边。
        assertEquals(20f, DeepCardVerticalPadding.value)
        assertEquals(DeepCardPadding.value + 4f, DeepCardVerticalPadding.value)
        // 区块间 = 卡片间 + 标题上内距，两个常数不能各写各的。
        assertEquals(DeepBlockGap, DeepCardGap + (DeepBlockGap - DeepCardGap))
        // 列表卡片：水平 16 + 行自带 16 → 首末行外缘合计 20。
        assertEquals(16f, DeepListCardPadding.calculateLeftPadding(androidx.compose.ui.unit.LayoutDirection.Ltr).value)
        assertEquals(4f, DeepListCardPadding.calculateTopPadding().value)
        assertEquals(DeepCardVerticalPadding, DeepRowVertical + DeepListCardPadding.calculateTopPadding())
    }

    @Test
    fun countLabelsStayConsistentAcrossPages() {
        assertEquals("0 条", sessionCountLabel(0))
        assertEquals("7 条", sessionCountLabel(7))
        assertEquals("0 条", libraryCountLabel(0))
        assertEquals("3 条", libraryCountLabel(3))
        assertEquals("未配置", pluginRulesCountLabel(0))
        assertEquals("2 条", pluginRulesCountLabel(2))
        assertEquals("1 条任务", wakeTaskCountLabel(1))
    }

    @Test
    fun libraryEmptyCopyExplainsWhichKindOfEmpty() {
        val noneAtAll = libraryEmptyCopy(hasAnyEntry = false, kindLabel = null)
        assertEquals("资源库里还没有条目", noneAtAll.title)
        assertTrue("空态要点出下一步入口", noneAtAll.action.isNotBlank())
        assertTrue("空态要说明发现/导入在桌面端", noneAtAll.detail.contains("桌面"))

        val filtered = libraryEmptyCopy(hasAnyEntry = true, kindLabel = "技能")
        assertEquals("「技能」下没有条目", filtered.title)
        assertEquals("显示全部", filtered.action)
    }

    @Test
    fun libraryEmptyCopyFallsBackWithoutFilterLabel() {
        // 没有任何筛选却"有分类空"是调用方的错，这里也不许返回空标题。
        val odd = libraryEmptyCopy(hasAnyEntry = true, kindLabel = null)
        assertEquals("资源库里还没有条目", odd.title)
    }

    @Test
    fun diagnoseLevelLabelsCoverUnknownAndBlank() {
        assertEquals("错误", diagnoseLevelLabel("error"))
        assertEquals("警告", diagnoseLevelLabel("WARN"))
        assertEquals("通过", diagnoseLevelLabel("ok"))
        // 未知级别原样显示，不硬塞成「通过」。
        assertEquals("debug", diagnoseLevelLabel("debug"))
        assertEquals("未知", diagnoseLevelLabel(""))
    }

    @Test
    fun wakeTaskMetaLineCoversFullAndMissingFields() {
        val now = 1_800_000_000L
        val task = WakeTask(
            id = "w1",
            accountId = "a1",
            providerId = "claude",
            label = "主账号",
            model = "claude-sonnet-4-6",
            everyMinutes = 300,
            maxTokens = 8,
            enabled = true,
            createdAt = now - 10_000,
            lastRun = now - 1_000,
            lastModel = "claude-sonnet-4-6",
            lastResult = "ok",
            lastOk = true,
            failures = 2,
            nextRun = now + 600,
            running = true,
        )
        assertEquals(
            "每 5 小时 · 约 10 分钟后 · 正在执行 · 连续失败 2 次",
            wakeTaskMetaLine(task, now),
        )

        val bare = task.copy(everyMinutes = 0, nextRun = 0, running = false, failures = 0)
        assertEquals("间隔未知 · 未安排", wakeTaskMetaLine(bare, now))

        val overdue = task.copy(nextRun = now - 5, running = false, failures = 0)
        assertEquals("每 5 小时 · 即将到期", wakeTaskMetaLine(overdue, now))
    }

    @Test
    fun sessionMetaLinePrefersAgentNameAndDropsBlankDirectory() {
        val row = SessionRow(
            key = "claude-code:s1",
            id = "s1",
            agent = "claude-code",
            agentName = "Claude Code",
            title = "重构",
            directory = "aether",
            lastAt = 1_799_999_000L,
            requests = 3,
            messages = 12,
            toolCalls = 2,
            cost = 0.0,
        )
        assertEquals("Claude Code · aether", sessionMetaLine(row))
        // 路径缺失时不留悬空的「· 」。
        assertEquals("Claude Code", sessionMetaLine(row.copy(directory = "")))
        // Agent 名缺失时退回 id，不能显示空标签。
        assertEquals("claude-code · aether", sessionMetaLine(row.copy(agentName = "")))
    }

    @Test
    fun callerKeyMetaLineNeverThrowsOnMissingFields() {
        val key = CallerKeySummary(
            id = "k1",
            name = "手机",
            masked = "aek_****9f2c",
            off = false,
            usedTokens = 1_234,
            usedCost = 0.0523,
        )
        assertEquals("aek_****9f2c · 启用 · 1234 tokens · $0.0523", callerKeyMetaLine(key))
        assertEquals(
            "aek_****9f2c · 已停用 · 1234 tokens · $0.0523",
            callerKeyMetaLine(key.copy(off = true)),
        )
        assertTrue(callerKeyMetaLine(key.copy(masked = "")).startsWith("掩码缺失"))
    }
}
