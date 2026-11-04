package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
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
import com.muyuchat.aether.runtime.presetDraftFrom
import com.muyuchat.aether.runtime.validatePresetDraft
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.launch

/**
 * 设置 → 预设（桌面 `settings.js:847-884` 列表 + `:887-931` 编辑抽屉）。
 *
 * 结构已核（`request_presets.go:23-33`、`:345-354`）：
 *  - `body` 是 **JSON 对象**、`headers` 是**字符串→字符串对象**（都不是字符串）；
 *  - 保存输入用指针字段：**出现即替换、缺省即保留**。所以编辑时必须把服务端返回的
 *    完整 body/headers 回填后整体提交——只提交改动过的键会静默清掉未展示字段
 *    （上轮 `guard_accounts` 就是这个坑的同类）；
 *  - `id` 留空由服务端按名称生成；非 slug 会 400，本地先用 `validatePresetDraft` 拦。
 *
 * 表单里 Body/Headers 直接展示服务端返回的**完整对象**，用户看到什么就写回什么。
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
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("请求预设")
            Spacer(Modifier.weight(1f))
            Text(
                text = presetCountLabel(snapshot?.presets?.size ?: 0),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
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
        val list = snapshot?.presets.orEmpty()
        if (error == null && snapshot == null) {
            DeepCardBody("正在读取预设…")
            return@DeepCard
        }
        if (list.isEmpty()) {
            Text(
                text = presetEmptyCopy(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            list.forEach { preset ->
                PresetRow(
                    preset = preset,
                    busy = busyId == preset.id,
                    onToggle = {
                        if (busyId != null) {
                            return@PresetRow
                        }
                        val current = snapshot ?: return@PresetRow
                        busyId = preset.id
                        scope.launch {
                            try {
                                snapshot = client.togglePreset(status, current, preset.id, !preset.enabled)
                                error = null
                                notice = if (preset.enabled) "已停用「${preset.name}」" else "已启用「${preset.name}」"
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
                        // 关键：整对象回填（body/headers 是服务端返回的**完整**对象），不做字段白名单。
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
        Spacer(Modifier.height(12.dp))
        McaButton(
            onClick = { editing = PresetDraft() },
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = !saving,
        ) { Text("新建预设") }
        Text(
            text = "预设只做静态 Body / Headers 合并；停用或没有挂载时不会生效。" +
                "Body 不能覆盖 model / stream / messages / tools 等网关自管字段。",
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
                        client.savePreset(status, draft)
                        // 重拉确认：列表以后端为准，不用本地回显
                        snapshot = client.fetchPresets(status)
                        error = null
                        notice = if (draft.id.isBlank()) "已新建「${draft.name}」" else "已保存「${draft.name}」"
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

    pendingDelete?.let { preset ->
        McaDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("删除预设", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "删除「${preset.name}」？渠道/路由上的挂载会一并移除。",
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
                                notice = "已删除「${target.name}」"
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
 * 新建 / 编辑弹窗。
 *
 * Body / Headers 用等宽多行输入，内容就是**服务端返回的完整 JSON 对象**；
 * 校验失败时显示带行号的原因并**禁用保存**（`validatePresetDraft` + `jsonErrorMessage`）。
 */
@Composable
private fun PresetEditorDialog(
    initial: PresetDraft,
    saving: Boolean,
    onDismiss: () -> Unit,
    onSave: (PresetDraft) -> Unit,
) {
    var draft by remember(initial) { mutableStateOf(initial) }
    val validation = validatePresetDraft(draft)
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = if (initial.id.isBlank()) "新建预设" else "编辑预设 · ${initial.name}",
                style = MaterialTheme.typography.titleLarge,
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
                    onValueChange = { draft = draft.copy(name = it) },
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
                PresetFieldLabel("ID（留空 = 按名称自动生成；只能用小写字母、数字、连字符）")
                SettingsLineField(
                    value = draft.id,
                    onValueChange = { draft = draft.copy(id = it.filter { ch -> ch.isLetterOrDigit() || ch == '-' }.lowercase()) },
                    enabled = !saving && initial.id.isBlank(),
                    placeholder = "留空即可",
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaChip(
                        selected = draft.enabled,
                        enabled = !saving,
                        onClick = { draft = draft.copy(enabled = !draft.enabled) },
                        label = { Text(if (draft.enabled) "已启用" else "已停用") },
                    )
                    McaChip(
                        selected = draft.concatSystem,
                        enabled = !saving,
                        onClick = { draft = draft.copy(concatSystem = !draft.concatSystem) },
                        label = { Text(if (draft.concatSystem) "提示词拼接" else "替换提示词") },
                    )
                }
                PresetFieldLabel("系统提示词（可空）")
                PresetMultilineField(
                    value = draft.systemPrompt,
                    onValueChange = { draft = draft.copy(systemPrompt = it) },
                    enabled = !saving,
                    placeholder = "输入系统提示词（用户提示词优先）",
                    monospace = false,
                )
                PresetFieldLabel("Body（JSON 对象）")
                SettingsJsonField(
                    value = draft.bodyJson,
                    onValueChange = { draft = draft.copy(bodyJson = it) },
                    enabled = !saving,
                    minHeight = 120.dp,
                )
                PresetFieldLabel("Headers（JSON 对象，值必须是字符串）")
                SettingsJsonField(
                    value = draft.headersJson,
                    onValueChange = { draft = draft.copy(headersJson = it) },
                    enabled = !saving,
                    minHeight = 96.dp,
                )
                if (validation != null) {
                    Text(
                        text = validation,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                } else {
                    Text(
                        text = "Body / Headers 展示的就是会整体写回的完整对象；未展示的字段不存在被覆盖的风险。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = { onSave(draft) },
                style = McaButtonStyle.Primary,
                enabled = !saving && validation == null,
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

/** 自由文本多行输入（系统提示词用；JSON 用 [SettingsJsonField]）。 */
@Composable
private fun PresetMultilineField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    placeholder: String,
    monospace: Boolean,
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
                fontFamily = if (monospace) {
                    androidx.compose.ui.text.font.FontFamily.Monospace
                } else {
                    androidx.compose.ui.text.font.FontFamily.Default
                },
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier.fillMaxWidth().heightIn(min = 72.dp).padding(12.dp),
            decorationBox = { inner ->
                androidx.compose.foundation.layout.Box {
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

@Composable
private fun PresetRow(
    preset: PresetSummary,
    busy: Boolean,
    onToggle: () -> Unit,
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
            McaChip(
                selected = preset.enabled,
                enabled = !busy,
                onClick = onToggle,
                label = { Text(if (preset.enabled) "已启用" else "已停用") },
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                text = presetMetaLine(preset),
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
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

/** 预设行副标题：Body/Headers 条数 + 提示词 + 挂载情况（桌面 `settings.js:857-872`）。 */
internal fun presetMetaLine(preset: PresetSummary): String {
    val attached = buildList {
        if (preset.attachedProviders > 0) add("${preset.attachedProviders} 渠道")
        if (preset.attachedRoutes > 0) add("${preset.attachedRoutes} 路由")
    }.joinToString(" · ").ifBlank { "未挂载" }
    val prompt = if (preset.hasSystemPrompt) " · 提示词" else ""
    return "Body ${preset.bodyCount} · Headers ${preset.headerCount}$prompt · $attached"
}

internal fun presetCountLabel(count: Int): String = if (count <= 0) "未配置" else "$count 个"

internal fun presetEmptyCopy(): String =
    "为什么空：本机还没有任何请求预设。" +
        "下一步：点下面「新建预设」直接建一个（Body / Headers 各写一个 JSON 对象即可）。"
