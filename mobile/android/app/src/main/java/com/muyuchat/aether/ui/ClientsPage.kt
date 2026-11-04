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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
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
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
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

/** 区块标题与卡片之间的视觉间距（标题上内距 12 + 这个 8 + 行内 16 ≈ 标题到首行 24dp）。 */
private val TitleToRows = 8.dp

/**
 * 客户端页（只读）。
 *
 * 排版（UI_REFRESH_SPEC §1）：页头 → 只读说明卡 → 一个区块「客户端」，客户端清单是
 * **一张** DeepCard 里的行（每行垂直 16dp），状态词右缘对齐；没有客户端时给空态，
 * 说清"为什么空"和"下一步做什么"，不写「暂无数据」。
 */
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
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                PageHeadline(
                    onBack = onBack,
                    onRefresh = viewModel::refresh,
                    refreshEnabled = state !is ClientsUiState.Loading,
                )
            }
        }
        item {
            DeepCard {
                DeepCardTitle("手机端只读")
                Spacer(Modifier.height(6.dp))
                DeepCardBody(
                    "「把网关配置写进客户端」要改桌面上的配置文件（Claude Code、Codex 等的 home 目录），" +
                        "手机上既没有那些路径也不该远程改。这里只显示客户端清单与检测状态；" +
                        "需要注入或还原请到桌面端。",
                )
            }
        }
        when (val current = state) {
            ClientsUiState.Idle, ClientsUiState.Loading -> item {
                DeepCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(22.dp),
                            strokeWidth = 2.dp,
                            color = MaterialTheme.colorScheme.primary,
                        )
                        Spacer(Modifier.width(12.dp))
                        Text(
                            text = "正在读取客户端清单…",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
            is ClientsUiState.Failed -> item {
                if (current.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "客户端清单来自本机 /api/clients；网关没起来时这个端点没有服务可应答。" +
                            "先启动网关，再回本页。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "读取失败",
                        detail = "${current.message}（错误码：${current.code}）。这次没有拿到清单，" +
                            "所以下面不显示上一次的旧数据。",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = viewModel::refresh,
                    )
                }
            }
            is ClientsUiState.Ready -> {
                val snapshot = current.snapshot
                val detected = snapshot.clients.count { it.detected }
                if (snapshot.clients.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "没有读到客户端",
                            detail = clientsEmptyDetail(),
                            actionLabel = "重试",
                            onAction = viewModel::refresh,
                        )
                    }
                } else {
                    item {
                        Column {
                            DeepSectionTitle(
                                text = "客户端",
                                trailing = "检测到 $detected / ${snapshot.clients.size}",
                            )
                            Spacer(Modifier.height(TitleToRows))
                            DeepCard(contentPadding = DeepListCardPadding) {
                                snapshot.clients.forEach { entry -> ClientRow(entry) }
                            }
                        }
                    }
                }
                item {
                    Text(
                        text = clientSummaryLine(snapshot),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/** 空态说明：为什么空（手机没有桌面客户端的家目录）+ 下一步（刷新/确认网关）。 */
internal fun clientsEmptyDetail(): String =
    "网关这次返回的客户端清单是空的。客户端条目来自桌面上的 Claude Code、Codex 等配置目录，" +
        "手机上没有这些目录，所以清单常见为空——这里不会去猜路径，也不会造一条出来。" +
        "点刷新再试一次；如果本机网关跑在有桌面客户端的机器上，请确认那边确实装了客户端。"

/** 状态词：只做二值映射，不改写服务端的 detected。 */
internal fun clientDetectedLabel(detected: Boolean): String =
    if (detected) "已检测到" else "未检测到"

/** 副行：类型与备份份数，都来自 `/api/clients`；两者都没有时返回空串，不占一行。 */
internal fun clientKindLine(entry: ClientEntry): String = buildList {
    if (entry.kind.isNotBlank()) {
        add(entry.kind)
    }
    if (entry.backups > 0) {
        add("${entry.backups} 份备份")
    }
}.joinToString(" · ")

@Composable
private fun PageHeadline(onBack: () -> Unit, onRefresh: () -> Unit, refreshEnabled: Boolean) {
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
            DeepPageTitle(
                title = "客户端",
                subtitle = "复制即用的接入片段 · 不会改动你的配置",
            )
        }
        McaIconButton(
            contentDescription = "刷新客户端",
            onClick = onRefresh,
            enabled = refreshEnabled,
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

/**
 * 一行客户端：名称/类型/备注在左（严格左缘对齐），检测状态在右（右缘对齐）。
 *
 * 行高沿用共享的 16dp 垂直内边距；状态词不换行、不被挤压——左侧信息列 `weight(1f)`。
 */
@Composable
private fun ClientRow(entry: ClientEntry) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.Top,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = entry.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            val kindLine = clientKindLine(entry)
            if (kindLine.isNotEmpty()) {
                Text(
                    text = kindLine,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (entry.note.isNotBlank()) {
                Text(
                    text = entry.note,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = clientDetectedLabel(entry.detected),
            style = MaterialTheme.typography.labelSmall,
            color = if (entry.detected) {
                MaterialTheme.colorScheme.primary
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            modifier = Modifier.padding(top = 4.dp),
        )
    }
}
