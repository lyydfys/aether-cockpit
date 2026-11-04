package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.RequestRow
import com.muyuchat.aether.runtime.RequestsPage as RequestsData
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
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

/**
 * 请求日志页，1:1 对齐桌面 `internal/web/ui2/js/views/logs.js`：
 *
 * 1. KPI 五格（日志.js:152-157）：请求数 / 成功·失败 / 平均延迟 / Token / 费用；
 * 2. 「请求明细」卡（:159-160）：计数 chip + 时间范围（:161-165）+ 搜索（:166-168）
 *    + 全部/成功/失败（:169-172）+ 自动刷新（:173-185）+ 刷新（:186）；
 * 3. 列头（:205-213）：时间 / 协议 / 模型·渠道 / 首字节·总耗时 / 输入·输出 / 输出速率 / 费用 / 状态；
 * 4. 分页（:244-259）：显示 a–b / 共 N 条 + 首页·上一页·第 n/m 页·下一页·末页；
 * 5. 页脚说明（:190）；6. 详情抽屉（:263-325）。
 *
 * `viewModel` 默认取本页所属的同一个 [RequestsViewModel] 实例：`GatewayHome` 已经把
 * state/回调接线了，这里再拿一次是为了时间范围与搜索框（它俩不在旧参数里）。
 */
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
    viewModel: RequestsViewModel = viewModel(),
) {
    val query by viewModel.query.collectAsStateWithLifecycle()
    var autoRefresh by remember { mutableStateOf(false) }
    LaunchedEffect(autoRefresh) {
        while (autoRefresh) {
            kotlinx.coroutines.delay(5_000)
            if (autoRefresh) {
                onRefresh()
            }
        }
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item { MobileHeader(status) }
        item {
            Column {
                Text(
                    text = "请求日志",
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onBackground,
                )
                Text(
                    text = "持久化账本 · 错误解释与耗时、Token 明细",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (exportState !is RequestsExportUiState.Idle) {
            item { RequestsExportCard(exportState, onExportCsv, onShareCsv, onDismissExport) }
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
            is RequestsUiState.Failed -> item { RequestsErrorCard(state.code, state.message, onRefresh) }
            is RequestsUiState.Ready -> {
                item { RequestsKpiCard(state.page, query.period) }
                item {
                    RequestsDetailCard(
                        page = state.page,
                        query = query,
                        failedOnly = failedOnly,
                        autoRefresh = autoRefresh,
                        exportState = exportState,
                        onAutoRefresh = { autoRefresh = !autoRefresh },
                        onPeriod = viewModel::setPeriod,
                        onSearch = viewModel::setSearch,
                        onStatus = viewModel::setStatus,
                        onFailedOnly = onFailedOnly,
                        onRefresh = onRefresh,
                        onExportCsv = onExportCsv,
                        onSelect = onSelect,
                    )
                }
                if (state.page.total > 0) {
                    item {
                        RequestsPager(
                            page = state.page,
                            onFirst = viewModel::firstPage,
                            onPrev = onPrev,
                            onNext = onNext,
                            onLast = viewModel::lastPage,
                        )
                    }
                }
            }
        }
        item {
            Text(
                text = "日志来自本机账本 data/ledger（按天 JSONL）；不保存请求/回复正文，" +
                    "但错误信息可能含上游返回内容，分享前请先检查并脱敏。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (selected != null) {
        RequestDetailDialog(row = selected, onDismiss = onDismissDetail)
    }
}

/* ---------------- KPI ---------------- */

/** KPI 五格：内容、顺序与桌面 `logs.js:152-157` 一致，手机上纵向排列。 */
internal data class RequestsKpi(val label: String, val value: String, val detail: String)

internal fun requestsKpis(page: RequestsData, period: String): List<RequestsKpi> {
    val calls = page.calls.takeIf { it > 0 } ?: page.total
    val errors = page.errors
    val okCount = (calls - errors).coerceAtLeast(0)
    val tokens = page.inputTokens + page.outputTokens + page.cacheRead + page.cacheWrite
    return listOf(
        RequestsKpi("请求数", requestsCount(calls), "已持久化 · ${requestsPeriodLabel(period)}"),
        RequestsKpi(
            "成功 / 失败",
            "$okCount / $errors",
            if (calls > 0) "失败率 ${String.format(Locale.US, "%.1f", errors.toDouble() / calls * 100)}%" else "—",
        ),
        RequestsKpi("平均延迟", "${page.avgMs} ms", "P95 ${page.p95Ms} ms"),
        RequestsKpi("Token", requestsTokens(tokens), "缓存读 ${requestsTokens(page.cacheRead)}"),
        RequestsKpi(
            "费用",
            requestsCost(page.cost),
            if (page.unpriced > 0) "未计价 ${page.unpriced} 次" else "按当前价格表",
        ),
    )
}

@Composable
private fun RequestsKpiCard(page: RequestsData, period: String) {
    DeepCard {
        requestsKpis(page, period).forEachIndexed { index, kpi ->
            if (index > 0) {
                Spacer(Modifier.height(10.dp))
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = kpi.label,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = kpi.detail,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = kpi.value,
                    style = MaterialTheme.typography.titleMedium,
                    color = MaterialTheme.colorScheme.onSurface,
                    textAlign = TextAlign.End,
                )
            }
        }
    }
}

/* ---------------- 明细卡 ---------------- */

@Composable
private fun RequestsDetailCard(
    page: RequestsData,
    query: RequestsQuery,
    failedOnly: Boolean,
    autoRefresh: Boolean,
    exportState: RequestsExportUiState,
    onAutoRefresh: () -> Unit,
    onPeriod: (String) -> Unit,
    onSearch: (String) -> Unit,
    onStatus: (String) -> Unit,
    onFailedOnly: (Boolean) -> Unit,
    onRefresh: () -> Unit,
    onExportCsv: () -> Unit,
    onSelect: (RequestRow) -> Unit,
) {
    DeepCard(contentPadding = DeepListCardPadding) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "请求明细",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = "共 ${page.total} 条",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            McaButton(
                onClick = onExportCsv,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = exportState !is RequestsExportUiState.Exporting,
            ) { Text(if (exportState is RequestsExportUiState.Exporting) "导出中…" else "导出") }
            Spacer(Modifier.width(4.dp))
            McaIconButton(contentDescription = "刷新日志", onClick = onRefresh) {
                Icon(
                    painter = painterResource(R.drawable.ic_refresh),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.Optical.Action),
                )
            }
        }
        // 时间范围（logs.js:161-165）
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.fillMaxWidth()) {
            RequestsPeriods.forEach { (id, label) ->
                RequestsTab(label = label, selected = query.period == id, onClick = { onPeriod(id) })
            }
        }
        Spacer(Modifier.height(8.dp))
        // 搜索（logs.js:166-168）
        SearchField(
            query = query.search,
            onQueryChange = onSearch,
            placeholder = "搜索模型 / 渠道 / 账号 / 错误 / 会话…",
        )
        Spacer(Modifier.height(8.dp))
        // 全部 / 成功 / 失败（logs.js:169-172）+ 自动刷新（:173-185）
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.fillMaxWidth()) {
            RequestsTab("全部", query.status == RequestsStatus.ALL) { onStatus(RequestsStatus.ALL) }
            RequestsTab("成功", query.status == RequestsStatus.OK) { onStatus(RequestsStatus.OK) }
            RequestsTab("失败", query.status == RequestsStatus.ERROR) { onStatus(RequestsStatus.ERROR) }
            Spacer(Modifier.weight(1f))
            RequestsTab(if (autoRefresh) "自动刷新 · 开" else "自动刷新 · 关", autoRefresh, onAutoRefresh)
        }
        Spacer(Modifier.height(10.dp))
        if (page.rows.isEmpty()) {
            val filtered = query.status != RequestsStatus.ALL || query.search.isNotBlank()
            Text(
                text = if (filtered) "没有符合条件的请求" else "还没有请求",
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = if (filtered) {
                    "换一个筛选。"
                } else {
                    "接入客户端并发一次请求后，这里会出现审计记录。"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (failedOnly) {
                Spacer(Modifier.height(8.dp))
                McaButton(
                    onClick = { onFailedOnly(false) },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("显示全部") }
            }
        } else {
            RequestColumnHeader()
            page.rows.forEach { row ->
                RequestRowView(row, onClick = { onSelect(row) })
            }
        }
    }
}

/** 列头：只标数值列（时间/协议/模型·渠道在行内自明），顺序与桌面 :205-213 一致。 */
@Composable
private fun RequestColumnHeader() {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 4.dp, bottom = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "首字节 / 总耗时 · 输入 / 输出 · 输出速率 · 费用",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun RequestsTab(label: String, selected: Boolean, onClick: () -> Unit) {
    McaButton(
        onClick = onClick,
        style = if (selected) McaButtonStyle.Primary else McaButtonStyle.Outlined,
        size = McaButtonSize.CardAction,
    ) { Text(label) }
}

/* ---------------- 行 ---------------- */

@Composable
private fun RequestRowView(row: RequestRow, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = DeepRowVertical),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = formatRowTime(row.timeMs),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = protocolLabel(row.kind),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (row.stream) {
                Spacer(Modifier.width(6.dp))
                Text(
                    text = "流式",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (row.upstreamTruncated) {
                Spacer(Modifier.width(6.dp))
                Text(
                    text = "上游截断",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.tertiary,
                )
            }
        }
        Text(
            text = row.model,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Medium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = requestProviderLine(row),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        requestErrorExplanation(row)?.let { explanation ->
            Text(
                text = if (row.errorType.isBlank()) explanation else "$explanation · ${row.errorType}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(top = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RequestMetric("首字节 / 总耗时", requestTtftText(row), Modifier.weight(1f))
            RequestMetric("输入 / 输出", requestTokenPairText(row), Modifier.weight(1f))
        }
        Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            RequestMetric("输出速率", requestSpeedText(row), Modifier.weight(1f))
            RequestMetric("费用", requestCostText(row), Modifier.weight(1f))
            Text(
                text = requestStatusText(row),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Medium,
                color = when {
                    row.status == 0 -> MaterialTheme.colorScheme.onSurfaceVariant
                    row.ok -> MaterialTheme.colorScheme.primary
                    else -> MaterialTheme.colorScheme.error
                },
            )
        }
    }
}

@Composable
private fun RequestMetric(label: String, value: String, modifier: Modifier = Modifier) {
    Column(modifier) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
}

/* ---------------- 分页 ---------------- */

@Composable
private fun RequestsPager(
    page: RequestsData,
    onFirst: () -> Unit,
    onPrev: () -> Unit,
    onNext: () -> Unit,
    onLast: () -> Unit,
) {
    val pages = requestsPageCount(page.total.toInt())
    val current = (page.offset / RequestsPageSize + 1).coerceIn(1, pages)
    val start = page.offset + 1
    val end = page.offset + page.rows.size
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "显示 $start–$end / 共 ${page.total} 条",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = "第 $current / $pages 页",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.fillMaxWidth()) {
            PagerButton("首页", enabled = current > 1, onClick = onFirst, modifier = Modifier.weight(1f))
            PagerButton("上一页", enabled = current > 1, onClick = onPrev, modifier = Modifier.weight(1f))
            PagerButton("下一页", enabled = current < pages, onClick = onNext, modifier = Modifier.weight(1f))
            PagerButton("末页", enabled = current < pages, onClick = onLast, modifier = Modifier.weight(1f))
        }
    }
}

