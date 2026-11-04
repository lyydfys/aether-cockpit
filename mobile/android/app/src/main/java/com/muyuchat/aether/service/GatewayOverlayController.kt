package com.muyuchat.aether.service

import android.content.Context
import android.content.Intent
import android.graphics.PixelFormat
import android.provider.Settings
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowManager
import android.widget.FrameLayout
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ComposeView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.setViewTreeLifecycleOwner
import androidx.lifecycle.setViewTreeViewModelStoreOwner
import androidx.savedstate.SavedStateRegistry
import androidx.savedstate.SavedStateRegistryController
import androidx.savedstate.SavedStateRegistryOwner
import androidx.savedstate.setViewTreeSavedStateRegistryOwner
import com.muyuchat.aether.MainActivity
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.ui.BubbleMarkDp
import com.muyuchat.aether.ui.GatewaySystemOverlayBubble
import com.muyuchat.aether.ui.OverlayGeometry
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlin.math.hypot
import kotlin.math.roundToInt

/**
 * 页面 → 服务传递的 MCA 颜色。
 *
 * 不在 overlay 里硬编码颜色，也不在 Service 里调用 [com.muyuchat.core.designsystem.McaTheme]
 * （McaTheme 会写 Activity window；overlay 的 context 是 Service，会崩）。页面从当前
 * `MaterialTheme.colorScheme` 取色后随 Intent 带过来，仍是同一套 token。
 */
data class OverlayColors(
    val primary: Int,
    val surface: Int,
    val outline: Int,
) {
    companion object {
        const val EXTRA_PRIMARY = "com.muyuchat.aether.overlay.primary"
        const val EXTRA_SURFACE = "com.muyuchat.aether.overlay.surface"
        const val EXTRA_OUTLINE = "com.muyuchat.aether.overlay.outline"

        fun from(intent: Intent?): OverlayColors? {
            if (intent == null) {
                return null
            }
            if (!intent.hasExtra(EXTRA_PRIMARY) ||
                !intent.hasExtra(EXTRA_SURFACE) ||
                !intent.hasExtra(EXTRA_OUTLINE)
            ) {
                return null
            }
            return OverlayColors(
                primary = intent.getIntExtra(EXTRA_PRIMARY, 0),
                surface = intent.getIntExtra(EXTRA_SURFACE, 0),
                outline = intent.getIntExtra(EXTRA_OUTLINE, 0),
            )
        }
    }
}

/**
 * 系统级悬浮窗的唯一 owner。
 *
 * 由前台 [AetherGatewayService] 调用：服务活着，overlay 就活着；服务被停止/销毁时
 * 一定会 `hide()`。它不是独立进程、也不会在服务死后单独存活——这一点必须如实说明。
 *
 * 拖动过程只用像素：ACTION_MOVE 把 rawX/rawY 增量累加到 params.x/y 并 updateViewLayout，
 * **不在 move 里改 gravity**；只有 ACTION_UP 才把最终像素换算成比例、吸附并切 gravity。
 */
object GatewayOverlayController {
    private val _visible = MutableStateFlow(false)
    val visible: StateFlow<Boolean> = _visible.asStateFlow()

    private var root: FrameLayout? = null
    private var composeView: ComposeView? = null
    private var owner: OverlayViewTreeOwner? = null
    private var windowManager: WindowManager? = null

    /** 进程内记住位置比例；关闭外显再打开回到这里（不跨进程/重启）。 */
    private var xFraction: Float = 1f
    private var yFraction: Float = 0.15f

