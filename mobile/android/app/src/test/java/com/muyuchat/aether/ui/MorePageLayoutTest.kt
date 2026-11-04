package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「更多」目录页的排版合同。
 *
 * 覆盖三件事：
 *  1. 每一行的右缘尾标与该行的真实去向一一对应（不许把未接线的写成「已接入」）；
 *  2. 搜索空态必须说清「为什么空」和「下一步」；
 *  3. 目录计数取自 [settingsGroups] / [workspaceEntries]，不是手写的数字。
 */
class MorePageLayoutTest {

    @Test
    fun workspaceTailTellsTheRealDestination() {
        assertEquals("当前", workspaceRowTrailing(entry(WorkspaceAction.Tab(MobileTab.OVERVIEW))))
        assertEquals("用量", workspaceRowTrailing(entry(WorkspaceAction.Tab(MobileTab.USAGE))))
        assertEquals("请求", workspaceRowTrailing(entry(WorkspaceAction.Tab(MobileTab.REQUESTS))))
        assertEquals("仅 Windows", workspaceRowTrailing(entry(WorkspaceAction.DesktopOnly)))
        assertEquals("Stage 6", workspaceRowTrailing(entry(WorkspaceAction.Pending("Stage 6"))))
    }

    @Test
    fun workspaceTailMarksEveryWiredPageAsWired() {
        // 这些是已经能打开的页面：尾标统一写「已接入」，不各写一套。
        listOf(
            WorkspaceAction.Probe,
            WorkspaceAction.Routing,
            WorkspaceAction.Settings,
            WorkspaceAction.Totp,
            WorkspaceAction.Clients,
            WorkspaceAction.Sessions,
            WorkspaceAction.Wake,
            WorkspaceAction.Library,
            WorkspaceAction.Plugins,
        ).forEach { action ->
            assertEquals(action.toString(), "已接入", workspaceRowTrailing(entry(action)))
        }
    }

    @Test
    fun settingsTailOnlyClaimsWiredForAvailableGroups() {
        val wired = SettingsGroup("02", "网络与共享", "端口与局域网", available = true)
        val pending = SettingsGroup("01", "常规", "外观与语言")
        assertEquals("已接入", settingsRowTrailing(wired))
        assertEquals("后续阶段", settingsRowTrailing(pending))
        // 目录本身的事实：17 个工作区、9 个设置分组，且只有 2 组真的接进了设置页。
        assertEquals(17, workspaceEntries().size)
        assertEquals(9, settingsGroups().size)
        assertEquals(2, settingsGroups().count { it.available })
    }

    @Test
    fun searchEmptyStateSaysWhyAndWhatNext() {
        val detail = moreSearchEmptyDetail("路由")
        assertTrue("要点出被搜索的关键词", detail.contains("「路由」"))
        assertTrue("要用真实计数，不写死", detail.contains("9 个设置分组"))
        assertTrue("要用真实计数，不写死", detail.contains("17 个工作区"))
        assertTrue("要给出下一步", detail.contains("清除搜索"))
        // 匹配是「标题或说明」，文案不能夸大成「全文搜索」。
        assertTrue(detail.contains("标题或说明"))
    }

    @Test
    fun searchEmptyStateDegradesForBlankQuery() {
        // 纯函数不留未定义分支：空查询也要有一句能显示的话。
        val detail = moreSearchEmptyDetail("   ")
        assertTrue(detail.isNotBlank())
        assertTrue(detail.contains("9 个设置分组"))
    }

    private fun entry(action: WorkspaceAction): WorkspaceEntry =
        WorkspaceEntry(title = "测试项", subtitle = "说明", action = action)
}
