package com.muyuchat.aether.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
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
import com.muyuchat.aether.runtime.RequestsModelSeries
import com.muyuchat.aether.runtime.RequestsPage as RequestsData
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
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
            // 页面级标题必须走共享的 DeepPageTitle（DeepPagesLayoutTest 的源码合同：
            // 18 个页面统一用它，且不许再各自写 headline 系列字号当页面标题——注意
            // 那句断言是纯字符串包含，注释里也别写出那个属性名，否则照样判红）。
            // 副标题故意写短：DeepPageTitle 的副标题与标题同一行、maxLines=1，
            // 1.3× 下长副标题会被省略号截断（实测「…Toke…」）。细节留给下面的卡片与页脚。
            DeepPageTitle(
                title = "请求日志",
                subtitle = "持久化账本 · 耗时与 Token",
            )
        }
        if (exportState !is RequestsExportUiState.Idle) {
            item { RequestsExportCard(exportState, onExportCsv, onShareCsv, onDismissExport) }
        }
        when (state) {
            RequestsUiState.Idle -> item {
                DeepStateCard(
                    title = "还没有请求",
                    detail = "启动网关后，这里会一条条列出请求——耗时、Token 都能看到。",
                    tone = DeepTone.Info,
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            RequestsUiState.Loading -> item { RequestsLoadingCard() }
            is RequestsUiState.Failed -> item { RequestsErrorCard(state.code, state.message, onRefresh) }
            is RequestsUiState.Ready -> {
                item { RequestsKpiCard(state.page, query.period) }
                // 趋势卡放在 KPI 之后、明细之前（桌面 logs.js 也是这个顺序）。
                item { RequestsTrendCard(state.page, query.period) }
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
    val kpis = requestsKpis(page, period)
    val accents = listOf(
        AetherIdentityTone.BLUE.foreground(),
        AetherIdentityTone.GREEN.foreground(),
        AetherIdentityTone.CYAN.foreground(),
        AetherIdentityTone.PURPLE.foreground(),
        AetherIdentityTone.AMBER.foreground(),
    )
    val columns = if (isLargeFontScale()) 1 else 2
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("概览指标")
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = requestsPeriodLabel(period),
                tone = DeepTone.Neutral,
            )
        }
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            kpis.chunked(columns).forEachIndexed { rowIndex, rowItems ->
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    rowItems.forEachIndexed { colIndex, kpi ->
                        val accent = accents.getOrElse(rowIndex * columns + colIndex) {
                            MaterialTheme.colorScheme.primary
                        }
                        AetherSubSurface(
                            modifier = Modifier.weight(1f),
                            padding = PaddingValues(12.dp),
                        ) {
                            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Row(
                                    verticalAlignment = Alignment.CenterVertically,
                                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                                ) {
                                    Box(
                                        modifier = Modifier
                                            .size(6.dp)
                                            .clip(CircleShape)
                                            .background(accent),
                                    )
                                    Text(
                                        text = kpi.label,
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                }
                                Text(
                                    text = kpi.value,
                                    style = MaterialTheme.typography.titleMedium.copy(fontFeatureSettings = "tnum"),
                                    fontWeight = FontWeight.Bold,
                                    color = MaterialTheme.colorScheme.onSurface,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                                Text(
                                    text = kpi.detail,
                                    style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                                    color = AetherColors.textTertiary(),
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

/* ---------------- 趋势图 ---------------- */

/**
 * 趋势图的两种指标 / 两种形态（桌面 `usage.js:137-168` 的两组 seg 控件）。
 *
 * 只改画法，不重新取数：数据都来自同一次 `/api/usage/requests`（`series[]` 与 `chartBy.model`）。
 * 项目里没有图表库，也不打算引入——这里用 Canvas 自绘。
 */
internal enum class RequestsChartMetric(val label: String) {
    TOTAL("总计"),
    BY_MODEL("按模型"),
}

internal enum class RequestsChartForm(val label: String) {
    BARS("柱状"),
    AREA("面积"),
}

/** 一根柱在画布上的矩形（像素，原点左上）。 */
internal data class RequestsChartBar(val left: Float, val top: Float, val right: Float, val bottom: Float)

/** 折线/面积的一个顶点。 */
internal data class RequestsChartDot(val x: Float, val y: Float)

/** 图表固定高度：字号放大不改变画布高度，只用 x 轴标签稀疏化来避免拥挤。 */
private val TrendChartHeight = 132.dp

/** 峰值：空列表或全 0 → 0（画的时候按 0 处理，不编造比例尺）。 */
internal fun requestsChartPeak(values: List<Double>): Double =
    values.maxOrNull()?.coerceAtLeast(0.0) ?: 0.0

/** 失败占比：柱顶按这个比例染红；没有调用就是 0（不是 100%）。 */
internal fun requestsFailureRatio(calls: Long, errors: Long): Float {
    if (calls <= 0L || errors <= 0L) {
        return 0f
    }
    return (errors.toDouble() / calls.toDouble()).coerceIn(0.0, 1.0).toFloat()
}

private fun requestsBarHeight(value: Double, max: Double, height: Float): Float =
    if (max <= 0.0 || height <= 0f) 0f else (value.coerceIn(0.0, max) / max * height).toFloat()

/** 单序列柱：每天一根，柱宽 = 槽宽 ×(1-gap)。 */
internal fun requestsBarRects(
    values: List<Double>,
    max: Double,
    width: Float,
    height: Float,
    gapRatio: Float = 0.3f,
): List<RequestsChartBar> {
    if (values.isEmpty() || width <= 0f || height <= 0f) {
        return emptyList()
    }
    val slot = width / values.size
    val barWidth = slot * (1f - gapRatio.coerceIn(0f, 0.9f))
    return values.mapIndexed { index, value ->
        val left = index * slot + (slot - barWidth) / 2f
        val barHeight = requestsBarHeight(value, max, height)
        RequestsChartBar(left = left, top = height - barHeight, right = left + barWidth, bottom = height)
    }
}

/** 分组柱：同一天并排 [groupCount] 根，第 [groupIndex] 根的位置。 */
internal fun requestsGroupedBarRects(
    values: List<Double>,
    max: Double,
    width: Float,
    height: Float,
    groupCount: Int,
    groupIndex: Int,
    gapRatio: Float = 0.22f,
): List<RequestsChartBar> {
    if (values.isEmpty() || width <= 0f || height <= 0f || groupCount <= 0 || groupIndex !in 0 until groupCount) {
        return emptyList()
    }
    val slot = width / values.size
    val inner = slot * (1f - gapRatio.coerceIn(0f, 0.9f))
    val barWidth = inner / groupCount
    return values.mapIndexed { index, value ->
        val left = index * slot + (slot - inner) / 2f + groupIndex * barWidth
        val barHeight = requestsBarHeight(value, max, height)
        RequestsChartBar(left = left, top = height - barHeight, right = left + barWidth, bottom = height)
    }
}

/** 折线顶点：只有一个点时居中；多个点等距铺满宽度。 */
internal fun requestsLineDots(
    values: List<Double>,
    max: Double,
    width: Float,
    height: Float,
): List<RequestsChartDot> {
    if (values.isEmpty() || width <= 0f || height <= 0f) {
        return emptyList()
    }
    val step = if (values.size <= 1) 0f else width / (values.size - 1)
    return values.mapIndexed { index, value ->
        val x = if (values.size <= 1) width / 2f else index * step
        val y = height - requestsBarHeight(value, max, height)
        RequestsChartDot(x = x, y = y)
    }
}

/** 面积图的闭合顶点：折线 + 右下拉回左下（画填充用）。 */
internal fun requestsAreaDots(
    values: List<Double>,
    max: Double,
    width: Float,
    height: Float,
): List<RequestsChartDot> {
    val line = requestsLineDots(values, max, width, height)
    if (line.isEmpty()) {
        return emptyList()
    }
    return line + listOf(
        RequestsChartDot(x = line.last().x, y = height),
        RequestsChartDot(x = line.first().x, y = height),
    )
}

/**
 * x 轴要显示的下标：最多 [maxLabels] 个，首尾必含、中间均匀取。
 *
 * 1.3× 时标签更宽，靠"少画几个"而不是"挤在一起"来避免重叠（图表高度与字号无关）。
 */
internal fun requestsChartLabelIndices(count: Int, maxLabels: Int = 4): List<Int> {
    if (count <= 0) {
        return emptyList()
    }
    val limit = maxLabels.coerceIn(2, 12)
    if (count <= limit) {
        return (0 until count).toList()
    }
    val step = (count - 1).toFloat() / (limit - 1).toFloat()
    return (0 until limit).map { i -> Math.round(i * step).toInt().coerceIn(0, count - 1) }.distinct()
}

/** 图例：只列按模型时的序列名（成本线固定一条，单独写文案）。 */
internal fun requestsChartLegendNames(series: List<RequestsModelSeries>): List<String> =
    series.map { it.name }

/** 右轴成本线在图例里的固定文案。 */
internal const val REQUESTS_CHART_COST_LEGEND: String = "成本（右轴）"

@Composable
private fun RequestsTrendCard(page: RequestsData, period: String) {
    var metric by remember { mutableStateOf(RequestsChartMetric.TOTAL) }
    var form by remember { mutableStateOf(RequestsChartForm.BARS) }
    val scheme = MaterialTheme.colorScheme
    val dangerColor = AetherColors.danger()
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("趋势")
            Spacer(Modifier.weight(1f))
            Text(
                text = "总计 ${requestsCount(page.calls)} 次 · ${requestsCost(page.cost)}",
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = scheme.onSurfaceVariant,
            )
        }
        // 两组切换放在**可横滚的单行**里：1.3× 下按钮变宽，横滚比折行更省纵向空间，
        // 也满足"图例/控件单行不截断"。
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AetherSegmentedPill(
                options = RequestsChartMetric.entries,
                selected = metric,
                onSelect = { metric = it },
                label = { it.label },
            )
            AetherSegmentedPill(
                options = RequestsChartForm.entries,
                selected = form,
                onSelect = { form = it },
                label = { it.label },
            )
        }
        if (page.series.isEmpty()) {
            Text(
                text = "这个周期里还没有按天的调用量，所以趋势是空的。" +
                    "下一步：发出请求后回来刷新，或把周期切成「今日 / 近 7 天 / 近 30 天」对比。",
                style = MaterialTheme.typography.bodySmall,
                color = scheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        val totalCalls = page.series.map { it.calls.toDouble() }
        val totalCost = page.series.map { it.cost }
        val labels = page.series.map { it.label }
        val byModel = metric == RequestsChartMetric.BY_MODEL && page.modelSeries.isNotEmpty()
        val peakCalls = if (byModel) {
            requestsChartPeak(page.modelSeries.flatMap { s -> s.points.map { it.calls.toDouble() } })
        } else {
            requestsChartPeak(totalCalls)
        }
        val peakCost = requestsChartPeak(totalCost)
        val palette = listOf(
            AetherIdentityTone.BLUE.foreground(),
            AetherIdentityTone.PURPLE.foreground(),
            AetherIdentityTone.GREEN.foreground(),
            AetherIdentityTone.AMBER.foreground(),
            AetherIdentityTone.CYAN.foreground(),
            AetherIdentityTone.PINK.foreground(),
        )
        AetherSubSurface(padding = PaddingValues(12.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = "峰值 ${requestsCount(peakCalls.toLong())} 次",
                        style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                        color = scheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    Text(
                        text = "成本 ${requestsCost(peakCost)}",
                        style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                        color = scheme.onSurfaceVariant,
                        textAlign = TextAlign.End,
                    )
                }
                Canvas(
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(TrendChartHeight),
                ) {
                    val chartWidth = size.width
                    val chartHeight = size.height
                    val barRadius = CornerRadius(3.dp.toPx(), 3.dp.toPx())
                    // 基线：给柱/面积一个落脚点，也让"0"有视觉参照。
                    drawLine(
                        color = scheme.outlineVariant.copy(alpha = 0.5f),
                        start = Offset(0f, chartHeight),
                        end = Offset(chartWidth, chartHeight),
                        strokeWidth = 1.dp.toPx(),
                    )
                    if (byModel) {
                        if (form == RequestsChartForm.AREA) {
                            page.modelSeries.forEachIndexed { index, series ->
                                val color = palette[index % palette.size]
                                val values = series.points.map { it.calls.toDouble() }
                                val dots = requestsAreaDots(
                                    values = values,
                                    max = peakCalls,
                                    width = chartWidth,
                                    height = chartHeight,
                                )
                                if (dots.size >= 3) {
                                    val path = Path().apply {
                                        moveTo(dots.first().x, dots.first().y)
                                        dots.drop(1).forEach { lineTo(it.x, it.y) }
                                        close()
                                    }
                                    drawPath(
                                        path = path,
                                        brush = Brush.verticalGradient(
                                            colors = listOf(
                                                color.copy(alpha = 0.34f),
                                                color.copy(alpha = 0.04f),
                                            ),
                                        ),
                                    )
                                    val lineDots = requestsLineDots(values, peakCalls, chartWidth, chartHeight)
                                    if (lineDots.size >= 2) {
                                        val strokePath = Path().apply {
                                            moveTo(lineDots.first().x, lineDots.first().y)
                                            lineDots.drop(1).forEach { lineTo(it.x, it.y) }
                                        }
                                        drawPath(
                                            path = strokePath,
                                            color = color,
                                            style = Stroke(
                                                width = 1.75.dp.toPx(),
                                                cap = StrokeCap.Round,
                                                join = StrokeJoin.Round,
                                            ),
                                        )
                                    }
                                }
                            }
                        } else {
                            page.modelSeries.forEachIndexed { index, series ->
                                val color = palette[index % palette.size]
                                requestsGroupedBarRects(
                                    values = series.points.map { it.calls.toDouble() },
                                    max = peakCalls,
                                    width = chartWidth,
                                    height = chartHeight,
                                    groupCount = page.modelSeries.size,
                                    groupIndex = index,
                                ).forEach { bar ->
                                    val w = (bar.right - bar.left).coerceAtLeast(0f)
                                    val h = (bar.bottom - bar.top).coerceAtLeast(0f)
                                    if (w > 0f && h > 0f) {
                                        drawRoundRect(
                                            color = color,
                                            topLeft = Offset(bar.left, bar.top),
                                            size = Size(width = w, height = h),
                                            cornerRadius = barRadius,
                                        )
                                    }
                                }
                            }
                        }
                    } else {
                        if (form == RequestsChartForm.AREA) {
                            val areaDots = requestsAreaDots(totalCalls, peakCalls, chartWidth, chartHeight)
                            if (areaDots.size >= 3) {
                                val areaPath = Path().apply {
                                    moveTo(areaDots.first().x, areaDots.first().y)
                                    areaDots.drop(1).forEach { lineTo(it.x, it.y) }
                                    close()
                                }
                                drawPath(
                                    path = areaPath,
                                    brush = Brush.verticalGradient(
                                        colors = listOf(
                                            scheme.primary.copy(alpha = 0.34f),
                                            scheme.primary.copy(alpha = 0.03f),
                                        ),
                                    ),
                                )
                            }
                            val lineDots = requestsLineDots(totalCalls, peakCalls, chartWidth, chartHeight)
                            if (lineDots.size >= 2) {
                                val linePath = Path().apply {
                                    moveTo(lineDots.first().x, lineDots.first().y)
                                    lineDots.drop(1).forEach { lineTo(it.x, it.y) }
                                }
                                drawPath(
                                    path = linePath,
                                    color = scheme.primary,
                                    style = Stroke(
                                        width = 2.dp.toPx(),
                                        cap = StrokeCap.Round,
                                        join = StrokeJoin.Round,
                                    ),
                                )
                            }
                        } else {
                            val bars = requestsBarRects(totalCalls, peakCalls, chartWidth, chartHeight)
                            page.series.forEachIndexed { index, point ->
                                val bar = bars.getOrNull(index) ?: return@forEachIndexed
                                val w = (bar.right - bar.left).coerceAtLeast(0f)
                                val h = (bar.bottom - bar.top).coerceAtLeast(0f)
                                if (w > 0f && h > 0f) {
                                    drawRoundRect(
                                        color = scheme.primary.copy(alpha = 0.88f),
                                        topLeft = Offset(bar.left, bar.top),
                                        size = Size(width = w, height = h),
                                        cornerRadius = barRadius,
                                    )
                                    // 失败日：柱顶按失败占比染红（桌面同一语义，不把整天算失败）。
                                    val ratio = requestsFailureRatio(point.calls, point.errors)
                                    if (ratio > 0f) {
                                        val failedHeight = (h * ratio).coerceAtLeast(0f)
                                        drawRoundRect(
                                            color = dangerColor,
                                            topLeft = Offset(bar.left, bar.top),
                                            size = Size(width = w, height = failedHeight),
                                            cornerRadius = barRadius,
                                        )
                                    }
                                }
                            }
                        }
                    }
                    // 成本右轴折线：与柱子共用 x，但 y 按自己的峰值缩放。
                    val costDots = requestsLineDots(totalCost, peakCost, chartWidth, chartHeight)
                    if (costDots.size >= 2) {
                        val costPath = Path().apply {
                            moveTo(costDots.first().x, costDots.first().y)
                            costDots.drop(1).forEach { lineTo(it.x, it.y) }
                        }
                        drawPath(
                            path = costPath,
                            color = scheme.tertiary,
                            style = Stroke(
                                width = 2.dp.toPx(),
                                cap = StrokeCap.Round,
                                join = StrokeJoin.Round,
                            ),
                        )
                    }
                }
                // x 轴标签：只画稀疏后的下标，1.3× 也不会挤成一片或截断。
                val indices = requestsChartLabelIndices(labels.size)
                if (indices.isNotEmpty()) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                        indices.forEach { index ->
                            Text(
                                text = labels.getOrElse(index) { "" },
                                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                                color = AetherColors.textTertiary(),
                                maxLines = 1,
                            )
                        }
                    }
                }
            }
        }
        // 图例：单行、可横滚（按模型时列出模型名，成本线固定标注）。
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = if (totalCalls.any { it > 0.0 }) "调用（左轴）" else "调用（左轴）· 本周期无调用",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
            Text(
                text = REQUESTS_CHART_COST_LEGEND,
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
            if (byModel) {
                requestsChartLegendNames(page.modelSeries).forEachIndexed { index, name ->
                    Text(
                        text = name,
                        style = MaterialTheme.typography.labelSmall,
                        color = palette[index % palette.size],
                    )
                }
            }
        }
        if (period.isBlank()) {
            Text(
                text = "周期未知：趋势按服务端返回的 series 原样画，不推算。",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
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
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = "请求明细",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            AetherStatusChip(
                text = "共 ${page.total} 条",
                tone = DeepTone.Neutral,
            )
            Spacer(Modifier.weight(1f))
            AetherPillButton(
                text = if (exportState is RequestsExportUiState.Exporting) "导出中…" else "导出",
                onClick = onExportCsv,
                enabled = exportState !is RequestsExportUiState.Exporting,
            )
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_refresh,
                contentDescription = "刷新日志",
                onClick = onRefresh,
            )
        }
        // 时间范围（logs.js:161-165）
        AetherSegmentedPill(
            options = RequestsPeriods,
            selected = RequestsPeriods.firstOrNull { it.first == query.period } ?: RequestsPeriods.first(),
            onSelect = { onPeriod(it.first) },
            label = { it.second },
        )
        Spacer(Modifier.height(8.dp))
        // 搜索（logs.js:166-168）
        SearchField(
            query = query.search,
            onQueryChange = onSearch,
            placeholder = "搜索模型 / 渠道 / 账号 / 错误 / 会话…",
        )
        Spacer(Modifier.height(8.dp))
        // 全部 / 成功 / 失败（logs.js:169-172）+ 自动刷新（:173-185）。
        val statusOptions = listOf(
            RequestsStatus.ALL to "全部",
            RequestsStatus.OK to "成功",
            RequestsStatus.ERROR to "失败",
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            AetherSegmentedPill(
                options = statusOptions,
                selected = statusOptions.firstOrNull { it.first == query.status } ?: statusOptions.first(),
                onSelect = { onStatus(it.first) },
                label = { it.second },
            )
            Spacer(Modifier.weight(1f))
            AetherPillButton(
                text = if (autoRefresh) "自动刷新 · 开" else "自动刷新 · 关",
                onClick = onAutoRefresh,
                primary = autoRefresh,
            )
        }
        Spacer(Modifier.height(12.dp))
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
                AetherPillButton(
                    text = "显示全部",
                    onClick = { onFailedOnly(false) },
                )
            }
        } else {
            RequestColumnHeader()
            page.rows.forEachIndexed { index, row ->
                RequestRowView(row, onClick = { onSelect(row) })
                if (index < page.rows.lastIndex) {
                    HorizontalDivider(
                        thickness = McaHairline.Width,
                        color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f),
                    )
                }
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
            color = AetherColors.textTertiary(),
        )
    }
}

@Composable
private fun RequestsTab(label: String, selected: Boolean, onClick: () -> Unit) {
    AetherPillButton(
        text = label,
        onClick = onClick,
        primary = selected,
    )
}

/* ---------------- 行 ---------------- */

@Composable
private fun RequestRowView(row: RequestRow, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .aetherPressable()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = DeepRowVertical),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text(
                text = formatRowTime(row.timeMs),
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = AetherColors.textTertiary(),
            )
            AetherStatusChip(
                text = protocolLabel(row.kind),
                tone = DeepTone.Neutral,
            )
            if (row.stream) {
                AetherStatusChip(
                    text = "流式",
                    tone = DeepTone.Neutral,
                )
            }
            if (row.upstreamTruncated) {
                AetherStatusChip(
                    text = "上游截断",
                    tone = DeepTone.Warning,
                )
            }
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = requestStatusText(row),
                tone = when {
                    row.status == 0 -> DeepTone.Neutral
                    row.ok -> DeepTone.Info
                    else -> DeepTone.Danger
                },
            )
        }
        Text(
            text = row.model,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (row.providerId.isNotBlank() || row.providerName.isNotBlank()) {
                ProviderBrandPlate(
                    providerId = row.providerId,
                    providerName = row.providerName.ifBlank { row.providerId },
                    size = 20.dp,
                )
            }
            Text(
                text = requestProviderLine(row),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        requestErrorExplanation(row)?.let { explanation ->
            Text(
                text = if (row.errorType.isBlank()) explanation else "$explanation · ${row.errorType}",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.danger(),
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        AetherSubSurface(padding = PaddingValues(horizontal = 10.dp, vertical = 8.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RequestMetric("首字节 / 总耗时", requestTtftText(row), Modifier.weight(1f))
                    RequestMetric("输入 / 输出", requestTokenPairText(row), Modifier.weight(1f))
                }
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RequestMetric("输出速率", requestSpeedText(row), Modifier.weight(1f))
                    RequestMetric("费用", requestCostText(row), Modifier.weight(1f))
                }
            }
        }
    }
}

