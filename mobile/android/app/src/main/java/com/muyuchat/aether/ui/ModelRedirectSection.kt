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
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ModelRedirect
import com.muyuchat.aether.runtime.ModelRedirectApiClient
import com.muyuchat.aether.runtime.modelRedirectLine
import com.muyuchat.aether.runtime.modelRedirectSummary
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

sealed interface ModelRedirectUiState {
    data object Idle : ModelRedirectUiState
    data object Loading : ModelRedirectUiState
    data class Ready(val redirects: List<ModelRedirect>) : ModelRedirectUiState
    data class Failed(val code: String, val message: String) : ModelRedirectUiState
}

class ModelRedirectViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ModelRedirectApiClient()
    private val _state = MutableStateFlow<ModelRedirectUiState>(ModelRedirectUiState.Idle)
    val state: StateFlow<ModelRedirectUiState> = _state.asStateFlow()

    fun refresh() {
        if (_state.value is ModelRedirectUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = ModelRedirectUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = ModelRedirectUiState.Loading
        viewModelScope.launch {
            _state.value = try {
                ModelRedirectUiState.Ready(withContext(Dispatchers.IO) { client.fetchRedirects(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                ModelRedirectUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                ModelRedirectUiState.Failed("unknown", "读取模型重定向失败，请重试")
            }
        }
    }
}

/**
 * 模型重定向（只读）。
 *
 * 回答"这个虚拟模型名在某个渠道上到底走哪个上游模型"。改映射要 POST 同路径，
 * 会直接改变路由结果，所以手机端只读。
 */
@Composable
internal fun ModelRedirectSection(
    running: Boolean,
    modifier: Modifier = Modifier,
    viewModel: ModelRedirectViewModel = viewModel(),
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
                text = "模型重定向",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "同一个虚拟模型名在不同渠道上可以指向不同上游模型。改映射会直接改变路由结果，" +
                    "所以手机端只读；需要调整请到桌面端。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = running && state !is ModelRedirectUiState.Loading,
            ) { Text(if (state is ModelRedirectUiState.Loading) "读取中…" else "读取模型重定向") }
            when (val current = state) {
                ModelRedirectUiState.Idle -> Text(
                    text = modelRedirectIdleHint(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                ModelRedirectUiState.Loading -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(TightGap),
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Text(
                        text = "正在读取模型重定向…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is ModelRedirectUiState.Failed -> Text(
                    text = sectionErrorLine(current.code, current.message, "模型重定向"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                is ModelRedirectUiState.Ready -> {
                    Text(
                        text = modelRedirectSummary(current.redirects),
                        style = MaterialTheme.typography.bodySmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    if (current.redirects.isEmpty()) {
                        Text(
                            text = modelRedirectEmptyHint(),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else {
                        current.redirects.forEach { redirect ->
                            Text(
                                text = modelRedirectLine(redirect),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurface,
                            )
                        }
                    }
                }
            }
        }
    }
}

/** 还没点过读取：说清这一步会做什么。 */
internal fun modelRedirectIdleHint(): String =
    "还没有读取。下一步：点「读取模型重定向」看当前映射（只读，不会改路由）。"

/** 读到空列表：为什么空 + 下一步去哪儿改。 */
internal fun modelRedirectEmptyHint(): String =
    "为什么空：本机没有配置任何重定向，所有模型名都按原名直接走上游。" +
        "下一步：要新增或修改映射请到桌面端的路由设置，手机端只读。"
