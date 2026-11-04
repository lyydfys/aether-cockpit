package com.muyuchat.aether.ui

import kotlin.math.roundToInt

/**
 * 悬浮球的纯像素/比例几何。
 *
 * 与桌面色板无关，只做旋转/分屏/改尺寸后仍然正确的换算：
 * 存的是 `0..1` 比例，不是像素；窗口变大变小后同一比例落在等效位置。
 * 参考 `E:\MAC` 的 `DshOverlayGeometry`，6 个函数语义保持一一对应。
 */
internal object OverlayGeometry {

    /** 离屏幕边缘的 inset，避免贴状态栏/挖孔。 */
    const val EDGE_DP = 8

    /** 非有限值（NaN/∞）回落到 0，其余夹在 0..1。 */
    fun clampFraction(value: Float): Float =
        if (value.isFinite()) value.coerceIn(0f, 1f) else 0f

    /** 可移动像素带；球比容器还宽时也返回 0，绝不产生负 travel。 */
    fun travelPx(containerPx: Int, markPx: Int, edgePx: Int): Int =
        (containerPx - markPx - edgePx * 2).coerceAtLeast(0)

    /** 比例 → 离边缘的像素；夹在 0..travel。 */
    fun offsetPx(fraction: Float, travelPx: Int): Int {
        if (travelPx <= 0) {
            return 0
        }
        return (clampFraction(fraction) * travelPx)
            .roundToInt()
            .coerceIn(0, travelPx)
    }

    /** 绝对像素 → 比例；travel=0 时固定 0。 */
    fun fractionForOffset(offsetPx: Int, travelPx: Int): Float {
        if (travelPx <= 0) {
            return 0f
        }
        return (offsetPx.toFloat() / travelPx).coerceIn(0f, 1f)
    }

    /**
     * 拖动增量 → 新比例。
     *
     * 越界在这里夹住，窗口层不再做绝对坐标贴边，保证"存的比例"和"看到的位置"永远一致。
     */
    fun fractionAfterDrag(currentFraction: Float, deltaPx: Float, travelPx: Int): Float {
        if (travelPx <= 0 || !deltaPx.isFinite()) {
            return clampFraction(currentFraction)
        }
        val moved = clampFraction(currentFraction) + deltaPx / travelPx
        return if (moved.isFinite()) moved.coerceIn(0f, 1f) else clampFraction(currentFraction)
    }
}
