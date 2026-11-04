package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 悬浮球几何的纯函数合同（参考 DSH 的 DshOverlayGeometry）：
 * 比例持久化、旋转/尺寸变化、越界夹取、NaN 兜底都在这里钉住，不依赖设备。
 */
class OverlayGeometryTest {
    @Test
    fun clampFractionCollapsesNonFiniteAndClampsRange() {
        assertEquals(0f, OverlayGeometry.clampFraction(Float.NaN))
        assertEquals(0f, OverlayGeometry.clampFraction(Float.POSITIVE_INFINITY))
        assertEquals(0f, OverlayGeometry.clampFraction(Float.NEGATIVE_INFINITY))
        assertEquals(0f, OverlayGeometry.clampFraction(-0.2f))
        assertEquals(1f, OverlayGeometry.clampFraction(1.2f))
        assertEquals(0.42f, OverlayGeometry.clampFraction(0.42f))
    }

    @Test
    fun travelNeverGoesNegativeEvenWhenMarkIsWiderThanContainer() {
        assertEquals(100, OverlayGeometry.travelPx(containerPx = 300, markPx = 100, edgePx = 50))
        assertEquals(0, OverlayGeometry.travelPx(containerPx = 100, markPx = 100, edgePx = 0))
        assertEquals(0, OverlayGeometry.travelPx(containerPx = 40, markPx = 100, edgePx = 8))
    }

    @Test
    fun offsetIsFractionTimesTravelAndClamped() {
        assertEquals(0, OverlayGeometry.offsetPx(0f, 800))
        assertEquals(400, OverlayGeometry.offsetPx(0.5f, 800))
        assertEquals(800, OverlayGeometry.offsetPx(1f, 800))
        assertEquals(800, OverlayGeometry.offsetPx(2f, 800))
        assertEquals(0, OverlayGeometry.offsetPx(Float.NaN, 800))
        assertEquals(0, OverlayGeometry.offsetPx(0.5f, 0))
    }

    @Test
    fun fractionForOffsetHandlesZeroTravelAndOutOfRange() {
        assertEquals(0f, OverlayGeometry.fractionForOffset(100, 0))
        assertEquals(0f, OverlayGeometry.fractionForOffset(-10, 800))
        assertEquals(0.75f, OverlayGeometry.fractionForOffset(600, 800))
        assertEquals(1f, OverlayGeometry.fractionForOffset(900, 800))
    }

    @Test
    fun dragDeltaUpdatesFractionAndClampsAtBothEnds() {
        val start = 0.5f
        assertEquals(0.625f, OverlayGeometry.fractionAfterDrag(start, 100f, 800), 0.0001f)
        assertEquals(0f, OverlayGeometry.fractionAfterDrag(0.1f, -500f, 800), 0.0001f)
        assertEquals(1f, OverlayGeometry.fractionAfterDrag(0.9f, 500f, 800), 0.0001f)
        assertEquals(start, OverlayGeometry.fractionAfterDrag(start, Float.NaN, 800), 0.0001f)
        assertEquals(start, OverlayGeometry.fractionAfterDrag(start, 100f, 0), 0.0001f)
    }

    @Test
    fun sameFractionSurvivesRotationAndDisplayResize() {
        // 竖屏 800px travel 的 0.75 → 600px；横屏 travel 变 500 → 375px，比例不变。
        val portrait = OverlayGeometry.fractionForOffset(600, 800)
        val landscapePx = OverlayGeometry.offsetPx(portrait, 500)
        assertEquals(375, landscapePx)
        assertEquals(portrait, OverlayGeometry.fractionForOffset(landscapePx, 500), 0.0001f)
    }

    @Test
    fun dragOffsetUsesRawPixelDeltaAndClamps() {
        assertEquals(140, OverlayGeometry.dragOffsetPx(100, 50f, 90f, 16, 900))
        assertEquals(80, OverlayGeometry.dragOffsetPx(100, 50f, 30f, 16, 900))
        assertEquals(16, OverlayGeometry.dragOffsetPx(100, 50f, -500f, 16, 900))
        assertEquals(900, OverlayGeometry.dragOffsetPx(100, 50f, 900f, 16, 900))
        assertEquals(100, OverlayGeometry.dragOffsetPx(100, Float.NaN, 90f, 16, 900))
        assertEquals(100, OverlayGeometry.dragOffsetPx(100, 50f, Float.NaN, 16, 900))
        // 起点本身越界时也先夹到活动区。
        assertEquals(16, OverlayGeometry.dragOffsetPx(0, 50f, 50f, 16, 900))
    }

    @Test
    fun snapFractionGoesToNearestHorizontalEdge() {
        assertEquals(0f, OverlayGeometry.snapFraction(0.49f))
        assertEquals(1f, OverlayGeometry.snapFraction(0.5f))
        assertEquals(1f, OverlayGeometry.snapFraction(0.9f))
        assertEquals(0f, OverlayGeometry.snapFraction(-1f))
        assertEquals(1f, OverlayGeometry.snapFraction(2f))
        assertEquals(0f, OverlayGeometry.snapFraction(Float.NaN))
    }

    @Test
    fun edgeInsetIsEightDp() {
        assertEquals(8, OverlayGeometry.EDGE_DP)
        assertTrue(OverlayGeometry.EDGE_DP > 0)
    }
}