@Composable
private fun RequestMetric(label: String, value: String, modifier: Modifier = Modifier) {
    Column(modifier) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
            fontWeight = FontWeight.Medium,
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
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = "第 $current / $pages 页",
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
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
                Row(
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    AetherStatusChip(
                        text = if (row.status != 0 && !row.ok) "失败" else "成功",
                        tone = if (row.status != 0 && !row.ok) DeepTone.Danger else DeepTone.Info,
                    )
                    AetherStatusChip(
                        text = "HTTP ${if (row.status == 0) "—" else row.status}",
                        tone = DeepTone.Neutral,
                    )
                    AetherStatusChip(
                        text = row.kind.ifBlank { "http" },
                        tone = DeepTone.Neutral,
                    )
                    AetherStatusChip(
                        text = if (row.stream) "流式" else "一次性",
                        tone = DeepTone.Neutral,
                    )
                }
                if (err != null) {
                    AetherSubSurface(
                        color = AetherColors.dangerContainer(),
                        padding = PaddingValues(12.dp),
                    ) {
                        Column(Modifier.fillMaxWidth()) {
                            Text(
                                text = err,
                                style = MaterialTheme.typography.bodySmall,
                                fontWeight = FontWeight.SemiBold,
                                color = AetherColors.danger(),
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
                    AetherSubSurface(padding = PaddingValues(12.dp)) {
                        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text(
                                text = section.title,
                                style = MaterialTheme.typography.labelSmall,
                                fontWeight = FontWeight.SemiBold,
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
                                        style = MaterialTheme.typography.bodySmall.copy(fontFeatureSettings = "tnum"),
                                        color = MaterialTheme.colorScheme.onSurface,
                                        modifier = Modifier.weight(1f),
                                    )
                                }
                            }
                        }
                    }
                }
                Text(
                    text = "只读：不包含请求正文；错误正文来自上游，分享前请先脱敏。",
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
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
