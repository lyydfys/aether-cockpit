package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.UsageDayPoint
import com.muyuchat.aether.runtime.UsageModelRow
import com.muyuchat.aether.runtime.UsageSummary
import com.muyuchat.aether.runtime.formatUsageCost
import com.muyuchat.aether.runtime.formatUsageCount
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import java.util.Locale

// 间距节奏与概览页同一套：区块之间 24 / 卡片内与行之间 12 / 紧邻元素 8。
private val BlockGap = 24.dp
private val CardPadding = 16.dp
private val RowGap = 12.dp
private val TightGap = 8.dp

/**
 * 用量页：总量 → 按模型 → 近期趋势 → 细分（热力图 / 订阅 / 配额历史 / 价格表）。
 *
 * 每个区块都是一张自带 `titleMedium+Bold` 标题的卡；卡内 16dp、行间 12dp。
 * 数字口径与概览页严格一致（Token = 输入 + 输出，不含缓存命中），否则同一台手机上两页会打架。
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
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(BlockGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(RowGap)) {
                MobileHeader(status)
                UsageTitle(state, onRefresh)
                PeriodRow(period, onPeriod)
            }
        }
        when (state) {
            UsageUiState.Idle -> item {
                UsageStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后才能读取本机账本；账本只记录这台手机自己处理过的请求。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            UsageUiState.Loading -> item { UsageLoadingCard("正在读取本机用量…") }
            is UsageUiState.Failed -> item {
                UsageErrorCard(code = state.code, message = state.message, onRetry = onRefresh)
            }
            is UsageUiState.Ready -> {
                val summary = state.summary
                if (summary.calls == 0L) {
                    item {
                        UsageStateCard(
                            title = usageEmptyHeadline(),
                            detail = usageEmptyDetail(),
                            actionLabel = "前往概览",
                            onAction = onOpenOverview,
                        )
                    }
                } else {
                    item { UsageTotalsCard(summary, period) }
                    item { UsageModelsCard(summary.models) }
                    item { UsageSeriesCard(summary.series.takeLast(7).filter { it.calls > 0 }) }
                }
            }
        }
        // 细分四块各自独立：任何一块读不到都只在自己那张卡里报错。
        when (detail) {
            UsageDetailUiState.Idle -> Unit
            UsageDetailUiState.Loading -> item { UsageLoadingCard("正在读取用量细分…") }
            is UsageDetailUiState.Failed -> item {
                UsageStateCard(
                    title = "用量细分读不到",
                    detail = usageDetailErrorLine(detail.code, detail.message),
                    actionLabel = "重试",
                    onAction = onRefreshDetail,
                )
            }
            is UsageDetailUiState.Ready -> {
                val today = java.time.LocalDate.now().toString()
                item {
                    UsageHeatmapSection(
                        days = detail.heatmap.recentDays(30, today),
                        error = detail.errors["heatmap"],
                    )
                }
                item { QuotaPlanSection(running = status.running) }
                item {
                    UsageQuotaSection(accounts = detail.quotaHistory, error = detail.errors["quota"])
                }
                item {
                    UsagePricingSection(pricing = detail.pricing, error = detail.errors["pricing"])
                }
            }
        }
        item {
            Text(
                text = "只读：数据来自本机 /api/usage，不展示请求正文，也不上传到云端。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun UsageTitle(state: UsageUiState, onRefresh: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(TightGap),
        ) {
            Text(
                text = "用量",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = "本机账本 · 只读",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        McaIconButton(
            contentDescription = "刷新用量",
            onClick = onRefresh,
            enabled = state is UsageUiState.Ready || state is UsageUiState.Failed,
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

@Composable
private fun PeriodRow(period: UsagePeriod, onPeriod: (UsagePeriod) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(TightGap)) {
        UsagePeriod.entries.forEach { value ->
            McaButton(
                onClick = { onPeriod(value) },
                style = if (value == period) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) {
                Text(value.label)
            }
        }
    }
}

/** 唯一的一层卡片：Sheet + hairline + 16dp 内边距。 */
@Composable
private fun UsageCard(content: @Composable () -> Unit) {
    Surface(
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(CardPadding),
            verticalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            content()
        }
    }
}

@Composable
private fun UsageCardTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.onSurface,
    )
}

@Composable
private fun UsageTotalsCard(summary: UsageSummary, period: UsagePeriod) {
    UsageCard {
        UsageCardTitle(usagePeriodScopeLabel(period))
        Row(horizontalArrangement = Arrangement.spacedBy(CardPadding)) {
            UsageStatCell(
                label = "请求",
                value = formatUsageCount(summary.calls),
                note = usageRequestsNoteText(summary),
                modifier = Modifier.weight(1f),
            )
            UsageStatCell(
                label = "Token",
                value = formatUsageCount(summary.inputTokens + summary.outputTokens),
                note = usageTokensNoteText(summary),
                modifier = Modifier.weight(1f),
            )
            UsageStatCell(
                label = "成本",
                value = formatUsageCost(summary.cost),
                note = "按本机价格表",
                modifier = Modifier.weight(1f),
            )
        }
    }
}

