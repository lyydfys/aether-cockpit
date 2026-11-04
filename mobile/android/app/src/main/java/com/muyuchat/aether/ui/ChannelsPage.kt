package com.muyuchat.aether.ui

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
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
import com.muyuchat.aether.runtime.AccountCheckin
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.AccountQuotaWindow
import com.muyuchat.aether.runtime.ChannelProbe
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.QUOTA_UNSUPPORTED_COPY
import com.muyuchat.aether.runtime.relativeTimeLabel
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.channelProbeAgeLabel
import com.muyuchat.aether.runtime.channelProbeSummary
import com.muyuchat.aether.runtime.channelToggleConfirmBody
import com.muyuchat.aether.runtime.channelToggleConfirmTitle
import com.muyuchat.aether.runtime.channelToggleLabel
import com.muyuchat.aether.runtime.providerModelsPreview
import com.muyuchat.aether.runtime.providerModelsSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaIcons
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import kotlin.math.roundToInt

/** 卡片上一屏最多铺三个额度窗口：再多就该去桌面端看，手机不堆到看不清。 */
private const val MAX_QUOTA_WINDOWS = 3

/** 从未读过额度时的值；`read_at` 为 0 / 缺失 / 脏值都归到这一句，绝不编时间。 */
internal const val QUOTA_READ_NEVER_COPY = "从未读过"

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
    var filter by rememberSaveable { mutableStateOf(AccountFilter.ALL) }
    var selecting by rememberSaveable { mutableStateOf(false) }
    // 选中项存成 List（Bundle 可保存）；N 很小，查表用 contains 足够。
    var selectedIds by rememberSaveable { mutableStateOf(listOf<String>()) }
    val ready = state as? ChannelsUiState.Ready
    val batchState = ready?.batch ?: BatchUiState.Idle
    val batchRunning = batchState is BatchUiState.Running
    val accounts = ready?.accounts.orEmpty()
    val filtered = filterAccountRows(accounts, filter, query)
    val visibleSelected = visibleSelection(selectedIds, filtered)
    val checkinTargets = batchCheckinTargets(filtered, visibleSelected)
    val allSelected = allVisibleSelected(visibleSelected, filtered)

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { MobileHeader(status) }
        if (selecting) {
            item {
                SelectionBar(
                    selectedCount = visibleSelected.size,
                    totalVisible = filtered.size,
                    allSelected = allSelected,
                    onToggleAll = {
                        selectedIds = if (allSelected) {
                            selectedIds - filtered.map { it.accountId }.toSet()
                        } else {
                            (selectedIds + filtered.map { it.accountId }).distinct()
                        }
                    },
                    onCancel = {
                        selecting = false
                        selectedIds = emptyList()
                    },
                )
            }
        } else {
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
                        contentDescription = "刷新账号与额度",
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
        }

        // ——— 主体：账号卡片优先 ———
        when (state) {
            ChannelsUiState.Idle -> item {
                StateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机账号与额度。",
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
                item { AccountsHeadline(state.providers, state.accounts) }
                if (state.accounts.isEmpty()) {
                    item {
                        StateCard(
                            title = "还没有账号",
                            detail = "导入加密账号包，或在下方「接入与迁移」里用 Qoder 登录；账号与额度会显示在这里。",
                            actionLabel = "选择账号包",
                            onAction = onPickBundle,
                        )
                    }
                } else {
                    item {
                        AccountFilterChips(
                            selected = filter,
                            onSelect = { filter = it },
                            enabled = !batchRunning,
                        )
                    }
                    item {
                        SearchField(query = query, onQueryChange = { query = it }, placeholder = "搜索账号或渠道")
                    }
                    if (selecting) {
                        item {
                            BatchActionRow(
                                selectedCount = visibleSelected.size,
                                checkinCount = checkinTargets.size,
                                running = batchRunning,
                                onRefresh = { onAction(ChannelsAction.BatchRefresh(visibleSelected)) },
                                onEnable = { onAction(ChannelsAction.BatchSetDisabled(visibleSelected, disabled = false)) },
                                onDisable = { onAction(ChannelsAction.BatchSetDisabled(visibleSelected, disabled = true)) },
                                onCheckin = { onAction(ChannelsAction.BatchCheckin(checkinTargets)) },
                            )
                        }
                    }
                    if (batchState !is BatchUiState.Idle) {
                        item {
                            BatchResultCard(
                                state = batchState,
                                onDismiss = { onAction(ChannelsAction.DismissBatch) },
                            )
                        }
                    }
                    if (filtered.isEmpty()) {
                        item {
                            StateCard(
                                title = "没有匹配的账号",
                                detail = if (query.isNotBlank() || filter != AccountFilter.ALL) {
                                    "换个关键词，或把筛选切回「全部」。"
                                } else {
                                    "换个关键词试试；也可以清空搜索框看全部账号。"
                                },
                            )
                        }
                    } else {
                        items(filtered.size) { index ->
                            val row = filtered[index]
                            AccountQuotaCard(
                                row = row,
                                provider = state.providers.firstOrNull { it.id == row.providerId },
                                action = state.accountAction,
                                probe = probes[row.providerId],
                                probing = probing == row.providerId,
                                toggling = toggling == row.providerId,
                                modelsState = models[row.providerId],
                                selectionMode = selecting,
                                selected = row.accountId in visibleSelected,
                                onToggleSelected = {
                                    selectedIds = toggleSelection(selectedIds, row.accountId)
                                },
                                onLongPress = {
                                    selecting = true
                                    if (row.accountId !in selectedIds) {
                                        selectedIds = (selectedIds + row.accountId).distinct()
                                    }
                                },
                                onSetDisabled = { disabled ->
                                    onAction(ChannelsAction.SetAccountDisabled(row.accountId, disabled))
                                },
                                onCheckin = { onAction(ChannelsAction.CheckinAccount(row.accountId)) },
                                onReadQuota = { onAction(ChannelsAction.ReadAccountQuota(row.accountId)) },
                                onLoadModels = { onLoadModels(row.providerId) },
                                onToggleProvider = { enable -> onToggleProvider(row.providerId, enable) },
                                onTest = { onTestProvider(row.providerId) },
                            )
                        }
                    }
                }
            }
        }

        // ——— 收尾：接入与迁移 ———
        item {
            SectionHeader(
                title = "接入与迁移",
                note = "账号包导入/导出与 Qoder 登录。这些动作会真的往本机账号池里写账号。",
            )
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
        item {
            Text(
                text = "额度来自本机管理接口的最近一次读数，卡片不展示任何凭据；" +
                    "签到与启停都是写操作，失败原因会原样贴在卡片上。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    // 模型清单弹窗按渠道只弹一个：同一渠道的多个账号共用一份清单。
    if (ready != null) {
        ready.providers.forEach { provider ->
            val openModels = models[provider.id]
            if (openModels != null) {
                ProviderModelsDialog(
                    provider = provider,
                    state = openModels,
                    onDismiss = { onDismissModels(provider.id) },
                )
            }
        }
    }
}

/** 选择态顶栏：已选 N / 全选 / 取消。 */
@Composable
private fun SelectionBar(
    selectedCount: Int,
    totalVisible: Int,
    allSelected: Boolean,
    onToggleAll: () -> Unit,
    onCancel: () -> Unit,
) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        // 两行而不是一行长文案：1.3× 字号下「已选 12 / 12」+ 四个字的按钮会把顶栏挤爆。
        Column(Modifier.weight(1f)) {
            Text(
                text = "已选 $selectedCount",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "共 $totalVisible 个",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
            )
        }
        McaButton(
            onClick = onToggleAll,
            style = McaButtonStyle.Text,
            size = McaButtonSize.CardAction,
            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
            enabled = totalVisible > 0,
        ) { Text(if (allSelected) "取消全选" else "全选") }
        Spacer(Modifier.width(4.dp))
        McaButton(
            onClick = onCancel,
            style = McaButtonStyle.Text,
            size = McaButtonSize.CardAction,
            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
        ) {
            Text("取消")
        }
    }
}

