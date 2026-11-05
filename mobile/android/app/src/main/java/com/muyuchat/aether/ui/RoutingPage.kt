package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.RoutingApiClient
import com.muyuchat.aether.runtime.RoutingCatalogModel
import com.muyuchat.aether.runtime.RoutingGroup
import com.muyuchat.aether.runtime.RoutingPreset
import com.muyuchat.aether.runtime.RoutingPresets
import com.muyuchat.aether.runtime.RoutingTemplate
import com.muyuchat.aether.runtime.attachRoutingPresets
import com.muyuchat.aether.runtime.fetchRoutingPresets
import com.muyuchat.aether.runtime.routingChainProviders
import com.muyuchat.aether.runtime.routingGroupMembers
import com.muyuchat.aether.runtime.routingStrategyLabel
import com.muyuchat.aether.runtime.routingTemplateLine
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.Locale

/*
 * 路由页。
 *
 * 版面（UI_REFRESH_SPEC §1）：页头 → 路由组（一组一张卡）→ 组模板（**一张**卡里排行）
 * → 模型重定向（隔壁文件，只负责给区块间距）→ 口径说明。
 *
 * 三处对齐：
 *  1. 组模板卡里的模板行与路由组卡里的内容左缘都由卡片的 16dp 内边距决定，严格同一条竖线；
 *  2. 组卡内的「成员」计数用左标签 + 右数值，数值右缘对齐；
 *  3. 成员编辑器里的「草稿 / 已保存」同样右对齐。
 */

/** 目录预览行数：超过就问用户缩小范围，而不是把 200 个模型全画出来。 */
private const val RoutingCatalogPreview: Int = 8

/** 路由组内成员计数：`online` 只按服务端的 `providerOff` 判定，不猜账号健康度。 */
internal data class RoutingMemberTally(
    val total: Int,
    val online: Int,
    val unresolved: Int,
)

