package com.muyuchat.aether.ui

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.PredictiveBackHandler
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationSpec
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.offset
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.IntOffset
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.math.roundToInt
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.invisibleToUser
import androidx.compose.ui.semantics.semantics

/**
 * 系统级预测性返回（Android 13+）的跟手容器。
 *
 * 覆盖页不再用裸 `BackHandler`：那种写法只拦按键，用户在屏幕边缘滑动时**页面不动**，
 * 松手才"啪"地切走。这里把系统派发的 `progress` 直接驱动位移动画，于是：
 * - 手指挥到哪，页面就被推开多少（progress 0→1）；
 * - 中途松手 → spring 回弹到 0（不是瞬跳）；
 * - 一滑到底 → 先补一段 180ms 滑出（对齐 MCA `closeAnimated()`），再交给调用方的 `onBack()`。
 *
 * MCA 里等价的低层工具是 `com.muyuchat.core.designsystem.SystemBackMotionHandler`
 * （`BackMotion.kt`，用 `OnBackPressedCallback` + `BackEventCompat` 收进度）。
 * 这里不用它，是因为那个版本只报 progress、不负责托管动画；而本组件要把"回弹"和"滑出"
 * 这两段动画自己管住，所以直接用 `androidx.activity.compose.PredictiveBackHandler`
 * （activity-compose 1.9.0+ 提供，本仓 1.13.0）。两者的进度口径一致：都来自 `BackEventCompat.progress`。
 *
 * 与 MCA 对齐的三点（用户点名"MCA 的系统级返回做得很好"）：
 * 1. **进度零滞后**：`snapTo(progress * width)`，不做插值；
 * 2. **键盘优先**：IME 可见时本组件不接管返回（见 [predictiveBackEnabled]），
 *    第一次返回先收键盘，页面不动；
 * 3. **确认后播完整滑出**：180ms `tween` 滑到 1 再 `onBack()`，而不是瞬间消失。
 *
 * API < 33 没有预测事件：退回普通 [BackHandler]，页面不做位移（不闪、不崩）。
 */
internal const val PredictiveBackSupportedSdk = 33

/**
 * 提交返回后等 `open` 变 false 的上限（毫秒）。
 *
 * 正常情况下 `onBack()` 下一两帧就会把 `open` 置 false（`LaunchedEffect(open)` 负责归零）；
 * 超过这个时间还没关，说明调用方忘了改状态，才由兜底逻辑强制归零。**不要**改成"等两帧"——
 * 两帧不足以覆盖慢设备，会在内容还没切走时提前释放底层截图（真机上表现为闪第二下）。
 */
internal const val BackSettleOpenTimeoutMs = 500L

/** 手指滑到底时页面的缩放（1 → 0.96）。 */
internal const val BackOverlayRestScale = 0.96f

/** 手指滑到底时背景遮罩的目标 alpha（0 → 0.25）。 */
internal const val BackOverlayScrimAlpha = 0.25f

/**
 * 确认返回后"滑出"这段动画的时长（ms）。
 *
 * 与 MCA `closeAnimated()` 的 `offsetX.animateTo(width, tween(180))` 同值：这段退场要和
 * 系统把窗口画走的节奏对齐，180ms 是 MCA 实机调过的数；改大像拖泥带水，改小又变回"啪"。
 */
internal const val BackSettleOutMs = 180

/** 返回目标页在手势起点时的缩放（0.96，比正常略小，读起来"在后景"）。 */
internal const val PreviousLayerRestScale = 0.96f

/** 返回目标页在手势起点时的压暗强度（0.32）；进度走到 1 时归零。 */
internal const val PreviousLayerRestDim = 0.32f

/**
 * 没有真实底层时（根页/没有上一页）纯色遮罩的终值（0.40）。
 *
 * 比有真实内容时更暗：底下什么都没有，露出半透明灰只会显得更廉价。
 */
internal const val BackScrimAlphaNoPrevious = 0.40f

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
 * 底层（返回目标页）的缩放：progress=0 → [PreviousLayerRestScale]（略小，显得"在后景"）；
 * progress=1 → 1（手势走到哪，上一页就长到正常大小）。
 *
 * 注意与 [backOverlayScale] 方向相反：那一层是被推走的当前页（1 → 0.92），
 * 这一层是要露出来的上一页（0.96 → 1）。两层都向 1 靠拢会产生"双放大"的廉价感。
 */
