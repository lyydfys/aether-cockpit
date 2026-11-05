package com.muyuchat.aether.ui

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ONBOARDING_STEP_ONBOARD
import com.muyuchat.aether.runtime.ONBOARDING_STEP_SETTINGS
import com.muyuchat.aether.runtime.OverviewExtras
import com.muyuchat.aether.runtime.OverviewHealthRow
import com.muyuchat.aether.runtime.OverviewLinksState
import com.muyuchat.aether.runtime.OverviewProviderLink
import com.muyuchat.aether.runtime.OverviewQuotaRow
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.formatUsageCost
import com.muyuchat.aether.runtime.formatUsageCount
import com.muyuchat.aether.runtime.onboardingStepActionLabel
import com.muyuchat.aether.runtime.overviewEndpoints
import com.muyuchat.aether.runtime.overviewOnboardingSteps
import com.muyuchat.aether.runtime.providerDetail
import com.muyuchat.aether.runtime.stateLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private val TightGap = 8.dp

/**
 * 旗舰级概览页（对标桌面 `overview.js` 视觉精工）。
 *
 * 区块顺序：网关主控舱 → 今日 Bento 四宫格 KPI → 三步接入（仅空库时）→ 聚合链路拓扑 → 额度余量 → 渠道健康。
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
    onGotoSettings: () -> Unit = {},
    onGotoOnboard: () -> Unit = {},
    onGotoProbe: () -> Unit = {},
    /** 局域网共享开关（另一位同事接网络层；这里只放位置与回调）。 */
    lanSharing: Boolean = false,
    onToggleLanSharing: ((Boolean) -> Unit)? = null,
    /** 局域网切换失败原因（由 repository 提供；成功时自动清空）。 */
    lanError: String? = null,
    modifier: Modifier = Modifier,
    viewModel: OverviewViewModel = viewModel(),
) {
    val links by viewModel.links.collectAsStateWithLifecycle()
    val extras by viewModel.extras.collectAsStateWithLifecycle()

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item(key = "ov_header") {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                MobileHeader(state.status, onSearch = onSearch, showBrand = true)
                OverviewTitle(state.status)
            }
        }
        item(key = "ov_gateway") {
            // 观测：确认网关卡是否真的进了组合（只打日志，不改渲染）。
            LaunchedEffect(state.status.state) {
                android.util.Log.d("AetherOverview", "gateway card rendered, state=${state.status.state}")
            }
            GatewayCard(
                status = state.status,
                url = url,
                copied = copied,
                notice = state.notice,
                onPrimaryAction = onPrimaryAction,
                onCopy = onCopy,
                lanSharing = lanSharing,
                onToggleLanSharing = onToggleLanSharing,
                lanError = lanError,
            )
        }
        item(key = "ov_kpi") { KpiCard(stats) }
        if (links?.providersLoaded == true && links?.totalAccounts == 0) {
            item(key = "ov_onboarding") {
                OnboardingCard(
                    status = state.status,
                    links = links,
                    onGotoSettings = onGotoSettings,
                    onGotoOnboard = onGotoOnboard,
                    onGotoProbe = onGotoProbe,
                )
            }
        }
        item(key = "ov_aggregation") {
            AggregationCard(
                status = state.status,
                links = links,
                updatedAtMs = stats?.updatedAtMs ?: 0L,
                quotas = extras?.quotas.orEmpty(),
            )
        }
        item(key = "ov_quota") { QuotaCard(extras) }
        item(key = "ov_health") { HealthCard(extras) }
        item(key = "ov_footnote") {
            Text(
                text = "前台服务可能被系统或电池策略停止；此页状态即真实状态。" +
                    "调用方密钥在「设置」里管理，桌面客户端接线见「客户端」页。",
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.textTertiary(),
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
    }
}

@Composable
private fun OverviewTitle(status: GatewayStatus) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        DeepPageTitle(title = "概览", modifier = Modifier.weight(1f))
        StatusPill(status)
    }
}

