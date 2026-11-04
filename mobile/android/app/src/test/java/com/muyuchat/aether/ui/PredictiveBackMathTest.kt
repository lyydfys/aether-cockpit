package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 预测性返回的**纯数学**部分：progress → 位移 / 缩放 / 遮罩。
 *
 * 之所以把这些单独抽成函数，是因为手势本身（`PredictiveBackHandler` 的流）只能在真机上验，
 * 而"手指挥到 30% 时页面该被推开多少"是可以用断言钉死的：这套坐标同时决定 1.0× / 1.3×
 * 与不同屏宽下的观感，写错了就是"页面甩出屏幕"或"根本不跟手"。
 */
class PredictiveBackMathTest {

    @Test
    fun zeroProgressMeansNoMotionAtAll() {
        // progress=0（还没开始 / 已回弹完）：不位移、不缩放、遮罩全透明。
        assertEquals(0f, backOverlayOffset(0f, width = 1080f), 1e-4f)
        assertEquals(1f, backOverlayScale(0f), 1e-4f)
        assertEquals(0f, backScrimAlpha(0f), 1e-4f)
    }

    @Test
    fun fullProgressMovesExactlyOneScreenWidth() {
        // progress=1（滑到底）：位移 = 整屏宽度，缩放 = 0.96，遮罩 = 0.25。
        assertEquals(1080f, backOverlayOffset(1f, width = 1080f), 1e-4f)
        assertEquals(BackOverlayRestScale, backOverlayScale(1f), 1e-4f)
        assertEquals(0.96f, backOverlayScale(1f), 1e-4f)
        assertEquals(BackOverlayScrimAlpha, backScrimAlpha(1f), 1e-4f)
        assertEquals(0.25f, backScrimAlpha(1f), 1e-4f)
    }

    @Test
    fun midwayProgressIsLinearAndMonotonic() {
        // 线性：50% → 半屏、0.98、0.125。线性是刻意的——非线性会让"跟手"变成"追不上/超前"。
        assertEquals(540f, backOverlayOffset(0.5f, width = 1080f), 1e-4f)
        assertEquals(0.98f, backOverlayScale(0.5f), 1e-4f)
        assertEquals(0.125f, backScrimAlpha(0.5f), 1e-4f)
        // 单调：进度越大，位移越大、缩放越小、遮罩越深。
        val samples = listOf(0f, 0.2f, 0.4f, 0.6f, 0.8f, 1f)
        assertTrue(samples.zipWithNext().all { (a, b) -> backOverlayOffset(a, 1000f) < backOverlayOffset(b, 1000f) })
        assertTrue(samples.zipWithNext().all { (a, b) -> backOverlayScale(a) > backOverlayScale(b) })
        assertTrue(samples.zipWithNext().all { (a, b) -> backScrimAlpha(a) < backScrimAlpha(b) })
    }

    @Test
    fun outOfRangeProgressIsClamped() {
        // 系统理论只给 0..1，但回弹期间/异常帧可能越界；越界必须夹住，不能把页面推两屏。
        assertEquals(0f, backOverlayOffset(-0.4f, 1080f), 1e-4f)
        assertEquals(0f, backScrimAlpha(-0.4f), 1e-4f)
        assertEquals(1f, backOverlayScale(-0.4f), 1e-4f)
        assertEquals(1080f, backOverlayOffset(1.7f, 1080f), 1e-4f)
        assertEquals(0.25f, backScrimAlpha(1.7f), 1e-4f)
        assertEquals(0.96f, backOverlayScale(1.7f), 1e-4f)
        // Inf 交给 coerceIn 收敛到端点。
        assertEquals(1f, sanitizeBackProgress(Float.POSITIVE_INFINITY), 1e-4f)
        assertEquals(0f, sanitizeBackProgress(Float.NEGATIVE_INFINITY), 1e-4f)
    }

    @Test
    fun nanProgressFallsBackToZeroInsteadOfPoisoningLayout() {
        // Float.coerceIn 对 NaN 会原样返回 NaN；若不单独兜底，位移/缩放会变成 NaN，
        // 页面直接消失（Compose 布局里 NaN 尺寸的典型表现）。所以 NaN 一律当 0。
        assertEquals(0f, sanitizeBackProgress(Float.NaN), 1e-4f)
        assertEquals(0f, backOverlayOffset(Float.NaN, 1080f), 1e-4f)
        assertEquals(1f, backOverlayScale(Float.NaN), 1e-4f)
        assertEquals(0f, backScrimAlpha(Float.NaN), 1e-4f)
    }

    @Test
    fun invalidWidthKeepsPageStill() {
        // 宽度非法（0 / 负 / NaN）时位移为 0：宁可不动，也不把页面偏到看不见的地方。
        assertEquals(0f, backOverlayOffset(0.8f, width = 0f), 1e-4f)
        assertEquals(0f, backOverlayOffset(0.8f, width = -320f), 1e-4f)
        assertEquals(0f, backOverlayOffset(0.8f, width = Float.NaN), 1e-4f)
        // 缩放/遮罩与宽度无关，照常按进度算（内容不该因为拿不到宽度就定格）。
        assertEquals(0.968f, backOverlayScale(0.8f), 1e-4f)
    }

    @Test
    fun animationConstantsMatchTheSpec() {
        // 规格值写进常量，避免调用点各自发明 0.9 / 0.3 这类随手数。
        assertEquals(0.96f, BackOverlayRestScale, 1e-6f)
        assertEquals(0.25f, BackOverlayScrimAlpha, 1e-6f)
        // API 33 = Android 13：低于它系统不会派发预测性返回事件。
        assertEquals(33, PredictiveBackSupportedSdk)
        assertFalse(BackOverlayRestScale >= 1f)
        assertTrue(BackOverlayScrimAlpha in 0f..1f)
    }
}
