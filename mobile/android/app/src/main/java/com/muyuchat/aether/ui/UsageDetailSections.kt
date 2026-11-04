package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
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
import com.muyuchat.aether.runtime.QuotaHistoryAccount
import com.muyuchat.aether.runtime.StripDay
import com.muyuchat.aether.runtime.formatMoney
import com.muyuchat.aether.runtime.formatTokens
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

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
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "近 ${days.size} 天用量",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
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
                    text = "暂无可显示的日用量",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                return@Column
            }
            val peak = days.maxOf { it.calls }
            Spacer(Modifier.height(6.dp))
            days.chunked(10).forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    row.forEach { day ->
                        HeatmapCell(day = day, peak = peak)
                    }
                }
                Spacer(Modifier.height(6.dp))
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
        modifier = Modifier.width(28.dp).height(34.dp).clip(RoundedCornerShape(6.dp)),
        color = background,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 2.dp),
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

/** 配额历史：每个渠道/账号一行，显示各自周期线的**最新剩余**与重置时间。 */
@Composable
internal fun UsageQuotaSection(accounts: List<QuotaHistoryAccount>, error: String?) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "配额历史",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            when {
                error != null -> Text(
                    text = "读不到配额历史：$error",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                accounts.isEmpty() -> Text(
                    text = "暂无可显示的配额记录（只有拿到过额度数据的账号才会出现在这里）",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> accounts.forEach { account ->
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = if (account.user.isBlank()) account.provider else "${account.provider} · ${account.user}",
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    account.lines.forEach { line ->
                        val latest = line.latest()
                        Text(
                            text = if (latest == null) {
                                "${line.name}：无采样点"
                            } else {
                                "${line.name}：剩余 ${latest.left}%" +
                                    if (latest.resetsAt.isNotBlank()) " · 重置于 ${shortStamp(latest.resetsAt)}" else ""
                            },
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}

/** 价格表：**只读**。编辑留在桌面端——改错单价会直接改掉账本口径。 */
@Composable
internal fun UsagePricingSection(pricing: PricingCatalog, error: String?) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "价格表（只读）",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
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
                    text = "暂无可显示的价格项",
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
            Spacer(Modifier.height(6.dp))
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
            Spacer(Modifier.width(6.dp))
            Text(
                text = "自定义",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
    }
}

/** ISO 时间只留 `MM-DD HH:mm`（UTC），避免长串把一行撑开。 */
internal fun shortStamp(iso: String): String {
    val trimmed = iso.trim()
    if (trimmed.length < 16) {
        return trimmed
    }
    // 2026-10-27T21:38:03Z → 10-27 21:38
    return trimmed.substring(5, 10) + " " + trimmed.substring(11, 16)
}

/** token 数展示复用 runtime 里的同一个格式化函数，避免两套小数位。 */
internal fun heatmapTokensLabel(day: StripDay): String = formatTokens(day.tokens)
