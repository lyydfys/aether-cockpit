package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.BuiltinPlugin
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PluginRule
import com.muyuchat.aether.runtime.PluginsApiClient
import com.muyuchat.aether.runtime.PluginsSnapshot
import com.muyuchat.aether.runtime.pluginRuleSummary
import com.muyuchat.aether.runtime.pluginsSummaryLine
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import androidx.compose.ui.res.painterResource
import com.muyuchat.aether.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface PluginsUiState {
    data object Idle : PluginsUiState
    data object Loading : PluginsUiState
    data class Ready(val snapshot: PluginsSnapshot) : PluginsUiState
    data class Failed(val code: String, val message: String) : PluginsUiState
}

/**
 * 插件页状态。只读：新增/编辑/删除规则都留在桌面端——手机上误改一条改写规则，
 * 会让所有请求悄悄变形，代价远大于"少一个入口"。
 */
class PluginsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = PluginsApiClient()
    private val _state = MutableStateFlow<PluginsUiState>(PluginsUiState.Idle)
    val state: StateFlow<PluginsUiState> = _state.asStateFlow()

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = PluginsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (_state.value !is PluginsUiState.Ready) {
            _state.value = PluginsUiState.Loading
        }
        viewModelScope.launch {
            _state.value = try {
                val snapshot = withContext(Dispatchers.IO) { client.fetchPlugins(status) }
                PluginsUiState.Ready(snapshot)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                PluginsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                PluginsUiState.Failed("unknown", "读取插件列表失败，请重试")
            }
        }
    }
}

@Composable
internal fun PluginsPage(
    status: GatewayStatus,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: PluginsViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
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
                        text = "插件",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "请求改写规则 · 只读",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新插件",
                    onClick = viewModel::refresh,
                    enabled = state !is PluginsUiState.Loading,
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
        item { DeepSectionTitle("手机端只读") }
        item {
            DeepCard {
                DeepCardBody(
                    "新增、编辑、删除插件规则都在桌面端完成：改写规则一旦改错，会让所有请求悄悄变形，" +
                        "手机上不适合顺手改。供应商自定义图标也没有列表接口（只有上传与按名取图），" +
                        "同样只能在桌面端管理。",
                )
            }
        }
        when (val current = state) {
            PluginsUiState.Idle, PluginsUiState.Loading -> item {
                DeepCardBody("正在读取插件…")
            }
            is PluginsUiState.Failed -> item {
                DeepStateCard(
                    title = if (current.code == "gateway_not_running") "网关未启动" else "读取失败",
                    detail = if (current.code == "gateway_not_running") {
                        "启动本地网关后才能读取本机插件。"
                    } else {
                        "${current.message}（错误码：${current.code}）"
                    },
                    tone = if (current.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
                    actionLabel = if (current.code == "gateway_not_running") "前往概览" else "重试",
                    onAction = if (current.code == "gateway_not_running") onOpenOverview else viewModel::refresh,
                )
            }
            is PluginsUiState.Ready -> {
                val snapshot = current.snapshot
                item {
                    DeepSectionTitle(
                        text = "自定义规则",
                        trailing = if (snapshot.rules.isEmpty()) "未配置" else pluginRulesCountLabel(snapshot.rules.size),
                    )
                }
                if (snapshot.rules.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "还没有自定义规则",
                            detail = "本机没有改写规则时所有请求原样转发。规则只能在桌面端新增或编辑。",
                            actionLabel = "刷新",
                            onAction = viewModel::refresh,
                        )
                    }
                } else {
                    items(snapshot.rules) { rule -> PluginRuleCard(rule) }
                }
                if (snapshot.builtins.isNotEmpty()) {
                    item {
                        DeepSectionTitle(
                            text = "内置插件",
                            trailing = "${snapshot.builtins.size} 个",
                        )
                    }
                    items(snapshot.builtins) { builtin -> BuiltinPluginCard(builtin) }
                }
            }
        }
    }
}

/** 规则计数文案（与设置页 countLabel 同款口径：0 条写「未配置」，不写「0 项」）。 */
internal fun pluginRulesCountLabel(count: Int): String = if (count == 0) "未配置" else "$count 条"

@Composable
private fun PluginRuleCard(rule: PluginRule) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .size(8.dp)
                    .clip(CircleShape)
                    .background(
                        if (rule.enabled) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.4f)
                        },
                    ),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = rule.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.weight(1f),
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (rule.kind.isNotBlank()) {
                Spacer(Modifier.width(8.dp))
                Text(
                    text = rule.kind,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            text = pluginRuleSummary(rule),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (rule.note.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = rule.note,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun BuiltinPluginCard(builtin: BuiltinPlugin) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = builtin.name,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (builtin.on) "内置 · 开启" else "内置 · 关闭",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (builtin.desc.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = builtin.desc,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
