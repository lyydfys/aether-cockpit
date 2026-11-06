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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PipelineHooks
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.SettingsSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 设置页的统一错误文案。
 *
 * `HttpURLConnection` 抛出的 `NetworkOnMainThreadException` / 部分 `IOException`
 * 的 `message` 是 **null**；早前用 `it.message ?: "读取设置失败"` 会把它吞成一句
 * 没有信息量的「读取设置失败」，页面看起来像"配置损坏"。这里至少带上异常类型。
 */
internal fun settingsErrorText(error: Throwable, fallback: String): String {
    val message = error.message?.trim()
    if (!message.isNullOrEmpty()) {
        return message
    }
    val kind = error.javaClass.simpleName.ifBlank { "未知错误" }
    return "$fallback（$kind）"
}

/**
 * 设置 → 常规（桌面 `settings.js:130-205`）。
 *
 * 字段顺序与文案照桌面：外观（主题）→ 常规（开机自启 / WSL 接入 / 防止休眠 / 渠道自动探测）→ 导入。
 * **桌面专属项一律置灰并写明原因**（开机自启、WSL、防休眠改的是桌面宿主行为）。
 * 「本机账号自动导入」在手机端恒不可用（服务端 `!platform.WindowsAutoImport` → 501），只作状态说明。
 */
