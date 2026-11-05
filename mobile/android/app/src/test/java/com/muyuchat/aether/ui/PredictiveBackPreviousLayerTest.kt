package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 预测性返回的**底层**（返回目标页）动效曲线。
 *
 * 设计意图：当前页被推走（[backOverlayScale] 1 → 0.92），
 * 底层从 0.96 → 1.0 长到正常大小，同时压暗从 0.32 收到 0——
 * 两层都向 1 靠拢会产生"双放大"的廉价感，所以两者方向必须相反。
 */
class PredictiveBackPreviousLayerTest {
    @Test
    fun previousLayerGrowsFromRestToFullSize() {
        assertEquals(PreviousLayerRestScale, previousBackScale(0f), 0.0001f)
        assertEquals(1f, previousBackScale(1f), 0.0001f)
        // 单调递增，且始终不大于 1（不会放大过头）
        var last = previousBackScale(0f)
        for (step in 1..10) {
            val value = previousBackScale(step / 10f)
            assertTrue("进度增大时底层应单调变大", value >= last)
            assertTrue("底层不得超过原尺寸", value <= 1f)
            last = value
        }
    }

    @Test
    fun previousLayerSitsBehindTheOutgoingPage() {
        // 手势起点：当前页还是原尺寸 1.0，底层略小 0.96 → 层次是"前一页在后景"
        assertTrue("底层起点应小于当前页起点", previousBackScale(0f) < backOverlayScale(0f))
        // 手势走完：当前页缩到 0.96 并被推走，底层长到 1.0 → 角色对调
        assertEquals(backOverlayScale(1f), previousBackScale(0f), 0.0001f)
        assertEquals(1f, previousBackScale(1f), 0.0001f)
    }

    @Test
    fun previousLayerDimFadesOut() {
        assertEquals(PreviousLayerRestDim, previousBackDim(0f), 0.0001f)
        assertEquals(0f, previousBackDim(1f), 0.0001f)
        assertTrue(
            "压暗随进度单调递减",
            previousBackDim(0.25f) > previousBackDim(0.75f),
        )
        // 压暗不能超过 1，也不能是负数
        assertTrue(previousBackDim(0f) in 0f..1f)
        assertTrue(previousBackDim(1f) >= 0f)
    }

    @Test
    fun outOfRangeProgressIsSanitized() {
        // 系统的 progress 偶尔会有负值/超过 1 的抖动，曲线必须夹住而不是失控
        assertEquals(PreviousLayerRestScale, previousBackScale(-0.5f), 0.0001f)
        assertEquals(1f, previousBackScale(1.5f), 0.0001f)
        assertEquals(PreviousLayerRestDim, previousBackDim(-1f), 0.0001f)
        assertEquals(0f, previousBackDim(2f), 0.0001f)
    }

    @Test
    fun plainScrimFallbackIsDarkerThanTheRealPreviousLayer() {
        // 没有真实底层（根页/无上一页）时用纯色遮罩，且必须比"有真实内容"时更暗：
        // 底下什么都没有，露出半透明灰只会更廉价。
        assertEquals(0f, backScrimAlphaNoPrevious(0f), 0.0001f)
        assertEquals(BackScrimAlphaNoPrevious, backScrimAlphaNoPrevious(1f), 0.0001f)
        assertTrue(
            "纯色兜底应比真实底层起点更暗",
            backScrimAlphaNoPrevious(1f) > previousBackDim(0f),
        )
        assertTrue("不能暗到全黑", backScrimAlphaNoPrevious(1f) < 1f)
    }
}
