package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 验证码展示格式与进度占比。
 *
 * 两件事必须锁住：① 只对纯数字码做分组，服务端返回异常形状时原样透出（便于诊断，
 * 也避免把 `error` 文案当成码去"美化"）；② 进度占比不会被负数或 0 周期算出 NaN/负值。
 */
class TotpFormatTest {
    @Test
    fun groupsSixDigitsIntoTwoBlocks() {
        assertEquals("123 456", formatTotpCode("123456"))
        assertEquals("000 000", formatTotpCode("000000"))
    }

    @Test
    fun groupsEightDigitsToo() {
        // 服务端 digits 归一范围是 4-10，8 位要按 3-3-2 分组而不是丢字。
        assertEquals("123 456 78", formatTotpCode("12345678"))
        assertEquals("123 4", formatTotpCode("1234"))
    }

    @Test
    fun keepsUnexpectedPayloadsVerbatim() {
        assertEquals("", formatTotpCode(""))
        assertEquals("12 34", formatTotpCode("12 34"))
        // 非数字内容整段原样返回，不要静默清空。
        assertEquals("ABC DEF", formatTotpCode("ABC DEF"))
    }

    @Test
    fun remainingFractionIsClamped() {
        assertEquals(0.5f, totpRemainingFraction(15, 30), 0.0001f)
        assertEquals(1f, totpRemainingFraction(30, 30), 0.0001f)
        assertEquals(0f, totpRemainingFraction(0, 30), 0.0001f)
        // 负数（时钟回拨/服务端异常）夹到 0，不能变成负进度。
        assertEquals(0f, totpRemainingFraction(-5, 30), 0.0001f)
        // 超过周期也夹到 1。
        assertEquals(1f, totpRemainingFraction(60, 30), 0.0001f)
    }

    @Test
    fun zeroPeriodDoesNotProduceNaN() {
        assertEquals(0f, totpRemainingFraction(10, 0), 0.0001f)
        assertEquals(0f, totpRemainingFraction(10, -30), 0.0001f)
    }
}
