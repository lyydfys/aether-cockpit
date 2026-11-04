package com.muyuchat.aether.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.stateLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape

// 间距节奏（UI_REFRESH_SPEC 第 1 节）：区块之间 24 / 卡片内与行之间 12 / 紧邻元素 8。
// 页面左右 20dp 是页框（MobileHeader 与所有页面共用），不随本页改动。
private val BlockGap = 24.dp
private val CardPadding = 16.dp
private val RowGap = 12.dp
private val TightGap = 8.dp
private val StatusDotSize = 8.dp

/**
 * 概览页。
 *
 * 排版规则（与用量页同一套）：每张卡自带一个 `titleMedium+Bold` 标题，卡与卡之间 24dp，
 * 卡内 16dp 内边距、行间 12dp；关键数字 `headlineSmall+Bold`，单位与注解降级。
 * 字段全部来自 [OverviewStats] / [GatewayStatus]，没有后端出处的数字一律不画。
 */
@Composable
internal fun OverviewPage(
    state: OverviewUiState,
    url: String,
    copied: Boolean,
    onPrimaryAction: () -> Unit,
    onCopy: () -> Unit,
    onSearch: () -> Unit,
    stats: OverviewStats? = null,
    latestRequest: String? = null,
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(BlockGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(RowGap)) {
                MobileHeader(state.status, onSearch = onSearch)
                OverviewTitle(state.status)
            }
        }
        item {
            GatewayCard(
                status = state.status,
                url = url,
                copied = copied,
                notice = state.notice,
                onPrimaryAction = onPrimaryAction,
                onCopy = onCopy,
            )
        }
        item { TodayCard(stats) }
        // latestRequest == null 表示网关未启动：这一块此时没有真实内容，就不画空壳。
        if (latestRequest != null) {
            item { LatestRequestCard(latestRequest) }
        }
        item { ProtocolsCard(state.status) }
        item {
            Text(
                text = "前台服务可能被系统或电池策略停止；此页状态即真实状态。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun OverviewTitle(status: GatewayStatus) {
    Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "概览",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Spacer(Modifier.weight(1f))
            StatusPill(status)
        }
        Text(
            text = "此手机独立运行 · 无需电脑或云端",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun StatusPill(status: GatewayStatus) {
    val accent = statusAccent(status)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier
                .size(StatusDotSize)
                .clip(CircleShape)
                .background(accent),
        )
        Spacer(Modifier.width(TightGap))
        Text(
            text = stateLabel(status),
            style = MaterialTheme.typography.labelMedium,
            color = accent,
        )
    }
}

/** 唯一的一层卡片：Sheet + hairline + 16dp 内边距，卡内不再套卡片。 */
@Composable
private fun OverviewCard(content: @Composable () -> Unit) {
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
private fun CardTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.onSurface,
    )
}

@Composable
private fun RowHairline() {
    Box(
        Modifier
            .fillMaxWidth()
            .height(McaHairline.Width)
            .background(McaHairline.subtle()),
    )
}

