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
    fun menuEntrySitsBetweenModelsAndPresets() {
        val actions = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0).map { it.action }
        val models = actions.indexOf(AccountMoreAction.Models)
        val extra = actions.indexOf(AccountMoreAction.ExtraModels)
        val presets = actions.indexOf(AccountMoreAction.Presets)
        assertTrue("三项都要存在", models >= 0 && extra >= 0 && presets >= 0)
        // 「模型」相关动作聚在一起：Models → ModelMapping → ExtraModels → Presets。
        // 不断言 extra == models+1（中间还有「模型映射」），只断言它落在 Models 之后、Presets 之前。
        assertTrue("「添加缺失模型」要在「查看模型」之后", extra > models)
        assertTrue("「添加缺失模型」要在「预设」之前", extra < presets)

        val item = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0)
            .first { it.action == AccountMoreAction.ExtraModels }
        assertEquals("添加缺失模型", item.label)
        // 菜单行只放一句话（≤48 字约束）；桌面原话完整版放在弹窗正文，见下一条断言。
        assertTrue("菜单说明要提到漏报", item.detail.contains("漏报"))
        assertTrue("菜单说明不能太长", item.detail.length <= 48)
        assertEquals(
            "弹窗正文用桌面原话",
            "上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models",
            extraModelNote(),
        )
        assertFalse("它不是危险操作", item.danger)
    }
}
