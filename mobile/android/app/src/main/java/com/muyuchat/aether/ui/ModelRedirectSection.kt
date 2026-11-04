package com.muyuchat.aether.ui

import android.app.Application
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
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ModelRedirect
import com.muyuchat.aether.runtime.ModelRedirectApiClient
import com.muyuchat.aether.runtime.modelRedirectLine
import com.muyuchat.aether.runtime.modelRedirectSummary
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

    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke(), modifier = modifier) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "模型重定向",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "同一个虚拟模型名在不同渠道上可以指向不同上游模型。改映射会直接改变路由结果，" +
                    "所以手机端只读；需要调整请到桌面端。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                enabled = running && state !is ModelRedirectUiState.Loading,
            ) { Text(if (state is ModelRedirectUiState.Loading) "读取中…" else "读取模型重定向") }
            when (val current = state) {
                ModelRedirectUiState.Idle, ModelRedirectUiState.Loading -> Unit
                is ModelRedirectUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = if (current.code == "gateway_not_running") {
                            "网关未启动：启动后才能读取模型重定向。"
                        } else {
                            "${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                is ModelRedirectUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = modelRedirectSummary(current.redirects),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    current.redirects.forEach { redirect ->
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "· ${modelRedirectLine(redirect)}",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}
