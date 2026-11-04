package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.BuiltinPlugin
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PluginRule
import com.muyuchat.aether.runtime.PluginsApiClient
import com.muyuchat.aether.runtime.PluginsSnapshot
import com.muyuchat.aether.runtime.pluginRuleSummary
import com.muyuchat.aether.runtime.pluginRuleToggleJson
import com.muyuchat.aether.runtime.pluginRuleWithEdits
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch
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
 * 插件页状态。对标桌面 `misc.js:563-681` 的 plugins 视图：
 * 内置能力清单 + 改写规则清单（启用开关、配置入口、删除）。
 *
 * **新建规则仍留在桌面端**：一条改写规则的核心是 kind 专有字段（temperature / prompt /
 * words+patterns / from→to），手机上不给这些字段的编辑入口就无法建出有效规则；
 * 手机端只编辑可见字段（名称/说明/渠道与模型范围/启用），kind 专有字段原样回写不丢失。
 */
class PluginsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = PluginsApiClient()
    private val _state = MutableStateFlow<PluginsUiState>(PluginsUiState.Idle)
    val state: StateFlow<PluginsUiState> = _state.asStateFlow()
    private val _busyId = MutableStateFlow<String?>(null)
    val busyId: StateFlow<String?> = _busyId.asStateFlow()
    private val _actionError = MutableStateFlow<String?>(null)
    val actionError: StateFlow<String?> = _actionError.asStateFlow()

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
                PluginsUiState.Ready(withContext(Dispatchers.IO) { client.fetchPlugins(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                PluginsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                PluginsUiState.Failed("unknown", "读取插件列表失败，请重试")
            }
        }
    }

    fun dismissActionError() {
        _actionError.value = null
    }

    fun setEnabled(rule: PluginRule, enabled: Boolean) {
        write(rule.id, pluginRuleToggleJson(rule, enabled))
    }

    fun saveRule(rule: PluginRule, name: String, note: String, providers: List<String>, models: List<String>, enabled: Boolean) {
        write(
            rule.id,
            pluginRuleWithEdits(rule, name = name, note = note, providers = providers, models = models, enabled = enabled),
        )
    }

    fun deleteRule(rule: PluginRule) {
        if (_busyId.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        _busyId.value = rule.id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.deletePlugin(status, rule.id) }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "删除失败，请重试"
            } finally {
                _busyId.value = null
            }
        }
    }

    private fun write(id: String, json: String) {
        if (_busyId.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        _busyId.value = id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.savePlugin(status, json) }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "保存失败，请重试"
            } finally {
                _busyId.value = null
            }
        }
    }
}

/**
 * 插件页：桌面顺序 = 内置能力 → 改写规则（`misc.js:577-604`）。
 *
 * 桌面有的：列表字段（名称/kind/说明/范围/启用）、启用开关、编辑入口、删除、新建。
 * 手机端不做新建（kind 专有字段见 [PluginsViewModel] 注释），编辑只开放可见字段；
 * 供应商自定义图标没有列表接口，仍只在桌面端管理。
 */
