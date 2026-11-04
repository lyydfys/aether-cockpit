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
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.sessionContentSearchLine
import com.muyuchat.aether.runtime.shortenSessionId
import com.muyuchat.aether.runtime.sessionStatsLine
import com.muyuchat.aether.runtime.sessionStatsTokensLine
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.SessionRow
import com.muyuchat.aether.runtime.SessionUsage
import com.muyuchat.aether.runtime.SessionUsageDay
import com.muyuchat.aether.runtime.SessionUsageTotals
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize

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
    // 桌面端的 segbar（`sessions.js:117`）：会话列表 / 会话用量。手机端同样两档，本地切换。
    var view by rememberSaveable { mutableStateOf(SessionView.LIST.name) }
    var period by rememberSaveable { mutableStateOf(7) }
    val currentView = if (view == SessionView.USAGE.name) SessionView.USAGE else SessionView.LIST

    LaunchedEffect(Unit) {
        onRefresh()
        onRefreshStats()
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
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
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "本机只读 · 不修改 Agent 记录",
                        style = MaterialTheme.typography.bodySmall,
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
        // 桌面端的 segbar（`sessions.js:117-131`）：会话列表 / 会话用量
        item {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                SessionView.entries.forEach { option ->
                    McaButton(
                        onClick = { view = option.name },
                        style = if (currentView == option) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                    ) { Text(sessionViewLabel(option)) }
                }
            }
        }
        // 空态解释排在统计之前：这是手机上最常见、也最容易被误读成故障的状态。
        item { DeepSectionTitle("为什么这里通常是空的") }
        item {
            DeepCard {
                DeepCardBody(
                    "会话列表来自 Claude Code / Codex / Gemini 等 Agent 写在桌面用户目录里的文件。" +
                        "Android 应用只能读自己的沙箱，也不会去扫描其它 App 的私有目录，所以这里是空的——" +
                        "这不是故障，本页与 Windows 数据之间也没有任何同步通路。",
                )
            }
        }
        // 会话统计：只有计数与 token 分布，不解析 top/shape 里的会话级明细。
        item { DeepSectionTitle("本机会话统计") }
        item {
            DeepCard {
                when (val current = stats) {
                    SessionStatsUiState.Idle, SessionStatsUiState.Running -> DeepCardBody("正在读取…")
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
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(6.dp))
                        DeepCardBody(sessionStatsTokensLine(current.stats))
                    }
                }
            }
        }
        if (currentView == SessionView.USAGE) {
            sessionUsageItems(
                usage = (state as? SessionsUiState.Ready)?.usage ?: SessionUsageUiState.Idle,
                period = period,
                agents = (state as? SessionsUiState.Ready)?.agents.orEmpty(),
                agentFilter = agentFilter,
                onPeriod = { period = it },
                onAgentChange = onAgentChange,
                onRefresh = onRefresh,
                onOpenOverview = onOpenOverview,
            )
        } else {
        when (state) {
            SessionsUiState.Idle, SessionsUiState.Loading -> item {
                DeepCardBody("正在读取本机会话索引…")
            }
            is SessionsUiState.Failed -> item {
                DeepStateCard(
                    title = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                    detail = if (state.code == "gateway_not_running") {
                        "启动本地网关后即可读取会话索引。"
                    } else {
                        "${state.message}（错误码：${state.code}）"
                    },
                    tone = if (state.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
                    actionLabel = if (state.code == "gateway_not_running") "前往概览" else "重试",
                    onAction = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                )
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
                                Spacer(Modifier.height(12.dp))
                                Text(
                                    text = "${current.message}（错误码：${current.code}）",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                            is SessionContentUiState.Ready -> {
                                Spacer(Modifier.height(12.dp))
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
                                Spacer(Modifier.height(6.dp))
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
                        DeepStateCard(
                            title = if (query.isBlank()) "本机索引里没有会话记录" else "没有匹配「$query」的会话",
                            detail = if (query.isBlank()) {
                                "手机上是正常状态：会话文件在桌面端的用户目录里，这里读不到。"
                            } else {
                                "换一个关键词，或切换上方的 Agent 筛选。"
                            },
                            actionLabel = if (query.isBlank()) "重新扫描" else "清空搜索",
                            onAction = if (query.isBlank()) {
                                onRefresh
                            } else {
                                { onQueryChange("") }
                            },
                        )
                    }
                } else {
                    item {
                        DeepSectionTitle(
                            text = "会话列表",
                            trailing = sessionCountLabel(state.sessions.size),
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
}

/** 会话页的两档视图（桌面 `sessions.js:119`：会话列表 / 会话用量）。 */
internal enum class SessionView {
    LIST,
    USAGE,
}

internal fun sessionViewLabel(view: SessionView): String = when (view) {
    SessionView.LIST -> "会话列表"
    SessionView.USAGE -> "会话用量"
}

/** 时间范围的文案（桌面 `sessions.js:263`：今日 / 近 7 天 / 近 30 天）。 */
internal fun sessionUsagePeriodLabel(days: Int): String = when (days) {
    1 -> "今日"
    30 -> "近 30 天"
    else -> "近 7 天"
}

/** 桌面 `fmtBig`（`sessions.js:29-34`）：≥1 亿用「亿」、≥1 万用「万」，其余取整。 */
internal fun sessionUsageBigLabel(value: Long): String {
    val v = value.coerceAtLeast(0L)
    return when {
        v >= 100_000_000L -> String.format(java.util.Locale.US, "%.2f 亿", v / 100_000_000.0)
        v >= 10_000L -> String.format(java.util.Locale.US, "%.2f 万", v / 10_000.0)
        else -> v.toString()
    }
}

/** 费用：有未计价请求时如实标注，不把未知成本算成 0。 */
internal fun sessionUsageCostLabel(cost: Double, unpriced: Long): String {
    val safe = if (cost.isFinite() && cost > 0.0) cost else 0.0
    val money = String.format(java.util.Locale.US, "$%.4f", safe)
    return if (unpriced > 0L) "$money · 未计价 $unpriced 次" else money
}

/** 一行汇总：Token / 请求 / 费用（桌面三张表都用同一组列）。 */
internal fun sessionUsageTotalsLine(totals: SessionUsageTotals, unpriced: Long = 0L): String =
    "Token ${sessionUsageBigLabel(totals.tokens)} · 请求 ${totals.requests} · " +
        sessionUsageCostLabel(totals.cost, unpriced)

/**
 * 按日期表的行序：**日期倒序**（桌面 `sessions.js:248` 对 series 做了 `b.date.localeCompare(a.date)`）。
 * 服务端按升序回（`sessions_usage_summary.go:184-192`），这里必须翻过来。
 */
internal fun sessionUsageDayRows(usage: SessionUsage): List<SessionUsageDay> =
    usage.series.sortedByDescending { it.date }

/** 按实例表的行序：服务端已按 tokens 降序（`sessions_usage_summary.go:208-213`），保持不动。 */
internal fun sessionUsageAgentRows(usage: SessionUsage): List<com.muyuchat.aether.runtime.SessionUsageAgent> =
    usage.agents

/**
 * 会话用量区（桌面 `sessions.js:210-256`）：
 * 时间范围 → 实例筛选 → 总览 → 按模型 / 按实例 / 按日期三张表。
 */
private fun LazyListScope.sessionUsageItems(
    usage: SessionUsageUiState,
    period: Int,
    agents: List<com.muyuchat.aether.runtime.AgentOption>,
    agentFilter: String,
    onPeriod: (Int) -> Unit,
    onAgentChange: (String) -> Unit,
    onRefresh: () -> Unit,
    onOpenOverview: () -> Unit,
) {
    item {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            SESSION_USAGE_PERIODS.forEach { days ->
                McaButton(
                    onClick = { onPeriod(days) },
                    style = if (period == days) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                ) { Text(sessionUsagePeriodLabel(days)) }
            }
        }
    }
    if (agents.isNotEmpty()) {
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
                ) { Text("全部实例") }
                agents.forEach { agent ->
                    McaButton(
                        onClick = { onAgentChange(agent.id) },
                        style = if (agentFilter == agent.id) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                    ) { Text(agent.label) }
                }
            }
        }
    }
    when (usage) {
        SessionUsageUiState.Idle, SessionUsageUiState.Loading -> item {
            DeepCard {
                DeepCardBody("正在汇总会话用量…")
                Spacer(Modifier.height(8.dp))
                McaButton(onClick = onRefresh, style = McaButtonStyle.Outlined) { Text("重新扫描") }
            }
        }
        is SessionUsageUiState.Failed -> item {
            DeepStateCard(
                title = if (usage.code == "gateway_not_running") "网关未启动" else "读取会话用量失败",
                detail = if (usage.code == "gateway_not_running") {
                    "启动本地网关后即可汇总会话用量。"
                } else {
                    "${usage.message}（错误码：${usage.code}）"
                },
                tone = if (usage.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
                actionLabel = if (usage.code == "gateway_not_running") "前往概览" else "重试",
                onAction = if (usage.code == "gateway_not_running") onOpenOverview else onRefresh,
            )
        }
        is SessionUsageUiState.Ready -> {
            val current = usage.byDays[period] ?: usage.byDays[SESSION_USAGE_PERIODS.first()]
            if (current == null) {
                item { DeepCard { DeepCardBody("这个周期还没有数据。") } }
            } else {
                item {
                    DeepSectionTitle(
                        text = "会话用量",
                        trailing = sessionUsagePeriodLabel(current.days.takeIf { it > 0 } ?: period),
                    )
                }
                item {
                    DeepCard {
                        DeepCardBody("合计 " + sessionUsageTotalsLine(current.totals, current.unpriced))
                        Spacer(Modifier.height(6.dp))
                        Text(
                            text = "${current.from} ~ ${current.to} · ${current.sessions} 个会话 · 扫描 ${current.files} 个文件",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(8.dp))
                        McaButton(onClick = onRefresh, style = McaButtonStyle.Outlined) { Text("重新扫描") }
                    }
                }
                item { DeepSectionTitle(text = "按模型", trailing = "${current.models.size} 行") }
                if (current.models.isEmpty()) {
                    item { DeepCard { DeepCardBody("这个周期没有模型用量。") } }
                } else {
                    current.models.forEach { row ->
                        item { SessionUsageRowCard(name = row.name, detail = sessionUsageTotalsLine(row.totals)) }
                    }
                }
                item { DeepSectionTitle(text = "按实例", trailing = "${sessionUsageAgentRows(current).size} 行") }
                if (sessionUsageAgentRows(current).isEmpty()) {
                    item { DeepCard { DeepCardBody("这个周期没有实例用量。") } }
                } else {
                    sessionUsageAgentRows(current).forEach { row ->
                        item {
                            SessionUsageRowCard(
                                name = row.name,
                                detail = "${row.sessions} 个会话 · " + sessionUsageTotalsLine(row.totals),
                            )
                        }
                    }
                }
                item {
                    DeepSectionTitle(
                        text = "按日期",
                        trailing = sessionCountLabel(sessionUsageDayRows(current).size),
                    )
                }
                if (sessionUsageDayRows(current).isEmpty()) {
                    item { DeepCard { DeepCardBody("这个周期没有按日数据。") } }
                } else {
                    sessionUsageDayRows(current).forEach { row ->
                        item { SessionUsageRowCard(name = row.date, detail = sessionUsageTotalsLine(row.totals)) }
                    }
                }
                item {
                    Text(
                        text = "从本机各 Agent 的会话 JSONL 汇总，不依赖官方额度或网关请求日志；" +
                            "费用按当前价格表估算，未计价的请求会单独标注。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun SessionUsageRowCard(name: String, detail: String) {
    DeepCard {
        Text(
            text = name,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Medium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = detail,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** 列表计数文案：0 条时不写「共 0 条」，交给空态卡解释。 */
internal fun sessionCountLabel(count: Int): String = "$count 条"

/** 第一行副标题：Agent 名 + 末级目录（全路径里带用户名，不显示）。 */
internal fun sessionMetaLine(row: SessionRow): String = buildString {
    append(row.agentName.ifBlank { row.agent.ifBlank { "未知 Agent" } })
    if (row.directory.isNotBlank()) {
        append(" · ")
        append(row.directory)
    }
}

/** 第二行副标题：相对时间 + 请求数 + 消息数。 */
internal fun sessionStatsMetaLine(row: SessionRow, nowMs: Long): String =
    "${sessionAgeLabel(row.lastAt, nowMs)} · ${row.requests} 请求 · ${row.messages} 消息"

@Composable
private fun SessionRowCard(row: SessionRow) {
    DeepCard {
        Text(
            text = row.title.ifBlank { row.id.ifBlank { row.key } },
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Medium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(6.dp))
        Text(
            text = sessionMetaLine(row),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = sessionStatsMetaLine(row, System.currentTimeMillis()),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
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