@Composable
internal fun RoutingPage(
    status: GatewayStatus,
    state: RoutingUiState,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    selected: RoutingGroup? = null,
    preview: RoutePreviewState = RoutePreviewState.Idle,
    saveState: RouteSaveState = RouteSaveState.Idle,
    onSelect: (RoutingGroup) -> Unit = {},
    onRetryPreview: () -> Unit = {},
    onDismissPreview: () -> Unit = {},
    onSaveStrategy: (String) -> Unit = {},
    catalog: List<RoutingCatalogModel> = emptyList(),
    filteredCatalog: List<RoutingCatalogModel> = emptyList(),
    memberQuery: String = "",
    draftName: String = "",
    draftMembers: List<String> = emptyList(),
    onMemberQueryChange: (String) -> Unit = {},
    onAddMember: (String) -> Unit = {},
    onRemoveMember: (String) -> Unit = {},
    onMoveMember: (String, Boolean) -> Unit = { _, _ -> },
    onDraftNameChange: (String) -> Unit = {},
    onSaveDraft: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    val presetsViewModel: RoutingPresetsViewModel = viewModel()
    val presetsState by presetsViewModel.state.collectAsStateWithLifecycle()
    var presetTarget by remember { mutableStateOf<RoutingGroup?>(null) }
    LaunchedEffect(Unit) { presetsViewModel.reload() }
    val presets = (presetsState as? RoutingPresetsUiState.Ready)?.presets
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                PageHeadline(
                    onBack = onBack,
                    onRefresh = onRefresh,
                    refreshEnabled = state is RoutingUiState.Ready || state is RoutingUiState.Failed,
                )
            }
        }
        when (state) {
            RoutingUiState.Idle -> item {
                DeepStateCard(
                    title = "网关未启动",
                    detail = "路由组来自本机 /api/groups；网关没起来时这个端点没有服务可应答。" +
                        "先启动网关，再回本页读取路由组。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            RoutingUiState.Loading -> item { RoutingLoadingCard() }
            is RoutingUiState.Failed -> item {
                DeepStateCard(
                    title = "读取路由失败",
                    detail = "${state.message}（错误码：${state.code}）。这次没有拿到路由组，" +
                        "所以下面不显示上一次的旧数据。",
                    tone = DeepTone.Danger,
                    actionLabel = "重试",
                    onAction = onRefresh,
                )
            }
            is RoutingUiState.Ready -> {
                val overview = state.overview
                if (overview.groups.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "还没有路由组",
                            detail = routingGroupsEmptyDetail(),
                            actionLabel = "重新读取",
                            onAction = onRefresh,
                        )
                    }
                } else {
                    item {
                        DeepSectionTitle(
                            text = "路由组",
                            trailing = "${overview.groups.size} 组 · ${overview.modelCount} 个可选模型",
                        )
                    }
                    items(
                        count = overview.groups.size,
                        key = { index -> overview.groups[index].id },
                    ) { index ->
                        val group = overview.groups[index]
                        val attached = presets?.routePresets?.get(group.id).orEmpty()
                        val attachedNames = attached.map { id ->
                            presets?.presets?.firstOrNull { it.id == id }?.name ?: id
                        }
                        RoutingGroupCard(
                            group = group,
                            attachedPresetNames = attachedNames,
                            onOpenPresets = { presetTarget = group },
                            onClick = { onSelect(group) },
                        )
                    }
                }
                // 组模板（只读）：服务端给的"怎么组一套路由"的现成方案。
                // 套用模板要写配置，留在桌面端；这里只说明每个模板是否具备条件。
                if (overview.templates.isNotEmpty()) {
                    item {
                        DeepSectionTitle(
                            text = "组模板",
                            trailing = "${overview.templates.size} 个",
                        )
                    }
                    item { TemplatesCard(overview.templates) }
                }
            }
        }
        item {
            ModelRedirectSection(
                running = state is RoutingUiState.Ready,
                modifier = Modifier.padding(top = DeepBlockGap - DeepCardGap),
            )
        }
        item {
            Text(
                text = "点击路由组可查看解析链、修改组名与成员；保存以组 id 为 from 基准，失败原因由服务端返回。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (selected != null) {
        RoutePreviewDialog(
            group = selected,
            preview = preview,
            saveState = saveState,
            onRetry = onRetryPreview,
            onDismiss = onDismissPreview,
            onSave = onSaveStrategy,
            catalog = catalog,
            filteredCatalog = filteredCatalog,
            memberQuery = memberQuery,
            draftName = draftName,
            draftMembers = draftMembers,
            onMemberQueryChange = onMemberQueryChange,
            onAddMember = onAddMember,
            onRemoveMember = onRemoveMember,
            onMoveMember = onMoveMember,
            onDraftNameChange = onDraftNameChange,
            onSaveDraft = onSaveDraft,
        )
    }
    presetTarget?.let { group ->
        RoutingPresetDialog(
            group = group,
            state = presetsState,
            saving = presetsViewModel.saving.collectAsStateWithLifecycle().value,
            saveError = presetsViewModel.error.collectAsStateWithLifecycle().value,
            onRetry = presetsViewModel::reload,
            onDismiss = { presetTarget = null },
            onSave = { ids -> presetsViewModel.attach(group.id, ids) { presetTarget = null } },
        )
    }
}

/** 空态：为什么空（手机端不能新建组）+ 下一步（桌面端建组 → 回本页重新读取）。 */
internal fun routingGroupsEmptyDetail(): String =
    "网关这次返回的路由组列表是空的。路由组是桌面端建好的模型回退链，手机端只读，" +
        "不会替你新建一条。" +
        "去桌面端建一个组，再回本页点「重新读取」。"

/** 组内成员的在线计数：`memberInfo` 缺条目算「未解析」，不猜成离线。 */
internal fun routingGroupMemberTally(group: RoutingGroup): RoutingMemberTally {
    val ids = routingGroupMembers(group)
    val infoById = group.memberInfo.associateBy { it.id }
    var online = 0
    var unresolved = 0
    ids.forEach { id ->
        val info = infoById[id]
        when {
            info == null -> unresolved++
            !info.providerOff -> online++
        }
    }
    return RoutingMemberTally(total = ids.size, online = online, unresolved = unresolved)
}

/** 右对齐的成员计数文案。 */
internal fun routingMemberTallyText(tally: RoutingMemberTally): String = when {
    tally.total == 0 -> "未配置"
    tally.unresolved > 0 -> "${tally.online}/${tally.total} 在线 · ${tally.unresolved} 个未解析"
    else -> "${tally.online}/${tally.total} 在线"
}

