package com.muyuchat.aether.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxHeight
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.stateLabel
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape

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
        verticalArrangement = Arrangement.spacedBy(18.dp),
    ) {
        item { MobileHeader(state.status, onSearch = onSearch) }
        item { PageTitle(state.status) }
        item {
            Text(
                text = "此手机独立运行 · 无需电脑或云端",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        item {
            GatewayCard(
                status = state.status,
                url = url,
                copied = copied,
                onPrimaryAction = onPrimaryAction,
                onCopy = onCopy,
            )
        }
        item { StatsGrid(stats) }
        item {
            // 「最近一次模型请求」：回答"这台手机的网关被客户端用过没有"。
            // latestRequest 为 null 表示网关未启动（不写"未知"，也不写"没有请求"）。
            if (latestRequest != null) {
                Text(
                    text = latestRequest,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        item { EntryProtocols(state.status) }
        if (state.notice.isNotEmpty()) {
            item {
                Text(
                    text = state.notice,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
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
private fun PageTitle(status: GatewayStatus) {
    val accent = statusAccent(status)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = "概览",
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.weight(1f))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .size(8.dp)
                    .clip(CircleShape)
                    .background(accent),
            )
            Spacer(Modifier.width(6.dp))
            Text(
                text = stateLabel(status),
                style = MaterialTheme.typography.labelMedium,
                color = accent,
            )
        }
    }
}

@Composable
private fun GatewayCard(
    status: GatewayStatus,
    url: String,
    copied: Boolean,
    onPrimaryAction: () -> Unit,
    onCopy: () -> Unit,
) {
    val buttonContainer = MaterialTheme.colorScheme.primary
    val buttonContent = MaterialTheme.colorScheme.onPrimary
    Surface(
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.primaryContainer,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        // FAILED 不能笼统说成"网关未启动"：用户需要知道是启动失败，
                        // 以及稳定错误码（例如首选端口被占用时的 listen_failed）。
                        text = when {
                            status.running -> "网关正在运行"
                            status.state == GatewayState.FAILED -> "启动失败"
                            else -> "网关未启动"
                        },
                        style = MaterialTheme.typography.titleLarge,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = when {
                            status.running -> "本机应用可通过回环地址接入"
                            status.state == GatewayState.FAILED ->
                                "错误码：${status.errorCode.ifBlank { "unknown" }}（可在设置里改首选端口，或改回系统分配后重试）"
                            else -> "启动后仅监听 127.0.0.1，不暴露局域网"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
                    )
                }
                Spacer(Modifier.width(12.dp))
                Surface(
                    modifier = Modifier.size(48.dp),
                    shape = CircleShape,
                    color = buttonContainer,
                ) {
                    McaIconButton(
                        contentDescription = if (status.running) "停止网关" else "启动网关",
                        onClick = onPrimaryAction,
                        enabled = !status.busy,
                        tint = buttonContent,
                    ) {
                        Icon(
                            painter = painterResource(
                                if (status.running) R.drawable.ic_close else R.drawable.ic_bolt,
                            ),
                            contentDescription = null,
                            tint = buttonContent,
                            modifier = Modifier.size(McaIconSize.Optical.Action),
                        )
                    }
                }
            }
            Spacer(Modifier.height(16.dp))
            Surface(
                shape = McaShape.Card,
                color = MaterialTheme.colorScheme.surface,
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 14.dp, vertical = 12.dp),
                ) {
                    Text(
                        text = if (copied) "已复制" else "OpenAI-compatible · 仅本机",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(4.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = url.ifEmpty { "启动后显示本机地址" },
                            modifier = Modifier.weight(1f),
                            style = if (url.isEmpty()) {
                                MaterialTheme.typography.bodyMedium
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
            }
            Spacer(Modifier.height(12.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    painter = painterResource(R.drawable.ic_shield),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
                    modifier = Modifier.size(McaIconSize.S),
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "只接受此手机上的本地连接。局域网共享默认关闭。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
                )
            }
        }
    }
}

@Composable
private fun StatsGrid(stats: OverviewStats?) {
    Surface(
        shape = McaShape.Card,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column {
            Row(Modifier.height(IntrinsicSize.Min)) {
                StatCell("今日请求", stats?.calls ?: "—", stats?.captions?.get(0) ?: "读不到", Modifier.weight(1f))
                VerticalHairline()
                StatCell("已启用账号", stats?.accounts ?: "—", stats?.captions?.get(3) ?: "读不到", Modifier.weight(1f))
            }
            HorizontalHairline()
            Row(Modifier.height(IntrinsicSize.Min)) {
                StatCell("今日 Token", stats?.tokens ?: "—", stats?.captions?.get(1) ?: "读不到", Modifier.weight(1f))
                VerticalHairline()
                StatCell("今日成本", stats?.cost ?: "—", stats?.captions?.get(2) ?: "读不到", Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun StatCell(label: String, value: String, note: String, modifier: Modifier = Modifier) {
    Column(modifier.padding(horizontal = 16.dp, vertical = 14.dp)) {
        Text(
            text = value,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.Normal,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(2.dp))
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(2.dp))
        Text(
            text = note,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun VerticalHairline() {
    // 高度交给父 Row 的 IntrinsicSize：写死 82dp 在 1.3x 字体下会比 StatCell 短一截。
    Box(
        Modifier
            .width(1.dp)
            .fillMaxHeight()
            .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f)),
    )
}

@Composable
private fun HorizontalHairline() {
    Box(
        Modifier
            .fillMaxWidth()
            .height(1.dp)
            .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f)),
    )
}

@Composable
private fun EntryProtocols(status: GatewayStatus) {
    val port = if (status.running && status.addr.isNotBlank()) {
        status.addr.substringAfterLast(':')
    } else {
        "未启动"
    }
    Column {
        Text(
            text = "入口协议",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.height(8.dp))
        Surface(
            shape = McaShape.Card,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            Column(Modifier.padding(horizontal = 4.dp, vertical = 4.dp)) {
                ProtocolRow("OpenAI Chat / Responses", "/v1/chat/completions · /v1/responses", port)
                ProtocolRow("Anthropic Messages", "/v1/messages", port)
                ProtocolRow("Gemini API", "/v1beta", port)
            }
        }
    }
}

@Composable
private fun ProtocolRow(name: String, path: String, port: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(2.dp))
            Text(
                text = path,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            text = port,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = if (port == "未启动") {
                MaterialTheme.colorScheme.onSurfaceVariant
            } else {
                MaterialTheme.colorScheme.onSurface
            },
        )
    }
}
