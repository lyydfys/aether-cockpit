package com.muyuchat.aether.runtime

import android.content.Context
import android.content.Intent
import com.muyuchat.aether.service.AetherGatewayService
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Narrow platform adapter used by the overview ViewModel. Composables never
 * touch the Service directly; this repository only forwards lifecycle intents
 * and exposes the single controller-owned status flow.
 */
class GatewayRuntimeRepository(context: Context) {
    private val appContext = context.applicationContext

    val status: StateFlow<GatewayStatus> = GatewayServiceController.status

    private val lanStore = LanSharingStore(appContext)

    /** 「用户希望网关在跑」的持久化意图，供冷启动后自动恢复（见 GatewayWantedStore）。 */
    private val wantedStore = GatewayWantedStore(appContext)

    /** 局域网共享的**用户意图**（持久化值）。真正生效的值看 [status] 的 `lanEnabled`。 */
    private val _lanSharing = MutableStateFlow(runCatching { lanStore.load() }.getOrDefault(false))
    val lanSharing: StateFlow<Boolean> = _lanSharing.asStateFlow()

    /** 最近一次切换失败的原因（例如绑 0.0.0.0 失败）；成功后清空。 */
    private val _lanError = MutableStateFlow<String?>(null)
    val lanError: StateFlow<String?> = _lanError.asStateFlow()

    /**
     * 用户点「启动网关」。
     *
     * 先落盘"希望网关在跑"的意图，再起前台 Service。被 force-stop 杀掉之后
     * Android 不会自动把 Service 拉回来，下次打开 App 由 MainActivity 读这条意图
     * 静默恢复；否则手机端所有页面（渠道/账号/设置）都会因为网关没起而显示为空。
     *
     * [recordIntent] 给自动恢复路径用：它自己负责失败计数，不需要这里再写一次。
     */
    fun start(recordIntent: Boolean = true) {
        if (recordIntent) {
            // 用户主动启动 = 重新开始计数，之前的连败记录作废。
            runCatching { wantedStore.save(GatewayWanted(wanted = true, failures = 0)) }
        }
        appContext.startForegroundService(
            Intent(appContext, AetherGatewayService::class.java)
                .setAction(AetherGatewayService.ACTION_START),
        )
    }

    /** 用户点「停止网关」：意图一并落盘为 false，下次冷启动不会再自动恢复。 */
    fun stop() {
        runCatching { wantedStore.save(GatewayWanted(wanted = false, failures = 0)) }
        appContext.startService(
            Intent(appContext, AetherGatewayService::class.java)
                .setAction(AetherGatewayService.ACTION_STOP),
        )
    }

    /**
     * 打开/关闭局域网共享。
     *
     * 绑定地址只在 `nativeStart` 时生效，所以运行中切换必须**重启一次 Go 运行时**。
     *
     * 真机 B1 的教训：上一版用 Service 的 ACTION_STOP/ACTION_START 来回切，结果
     * 服务在 `stopSelf()` 后 `onDestroy()` 看到状态是 RUNNING，又补了一次
     * `GatewayServiceController.stop()`，把刚起来的网关打死（表现为"约 12s 后已停止"）。
     * 现在改成**直接驱动 controller**（Go 运行时的唯一 owner），前台 Service 全程存活，
     * 不参与重启；失败时自动回落回环再起一次，保证"开个开关不会把网关搞挂"。
     */
    suspend fun setLanSharing(enabled: Boolean) {
        _lanError.value = null
        val persisted = runCatching {
            lanStore.save(enabled)
            true
        }.getOrDefault(false)
        if (!persisted) {
            _lanError.value = "无法保存局域网共享设置"
            return
        }
        _lanSharing.value = enabled

        if (GatewayServiceController.status.value.state != GatewayState.RUNNING) {
            // 没在跑：值已经落盘，下次启动会按它选 ListenHost。
            return
        }

        val switched = restartRuntime()
        if (switched.startedOk && switched.status.lanEnabled == enabled) {
            return
        }

        // —— fail-safe：任何没落到期望绑定上的情况，都退回"仅本机"并确保网关可用 ——
        val reason = when {
            !switched.startedOk -> "网关重启失败：${switched.status.errorCode.ifBlank { "unknown" }}"
            else -> "网关未按预期绑定${if (enabled) "局域网" else "仅本机"}"
        }
        lanStore.save(false)
        _lanSharing.value = false
        // 无论首跳是"起不来"还是"起错绑定"，都再试一次回环；两次都失败才让用户介入。
        val fallback = restartRuntime()
        _lanError.value = if (fallback.startedOk) {
            "$reason；已自动回到仅本机监听（网关可用）"
        } else {
            "$reason；自动恢复也失败（${fallback.status.errorCode.ifBlank { "unknown" }}），请手动点「启动网关」"
        }
    }

    /** 停一次、再起一次；返回"起来了吗"与最终状态。永不抛（除取消）。 */
    private suspend fun restartRuntime(): RestartOutcome {
        GatewayServiceController.stop()
        val started = runCatching { GatewayServiceController.start(appContext) }.getOrNull()
            ?: GatewayStatus(state = GatewayState.FAILED, errorCode = "restart_threw")
        return RestartOutcome(startedOk = started.state == GatewayState.RUNNING, status = started)
    }

    private data class RestartOutcome(val startedOk: Boolean, val status: GatewayStatus)
}