@Composable
private fun StatusPill(status: GatewayStatus) {
    val kind = when (status.state) {
        GatewayState.RUNNING -> AetherChipKind.SUCCESS
        GatewayState.STARTING, GatewayState.STOPPING -> AetherChipKind.WARNING
        GatewayState.FAILED -> AetherChipKind.DANGER
        GatewayState.STOPPED -> AetherChipKind.NEUTRAL
    }
    AetherStatusChip(
        text = stateLabel(status),
        kind = kind,
        showDot = true,
        pulsingDot = status.running,
    )
}

@Composable
private fun RowHairline() {
    Box(
        Modifier
            .fillMaxWidth()
            .height(McaHairline.Width)
            .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.35f)),
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
    lanSharing: Boolean = false,
    onToggleLanSharing: ((Boolean) -> Unit)? = null,
    lanError: String? = null,
) {
    val headline = gatewayStateHeadline(status)
    val accent = statusAccent(status)
    DeepCard {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AetherIdentityDisc(
                iconRes = R.drawable.ic_ws_probe,
                tone = when {
                    status.running -> AetherIdentityTone.GREEN
                    status.state == GatewayState.FAILED -> AetherIdentityTone.PINK
                    else -> AetherIdentityTone.BLUE
                },
                size = 38.dp,
                glyphSize = 20.dp,
                shape = RoundedCornerShape(12.dp),
            )
            Spacer(Modifier.width(12.dp))
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Text(
                    text = headline,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = if (status.state == GatewayState.FAILED) {
                        AetherColors.danger()
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
            Spacer(Modifier.width(8.dp))
            // 主按钮上移到状态行右侧：运行中是次要样式「停止网关」，未启动是主样式「启动网关」。
            McaButton(
                onClick = onPrimaryAction,
                style = if (status.running) McaButtonStyle.Outlined else McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
                enabled = !status.busy,
            ) { Text(gatewayActionLabel(status), maxLines = 1) }
        }
        Spacer(Modifier.height(6.dp))
        // 地址舱：局域网开启时主位换成本机 IP 的 /v1，回环地址降到下面一行小字。
        val lanAddress = status.lanV1Urls.firstOrNull().orEmpty()
        val primaryUrl = if (status.lanEnabled) lanAddress else url
        val pendingLanAddress = status.lanEnabled && lanAddress.isEmpty()
        val secondaryUrl = if (status.lanEnabled && url.isNotEmpty()) url else ""
        // 局域网开启时 `status.addr` 会变成 `0.0.0.0:<port>` —— 那不是能给用户用的地址。
        // 次要行固定写回环、端口从 addr 里取；取不到就不显示（宁可少一行，也不显示 0.0.0.0）。
        val loopbackUrl = if (secondaryUrl.isNotEmpty()) {
            val port = secondaryUrl.substringAfterLast(':', "").takeWhile { it.isDigit() }
            if (port.isNotEmpty()) "http://127.0.0.1:$port/v1" else ""
        } else {
            ""
        }
        AetherSubSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(12.dp),
            contentPadding = PaddingValues(horizontal = 14.dp, vertical = 8.dp),
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = if (status.lanEnabled) {
                                "OpenAI-compatible · 局域网"
                            } else {
                                "OpenAI-compatible · 仅本机"
                            },
                            style = MaterialTheme.typography.labelSmall,
                            color = AetherColors.textTertiary(),
                        )
                        if (copied) {
                            Spacer(Modifier.width(8.dp))
                            AetherStatusChip(
                                text = "已复制",
                                kind = AetherChipKind.SUCCESS,
                            )
                        }
                    }
                    // 注意：这里**不是**"正在获取"——`lanUrls` 是网关启动后一次性算出来的，拿不到就说明没连 Wi-Fi/热点（`status.lanEnabled && lanAddress.isEmpty()`），继续写"正在获取"会让用户一直等下去。
        val placeholder = if (pendingLanAddress) "未获取到局域网地址（请确认手机已连上 Wi-Fi 或热点）" else "启动后显示本机地址"
                    Text(
                        text = primaryUrl.ifEmpty { placeholder },
                        style = if (primaryUrl.isEmpty()) {
                            MaterialTheme.typography.bodySmall
                        } else {
                            MaterialTheme.typography.titleSmall
                        },
                        fontWeight = if (primaryUrl.isEmpty()) FontWeight.Normal else FontWeight.Bold,
                        fontFamily = if (primaryUrl.isEmpty()) FontFamily.Default else FontFamily.Monospace,
                        color = if (primaryUrl.isEmpty()) {
                            AetherColors.textTertiary()
                        } else {
                            MaterialTheme.colorScheme.onSurface
                        },
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    if (loopbackUrl.isNotEmpty()) {
                        Text(
                            text = loopbackUrl,
                            style = MaterialTheme.typography.labelSmall,
                            fontFamily = FontFamily.Monospace,
                            color = AetherColors.textTertiary(),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
                Spacer(Modifier.width(8.dp))
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_copy,
                    contentDescription = "复制本机地址",
                    onClick = onCopy,
                    enabled = primaryUrl.isNotEmpty(),
                    active = copied,
                )
            }
        }
        if (notice.isNotEmpty()) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.danger(),
            )
        }
        if (!lanError.isNullOrBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = "局域网切换失败：$lanError",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.danger(),
            )
        }
        Spacer(Modifier.height(6.dp))
        // 局域网共享：开关切换的是**主监听绑定**（127.0.0.1 ↔ 0.0.0.0）。
        // 开启后对话接口(/v1)与管理接口(/api)同时对同网络设备开放，
        // 管理接口仍然要求 App 内生成的会话凭据（Authorization 头），所以要先二次确认。
        var confirmLan by remember { mutableStateOf(false) }
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = "局域网共享",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    text = "局域网内设备可访问对话接口（/v1）与管理接口；管理接口仍需本机 App 内的会话凭据",
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                )
            }
            Spacer(Modifier.width(8.dp))
            McaSwitch(
                checked = lanSharing,
                onCheckedChange = { next ->
                    // 只有"打开"需要确认；关闭随时可以。
                    if (next) confirmLan = true else onToggleLanSharing?.invoke(false)
                },
                enabled = onToggleLanSharing != null && !status.busy,
                contentDescription = "局域网共享",
            )
        }
        if (confirmLan) {
            McaDialog(
                onDismissRequest = { confirmLan = false },
                title = {
                    Text("开启局域网共享？", fontWeight = FontWeight.Bold)
                },
                text = {
                    Text(
                        "开启后，同一网络内的设备可以访问对话接口（/v1）与管理接口（/api）。" +
                            "管理接口仍然需要本机 App 内的会话凭据，但攻击面确实变大了。" +
                            "\n\n请确认当前网络可信（公共 Wi-Fi、咖啡厅网络不要开启）。",
                        style = MaterialTheme.typography.bodySmall,
                    )
                },
                confirmButton = {
                    McaButton(
                        onClick = {
                            confirmLan = false
                            onToggleLanSharing?.invoke(true)
                        },
                        style = McaButtonStyle.Primary,
                    ) { Text("确认开启") }
                },
                dismissButton = {
                    McaButton(
                        onClick = { confirmLan = false },
                        style = McaButtonStyle.Outlined,
                    ) { Text("取消") }
                },
            )
        }
        Spacer(Modifier.height(6.dp))
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(TightGap),
        ) {
            Icon(
                painter = painterResource(R.drawable.ic_ws_2fa),
                contentDescription = null,
                tint = accent,
                modifier = Modifier.size(16.dp),
            )
            Column(Modifier.weight(1f)) {
                Text(
                    text = if (lanSharing) {
                        // 打开时不再说"默认关闭"，否则自相矛盾。
                        "已开启：同网络设备可访问对话接口与管理接口（管理接口需会话凭据）。"
                    } else {
                        "只接受此手机上的本地连接。局域网共享默认关闭。"
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                )
                if (lanSharing) {
                    val v1 = status.lanV1Urls.firstOrNull()
                    val base = status.lanUrls.firstOrNull()
                    Spacer(Modifier.height(2.dp))
                    Text(
                        text = if (v1 != null) {
                            "局域网地址：$v1" + if (base != null) "（管理接口：$base）" else ""
                        } else {
                            "未获取到局域网地址（请确认手机已连上 Wi-Fi 或热点）"
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = accent,
                    )
                }
            }
        }
    }
}

