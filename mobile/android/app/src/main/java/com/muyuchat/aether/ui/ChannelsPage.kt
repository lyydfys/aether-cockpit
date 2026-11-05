package com.muyuchat.aether.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.text.BasicTextField
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import org.json.JSONObject
import com.muyuchat.aether.runtime.AccountCheckin
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.AccountSettingsPatch
import com.muyuchat.aether.runtime.MAX_ACCOUNT_TAGS
import com.muyuchat.aether.runtime.RequestPreset
import com.muyuchat.aether.runtime.parseTagInput
import com.muyuchat.aether.runtime.formatAccountMappingLines
import com.muyuchat.aether.runtime.parseAccountMappingLines
import com.muyuchat.aether.runtime.extraModelNote
import com.muyuchat.aether.runtime.validateExtraModelName
import com.muyuchat.aether.runtime.AccountQuotaWindow
import com.muyuchat.aether.runtime.ChannelProbe
import com.muyuchat.aether.runtime.CUSTOM_CHANNEL_PROTOCOLS
import com.muyuchat.aether.runtime.CustomChannel
import com.muyuchat.aether.runtime.CustomChannelDraft
import com.muyuchat.aether.runtime.customChannelHeadersToText
import com.muyuchat.aether.runtime.customChannelProtocolLabel
import com.muyuchat.aether.runtime.slugifyCustomChannelId
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.QUOTA_UNSUPPORTED_COPY
import com.muyuchat.aether.runtime.relativeTimeLabel
import com.muyuchat.aether.runtime.ExtraModelAction
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
import com.muyuchat.core.designsystem.McaPlateSize
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconPlate
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

/** 点过「读取额度」、请求也回来了，但这次没读到额度（服务端 quota_read=false）。 */
internal const val QUOTA_READ_EMPTY_COPY = "无（这次读取没返回额度）"

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
    /** 打开「接入渠道」页（桌面 `channels.js:176/188` 的入口）；为 null 时不画入口。 */
    onOpenOnboard: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    var query by rememberSaveable { mutableStateOf("") }
    var filter by rememberSaveable { mutableStateOf(AccountFilter.ALL) }
    var selecting by rememberSaveable { mutableStateOf(false) }
    // 选中项存成 List（Bundle 可保存）；N 很小，查表用 contains 足够。
    var selectedIds by rememberSaveable { mutableStateOf(listOf<String>()) }
    // 自定义渠道（custom-channels）：独立 ViewModel，避免改 GatewayHome 的参数面。
    val customVm: CustomChannelsViewModel = viewModel()
    val customState by customVm.state.collectAsStateWithLifecycle()
    val customSaving by customVm.saving.collectAsStateWithLifecycle()
    val customDeleting by customVm.deleting.collectAsStateWithLifecycle()
    val customProbe by customVm.probe.collectAsStateWithLifecycle()
    val customError by customVm.error.collectAsStateWithLifecycle()
    var showCustomChannels by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(showCustomChannels) {
        if (showCustomChannels) {
            customVm.clearError()
            customVm.load(status)
        }
    }
    val ready = state as? ChannelsUiState.Ready
    val batchState = ready?.batch ?: BatchUiState.Idle
    val batchRunning = batchState is BatchUiState.Running
    val accounts = ready?.accounts.orEmpty()
    val filtered = filterAccountRows(accounts, filter, query)
    // 「复制配置」用的接入地址（与概览页口径一致：本机 /v1）；网关没跑时留空，不发假地址。
    val gatewayBaseUrl = if (status.running && status.addr.isNotBlank()) "http://${status.addr}/v1" else ""
    val visibleSelected = visibleSelection(selectedIds, filtered)
    val checkinTargets = batchCheckinTargets(filtered, visibleSelected)
    val allSelected = allVisibleSelected(visibleSelected, filtered)

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
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
                        // 与桌面端同名的页面标题：桌面导航写「账号与额度」，
                        // 副标题也照抄桌面端的原句，避免两端对同一页有两种说法。
                        DeepPageTitle(
                            title = "账号与额度",
                            subtitle = "账号、模型与余量，一眼看到底",
                        )
                    }
                    AetherHeaderIconButton(
                        iconRes = R.drawable.ic_refresh,
                        contentDescription = "刷新账号与额度",
                        onClick = { onAction(ChannelsAction.Refresh) },
                        enabled = state is ChannelsUiState.Ready || state is ChannelsUiState.Failed,
                    )
                }
            }
        }

        // ——— 主入口：接入渠道 / 自定义渠道（桌面 channels.js:177 的两个按钮） ———
        item {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (onOpenOnboard != null) {
                    AetherPillButton(
                        text = "接入渠道",
                        onClick = onOpenOnboard,
                        primary = true,
                    )
                }
                AetherPillButton(
                    text = "自定义渠道",
                    onClick = { showCustomChannels = true },
                )
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
                            detail = "点上方「接入渠道」选择接入方式：Qoder 设备码登录，或导入加密账号包。" +
                                "想注册一个新渠道行，用「自定义渠道」。",
                            actionLabel = if (onOpenOnboard != null) "接入渠道" else null,
                            onAction = onOpenOnboard,
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
                        // 单列列表：按用户要求去掉网格（不再有 2 列与「列表/网格」切换）。
                        items(
                            count = filtered.size,
                            key = { index -> filtered[index].accountId },
                        ) { index ->
                            val row = filtered[index]
                            AccountCard(
                                row = row,
                                providers = state.providers,
                                state = state,
                                probes = probes,
                                probing = probing,
                                toggling = toggling,
                                models = models,
                                baseUrl = gatewayBaseUrl,
                                selecting = selecting,
                                visibleSelected = visibleSelected,
                                selectedIds = selectedIds,
                                onAction = onAction,
                                onLoadModels = onLoadModels,
                                onToggleProvider = onToggleProvider,
                                onTestProvider = onTestProvider,
                                onSelect = { selectedIds = toggleSelection(selectedIds, row.accountId) },
                                onLongPress = {
                                    selecting = true
                                    if (row.accountId !in selectedIds) {
                                        selectedIds = (selectedIds + row.accountId).distinct()
                                    }
                                },
                            )
                        }
                    }
                }
            }
        }

        // ——— 收尾：口径说明（账号包与 Qoder 登录已移到「接入渠道」页） ———
        item {
            Text(
                text = "额度来自本机管理接口的最近一次读数，卡片不展示任何凭据；" +
                    "签到与启停都是写操作，失败原因会原样贴在卡片上。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        // 桌面才能做的两件事放在一起讲，口径统一：不是"没端点"，而是"手机上语义不适用"。
        item {
            Text(
                text = channelsDesktopOnlyNote(),
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
    // 自定义渠道弹窗（桌面 channels.js:177 的「自定义渠道」抽屉）。
    if (showCustomChannels) {
        CustomChannelsDialog(
            status = status,
            state = customState,
            saving = customSaving,
            deletingId = customDeleting,
            probe = customProbe,
            error = customError,
            canAttach = onOpenOnboard != null,
            onSave = { draft, done -> customVm.save(status, draft, done) },
            onDelete = { id, done -> customVm.delete(status, id, done) },
            onProbe = { base, key -> customVm.probe(status, base, key) },
            onDismissProbe = { customVm.dismissProbe() },
            onClearError = { customVm.clearError() },
            onOpenOnboard = { onOpenOnboard?.invoke() },
            onDismiss = { showCustomChannels = false },
        )
    }
}

/**
 * 账号区布局**固定单列**。
 *
 * 桌面 `channels.js:183` 有「列表 / 网格」两档（网格 2 列），但手机端视野更窄、卡片信息
 * 更密（额度条 + 状态行 + 操作行），两列会把每张卡压到几乎不可读；用户也明确要求不要
 * 网格列表。因此这里删掉了 `AccountsLayout` / `accountGridColumns` / 网格虚线卡，
 * 只保留单列。
 */

/** 账号卡的统一入口。 */
@Composable
private fun AccountCard(
    row: AccountQuotaRow,
    providers: List<ProviderSummary>,
    state: ChannelsUiState.Ready,
    probes: Map<String, ChannelProbe>,
    probing: String?,
    toggling: String?,
    models: Map<String, ProviderModelsUiState>,
    baseUrl: String,
    selecting: Boolean,
    visibleSelected: List<String>,
    selectedIds: List<String>,
    onAction: (ChannelsAction) -> Unit,
    onLoadModels: (String) -> Unit,
    onToggleProvider: (String, Boolean) -> Unit,
    onTestProvider: (String) -> Unit,
    onSelect: () -> Unit,
    onLongPress: () -> Unit,
) {
    AccountQuotaCard(
        row = row,
        provider = providers.firstOrNull { it.id == row.providerId },
        action = state.accountAction,
        probe = probes[row.providerId],
        probing = probing == row.providerId,
        toggling = toggling == row.providerId,
        modelsState = models[row.providerId],
        readOutcome = state.quotaReadOutcomes[row.accountId],
        edit = state.accountEdit,
        presetsState = state.presets,
        onAccountAction = onAction,
        baseUrl = baseUrl,
        selectionMode = selecting,
        selected = row.accountId in visibleSelected || row.accountId in selectedIds,
        onToggleSelected = onSelect,
        onLongPress = onLongPress,
        onSetDisabled = { disabled -> onAction(ChannelsAction.SetAccountDisabled(row.accountId, disabled)) },
        onCheckin = { onAction(ChannelsAction.CheckinAccount(row.accountId)) },
        onReadQuota = { onAction(ChannelsAction.ReadAccountQuota(row.accountId)) },
        onLoadModels = { onLoadModels(row.providerId) },
        onToggleProvider = { enable -> onToggleProvider(row.providerId, enable) },
        onTest = { onTestProvider(row.providerId) },
    )
}

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
                style = MaterialTheme.typography.titleMedium.copy(fontFeatureSettings = "tnum"),
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "共 $totalVisible 个",
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
            )
        }
        AetherPillButton(
            text = if (allSelected) "取消全选" else "全选",
            onClick = onToggleAll,
            enabled = totalVisible > 0,
        )
        Spacer(Modifier.width(8.dp))
        AetherPillButton(
            text = "取消",
            onClick = onCancel,
        )
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
            AetherPillButton(
                text = accountFilterLabel(option),
                onClick = { onSelect(option) },
                primary = option == selected,
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
            AetherPillButton(
                text = batchKindLabel(BatchKind.REFRESH),
                onClick = onRefresh,
                enabled = canRun,
            )
            AetherPillButton(
                text = batchKindLabel(BatchKind.ENABLE),
                onClick = onEnable,
                enabled = canRun,
            )
            AetherPillButton(
                text = batchKindLabel(BatchKind.DISABLE),
                onClick = onDisable,
                enabled = canRun,
            )
            AetherPillButton(
                text = batchKindLabel(BatchKind.CHECKIN),
                onClick = onCheckin,
                enabled = canRun && checkinCount > 0,
            )
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
        modifier = Modifier.aetherSpecularRim(McaShape.Sheet),
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
                            AetherColors.danger()
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
                        color = AetherColors.danger(),
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "${state.message}（错误码：${state.code}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = AetherColors.danger(),
                    )
                }
                BatchUiState.Idle -> Unit
            }
            Spacer(Modifier.height(8.dp))
            AetherPillButton(
                text = "关闭",
                onClick = onDismiss,
            )
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
        style = MaterialTheme.typography.bodySmall.copy(fontFeatureSettings = "tnum"),
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
 * 「仅账号/额度」被动标签：给 `chat == false` 的渠道（cursor / windsurf / zed）用。
 *
 * 尺寸上刻意压到 labelSmall + 1dp 垂直内边距，并且**不做可点击**（它不是筛选 chip）：
 * 位置跟在渠道名后面，不抢账号主标题。
 */
