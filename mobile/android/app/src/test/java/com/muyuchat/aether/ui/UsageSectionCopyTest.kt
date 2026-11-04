package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 订阅 / 模型重定向 / 最近异常三块的文案一致性。
 *
 * 三块是同一套结构（标题 → 说明 → 按钮 → 状态），所以空态必须同一种写法：
 * 「为什么空 + 下一步」，而不是"暂无数据"。
 */
class UsageSectionCopyTest {

    @Test
    fun idleHintsSayWhatTheButtonWillDo() {
        for (hint in listOf(quotaPlanIdleHint(), modelRedirectIdleHint(), recentFailuresIdleHint())) {
            assertTrue(hint, hint.isNotBlank())
            assertTrue(hint, hint.contains("下一步"))
            assertTrue(hint, hint.contains("只读"))
        }
    }

    @Test
    fun emptyHintsExplainWhyAndWhatNext() {
        for (hint in listOf(
            quotaPlanEmptyHint(),
            modelRedirectEmptyHint(),
            recentFailuresEmptyHint(),
            heatmapEmptyHint(),
            quotaHistoryEmptyHint(),
            pricingEmptyHint(),
        )) {
            assertTrue(hint, hint.isNotBlank())
            assertTrue(hint, hint.contains("为什么空"))
            assertTrue(hint, hint.contains("下一步"))
            assertFalse(hint, hint.contains("暂无数据"))
            // 空态只指一次路：不叠加"点击下面的按钮"这种第二动作。
            assertFalse(hint, hint.contains("点击"))
        }
    }

    @Test
    fun quotaHistoryEmptyHintPointsAtTheQuotaPage() {
        val hint = quotaHistoryEmptyHint()
        assertTrue(hint, hint.contains("额度"))
        // "没数据"不等于 0%——写 0% 会让人以为额度真的用完了。
        assertTrue(hint, hint.contains("不会写 0%"))
    }

    @Test
    fun heatmapEmptyHintSeparatesEmptyFromZero() {
        val hint = heatmapEmptyHint()
        assertTrue(hint, hint.contains("空不等于 0 次调用"))
    }

    @Test
    fun emptyHintsDoNotClaimEverythingIsFine() {
        // 最近异常为空是"这段历史里没有失败"，不是"当前正常"。
        val failures = recentFailuresEmptyHint()
        assertFalse(failures, failures.contains("当前正常"))
        assertTrue(failures, failures.contains("轨迹"))
        // 重定向为空是"没配置"，不是"已优化"。
        val redirects = modelRedirectEmptyHint()
        assertFalse(redirects, redirects.contains("已优化"))
        assertTrue(redirects, redirects.contains("桌面端"))
        // 订阅为空指向额度页，不编造到期日。
        val plans = quotaPlanEmptyHint()
        assertTrue(plans, plans.contains("额度"))
        assertFalse(plans, plans.contains("到期日："))
    }

    @Test
    fun failureStatusWordOnlyNamesTheStatus() {
        assertEquals("失败 · HTTP 502", failureStatusWord(502, 1))
        // 没有 HTTP 码时不编数字。
        assertEquals("失败", failureStatusWord(0, 1))
        // 重试过就把次数写出来。
        assertEquals("失败 · HTTP 502 · 3 次尝试", failureStatusWord(502, 3))
        assertEquals("失败", failureStatusWord(0, 0))
    }

    @Test
    fun sectionErrorLineIsSharedByAllThreeSections() {
        // 同一句口径，只有"读什么"不同。
        assertEquals("网关未启动：启动后才能读取模型重定向。", sectionErrorLine("gateway_not_running", "网关未启动", "模型重定向"))
        val quota = sectionErrorLine("http_500", "管理接口返回 500", "配额历史")
        val failures = sectionErrorLine("http_500", "管理接口返回 500", "请求轨迹")
        assertTrue(quota, quota.contains("配额历史"))
        assertTrue(failures, failures.contains("请求轨迹"))
        assertTrue(quota, quota.contains("http_500"))
        assertTrue(failures, failures.contains("http_500"))
    }
}