/**
 * 今日 Bento 四宫格 KPI。
 */
@Composable
private fun KpiCard(stats: OverviewStats?) {
    val cells = listOf(
        Triple("可用账号", stats?.accounts ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(0))),
        Triple("今日请求", stats?.calls ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(1))),
        Triple("今日 Token", stats?.tokens ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(2))),
        Triple("会话", stats?.sessions ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(3))),
    )
    val tones = listOf(
        AetherIdentityTone.BLUE,
        AetherIdentityTone.GREEN,
        AetherIdentityTone.PURPLE,
        AetherIdentityTone.AMBER,
    )
    val columns = if (isLargeFontScale()) 1 else 2
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("今日")
            Spacer(Modifier.weight(1f))
            val updated = formatOverviewUpdatedAt(stats?.updatedAtMs ?: 0L)
            if (updated.isNotEmpty()) {
                Text(
                    text = updated,
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            cells.mapIndexed { idx, triple -> idx to triple }.chunked(columns).forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    row.forEach { (idx, item) ->
                        val (label, value, note) = item
                        StatCell(
                            label = label,
                            value = value,
                            note = note,
                            tone = tones[idx % tones.size],
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun StatCell(
    label: String,
    value: String,
    note: String,
    tone: AetherIdentityTone,
    modifier: Modifier = Modifier,
) {
    val parts = statFocusParts(value)
    val bigSize = MaterialTheme.typography.headlineSmall.fontSize
    val smallSize = MaterialTheme.typography.labelSmall.fontSize
    val onSurface = MaterialTheme.colorScheme.onSurface
    val onSurfaceVariant = MaterialTheme.colorScheme.onSurfaceVariant
    val tonePair = AetherColors.identityPair(tone)

    AetherSubSurface(
        modifier = modifier,
        shape = RoundedCornerShape(14.dp),
        contentPadding = PaddingValues(horizontal = 14.dp, vertical = 12.dp),
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(6.dp)
                        .clip(CircleShape)
                        .background(tonePair.content, CircleShape),
                )
                Spacer(Modifier.width(6.dp))
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelMedium,
                    color = onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                text = buildAnnotatedString {
                    if (parts.prefix.isNotEmpty()) {
                        withStyle(SpanStyle(fontSize = smallSize, color = onSurfaceVariant)) {
                            append(parts.prefix)
                        }
                    }
                    withStyle(
                        SpanStyle(
                            fontSize = bigSize,
                            fontWeight = FontWeight.Bold,
                            color = onSurface,
                            fontFeatureSettings = "tnum",
                        ),
                    ) {
                        append(parts.number)
                    }
                    if (parts.suffix.isNotEmpty()) {
                        withStyle(SpanStyle(fontSize = smallSize, color = onSurfaceVariant)) {
                            append(parts.suffix)
                        }
                    }
                },
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (note.isNotEmpty()) {
                Text(
                    text = note,
                    style = MaterialTheme.typography.labelSmall,
                    color = if (note.contains("读不到")) {
                        AetherColors.danger()
                    } else {
                        AetherColors.textTertiary()
                    },
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun OnboardingCard(
    status: GatewayStatus,
    links: OverviewLinksState?,
    onGotoSettings: () -> Unit = {},
    onGotoOnboard: () -> Unit = {},
    onGotoProbe: () -> Unit = {},
) {
    val steps = overviewOnboardingSteps(
        running = status.running,
        linksLoaded = links != null,
        accountsKnown = links?.providersLoaded == true,
        accountCount = links?.totalAccounts,
        usageKnown = links?.usageLoaded == true,
        calls = links?.calls ?: 0L,
    )
    val doneCount = steps.count { it.done }
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("三步接入")
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = "首次使用 · $doneCount/3 已完成",
                kind = if (doneCount == 3) AetherChipKind.SUCCESS else AetherChipKind.ACCENT,
            )
        }
        Spacer(Modifier.height(14.dp))
        steps.forEachIndexed { index, step ->
            val stepPair = AetherColors.identityPair(
                if (step.done) AetherIdentityTone.GREEN else AetherIdentityTone.BLUE,
            )
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.Top,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Box(
                    modifier = Modifier
                        .size(26.dp)
                        .clip(CircleShape)
                        .background(stepPair.container, CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = "${index + 1}",
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Bold,
                        color = stepPair.content,
                    )
                }
                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = step.title,
                            style = MaterialTheme.typography.titleSmall,
                            fontWeight = FontWeight.SemiBold,
                            color = MaterialTheme.colorScheme.onSurface,
                            modifier = Modifier.weight(1f),
                        )
                        AetherStatusChip(
                            text = step.status,
                            kind = if (step.done) AetherChipKind.SUCCESS else AetherChipKind.NEUTRAL,
                        )
                    }
                    Text(
                        text = step.description,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(4.dp))
                    AetherPillButton(
                        text = onboardingStepActionLabel(step.id),
                        accent = step.id == ONBOARDING_STEP_ONBOARD,
                        onClick = {
                            when (step.id) {
                                ONBOARDING_STEP_SETTINGS -> onGotoSettings()
                                ONBOARDING_STEP_ONBOARD -> onGotoOnboard()
                                else -> onGotoProbe()
                            }
                        },
                    )
                }
            }
            if (index != steps.lastIndex) {
                Spacer(Modifier.height(14.dp))
                RowHairline()
                Spacer(Modifier.height(14.dp))
            }
        }
    }
}