internal fun previousBackScale(progress: Float): Float {
    val p = sanitizeBackProgress(progress)
    return PreviousLayerRestScale + (1f - PreviousLayerRestScale) * p
}

/**
 * 底层压暗：progress=0 → [PreviousLayerRestDim]（最暗，表示它还在后景）；
 * progress=1 → 0（完全露出）。
 */
internal fun previousBackDim(progress: Float): Float {
    val p = sanitizeBackProgress(progress)
    return PreviousLayerRestDim * (1f - p)
}

/** 没有真实底层时的纯色遮罩 alpha（progress=0 → 0，progress=1 → [BackScrimAlphaNoPrevious]）。 */
internal fun backScrimAlphaNoPrevious(progress: Float): Float {
    val p = sanitizeBackProgress(progress)
    return BackScrimAlphaNoPrevious * p
}

/**
 * 本组件是否接管这次返回。
 *
 * 与 MCA 的 `enabled = visible && !imeVisible`（`BackMotion.kt` 调用方）同一条规则：
 * **键盘弹着的时候不抢返回**，让系统先把 IME 收掉——否则用户想收键盘，页面却先滑走了，
 * 而且滑到一半露出的是键盘，观感很乱。
 *
 * `imeBlocksBack = false` 留给"输入框是页面主体、键盘不该挡住返回"的极端场景。
 */
internal fun predictiveBackEnabled(
    enabled: Boolean,
    imeVisible: Boolean,
    imeBlocksBack: Boolean = true,
): Boolean = enabled && !(imeBlocksBack && imeVisible)

/**
 * 确认返回后的滑出动画：`tween(180)`，与 MCA `closeAnimated()` 的
 * `offsetX.animateTo(width, tween(180))` 对齐。
 *
 * 为什么不是 spring：这一段是"手势已经完成、页面退场"，必须**可预期地 180ms 走完**；
 * 用 spring 会因为速度/阻尼不同而快慢不一，和系统把窗口画走的节奏打架。
 */
internal fun backSettleOutSpec(): AnimationSpec<Float> = tween(durationMillis = BackSettleOutMs)