/** 成员名字摘要（计数已经在右侧单独给出，这里不再重复「成员 N」）。 */
internal fun routingMemberNamesText(group: RoutingGroup): String {
    val ids = routingGroupMembers(group)
    if (ids.isEmpty()) {
        return ""
    }
    val shown = ids.take(3).map { id -> routingMemberLabel(id, emptyList(), group) }
    val rest = ids.size - 3
    return if (rest > 0) {
        shown.joinToString("、") + " 等 $rest 个"
    } else {
        shown.joinToString("、")
    }
}

/* ---------------- 成员倍率（credit_rate）：数据层早就解析了，这里才上屏 ---------------- */

/**
 * 倍率文案：桌面 `misc.js:193` 用 `（×1.5）` 这种括号后缀，`memberInfo[].credit_rate`
 * 来自 `groups_api.go:28`（`json:"credit_rate,omitempty"`）。
 *
 * 缺省即 0（服务端 omitempty）→ **返回空串，绝不渲染 `×0`**：0 是"没配倍率"，不是"倍率是 0"。
 * 负数/NaN/Inf 同样返回空串：倍率是计费乘数，负数和无穷都说明数据坏了，显示出来只会误导。
 * 整数倍率不带小数点（`×2` 而不是 `×2.0`），与桌面 JS 的 `'×' + m.credit_rate` 输出一致。
 */
internal fun memberCreditLabel(rate: Double): String {
    if (rate.isNaN() || rate.isInfinite() || rate <= 0.0) {
        return ""
    }
    // 最多 4 位小数再去掉尾零：1.5 → "1.5"、2.0 → "2"、0.0001 → "0.0001"。
    // 不用 `rate.toString()`：它会把 1.0E-4 这种小倍率印成 "1.0E-4"，用户读不出乘数。
    val text = String.format(Locale.US, "%.4f", rate).trimEnd('0').trimEnd('.')
    if (text.isEmpty() || text == "0") {
        // 小到 4 位小数显示不出来（<0.00005）：与其显示 "×0"，不如不显示。
        return ""
    }
    return "×$text"
}

/**
 * 成员 chip 文案（桌面同款形态：`providerName · name（×1.5）`，`misc.js:193`）。
 *
 * `provider`/`name` 缺哪个就省哪个（用 `·` 连接非空部分），倍率为空时不带括号——
 * 空括号 `（）` 比不显示更难看，也更容易被误读成"倍率是空值"。
 */
internal fun routingMemberChipLabel(providerName: String, name: String, creditRate: Double): String {
    val parts = listOf(providerName.trim(), name.trim()).filter { it.isNotEmpty() }
    val base = parts.joinToString(" · ")
    if (base.isEmpty()) {
        // 连名字都没有的 chip 只剩一个「（×2）」，读不出是什么——这种就不显示。
        return ""
    }
    val credit = memberCreditLabel(creditRate)
    return if (credit.isEmpty()) base else "$base（$credit）"
}

/**
 * 组卡上要展示的成员 chip：按 `members` 里的顺序（与桌面 `memberInfo` 的顺序一致，
 * 因为服务端就是按 members 展开的），最多 [limit] 个（桌面 `members.slice(0, 12)`）。
 *
 * 名字优先用 `memberInfo`（含 provider 显示名）；没有 memberInfo 的成员退化成裸 id，
 * 不猜它属于哪个渠道。空 id 直接跳过。
 */
internal fun routingMemberChips(group: RoutingGroup, limit: Int = 12): List<String> {
    val safeLimit = limit.coerceAtLeast(0)
    if (safeLimit == 0) {
        return emptyList()
    }
    val infoById = group.memberInfo.associateBy { it.id }
    return routingGroupMembers(group)
        .filter { it.isNotBlank() }
        .map { id ->
            val info = infoById[id]
            if (info == null) {
                routingMemberChipLabel("", id, 0.0)
            } else {
                routingMemberChipLabel(info.providerName, info.name.ifBlank { id }, info.creditRate)
            }
        }
        .take(safeLimit)
}

/** 成员数超过 chip 上限时的尾巴（`+3`）；没超过返回空串。 */
internal fun routingMemberOverflowLabel(group: RoutingGroup, limit: Int = 12): String {
    val total = routingGroupMembers(group).count { it.isNotBlank() }
    val rest = total - limit.coerceAtLeast(0)
    return if (rest > 0) "+$rest" else ""
}

