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
        canDelete: Boolean = false,
        channelOff: Boolean = false,
    ) = accountMoreMenuItems(hasNote, tagCount, presetCount, disabled, canDelete, channelOff)

    @Test
    fun menuOrderMatchesDesktop() {
        val actions = items().map { it.action }
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
                AccountMoreAction.ToggleDisabled,
                AccountMoreAction.ToggleChannel,
            ),
            actions,
        )
    }

    @Test
    fun everyItemCarriesAOneLineExplanation() {
        items(canDelete = true).forEach { item ->
            assertTrue("${item.label} 缺少说明", item.detail.isNotBlank())
            assertTrue("${item.label} 的说明太长，应是一句话", item.detail.length <= 40)
        }
        // 关键项要说到"做什么"，不是复述标题
        val byAction = items().associateBy { it.action }
        assertTrue(byAction.getValue(AccountMoreAction.Details).detail.contains("只读"))
        assertTrue(byAction.getValue(AccountMoreAction.Settings).detail.contains("路由"))
        assertTrue(byAction.getValue(AccountMoreAction.Test).detail.contains("验证"))
        assertTrue(byAction.getValue(AccountMoreAction.ToggleDisabled).detail.contains("路由"))
    }

    @Test
    fun destructiveItemsAreMarkedDanger() {
        // 停用是危险动作（要二次确认）；启用不是
        val enabledItems = items(disabled = false)
        assertTrue(
            "停用必须标 danger",
            enabledItems.first { it.action == AccountMoreAction.ToggleDisabled }.danger,
        )
        assertEquals("停用账号", enabledItems.first { it.action == AccountMoreAction.ToggleDisabled }.label)

        val disabledItems = items(disabled = true)
        val reenable = disabledItems.first { it.action == AccountMoreAction.ToggleDisabled }
        assertEquals("启用账号", reenable.label)
        assertFalse("启用不是危险动作", reenable.danger)

        // 删除是危险动作（有端点时才出现）
        val deletable = items(canDelete = true).first { it.action == AccountMoreAction.Delete }
        assertTrue(deletable.danger)
        assertEquals("删除账号", deletable.label)

        // 渠道级停用同为危险动作；启用不是
        val channelOn = items(channelOff = false).first { it.action == AccountMoreAction.ToggleChannel }
        assertEquals("停用渠道", channelOn.label)
        assertTrue(channelOn.danger)
        val channelOffItem = items(channelOff = true).first { it.action == AccountMoreAction.ToggleChannel }
        assertEquals("启用渠道", channelOffItem.label)
        assertFalse(channelOffItem.danger)
    }

    @Test
    fun deleteIsHiddenWhenMobileHasNoEndpoint() {
        // 手机端没有删除账号的端点：默认不渲染，避免"点了必失败"的入口
        assertFalse(items().any { it.action == AccountMoreAction.Delete })
        assertTrue(items(canDelete = true).any { it.action == AccountMoreAction.Delete })
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
        // 桌面有、手机端没实现的入口（导出…、写入本机 IDE）不应出现在菜单里
        val labels = accountMoreMenuItems(false, 0, 0, canDelete = true).map { it.label }
        assertFalse(labels.any { it.contains("导出") })
        assertFalse(labels.any { it.contains("IDE") })
        // 但桌面有的核心项一个不能少
        listOf("详情", "设置：优先级 / 权重 / 并发", "预设", "标签", "复制配置", "测试连通性", "删除账号")
            .forEach { expected ->
                assertTrue("缺少桌面项：$expected", labels.any { it == expected || it.startsWith("$expected（") })
            }
    }
}
