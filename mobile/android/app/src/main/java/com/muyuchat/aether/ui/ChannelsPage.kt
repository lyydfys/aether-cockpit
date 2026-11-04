package com.muyuchat.aether.ui

import androidx.compose.foundation.background
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.channelToggleConfirmTitle
import com.muyuchat.core.designsystem.McaDialog
import androidx.compose.runtime.remember
import com.muyuchat.aether.runtime.channelToggleLabel
import com.muyuchat.aether.runtime.channelToggleConfirmBody
import com.muyuchat.aether.runtime.providerModelsPreview
import com.muyuchat.aether.runtime.providerModelsSummary
import com.muyuchat.aether.runtime.ChannelProbe
import com.muyuchat.aether.runtime.channelProbeAgeLabel
import com.muyuchat.aether.runtime.channelProbeSummary
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.parseBrandColor
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

@Composable
internal fun ChannelsPage(
    status: GatewayStatus,
    state: ChannelsUiState,
    bundleState: BundlePreviewUiState,
    bundleImportState: BundleImportUiState,
    qoderLoginState: QoderLoginUiState,
    onAction: (ChannelsAction) -> Unit,
    onOpenOverview: () -> Unit,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onPickBundle: () -> Unit,
    onImportBundle: () -> Unit,
    onDismissBundle: () -> Unit,
    probes: Map<String, ChannelProbe> = emptyMap(),
    probing: String? = null,
    onTestProvider: (String) -> Unit = {},
    toggling: String? = null,
    models: Map<String, ProviderModelsUiState> = emptyMap(),
    onLoadModels: (String) -> Unit = {},
    onDismissModels: (String) -> Unit = {},
    onToggleProvider: (String, Boolean) -> Unit = { _, _ -> },
    modifier: Modifier = Modifier,
) {
    var query by rememberSaveable { mutableStateOf("") }
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        // 与桌面端同名的页面标题：桌面导航写「账号与额度」，
                        // 副标题也照抄桌面端的原句，避免两端对同一页有两种说法。
                        text = "账号与额度",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "账号、模型与余量，一眼看到底",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新渠道",
                    onClick = { onAction(ChannelsAction.Refresh) },
                    enabled = state is ChannelsUiState.Ready || state is ChannelsUiState.Failed,
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
        item {
            QoderLoginSection(
                state = qoderLoginState,
                onStart = onStartQoderLogin,
                onOpenUrl = onOpenQoderLogin,
                onCancel = onCancelQoderLogin,
                onDismiss = onDismissQoderLogin,
            )
        }
        item {
            BundlePreviewSection(
                state = bundleState,
                importState = bundleImportState,
                onPick = onPickBundle,
                onImport = onImportBundle,
                onDismiss = onDismissBundle,
            )
        }
        // 账号包的另一半：把本机账号导回桌面。放在导入卡片旁边，口径成对出现。
        item { AccountExportSection(status = status) }
        when (state) {
            ChannelsUiState.Idle -> item {
                StateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机渠道与账号。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            ChannelsUiState.Loading -> item { LoadingCard() }
            is ChannelsUiState.Failed -> item {
                ErrorCard(code = state.code, message = state.message) {
                    onAction(ChannelsAction.Refresh)
                }
            }
            is ChannelsUiState.Ready -> {
                if (state.providers.isEmpty()) {
                    item {
                        StateCard(
                            title = "尚未接入任何渠道",
                            detail = "可先在上方导入加密账号包；登录将在后续卡片接入。",
                        )
                    }
                } else {
                    val filtered = filterProviders(state.providers, query)
                    item { SummaryCard(state.providers) }
                    item {
                        SearchField(query = query, onQueryChange = { query = it }, placeholder = "搜索渠道或账号")
                    }
                    if (filtered.isEmpty()) {
                        item {
                            StateCard(
                                title = "没有匹配的渠道",
                                detail = "换个关键词试试。",
                            )
                        }
                    } else {
                        item {
                        ProviderList(
                            providers = filtered,
                            probes = probes,
                            probing = probing,
                            onTestProvider = onTestProvider,
                            toggling = toggling,
                            models = models,
                            onLoadModels = onLoadModels,
                            onDismissModels = onDismissModels,
                            onToggleProvider = onToggleProvider,
                        )
                    }
                    }
                }
            }
        }
        item {
            Text(
                text = "渠道与账号列表只读，来自本机管理接口，不展示凭据；账号包导入已可用，登录将在后续卡片接入。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun SummaryCard(providers: List<ProviderSummary>) {
    val readyAccounts = providers.sumOf { it.readyCount }
    val accounts = providers.sumOf { it.accountCount }
    Surface(
        shape = McaShape.Card,
        color = MaterialTheme.colorScheme.primaryContainer,
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 18.dp, vertical = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = "${providers.size} 个渠道",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onPrimaryContainer,
                )
                Text(
                    text = "来自本机账号池",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
                )
            }
            Column(horizontalAlignment = Alignment.End) {
                Text(
                    text = "$readyAccounts / $accounts",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onPrimaryContainer,
                )
                Text(
                    text = "可用账号",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f),
                )
            }
        }
    }
}

