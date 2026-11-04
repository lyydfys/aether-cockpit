package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AgentCliInfo
import com.muyuchat.aether.runtime.AgentSummary
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.agentCliSummaryLine
import com.muyuchat.aether.runtime.agentCliUnsupportedDetail
import com.muyuchat.aether.runtime.agentModelLine
import com.muyuchat.aether.runtime.agentModelsEmptyDetail
import com.muyuchat.aether.runtime.agentStatusLabel
import com.muyuchat.aether.runtime.agentsEmptyDetail
import com.muyuchat.aether.runtime.agentsHiddenCount
import com.muyuchat.aether.runtime.agentsVisible
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize

/** 区块标题到首行的视觉间距（与客户端页一致：标题上内距 12 + 这里 8 + 行内 16）。 */
private val TitleToRows = 8.dp

/**
 * Agent 接入页（对应桌面 `views/misc.js:10` 的 agents 视图）。
 *
 * 桌面要做三件事：看各 agent 的当前模型、选模型并写入配置、看 CLI 版本。
 * 手机端**只做第一和第三**——写模型要改桌面上的 agent 配置文件（`agents_api.go:14-18`），
 * 所以这里每一行都是只读，界面上不给"应用模型"这类按不动的按钮。
 *
 * 排版（UI_REFRESH_SPEC §1）：页头 → 只读说明卡 → 区块「Agent」→ 区块「命令行 Agent 巡检」，
 * 区块之间 24dp，卡片内 16dp，行内垂直 16dp；空态一律"为什么空 + 下一步"。
 */
@Composable
internal fun AgentsPage(
    status: GatewayStatus,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: AgentsViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val cli by viewModel.cli.collectAsStateWithLifecycle()
    val models by viewModel.models.collectAsStateWithLifecycle()
    val showAll by viewModel.showAll.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                AgentsHeadline(
                    onBack = onBack,
                    onRefresh = viewModel::refresh,
                    refreshEnabled = state !is AgentsUiState.Loading,
                )
            }
        }
        item {
            DeepCard {
                DeepCardTitle("手机端只读")
                Spacer(Modifier.height(4.dp))
                DeepCardBody(
                    "「接入 / 断开 / 应用模型」都会改写本机 agent 的配置文件（Claude Code、Codex 等的配置目录），" +
                        "这些动作只在桌面端提供。这里显示各 agent 的检测状态、当前模型与可选模型；" +
                        "模型清单可以点开看，但不能在手机上写回去。",
                )
            }
        }
        when (val current = state) {
            AgentsUiState.Idle, AgentsUiState.Loading -> item { AgentsLoadingCard("正在读取 Agent 清单…") }
            is AgentsUiState.Failed -> item {
                if (current.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "Agent 清单来自本机 /api/state；网关没起来时这个端点没有服务可应答。" +
                            "先启动网关，再回本页刷新。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "读取失败",
                        detail = "${current.message}（错误码：${current.code}）。这次没有拿到清单，" +
                            "所以下面不显示上一次的旧数据。",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = viewModel::refresh,
                    )
                }
            }
            is AgentsUiState.Ready -> {
                val all = current.overview.agents
                val visible = agentsVisible(all, showAll)
                if (all.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "没有检测到 agent",
                            detail = agentsEmptyDetail(),
                            actionLabel = "重试",
                            onAction = viewModel::refresh,
                        )
                    }
                } else {
                    item {
                        Column {
                            DeepSectionTitle(
                                text = "Agent",
                                trailing = "已接入 ${all.count { it.wired }} / 共 ${all.size}",
                            )
                            Spacer(Modifier.height(TitleToRows))
                            DeepCard(contentPadding = PaddingValues(horizontal = DeepCardPadding)) {
                                if (visible.isEmpty()) {
                                    Text(
                                        text = "当前只显示已安装/已接入的条目，这里一条都没有。" +
                                            "点下面的「显示全部」可以看完整清单（共 ${all.size} 个）。",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.padding(vertical = DeepRowVertical),
                                    )
                                } else {
                                    visible.forEach { agent ->
                                        AgentRow(
                                            agent = agent,
                                            onClick = { viewModel.openModels(agent.id) },
                                        )
                                    }
                                }
                            }
                        }
                    }
                    item {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            val hidden = agentsHiddenCount(all, showAll)
                            Text(
                                text = if (showAll) {
                                    "正在显示全部 ${all.size} 个 agent"
                                } else {
                                    "已隐藏 $hidden 个未安装、未接入的 agent"
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.weight(1f),
                            )
                            Spacer(Modifier.width(8.dp))
                            McaButton(
                                onClick = viewModel::toggleShowAll,
                                style = McaButtonStyle.Outlined,
                                size = McaButtonSize.CardAction,
                            ) { Text(if (showAll) "只看已安装" else "显示全部") }
                        }
                    }
                }
                if (current.overview.profiles.isNotEmpty()) {
                    item {
                        Column {
                            DeepSectionTitle(
                                text = "方案",
                                trailing = "${current.overview.profiles.size} 份 · 只读",
                            )
                            Spacer(Modifier.height(TitleToRows))
                            DeepCard {
                                current.overview.profiles.forEach { profile ->
                                    Text(
                                        text = profile.name,
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurface,
                                        modifier = Modifier.padding(vertical = 4.dp),
                                    )
                                }
                                Spacer(Modifier.height(4.dp))
                                DeepCardBody("方案把「哪个 agent 用哪个模型」存成快照；切换会改写各 agent 的配置，只能在桌面端执行。")
                            }
                        }
                    }
                }
            }
        }
        item {
            Column {
                DeepSectionTitle(text = "命令行 Agent 巡检")
                Spacer(Modifier.height(TitleToRows))
                AgentCliCard(
                    cli = cli,
                    onRefresh = viewModel::refreshCli,
                )
            }
        }
        item {
            Text(
                text = "只读：Agent 清单来自 /api/state，CLI 版本来自 /api/agents/cli；手机端不会改写任何 agent 配置文件。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (models !is AgentModelsUiState.Idle) {
        AgentModelsDialog(state = models, onDismiss = viewModel::closeModels)
    }
}

