package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
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
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.SessionRow
import com.muyuchat.aether.runtime.SessionTranscriptPart
import com.muyuchat.aether.runtime.SessionUsage
import com.muyuchat.aether.runtime.SessionUsageDay
import com.muyuchat.aether.runtime.SessionUsageTotals
import com.muyuchat.aether.runtime.SessionUsageView
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
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
    /** 会话详情（桌面 `sessions.js:403` 的详情抽屉）；为 null 时行不可点，避免点了没反应的死入口。 */
    detail: SessionDetailUiState = SessionDetailUiState.Idle,
    onOpenSession: ((SessionRow) -> Unit)? = null,
    onCloseDetail: () -> Unit = {},
    /** 「复制 Markdown」（桌面是「下载 Markdown」，`sessions.js:412`）。 */
    markdown: SessionMarkdownUiState = SessionMarkdownUiState.Idle,
    onLoadMarkdown: (agent: String, id: String) -> Unit = { _, _ -> },
    onMarkdownCopied: () -> Unit = {},
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
        contentPadding = bubbleSafeContentPadding(),
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
        // 桌面 KPI 行（sessions.js:152-157）：会话总数 / 客户端 / 近 7 天活跃 / 当前筛选。
        val readyList = (state as? SessionsUiState.Ready)?.sessions.orEmpty()
        val readyAgents = (state as? SessionsUiState.Ready)?.agents.orEmpty()
        item {
            DeepCard {
                SessionKpiRow("会话总数", readyList.size.toString(), "本机可读取的会话文件")
                SessionKpiRow(
                    "客户端",
                    readyAgents.size.toString(),
                    readyAgents.joinToString(" · ") { it.label }.ifBlank { "—" },
                )
                SessionKpiRow(
                    "近 7 天活跃",
                    ((stats as? SessionStatsUiState.Ready)?.stats?.sessions ?: 0).toString(),
                    "活跃记录 · 最近 7 天",
                )
                SessionKpiRow(
                    "当前筛选",
                    readyList.count { sessionMatches(it, agentFilter, query) }.toString(),
                    readyAgents.firstOrNull { it.id == agentFilter }?.label ?: "全部客户端",
                )
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
                // 桌面卡片头（sessions.js:159-182）：标题「会话列表」+「N 个」+ 搜索 + 客户端页签（带计数）+ 刷新。
                item {
                    DeepSectionTitle(
                        text = "会话列表",
                        trailing = sessionCountLabel(state.sessions.size),
                        action = {
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
                        },
                    )
                }
                item { SearchField(query, onQueryChange, placeholder = "搜索标题、项目路径或客户端…") }
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
                        ) { Text("全部 ${state.sessions.size}") }
                        state.agents.forEach { agent ->
                            McaButton(
                                onClick = { onAgentChange(agent.id) },
                                style = if (agentFilter == agent.id) {
                                    McaButtonStyle.Primary
                                } else {
                                    McaButtonStyle.Outlined
                                },
                            ) { Text("${agent.label} ${state.sessions.count { it.agent == agent.id }}") }
                        }
                    }
                }
                val visible = state.sessions.filter { sessionMatches(it, agentFilter, query) }
                if (state.sessions.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = if (query.isBlank()) "还没有可读取的会话" else "没有匹配「$query」的会话",
                            detail = if (query.isBlank()) {
                                "本机 Claude Code / Codex CLI 的会话文件出现后，这里会自动显示；" +
                                    "会话文件写在桌面用户目录，手机端读不到属于正常状态。"
                            } else {
                                "换一个关键词，或切换上方的客户端筛选。"
                            },
                            actionLabel = if (query.isBlank()) "重新扫描" else "清空搜索",
                            onAction = if (query.isBlank()) {
                                onRefresh
                            } else {
                                { onQueryChange("") }
                            },
                        )
                    }
                } else if (visible.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "没有匹配的会话",
                            detail = "换一个关键词，或切换客户端筛选。",
                            actionLabel = "清空筛选",
                            onAction = {
                                onAgentChange("")
                                onQueryChange("")
                            },
                        )
                    }
                } else {
                    items(visible.size) { index ->
                        val row = visible[index]
                        SessionRowCard(
                            row = row,
                            onClick = onOpenSession?.let { open -> { open(row) } },
                        )
                    }
                }
                // 桌面脚注（sessions.js:205）。
                item {
                    Text(
                        text = "会话内容只从本机客户端写入的本地文件读取，不修改原始记录；" +
                            "Gemini CLI 历史会话展示最近 90 天。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        }
    }

    // 会话详情抽屉（桌面 sessions.js:403-478）：手机端用对话框，正文区可滚动。
    SessionsDetailDialog(
        detail = detail,
        markdown = markdown,
        onLoadMarkdown = onLoadMarkdown,
        onMarkdownCopied = onMarkdownCopied,
        onClose = onCloseDetail,
    )
}

