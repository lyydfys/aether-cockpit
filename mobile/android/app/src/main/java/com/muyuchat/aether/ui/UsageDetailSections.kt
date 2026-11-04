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
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.PriceRow
import com.muyuchat.aether.runtime.PricingCatalog
import com.muyuchat.aether.runtime.StripDay
import com.muyuchat.aether.runtime.formatMoney
import com.muyuchat.aether.runtime.formatTokens
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape

// 与概览页/用量页同一套节奏：卡内 16dp、行间 8~12dp，不再出现 2/6/14dp 这类随手值。
private val CardPadding = 16.dp
private val TightGap = 8.dp
private val CellPadding = 4.dp

/**
 * 用量热力图：**文字格子**，不引入图表库。
 *
 * 口径：格子数值只能来自服务端；服务端没给的那天是"占位格"（空底纹 + 不显示 0 次），
 * 不是"0 次调用"——两者含义不同，混着画会让人以为那天真的没用过。
 */
@Composable
internal fun UsageHeatmapSection(days: List<StripDay>, error: String?) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(CardPadding)) {
            Text(
                text = "近 ${days.size} 天用量",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(TightGap))
            if (error != null) {
                Text(
                    text = "读不到热力图：$error",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                return@Column
            }
            if (days.isEmpty()) {
                Text(
                    text = heatmapEmptyHint(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                return@Column
            }
            val peak = days.maxOf { it.calls }
            days.chunked(10).forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(TightGap)) {
                    row.forEach { day ->
                        HeatmapCell(day = day, peak = peak)
                    }
                }
                Spacer(Modifier.height(TightGap))
            }
            Text(
                text = "深色=调用更多；空格=那天服务端没有记录（不是 0 次）。最近一天在最右。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun HeatmapCell(day: StripDay, peak: Int) {
    val intensity = if (peak <= 0 || day.placeholder) 0f else (day.calls.toFloat() / peak).coerceIn(0.15f, 1f)
    val background = when {
        day.placeholder -> MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.35f)
        else -> MaterialTheme.colorScheme.primary.copy(alpha = 0.12f + 0.55f * intensity)
    }
    Surface(
        modifier = Modifier.width(28.dp).height(34.dp).clip(McaShape.Badge),
        color = background,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(horizontal = CellPadding),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = day.date.takeLast(2),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (!day.placeholder) {
                Text(
                    text = day.calls.toString(),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

/** 价格表：**只读**。编辑留在桌面端——改错单价会直接改掉账本口径。 */
@Composable
internal fun UsagePricingSection(pricing: PricingCatalog, error: String?) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(CardPadding)) {
            Text(
                text = "价格表（只读）",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(TightGap))
            if (error != null) {
                Text(
                    text = "读不到价格表：$error",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                return@Column
            }
            val total = pricing.defaults.size + pricing.overrides.size
            if (total == 0) {
                Text(
                    text = pricingEmptyHint(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                return@Column
            }
            Text(
                // 注意：`usd_cny` 恒为"1 USD 折合多少 CNY"，与展示币种无关，
                // 所以右半边固定写 CNY——写成展示币种会变成"1 USD = 7.2 USD"这种胡话。
                text = "单位：${pricing.currency} / 百万 token" +
                    if (pricing.usdCny > 0) " · 汇率 1 USD = ${pricing.usdCny} CNY" else "",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(TightGap))
            pricing.overrides.forEach { row ->
                PriceLine(row = row, currency = pricing.currency, overridden = true)
            }
            pricing.defaults.take(12).forEach { row ->
                PriceLine(row = row, currency = pricing.currency, overridden = false)
            }
            if (pricing.defaults.size > 12) {
                Text(
                    text = "…另有 ${pricing.defaults.size - 12} 条内置价格项（完整列表请在桌面端查看）",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun PriceLine(row: PriceRow, currency: String, overridden: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = row.pattern,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.width(150.dp),
        )
        Text(
            text = "入 ${formatMoney(row.inputPerM, currency)} · 出 ${formatMoney(row.outputPerM, currency)}" +
                if (row.cachedReadPerM > 0) " · 缓存 ${formatMoney(row.cachedReadPerM, currency)}" else "",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (overridden) {
            Spacer(Modifier.width(TightGap))
            Text(
                text = "自定义",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
    }
}

/** token 数展示复用 runtime 里的同一个格式化函数，避免两套小数位。 */
internal fun heatmapTokensLabel(day: StripDay): String = formatTokens(day.tokens)

/** 热力图空态：为什么空 + 下一步。"空"不是 0 次调用，这句必须说清。 */
internal fun heatmapEmptyHint(): String =
    "为什么空：服务端最近没有返回任何按天的记录——空不等于 0 次调用。" +
        "下一步：发出一次请求后回来刷新，这一节会按天亮起来。"

/** 价格表空态：为什么空 + 下一步。 */
internal fun pricingEmptyHint(): String =
    "为什么空：本机价格表里既没有内置项也没有自定义项，通常意味着价格接口还没读到。" +
        "下一步：确认网关在运行后重新刷新这一页。"
