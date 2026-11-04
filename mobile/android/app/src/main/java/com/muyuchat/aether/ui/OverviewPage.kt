package com.muyuchat.aether.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
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
import com.muyuchat.aether.runtime.OverviewExtras
import com.muyuchat.aether.runtime.OverviewHealthRow
import com.muyuchat.aether.runtime.OverviewLinksState
import com.muyuchat.aether.runtime.OverviewProviderLink
import com.muyuchat.aether.runtime.OverviewQuotaRow
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.formatUsageCost
import com.muyuchat.aether.runtime.formatUsageCount
import com.muyuchat.aether.runtime.overviewEndpoints
import com.muyuchat.aether.runtime.overviewOnboardingSteps
import com.muyuchat.aether.runtime.stateLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private val TightGap = 8.dp
private val StatusDotSize = 8.dp

/**
 * 概览页（对标桌面 `internal/web/ui2/js/views/overview.js`）。
 *
 * 桌面区块顺序（`overview.js:166-172` 的注释与 append 顺序）：
 * KPI 四格 → 三步接入（仅账号库为空时）→ 聚合链路 → 额度余量 + 渠道健康 → 接入卡。
 * 手机端按同一顺序落位，两处差异都有理由：
 *  1. 页首保留「网关运行卡」：桌面网关是常驻服务、概览没有启停按钮；手机端必须能启停，
 *     这是手机端唯一必需的额外卡片（入口地址与复制也在它里面，对应桌面接入卡的入口行）。
 *  2. 桌面接入卡右侧的「调用方密钥 / 客户端」两列，在手机上分别由设置页与客户端页承担，
 *     这里不重复第三个入口。
 *
 * 桌面没有的两块已删除：「最近一次请求」与「入口协议」（端点行本来就在聚合链路里）。
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
    // 桌面概览没有「最近一次请求」卡；参数保留只为不改 GatewayHome 的调用点（写集外）。
    latestRequest: String? = null,
    modifier: Modifier = Modifier,
    viewModel: OverviewViewModel = viewModel(),
) {
    // 与 GatewayHome 共用同一个 ViewModelStoreOwner，因此这里拿到的是同一个 OverviewViewModel。
    val links by viewModel.links.collectAsStateWithLifecycle()
    val extras by viewModel.extras.collectAsStateWithLifecycle()

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
                // 品牌行只在概览页出现（用户要求：其它页不要 app 名那一行）。
                MobileHeader(state.status, onSearch = onSearch, showBrand = true)
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
        // ① KPI 四格（桌面 overview.js:157-164）
        item { KpiCard(stats) }
        // ② 三步接入：桌面只在账号库为空时出现（overview.js:167）
        if (links?.providersLoaded == true && links?.totalAccounts == 0) {
            item { OnboardingCard(status = state.status, links = links) }
        }
        // ③ 聚合链路（桌面 overview.js:169/225）
        item {
            AggregationCard(
                status = state.status,
                links = links,
                updatedAtMs = stats?.updatedAtMs ?: 0L,
            )
        }
        // ④ 额度余量 + 渠道健康（桌面 overview.js:172 的 ov-grid；手机纵向堆叠）
        item { QuotaCard(extras) }
        item { HealthCard(extras) }
        item {
            Text(
                text = "前台服务可能被系统或电池策略停止；此页状态即真实状态。" +
                    "调用方密钥在「设置」里管理，桌面客户端接线见「客户端」页。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun OverviewTitle(status: GatewayStatus) {
    // 概览页保留品牌行，标题不再叠副标题（"此手机独立运行…"与下方卡片重复），
    // 标题右侧继续挂网关状态点。
    Row(verticalAlignment = Alignment.CenterVertically) {
        DeepPageTitle(title = "概览", modifier = Modifier.weight(1f))
        StatusPill(status)
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
    DeepCard {
        // 标题/说明占整行、动作单独一行靠右：
        // 1.3× 下按钮与说明并排会把说明列压到半宽，"本机应用可通过回环地址接入" 会折出
        // 孤立单字（实测 "…接" + "入"），既难读也违反"无竖排单字"。
        Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
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
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.End,
        ) {
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

/** KPI 四格：可用账号 / 今日请求 / 今日 Token / 会话（桌面 `overview.js:157-164`）。
 *
 * 1.0× 两列、1.3× 单列——这是任务允许的唯一两档差异（列数随字号变化），
 * 否则 `1.5K`、`$0.0123` 这类值会在两列里被省略号吃掉。
 */
