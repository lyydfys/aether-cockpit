package com.muyuchat.aether.ui

import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AgentOption
import com.muyuchat.aether.runtime.sessionContentSearchLine
import com.muyuchat.aether.runtime.shortenSessionId
import com.muyuchat.aether.runtime.sessionStatsLine
import com.muyuchat.aether.runtime.sessionStatsTokensLine
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.SessionRow
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/**
 * 会话页（只读）。
 *
 * 这一页在手机上**大多数时候就是空的**，因为会话文件写在各 Agent 自己的用户目录下，
 * 而 Android 应用只能读自己的沙箱。空态因此是最重要的内容：说清「为什么看不到」，
 * 并且绝不出现「已同步 / 同步成功」——本页与 Windows 数据没有任何同步通路。
 */
@Composable
internal fun SessionsPage(
    status: GatewayStatus,
    state: SessionsUiState,
    agentFilter: String,
    query: String,
    onAgentChange: (String) -> Unit,
    onQueryChange: (String) -> Unit,
    onRefresh: () -> Unit,
    onRefreshStats: () -> Unit = {},
    stats: SessionStatsUiState = SessionStatsUiState.Idle,
    contentSearch: SessionContentUiState = SessionContentUiState.Idle,
    onSearchContent: () -> Unit = {},
    onDismissContentSearch: () -> Unit = {},
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LaunchedEffect(Unit) {
        onRefresh()
        onRefreshStats()
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                McaIconButton(contentDescription = "返回", onClick = onBack) {
                    Icon(
                        painter = painterResource(R.drawable.ic_back),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onBackground,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "会话",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "本机只读 · 不修改 Agent 记录",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新会话",
                    onClick = onRefresh,
                    enabled = state !is SessionsUiState.Loading,
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
        // 会话统计：只有计数与 token 分布，不解析 top/shape 里的会话级明细。
        item {
            Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke()) {
                Column(Modifier.fillMaxWidth().padding(14.dp)) {
                    Text(
                        text = "本机会话统计",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    when (val current = stats) {
                        SessionStatsUiState.Idle, SessionStatsUiState.Running -> Text(
                            text = "正在读取…",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        is SessionStatsUiState.Failed -> Text(
                            text = if (current.code == "gateway_not_running") {
                                "网关未启动，读不到统计。"
                            } else {
                                "${current.message}（错误码：${current.code}）"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        is SessionStatsUiState.Ready -> {
                            Text(
                                text = sessionStatsLine(current.stats),
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurface,
                            )
                            Text(
                                text = sessionStatsTokensLine(current.stats),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
        // 空态解释放在最上面：这是手机上最常见、也最容易被误读成故障的状态。
        item {
            Surface(
                shape = McaShape.Card,
                color = McaTonal.fill(),
                border = McaHairline.stroke(),
            ) {
                Column(Modifier.fillMaxWidth().padding(14.dp)) {
                    Text(
                        text = "手机上通常看不到会话",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "会话列表来自 Claude Code / Codex / Gemini 等 Agent 写在桌面用户目录里的文件。" +
                            "Android 应用只能读自己的沙箱，也不会去扫描其它 App 的私有目录，所以这里是空的——" +
                            "这不是故障，本页与 Windows 数据之间也没有任何同步通路。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        when (state) {
            SessionsUiState.Idle, SessionsUiState.Loading -> item {
                Text(
                    text = "正在读取本机会话索引…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is SessionsUiState.Failed -> item {
                Surface(
                    shape = McaShape.Card,
                    color = McaTonal.fill(),
                    border = McaHairline.stroke(),
                ) {
                    Column(Modifier.fillMaxWidth().padding(14.dp)) {
                        Text(
                            text = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = if (state.code == "gateway_not_running") {
                                "启动本地网关后即可读取会话索引。"
                            } else {
                                "${state.message}（错误码：${state.code}）"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(10.dp))
                        McaButton(
                            onClick = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                            style = McaButtonStyle.Primary,
                        ) {
                            Text(if (state.code == "gateway_not_running") "前往概览" else "重试")
                        }
                    }
                }
            }
            is SessionsUiState.Ready -> {
                item { SearchField(query, onQueryChange, placeholder = "搜索标题或目录") }
                item {
                    // 内容检索：只回答"哪些会话提到过"，**不展示正文片段**
                    //（服务端命中体里 before/match/after 就是会话正文，手机端不碰）。
                    Column(Modifier.fillMaxWidth()) {
                        McaButton(
                            onClick = onSearchContent,
                            style = McaButtonStyle.Outlined,
                            enabled = query.isNotBlank() && contentSearch !is SessionContentUiState.Running,
                        ) {
                            Text(if (contentSearch is SessionContentUiState.Running) "检索中…" else "在会话内容里搜索")
                        }
                        when (val current = contentSearch) {
                            SessionContentUiState.Idle, SessionContentUiState.Running -> Unit
                            is SessionContentUiState.Failed -> {
                                Spacer(Modifier.height(6.dp))
                                Text(
                                    text = "${current.message}（错误码：${current.code}）",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                            is SessionContentUiState.Ready -> {
                                Spacer(Modifier.height(6.dp))
                                Text(
                                    text = sessionContentSearchLine(current.result),
                                    style = MaterialTheme.typography.bodySmall,
                                    fontWeight = FontWeight.Medium,
                                    color = MaterialTheme.colorScheme.onSurface,
                                )
                                if (current.result.hits.isNotEmpty()) {
                                    Text(
                                        text = current.result.hits.joinToString("、") {
                                            shortenSessionId(it.sessionId)
                                        },
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                Text(
                                    text = "只显示命中的会话，不显示会话正文。",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                                Spacer(Modifier.height(4.dp))
                                McaButton(
                                    onClick = onDismissContentSearch,
                                    style = McaButtonStyle.Text,
                                ) { Text("收起结果") }
                            }
                        }
                    }
                }
                if (state.agents.isNotEmpty()) {
                    item {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .horizontalScroll(rememberScrollState()),
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            McaButton(
                                onClick = { onAgentChange("") },
                                style = if (agentFilter.isEmpty()) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                            ) { Text("全部") }
                            state.agents.forEach { agent ->
                                McaButton(
                                    onClick = { onAgentChange(agent.id) },
                                    style = if (agentFilter == agent.id) {
                                        McaButtonStyle.Primary
                                    } else {
                                        McaButtonStyle.Outlined
                                    },
                                ) { Text(agent.label) }
                            }
                        }
                    }
                }
                if (state.sessions.isEmpty()) {
                    item {
                        Text(
                            text = if (query.isBlank()) {
                                "本机索引里没有会话记录（手机上是正常状态）。"
                            } else {
                                "没有匹配「$query」的会话。"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                } else {
                    item {
                        Text(
                            text = "共 ${state.sessions.size} 条 · 只显示末级目录名",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    items(state.sessions.size) { index ->
                        SessionRowCard(state.sessions[index])
                    }
                }
            }
        }
    }
}

@Composable
private fun SessionRowCard(row: SessionRow) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = row.title.ifBlank { row.id.ifBlank { row.key } },
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = buildString {
                    append(row.agentName.ifBlank { row.agent })
                    if (row.directory.isNotBlank()) {
                        append(" · ")
                        append(row.directory)
                    }
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = "${sessionAgeLabel(row.lastAt, System.currentTimeMillis())} · " +
                    "${row.requests} 请求 · ${row.messages} 消息",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * 相对时间。服务端给的是 Unix 秒；时钟回拨（负数）按"刚刚"处理，
 * 不要拼出「-3 分钟前」这种文案。
 */
internal fun sessionAgeLabel(lastAtSeconds: Long, nowMs: Long): String {
    if (lastAtSeconds <= 0L) {
        return "时间未知"
    }
    val deltaSec = (nowMs / 1000L) - lastAtSeconds
    return when {
        deltaSec < 0L -> "刚刚"
        deltaSec < 60L -> "刚刚"
        deltaSec < 3600L -> "${deltaSec / 60L} 分钟前"
        deltaSec < 86_400L -> "${deltaSec / 3600L} 小时前"
        else -> "${deltaSec / 86_400L} 天前"
    }
}