    fun show(context: Context, colors: OverlayColors): Boolean {
        if (root != null) {
            _visible.value = true
            return true
        }
        if (!Settings.canDrawOverlays(context)) {
            return false
        }
        val manager = context.getSystemService(WindowManager::class.java) ?: return false
        val treeOwner = OverlayViewTreeOwner()
        treeOwner.create()
        val compose = ComposeView(context).apply {
            setViewTreeLifecycleOwner(treeOwner)
            setViewTreeSavedStateRegistryOwner(treeOwner)
            setViewTreeViewModelStoreOwner(treeOwner)
            setContent {
                val status by GatewayServiceController.status.collectAsState()
                GatewaySystemOverlayBubble(
                    running = status.state == GatewayState.RUNNING ||
                        status.state == GatewayState.STARTING,
                    primary = Color(colors.primary),
                    surface = Color(colors.surface),
                    outline = Color(colors.outline),
                    onClick = { openApp(context) },
                )
            }
        }
        val density = context.resources.displayMetrics.density
        val screenWidthPx = context.resources.displayMetrics.widthPixels
        val screenHeightPx = context.resources.displayMetrics.heightPixels
        val markPx = BubbleMarkDp * density
        val edgePx = OverlayGeometry.EDGE_DP * density
        val travelX = OverlayGeometry.travelPx(screenWidthPx, markPx.roundToInt(), edgePx.roundToInt())
        val travelY = OverlayGeometry.travelPx(screenHeightPx, markPx.roundToInt(), edgePx.roundToInt())
        val minWindowX = edgePx.roundToInt()
        val minWindowY = edgePx.roundToInt()
        val maxWindowX = (screenWidthPx - markPx - edgePx).roundToInt().coerceAtLeast(minWindowX)
        val maxWindowY = (screenHeightPx - markPx - edgePx).roundToInt().coerceAtLeast(minWindowY)
        xFraction = OverlayGeometry.clampFraction(xFraction)
        yFraction = OverlayGeometry.clampFraction(yFraction)

        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
                WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
            PixelFormat.TRANSLUCENT,
        )
        var rightAnchored = xFraction >= 0.5f

        fun applyFractions(target: View, updateLayout: Boolean) {
            rightAnchored = xFraction >= 0.5f
            params.gravity = (if (rightAnchored) Gravity.END else Gravity.START) or Gravity.TOP
            val left = edgePx + OverlayGeometry.offsetPx(xFraction, travelX)
            params.x = if (rightAnchored) {
                (screenWidthPx - (left + markPx))
                    .roundToInt()
                    .coerceIn(minWindowX, maxWindowX)
            } else {
                left.roundToInt().coerceIn(minWindowX, maxWindowX)
            }
            params.y = (edgePx + OverlayGeometry.offsetPx(yFraction, travelY))
                .roundToInt()
                .coerceIn(minWindowY, maxWindowY)
            if (updateLayout) {
                runCatching { manager.updateViewLayout(target, params) }
            }
        }

        // 松手才落比例：先由当前像素反推左/上绝对位置，再吸附并重算 gravity。
        fun commitPosition(view: View) {
            val absoluteLeft = if (rightAnchored) {
                screenWidthPx - (params.x + markPx)
            } else {
                params.x.toFloat()
            }
            xFraction = OverlayGeometry.snapFraction(
                OverlayGeometry.fractionForOffset(
                    (absoluteLeft - edgePx).roundToInt(),
                    travelX,
                ),
            )
            yFraction = OverlayGeometry.fractionForOffset(
                (params.y - edgePx).roundToInt(),
                travelY,
            )
            applyFractions(view, updateLayout = true)
        }