/** CLI 巡检卡：可用时列版本，501 时把服务端的拒绝原话摊开（不假装"一个都没装"）。 */
@Composable
private fun AgentCliCard(
    cli: AgentCliUiState,
    onRefresh: () -> Unit,
) {
    DeepCard {
        when (cli) {
            AgentCliUiState.Idle, AgentCliUiState.Loading -> Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(
                    modifier = Modifier.size(18.dp),
                    strokeWidth = 2.dp,
                    color = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(12.dp))
                Text(
                    text = "正在巡检本机 CLI…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is AgentCliUiState.Unsupported -> {
                DeepCardTitle("仅桌面端提供")
                Spacer(Modifier.height(4.dp))
                DeepCardBody(agentCliUnsupportedDetail(cli.reason))
            }
            is AgentCliUiState.Failed -> {
                DeepCardTitle("巡检读取失败", tone = DeepTone.Danger)
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "${cli.message}（错误码：${cli.code}）",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                Spacer(Modifier.height(12.dp))
                McaButton(onClick = onRefresh, style = McaButtonStyle.Outlined) { Text("重试") }
            }
            is AgentCliUiState.Ready -> {
                Text(
                    text = agentCliSummaryLine(cli.report),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (cli.report.agents.isEmpty()) {
                    Spacer(Modifier.height(4.dp))
                    DeepCardBody("为什么空：服务端这一轮巡检没有返回任何 CLI。" + "下一步：点下面的「重新巡检」再试一次。")
                } else {
                    cli.report.agents.forEach { info ->
                        Spacer(Modifier.height(8.dp))
                        CliRow(info)
                    }
                }
                Spacer(Modifier.height(12.dp))
                McaButton(
                    onClick = onRefresh,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("重新巡检") }
            }
        }
    }
}

@Composable
private fun CliRow(info: AgentCliInfo) {
    Column(Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = info.name,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (info.installed) (info.version.ifBlank { "已安装" }) else "未安装",
                style = MaterialTheme.typography.labelMedium,
                color = if (info.installed) {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
                textAlign = TextAlign.End,
                maxLines = 1,
            )
        }
        val command = if (info.installed) info.updateCmd else info.installCmd
        if (command.isNotBlank()) {
            Text(
                text = if (info.installed) "升级：$command" else "安装：$command",
                style = MaterialTheme.typography.labelSmall,
                fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** 一行 agent：名称/路径/模型在左，状态词在右；点击看模型清单（只读）。 */
@Composable
private fun AgentRow(agent: AgentSummary, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = agent.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = agentModelLine(agent),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (agent.path.isNotBlank()) {
                Text(
                    text = agent.path,
                    style = MaterialTheme.typography.labelSmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = agentStatusLabel(agent),
            style = MaterialTheme.typography.labelMedium,
            color = when {
                agent.wired || agent.detected -> MaterialTheme.colorScheme.primary
                else -> MaterialTheme.colorScheme.onSurfaceVariant
            },
            textAlign = TextAlign.End,
            maxLines = 1,
        )
    }
}

/** 模型清单对话框：只列模型与"当前"标记，不提供写入。 */
@Composable
private fun AgentModelsDialog(
    state: AgentModelsUiState,
    onDismiss: () -> Unit,
) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = when (state) {
                    is AgentModelsUiState.Loading -> "模型清单"
                    is AgentModelsUiState.Ready -> "模型清单 · ${state.view.models.size} 个"
                    is AgentModelsUiState.Failed -> "模型清单"
                    AgentModelsUiState.Idle -> "模型清单"
                },
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 360.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                when (state) {
                    AgentModelsUiState.Idle -> Unit
                    is AgentModelsUiState.Loading -> Text(
                        text = "正在读取模型清单…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is AgentModelsUiState.Failed -> Text(
                        text = state.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is AgentModelsUiState.Ready -> {
                        if (state.view.models.isEmpty()) {
                            Text(
                                text = agentModelsEmptyDetail(),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            state.view.models.forEach { model ->
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Text(
                                        text = model.name,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurface,
                                        modifier = Modifier.weight(1f),
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                    if (model.inUse) {
                                        Spacer(Modifier.width(8.dp))
                                        Text(
                                            text = "当前",
                                            style = MaterialTheme.typography.labelSmall,
                                            color = MaterialTheme.colorScheme.primary,
                                        )
                                    } else if (model.hidden) {
                                        Spacer(Modifier.width(8.dp))
                                        Text(
                                            text = "已隐藏",
                                            style = MaterialTheme.typography.labelSmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                }
                            }
                            Spacer(Modifier.height(8.dp))
                            Text(
                                text = "写入模型会改本机 agent 的配置文件，只能在桌面端执行。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
    )
}

@Composable
private fun AgentsLoadingCard(text: String) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = text,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun AgentsHeadline(
    onBack: () -> Unit,
    onRefresh: () -> Unit,
    refreshEnabled: Boolean,
) {
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
                text = "Agent 接入",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = "本机 agent 的接入与模型 · 只读",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        McaIconButton(
            contentDescription = "刷新 Agent 清单",
            onClick = onRefresh,
            enabled = refreshEnabled,
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
