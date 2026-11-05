package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
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
import com.muyuchat.aether.runtime.StripDay
import com.muyuchat.aether.runtime.formatTokens

private val TightGap = 6.dp
private val CellPadding = 4.dp

/**
 * 用量日历（桌面 `usage.js:251-345` 的「用量日历」卡）：**文字格子**，不引入图表库。
 *
 * 口径：格子数值只能来自服务端；服务端没给的那天是"占位格"（空底纹 + 不显示 0 次），
 * 不是"0 次调用"——两者含义不同，混着画会让人以为那天真的没用过。
 *
 * @param rangeLabel 卡头右侧的区间（桌面是 chip：`from → to`），空则不显示
 */
@Composable
internal fun UsageHeatmapSection(days: List<StripDay>, error: String?, rangeLabel: String = "") {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("用量日历")
            Spacer(Modifier.weight(1f))
            if (rangeLabel.isNotBlank()) {
                AetherStatusChip(
                    text = rangeLabel,
                    tone = DeepTone.Neutral,
                )
            }
        }
        if (error != null) {
            Text(
                text = "读不到用量日历：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            return@DeepCard
        }
        if (days.isEmpty()) {
            Text(
                text = heatmapEmptyHint(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        val peak = days.maxOf { it.calls }
        // 7 格一行：360dp 屏、1.3× 字号下也放得下（10 格会横向溢出），30 天正好 5 行。
        Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
            days.chunked(7).forEach { row ->
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(TightGap),
                ) {
                    row.forEach { day ->
                        HeatmapCell(day = day, peak = peak, modifier = Modifier.weight(1f))
                    }
                    repeat((7 - row.size).coerceAtLeast(0)) {
                        Spacer(Modifier.weight(1f))
                    }
                }
            }
        }
        Text(
            text = "深色=调用更多；空格=那天服务端没有记录（不是 0 次）。最近一天在最右。",
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
    }
}

@Composable
private fun HeatmapCell(day: StripDay, peak: Int, modifier: Modifier = Modifier) {
    val intensity = if (peak <= 0 || day.placeholder) 0f else (day.calls.toFloat() / peak).coerceIn(0.15f, 1f)
    val background = when {
        day.placeholder -> MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.25f)
        else -> MaterialTheme.colorScheme.primary.copy(alpha = 0.14f + 0.58f * intensity)
    }
    Surface(
        modifier = modifier
            .widthIn(min = 28.dp)
            .heightIn(min = 38.dp)
            .clip(RoundedCornerShape(8.dp)),
        shape = RoundedCornerShape(8.dp),
        color = background,
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(horizontal = CellPadding, vertical = 4.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = day.date.takeLast(2),
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
            if (!day.placeholder) {
                Text(
                    text = day.calls.toString(),
                    style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

/** token 数展示复用 runtime 里的同一个格式化函数，避免两套小数位。 */
internal fun heatmapTokensLabel(day: StripDay): String = formatTokens(day.tokens)

/** 热力图空态：为什么空 + 下一步。"空"不是 0 次调用，这句必须说清。 */
internal fun heatmapEmptyHint(): String =
    "为什么空：服务端最近没有返回任何按天的记录——空不等于 0 次调用。" +
        "下一步：发出一次请求后回来刷新，这一节会按天亮起来。"
