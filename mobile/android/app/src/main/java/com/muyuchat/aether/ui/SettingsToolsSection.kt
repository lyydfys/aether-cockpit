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
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.ToolsConfig
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import kotlinx.coroutines.launch

/**
 * 设置 → 工具服务（桌面 `settings.js:275-398`）。
 *
 * 字段顺序照桌面：网络搜索 8 项 → 图像 / 音频 / 视频 / 实时各 3 项 → 保存与测试。
 *
 * **只读 + 两个真测试**（这是刻意的取舍）：保存 `tools` 要求把 API Key 一起回写，
 * 而 GET 只回掩码/`******`；在手机上"留空保留"的语义要逐字段验证（桌面用 `'******'` 哨兵），
 * 盲写会把用户已存的 Key 清掉。所以本页只展示真实配置 + 允许**真实发起**
 * `POST /api/tools/web-search` 与 `/api/tools/image-test`（失败贴服务端原文）；编辑留桌面端。
 */
@Composable
internal fun SettingsToolsSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var tools by remember { mutableStateOf<ToolsConfig?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var searchResult by remember { mutableStateOf<String?>(null) }
    var imageResult by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            tools = null
            return@LaunchedEffect
        }
        error = null
        runCatching { client.fetchSettings(status).tools }
            .onSuccess { tools = it }
            .onFailure { error = it.message ?: "读取工具服务配置失败" }
    }

    fun runTest(target: String) {
        if (busy) {
            return
        }
        busy = true
        scope.launch {
            try {
                if (target == "search") {
                    searchResult = client.testWebSearch(status)
                } else {
                    imageResult = client.testImageService(status)
                }
            } catch (e: ManagementApiException) {
                val line = "${if (target == "search") "搜索" else "图像"}测试失败：${e.message}（错误码：${e.code}）"
                if (target == "search") searchResult = line else imageResult = line
            } catch (_: Exception) {
                val line = "${if (target == "search") "搜索" else "图像"}测试失败，请重试"
                if (target == "search") searchResult = line else imageResult = line
            } finally {
                busy = false
            }
        }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("工具服务")
        val current = tools
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取工具服务配置。")
            return@DeepCard
        }
        if (current == null && error == null) {
            DeepCardBody("正在读取工具服务配置…")
            return@DeepCard
        }
        error?.let {
            Text(
                text = "读不到工具服务配置：$it",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        // —— 网络搜索（桌面 :364-372，8 项里除 Key 明文外全部展示） ——
        Text(
            text = "网络搜索",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingReadRow("供应商", current?.webSearchProvider?.ifBlank { "未配置" } ?: "—")
        SettingReadRow("地址", current?.webSearchEndpoint?.ifBlank { "供应商默认" } ?: "—")
        SettingReadRow(
            "API Key",
            when {
                current == null -> "—"
                current.webSearchKeyCount > 0 -> "已保存 ${current.webSearchKeyCount} 个（明文只在桌面端）"
                else -> "未配置"
            },
        )
        SettingReadRow("返回条数", (current?.webSearchMaxResults ?: 0).let { if (it > 0) "$it" else "默认 5" })
        SettingReadRow("Key 顺序", searchKeyOrderLabel(current?.webSearchKeyOrder ?: ""))
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { runTest("search") },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text(if (busy) "测试中…" else "测试搜索") }
            searchResult?.let {
                Text(
                    text = it,
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (it.contains("失败")) {
                        MaterialTheme.colorScheme.error
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    },
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        // —— 图像 / 音频 / 视频 / 实时（桌面 :373-392，各 3 项） ——
        Text(
            text = "图像服务",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingReadRow("Base URL", current?.imageBaseUrl?.ifBlank { "未配置" } ?: "—")
        SettingReadRow("API Key", savedKeyLabel(current?.imageKeySaved == true))
        SettingReadRow("默认模型", current?.imageModel?.ifBlank { "未设置" } ?: "—")
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { runTest("image") },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text("测试图像") }
            imageResult?.let {
                Text(
                    text = it,
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (it.contains("失败")) {
                        MaterialTheme.colorScheme.error
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    },
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        Text(
            text = "音频服务",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingReadRow("Base URL", current?.audioBaseUrl?.ifBlank { "未配置" } ?: "—")
        SettingReadRow("API Key", savedKeyLabel(current?.audioKeySaved == true))
        SettingReadRow("默认模型", current?.audioModel?.ifBlank { "未设置" } ?: "—")
        Spacer(Modifier.height(12.dp))
        Text(
            text = "视频服务",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingReadRow("Base URL", current?.videoBaseUrl?.ifBlank { "未配置" } ?: "—")
        SettingReadRow("API Key", savedKeyLabel(current?.videoKeySaved == true))
        SettingReadRow("默认模型", current?.videoModel?.ifBlank { "未设置" } ?: "—")
        Spacer(Modifier.height(12.dp))
        Text(
            text = "实时语音",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingReadRow("Base URL", current?.realtimeBaseUrl?.ifBlank { "未配置" } ?: "—")
        SettingReadRow("API Key", savedKeyLabel(current?.realtimeKeySaved == true))
        SettingReadRow("默认模型", current?.realtimeModel?.ifBlank { "未设置" } ?: "—")
        Text(
            text = "本页只读：工具服务的 Key 回写需要「留空保留」的逐字段语义，手机上编辑会清掉已存 Key；" +
                "改配置请用桌面端。两个测试按钮是真实的（会消耗一次调用）。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun SettingReadRow(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            modifier = Modifier.width(96.dp),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = value,
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Key 是否已保存（服务端只回掩码，不回明文）。 */
internal fun savedKeyLabel(saved: Boolean): String = if (saved) "已保存（明文只在桌面端）" else "未配置"

/** Key 顺序的人类可读文案（桌面 `settings.js:292-295`）。 */
internal fun searchKeyOrderLabel(order: String): String = when (order.trim().lowercase()) {
    "rotate" -> "多 Key 轮换"
    "order" -> "按顺序优先"
    "" -> "默认（轮换）"
    else -> order.trim()
}
