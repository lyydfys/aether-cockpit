package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「仅 Windows」与「按设计不做」的说明数据。
 *
 * 这份清单是给用户看的**解释**，不是待办列表，所以测试盯三件事：
 * 1. 11 项一个不少（少一项就等于偷偷承诺了）；
 * 2. 每条都必须有**原因**（为什么手机上做不到），不能只列功能名；
 * 3. 不许出现"即将支持/敬请期待/稍后"这类空承诺——那是假希望。
 */
class DesktopOnlyCapabilitiesTest {
    @Test
    fun elevenDesktopOnlyItems() {
        assertEquals(11, desktopOnlyCapabilities().size)
    }

    @Test
    fun everyItemCarriesAReason() {
        desktopOnlyCapabilities().forEach { item ->
            assertTrue("「${item.title}」缺少原因", item.reason.isNotBlank())
            // 原因必须是"为什么做不到"，而不是重复标题
            assertTrue("「${item.title}」的原因不能只是标题", item.reason != item.title)
        }
    }

    @Test
    fun noEmptyPromises() {
        val forbidden = listOf("即将支持", "敬请期待", "稍后支持", "正在开发", "coming soon")
        val all = (desktopOnlyCapabilities() + listOf(byDesignNotSupported())).joinToString(" ") {
            it.title + it.reason
        }
        forbidden.forEach { word ->
            assertTrue("不许出现空承诺「$word」", !all.contains(word, ignoreCase = true))
        }
    }

    @Test
    fun titlesAreUnique() {
        val titles = desktopOnlyCapabilities().map { it.title }
        assertEquals(titles.size, titles.distinct().size)
    }

    @Test
    fun byDesignItemIsSeparatedFromDesktopOnly() {
        val design = byDesignNotSupported()
        assertTrue(design.isByDesign)
        assertEquals("请求正文查看", design.title)
        // 必须点明"正文根本没存"，否则读者会以为补个接口就行
        assertTrue(design.reason.contains("落盘") || design.reason.contains("不保存"))
        assertTrue(
            "按设计不做的项不能混进仅 Windows 列表",
            desktopOnlyCapabilities().none { it.title == design.title },
        )
    }
}
