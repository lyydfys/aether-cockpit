package com.muyuchat.aether.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
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
import com.muyuchat.aether.runtime.TOOLS_KEY_SENTINEL
import com.muyuchat.aether.runtime.ToolsConfig
import com.muyuchat.aether.runtime.ToolsDraft
import com.muyuchat.aether.runtime.ToolsEndpointDraft
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** 网络搜索供应商（桌面 `settings.js:278-287` 的 8 项）。 */
private val WEB_SEARCH_PROVIDERS = listOf(
    "tavily" to "Tavily",
    "serper" to "Serper",
    "searxng" to "SearXNG",
    "brave" to "Brave",
    "exa" to "Exa",
    "firecrawl" to "Firecrawl",
    "deepseek" to "DeepSeek",
    "you" to "You.com",
)

/**
 * 设置 → 工具服务（桌面 `settings.js:275-398`）：**可编辑版**。
 *
 * 密钥语义已核（`tools_api.go:75-170`）：`api_key == "******"` 或空串都表示"保留旧值"，
 * 子对象不出现则该子服务不动。用户没改 Key 时由 [buildToolsJson] 经 `effectiveToolKey`
 * 自动回传哨兵，所以不会清掉已存 Key。
 *
 * 保存成功后**重拉一次** `/api/settings` 确认落盘值；失败贴服务端原文。
 */
