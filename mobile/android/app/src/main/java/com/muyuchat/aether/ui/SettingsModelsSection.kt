package com.muyuchat.aether.ui

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
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.launch

/**
 * 设置 → 模型目录（桌面 `settings.js:400-446`）。
 *
 * 桌面本身就是**一个 JSON 文本框**（`:403` 的 `textarea`）：客户端只写虚拟名，
 * 转发时按渠道换上游名。这里保持同一形态，不做逐行表格（桌面也没有），
 * 外加 `POST /api/providers/models/refresh-all` 的「刷新全部模型目录」（`:424-436`）。
 */
@Composable
internal fun SettingsModelsSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var draft by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            return@LaunchedEffect
        }
        error = null
        runCatching { client.fetchModelRedirects(status) }
            .onSuccess { draft = modelRedirectsDraftText(it.redirects) }
            .onFailure { error = it.message ?: "读取模型重定向失败" }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("模型重定向")
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取模型重定向。")
            return@DeepCard
        }
        Text(
            text = "客户端只写虚拟名；路由按渠道顺序尝试，转发时使用该渠道配置的上游名。" +
                "重定向优先于同名裸模型与路由组。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        error?.let {
            Text(
                text = "读不到模型重定向：$it",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        notice?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
        SettingsJsonField(
            value = draft,
            onValueChange = { draft = it },
            enabled = !busy,
            minHeight = 180.dp,
        )
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = {
                    if (busy) {
                        return@McaButton
                    }
                    val parsed = parseModelRedirectsDraft(draft)
                    if (parsed == null) {
                        error = "JSON 需要是对象：{\"虚拟名\":{\"渠道\":\"上游模型名\"}}"
                        return@McaButton
                    }
                    busy = true
                    scope.launch {
                        try {
                            val saved = client.saveModelRedirects(status, parsed)
                            draft = modelRedirectsDraftText(saved.redirects)
                            error = null
                            notice = "模型重定向已保存"
                        } catch (e: ManagementApiException) {
                            error = "保存失败：${e.message}（错误码：${e.code}）"
                        } catch (_: Exception) {
                            error = "保存失败，请重试"
                        } finally {
                            busy = false
                        }
                    }
                },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text(if (busy) "保存中…" else "保存重定向") }
            McaButton(
                onClick = {
                    if (busy) {
                        return@McaButton
                    }
                    busy = true
                    scope.launch {
                        try {
                            notice = client.refreshAllModelCatalogs(status)
                            error = null
                        } catch (e: ManagementApiException) {
                            error = "刷新失败：${e.message}（错误码：${e.code}）"
                        } catch (_: Exception) {
                            error = "刷新失败，请重试"
                        } finally {
                            busy = false
                        }
                    }
                },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text("刷新全部模型目录") }
        }
        Text(
            text = "渠道级模型目录模式（merge / fallback / allowlist）在「额度 → 渠道详情 → 模型」里配置；这里管跨渠道虚拟名。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** 多行 JSON 输入（桌面 `settings.js:403` 的 textarea 形态）。 */
@Composable
internal fun SettingsJsonField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    minHeight: androidx.compose.ui.unit.Dp,
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
                fontFamily = FontFamily.Monospace,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = minHeight)
                .padding(12.dp),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty()) {
                        Text(
                            text = "{\"deepseek-v4.1-flash\":{\"qoder-cn\":\"DeepSeek-Flash\"}}",
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

/** 单行文本输入（与 [SettingsJsonField] 同一视觉骨架）。 */
@Composable
internal fun SettingsLineField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    placeholder: String,
    modifier: Modifier = Modifier,
) {
    Surface(
        modifier = modifier,
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            singleLine = true,
            textStyle = TextStyle(
                color = MaterialTheme.colorScheme.onSurface,
                fontSize = MaterialTheme.typography.bodySmall.fontSize,
                fontFamily = FontFamily.Monospace,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier.fillMaxWidth().heightIn(min = 44.dp).padding(horizontal = 12.dp, vertical = 10.dp),
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

/** 重定向的草稿文本：稳定排序 + 缩进，方便在手机上肉眼校对。 */
internal fun modelRedirectsDraftText(redirects: Map<String, Map<String, String>>): String {
    if (redirects.isEmpty()) {
        return "{}"
    }
    return buildString {
        append("{\n")
        val virtuals = redirects.keys.sorted()
        virtuals.forEachIndexed { index, virtual ->
            append("  ")
            append(com.muyuchat.aether.runtime.quoteJson(virtual))
            append(": {")
            val targets = redirects.getValue(virtual)
            append(
                targets.keys.sorted().joinToString(", ") { provider ->
                    com.muyuchat.aether.runtime.quoteJson(provider) + ": " +
                        com.muyuchat.aether.runtime.quoteJson(targets.getValue(provider))
                },
            )
            append("}")
            if (index != virtuals.lastIndex) {
                append(",")
            }
            append("\n")
        }
        append("}")
    }
}

/**
 * 解析用户编辑的 JSON：必须是 `{虚拟名: {渠道: 上游名}}`；不合法返回 null（页面据此提示，不提交）。
 * 空文本按"清空全部重定向"处理（与桌面 `settings.js:411` 的 `ta.value.trim() ? JSON.parse : {}` 一致）。
 */
internal fun parseModelRedirectsDraft(text: String): Map<String, Map<String, String>>? {
    val trimmed = text.trim()
    if (trimmed.isEmpty()) {
        return emptyMap()
    }
    val root = runCatching { org.json.JSONObject(trimmed) }.getOrNull() ?: return null
    val out = LinkedHashMap<String, Map<String, String>>()
    val keys = root.keys()
    while (keys.hasNext()) {
        val virtual = keys.next().trim()
        if (virtual.isEmpty()) {
            return null
        }
        val target = root.optJSONObject(virtual) ?: return null
        val inner = LinkedHashMap<String, String>()
        val innerKeys = target.keys()
        while (innerKeys.hasNext()) {
            val provider = innerKeys.next().trim()
            if (provider.isEmpty()) {
                return null
            }
            inner[provider] = target.optString(provider).trim()
        }
        out[virtual] = inner
    }
    return out
}
