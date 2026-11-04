package com.muyuchat.aether.ui

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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AnonymousProbeReport
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.HealthProbe
import com.muyuchat.aether.runtime.HealthzProbe
import com.muyuchat.aether.runtime.MetricsDigest
import com.muyuchat.aether.runtime.ProbeOutcome
import com.muyuchat.aether.runtime.ReadyzProbe
import com.muyuchat.aether.runtime.RuntimeInfo
import com.muyuchat.aether.runtime.SessionProbeReport
import com.muyuchat.aether.runtime.VersionInfo
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
 * 网关探针页。
 *
 * 版面顺序：页头（返回 + 标题 + 重新探测 + 采样时刻）→ 探针结果（匿名端点 / 会话端点，
 * 各一张卡，卡内 16dp、端点之间 16dp）→ 自检 → 最近异常 → 配置漂移 → 口径说明。
 *
 * 三个诊断区块（自检 / 最近异常 / 配置漂移）各自在自己的文件里画卡与标题，本页只负责
 * 把它们排成 24dp 的区块间距：列表本身是 12dp，所以给每块补 12dp 上内距。
 * 本页不套第二层卡：端点明细直接排在区块卡里，不再一条端点一张卡。
 */

/** 字段行：label 左，value 右；[emphasis] 用于 reasons 这种要报警的文案。 */
internal data class ProbeField(
    val label: String,
    val value: String,
    val emphasis: Boolean = false,
)

/**
 * 一个端点一格的渲染模型。已经把 [ProbeOutcome] 折平：页面不再关心泛型，
 * 只按 [httpStatus]/[attention] 决定徽标颜色，按 [fields] 决定明细。
 */
internal data class ProbeRow(
    val path: String,
    val httpStatus: Int,
    val badge: String,
    val attention: Boolean,
    val code: String,
    val message: String,
    val fields: List<ProbeField>,
)

@Composable
internal fun GatewayProbePage(
    status: GatewayStatus,
    state: ProbeUiState,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                ProbeHeadline(onBack = onBack, onRefresh = onRefresh, enabled = state !is ProbeUiState.Loading)
                if (state is ProbeUiState.Ready) {
                    ProbeSampledLine(state.sampledAtMs)
                }
            }
        }
        when (state) {
            ProbeUiState.Idle, ProbeUiState.Loading -> item { ProbeLoadingCard() }
            is ProbeUiState.Failed -> item {
                if (state.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "探针读的是本机回环上的 /healthz、/api/health、/readyz 等端点；" +
                            "网关没起来时这些端点没有服务可应答。先启动网关，再回本页重新探测。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "探测失败",
                        detail = "${state.message}（错误码：${state.code}）。这次没有拿到任何端点结果，" +
                            "所以下面不显示上一次的旧数据。重试一次，仍失败就看「自检」区块。",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = onRefresh,
                    )
                }
            }
            is ProbeUiState.Ready -> {
                item {
                    ProbeSectionCard(
                        title = "匿名端点",
                        note = "不带 Authorization：/healthz 由移动运行时在进入网关前接管，" +
                            "/api/health 在移动鉴权里被显式放行。给它们带会话凭据只会把凭据扩散到本可匿名的地方。",
                        rows = anonymousProbeRows(state.anonymous),
                    )
                }
                item {
                    ProbeSectionCard(
                        title = "会话端点（需会话凭据）",
                        note = "移动运行时把 /readyz、/version、/metrics 纳入管理面（回环不等于可信），" +
                            "桌面端这三个是匿名端点；/api/runtime 同样在管理面之下。" +
                            "本页按移动运行时的真实面分组，所以这四项都带 Authorization: Bearer 会话凭据访问。",
                        rows = sessionProbeRows(state.session),
                    )
                }
            }
        }
        // 自检 / 最近异常 / 配置漂移：三块都是独立区块，彼此留 24dp（列表 12 + 上内距 12）。
        item {
            DiagnoseSection(
                status = status,
                modifier = Modifier.padding(top = DeepBlockGap - DeepCardGap),
            )
        }
        item {
            RecentFailuresSection(
                status = status,
                modifier = Modifier.padding(top = DeepBlockGap - DeepCardGap),
            )
        }
        item {
            DriftSection(
                status = status,
                modifier = Modifier.padding(top = DeepBlockGap - DeepCardGap),
            )
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
}

/** 页头：返回 + 标题/副标题 + 重新探测。三段的左右内边距由页框的 20dp 统一给。 */
@Composable
private fun ProbeHeadline(onBack: () -> Unit, onRefresh: () -> Unit, enabled: Boolean) {
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
                text = "探针",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = "网关探针工作区 · 只读",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        McaIconButton(
            contentDescription = "重新探测",
            onClick = onRefresh,
            enabled = enabled,
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

/**
 * 一组端点的卡：标题（titleMedium + Bold）+ 右对齐的端点计数 + 口径说明 + 端点明细。
 *
 * 端点之间用 16dp 垂直节奏；标题与首个端点也隔 16dp，所以卡内不会出现「标题贴着数据」。
 */
@Composable
private fun ProbeSectionCard(title: String, note: String, rows: List<ProbeRow>) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.weight(1f)) { DeepCardTitle(title) }
            Text(
                text = probeRowsSummary(rows),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(4.dp))
        DeepCardBody(note)
        Spacer(Modifier.height(DeepRowVertical))
        rows.forEachIndexed { index, row ->
            if (index > 0) {
                Spacer(Modifier.height(DeepRowVertical))
            }
            ProbeRowBlock(row)
        }
    }
}

