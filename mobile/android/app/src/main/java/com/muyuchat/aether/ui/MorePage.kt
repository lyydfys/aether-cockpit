package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

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
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Text(
                text = "更多",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = "全部工作区与设置入口",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        item { SearchField(query, onQueryChange) }
        if (searching) {
            item { SectionHeader("搜索结果", "${workspaces.size + settings.size} 项") }
            if (workspaces.isEmpty() && settings.isEmpty()) {
                item { EmptyResult() }
            } else {
                item {
                    DirectoryCard {
                        settings.forEach { group ->
                            SettingsRow(group = group, onClick = { onSettings(group) })
                        }
                        workspaces.forEach { entry ->
                            WorkspaceRow(entry = entry, onClick = { onWorkspace(entry) })
                        }
                    }
                }
            }
        } else {
            item { SectionHeader("设置分组", "Stage 5 · ${settingsGroups().size} 个分组") }
            item {
                DirectoryCard {
                    settingsGroups().forEach { group ->
                        SettingsRow(group = group, onClick = { onSettings(group) })
                    }
                }
            }
            item { SectionHeader("全部工作区", "${workspaceEntries().size} 个工作区") }
            item {
                DirectoryCard {
                    workspaceEntries().forEach { entry ->
                        WorkspaceRow(entry = entry, onClick = { onWorkspace(entry) })
                    }
                }
            }
        }
        item { CapabilityNote() }
    }
}

@Composable
private fun SectionHeader(title: String, trailing: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.weight(1f))
        Text(
            text = trailing,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun DirectoryCard(content: @Composable ColumnScope.() -> Unit) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 6.dp, vertical = 6.dp),
            content = content,
        )
    }
}

@Composable
private fun SettingsRow(group: SettingsGroup, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(McaShape.CardAction)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(
            modifier = Modifier.size(32.dp),
            shape = McaShape.Badge,
            color = MaterialTheme.colorScheme.background,
            border = McaHairline.stroke(),
        ) {
            Box(contentAlignment = Alignment.Center) {
                Text(
                    text = group.index,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = group.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = group.subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            // 已接入的组去设置页；其余如实标阶段，不用"设置页存在"冒充 9 组都能用。
            text = if (group.available) "已接入" else "后续阶段",
            style = MaterialTheme.typography.labelSmall,
            color = if (group.available) {
                MaterialTheme.colorScheme.primary
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        )
        Icon(
            painter = painterResource(R.drawable.ic_chevron),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(McaIconSize.S),
        )
    }
}

@Composable
private fun WorkspaceRow(entry: WorkspaceEntry, onClick: () -> Unit) {
    // 仅 Windows 的行也可点：点开是**说明为什么做不到**，不是执行动作。
    val enabled = true
    val trailing = when (val action = entry.action) {
        is WorkspaceAction.Tab -> if (action.tab == MobileTab.OVERVIEW) "当前" else action.tab.label
        WorkspaceAction.DesktopOnly -> "仅 Windows"
        is WorkspaceAction.Pending -> action.stage
        WorkspaceAction.Probe, WorkspaceAction.Routing, WorkspaceAction.Settings,
        WorkspaceAction.Totp, WorkspaceAction.Sessions, WorkspaceAction.Wake,
        WorkspaceAction.Library, WorkspaceAction.Plugins, WorkspaceAction.Clients -> "已接入"
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(McaShape.CardAction)
            .then(
                if (enabled) {
                    Modifier.clickable(role = Role.Button, onClick = onClick)
                } else {
                    Modifier
                },
            )
            .padding(12.dp)
            .alpha(if (entry.desktopOnly) 0.75f else 1f),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(
            modifier = Modifier.size(32.dp),
            shape = McaShape.Badge,
            color = MaterialTheme.colorScheme.background,
            border = McaHairline.stroke(),
        ) {
            Box(contentAlignment = Alignment.Center) {
                Icon(
                    painter = painterResource(entry.iconRes()),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.M),
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = entry.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = entry.subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (trailing.isNotEmpty()) {
            Text(
                text = trailing,
                style = MaterialTheme.typography.labelSmall,
                color = if (entry.action is WorkspaceAction.Tab &&
                    (entry.action as WorkspaceAction.Tab).tab == MobileTab.OVERVIEW
                ) {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
            )
        }
        if (enabled) {
            Icon(
                painter = painterResource(R.drawable.ic_chevron),
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(McaIconSize.S),
            )
        }
    }
}

@Composable
private fun EmptyResult() {
    Surface(
        shape = McaShape.Card,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
        ) {
            Text(
                text = "没有匹配的功能",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "试试工作区名称、设置分组或关键词。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

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
