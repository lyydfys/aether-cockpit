package com.muyuchat.aether.ui

import kotlin.math.roundToInt

/**
 * 悬浮球位置：活动区内的比例坐标（0..1），页面球与系统悬浮窗共用同一套语义。
 *
 * 存比例而不是像素，旋转/分屏/改刷新率后仍落在等效位置；页面球开外显时把自己的
 * 比例交给系统窗，系统窗关外显时再把比例交回来，用户看到的始终是"同一颗球"。
 */
data class BubblePosition(
    val xFraction: Float,
    val yFraction: Float,
) {
    companion object {
        /** 页面球的初始位置：右下角。 */
        val BottomEnd = BubblePosition(xFraction = 1f, yFraction = 1f)
    }
}

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

    /**
     * 拖动中的绝对像素位置：起点像素 + 指针位移，直接夹在活动区内。
     *
     * 拖动全程用它（不经过比例往返），只有松手时才把结果换算成 proportion。
     */
    fun dragOffsetPx(
        startOffsetPx: Int,
        startPointerPx: Float,
        currentPointerPx: Float,
        minPx: Int,
        maxPx: Int,
    ): Int {
        val low = minPx.coerceAtMost(maxPx)
        val high = maxPx.coerceAtLeast(minPx)
        val start = startOffsetPx.coerceIn(low, high)
        if (!startPointerPx.isFinite() || !currentPointerPx.isFinite()) {
            return start
        }
        val delta = currentPointerPx - startPointerPx
        if (!delta.isFinite()) {
            return start
        }
        return (startOffsetPx + delta.roundToInt()).coerceIn(low, high)
    }

    /** 全屏层 hit-test：指针与球矩形的纯浮点判断，命中才消费事件。 */
    fun hitTest(
        pointerX: Float,
        pointerY: Float,
        left: Float,
        top: Float,
        sizePx: Float,
    ): Boolean {
        if (!pointerX.isFinite() || !pointerY.isFinite()) {
            return false
        }
        if (!left.isFinite() || !top.isFinite() || !sizePx.isFinite() || sizePx <= 0f) {
            return false
        }
        return pointerX >= left && pointerX <= left + sizePx &&
            pointerY >= top && pointerY <= top + sizePx
    }

    /**
     * 可选的横向吸附：把比例落到最近的一条边（0 或 1）。
     *
     * **默认不启用**：页面球和系统悬浮窗松手后都停在手指位置（用户要求自由拖动）。
     * 这个纯函数保留给将来可能加的"吸边"开关；比例存储仍是二维的（x + y）。
     */
    fun snapFraction(fraction: Float): Float =
        if (clampFraction(fraction) >= 0.5f) 1f else 0f
}
