package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaIconPlate
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaPlateSize
import com.muyuchat.core.designsystem.McaShape

/*
 * 「更多」目录页。
 *
 * 排版照 UI_REFRESH_SPEC §1：区块标题用共享的 DeepSectionTitle（titleMedium + Bold，
 * 自带上内距，配列表 12dp 间距凑成区块间 24dp）；列表是**一层** DeepCard
 * （McaShape.Sheet + McaHairline + 左右 16dp），行只提供垂直 16dp，不再每行套一张卡。
 * 右侧状态文字与箭头在 SettingsRow / WorkspaceRow 里用同一串固定间隔收边，
 * 所以两类行的右缘严格对齐。
 */

/**
 * 组标题与首行之间的视觉间距。
 *
 * `DeepSectionTitle` 自己带上内距 12（与列表 12 凑成区块间 24），标题与卡片放在同一个 item 里，
 * 所以这里再补 8 + 行自身的 16 垂直内边距 = 标题到首行文字 24dp；卡片底边到下个标题文字仍是 12+12。
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
        item {
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
                item {
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
                item {
                    Column {
                        DeepSectionTitle(
                            text = "搜索结果",
                            trailing = "${workspaces.size + settings.size} 项",
                        )
                        Spacer(Modifier.height(TitleToRows))
                        DeepCard(contentPadding = DeepListCardPadding) {
                            // 命中时两类行混排：行的左右内边距与外缘都由这一张卡统一给。
                            if (settings.isNotEmpty()) {
                                SettingsRows(settings, onSettings)
                            }
                            WorkspaceRows(workspaces, onWorkspace)
                        }
                    }
                }
            }
        } else {
            item {
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
            item {
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
        }
        item { CapabilityNote() }
    }
}

/**
 * 搜索无结果的空态说明：必须回答「为什么空」+「下一步做什么」。
 *
 * 计数取自 [settingsGroups] / [workspaceEntries]，不是手写的数字——目录哪天变了，这句话不会撒谎。
 */
internal fun moreSearchEmptyDetail(query: String): String {
    val needle = query.trim()
    val hitScope = "${settingsGroups().size} 个设置分组与 ${workspaceEntries().size} 个工作区"
    if (needle.isEmpty()) {
        // 理论上进不到这里（空查询会走完整目录），但纯函数不能留一个未定义分支。
        return "目录里共 $hitScope；输入关键词可以按名称或说明过滤。"
    }
    return "「$needle」没有命中 $hitScope 的标题或说明。换个更短的关键词，或清除搜索回到完整目录。"
}

/** 设置分组行右侧的状态尾标：只有真的能在设置页里找到的组才写「已接入」。 */
internal fun settingsRowTrailing(group: SettingsGroup): String =
    if (group.available) "已接入" else "后续阶段"

/** 工作区行右侧的去向尾标。与 `WorkspaceAction` 一一对应，不允许兜底成「已接入」。 */
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
private fun SettingsRows(groups: List<SettingsGroup>, onSettings: (SettingsGroup) -> Unit) {
    groups.forEach { group ->
        DirectoryRow(
            trailing = settingsRowTrailing(group),
            trailingAccent = group.available,
            onClick = { onSettings(group) },
            badge = {
                Text(
                    text = group.index,
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            title = group.title,
            subtitle = group.subtitle,
        )
    }
}

@Composable
private fun WorkspaceRows(entries: List<WorkspaceEntry>, onWorkspace: (WorkspaceEntry) -> Unit) {
    entries.forEach { entry ->
        DirectoryRow(
            trailing = workspaceRowTrailing(entry),
            trailingAccent = !entry.desktopOnly &&
                (entry.action as? WorkspaceAction.Tab)?.tab == MobileTab.OVERVIEW,
            // 仅 Windows 的行也可点：点开是**说明为什么做不到**，不是执行动作。
            dimmed = entry.desktopOnly,
            onClick = { onWorkspace(entry) },
            badge = {
                Icon(
                    painter = painterResource(entry.iconRes()),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.M),
                )
            },
            title = entry.title,
            subtitle = entry.subtitle,
        )
    }
}

/**
 * 目录行的唯一形状：32dp 图形底板 + 标题/说明 + 右侧状态 + 箭头。
 *
 * 48dp 触控由整行承担（clickable 在 padding 之前）；右侧「状态 + 4dp + 箭头」是固定收边，
 * 所以同一张卡里所有行的箭头右缘、状态右缘都对齐，不会因为状态文字长短而错位。
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
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = DeepRowVertical)
            .alpha(if (dimmed) 0.75f else 1f),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        McaIconPlate(size = McaPlateSize.Medium, shape = McaShape.Badge) { badge() }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                // 1.3× 下标题不许被右侧状态挤成竖排单字：最多两行并允许省略。
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = trailing,
            style = MaterialTheme.typography.labelSmall,
            color = if (trailingAccent) {
                MaterialTheme.colorScheme.primary
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            // 状态词与箭头右缘同一条竖线：状态右对齐、单行、不换行；
            // 最短留 72dp 让「已接入 / 后续阶段 / 仅 Windows」在 1.0× 与 1.3× 都右缘对齐。
            textAlign = TextAlign.End,
            maxLines = 1,
            softWrap = false,
            overflow = TextOverflow.Clip,
            modifier = Modifier.widthIn(min = 72.dp),
        )
        Spacer(Modifier.width(4.dp))
        Icon(
            painter = painterResource(R.drawable.ic_chevron),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(McaIconSize.S),
        )
    }
}

/** 页脚口径说明：手机与 PC 的能力/数据边界，一句话讲清，不做成卡片。 */
@Composable
private fun CapabilityNote() {
    Text(
        text = "WSL、Windows 客户端扫描与进程控制、PC 隔离实例仅桌面端可用；手机与 PC 的账号、会话和账本不会自动同步。",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@DrawableRes
private fun WorkspaceEntry.iconRes(): Int = when (title) {
    "概览" -> R.drawable.ic_ws_overview
    "渠道接入", "路由" -> R.drawable.ic_ws_channels
    "账号额度", "额度快捷查看" -> R.drawable.ic_bolt
    "用量" -> R.drawable.ic_ws_usage
    "任务" -> R.drawable.ic_clock
    "应用", "网关探针" -> R.drawable.ic_gateway
    "会话", "请求" -> R.drawable.ic_ws_logs
    "2FA", "脱敏" -> R.drawable.ic_shield
    else -> R.drawable.ic_ws_settings
}
