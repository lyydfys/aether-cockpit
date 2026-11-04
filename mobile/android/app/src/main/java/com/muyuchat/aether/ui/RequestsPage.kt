package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
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
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.RequestRow
import com.muyuchat.aether.runtime.RequestsPage as RequestsData
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale


/* 深层页面共享排版原语（DeepCard / DeepSectionTitle / DeepStateCard / DeepNotice …）
 * 已迁到 DeepPageScaffold.kt —— 同包，调用点无需 import。
 * 那里是全站唯一定义处，改签名请一次改完所有调用点。 */

/* ========================================================================== */

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
    exportState: RequestsExportUiState = RequestsExportUiState.Idle,
    onExportCsv: () -> Unit = {},
    onShareCsv: () -> Unit = {},
    onDismissExport: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "请求",
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "今日请求账本 · 只读",
                        style = MaterialTheme.typography.bodySmall,
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
            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                McaButton(
                    onClick = { onFailedOnly(false) },
                    style = if (!failedOnly) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                ) { Text("全部") }
                McaButton(
                    onClick = { onFailedOnly(true) },
                    style = if (failedOnly) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                ) { Text("仅失败") }
                Spacer(Modifier.weight(1f))
                McaButton(
                    onClick = onExportCsv,
                    style = McaButtonStyle.Outlined,
                    enabled = exportState !is RequestsExportUiState.Exporting,
                ) { Text(if (exportState is RequestsExportUiState.Exporting) "导出中…" else "导出 CSV") }
            }
        }
        // 导出结果：说清楚"导了多少条、去哪一步"，不说"已备份"。
        if (exportState !is RequestsExportUiState.Idle) {
            item {
                when (exportState) {
                    is RequestsExportUiState.Exporting -> DeepStateCard(
                        title = "正在导出",
                        detail = "服务端会按当前筛选生成 CSV；条数多时会慢一些。",
                        tone = DeepTone.Info,
                    )
                    is RequestsExportUiState.Ready -> Column {
                        DeepStateCard(
                            title = "已生成 ${exportState.fileName}",
                            detail = "共 ${exportState.rows} 条记录、${exportState.bytes} 字节。" +
                                "下一步选择保存位置或直接分享。" +
                                "CSV 保留台账全部列（与桌面导出一致），请勿分享给他人。",
                            tone = DeepTone.Info,
                            actionLabel = "分享 / 保存",
                            onAction = onShareCsv,
                        )
                        Spacer(Modifier.height(12.dp))
                        McaButton(onClick = onDismissExport, style = McaButtonStyle.Outlined) { Text("关闭") }
                    }
                    is RequestsExportUiState.Failed -> Column {
                        DeepStateCard(
                            title = "导出失败",
                            detail = when (exportState.code) {
                                "empty_csv" -> "没有可导出的记录（当前筛选下是空的）"
                                "gateway_not_running" -> "网关未启动：先回「概览」启动网关"
                                else -> "${exportState.message}（错误码：${exportState.code}）"
                            },
                            tone = DeepTone.Danger,
                            actionLabel = "重试",
                            onAction = onExportCsv,
                        )
                        Spacer(Modifier.height(12.dp))
                        McaButton(onClick = onDismissExport, style = McaButtonStyle.Outlined) { Text("关闭") }
                    }
                    RequestsExportUiState.Idle -> Unit
                }
            }
        }
        when (state) {
            RequestsUiState.Idle -> item {
                DeepStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机请求账本。",
                    tone = DeepTone.Info,
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
                        // 与桌面 logs.js:202 同句：标题说清是哪一种空，正文说下一步。
                        DeepStateCard(
                            title = if (failedOnly) "没有失败请求" else "还没有请求",
                            detail = if (failedOnly) {
                                "当前筛选只保留失败请求，这一页没有失败记录。换回「全部」查看其它请求。"
                            } else {
                                "接入客户端并发一次请求后，这里会出现审计记录。"
                            },
                            actionLabel = if (failedOnly) "显示全部" else "刷新",
                            onAction = if (failedOnly) ({ onFailedOnly(false) }) else onRefresh,
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
 * 状态词。与桌面端 `logs.js:240` 同形状（「成功 200」/「失败 401」）：
 * 列表里**只有这一个词染色**，时间、渠道、耗时、Token 一律中性色。
 */
internal fun requestStatusText(row: RequestRow): String = when {
    row.status == 0 -> "—"
    row.ok -> "成功 ${row.status}"
    else -> "失败 ${row.status}"
}

/** 右列第二行：耗时 + 输入/输出 Token，作为一整串右对齐。 */
internal fun requestLatencyTokensText(row: RequestRow): String {
    val latency = if (row.ms > 0) "${row.ms} ms" else "—"
    return "$latency · ${row.inputTokens}/${row.outputTokens} tok"
}

/** 行内渠道名：缺失时给明确文案，不留空（详情页另用「未记录渠道」）。 */
internal fun requestProviderText(row: RequestRow): String =
    row.providerName.ifBlank { "未知渠道" }

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
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                requestDetailFields(row).forEach { field ->
                    Row(Modifier.fillMaxWidth()) {
                        Text(
                            text = field.label,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.widthIn(min = 56.dp),
                        )
                        Text(
                            text = field.value,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
                Spacer(Modifier.height(12.dp))
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
    DeepCard(contentPadding = DeepListCardPadding) {
        page.rows.forEach { row ->
            RequestRowView(row, onClick = { onSelect(row) })
        }
    }
}

@Composable
private fun RequestRowView(row: RequestRow, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = formatClock(row.timeMs),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            // 最小宽度而不是定宽：1.3× 字号下「12:34」变宽也不会被截。
            modifier = Modifier.widthIn(min = 44.dp),
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            // 长模型名放两行再省略，避免一行里被挤成「claude-sub/cla…」。
            Text(
                text = row.model,
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = requestProviderText(row),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Column(horizontalAlignment = Alignment.End) {
            Text(
                text = requestStatusText(row),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Medium,
                // 只有状态词染色；其余一律中性。
                color = when {
                    row.status == 0 -> MaterialTheme.colorScheme.onSurfaceVariant
                    row.ok -> MaterialTheme.colorScheme.primary
                    else -> MaterialTheme.colorScheme.error
                },
            )
            Text(
                text = requestLatencyTokensText(row),
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
private fun RequestsLoadingCard() {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = "正在读取请求账本…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun RequestsErrorCard(code: String, message: String, onRetry: () -> Unit) {
    DeepStateCard(
        title = "读取请求失败",
        detail = "$message（错误码：$code）",
        tone = DeepTone.Danger,
        actionLabel = "重试",
        onAction = onRetry,
    )
}

private val requestClock: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm", Locale.US)

private fun formatClock(ms: Long): String {
    if (ms <= 0L) {
        return "—"
    }
    return Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(requestClock)
}