        val touchRoot = OverlayTouchRoot(context)
        // Owner 必须挂在 WindowManager 真正 add 的根视图上；只挂在 ComposeView 上时，
        // Compose 从 ComposeView 向上找会先撞到没有 owner 的 OverlayTouchRoot 并崩。
        touchRoot.setViewTreeLifecycleOwner(treeOwner)
        touchRoot.setViewTreeSavedStateRegistryOwner(treeOwner)
        touchRoot.setViewTreeViewModelStoreOwner(treeOwner)
        touchRoot.content = compose
        touchRoot.addView(
            compose,
            FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            ),
        )
        val touchSlop = ViewConfiguration.get(context).scaledTouchSlop.toFloat()
        var downRawX = 0f
        var downRawY = 0f
        var startParamsX = 0
        var startParamsY = 0
        var dragging = false
        touchRoot.setOnTouchListener { view, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    downRawX = event.rawX
                    downRawY = event.rawY
                    startParamsX = params.x
                    startParamsY = params.y
                    dragging = false
                    true
                }
                MotionEvent.ACTION_MOVE -> {
                    val dx = event.rawX - downRawX
                    val dy = event.rawY - downRawY
                    if (!dragging) {
                        val distance = hypot(dx.toDouble(), dy.toDouble()).toFloat()
                        if (distance > touchSlop) {
                            dragging = true
                        }
                    }
                    // 全程像素、全帧跟随；gravity 保持不动，避免重排跳变。
                    val signedDx = if (rightAnchored) -dx else dx
                    params.x = (startParamsX + signedDx).roundToInt()
                        .coerceIn(minWindowX, maxWindowX)
                    params.y = (startParamsY + dy).roundToInt()
                        .coerceIn(minWindowY, maxWindowY)
                    runCatching { manager.updateViewLayout(view, params) }
                    true
                }
                MotionEvent.ACTION_UP -> {
                    if (dragging) {
                        commitPosition(view)
                    } else {
                        // 小位移回弹，仍当作点击。
                        params.x = startParamsX
                        params.y = startParamsY
                        runCatching { manager.updateViewLayout(view, params) }
                        openApp(context)
                    }
                    true
                }
                MotionEvent.ACTION_CANCEL -> {
                    if (dragging) {
                        commitPosition(view)
                    } else {
                        params.x = startParamsX
                        params.y = startParamsY
                        runCatching { manager.updateViewLayout(view, params) }
                    }
                    true
                }
                else -> false
            }
        }
        applyFractions(touchRoot, updateLayout = false)
        return try {
            manager.addView(touchRoot, params)
            treeOwner.start()
            root = touchRoot
            composeView = compose
            owner = treeOwner
            windowManager = manager
            _visible.value = true
            true
        } catch (_: Exception) {
            touchRoot.content = null
            treeOwner.destroy()
            runCatching { compose.disposeComposition() }
            false
        }
    }

    fun hide() {
        val current = root ?: run {
            _visible.value = false
            return
        }
        runCatching { windowManager?.removeViewImmediate(current) }
        runCatching { composeView?.disposeComposition() }
        owner?.destroy()
        root = null
        composeView = null
        owner = null
        windowManager = null
        _visible.value = false
    }

    private fun openApp(context: Context) {
        runCatching {
            context.startActivity(
                Intent(context, MainActivity::class.java).apply {
                    addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
                },
            )
        }
    }
}

/**
 * 把 overlay 的触摸全部拦截到自己的 OnTouchListener：
 * ComposeView 子层不再消费事件，拖动才能稳定生效。
 *
 * 它也是 WindowManager 真正 add 的根视图：Lifecycle/SavedState/ViewModelStore owner
 * 都挂在它身上；detach 时顺手释放 Compose composition。
 */
private class OverlayTouchRoot(context: Context) : FrameLayout(context) {
    var content: ComposeView? = null

    override fun onInterceptTouchEvent(ev: MotionEvent): Boolean = true

    override fun onDetachedFromWindow() {
        super.onDetachedFromWindow()
        val current = content
        content = null
        runCatching { current?.disposeComposition() }
    }
}

/** ComposeView 在 WindowManager 里也需要 Lifecycle/SavedState/ViewModelStore owner。 */
private class OverlayViewTreeOwner : LifecycleOwner, SavedStateRegistryOwner, ViewModelStoreOwner {
    private val registry = LifecycleRegistry(this)
    private val savedState = SavedStateRegistryController.create(this)
    private val store = ViewModelStore()

    override val lifecycle: Lifecycle get() = registry
    override val savedStateRegistry: SavedStateRegistry get() = savedState.savedStateRegistry
    override val viewModelStore: ViewModelStore get() = store

    fun create() {
        savedState.performRestore(null)
        registry.currentState = Lifecycle.State.CREATED
    }

    fun start() {
        registry.currentState = Lifecycle.State.STARTED
    }

    fun destroy() {
        registry.currentState = Lifecycle.State.DESTROYED
        store.clear()
    }
}
