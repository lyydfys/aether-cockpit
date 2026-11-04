package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Test

/** 会话页的相对时间文案；负数与缺失值都不能拼出「-3 分钟前」。 */
class SessionsFormatTest {
    private val now = 1_800_000_000_000L // 固定"现在"，避免测试依赖真实时钟

    @Test
    fun formatsRelativeAges() {
        assertEquals("刚刚", sessionAgeLabel(now / 1000 - 10, now))
        assertEquals("1 分钟前", sessionAgeLabel(now / 1000 - 90, now))
        assertEquals("2 小时前", sessionAgeLabel(now / 1000 - 7_200, now))
        assertEquals("3 天前", sessionAgeLabel(now / 1000 - 3 * 86_400, now))
    }

    @Test
    fun clockSkewAndMissingValuesAreHonest() {
        // 时钟回拨：不显示负时间。
        assertEquals("刚刚", sessionAgeLabel(now / 1000 + 500, now))
        // 服务端没给时间。
        assertEquals("时间未知", sessionAgeLabel(0L, now))
        assertEquals("时间未知", sessionAgeLabel(-5L, now))
    }
}
