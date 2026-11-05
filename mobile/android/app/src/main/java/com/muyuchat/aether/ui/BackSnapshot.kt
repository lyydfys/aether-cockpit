package com.muyuchat.aether.ui

import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap

/**
 * 返回手势的底层快照：**只保留最近一帧**。
 *
 * 为什么用抓帧而不是"再渲染一遍上一页"：上一页需要的状态分散在各自分支内部
 * （设置页 20+ 个 `collectAsStateWithLifecycle`），提升它们要动整个 `GatewayHome` 的结构；
 * 而第二份组合会重复 `BackHandler` / `LaunchedEffect` / 首帧测量——返回手势是高频路径，不值得赌。
 *
 * 内存：1440×3200 的 ARGB_8888 约 18MB。这里**始终只持有一张**：新的抓帧先让旧帧可回收，
 * [clear] 在退出覆盖层时把引用置空，靠 GC 释放，不累积。
 */
internal class BackSnapshot {
    private companion object {
        /** 抓帧最大宽度（px）。原图 1440 宽时降到 720，绘制量约 1/4。 */
        const val MAX_WIDTH = 720
    }
    var image: ImageBitmap? by mutableStateOf(null)
        private set

    /** 抓帧进行中标志：主线程 `view.draw()` 是同步的，重入会直接叠出多倍卡顿。 */
    private var capturing = false

    /**
     * 抓帧失败（视图未附着 / OOM / 重入）→ 返回 false，调用方退回纯色遮罩，绝不崩。
     *
     * **降采样**：手机屏 1440×3200 的 ARGB_8888 软件绘制一次要 30–80ms（真机 `dumpsys gfxinfo`
     * 里能直接看到 150–450ms 的尖峰）。这里把宽度上限压到 [MAX_WIDTH]（720），
     * 绘制量降到约 1/4，底层只是"后景里的静态图"，肉眼几乎无差别。
     */
    fun capture(root: View?): Boolean {
        if (root == null || capturing) {
            return false
        }
        val width = root.width
        val height = root.height
        if (width <= 0 || height <= 0) {
            return false
        }
        capturing = true
        return try {
            runCatching {
                val scale = if (width > MAX_WIDTH) MAX_WIDTH.toFloat() / width else 1f
                val targetW = (width * scale).toInt().coerceAtLeast(1)
                val targetH = (height * scale).toInt().coerceAtLeast(1)
                val bitmap = Bitmap.createBitmap(targetW, targetH, Bitmap.Config.ARGB_8888)
                try {
                    val canvas = Canvas(bitmap)
                    canvas.scale(scale, scale)
                    root.draw(canvas)
                    image = bitmap.asImageBitmap()
                    true
                } catch (t: Throwable) {
                    bitmap.recycle()
                    throw t
                }
            }.getOrElse {
                // 已经有一张旧帧时不要因为这次失败把它清掉：宁可用旧帧，也不要突然变纯灰。
                false
            }
        } finally {
            capturing = false
        }
    }

    fun clear() {
        image = null
    }
}