@Composable
private fun AggregationCard(
    status: GatewayStatus,
    links: OverviewLinksState?,
    updatedAtMs: Long,
    quotas: List<OverviewQuotaRow> = emptyList(),
) {
    var selectedLink by remember { mutableStateOf<OverviewProviderLink?>(null) }
    val baseUrl = if (status.running && status.addr.isNotBlank()) {
        "http://${status.addr}"
    } else {
        ""
    }
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("聚合链路")
            Spacer(Modifier.weight(1f))
            val trailing = if (status.running && links?.providersLoaded == true) {
                "${links.providers.size} 个渠道 → 3 个端点"
            } else {
                null
            }
            if (trailing != null) {
                AetherStatusChip(
                    text = trailing,
                    kind = AetherChipKind.NEUTRAL,
                )
            }
        }
        val updated = formatOverviewUpdatedAt(updatedAtMs)
        if (updated.isNotEmpty()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = updated,
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
        }
        Spacer(Modifier.height(12.dp))
        if (!status.running) {
            Text(
                text = "网关未启动：启动后这里会显示从入站到上游的链路。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        if (links == null) {
            Text(
                text = "正在读取链路…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        Text(
            text = "入站端点",
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.SemiBold,
            color = AetherColors.textTertiary(),
        )
        Spacer(Modifier.height(4.dp))
        AetherSubSurface(
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(12.dp),
            contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                overviewEndpoints(baseUrl).forEach { endpoint ->
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(TightGap),
                    ) {
                        Text(
                            text = endpoint.label,
                            modifier = Modifier.width(104.dp),
                            style = MaterialTheme.typography.labelMedium,
                            fontWeight = FontWeight.Medium,
                            color = MaterialTheme.colorScheme.onSurface,
                            // 1.3× 下标签只允许占一行（省略号），绝不折成竖排
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            text = endpoint.url.ifEmpty { "启动后显示" },
                            modifier = Modifier.weight(1f),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            fontFamily = if (endpoint.url.isEmpty()) FontFamily.Default else FontFamily.Monospace,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
        Spacer(Modifier.height(10.dp))
        Text(
            text = "上游渠道",
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.SemiBold,
            color = AetherColors.textTertiary(),
        )
        Spacer(Modifier.height(6.dp))
        when {
            !links.providersLoaded -> Text(
                text = "读不到渠道列表：重新进入概览会再试一次。",
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.danger(),
            )
            links.providers.isEmpty() -> Text(
                text = "还没有可路由的渠道，先接入一个。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                links.providers.forEachIndexed { idx, provider ->
                    ProviderLinkRow(
                        provider = provider,
                        usageLoaded = links.usageLoaded,
                        onClick = { selectedLink = provider },
                    )
                    if (idx < links.providers.lastIndex) {
                        RowHairline()
                    }
                }
            }
        }
        Spacer(Modifier.height(8.dp))
        Text(
            text = "端点随监听端口自动适配 · 客户端只需一个 base_url",
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
    }

    selectedLink?.let { link ->
        val detail = providerDetail(link, quotas)
        McaDialog(
            onDismissRequest = { selectedLink = null },
            title = {
                Text(
                    text = "渠道详情",
                    fontWeight = FontWeight.Bold,
                )
            },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    detailLine("端点", detail.endpoint, null)
                    detailLine("渠道", detail.channel, null)
                    detailLine("最低剩余", detail.lowest, detail.lowestNote)
                }
            },
            confirmButton = {
                McaButton(
                    onClick = { selectedLink = null },
                    style = McaButtonStyle.Outlined,
                ) { Text("关闭") }
            },
        )
    }
}

@Composable
private fun detailLine(label: String, value: String, note: String?) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
        )
        if (!note.isNullOrBlank()) {
            Text(
                text = note,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ProviderLinkRow(
    provider: OverviewProviderLink,
    usageLoaded: Boolean,
    onClick: () -> Unit = {},
) {
    val state = when {
        provider.off -> "已停用"
        provider.readyCount > 0 -> "可用"
        else -> "无可用账号"
    }
    val chipKind = when {
        provider.off -> AetherChipKind.NEUTRAL
        provider.readyCount > 0 -> AetherChipKind.SUCCESS
        else -> AetherChipKind.DANGER
    }
    val usage = if (usageLoaded) {
        "今日 ${formatUsageCount(provider.calls)} 次 · 失败 ${formatUsageCount(provider.errors)} · " +
            formatUsageCost(provider.cost)
    } else {
        "用量读不到"
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .aetherPressable(scaleDown = 0.985f, onClick = onClick)
            .padding(vertical = 4.dp)
            .semantics { contentDescription = "查看 ${provider.name} 详情" },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        ProviderBrandPlate(
            providerId = provider.id,
            providerName = provider.name,
            plateSize = 36.dp,
            glyphSize = 20.dp,
            online = !provider.off && provider.readyCount > 0,
        )
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Text(
                text = provider.name,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "${provider.accountCount} 账号 · ${provider.readyCount} 可用 · $usage",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        AetherStatusChip(
            text = state,
            kind = chipKind,
        )
        AetherChevron()
    }
}

@Composable
private fun QuotaCard(extras: OverviewExtras?) {
    DeepCard {
        DeepCardTitle("额度余量")
        Spacer(Modifier.height(10.dp))
        val rows = extras?.quotas
        val error = extras?.quotaError
        when {
            error != null -> Text(
                text = "读不到额度窗口：$error",
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.danger(),
            )
            rows == null -> Text(
                text = "正在读取额度…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            rows.isEmpty() -> {
                Text(
                    text = "暂无可读的额度窗口",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.height(2.dp))
                Text(
                    text = "额度读数还没回来，稍后自动重试。",
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                )
            }
            else -> Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                rows.forEach { row -> QuotaRowView(row) }
            }
        }
    }
}

@Composable
private fun QuotaRowView(row: OverviewQuotaRow) {
    val barColor = when {
        row.remainingPct <= 10.0 -> AetherColors.danger()
        row.remainingPct <= 40.0 -> AetherColors.warning()
        else -> AetherColors.success()
    }
    val fraction by animateFloatAsState(
        targetValue = (row.remainingPct.toFloat() / 100f).coerceIn(0f, 1f),
        animationSpec = tween(durationMillis = 360, easing = FastOutSlowInEasing),
        label = "ovQuotaFraction",
    )
    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(TightGap),
        ) {
            Text(
                text = row.name,
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = row.resetNote,
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.width(4.dp))
            Text(
                text = quotaRemainingText(row.remainingPct),
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = barColor,
                textAlign = TextAlign.End,
            )
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(5.dp)
                .clip(CircleShape)
                .background(AetherColors.pillTrack(), CircleShape),
        ) {
            Box(
                modifier = Modifier
                    .fillMaxWidth(fraction)
                    .fillMaxHeight()
                    .clip(CircleShape)
                    .background(barColor, CircleShape),
            )
        }
    }
}