@Composable
private fun GatewayCard(
    status: GatewayStatus,
    url: String,
    copied: Boolean,
    notice: String,
    onPrimaryAction: () -> Unit,
    onCopy: () -> Unit,
) {
    val headline = gatewayStateHeadline(status)
    val accent = statusAccent(status)
    OverviewCard {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(TightGap),
            ) {
                Text(
                    text = headline,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = if (status.state == GatewayState.FAILED) {
                        MaterialTheme.colorScheme.error
                    } else {
                        MaterialTheme.colorScheme.onSurface
                    },
                )
                Text(
                    text = gatewayStateDetail(status),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            McaButton(
                onClick = onPrimaryAction,
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
                enabled = !status.busy,
            ) { Text(gatewayActionLabel(status)) }
        }
        RowHairline()
        Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
            Text(
                text = if (copied) "已复制" else "OpenAI-compatible · 仅本机",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = url.ifEmpty { "启动后显示本机地址" },
                    modifier = Modifier.weight(1f),
                    style = if (url.isEmpty()) {
                        MaterialTheme.typography.bodySmall
                    } else {
                        MaterialTheme.typography.titleSmall
                    },
                    fontWeight = if (url.isEmpty()) FontWeight.Normal else FontWeight.Bold,
                    fontFamily = if (url.isEmpty()) FontFamily.Default else FontFamily.Monospace,
                    color = if (url.isEmpty()) {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    } else {
                        MaterialTheme.colorScheme.onSurface
                    },
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                McaIconButton(
                    contentDescription = "复制本机地址",
                    onClick = onCopy,
                    enabled = url.isNotEmpty(),
                ) {
                    Icon(
                        painter = painterResource(R.drawable.ic_copy),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(McaIconSize.M),
                    )
                }
            }
        }
        if (notice.isNotEmpty()) {
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        RowHairline()
        Row(horizontalArrangement = Arrangement.spacedBy(TightGap)) {
            Icon(
                painter = painterResource(R.drawable.ic_shield),
                contentDescription = null,
                tint = accent,
                modifier = Modifier.size(McaIconSize.S),
            )
            Text(
                text = "只接受此手机上的本地连接。局域网共享默认关闭。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun TodayCard(stats: OverviewStats?) {
    OverviewCard {
        CardTitle("今日")
        Row(horizontalArrangement = Arrangement.spacedBy(CardPadding)) {
            StatCell(
                label = "请求",
                value = stats?.calls ?: "—",
                note = statNoteOrBlank(stats?.captions?.getOrNull(0)),
                modifier = Modifier.weight(1f),
            )
            StatCell(
                label = "可用账号",
                value = stats?.accounts ?: "—",
                note = statNoteOrBlank(stats?.captions?.getOrNull(3)),
                modifier = Modifier.weight(1f),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(CardPadding)) {
            StatCell(
                label = "Token",
                value = stats?.tokens ?: "—",
                note = statNoteOrBlank(stats?.captions?.getOrNull(1)),
                modifier = Modifier.weight(1f),
            )
            StatCell(
                label = "成本",
                value = stats?.cost ?: "—",
                note = statNoteOrBlank(stats?.captions?.getOrNull(2)),
                modifier = Modifier.weight(1f),
            )
        }
    }
}

/** 一格数字：说明 → 大数字（单位降级）→ 只有缺失时才写一行注解。 */
@Composable
private fun StatCell(label: String, value: String, note: String, modifier: Modifier = Modifier) {
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
            overflow = TextOverflow.Ellipsis,
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
        if (note.isNotEmpty()) {
            Text(
                text = note,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

@Composable
private fun LatestRequestCard(latestRequest: String) {
    OverviewCard {
        CardTitle("最近一次请求")
        Text(
            text = latestRequestBody(latestRequest),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
}

@Composable
private fun ProtocolsCard(status: GatewayStatus) {
    val port = if (status.running && status.addr.isNotBlank()) {
        status.addr.substringAfterLast(':')
    } else {
        "未启动"
    }
    OverviewCard {
        CardTitle("入口协议")
        ProtocolRow("OpenAI Chat / Responses", "/v1/chat/completions · /v1/responses", port)
        RowHairline()
        ProtocolRow("Anthropic Messages", "/v1/messages", port)
        RowHairline()
        ProtocolRow("Gemini API", "/v1beta", port)
    }
}

@Composable
private fun ProtocolRow(name: String, path: String, port: String) {
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
                text = name,
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = path,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            text = port,
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.labelMedium,
            color = if (port == "未启动") {
                MaterialTheme.colorScheme.onSurfaceVariant
            } else {
                MaterialTheme.colorScheme.onSurface
            },
            textAlign = TextAlign.End,
            maxLines = 1,
        )
    }
}

/** 一句话状态词：运行 / 启动失败 / 未启动。**不**把启动失败说成"未启动"。 */
internal fun gatewayStateHeadline(status: GatewayStatus): String = when {
    status.running -> "网关正在运行"
    status.state == GatewayState.FAILED -> "启动失败"
    else -> "网关未启动"
}

/** 状态词下面的事实说明；失败时给出稳定错误码，不写"未知错误"。 */
internal fun gatewayStateDetail(status: GatewayStatus): String = when {
    status.running -> "本机应用可通过回环地址接入"
    status.state == GatewayState.FAILED ->
        "错误码：${status.errorCode.ifBlank { "unknown" }}（可在设置里改首选端口，或改回系统分配后重试）"
    else -> "启动后仅监听 127.0.0.1，不暴露局域网"
}

/** 主按钮文案：按钮永远描述"点下去会发生什么"，停用时由 MCA 的透明度表达。 */
internal fun gatewayActionLabel(status: GatewayStatus): String =
    if (status.running) "停止网关" else "启动网关"

/**
 * 把 runtime 已经格式化好的数字拆成「前缀 / 数字 / 单位」。
 *
 * 目的只是排版：数字用 `headlineSmall+Bold`，币种与 K/M 这类量级降级到 `labelSmall`。
 * 拆分不改数值，也不猜单位——认不出来的原样留在数字里。
 */
internal data class StatFocusParts(
    val prefix: String,
    val number: String,
    val suffix: String,
)

internal fun statFocusParts(value: String): StatFocusParts {
    val trimmed = value.trim()
    if (trimmed.isEmpty()) {
        return StatFocusParts("", "—", "")
    }
    if (trimmed == "—") {
        return StatFocusParts("", trimmed, "")
    }
    val prefix = when {
        trimmed.startsWith("\$") -> "\$"
        trimmed.startsWith("¥") -> "¥"
        else -> ""
    }
    val rest = trimmed.removePrefix(prefix)
    val suffix = when {
        rest.length > 1 &&
            rest.last() in "KMB" &&
            rest.dropLast(1).all { it.isDigit() || it == '.' } -> rest.takeLast(1)
        else -> ""
    }
    return StatFocusParts(prefix = prefix, number = rest.dropLast(suffix.length), suffix = suffix)
}

/**
 * 统计格的注解。
 *
 * runtime 的 caption 在成功时是"今日/已启用"这类范围词——卡片标题已经交代了范围，
 * 再重复一遍就是满屏小字；只有"读不到"这种**缺失信号**必须留在数字下面。
 */
internal fun statNoteOrBlank(caption: String?): String {
    val text = caption?.trim().orEmpty()
    return if (text.contains("读不到")) text else ""
}

/** `latestRequestLine()` 自带"最近一次："前缀；卡片标题已经说了同一件事，这里只去前缀。 */
internal fun latestRequestBody(line: String): String = line.removePrefix("最近一次：")
