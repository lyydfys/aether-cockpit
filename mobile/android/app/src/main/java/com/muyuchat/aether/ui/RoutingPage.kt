package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.RoutingCatalogModel
import com.muyuchat.aether.runtime.routingTemplateLine
import com.muyuchat.aether.runtime.RoutingGroup
import com.muyuchat.aether.runtime.routingChainProviders
import com.muyuchat.aether.runtime.routingGroupMembers
import com.muyuchat.aether.runtime.routingStrategyLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

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
                        text = "路由",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "模型路由组 · 可改组名与成员",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新路由",
                    onClick = onRefresh,
                    enabled = state is RoutingUiState.Ready || state is RoutingUiState.Failed,
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
            RoutingUiState.Idle -> item {
                RoutingStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机路由组。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            RoutingUiState.Loading -> item { RoutingLoadingCard() }
            is RoutingUiState.Failed -> item {
                RoutingErrorCard(state.code, state.message, onRefresh)
            }
            is RoutingUiState.Ready -> {
                val overview = state.overview
                if (overview.groups.isEmpty()) {
                    item {
                        RoutingStateCard(
                            title = "还没有路由组",
                            detail = "在 Windows 端创建模型路由组后，这里会显示路由组列表。",
                        )
                    }
                } else {
                    item {
                        Text(
                            text = "${overview.groups.size} 个路由组 · ${overview.modelCount} 个可选模型",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    items(overview.groups.size) { index ->
                        val group = overview.groups[index]
                        RoutingGroupCard(group, onClick = { onSelect(group) })
                    }
                }
            }
        }
        // 组模板（只读）：服务端给的"怎么组一套路由"的现成方案。
        // 套用模板要写配置，留在桌面端；这里只说明每个模板是否具备条件。
        if (state is RoutingUiState.Ready && state.overview.templates.isNotEmpty()) {
            item {
                Text(
                    text = "组模板",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
            }
            items(state.overview.templates.size) { index ->
                val template = state.overview.templates[index]
                Surface(shape = McaShape.Card, color = McaTonal.fill()) {
                    Column(Modifier.fillMaxWidth().padding(14.dp)) {
                        Text(
                            text = routingTemplateLine(template),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Text(
                            text = "模板只读：套用与调整请到桌面端。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
        item { ModelRedirectSection(running = state is RoutingUiState.Ready) }
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
}

@Composable
private fun RoutingGroupCard(group: RoutingGroup, onClick: () -> Unit) {
    Surface(shape = McaShape.Card, color = McaTonal.fill()) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(role = Role.Button, onClick = onClick)
                .padding(14.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = group.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                RoutingBadge(
                    text = if (group.ready) "可路由" else "未就绪",
                    error = !group.ready,
                )
                if (group.auto) {
                    Spacer(Modifier.width(6.dp))
                    RoutingBadge(text = "自动", error = false)
                }
                if (group.disabled) {
                    Spacer(Modifier.width(6.dp))
                    RoutingBadge(text = "已停用", error = true)
                }
            }
            Spacer(Modifier.height(4.dp))
            Text(
                text = "${group.id} · ${routingStrategyLabel(group.routingEffective)}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = memberSummary(group),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            group.warnings.forEach { warning ->
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "! $warning",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}

private fun memberSummary(group: RoutingGroup): String {
    val ids = routingGroupMembers(group)
    if (ids.isEmpty()) {
        return "未配置成员"
    }
    val names = ids.take(3).joinToString("、") { id ->
        val info = group.memberInfo.firstOrNull { it.id == id }
        if (info == null) {
            id
        } else {
            val provider = info.providerName.ifBlank { id.substringBefore("/") }
            "$provider/${info.name}"
        }
    }
    val rest = ids.size - 3
    return if (rest > 0) "成员 ${ids.size}：$names 等 $rest 个" else "成员 ${ids.size}：$names"
}

@Composable
private fun RoutingBadge(text: String, error: Boolean) {
    val color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        color = color,
    )
}

@Composable
private fun RoutingStateCard(
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
private fun RoutingLoadingCard() {
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
                text = "正在读取路由组…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun RoutingErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "读取路由失败",
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
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
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
                            Text(
                                text = "没有命中的渠道",
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
                                            style = MaterialTheme.typography.bodyMedium,
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
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                val strategies = listOf("order", "smart", "rotate", "pace", "fast")
                strategies.chunked(3).forEach { row ->
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        row.forEach { value ->
                            McaButton(
                                onClick = { draft = value },
                                style = if (draft == value) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                            ) { Text(routingStrategyLabel(value)) }
                        }
                    }
                    Spacer(Modifier.height(6.dp))
                }
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
                    style = MaterialTheme.typography.labelMedium,
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
    Spacer(Modifier.height(8.dp))
    Text(
        text = "成员（草稿 ${draftMembers.size} 个 · 已保存 ${savedMembers.size} 个）",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    if (draftMembers.isEmpty()) {
        Text(
            text = "至少选择一个成员模型；成员为空时保存会由服务端返回 400。",
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
    val shown = filteredCatalog.take(ROUTING_CATALOG_PREVIEW)
    if (shown.isEmpty()) {
        Text(
            text = if (catalog.isEmpty()) "还没有可选模型，请先刷新。" else "没有匹配的模型。",
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
        Spacer(Modifier.height(4.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
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
            style = MaterialTheme.typography.bodyMedium,
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
                style = MaterialTheme.typography.bodyMedium,
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
            style = MaterialTheme.typography.labelMedium,
            color = if (added) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
        )
    }
}

private val RoutingRowButtonPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp)

/** How many picker rows the dialog previews before asking for a narrower query. */
private const val ROUTING_CATALOG_PREVIEW = 8

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