@Composable
private fun ProviderList(
    providers: List<ProviderSummary>,
    probes: Map<String, ChannelProbe>,
    probing: String?,
    onTestProvider: (String) -> Unit,
    toggling: String?,
    onToggleProvider: (String, Boolean) -> Unit,
    models: Map<String, ProviderModelsUiState>,
    onLoadModels: (String) -> Unit,
    onDismissModels: (String) -> Unit,
) {
    val nowMs = System.currentTimeMillis()
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 6.dp, vertical = 6.dp),
        ) {
            providers.forEach { provider ->
                Column {
                    ProviderRow(
                        provider = provider,
                        probe = probes[provider.id],
                        probing = probing == provider.id,
                        onTest = { onTestProvider(provider.id) },
                        toggling = toggling == provider.id,
                        onToggle = { enable -> onToggleProvider(provider.id, enable) },
                        modelsState = models[provider.id],
                        onLoadModels = { onLoadModels(provider.id) },
                        onDismissModels = { onDismissModels(provider.id) },
                    )
                    provider.accounts.forEach { account ->
                        AccountRow(account = account, nowMs = nowMs)
                    }
                }
            }
        }
    }
}

@Composable
private fun AccountRow(account: AccountSummary, nowMs: Long) {
    val status = accountStatusText(account, nowMs)
    val resting = account.enabled && account.restWhy.isNotEmpty() && account.restUntilMs > nowMs
    val invalid = account.enabled && !resting && !account.quota?.error.isNullOrEmpty()
    val accent = when {
        !account.enabled -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.35f)
        invalid -> MaterialTheme.colorScheme.error
        resting -> MaterialTheme.colorScheme.tertiary
        account.active -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.primary.copy(alpha = 0.5f)
    }
    val statusColor = when {
        !account.enabled -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.7f)
        invalid -> MaterialTheme.colorScheme.error
        resting -> MaterialTheme.colorScheme.tertiary
        account.active -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    val badge = accountQuotaBadge(account)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 54.dp, end = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Box(
            Modifier
                .padding(top = 7.dp)
                .size(6.dp)
                .clip(CircleShape)
                .background(accent),
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = accountDisplayName(account),
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(2.dp))
            Text(
                text = status,
                style = MaterialTheme.typography.bodySmall,
                color = statusColor,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (badge.isNotEmpty()) {
            Spacer(Modifier.width(8.dp))
            Text(
                text = badge,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.widthIn(max = 110.dp),
            )
        }
    }
}

