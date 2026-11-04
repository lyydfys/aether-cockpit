package com.muyuchat.aether.ui

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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.UsageDayPoint
import com.muyuchat.aether.runtime.UsageModelRow
import com.muyuchat.aether.runtime.UsageSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.util.Locale

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
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "用量",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "本机账本 · 只读",
                        style = MaterialTheme.typography.bodyMedium,
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
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                UsagePeriod.entries.forEach { value ->
                    McaButton(
                        onClick = { onPeriod(value) },
                        style = if (value == period) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                    ) {
                        Text(value.label)
                    }
                }
            }
        }
        when (state) {
            UsageUiState.Idle -> item {
                UsageStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机用量账本。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            UsageUiState.Loading -> item { UsageLoadingCard() }
            is UsageUiState.Failed -> item {
                UsageErrorCard(code = state.code, message = state.message, onRetry = onRefresh)
            }
            is UsageUiState.Ready -> {
                val summary = state.summary
                if (summary.calls == 0L) {
                    item {
                        UsageStateCard(
                            title = "还没有用量",
                            detail = "通过本机 /v1 发起的请求会汇总到这里。",
                        )
                    }
                } else {
                    item { UsageTotalsCard(summary) }
                    if (summary.models.isNotEmpty()) {
                        item { SectionTitle("按模型") }
                        item { UsageModelsCard(summary.models) }
                    }
                    val points = summary.series.takeLast(7).filter { it.calls > 0 }
                    if (points.isNotEmpty()) {
                        item { SectionTitle("近期趋势") }
                        item { UsageSeriesCard(points) }
                    }
                }
            }
        }
        // 用量细分三节：各自独立，任何一节读不到都只在本节报错。
        when (detail) {
            UsageDetailUiState.Idle, UsageDetailUiState.Loading -> Unit
            is UsageDetailUiState.Failed -> item {
                UsageStateCard(
                    title = "用量细分读不到",
                    detail = "错误码： · 原因：",
                    actionLabel = "重试",
                    onAction = onRefreshDetail,
                )
            }
            is UsageDetailUiState.Ready -> {
                val today = java.time.LocalDate.now().toString()
                item { SectionTitle("近 30 天") }
                item {
                    UsageHeatmapSection(
                        days = detail.heatmap.recentDays(30, today),
                        error = detail.errors["heatmap"],
                    )
                }
                item { QuotaPlanSection(running = status.running) }
                item { SectionTitle("配额历史") }
                item {
                    UsageQuotaSection(accounts = detail.quotaHistory, error = detail.errors["quota"])
                }
                item { SectionTitle("价格表") }
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
private fun UsageTotalsCard(summary: UsageSummary) {
    Surface(shape = McaShape.Card, color = MaterialTheme.colorScheme.primaryContainer) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Row {
                UsageStat("请求", formatCount(summary.calls), Modifier.weight(1f))
                UsageStat("Token", formatCount(summary.inputTokens + summary.outputTokens), Modifier.weight(1f))
            }
            Row {
                UsageStat("成本", formatCost(summary.cost), Modifier.weight(1f))
                UsageStat("错误", formatCount(summary.errors), Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun UsageStat(label: String, value: String, modifier: Modifier = Modifier) {
    Column(modifier) {
        Text(
            text = value,
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onPrimaryContainer,
        )
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
        )
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.onBackground,
    )
}

@Composable
private fun UsageModelsCard(models: List<UsageModelRow>) {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 8.dp),
        ) {
            models.take(8).forEach { model ->
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            text = model.name,
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.Medium,
                            color = MaterialTheme.colorScheme.onSurface,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            text = "${formatCount(model.calls)} 次 · ${formatCount(model.inputTokens + model.outputTokens)} tokens",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = formatCost(model.cost),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun UsageSeriesCard(points: List<UsageDayPoint>) {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 8.dp),
        ) {
            points.forEach { point ->
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text = point.label.ifBlank { "—" },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    Text(
                        text = "${formatCount(point.calls)} 次",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.width(10.dp))
                    Text(
                        text = formatCost(point.cost),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun UsageStateCard(
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
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) {
                    Text(actionLabel)
                }
            }
        }
    }
}

@Composable
private fun UsageLoadingCard() {
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
                text = "正在读取本机用量…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun UsageErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "读取用量失败",
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
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) {
                Text("重试")
            }
        }
    }
}

private fun formatCount(value: Long): String = when {
    value >= 1_000_000 -> String.format(Locale.US, "%.1fM", value / 1_000_000.0)
    value >= 1_000 -> String.format(Locale.US, "%.1fK", value / 1_000.0)
    else -> value.toString()
}

private fun formatCost(value: Double): String =
    if (value <= 0.0) "$0" else String.format(Locale.US, "$%.4f", value)