/**
 * 会话详情对话框：用量 chips + 正文片段 + 两个真能用的动作
 * （复制恢复命令、复制 Markdown）。
 */
@Composable
private fun SessionsDetailDialog(
    detail: SessionDetailUiState,
    markdown: SessionMarkdownUiState,
    onLoadMarkdown: (agent: String, id: String) -> Unit,
    onMarkdownCopied: () -> Unit,
    onClose: () -> Unit,
) {
    val clipboard = LocalClipboardManager.current
    // Markdown 取回来就写剪贴板，然后让 VM 丢掉这份一次性结果（避免重复写）。
    LaunchedEffect(markdown) {
        if (markdown is SessionMarkdownUiState.Ready) {
            clipboard.setText(AnnotatedString(markdown.markdown))
            onMarkdownCopied()
        }
    }
    if (detail is SessionDetailUiState.Idle) {
        return
    }
    val sessionKey = when (detail) {
        is SessionDetailUiState.Loading -> detail.sessionKey
        is SessionDetailUiState.Ready -> detail.sessionKey
        is SessionDetailUiState.Failed -> detail.sessionKey
        SessionDetailUiState.Idle -> ""
    }
    McaDialog(
        onDismissRequest = onClose,
        title = {
            Text(
                text = "会话 · ${sessionDetailTitle(detail)}",
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column {
                // 动作在上、正文在下：桌面把按钮组放在用量与正文之前（`sessions.js:409-478`）。
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    val usage = (detail as? SessionDetailUiState.Ready)?.usage
                    if (usage != null && usage.resume.isNotBlank()) {
                        McaButton(
                            onClick = { clipboard.setText(AnnotatedString(usage.resume)) },
                            style = McaButtonStyle.Outlined,
                        ) { Text("复制恢复命令") }
                    }
                    if (detail is SessionDetailUiState.Ready) {
                        McaButton(
                            onClick = {
                                val agent = sessionKey.substringBefore(":", "")
                                val id = sessionKey.substringAfter(":", "")
                                if (agent.isNotBlank() && id.isNotBlank()) {
                                    onLoadMarkdown(agent, id)
                                }
                            },
                            style = McaButtonStyle.Outlined,
                        ) { Text(if (markdown is SessionMarkdownUiState.Ready) "已复制" else "复制 Markdown") }
                    }
                }
                Spacer(Modifier.height(12.dp))
                when (detail) {
                    SessionDetailUiState.Idle -> Unit
                    is SessionDetailUiState.Loading -> Text(
                        text = "正在读取会话正文…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is SessionDetailUiState.Failed -> {
                        Text(
                            text = if (detail.code == "gateway_not_running") {
                                "网关未启动，启动本地网关后可读这条会话。"
                            } else {
                                "${detail.message}（错误码：${detail.code}）"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        Spacer(Modifier.height(8.dp))
                        Text(
                            text = "下一步：回到列表确认这条会话还在（会话文件可能已被清理），或换一个 Agent 筛选再试。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    is SessionDetailUiState.Ready -> {
                        detail.usage?.let { usage ->
                            Text(
                                text = sessionDetailUsageLine(usage),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurface,
                            )
                            if (usage.models.isNotEmpty()) {
                                Text(
                                    text = "模型：${usage.models.joinToString(" · ") { it.model }}",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Spacer(Modifier.height(8.dp))
                        }
                        if (detail.parts.isEmpty()) {
                            val copy = sessionDetailEmptyCopy(detail.source)
                            Text(
                                text = copy.first,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurface,
                            )
                            Text(
                                text = copy.second,
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            LazyColumn(modifier = Modifier.heightIn(max = 360.dp)) {
                                items(detail.parts.size) { index ->
                                    val part = detail.parts[index]
                                    Column(Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
                                        Text(
                                            text = sessionTranscriptRoleLine(part),
                                            style = MaterialTheme.typography.labelSmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                        Text(
                                            text = sessionTranscriptBody(part),
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurface,
                                        )
                                    }
                                }
                            }
                            Text(
                                text = sessionTranscriptFooter(detail.parts.size, detail.cut),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        if (markdown is SessionMarkdownUiState.Failed) {
                            Spacer(Modifier.height(6.dp))
                            Text(
                                text = "${markdown.message}（错误码：${markdown.code}）",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.error,
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onClose, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/** 详情标题：优先会话标题，其次 id。 */
internal fun sessionDetailTitle(detail: SessionDetailUiState): String = when (detail) {
    is SessionDetailUiState.Ready -> detail.usage?.title?.ifBlank { detail.usage.id }
        ?: detail.sessionKey.substringAfter(":", detail.sessionKey)
    is SessionDetailUiState.Loading -> detail.sessionKey.substringAfter(":", detail.sessionKey)
    is SessionDetailUiState.Failed -> detail.sessionKey.substringAfter(":", detail.sessionKey)
    SessionDetailUiState.Idle -> ""
}

/** 用量一行（桌面 `sessions.js:449-454` 的六个 chip，压成一行）。 */
internal fun sessionDetailUsageLine(usage: SessionUsageView): String = buildString {
    append("合计 ${sessionUsageBigLabel(usage.totalTokens)} Token")
    append(" · 输入 ${sessionUsageBigLabel(usage.inputWithCache)}")
    append(" · 缓存 ${sessionUsageBigLabel(usage.cacheRead)}")
    append(" · 输出 ${sessionUsageBigLabel(usage.output)}")
    append(" · ")
    append(if (usage.unpriced) "部分未计价" else "估算 ${sessionUsageCostLabel(usage.cost, 0L)}")
    append(" · ${usage.requests} 次请求")
}

/**
 * 正文空态文案（桌面 `sessions.js:462-464`）：
 * `source == gateway` 表示网关自己的会话没有落文件，另一种是文件空/格式不同。
 */
internal fun sessionDetailEmptyCopy(source: String): Pair<String, String> = if (source == "gateway") {
    "这里没有可读的正文" to "网关自己的会话没有落文件；下一步可以在桌面端的会话目录里确认这条记录是否存在。"
} else {
    "这里没有可读的正文" to "会话文件为空或格式不同；下一步用「复制 Markdown」看能不能拿到导出内容。"
}

/** 一条片段的小标题：角色 + 时间（桌面 `sessions.js:471`）。 */
internal fun sessionTranscriptRoleLine(part: SessionTranscriptPart): String =
    if (part.at > 0L) {
        "${part.role} · ${sessionAgeLabel(part.at, System.currentTimeMillis())}"
    } else {
        part.role
    }

/** 单条正文截断：桌面每条 6000 字（`sessions.js:472`）。 */
internal fun sessionTranscriptBody(part: SessionTranscriptPart, limit: Int = 6000): String =
    part.text.trim().take(limit)

/** 正文脚注：条数与是否被服务端截断。 */
internal fun sessionTranscriptFooter(count: Int, cut: Boolean): String = buildString {
    append("共 $count 段")
    if (cut) {
        append(" · 服务端已截断，完整内容请用「复制 Markdown」")
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
                // 桌面 6 个 KPI（sessions.js:226-232）：合计 / 请求数 / 估算费用 / 输入 / 缓存输入 / 输出。
                item {
                    val inputTotal = current.totals.input + current.totals.cacheRead + current.totals.cacheWrite
                    val cacheRate = if (inputTotal > 0L) {
                        current.totals.cacheRead.toDouble() / inputTotal.toDouble() * 100.0
                    } else {
                        0.0
                    }
                    DeepCard {
                        SessionKpiRow(
                            "合计 Tokens",
                            sessionUsageBigLabel(current.totals.tokens),
                            "输入 ${sessionUsageBigLabel(inputTotal)} · 输出 ${sessionUsageBigLabel(current.totals.output)}",
                        )
                        SessionKpiRow(
                            "请求数",
                            current.totals.requests.toString(),
                            "${current.sessions} 个会话 · 本窗口",
                        )
                        SessionKpiRow(
                            "估算费用",
                            sessionUsageCostLabel(current.totals.cost, current.unpriced),
                            if (current.unpriced > 0L) "未计价 ${current.unpriced} 次" else "按当前价格表",
                        )
                        SessionKpiRow(
                            "输入 Tokens",
                            sessionUsageBigLabel(inputTotal),
                            "不含缓存 ${sessionUsageBigLabel(current.totals.input)}",
                        )
                        SessionKpiRow(
                            "缓存输入 Tokens",
                            sessionUsageBigLabel(current.totals.cacheRead),
                            String.format(java.util.Locale.US, "命中率 %.1f%%", cacheRate),
                        )
                        SessionKpiRow(
                            "输出 Tokens",
                            sessionUsageBigLabel(current.totals.output),
                            "含推理输出",
                        )
                        Spacer(Modifier.height(8.dp))
                        Text(
                            text = "${current.from} ~ ${current.to} · ${current.sessions} 个会话 · 扫描 ${current.files} 个文件",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(8.dp))
                        McaButton(onClick = onRefresh, style = McaButtonStyle.Outlined) { Text("重新扫描") }
                    }
                }
                // 桌面三张表（sessions.js:236-252），列顺序逐字对齐。
                item {
                    SessionUsageTable(
                        title = "按模型",
                        trailing = "${current.models.size} 行",
                        headers = SESSION_USAGE_MODEL_HEADERS,
                        rows = current.models.map { row ->
                            sessionUsageColumns(row.name, row.totals)
                        },
                    )
                }
                item {
                    SessionUsageTable(
                        title = "按实例",
                        trailing = "${sessionUsageAgentRows(current).size} 行",
                        headers = SESSION_USAGE_AGENT_HEADERS,
                        rows = sessionUsageAgentRows(current).map { row ->
                            sessionUsageColumns(
                                listOf(row.name, row.sessions.toString()),
                                row.totals,
                            )
                        },
                    )
                }
                item {
                    SessionUsageTable(
                        title = "按日期",
                        trailing = sessionCountLabel(sessionUsageDayRows(current).size),
                        headers = SESSION_USAGE_DAY_HEADERS,
                        rows = sessionUsageDayRows(current).map { row ->
                            sessionUsageColumns(row.date, row.totals)
                        },
                    )
                }
                item {
                    Text(
                        text = "从本机各 Agent 的会话 JSONL 汇总，不依赖官方额度或网关请求日志；" +
                            "Codex 开启「统一会话历史」后，官方与第三方会话都会计入。费用按当前价格表估算。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/** 桌面三张表的列头（`sessions.js:236` / `:242` / `:252`），顺序逐字对齐。 */
internal val SESSION_USAGE_MODEL_HEADERS: List<String> =
    listOf("模型", "输入 Tokens", "缓存 Tokens", "输出 Tokens", "合计 Tokens", "请求", "估算费用")
internal val SESSION_USAGE_AGENT_HEADERS: List<String> =
    listOf("实例", "会话", "输入 Tokens", "缓存 Tokens", "输出 Tokens", "合计 Tokens", "请求", "估算费用")
internal val SESSION_USAGE_DAY_HEADERS: List<String> =
    listOf("日期", "输入 Tokens", "缓存 Tokens", "输出 Tokens", "合计 Tokens", "请求", "估算费用")

/** 一行表格数据：前置列（名称 / 会话数）+ totals 的六列（`sessions.js:238-250`）。 */
internal fun sessionUsageColumns(leading: String, totals: SessionUsageTotals): List<String> =
    sessionUsageColumns(listOf(leading), totals)

internal fun sessionUsageColumns(leading: List<String>, totals: SessionUsageTotals): List<String> = buildList {
    addAll(leading)
    add(sessionUsageBigLabel(totals.input + totals.cacheRead + totals.cacheWrite))
    add(sessionUsageBigLabel(totals.cacheRead))
    add(sessionUsageBigLabel(totals.output))
    add(sessionUsageBigLabel(totals.tokens))
    add(totals.requests.toString())
    add(sessionUsageCostLabel(totals.cost, 0L))
}

/** 窄屏上按桌面列顺序横向滚动的表格（列宽固定，避免数字挤成一团）。 */
@Composable
private fun SessionUsageTable(
    title: String,
    trailing: String,
    headers: List<String>,
    rows: List<List<String>>,
) {
    Column {
        DeepSectionTitle(text = title, trailing = trailing)
        if (rows.isEmpty()) {
            DeepCard { DeepCardBody("暂无数据 · 当前时间范围内没有对应的会话用量。") }
            return@Column
        }
        DeepCard {
            Column(Modifier.horizontalScroll(rememberScrollState())) {
                Row {
                    headers.forEachIndexed { index, head ->
                        Text(
                            text = head,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            modifier = Modifier.width(sessionUsageColumnWidth(index)),
                        )
                    }
                }
                Spacer(Modifier.height(6.dp))
                rows.forEach { cells ->
                    Row(Modifier.padding(vertical = 4.dp)) {
                        cells.forEachIndexed { index, cell ->
                            Text(
                                text = cell,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurface,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.width(sessionUsageColumnWidth(index)),
                            )
                        }
                    }
                }
            }
        }
    }
}

/** 首列（名称）宽一些，数字列等宽。 */
private fun sessionUsageColumnWidth(index: Int) = if (index == 0) 104.dp else 84.dp

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

/** 桌面 `fmtSize`（`sessions.js:22-27`）：≥1 MB 用 MB、≥1 KB 用 KB，否则 B。 */
internal fun sessionSizeLabel(bytes: Long): String {
    val v = bytes.coerceAtLeast(0L)
    return when {
        v >= (1L shl 20) -> String.format(java.util.Locale.US, "%.1f MB", v / (1L shl 20).toDouble())
        v >= (1L shl 10) -> String.format(java.util.Locale.US, "%.1f KB", v / (1L shl 10).toDouble())
        else -> "$v B"
    }
}

/** 桌面 `fmtDate`（`sessions.js:200` 行右侧）：绝对时间，读不出时给「时间未知」。 */
internal fun sessionDateLabel(lastAtSeconds: Long): String {
    if (lastAtSeconds <= 0L) {
        return "时间未知"
    }
    return java.text.SimpleDateFormat("MM-dd HH:mm", java.util.Locale.US)
        .format(java.util.Date(lastAtSeconds * 1000L))
}

/** 行右侧：文件大小 · 最后活动时间（桌面 `sessions.js:199-201`）。 */
internal fun sessionRowRightLine(row: SessionRow): String = buildString {
    if (row.sizeBytes > 0L) {
        append(sessionSizeLabel(row.sizeBytes))
        append(" · ")
    }
    append(sessionDateLabel(row.lastAt))
}

/**
 * 客户端 + 关键词筛选（桌面 `sessions.js:150` 的 `all.filter(...)`）。
 *
 * 服务端已经按同样条件过滤过一次，这里再过滤是为了让「当前筛选」KPI 与列表口径一致：
 * 切页签是本地即时生效，不必等往返。
 */
internal fun sessionMatches(row: SessionRow, agentFilter: String, query: String): Boolean {
    if (agentFilter.isNotEmpty() && row.agent != agentFilter) {
        return false
    }
    val needle = query.trim()
    if (needle.isEmpty()) {
        return true
    }
    return listOf(row.title, row.id, row.directory, row.agent, row.agentName)
        .joinToString(" ")
        .contains(needle, ignoreCase = true)
}

/** KPI 一行：左标签 + 说明，右数值（桌面 `.kpi`，`sessions.js:480-482`）。 */
@Composable
private fun SessionKpiRow(label: String, value: String, detail: String) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = label,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
}

@Composable
private fun SessionRowCard(row: SessionRow, onClick: (() -> Unit)? = null) {
    DeepCard(
        modifier = if (onClick != null) {
            Modifier.clickable(onClick = onClick)
        } else {
            Modifier
        },
    ) {
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
        Spacer(Modifier.height(6.dp))
        Text(
            text = sessionRowRightLine(row),
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
