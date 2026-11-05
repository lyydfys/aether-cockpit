package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 根页返回策略（最终口径）：**概览是唯一根部**。
 *
 * - 概览 → 不接管（系统退出）；
 * - 其它四个 tab → 一律回概览，不退出。
 * 没有 tab 历史栈——之前的 push/pop 方案已随需求简化删除。
 */
class RootBackPolicyTest {
    private val nonOverviewTabs = MobileTab.entries.filter { it != MobileTab.OVERVIEW }

    @Test
    fun overviewDoesNotHandleBack() {
        assertFalse(
            "概览必须交系统退出",
            RootBackPolicy.shouldHandleBack(anySubpageOpen = false, selectedTab = MobileTab.OVERVIEW),
        )
        assertNull("概览没有返回目标", RootBackPolicy.targetTab(MobileTab.OVERVIEW))
    }

    @Test
    fun subpageAlwaysHandlesBack() {
        assertTrue(
            "覆盖层打开时必须接管",
            RootBackPolicy.shouldHandleBack(anySubpageOpen = true, selectedTab = MobileTab.OVERVIEW),
        )
    }

    @Test
    fun everyNonOverviewTabHandlesBackAndTargetsOverview() {
        assertTrue("非概览 tab 不应为空", nonOverviewTabs.isNotEmpty())
        nonOverviewTabs.forEach { tab ->
            assertTrue("$tab 必须接管返回", RootBackPolicy.shouldHandleBack(false, tab))
            assertEquals("$tab 的返回目标必须是概览", MobileTab.OVERVIEW, RootBackPolicy.targetTab(tab))
        }
    }

    @Test
    fun theFourSeriesPagesAreCovered() {
        val named = listOf(MobileTab.CHANNELS, MobileTab.USAGE, MobileTab.REQUESTS, MobileTab.MORE)
        named.forEach { tab ->
            assertTrue("$tab 要接管", RootBackPolicy.shouldHandleBack(false, tab))
            assertEquals(MobileTab.OVERVIEW, RootBackPolicy.targetTab(tab))
        }
    }

    @Test
    fun overviewWithSubpageStillHandles() {
        assertTrue(RootBackPolicy.shouldHandleBack(anySubpageOpen = true, selectedTab = MobileTab.OVERVIEW))
    }
}