@Composable
private fun ChatUnsupportedTag() {
    AetherStatusChip(
        text = "仅账号/额度",
        tone = DeepTone.Neutral,
    )
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
    readOutcome: QuotaReadOutcome? = null,
    edit: AccountEditUiState = AccountEditUiState.Idle,
    presetsState: PresetsUiState = PresetsUiState.Idle,
    onAccountAction: (ChannelsAction) -> Unit = {},
    baseUrl: String = "",
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
    // 「最近读数」要相对当前时刻算；以读数时间戳为 key 记忆，避免滚动时每帧调用系统时钟。
    val nowMs = remember(row.quotaReadAt, readOutcome) { System.currentTimeMillis() }
    val failure = (action as? AccountActionUiState.Failed)?.takeIf { it.accountId == row.accountId }
    // 卡片级弹窗（桌面端那些账号入口在这里归到「更多」里，避免卡片按钮堆到三行以上）
    var sheet by remember(row.accountId) { mutableStateOf(CardSheet.NONE) }
    val headingColor = if (row.disabled) {
        MaterialTheme.colorScheme.onSurfaceVariant
    } else {
        MaterialTheme.colorScheme.onSurface
    }

    Surface(
        // 长按进入选择态；已在选择态时点卡片就是勾选/取消勾选。
        modifier = Modifier
            .aetherSpecularRim(McaShape.Sheet)
            .combinedClickable(
                onClick = { if (selectionMode) onToggleSelected() },
                onLongClick = onLongPress,
            ),
        shape = McaShape.Sheet,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(16.dp)) {
            // 名称 + 渠道品牌图标 + 启用开关（品牌图标按 provider_id 取，映射见 ProviderIcons.kt）
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (selectionMode) {
                    SelectionMark(selected = selected)
                    Spacer(Modifier.width(12.dp))
                }
                ProviderBrandPlate(
                    providerId = row.providerId,
                    providerName = provider?.name ?: row.label,
                    size = McaPlateSize.Medium,
                    online = !row.disabled,
                )
                Spacer(Modifier.width(12.dp))
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
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = provider?.name ?: row.providerId,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f, fill = false),
                        )
                        // 只有账号/额度能力的渠道（cursor / windsurf / zed）：提前说清不能对话转发，
                        // 别等用户发请求才撞 503/400。标签跟在渠道名后面，不抢主标题位置。
                        if (provider?.chat == false) {
                            Spacer(Modifier.width(6.dp))
                            ChatUnsupportedTag()
                        }
                    }
                }
                if (row.disabled) {
                    AetherStatusChip(
                        text = "已停用",
                        tone = DeepTone.Neutral,
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

            // 标签：做成 chip 并横滚（两个以上标签不许换行、不许把标题挤下去）
            if (row.tags.isNotEmpty()) {
                Spacer(Modifier.height(6.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    row.tags.forEach { tag ->
                        AetherStatusChip(
                            text = tag,
                            tone = DeepTone.Info,
                        )
                    }
                }
            }
            if (row.note.isNotBlank()) {
                Spacer(Modifier.height(4.dp))
                Text(
                    text = row.note,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }

            // 额度窗口（有窗口画窗口条，有总量画总条，都没有就说明原因）
            Spacer(Modifier.height(12.dp))
            AetherSubSurface(padding = PaddingValues(12.dp)) {
                Column {
                    QuotaBlock(row = row, nowMs = nowMs, actionFailure = failure, readOutcome = readOutcome)
                    Spacer(Modifier.height(8.dp))
                    AccountStatusRows(row = row)
                }
            }

            // 签到（只有渠道真的声明了签到能力才出现；状态色与按钮并行，不挤成竖排）
            if (row.checkinSupported) {
                Spacer(Modifier.height(8.dp))
                AccountCheckinRow(
                    row = row,
                    busy = busyCheckin,
                    onCheckin = onCheckin,
                )
            }

            // 探测结论：独占一行（1.3× 字号下跟按钮挤一行会压成竖排单字）
            ProbeStatusText(probe = probe, probing = probing)

            // 读取额度失败已经进额度区（`读取失败：…`），这里只留启停/签到这类通用失败，
            // 否则同一句原因会在卡片上出现两次。
            if (failure != null && showsGenericActionFailure(failure.kind)) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = "${failure.message}（错误码：${failure.code}）",
                    style = MaterialTheme.typography.bodySmall,
                    color = AetherColors.danger(),
                )
            }

            // 动作行（桌面 `acct-foot` 的右侧动作）：读取额度 + 更多。
            // 查看模型 / 测试 / 启停都收进「更多」菜单，卡片上只留最常用的一颗。
            // 用横滚而不是换行：1.3× 字号下并排两三个中文按钮会被压成竖排单字。
            if (!selectionMode) {
                Spacer(Modifier.height(12.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    if (row.quotaSupported) {
                        AetherPillButton(
                            text = quotaReadLabel(busyQuota),
                            onClick = onReadQuota,
                            enabled = quotaReadEnabled(row.quotaSupported, anyBusy),
                        )
                    }
                    AetherPillButton(
                        text = "更多",
                        onClick = { sheet = CardSheet.MORE },
                    )
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

    AccountSheets(
        sheet = sheet,
        row = row,
        provider = provider,
        edit = edit,
        presetsState = presetsState,
        baseUrl = baseUrl,
        action = action,
        onAction = onAccountAction,
        onClose = { sheet = CardSheet.NONE },
        onOpen = { next -> sheet = next },
        onTest = onTest,
        onSetDisabled = onSetDisabled,
        onLoadModels = onLoadModels,
        onConfirmToggleChannel = { confirmToggle = true },
    )
}

/**
 * 卡片底部的状态两行：`额度重置` / `最近使用`。
 *
 * 版式照桌面 `acct-foot` 的元信息行：**左侧标签定宽、右侧数值右对齐**，
 * 这样多张卡纵向排下来时数值是一列，而不是每行各自飘。
 */
@Composable
private fun AccountStatusRows(row: AccountQuotaRow) {
    val labelWidth = 76.dp
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        AccountStatusRow("额度重置", accountTimeLabel(row.resetAt) ?: "—", labelWidth)
        AccountStatusRow("最近使用", accountTimeLabel(row.lastUsed) ?: "尚未使用", labelWidth)
    }
}

@Composable
private fun AccountStatusRow(label: String, value: String, labelWidth: Dp) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
            modifier = Modifier.width(labelWidth),
        )
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.End,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
    }
}

