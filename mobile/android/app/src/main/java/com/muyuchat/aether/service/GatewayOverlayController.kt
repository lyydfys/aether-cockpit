package com.muyuchat.aether.service

import android.content.Context
import android.content.Intent
import android.graphics.PixelFormat
import android.provider.Settings
import android.view.Gravity
import android.view.WindowManager
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ComposeView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.setViewTreeLifecycleOwner
import androidx.savedstate.SavedStateRegistry
import androidx.savedstate.SavedStateRegistryController
import androidx.savedstate.SavedStateRegistryOwner
import androidx.savedstate.setViewTreeSavedStateRegistryOwner
import com.muyuchat.aether.MainActivity
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.ui.GatewaySystemOverlayBubble
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

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
 */
object GatewayOverlayController {
    private val _visible = MutableStateFlow(false)
    val visible: StateFlow<Boolean> = _visible.asStateFlow()

    private var view: ComposeView? = null
    private var owner: OverlayViewTreeOwner? = null
    private var windowManager: WindowManager? = null

    fun show(context: Context, colors: OverlayColors): Boolean {
        if (view != null) {
            _visible.value = true
            return true
        }
        if (!Settings.canDrawOverlays(context)) {
            return false
        }
        val manager = context.getSystemService(WindowManager::class.java) ?: return false
        val treeOwner = OverlayViewTreeOwner()
        treeOwner.create()
        val composeView = ComposeView(context).apply {
            setViewTreeLifecycleOwner(treeOwner)
            setViewTreeSavedStateRegistryOwner(treeOwner)
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
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.END
            x = (16 * density).toInt()
            y = (160 * density).toInt()
        }
        return try {
            manager.addView(composeView, params)
            treeOwner.start()
            view = composeView
            owner = treeOwner
            windowManager = manager
            _visible.value = true
            true
        } catch (_: Exception) {
            treeOwner.destroy()
            runCatching { composeView.disposeComposition() }
            false
        }
    }

    fun hide() {
        val current = view ?: run {
            _visible.value = false
            return
        }
        runCatching { windowManager?.removeViewImmediate(current) }
        runCatching { current.disposeComposition() }
        owner?.destroy()
        view = null
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

/** ComposeView 在 WindowManager 里也需要 Lifecycle/SavedState owner；否则组合会抛异常。 */
private class OverlayViewTreeOwner : LifecycleOwner, SavedStateRegistryOwner {
    private val registry = LifecycleRegistry(this)
    private val savedState = SavedStateRegistryController.create(this)

    override val lifecycle: Lifecycle get() = registry
    override val savedStateRegistry: SavedStateRegistry get() = savedState.savedStateRegistry

    fun create() {
        savedState.performRestore(null)
        registry.currentState = Lifecycle.State.CREATED
    }

    fun start() {
        registry.currentState = Lifecycle.State.STARTED
    }

    fun destroy() {
        registry.currentState = Lifecycle.State.DESTROYED
    }
}