/** 筛选芯片：横向滚动一行，1.3× 下不换行、不压扁。 */
@Composable
private fun AccountFilterChips(
    selected: AccountFilter,
    onSelect: (AccountFilter) -> Unit,
    enabled: Boolean,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        AccountFilter.entries.forEach { option ->
            McaChip(
                selected = option == selected,
                onClick = { onSelect(option) },
                label = { Text(accountFilterLabel(option)) },
                enabled = enabled,
            )
        }
    }
}

/**
 * 批量动作行。
 *
 * 文案在禁用时**不变**（靠透明度表达），避免把用户的选择误读成"功能不存在"。
 */
@Composable
private fun BatchActionRow(
    selectedCount: Int,
    checkinCount: Int,
    running: Boolean,
    onRefresh: () -> Unit,
    onEnable: () -> Unit,
    onDisable: () -> Unit,
    onCheckin: () -> Unit,
) {
    val canRun = selectedCount > 0 && !running
    Column {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            McaButton(
                onClick = onRefresh,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                enabled = canRun,
            ) { Text(batchKindLabel(BatchKind.REFRESH)) }
            McaButton(
                onClick = onEnable,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                enabled = canRun,
            ) { Text(batchKindLabel(BatchKind.ENABLE)) }
            McaButton(
                onClick = onDisable,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                enabled = canRun,
            ) { Text(batchKindLabel(BatchKind.DISABLE)) }
            McaButton(
                onClick = onCheckin,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                enabled = canRun && checkinCount > 0,
            ) { Text(batchKindLabel(BatchKind.CHECKIN)) }
        }
        if (!running && selectedCount > 0 && checkinCount == 0) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = batchCheckinEmptyHint(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/** 批量结果卡：只显示回包数出来的成功/失败与第一个失败原因。 */
@Composable
private fun BatchResultCard(state: BatchUiState, onDismiss: () -> Unit) {
    if (state is BatchUiState.Idle) {
        return
    }
    Surface(
        shape = McaShape.Sheet,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(16.dp)) {
            when (state) {
                is BatchUiState.Running -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(McaIconSize.S),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = "正在${batchKindLabel(state.kind)}：${state.total} 个账号…",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is BatchUiState.Done -> {
                    Text(
                        text = batchKindLabel(state.kind),
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = batchSummaryText(
                            kind = state.kind,
                            ok = state.ok,
                            failed = state.failed,
                            total = state.total,
                            firstError = state.firstError,
                        ),
                        style = MaterialTheme.typography.bodySmall,
                        color = if (state.failed > 0) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
                is BatchUiState.Failed -> {
                    Text(
                        text = "${batchKindLabel(state.kind)}失败",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "${state.message}（错误码：${state.code}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                BatchUiState.Idle -> Unit
            }
            Spacer(Modifier.height(8.dp))
            McaButton(
                onClick = onDismiss,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("关闭") }
        }
    }
}

/** 选择态的勾选标记（MCA 没有 Checkbox 组件，用 Surface + McaIcons.Check 自绘结构）。 */
@Composable
private fun SelectionMark(selected: Boolean) {
    Surface(
        modifier = Modifier.size(McaIconSize.L),
        shape = McaShape.Capsule,
        color = if (selected) {
            MaterialTheme.colorScheme.primaryContainer
        } else {
            McaTonal.badgeFill()
        },
        border = McaHairline.stroke(),
    ) {
        Box(contentAlignment = Alignment.Center) {
            if (selected) {
                Icon(
                    imageVector = McaIcons.Check,
                    contentDescription = "已选中",
                    tint = MaterialTheme.colorScheme.onPrimaryContainer,
                    modifier = Modifier.size(McaIconSize.Xs),
                )
            }
        }
    }
}

/** 页面级小结：只报可数的真实数字，不做"健康度"之类的推断。 */
@Composable
private fun AccountsHeadline(providers: List<ProviderSummary>, accounts: List<AccountQuotaRow>) {
    val disabled = accounts.count { it.disabled }
    val detail = buildString {
        append("${accounts.size} 个账号 · ${providers.size} 个渠道")
        if (disabled > 0) {
            append(" · $disabled 个已停用")
        }
    }
    Text(
        text = detail,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun SectionHeader(title: String, note: String) {
    Column {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = note,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * 账号卡片。
 *
 * 只显示核实过的字段：名称、启用开关（`disabled`）、额度百分比条与窗口、
 * 重置时间（`reset_at`）、最近使用（`last_used`）、额度错误原文（`quota_error`）、
 * 签到入口（仅 `checkin_supported`）。`quota_supported == false` 时写
 * 「该渠道不提供额度接口」并且**不画 0% 的空条**，也不给「读取额度」按钮（点了也没用）。
 *
 * 「读取额度」是单账号动作：走 `POST /api/accounts/{id}/refresh`，成功用回包快照就地重画。
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun AccountQuotaCard(
    row: AccountQuotaRow,
    provider: ProviderSummary?,
    action: AccountActionUiState,
    probe: ChannelProbe?,
    probing: Boolean,
    toggling: Boolean,
    modelsState: ProviderModelsUiState?,
    selectionMode: Boolean = false,
    selected: Boolean = false,
    onToggleSelected: () -> Unit = {},
    onLongPress: () -> Unit = {},
    onSetDisabled: (Boolean) -> Unit,
    onCheckin: () -> Unit,
    onReadQuota: () -> Unit,
    onLoadModels: () -> Unit,
    onToggleProvider: (Boolean) -> Unit,
    onTest: () -> Unit,
) {
    var confirmToggle by remember(row.accountId) { mutableStateOf(false) }
    val busyToggle = action is AccountActionUiState.Busy &&
        action.accountId == row.accountId &&
        action.kind == AccountActionKind.TOGGLE
    val busyCheckin = action is AccountActionUiState.Busy &&
        action.accountId == row.accountId &&
        action.kind == AccountActionKind.CHECKIN
    val busyQuota = action is AccountActionUiState.Busy &&
        action.accountId == row.accountId &&
        action.kind == AccountActionKind.QUOTA
    // 同一时刻只允许一个账号在动：别的卡片在忙时，这一张的写操作一起禁用。
    val anyBusy = action is AccountActionUiState.Busy
    // 「最近读数」要相对当前时刻算；每次重组取一次即可（不缓存，读过之后自然会变）。
    val nowMs = System.currentTimeMillis()
    val failure = (action as? AccountActionUiState.Failed)?.takeIf { it.accountId == row.accountId }
    val headingColor = if (row.disabled) {
        MaterialTheme.colorScheme.onSurfaceVariant
    } else {
        MaterialTheme.colorScheme.onSurface
    }

    Surface(
        // 长按进入选择态；已在选择态时点卡片就是勾选/取消勾选。
        modifier = Modifier.combinedClickable(
            onClick = { if (selectionMode) onToggleSelected() },
            onLongClick = onLongPress,
        ),
        shape = McaShape.Sheet,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(16.dp)) {
            // 名称 + 启用开关
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (selectionMode) {
                    SelectionMark(selected = selected)
                    Spacer(Modifier.width(12.dp))
                }
                Column(Modifier.weight(1f)) {
                    Text(
                        text = row.label,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = headingColor,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = provider?.name ?: row.providerId,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                if (row.disabled) {
                    Text(
                        text = "已停用",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.width(8.dp))
                }
                Spacer(Modifier.width(8.dp))
                McaSwitch(
                    checked = !row.disabled,
                    onCheckedChange = { checked -> onSetDisabled(!checked) },
                    enabled = !busyToggle,
                    contentDescription = if (row.disabled) {
                        "启用账号 ${row.label}"
                    } else {
                        "停用账号 ${row.label}"
                    },
                )
            }

            // 额度
            Spacer(Modifier.height(12.dp))
            QuotaBlock(row = row, nowMs = nowMs)

            // 重置时间 / 最近使用
            Spacer(Modifier.height(8.dp))
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                accountTimeLabel(row.resetAt)?.let { resetAt ->
                    Text(
                        text = "额度重置 $resetAt",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = "最近使用 ${accountTimeLabel(row.lastUsed) ?: "尚未使用"}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            // 签到（只有渠道真的声明了签到能力才出现）
            if (row.checkinSupported) {
                Spacer(Modifier.height(12.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            text = "每日签到",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Text(
                            text = checkinLabel(row.checkin),
                            style = MaterialTheme.typography.bodySmall,
                            color = checkinColor(row.checkin),
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    McaButton(
                        onClick = onCheckin,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = !busyCheckin && !row.disabled && !checkinSettled(row.checkin),
                    ) {
                        Text(if (busyCheckin) "签到中…" else checkinButtonLabel(row.checkin))
                    }
                }
            }

            // 探测结论：独占一行（1.3× 字号下跟按钮挤一行会压成竖排单字）
            ProbeStatusText(probe = probe, probing = probing)

            if (failure != null) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = "${failure.message}（错误码：${failure.code}）",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            // 三个动作排成一行：模型 / 渠道启停 / 测试。
            // 选择态下整行隐藏：这时候点卡片是勾选，误触渠道启停的代价太大。
            if (!selectionMode) {
                Spacer(Modifier.height(12.dp))
                // 四个动作排成两行 × 两列：1.3× 字号下四个中文按钮并排放进 288dp 会顶边
                // （读取额度 89 + 查看模型 89 + 停用渠道 89 + 测试 58 + 间距 24 = 349dp），
                // 所以让它们换行，而不是横滚或被压成分行单字。
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (row.quotaSupported) {
                            McaButton(
                                onClick = onReadQuota,
                                style = McaButtonStyle.Outlined,
                                size = McaButtonSize.CardAction,
                                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                                enabled = quotaReadEnabled(row.quotaSupported, anyBusy),
                            ) { Text(quotaReadLabel(busyQuota)) }
                        }
                        McaButton(
                            onClick = onLoadModels,
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                            enabled = provider != null && modelsState !is ProviderModelsUiState.Loading,
                        ) {
                            Text(if (modelsState is ProviderModelsUiState.Loading) "读取中…" else "查看模型")
                        }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        McaButton(
                            onClick = { confirmToggle = true },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                            enabled = provider != null && !toggling,
                        ) {
                            Text(
                                when {
                                    toggling -> "切换中…"
                                    provider?.off == true -> "启用渠道"
                                    else -> "停用渠道"
                                },
                            )
                        }
                        McaButton(
                            onClick = onTest,
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                            enabled = provider != null && !probing,
                        ) {
                            Text(if (probing) "探测中…" else "测试")
                        }
                    }
                }
            }
        }
    }

    if (confirmToggle && provider != null) {
        // 启停是写操作，先弹确认；文案随**当前渠道状态**变化（off=true → 显示「启用」）。
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
                        onToggleProvider(provider.off)
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

/** 额度区：有窗口画窗口条，有总量/已用画一条总条，都没有就说明原因（绝不画 0%）。 */
@Composable
private fun QuotaBlock(row: AccountQuotaRow, nowMs: Long) {
    if (!row.quotaSupported) {
        Text(
            text = QUOTA_UNSUPPORTED_COPY,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        return
    }
    if (row.quotaWindows.isNotEmpty()) {
        row.quotaWindows.take(MAX_QUOTA_WINDOWS).forEach { window ->
            QuotaWindowBar(window = window)
            Spacer(Modifier.height(8.dp))
        }
    } else {
        val percent = accountRemainingPercent(row)
        if (percent != null) {
            QuotaBarBlock(
                label = "额度",
                percent = percent,
                resetsAt = row.resetAt,
                unit = row.quotaUnit,
                absolute = absoluteUsage(
                    used = row.quotaUsed.toLong(),
                    limit = row.quotaTotal.toLong(),
                    unit = row.quotaUnit,
                ),
            )
            Spacer(Modifier.height(8.dp))
        }
    }
    // 最近读数：点过一次「读取额度」后这行会变，用户据此确认"这次真的读到了"。
    Text(
        text = "最近读数 ${quotaReadAtLabel(row.quotaReadAt, nowMs)}",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    accountQuotaNote(row)?.let { note ->
        Text(
            text = note,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    if (row.quotaError.isNotBlank()) {
        Text(
            text = "额度读取失败：${row.quotaError}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
    }
}

@Composable
private fun QuotaWindowBar(window: AccountQuotaWindow) {
    QuotaBarBlock(
        label = window.name.ifBlank { "额度" },
        percent = windowRemainingPercent(window),
        resetsAt = window.resetsAt,
        unit = window.unit,
        absolute = absoluteUsage(used = window.amount, limit = window.limit, unit = window.unit),
    )
}

@Composable
private fun QuotaBarBlock(
    label: String,
    percent: Int,
    resetsAt: Long,
    unit: String,
    absolute: String,
) {
    val tone = quotaTone(percent)
    Column {
        Row(verticalAlignment = Alignment.Bottom) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = "剩余 $percent%",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = tone,
                maxLines = 1,
            )
        }
        Spacer(Modifier.height(4.dp))
        QuotaBar(percent = percent)
        if (resetsAt > 0) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = "${accountTimeLabel(resetsAt)} 重置",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        // 绝对用量只在渠道真的给了 amount/limit 时才出现（契约里两者都可能缺省）。
        if (absolute.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = absolute,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else if (unit.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = "单位：$unit",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * 额度条。MCA 没有现成的进度条组件，这里自绘**结构**：
 * 底色取 `McaTonal.badgeFill()`、填充色取语义色（error / tertiary / primary），
 * 圆角用 `McaShape.Capsule`，不新增任何颜色或圆角常量。
 */
@Composable
private fun QuotaBar(percent: Int) {
    val fraction = (percent.coerceIn(0, 100)) / 100f
    Box(
        Modifier
            .fillMaxWidth()
            .height(6.dp)
            .clip(McaShape.Capsule)
            .background(McaTonal.badgeFill()),
    ) {
        if (fraction > 0f) {
            Box(
                Modifier
                    .fillMaxWidth(fraction)
                    .fillMaxHeight()
                    .clip(McaShape.Capsule)
                    .background(quotaTone(percent)),
            )
        }
    }
}

@Composable
private fun quotaTone(percent: Int): Color = when {
    percent <= 10 -> MaterialTheme.colorScheme.error
    percent <= 40 -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.primary
}

/** 探测结论行（行为与上一轮一致：只换成不带按钮的独立一行）。 */
@Composable
private fun ProbeStatusText(probe: ChannelProbe?, probing: Boolean) {
    Spacer(Modifier.height(8.dp))
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

/** 模型清单弹窗：只列条数与名字，不展示任何凭据。 */
@Composable
private fun ProviderModelsDialog(
    provider: ProviderSummary,
    state: ProviderModelsUiState,
    onDismiss: () -> Unit,
) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("${provider.name} 的模型", fontWeight = FontWeight.Bold) },
        text = {
            when (state) {
                ProviderModelsUiState.Loading -> Text("读取中…")
                is ProviderModelsUiState.Failed -> Text(
                    text = "${state.message}（错误码：${state.code}）",
                    color = MaterialTheme.colorScheme.error,
                )
                is ProviderModelsUiState.Ready -> {
                    val preview = providerModelsPreview(state.models.models)
                    Column {
                        Text(
                            text = providerModelsSummary(state.models),
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(4.dp))
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
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("知道了") }
        },
    )
}

// ——— 纯展示计算（都能单测，不碰 Compose 运行时） ———

// ——— 筛选与批量选择的纯逻辑（都能单测，不碰 Compose 运行时） ———

/** 筛选芯片的四种口径；声明顺序即芯片顺序。 */
internal enum class AccountFilter {
    ALL,
    ENABLED,
    DISABLED,
    WITH_QUOTA,
}

internal fun accountFilterLabel(filter: AccountFilter): String = when (filter) {
    AccountFilter.ALL -> "全部"
    AccountFilter.ENABLED -> "仅启用"
    AccountFilter.DISABLED -> "仅停用"
    AccountFilter.WITH_QUOTA -> "有额度窗口"
}

/**
 * 先按芯片过一遍，再叠加搜索框（两个条件同时生效，顺序不影响结果）。
 *
 * 「有额度窗口」只认**真的带回窗口读数**的账号：`quota_supported && windows.isNotEmpty()`
 * ——只有 `total/used` 的积分制账号不算"有额度窗口"（它有额度，但没有窗口）。
 */
internal fun filterAccountRows(
    accounts: List<AccountQuotaRow>,
    filter: AccountFilter,
    query: String,
): List<AccountQuotaRow> {
    val byChip = accounts.filter { row ->
        when (filter) {
            AccountFilter.ALL -> true
            AccountFilter.ENABLED -> !row.disabled
            AccountFilter.DISABLED -> row.disabled
            AccountFilter.WITH_QUOTA -> row.quotaSupported && row.quotaWindows.isNotEmpty()
        }
    }
    return filterAccounts(byChip, query)
}

/** 选择只对**当前可见**的行生效：筛选把某些行隐藏后，"已选 N"和批量动作都跟着收缩。 */
internal fun visibleSelection(selectedIds: List<String>, rows: List<AccountQuotaRow>): List<String> {
    val visible = rows.map { it.accountId }.toSet()
    return selectedIds.filter { it in visible }
}

internal fun toggleSelection(selectedIds: List<String>, accountId: String): List<String> =
    if (accountId in selectedIds) {
        selectedIds - accountId
    } else {
        (selectedIds + accountId).distinct()
    }

internal fun allVisibleSelected(selectedIds: List<String>, rows: List<AccountQuotaRow>): Boolean =
    rows.isNotEmpty() && rows.all { it.accountId in selectedIds }

/** 一键签到只对 `checkin_supported` 的账号发请求；一个都没有时返回空列表（页面据此禁用按钮）。 */
internal fun batchCheckinTargets(
    rows: List<AccountQuotaRow>,
    selectedIds: List<String>,
): List<String> {
    val chosen = visibleSelection(selectedIds, rows).toSet()
    return rows.filter { it.accountId in chosen && it.checkinSupported }.map { it.accountId }
}

internal fun batchCheckinEmptyHint(): String =
    "所选账号都不支持签到：这些渠道没有签到能力，不会发任何请求。"

internal fun batchKindLabel(kind: BatchKind): String = when (kind) {
    BatchKind.REFRESH -> "刷新额度"
    BatchKind.ENABLE -> "批量启用"
    BatchKind.DISABLE -> "批量停用"
    BatchKind.CHECKIN -> "一键签到"
}

/**
 * 批量结果文案。计数**全部来自逐条回包**；即使全成功也只写「成功 N / 总数」，
 * 不写"全部成功"这种无法核实的断言。
 */
internal fun batchSummaryText(
    kind: BatchKind,
    ok: Int,
    failed: Int,
    total: Int,
    firstError: String,
): String {
    val label = batchKindLabel(kind)
    val reason = if (firstError.isBlank()) "" else "（第一个失败原因：$firstError）"
    return when {
        total <= 0 -> "$label：没有可执行的账号"
        failed <= 0 -> "$label：成功 $ok / $total"
        ok <= 0 -> "$label：失败 $failed / $total$reason"
        else -> "$label：成功 $ok / $total，失败 $failed$reason"
    }
}

/** 窗口剩余百分比：契约里 `used` 已是 0-100 的百分比（`contract.go:211`）。 */
internal fun windowRemainingPercent(window: AccountQuotaWindow): Int =
    (100.0 - window.usedPercent).roundToInt().coerceIn(0, 100)

/** 没有窗口时用 `quota.total/used` 折算；`total <= 0` 就没有百分比可言（返回 null，不编 0%）。 */
internal fun accountRemainingPercent(row: AccountQuotaRow): Int? {
    if (row.quotaTotal <= 0) {
        return null
    }
    val percent = 100.0 - row.quotaUsed * 100.0 / row.quotaTotal
    return percent.roundToInt().coerceIn(0, 100)
}

/** 「读取额度」按钮文案：进行中就写进行中，别让用户重复点。 */
internal fun quotaReadLabel(reading: Boolean): String = if (reading) "读取中…" else "读取额度"

/**
 * 「最近读数」的值：`read_at` 是**Unix 秒**（`contract.go:249`，服务端用 `time.Now().Unix()` 写入）。
 *
 * - `<= 0`（缺字段/从未读过/脏值）→ [QUOTA_READ_NEVER_COPY]，**不编时间**；
 * - 有值 → 复用 [relativeTimeLabel]（刚刚 / N 分钟前 / N 小时前 / N 天前），不另写一套格式化。
 *
 * [nowMs] 由调用方传入，纯函数好测（不读系统时钟）。
 */
internal fun quotaReadAtLabel(readAtSec: Long, nowMs: Long): String {
    if (readAtSec <= 0L) {
        return QUOTA_READ_NEVER_COPY
    }
    return relativeTimeLabel(nowMs, readAtSec * 1000L)
}

/**
 * 只有「渠道有额度接口」且「当前没有任何账号在动」时才可点。
 * 同一时刻只允许一个账号在读取，所以别的卡片在忙时这里也要禁用。
 */
internal fun quotaReadEnabled(quotaSupported: Boolean, anyBusy: Boolean): Boolean =
    quotaSupported && !anyBusy

/**
 * 没有额度条可画时的说明文案；有条可画就返回 null（不再叠一句话）。
 *
 * 三种"没有条"的情况：
 * - `quota_supported == false` → 渠道**没有额度接口**（能力缺失，不是读到 0%）；
 * - 支持额度但没读过 → 这一态由额度区那行 `最近读数 从未读过` 表达，这里不再重复；
 * - 读过了但没有窗口 → 上游这次确实没给窗口。
 */
internal fun accountQuotaNote(row: AccountQuotaRow): String? = when {
    !row.quotaSupported -> QUOTA_UNSUPPORTED_COPY
    row.quotaWindows.isNotEmpty() -> null
    accountRemainingPercent(row) != null -> null
    row.quotaError.isNotBlank() -> null
    row.quotaMessage.isNotBlank() -> row.quotaMessage
    row.quotaReadAt <= 0L -> null
    else -> "上次读取没有返回可用窗口"
}

/**
 * 「1,234 / 10,000 次」这种绝对用量：只有 amount 与 limit 都 > 0 才有意义
 * （契约 `contract.go:212-214` 里两者都是 omitempty，可能缺省）。缺省时返回空串，整行不显示。
 */
internal fun absoluteUsage(used: Long, limit: Long, unit: String): String {
    if (used <= 0 || limit <= 0) {
        return ""
    }
    val suffix = if (unit.isBlank()) "" else " $unit"
    return "${String.format(Locale.US, "%,d", used)} / ${String.format(Locale.US, "%,d", limit)}$suffix"
}

/** 今日签到状态文案（对齐桌面 `checkinRow` 的口径）。 */internal fun checkinLabel(checkin: AccountCheckin?): String {
    if (checkin == null) {
        return "今日未签到"
    }
    val base = when (checkin.outcome) {
        "claimed" -> "已签到"
        "done" -> "今天已签"
        "own-app" -> "需在官方 App 签到"
        "inactive" -> "今天没有活动"
        "ineligible" -> "今天不可签"
        "failed" -> "签到失败"
        "" -> "今日未签到"
        else -> "签到状态：${checkin.outcome}"
    }
    val extras = buildList {
        if (checkin.credit > 0) {
            add("+${formatCredits(checkin.credit)} 积分")
        }
        if (checkin.streak > 0) {
            add("连续 ${checkin.streak} 天")
        }
    }
    val tail = if (checkin.outcome == "failed" && checkin.message.isNotBlank()) {
        "：${checkin.message}"
    } else {
        ""
    }
    return if (extras.isEmpty()) base + tail else "$base · ${extras.joinToString(" · ")}$tail"
}

/** 今天已经确定领过的不再给「立即签到」（桌面口径：claimed，或 done 且不可再审）。 */
internal fun checkinSettled(checkin: AccountCheckin?): Boolean {
    if (checkin == null) {
        return false
    }
    return checkin.outcome == "claimed" || (checkin.outcome == "done" && !checkin.again)
}

internal fun checkinButtonLabel(checkin: AccountCheckin?): String = when {
    checkinSettled(checkin) -> "已签到"
    checkin?.outcome == "done" && checkin.again -> "再确认"
    else -> "立即签到"
}

/** Unix 秒 → `MM-dd HH:mm`；没有时间（<=0）时返回 null，让调用方整行不显示。 */
internal fun accountTimeLabel(seconds: Long): String? {
    if (seconds <= 0L) {
        return null
    }
    return accountTimeFormat.format(Instant.ofEpochSecond(seconds).atZone(ZoneId.systemDefault()))
}

/** 账号搜索：名称、渠道名/渠道 id、账号 id。空查询原样返回。 */
internal fun filterAccounts(accounts: List<AccountQuotaRow>, query: String): List<AccountQuotaRow> {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return accounts
    }
    return accounts.filter { row ->
        row.label.contains(needle, ignoreCase = true) ||
            row.providerId.contains(needle, ignoreCase = true) ||
            row.accountId.contains(needle, ignoreCase = true)
    }
}

private fun formatCredits(value: Double): String =
    if (value == value.toLong().toDouble()) {
        value.toLong().toString()
    } else {
        String.format(Locale.US, "%.1f", value)
    }

@Composable
private fun checkinColor(checkin: AccountCheckin?): Color = when {
    checkin?.outcome == "failed" -> MaterialTheme.colorScheme.error
    checkinSettled(checkin) -> MaterialTheme.colorScheme.primary
    else -> MaterialTheme.colorScheme.onSurfaceVariant
}

private val accountTimeFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("MM-dd HH:mm", Locale.US)

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
        Column(Modifier.padding(16.dp)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f),
            )
            if (actionLabel != null && onAction != null) {
                Spacer(Modifier.height(12.dp))
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
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = "正在读取本机账号与额度…",
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
        Column(Modifier.padding(16.dp)) {
            Text(
                text = "读取账号失败",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Spacer(Modifier.height(4.dp))
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
            Spacer(Modifier.height(12.dp))
            McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) {
                Text("重试")
            }
        }
    }
}
