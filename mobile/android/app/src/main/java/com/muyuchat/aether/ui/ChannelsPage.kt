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
                        text = "渠道",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "本机渠道与账号概览",
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
                        item { ProviderList(filtered) }
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
private fun ProviderList(providers: List<ProviderSummary>) {
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
                    ProviderRow(provider)
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
private fun ProviderRow(provider: ProviderSummary) {
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
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 12.dp),
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
