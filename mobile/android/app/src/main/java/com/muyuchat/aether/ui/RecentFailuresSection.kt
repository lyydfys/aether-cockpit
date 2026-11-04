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
import com.muyuchat.aether.runtime.GatewayHistoryApiClient
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.HistorySnapshot
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.TraceFailure
import com.muyuchat.aether.runtime.historySummaryLine
import com.muyuchat.aether.runtime.relativeTimeLabel
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

sealed interface RecentFailuresUiState {
    data object Idle : RecentFailuresUiState
    data object Loading : RecentFailuresUiState
    data class Ready(val snapshot: HistorySnapshot, val nowMs: Long) : RecentFailuresUiState
    data class Failed(val code: String, val message: String) : RecentFailuresUiState
}

/**
 * 最近异常：从 `/api/gateway/history` 的轨迹里筛出失败。
 * 这是**历史记录**，不是实时状态；页面不写"当前正常"。
 */
class RecentFailuresViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = GatewayHistoryApiClient()
    private val _state = MutableStateFlow<RecentFailuresUiState>(RecentFailuresUiState.Idle)
    val state: StateFlow<RecentFailuresUiState> = _state.asStateFlow()

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = RecentFailuresUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (_state.value !is RecentFailuresUiState.Ready) {
            _state.value = RecentFailuresUiState.Loading
        }
        viewModelScope.launch {
            _state.value = try {
                // 先取 day 列表，再按最新一天取轨迹（不带 day 时 routes 恒为空）
                val first = withContext(Dispatchers.IO) { client.fetchHistory(status) }
                val latestDay = first.days.firstOrNull()?.day.orEmpty()
                val snapshot = if (latestDay.isEmpty()) {
                    first
                } else {
                    withContext(Dispatchers.IO) { client.fetchHistory(status, latestDay) }
                }
                RecentFailuresUiState.Ready(snapshot, System.currentTimeMillis())
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                RecentFailuresUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                RecentFailuresUiState.Failed("unknown", "读取请求轨迹失败，请重试")
            }
        }
    }
}

@Composable
internal fun RecentFailuresSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: RecentFailuresViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke(), modifier = modifier) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "最近异常",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "从本机的请求轨迹里筛出失败的那几条：哪次、哪个模型、上游报了什么。" +
                    "这是历史记录，不代表此刻的状态。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                enabled = status.running && state !is RecentFailuresUiState.Loading,
            ) { Text(if (state is RecentFailuresUiState.Loading) "读取中…" else "读取最近异常") }
            when (val current = state) {
                RecentFailuresUiState.Idle, RecentFailuresUiState.Loading -> Unit
                is RecentFailuresUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = if (current.code == "gateway_not_running") {
                            "网关未启动：启动后才能读取请求轨迹。"
                        } else {
                            "读取失败：${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                is RecentFailuresUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = historySummaryLine(current.snapshot),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    current.snapshot.failures.take(12).forEach { failure ->
                        Spacer(Modifier.height(8.dp))
                        FailureRow(failure, current.nowMs)
                    }
                }
            }
        }
    }
}

@Composable
private fun FailureRow(failure: TraceFailure, nowMs: Long) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(1.dp)) {
        val title = buildString {
            append(relativeTimeLabel(nowMs, failure.timeMs))
            append(" · ")
            append(failure.model.ifBlank { "未知模型" })
            if (failure.status > 0) {
                append(" · HTTP ")
                append(failure.status)
            }
            if (failure.attempts > 1) {
                append(" · ")
                append(failure.attempts)
                append(" 次尝试")
            }
        }
        Text(
            text = "· $title",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.error,
        )
        failure.fails.forEach { fail ->
            Text(
                text = fail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
