package com.muyuchat.aether.service

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.PixelFormat
import android.os.Build
import android.provider.Settings
import android.util.DisplayMetrics
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.widget.FrameLayout
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
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
import com.muyuchat.aether.runtime.AppForegroundTracker
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.ui.BubbleMarkDp
import com.muyuchat.aether.ui.BubblePosition
import com.muyuchat.aether.ui.GatewayOverlayWindowContent
import com.muyuchat.aether.ui.OverlayGeometry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlin.math.hypot
import kotlin.math.roundToInt

/**
 * 页面 → 服务传递的 MCA 颜色。
 *
 * 不在 overlay 里硬编码颜色，也不在 Service 里调用 [com.muyuchat.core.designsystem.McaTheme]
 * （McaTheme 会写 Activity window；overlay 的 context 是 Service，会崩）。页面从当前
 * `MaterialTheme.colorScheme` 取色后随 Intent 带过来，仍是同一套 token。
 *
 * 这里把功能栏会用到的角色**一次带全**：系统窗里的 MaterialTheme 由这些角色 copy 出来
 * （见 `GatewayOverlayWindowContent` 里的 `OverlayMcaTheme`），所以按钮、描边、文字
 * 在应用内外是同一套颜色，而不是 Material3 默认的薰衣草灰。
 */
data class OverlayColors(
    val primary: Int,
    val onPrimary: Int,
    val surface: Int,
    val onSurface: Int,
    val surfaceVariant: Int,
    val onSurfaceVariant: Int,
    val outline: Int,
    val outlineVariant: Int,
    val error: Int,
    val onError: Int,
    val background: Int,
) {
    companion object {
        const val EXTRA_PRIMARY = "com.muyuchat.aether.overlay.primary"
        const val EXTRA_ON_PRIMARY = "com.muyuchat.aether.overlay.onPrimary"
        const val EXTRA_SURFACE = "com.muyuchat.aether.overlay.surface"
        const val EXTRA_ON_SURFACE = "com.muyuchat.aether.overlay.onSurface"
        const val EXTRA_SURFACE_VARIANT = "com.muyuchat.aether.overlay.surfaceVariant"
        const val EXTRA_ON_SURFACE_VARIANT = "com.muyuchat.aether.overlay.onSurfaceVariant"
        const val EXTRA_OUTLINE = "com.muyuchat.aether.overlay.outline"
        const val EXTRA_OUTLINE_VARIANT = "com.muyuchat.aether.overlay.outlineVariant"
        const val EXTRA_ERROR = "com.muyuchat.aether.overlay.error"
        const val EXTRA_ON_ERROR = "com.muyuchat.aether.overlay.onError"
        const val EXTRA_BACKGROUND = "com.muyuchat.aether.overlay.background"

        private val REQUIRED_EXTRAS = arrayOf(
            EXTRA_PRIMARY,
            EXTRA_ON_PRIMARY,
            EXTRA_SURFACE,
            EXTRA_ON_SURFACE,
            EXTRA_SURFACE_VARIANT,
            EXTRA_ON_SURFACE_VARIANT,
            EXTRA_OUTLINE,
            EXTRA_OUTLINE_VARIANT,
            EXTRA_ERROR,
            EXTRA_ON_ERROR,
            EXTRA_BACKGROUND,
        )

        fun from(intent: Intent?): OverlayColors? {
            if (intent == null) {
                return null
            }
            if (REQUIRED_EXTRAS.any { !intent.hasExtra(it) }) {
                return null
            }
            return OverlayColors(
                primary = intent.getIntExtra(EXTRA_PRIMARY, 0),
                onPrimary = intent.getIntExtra(EXTRA_ON_PRIMARY, 0),
                surface = intent.getIntExtra(EXTRA_SURFACE, 0),
                onSurface = intent.getIntExtra(EXTRA_ON_SURFACE, 0),
                surfaceVariant = intent.getIntExtra(EXTRA_SURFACE_VARIANT, 0),
                onSurfaceVariant = intent.getIntExtra(EXTRA_ON_SURFACE_VARIANT, 0),
                outline = intent.getIntExtra(EXTRA_OUTLINE, 0),
                outlineVariant = intent.getIntExtra(EXTRA_OUTLINE_VARIANT, 0),
                error = intent.getIntExtra(EXTRA_ERROR, 0),
                onError = intent.getIntExtra(EXTRA_ON_ERROR, 0),
                background = intent.getIntExtra(EXTRA_BACKGROUND, 0),
            )
        }
    }
}

