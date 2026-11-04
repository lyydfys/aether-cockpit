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
}
