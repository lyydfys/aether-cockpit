package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WorkspaceTest {
    @Test
    fun directoryMatchesTheApprovedDesign() {
        val workspaces = workspaceEntries()
        // 18 条：桌面端「Agent 接入」与「客户端接入片段」是两个独立视图
        // （internal/web/ui2/js/views/gateway.js:113、overview.js:64），
        // 所以手机端也保留两条入口，不再为了守住 17 让 ClientsPage 变成不可达死页面。
        assertEquals(18, workspaces.size)
        assertEquals(workspaces.size, workspaces.map { it.title }.toSet().size)
        val settings = settingsGroups()
        assertEquals(9, settings.size)
        assertEquals(settings.size, settings.map { it.title }.toSet().size)
        assertEquals((1..9).map { "%02d".format(it) }, settings.map { it.index })
    }

    /**
     * 回归：上一版把去向藏在 `entry.title == "路由"` 这类字符串比较里，结果 17 个工作区
     * 只有 3 个真跳——用量/请求/渠道三个**已经能用**的页面点了却弹「后续阶段」。
     * 现在去向是数据（[WorkspaceAction]），这里逐条锁住"哪些必须真跳"。
     */
    @Test
    fun implementedWorkspacesGoToRealDestinations() {
        val byTitle = workspaceEntries().associateBy { it.title }
        assertEquals(WorkspaceAction.Tab(MobileTab.OVERVIEW), byTitle.getValue("概览").action)
        assertEquals(WorkspaceAction.Tab(MobileTab.CHANNELS), byTitle.getValue("渠道接入").action)
        assertEquals(WorkspaceAction.Tab(MobileTab.CHANNELS), byTitle.getValue("账号额度").action)
        assertEquals(WorkspaceAction.Tab(MobileTab.CHANNELS), byTitle.getValue("额度快捷查看").action)
        assertEquals(WorkspaceAction.Tab(MobileTab.USAGE), byTitle.getValue("用量").action)
        assertEquals(WorkspaceAction.Tab(MobileTab.REQUESTS), byTitle.getValue("请求").action)
        assertEquals(WorkspaceAction.Routing, byTitle.getValue("路由").action)
        assertEquals(WorkspaceAction.Settings, byTitle.getValue("设置").action)
        assertEquals(WorkspaceAction.Settings, byTitle.getValue("脱敏").action)
        assertEquals(WorkspaceAction.Probe, byTitle.getValue("网关探针").action)
        // Stage 6 第一片：2FA 从"后续阶段"变成真实页面。
        assertEquals(WorkspaceAction.Totp, byTitle.getValue("2FA").action)
        // Stage 6 第二片：会话页（只读、手机端如实空态）。
        assertEquals(WorkspaceAction.Sessions, byTitle.getValue("会话").action)
        // Stage 6 第三片：唤醒队列（只读状态 + 手动触发，不承诺定时）。
        assertEquals(WorkspaceAction.Wake, byTitle.getValue("任务").action)
        // Stage 6 第四片：资源库（查看/删除；发现与下发仅桌面端）。
        assertEquals(WorkspaceAction.Library, byTitle.getValue("资源库").action)
        // 「Agent」曾经错指到客户端页（WorkspaceAction.Clients）；现在指向真正的 Agent 接入页。
        assertEquals(WorkspaceAction.Agents, byTitle.getValue("Agent 接入").action)
        // 桌面端这两个是独立视图（misc.js:10 与 gateway.js:113），所以手机端也拆成两行；
        // 「客户端」那一行的入口必须回来，否则 ClientsPage 变成不可达页面。
        assertEquals(WorkspaceAction.Clients, byTitle.getValue("客户端").action)
    }

    @Test
    fun nothingIsSilentlyWiredToANonExistentDestination() {
        // 每一行要么能到真实页面，要么明确是后续阶段，要么明确是仅桌面端——不允许出现
        // 「点了什么都不发生，也没有说明」的第四种状态。
        workspaceEntries().forEach { entry ->
            when (val action = entry.action) {
                is WorkspaceAction.Tab -> assertTrue(action.tab != MobileTab.MORE)
                WorkspaceAction.Routing, WorkspaceAction.Settings, WorkspaceAction.Probe,
                WorkspaceAction.Totp, WorkspaceAction.Sessions, WorkspaceAction.Wake,
                WorkspaceAction.Library, WorkspaceAction.Plugins, WorkspaceAction.Clients,
                WorkspaceAction.Agents -> Unit
                is WorkspaceAction.Pending -> assertTrue(
                    "${entry.title} 的占位阶段不能为空",
                    action.stage.isNotBlank(),
                )
                WorkspaceAction.DesktopOnly -> assertTrue(entry.subtitle.contains("仅 Windows"))
            }
        }
    }

    @Test
    fun desktopOnlyEntryIsHonest() {
        val desktop = workspaceEntries().filter { it.desktopOnly }
        assertEquals(listOf("应用"), desktop.map { it.title })
        assertEquals(null, desktop.single().pendingStage)
        assertTrue(desktop.single().subtitle.contains("仅 Windows"))
    }

    @Test
    fun pendingEntriesCarryTheirOwnStage() {
        val pending = workspaceEntries().filter { it.action is WorkspaceAction.Pending }
        // 18 条工作区现已全部接线（「Agent 接入」与「客户端」按桌面端拆成两行），
        // 所以这里锁的不再是"至少要有一个占位"，而是"占位消失了"这件事本身——
        // 将来若再加占位，这条会失败，提醒作者显式承认。
        assertTrue("18 条工作区应已全部接线，仍有占位：${pending.map { it.title }}", pending.isEmpty())
        assertTrue(pending.all { !it.pendingStage.isNullOrBlank() })
        // 已接线的入口不该再带阶段占位。
        val wired = workspaceEntries().filter { it.action !is WorkspaceAction.Pending }
        assertTrue(wired.all { it.pendingStage == null })
    }

    /**
     * 只有 02（网络与共享）与 06（隐私）的内容今天真的在设置页里；
     * 不能因为"设置页存在"就宣称 9 组都能用。
     */
    @Test
    fun onlyImplementedsSettingsGroupsAreMarkedAvailable() {
        val available = settingsGroups().filter { it.available }.map { it.index }
        assertEquals(listOf("02", "06"), available)
    }

    @Test
    fun searchFiltersBothSections() {
        val quota = filterWorkspaces("额度").map { it.title }
        assertTrue(quota.contains("账号额度"))
        assertTrue(quota.contains("额度快捷查看"))

        assertEquals(listOf("2FA"), filterWorkspaces("2fa").map { it.title })
        assertTrue(filterWorkspaces("Windows").map { it.title }.contains("应用"))
        assertEquals(listOf("隐私"), filterSettings("脱敏").map { it.title })
        assertTrue(filterWorkspaces("没有这个功能").isEmpty())
        assertTrue(filterSettings("没有这个功能").isEmpty())
    }

    @Test
    fun desktopOnlyEntryIsNotClickable() {
        val desktop = workspaceEntries().single { it.desktopOnly }
        assertFalse(desktop.action is WorkspaceAction.Tab)
    }
}
