package com.muyuchat.aether.ui

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.PredictiveBackHandler
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.IntOffset
import kotlinx.coroutines.CancellationException
import kotlin.math.roundToInt

/**
 * 系统级预测性返回（Android 13+）的跟手容器。
 *
 * 覆盖页不再用裸 `BackHandler`：那种写法只拦按键，用户在屏幕边缘滑动时**页面不动**，
 * 松手才"啪"地切走。这里把系统派发的 `progress` 直接驱动位移动画，于是：
 * - 手指挥到哪，页面就被推开多少（progress 0→1）；
 * - 中途松手 → spring 回弹到 0（不是瞬跳）；
 * - 一滑到底 → 交给调用方的 `onBack()`，系统接着把页面画走。
 *
 * MCA 里等价的低层工具是 `com.muyuchat.core.designsystem.SystemBackMotionHandler`
 * （`BackMotion.kt`，用 `OnBackPressedCallback` + `BackEventCompat` 收进度）。
 * 这里不用它，是因为那个版本只报 progress、不负责托管动画；而本组件要把"回弹"这件事
 * 自己管住，所以直接用 `androidx.activity.compose.PredictiveBackHandler`（activity-compose
 * 1.9.0+ 提供，本仓 1.13.0）。两者的进度口径一致：都来自系统的 BackEventCompat.progress。
 *
 * API < 33 没有预测事件：退回普通 [BackHandler]，页面不做位移（不闪、不崩）。
 */
internal const val PredictiveBackSupportedSdk = 33

/** 手指滑到底时页面的缩放（1 → 0.96）。 */
internal const val BackOverlayRestScale = 0.96f

/** 手指滑到底时背景遮罩的目标 alpha（0 → 0.25）。 */
internal const val BackOverlayScrimAlpha = 0.25f

/**
 * 把系统给的 progress 夹到 0..1。
 *
 * NaN 一律当 0：`Float.coerceIn` 对 NaN 会返回 NaN 本身，那样位移/缩放会变成脏值，
 * 页面可能整块甩出屏幕。Inf 交给 coerceIn 收敛到端点。
 */
internal fun sanitizeBackProgress(progress: Float): Float =
    if (progress.isNaN()) 0f else progress.coerceIn(0f, 1f)

/**
 * 页面横向位移（px）：progress=0 → 0；progress=1 → 整个宽度。
 *
 * 与系统"页面被推走一整屏"的口径一致；宽度非法（0/负/NaN）时返回 0，
 * 宁可不动，也不把页面偏到看不见的地方。
 */
internal fun backOverlayOffset(progress: Float, width: Float): Float {
    val p = sanitizeBackProgress(progress)
    val safeWidth = if (width.isNaN() || width <= 0f) 0f else width
    return p * safeWidth
}

/** 页面缩放：progress=0 → 1；progress=1 → [BackOverlayRestScale]（线性，中途不反弹）。 */
internal fun backOverlayScale(progress: Float): Float {
    val p = sanitizeBackProgress(progress)
    return 1f - (1f - BackOverlayRestScale) * p
}

/** 背景遮罩 alpha：progress=0 → 0（全透明）；progress=1 → [BackOverlayScrimAlpha]。 */
internal fun backScrimAlpha(progress: Float): Float {
    val p = sanitizeBackProgress(progress)
    return BackOverlayScrimAlpha * p
}

/**
 * 跟手容器：只负责"进度 → 位移/缩放/遮罩"和"取消回弹"，不关心内容是什么。
 *
 * @param enabled 为 false 时完全不接管返回（例如当前没有覆盖页可关）。
 * @param onBack 手势**滑到底**时调用；中途松手不会调。
 * @param content 收到实时 progress（0..1），内容本身按需要再决定要不要用。
 */
@Composable
internal fun PredictiveBackHost(
    enabled: Boolean,
    onBack: () -> Unit,
    content: @Composable (progress: Float) -> Unit,
) {
    // 用 Animatable 而不是普通 state：取消手势时 animateTo(0f) 天然带 spring 回弹，
    // 直接写 0f 会"瞬间跳回"，那正是要避免的廉价感。
    val motion = remember { Animatable(0f) }
    val scrimColor = MaterialTheme.colorScheme.scrim

    if (Build.VERSION.SDK_INT >= PredictiveBackSupportedSdk) {
        PredictiveBackHandler(enabled = enabled) { events ->
            try {
                // 每来一帧就把进度贴上去：snapTo 不做插值，跟手才不会有滞后感。
                events.collect { event ->
                    motion.snapTo(sanitizeBackProgress(event.progress))
                }
                // 流正常结束 = 手势完成（滑到底）：交给调用方收尾。
                onBack()
            } catch (e: CancellationException) {
                // 中途松手：回弹到 0 再让异常继续冒泡（协程取消语义不能吞）。
                motion.animateTo(
                    targetValue = 0f,
                    animationSpec = spring(
                        dampingRatio = Spring.DampingRatioNoBouncy,
                        stiffness = Spring.StiffnessMediumLow,
                    ),
                )
                throw e
            }
        }
    } else {
        // API < 33：没有预测事件可跟手，退回普通返回键（行为与改造前一致）。
        BackHandler(enabled = enabled, onBack = onBack)
    }

    val progress = motion.value
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val widthPx = with(LocalDensity.current) { maxWidth.toPx() }
        // 遮罩在最底层：被推走的页面后面露出的是它，而不是上一页的真实内容。
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(scrimColor.copy(alpha = backScrimAlpha(progress))),
        )
        Box(
            modifier = Modifier
                .fillMaxSize()
                .offset { IntOffset(backOverlayOffset(progress, widthPx).roundToInt(), 0) }
                .graphicsLayer {
                    val scale = backOverlayScale(progress)
                    scaleX = scale
                    scaleY = scale
                    // 以左缘为锚点缩放：页面被推向右时右边缘不会缩成一条。
                    transformOrigin = TransformOrigin(0f, 0.5f)
                },
        ) {
            content(progress)
        }
    }
}
