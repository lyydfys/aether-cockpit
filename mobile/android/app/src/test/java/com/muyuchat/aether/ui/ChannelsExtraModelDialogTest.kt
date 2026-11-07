package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ExtraModelAction
import com.muyuchat.aether.runtime.extraModelNote
import com.muyuchat.aether.runtime.extraModelRowShowsRemove
import com.muyuchat.aether.runtime.validateExtraModelName
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道额外模型弹窗的 UI 判定（数据层在 `ChannelsExtraModelTest` 已覆盖路径/请求体）。
 *
 * 桌面依据 `channels.js:1018-1034`：「添加缺失模型」+ 每模型「删除」（action=remove），
 * 说明原话「上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models」。
 */
class ChannelsExtraModelDialogTest {
    @Test
    fun noteKeepsDesktopWording() {
        val note = extraModelNote()
        assertEquals("上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models", note)
        assertTrue(note.contains("/models"))
        assertTrue(note.contains("路由"))
    }

    @Test
    fun removeButtonOnlyOnExtraModelRows() {
        // 只给"手动追加"的模型渲染删除：对内置模型发 remove 会被服务端 400
        assertTrue(extraModelRowShowsRemove(isExtraModel = true))
        assertFalse(extraModelRowShowsRemove(isExtraModel = false))
    }

    @Test
    fun addFormValidatesBeforeHittingTheNetwork() {
        // 空 / 含空格在本地就拦住（错误显示在输入框下方），不发请求
        assertEquals("请输入模型名", validateExtraModelName(""))
        assertEquals("请输入模型名", validateExtraModelName("   "))
        assertEquals("模型名里不能有空格", validateExtraModelName("gpt 4o"))
        assertNull(validateExtraModelName("deepseek-v4.1-flash"))
    }

    @Test
    fun bothActionsMapToTheHandlerWireValues() {
        assertEquals("add", ExtraModelAction.Add.wire)
        assertEquals("remove", ExtraModelAction.Remove.wire)
    }

    @Test
    fun menuHasSingleModelsEntryAfterMerge() {
        val items = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0)
        val actions = items.map { it.action }
        val models = actions.indexOf(AccountMoreAction.Models)
        val presets = actions.indexOf(AccountMoreAction.Presets)
        assertTrue("「模型」与「预设」都要存在", models >= 0 && presets >= 0)
        // 合并后只有一条「模型」：清单 / 添加缺失模型 / 模型映射都在同一个弹窗里
        //（桌面 channels.js:1011-1060 的同一个「模型」tab）。
        assertEquals(1, actions.count { it == AccountMoreAction.Models })
        assertTrue("「模型」要在「预设」之前", models < presets)

        val item = items.first { it.action == AccountMoreAction.Models }
        assertTrue("菜单说明要提到这三段都在里面", item.detail.contains("模型清单"))
        assertTrue("菜单说明不能太长", item.detail.length <= 48)
        assertEquals(
            "弹窗正文仍用桌面原话",
            "上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models",
            extraModelNote(),
        )
        assertFalse("它不是危险操作", item.danger)
    }

    @Test
    fun modelsEntryShowsMappingCountWhenPresent() {
        // 有映射时把条数写在标签里，用户一眼知道配过（与之前「模型映射（N 条）」同口径）。
        val withMapping = accountMoreMenuItems(
            hasNote = false,
            tagCount = 0,
            presetCount = 0,
            mappingCount = 3,
        ).first { it.action == AccountMoreAction.Models }
        assertEquals("模型（含 3 条映射）", withMapping.label)

        val without = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0)
            .first { it.action == AccountMoreAction.Models }
        assertEquals("模型", without.label)
    }
}