@Composable
internal fun PluginsPage(
    status: GatewayStatus,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: PluginsViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val busyId by viewModel.busyId.collectAsStateWithLifecycle()
    val actionError by viewModel.actionError.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }
    var editing by remember { mutableStateOf<PluginRule?>(null) }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
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
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "请求改写规则与内置能力",
                        style = MaterialTheme.typography.bodySmall,
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
        actionError?.let { message ->
            item { DeepNotice(text = message, isError = true, onDismiss = viewModel::dismissActionError) }
        }
        when (val current = state) {
            PluginsUiState.Idle, PluginsUiState.Loading -> item {
                DeepStateCard(
                    title = "正在读取插件…",
                    detail = "内置能力与改写规则都来自本机 /api/plugins。",
                )
            }
            is PluginsUiState.Failed -> item {
                if (current.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "启动本地网关后才能读取本机插件。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "读取失败",
                        detail = "${current.message}（错误码：${current.code}）",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = viewModel::refresh,
                    )
                }
            }
            is PluginsUiState.Ready -> {
                val snapshot = current.snapshot
                // 桌面顺序：内置能力在前，改写规则在后（misc.js:577-604）。
                item {
                    DeepSectionTitle(
                        text = "内置能力",
                        trailing = "${snapshot.builtins.size} 项",
                    )
                }
                if (snapshot.builtins.isEmpty()) {
                    item { DeepStateCard(title = "没有内置能力", detail = "服务端 builtin 列表为空。") }
                } else {
                    items(snapshot.builtins) { builtin -> BuiltinPluginCard(builtin) }
                }
                item {
                    DeepSectionTitle(
                        text = "改写规则",
                        trailing = if (snapshot.rules.isEmpty()) "未配置" else pluginRulesCountLabel(snapshot.rules.size),
                    )
                }
                if (snapshot.rules.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "还没有改写规则",
                            detail = "本机没有改写规则时所有请求原样转发。新建规则要填 kind 专有字段（温度/提示词/关键词），" +
                                "只能在桌面端新建；新建后回本页可启用/停用与改名称、说明、范围。",
                            actionLabel = "刷新",
                            onAction = viewModel::refresh,
                        )
                    }
                } else {
                    items(snapshot.rules) { rule ->
                        PluginRuleCard(
                            rule = rule,
                            busy = busyId == rule.id,
                            onToggle = { enabled -> viewModel.setEnabled(rule, enabled) },
                            onConfigure = { editing = rule },
                        )
                    }
                }
                item {
                    Text(
                        text = "新建规则与 kind 专有字段（temperature / prompt / words / from→to）只在桌面端编辑；" +
                            "手机端保存时按服务端原 JSON 回写，不会清掉这些字段。供应商自定义图标没有列表接口，同样只在桌面端管理。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
    editing?.let { rule ->
        PluginRuleDialog(
            rule = rule,
            busy = busyId == rule.id,
            onDismiss = { editing = null },
            onSave = { name, note, providers, models, enabled ->
                viewModel.saveRule(rule, name, note, providers, models, enabled)
                editing = null
            },
            onDelete = {
                viewModel.deleteRule(rule)
                editing = null
            },
        )
    }
}

/** 规则计数文案（与设置页 countLabel 同款口径：0 条写「未配置」，不写「0 项」）。 */
internal fun pluginRulesCountLabel(count: Int): String = if (count == 0) "未配置" else "$count 条"

@Composable
private fun PluginRuleCard(
    rule: PluginRule,
    busy: Boolean,
    onToggle: (Boolean) -> Unit,
    onConfigure: () -> Unit,
) {
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
            Column(Modifier.weight(1f)) {
                Text(
                    text = rule.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                if (rule.kind.isNotBlank()) {
                    Text(
                        text = rule.kind,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.width(8.dp))
            McaSwitch(
                checked = rule.enabled,
                onCheckedChange = onToggle,
                enabled = !busy,
                contentDescription = if (rule.enabled) "停用规则 ${rule.name}" else "启用规则 ${rule.name}",
            )
        }
        Spacer(Modifier.height(6.dp))
        Text(
            text = pluginRuleSummary(rule),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (rule.note.isNotBlank()) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = rule.note,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(10.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Spacer(Modifier.weight(1f))
            McaButton(
                onClick = onConfigure,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text("配置") }
        }
    }
}

/** 配置入口：只开放可见字段；kind 专有字段只读展示，保存时原样带回。 */
@Composable
private fun PluginRuleDialog(
    rule: PluginRule,
    busy: Boolean,
    onDismiss: () -> Unit,
    onSave: (String, String, List<String>, List<String>, Boolean) -> Unit,
    onDelete: () -> Unit,
) {
    var name by remember(rule.id) { mutableStateOf(rule.name) }
    var note by remember(rule.id) { mutableStateOf(rule.note) }
    var providers by remember(rule.id) { mutableStateOf(rule.providers.joinToString(",")) }
    var models by remember(rule.id) { mutableStateOf(rule.models.joinToString(",")) }
    var enabled by remember(rule.id) { mutableStateOf(rule.enabled) }
    var confirmDelete by remember(rule.id) { mutableStateOf(false) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("配置规则", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(
                    text = "kind ${rule.kind.ifBlank { "—" }} · ${pluginRuleSummary(rule)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(10.dp))
                PluginField("名称", name) { name = it }
                PluginField("说明（可选）", note) { note = it }
                PluginField("渠道范围（逗号分隔；空=全部）", providers) { providers = it }
                PluginField("模型范围（逗号分隔；支持 *；空=全部）", models) { models = it }
                Spacer(Modifier.height(6.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = "启用",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    McaSwitch(
                        checked = enabled,
                        onCheckedChange = { enabled = it },
                        contentDescription = "启用规则 ${rule.name}",
                    )
                }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "kind 专有字段（温度 / max_tokens / 提示词 / 关键词 / from→to）只能在桌面端编辑；这里保存时按原样回写，不会清空。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (confirmDelete) {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = "删除规则「${rule.name}」？删除后这条改写立即停止生效。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    onSave(
                        name,
                        note,
                        splitTagList(providers),
                        splitTagList(models),
                        enabled,
                    )
                },
                style = McaButtonStyle.Primary,
                enabled = !busy,
            ) { Text(if (busy) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(
                onClick = {
                    if (confirmDelete) onDelete() else confirmDelete = true
                },
                style = McaButtonStyle.Outlined,
                enabled = !busy,
            ) { Text(if (confirmDelete) "确认删除" else "删除") }
        },
    )
}

/** 逗号分隔的范围列表：中英文逗号都认，去空。 */
internal fun splitTagList(text: String): List<String> =
    text.split(',', '，').map { it.trim() }.filter { it.isNotEmpty() }

@Composable
private fun PluginField(label: String, value: String, onValueChange: (String) -> Unit) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .padding(horizontal = 12.dp),
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
            Spacer(Modifier.height(6.dp))
            Text(
                text = builtin.desc,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
