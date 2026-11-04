package com.muyuchat.aether.ui

import android.app.Application
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.DiagnoseApiClient
import com.muyuchat.aether.runtime.DiagnoseItem
import com.muyuchat.aether.runtime.DiagnoseReport
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.diagnoseDisplayOrder
import com.muyuchat.aether.runtime.diagnoseSummaryLine
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface DiagnoseUiState {
    data object Idle : DiagnoseUiState
    data object Running : DiagnoseUiState
    data class Ready(val report: DiagnoseReport) : DiagnoseUiState
    data class Failed(val code: String, val message: String) : DiagnoseUiState
}

/**
 * 自检：点一次才跑，不自动跑（会给网关带来真实计算）。
 * 结论写「刚刚检查」，不写"系统正常"——那是持续状态承诺。
 */
class DiagnoseViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = DiagnoseApiClient()
    private val _state = MutableStateFlow<DiagnoseUiState>(DiagnoseUiState.Idle)
    val state: StateFlow<DiagnoseUiState> = _state.asStateFlow()

    fun run() {
        if (_state.value is DiagnoseUiState.Running) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = DiagnoseUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = DiagnoseUiState.Running
        viewModelScope.launch {
            _state.value = try {
                DiagnoseUiState.Ready(withContext(Dispatchers.IO) { client.fetchDiagnose(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                DiagnoseUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                DiagnoseUiState.Failed("unknown", "自检失败，请重试")
            }
        }
    }
}

@Composable
internal fun DiagnoseSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: DiagnoseViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    DeepCard(modifier = modifier) {
        DeepCardTitle("自检（为什么连不上）")
        Spacer(Modifier.height(6.dp))
        DeepCardBody(
            "跑一次端到端检查：监听地址、渠道注册、可用账号、模型路由、虚拟组、网络环境、会话状态。" +
                "结果是这一刻的快照，不会持续宣称「系统正常」。",
        )
        Spacer(Modifier.height(12.dp))
        McaButton(
            onClick = viewModel::run,
            style = McaButtonStyle.Primary,
            enabled = status.running && state !is DiagnoseUiState.Running,
        ) { Text(if (state is DiagnoseUiState.Running) "自检中…" else "跑一次自检") }
        when (val current = state) {
            DiagnoseUiState.Idle, DiagnoseUiState.Running -> Unit
            is DiagnoseUiState.Failed -> {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = if (current.code == "gateway_not_running") {
                        "网关未启动：先回「概览」启动网关再自检。"
                    } else {
                        "自检失败：${current.message}（错误码：${current.code}）"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            is DiagnoseUiState.Ready -> {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = diagnoseSummaryLine(current.report),
                    style = MaterialTheme.typography.bodySmall,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                if (current.report.summary.isNotBlank()) {
                    Spacer(Modifier.height(6.dp))
                    DeepCardBody(current.report.summary)
                }
                diagnoseDisplayOrder(current.report).forEach { item ->
                    Spacer(Modifier.height(12.dp))
                    DiagnoseItemRow(item)
                }
            }
        }
    }
}

/** 级别文案：只映射这四档，未知级别原样显示，不猜。 */
internal fun diagnoseLevelLabel(level: String): String = when (level.lowercase()) {
    "error" -> "错误"
    "warn" -> "警告"
    "ok" -> "通过"
    else -> level.ifBlank { "未知" }
}

@Composable
private fun DiagnoseItemRow(item: DiagnoseItem) {
    val color = when (item.level.lowercase()) {
        "error" -> MaterialTheme.colorScheme.error
        "warn" -> MaterialTheme.colorScheme.tertiary
        "ok" -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    // 只有级别词染色，条目标题用正文色——整行染红会让 6 条并排时看不出重点。
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Top) {
        Text(
            text = "[${diagnoseLevelLabel(item.level)}]",
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Medium,
            color = color,
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(
                text = item.title,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            if (item.detail.isNotBlank()) {
                Text(
                    text = item.detail,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (item.suggestion.isNotBlank()) {
                Text(
                    text = "建议：${item.suggestion}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
