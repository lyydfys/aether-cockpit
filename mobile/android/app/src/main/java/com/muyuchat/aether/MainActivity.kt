package com.muyuchat.aether

import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.core.view.WindowCompat
import com.muyuchat.aether.runtime.AppForegroundTracker
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.GatewayWanted
import com.muyuchat.aether.runtime.GatewayWantedStore
import com.muyuchat.aether.runtime.afterAutoStart
import com.muyuchat.aether.runtime.shouldAutoStart
import com.muyuchat.aether.ui.GatewayHome
import com.muyuchat.core.designsystem.McaTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

class MainActivity : ComponentActivity() {
    // 与 AetherGatewayService / GatewayOverlayController 同款：Activity 自己的
    // 生命周期作用域，onDestroy 里整体取消，不会泄漏到进程级。
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var autoStartJob: Job? = null

    // 系统悬浮窗由前台 Service 持有，没有 Activity 可借用；这里把"本进程有可见界面"
    // 这件事记到进程级 tracker，悬浮窗功能栏据此切换「关闭 app 外显 / 返回 app」。
    override fun onStart() {
        super.onStart()
        AppForegroundTracker.onActivityStarted()
        maybeAutoStartGateway()
    }

    override fun onStop() {
        AppForegroundTracker.onActivityStopped()
        super.onStop()
    }

    /**
     * 冷启动后静默恢复网关。
     *
     * 背景：`am force-stop` / 系统回收杀进程后，Android 不会自动重拉前台 Service，
     * 而手机上渠道、账号、设置页读的都是 App 内 loopback 上的网关——网关没起来，
     * 页面就全空，用户看到的是"接入的渠道全没了"。数据其实一直好好躺在
     * `no_backup/aether/`，只是没人去读。
     *
     * 只在 `onStart` 判定，且只在用户**上次是主动开着**（wanted=true）时恢复；
     * 不做 BOOT_COMPLETED 之类的后台自启。静默进行，不弹任何"正在启动"提示。
     */
    private fun maybeAutoStartGateway() {
        // 上一次尝试还没结束就不重复发起（onStart 可能被多次调用）。
        if (autoStartJob?.isActive == true) {
            return
        }
        autoStartJob = scope.launch {
            val store = GatewayWantedStore(applicationContext)
            val wanted = withContext(Dispatchers.IO) {
                runCatching { store.load() }.getOrDefault(GatewayWanted())
            }
            val current = GatewayServiceController.status.value
            if (!shouldAutoStart(wanted, current.state)) {
                return@launch
            }
            Log.i(TAG, "auto-start gateway: wanted=true, state=${current.state} → starting")
            // recordIntent=false：失败计数由下面这段统一负责，别在这里被清零。
            val dispatched = runCatching {
                GatewayRuntimeRepository(applicationContext).start(recordIntent = false)
            }.isSuccess
            if (!dispatched) {
                Log.w(TAG, "auto-start gateway: 无法启动前台服务")
                withContext(Dispatchers.IO) {
                    runCatching { store.save(afterAutoStart(wanted, running = false)) }
                }
                return@launch
            }
            // 等一次终态：前台 Service 会驱动 controller.start()，状态经 StateFlow 回来。
            val settled: GatewayStatus? = withTimeoutOrNull(AUTO_START_TIMEOUT_MS) {
                GatewayServiceController.status.first {
                    it.state == GatewayState.RUNNING || it.state == GatewayState.FAILED
                }
            }
            val running = settled?.state == GatewayState.RUNNING
            // 失败保留 wanted=true 以便下次再试；连败到上限则复位，避免无限重试。
            val next = afterAutoStart(wanted, running)
            withContext(Dispatchers.IO) {
                runCatching { store.save(next) }
            }
            if (running) {
                Log.i(TAG, "auto-start gateway: running at ${settled?.addr.orEmpty()}")
            } else {
                val reason = settled?.errorCode?.takeIf { it.isNotBlank() } ?: "timeout"
                Log.w(TAG, "auto-start gateway failed: $reason（连续失败 ${wanted.failures + 1} 次，wanted=${next.wanted}）")
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        setContent {
            McaTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    GatewayHome()
                }
            }
        }
    }

    override fun onDestroy() {
        autoStartJob = null
        scope.cancel()
        super.onDestroy()
    }

    private companion object {
        const val TAG = "AetherAutoStart"

        /** 等网关进入终态的耐心上限；超过按一次失败计。 */
        const val AUTO_START_TIMEOUT_MS = 20_000L
    }
}
