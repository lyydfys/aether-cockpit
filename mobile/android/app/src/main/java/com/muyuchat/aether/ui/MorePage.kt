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
import androidx.compose.material3.Icon
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
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaIconSize

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
    /** 「设置」行的落点：优先进**设置首页导航列表**（initialGroup = null）；
     *  为空时保持旧行为（落到第一个分组）。 */
    onOpenSettingsHome: (() -> Unit)? = null,
    /** 「关于」行的落点：直达设置里的「10 关于」页。为空时才回落到按分组路由。 */
    onOpenAbout: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    val groups = settingsGroups()
    val presetGroup = groups.firstOrNull { it.title == "预设" }
    // 按**分区号**取「10 关于」，不按标题匹配（标题一改就失配，会静默落到设置首页）。
    val aboutGroup = groups.firstOrNull { it.index == SETTINGS_ABOUT_GROUP_ID }
    // 条目「设置」「脱敏」都走 WorkspaceAction.Settings，与下面的「设置」行重复，不再重复列。
    val workspaces = workspaceEntries().filter { it.action != WorkspaceAction.Settings }
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
                        subtitle = "预设 / 设置 / 关于",
                    )
                }
            }
        }
        // 用户指令：预设 / 设置 / 关于 移到列表**最底部**（工作区之后），并与工作区条目
        // 完全同一套行构件：同一个 DirectoryRow、同样的 identity 徽标、同样 trailing + chevron、同样 16dp 行高。
        item(key = "more_unified_list") {
            DeepCard(contentPadding = DeepListCardPadding) {
                WorkspaceRows(workspaces, onWorkspace)
                SettingsJumpRow(
                    title = "预设",
                    subtitle = "客户端、模型与请求参数模板",
                    trailing = "已接入",
                    group = presetGroup,
                    onSettings = onSettings,
                    showDivider = true,
                    iconRes = R.drawable.ic_tune,
                )
                SettingsJumpRow(
                    title = "设置",
                    subtitle = MORE_SETTINGS_ENTRY_SUBTITLE,
                    trailing = "已接入",
                    group = groups.firstOrNull(),
                    onSettings = onSettings,
                    onClickOverride = onOpenSettingsHome,
                    showDivider = true,
                    iconRes = R.drawable.ic_ws_settings,
                )
                SettingsJumpRow(
                    title = "关于",
                    subtitle = "版本、更新通道、许可与诊断包",
                    trailing = "已接入",
                    group = aboutGroup,
                    onSettings = onSettings,
                    // 直达「10 关于」：不依赖 group 是否匹配上，也不会落到设置首页；
                    // 连 group 都取不到时**禁用**该行（宁可点不动，也不把人送错页）。
                    onClickOverride = if (aboutGroup != null) onOpenAbout else null,
                    showDivider = false,
                    iconRes = R.drawable.ic_info,
                )
            }
        }
        item(key = "more_capability_note") { CapabilityNote() }
    }
}

/** 「设置」入口的副标题：只说清里面有什么，不再逐个列分组（分组在设置页里）。 */
internal const val MORE_SETTINGS_ENTRY_SUBTITLE: String =
    "工具服务 / 模型目录 / 预设 / 计价 / 可观测 / 关于"

/**
 * 更多页主入口的固定顺序（用户指令：只保留预设 / 设置 / 关于）。
 * 纯函数供单测断言顺序，页面渲染也从这里取，避免两处各写一份。
 */
internal fun morePrimaryEntryTitles(): List<String> = listOf("预设", "设置", "关于")

/**
 * 一行「跳设置某分组」的入口。
 *
 * **与工作区条目共用同一套行构件**：`DirectoryRow` + identity 徽标 + `trailing` 状态胶囊 + chevron，
 * 行高同为 `DeepRowVertical`——这三项不是"另一种控件"，只是列表末尾的普通条目。
 * [group] 为 null 时不跳（数据异常时保持安静，而不是崩），徽标退回与其它行同色的普通图标。
 */
@Composable
private fun SettingsJumpRow(
    title: String,
    subtitle: String,
    trailing: String,
    group: SettingsGroup?,
    onSettings: (SettingsGroup) -> Unit,
    showDivider: Boolean,
    /** 每行各自的图标：三项不能共用一个齿轮（用户反馈"图标全一样"）。 */
    @DrawableRes iconRes: Int = R.drawable.ic_ws_settings,
    /** 非空时优先用它（「设置」行要进首页列表，不落到第一个分组）。 */
    onClickOverride: (() -> Unit)? = null,
) {
    val tone = group?.identityTone()
    DirectoryRow(
        title = title,
        subtitle = subtitle,
        trailing = trailing,
        trailingAccent = false,
        showDivider = showDivider,
        onClick = { (onClickOverride ?: { group?.let(onSettings) })() },
        badge = {
            if (tone != null) {
                AetherIdentityDisc(iconRes = iconRes, tone = tone)
            } else {
                Icon(
                    painter = painterResource(iconRes),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.M),
                )
            }
        },
    )
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
