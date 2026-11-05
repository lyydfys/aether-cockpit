package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Release notes 不显示 Markdown 强调符（手机端不做完整 Markdown 渲染，
 * 但 `**` 原样出现是最典型的"廉价感"来源）。
 *
 * 依据：MCA 的更新说明走自己的渲染器；本仓库只做纯文本降级，所以必须显式剥掉强调符。
 */
class AboutReleaseNotesMarkdownTest {
    @Test
    fun stripsBoldMarkers() {
        assertEquals("修复登录问题", stripMarkdownEmphasis("**修复登录问题**"))
        assertEquals("新增 A 和 B", stripMarkdownEmphasis("新增 **A** 和 **B**"))
        assertFalse(stripMarkdownEmphasis("**重要**").contains("**"))
    }

    @Test
    fun stripsUnderscoreBoldAndInlineCode() {
        assertEquals("重点", stripMarkdownEmphasis("__重点__"))
        assertEquals("运行 aether --help", stripMarkdownEmphasis("运行 `aether --help`"))
    }

    @Test
    fun stripsSingleStarItalicButKeepsListBullets() {
        assertEquals("斜体", stripMarkdownEmphasis("*斜体*"))
        // 列表符号 `* 项目` 不是成对强调，必须保留
        assertEquals("* 项目一\n* 项目二", stripMarkdownEmphasis("* 项目一\n* 项目二"))
    }

    @Test
    fun keepsPlainTextUntouched() {
        val plain = "- 修复 X\n- 优化 Y"
        assertEquals(plain, stripMarkdownEmphasis(plain))
        assertEquals("", stripMarkdownEmphasis(""))
    }

    @Test
    fun releaseNotesTextAppliesStrippingThenTruncates() {
        val text = aboutReleaseNotesText("**标题**\n正文", limit = 1200)
        assertEquals("标题\n正文", text)
        val long = aboutReleaseNotesText("**" + "x".repeat(2000) + "**", limit = 100)
        assertTrue("要截断并加省略号", long.endsWith("..."))
        assertFalse("截断后也不能残留 **", long.contains("**"))
        assertEquals(100, long.length)
    }
}
