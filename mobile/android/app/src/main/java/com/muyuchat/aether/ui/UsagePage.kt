package com.muyuchat.aether.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.CheckinVendor
import com.muyuchat.aether.runtime.MoveDirection
import com.muyuchat.aether.runtime.moveProvider
import com.muyuchat.aether.runtime.QuotaHistoryAccount
import com.muyuchat.aether.runtime.QuotaHistoryLine
import com.muyuchat.aether.runtime.QuotaHistoryPoint
import com.muyuchat.aether.runtime.QuotaHoldWindow
import com.muyuchat.aether.runtime.UsageBreakdownRow
import com.muyuchat.aether.runtime.UsageDayPoint
import com.muyuchat.aether.runtime.UsageSummary
import com.muyuchat.aether.runtime.formatTokens
import com.muyuchat.aether.runtime.formatUsageAmount
import com.muyuchat.aether.runtime.formatUsageCount
import com.muyuchat.aether.runtime.normalizeQuotaPoints
import com.muyuchat.aether.runtime.percentText
import com.muyuchat.aether.runtime.quotaAccountLabel
import com.muyuchat.aether.runtime.quotaConsumedPercent
import com.muyuchat.aether.runtime.quotaHistoryEmptyHint
import com.muyuchat.aether.runtime.quotaHoldsWhyLabel
import com.muyuchat.aether.runtime.usageCacheHitRatePercent
import com.muyuchat.aether.runtime.usageSuccessRatePercent
import com.muyuchat.aether.runtime.vendorCheckinLine
import com.muyuchat.aether.runtime.vendorCheckinSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaShape
import java.util.Locale

private val TightGap = 8.dp
private val TrendPlotHeight = 96.dp
private val TrendBarMinHeight = 2.dp

/**
 * 用量页（对标桌面 `internal/web/ui2/js/views/usage.js`）。
 *
 * 桌面区块顺序（`usage.js:42-51` 的 render）：
 * 时间范围 + 计价币种条 → KPI 四格 → 趋势 → 用量分布（渠道/模型/调用方）→ 用量日历。
 * 手机端按同一顺序落位；桌面没有的「价格表」卡已移出本页（价格表在桌面设置里），
 * 「按模型」独立卡并入用量分布的「模型」tab。
 */
