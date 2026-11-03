package com.muyuchat.aether.ui

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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

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
        verticalArrangement = Arrangement.spacedBy(14.dp),
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
                    enabled = state is ProbeUiState.Ready || state is ProbeUiState.Failed,
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
        when (state) {
            ProbeUiState.Idle, ProbeUiState.Loading -> item { ProbeLoadingCard() }
            is ProbeUiState.Failed -> item {
                if (state.code == "gateway_not_running") {
                    ProbeStateCard(
                        title = "网关未启动",
                        detail = "启动本地网关后即可运行探针检查；停止后本页不会保留上一次的探测结果。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    ProbeErrorCard(state.code, state.message, onRefresh)
                }
            }
            is ProbeUiState.Ready -> {
                val anonymousRows = anonymousProbeRows(state.anonymous)
                val sessionRows = sessionProbeRows(state.session)
                item { ProbeSampledLine(state.sampledAtMs) }
                item {
                    ProbeSectionHeader(
                        title = "匿名端点",
                        note = "不带 Authorization：/healthz 由移动运行时在进入网关前接管，" +
                            "/api/health 在移动鉴权里被显式放行。给它们带会话凭据只会把凭据扩散到本可匿名的地方。",
                    )
                }
                items(anonymousRows.size) { index -> ProbeRowCard(anonymousRows[index]) }
                item {
                    ProbeSectionHeader(
                        title = "会话端点（需会话凭据）",
                        note = "移动运行时把 /readyz、/version、/metrics 纳入管理面（回环不等于可信），" +
                            "桌面端这三个是匿名端点；/api/runtime 同样在管理面之下。" +
                            "本页按移动运行时的真实面分组，所以这四项都带 Authorization: Bearer 会话凭据访问。",
                    )
                }
                items(sessionRows.size) { index -> ProbeRowCard(sessionRows[index]) }
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

@Composable
private fun ProbeRowCard(row: ProbeRow) {
    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(14.dp),
        ) {
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
            maxLines = 1,
            modifier = Modifier.width(56.dp),
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
private fun ProbeSectionHeader(title: String, note: String) {
    Column {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.height(2.dp))
        Text(
            text = note,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
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
private fun ProbeStateCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.primaryContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f),
            )
            if (actionLabel != null && onAction != null) {
                Spacer(Modifier.height(14.dp))
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
            }
        }
    }
}

@Composable
private fun ProbeLoadingCard() {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
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

@Composable
private fun ProbeErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "探测失败",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.85f),
            )
            Text(
                text = "错误码：$code",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.75f),
            )
            Spacer(Modifier.height(14.dp))
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) { Text("重试") }
        }
    }
}

private val probeClockFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss", Locale.US)

private fun probeInstant(ms: Long) = Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault())
