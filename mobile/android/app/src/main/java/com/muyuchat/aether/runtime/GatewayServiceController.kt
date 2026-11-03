package com.muyuchat.aether.runtime

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
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
            val config = buildRuntimeConfig(
                dataDir = dataDir.absolutePath,
                cacheDir = cacheDir.absolutePath,
                tempDir = tempDir.absolutePath,
                port = preferredPort,
                accountKey = accountKey,
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
                addr = started.addr,
                adminSession = started.adminSession,
                callerKey = started.callerKey,
            )
        } else {
            GatewayStatus(
                state = GatewayState.FAILED,
                errorCode = started.errorCode.ifBlank { "start_failed" },
            )
        }
        _status.value
    }

    internal suspend fun stop(): GatewayStatus = mutex.withLock {
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
}
