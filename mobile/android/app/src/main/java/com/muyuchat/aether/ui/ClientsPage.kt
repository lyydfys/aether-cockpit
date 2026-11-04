package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.ClientEntry
import com.muyuchat.aether.runtime.ClientsApiClient
import com.muyuchat.aether.runtime.ClientsSnapshot
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.clientSummaryLine
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface ClientsUiState {
    data object Idle : ClientsUiState
    data object Loading : ClientsUiState
    data class Ready(val snapshot: ClientsSnapshot) : ClientsUiState
    data class Failed(val code: String, val message: String) : ClientsUiState
}

/**
 * 客户端页状态。只读：把配置写进客户端（apply/restore）要改**桌面**的配置文件，
 * 手机上没有那些路径，也不该远程改。
 */
class ClientsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ClientsApiClient()
    private val _state = MutableStateFlow<ClientsUiState>(ClientsUiState.Idle)
    val state: StateFlow<ClientsUiState> = _state.asStateFlow()

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = ClientsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (_state.value !is ClientsUiState.Ready) {
            _state.value = ClientsUiState.Loading
        }
        viewModelScope.launch {
            _state.value = try {
                ClientsUiState.Ready(withContext(Dispatchers.IO) { client.fetchClients(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                ClientsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                ClientsUiState.Failed("unknown", "读取客户端列表失败，请重试")
            }
        }
    }
}

@Composable
internal fun ClientsPage(
    status: GatewayStatus,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ClientsViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                McaIconButton(contentDescription = "返回", onClick = onBack) {
                    Icon(
                        painter = painterResource(R.drawable.ic_back),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onBackground,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "客户端",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "接入片段 · 只读",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新客户端",
                    onClick = viewModel::refresh,
                    enabled = state !is ClientsUiState.Loading,
                ) {
                    Icon(
                        painter = painterResource(R.drawable.ic_refresh),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
            }
        }
        item {
            Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke()) {
                Column(Modifier.fillMaxWidth().padding(14.dp)) {
                    Text(
                        text = "手机端只读",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "「把网关配置写进客户端」要改桌面上的配置文件（Claude Code、Codex 等的 home 目录），" +
                            "手机上既没有那些路径也不该远程改。这里只显示客户端清单与检测状态；" +
                            "需要注入或还原请到桌面端。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        when (val current = state) {
            ClientsUiState.Idle, ClientsUiState.Loading -> item {
                Text(
                    text = "正在读取客户端…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is ClientsUiState.Failed -> item {
                Column {
                    Text(
                        text = if (current.code == "gateway_not_running") {
                            "网关未启动：启动后才能读取客户端清单。"
                        } else {
                            "读取失败：${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(8.dp))
                    McaButton(
                        onClick = if (current.code == "gateway_not_running") {
                            { onOpenOverview() }
                        } else {
                            { viewModel.refresh() }
                        },
                        style = McaButtonStyle.Outlined,
                    ) { Text(if (current.code == "gateway_not_running") "前往概览" else "重试") }
                }
            }
            is ClientsUiState.Ready -> {
                val snapshot = current.snapshot
                item {
                    Text(
                        text = clientSummaryLine(snapshot),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (snapshot.clients.isNotEmpty()) {
                    items(snapshot.clients) { entry -> ClientCard(entry) }
                }
            }
        }
    }
}

@Composable
private fun ClientCard(entry: ClientEntry) {
    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke()) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = entry.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    modifier = Modifier.weight(1f),
                )
                if (entry.kind.isNotBlank()) {
                    Text(
                        text = entry.kind,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(4.dp))
            Text(
                text = buildString {
                    append(if (entry.detected) "已检测到" else "未检测到")
                    if (entry.backups > 0) {
                        append(" · ${entry.backups} 份备份")
                    }
                    append(" · 注入仅桌面端")
                },
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (entry.note.isNotBlank()) {
                Spacer(Modifier.height(2.dp))
                Text(
                    text = entry.note,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
