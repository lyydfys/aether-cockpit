package com.muyuchat.aether.ui

import android.app.Application
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.SettingsSummary
import com.muyuchat.aether.runtime.HealthSnapshot
import com.muyuchat.aether.runtime.buildHealthReport
import com.muyuchat.aether.runtime.healthReportFileName
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

sealed interface HealthReportUiState {
    data object Idle : HealthReportUiState
    data class Ready(val text: String, val uri: android.net.Uri, val fileName: String) : HealthReportUiState
    data class Failed(val message: String) : HealthReportUiState
}

/**
 * 自检摘要导出。
 *
 * 只用**页面已经拿到的** `GatewayStatus` 与 `SettingsSummary` 组装（不新发请求），
 * 因此导出永远不会改变现场，也不会因为某个接口慢而拖住。
 */
class HealthReportViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val _state = MutableStateFlow<HealthReportUiState>(HealthReportUiState.Idle)
    val state: StateFlow<HealthReportUiState> = _state.asStateFlow()

    fun export(settings: SettingsSummary?) {
        viewModelScope.launch {
            _state.value = try {
                val status = repository.status.value
                val now = System.currentTimeMillis()
                val text = withContext(Dispatchers.Default) {
                    buildHealthReport(
                        HealthSnapshot(
                            generatedAtMs = now,
                            appVersion = appVersion(),
                            running = status.running,
                            listenAddr = status.addr,
                            settings = settings,
                            security = null,
                        ),
                    )
                }
                val name = healthReportFileName(now)
                val written = withContext(Dispatchers.IO) {
                    val dir = File(getApplication<Application>().cacheDir, "reports").apply { mkdirs() }
                    val file = File(dir, name)
                    file.writeText(text, Charsets.UTF_8)
                    // 只让本 App 可读写：FileProvider 以自身身份读，不需要放开 other。
                    file.setReadable(false, false)
                    file.setReadable(true, true)
                    file.setWritable(false, false)
                    file.setWritable(true, true)
                    file
                }
                val uri = FileProvider.getUriForFile(
                    getApplication(),
                    "${getApplication<Application>().packageName}.fileprovider",
                    written,
                )
                HealthReportUiState.Ready(text = text, uri = uri, fileName = name)
            } catch (_: Exception) {
                HealthReportUiState.Failed("导出失败：无法写入本机缓存目录")
            }
        }
    }

    fun shareIntent(ready: HealthReportUiState.Ready): Intent = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_STREAM, ready.uri)
        putExtra(Intent.EXTRA_SUBJECT, "Aether Cockpit 本机自检摘要")
        putExtra(Intent.EXTRA_TEXT, ready.text)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }

    /** 版本名从 PackageManager 读（不依赖 BuildConfig 是否开启）；读不到就写"未知"，不编造。 */
    private fun appVersion(): String = runCatching {
        val pm = getApplication<Application>().packageManager
        pm.getPackageInfo(getApplication<Application>().packageName, 0).versionName
    }.getOrNull().orEmpty().ifBlank { "未知" }
}

/**
 * 自检摘要：把设置页已有的信息整理成一段可分享的纯文本。
 * 不含密钥/令牌/账号标识/文件路径（有单测守着）。
 */
@Composable
internal fun HealthReportSection(
    status: GatewayStatus,
    settings: SettingsSummary?,
    onShare: (Intent) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: HealthReportViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke(), modifier = modifier) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "本机自检摘要",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "把本机状态整理成一段纯文本，方便自查或贴给别人排障。" +
                    "只含计数与开关状态，不含密钥、令牌、账号标识或文件路径。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            McaButton(
                onClick = { viewModel.export(settings) },
                style = McaButtonStyle.Outlined,
            ) { Text("生成摘要") }
            when (val current = state) {
                HealthReportUiState.Idle -> Unit
                is HealthReportUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = current.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                is HealthReportUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = current.text,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(10.dp))
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        McaButton(
                            onClick = { onShare(viewModel.shareIntent(current)) },
                            style = McaButtonStyle.Primary,
                        ) { Text("分享 ${current.fileName}") }
                    }
                }
            }
        }
    }
}
