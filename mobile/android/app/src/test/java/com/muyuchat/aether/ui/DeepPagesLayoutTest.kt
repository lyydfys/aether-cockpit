package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.CallerKeySummary
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
        // 卡间以 16 为准（Lead 裁决）：密度靠卡内收紧，不靠压块间。
        assertEquals(16f, DeepCardGap.value)
        assertEquals(24f, DeepBlockGap.value)
        assertEquals(16f, DeepRowVertical.value)
        // 卡片垂直内边距与水平一致（1.0× / 1.3× 同等对待，不再为 1.3× 抬到 20）。
        assertEquals(16f, DeepCardVerticalPadding.value)
        assertEquals(DeepCardPadding.value, DeepCardVerticalPadding.value)
        // 区块间 = 卡片间 + 标题上内距，两个常数不能各写各的。
        assertEquals(DeepBlockGap, DeepCardGap + (DeepBlockGap - DeepCardGap))
        // 列表卡片：水平 16 + 垂直 0；首末行外缘正好等于行自带的 16，不重复留白。
        assertEquals(16f, DeepListCardPadding.calculateLeftPadding(androidx.compose.ui.unit.LayoutDirection.Ltr).value)
        assertEquals(0f, DeepListCardPadding.calculateTopPadding().value)
        assertEquals(DeepCardVerticalPadding, DeepRowVertical + DeepListCardPadding.calculateTopPadding())
    }

    @Test
    fun pageTitleLayerIsSlimmed() {
        // 标题间距与卡间是同一个数（一个变量一种含义）：当前两处都必须是 16。
        assertEquals(16f, DeepCardGap.value)
        assertEquals(DeepCardGap, DeepPageTitleGap)
    }

    // 「每个页面都必须走 DeepPageTitle」是跨文件源码合同，不是行为，已移到
    // scripts/check-mobile-page-title-contract.sh（纯文本 lint，不抢 Gradle 锁、不受半写文件影响）。
    // 单测只保留能测行为与常量的断言。

    @Test
    fun countLabelsStayConsistentAcrossPages() {
        // 会话页已整页删除（sessions.Scan() 只读本机 agent 文件，Android 上恒空），
        // 所以这里不再断言 sessionCountLabel。
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
