package com.muyuchat.aether.runtime

/** Product-level gateway state exposed to the UI and the foreground notification. */
enum class GatewayState {
    STOPPED,
    STARTING,
    RUNNING,
    STOPPING,
    FAILED,
}

/**
 * Single source of truth shared by the Activity UI and the foreground service.
 * The two session keys stay in this in-memory object only; they are never
 * persisted or included in notification text.
 */
data class GatewayStatus(
    val state: GatewayState = GatewayState.STOPPED,
    val addr: String = "",
    val errorCode: String = "",
    val adminSession: String = "",
    val callerKey: String = "",
) {
    val busy: Boolean get() = state == GatewayState.STARTING || state == GatewayState.STOPPING
    val running: Boolean get() = state == GatewayState.RUNNING
}

/** Notification copy is pure so it can be unit-tested without Android. */
fun notificationText(status: GatewayStatus): String = when (status.state) {
    GatewayState.STARTING -> "正在启动"
    GatewayState.RUNNING -> "运行中 · ${status.addr}"
    GatewayState.STOPPING -> "正在停止"
    GatewayState.FAILED -> "启动失败 · ${status.errorCode.ifBlank { "unknown" }}"
    GatewayState.STOPPED -> "已停止"
}

/** Short state badge used by the overview and directory rows. */
fun stateLabel(status: GatewayStatus): String = when (status.state) {
    GatewayState.STARTING -> "启动中"
    GatewayState.RUNNING -> "运行中"
    GatewayState.STOPPING -> "停止中"
    GatewayState.FAILED -> "启动失败"
    GatewayState.STOPPED -> "已停止"
}