@Composable
internal fun SettingsToolsSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var draft by remember { mutableStateOf(ToolsDraft()) }
    var savedKeys by remember { mutableStateOf(emptyMap<String, Boolean>()) }
    var loaded by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var searchResult by remember { mutableStateOf<String?>(null) }
    var imageResult by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    var testing by remember { mutableStateOf(false) }

    fun apply(tools: ToolsConfig) {
        draft = ToolsDraft(
            webSearchProvider = tools.webSearchProvider.ifBlank { "tavily" },
            webSearchEndpoint = tools.webSearchEndpoint,
            webSearchApiKey = "",
            webSearchApiKeys = emptyList(),
            webSearchMaxResults = tools.webSearchMaxResults.coerceIn(1, 20),
            webSearchKeyOrder = tools.webSearchKeyOrder.ifBlank { "rotate" },
            image = ToolsEndpointDraft(tools.imageBaseUrl, "", tools.imageModel),
            audio = ToolsEndpointDraft(tools.audioBaseUrl, "", tools.audioModel),
            video = ToolsEndpointDraft(tools.videoBaseUrl, "", tools.videoModel),
            realtime = ToolsEndpointDraft(tools.realtimeBaseUrl, "", tools.realtimeModel),
        )
        savedKeys = mapOf(
            "web_search" to (tools.webSearchKeyCount > 0),
            "image" to tools.imageKeySaved,
            "audio" to tools.audioKeySaved,
            "video" to tools.videoKeySaved,
            "realtime" to tools.realtimeKeySaved,
        )
        loaded = true
    }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            loaded = false
            return@LaunchedEffect
        }
        error = null
        // LaunchedEffect 跑在 Main 调度器：先切到 IO，再发起阻塞式 HttpURLConnection。
        runCatching { withContext(Dispatchers.IO) { client.fetchSettings(status) } }
            .onSuccess { apply(it.tools) }
            .onFailure { error = it.message ?: "读取工具服务配置失败" }
    }

    fun save() {
        if (saving) {
            return
        }
        saving = true
        scope.launch {
            try {
                withContext(Dispatchers.IO) { client.saveTools(status, draft, savedKeys) }
                // 重拉确认：不拿本地回显当"已保存"
                apply(withContext(Dispatchers.IO) { client.fetchSettings(status) }.tools)
                error = null
                notice = "已保存，并重新读取确认"
            } catch (e: ManagementApiException) {
                error = "保存失败：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                error = "保存失败，请重试"
            } finally {
                saving = false
            }
        }
    }

    fun runTest(isSearch: Boolean) {
        if (testing) {
            return
        }
        testing = true
        scope.launch {
            try {
                if (isSearch) {
                    searchResult = withContext(Dispatchers.IO) { client.testWebSearch(status) }
                } else {
                    imageResult = withContext(Dispatchers.IO) { client.testImageService(status) }
                }
            } catch (e: ManagementApiException) {
                val line = "${if (isSearch) "搜索" else "图像"}测试失败：${e.message}（错误码：${e.code}）"
                if (isSearch) searchResult = line else imageResult = line
            } catch (_: Exception) {
                val line = "${if (isSearch) "搜索" else "图像"}测试失败，请重试"
                if (isSearch) searchResult = line else imageResult = line
            } finally {
                testing = false
            }
        }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("工具服务")
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取工具服务配置。")
            return@DeepCard
        }
        if (!loaded && error == null) {
            DeepCardBody("正在读取工具服务配置…")
            return@DeepCard
        }
        error?.let {
            Text(
                text = it,
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

        // —— 网络搜索（桌面 :364-372）——
        ToolGroupTitle("网络搜索")
        Text(
            text = "供应商",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Row(
            modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            WEB_SEARCH_PROVIDERS.forEach { (value, label) ->
                McaChip(
                    selected = draft.webSearchProvider == value,
                    enabled = !saving,
                    onClick = { draft = draft.copy(webSearchProvider = value) },
                    label = { Text(label) },
                )
            }
        }
        ToolFieldLabel("地址（留空用供应商默认；SearXNG 必填）")
        SettingsLineField(
            value = draft.webSearchEndpoint,
            onValueChange = { draft = draft.copy(webSearchEndpoint = it) },
            enabled = !saving,
            placeholder = "http://127.0.0.1:8080",
        )
        ToolFieldLabel("API Key（单 Key；与多 Key 二选一）")
        SettingsLineField(
            value = draft.webSearchApiKey,
            onValueChange = { draft = draft.copy(webSearchApiKey = it) },
            enabled = !saving,
            placeholder = apiKeyPlaceholder(savedKeys["web_search"] == true),
        )
        ToolFieldLabel("多 Key（逗号分隔；填写会替换现有列表）")
        SettingsLineField(
            value = draft.webSearchApiKeys.joinToString(", "),
            onValueChange = { text ->
                draft = draft.copy(
                    webSearchApiKeys = text.split(',', '，', '\n').map { it.trim() }.filter { it.isNotEmpty() },
                )
            },
            enabled = !saving,
            placeholder = "留空 = 保留现有 Key",
        )
        ToolFieldLabel("返回条数（1–20）")
        SettingsLineField(
            value = draft.webSearchMaxResults.toString(),
            onValueChange = { text ->
                val n = text.filter { ch -> ch.isDigit() }.take(2).toIntOrNull() ?: 5
                draft = draft.copy(webSearchMaxResults = n.coerceIn(1, 20))
            },
            enabled = !saving,
            placeholder = "5",
        )
        ToolFieldLabel("Key 顺序")
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf("rotate" to "多 Key 轮换", "order" to "按顺序优先").forEach { (value, label) ->
                McaChip(
                    selected = draft.webSearchKeyOrder == value,
                    enabled = !saving,
                    onClick = { draft = draft.copy(webSearchKeyOrder = value) },
                    label = { Text(label) },
                )
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { runTest(isSearch = true) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !testing && !saving,
            ) { Text(if (testing) "测试中…" else "测试搜索") }
            TestResultLine(result = searchResult, modifier = Modifier.weight(1f))
        }

        // —— 图像 / 音频 / 视频 / 实时（桌面 :373-392，各 3 项）——
        ToolEndpointGroup(
            title = "图像服务",
            detail = "OpenAI 兼容 /images/generations 与 /images/edits",
            draft = draft.image,
            keySaved = savedKeys["image"] == true,
            enabled = !saving,
            onChange = { draft = draft.copy(image = it) },
        )
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { runTest(isSearch = false) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !testing && !saving,
            ) { Text("测试图像") }
            TestResultLine(result = imageResult, modifier = Modifier.weight(1f))
        }
        ToolEndpointGroup(
            title = "音频服务",
            detail = "OpenAI 兼容 /audio/transcriptions 与 /audio/speech",
            draft = draft.audio,
            keySaved = savedKeys["audio"] == true,
            enabled = !saving,
            onChange = { draft = draft.copy(audio = it) },
        )
        ToolEndpointGroup(
            title = "视频服务",
            detail = "xAI 用 https://api.x.ai/v1（/videos/generations）",
            draft = draft.video,
            keySaved = savedKeys["video"] == true,
            enabled = !saving,
            onChange = { draft = draft.copy(video = it) },
        )
        ToolEndpointGroup(
            title = "实时语音",
            detail = "OpenAI Realtime 兼容；网关提供 /v1/realtime WebSocket 桥",
            draft = draft.realtime,
            keySaved = savedKeys["realtime"] == true,
            enabled = !saving,
            onChange = { draft = draft.copy(realtime = it) },
        )

        Spacer(Modifier.height(12.dp))
        McaButton(
            onClick = { save() },
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = !saving,
        ) { Text(if (saving) "保存中…" else "保存工具服务") }
        Text(
            text = "Key 留空且已存过 → 回传哨兵「$TOOLS_KEY_SENTINEL」表示保留（服务端 tools_api.go:86-94 同义）；" +
                "填新值则替换。图像/音频/视频/实时一次写入。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ToolGroupTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 12.dp, bottom = 4.dp),
    )
}

@Composable
private fun ToolFieldLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 8.dp),
    )
}

