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
    /** 主监听是否绑在 0.0.0.0（局域网共享开启）。管理面同时对该网络开放。 */
    val lanEnabled: Boolean = false,
    /** 枚举到的局域网地址（`http://ip:port`）；拿不到时为空。 */
    val lanUrls: List<String> = emptyList(),
) {
    val busy: Boolean get() = state == GatewayState.STARTING || state == GatewayState.STOPPING
    val running: Boolean get() = state == GatewayState.RUNNING

    /** 当前监听是否含非回环地址（0.0.0.0 或具体网卡 IP）。 */
    val listeningBeyondLoopback: Boolean
        get() = lanEnabled || (addr.isNotBlank() && !addr.startsWith("127.0.0.1:"))

    /** 局域网内设备该填的对话接口地址（`http://ip:port/v1`）；为空表示没拿到地址。 */
    val lanV1Urls: List<String>
        get() = lanUrls.map { it.trimEnd('/') + "/v1" }
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
