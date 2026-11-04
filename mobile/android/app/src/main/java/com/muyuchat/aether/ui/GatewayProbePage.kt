package com.muyuchat.aether.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
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
import com.muyuchat.aether.runtime.AgentCliInfo
import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.ChannelProbeReport
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.HealthzProbe
import com.muyuchat.aether.runtime.MetricsDigest
import com.muyuchat.aether.runtime.CODEX_IDENTITY_EMPTY_COPY
import com.muyuchat.aether.runtime.codexIdentityBits
import com.muyuchat.aether.runtime.codexIdentityExpectedLine
import com.muyuchat.aether.runtime.codexIdentityMismatchLine
import com.muyuchat.aether.runtime.codexIdentityStatusLabel
import com.muyuchat.aether.runtime.ProbeOutcome
import com.muyuchat.aether.runtime.QuotaAlertsReport
import com.muyuchat.aether.runtime.quotaAlertCountLabel
import com.muyuchat.aether.runtime.quotaAlertKindIsAmber
import com.muyuchat.aether.runtime.quotaAlertKindLabel
import com.muyuchat.aether.runtime.quotaAlertLine
import com.muyuchat.aether.runtime.quotaAlertRows
import com.muyuchat.aether.runtime.ReadyzProbe
import com.muyuchat.aether.runtime.VersionInfo
import com.muyuchat.aether.runtime.channelProbeAgeLabel
import com.muyuchat.aether.runtime.channelProbeSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
 * 网关与探针，1:1 对齐桌面 `internal/web/ui2/js/views/gateway.js`。
 *
 * 桌面区块顺序（gateway.js:136 的 append 顺序）：
 *   API 服务(:101-114) → 接入片段(:116-120) → 调用方密钥(:202-257) →
 *   Codex 身份对照(:141-175) → grid2[渠道探测(:122-127) + 版本与探针(:129-134)] →
 *   本机 Agent 版本(:177-200)。
 * 「额度提醒」(:86-100) 与「Codex 身份对照」在移动端没有端点/客户端，本轮不做（见汇报）。
 *
 * 手机上一列排：grid2 拆成「渠道探测」在上、「版本与探针」在下，顺序不变。
 */

/** 桌面 `gateway.js:6-11` 的四个协议 tab。 */
internal val ProbeProtocols: List<Triple<String, String, String>> = listOf(
    Triple("openai", "OpenAI", "/v1"),
    Triple("responses", "Responses", "/v1"),
    Triple("anthropic", "Anthropic", ""),
    Triple("gemini", "Gemini", "/v1beta"),
)