@Composable
private fun TestResultLine(result: String?, modifier: Modifier = Modifier) {
    if (result == null) {
        return
    }
    Text(
        text = result,
        modifier = modifier,
        style = MaterialTheme.typography.labelSmall,
        color = if (result.contains("失败")) {
            MaterialTheme.colorScheme.error
        } else {
            MaterialTheme.colorScheme.onSurfaceVariant
        },
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
    )
}

@Composable
private fun ToolEndpointGroup(
    title: String,
    detail: String,
    draft: ToolsEndpointDraft,
    keySaved: Boolean,
    enabled: Boolean,
    onChange: (ToolsEndpointDraft) -> Unit,
) {
    ToolGroupTitle(title)
    Text(
        text = detail,
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(4.dp))
    SettingsLineField(
        value = draft.baseUrl,
        onValueChange = { onChange(draft.copy(baseUrl = it)) },
        enabled = enabled,
        placeholder = "https://api.openai.com/v1",
    )
    Spacer(Modifier.height(8.dp))
    SettingsLineField(
        value = draft.apiKey,
        onValueChange = { onChange(draft.copy(apiKey = it)) },
        enabled = enabled,
        placeholder = apiKeyPlaceholder(keySaved),
    )
    Spacer(Modifier.height(8.dp))
    SettingsLineField(
        value = draft.model,
        onValueChange = { onChange(draft.copy(model = it)) },
        enabled = enabled,
        placeholder = "默认模型（可选）",
    )
}

/** Key 输入框占位：已存过就说"不改动"，避免用户以为要重填。 */
internal fun apiKeyPlaceholder(saved: Boolean): String =
    if (saved) "已保存（不改动）" else "未配置，可填写"

/** 只读场景的 Key 状态文案（保留给单测与只读区块使用）。 */
internal fun savedKeyLabel(saved: Boolean): String =
    if (saved) "已保存（明文只在桌面端）" else "未配置"

/** Key 顺序的人类可读文案（桌面 `settings.js:292-295`）。 */
internal fun searchKeyOrderLabel(order: String): String = when (order.trim().lowercase()) {
    "rotate" -> "多 Key 轮换"
    "order" -> "按顺序优先"
    "" -> "默认（轮换）"
    else -> order.trim()
}