@Composable
private fun ProviderRow(
    provider: ProviderSummary,
    probe: ChannelProbe?,
    probing: Boolean,
    onTest: () -> Unit,
    toggling: Boolean = false,
    onToggle: (Boolean) -> Unit = {},
    modelsState: ProviderModelsUiState? = null,
    onLoadModels: () -> Unit = {},
    onDismissModels: () -> Unit = {},
) {
    var confirmToggle by remember { mutableStateOf(false) }
    val accent = when {
        provider.off -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.5f)
        provider.ready -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f)
    }
    val brand: Color = parseBrandColor(provider.brandColor)?.let { Color(it) }
        ?: MaterialTheme.colorScheme.onSurfaceVariant
    val trailing = when {
        provider.off -> "已停用"
        provider.accountCount == 0 -> "未添加账号"
        else -> "${provider.readyCount}/${provider.accountCount} 可用"
    }
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 12.dp)) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(
            modifier = Modifier.size(32.dp),
            shape = McaShape.Badge,
            color = brand.copy(alpha = 0.10f),
            border = McaHairline.stroke(),
        ) {
            Box(contentAlignment = Alignment.Center) {
                Icon(
                    painter = painterResource(R.drawable.ic_ws_channels),
                    contentDescription = null,
                    tint = brand,
                    modifier = Modifier.size(McaIconSize.M),
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(7.dp)
                        .clip(CircleShape)
                        .background(accent),
                )
                Spacer(Modifier.width(6.dp))
                Text(
                    text = provider.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (provider.description.isNotEmpty()) {
                Spacer(Modifier.height(2.dp))
                Text(
                    text = provider.description,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Text(
            text = trailing,
            style = MaterialTheme.typography.labelSmall,
            color = if (provider.ready && !provider.off) {
                MaterialTheme.colorScheme.primary
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            maxLines = 1,
        )
    }
        // 启停：写操作，先弹确认；按钮文案随**当前状态**变化（off=true → 显示「启用」）。
        Row(verticalAlignment = Alignment.CenterVertically) {
            McaButton(
                onClick = { confirmToggle = true },
                style = McaButtonStyle.Text,
                enabled = !toggling,
            ) {
                Text(
                    when {
                        toggling -> "切换中…"
                        provider.off -> "启用此渠道"
                        else -> "停用此渠道"
                    },
                )
            }
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = onLoadModels,
                style = McaButtonStyle.Text,
                enabled = modelsState !is ProviderModelsUiState.Loading,
            ) { Text(if (modelsState is ProviderModelsUiState.Loading) "读取模型…" else "查看模型") }
        }
        // 探测行单独占一行：1.3× 字号下跟按钮挤在同一行会把「还没有探测记录」压成竖排单字。
        ProbeLine(probe = probe, probing = probing, onTest = onTest)
        // 模型清单结果：只列条数与名字（超长折叠），不展示任何凭据。
        val openModels = modelsState
        if (openModels != null) {
            McaDialog(
                onDismissRequest = { onDismissModels() },
                title = { Text("${provider.name} 的模型", fontWeight = FontWeight.Bold) },
                text = {
                    when (openModels) {
                        ProviderModelsUiState.Loading -> Text("读取中…")
                        is ProviderModelsUiState.Failed -> Text(
                            text = "${openModels.message}（错误码：${openModels.code}）",
                            color = MaterialTheme.colorScheme.error,
                        )
                        is ProviderModelsUiState.Ready -> {
                            val preview = providerModelsPreview(openModels.models.models)
                            Column {
                                Text(
                                    text = providerModelsSummary(openModels.models),
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurface,
                                )
                                Spacer(Modifier.height(6.dp))
                                preview.shown.forEach { model ->
                                    Text(
                                        text = "· $model",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                if (preview.more > 0) {
                                    Spacer(Modifier.height(4.dp))
                                    Text(
                                        text = "还有 ${preview.more} 个未显示",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }
                    }
                },
                confirmButton = {
                    McaButton(onClick = { onDismissModels() }, style = McaButtonStyle.Outlined) { Text("知道了") }
                },
            )
        }
        if (confirmToggle) {
            McaDialog(
                onDismissRequest = { confirmToggle = false },
                title = {
                    Text(
                        text = channelToggleConfirmTitle(provider.off, provider.name),
                        fontWeight = FontWeight.Bold,
                    )
                },
                text = {
                    Text(
                        text = channelToggleConfirmBody(provider.off),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                },
                confirmButton = {
                    McaButton(
                        onClick = {
                            confirmToggle = false
                            onToggle(provider.off)
                        },
                        style = McaButtonStyle.Primary,
                    ) { Text(channelToggleLabel(provider.off)) }
                },
                dismissButton = {
                    McaButton(onClick = { confirmToggle = false }, style = McaButtonStyle.Outlined) { Text("取消") }
                },
            )
        }
    }
}

/**
 * 渠道探测结论行。
 *
 * 口径：这是**一次探测的结果**（"刚刚测试"），不是"已连接"这种持续状态；
 * 失败时把服务端原文贴出来，不写"测试失败"四个字了事。
 */
@Composable
private fun ProbeLine(
    probe: ChannelProbe?,
    probing: Boolean,
    onTest: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Spacer(Modifier.height(8.dp))
    Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            when {
                probing -> Text(
                    text = "正在探测（服务端用账号池里的账号发起一次真实请求）…",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                probe == null -> Text(
                    text = "还没有探测记录",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> {
                    Text(
                        text = channelProbeSummary(probe),
                        style = MaterialTheme.typography.labelSmall,
                        color = if (probe.ok) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.error
                        },
                    )
                    if (probe.at > 0) {
                        Text(
                            text = channelProbeAgeLabel(probe.at, System.currentTimeMillis() / 1000),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
        McaButton(
            onClick = onTest,
            style = McaButtonStyle.Outlined,
            enabled = !probing,
        ) { Text(if (probing) "探测中…" else "测试") }
    }
}

@Composable
private fun StateCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Surface(
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.primaryContainer,
    ) {
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
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) {
                    Text(actionLabel)
                }
            }
        }
    }
}

@Composable
private fun LoadingCard() {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
    ) {
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
                text = "正在读取本机渠道…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.errorContainer,
    ) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "读取渠道失败",
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
            Spacer(Modifier.height(4.dp))
            Text(
                text = "错误码：$code",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.75f),
            )
            Spacer(Modifier.height(14.dp))
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) {
                Text("重试")
            }
        }
    }
}
