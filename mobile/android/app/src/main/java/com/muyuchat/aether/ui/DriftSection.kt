package com.muyuchat.aether.ui

import android.app.Application
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
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.DriftApiClient
import com.muyuchat.aether.runtime.DriftReport
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.driftKindLabel
import com.muyuchat.aether.runtime.driftSummaryLine
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface DriftUiState {
    data object Idle : DriftUiState
    data object Loading : DriftUiState
    data class Ready(val report: DriftReport) : DriftUiState
    data class Failed(val code: String, val message: String) : DriftUiState
}

/**
 * 配置漂移：点一次才查（对比桌面客户端的配置文件，代价不低）。
 * **只显示"哪几个客户端漂移了、属于哪类"**——`detail` 里是绝对路径与配置行原文，手机端不碰。
 */
class DriftViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = DriftApiClient()
    private val _state = MutableStateFlow<DriftUiState>(DriftUiState.Idle)
    val state: StateFlow<DriftUiState> = _state.asStateFlow()

    fun refresh() {
        if (_state.value is DriftUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = DriftUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = DriftUiState.Loading
        viewModelScope.launch {
            _state.value = try {
                DriftUiState.Ready(withContext(Dispatchers.IO) { client.fetchDrift(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                DriftUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                DriftUiState.Failed("unknown", "读取配置漂移失败，请重试")
            }
        }
    }
}

@Composable
internal fun DriftSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: DriftViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke(), modifier = modifier) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "配置漂移",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "对比桌面客户端配置文件与网关期望值：地址被改回官方、文件被删、模型被改都算漂移。" +
                    "手机端只看「哪个客户端、哪一类」；逐行差异与修复都在桌面端。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                enabled = status.running && state !is DriftUiState.Loading,
            ) { Text(if (state is DriftUiState.Loading) "检查中…" else "检查配置漂移") }
            when (val current = state) {
                DriftUiState.Idle, DriftUiState.Loading -> Unit
                is DriftUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = if (current.code == "gateway_not_running") {
                            "网关未启动：启动后才能检查配置漂移。"
                        } else {
                            "${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                is DriftUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = driftSummaryLine(current.report),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    current.report.entries.forEach { entry ->
                        Spacer(Modifier.height(6.dp))
                        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(1.dp)) {
                            Text(
                                text = "· ${entry.agentId}",
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.error,
                            )
                            Text(
                                text = driftKindLabel(entry.kind),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
}