/**
 * 每日签到行（桌面 `channels.js:373-399` 的 `checkinRow`）。
 *
 * 左边状态文字可换两行并省略，右边按钮定宽；1.3× 字号下也不会挤成竖排单字。
 */
@Composable
private fun AccountCheckinRow(
    row: AccountQuotaRow,
    busy: Boolean,
    onCheckin: () -> Unit,
) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(
                text = "每日签到",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
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
        AetherPillButton(
            text = if (busy) "签到中…" else checkinButtonLabel(row.checkin),
            onClick = onCheckin,
            enabled = !busy && !row.disabled && !checkinSettled(row.checkin),
        )
    }
}

/** 额度区：有窗口画窗口条，有总量/已用画一条总条，都没有就说明原因（绝不画 0%）。 */
@Composable
private fun QuotaBlock(
    row: AccountQuotaRow,
    nowMs: Long,
    actionFailure: AccountActionUiState.Failed?,
    readOutcome: QuotaReadOutcome?,
) {
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
        text = "最近读数 ${quotaReadAtLabel(row.quotaReadAt, nowMs, readOutcome)}",
        style = MaterialTheme.typography.labelSmall,
        color = AetherColors.textTertiary(),
    )
    accountQuotaNote(row)?.let { note ->
        Text(
            text = note,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    // 读取失败：服务端 quota_error 原文优先；没有原文但本次确实是"读取额度"失败的，
    // 用这次动作的真实原因（只认 kind == QUOTA，不把启停/签到失败误标成读取失败）。
    // 只有这一行染 error 色，容器与其它行保持中性。
    quotaReadFailureText(
        quotaError = row.quotaError,
        actionKind = actionFailure?.kind,
        actionMessage = actionFailure?.message.orEmpty(),
    )?.let { line ->
        Text(
            text = line,
            style = MaterialTheme.typography.bodySmall,
            color = AetherColors.danger(),
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
                style = MaterialTheme.typography.titleMedium.copy(fontFeatureSettings = "tnum"),
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
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = AetherColors.textTertiary(),
            )
        }
        // 绝对用量只在渠道真的给了 amount/limit 时才出现（契约里两者都可能缺省）。
        if (absolute.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = absolute,
                style = MaterialTheme.typography.labelSmall.copy(fontFeatureSettings = "tnum"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else if (unit.isNotBlank()) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = "单位：$unit",
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
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
    val targetFraction = (percent.coerceIn(0, 100)) / 100f
    val animatedFraction by animateFloatAsState(
        targetValue = targetFraction,
        animationSpec = spring(
            dampingRatio = Spring.DampingRatioNoBouncy,
            stiffness = Spring.StiffnessMediumLow,
        ),
        label = "channelQuotaBar",
    )
    val barColor = quotaTone(percent)
    Box(
        Modifier
            .fillMaxWidth()
            .height(6.dp)
            .clip(McaShape.Capsule)
            .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.45f)),
    ) {
        if (animatedFraction > 0f) {
            Box(
                Modifier
                    .fillMaxWidth(animatedFraction)
                    .fillMaxHeight()
                    .clip(McaShape.Capsule)
                    .background(
                        Brush.horizontalGradient(
                            colors = listOf(
                                barColor.copy(alpha = 0.78f),
                                barColor,
                            ),
                        ),
                    ),
            )
        }
    }
}

@Composable
private fun quotaTone(percent: Int): Color = when {
    percent <= 10 -> AetherColors.danger()
    percent <= 40 -> AetherColors.warning()
    else -> AetherColors.success()
}

/** 探测结论行（行为与上一轮一致：只换成不带按钮的独立一行）。 */
@Composable
private fun ProbeStatusText(probe: ChannelProbe?, probing: Boolean) {
    Spacer(Modifier.height(8.dp))
    val nowSec = remember(probe?.at) { System.currentTimeMillis() / 1000 }
    when {
        probing -> Text(
            text = "正在探测（服务端用账号池里的账号发起一次真实请求）…",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        probe == null -> Text(
            text = "还没有探测记录",
            style = MaterialTheme.typography.labelSmall,
            color = AetherColors.textTertiary(),
        )
        else -> {
            Text(
                text = channelProbeSummary(probe),
                style = MaterialTheme.typography.labelSmall,
                color = if (probe.ok) {
                    AetherColors.success()
                } else {
                    AetherColors.danger()
                },
            )
            if (probe.at > 0) {
                Text(
                    text = channelProbeAgeLabel(probe.at, nowSec),
                    style = MaterialTheme.typography.labelSmall,
                    color = AetherColors.textTertiary(),
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
 * 三态（`read_at` 有值永远是第一态，因为那是服务端的真读数）：
 * - `read_at > 0` → 复用 [relativeTimeLabel]（刚刚 / N 分钟前 / N 小时前 / N 天前）；
 * - `read_at == 0` 且**上次读取成功但没读到额度**（[QuotaReadOutcome.OK_WITHOUT_QUOTA]）
 *   → [QUOTA_READ_EMPTY_COPY]：与"从来没点过"分开；
 * - 其余（没点过 / 上次失败 / 缺字段 / 脏值）→ [QUOTA_READ_NEVER_COPY]，**不编时间**。
 *
 * [nowMs] 与 [lastAttempt] 由调用方传入，纯函数好测（不读系统时钟、不猜时间戳）。
 */
internal fun quotaReadAtLabel(
    readAtSec: Long,
    nowMs: Long,
    lastAttempt: QuotaReadOutcome? = null,
): String {
    if (readAtSec > 0L) {
        return relativeTimeLabel(nowMs, readAtSec * 1000L)
    }
    return if (lastAttempt == QuotaReadOutcome.OK_WITHOUT_QUOTA) {
        QUOTA_READ_EMPTY_COPY
    } else {
        QUOTA_READ_NEVER_COPY
    }
}

/**
 * 只有「渠道有额度接口」且「当前没有任何账号在动」时才可点。
 * 同一时刻只允许一个账号在读取，所以别的卡片在忙时这里也要禁用。
 */
internal fun quotaReadEnabled(quotaSupported: Boolean, anyBusy: Boolean): Boolean =
    quotaSupported && !anyBusy

/**
 * 额度区的「读取失败」一行：只有服务端真的给了 `quota_error` 原文才返回文案。
 *
 * `quota_error` 来源：`readQuotaFor` 读失败 → `noteQuotaFailure()`（`usage_quota_api.go:466`）
 * → `handleTokenRefresh` 回包 `quota_error`（`auth_api.go:349-351`）与 `accountView.quota_error`
 * （`admin.go:390`）。**没有原文就返回 null**，不编一句"读取失败"。
 */
internal fun quotaReadFailureLabel(quotaError: String): String? {
    val text = quotaError.trim()
    return if (text.isEmpty()) null else "读取失败：$text"
}

/**
 * 额度区「读取失败」这一行最终显示什么。
 *
 * 优先级：服务端 `quota_error` 原文 → 本次「读取额度」动作的真实失败原因（**只认 kind == QUOTA**）。
 * 启停/签到的失败返回 null，不会混进额度区（它们仍走卡片底部那条通用红字）。
 */
internal fun quotaReadFailureText(
    quotaError: String,
    actionKind: AccountActionKind?,
    actionMessage: String,
): String? {
    quotaReadFailureLabel(quotaError)?.let { return it }
    if (actionKind != AccountActionKind.QUOTA) {
        return null
    }
    return quotaReadFailureLabel(actionMessage)
}

/** 通用失败红字只服务启停/签到：额度动作的失败已经在额度区里显示了，不重复贴第二遍。 */
internal fun showsGenericActionFailure(kind: AccountActionKind): Boolean =
    kind != AccountActionKind.QUOTA

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
    DeepStateCard(
        title = title,
        detail = detail,
        tone = DeepTone.Info,
        actionLabel = actionLabel,
        onAction = onAction,
    )
}

@Composable
private fun LoadingCard() {
    DeepCard {
        Row(
            modifier = Modifier.fillMaxWidth(),
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
    DeepStateCard(
        title = "读取账号失败",
        detail = "$message（错误码：$code）",
        tone = DeepTone.Danger,
        actionLabel = "重试",
        onAction = onRetry,
    )
}

// ——— 卡片级弹窗：详情/设置、标签、备注、预设、复制配置（对齐桌面 channels.js） ———

/** 卡片上「更多」里的入口。 */
internal enum class CardSheet {
    NONE,
    MORE,
    DETAILS,
    TAGS,
    NOTE,
    MAPPING,
    PRESETS,
    COPY,
    EXTRA_MODELS,
}

/* ==========================================================================
 * 「更多」菜单（桌面 `channels.js:303-318` 的顺序与逐字文案）
 *
 * 用户反馈「表达不清」：光给一个词（预设 / 标签 / 复制配置）没人知道点下去干什么，
 * 所以每项都配一句"它到底做什么"。危险项（停用 / 删除）单独标记，渲染时用错误色 + 二次确认。
 * ========================================================================== */

/** 菜单里的一项对应哪个动作。 */
internal enum class AccountMoreAction {
    Details,
    Settings,
    Models,
    ModelMapping,
    // 桌面 channels.js:1018-1034 的「添加缺失模型」：上游 /models 漏报时手工追加。
    ExtraModels,
    Presets,
    Tags,
    Note,
    CopyConfig,
    Test,
    Refresh,
    Wake,
    ToggleDisabled,
    ToggleChannel,
    Delete,
}

/* ==========================================================================
 * 刷新动作的两种模式（桌面 `capabilities.js:1-5` 的移植）
 *
 * 同一个端点 `POST /api/accounts/{id}/refresh`，但文案与成功提示不同：
 * 额度型（渠道有 quota 能力或有自定义余额）→「刷新额度」/「额度已刷新」；
 * 凭据型（渠道声明 refresh）→「刷新凭据」/「凭据已刷新」。
 * ========================================================================== */

/** 刷新模式：额度型优先，其次凭据型；null = 这个渠道两种都不支持。 */
internal enum class AccountRefreshMode { Quota, Credentials }

internal fun accountRefreshMode(
    capabilities: Map<String, Boolean>,
    hasCustomQuota: Boolean = false,
): AccountRefreshMode? = when {
    capabilities["quota"] == true || hasCustomQuota -> AccountRefreshMode.Quota
    capabilities["refresh"] == true -> AccountRefreshMode.Credentials
    else -> null
}

/** 刷新按钮文案（桌面 `channels.js:335`：`refreshMode === 'quota' ? '刷新额度' : '刷新凭据'`）。 */
internal fun accountRefreshLabel(mode: AccountRefreshMode): String = when (mode) {
    AccountRefreshMode.Quota -> "刷新额度"
    AccountRefreshMode.Credentials -> "刷新凭据"
}

/** 刷新成功提示（桌面 `channels.js:343/348`）。 */
internal fun accountRefreshSuccessNote(mode: AccountRefreshMode): String = when (mode) {
    AccountRefreshMode.Quota -> "额度已刷新"
    AccountRefreshMode.Credentials -> "凭据已刷新"
}

/** 立即唤醒的文案：桌面 tooltip 原话「发一个最小请求，提前触发额度重置」。 */
internal fun accountWakeHint(): String = "发一个最小请求，提前触发额度重置"

/** 立即唤醒的按钮文案。 */
internal fun accountWakeLabel(): String = "立即唤醒"

/** 删除确认的标题与说明：桌面 `channels.js:314` 原话。 */
internal fun accountDeleteConfirmTitle(label: String): String = "删除账号「$label」？"

internal fun accountDeleteConfirmBody(): String = "只移除本机记录，渠道本身不动。"

/**
 * 渠道页脚注：讲清"为什么这两块只有桌面能做"。
 *
 * 注意口径：`inject`（`POST /api/accounts/{id}/inject`，`gateway.go:1123`）**端点存在**，
 * 只是语义是把凭据写进**桌面 IDE 的配置文件**——手机上没有那个目录，所以不是"没接口"，
 * 而是"这件事在手机上不成立"。`local_import` 同理（要读桌面客户端刚写好的凭据文件）。
 */
internal fun channelsDesktopOnlyNote(): String =
    "「写入本机 IDE（一键切号）」只在桌面端可用：它把凭据写进本机 IDE 的配置文件，" +
        "手机上没有对应的配置目录。「本机导入」同理——它要读桌面客户端刚写好的凭据文件，" +
        "手机上也没有那些目录。"

/** 一项菜单：文案 + 一句话说明 + 是否危险（危险项要二次确认）。 */
internal data class AccountMoreItem(
    val action: AccountMoreAction,
    val label: String,
    val detail: String,
    val danger: Boolean = false,
)

/**
 * 账号卡「更多」菜单的完整表（纯函数，便于单测与"以后加项只改这里"）。
 *
 * 顺序对齐桌面卡脚（`channels.js:303-318`）：… 测试连通性 → 刷新 → 立即唤醒 → 停用 → 删除。
 *
 * @param canDelete 删除账号端点**已核实存在**（`DELETE /api/accounts/{id}`，`gateway.go:873`），
 *   默认 true；保留开关是为了将来"只读演示"场景能整项关掉。
 * @param refreshMode 额度型/凭据型决定按钮文案；null = 该渠道两种都不支持，不渲染该项。
 * @param chat 只有 `capabilities.chat` 为真才显示「立即唤醒」（桌面 `channels.js:352` 同口径）。
 * @param disabled 账号当前是否已停用：决定危险项显示「停用账号」还是「启用账号」。
 */
internal fun accountMoreMenuItems(
    hasNote: Boolean,
    tagCount: Int,
    presetCount: Int,
    /** 该账号已配置的模型映射条数（`AccountSnapshot.model_mapping`）。 */
    mappingCount: Int = 0,
    disabled: Boolean = false,
    canDelete: Boolean = true,
    channelOff: Boolean = false,
    refreshMode: AccountRefreshMode? = null,
    chat: Boolean = false,
): List<AccountMoreItem> {
    val items = mutableListOf(
        AccountMoreItem(
            action = AccountMoreAction.Details,
            label = "详情",
            detail = "账号的只读信息（渠道、入库时间、额度来源）",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Settings,
            label = "设置：优先级 / 权重 / 并发",
            detail = "决定路由优先级、负载权重与并发上限",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Models,
            label = "查看模型",
            detail = "这个渠道声明了哪些模型、是否已启用",
        ),
        AccountMoreItem(
            action = AccountMoreAction.ExtraModels,
            label = "添加缺失模型",
            // 菜单里用一句话；完整桌面原话（含"添加后参与路由与 /v1/models"）放在弹窗正文，
        // 因为菜单行只放得下一句话（ChannelsMoreMenuTest 有 ≤48 字的约束）。
        detail = "上游 /models 漏报时手工追加",
        ),
        AccountMoreItem(
            action = AccountMoreAction.ModelMapping,
            label = if (mappingCount > 0) "模型映射（$mappingCount 条）" else "模型映射",
            detail = "把客户端模型名改成该账号真正请求的上游模型名",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Presets,
            label = if (presetCount > 0) "预设（已挂 $presetCount 个）" else "预设",
            detail = "这个渠道的参数预设，换模型/换额度规则时用",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Tags,
            label = if (tagCount > 0) "标签（$tagCount 个）" else "标签",
            detail = "给账号打标签，列表里可按标签筛",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Note,
            label = if (hasNote) "编辑备注" else "加备注",
            detail = "自己记一句，只存在本机",
        ),
        AccountMoreItem(
            action = AccountMoreAction.CopyConfig,
            label = "复制配置",
            detail = "复制接入片段（Base URL / 渠道 / 模型，不含凭据）",
        ),
        AccountMoreItem(
            action = AccountMoreAction.Test,
            label = "测试连通性",
            detail = "发一个最小请求验证 Key 现在还能用",
        ),
    )
    // 刷新：额度型 / 凭据型文案不同（桌面 `capabilities.js:1-5` + `channels.js:335`），
    // 两种都不支持的渠道不出现（不给点了没反应的项）。
    refreshMode?.let { mode ->
        items += AccountMoreItem(
            action = AccountMoreAction.Refresh,
            label = accountRefreshLabel(mode),
            detail = if (mode == AccountRefreshMode.Quota) {
                "重新向渠道读一次剩余额度"
            } else {
                "重新续期账号凭据（Token / 授权）"
            },
        )
    }
    // 立即唤醒：只有 chat 渠道才有意义（桌面 `channels.js:352` 的判定同此）。
    if (chat) {
        items += AccountMoreItem(
            action = AccountMoreAction.Wake,
            label = accountWakeLabel(),
            detail = accountWakeHint(),
        )
    }
    items += AccountMoreItem(
        action = AccountMoreAction.ToggleDisabled,
        label = if (disabled) "启用账号" else "停用账号",
        detail = if (disabled) "恢复参与路由" else "停用后不再参与路由，凭据保留",
        // 停用是写操作，用危险色 + 二次确认；启用不算危险。
        danger = !disabled,
    )
    // 渠道级开关（桌面 `acct-acts` 里的那个 toggle）：整条渠道退出/恢复路由。
    items += AccountMoreItem(
        action = AccountMoreAction.ToggleChannel,
        label = if (channelOff) "启用渠道" else "停用渠道",
        detail = if (channelOff) "恢复这个渠道的所有账号" else "整个渠道退出路由，所有账号一起停",
        danger = !channelOff,
    )
    if (canDelete) {
        items += AccountMoreItem(
            action = AccountMoreAction.Delete,
            label = "删除账号",
            detail = "只移除本机记录，渠道本身不动",
            danger = true,
        )
    }
    return items
}

/* ==========================================================================
 * 字段文案：每个数字都要说清「是什么 / 影响什么 / 范围与默认值」
 *
 * 用户反馈：详情与设置弹窗里只显示数字，没人知道 5 / 80 / 留空 是什么意思。
 * 桌面依据（`channels.js`）：优先级 `0 = 默认，1 = 首选`（`:476`，clamp ≥0 `:504`）、
 * 权重 `1 - 1000`（`:477`，clamp 1..1000 `:505`）、并发上限 `留空 = 跟随全局`（`:478`，
 * 空 → -1 `:506`）、渠道并发上限 `跟随全局`（`:1065`）、计费倍率 `1`（`:1064`）、
 * 价格单位 `美元 / 百万 tokens`（`:1038`）、模型倍率 `留空=未配置`（`:1057`）。
 * ========================================================================== */

/** 账号详情/设置里出现的数字字段。 */
internal enum class AccountField {
    Enabled,
    Priority,
    Weight,
    Concurrency,
    Whitelist,
    QuotaUsage,
    TokenUsage,
    Cost,
}

/**
 * 一个字段的三段文案。
 *
 * @property label 它叫什么（与桌面字段名一致）
 * @property help 它影响什么（显示在输入框下方/数值下方的那行小字）
 * @property range 范围 / 默认值 / 单位——有默认值就必须写出来
 */
internal data class AccountFieldCopy(
    val label: String,
    val help: String,
    val range: String,
)

/** 字段文案表：全站唯一来源，页面里不许散写。 */
internal fun accountFieldCopy(field: AccountField): AccountFieldCopy = when (field) {
    AccountField.Enabled -> AccountFieldCopy(
        label = "启用",
        help = "停用后这个账号不再参与路由，凭据仍保留在本机",
        range = "默认：启用",
    )
    AccountField.Priority -> AccountFieldCopy(
        label = "优先级",
        help = "数字越小越先被选中；同一模型下按优先级排队",
        range = "范围 ≥ 0 · 默认 0 · 填 1 = 首选",
    )
    AccountField.Weight -> AccountFieldCopy(
        label = "权重",
        help = "同优先级时按权重分配流量，越大分到越多",
        range = "范围 1 – 1000 · 默认 1",
    )
    AccountField.Concurrency -> AccountFieldCopy(
        label = "并发上限",
        help = "该账号同时最多几个请求；到上限后请求会排队或转给别的账号",
        range = "留空或 -1 = 跟随全局 · 单位：个",
    )
    AccountField.Whitelist -> AccountFieldCopy(
        label = "账号级白名单",
        help = "限制这个账号只能路由哪些模型；桌面端已改为跟随渠道勾选，这里只保留「清空」",
        range = "单位：个 · 清空后跟随渠道",
    )
    AccountField.QuotaUsage -> AccountFieldCopy(
        label = "额度用量",
        help = "已用 / 总额：来自渠道接口的读数，不是本机统计",
        range = "单位见数值后缀（%, 次, tokens）",
    )
    AccountField.TokenUsage -> AccountFieldCopy(
        label = "令牌用量",
        help = "本机账本累计，不是渠道后台数字",
        range = "单位：tokens · 含输入与输出",
    )
    AccountField.Cost -> AccountFieldCopy(
        label = "费用",
        help = "按本机价格表估算，未计价次数单独列出",
        range = "单位：$ · 未计价的请求不计入",
    )
}

/**
 * 只读数字行：**标签 —— 值（右对齐）—— 单位** 三段式，说明在下一行小字。
 *
 * 与桌面 `detail-field` 同结构；值固定右对齐，多行纵向能对齐成一列。
 */
@Composable
private fun AccountReadonlyRow(
    copy: AccountFieldCopy,
    value: String,
    unit: String = "",
) {
    Column(Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = copy.label,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.width(88.dp),
            )
            Text(
                text = value,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.End,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            if (unit.isNotBlank()) {
                Spacer(Modifier.width(4.dp))
                Text(
                    text = unit,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Text(
            text = copy.help,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** 可改字段：标签在输入框里、范围当 placeholder、说明贴一行 `labelSmall` 在下面。 */
@Composable
private fun AccountFieldEditor(
    copy: AccountFieldCopy,
    value: String,
    onValueChange: (String) -> Unit,
) {
    Column {
        ChannelTextField(
            value = value,
            onValueChange = onValueChange,
            label = copy.label,
            placeholder = copy.range,
        )
        Text(
            text = copy.help,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}


/**
 * 额外模型弹窗（桌面 `channels.js:1018-1034`）：说明 + 已登记模型（可删）+ 添加输入框。
 *
 * 复用卡片既有的 [AccountActionUiState] 通道（`AccountActionKind.EXTRA_MODEL`），
 * 不额外引入状态：进行中禁用、失败贴服务端原文。
 */
@Composable
private fun ExtraModelsBody(
    provider: ProviderSummary?,
    action: AccountActionUiState,
    onAction: (ChannelsAction) -> Unit,
) {
    var input by remember(provider?.id) { mutableStateOf("") }
    val busy = action is AccountActionUiState.Busy && action.kind == AccountActionKind.EXTRA_MODEL
    val failure = (action as? AccountActionUiState.Failed)
        ?.takeIf { it.kind == AccountActionKind.EXTRA_MODEL }
    val invalid = if (input.isBlank()) null else validateExtraModelName(input)
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            text = extraModelNote(),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        val models = provider?.modelIds.orEmpty()
        if (models.isEmpty()) {
            Text(
                text = "还没有额外模型",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                models.forEach { model ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = model,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f),
                        )
                        McaButton(
                            onClick = {
                                onAction(
                                    ChannelsAction.ExtraModel(
                                        providerId = provider?.id.orEmpty(),
                                        model = model,
                                        action = ExtraModelAction.Remove,
                                    ),
                                )
                            },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                            enabled = !busy,
                        ) { Text(if (busy) "处理中…" else "删除", maxLines = 1) }
                    }
                }
            }
        }
        ChannelTextField(
            value = input,
            onValueChange = { input = it },
            label = "模型名",
            placeholder = "例如 deepseek-v4.1-flash",
        )
        invalid?.let { problem ->
            Text(
                text = problem,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        McaButton(
            onClick = {
                onAction(
                    ChannelsAction.ExtraModel(
                        providerId = provider?.id.orEmpty(),
                        model = input,
                        action = ExtraModelAction.Add,
                    ),
                )
            },
            style = McaButtonStyle.Primary,
            enabled = !busy && invalid == null && input.isNotBlank(),
        ) { Text(if (busy) "处理中…" else "添加", maxLines = 1) }
        failure?.let {
            Text(
                text = it.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

/** 桌面端一样的小输入框：Surface(hairline) + BasicTextField，自己撑到 48dp。 */
@Composable
private fun ChannelTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    placeholder: String = "",
    singleLine: Boolean = true,
    minHeight: Int = 48,
    visualTransformation: VisualTransformation = VisualTransformation.None,
) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            singleLine = singleLine,
            textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
            visualTransformation = visualTransformation,
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = minHeight.dp)
                .wrapContentHeight(Alignment.CenterVertically)
                .padding(horizontal = 12.dp, vertical = 8.dp)
                .semantics { contentDescription = label },
            decorationBox = { inner ->
                if (value.isEmpty() && placeholder.isNotEmpty()) {
                    Text(
                        text = placeholder,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                inner()
            },
        )
    }
}

/** 保存失败时贴在弹窗里的那一行（错误码 + 服务端原文）。 */
@Composable
private fun EditFailureLine(edit: AccountEditUiState, accountId: String, kind: AccountEditKind) {
    val failure = (edit as? AccountEditUiState.Failed)
        ?.takeIf { it.accountId == accountId && it.kind == kind }
        ?: return
    Spacer(Modifier.height(4.dp))
    Text(
        text = "${failure.message}（错误码：${failure.code}）",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
    )
}

/** 保存成功后自动关弹窗：只在"刚才在保存 → 现在 Idle"时关，失败（Failed）留着让用户看原因。 */
@Composable
private fun CloseOnSaved(edit: AccountEditUiState, onSaved: () -> Unit) {
    var wasSaving by remember { mutableStateOf(false) }
    LaunchedEffect(edit) {
        if (edit is AccountEditUiState.Idle && wasSaving) {
            onSaved()
        }
        wasSaving = edit is AccountEditUiState.Saving
    }
}

@Composable
private fun AccountSheets(
    sheet: CardSheet,
    row: AccountQuotaRow,
    provider: ProviderSummary?,
    edit: AccountEditUiState,
    presetsState: PresetsUiState,
    baseUrl: String,
    action: AccountActionUiState,
    onAction: (ChannelsAction) -> Unit,
    onClose: () -> Unit,
    onOpen: (CardSheet) -> Unit,
    onTest: () -> Unit,
    onSetDisabled: (Boolean) -> Unit,
    onLoadModels: () -> Unit,
    onConfirmToggleChannel: () -> Unit,
) {
    if (sheet == CardSheet.NONE) {
        return
    }
    var confirmDisable by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    val savingKind = (edit as? AccountEditUiState.Saving)?.takeIf { it.accountId == row.accountId }?.kind
    McaDialog(
        onDismissRequest = onClose,
        title = {
            Text(
                text = when (sheet) {
                    CardSheet.DETAILS -> "账号详情 · ${row.label}"
                    CardSheet.TAGS -> "标签 · ${row.label}"
                    CardSheet.NOTE -> "备注 · ${row.label}"
                    CardSheet.MAPPING -> "模型映射 · ${row.label}"
                    CardSheet.PRESETS -> "渠道预设 · ${provider?.name ?: row.providerId}"
                    CardSheet.EXTRA_MODELS -> "添加缺失模型 · ${provider?.name ?: row.providerId}"
                    CardSheet.COPY -> "复制配置 · ${row.label}"
                    else -> "更多 · ${row.label}"
                },
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            when (sheet) {
                CardSheet.MORE -> Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    val presetCount = (presetsState as? PresetsUiState.Ready)
                        ?.view?.providerPresets?.get(row.providerId)?.size ?: 0
                    // 菜单表是纯函数（`accountMoreMenuItems`）：文案、说明、危险标记都在那里定，
                    // 这里只负责渲染与分发。
                    val items = accountMoreMenuItems(
                        hasNote = row.note.isNotBlank(),
                        tagCount = row.tags.size,
                        presetCount = presetCount,
                        mappingCount = row.modelMapping.size,
                        disabled = row.disabled,
                        channelOff = provider?.off == true,
                        refreshMode = provider?.let { accountRefreshMode(it.capabilities) },
                        chat = provider?.chat == true,
                    )
                    items.forEach { item ->
                        val color = if (item.danger) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.onSurface
                        }
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(McaShape.CardAction)
                                .clickable {
                                    when (item.action) {
                                        AccountMoreAction.Details,
                                        AccountMoreAction.Settings,
                                        -> onOpen(CardSheet.DETAILS)
                                        AccountMoreAction.ExtraModels -> onOpen(CardSheet.EXTRA_MODELS)
                                        AccountMoreAction.Presets -> onOpen(CardSheet.PRESETS)
                                        AccountMoreAction.Models -> {
                                            onClose()
                                            onLoadModels()
                                        }
                                        AccountMoreAction.Tags -> onOpen(CardSheet.TAGS)
                                        AccountMoreAction.Note -> onOpen(CardSheet.NOTE)
                                        AccountMoreAction.ModelMapping -> onOpen(CardSheet.MAPPING)
                                        AccountMoreAction.CopyConfig -> onOpen(CardSheet.COPY)
                                        AccountMoreAction.Test -> {
                                            onClose()
                                            onTest()
                                        }
                                        AccountMoreAction.Refresh -> {
                                            // 同一个端点（`POST /api/accounts/{id}/refresh`），
                                            // 文案已由 mode 决定（刷新额度 / 刷新凭据）。
                                            onClose()
                                            onAction(ChannelsAction.ReadAccountQuota(row.accountId))
                                        }
                                        AccountMoreAction.Wake -> {
                                            onClose()
                                            onAction(ChannelsAction.WakeAccount(row.accountId))
                                        }
                                        AccountMoreAction.ToggleDisabled -> confirmDisable = true
                                        AccountMoreAction.ToggleChannel -> onConfirmToggleChannel()
                                        AccountMoreAction.Delete -> confirmDelete = true
                                    }
                                }
                                .padding(horizontal = 8.dp, vertical = 10.dp),
                        ) {
                            Text(
                                text = item.label,
                                style = MaterialTheme.typography.bodyMedium,
                                color = color,
                            )
                            Text(
                                text = item.detail,
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
                CardSheet.DETAILS -> AccountDetailsBody(
                    row = row,
                    edit = edit,
                    saving = savingKind == AccountEditKind.DETAILS,
                    onAction = onAction,
                    onSaved = onClose,
                )
                CardSheet.TAGS -> AccountTagsBody(
                    row = row,
                    edit = edit,
                    saving = savingKind == AccountEditKind.TAGS,
                    onAction = onAction,
                    onSaved = onClose,
                )
                CardSheet.NOTE -> AccountNoteBody(
                    row = row,
                    edit = edit,
                    saving = savingKind == AccountEditKind.NOTE,
                    onAction = onAction,
                    onSaved = onClose,
                )
                CardSheet.MAPPING -> AccountMappingBody(
                    row = row,
                    edit = edit,
                    saving = savingKind == AccountEditKind.MAPPING,
                    onAction = onAction,
                    onSaved = onClose,
                )
                CardSheet.PRESETS -> {
                    LaunchedEffect(row.providerId) { onAction(ChannelsAction.LoadPresets) }
                    AccountPresetsBody(
                        row = row,
                        presetsState = presetsState,
                        saving = savingKind == AccountEditKind.PRESETS,
                        onAction = onAction,
                    )
                }
                CardSheet.COPY -> {
                    val json = accountConfigJson(baseUrl, row.providerId, row.label)
                    val clipboard = LocalClipboardManager.current
                    Column {
                        Text(
                            text = json,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(8.dp))
                        Text(
                            text = "只含接入地址、渠道 id 与账号名；凭据永远不进剪贴板。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(8.dp))
                        McaButton(
                            onClick = {
                                clipboard.setText(AnnotatedString(json))
                                onClose()
                            },
                            style = McaButtonStyle.Outlined,
                        ) { Text("复制到剪贴板") }
                    }
                }
                CardSheet.EXTRA_MODELS -> ExtraModelsBody(
                    provider = provider,
                    action = action,
                    onAction = onAction,
                )
                CardSheet.NONE -> Unit
                else -> Unit
            }
        },
        confirmButton = {
            McaButton(onClick = onClose, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )

    // 危险动作（停用）二次确认：桌面 `channels.js:475` 的「停用后不再参与路由」同一口径。
    if (confirmDisable) {
        McaDialog(
            onDismissRequest = { confirmDisable = false },
            title = { Text("停用账号「${row.label}」？", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "停用后不再参与路由，凭据仍保留在本机；随时可以再启用。",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        confirmDisable = false
                        onClose()
                        onSetDisabled(true)
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("停用") }
            },
            dismissButton = {
                McaButton(onClick = { confirmDisable = false }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }

    // 删除二次确认：文案逐字对齐桌面 `channels.js:314`
    //（标题「删除账号「X」？」/ 说明「只移除本机记录，渠道本身不动。」/ 危险色确认按钮）。
    if (confirmDelete) {
        McaDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text(accountDeleteConfirmTitle(row.label), fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = accountDeleteConfirmBody(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        confirmDelete = false
                        onClose()
                        onAction(ChannelsAction.DeleteAccount(row.accountId))
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("删除") }
            },
            dismissButton = {
                McaButton(onClick = { confirmDelete = false }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }
}

@Composable
private fun AccountDetailsBody(
    row: AccountQuotaRow,
    edit: AccountEditUiState,
    saving: Boolean,
    onAction: (ChannelsAction) -> Unit,
    onSaved: () -> Unit,
) {
    CloseOnSaved(edit, onSaved)
    var priorityText by remember(row.accountId) { mutableStateOf(row.priority.toString()) }
    var weightText by remember(row.accountId) { mutableStateOf((if (row.weight <= 0) 1 else row.weight).toString()) }
    var concurrencyText by remember(row.accountId) { mutableStateOf(row.concurrency?.toString() ?: "") }
    var disabledDraft by remember(row.accountId) { mutableStateOf(row.disabled) }
    var clearWhitelist by remember(row.accountId) { mutableStateOf(false) }
    val patch = accountSettingsPatch(row, priorityText, weightText, concurrencyText, disabledDraft, clearWhitelist)
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        // 只读信息在前：数字都带单位与「它是什么」的说明
        if (row.quotaTotal > 0) {
            AccountReadonlyRow(
                copy = accountFieldCopy(AccountField.QuotaUsage),
                value = "${row.quotaUsed} / ${row.quotaTotal}",
                unit = row.quotaUnit,
            )
        }
        AccountReadonlyRow(
            copy = AccountFieldCopy(
                label = "额度读取",
                help = "这条卡片上的额度是何时从渠道读回来的",
                range = "只读",
            ),
            value = accountTimeLabel(row.quotaReadAt) ?: "尚未读取",
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = accountFieldCopy(AccountField.Enabled).label,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    text = accountFieldCopy(AccountField.Enabled).help,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            McaSwitch(
                checked = !disabledDraft,
                onCheckedChange = { disabledDraft = !it },
                enabled = !saving,
                contentDescription = accountFieldCopy(AccountField.Enabled).label,
            )
        }
        Text(
            text = "可改设置",
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.Medium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        AccountFieldEditor(
            copy = accountFieldCopy(AccountField.Priority),
            value = priorityText,
            onValueChange = { priorityText = it },
        )
        AccountFieldEditor(
            copy = accountFieldCopy(AccountField.Weight),
            value = weightText,
            onValueChange = { weightText = it },
        )
        AccountFieldEditor(
            copy = accountFieldCopy(AccountField.Concurrency),
            value = concurrencyText,
            onValueChange = { concurrencyText = it },
        )
        // 账号级白名单：桌面端已改为跟随渠道，这里只提供"清空"这一条真改动
        if (row.allowedModels.isNotEmpty()) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "${accountFieldCopy(AccountField.Whitelist).label} ${row.allowedModels.size} 个",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = accountFieldCopy(AccountField.Whitelist).help,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaChip(
                    selected = clearWhitelist,
                    onClick = { clearWhitelist = !clearWhitelist },
                    label = { Text(if (clearWhitelist) "将清空" else "清空") },
                    enabled = !saving,
                )
            }
        } else {
            Text(
                text = "没有账号级白名单（跟随渠道模型勾选）",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            text = "只提交改动过的字段；没动的保持服务端原值。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        EditFailureLine(edit, row.accountId, AccountEditKind.DETAILS)
        McaButton(
            onClick = { onAction(ChannelsAction.UpdateAccountDetails(row.accountId, patch)) },
            style = McaButtonStyle.Primary,
            enabled = !saving && !patch.isEmpty,
        ) { Text(if (saving) "保存中…" else if (patch.isEmpty) "没有改动" else "保存") }
    }
}

@Composable
private fun AccountTagsBody(
    row: AccountQuotaRow,
    edit: AccountEditUiState,
    saving: Boolean,
    onAction: (ChannelsAction) -> Unit,
    onSaved: () -> Unit,
) {
    CloseOnSaved(edit, onSaved)
    var text by remember(row.accountId) { mutableStateOf(row.tags.joinToString(", ")) }
    val tags = parseTagInput(text)
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        ChannelTextField(
            value = text,
            onValueChange = { text = it },
            label = "标签",
            placeholder = "逗号或空格分隔，最多 8 个",
        )
        Text(
            text = if (tags.isEmpty()) "留空 = 清空标签" else "${tags.size} / $MAX_ACCOUNT_TAGS 个：${tags.joinToString(" · ")}",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        EditFailureLine(edit, row.accountId, AccountEditKind.TAGS)
        McaButton(
            onClick = { onAction(ChannelsAction.SetAccountTags(row.accountId, tags)) },
            style = McaButtonStyle.Primary,
            enabled = !saving && tags != row.tags,
        ) { Text(if (saving) "保存中…" else "保存") }
    }
}

@Composable
private fun AccountNoteBody(
    row: AccountQuotaRow,
    edit: AccountEditUiState,
    saving: Boolean,
    onAction: (ChannelsAction) -> Unit,
    onSaved: () -> Unit,
) {
    CloseOnSaved(edit, onSaved)
    var text by remember(row.accountId) { mutableStateOf(row.note) }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        ChannelTextField(
            value = text,
            onValueChange = { text = it },
            label = "备注",
            placeholder = "例如：主力号，额度月底重置",
            singleLine = false,
            minHeight = 96,
        )
        Text(
            text = "留空 = 清空备注",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        EditFailureLine(edit, row.accountId, AccountEditKind.NOTE)
        McaButton(
            onClick = { onAction(ChannelsAction.SetAccountNote(row.accountId, text.trim())) },
            style = McaButtonStyle.Primary,
            enabled = !saving && text.trim() != row.note,
        ) { Text(if (saving) "保存中…" else "保存") }
    }
}

/**
 * 模型映射编辑器：一行 `模型=上游模型`，**整表提交**。
 *
 * 服务端是整表替换（`SetModelMapping`，`provider/internal/provider/env.go:585-602`），
 * 所以这里把当前值全量预填、提交全量；"留空 = 清除映射"也要说清楚，否则用户会以为清空没生效。
 */
@Composable
private fun AccountMappingBody(
    row: AccountQuotaRow,
    edit: AccountEditUiState,
    saving: Boolean,
    onAction: (ChannelsAction) -> Unit,
    onSaved: () -> Unit,
) {
    CloseOnSaved(edit, onSaved)
    var text by remember(row.accountId) {
        mutableStateOf(formatAccountMappingLines(row.modelMapping))
    }
    val parsed = parseAccountMappingLines(text)
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        ChannelTextField(
            value = text,
            onValueChange = { text = it },
            label = "映射（一行一条）",
            placeholder = "gpt-4o=gpt-4o-2024-11-20\nclaude-3-5=claude-sonnet-4",
            singleLine = false,
            minHeight = 120,
        )
        Text(
            text = "格式：`客户端模型名=该账号真实上游模型名`，一行一条；`#` 开头是注释。" +
                "共 ${parsed.size} 条生效。" +
                "留空提交 = 清除全部映射（服务端整表替换，不是增量）。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        EditFailureLine(edit, row.accountId, AccountEditKind.MAPPING)
        McaButton(
            onClick = {
                onAction(
                    ChannelsAction.SetAccountMapping(
                        providerId = row.providerId,
                        accountId = row.accountId,
                        mapping = parsed,
                    ),
                )
            },
            style = McaButtonStyle.Primary,
            enabled = !saving && parsed != row.modelMapping,
        ) { Text(if (saving) "保存中…" else "保存映射") }
    }
}

@Composable
private fun AccountPresetsBody(    row: AccountQuotaRow,
    presetsState: PresetsUiState,
    saving: Boolean,
    onAction: (ChannelsAction) -> Unit,
) {
    when (presetsState) {
        PresetsUiState.Idle, PresetsUiState.Loading -> Text(
            text = "正在读取预设…",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        is PresetsUiState.Failed -> Text(
            text = "${presetsState.message}（错误码：${presetsState.code}）",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        is PresetsUiState.Ready -> {
            val view = presetsState.view
            var selected by remember(row.providerId, view) {
                mutableStateOf(view.providerPresets[row.providerId].orEmpty().toSet())
            }
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (view.presets.isEmpty()) {
                    Text(
                        text = "还没有预设；先到桌面端「设置 → 预设」新建，再回来挂载。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    view.presets.forEach { preset ->
                        PresetRow(
                            preset = preset,
                            selected = preset.id in selected,
                            enabled = !saving,
                            onToggle = {
                                selected = if (preset.id in selected) selected - preset.id else selected + preset.id
                            },
                        )
                    }
                }
                Text(
                    text = "挂载是渠道级：同一渠道的所有账号共享这些预设；全部取消 = 解绑。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                McaButton(
                    onClick = { onAction(ChannelsAction.AttachPresets(row.providerId, selected.toList())) },
                    style = McaButtonStyle.Primary,
                    enabled = !saving,
                ) { Text(if (saving) "保存中…" else "保存挂载") }
            }
        }
    }
}

@Composable
private fun PresetRow(
    preset: RequestPreset,
    selected: Boolean,
    enabled: Boolean,
    onToggle: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, onClick = onToggle)
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        SelectionMark(selected = selected)
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = preset.name,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = buildString {
                    append("Body ${preset.bodyCount} · Headers ${preset.headerCount}")
                    if (preset.hasSystemPrompt) append(" · 提示词")
                    if (!preset.enabled) append(" · 已停用")
                    if (preset.description.isNotBlank()) append(" · ${preset.description}")
                },
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * 详情表单 → 请求补丁：**只有和原值不同**的字段才非空（服务端缺省=不修改）。
 *
 * - 权重缺省按 1 算（服务端 omitempty，0 表示没设过）；
 * - 并发清空 = `-1`（跟随全局，`accounts_api.go:103-108`）；
 * - 白名单只在用户真的点了"清空"且原本非空时才发 `[]`。
 */
internal fun accountSettingsPatch(
    row: AccountQuotaRow,
    priorityText: String,
    weightText: String,
    concurrencyText: String,
    disabledDraft: Boolean,
    clearAllowedModels: Boolean,
): AccountSettingsPatch {
    val baseWeight = if (row.weight <= 0) 1 else row.weight
    val priority = priorityText.trim().toIntOrNull()?.coerceAtLeast(0)?.takeIf { it != row.priority }
    val weight = weightText.trim().toIntOrNull()?.coerceIn(1, 1000)?.takeIf { it != baseWeight }
    val concurrencyText2 = concurrencyText.trim()
    val concurrency = if (concurrencyText2.isEmpty()) {
        // 清空输入 = 跟随全局；原本就是"跟随全局"时不算改动
        if (row.concurrency == null) null else -1
    } else {
        concurrencyText2.toIntOrNull()?.coerceAtLeast(0)?.takeIf { it != row.concurrency }
    }
    return AccountSettingsPatch(
        disabled = if (disabledDraft != row.disabled) disabledDraft else null,
        priority = priority,
        weight = weight,
        concurrency = concurrency,
        clearAllowedModels = clearAllowedModels && row.allowedModels.isNotEmpty(),
    )
}

/** 「复制配置」的 JSON：只含接入地址、渠道 id、账号名——**凭据永不进剪贴板**。 */
internal fun accountConfigJson(baseUrl: String, providerId: String, accountLabel: String): String {
    val json = JSONObject()
    json.put("base_url", baseUrl)
    json.put("provider", providerId)
    json.put("account", accountLabel)
    return json.toString()
}

/* ========================================================================== */
/* 自定义渠道（custom-channels）：注册一个新渠道行                              */
/* ========================================================================== */

/**
 * 「自定义渠道」弹窗（桌面 `channels.js:177` → `customchannels.js:67` 抽屉）。
 *
 * 与「自定义供应商（走内置协议）」的区别写在顶部说明里：
 * 这里保存的是**新渠道定义**（provider id = `custom-<slug>`），密钥仍在账号里；
 * 探测用的 Key 只用于本次请求，不保存。
 */
@Composable
private fun CustomChannelsDialog(
    status: GatewayStatus,
    state: CustomChannelsUiState,
    saving: Boolean,
    deletingId: String?,
    probe: CustomChannelProbeUiState,
    error: String?,
    canAttach: Boolean,
    onSave: (CustomChannelDraft, (CustomChannel) -> Unit) -> Unit,
    onDelete: (String, () -> Unit) -> Unit,
    onProbe: (String, String) -> Unit,
    onDismissProbe: () -> Unit,
    onClearError: () -> Unit,
    onOpenOnboard: () -> Unit,
    onDismiss: () -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var channelId by remember { mutableStateOf("") }
    var baseUrl by remember { mutableStateOf("") }
    var protocol by remember { mutableStateOf("openai") }
    var modelsText by remember { mutableStateOf("") }
    var headersText by remember { mutableStateOf("") }
    var probeKey by remember { mutableStateOf("") }
    var editingId by remember { mutableStateOf<String?>(null) }
    var pendingDelete by remember { mutableStateOf<CustomChannel?>(null) }

    fun fill(channel: CustomChannel) {
        editingId = channel.id
        name = channel.name
        channelId = channel.id
        baseUrl = channel.baseUrl
        protocol = channel.protocol
        modelsText = channel.models.joinToString("\n")
        headersText = customChannelHeadersToText(channel.headers)
        onDismissProbe()
        onClearError()
    }

    fun reset() {
        editingId = null
        name = ""
        channelId = ""
        baseUrl = ""
        protocol = "openai"
        modelsText = ""
        headersText = ""
        onDismissProbe()
        onClearError()
    }

    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "自定义渠道（新渠道行）",
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 520.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Text(
                    text = "把「地址 + 模型名」注册成一个新渠道（provider id = custom-<slug>），保存后会出现在渠道列表与接入流程里。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text(
                    text = "密钥不在这里保存：它只放在账号里；下面的「探测 Key」只用于这一次请求。删除渠道只删定义、不删账号。" +
                        "若你只是想给已有渠道加一个账号，请用「自定义供应商（走内置协议）」——那不会新增渠道行。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )

                Text(
                    text = "已保存的渠道",
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                when (state) {
                    CustomChannelsUiState.Idle, CustomChannelsUiState.Loading -> Text(
                        text = "正在读取…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is CustomChannelsUiState.Failed -> Text(
                        text = "${state.message}（${state.code}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is CustomChannelsUiState.Ready -> {
                        if (state.channels.isEmpty()) {
                            Text(
                                text = "还没有自定义渠道；填下面的表单保存一个。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            state.channels.forEach { channel ->
                                CustomChannelRow(
                                    channel = channel,
                                    deleting = deletingId == channel.id,
                                    canAttach = canAttach,
                                    onEdit = { fill(channel) },
                                    onDelete = { pendingDelete = channel },
                                    onProbe = { onProbe(channel.baseUrl, probeKey) },
                                    onAttach = {
                                        onDismiss()
                                        onOpenOnboard()
                                    },
                                )
                                Spacer(Modifier.height(8.dp))
                            }
                        }
                    }
                }

                pendingDelete?.let { channel ->
                    CustomChannelDeleteConfirm(
                        channel = channel,
                        deleting = deletingId == channel.id,
                        onConfirm = {
                            onDelete(channel.id) { pendingDelete = null }
                        },
                        onCancel = { pendingDelete = null },
                    )
                }

                Text(
                    text = if (editingId == null) "新建渠道" else "编辑渠道：custom-$editingId",
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                ChannelTextField(name, { name = it }, "渠道名称", "例如：公司中转")
                ChannelTextField(
                    channelId,
                    { channelId = it },
                    "渠道 ID（选填）",
                    "留空按名称生成",
                )
                val finalSlug = slugifyCustomChannelId(if (channelId.isBlank()) name else channelId)
                Text(
                    text = if (finalSlug.isBlank()) {
                        "最终 id 为 custom-<slug>：保存时按名称生成"
                    } else {
                        "最终 id：custom-$finalSlug"
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                ChannelTextField(baseUrl, { baseUrl = it }, "Base URL", "https://api.example.com/v1")
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    CUSTOM_CHANNEL_PROTOCOLS.forEach { option ->
                        McaChip(
                            selected = protocol == option.value,
                            onClick = { protocol = option.value },
                            label = { Text(option.label, maxLines = 1) },
                        )
                    }
                }
                Text(
                    text = "协议决定上游路径：openai → /chat/completions；anthropic → /messages；responses → /responses。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                ChannelTextField(
                    modelsText,
                    { modelsText = it },
                    "模型（逗号或换行）",
                    "gpt-4o, claude-sonnet-4；留空表示任意模型透传",
                    singleLine = false,
                    minHeight = 72,
                )
                ChannelTextField(
                    headersText,
                    { headersText = it },
                    "额外请求头（每行 Key: Value）",
                    "X-Custom-Header: value\n值里可以含冒号；Authorization 等由网关管理",
                    singleLine = false,
                    minHeight = 72,
                )
                ChannelTextField(
                    probeKey,
                    { probeKey = it },
                    "探测用 API Key（不保存）",
                    "sk-…（只用于本次探测）",
                    visualTransformation = PasswordVisualTransformation(),
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { onProbe(baseUrl, probeKey) },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = baseUrl.isNotBlank() && probe !is CustomChannelProbeUiState.Probing,
                    ) { Text(if (probe is CustomChannelProbeUiState.Probing) "探测中…" else "探测模型") }
                }
                CustomChannelProbeResult(probe = probe, onDismiss = onDismissProbe)
                error?.let {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(onClick = { reset() }, style = McaButtonStyle.Outlined) { Text("新建") }
                McaButton(
                    onClick = {
                        if (status.running) {
                            onSave(
                                CustomChannelDraft(
                                    name = name,
                                    id = channelId,
                                    baseUrl = baseUrl,
                                    protocol = protocol,
                                    modelsText = modelsText,
                                    headersText = headersText,
                                ),
                            ) { saved ->
                                editingId = saved.id
                                channelId = saved.id
                            }
                        }
                    },
                    style = McaButtonStyle.Primary,
                    enabled = !saving && status.running,
                ) { Text(if (saving) "保存中…" else "保存") }
            }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

@Composable
private fun CustomChannelRow(
    channel: CustomChannel,
    deleting: Boolean,
    canAttach: Boolean,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
    onProbe: () -> Unit,
    onAttach: () -> Unit,
) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text(
                text = channel.name,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "custom-${channel.id} · ${customChannelProtocolLabel(channel.protocol)}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = channel.baseUrl,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (channel.models.isNotEmpty()) {
                Text(
                    text = "模型：" + channel.models.joinToString("、"),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Row(
                modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                McaButton(
                    onClick = onProbe,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("探测模型", maxLines = 1) }
                McaButton(
                    onClick = onEdit,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("编辑", maxLines = 1) }
                McaButton(
                    onClick = onDelete,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                    enabled = !deleting,
                ) { Text(if (deleting) "删除中…" else "删除", maxLines = 1) }
                if (canAttach) {
                    McaButton(
                        onClick = onAttach,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("接入账号", maxLines = 1) }
                }
            }
        }
    }
}

@Composable
private fun CustomChannelDeleteConfirm(
    channel: CustomChannel,
    deleting: Boolean,
    onConfirm: () -> Unit,
    onCancel: () -> Unit,
) {
    Surface(
        shape = McaShape.CardAction,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = "删除「${channel.name}」？",
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "只删除渠道定义，不删账号；已接入的账号会显示成「渠道不存在」。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = onConfirm,
                    style = McaButtonStyle.Primary,
                    size = McaButtonSize.CardAction,
                    enabled = !deleting,
                ) { Text(if (deleting) "删除中…" else "确认删除", maxLines = 1) }
                McaButton(
                    onClick = onCancel,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                    enabled = !deleting,
                ) { Text("取消", maxLines = 1) }
            }
        }
    }
}

@Composable
private fun CustomChannelProbeResult(probe: CustomChannelProbeUiState, onDismiss: () -> Unit) {
    when (probe) {
        CustomChannelProbeUiState.Idle -> Unit
        CustomChannelProbeUiState.Probing -> Text(
            text = "正在探测上游 /models…",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        is CustomChannelProbeUiState.Failed -> Text(
            text = probe.message,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        is CustomChannelProbeUiState.Ok -> {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(
                    text = if (probe.models.isEmpty()) {
                        "上游没有返回模型清单（可手动填写）"
                    } else {
                        "探测到 ${probe.models.size} 个模型"
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (probe.models.isNotEmpty()) {
                    Text(
                        text = probe.models.joinToString("、"),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                }
                McaButton(
                    onClick = onDismiss,
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("清除探测结果", maxLines = 1) }
            }
        }
    }
}
