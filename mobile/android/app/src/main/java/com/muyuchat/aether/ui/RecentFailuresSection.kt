package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
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
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val CardPadding = 16.dp
private val RowGap = 12.dp
private val TightGap = 6.dp

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

    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = CardPadding, vertical = DeepCardVerticalPadding),
            verticalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Text(
                text = "最近异常",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "从本机的请求轨迹里筛出失败的那几条：哪次、哪个模型、上游报了什么。" +
                    "这是历史记录，不代表此刻的状态。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = status.running && state !is RecentFailuresUiState.Loading,
            ) { Text(if (state is RecentFailuresUiState.Loading) "读取中…" else "读取最近异常") }
            when (val current = state) {
                RecentFailuresUiState.Idle -> Text(
                    text = recentFailuresIdleHint(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                RecentFailuresUiState.Loading -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(TightGap),
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Text(
                        text = "正在读取请求轨迹…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is RecentFailuresUiState.Failed -> Text(
                    text = sectionErrorLine(current.code, current.message, "请求轨迹"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                is RecentFailuresUiState.Ready -> {
                    Text(
                        text = historySummaryLine(current.snapshot),
                        style = MaterialTheme.typography.bodySmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    if (current.snapshot.failures.isEmpty()) {
                        Text(
                            text = recentFailuresEmptyHint(),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else {
                        current.snapshot.failures.take(12).forEach { failure ->
                            FailureRow(failure, current.nowMs)
                        }
                    }
                }
            }
        }
    }
}

/**
 * 一条失败：**只有状态词**用 error 色，模型名与上游原因保持普通文字——
 * 整行染红会让人以为模型本身有问题，而这只是某一次调用的结果。
 */
@Composable
private fun FailureRow(failure: TraceFailure, nowMs: Long) {
    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Text(
                text = failure.model.ifBlank { "未知模型" },
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = failureStatusWord(failure.status, failure.attempts),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.End,
                maxLines = 1,
            )
        }
        Text(
            text = relativeTimeLabel(nowMs, failure.timeMs),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
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

/** 失败状态词：有 HTTP 码就带上，重试过就写清几次；没有码时不编数字。 */
internal fun failureStatusWord(status: Int, attempts: Int): String {
    val head = if (status > 0) "失败 · HTTP $status" else "失败"
    return if (attempts > 1) "$head · $attempts 次尝试" else head
}

/** 还没点过读取：说清这一步会做什么。 */
internal fun recentFailuresIdleHint(): String =
    "还没有读取。下一步：点「读取最近异常」从本机请求轨迹里筛一次（只读）。"

/** 读到空列表：为什么空 + 下一步。 */
internal fun recentFailuresEmptyHint(): String =
    "为什么空：这段轨迹里没有任何失败项——成功请求不会出现在这里。" +
        "下一步：发出一次请求后再读取，或到「请求」页按失败筛选这一天的记录。"
