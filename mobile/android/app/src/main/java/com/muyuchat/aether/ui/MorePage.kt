package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus

/*
 * 「更多」目录页。
 *
 * 1. 修复原本 `WorkspaceEntry.iconRes()` 因旧标题字符串失配导致 10/12 行全部回退为太阳图标的严重 Bug；
 * 2. 引入 8 色低饱和莫兰迪身份底盘 ([AetherIdentityDisc]) 与内缩分割线（Inset Hairline），
 *    赋予目录页旗舰级视觉韵律；
 * 3. 行尾采用低饱和胶囊标签 + 光学墨迹右对齐箭头 ([AetherChevron]) + 弹簧按压回弹 ([aetherPressable])。
 */

private val TitleToRows = 8.dp

@Composable
internal fun MorePage(
    status: GatewayStatus,
    query: String,
    onQueryChange: (String) -> Unit,
    onWorkspace: (WorkspaceEntry) -> Unit,
    onSettings: (SettingsGroup) -> Unit,
    modifier: Modifier = Modifier,
) {
    val searching = query.isNotBlank()
    val workspaces = filterWorkspaces(query)
    val settings = filterSettings(query)
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item(key = "more_header") {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    DeepPageTitle(
                        title = "更多",
                        subtitle = "全部工作区与设置入口",
                    )
                }
                SearchField(query, onQueryChange)
            }
        }
        if (searching) {
            if (workspaces.isEmpty() && settings.isEmpty()) {
                item(key = "more_empty") {
                    Column {
                        DeepSectionTitle(text = "搜索结果", trailing = "0 项")
                        Spacer(Modifier.height(TitleToRows))
                        DeepStateCard(
                            title = "没有匹配的功能",
                            detail = moreSearchEmptyDetail(query),
                            actionLabel = "清除搜索",
                            onAction = { onQueryChange("") },
                        )
                    }
                }
            } else {
                item(key = "more_search_results") {
                    Column {
                        DeepSectionTitle(
                            text = "搜索结果",
                            trailing = "${workspaces.size + settings.size} 项",
                        )
                        Spacer(Modifier.height(TitleToRows))
                        DeepCard(contentPadding = DeepListCardPadding) {
                            if (settings.isNotEmpty()) {
                                SettingsRows(
                                    groups = settings,
                                    onSettings = onSettings,
                                    showBottomDividerOnLast = workspaces.isNotEmpty(),
                                )
                            }
                            if (workspaces.isNotEmpty()) {
                                WorkspaceRows(workspaces, onWorkspace)
                            }
                        }
                    }
                }
            }
        } else {
            item(key = "more_workspaces_group") {
                Column {
                    DeepSectionTitle(
                        text = "全部工作区",
                        trailing = "${workspaceEntries().size} 个",
                    )
                    Spacer(Modifier.height(TitleToRows))
                    DeepCard(contentPadding = DeepListCardPadding) {
                        WorkspaceRows(workspaceEntries(), onWorkspace)
                    }
                }
            }
            item(key = "more_settings_group") {
                Column {
                    DeepSectionTitle(
                        text = "设置分组",
                        trailing = "${settingsGroups().size} 组",
                    )
                    Spacer(Modifier.height(TitleToRows))
                    DeepCard(contentPadding = DeepListCardPadding) {
                        SettingsRows(settingsGroups(), onSettings)
                    }
                }
            }
        }
        item(key = "more_capability_note") { CapabilityNote() }
    }
}

internal fun moreSearchEmptyDetail(query: String): String {
    val needle = query.trim()
    val hitScope = "${settingsGroups().size} 个设置分组与 ${workspaceEntries().size} 个工作区"
    if (needle.isEmpty()) {
        return "目录里共 $hitScope；输入关键词可以按名称或说明过滤。"
    }
    return "「$needle」没有命中 $hitScope 的标题或说明。换个更短的关键词，或清除搜索回到完整目录。"
}

internal fun settingsRowTrailing(group: SettingsGroup): String =
    if (group.available) "已接入" else "后续阶段"

