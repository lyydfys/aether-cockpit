package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PresetSummary
import com.muyuchat.aether.runtime.PresetsSnapshot
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import kotlinx.coroutines.launch

/**
 * 设置 → 预设（桌面 `settings.js:847-884` 的「请求预设」卡）。
 *
 * 列表 + 启停 + 删除（删除带二次确认，桌面 `:875`）。
 * **新建/编辑不在手机做**：要编辑 Body / Headers 两段 JSON 与系统提示词（桌面 `presetDialog :887-931`），
 * 编辑错一处就改坏所有挂载它的请求；桌面端有完整表单与校验。
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
    var pendingDelete by remember { mutableStateOf<PresetSummary?>(null) }

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
            return@DeepCard
        }
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
                onDelete = { pendingDelete = preset },
            )
        }
        Text(
            text = "预设只做静态 Body / Headers 合并；停用或没有挂载时不会生效。新建与编辑在桌面端。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
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

@Composable
private fun PresetRow(
    preset: PresetSummary,
    busy: Boolean,
    onToggle: () -> Unit,
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
        "下一步：到桌面端「设置 → 预设」新建一个 Body / Headers 预设，再回来这里启停或删除。"