@Composable
internal fun UsagePage(
    status: GatewayStatus,
    state: UsageUiState,
    period: UsagePeriod,
    onPeriod: (UsagePeriod) -> Unit,
    onRefresh: () -> Unit,
    onOpenOverview: () -> Unit,
    detail: UsageDetailUiState = UsageDetailUiState.Idle,
    onRefreshDetail: () -> Unit = {},
    modifier: Modifier = Modifier,
    viewModel: UsageViewModel = viewModel(),
) {
    // 与 GatewayHome 共用同一个 ViewModelStoreOwner（同 OverviewPage 的取法），
    // 所以新增的「分类 tab / 币种」状态不必改 GatewayHome 的调用点。
    val breakdown by viewModel.breakdown.collectAsStateWithLifecycle()
    val pricing by viewModel.pricing.collectAsStateWithLifecycle()
    val currencyError by viewModel.currencyError.collectAsStateWithLifecycle()
    // 额度历史与周期估算：本节自己读（不改 GatewayHome 的接线点）。
    val quotaHistory by viewModel.quotaHistory.collectAsStateWithLifecycle()
    val quotaHolds by viewModel.quotaHolds.collectAsStateWithLifecycle()
    // 卡片顺序：纯本地偏好（服务端 `/api/usage/arrange` 是空实现，见 UsageCardKey 上方注释）。
    // 存成逗号串而不是 List<String>：`rememberSaveable` 的自动 saver 对 ArrayList 之外
    // 的集合类型不稳，字符串是 Bundle 里最稳的形态。
    var cardOrderRaw by rememberSaveable { mutableStateOf(DefaultUsageCardOrder.joinToString(",")) }
    val cardOrder = remember(cardOrderRaw) {
        normalizeUsageCardOrder(cardOrderRaw.split(","))
    }
    var showCardOrder by remember { mutableStateOf(false) }
    val vendorCheckin by viewModel.vendorCheckin.collectAsStateWithLifecycle()
    LaunchedEffect(status.running) {
        if (status.running) {
            viewModel.loadQuotaHistory()
        }
    }

    val currency = pricing?.currency?.takeIf { it.isNotBlank() } ?: "USD"
    val rate = pricing?.usdCny?.takeIf { it > 0.0 } ?: 7.2

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                UsageTitle(state, onRefresh)
            }
        }
        // 卡片内容按 `cardOrder` 顺序渲染（顺序是纯本地偏好）。
        // 时间范围条也在其中：它跟其它卡一样是"页面上的一块"，但不包含页头标题。
        cardOrder.forEach { key ->
            item(key = key) {
                when (key) {
                    UsageCardKey.SCOPE.name -> UsageScopeBar(
                        period = period,
                        onPeriod = onPeriod,
                        currency = currency,
                        usdCny = rate,
                        pricingKnown = pricing != null,
                        onCurrency = viewModel::setCurrency,
                        onOpenCardOrder = { showCardOrder = true },
                    )
                    UsageCardKey.KPI.name -> (state as? UsageUiState.Ready)?.let { ready ->
                        UsageKpiCard(ready.summary, period, currency, rate)
                    }
                    UsageCardKey.TREND.name -> (state as? UsageUiState.Ready)?.let { ready ->
                        UsageTrendCard(ready.summary.series, period, currency, rate)
                    }
                    UsageCardKey.BREAKDOWN.name -> (state as? UsageUiState.Ready)?.let { ready ->
                        UsageBreakdownCard(
                            summary = ready.summary,
                            breakdown = breakdown,
                            onBreakdown = viewModel::setBreakdown,
                            currency = currency,
                            rate = rate,
                            onOpenOverview = onOpenOverview,
                        )
                    }
                    UsageCardKey.CHECKIN.name -> (state as? UsageUiState.Ready)?.let {
                        UsageVendorCheckinCard(
                            state = vendorCheckin,
                            onCheckin = viewModel::checkinVendor,
                            onDismiss = viewModel::dismissVendorCheckin,
                        )
                    }
                    UsageCardKey.QUOTA_HISTORY.name -> (state as? UsageUiState.Ready)?.let {
                        UsageQuotaHistoryCard(
                            history = quotaHistory,
                            holds = quotaHolds,
                            onRetry = { viewModel.loadQuotaHistory() },
                        )
                    }
                    UsageCardKey.CALENDAR.name -> (state as? UsageUiState.Ready)?.let {
                        when (detail) {
                            UsageDetailUiState.Idle, UsageDetailUiState.Loading ->
                                UsageLoadingCard("正在读取用量日历…")
                            is UsageDetailUiState.Failed -> DeepStateCard(
                                title = "用量日历读不到",
                                detail = usageDetailErrorLine(detail.code, detail.message),
                                tone = DeepTone.Danger,
                                actionLabel = "重试",
                                onAction = onRefreshDetail,
                            )
                            is UsageDetailUiState.Ready -> UsageHeatmapSection(
                                days = detail.heatmap.recentDays(30, java.time.LocalDate.now().toString()),
                                error = detail.errors["heatmap"],
                                rangeLabel = usageCalendarRangeLabel(detail.heatmap.from, detail.heatmap.to),
                            )
                        }
                    }
                }
            }
        }
        currencyError?.let { message ->
            item {
                Text(
                    text = message,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
        // 空/加载/失败态仍然是**固定位置**（在卡片区之上），不参与排序：
        // 这时候页面上没有可排序的卡片，把错误挪到 KPI 下面就说不清"哪块失败了"。
        when (state) {
            UsageUiState.Idle -> item {
                DeepStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后才能读取本机账本；账本只记录这台手机自己处理过的请求。",
                    tone = DeepTone.Info,
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            UsageUiState.Loading -> item { UsageLoadingCard("正在读取本机用量…") }
            is UsageUiState.Failed -> item {
                DeepStateCard(
                    title = "读取用量失败",
                    detail = usageDetailErrorLine(state.code, state.message),
                    tone = DeepTone.Danger,
                    actionLabel = "重试",
                    onAction = onRefresh,
                )
            }
            is UsageUiState.Ready -> Unit
        }
        item {
            Text(
                text = "只读：数据来自本机 /api/usage 与 /api/usage/heatmap，不展示请求正文，也不上传到云端。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    // 卡片排序面板：顺序只存在本地保存态里（不调 /api/usage/arrange，理由见 UsageCardKey 注释）。
    if (showCardOrder) {
        UsageCardOrderDialog(
            order = cardOrder,
            onMove = { key, direction ->
                cardOrderRaw = moveUsageCard(cardOrder, key, direction).joinToString(",")
            },
            onReset = { cardOrderRaw = DefaultUsageCardOrder.joinToString(",") },
            onDismiss = { showCardOrder = false },
        )
    }
}

/**
 * 「卡片排序」面板：每行一张卡 + ↑/↓。
 *
 * 首行 ↑、末行 ↓ **置灰不隐藏**——隐藏会让列表长短不一，置灰才说明"到顶了"。
 * 面板本身不落盘，改动即时写回 `rememberSaveable` 的顺序串。
 */
@Composable
private fun UsageCardOrderDialog(
    order: List<String>,
    onMove: (String, MoveDirection) -> Unit,
    onReset: () -> Unit,
    onDismiss: () -> Unit,
) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("卡片排序", fontWeight = FontWeight.Bold) },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 420.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Text(
                    text = "顺序只保存在本机（应用内重启后仍在）；网关不保存排序。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                order.forEach { key ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = usageCardLabel(key),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f),
                        )
                        Spacer(Modifier.width(8.dp))
                        McaButton(
                            onClick = { onMove(key, MoveDirection.Up) },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            enabled = canMoveUsageCard(order, key, MoveDirection.Up),
                        ) { Text("↑", maxLines = 1) }
                        Spacer(Modifier.width(4.dp))
                        McaButton(
                            onClick = { onMove(key, MoveDirection.Down) },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            enabled = canMoveUsageCard(order, key, MoveDirection.Down),
                        ) { Text("↓", maxLines = 1) }
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(onClick = onReset, style = McaButtonStyle.Outlined) { Text("恢复默认顺序") }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("完成") }
        },
    )
}

@Composable
private fun UsageTitle(state: UsageUiState, onRefresh: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(TightGap),
        ) {
            DeepPageTitle(
                title = "用量",
                subtitle = "本机账本 · 只读",
            )
        }
        AetherHeaderIconButton(
            iconRes = R.drawable.ic_refresh,
            contentDescription = "刷新用量",
            onClick = onRefresh,
            enabled = state is UsageUiState.Ready || state is UsageUiState.Failed,
        )
    }
}

/**
 * 时间范围 + 计价币种（桌面 `usage.js:57-86` 的 currencyBar）。
 *
 * 桌面上两行并排；手机窄，折成两行：第一行三个时间范围，第二行币种与汇率。
 */
