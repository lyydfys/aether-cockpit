package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
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
import com.muyuchat.aether.runtime.OtlpConfig
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import kotlinx.coroutines.launch

/**
 * 设置 → 可观测（桌面 `settings.js:504-540`）。
 *
 * **OTLP headers 语义（已核源码，不是猜）**：`POST /api/settings` 里 `otlp.headers`
 * 未携带、或为打码形态 `{"___masked___":"true"}` 时，服务端沿用**旧 headers**
 * （`settings.go:515-516` 注释 + `:716-729` 的 `in.Headers = g.settings.OTLP.Headers`）。
 * 因此本页：
 *  - endpoint / 批大小 / 周期 / 队列可写（请求体**不带 headers** → 鉴权头原样保留）；
 *  - headers 明文**不在手机上编辑**（GET 只回掩码，明文编辑既看不到原值也容易写坏）；
 *    需要换鉴权头请去桌面端；这里只提供「清空鉴权头」（显式发 `{}`，语义已确认）。
 */
@Composable
internal fun SettingsObservabilitySection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var otlp by remember { mutableStateOf<OtlpConfig?>(null) }
    var endpoint by remember { mutableStateOf("") }
    var batch by remember { mutableStateOf("") }
    var flush by remember { mutableStateOf("") }
    var queue by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }

    fun apply(current: OtlpConfig) {
        otlp = current
        endpoint = current.endpoint
        batch = if (current.batchSize > 0) current.batchSize.toString() else ""
        flush = if (current.flushSeconds > 0) current.flushSeconds.toString() else ""
        queue = if (current.queueMax > 0) current.queueMax.toString() else ""
    }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            otlp = null
            return@LaunchedEffect
        }
        error = null
        runCatching { client.fetchSettings(status).otlp }
            .onSuccess { apply(it) }
            .onFailure { error = it.message ?: "读取 OTLP 配置失败" }
    }

    fun save(clearHeaders: Boolean) {
        if (busy) {
            return
        }
        busy = true
        scope.launch {
            try {
                val next = client.saveOtlp(
                    status = status,
                    otlp = OtlpConfig(
                        endpoint = endpoint.trim(),
                        batchSize = parsePositiveInt(batch),
                        flushSeconds = parsePositiveInt(flush),
                        queueMax = parsePositiveInt(queue),
                    ),
                    clearHeaders = clearHeaders,
                )
                apply(next.otlp)
                error = null
                notice = if (clearHeaders) "已保存，并清空 OTLP 鉴权头" else "已保存（鉴权头保持原值）"
            } catch (e: ManagementApiException) {
                error = "保存失败：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                error = "保存失败，请重试"
            } finally {
                busy = false
            }
        }
    }

    DeepCard(modifier = modifier) {
        DeepCardTitle("OpenTelemetry")
        val current = otlp
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取 OTLP 配置。")
            return@DeepCard
        }
        if (current == null && error == null) {
            DeepCardBody("正在读取 OTLP 配置…")
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
        Text(
            text = "OTLP 导出地址（非空即启用）",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SettingsLineField(
            value = endpoint,
            onValueChange = { endpoint = it },
            enabled = !busy,
            placeholder = "http://127.0.0.1:4318",
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = "攒批",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                SettingsLineField(
                    value = batch,
                    onValueChange = { batch = it.filter { ch -> ch.isDigit() }.take(5) },
                    enabled = !busy,
                    placeholder = "32",
                )
            }
            Column(Modifier.weight(1f)) {
                Text(
                    text = "周期（秒）",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                SettingsLineField(
                    value = flush,
                    onValueChange = { flush = it.filter { ch -> ch.isDigit() }.take(4) },
                    enabled = !busy,
                    placeholder = "5",
                )
            }
            Column(Modifier.weight(1f)) {
                Text(
                    text = "队列",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                SettingsLineField(
                    value = queue,
                    onValueChange = { queue = it.filter { ch -> ch.isDigit() }.take(5) },
                    enabled = !busy,
                    placeholder = "128",
                )
            }
        }
        Text(
            text = if (current?.headersMasked == true) {
                "请求头：已保存（GET 只回掩码，明文只在桌面端维护）"
            } else {
                "请求头：未配置"
            },
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { save(clearHeaders = false) },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
                enabled = !busy,
            ) { Text(if (busy) "保存中…" else "保存") }
            McaButton(
                onClick = { save(clearHeaders = true) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !busy && current != null,
            ) { Text("清空鉴权头") }
            Spacer(Modifier.width(8.dp))
        }
        Text(
            text = "「保存」不带 headers，服务端沿用原鉴权头（settings.go:728）；" +
                "「清空鉴权头」显式发 {}，是唯一会删除它的操作。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** 数字输入解析：空/非法 → 0（服务端把 0 当"用默认值"）。 */
internal fun parsePositiveInt(text: String): Int =
    text.trim().toIntOrNull()?.coerceAtLeast(0) ?: 0