@Composable
internal fun SettingsGeneralSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var summary by remember { mutableStateOf<SettingsSummary?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            summary = null
            return@LaunchedEffect
        }
        error = null
        // 必须离开主线程：LaunchedEffect 跑在 Main 调度器，`openConnection().responseCode`
        // 会直接抛 NetworkOnMainThreadException（message 为 null），表现得就像"读取设置失败"。
        runCatching { withContext(Dispatchers.IO) { client.fetchSettings(status) } }
            .onSuccess { summary = it }
            .onFailure { error = settingsErrorText(it, "读取设置失败") }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("常规")
        val current = summary
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取常规设置。")
            return@DeepCard
        }
        if (current == null && error == null) {
            DeepCardBody("正在读取常规设置…")
            return@DeepCard
        }
        if (error != null) {
            Text(
                text = "保存/读取失败：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        Text(
            text = "外观",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf("auto" to "跟随系统", "light" to "浅色", "dark" to "深色").forEach { (value, label) ->
                McaChip(
                    selected = (current?.uiTheme ?: "").ifBlank { "auto" } == value,
                    enabled = !busy && current != null,
                    onClick = { if (!busy) { busy = true; scope.launch {
                        try { summary = withContext(Dispatchers.IO) { client.setTheme(status, value) }; error = null }
                        catch (e: ManagementApiException) { error = "${e.message}（错误码：${e.code}）" }
                        catch (e: Exception) { error = "保存失败：${settingsErrorText(e, "请重试")}" }
                        finally { busy = false }
                    } } },
                    label = { Text(label) },
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        Text(
            text = "常规",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        ReadonlyRow(
            label = "开机自启",
            detail = "登录桌面系统时自动启动；手机没有对应概念（App 由用户显式启动 + 前台服务）",
            value = current?.let { if (it.autostartSupported) "桌面端可配置" else "不可用" } ?: "—",
        )
        ReadonlyRow(
            label = "WSL 接入",
            detail = "关闭后不枚举发行版、不访问 WSL 家目录；Android 上不存在 WSL",
            value = current?.let { if (it.wslDisabled) "桌面端：已关闭" else "桌面端：已开启" } ?: "—",
        )
        ReadonlyRow(
            label = "防止休眠",
            detail = "运行期间让电脑保持唤醒；手机由系统省电策略管理，无法替用户阻止休眠",
            value = current?.let { if (it.keepAwake) "桌面端：已开启" else "桌面端：已关闭" } ?: "—",
        )
        // 渠道自动探测是真实可写的（`monitor_minutes`，桌面 :198/:207-209）
        Text(
            text = "渠道自动探测",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Text(
            text = "会真实消耗账号额度，默认关闭",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf(0 to "关闭", 5 to "5 分", 15 to "15 分", 30 to "30 分", 60 to "60 分").forEach { (minutes, label) ->
                McaChip(
                    selected = current?.monitorMinutes == minutes,
                    enabled = !busy && current != null,
                    onClick = { if (!busy) { busy = true; scope.launch {
                        try { summary = withContext(Dispatchers.IO) { client.setMonitorMinutes(status, minutes) }; error = null }
                        catch (e: ManagementApiException) { error = "${e.message}（错误码：${e.code}）" }
                        catch (e: Exception) { error = "保存失败：${settingsErrorText(e, "请重试")}" }
                        finally { busy = false }
                    } } },
                    label = { Text(label) },
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        Text(
            text = "导入",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        ReadonlyRow(
            label = "本机账号自动导入",
            detail = "每 60 秒扫描桌面已登录的客户端（Codex / Qoder / Trae / Claude / Kiro 等）；" +
                "服务端对非 Windows 平台直接拒绝（501）",
            value = "仅桌面端",
        )
        if (busy) {
            Text(
                text = "保存中…",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * 设置 → 隐私的三项补充（桌面 `settings.js:463-482`）。
 *
 * 只做服务端**确实回传**的键：`redactShape`、`privacy_mask.enabled`、`hooks`（四项）。
 * 依据：`settingsView()` 把 `settings.ui` 合并进视图（`lan.go:213-221`），
 * `privacy_mask` 是 `Settings` 的顶层字段（`settings.go:43`），`hooks` 读的也是 `settings.ui["hooks"]`
 * （`pipeline_hooks.go:44`）—— 都能回读，不是盲开关。
 *
 * 「拦截模型 / 跳过渠道」是自由文本列表，本轮只**只读展示**（编辑留桌面端），
 * 避免在没有编译器验证的情况下引入第二个文本输入组件。
 */
@Composable
internal fun SettingsPrivacySection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var summary by remember { mutableStateOf<SettingsSummary?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            summary = null
            return@LaunchedEffect
        }
        error = null
        runCatching { withContext(Dispatchers.IO) { client.fetchSettings(status) } }
            .onSuccess { summary = it }
            .onFailure { error = settingsErrorText(it, "读取隐私设置失败") }
    }

    fun write(block: (GatewayStatus) -> SettingsSummary) {
        if (busy) {
            return
        }
        busy = true
        scope.launch {
            try {
                summary = withContext(Dispatchers.IO) { block(status) }
                error = null
            } catch (e: ManagementApiException) {
                error = "${e.message}（错误码：${e.code}）"
            } catch (e: Exception) {
                error = "保存失败：${settingsErrorText(e, "请重试")}"
            } finally {
                busy = false
            }
        }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("隐私（补充）")
        val current = summary
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取隐私设置。")
            return@DeepCard
        }
        if (current == null && error == null) {
            DeepCardBody("正在读取隐私设置…")
            return@DeepCard
        }
        if (error != null) {
            Text(
                text = "保存/读取失败：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        ToggleRow(
            label = "保留格式的假值",
            detail = "密钥类改成同形假值（sk-… 仍是 sk-…），上游更难识别",
            on = current?.redactShape == true,
            enabled = !busy && current != null,
            onClick = { write { client.setRedactShape(it, !(current?.redactShape ?: false)) } },
        )
        ToggleRow(
            label = "响应回填",
            detail = "上游响应里的占位符换回原文（不打开则保持占位符）",
            on = current?.privacyMaskEnabled == true,
            enabled = !busy && current != null,
            onClick = { write { client.setPrivacyMask(it, !(current?.privacyMaskEnabled ?: false)) } },
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = "请求钩子",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        ToggleRow(
            label = "丢弃推理增量",
            detail = "部分客户端会把 reasoning 当正文显示；打开后从流里过滤掉",
            on = current?.hooks?.dropReasoning == true,
            enabled = !busy && current != null,
            onClick = {
                val hooks = current?.hooks ?: PipelineHooks()
                write { client.saveHooks(it, hooks.copy(dropReasoning = !hooks.dropReasoning)) }
            },
        )
        HookPresetRow(
            label = "输出上限",
            detail = "客户端不设 max_tokens 时按这个值封顶；默认不限制",
            presets = listOf(0 to "不限制", 1024 to "1024", 2048 to "2048", 4096 to "4096", 8192 to "8192"),
            selected = current?.hooks?.clampOutput ?: 0,
            enabled = !busy && current != null,
            onSelect = { value ->
                val hooks = current?.hooks ?: PipelineHooks()
                write { client.saveHooks(it, hooks.copy(clampOutput = value)) }
            },
        )
        ReadonlyRow(
            label = "拦截模型",
            detail = "按前缀拦截（* 结尾表示前缀），被拦的请求直接 4xx；列表编辑在桌面端",
            value = current?.hooks?.blockModels?.joinToString(", ")?.ifBlank { "未设置" } ?: "—",
        )
        ReadonlyRow(
            label = "跳过渠道",
            detail = "这些渠道不参与本次请求；列表编辑在桌面端",
            value = current?.hooks?.guardProviders?.joinToString(", ")?.ifBlank { "未设置" } ?: "—",
        )
        Text(
            text = "钩子跑在网关进程内（不启动外部进程）；单个钩子 panic 或超时只跳过它自己。" +
                "按类型处置与自定义规则仍在「脱敏」页。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** 只读行：标签 + 说明 + 置灰状态（桌面专属项统一用这个形状）。 */
@Composable
private fun ReadonlyRow(label: String, detail: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                text = label,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f),
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun ToggleRow(label: String, detail: String, on: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(text = label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurface)
            Text(
                text = detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.width(8.dp))
        McaButton(
            onClick = onClick,
            style = if (on) McaButtonStyle.Primary else McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
            enabled = enabled,
        ) { Text(if (on) "开启" else "关闭") }
    }
}

@Composable
private fun HookPresetRow(
    label: String,
    detail: String,
    presets: List<Pair<Int, String>>,
    selected: Int,
    enabled: Boolean,
    onSelect: (Int) -> Unit,
) {
    Column(Modifier.fillMaxWidth().padding(vertical = DeepRowVertical), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(text = label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurface)
        Text(
            text = detail,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            presets.forEach { (value, text) ->
                McaChip(
                    selected = selected == value,
                    enabled = enabled,
                    onClick = { onSelect(value) },
                    label = { Text(text) },
                )
            }
        }
    }
}