/**
 * 系统窗的展开状态。
 *
 * [above] 只是"菜单排到球上方还是下方"，真正的窗口位置由 gravity 锚定：
 * 菜单在上时窗口底边贴球底（[android.view.Gravity.BOTTOM]），窗口向上生长，
 * 球在屏幕上完全不动；不需要预先量菜单高度。
 */
internal data class OverlayMenuState(
    val open: Boolean = false,
    val above: Boolean = true,
    val alignEnd: Boolean = true,
)

/** 系统栏安全边距（px）；只用于球的活动区，避免压住状态栏/导航条。 */
private data class EdgeInsets(
    val left: Int,
    val right: Int,
    val top: Int,
    val bottom: Int,
)

/**
 * 系统级悬浮窗的唯一 owner。
 *
 * 一颗球 +（展开时的）功能栏住在**同一个** `TYPE_APPLICATION_OVERLAY` 窗口里：应用内外
 * 都只有这一个悬浮窗，不会出现"页面球 + 系统球"两份。
 *
 * 由前台 [AetherGatewayService] 调用：服务活着，overlay 就活着；服务被停止/销毁时
 * 一定会 `hide()`。它不是独立进程、也不会在服务死后单独存活——这一点必须如实说明。
 *
 * 拖动过程只用像素：ACTION_MOVE 把 rawX/rawY 增量累加到 params.x/y 并 updateViewLayout，
 * **不在 move 里改 gravity**；只有 ACTION_UP 才把最终像素换算成比例并切 gravity。
 * 松手后**不吸附左右边缘**：球停在手指松开的位置（二维自由位置），只做边界钳制。
 * 球是窗口里唯一可拖的部分：手指落在功能栏上时完全不拦截，交给 Compose 的按钮处理。
 */
object GatewayOverlayController {
    /** 页面球把当前比例位置交给系统窗（开外显时随 Intent 带过来）。 */
    const val EXTRA_OVERLAY_X = "com.muyuchat.aether.overlay.position.x"
    const val EXTRA_OVERLAY_Y = "com.muyuchat.aether.overlay.position.y"

    private val _visible = MutableStateFlow(false)
    val visible: StateFlow<Boolean> = _visible.asStateFlow()

    /** 最后一次落定的比例位置；页面球在关外显后用它对回"同一颗球"。 */
    private val _position = MutableStateFlow<BubblePosition?>(null)
    val position: StateFlow<BubblePosition?> = _position.asStateFlow()

    private val _menu = MutableStateFlow(OverlayMenuState())
    private val _copied = MutableStateFlow(false)
    private val _copyFailed = MutableStateFlow(false)
    private val copyScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    private var root: OverlayTouchRoot? = null
    private var composeView: ComposeView? = null
    private var owner: OverlayViewTreeOwner? = null
    private var windowManager: WindowManager? = null
    private var params: WindowManager.LayoutParams? = null

    private var screenWidthPx = 0
    private var screenHeightPx = 0
    private var markPx = 0
    private var edgePx = 0
    private var travelX = 0
    private var travelY = 0

    /**
     * 系统栏安全边距（px）：球的活动区要扣掉这些，保证球不会压住状态栏文字/导航条。
     *
     * 左右取较大值（竖屏时通常为 0，横屏接缝导航条可能只在一侧）；顶部=状态栏，
     * 底部=导航条。
     */
    private var insetSidePx = 0
    private var insetTopPx = 0
    private var insetBottomPx = 0

    /** 进程内记住位置比例；关闭外显再打开回到这里（不跨进程/重启）。 */
    private var xFraction = 1f
    private var yFraction = 0.15f