@Composable
private fun UsageScopeBar(
    period: UsagePeriod,
    onPeriod: (UsagePeriod) -> Unit,
    currency: String,
    usdCny: Double,
    pricingKnown: Boolean,
    onCurrency: (String) -> Unit,
    /** 「卡片排序」入口；null 时不渲染（页面未接线时保持原样）。 */
    onOpenCardOrder: (() -> Unit)? = null,
) {
    val activeCurrency = if (currency.equals("CNY", ignoreCase = true)) "CNY" else "USD"
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        AetherSegmentedPill(
            options = UsagePeriod.entries,
            selected = period,
            onSelect = onPeriod,
            label = { it.label },
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(TightGap),
        ) {
            Text(
                text = "计价币种",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            AetherSegmentedPill(
                options = listOf("USD", "CNY"),
                selected = activeCurrency,
                onSelect = onCurrency,
                label = { if (it == "USD") "\$ USD" else "¥ CNY" },
            )
            Spacer(Modifier.weight(1f))
            if (onOpenCardOrder != null) {
                AetherPillButton(
                    text = "卡片排序",
                    onClick = onOpenCardOrder,
                )
            }
        }
        // 汇率单独一行：1.3× 下与币种按钮挤同一行会被省略号截断（「汇率 1 USD = 7…」）。
        // 这一行允许折行：它是说明，折成两行也比截断成"汇率 1 USD = 7…"更诚实。
        Text(
            text = usageRateText(usdCny, pricingKnown),
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
    }
}

/**
 * KPI 四格：请求数 / 成功率 / Token / 费用（桌面 `usage.js:96-101`）。
 *
 * 成本口径跟随计价币种：`formatUsageAmount` 与桌面 `fmtCost` 同一套（CNY 先乘汇率）。
 * 列数随字号：1.0× 两列、1.3× 单列（`100.0%` 这类值在两列里会被省略号吃掉）。
 */
@Composable
private fun UsageKpiCard(summary: UsageSummary, period: UsagePeriod, currency: String, rate: Double) {
    val cells = listOf(
        // 请求数不再写「今日」注解：卡片标题已经交代了周期，同一句写两遍只是噪音。
        KpiMetricSpec("请求数", formatUsageCount(summary.calls), "", AetherIdentityTone.BLUE.foreground()),
        KpiMetricSpec("成功率", usageSuccessRateText(summary), "失败 ${formatUsageCount(summary.errors)} 次", AetherIdentityTone.GREEN.foreground()),
        KpiMetricSpec("Token", formatUsageCount(summary.totalTokens), usageTokensNoteText(summary), AetherIdentityTone.PURPLE.foreground()),
        KpiMetricSpec("费用", formatUsageAmount(summary.cost, currency, rate), usageCostNoteText(summary), AetherIdentityTone.AMBER.foreground()),
    )
    val columns = if (isLargeFontScale()) 1 else 2
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle(usagePeriodScopeLabel(period))
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = "本机账本",
                tone = DeepTone.Neutral,
            )
        }
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            cells.chunked(columns).forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    row.forEach { spec ->
                        UsageStatCell(
                            label = spec.label,
                            value = spec.value,
                            note = spec.note,
                            accentColor = spec.accentColor,
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            }
        }
    }
}

private data class KpiMetricSpec(
    val label: String,
    val value: String,
    val note: String,
    val accentColor: Color,
)

/**
 * 一格数字：说明 → 大数字（单位降级）→ 注解。
 *
 * 值和单位是**同一段 AnnotatedString**（早先三个并排 `Text` + `Alignment.Bottom`，
 * 小号 `$` 在大号数字旁会沉到基线以下）。同一段文字里降级单位，基线与行高由排版引擎保证。
 */
@Composable
private fun UsageStatCell(
    label: String,
    value: String,
    note: String,
    accentColor: Color,
    modifier: Modifier = Modifier,
) {
    val parts = statFocusParts(value)
    val bigSize = MaterialTheme.typography.headlineSmall.fontSize
    val smallSize = MaterialTheme.typography.labelSmall.fontSize
    val onSurface = MaterialTheme.colorScheme.onSurface
    val onSurfaceVariant = MaterialTheme.colorScheme.onSurfaceVariant
    AetherSubSurface(
        modifier = modifier,
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
                        .background(accentColor),
                )
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelMedium,
                    color = onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                text = buildAnnotatedString {
                    if (parts.prefix.isNotEmpty()) {
                        withStyle(SpanStyle(fontSize = smallSize, color = onSurfaceVariant)) {
                            append(parts.prefix)
                        }
                    }
                    withStyle(
                        SpanStyle(
                            fontSize = bigSize,
                            fontWeight = FontWeight.Bold,
                            color = onSurface,
                            fontFeatureSettings = "tnum",
                        ),
                    ) {
                        append(parts.number)
                    }
                    if (parts.suffix.isNotEmpty()) {
                        withStyle(SpanStyle(fontSize = smallSize, color = onSurfaceVariant)) {
                            append(parts.suffix)
                        }
                    }
                },
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (note.isNotEmpty()) {
                Text(
                    text = note,
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                    maxLines = 2,
                )
            }
        }
    }
}

/**
 * 趋势（桌面 `usage.js:104-211`）：柱状调用量 + 总计行。
 *
 * 桌面还有「按模型 / 面积图 / 成本折线」三档；逐模型逐日序列来自
 * `GET /api/usage/requests` 的 `chartBy.model`（端点存在，本轮未接，见汇报）。
 * 这里画的是**总计口径的柱状图**，即桌面默认形态（`mode='bars'`, `chartBy='total'`）。
 */
