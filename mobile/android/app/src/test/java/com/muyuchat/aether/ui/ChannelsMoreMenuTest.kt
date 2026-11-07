package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号卡「更多」菜单 + 卡片元信息。
 *
 * 桌面依据：`internal/web/ui2/js/views/channels.js:303-318`（菜单顺序与逐字文案）、
 * `:217-357`（卡片结构：head → note → inset(额度/签到) → foot(入库 + 动作)）。
 *
 * 用户反馈"表达不清"：每个菜单项都必须带一句"它到底做什么"；
 * 危险项（停用/删除）单独标记，渲染时用错误色 + 二次确认。
 */
class ChannelsMoreMenuTest {
    private fun items(
        hasNote: Boolean = false,
        tagCount: Int = 0,
        presetCount: Int = 0,
        disabled: Boolean = false,
        canDelete: Boolean = true,
        channelOff: Boolean = false,
        refreshMode: AccountRefreshMode? = null,
        chat: Boolean = false,
    ) = accountMoreMenuItems(
        hasNote = hasNote,
        tagCount = tagCount,
        presetCount = presetCount,
        disabled = disabled,
        canDelete = canDelete,
        channelOff = channelOff,
        refreshMode = refreshMode,
        chat = chat,
    )

    @Test
    fun menuOrderMatchesDesktop() {
        // 基础形态（没有刷新模式、非 chat 渠道）：刷新/唤醒不出现
        assertEquals(
            listOf(
                AccountMoreAction.Details,
                AccountMoreAction.Settings,
                // 「模型」是唯一入口：清单 + 添加缺失模型 + 模型映射合并成一条
                // （桌面 channels.js:1011-1060 的同一个「模型」tab）。
                AccountMoreAction.Models,
                AccountMoreAction.Presets,
                AccountMoreAction.Tags,
                AccountMoreAction.Note,
                AccountMoreAction.CopyConfig,
                AccountMoreAction.Test,
                AccountMoreAction.ToggleDisabled,
                AccountMoreAction.ToggleChannel,
                AccountMoreAction.Delete,
            ),
            items().map { it.action },
        )
    }

    @Test
    fun actionOrderFollowsDesktopFootWithRefreshAndWake() {
        // 桌面卡脚顺序：刷新 → 立即唤醒；菜单内 … 测试 → 刷新 → 唤醒 → 停用 → 删除
        val actions = items(
            refreshMode = AccountRefreshMode.Quota,
            chat = true,
        ).map { it.action }
        assertEquals(
            listOf(
                AccountMoreAction.Details,
                AccountMoreAction.Settings,
                AccountMoreAction.Models,
                AccountMoreAction.Presets,
                AccountMoreAction.Tags,
                AccountMoreAction.Note,
                AccountMoreAction.CopyConfig,
                AccountMoreAction.Test,
                AccountMoreAction.Refresh,
                AccountMoreAction.Wake,
                AccountMoreAction.ToggleDisabled,
                AccountMoreAction.ToggleChannel,
                AccountMoreAction.Delete,
            ),
            actions,
        )
    }

    @Test
    fun refreshAndWakeAreConditional() {
        // 两种刷新模式都不支持的渠道：不出现刷新项（点了没反应比不出现更糟）
        assertFalse(items(refreshMode = null).any { it.action == AccountMoreAction.Refresh })
        assertTrue(
            items(refreshMode = AccountRefreshMode.Quota).any { it.action == AccountMoreAction.Refresh },
        )
        assertTrue(
            items(refreshMode = AccountRefreshMode.Credentials).any { it.action == AccountMoreAction.Refresh },
        )

        // 非 chat 渠道：不出现「立即唤醒」（桌面 `channels.js:352` 同口径）
        assertFalse(items(chat = false).any { it.action == AccountMoreAction.Wake })
        assertTrue(items(chat = true).any { it.action == AccountMoreAction.Wake })
    }

