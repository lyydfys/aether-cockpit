package com.muyuchat.aether.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PresetDraft
import com.muyuchat.aether.runtime.PresetSummary
import com.muyuchat.aether.runtime.PresetsSnapshot
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.TavernPresetPreview
import com.muyuchat.aether.runtime.presetDraftFrom
import com.muyuchat.aether.runtime.validatePresetDraft
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 设置 → 预设，按桌面 `settings.js:847-884`（列表）与 `:887-931`（抽屉）1:1 重做。
 *
 * 契约（已核 `request_presets.go:23-33`、`:345-354`）：`body` 是 JSON 对象、`headers` 是
 * 字符串→字符串对象；指针字段"出现即替换/缺省即保留"，所以编辑必须**整体回填 + 整体提交**
 * （只提交改动的键会静默清掉未展示字段，同 `guard_accounts` 事故）；`id` 由服务端按名称生成，
 * 桌面抽屉里**没有 ID 字段**，这里也不放。
 */
@Composable
internal fun SettingsPresetsSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var snapshot by remember { mutableStateOf<PresetsSnapshot?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var busyId by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    var pendingDelete by remember { mutableStateOf<PresetSummary?>(null) }
    var editing by remember { mutableStateOf<PresetDraft?>(null) }

    // —— 酒馆预设导入（解析在 Go 侧：POST /api/presets/import/preview，只读） ——
    val context = LocalContext.current
    var preview by remember { mutableStateOf<TavernPresetPreview?>(null) }
    var previewIncludePrompt by remember { mutableStateOf(true) }
    var importError by remember { mutableStateOf<String?>(null) }
    var importing by remember { mutableStateOf(false) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri == null) {
            return@rememberLauncherForActivityResult
        }
        scope.launch {
            val text = withContext(Dispatchers.IO) {
                runCatching {
                    context.contentResolver.openInputStream(uri)?.use { it.readBytes().toString(Charsets.UTF_8) }
                }.getOrNull()
            }
            if (text.isNullOrBlank()) {
                importError = "读取失败：文件为空或没有读取权限"
                return@launch
            }
            try {
                val result = client.previewTavernPreset(status, text, true)
                preview = result
                // 超限时强制关掉提示词：宁可只导参数，也不截断人设
                previewIncludePrompt = result.canImportPrompt
                importError = null
            } catch (e: ManagementApiException) {
                importError = "解析失败：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                importError = "解析失败，请重试"
            }
        }
    }

    fun reload() {
        if (!status.running) {
            snapshot = null
            return
        }
        scope.launch {
            try {
                snapshot = client.fetchPresets(status)
                error = null
            } catch (e: ManagementApiException) {
                error = "读不到预设：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                error = "读不到预设，请重试"
            }
        }
    }

    LaunchedEffect(status.running, status.addr) { reload() }

    DeepCard(modifier = modifier) {
        // 卡片头：标题 + 计数 chip 独占一行（1.3× 下再挤两个按钮会把标题压折）。
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("请求预设")
            Spacer(Modifier.width(8.dp))
            McaChip(
                selected = false,
                enabled = false,
                onClick = {},
                label = { Text("${snapshot?.presets?.size ?: 0} 个") },
            )
        }
        Spacer(Modifier.height(8.dp))
        // 动作分两行、各占满宽：主「新建预设」在上（Primary），次「导入预设」在下（Outlined），
        // 视觉权重明确区分，且任何字号下都不会被挤成竖排。
        McaButton(
            onClick = { editing = PresetDraft() },
            modifier = Modifier.fillMaxWidth(),
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = status.running,
        ) { Text("新建预设") }
        Spacer(Modifier.height(8.dp))
        McaButton(
            onClick = { picker.launch(arrayOf("application/json", "text/plain", "*/*")) },
            modifier = Modifier.fillMaxWidth(),
            style = McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
            enabled = status.running,
        ) { Text("导入预设") }
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取预设。")
            return@DeepCard
        }
        error?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            McaButton(onClick = { reload() }, style = McaButtonStyle.Outlined) { Text("重试") }
        }
        notice?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
        importError?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        val list = snapshot?.presets.orEmpty()
        if (error == null && snapshot == null) {
            DeepCardBody("正在读取预设…")
            return@DeepCard
        }
        if (list.isEmpty()) {
            // 桌面原话（settings.js:851），不自造
            Text(
                text = "还没有预设",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "新建一个 Body / Headers 预设，然后在渠道或路由组里勾选。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            list.forEach { preset ->
                PresetRow(
                    preset = preset,
                    busy = busyId == preset.id,
                    onToggle = { enabled ->
                        if (busyId != null) {
                            return@PresetRow
                        }
                        val current = snapshot ?: return@PresetRow
                        busyId = preset.id
                        scope.launch {
                            try {
                                snapshot = client.togglePreset(status, current, preset.id, enabled)
                                error = null
                                notice = if (enabled) "预设已启用" else "预设已停用"
                            } catch (e: ManagementApiException) {
                                error = "切换失败：${e.message}（错误码：${e.code}）"
                            } catch (_: Exception) {
                                error = "切换失败，请重试"
                            } finally {
                                busyId = null
                            }
                        }
                    },
                    onEdit = {
                        // 整体回填：body/headers 用服务端返回的完整对象（toString(2)），不做字段白名单
                        val raw = snapshot?.rawBody.orEmpty()
                        editing = presetDraftFrom(raw, preset.id) ?: PresetDraft(
                            id = preset.id,
                            name = preset.name,
                            description = preset.description,
                            enabled = preset.enabled,
                        )
                    },
                    onDelete = { pendingDelete = preset },
                )
            }
        }
        // 桌面页脚（settings.js:884）
        Text(
            text = "预设只做静态 Body / Headers 合并；停用或没有挂载时不会生效。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }

    editing?.let { initial ->
        PresetEditorDialog(
            initial = initial,
            saving = saving,
            onDismiss = { editing = null },
            onSave = { draft ->
                if (saving) {
                    return@PresetEditorDialog
                }
                saving = true
                scope.launch {
                    try {
                        val saved = client.savePreset(status, draft)
                        // 重拉确认：列表以后端为准，回执用服务端返回的名字
                        snapshot = client.fetchPresets(status)
                        val savedName = saved.presets.firstOrNull { it.id == draft.id }?.name
                            ?: saved.presets.firstOrNull { it.name == draft.name.trim() }?.name
                            ?: draft.name.trim()
                        error = null
                        notice = "已保存「$savedName」"
                        editing = null
                    } catch (e: ManagementApiException) {
                        error = "保存失败：${e.message}（错误码：${e.code}）"
                    } catch (_: Exception) {
                        error = "保存失败，请重试"
                    } finally {
                        saving = false
                    }
                }
            },
        )
    }

    preview?.let { result ->
        McaDialog(
            onDismissRequest = { if (!importing) preview = null },
            title = {
                Text(
                    text = "导入预设 · ${result.name.ifBlank { "未命名" }}",
                    fontWeight = FontWeight.Bold,
                )
            },
            text = {
                Column(
                    modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Text(
                        text = "来源格式：${tavernFormatLabel(result.format)}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    // —— 提示词：可开关；超限强制只导参数 ——
                    when {
                        result.systemPrompt.isBlank() -> Text(
                            text = "该文件没有可导入的提示词（prompts 里没有启用且有内容的段落）。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        result.promptOverflow -> Text(
                            text = tavernPromptSummary(result),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        else -> Row(verticalAlignment = Alignment.CenterVertically) {
                            McaSwitch(
                                checked = previewIncludePrompt,
                                enabled = !importing,
                                contentDescription = "导入提示词",
                                onCheckedChange = { previewIncludePrompt = it },
                            )
                            Spacer(Modifier.width(8.dp))
                            Text(
                                text = tavernPromptSummary(result),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    // —— 将写入的采样参数 ——
                    if (result.mapped.isNotEmpty()) {
                        Text(
                            text = "将写入采样参数（${result.mapped.size}）",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        result.mapped.forEach { item ->
                            Text(
                                text = "· ${item.key} ← ${item.source}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    // —— 丢弃项按组展示（每组都带原因） ——
                    listOf("upstream", "client", "gateway", "unknown").forEach { group ->
                        val items = result.dropped.filter { it.group == group }
                        if (items.isNotEmpty()) {
                            Text(
                                text = "${tavernDropGroupLabel(group)}（${items.size}）",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurface,
                                modifier = Modifier.padding(top = 4.dp),
                            )
                            items.forEach { item ->
                                Text(
                                    text = "· ${item.key} — ${item.reason}",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                    Text(
                        text = "导入后可以在列表里继续编辑；Body 里不会包含提示词。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        if (importing) {
                            return@McaButton
                        }
                        val draft = PresetDraft(
                            name = result.name.ifBlank { "导入的预设" },
                            description = "从酒馆预设导入（${result.format}）",
                            enabled = true,
                            systemPrompt = if (previewIncludePrompt && result.canImportPrompt) {
                                result.systemPrompt
                            } else {
                                ""
                            },
                            bodyJson = result.bodyJson,
                            headersJson = "{}",
                        )
                        importing = true
                        scope.launch {
                            try {
                                val saved = client.savePreset(status, draft)
                                snapshot = client.fetchPresets(status)
                                val savedName = saved.presets.firstOrNull { it.name == draft.name }?.name
                                    ?: draft.name
                                error = null
                                notice = "已导入「$savedName」"
                                preview = null
                            } catch (e: ManagementApiException) {
                                importError = "保存失败：${e.message}（错误码：${e.code}）"
                            } catch (_: Exception) {
                                importError = "保存失败，请重试"
                            } finally {
                                importing = false
                            }
                        }
                    },
                    style = McaButtonStyle.Primary,
                    enabled = !importing,
                ) { Text(if (importing) "导入中…" else "导入") }
            },
            dismissButton = {
                McaButton(onClick = { preview = null }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }

    pendingDelete?.let { preset ->
        // 文案对齐桌面 settings.js:875
        McaDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("删除预设「${preset.name}」？", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "渠道/路由上的挂载会一并移除。",
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val target = preset
                        pendingDelete = null
                        if (busyId != null) {
                            return@McaButton
                        }
                        busyId = target.id
                        scope.launch {
                            try {
                                snapshot = client.deletePreset(status, target.id)
                                error = null
                                notice = "已删除"
                            } catch (e: ManagementApiException) {
                                error = "删除失败：${e.message}（错误码：${e.code}）"
                            } catch (_: Exception) {
                                error = "删除失败，请重试"
                            } finally {
                                busyId = null
                            }
                        }
                    },
                    style = McaButtonStyle.Outlined,
                    contentColor = MaterialTheme.colorScheme.error,
                ) { Text("删除") }
            },
            dismissButton = {
                McaButton(onClick = { pendingDelete = null }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }
}

/**
 * 编辑抽屉（桌面 `settings.js:898-929`）：字段顺序与标签逐条照抄，
 * **没有 ID 字段**（服务端按名称生成）。
 *
 * 保存按钮**始终可点**：桌面同样是"点击后校验 + 提示"，不是禁用；
 * 校验顺序也照桌面：名称 → Body JSON → Headers JSON → 对象形状。
 */
@Composable
private fun PresetEditorDialog(
    initial: PresetDraft,
    saving: Boolean,
    onDismiss: () -> Unit,
    onSave: (PresetDraft) -> Unit,
) {
    var draft by remember(initial) { mutableStateOf(initial) }
    var submitError by remember(initial) { mutableStateOf<String?>(null) }
    val liveError = validatePresetDraft(draft)
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = if (initial.id.isBlank()) "新建预设" else "编辑预设 · ${initial.name}",
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                PresetFieldLabel("名称")
                SettingsLineField(
                    value = draft.name,
                    onValueChange = { draft = draft.copy(name = it); submitError = null },
                    enabled = !saving,
                    placeholder = "例如：Codex 缓存",
                )
                PresetFieldLabel("说明（可选）")
                SettingsLineField(
                    value = draft.description,
                    onValueChange = { draft = draft.copy(description = it) },
                    enabled = !saving,
                    placeholder = "可选",
                )
                PresetSwitchRow(
                    label = "启用",
                    checked = draft.enabled,
                    enabled = !saving,
                    onCheckedChange = { draft = draft.copy(enabled = it) },
                )
                PresetFieldLabel("系统提示词")
                PresetMultilineField(
                    value = draft.systemPrompt,
                    onValueChange = { draft = draft.copy(systemPrompt = it) },
                    enabled = !saving,
                    placeholder = "输入系统提示词（用户提示词优先）",
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    McaSwitch(
                        checked = draft.concatSystem,
                        enabled = !saving,
                        contentDescription = "提示词拼接",
                        onCheckedChange = { draft = draft.copy(concatSystem = it) },
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = "提示词拼接：开启后与用户提示词拼接",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                PresetFieldLabel("Body（JSON 对象）")
                SettingsJsonField(
                    value = draft.bodyJson,
                    onValueChange = { draft = draft.copy(bodyJson = it); submitError = null },
                    enabled = !saving,
                    minHeight = 120.dp,
                )
                PresetFieldLabel("Headers（JSON 对象）")
                SettingsJsonField(
                    value = draft.headersJson,
                    onValueChange = { draft = draft.copy(headersJson = it); submitError = null },
                    enabled = !saving,
                    minHeight = 96.dp,
                )
                // 桌面帮助文案（settings.js:907）原样
                Text(
                    text = "Body 不能覆盖 model / stream / messages / tools 等网关自管字段；" +
                        "不要放长期密钥；Headers 里的敏感值会打码回显；同名 Header 不会覆盖渠道代码已设置的头。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                (submitError ?: liveError)?.let {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    val problem = validatePresetDraft(draft)
                    if (problem != null) {
                        submitError = problem
                        return@McaButton
                    }
                    submitError = null
                    onSave(draft)
                },
                style = McaButtonStyle.Primary,
                enabled = !saving,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

@Composable
private fun PresetFieldLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 4.dp),
    )
}

@Composable
private fun PresetSwitchRow(
    label: String,
    checked: Boolean,
    enabled: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = label,
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
        )
        McaSwitch(
            checked = checked,
            enabled = enabled,
            contentDescription = label,
            onCheckedChange = onCheckedChange,
        )
    }
}

/** 自由文本多行输入（系统提示词用；JSON 用 [SettingsJsonField]）。 */
@Composable
private fun PresetMultilineField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    placeholder: String,
) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            textStyle = TextStyle(
                color = MaterialTheme.colorScheme.onSurface,
                fontSize = MaterialTheme.typography.bodySmall.fontSize,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier.fillMaxWidth().heightIn(min = 72.dp).padding(12.dp),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty()) {
                        Text(
                            text = placeholder,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    inner()
                }
            },
        )
    }
}

/**
 * 一行预设，字段顺序照桌面 `settings.js:865-872`：
 * 名称 → 启用开关 → `Body N` chip → `Headers N` chip → `提示词` chip → 挂载数 → 编辑 → 删除。
 */
@Composable
private fun PresetRow(
    preset: PresetSummary,
    busy: Boolean,
    onToggle: (Boolean) -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    Column(
        Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = preset.name,
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            McaSwitch(
                checked = preset.enabled,
                enabled = !busy,
                contentDescription = "启用预设 ${preset.name}",
                onCheckedChange = onToggle,
            )
        }
        Row(
            modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            McaChip(selected = false, enabled = false, onClick = {}, label = { Text("Body ${preset.bodyCount}") })
            McaChip(selected = false, enabled = false, onClick = {}, label = { Text("Headers ${preset.headerCount}") })
            if (preset.hasSystemPrompt) {
                McaChip(selected = true, enabled = false, onClick = {}, label = { Text("提示词") })
            }
            Text(
                text = presetAttachedLabel(preset),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Spacer(Modifier.weight(1f))
            McaButton(
                onClick = onEdit,
                style = McaButtonStyle.Text,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text("编辑") }
            McaButton(
                onClick = onDelete,
                style = McaButtonStyle.Text,
                size = McaButtonSize.CardAction,
                contentColor = MaterialTheme.colorScheme.error,
                enabled = !busy,
            ) { Text("删除") }
        }
        // 桌面 settings.js:882：说明单独一行
        if (preset.description.isNotBlank()) {
            Text(
                text = preset.description,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** 挂载数文案（桌面 `settings.js:857`）：`N 渠道 · N 路由` 或「未挂载」。 */
internal fun presetAttachedLabel(preset: PresetSummary): String {
    val parts = buildList {
        if (preset.attachedProviders > 0) add("${preset.attachedProviders} 渠道")
        if (preset.attachedRoutes > 0) add("${preset.attachedRoutes} 路由")
    }
    return parts.joinToString(" · ").ifBlank { "未挂载" }
}

/** 行内一行式摘要（保留给单测与窄屏使用）。 */
internal fun presetMetaLine(preset: PresetSummary): String {
    val prompt = if (preset.hasSystemPrompt) " · 提示词" else ""
    return "Body ${preset.bodyCount} · Headers ${preset.headerCount}$prompt · ${presetAttachedLabel(preset)}"
}

internal fun presetCountLabel(count: Int): String = if (count <= 0) "未配置" else "$count 个"

/** 空态文案与桌面 `settings.js:850-851` 同一句（不自造）。 */
internal fun presetEmptyCopy(): String =
    "还没有预设：新建一个 Body / Headers 预设，然后在渠道或路由组里勾选。"

/** 导入来源格式的人类可读文案。 */
internal fun tavernFormatLabel(format: String): String = when (format.trim().lowercase()) {
    "chat" -> "Chat Completion 预设"
    "text" -> "Text Generation 预设"
    else -> "未识别格式（按通用字段解析）"
}

/** 丢弃分组标题：让用户分得清"网关自管"与"上游不支持"。 */
internal fun tavernDropGroupLabel(group: String): String = when (group.trim().lowercase()) {
    "gateway" -> "网关自管（写进预设会被拒绝）"
    "upstream" -> "上游不通用（发了会 400 或被忽略）"
    "client" -> "客户端行为（不是请求参数）"
    else -> "未识别字段"
}

/** 提示词一句话摘要：正常给段数/字数，超限时明确说"只能导入采样参数"。 */
internal fun tavernPromptSummary(preview: TavernPresetPreview): String = if (preview.promptOverflow) {
    "提示词 ${preview.promptChars} 字超过预设 32KB 上限，本次只能导入采样参数（不截断）"
} else {
    "将写入 system_prompt：${preview.promptSegments} 段（共 ${preview.promptChars} 字）"
}