@Composable
private fun UsageTrendCard(points: List<UsageDayPoint>, period: UsagePeriod, currency: String, rate: Double) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("趋势")
            Spacer(Modifier.weight(1f))
            val totalCalls = points.sumOf { it.calls }
            val totalCost = points.sumOf { it.cost }
            Text(
                text = usageTrendTotalText(totalCalls, totalCost, currency, rate),
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (points.isEmpty() || points.all { it.calls == 0L }) {
            Text(
                text = usageSeriesEmptyHint(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        val maxCalls = points.maxOf { it.calls }.coerceAtLeast(1L)
        val step = usageTrendLabelStep(points.size)
        val primaryColor = MaterialTheme.colorScheme.primary
        val dangerColor = AetherColors.danger()
        AetherSubSurface(padding = PaddingValues(12.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(
                    modifier = Modifier.fillMaxWidth().height(TrendPlotHeight),
                    verticalAlignment = Alignment.Bottom,
                    horizontalArrangement = Arrangement.spacedBy(3.dp),
                ) {
                    points.forEach { point ->
                        Column(
                            modifier = Modifier.weight(1f).fillMaxHeight(),
                            verticalArrangement = Arrangement.Bottom,
                            horizontalAlignment = Alignment.CenterHorizontally,
                        ) {
                            val barHeight: Dp = if (point.calls <= 0L) {
                                0.dp
                            } else {
                                (TrendPlotHeight * (point.calls.toFloat() / maxCalls.toFloat()))
                                    .coerceAtLeast(TrendBarMinHeight)
                            }
                            val baseColor = if (point.errors > 0L) dangerColor else primaryColor
                            Box(
                                Modifier
                                    .fillMaxWidth()
                                    .height(barHeight)
                                    .clip(RoundedCornerShape(topStart = 4.dp, topEnd = 4.dp, bottomStart = 2.dp, bottomEnd = 2.dp))
                                    .background(
                                        Brush.verticalGradient(
                                            colors = listOf(
                                                baseColor.copy(alpha = 0.92f),
                                                baseColor.copy(alpha = 0.55f),
                                            ),
                                        ),
                                    ),
                            )
                        }
                    }
                }
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
                    points.forEachIndexed { index, point ->
                        Text(
                            text = if (index % step == 0) trendAxisLabel(point.label) else "",
                            modifier = Modifier.weight(1f),
                            style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                            color = AetherColors.textTertiary(),
                            textAlign = TextAlign.Center,
                            maxLines = 1,
                            overflow = TextOverflow.Clip,
                        )
                    }
                }
            }
        }
        Text(
            text = usageTrendLegendText(period),
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
    }
}

/**
 * 用量分布（桌面 `usage.js:213-237`）：三个分类 tab + 合计行 + 按成本降序前 12 行。
 *
 * 列顺序与桌面 `rankRow` 一致：名称（左）→ 次数 · Token → 缓存率 → 费用（右）；下面是占比条。
 */
@Composable
private fun UsageBreakdownCard(
    summary: UsageSummary,
    breakdown: UsageBreakdown,
    onBreakdown: (UsageBreakdown) -> Unit,
    currency: String,
    rate: Double,
    onOpenOverview: () -> Unit,
) {
    // 排序是纯本地偏好（服务端 /api/usage/arrange 只是回显，见 usageBreakdownComparator 注释），
    // 所以状态就放在卡片里：离开页面即恢复默认「按费用」，不需要持久化。
    var sortKey by remember { mutableStateOf(UsageSortKey.COST) }
    val rows = usageBreakdownRows(summary, breakdown, sortKey)
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("用量分布")
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = usageBreakdownChipText(rows.size),
                tone = DeepTone.Neutral,
            )
        }
        AetherSegmentedPill(
            options = UsageBreakdown.entries,
            selected = breakdown,
            onSelect = onBreakdown,
            label = { it.label },
        )
        // 排序维度：默认「按费用」（桌面固定顺序），另外两档按调用/按 Token 看同一批数据。
        // 单行横滚，1.3× 下也不会挤成两行或竖排。
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(TightGap),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "排序",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            AetherSegmentedPill(
                options = UsageSortKey.entries,
                selected = sortKey,
                onSelect = { sortKey = it },
                label = { it.label },
            )
        }
        if (rows.isEmpty()) {
            Text(
                text = usageBreakdownEmptyHeadline(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = usageBreakdownEmptyDetail(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            McaButton(onClick = onOpenOverview, style = McaButtonStyle.Outlined) { Text("前往概览") }
            return@DeepCard
        }
        val maxCost = rows.maxOf { it.cost }.coerceAtLeast(0.000001)
        // 合计行（桌面 usage.js:227）：占比恒 100%。
        AetherSubSurface(padding = PaddingValues(12.dp)) {
            UsageRankRow(
                name = "合计",
                calls = summary.calls,
                errors = summary.errors,
                tokens = summary.totalTokens,
                cacheRate = usageCacheHitRateText(summary),
                amountText = formatUsageAmount(summary.cost, currency, rate),
                sharePct = 100.0,
                emphasized = true,
            )
        }
        rows.take(12).forEach { row ->
            UsageRankRow(
                name = row.name,
                calls = row.calls,
                errors = row.errors,
                tokens = row.totalTokens,
                cacheRate = usageBreakdownCacheRateText(row),
                amountText = formatUsageAmount(row.cost, currency, rate),
                sharePct = (row.cost / maxCost) * 100.0,
                emphasized = false,
            )
        }
        if (rows.size > 12) {
            Text(
                text = "…另有 ${rows.size - 12} 类，可在「请求」页按同一维度筛选查看。",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
        }
    }
}

@Composable
private fun UsageRankRow(
    name: String,
    calls: Long,
    errors: Long,
    tokens: Long,
    cacheRate: String,
    amountText: String,
    sharePct: Double,
    emphasized: Boolean,
) {
    val targetRatio = (sharePct / 100.0).coerceIn(0.0, 1.0).toFloat()
    val animatedRatio by animateFloatAsState(
        targetValue = targetRatio,
        animationSpec = spring(
            dampingRatio = Spring.DampingRatioNoBouncy,
            stiffness = Spring.StiffnessMediumLow,
        ),
        label = "usageRankShare",
    )
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = name,
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                fontWeight = if (emphasized) FontWeight.Bold else FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (errors > 0L) {
                Text(
                    text = "失败 ${formatUsageCount(errors)}",
                    style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                    color = AetherColors.danger(),
                )
                Spacer(Modifier.width(TightGap))
            }
            Text(
                text = amountText,
                style = MaterialTheme.typography.bodySmall.copy(fontFeatureSettings = "tnum"),
                fontWeight = if (emphasized) FontWeight.Bold else FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.End,
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "${formatUsageCount(calls)} 次 · ${formatTokens(tokens)} Token",
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            Text(
                text = "缓存 $cacheRate",
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = AetherColors.textTertiary(),
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .weight(1f)
                    .height(6.dp)
                    .clip(McaShape.Badge)
                    .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.45f)),
            ) {
                if (animatedRatio > 0f) {
                    Box(
                        Modifier
                            .fillMaxHeight()
                            .fillMaxWidth(animatedRatio)
                            .clip(McaShape.Badge)
                            .background(MaterialTheme.colorScheme.primary.copy(alpha = 0.88f)),
                    )
                }
            }
            Spacer(Modifier.width(TightGap))
            Text(
                text = sharePctText(sharePct),
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun UsageLoadingCard(text: String) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(16.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(DeepCardGap))
            DeepCardBody(text)
        }
    }
}

/* ------------------------------- 纯函数 ------------------------------- */

