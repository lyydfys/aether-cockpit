package com.muyuchat.aether.runtime

import android.content.Context
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Process-wide owner of the Go runtime lifecycle. The Activity and the
 * foreground service both observe [status]; only this object calls the JNI
 * bridge, so start/stop cannot interleave.
 *
 * The session keys live only in the emitted [GatewayStatus] (private app
 * memory). They are deliberately absent from logs, notifications and prefs.
 */
object GatewayServiceController {
    private val _status = MutableStateFlow(GatewayStatus())
    val status: StateFlow<GatewayStatus> = _status.asStateFlow()

    private val mutex = Mutex()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var pollJob: Job? = null
    private var appContext: Context? = null

    internal suspend fun start(context: Context): GatewayStatus = mutex.withLock {
        if (_status.value.state == GatewayState.RUNNING || _status.value.state == GatewayState.STARTING) {
            return _status.value
        }
        _status.value = GatewayStatus(state = GatewayState.STARTING)
        val started = withContext(Dispatchers.IO) {
            val accountKey = runCatching { AccountKeyStore(context).loadOrCreate() }.getOrNull()
            if (accountKey == null) {
                return@withContext RuntimeSnapshot(
                    state = "failed",
                    addr = "",
                    startedAt = 0,
                    errorCode = "account_key_unavailable",
                    adminSession = "",
                    callerKey = "",
                )
            }
            val dataDir = File(context.noBackupFilesDir, "aether")
            val cacheDir = File(context.filesDir, "aether/cache")
            val tempDir = File(context.cacheDir, "aether/tmp")
            // 首选端口：0 表示交给系统分配。读失败一律回落 0——宁可每次换端口，
            // 也不要因为一个坏配置让网关永远起不来。
            val preferredPort = runCatching { PreferredPortStore(context).load() }.getOrDefault(0)
            // 局域网共享：默认关；开启时绑 0.0.0.0（/v1 与管理面同时对该网络开放）。
            val lanSharing = runCatching { LanSharingStore(context).load() }.getOrDefault(false)
            // 局域网地址由宿主探测后注入：Android 的 SELinux 不让 app 用 netlink，
            // Go 侧 net.Interfaces() 会失败（真机 B2 就是这么暴露出来的）。
            val lanHint = if (lanSharing) {
                runCatching {
                    lanAddressHint(LanAddressResolver.ipv4Addresses(context))
                }.getOrDefault("")
            } else {
                ""
            }
            val config = buildRuntimeConfig(
                dataDir = dataDir.absolutePath,
                cacheDir = cacheDir.absolutePath,
                tempDir = tempDir.absolutePath,
                listenHost = lanListenHost(lanSharing),
                port = preferredPort,
                accountKey = accountKey,
                lanAddressHint = lanHint,
            )
            val started: RuntimeSnapshot = runCatching {
                RuntimeSnapshot.parse(NativeBridge.nativeStart(config))
            }.getOrElse {
                RuntimeSnapshot(
                    state = "failed",
                    addr = "",
                    startedAt = 0,
                    errorCode = "bridge_call_failed",
                    adminSession = "",
                    callerKey = "",
                )
            }
            started
        }
        _status.value = if (started.running) {
            GatewayStatus(
                state = GatewayState.RUNNING,
                addr = localEndpoint(started.addr),
                adminSession = started.adminSession,
                callerKey = started.callerKey,
                lanEnabled = started.lanEnabled,
                lanUrls = started.lanUrls,
            )
        } else {
            GatewayStatus(
                state = GatewayState.FAILED,
                errorCode = started.errorCode.ifBlank { "start_failed" },
            )
        }
        appContext = context.applicationContext
        startStatusPolling()
        _status.value
    }

    /**
     * 局域网地址会随 Wi-Fi 变化（拔网/换网段/重连），但 Go 侧启动后不会再收到
     * 新的探测结果。这里每 5 秒拉一次 `nativeGetStatus`，并用**宿主现探测到的**
     * IPv4 重算 lanUrls：拿不到地址就清空（界面显示"未获取到局域网地址"），
     * 绝不停留在旧 IP 上。
     */
    private fun startStatusPolling() {
        pollJob?.cancel()
        pollJob = scope.launch {
            while (isActive) {
                delay(STATUS_POLL_INTERVAL_MS)
                val current = _status.value
                if (current.state != GatewayState.RUNNING) continue
                val snapshot = runCatching {
                    RuntimeSnapshot.parse(NativeBridge.nativeGetStatus())
                }.getOrNull() ?: continue
                val port = snapshot.addr.substringAfterLast(':', "").toIntOrNull()
                val freshUrls = when {
                    !current.lanEnabled -> emptyList()
                    port == null -> current.lanUrls
                    else -> {
                        val context = appContext
                        if (context == null) {
                            current.lanUrls
                        } else {
                            runCatching { LanAddressResolver.ipv4Addresses(context) }
                                .getOrDefault(emptyList())
                                .map { "http://$it:$port" }
                        }
                    }
                }
                val next = current.copy(
                    addr = localEndpoint(snapshot.addr).ifBlank { current.addr },
                    lanEnabled = snapshot.lanEnabled,
                    lanUrls = freshUrls,
                )
                if (next != current) _status.value = next
            }
        }
    }

    internal suspend fun stop(): GatewayStatus = mutex.withLock {
        pollJob?.cancel()
        pollJob = null
        if (_status.value.state == GatewayState.STOPPED || _status.value.state == GatewayState.STOPPING) {
            return _status.value
        }
        _status.value = _status.value.copy(state = GatewayState.STOPPING)
        val stopped = withContext(Dispatchers.IO) {
            runCatching { RuntimeSnapshot.parse(NativeBridge.nativeStop()) }
                .getOrElse { RuntimeSnapshot("failed", "", 0, "bridge_call_failed", "", "") }
        }
        _status.value = GatewayStatus(
            state = if (stopped.state == "failed") GatewayState.FAILED else GatewayState.STOPPED,
            errorCode = stopped.errorCode,
        )
        _status.value
    }

    /**
     * 局域网模式下 Go 报的是真实绑定地址 `0.0.0.0:<port>`。`0.0.0.0` 只能用于
     * listen，不能用于 connect——本机客户端（设置页/关于页/健康检查）拿它拼 URL
     * 会直接 network_error。所以对外统一换成回环端点；局域网地址另走 lanUrls。
     */
    private fun localEndpoint(addr: String): String {
        val trimmed = addr.trim()
        return if (trimmed.startsWith("0.0.0.0:")) {
            LoopbackHostAlias + trimmed.removePrefix("0.0.0.0")
        } else {
            trimmed
        }
    }

    private const val STATUS_POLL_INTERVAL_MS = 5_000L
    private const val LoopbackHostAlias = "127.0.0.1"
}