/**
 * 跟手容器：只负责"进度 → 位移/缩放/遮罩"、"取消回弹"和"确认滑出"，不关心内容是什么。
 *
 * @param enabled 为 false 时完全不接管返回（例如当前没有覆盖页可关）。
 * @param imeBlocksBack 键盘可见时是否让出返回（默认 true，见 [predictiveBackEnabled]）。
 * @param onBack 手势**滑到底**且滑出动画播完后调用；中途松手不会调。
 * @param previous 返回目标页的真实内容（静态、不可交互）。为空时退回纯色遮罩。
 * @param content 收到实时 progress（0..1），内容本身按需要再决定要不要用。
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun PredictiveBackHost(
    enabled: Boolean,
    onBack: () -> Unit,
    // 注意顺序：`imeBlocksBack` 必须排在 `content` 之前，否则尾随 lambda 会绑到它身上
    // （Kotlin 的尾随 lambda 永远给最后一个参数）。
    imeBlocksBack: Boolean = true,
    /** 覆盖页当前是否仍打开。返回手势提交后由 false 触发"归零"，而不是动画一结束就归零。 */
    open: Boolean = true,
    previous: (@Composable () -> Unit)? = null,
    /**
     * 预测返回手势是否正在收尾（已提交、动画未结束）。
     *
     * 调用方用它来**抑制自己的页面转场**：手势提交后 onBack() 会同时改导航状态，
     * 若外层 AnimatedContent 再播一次滑出，两个右移位移会叠加在同一份内容上，
     * 表现就是"滑出去以后又弹回来一下"（只在转场时长与预测动画错开的设备上明显）。
     * 回调在收尾结束时置回 false。
     */
    onSettlingChanged: ((Boolean) -> Unit)? = null,
    // 传**提供者**而不是值：值在组合阶段读会让整个覆盖页每帧重组（快滑掉帧→看起来像闪）。
    content: @Composable (progress: () -> Float) -> Unit,
) {
    // 用 Animatable 而不是普通 state：取消手势时 animateTo(0f) 天然带 spring 回弹，
    // 直接写 0f 会"瞬间跳回"，那正是要避免的廉价感。
    val motion = remember { Animatable(0f) }
    // 收尾动画必须跑在**独立作用域**里：
    //   - 手势取消时，PredictiveBackHandler 的协程已经处于 cancelled 状态，
    //     在它里面再调 animateTo 会立刻再抛 CancellationException，回弹一帧都不跑，
    //     进度就停在手指松开的位置——真机上表现为「页面卡在半屏」。
    //   - 完成分支同理：确认返回时系统会取消该协程，滑出动效会被吃掉。
    val settleScope = rememberCoroutineScope()
    var settling by remember { mutableStateOf(false) }
    val scrimColor = MaterialTheme.colorScheme.scrim
    // 冻结手势开始那一刻的 previous：提交时 onBack() 会立刻改 anySubpageOpen/snapshot，
    // 若底层跟着变成 null，动画中间会"图片 → 纯灰"闪一下。
    // 只在 progress 归零且动画结束（settling=false）后才释放，避免下一次手势用旧帧。
    var frozenPrevious by remember { mutableStateOf<(@Composable () -> Unit)?>(null) }
    // isImeVisible 是 @Composable 读的窗口状态：键盘收起时本组件自动恢复接管返回。
    val imeVisible = WindowInsets.isImeVisible
    val handleBack = predictiveBackEnabled(enabled, imeVisible, imeBlocksBack)
    // 不接管返回时（根页）保证进度恒为 0：否则残留进度会让遮罩/位移卡在半途。
    LaunchedEffect(handleBack) {
        if (!handleBack) {
            runCatching { motion.stop() }
            motion.snapTo(0f)
            frozenPrevious = null
        }
    }
    val openState = rememberUpdatedState(open)
    LaunchedEffect(open) {
        if (!open) {
            // 内容已经换掉（覆盖页关闭）→ 现在归零不会有一帧旧内容回归。
            motion.snapTo(0f)
            settling = false
            frozenPrevious = null
        }
    }
    val previousNow = previous
    // **关键**：底层只在手势进行中（motion > 0）才组合。
    // 之前只要 enabled 为真就长期挂着上一页 → 两份页面同时绘制 → 重影/透底。
    // 用 derivedStateOf：只在 true/false 翻转时触发一次重组，不随每帧进度变化。
    val gestureActive by remember { derivedStateOf { motion.value > 0f } }
    LaunchedEffect(previousNow) {
        snapshotFlow { motion.value }.collect { value ->
            if (value > 0f && frozenPrevious == null && previousNow != null) {
                frozenPrevious = previousNow
            } else if (value <= 0f && !settling) {
                frozenPrevious = null
            }
        }
    }
    val activePrevious = frozenPrevious ?: previousNow

    // 防抖：防止快速连滑两次叠加动画
    var isAnimating by remember { mutableStateOf(false) }
    
    if (Build.VERSION.SDK_INT >= PredictiveBackSupportedSdk) {
        PredictiveBackHandler(enabled = handleBack && !isAnimating) { events ->
            try {
                // 每来一帧就把进度贴上去：snapTo 不做插值，跟手才不会有滞后感。
                events.collect { event ->
                    motion.snapTo(sanitizeBackProgress(event.progress))
                }
                // 流正常结束 = 手势完成（滑到底）。
                // 先滑出去再 onBack()，否则页面会"啪"地消失；但动效放在独立作用域里跑，
                // 避免被系统在手势提交时取消。
                isAnimating = true
                settling = true
                // 告诉调用方"手势正在收尾"：它应当在 onBack() 后抑制自己的页面转场，
                // 否则两段右移动画叠在同一份内容上（滑出去又弹回来 = 重复/闪烁）。
                onSettlingChanged?.invoke(true)
                settleScope.launch {
                    try {
                        // 快滑时 motion 已经接近 1：从当前值补到 1，不会再"弹回原位再滑出"。
                        motion.animateTo(targetValue = 1f, animationSpec = backSettleOutSpec())
                        onBack()
                        // onBack() 只是改状态，真正换内容要等下一帧。**不要**在这里 snapTo(0f)：
                        // 归零只由 LaunchedEffect(open) 做。这里若抢先归零，gestureActive 会从
                        // true 翻 false，底层截图在旧内容还没消失时就撤掉 → 真机上就是"闪第二下"。
                        withTimeoutOrNull(BackSettleOpenTimeoutMs) {
                            snapshotFlow { openState.value }.first { !it }
                        }
                    } finally {
                        settling = false
                        isAnimating = false
                        // 兜底只在**调用方始终没把 open 置 false**时触发（动画已滑到 1f，
                        // 页面却不走 → 会一直停在"滑出去"的空白态）。超时后再归零，
                        // 并且先 stop 再 snapTo，避免和 LaunchedEffect(open) 抢同一个 Animatable。
                        if (openState.value) {
                            runCatching { motion.stop() }
                            runCatching { motion.snapTo(0f) }
                        }
                        onSettlingChanged?.invoke(false)
                    }
                }
            } catch (e: CancellationException) {
                // 中途松手：回到原位。同样不能在被取消的协程里做动画。
                settling = true
                onSettlingChanged?.invoke(true)
                settleScope.launch {
                    try {
                        motion.animateTo(
                            targetValue = 0f,
                            animationSpec = spring(
                                dampingRatio = Spring.DampingRatioNoBouncy,
                                stiffness = Spring.StiffnessMediumLow,
                            ),
                        )
                    } finally {
                        settling = false
                        isAnimating = false
                        onSettlingChanged?.invoke(false)
                    }
                }
                throw e
            }
        }
    } else {
        // API < 33：没有预测事件可跟手，退回普通返回键（行为与改造前一致）。
        // 防抖用挂起的 scope，不能在 onBack 回调里直接调用 @Composable。
        BackHandler(enabled = handleBack && !isAnimating, onBack = {
            if (!isAnimating) {
                isAnimating = true
                onBack()
                settleScope.launch {
                    kotlinx.coroutines.delay(300)
                    isAnimating = false
                }
            }
        })
    }

    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        val widthPx = with(LocalDensity.current) { maxWidth.toPx() }
        // 底层 1：返回目标页的**真实内容**（若调用方给了）。静态、不接收点击，
        // 只做 0.96→1.0 的轻微放大 + 压暗回收，读起来"在后景里"但不抢焦点。
        if (gestureActive && activePrevious != null) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer {
                        val p = motion.value
                        val scale = previousBackScale(p)
                        scaleX = scale
                        scaleY = scale
                    }
                    // 手势期间底层不能吃掉点击/滑动：整块吞掉指针事件。
                    .pointerInput(Unit) {
                        awaitPointerEventScope {
                            while (true) {
                                awaitPointerEvent().changes.forEach { it.consume() }
                            }
                        }
                    }
                    .semantics { invisibleToUser() },
            ) {
                activePrevious()
            }
        }
        // 底层 2：遮罩。没给 previous 时它是唯一底层（原有行为）；
        // 给了 previous 时它仍叠在上面，用于统一压暗、制造层次。
        Box(
            modifier = Modifier
                .fillMaxSize()
                .drawBehind {
                    drawRect(
                        scrimColor.copy(
                            // 没接管返回（根页）时全透明：根页不允许出现任何灰块。
                            alpha = if (!gestureActive) {
                                0f
                            } else if (activePrevious == null) {
                                backScrimAlphaNoPrevious(motion.value)
                            } else {
                                previousBackDim(motion.value)
                            },
                        ),
                    )
                },
        )
        Box(
            modifier = Modifier
                .fillMaxSize()
                // offset / graphicsLayer 的 lambda 在布局/绘制阶段求值，
                // 这里读 motion.value 属于「延迟读取」，不会触发重组。
                .offset { IntOffset(backOverlayOffset(motion.value, widthPx).roundToInt(), 0) }
                .graphicsLayer {
                    val scale = backOverlayScale(motion.value)
                    scaleX = scale
                    scaleY = scale
                    // 以左缘为锚点缩放：页面被推向右时右边缘不会缩成一条。
                    transformOrigin = TransformOrigin(0f, 0.5f)
                },
        ) {
            content { motion.value }
        }
    }
}