/** 区块标题下的周期口径：与 [UsagePeriod] 一一对应，不写"当前"。 */
internal fun usagePeriodScopeLabel(period: UsagePeriod): String = when (period) {
    UsagePeriod.TODAY -> "今日"
    UsagePeriod.WEEK -> "近 7 天"
    UsagePeriod.MONTH -> "近 30 天"
}

/** 成功率 = (请求 - 失败) / 请求；没有请求时写"—"，不写 0%。 */
internal fun usageSuccessRateText(summary: UsageSummary): String =
    percentText(usageSuccessRatePercent(summary.calls, summary.errors))

/** 请求格的注解行：失败次数 + 成功率。 */
internal fun usageRequestsNoteText(summary: UsageSummary): String =
    "失败 ${formatUsageCount(summary.errors)} · 成功率 ${usageSuccessRateText(summary)}"

/** 缓存命中率 = 缓存读 / (输入 + 缓存读 + 缓存写)；分母为 0 时写"—"。 */
internal fun usageCacheHitRateText(summary: UsageSummary): String = percentText(
    usageCacheHitRatePercent(
        input = summary.inputTokens,
        cacheRead = summary.cacheReadTokens,
        cacheWrite = summary.cacheWriteTokens,
    ),
)

/** Token 格的注解行：缓存 token 与命中率（桌面 `usage.js:99` 同一句）。 */
internal fun usageTokensNoteText(summary: UsageSummary): String =
    "缓存 ${formatUsageCount(summary.cacheReadTokens)} · 命中 ${usageCacheHitRateText(summary)}"

/** 费用格的注解行：未计价请求数 / 按当前价格表（桌面 `usage.js:100`）。 */
internal fun usageCostNoteText(summary: UsageSummary): String =
    if (summary.unpriced > 0L) {
        "未计价 ${formatUsageCount(summary.unpriced)} 次"
    } else {
        "按当前价格表"
    }

/** 趋势卡的总计行：桌面 `usage.js:157-158` 的「总计: N 次 · 金额」。 */
internal fun usageTrendTotalText(calls: Long, costUsd: Double, currency: String, rate: Double): String =
    "总计: ${formatUsageCount(calls)} 次 · ${formatUsageAmount(costUsd, currency, rate)}"

/** 趋势卡底部的读图说明。 */
internal fun usageTrendLegendText(period: UsagePeriod): String =
    "${usagePeriodScopeLabel(period)}每天调用量（柱高）· 红色柱=当天有失败请求"

/** 横轴标签抽稀步长：最多显示 6 个标签，避免 30 天挤成一团。 */
internal fun usageTrendLabelStep(count: Int): Int {
    if (count <= 6) {
        return 1
    }
    return (count + 5) / 6
}

/**
 * 横轴刻度：服务端给 `Jan 2` 这样的本地化标签，只取日期部分；
 * 认不出来就原样显示（不编日期）。
 */
internal fun trendAxisLabel(label: String): String {
    val text = label.trim()
    if (text.isEmpty()) {
        return ""
    }
    val tail = text.substringAfterLast(' ').trim()
    return tail.ifEmpty { text }
}

/** 汇率说明：价格表没读到时不编汇率。 */
internal fun usageRateText(usdCny: Double, pricingKnown: Boolean): String = when {
    !pricingKnown -> "汇率未读取"
    usdCny > 0.0 -> String.format(Locale.US, "汇率 1 USD = %.2f CNY", usdCny)
    else -> "汇率未提供"
}

/** 用量日历的区间标签（桌面 `usage.js:253` 的 chip：`from → to`）。 */
internal fun usageCalendarRangeLabel(from: String, to: String): String =
    if (from.isNotBlank() && to.isNotBlank()) "$from → $to" else "近一年"

/** 分布卡的计数 chip：桌面 `usage.js:216` 的「N 类」。 */
internal fun usageBreakdownChipText(count: Int): String = "$count 类"

/** 占比条文字：整数不带小数，方便扫读（桌面 `usage.js:247` 用 `toFixed(0)`）。 */
internal fun sharePctText(pct: Double): String {
    val safe = if (pct.isFinite()) pct.coerceIn(0.0, 100.0) else 0.0
    return "${kotlin.math.round(safe).toInt()}%"
}

/** 单行缓存命中率文字（分布卡的行内注解）。 */
internal fun usageBreakdownCacheRateText(row: UsageBreakdownRow): String = percentText(
    usageCacheHitRatePercent(
        input = row.inputTokens,
        cacheRead = row.cacheReadTokens,
        cacheWrite = row.cacheWriteTokens,
    ),
)

/**
 * 分类行：渠道 → `summary.providers`、模型 → `summary.models`、调用方 → `summary.agents`。
 * 排序与桌面一致（`usage.js:215`）：成本降序，其次调用数降序。
 */
internal fun usageBreakdownRows(
    summary: UsageSummary,
    breakdown: UsageBreakdown,
    sort: UsageSortKey = UsageSortKey.COST,
): List<UsageBreakdownRow> {
    val rows = when (breakdown) {
        UsageBreakdown.PROVIDER -> summary.providers.map { provider ->
            UsageBreakdownRow(
                id = provider.id,
                name = provider.name,
                calls = provider.calls,
                errors = provider.errors,
                inputTokens = provider.inputTokens,
                outputTokens = provider.outputTokens,
                cacheReadTokens = provider.cacheReadTokens,
                cacheWriteTokens = provider.cacheWriteTokens,
                cost = provider.cost,
            )
        }
        UsageBreakdown.MODEL -> summary.models.map { model ->
            UsageBreakdownRow(
                id = model.id,
                name = model.name,
                calls = model.calls,
                errors = model.errors,
                inputTokens = model.inputTokens,
                outputTokens = model.outputTokens,
                cacheReadTokens = model.cacheReadTokens,
                cacheWriteTokens = model.cacheWriteTokens,
                cost = model.cost,
            )
        }
        UsageBreakdown.AGENT -> summary.agents
    }
    return rows.sortedWith(usageBreakdownComparator(sort))
}