    @Test
    fun everyItemCarriesAOneLineExplanation() {
        items(refreshMode = AccountRefreshMode.Quota, chat = true).forEach { item ->
            assertTrue("${item.label} 缺少说明", item.detail.isNotBlank())
            assertTrue("${item.label} 的说明太长，应是一句话", item.detail.length <= 48)
        }
        val byAction = items(refreshMode = AccountRefreshMode.Quota, chat = true)
            .associateBy { it.action }
        assertTrue(byAction.getValue(AccountMoreAction.Details).detail.contains("只读"))
        assertTrue(byAction.getValue(AccountMoreAction.Settings).detail.contains("路由"))
        assertTrue(byAction.getValue(AccountMoreAction.Test).detail.contains("验证"))
        assertTrue(byAction.getValue(AccountMoreAction.ToggleDisabled).detail.contains("路由"))
    }

    @Test
    fun destructiveItemsAreMarkedDanger() {
        val enabledItems = items(canDelete = true)
        assertTrue(
            "停用必须标 danger",
            enabledItems.first { it.action == AccountMoreAction.ToggleDisabled }.danger,
        )
        assertEquals("停用账号", enabledItems.first { it.action == AccountMoreAction.ToggleDisabled }.label)

        val reenable = items(disabled = true).first { it.action == AccountMoreAction.ToggleDisabled }
        assertEquals("启用账号", reenable.label)
        assertFalse("启用不是危险动作", reenable.danger)

        val deletable = enabledItems.first { it.action == AccountMoreAction.Delete }
        assertTrue(deletable.danger)
        assertEquals("删除账号", deletable.label)

        val channelOn = enabledItems.first { it.action == AccountMoreAction.ToggleChannel }
        assertEquals("停用渠道", channelOn.label)
        assertTrue(channelOn.danger)
        val channelOffItem = items(channelOff = true).first { it.action == AccountMoreAction.ToggleChannel }
        assertEquals("启用渠道", channelOffItem.label)
        assertFalse(channelOffItem.danger)
    }

    @Test
    fun deleteIsRenderedByDefaultAndCanBeTurnedOff() {
        // 端点已核实存在（`DELETE /api/accounts/{id}`，`gateway.go:873`）→ 默认渲染
        assertTrue(items().any { it.action == AccountMoreAction.Delete })
        assertFalse(items(canDelete = false).any { it.action == AccountMoreAction.Delete })
    }

    @Test
    fun labelsReflectCurrentState() {
        val empty = items(hasNote = false, tagCount = 0, presetCount = 0).associateBy { it.action }
        assertEquals("标签", empty.getValue(AccountMoreAction.Tags).label)
        assertEquals("加备注", empty.getValue(AccountMoreAction.Note).label)
        assertEquals("预设", empty.getValue(AccountMoreAction.Presets).label)

        val filled = items(hasNote = true, tagCount = 3, presetCount = 2).associateBy { it.action }
        assertEquals("标签（3 个）", filled.getValue(AccountMoreAction.Tags).label)
        assertEquals("编辑备注", filled.getValue(AccountMoreAction.Note).label)
        assertEquals("预设（已挂 2 个）", filled.getValue(AccountMoreAction.Presets).label)
    }

    @Test
    fun desktopOnlyEntriesAreNotAdvertised() {
        // 桌面有、手机端没实现的入口不出现
        val labels = accountMoreMenuItems(false, 0, 0, canDelete = true).map { it.label }
        assertFalse(labels.any { it.contains("导出") })
        assertFalse(labels.any { it.contains("IDE") })
        // 桌面有的核心项一个不能少
        listOf("详情", "设置：优先级 / 权重 / 并发", "预设", "标签", "复制配置", "测试连通性", "删除账号")
            .forEach { expected ->
                assertTrue("缺少桌面项：$expected", labels.any { it == expected || it.startsWith("$expected（") })
            }
    }
}