@Composable
private fun PageHeadline(onBack: () -> Unit, onRefresh: () -> Unit, refreshEnabled: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        AetherHeaderIconButton(
            iconRes = R.drawable.ic_back,
            contentDescription = "返回",
            onClick = onBack,
            tint = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            DeepPageTitle(
                title = "路由",
                subtitle = "模型路由组与成员",
            )
        }
        AetherHeaderIconButton(
            iconRes = R.drawable.ic_refresh,
            contentDescription = "刷新路由",
            onClick = onRefresh,
            enabled = refreshEnabled,
        )
    }
}

/**
 * 一张路由组卡：标题行（组名 + 状态徽标）→ id/策略 → 「成员」计数（右对齐）→ 成员名 → 警告。
 *
 * `clip(McaShape.Sheet)` 在 `clickable` 之前，波纹被裁在圆角里；整卡可点，包括 16dp 内边距。
 */
@Composable
private fun RoutingGroupCard(
    group: RoutingGroup,
    attachedPresetNames: List<String>,
    onOpenPresets: () -> Unit,
    onClick: () -> Unit,
) {
    val tally = routingGroupMemberTally(group)
    DeepCard(
        modifier = Modifier
            .aetherPressable()
            .clip(McaShape.Sheet)
            .clickable(role = Role.Button, onClick = onClick),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = group.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            RoutingBadge(
                text = if (group.ready) "可路由" else "未就绪",
                error = !group.ready,
            )
            if (group.auto) {
                Spacer(Modifier.width(6.dp))
                RoutingBadge(text = "自动", error = false)
            } else {
                Spacer(Modifier.width(6.dp))
                RoutingBadge(text = "手动", error = false)
            }
            if (group.disabled) {
                Spacer(Modifier.width(6.dp))
                RoutingBadge(text = "已停用", error = true)
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            text = group.id,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        // 桌面组卡的三枚 chip：自动/手动（上面）、策略、规则条数（misc.js:201-203）。
        Spacer(Modifier.height(6.dp))
        Text(
            text = buildString {
                append("策略 ").append(routingStrategyLabel(group.routingEffective))
                if (group.ruleCount > 0) {
                    append(" · ").append(group.ruleCount).append(" 条规则")
                }
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(10.dp))
        CountRow(
            label = "成员",
            value = routingMemberTallyText(tally),
            valueColor = if (tally.total > 0 && tally.online == 0) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurface
            },
        )
        // 成员 chip：`provider · name（×1.5）`（桌面 `misc.js:193` 同款形态）。
        // 单行横滚——成员多时往右滚，1.0×/1.3× 都不会挤成竖排单字，也不吃第二行高度。
        val chips = routingMemberChips(group)
        if (chips.isNotEmpty()) {
            Spacer(Modifier.height(6.dp))
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                chips.forEach { label ->
                    RoutingMemberChip(label)
                }
                val overflow = routingMemberOverflowLabel(group)
                if (overflow.isNotEmpty()) {
                    Text(
                        text = overflow,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                    )
                }
            }
        }
        Spacer(Modifier.height(6.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = if (attachedPresetNames.isEmpty()) {
                    "无预设"
                } else {
                    "预设: " + attachedPresetNames.take(3).joinToString("、") +
                        if (attachedPresetNames.size > 3) "…" else ""
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.width(8.dp))
            AetherPillButton(
                text = "路由预设",
                onClick = onOpenPresets,
            )
        }
        group.warnings.forEach { warning ->
            Spacer(Modifier.height(6.dp))
            Text(
                text = "! $warning",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

/** 左标签 + 右数值：同一张卡里所有计数/状态都用这一种收边，右缘因此严格对齐。 */
@Composable
private fun CountRow(
    label: String,
    value: String,
    valueColor: Color = MaterialTheme.colorScheme.onSurface,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Medium,
            color = valueColor,
        )
    }
}

/** 组模板卡：一张卡里排行，和路由组卡共用 16dp 内容内边距，左缘因此对齐。 */
@Composable
private fun TemplatesCard(templates: List<RoutingTemplate>) {
    DeepCard {
        DeepCardBody("套用模板要写网关配置，只能在桌面端做；这里只说明每个模板现在的成员够不够。")
        templates.forEach { template ->
            Spacer(Modifier.height(12.dp))
            Text(
                text = routingTemplateLine(template),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
        }
    }
}

@Composable
private fun RoutingBadge(text: String, error: Boolean) {
    AetherStatusChip(
        text = text,
        containerColor = if (error) AetherColors.dangerContainer() else MaterialTheme.colorScheme.primaryContainer,
        contentColor = if (error) AetherColors.danger() else MaterialTheme.colorScheme.primary,
    )
}

/**
 * 成员 chip。单行不换行（`maxLines = 1`）——外层是横滚容器，长名字往右滚，
 * 绝不会折成"每行一个字"的竖排；倍率作为同一段文字的后缀（`（×1.5）`），基线天然对齐。
 */
@Composable
private fun RoutingMemberChip(text: String) {
    AetherStatusChip(
        text = text,
        containerColor = AetherColors.subSurface(),
        contentColor = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun RoutingLoadingCard() {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = "正在读取路由组…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * 路由预设对话框（桌面 `routePresetDialog`，misc.js:275-310）：
 * 只做「这个组挂载哪些已保存预设」；新建/停用预设仍在桌面端 设置 → 预设。
 */
@Composable
private fun RoutingPresetDialog(
    group: RoutingGroup,
    state: RoutingPresetsUiState,
    saving: Boolean,
    saveError: String?,
    onRetry: () -> Unit,
    onDismiss: () -> Unit,
    onSave: (List<String>) -> Unit,
) {
    val ready = state as? RoutingPresetsUiState.Ready
    var selected by remember(group.id, ready) {
        mutableStateOf(ready?.presets?.routePresets?.get(group.id).orEmpty().toSet())
    }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("路由预设 · ${group.id}", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                when (state) {
                    RoutingPresetsUiState.Idle, RoutingPresetsUiState.Loading -> Text(
                        text = "正在读取预设…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is RoutingPresetsUiState.Failed -> {
                        Text(
                            text = "${state.message}（错误码：${state.code}）",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        Spacer(Modifier.height(8.dp))
                        McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) { Text("重试") }
                    }
                    is RoutingPresetsUiState.Ready -> {
                        if (state.presets.presets.isEmpty()) {
                            Text(
                                text = "还没有预设；先到桌面端「设置 → 预设」新建，再回来挂载。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            Text(
                                text = "勾选后在该路由组命中时生效；不勾选 = 不带路由预设，渠道预设仍会生效。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Spacer(Modifier.height(8.dp))
                            state.presets.presets.forEach { preset ->
                                val attached = preset.id in selected
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .clickable(role = Role.Checkbox) {
                                            selected = if (attached) selected - preset.id else selected + preset.id
                                        }
                                        .padding(vertical = 10.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Column(Modifier.weight(1f)) {
                                        Text(
                                            text = preset.name,
                                            style = MaterialTheme.typography.bodyMedium,
                                            color = MaterialTheme.colorScheme.onSurface,
                                        )
                                        Text(
                                            text = presetLine(preset),
                                            style = MaterialTheme.typography.labelSmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                    Spacer(Modifier.width(8.dp))
                                    Text(
                                        text = if (attached) "已挂载" else "挂载",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = if (attached) {
                                            MaterialTheme.colorScheme.primary
                                        } else {
                                            MaterialTheme.colorScheme.onSurfaceVariant
                                        },
                                    )
                                }
                            }
                        }
                    }
                }
                saveError?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = { onSave(selected.toList()) },
                style = McaButtonStyle.Primary,
                enabled = ready != null && !saving,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

/** 预设副文案：停用 / 提示词 / Body·Headers 计数（桌面 misc.js:290-292）。 */
internal fun presetLine(preset: RoutingPreset): String = buildString {
    if (!preset.enabled) {
        append("停用 · ")
    }
    if (preset.hasSystemPrompt) {
        append("提示词 · ")
    }
    append("Body ").append(preset.bodyCount).append(" · Headers ").append(preset.headerCount)
    if (preset.description.isNotBlank()) {
        append(" · ").append(preset.description)
    }
}

sealed interface RoutingPresetsUiState {
    data object Idle : RoutingPresetsUiState
    data object Loading : RoutingPresetsUiState
    data class Ready(val presets: RoutingPresets) : RoutingPresetsUiState
    data class Failed(val code: String, val message: String) : RoutingPresetsUiState
}

/** 预设列表 / 挂载（自持状态，避免往 GatewayHome 增参）。 */
class RoutingPresetsViewModel(application: android.app.Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val _state = MutableStateFlow<RoutingPresetsUiState>(RoutingPresetsUiState.Idle)
    val state: StateFlow<RoutingPresetsUiState> = _state.asStateFlow()
    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()
    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    fun reload() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = RoutingPresetsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (_state.value !is RoutingPresetsUiState.Ready) {
            _state.value = RoutingPresetsUiState.Loading
        }
        viewModelScope.launch {
            _state.value = try {
                RoutingPresetsUiState.Ready(withContext(Dispatchers.IO) { fetchRoutingPresets(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                RoutingPresetsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                RoutingPresetsUiState.Failed("unknown", "读取预设失败，请重试")
            }
        }
    }

    fun attach(groupId: String, ids: List<String>, onDone: () -> Unit) {
        if (_saving.value) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _error.value = "网关未启动"
            return
        }
        _error.value = null
        _saving.value = true
        viewModelScope.launch {
            try {
                _state.value = RoutingPresetsUiState.Ready(
                    withContext(Dispatchers.IO) { attachRoutingPresets(status, groupId, ids) },
                )
                onDone()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _error.value = e.message
            } catch (_: Exception) {
                _error.value = "保存预设失败，请重试"
            } finally {
                _saving.value = false
            }
        }
    }
}

@Composable
private fun RoutePreviewDialog(
    group: RoutingGroup,
    preview: RoutePreviewState,
    saveState: RouteSaveState,
    onRetry: () -> Unit,
    onDismiss: () -> Unit,
    onSave: (String) -> Unit,
    catalog: List<RoutingCatalogModel>,
    filteredCatalog: List<RoutingCatalogModel>,
    memberQuery: String,
    draftName: String,
    draftMembers: List<String>,
    onMemberQueryChange: (String) -> Unit,
    onAddMember: (String) -> Unit,
    onRemoveMember: (String) -> Unit,
    onMoveMember: (String, Boolean) -> Unit,
    onDraftNameChange: (String) -> Unit,
    onSaveDraft: () -> Unit,
) {
    var draft by remember(group.id) { mutableStateOf(group.routingEffective) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = group.name,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            // Member editing adds a lot of rows; the dialog keeps them all reachable.
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(
                    text = "${group.id} · ${routingStrategyLabel(group.routingEffective)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                group.warnings.forEach { warning ->
                    Text(
                        text = "! $warning",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(10.dp))
                Text(
                    text = "解析结果（只读诊断）",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                when (preview) {
                    RoutePreviewState.Idle, RoutePreviewState.Loading -> Row(
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(18.dp),
                            strokeWidth = 2.dp,
                            color = MaterialTheme.colorScheme.primary,
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(
                            text = "正在解析路由链…",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    is RoutePreviewState.Failed -> {
                        Text(
                            text = preview.message,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        Spacer(Modifier.height(6.dp))
                        McaButton(onClick = onRetry, style = McaButtonStyle.Outlined) { Text("重试") }
                    }
                    is RoutePreviewState.Ready -> {
                        val bindings = preview.preview.bindings
                        if (bindings.isEmpty()) {
                            // 空态：说清为什么是空的 + 下一步该看哪里。
                            Text(
                                text = "没有命中的渠道：这组的成员当前都解析不到可用渠道。" +
                                    "先看下面成员行里的「渠道当前离线」提示，或到「额度」页确认渠道是启用状态。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            bindings.forEach { binding ->
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(vertical = 4.dp),
                                ) {
                                    Column(Modifier.weight(1f)) {
                                        Text(
                                            text = binding.name,
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurface,
                                        )
                                        Text(
                                            text = "${binding.upstreamModel} · ${binding.available}/${binding.accounts} 账号可用",
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                    Text(
                                        text = if (binding.ready) "可路由" else "无可用账号",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = if (binding.ready) {
                                            MaterialTheme.colorScheme.primary
                                        } else {
                                            MaterialTheme.colorScheme.error
                                        },
                                    )
                                }
                            }
                        }
                    }
                }
                Spacer(Modifier.height(12.dp))
                Text(
                    text = "生效策略 · 原策略：${routingStrategyLabel(group.routingEffective)}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                val strategies = listOf("", "order", "rotate", "usage", "pace", "smart", "fast")
                strategies.chunked(3).forEach { row ->
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        row.forEach { value ->
                            McaButton(
                                onClick = { draft = value },
                                style = if (draft == value) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                                size = McaButtonSize.CardAction,
                            ) { Text(if (value.isEmpty()) "默认" else routingStrategyLabel(value)) }
                        }
                    }
                    Spacer(Modifier.height(6.dp))
                }
                Text(
                    text = "weight / free / cheapest / fastest 服务端仍未实现（桌面同样只是列出且不生效），手机端不提供。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (saveState is RouteSaveState.Failed) {
                    Text(
                        text = saveState.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(6.dp))
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    McaButton(
                        onClick = { onSave(draft) },
                        style = McaButtonStyle.Primary,
                        enabled = draft != group.routingEffective && saveState !is RouteSaveState.Saving,
                    ) { Text(if (saveState is RouteSaveState.Saving) "保存中…" else "保存策略") }
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = "保存以当前组 id 为 from 基准；失败原因由服务端返回。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(16.dp))
                Text(
                    text = "组名与成员",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                RoutingMembersEditor(
                    group = group,
                    catalog = catalog,
                    filteredCatalog = filteredCatalog,
                    memberQuery = memberQuery,
                    draftName = draftName,
                    draftMembers = draftMembers,
                    saveState = saveState,
                    onMemberQueryChange = onMemberQueryChange,
                    onAddMember = onAddMember,
                    onRemoveMember = onRemoveMember,
                    onMoveMember = onMoveMember,
                    onDraftNameChange = onDraftNameChange,
                    onSaveDraft = onSaveDraft,
                )
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
    )
}

/**
 * Member/name draft editor. The draft is deliberately separate from [group]: a failed save must not
 * look applied, so only a successful server echo replaces the draft.
 */
@Composable
private fun RoutingMembersEditor(
    group: RoutingGroup,
    catalog: List<RoutingCatalogModel>,
    filteredCatalog: List<RoutingCatalogModel>,
    memberQuery: String,
    draftName: String,
    draftMembers: List<String>,
    saveState: RouteSaveState,
    onMemberQueryChange: (String) -> Unit,
    onAddMember: (String) -> Unit,
    onRemoveMember: (String) -> Unit,
    onMoveMember: (String, Boolean) -> Unit,
    onDraftNameChange: (String) -> Unit,
    onSaveDraft: () -> Unit,
) {
    val savedMembers = routingGroupMembers(group)
    val dirty = draftName.trim() != group.name || draftMembers != savedMembers
    val channels = routingChainProviders(draftMembers)

    RoutingEditorField(
        label = "组名（改名后仍以原组 id 提交）",
        value = draftName,
        onValueChange = onDraftNameChange,
    )
    Spacer(Modifier.height(12.dp))
    CountRow(
        label = "成员",
        value = "草稿 ${draftMembers.size} / 已保存 ${savedMembers.size}",
    )
    if (draftMembers.isEmpty()) {
        Text(
            text = "成员是空的：保存会被服务端拒绝（400）。在下面「搜索可选模型」里挑一个，" +
                "点该行的「加入」把它加进草稿。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
    } else {
        draftMembers.forEachIndexed { index, id ->
            RoutingMemberRow(
                label = routingMemberLabel(id, catalog, group),
                id = id,
                offline = routingMemberOffline(id, catalog, group),
                canMoveUp = index > 0,
                canMoveDown = index < draftMembers.lastIndex,
                onMoveUp = { onMoveMember(id, true) },
                onMoveDown = { onMoveMember(id, false) },
                onRemove = { onRemoveMember(id) },
            )
        }
    }
    Spacer(Modifier.height(6.dp))
    Text(
        text = routingChainNote(draftMembers, channels),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(10.dp))
    RoutingEditorField(
        label = "搜索可选模型（${filteredCatalog.size} 个匹配）",
        value = memberQuery,
        onValueChange = onMemberQueryChange,
    )
    val shown = filteredCatalog.take(RoutingCatalogPreview)
    if (shown.isEmpty()) {
        Text(
            text = if (catalog.isEmpty()) {
                "网关这次没有返回可选模型（models 为空）。刷新一次；仍为空就去「额度」页确认至少有一个渠道可用。"
            } else {
                "没有匹配「${memberQuery.trim()}」的模型。删掉几个字，或直接清空搜索框看全部可选模型。"
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    } else {
        shown.forEach { model ->
            RoutingCatalogRow(
                model = model,
                added = draftMembers.contains(model.id),
                onAdd = { onAddMember(model.id) },
            )
        }
        val hidden = filteredCatalog.size - shown.size
        if (hidden > 0) {
            Text(
                text = "还有 $hidden 个匹配，继续输入以缩小范围。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Spacer(Modifier.height(10.dp))
    Row(verticalAlignment = Alignment.CenterVertically) {
        McaButton(
            onClick = onSaveDraft,
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = dirty,
            loading = saveState is RouteSaveState.Saving,
        ) { Text(if (saveState is RouteSaveState.Saving) "保存中…" else "保存成员与组名") }
        Spacer(Modifier.width(8.dp))
        Text(
            text = if (dirty) "草稿未保存：当前生效的仍是已保存配置。" else "草稿与已保存配置一致。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
    }
    when (saveState) {
        is RouteSaveState.Conflict -> Text(
            text = "已有同名路由组：${saveState.message}。草稿已保留，换一个组名后重试。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        // A failure that left the draft clean came from the strategy save, which reports above.
        is RouteSaveState.Failed -> if (dirty) {
            Text(
                text = saveState.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        RouteSaveState.Saved -> if (!dirty) {
            Text(
                text = "已保存：界面已用服务端回读的路由组重建。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
        else -> Unit
    }
}

@Composable
private fun RoutingEditorField(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
                modifier = Modifier
                    .fillMaxWidth()
                    // BasicTextField 不吃 Compose 的触摸目标扩展，自己撑到 48dp。
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
            )
        }
    }
}

@Composable
private fun RoutingMemberRow(
    label: String,
    id: String,
    offline: Boolean,
    canMoveUp: Boolean,
    canMoveDown: Boolean,
    onMoveUp: () -> Unit,
    onMoveDown: () -> Unit,
    onRemove: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = if (offline) "$id · 渠道当前离线" else id,
            style = MaterialTheme.typography.bodySmall,
            color = if (offline) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = onMoveUp,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = canMoveUp,
                contentPadding = RoutingRowButtonPadding,
            ) { Text("上移") }
            McaButton(
                onClick = onMoveDown,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = canMoveDown,
                contentPadding = RoutingRowButtonPadding,
            ) { Text("下移") }
            McaButton(
                onClick = onRemove,
                style = McaButtonStyle.Text,
                size = McaButtonSize.CardAction,
                contentColor = MaterialTheme.colorScheme.error,
                contentPadding = RoutingRowButtonPadding,
            ) { Text("移除") }
        }
    }
}

@Composable
private fun RoutingCatalogRow(
    model: RoutingCatalogModel,
    added: Boolean,
    onAdd: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, enabled = !added, onClick = onAdd)
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = model.name,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "${model.id} · ${model.providerName}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (model.providerOff) {
                Text(
                    text = "渠道当前离线",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
        Text(
            text = if (added) "已加入" else "加入",
            style = MaterialTheme.typography.labelSmall,
            color = if (added) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
        )
    }
}

private val RoutingRowButtonPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp)

private fun routingMemberLabel(
    id: String,
    catalog: List<RoutingCatalogModel>,
    group: RoutingGroup,
): String {
    catalog.firstOrNull { it.id == id }?.let { model ->
        return "${model.providerName}/${model.name}"
    }
    group.memberInfo.firstOrNull { it.id == id }?.let { info ->
        val provider = info.providerName.ifBlank { id.substringBefore("/") }
        return "$provider/${info.name}"
    }
    return id
}

private fun routingMemberOffline(
    id: String,
    catalog: List<RoutingCatalogModel>,
    group: RoutingGroup,
): Boolean = catalog.firstOrNull { it.id == id }?.providerOff
    ?: group.memberInfo.firstOrNull { it.id == id }?.providerOff
    ?: false

/**
 * `chainFromMembers` (groups_action_api.go:41-53) keeps one channel per provider, so several models
 * of the same provider do not add fallback depth. Say that instead of implying one member = one hop.
 */
private fun routingChainNote(members: List<String>, channels: List<String>): String {
    if (members.isEmpty()) {
        return "同一渠道（provider）的多个模型只算一个回退渠道。"
    }
    val providers = if (channels.isEmpty()) "没有可识别的渠道前缀" else channels.joinToString("、")
    return "同一渠道（provider）的多个模型只算一个回退渠道：${members.size} 个成员 → ${channels.size} 条回退渠道（$providers）。"
}