/**
 * 用量分布的排序维度（桌面默认固定按费用降序，`usage.js:215`：
 * `sort((a,b) => (b.cost||0)-(a.cost||0) || (b.calls||0)-(a.calls||0))`）。
 *
 * 手机端把"按费用"作为默认值原样保留，另外给两个对照维度：看谁在烧成本、谁在占调用量、
 * 谁在吃 Token。**不是**桌面 `POST /api/usage/arrange`（那个端点只回 `{ok:true, order}`，
 * 服务端不持久化任何东西——见 `usage_quota_api.go:584-592`，排序是纯前端偏好）。
 */
internal enum class UsageSortKey(val label: String) {
    COST("按费用"),
    CALLS("按调用"),
    TOKENS("按 Token"),
}

/** 三种排序的比较器；同值时的次级键固定，保证顺序稳定（不是随机跳）。 */
internal fun usageBreakdownComparator(key: UsageSortKey): Comparator<UsageBreakdownRow> = when (key) {
    UsageSortKey.COST -> compareByDescending<UsageBreakdownRow> { it.cost }.thenByDescending { it.calls }
    UsageSortKey.CALLS -> compareByDescending<UsageBreakdownRow> { it.calls }.thenByDescending { it.cost }
    UsageSortKey.TOKENS -> compareByDescending<UsageBreakdownRow> { it.totalTokens }.thenByDescending { it.cost }
}

/** 账本为空时的标题：说清"为什么空"，不写"暂无数据"。 */
internal fun usageEmptyHeadline(): String = "还没有用量"

/** 账本为空时的说明：为什么空 + 下一步做什么。 */
internal fun usageEmptyDetail(): String =
    "这台手机的网关还没处理过请求，所以账本是空的。" +
        "下一步：用任意 OpenAI 兼容客户端向本机网关发一次请求，这里就会出现按模型汇总的条目。"

/** 分布卡为空：为什么空 + 去哪里看（桌面 `usage.js:220` 同结构）。 */
internal fun usageBreakdownEmptyHeadline(): String = "还没有用量数据"

internal fun usageBreakdownEmptyDetail(): String =
    "发出请求后，这里会按渠道、模型和调用方分类。" +
        "下一步：先在概览页确认网关在运行，再用客户端发一次请求。"

/** 「按模型」为空（保留旧函数名供既有测试引用）。 */
internal fun usageModelsEmptyHint(): String =
    "这个周期里没有按模型的记录：账本只统计真正走完的请求。" +
        "下一步：先在概览页确认网关在运行，再发出一次请求。"

/** 「趋势」为空：只有调用量 > 0 的日子才画，所以刚启动时是空的。 */
internal fun usageSeriesEmptyHint(): String =
    "这个周期里还没有按天的调用量，所以趋势是空的。" +
        "下一步：发出请求后回来刷新，或把周期切成「今日 / 7 天 / 30 天」对比。"

/** 用量细分读取失败：错误码与原因都要落地，不许留空标签。 */
internal fun usageDetailErrorLine(code: String, message: String): String =
    "原因：${message.ifBlank { "未返回原因" }}（错误码：$code）"

/**
 * 订阅 / 配额历史 / 模型重定向 / 最近异常四块共用的错误口径：
 * 网关没起来时说清是"没启动"，其它错误带服务端原文与错误码。
 */
internal fun sectionErrorLine(code: String, message: String, subject: String): String =
    if (code == "gateway_not_running") {
        "网关未启动：启动后才能读取$subject。"
    } else {
        "读取$subject 失败：${message.ifBlank { "未返回原因" }}（错误码：$code）"
    }

/* ---------------- 额度历史 + 周期估算 ---------------- */

/** 弯线的顶点（像素，原点左上）。 */
internal data class QuotaSparkDot(val x: Float, val y: Float)

/** 额度曲线的高度：固定，不随字号变（1.0×/1.3× 同高，只把标签排稀）。 */
private val QuotaSparkHeight = 44.dp

/**
 * 额度曲线坐标：横轴按时间（首末点铺满宽度），纵轴**固定 0..100%**（`left` 是剩余百分比）。
 *
 * 固定纵轴是刻意的：额度曲线的意义就是"还剩多少"，按数据自适应缩放会把"还剩 90%"和
 * "还剩 5%"画成同一条波浪线，看着都在"正常波动"。点少于 1 个或画布非法时返回空。
 */
internal fun quotaSparklineDots(
    points: List<QuotaHistoryPoint>,
    width: Float,
    height: Float,
): List<QuotaSparkDot> {
    val normalized = normalizeQuotaPoints(points)
    if (normalized.isEmpty() || width <= 0f || height <= 0f) {
        return emptyList()
    }
    val firstMs = normalized.first().atMs
    val span = (normalized.last().atMs - firstMs).toFloat()
    return normalized.map { point ->
        val x = if (span <= 0f) width / 2f else ((point.atMs - firstMs) / span) * width
        val ratio = (point.left / 100.0).coerceIn(0.0, 1.0)
        QuotaSparkDot(x = x, y = (height - ratio * height).toFloat())
    }
}

/** 曲线小结：`剩余 42% · 45 天消耗 38 个百分点`（只有一个点时只说剩余）。 */
internal fun quotaLineSummaryText(line: QuotaHistoryLine): String {
    val normalized = normalizeQuotaPoints(line.points)
    if (normalized.isEmpty()) {
        return "没有读数"
    }
    val last = normalized.last().left
    val consumed = quotaConsumedPercent(normalized)
    val remaining = "剩余 ${sharePctText(last)}"
    return if (normalized.size < 2 || consumed <= 0.0) {
        remaining
    } else {
        "$remaining · 期间消耗 ${sharePctText(consumed)}"
    }
}