    fun show(
        context: Context,
        colors: OverlayColors,
        initialPosition: BubblePosition? = null,
    ): Boolean {
        if (root != null) {
            _visible.value = true
            return true
        }
        if (!Settings.canDrawOverlays(context)) {
            return false
        }
        val manager = context.getSystemService(WindowManager::class.java) ?: return false
        val metrics = context.resources.displayMetrics
        // 窗口带 FLAG_LAYOUT_IN_SCREEN，坐标空间是**物理屏幕**；尺寸必须取整块屏幕，
        // 否则 BOTTOM 锚定会与导航栏高度差出几十像素，功能栏一展开球就上下跳。
        val screen = displayBounds(manager)
        screenWidthPx = screen.first
        screenHeightPx = screen.second
        markPx = (BubbleMarkDp * metrics.density).roundToInt().coerceAtLeast(1)
        edgePx = (OverlayGeometry.EDGE_DP * metrics.density).roundToInt()
        // 活动区 = 物理屏幕 - 状态栏/导航条 - 球 - 边缘留白。四边都不越界，也不遮系统栏文字。
        val insets = displayInsets(context, manager)
        insetSidePx = maxOf(insets.left, insets.right)
        insetTopPx = insets.top
        insetBottomPx = insets.bottom
        travelX = OverlayGeometry.travelPx(screenWidthPx - insetSidePx * 2, markPx, edgePx)
        travelY = OverlayGeometry.travelPx(
            screenHeightPx - insetTopPx - insetBottomPx,
            markPx,
            edgePx,
        )
        if (initialPosition != null) {
            // 从页面球接管过来的位置：同一颗球，不跳到默认角落。
            xFraction = OverlayGeometry.clampFraction(initialPosition.xFraction)
            yFraction = OverlayGeometry.clampFraction(initialPosition.yFraction)
        }
        xFraction = OverlayGeometry.clampFraction(xFraction)
        yFraction = OverlayGeometry.clampFraction(yFraction)
        _menu.value = OverlayMenuState()
        _copied.value = false
        _copyFailed.value = false

        val treeOwner = OverlayViewTreeOwner()
        treeOwner.create()
        val compose = ComposeView(context).apply {
            setViewTreeLifecycleOwner(treeOwner)
            setViewTreeSavedStateRegistryOwner(treeOwner)
            setViewTreeViewModelStoreOwner(treeOwner)
            setContent {
                val status by GatewayServiceController.status.collectAsState()
                val menu by _menu.collectAsState()
                val inApp by AppForegroundTracker.inForeground.collectAsState()
                val copied by _copied.collectAsState()
                val copyFailed by _copyFailed.collectAsState()
                GatewayOverlayWindowContent(
                    status = status,
                    colors = colors,
                    menuOpen = menu.open,
                    menuAbove = menu.above,
                    alignEnd = menu.alignEnd,
                    inApp = inApp,
                    copied = copied,
                    copyFailed = copyFailed,
                    onBallTap = { toggleMenu() },
                    onPrimaryAction = { requestPrimaryAction(context) },
                    onCopy = { copyAddress(context) },
                    onOverlayAction = {
                        if (AppForegroundTracker.inForeground.value) {
                            // 应用内：保持原样，关掉外显，页面球回来接管。
                            hideOverlay(context)
                        } else {
                            // 应用外：语义是「返回 app」，把 App 拉回前台。
                            closeMenu()
                            openApp(context)
                        }
                    },
                )
            }
        }

        val layoutParams = WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
                WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
                // 点窗口外面收起功能栏（否则菜单会一直浮着）。
                WindowManager.LayoutParams.FLAG_WATCH_OUTSIDE_TOUCH or
                // 用物理屏幕坐标：BOTTOM/TOP 两种锚定都按整屏算，不受系统栏裁切影响，
                // 功能栏展开时球不会因为底部导航栏高度而上下跳。
                WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN,
            PixelFormat.TRANSLUCENT,
        )
        params = layoutParams

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
        touchRoot.onOutsideTouch = { closeMenu() }
        // 只有落在球上的手势才归拖动/点开功能栏；落在功能栏上的一律放给 Compose 按钮。
        touchRoot.shouldInterceptDown = { x, y -> isInsideBall(touchRoot, x, y) }