/** 端点计数：只用真实的 [ProbeRow.attention] 判定「需注意」，不估算、不上色。 */
internal fun probeRowsSummary(rows: List<ProbeRow>): String {
    if (rows.isEmpty()) {
        return "0 个端点"
    }
    val attention = rows.count { it.attention }
    return if (attention == 0) {
        "${rows.size} 个端点"
    } else {
        "${rows.size} 个端点 · $attention 项需注意"
    }
}

/** 匿名组两格：移动运行时里不带凭据就能读的端点。失败只占自己那一格。 */
internal fun anonymousProbeRows(report: AnonymousProbeReport): List<ProbeRow> = listOf(
    when (val outcome = report.healthz) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/healthz",
            httpStatus = 200,
            badge = "正常",
            attention = false,
            code = "",
            message = "",
            fields = healthzFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/healthz", outcome)
    },
    when (val outcome = report.health) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/api/health",
            httpStatus = 200,
            badge = if (outcome.value.status == "ok") "正常" else "异常",
            attention = outcome.value.status != "ok",
            code = "",
            message = "",
            fields = healthFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/api/health", outcome)
    },
)

/** 会话组四格：移动运行时里必须带 Bearer 的端点，与匿名组完全独立的一盏灯。 */
internal fun sessionProbeRows(report: SessionProbeReport): List<ProbeRow> = listOf(
    when (val outcome = report.readyz) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/readyz",
            httpStatus = outcome.value.httpStatus,
            badge = if (outcome.value.ready) "就绪" else "未就绪",
            attention = !outcome.value.ready,
            code = "",
            message = "",
            fields = readyzFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/readyz", outcome)
    },
    when (val outcome = report.version) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/version",
            httpStatus = 200,
            badge = "正常",
            attention = false,
            code = "",
            message = "",
            fields = versionFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/version", outcome)
    },
    when (val outcome = report.metrics) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/metrics",
            httpStatus = 200,
            badge = "正常",
            attention = false,
            code = "",
            message = "",
            fields = metricsFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/metrics", outcome)
    },
    when (val outcome = report.runtime) {
        is ProbeOutcome.Ok -> ProbeRow(
            path = "/api/runtime",
            httpStatus = 200,
            badge = "正常",
            attention = false,
            code = "",
            message = "",
            fields = runtimeFields(outcome.value),
        )
        is ProbeOutcome.Failed -> probeFailedRow("/api/runtime", outcome)
    },
)

private fun probeFailedRow(path: String, failed: ProbeOutcome.Failed): ProbeRow = ProbeRow(
    path = path,
    httpStatus = 0,
    badge = "失败",
    attention = true,
    code = failed.code,
    message = failed.message,
    fields = emptyList(),
)

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

internal fun healthFields(health: HealthProbe): List<ProbeField> = listOf(
    ProbeField(label = "状态", value = health.status),
    ProbeField(label = "版本", value = health.version.ifBlank { "未记录" }),
    ProbeField(label = "修订", value = health.revision.ifBlank { "未记录" }),
    ProbeField(label = "渠道", value = health.providers.toString()),
    ProbeField(label = "账号", value = health.accounts.toString()),
    ProbeField(label = "运行", value = probeUptime(health.uptimeSec)),
)

internal fun runtimeFields(info: RuntimeInfo): List<ProbeField> = listOf(
    ProbeField(label = "Go", value = info.goVersion.ifBlank { "未记录" }),
    ProbeField(label = "协程", value = info.goroutines.toString()),
    ProbeField(label = "堆内存", value = "${info.heapMb} MB"),
    ProbeField(label = "状态记录", value = info.stateRecords.toString()),
    ProbeField(label = "模型", value = info.models.toString()),
    ProbeField(label = "TUN", value = if (info.tun) "已检测到" else "未检测到"),
    ProbeField(label = "接入地址", value = info.publicAddr.ifBlank { "未上报" }),
    ProbeField(label = "运行", value = probeUptime(info.uptimeSec)),
)

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

/** 单个端点：路径 + HTTP 状态 + 徽标一行，明细在下方；失败只占自己这一块。 */
@Composable
private fun ProbeRowBlock(row: ProbeRow) {
    Column(Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "GET ${row.path}",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            if (row.httpStatus > 0) {
                Text(
                    text = "HTTP ${row.httpStatus}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.width(8.dp))
            }
            Text(
                text = row.badge,
                style = MaterialTheme.typography.labelSmall,
                color = if (row.attention) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary,
            )
        }
        if (row.httpStatus == 0) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = row.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            Text(
                text = "错误码：${row.code}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error.copy(alpha = 0.75f),
            )
        } else {
            row.fields.forEach { field -> ProbeFieldRow(field) }
        }
    }
}

@Composable
private fun ProbeFieldRow(field: ProbeField) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 6.dp),
    ) {
        Text(
            text = field.label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            // 不写死宽度也不限行：1.3x 字体下「构建时间」需要 ≈57dp，固定 56dp + maxLines=1
            // 会把字硬裁掉。用 widthIn 当最小对齐宽度，放不下就换行。
            modifier = Modifier.widthIn(min = 56.dp),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = field.value,
            style = MaterialTheme.typography.bodyMedium,
            color = if (field.emphasis) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurface
            },
            modifier = Modifier.weight(1f),
        )
    }
}

@Composable
private fun ProbeSampledLine(sampledAtMs: Long) {
    Text(
        text = "本次探测：${probeClockFormat.format(probeInstant(sampledAtMs))}",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
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
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

private val probeClockFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss", Locale.US)

private fun probeInstant(ms: Long) = Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault())