internal fun workspaceRowTrailing(entry: WorkspaceEntry): String {
    val action = entry.action
    return when (action) {
        is WorkspaceAction.Tab ->
            if (action.tab == MobileTab.OVERVIEW) "当前" else action.tab.label
        WorkspaceAction.DesktopOnly -> "仅 Windows"
        is WorkspaceAction.Pending -> action.stage
        WorkspaceAction.Settings, WorkspaceAction.Routing, WorkspaceAction.Probe,
        WorkspaceAction.Totp, WorkspaceAction.Clients, WorkspaceAction.Agents,
        WorkspaceAction.Wake, WorkspaceAction.Library, WorkspaceAction.Plugins,
        WorkspaceAction.Onboard,
        -> "已接入"
    }
}

@Composable
private fun SettingsRows(
    groups: List<SettingsGroup>,
    onSettings: (SettingsGroup) -> Unit,
    showBottomDividerOnLast: Boolean = false,
) {
    groups.forEachIndexed { index, group ->
        val tone = group.identityTone()
        val pair = AetherColors.identityPair(tone)
        DirectoryRow(
            trailing = settingsRowTrailing(group),
            trailingAccent = group.available,
            onClick = { onSettings(group) },
            showDivider = index < groups.lastIndex || showBottomDividerOnLast,
            badge = {
                Box(
                    modifier = Modifier
                        .size(34.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(pair.container, RoundedCornerShape(10.dp))
                        .drawWithCache {
                            val strokePx = 0.6.dp.toPx()
                            val r = 10.dp.toPx()
                            onDrawWithContent {
                                drawContent()
                                drawRoundRect(
                                    color = pair.border.copy(alpha = 0.55f),
                                    topLeft = Offset(strokePx * 0.5f, strokePx * 0.5f),
                                    size = size.copy(
                                        width = (size.width - strokePx).coerceAtLeast(0f),
                                        height = (size.height - strokePx).coerceAtLeast(0f),
                                    ),
                                    cornerRadius = CornerRadius(r, r),
                                    style = Stroke(width = strokePx),
                                )
                            }
                        },
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = group.index,
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Bold,
                        color = pair.content,
                    )
                }
            },
            title = group.title,
            subtitle = group.subtitle,
        )
    }
}

@Composable
private fun WorkspaceRows(entries: List<WorkspaceEntry>, onWorkspace: (WorkspaceEntry) -> Unit) {
    entries.forEachIndexed { index, entry ->
        DirectoryRow(
            trailing = workspaceRowTrailing(entry),
            trailingAccent = !entry.desktopOnly,
            dimmed = entry.desktopOnly,
            onClick = { onWorkspace(entry) },
            showDivider = index < entries.lastIndex,
            badge = {
                AetherIdentityDisc(
                    iconRes = entry.iconRes(),
                    tone = entry.identityTone(),
                )
            },
            title = entry.title,
            subtitle = entry.subtitle,
        )
    }
}

/**
 * 旗舰级目录行：
 * 34dp 8 色柔和底盘 + 标题/副标 + 低饱和状态胶囊 + 光学右对齐箭头 + 文字起始对齐分割线。
 */
@Composable
private fun DirectoryRow(
    title: String,
    subtitle: String,
    trailing: String,
    trailingAccent: Boolean,
    onClick: () -> Unit,
    badge: @Composable () -> Unit,
    dimmed: Boolean = false,
    showDivider: Boolean = false,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .aetherPressable(scaleDown = 0.985f, onClick = onClick)
                .padding(vertical = DeepRowVertical)
                .alpha(if (dimmed) 0.68f else 1f),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            badge()
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.height(2.dp))
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.width(10.dp))
            AetherStatusChip(
                text = trailing,
                kind = when {
                    dimmed -> AetherChipKind.NEUTRAL
                    trailingAccent -> AetherChipKind.ACCENT
                    else -> AetherChipKind.NEUTRAL
                },
            )
            Spacer(Modifier.width(6.dp))
            AetherChevron()
        }
        if (showDivider) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(start = 48.dp)
                    .height(0.5.dp)
                    .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.35f)),
            )
        }
    }
}