        val touchSlop = ViewConfiguration.get(context).scaledTouchSlop.toFloat()
        // 自由拖动：活动区四边都留出「系统栏 + 8dp + 球尺寸」，球不会越界，也不压状态栏/导航条。
        val minWindowX = insetSidePx + edgePx
        val minWindowY = insetTopPx + edgePx
        val maxWindowX = (screenWidthPx - insetSidePx - markPx - edgePx).coerceAtLeast(minWindowX)
        val maxWindowY = (screenHeightPx - insetBottomPx - markPx - edgePx).coerceAtLeast(minWindowY)
        var downRawX = 0f
        var downRawY = 0f
        var startParamsX = 0
        var startParamsY = 0
        var dragging = false
        touchRoot.setOnTouchListener { view, event ->
            val current = params
            if (current == null || !touchRoot.intercepting) {
                return@setOnTouchListener false
            }
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    downRawX = event.rawX
                    downRawY = event.rawY
                    startParamsX = current.x
                    startParamsY = current.y
                    dragging = false
                    true
                }
                MotionEvent.ACTION_MOVE -> {
                    if (!dragging) {
                        val distance = hypot(
                            (event.rawX - downRawX).toDouble(),
                            (event.rawY - downRawY).toDouble(),
                        ).toFloat()
                        if (distance <= touchSlop) {
                            return@setOnTouchListener true
                        }
                        if (_menu.value.open) {
                            // 菜单展开时窗口是 BOTTOM 锚定的（像素语义不同）：先收菜单、
                            // 恢复 TOP 锚定，再以当前手指位置重新起算，球本身不会跳。
                            closeMenu()
                            downRawX = event.rawX
                            downRawY = event.rawY
                            startParamsX = current.x
                            startParamsY = current.y
                            dragging = true
                            return@setOnTouchListener true
                        }
                        dragging = true
                    }
                    val rightAnchored = xFraction >= 0.5f
                    val dx = event.rawX - downRawX
                    val signedDx = if (rightAnchored) -dx else dx
                    current.x = (startParamsX + signedDx).roundToInt().coerceIn(minWindowX, maxWindowX)
                    current.y = (startParamsY + (event.rawY - downRawY)).roundToInt()
                        .coerceIn(minWindowY, maxWindowY)
                    runCatching { manager.updateViewLayout(view, current) }
                    true
                }
                MotionEvent.ACTION_UP -> {
                    if (dragging) {
                        commitPosition()
                    } else {
                        // 小位移回弹，仍当作点击：开/关功能栏。
                        current.x = startParamsX
                        current.y = startParamsY
                        runCatching { manager.updateViewLayout(view, current) }
                        toggleMenu()
                    }
                    true
                }
                MotionEvent.ACTION_CANCEL -> {
                    if (dragging) {
                        commitPosition()
                    } else {
                        current.x = startParamsX
                        current.y = startParamsY
                        runCatching { manager.updateViewLayout(view, current) }
                    }
                    true
                }
                else -> false
            }
        }
        applyWindowLayout(update = false)
        return try {
            manager.addView(touchRoot, layoutParams)
            treeOwner.start()
            root = touchRoot
            composeView = compose
            owner = treeOwner
            windowManager = manager
            _position.value = BubblePosition(xFraction, yFraction)
            _visible.value = true
            true
        } catch (_: Exception) {
            touchRoot.content = null
            treeOwner.destroy()
            runCatching { compose.disposeComposition() }
            params = null
            false
        }
    }

    fun hide() {
        val current = root
        _menu.value = OverlayMenuState()
        _copied.value = false
        _copyFailed.value = false
        if (current == null) {
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
        params = null
        _visible.value = false
    }

    /**
     * 物理屏幕尺寸（overlay 窗口的坐标空间）。
     *
     * API 30+ 用 [WindowManager.currentWindowMetrics]；旧版本用 `getRealMetrics`
     * （`displayMetrics` 给的是应用可用区域，可能已经扣掉系统栏，不能拿来算 BOTTOM 锚定）。
     */
    private fun displayBounds(manager: WindowManager): Pair<Int, Int> {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val bounds = manager.currentWindowMetrics.bounds
            return bounds.width() to bounds.height()
        }
        val real = DisplayMetrics()
        @Suppress("DEPRECATION")
        val display = manager.defaultDisplay
        display.getRealMetrics(real)
        return real.widthPixels to real.heightPixels
    }

    /**
     * 系统栏安全边距：状态栏（顶）与导航条（底/左右）。
     *
     * API 30+ 读 `currentWindowMetrics.windowInsets`（含挖孔）；旧版本退回系统 dimen。
     * 这些边距只用于**球的活动区**，保证球拖到边缘时不会压住状态栏文字或导航条。
     */
    private fun displayInsets(context: Context, manager: WindowManager): EdgeInsets {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val insets = manager.currentWindowMetrics.windowInsets.getInsetsIgnoringVisibility(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout(),
            )
            return EdgeInsets(
                left = insets.left,
                right = insets.right,
                top = insets.top,
                bottom = insets.bottom,
            )
        }
        return EdgeInsets(
            left = 0,
            right = 0,
            top = systemDimenPx(context, "status_bar_height"),
            bottom = systemDimenPx(context, "navigation_bar_height"),
        )
    }

    private fun systemDimenPx(context: Context, name: String): Int {
        val id = context.resources.getIdentifier(name, "dimen", "android")
        if (id <= 0) {
            return 0
        }
        return runCatching { context.resources.getDimensionPixelSize(id) }.getOrDefault(0)
    }

    /**
     * 把球当前的比例位置翻成窗口像素参数。
     *
     * 菜单收起时窗口就是球，`Gravity.TOP` 下 `params.y` 就是球顶；
     * 菜单展开且在上方时改用 `Gravity.BOTTOM`，锚在球底——窗口向上生长，球不动。
     * BOTTOM 锚定时 `y` 是"窗口底边到屏幕底边"的距离，因此不需要知道菜单有多高。
     */
    private fun applyWindowLayout(update: Boolean) {
        val current = params ?: return
        val rightAnchored = xFraction >= 0.5f
        val ballLeft = insetSidePx + edgePx + OverlayGeometry.offsetPx(xFraction, travelX)
        val ballTop = insetTopPx + edgePx + OverlayGeometry.offsetPx(yFraction, travelY)
        val menu = _menu.value
        val above = menu.open && menu.above
        current.gravity = (if (rightAnchored) Gravity.END else Gravity.START) or
            (if (above) Gravity.BOTTOM else Gravity.TOP)
        current.x = if (rightAnchored) {
            (screenWidthPx - (ballLeft + markPx)).coerceAtLeast(0)
        } else {
            ballLeft
        }
        current.y = if (above) {
            (screenHeightPx - (ballTop + markPx)).coerceAtLeast(0)
        } else {
            ballTop
        }
        if (update) {
            val view = root ?: return
            runCatching { windowManager?.updateViewLayout(view, current) }
        }
    }

    /** 手指是否落在球上（窗口内坐标）。功能栏展开时球贴窗口的"球那一侧"角。 */
    private fun isInsideBall(view: View, x: Float, y: Float): Boolean {
        if (view.width <= 0 || view.height <= 0) {
            return true
        }
        val menu = _menu.value
        val ballLeft = when {
            !menu.open -> 0f
            menu.alignEnd -> (view.width - markPx).toFloat()
            else -> 0f
        }
        val ballTop = when {
            !menu.open -> 0f
            menu.above -> (view.height - markPx).toFloat()
            else -> 0f
        }
        return OverlayGeometry.hitTest(
            pointerX = x,
            pointerY = y,
            left = ballLeft,
            top = ballTop,
            sizePx = markPx.toFloat(),
        )
    }

    private fun toggleMenu() {
        val current = _menu.value
        if (current.open) {
            _menu.value = current.copy(open = false)
        } else {
            val ballTop = insetTopPx + edgePx + OverlayGeometry.offsetPx(yFraction, travelY)
            _menu.value = OverlayMenuState(
                open = true,
                // 球在下半屏说明上方空间更多 → 菜单向上长；反之向下长。
                above = ballTop + markPx > screenHeightPx / 2,
                alignEnd = xFraction >= 0.5f,
            )
        }
        applyWindowLayout(update = true)
    }

    private fun closeMenu() {
        if (!_menu.value.open) {
            return
        }
        _menu.value = _menu.value.copy(open = false)
        applyWindowLayout(update = true)
    }

    /** 落定位置：像素 → 比例，**不吸附**（停在哪就是哪），只做边界钳制并重算窗口参数。 */
    private fun commitPosition() {
        val current = params ?: return
        val rightAnchored = xFraction >= 0.5f
        // 统一用 px(Int)：两个分支本来都是整数像素，再由 fractionForOffset 换成比例。
        val absoluteLeft = if (rightAnchored) {
            screenWidthPx - (current.x + markPx)
        } else {
            current.x
        }
        xFraction = OverlayGeometry.fractionForOffset(
            absoluteLeft - insetSidePx - edgePx,
            travelX,
        )
        yFraction = OverlayGeometry.fractionForOffset(current.y - insetTopPx - edgePx, travelY)
        applyWindowLayout(update = true)
        _position.value = BubblePosition(xFraction, yFraction)
    }

    /** 主操作：与页面球菜单一致，走 Service 的 START/STOP（网关状态只有一个 owner）。 */
    private fun requestPrimaryAction(context: Context) {
        val running = GatewayServiceController.status.value.running
        val action = if (running) {
            AetherGatewayService.ACTION_STOP
        } else {
            AetherGatewayService.ACTION_START
        }
        runCatching {
            context.startService(
                Intent(context, AetherGatewayService::class.java).setAction(action),
            )
        }
    }

    /** 应用内语义：关闭 app 外显（页面球随即重新出现）。 */
    private fun hideOverlay(context: Context) {
        closeMenu()
        runCatching {
            context.startService(
                Intent(context, AetherGatewayService::class.java)
                    .setAction(AetherGatewayService.ACTION_HIDE_OVERLAY),
            )
        }
    }

    private fun copyAddress(context: Context) {
        val addr = GatewayServiceController.status.value.addr
        if (addr.isBlank()) {
            return
        }
        // 写剪贴板不受 Android 10+ 后台限制（被限制的是读），失败时按钮显示「复制失败」。
        val copied = runCatching {
            val clipboard = context.getSystemService(ClipboardManager::class.java)
                ?: return@runCatching false
            clipboard.setPrimaryClip(ClipData.newPlainText("Aether 网关地址", "http://$addr/v1"))
            true
        }.getOrDefault(false)
        _copied.value = copied
        _copyFailed.value = !copied
        copyScope.launch {
            delay(1500)
            _copied.value = false
            _copyFailed.value = false
        }
    }

    /**
     * 应用外语义：返回 app。
     *
     * Service 上下文启动 Activity 必须带 `FLAG_ACTIVITY_NEW_TASK`；本进程持有
     * `SYSTEM_ALERT_WINDOW`，属于 Android 10+ 后台启动 Activity 的豁免情形。
     */
    private fun openApp(context: Context) {
        runCatching {
            context.startActivity(
                Intent(context, MainActivity::class.java).apply {
                    addFlags(
                        Intent.FLAG_ACTIVITY_NEW_TASK or
                            Intent.FLAG_ACTIVITY_SINGLE_TOP or
                            Intent.FLAG_ACTIVITY_REORDER_TO_FRONT,
                    )
                },
            )
        }
    }
}