@Composable
internal fun GatewayProbePage(
    status: GatewayStatus,
    state: ProbeUiState,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    /** 额度提醒（桌面 `gateway.js:86-100` 的第一块）；数据来自 `GET /api/alerts`。 */
    alerts: QuotaAlertsUiState = QuotaAlertsUiState.Idle,
    /** 「去用量页」；为 null 时不渲染该按钮（避免死按钮）。 */
    onOpenUsage: (() -> Unit)? = null,
    /** Codex 请求身份对照（桌面 `gateway.js:141-175`）；只读观测。 */
    identity: CodexIdentityUiState = CodexIdentityUiState.Idle,
    /** 「刷新」身份对照；为 null 时不渲染按钮。 */
    onRefreshIdentity: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    var protocol by remember { mutableStateOf("openai") }
    var drawer by remember { mutableStateOf<String?>(null) }
    var troubleshootOpen by remember { mutableStateOf(false) }
    val clipboard = LocalClipboardManager.current

    LaunchedEffect(Unit) {
        if (state is ProbeUiState.Idle) {
            onRefresh()
        }
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item { MobileHeader(status) }
        item {
            ProbeHeadline(
                state = state,
                onBack = onBack,
                onRefresh = onRefresh,
                enabled = state !is ProbeUiState.Loading,
            )
        }
        // ① 额度提醒：桌面网关页的第一块（gateway.js:86-100）；无提醒时桌面**整卡不渲染**。
        when (alerts) {
            QuotaAlertsUiState.Idle -> Unit
            is QuotaAlertsUiState.Loading -> Unit
            is QuotaAlertsUiState.Failed -> item {
                QuotaAlertsFailedCard(alerts)
            }
            is QuotaAlertsUiState.Ready -> {
                if (alerts.report.alerts.isNotEmpty()) {
                    item {
                        QuotaAlertsCard(
                            report = alerts.report,
                            onOpenUsage = onOpenUsage,
                        )
                    }
                }
            }
        }
        // ② Codex 请求身份对照（桌面 gateway.js:141-175，只读观测）
        if (identity !is CodexIdentityUiState.Idle) {
            item {
                CodexIdentityCard(
                    state = identity,
                    onRefresh = onRefreshIdentity,
                )
            }
        }
        when (state) {
            ProbeUiState.Idle, ProbeUiState.Loading -> item { ProbeLoadingCard() }
            is ProbeUiState.Failed -> item {
                if (state.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "本页读的是本机回环上的 /healthz、/api/health、/readyz、/version、/metrics；" +
                            "网关没起来时这些端点没有服务可应答。先启动网关，再回本页重新探测。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "探测失败",
                        detail = "${state.message}（错误码：${state.code}）。这次没有拿到任何端点结果，" +
                            "所以下面不显示上一次的旧数据。",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = onRefresh,
                    )
                }
            }
            is ProbeUiState.Ready -> {
                item {
                    ApiServiceCard(
                        status = status,
                        state = state,
                        protocol = protocol,
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                    )
                }
                item {
                    SnippetCard(
                        status = status,
                        state = state,
                        protocol = protocol,
                        onProtocol = { protocol = it },
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                    )
                }
                item { CallerKeysCard(state.callerKeys) }
                item {
                    ChannelProbeCard(
                        report = state.channels,
                        probing = state.probingChannels,
                        onProbe = onRefresh,
                    )
                }
                item {
                    VersionProbeCard(
                        state = state,
                        onOpenEndpoint = { drawer = it },
                    )
                }
                item { AgentVersionCard(state.agents, state.agentsCheckedAt, onCopy = { clipboard.setText(AnnotatedString(it)) }) }
                item {
                    TroubleshootCard(
                        open = troubleshootOpen,
                        onToggle = { troubleshootOpen = !troubleshootOpen },
                        status = status,
                    )
                }
            }
        }
        item {
            Text(
                text = "诚实说明：探针结果只代表当前这一刻。每次进入本页或点刷新都会重新探测；" +
                    "网关停止后本页回到「网关未启动」，不会留着上一次的结果。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    drawer?.let { path ->
        ProbeEndpointDialog(path = path, state = state, onDismiss = { drawer = null })
    }
}

/* ---------------- 额度提醒（桌面 gateway.js:86-100） ---------------- */

/**
 * 有提醒时的卡片：标题「额度提醒」+「N 条」+「去用量页」，下面最多 3 条（桌面 `alerts.slice(0, 3)`）。
 *
 * 视觉：amber 描边（桌面用的是 `--amber` 边框 + 圆角）；每条一行「kind chip + 文案」，
 * 用 `Row` + 文案 `weight(1f)`，1.0×/1.3× 都只是换行，不会挤成竖排。
 */
@Composable
private fun QuotaAlertsCard(report: QuotaAlertsReport, onOpenUsage: (() -> Unit)?) {
    val amber = MaterialTheme.colorScheme.tertiary
    Surface(
        shape = McaShape.Sheet,
        color = McaTonal.fill(),
        border = BorderStroke(1.dp, amber),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = "额度提醒",
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = quotaAlertCountLabel(report.alerts.size),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.weight(1f))
                if (onOpenUsage != null) {
                    McaButton(onClick = onOpenUsage, style = McaButtonStyle.Text) {
                        Text("去用量页")
                    }
                }
            }
            Spacer(Modifier.height(6.dp))
            for (alert in quotaAlertRows(report.alerts)) {
                Row(
                    modifier = Modifier.fillMaxWidth().padding(vertical = 3.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.Top,
                ) {
                    Text(
                        text = quotaAlertKindLabel(alert.kind),
                        style = MaterialTheme.typography.labelSmall,
                        color = if (quotaAlertKindIsAmber(alert.kind)) {
                            MaterialTheme.colorScheme.tertiary
                        } else {
                            MaterialTheme.colorScheme.error
                        },
                        maxLines = 1,
                    )
                    Text(
                        text = quotaAlertLine(alert),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

/* ---------------- Codex 请求身份对照（桌面 gateway.js:141-175） ---------------- */

/**
 * 「Codex 请求身份对照」卡：标题 +「官方 CLI 形状」标签 + 刷新按钮；正文依次是
 * 期望形状一行、逐条样本（UA / originator / session 前 8 位 + 官方形状/有差异 + 差异清单）。
 *
 * 空态用桌面原话（`gateway.js:151`）：「还没有收到 Responses 请求；…」。
 * 只观测、不改写——服务端 note 原样展示。
 */
@Composable
private fun CodexIdentityCard(state: CodexIdentityUiState, onRefresh: (() -> Unit)?) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("Codex 请求身份对照")
            Spacer(Modifier.width(8.dp))
            Text(
                text = "官方 CLI 形状",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            if (onRefresh != null) {
                McaButton(onClick = onRefresh, style = McaButtonStyle.Text) { Text("刷新") }
            }
        }
        Spacer(Modifier.height(6.dp))
        when (state) {
            CodexIdentityUiState.Idle -> Unit
            CodexIdentityUiState.Loading -> DeepCardBody("正在读取下游请求头…")
            is CodexIdentityUiState.Failed -> DeepCardBody(
                if (state.code == "gateway_not_running") {
                    "网关未启动；启动后这里会显示下游请求里真实的身份头。"
                } else {
                    "读取失败：${state.message}（错误码：${state.code}）"
                },
                tone = if (state.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
            )
            is CodexIdentityUiState.Ready -> {
                val report = state.report
                if (report.items.isEmpty()) {
                    DeepCardBody(CODEX_IDENTITY_EMPTY_COPY)
                } else {
                    val expected = codexIdentityExpectedLine(report)
                    if (expected.isNotEmpty()) {
                        DeepCardBody(expected)
                        Spacer(Modifier.height(6.dp))
                    }
                    for (item in report.items) {
                        Row(
                            modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            verticalAlignment = Alignment.Top,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(
                                    text = codexIdentityBits(item),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurface,
                                )
                                val mismatch = codexIdentityMismatchLine(item)
                                if (mismatch.isNotEmpty()) {
                                    Text(
                                        text = mismatch,
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                            Text(
                                text = codexIdentityStatusLabel(item),
                                style = MaterialTheme.typography.labelSmall,
                                color = if (item.officialLike) {
                                    MaterialTheme.colorScheme.primary
                                } else {
                                    MaterialTheme.colorScheme.error
                                },
                                maxLines = 1,
                            )
                        }
                    }
                    if (report.note.isNotEmpty()) {
                        Spacer(Modifier.height(4.dp))
                        DeepCardBody(report.note)
                    }
                }
            }
        }
    }
}

/** 读失败：贴服务端原文，不装作"没有提醒"。 */
@Composable
private fun QuotaAlertsFailedCard(state: QuotaAlertsUiState.Failed) {    DeepCard {
        DeepCardTitle("额度提醒读取失败")
        Spacer(Modifier.height(4.dp))
        DeepCardBody(
            if (state.code == "gateway_not_running") {
                "网关未启动；启动后这一卡会自动出现。"
            } else {
                "${state.message}（错误码：${state.code}）"
            },
            tone = if (state.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
        )
    }
}

/* ---------------- 页头 ---------------- */

@Composable
private fun ProbeHeadline(
    state: ProbeUiState,
    onBack: () -> Unit,
    onRefresh: () -> Unit,
    enabled: Boolean,
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
            DeepPageTitle(
                title = "网关与探针",
                subtitle = "监听、探针与版本",
            )
            if (state is ProbeUiState.Ready) {
                Text(
                    text = "本次探测：${probeClockFormat.format(probeInstant(state.sampledAtMs))}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        McaIconButton(contentDescription = "重新探测", onClick = onRefresh, enabled = enabled) {
            Icon(
                painter = painterResource(R.drawable.ic_refresh),
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(McaIconSize.Optical.Action),
            )
        }
    }
}

/* ---------------- API 服务（gateway.js:101-114） ---------------- */

@Composable
private fun ApiServiceCard(
    status: GatewayStatus,
    state: ProbeUiState.Ready,
    protocol: String,
    onCopy: (String) -> Unit,
) {
    val base = probeBaseUrl(status, state)
    val lan = state.settings?.lanEnabled ?: false
    val health = (state.anonymous.health as? ProbeOutcome.Ok)?.value
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("API 服务")
            Spacer(Modifier.width(8.dp))
            Text(
                text = "实例网关",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            Text(
                text = if (health?.status == "ok") "运行中" else "未就绪",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Medium,
                color = if (health?.status == "ok") {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.error
                },
            )
        }
        Spacer(Modifier.height(10.dp))
        ProbeFieldRow("入口", base, trailing = {
            McaIconButton(contentDescription = "复制入口", onClick = { onCopy(base) }) {
                Icon(
                    painter = painterResource(R.drawable.ic_copy),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.Xs),
                )
            }
        })
        ProbeFieldRow("监听范围", if (lan) "局域网（需密钥）· 外部可达" else "仅本机 · 外部不可达")
        ProbeFieldRow("调用方密钥", "${state.callerKeys.size} 把 · 也可以在这里生成和管理")
        ProbeFieldRow(
            "客户端接入",
            if (state.clients.isEmpty()) "未检测到" else state.clients.joinToString(" · ") { it.name.ifBlank { it.id } },
        )
        Spacer(Modifier.height(8.dp))
        McaButton(
            onClick = { onCopy(probeSnippet(base, protocol, probeSampleModel())) },
            style = McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
        ) { Text("复制接入片段") }
    }
}

/* ---------------- 接入片段（gateway.js:116-120） ---------------- */

@Composable
private fun SnippetCard(
    status: GatewayStatus,
    state: ProbeUiState.Ready,
    protocol: String,
    onProtocol: (String) -> Unit,
    onCopy: (String) -> Unit,
) {
    val base = probeBaseUrl(status, state)
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("接入片段")
            Spacer(Modifier.width(8.dp))
            Text(
                text = "五种 API · 一个地址",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(8.dp))
        Row(
            modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            ProbeProtocols.forEach { (id, label, _) ->
                McaButton(
                    onClick = { onProtocol(id) },
                    style = if (protocol == id) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text(label) }
            }
        }
        Spacer(Modifier.height(8.dp))
        Surface(
            shape = McaShape.CardAction,
            color = McaTonal.fill(),
            border = McaHairline.stroke(),
        ) {
            Text(
                text = probeSnippet(base, protocol, probeSampleModel()),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.fillMaxWidth().padding(12.dp),
            )
        }
        Spacer(Modifier.height(8.dp))
        McaButton(
            onClick = { onCopy(probeSnippet(base, protocol, probeSampleModel())) },
            style = McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
        ) { Text("复制片段") }
    }
}

/* ---------------- 调用方密钥（gateway.js:202-257，只读版） ---------------- */

@Composable
private fun CallerKeysCard(keys: List<CallerKeySummary>) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("调用方密钥")
            Spacer(Modifier.width(8.dp))
            Text(
                text = "${keys.size} 把",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(6.dp))
        DeepCardBody(
            "生成、轮换、删除与限额在「设置 → 调用方密钥」里操作；本页只列现状，避免两处入口各写一套。",
        )
        if (keys.isEmpty()) {
            Spacer(Modifier.height(8.dp))
            DeepCardBody("还没有调用方密钥。生成一把给 CI 或其它电脑用；本机回环可免密钥。")
            return@DeepCard
        }
        keys.forEach { key ->
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = key.name.ifBlank { key.id },
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = key.masked.ifBlank { "—" },
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = if (key.off) "已停用" else "启用",
                    style = MaterialTheme.typography.labelSmall,
                    color = if (key.off) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
                )
            }
        }
    }
}