@Composable
private fun CapabilityNote() {
    Text(
        text = "WSL、Windows 客户端扫描与进程控制、PC 隔离实例仅桌面端可用；手机与 PC 的账号、会话和账本不会自动同步。",
        style = MaterialTheme.typography.bodySmall,
        color = AetherColors.textTertiary(),
        modifier = Modifier.padding(horizontal = 4.dp),
    )
}

/**
 * 12 个工作区入口的精确图标映射（按 `action` + `title` 双保险匹配，彻底修复 10/12 掉入 else 的 Bug）。
 */
@DrawableRes
internal fun WorkspaceEntry.iconRes(): Int = when (action) {
    WorkspaceAction.Onboard -> R.drawable.ic_ws_onboard
    WorkspaceAction.Wake -> R.drawable.ic_ws_tasks
    WorkspaceAction.DesktopOnly -> R.drawable.ic_ws_apps
    WorkspaceAction.Library -> R.drawable.ic_ws_library
    WorkspaceAction.Probe -> R.drawable.ic_ws_probe
    WorkspaceAction.Totp -> R.drawable.ic_ws_2fa
    WorkspaceAction.Agents -> R.drawable.ic_ws_agents
    WorkspaceAction.Clients -> R.drawable.ic_ws_clients
    WorkspaceAction.Routing -> R.drawable.ic_ws_routing
    WorkspaceAction.Plugins -> R.drawable.ic_ws_plugins
    WorkspaceAction.Settings -> if (title.contains("脱敏")) {
        R.drawable.ic_ws_redact
    } else {
        R.drawable.ic_ws_settings
    }
    is WorkspaceAction.Tab -> when (action.tab) {
        MobileTab.OVERVIEW -> R.drawable.ic_nav_overview
        MobileTab.CHANNELS -> R.drawable.ic_ws_channels
        MobileTab.USAGE -> R.drawable.ic_ws_usage
        MobileTab.REQUESTS -> R.drawable.ic_ws_logs
        MobileTab.MORE -> R.drawable.ic_more
    }
    is WorkspaceAction.Pending -> R.drawable.ic_ws_settings
}

private fun WorkspaceEntry.identityTone(): AetherIdentityTone = when (action) {
    WorkspaceAction.Onboard -> AetherIdentityTone.BLUE
    WorkspaceAction.Wake -> AetherIdentityTone.AMBER
    WorkspaceAction.DesktopOnly -> AetherIdentityTone.SLATE
    WorkspaceAction.Library -> AetherIdentityTone.PURPLE
    WorkspaceAction.Probe -> AetherIdentityTone.GREEN
    WorkspaceAction.Totp -> AetherIdentityTone.CYAN
    WorkspaceAction.Agents -> AetherIdentityTone.INDIGO
    WorkspaceAction.Clients -> AetherIdentityTone.BLUE
    WorkspaceAction.Routing -> AetherIdentityTone.PINK
    WorkspaceAction.Plugins -> AetherIdentityTone.PURPLE
    WorkspaceAction.Settings -> if (title.contains("脱敏")) {
        AetherIdentityTone.AMBER
    } else {
        AetherIdentityTone.SLATE
    }
    is WorkspaceAction.Tab -> AetherIdentityTone.BLUE
    is WorkspaceAction.Pending -> AetherIdentityTone.SLATE
}

private fun SettingsGroup.identityTone(): AetherIdentityTone = when (index) {
    "01" -> AetherIdentityTone.BLUE
    "02" -> AetherIdentityTone.CYAN
    "03" -> AetherIdentityTone.PURPLE
    "04" -> AetherIdentityTone.INDIGO
    "05" -> AetherIdentityTone.AMBER
    "06" -> AetherIdentityTone.GREEN
    "07" -> AetherIdentityTone.PINK
    "08" -> AetherIdentityTone.CYAN
    "09" -> AetherIdentityTone.BLUE
    "10" -> AetherIdentityTone.SLATE
    else -> AetherIdentityTone.SLATE
}
