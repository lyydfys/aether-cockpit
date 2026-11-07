package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.AutoImportApiClient
import com.muyuchat.aether.runtime.AutoImportStatus
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 「本机账号自动导入」（桌面 `settings.js:163-179`）。
 *
 * 端点都存在（`gateway.go:1018-1021`），但服务端每个入口都先判 `platform.WindowsAutoImport`
 * （`autoimport_api.go`）：Android 的 `PlatformCapabilities{}` 全 false → **501
 * `platform_unsupported`**。所以这里仍然照实调用，拿到服务端原因后：
 * - 明确写出「手机端没有本机客户端目录，扫不到」；
 * - 不摆"点了永远为空"的扫描按钮（按平台能力禁用）。
 */
@Composable
internal fun AutoImportSettingsSection(status: GatewayStatus) {
    val client = remember { AutoImportApiClient() }
    val scope = rememberCoroutineScope()
    var state by remember { mutableStateOf<AutoImportUiState>(AutoImportUiState.Loading) }
    var busy by remember { mutableStateOf(false) }
    var reload by remember { mutableIntStateOf(0) }

    fun handle(failure: Throwable, fallback: String): AutoImportUiState = when {
        failure is ManagementApiException && failure.code == "platform_unsupported" ->
            AutoImportUiState.Unsupported(failure.message)
        failure is ManagementApiException && failure.code == "gateway_not_running" ->
            AutoImportUiState.Unsupported("网关未启动：启动后才能读取自动导入状态")
        else -> AutoImportUiState.Failed(fallback)
    }

    LaunchedEffect(status.running, status.addr, reload) {
        if (!status.running) {
            state = AutoImportUiState.Unsupported("网关未启动：启动后才能读取自动导入状态")
            return@LaunchedEffect
        }
        state = AutoImportUiState.Loading
        state = try {
            AutoImportUiState.Ready(withContext(Dispatchers.IO) { client.fetchStatus(status) })
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            handle(e, "读取自动导入状态失败，请重试")
        }
    }

    fun run(action: suspend () -> AutoImportStatus, failure: String) {
        if (busy) {
            return
        }
        busy = true
        scope.launch {
            state = try {
                AutoImportUiState.Ready(withContext(Dispatchers.IO) { action() })
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                handle(e, failure)
            }
            busy = false
        }
    }

    SettingsSection("本机账号自动导入") {
        Text(
            text = "桌面端会定时扫描本机客户端（Windsurf / Cursor / Trae 等）的登录态并自动入库；" +
                "手机上没有这些目录，服务端在 Android 上也不开放该能力。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        when (val current = state) {
            AutoImportUiState.Loading -> Text(
                text = "正在读取自动导入状态…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            is AutoImportUiState.Unsupported -> {
                Text(
                    text = current.detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "手机端通常扫不到本机客户端：账号请用「接入渠道」或在桌面端自动导入。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is AutoImportUiState.Failed -> {
                Text(
                    text = current.detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                Spacer(Modifier.height(6.dp))
                McaButton(
                    onClick = { reload += 1 },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("重试", maxLines = 1) }
            }
            is AutoImportUiState.Ready -> {
                val auto = current.status
                Text(
                    text = buildString {
                        append(if (auto.enabled) "已开启" else "已关闭")
                        if (auto.intervalSec > 0) append(" · 每 ${auto.intervalSec} 秒扫描一次")
                        append(" · 已见过 ${auto.seen} 个凭据")
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                if (auto.lastScan > 0) {
                    Text(
                        text = "上次扫描：" + autoImportTimeLabel(auto.lastScan),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (auto.lastAdded.isNotEmpty()) {
                    Text(
                        text = "上次新增：" + auto.lastAdded.joinToString("、"),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                    )
                }
                if (auto.lastError.isNotBlank()) {
                    Text(
                        text = "上次错误：" + auto.lastError,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(8.dp))
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    McaButton(
                        onClick = { run({ client.scan(status) }, "扫描失败，请重试") },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = !busy,
                    ) { Text(if (busy) "处理中…" else "立即扫描", maxLines = 1) }
                    McaButton(
                        onClick = { run({ client.reset(status) }, "清除记录失败，请重试") },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = !busy,
                    ) { Text("清除记录", maxLines = 1) }
                    Spacer(Modifier.width(0.dp))
                }
            }
        }
    }
}

/** 自动导入状态：可读 / 平台不支持（501）/ 其它失败。 */
private sealed interface AutoImportUiState {
    data object Loading : AutoImportUiState
    data class Ready(val status: AutoImportStatus) : AutoImportUiState
    data class Unsupported(val detail: String) : AutoImportUiState
    data class Failed(val detail: String) : AutoImportUiState
}

/** `last_scan` 是 unix 秒；只显示到分钟，避免 1.3× 下换行。 */
internal fun autoImportTimeLabel(lastScan: Long): String {
    if (lastScan <= 0L) {
        return "从未扫描"
    }
    val fmt = java.time.format.DateTimeFormatter.ofPattern("MM-dd HH:mm")
    return java.time.Instant.ofEpochSecond(lastScan)
        .atZone(java.time.ZoneId.systemDefault())
        .format(fmt)
}