@Composable
private fun PagerButton(label: String, enabled: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    McaButton(
        onClick = onClick,
        style = McaButtonStyle.Outlined,
        size = McaButtonSize.CardAction,
        enabled = enabled,
        modifier = modifier,
    ) { Text(label) }
}

/* ---------------- 导出（移动端独有：桌面 logs.js 无导出） ---------------- */

@Composable
private fun RequestsExportCard(
    state: RequestsExportUiState,
    onExportCsv: () -> Unit,
    onShareCsv: () -> Unit,
    onDismissExport: () -> Unit,
) {
    when (state) {
        is RequestsExportUiState.Exporting -> DeepStateCard(
            title = "正在导出",
            detail = "服务端会按当前筛选生成 CSV；条数多时会慢一些。",
            tone = DeepTone.Info,
        )
        is RequestsExportUiState.Ready -> Column {
            DeepStateCard(
                title = "已生成 ${state.fileName}",
                detail = "共 ${state.rows} 条记录、${state.bytes} 字节。下一步选择保存位置或直接分享。",
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
                detail = when (state.code) {
                    "empty_csv" -> "没有可导出的记录（当前筛选下是空的）"
                    "gateway_not_running" -> "网关未启动：先回「概览」启动网关"
                    else -> "${state.message}（错误码：${state.code}）"
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

/* ---------------- 状态卡 ---------------- */

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

/* ---------------- 详情 ---------------- */

internal data class RequestDetailField(val label: String, val value: String)

internal data class RequestDetailSection(val title: String, val fields: List<RequestDetailField>)

/** 详情分节：与桌面 `logs.js:286-319` 的 section 一一对应，顺序也一致。 */
internal fun requestDetailSections(row: RequestRow): List<RequestDetailSection> = listOf(
    RequestDetailSection(
        title = "请求",
        fields = listOf(
            RequestDetailField("请求 ID", row.id.ifBlank { "—" }),
            RequestDetailField("时间", formatDetailTime(row.timeMs)),
            RequestDetailField("模型", row.model),
            RequestDetailField(
                "请求模型",
                if (row.requestedModel.isNotBlank() && row.requestedModel != row.model) {
                    row.requestedModel
                } else {
                    "（与模型同名）"
                },
            ),
            RequestDetailField("客户端", row.agent.ifBlank { "—" }),
            RequestDetailField("会话", row.session.ifBlank { "—" }),
        ),
    ),
    RequestDetailSection(
        title = "路由与渠道",
        fields = listOf(
            RequestDetailField(
                "渠道",
                listOf(row.providerName, row.providerId).filter { it.isNotBlank() }.joinToString(" · ")
                    .ifBlank { "未记录渠道" },
            ),
            RequestDetailField(
                "账号",
                listOf(row.accountName, row.accountId).filter { it.isNotBlank() }.joinToString(" · ")
                    .ifBlank { "—" },
            ),
            RequestDetailField("路由 ID", if (row.routeId > 0) "#${row.routeId}" else "—"),
            RequestDetailField(
                "尝试次数",
                "${row.tries} 次" + when {
                    row.handedOn -> " · 曾失败转移"
                    row.retried -> " · 重试成功"
                    else -> ""
                },
            ),
            RequestDetailField(
                "上下文",
                "条目 ${row.items}" +
                    (if (row.previousId) " · 续接上一轮" else "") +
                    (if (row.autoContinued) " · 自动续接（裁剪 ${row.trimmedItems}）" else ""),
            ),
            RequestDetailField("路径", if (row.fastPath) "Fast-Path 零损直通" else "IR 转换"),
        ),
    ),
    RequestDetailSection(
        title = "耗时",
        fields = listOf(
            RequestDetailField("首字节", if (row.ttftMs > 0) "${row.ttftMs} ms" else "—"),
            RequestDetailField("总耗时", if (row.ms > 0) "${row.ms} ms" else "—"),
            RequestDetailField("输出速度", requestSpeedText(row)),
        ),
    ),
    RequestDetailSection(
        title = "Token 明细",
        fields = listOf(
            RequestDetailField("输入 Token", requestsTokens(row.inputTokens)),
            RequestDetailField("缓存读取", requestsTokens(row.cacheRead)),
            RequestDetailField("缓存写入", requestsTokens(row.cacheWrite)),
            RequestDetailField("输出 Token", requestsTokens(row.outputTokens)),
            RequestDetailField("推理 Token", requestsTokens(row.reasoningTokens)),
            RequestDetailField(
                "总 Token",
                requestsTokens(row.inputTokens + row.cacheRead + row.cacheWrite + row.outputTokens),
            ),
        ),
    ),
    RequestDetailSection(
        title = "计费详情",
        fields = listOf(
            RequestDetailField("计价模型", row.pricingModel.ifBlank { "—" }),
            RequestDetailField("计价状态", if (row.priced) "按 Token" else "未匹配价格"),
            RequestDetailField("总费用", if (row.priced) requestsCost(row.cost) else "—"),
        ),
    ),
)

/** 拍平版（测试与既有调用点用）：顺序 = 上面五节的顺序。 */
internal fun requestDetailFields(row: RequestRow): List<RequestDetailField> =
    requestDetailSections(row).flatMap { it.fields }

@Composable
private fun RequestDetailDialog(row: RequestRow, onDismiss: () -> Unit) {
    val err = requestErrorExplanation(row)
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "日志详情 · ${row.model}",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(
                        text = if (row.status != 0 && !row.ok) "失败" else "成功",
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Medium,
                        color = if (row.status != 0 && !row.ok) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.primary
                        },
                    )
                    Text(
                        text = "HTTP ${if (row.status == 0) "—" else row.status}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = row.kind.ifBlank { "http" },
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = if (row.stream) "流式" else "一次性",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (err != null) {
                    Surface(
                        shape = McaShape.CardAction,
                        color = McaTonal.fill(),
                        border = McaHairline.stroke(),
                    ) {
                        Column(Modifier.fillMaxWidth().padding(12.dp)) {
                            Text(
                                text = err,
                                style = MaterialTheme.typography.bodySmall,
                                fontWeight = FontWeight.Medium,
                                color = MaterialTheme.colorScheme.error,
                            )
                            if (row.errorMessage.isNotBlank()) {
                                Spacer(Modifier.height(6.dp))
                                Text(
                                    text = row.errorMessage,
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                }
                requestDetailSections(row).forEach { section ->
                    Text(
                        text = section.title,
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    section.fields.forEach { field ->
                        Row(Modifier.fillMaxWidth()) {
                            Text(
                                text = field.label,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.widthIn(min = 72.dp),
                            )
                            Text(
                                text = field.value,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurface,
                                modifier = Modifier.weight(1f),
                            )
                        }
                    }
                }
                Text(
                    text = "只读：不包含请求正文；错误正文来自上游，分享前请先脱敏。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
    )
}

/* ---------------- 纯格式化（单测覆盖） ---------------- */

/** 协议标签，与桌面 `logs.js:8-14` 同映射。 */
internal fun protocolLabel(raw: String): String {
    val value = raw.ifBlank { "http" }
    return when (value) {
        "openai_chat" -> "chat"
        "anthropic_messages" -> "anthropic"
        "openai_responses", "responses" -> "responses"
        else -> value.removePrefix("openai_").removeSuffix("_messages")
    }
}

/** 提示 Token = 输入 + 缓存读 + 缓存写（桌面 `logs.js:6`）。 */
internal fun requestPromptTokens(row: RequestRow): Long =
    row.inputTokens + row.cacheRead + row.cacheWrite

/**
 * 首字节 / 总耗时，与桌面 `logs.js:221,235` 同形状。
 *
 * 只有一处刻意的移动端口径：总耗时 <= 0 时写「—」而不是「0 ms」——
 * 0 是"量到了但为 0"，缺值是这个字段没记，两者含义不同（旧行格式测试同样立场）。
 */
internal fun requestTtftText(row: RequestRow): String {
    val ttft = if (row.ttftMs > 0) "${row.ttftMs} / " else "— / "
    return ttft + if (row.ms > 0) "${row.ms} ms" else "—"
}

/** 输入 / 输出（按桌面提示 Token 口径，两个都为 0 时写「—」）。 */
internal fun requestTokenPairText(row: RequestRow): String {
    val input = requestPromptTokens(row)
    val output = row.outputTokens
    return if (input == 0L && output == 0L) "—" else "${requestsTokens(input)} / ${requestsTokens(output)}"
}

/** 输出速率：只算首字节之后的解码时间，没有 TTFT 时退回总耗时（桌面 `logs.js:17-23`）。 */
internal fun requestOutputSpeed(row: RequestRow): Double {
    val out = row.outputTokens
    val decode = if (row.ttftMs > 0 && row.ms > row.ttftMs) row.ms - row.ttftMs else row.ms
    return if (out > 0 && decode > 0) out.toDouble() / decode * 1000 else 0.0
}

internal fun requestSpeedText(row: RequestRow): String {
    val speed = requestOutputSpeed(row)
    return if (speed <= 0) "—" else String.format(Locale.US, "%.1f tok/s", speed)
}

/** 费用：未计价写「未计价」，否则按桌面 `format.js:15-23` 的小额多留小数。 */
internal fun requestCostText(row: RequestRow): String = if (row.priced) requestsCost(row.cost) else "未计价"

/** 渠道 / 账号 / 客户端 / 快速直通（桌面 `logs.js:220`）。 */
internal fun requestProviderLine(row: RequestRow): String {
    val bits = buildList {
        add(row.providerName.ifBlank { row.providerId })
        add(row.accountName.ifBlank { row.accountId })
        add(row.agent)
        if (row.fastPath) add("快速直通")
    }.filter { it.isNotBlank() }
    return bits.joinToString(" · ").ifBlank { "—" }
}

/**
 * 只按 err_type 与状态码给解释，绝不回显上游原始错误正文——正文可能带请求片段。
 * 文案取自桌面 `logs.js:29-40` 的 title 一列。
 */
internal fun requestErrorExplanation(row: RequestRow): String? {
    if (row.status == 0 || row.ok) {
        return null
    }
    return when (row.errorType) {
        "auth" -> "渠道鉴权失败"
        "rate", "rate_limit" -> "上游限流 / 额度耗尽"
        "shape" -> "请求被上游拒绝"
        "internal" -> "网关内部错误"
        "other" -> "请求失败"
        else -> if (row.status >= 500) "上游请求失败" else "请求失败"
    }
}

/** 状态词：与桌面 `logs.js:240` 同形状（「成功 200」/「失败 401」）。 */
internal fun requestStatusText(row: RequestRow): String = when {
    row.status == 0 -> "—"
    row.ok -> "成功 ${row.status}"
    else -> "失败 ${row.status}"
}

/** 行内渠道名的旧文案（详情页另用「未记录渠道」）。 */
internal fun requestProviderText(row: RequestRow): String =
    row.providerName.ifBlank { "未知渠道" }

/** Token 计数：与桌面 `format.js:3-9` 的 K/M/B 单位一致。 */
internal fun requestsTokens(value: Long): String {
    val v = value.coerceAtLeast(0L)
    return when {
        v >= 1_000_000_000L -> String.format(Locale.US, "%.1fB", v / 1e9)
        v >= 1_000_000L -> String.format(Locale.US, "%.1fM", v / 1e6)
        v >= 1_000L -> String.format(Locale.US, "%.1fK", v / 1e3)
        else -> v.toString()
    }
}

/** 请求数：桌面直接打印整数，这里同口径。 */
internal fun requestsCount(value: Long): String = value.coerceAtLeast(0L).toString()

/** 费用：与桌面 `format.js:15-23` 一致（0 → $0；<0.01 → 6 位；<1 → 4 位；否则 2 位）。 */
internal fun requestsCost(value: Double): String {
    val v = if (value.isNaN() || value < 0) 0.0 else value
    return when {
        v == 0.0 -> "$0"
        v < 0.01 -> String.format(Locale.US, "$%.6f", v)
        v < 1.0 -> String.format(Locale.US, "$%.4f", v)
        else -> String.format(Locale.US, "$%.2f", v)
    }
}

private val rowTimeFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("MM-dd HH:mm", Locale.US)

private fun formatRowTime(ms: Long): String {
    if (ms <= 0L) {
        return "—"
    }
    return Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(rowTimeFormat)
}

private val detailTimeFormat: DateTimeFormatter =
    DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss", Locale.US)

private fun formatDetailTime(ms: Long): String {
    if (ms <= 0L) {
        return "—"
    }
    return Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(detailTimeFormat)
}