/** 一格数字：说明 → 大数字（单位降级）→ 注解。三格在同一行，左缘按 weight 严格对齐。 */
@Composable
private fun UsageStatCell(label: String, value: String, note: String, modifier: Modifier = Modifier) {
    val parts = statFocusParts(value)
    Column(
        modifier = modifier,
        verticalArrangement = Arrangement.spacedBy(TightGap),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
        Row(verticalAlignment = Alignment.Bottom) {
            if (parts.prefix.isNotEmpty()) {
                Text(
                    text = parts.prefix,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                text = parts.number,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (parts.suffix.isNotEmpty()) {
                Text(
                    text = parts.suffix,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Text(
            text = note,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
        )
    }
}

@Composable
private fun UsageModelsCard(models: List<UsageModelRow>) {
    UsageCard {
        UsageCardTitle("按模型")
        if (models.isEmpty()) {
            Text(
                text = usageModelsEmptyHint(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@UsageCard
        }
        models.take(8).forEach { model ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(RowGap),
            ) {
                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text(
                        text = model.name,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = "${formatUsageCount(model.calls)} 次 · " +
                            "${formatUsageCount(model.inputTokens + model.outputTokens)} tokens",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Text(
                    text = formatUsageCost(model.cost),
                    modifier = Modifier.weight(UsageRightColumnWeight),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurface,
                    textAlign = TextAlign.End,
                    maxLines = 1,
                )
            }
        }
        if (models.size > 8) {
            Text(
                text = "…另有 ${models.size - 8} 个模型，可在「请求」页按模型筛选查看。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun UsageSeriesCard(points: List<UsageDayPoint>) {
    UsageCard {
        UsageCardTitle("近期趋势")
        if (points.isEmpty()) {
            Text(
                text = usageSeriesEmptyHint(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@UsageCard
        }
        points.forEach { point ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(RowGap),
            ) {
                Text(
                    text = point.label.ifBlank { "—" },
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                )
                Text(
                    text = "${formatUsageCount(point.calls)} 次",
                    modifier = Modifier.weight(UsageRightColumnWeight),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                    textAlign = TextAlign.End,
                    maxLines = 1,
                )
                Text(
                    text = formatUsageCost(point.cost),
                    modifier = Modifier.weight(UsageRightColumnWeight),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.End,
                    maxLines = 1,
                )
            }
        }
    }
}

/**
 * 状态卡：未启动 / 空账本 / 读不到。挂在 primaryContainer 或 errorContainer 上，
 * 与内容卡（Sheet + surface）区分开，避免"一张白卡解决所有状态"。
 */
@Composable
private fun UsageStateCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.primaryContainer) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(CardPadding),
            verticalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
            )
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f),
            )
            if (actionLabel != null && onAction != null) {
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
            }
        }
    }
}

@Composable
private fun UsageLoadingCard(text: String) {
    Surface(
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(CardPadding),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            CircularProgressIndicator(
                modifier = Modifier.size(16.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Text(
                text = text,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun UsageErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(CardPadding),
            verticalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Text(
                text = "读取用量失败",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
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
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) { Text("重试") }
        }
    }
}

/** 右侧数值列的权重：同一区块内所有行共用，保证右缘在同一条竖线上。 */
private const val UsageRightColumnWeight = 0.6f

/** 区块标题下的周期口径：与 [UsagePeriod] 一一对应，不写"当前"。 */
internal fun usagePeriodScopeLabel(period: UsagePeriod): String = when (period) {
    UsagePeriod.TODAY -> "今日"
    UsagePeriod.WEEK -> "近 7 天"
}

/** 成功率 = (请求 - 失败) / 请求；没有请求时写"—"，不写 0%。 */
internal fun usageSuccessRateText(summary: UsageSummary): String {
    if (summary.calls <= 0L) {
        return "—"
    }
    val rate = (summary.calls - summary.errors).toDouble() / summary.calls.toDouble() * 100.0
    return String.format(Locale.US, "%.1f%%", rate.coerceIn(0.0, 100.0))
}

/** 请求格的注解行：失败次数 + 成功率。 */
internal fun usageRequestsNoteText(summary: UsageSummary): String =
    "失败 ${formatUsageCount(summary.errors)} · 成功率 ${usageSuccessRateText(summary)}"

/** 缓存命中率 = 缓存读 / (输入 + 缓存读 + 缓存写)；分母为 0 时写"—"。 */
internal fun usageCacheHitRateText(summary: UsageSummary): String {
    val inputTotal = summary.inputTokens + summary.cacheReadTokens + summary.cacheWriteTokens
    if (inputTotal <= 0L) {
        return "—"
    }
    val rate = summary.cacheReadTokens.toDouble() / inputTotal.toDouble() * 100.0
    return String.format(Locale.US, "%.1f%%", rate.coerceIn(0.0, 100.0))
}

/** Token 格的注解行：缓存 token 与命中率（桌面端同一口径）。 */
internal fun usageTokensNoteText(summary: UsageSummary): String =
    "缓存 ${formatUsageCount(summary.cacheReadTokens)} · 命中 ${usageCacheHitRateText(summary)}"

/** 账本为空时的标题：说清"为什么空"，不写"暂无数据"。 */
internal fun usageEmptyHeadline(): String = "还没有用量"

/** 账本为空时的说明：为什么空 + 下一步做什么。 */
internal fun usageEmptyDetail(): String =
    "这台手机的网关还没处理过请求，所以账本是空的。" +
        "下一步：用任意 OpenAI 兼容客户端向本机网关发一次请求，这里就会出现按模型汇总的条目。"

/** 「按模型」为空：为什么空 + 去哪里看。 */
internal fun usageModelsEmptyHint(): String =
    "这个周期里没有按模型的记录：账本只统计真正走完的请求。" +
        "下一步：先在概览页确认网关在运行，再发出一次请求。"

/** 「近期趋势」为空：只有请求量 > 0 的日期才画，所以刚启动时是空的。 */
internal fun usageSeriesEmptyHint(): String =
    "这个周期里还没有按天的调用量，所以趋势是空的。" +
        "下一步：发出请求后回来刷新，或把周期切成「今日 / 7 天」对比。"

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