/* ---------------- 渠道探测（gateway.js:122-127, 289-301） ---------------- */

@Composable
private fun ChannelProbeCard(report: ChannelProbeReport, probing: Boolean, onProbe: () -> Unit) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("渠道探测")
            Spacer(Modifier.weight(1f))
            Text(
                text = if (report.checkedAt > 0) {
                    "上次 ${probeDateAuto(report.checkedAt)}"
                } else {
                    "未探测"
                },
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(8.dp))
        McaButton(
            onClick = onProbe,
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = !probing,
        ) { Text(if (probing) "探测中…" else "全部探测") }
        if (report.entries.isEmpty()) {
            Spacer(Modifier.height(8.dp))
            DeepCardBody("还没有探测结果。点「全部探测」发一次最小请求；会消耗真实账号额度。")
            return@DeepCard
        }
        report.entries.forEach { entry ->
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = entry.providerId + if (entry.label.isNotBlank()) " · ${entry.label}" else "",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = channelProbeSummary(entry),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (entry.ok) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        MaterialTheme.colorScheme.error
                    },
                    maxLines = 1,
                )
            }
            Text(
                text = channelProbeAgeLabel(entry.at, System.currentTimeMillis() / 1000),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/* ---------------- 版本与探针（gateway.js:129-134） ---------------- */

@Composable
private fun VersionProbeCard(state: ProbeUiState.Ready, onOpenEndpoint: (String) -> Unit) {
    val version = (state.session.version as? ProbeOutcome.Ok)?.value
    DeepCard {
        DeepCardTitle("版本与探针")
        Spacer(Modifier.height(10.dp))
        ProbeFieldRow("版本", version?.version?.ifBlank { "dev" } ?: "—")
        ProbeFieldRow("构建时间", version?.buildTime?.ifBlank { "—" } ?: "—")
        Column(
            modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
        ) {
            Text(
                text = "存活 / 就绪",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                McaButton(
                    onClick = { onOpenEndpoint("/healthz") },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("查看 /healthz") }
                McaButton(
                    onClick = { onOpenEndpoint("/readyz") },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("查看 /readyz") }
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "指标",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.widthIn(min = 96.dp),
            )
            McaButton(
                onClick = { onOpenEndpoint("/metrics") },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("查看 /metrics") }
        }
    }
}

/* ---------------- 本机 Agent 版本（gateway.js:177-200） ---------------- */

@Composable
private fun AgentVersionCard(agents: List<AgentCliInfo>, checkedAtSec: Long, onCopy: (String) -> Unit) {
    if (agents.isEmpty()) {
        DeepCard {
            DeepCardTitle("本机 Agent 版本")
            Spacer(Modifier.height(6.dp))
            DeepCardBody("还没有巡检结果。网关起来后会自动扫描本机 CLI。")
        }
        return
    }
    val installed = agents.count { it.installed }
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("本机 Agent 版本")
            Spacer(Modifier.width(8.dp))
            Text(
                text = "$installed / ${agents.size} 已安装" +
                    if (checkedAtSec > 0) " · 巡检于 ${probeDateAuto(checkedAtSec)}" else "",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        agents.forEach { agent ->
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = agent.name.ifBlank { agent.id },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                    modifier = Modifier.weight(1f),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = if (agent.installed) agent.version.ifBlank { "已安装" } else "未安装",
                    style = MaterialTheme.typography.labelSmall,
                    color = if (agent.installed) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            val cmd = if (agent.installed) agent.updateCmd else agent.installCmd
            if (cmd.isNotBlank()) {
                Spacer(Modifier.height(4.dp))
                McaButton(
                    onClick = { onCopy(cmd) },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text(if (agent.installed) "复制升级命令" else "复制安装命令") }
            }
        }
    }
}

/* ---------------- 排障（桌面无此入口：三块手机端独有，收进一个折叠入口） ---------------- */

@Composable
private fun TroubleshootCard(open: Boolean, onToggle: () -> Unit, status: GatewayStatus) {
    DeepCard {
        DeepCardTitle("排障（移动端独有）")
        Spacer(Modifier.height(6.dp))
        DeepCardBody(
            "桌面网关页没有这三块：自检、最近异常、配置漂移。手机上它们仍然有用（尤其是没有桌面可看的时候），" +
                "所以收进一个入口，不占版面。默认收起。",
        )
        Spacer(Modifier.height(12.dp))
        McaButton(
            onClick = onToggle,
            style = if (open) McaButtonStyle.Primary else McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
        ) { Text(if (open) "收起排障" else "展开排障") }
        if (open) {
            Spacer(Modifier.height(12.dp))
            DiagnoseSection(status = status)
            Spacer(Modifier.height(DeepCardGap))
            RecentFailuresSection(status = status)
            Spacer(Modifier.height(DeepCardGap))
            DriftSection(status = status)
        }
    }
}

/* ---------------- 探针端点抽屉（gateway.js:321-357） ---------------- */

@Composable
private fun ProbeEndpointDialog(path: String, state: ProbeUiState, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val fields = probeEndpointFields(path, state)
    val text = fields.joinToString("\n") { "${it.label}: ${it.value}" }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "探针 · $path",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = probeEndpointStatusText(path, state),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Medium,
                    color = if (probeEndpointBad(path, state)) {
                        MaterialTheme.colorScheme.error
                    } else {
                        MaterialTheme.colorScheme.primary
                    },
                )
                if (fields.isEmpty()) {
                    Text(
                        text = "这次没有拿到该端点的结果。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    fields.forEach { field -> ProbeFieldRow(field.label, field.value) }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
        dismissButton = {
            McaButton(
                onClick = { clipboard.setText(AnnotatedString(text)) },
                style = McaButtonStyle.Outlined,
            ) { Text("复制") }
        },
    )
}

/* ---------------- 纯函数（单测覆盖） ---------------- */

/**
 * 接入地址：优先 `/api/runtime` 的 publicAddr，其次 settings 的 addr，最后 status.addr。
 * 与桌面 `gateway.js:54-57` 同级回退；settings.addr 是 `host:port` 形态，这里补上 scheme。
 */
internal fun probeBaseUrl(status: GatewayStatus, state: ProbeUiState.Ready): String {
    val runtime = (state.session.runtime as? ProbeOutcome.Ok)?.value
    val fromSettings = state.settings?.addr?.takeIf { it.isNotBlank() }?.let { addr ->
        if (addr.startsWith("http://") || addr.startsWith("https://")) addr else "http://$addr"
    }
    return runtime?.publicAddr?.takeIf { it.isNotBlank() }
        ?: fromSettings
        ?: "http://${status.addr}"
}

/**
 * 接入片段。桌面 `gateway.js:63-74` 用"已接入的第一个模型"当示例；
 * 移动端的 provider 摘要不含模型清单，所以用 `provider/model` 占位（桌面在无模型时也这样）。
 */
internal fun probeSnippet(baseUrl: String, protocolId: String, sampleModel: String): String {
    val path = ProbeProtocols.firstOrNull { it.first == protocolId }?.third ?: ""
    val base = baseUrl + path
    return listOf(
        "# OpenAI 兼容",
        "\$env:OPENAI_BASE_URL=\"$base\"",
        "\$env:OPENAI_API_KEY=\"aether\"",
        "",
        "curl $base/chat/completions \\",
        "  -H \"Content-Type: application/json\" \\",
        "  -d '{\"model\":\"$sampleModel\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'",
    ).joinToString("\n")
}

/** 示例模型占位（与桌面 `gateway.js:58-62` 的兜底同值）。 */
internal fun probeSampleModel(): String = "provider/model"

/** 端点抽屉里的字段：复用探针解析结果，不重发请求。 */
internal fun probeEndpointFields(path: String, state: ProbeUiState): List<ProbeField> {
    if (state !is ProbeUiState.Ready) {
        return emptyList()
    }
    return when (path) {
        "/healthz" -> (state.anonymous.healthz as? ProbeOutcome.Ok)?.value?.let(::healthzFields)
        "/readyz" -> (state.session.readyz as? ProbeOutcome.Ok)?.value?.let(::readyzFields)
        "/metrics" -> (state.session.metrics as? ProbeOutcome.Ok)?.value?.let(::metricsFields)
        "/version" -> (state.session.version as? ProbeOutcome.Ok)?.value?.let(::versionFields)
        else -> null
    }.orEmpty()
}

/** 端点徽标文案：就绪/未就绪、正常、失败（失败时带错误码）。 */
internal fun probeEndpointStatusText(path: String, state: ProbeUiState): String {
    if (state !is ProbeUiState.Ready) {
        return "—"
    }
    return when (path) {
        "/healthz" -> outcomeLabel(state.anonymous.healthz) { "HTTP 200" }
        "/readyz" -> when (val outcome = state.session.readyz) {
            is ProbeOutcome.Ok -> if (outcome.value.ready) "HTTP ${outcome.value.httpStatus} 就绪" else "HTTP ${outcome.value.httpStatus} 未就绪"
            is ProbeOutcome.Failed -> "失败 · ${outcome.code}"
        }
        "/metrics" -> outcomeLabel(state.session.metrics) { "HTTP 200" }
        "/version" -> outcomeLabel(state.session.version) { "HTTP 200" }
        else -> "—"
    }
}

/** 该端点是否处于"需要注意"的状态（用于抽屉里的颜色）。 */
internal fun probeEndpointBad(path: String, state: ProbeUiState): Boolean {
    if (state !is ProbeUiState.Ready) {
        return false
    }
    return when (path) {
        "/readyz" -> when (val outcome = state.session.readyz) {
            is ProbeOutcome.Ok -> !outcome.value.ready
            is ProbeOutcome.Failed -> true
        }
        "/healthz" -> state.anonymous.healthz is ProbeOutcome.Failed
        "/metrics" -> state.session.metrics is ProbeOutcome.Failed
        "/version" -> state.session.version is ProbeOutcome.Failed
        else -> false
    }
}

private fun <T> outcomeLabel(outcome: ProbeOutcome<T>, okText: () -> String): String = when (outcome) {
    is ProbeOutcome.Ok -> okText()
    is ProbeOutcome.Failed -> "失败 · ${outcome.code}"
}

/* ---------------- 小 UI 件 ---------------- */

/** 字段行：label 左、value 右；[trailing] 放复制按钮这类行内动作。 */
@Composable
internal fun ProbeFieldRow(label: String, value: String, trailing: (@Composable () -> Unit)? = null) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.widthIn(min = 96.dp),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.weight(1f),
        )
        if (trailing != null) {
            trailing()
        }
    }
}