@Composable
private fun KpiCard(stats: OverviewStats?) {
    val cells = listOf(
        Triple("可用账号", stats?.accounts ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(0))),
        Triple("今日请求", stats?.calls ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(1))),
        Triple("今日 Token", stats?.tokens ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(2))),
        Triple("会话", stats?.sessions ?: "—", statNoteOrBlank(stats?.captions?.getOrNull(3))),
    )
    val columns = if (isLargeFontScale()) 1 else 2
    DeepCard {
        DeepCardTitle("今日")
        cells.chunked(columns).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(DeepCardPadding)) {
                row.forEach { (label, value, note) ->
                    StatCell(
                        label = label,
                        value = value,
                        note = note,
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

/**
 * 一格数字：说明 → 大数字（单位降级）→ 只有缺失时才写一行注解。
 *
 * 值和它的单位用**同一段 AnnotatedString**：早先是三个并排的 `Text` + `Alignment.Bottom`，
 * 小号前缀（`$`）在大号数字旁会沉到基线以下，看着像两块错位的东西。同一段文字里用
 * SpanStyle 把单位降级，基线与行高由排版引擎保证，1.0×/1.3× 都不会错位。
 */
@Composable
private fun StatCell(label: String, value: String, note: String, modifier: Modifier = Modifier) {
    val parts = statFocusParts(value)
    val bigSize = MaterialTheme.typography.headlineSmall.fontSize
    val smallSize = MaterialTheme.typography.labelSmall.fontSize
    val onSurface = MaterialTheme.colorScheme.onSurface
    val onSurfaceVariant = MaterialTheme.colorScheme.onSurfaceVariant
    Column(
        modifier = modifier,
        verticalArrangement = Arrangement.spacedBy(TightGap),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = buildAnnotatedString {
                if (parts.prefix.isNotEmpty()) {
                    withStyle(SpanStyle(fontSize = smallSize, color = onSurfaceVariant)) {
                        append(parts.prefix)
                    }
                }
                withStyle(SpanStyle(fontSize = bigSize, fontWeight = FontWeight.Bold, color = onSurface)) {
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
                    MaterialTheme.colorScheme.error
                } else {
                    onSurfaceVariant
                },
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * 三步接入（桌面 `overview.js:176-188` 的标题/说明原样）。
 *
 * 每步的完成状态都来自真实状态——网关运行、渠道账号数、今日请求数。
 * 桌面只在账号库为空时显示（`overview.js:167`），这里由调用方按同一条件决定是否渲染。
 * 桌面每步带一个跳转按钮；手机的导航回调在 `GatewayHome`（写集外），因此这里只留状态词。
 */
@Composable
private fun OnboardingCard(status: GatewayStatus, links: OverviewLinksState?) {
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
            Text(
                text = "首次使用 · $doneCount/3 已完成",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(12.dp))
        steps.forEachIndexed { index, step ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.Top,
                horizontalArrangement = Arrangement.spacedBy(TightGap),
            ) {
                Text(
                    text = "${index + 1}",
                    modifier = Modifier.width(18.dp),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Column(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text(
                        text = step.title,
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = step.description,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = step.status,
                        style = MaterialTheme.typography.labelSmall,
                        color = if (step.done) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
            }
            if (index != steps.lastIndex) {
                Spacer(Modifier.height(12.dp))
            }
        }
    }
}

/**
 * 聚合链路（桌面 `overview.js:212-456`）：入站 3 个端点 → 本机网关 → 上游渠道。
 * 渠道行只展示 `/api/providers` 与 `/api/usage` 已核实字段；读不到时明说，不补 0 冒充。
 */
@Composable
private fun AggregationCard(status: GatewayStatus, links: OverviewLinksState?, updatedAtMs: Long) {
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
                Text(
                    text = trailing,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        // 桌面把「更新于 HH:MM」并进这张卡的头部（overview.js:168-171）
        val updated = formatOverviewUpdatedAt(updatedAtMs)
        if (updated.isNotEmpty()) {
            Text(
                text = updated,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
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
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(TightGap))
        Column(verticalArrangement = Arrangement.spacedBy(TightGap)) {
            overviewEndpoints(baseUrl).forEach { endpoint ->
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(TightGap),
                ) {
                    Text(
                        text = endpoint.label,
                        modifier = Modifier.width(112.dp),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
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
        Spacer(Modifier.height(12.dp))
        RowHairline()
        Spacer(Modifier.height(12.dp))
        Text(
            text = "上游渠道",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(TightGap))
        when {
            !links.providersLoaded -> Text(
                text = "读不到渠道列表：重新进入概览会再试一次。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            links.providers.isEmpty() -> Text(
                text = "还没有可路由的渠道，先接入一个。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                links.providers.forEach { provider ->
                    ProviderLinkRow(provider = provider, usageLoaded = links.usageLoaded)
                }
            }
        }
        Spacer(Modifier.height(12.dp))
        Text(
            text = "端点随监听端口自动适配 · 客户端只需一个 base_url",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ProviderLinkRow(provider: OverviewProviderLink, usageLoaded: Boolean) {
    val state = when {
        provider.off -> "已停用"
        provider.readyCount > 0 -> "可用"
        else -> "无可用账号"
    }
    val stateColor = when {
        provider.off -> MaterialTheme.colorScheme.onSurfaceVariant
        provider.readyCount > 0 -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.error
    }
    val usage = if (usageLoaded) {
        "今日 ${formatUsageCount(provider.calls)} 次 · 失败 ${formatUsageCount(provider.errors)} · " +
            formatUsageCost(provider.cost)
    } else {
        "用量读不到"
    }
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(TightGap),
    ) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(4.dp),
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
                text = "${provider.accountCount} 账号 · ${provider.readyCount} 可用",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = usage,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            text = state,
            style = MaterialTheme.typography.labelSmall,
            color = stateColor,
        )
    }
}

/** 额度余量（桌面 `overview.js:563-609`）：最紧的排最前，前 6 条。 */
@Composable
private fun QuotaCard(extras: OverviewExtras?) {
    DeepCard {
        DeepCardTitle("额度余量")
        val rows = extras?.quotas
        val error = extras?.quotaError
        when {
            error != null -> Text(
                text = "读不到额度窗口：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            rows == null -> Text(
                text = "正在读取额度…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            rows.isEmpty() -> {
                // 桌面同一句（overview.js:588）
                Text(
                    text = "暂无可读的额度窗口",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    text = "额度读数还没回来，稍后自动重试。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            else -> rows.forEach { row -> QuotaRowView(row) }
        }
    }
}

@Composable
private fun QuotaRowView(row: OverviewQuotaRow) {
    val tone = when {
        row.remainingPct <= 10.0 -> MaterialTheme.colorScheme.error
        row.remainingPct <= 40.0 -> MaterialTheme.colorScheme.tertiary
        else -> MaterialTheme.colorScheme.primary
    }
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(TightGap),
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                text = row.name,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = row.resetNote,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            text = quotaRemainingText(row.remainingPct),
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = tone,
            textAlign = TextAlign.End,
        )
    }
}

/** 渠道健康（桌面 `overview.js:546-561`）：最近一次探测，前 8 条。 */
@Composable
private fun HealthCard(extras: OverviewExtras?) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            DeepCardTitle("渠道健康")
            Spacer(Modifier.weight(1f))
            Text(
                text = healthCheckedLabel(extras?.healthCheckedAt ?: 0L),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        val rows = extras?.health
        val error = extras?.healthError
        when {
            error != null -> Text(
                text = "读不到探测结果：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            rows == null -> Text(
                text = "正在读取探测结果…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            rows.isEmpty() -> {
                // 桌面同一句（overview.js:550）：说明 + 去哪里探测
                Text(
                    text = "还没有探测结果",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    text = "在「网关探针」页发起一次探测后，这里会显示每个渠道的最近一次结果。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            else -> rows.forEach { row -> HealthRowView(row) }
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
        Text(
            text = row.name,
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = row.statusLabel,
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = if (row.ok) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
        )
        if (row.detail.isNotBlank()) {
            Text(
                text = row.detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/* ------------------------------- 纯函数 ------------------------------- */

/** 「更新于 HH:MM」；时间戳为 0 时返回空串（不编一个时间）。 */
internal fun formatOverviewUpdatedAt(updatedAtMs: Long): String {
    if (updatedAtMs <= 0L) {
        return ""
    }
    val formatter = DateTimeFormatter.ofPattern("HH:mm", Locale.US)
    val text = Instant.ofEpochMilli(updatedAtMs).atZone(ZoneId.systemDefault()).format(formatter)
    return "更新于 $text"
}

/** 探测时间标签：0 = 未探测（桌面 `overview.js:548` 的「刚测 / 未探测」）。 */
internal fun healthCheckedLabel(checkedAtMs: Long): String =
    if (checkedAtMs <= 0L) "未探测" else "刚测"

/** 剩余百分比文案：整数时不留小数点，其余保留一位。 */
internal fun quotaRemainingText(remainingPct: Double): String {
    val safe = if (remainingPct.isFinite()) remainingPct else 0.0
    val rounded = kotlin.math.round(safe * 10.0) / 10.0
    return if (rounded % 1.0 == 0.0) {
        "${rounded.toInt()}%"
    } else {
        String.format(Locale.US, "%.1f%%", rounded)
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

/**
 * `latestRequestLine()` 自带"最近一次："前缀。
 *
 * 概览页已不再渲染「最近一次请求」卡（桌面 `overview.js` 没有这一块），
 * 这里保留格式化函数是因为 `GatewayHome` 仍在消费 `viewModel.latest`；
 * 恢复卡片时直接复用即可。
 */
internal fun latestRequestBody(line: String): String = line.removePrefix("最近一次：")