/** 读页面随 SHOW_OVERLAY 带过来的位置比例；缺字段表示"用系统窗自己记着的位置"。 */
internal fun overlayPositionFrom(intent: Intent?): BubblePosition? {
    if (intent == null) {
        return null
    }
    if (!intent.hasExtra(GatewayOverlayController.EXTRA_OVERLAY_X) ||
        !intent.hasExtra(GatewayOverlayController.EXTRA_OVERLAY_Y)
    ) {
        return null
    }
    return BubblePosition(
        xFraction = OverlayGeometry.clampFraction(
            intent.getFloatExtra(GatewayOverlayController.EXTRA_OVERLAY_X, 1f),
        ),
        yFraction = OverlayGeometry.clampFraction(
            intent.getFloatExtra(GatewayOverlayController.EXTRA_OVERLAY_Y, 0.15f),
        ),
    )
}

/**
 * 把 overlay 的触摸分派成两件事：
 *
 * - 落在球上：窗口层拦截（拖动/点开功能栏，全屏 raw 坐标，窗口移动不影响手势）；
 * - 落在功能栏上：不拦截，事件继续进 Compose，按钮拿得到点击；
 * - 点窗口外：`FLAG_WATCH_OUTSIDE_TOUCH` 送来的 ACTION_OUTSIDE，用来收起功能栏。
 *
 * 它也是 WindowManager 真正 add 的根视图：Lifecycle/SavedState/ViewModelStore owner
 * 都挂在它身上；detach 时顺手释放 Compose composition。
 */
private class OverlayTouchRoot(context: Context) : FrameLayout(context) {
    var content: ComposeView? = null
    var onOutsideTouch: (() -> Unit)? = null
    var shouldInterceptDown: ((Float, Float) -> Boolean)? = null

    private var interceptGesture = false

    /** 当前手势是否归窗口层（落在球上）。触摸监听必须先问它，避免误吞功能栏点击。 */
    val intercepting: Boolean get() = interceptGesture

    override fun onInterceptTouchEvent(ev: MotionEvent): Boolean {
        if (ev.actionMasked == MotionEvent.ACTION_DOWN) {
            interceptGesture = shouldInterceptDown?.invoke(ev.x, ev.y) ?: true
        }
        return interceptGesture
    }

    override fun dispatchTouchEvent(ev: MotionEvent): Boolean {
        if (ev.actionMasked == MotionEvent.ACTION_OUTSIDE) {
            onOutsideTouch?.invoke()
            return true
        }
        return super.dispatchTouchEvent(ev)
    }

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