@Composable
private fun ProbeLoadingCard() {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = "正在探测本机网关…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

internal data class ProbeField(val label: String, val value: String, val emphasis: Boolean = false)

internal fun healthzFields(probe: HealthzProbe): List<ProbeField> = listOf(
    ProbeField(label = "应答", value = probe.status),
    ProbeField(label = "格式", value = if (probe.plainText) "text/plain" else "application/json"),
)

internal fun readyzFields(probe: ReadyzProbe): List<ProbeField> = buildList {
    add(ProbeField(label = "就绪", value = if (probe.ready) "是" else "否"))
    add(ProbeField(label = "账号", value = "${probe.usableAccounts} / ${probe.accounts} 可用"))
    add(ProbeField(label = "渠道", value = "${probe.providersInUse} 在用 · ${probe.providers} 已注册"))
    add(ProbeField(label = "模型", value = probe.models.toString()))
    add(ProbeField(label = "运行", value = probeUptime(probe.uptimeSec)))
    probe.reasons.forEach { reason ->
        add(ProbeField(label = "原因", value = reason, emphasis = true))
    }
}

internal fun versionFields(info: VersionInfo): List<ProbeField> = listOf(
    ProbeField(label = "版本", value = info.version),
    ProbeField(label = "修订", value = info.revision.ifBlank { "未记录" }),
    ProbeField(label = "Go", value = info.go.ifBlank { "未记录" }),
    ProbeField(label = "构建时间", value = info.buildTime.ifBlank { "未记录" }),
    ProbeField(label = "时间来源", value = info.buildTimeSource.ifBlank { "unknown" }),
    ProbeField(label = "运行", value = probeUptime(info.uptimeSec)),
)

internal fun metricsFields(digest: MetricsDigest): List<ProbeField> = buildList {
    add(ProbeField(label = "非空行", value = "${digest.lineCount} 行 · ${digest.charCount} 字符"))
    digest.preview.forEach { line ->
        add(ProbeField(label = "预览", value = line))
    }
}

internal fun probeUptime(seconds: Int): String {
    if (seconds <= 0) {
        return "—"
    }
    val hours = seconds / 3_600
    val minutes = (seconds % 3_600) / 60
    val rest = seconds % 60
    return when {
        hours > 0 -> "${hours} 小时 ${minutes} 分"
        minutes > 0 -> "${minutes} 分 ${rest} 秒"
        else -> "${rest} 秒"
    }
}

private val probeClockFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss", Locale.US)

private fun probeInstant(ms: Long) = Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault())

private val probeDateFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy/MM/dd HH:mm", Locale.US)

/** 秒级时间戳 → `yyyy/MM/dd HH:mm`（桌面 `format.js:25-30` 同形状）。 */
internal fun probeDate(epochSeconds: Long): String {
    if (epochSeconds <= 0) {
        return "—"
    }
    return Instant.ofEpochSecond(epochSeconds).atZone(ZoneId.systemDefault()).format(probeDateFormat)
}

/**
 * 时间戳展示，秒/毫秒都吃：`/api/channels/status` 的 `checkedAt` 与
 * `/api/agents/cli` 的 `checked_at` 在服务端口径并不一致，这里按量级判断，
 * 免得把一个 1.7e12 当成秒显示成五万年后。
 */
internal fun probeDateAuto(epoch: Long): String {
    if (epoch <= 0) {
        return "—"
    }
    val instant = if (epoch > 100_000_000_000L) {
        Instant.ofEpochMilli(epoch)
    } else {
        Instant.ofEpochSecond(epoch)
    }
    return instant.atZone(ZoneId.systemDefault()).format(probeDateFormat)
}