@Composable
private fun UsageQuotaHistoryCard(
    history: QuotaHistoryUiState,
    holds: Map<String, QuotaHoldWindow>,
    onRetry: () -> Unit,
) {
    when (history) {
        QuotaHistoryUiState.Idle -> Unit
        QuotaHistoryUiState.Loading -> UsageLoadingCard("正在读取额度历史…")
        is QuotaHistoryUiState.Failed -> DeepStateCard(
            title = "额度历史读不到",
            detail = history.message,
            tone = DeepTone.Danger,
            actionLabel = "重试",
            onAction = onRetry,
        )
        is QuotaHistoryUiState.Ready -> {
            val accounts = history.accounts
            DeepCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    DeepCardTitle("额度历史")
                    Spacer(Modifier.weight(1f))
                    AetherStatusChip(
                        text = if (accounts.isEmpty()) "无读数" else "${accounts.size} 个账号 · 45 天",
                        tone = DeepTone.Neutral,
                    )
                }
                if (accounts.isEmpty()) {
                    Text(
                        text = quotaHistoryEmptyHint(),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    return@DeepCard
                }
                accounts.take(4).forEach { account ->
                    QuotaAccountBlock(account = account, holds = holds)
                }
                if (accounts.size > 4) {
                    Text(
                        text = "还有 ${accounts.size - 4} 个账号没有展开（同屏最多 4 个，避免刷不到底）",
                        style = MaterialTheme.typography.labelSmall,
                        color = AetherColors.textTertiary(),
                    )
                }
            }
        }
    }
}

@Composable
private fun QuotaAccountBlock(account: QuotaHistoryAccount, holds: Map<String, QuotaHoldWindow>) {
    AetherSubSurface(padding = PaddingValues(12.dp)) {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                ProviderBrandPlate(
                    providerId = account.provider,
                    providerName = account.provider,
                    size = 24.dp,
                )
                Text(
                    text = quotaAccountLabel(account.provider, account.user),
                    style = MaterialTheme.typography.bodySmall,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            account.lines.forEach { line ->
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = line.name,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.width(68.dp),
                        )
                        Spacer(Modifier.width(8.dp))
                        val dots = quotaSparklineDots(line.points, width = 1_000f, height = 100f)
                        val lineColor = MaterialTheme.colorScheme.primary
                        Canvas(
                            modifier = Modifier
                                .weight(1f)
                                .height(QuotaSparkHeight),
                        ) {
                            if (dots.size < 2) {
                                return@Canvas
                            }
                            val scaleX = size.width / 1_000f
                            val scaleY = size.height / 100f
                            val path = Path()
                            val fillPath = Path()
                            val first = dots.first()
                            val firstX = first.x * scaleX
                            val firstY = first.y * scaleY
                            path.moveTo(firstX, firstY)
                            fillPath.moveTo(firstX, size.height)
                            fillPath.lineTo(firstX, firstY)
                            dots.drop(1).forEach { dot ->
                                val dx = dot.x * scaleX
                                val dy = dot.y * scaleY
                                path.lineTo(dx, dy)
                                fillPath.lineTo(dx, dy)
                            }
                            val last = dots.last()
                            val lastX = last.x * scaleX
                            val lastY = last.y * scaleY
                            fillPath.lineTo(lastX, size.height)
                            fillPath.close()

                            drawPath(
                                path = fillPath,
                                brush = Brush.verticalGradient(
                                    colors = listOf(
                                        lineColor.copy(alpha = 0.24f),
                                        lineColor.copy(alpha = 0.02f),
                                    ),
                                ),
                            )
                            drawPath(
                                path = path,
                                color = lineColor,
                                style = Stroke(
                                    width = 2.dp.toPx(),
                                    cap = StrokeCap.Round,
                                    join = StrokeJoin.Round,
                                ),
                            )
                            drawCircle(
                                color = lineColor.copy(alpha = 0.28f),
                                radius = 5.5.dp.toPx(),
                                center = Offset(lastX, lastY),
                            )
                            drawCircle(
                                color = lineColor,
                                radius = 2.75.dp.toPx(),
                                center = Offset(lastX, lastY),
                            )
                        }
                        Spacer(Modifier.width(8.dp))
                        Text(
                            text = quotaLineSummaryText(line),
                            style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.End,
                            maxLines = 1,
                        )
                    }
                    val hold = holds[quotaHoldKey(account.provider, account.user, line.name)]
                    Text(
                        text = quotaHoldText(hold),
                        style = MaterialTheme.typography.labelSmall,
                        color = AetherColors.textTertiary(),
                    )
                }
            }
        }
    }
}

/**
 * 一个窗口的"周期估算"文案。
 *
 * `holds` 为空 = 还没拿到（或推不出周期）；有 `estimate` 就给估算值；只有 `why` 就照服务端
 * 的原因代码说清"为什么估不出来"——**不给看着像真的数字**。
 */
internal fun quotaHoldText(window: QuotaHoldWindow?): String {
    if (window == null) {
        return "周期估算：缺少窗口起止时间，无法反推整窗大小"
    }
    val estimate = window.estimate
    if (estimate != null) {
        val cost = if (estimate.priced) {
            " · 约 $${"%.2f".format(java.util.Locale.US, estimate.cost)}"
        } else {
            " · 成本未定价"
        }
        val used = sharePctText(estimate.used)
        return "周期估算：整窗约 ${formatTokens(estimate.tokens)} Token$cost（已用 $used，" +
            "本网关经手 ${formatTokens(estimate.routedTokens)} Token / ${formatUsageCount(estimate.routedCalls)} 次调用）"
    }
    return "周期估算：${quotaHoldsWhyLabel(window.why)}"
}

/* ---------------- 渠道每日签到 ---------------- */

/**
 * 卡片说明：一次点下去会打到该厂商下**所有**渠道的全部可用账号
 * （`vendorProviders`，`checkin_api.go:11-16`），所以按钮之后要二次确认。
 */
internal fun vendorCheckinScopeHint(vendor: CheckinVendor): String =
    "会打到 ${vendor.providers.joinToString("、")} 下的全部可用账号；" +
        "今天已经签到的账号由服务端直接返回旧结果，不会重复请求上游。"

