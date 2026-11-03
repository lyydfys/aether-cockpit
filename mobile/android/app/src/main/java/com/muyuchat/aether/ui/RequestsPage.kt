package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.RequestRow
import com.muyuchat.aether.runtime.RequestsPage as RequestsData
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

@Composable
internal fun RequestsPage(
    status: GatewayStatus,
    state: RequestsUiState,
    failedOnly: Boolean,
    onFailedOnly: (Boolean) -> Unit,
    onRefresh: () -> Unit,
    onPrev: () -> Unit,
    onNext: () -> Unit,
    onOpenOverview: () -> Unit,
    selected: RequestRow? = null,
    onSelect: (RequestRow) -> Unit = {},
    onDismissDetail: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "请求",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "今日请求账本 · 只读",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新请求",
                    onClick = onRefresh,
                    enabled = state is RequestsUiState.Ready || state is RequestsUiState.Failed,
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
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = { onFailedOnly(false) },
                    style = if (!failedOnly) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                ) { Text("全部") }
                McaButton(
                    onClick = { onFailedOnly(true) },
                    style = if (failedOnly) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                ) { Text("仅失败") }
            }
        }
        when (state) {
            RequestsUiState.Idle -> item {
                RequestsStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机请求账本。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            RequestsUiState.Loading -> item { RequestsLoadingCard() }
            is RequestsUiState.Failed -> item {
                RequestsErrorCard(state.code, state.message, onRefresh)
            }
            is RequestsUiState.Ready -> {
                val page = state.page
                if (page.rows.isEmpty()) {
                    item {
                        RequestsStateCard(
                            title = if (failedOnly) "没有失败请求" else "还没有请求",
                            detail = "通过本机 /v1 发起的请求会出现在这里。",
                        )
                    }
                } else {
                    item { RequestsSummaryLine(page) }
                    item { RequestsList(page, onSelect) }
                    item { RequestsPager(page, onPrev, onNext) }
                }
            }
        }
        item {
            Text(
                text = "只读：不保存或展示请求正文、会话 ID、caller key；数据来自本机 /api/usage/requests。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (selected != null) {
        RequestDetailDialog(row = selected, onDismiss = onDismissDetail)
    }
}

internal data class RequestDetailField(val label: String, val value: String)

internal fun requestDetailFields(row: RequestRow): List<RequestDetailField> = buildList {
    add(RequestDetailField("时间", formatDetailTime(row.timeMs)))
    add(RequestDetailField("模型", row.model))
    add(RequestDetailField("渠道", row.providerName.ifBlank { "未记录渠道" }))
    add(RequestDetailField("状态", if (row.status == 0) "—" else row.status.toString()))
    requestErrorExplanation(row)?.let { add(RequestDetailField("原因", it)) }
    add(RequestDetailField("耗时", if (row.ms > 0) "${row.ms} ms" else "—"))
    add(RequestDetailField("Token", "${row.inputTokens} 输入 / ${row.outputTokens} 输出"))
    add(RequestDetailField("成本", formatDetailCost(row.cost)))
}

/**
 * 只按 err_type 与状态码给解释，绝不回显上游原始错误正文——正文可能带请求片段。
 */
internal fun requestErrorExplanation(row: RequestRow): String? {
    if (row.status == 0 || row.ok) {
        return null
    }
    return when (row.errorType) {
        "auth" -> "上游鉴权失败（401）：账号凭证被拒绝，请检查该账号状态。"
        "rate", "rate_limit" -> "上游限流（429）：请求过密或额度耗尽，稍后重试。"
        "shape" -> "请求格式不被接受（400）：上游拒绝了请求字段。"
        "other" -> "上游或路由失败：请查看对应渠道与账号状态。"
        else -> "请求返回 ${row.status}：上游拒绝了本次调用。"
    }
}

@Composable
private fun RequestDetailDialog(row: RequestRow, onDismiss: () -> Unit) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "请求详情",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column {
                requestDetailFields(row).forEach { field ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 3.dp),
                    ) {
                        Text(
                            text = field.label,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.width(56.dp),
                        )
                        Text(
                            text = field.value,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurface,
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
                Spacer(Modifier.height(10.dp))
                Text(
                    text = "只读：不包含请求正文、会话 ID 或 caller key。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
    )
}

private val requestDetailTime: DateTimeFormatter =
    DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss", Locale.US)

private fun formatDetailTime(ms: Long): String {
    if (ms <= 0L) {
        return "—"
    }
    return Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(requestDetailTime)
}

private fun formatDetailCost(value: Double): String =
    if (value <= 0.0) "$0" else String.format(Locale.US, "$%.4f", value)

@Composable
private fun RequestsSummaryLine(page: RequestsData) {
    Text(
        text = "共 ${page.total} 条 · 平均 ${page.avgMs} ms · P95 ${page.p95Ms} ms",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun RequestsList(page: RequestsData, onSelect: (RequestRow) -> Unit) {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 14.dp, vertical = 6.dp),
        ) {
            page.rows.forEach { row ->
                RequestRowView(row, onClick = { onSelect(row) })
            }
        }
    }
}

@Composable
private fun RequestRowView(row: RequestRow, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = formatClock(row.timeMs),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(42.dp),
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = row.model,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "${row.providerName.ifBlank { "未知渠道" }} · ${row.inputTokens}/${row.outputTokens} tokens",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Column(horizontalAlignment = Alignment.End) {
            Text(
                text = if (row.status == 0) "—" else row.status.toString(),
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = if (row.ok) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
            )
            Text(
                text = if (row.ms > 0) "${row.ms} ms" else "—",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun RequestsPager(page: RequestsData, onPrev: () -> Unit, onNext: () -> Unit) {
    val pages = ((page.total + page.limit - 1) / page.limit).coerceAtLeast(1)
    val current = page.offset / page.limit + 1
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        McaButton(
            onClick = onPrev,
            style = McaButtonStyle.Outlined,
            enabled = page.offset > 0,
        ) { Text("上一页") }
        Text(
            text = "$current / $pages 页",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        McaButton(
            onClick = onNext,
            style = McaButtonStyle.Outlined,
            enabled = page.offset + page.limit < page.total,
        ) { Text("下一页") }
    }
}

@Composable
private fun RequestsStateCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.primaryContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f),
            )
            if (actionLabel != null && onAction != null) {
                Spacer(Modifier.height(14.dp))
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
            }
        }
    }
}

@Composable
private fun RequestsLoadingCard() {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = "正在读取请求账本…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun RequestsErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "读取请求失败",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.85f),
            )
            Text(
                text = "错误码：$code",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.75f),
            )
            Spacer(Modifier.height(14.dp))
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) { Text("重试") }
        }
    }
}

private val requestClock: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm", Locale.US)

private fun formatClock(ms: Long): String {
    if (ms <= 0L) {
        return "—"
    }
    return Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(requestClock)
}