@Composable
private fun HealthCard(extras: OverviewExtras?) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("渠道健康")
            Spacer(Modifier.weight(1f))
            AetherStatusChip(
                text = healthCheckedLabel(extras?.healthCheckedAt ?: 0L),
                kind = AetherChipKind.NEUTRAL,
            )
        }
        Spacer(Modifier.height(10.dp))
        val rows = extras?.health
        val error = extras?.healthError
        when {
            error != null -> Text(
                text = "读不到探测结果：$error",
                style = MaterialTheme.typography.bodySmall,
                color = AetherColors.danger(),
            )
            rows == null -> Text(
                text = "正在读取探测结果…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            rows.isEmpty() -> {
                Text(
                    text = "还没有探测结果",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.height(2.dp))
                Text(
                    text = "在「网关探针」页发起一次探测后，这里会显示每个渠道的最近一次结果。",
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
                )
            }
            else -> Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                rows.forEach { row -> HealthRowView(row) }
            }
        }
    }
}

@Composable
private fun HealthRowView(row: OverviewHealthRow) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(TightGap),
    ) {
        AetherStatusDot(
            color = if (row.ok) AetherColors.success() else AetherColors.danger(),
            pulsing = false,
            dotSize = 7.dp,
        )
        Text(
            text = row.name,
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.bodySmall,
            fontWeight = FontWeight.Medium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (row.detail.isNotBlank()) {
            Text(
                text = row.detail,
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        AetherStatusChip(
            text = row.statusLabel,
            kind = if (row.ok) AetherChipKind.SUCCESS else AetherChipKind.DANGER,
        )
    }
}

/* ------------------------------- 纯函数 ------------------------------- */

internal fun formatOverviewUpdatedAt(updatedAtMs: Long): String {
    if (updatedAtMs <= 0L) {
        return ""
    }
    val formatter = DateTimeFormatter.ofPattern("HH:mm", Locale.US)
    val text = Instant.ofEpochMilli(updatedAtMs).atZone(ZoneId.systemDefault()).format(formatter)
    return "更新于 $text"
}

internal fun healthCheckedLabel(checkedAtMs: Long): String =
    if (checkedAtMs <= 0L) "未探测" else "刚测"

internal fun quotaRemainingText(remainingPct: Double): String {
    val safe = if (remainingPct.isFinite()) remainingPct else 0.0
    val rounded = kotlin.math.round(safe * 10.0) / 10.0
    return if (rounded % 1.0 == 0.0) {
        "${rounded.toInt()}%"
    } else {
        String.format(Locale.US, "%.1f%%", rounded)
    }
}

internal fun gatewayStateHeadline(status: GatewayStatus): String = when {
    status.running -> "网关正在运行"
    status.state == GatewayState.FAILED -> "启动失败"
    else -> "网关未启动"
}

internal fun gatewayStateDetail(status: GatewayStatus): String = when {
    status.running && status.lanEnabled ->
        "本机与同网络设备都可接入；管理接口需要 App 内会话凭据"
    status.running -> "本机应用可通过回环地址接入"
    status.state == GatewayState.FAILED ->
        "错误码：${status.errorCode.ifBlank { "unknown" }}（可在设置里改首选端口，或改回系统分配后重试）"
    else -> "启动后按「局域网共享」设置决定监听地址"
}

internal fun gatewayActionLabel(status: GatewayStatus): String =
    if (status.running) "停止网关" else "启动网关"

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

internal fun statNoteOrBlank(caption: String?): String {
    val text = caption?.trim().orEmpty()
    return if (text.contains("读不到")) text else ""
}

internal fun latestRequestBody(line: String): String = line.removePrefix("最近一次：")
