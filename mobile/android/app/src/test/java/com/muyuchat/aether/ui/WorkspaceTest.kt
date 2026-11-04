package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WorkspaceTest {
    @Test
    fun directoryMatchesTheApprovedDesign() {
        val workspaces = workspaceEntries()
        // 12 条 = 桌面 16 项导航（shell.js:17-43）减去底栏已有的 4 项
        //（概览 / 账号与额度 / 用量 / 请求日志），加「客户端」
        //（桌面是 gateway.js:113 的一张卡，手机端有独立页），再减「会话」。
        // 会话页已整页删除：`sessions.Scan()` 只读本机 agent 会话文件，Android 上没有这些文件，
        // 页面在手机上恒为空，留着只会误导（用户 2026-11-04 决定删除）。
        assertEquals(12, workspaces.size)
        assertEquals(workspaces.size, workspaces.map { it.title }.toSet().size)
        val settings = settingsGroups()
        // 设置分组 = 桌面 settings.js:44-47 的 10 个 tab，逐字照抄。
        assertEquals(10, settings.size)
        assertEquals(settings.size, settings.map { it.title }.toSet().size)
        assertEquals((1..10).map { "%02d".format(it) }, settings.map { it.index })
        assertEquals(
            listOf("常规", "网络与共享", "工具服务", "预设", "定价", "模型目录", "隐私", "可观测", "同步与备份", "关于"),
            settings.map { it.title },
        )
    }

    /**
     * 回归：上一版把去向藏在 `entry.title == "路由"` 这类字符串比较里，结果只有 3 个真跳——
     * 用量/请求/渠道三个**已经能用**的页面点了却弹「后续阶段」。
     * 现在去向是数据（[WorkspaceAction]），这里逐条锁住"哪些必须真跳"。
     *
     * 标题与桌面 `shell.js:19-42` 逐字一致（唤醒任务 / 应用启动 / 库（指令/技能/MCP） /
     * 网关与探针 / 2FA 管理），这样「更多」页能和桌面导航逐条对照。
     */
    @Test
    fun implementedWorkspacesGoToRealDestinations() {
        val byTitle = workspaceEntries().associateBy { it.title }
        assertEquals(WorkspaceAction.Onboard, byTitle.getValue("接入渠道").action)
        assertEquals(WorkspaceAction.Wake, byTitle.getValue("唤醒任务").action)
        assertEquals(WorkspaceAction.DesktopOnly, byTitle.getValue("应用启动").action)
        assertEquals(WorkspaceAction.Library, byTitle.getValue("库（指令/技能/MCP）").action)
        assertEquals(WorkspaceAction.Probe, byTitle.getValue("网关与探针").action)
        assertEquals(WorkspaceAction.Totp, byTitle.getValue("2FA 管理").action)
        assertEquals(WorkspaceAction.Settings, byTitle.getValue("设置").action)
        assertEquals(WorkspaceAction.Settings, byTitle.getValue("脱敏").action)
        assertEquals(WorkspaceAction.Routing, byTitle.getValue("路由").action)
        assertEquals(WorkspaceAction.Plugins, byTitle.getValue("插件").action)
        // 调用方密钥页 / Agent 接入页 / 客户端页都是真实页面。
        assertEquals(WorkspaceAction.Agents, byTitle.getValue("Agent 接入").action)
        assertEquals(WorkspaceAction.Clients, byTitle.getValue("客户端").action)
    }

    /**
     * 「更多」页不许重复底栏已有的目的地。
     *
     * 桌面 16 项导航里，概览 / 账号与额度 / 用量 / 请求日志在手机上是底栏的 4 个 Tab；
     * 再在「更多」里列一遍会出现同一目的地的两个入口（而且手机端还自造过一条
     * 「额度快捷查看」指向额度 Tab）。这条测试把"不许重复"钉死。
     */
    @Test
    fun morePageDoesNotRepeatBottomBarDestinations() {
        val bottomTabs = setOf(
            MobileTab.OVERVIEW,
            MobileTab.CHANNELS,
            MobileTab.USAGE,
            MobileTab.REQUESTS,
        )
        val duplicated = workspaceEntries()
            .mapNotNull { entry -> (entry.action as? WorkspaceAction.Tab)?.tab }
            .filter { it in bottomTabs }
        assertTrue("「更多」里出现了底栏已有的 Tab：$duplicated", duplicated.isEmpty())
        // 也不允许出现指向「更多」自己的 Tab。
        assertTrue(workspaceEntries().none { (it.action as? WorkspaceAction.Tab)?.tab == MobileTab.MORE })
    }

    @Test
    fun nothingIsSilentlyWiredToANonExistentDestination() {
        // 每一行要么能到真实页面，要么明确是后续阶段，要么明确是仅桌面端——不允许出现
        // 「点了什么都不发生，也没有说明」的第四种状态。
        workspaceEntries().forEach { entry ->
            when (val action = entry.action) {
                is WorkspaceAction.Tab -> assertTrue(action.tab != MobileTab.MORE)
                WorkspaceAction.Routing, WorkspaceAction.Settings, WorkspaceAction.Probe,
                WorkspaceAction.Totp, WorkspaceAction.Wake,
                WorkspaceAction.Library, WorkspaceAction.Plugins, WorkspaceAction.Clients,
                WorkspaceAction.Agents, WorkspaceAction.Onboard -> Unit
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
        assertEquals(listOf("应用启动"), desktop.map { it.title })
        assertEquals(null, desktop.single().pendingStage)
        assertTrue(desktop.single().subtitle.contains("仅 Windows"))
    }

    @Test
    fun pendingEntriesCarryTheirOwnStage() {
        val pending = workspaceEntries().filter { it.action is WorkspaceAction.Pending }
        // 13 条工作区（桌面 16 项去掉底栏 4 项，加「客户端」）现已全部接线，
        // 所以这里锁的不再是"至少要有一个占位"，而是"占位消失了"这件事本身——
        // 将来若再加占位，这条会失败，提醒作者显式承认。
        assertTrue("13 条工作区应已全部接线，仍有占位：${pending.map { it.title }}", pending.isEmpty())
        assertTrue(pending.all { !it.pendingStage.isNullOrBlank() })
        // 已接线的入口不该再带阶段占位。
        val wired = workspaceEntries().filter { it.action !is WorkspaceAction.Pending }
        assertTrue(wired.all { it.pendingStage == null })
    }

    /**
     * 只有 02（网络与共享）与 07（隐私）的内容今天真的在设置页里；
     * 不能因为"设置页存在"就宣称 10 组都能用。
     */
    @Test
    fun onlyImplementedsSettingsGroupsAreMarkedAvailable() {
        val available = settingsGroups().filter { it.available }.map { it.index }
        assertEquals(listOf("02", "07"), available)
    }

    @Test
    fun searchFiltersBothSections() {
        // 「额度」是底栏 Tab，不在「更多」列表里；搜索它应当什么都不返回。
        assertTrue(filterWorkspaces("额度").isEmpty())

        assertEquals(listOf("2FA 管理"), filterWorkspaces("2fa").map { it.title })
        assertTrue(filterWorkspaces("Windows").map { it.title }.contains("应用启动"))
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