@Composable
private fun UsageVendorCheckinCard(
    state: VendorCheckinUiState,
    onCheckin: (CheckinVendor) -> Unit,
    onDismiss: () -> Unit,
) {
    // 二次确认用行内两段式（不弹对话框）：1.3× 下弹窗要重新排版，行内确认没有这个问题。
    var pending by remember { mutableStateOf<CheckinVendor?>(null) }
    val running = state as? VendorCheckinUiState.Running
    val variant = MaterialTheme.colorScheme.onSurfaceVariant
    DeepCard {
        DeepCardTitle("渠道签到")
        Text(
            text = "按厂商一次签到这个渠道下的所有账号；今日已签的会跳过。",
            style = MaterialTheme.typography.labelSmall,
            color = variant,
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(TightGap),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CheckinVendor.entries.forEach { vendor ->
                AetherPillButton(
                    text = if (running?.vendor == vendor) "签到中…" else vendor.label,
                    onClick = { pending = vendor },
                    primary = running?.vendor == vendor || pending == vendor,
                    enabled = running == null,
                )
            }
        }
        pending?.let { vendor ->
            AetherSubSurface(padding = PaddingValues(12.dp)) {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        text = "确认签到「${vendor.label}」？${vendorCheckinScopeHint(vendor)}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Row(horizontalArrangement = Arrangement.spacedBy(TightGap)) {
                        AetherPillButton(
                            text = "确认签到",
                            onClick = {
                                pending = null
                                onCheckin(vendor)
                            },
                            primary = true,
                        )
                        AetherPillButton(
                            text = "取消",
                            onClick = { pending = null },
                        )
                    }
                }
            }
        }
        when (state) {
            VendorCheckinUiState.Idle -> Unit
            is VendorCheckinUiState.Running -> Text(
                text = "正在签到…（服务端会逐个账号请求上游，可能需要几十秒）",
                style = MaterialTheme.typography.bodySmall,
                color = variant,
            )
            is VendorCheckinUiState.Failed -> Text(
                text = state.message,
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.danger(),
            )
            is VendorCheckinUiState.Ready -> {
                AetherSubSurface(padding = PaddingValues(12.dp)) {
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Text(
                            text = vendorCheckinSummary(state.results),
                            style = MaterialTheme.typography.bodySmall,
                            fontWeight = FontWeight.SemiBold,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        state.results.take(8).forEach { result ->
                            Text(
                                text = vendorCheckinLine(result),
                                style = MaterialTheme.typography.labelSmall,
                                color = variant,
                            )
                        }
                        if (state.results.size > 8) {
                            Text(
                                text = "…另有 ${state.results.size - 8} 个账号",
                                style = MaterialTheme.typography.labelSmall,
                                color = AetherColors.textTertiary(),
                            )
                        }
                        AetherPillButton(
                            text = "清除结果",
                            onClick = onDismiss,
                        )
                    }
                }
            }
        }
    }
}

/* ============================================================================
 * 卡片排序（纯客户端偏好）
 *
 * `POST /api/usage/arrange`（`gateway.go:1009` → `usage_quota_api.go:584-592`）**是空实现**：
 * 只把 `order` 原样回显，注释明说"排序属于前端本地偏好（localStorage），服务端不持久化"。
 * 所以这里**不调那个端点**——发一次只会拿到 `{ok:true}` 假装保存成功，下次启动顺序照样丢，
 * 比不保存更误导。顺序只用 `rememberSaveable` 存在保存态里（应用内重启/配置变更保留）。
 * ========================================================================== */

/**
 * 用量页可排序的卡片。枚举顺序 = 默认渲染顺序（与改造前逐块一致）。
 *
 * 页头（`MobileHeader` / `UsageTitle`）不在其中：标题不该被排到 KPI 下面。
 */
internal enum class UsageCardKey(val label: String) {
    SCOPE("时间范围"),
    KPI("KPI 四格"),
    TREND("趋势"),
    BREAKDOWN("用量分布"),
    CHECKIN("渠道签到"),
    QUOTA_HISTORY("额度历史"),
    CALENDAR("用量日历"),
}

/** 默认顺序：枚举声明顺序。 */
internal val DefaultUsageCardOrder: List<String> = UsageCardKey.entries.map { it.name }

/** `rememberSaveable` 的键名：改名等于静默清空老用户的顺序，所以抽常量并配单测钉住。 */
internal const val USAGE_CARD_ORDER_SAVED_KEY: String = "usage_card_order"

/**
 * 上移/下移一格。直接复用 [moveProvider]（`runtime/AccountQuotaApiClient.kt:826`：
 * 越界钳制、未知 id 原样返回），不为一次排序再写第二套移动逻辑。
 */
internal fun moveUsageCard(order: List<String>, key: String, direction: MoveDirection): List<String> =
    moveProvider(order, key, direction)

/**
 * 该方向现在能不能动：首行不能上移、末行不能下移。按钮**置灰不隐藏**——
 * 隐藏会让列表看起来长短不一，置灰才说明"到顶了"。
 */
internal fun canMoveUsageCard(order: List<String>, key: String, direction: MoveDirection): Boolean {
    val index = order.indexOf(key)
    if (index < 0) {
        return false
    }
    return index + direction.indexDelta in order.indices
}

/**
 * 规范化保存下来的顺序：丢未知 key、去重、补上缺失的新卡片（追加到末尾）。
 *
 * 补是必须的：以后再加卡片时老用户存下的列表里没有它，不补就永远不显示。
 * 空列表/全非法 → 回落默认顺序。
 */
internal fun normalizeUsageCardOrder(saved: List<String>?): List<String> {
    if (saved.isNullOrEmpty()) {
        return DefaultUsageCardOrder
    }
    val known = UsageCardKey.entries.map { it.name }.toSet()
    val out = LinkedHashSet<String>()
    saved.forEach { raw ->
        val key = raw.trim()
        if (key in known) {
            out.add(key)
        }
    }
    if (out.isEmpty()) {
        return DefaultUsageCardOrder
    }
    DefaultUsageCardOrder.forEach { key ->
        if (key !in out) {
            out.add(key)
        }
    }
    return out.toList()
}

/** 卡片名（弹窗列表用）；未知 key 原样回显，不静默显示空行。 */
internal fun usageCardLabel(key: String): String =
    UsageCardKey.entries.firstOrNull { it.name == key }?.label ?: key

