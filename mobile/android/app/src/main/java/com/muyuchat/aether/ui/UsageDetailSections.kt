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
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape

private val TightGap = 8.dp
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
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 20.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = "用量日历",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.weight(1f))
                if (rangeLabel.isNotBlank()) {
                    Text(
                        text = rangeLabel,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(TightGap))
            if (error != null) {
                Text(
                    text = "读不到用量日历：$error",
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
            // 7 格一行：360dp 屏、1.3× 字号下也放得下（10 格会横向溢出），30 天正好 5 行。
            days.chunked(7).forEach { row ->
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
        // 格子在卡片里：只靠底色深浅区分，不再描边（卡片已有一条外框，避免双边框）。
        // 宽高用 min 而不是定值：1.3× 字号下数字变大也不截断、不竖排。
        modifier = Modifier
            .widthIn(min = 28.dp)
            .heightIn(min = 34.dp)
            .clip(McaShape.Badge),
        color = background,
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
                    fontWeight = FontWeight.Medium,
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
