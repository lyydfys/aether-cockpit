package com.muyuchat.aether.ui

import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
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
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.LibraryDraft
import com.muyuchat.aether.runtime.LibraryDraftProblems
import com.muyuchat.aether.runtime.LibraryEntry
import com.muyuchat.aether.runtime.LibraryKind
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/**
 * 资源库页：列出沙盒库里的条目，并能删除。
 *
 * 手机端**不做**「发现」与「下发给 Agent」：前者以用户家目录为根扫描（手机上扫不到），
 * 后者会写 `~/.claude.json` 之类的桌面文件。这里如实说明，而不是放两个点了没用的按钮。
 *
 * 另一个如实说明：MCP 的完整 `config`（可能含 env 密钥）不在页面上显示——
 * 本页压根不解析那个字段。
 */
@Composable
internal fun LibraryPage(
    status: GatewayStatus,
    state: LibraryUiState,
    kindFilter: LibraryKind?,
    deleting: String?,
    notice: String?,
    actionError: String?,
    onKindFilter: (LibraryKind?) -> Unit,
    onRefresh: () -> Unit,
    onDelete: (LibraryKind, String) -> Unit,
    onDismissNotice: () -> Unit,
    onDismissActionError: () -> Unit,
    saving: Boolean = false,
    saveProblems: LibraryDraftProblems? = null,
    onCreate: (LibraryDraft, (Boolean) -> Unit) -> Unit = { _, done -> done(false) },
    onClearSaveProblems: () -> Unit = {},
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var pendingDelete by remember { mutableStateOf<LibraryEntry?>(null) }
    var showCreate by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { onRefresh() }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
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
                        text = "资源库",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "查看 / 新增 / 删除 · 不下发给 Agent",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaButton(
                    onClick = { showCreate = true },
                    style = McaButtonStyle.Outlined,
                    enabled = status.running && !saving,
                ) { Text("新增") }
                McaIconButton(
                    contentDescription = "刷新资源库",
                    onClick = onRefresh,
                    enabled = state !is LibraryUiState.Loading,
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
            Surface(
                shape = McaShape.Card,
                color = McaTonal.fill(),
                border = McaHairline.stroke(),
            ) {
                Column(Modifier.fillMaxWidth().padding(14.dp)) {
                    Text(
                        text = "手机端可查看 / 新增 / 删除",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "「发现」要以桌面用户目录为根扫描，「下发给 Agent」会写桌面端的配置文件，" +
                            "两者在手机上都不做。这里可以列出、新增和删除本机网关沙盒里的条目。" +
                            "MCP 的完整配置（可能包含密钥）不会显示在页面上，也不允许在手机上编辑。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        notice?.let { message ->
            item {
                Column(Modifier.fillMaxWidth()) {
                    Text(message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.height(6.dp))
                    McaButton(onClick = onDismissNotice, style = McaButtonStyle.Outlined) { Text("知道了") }
                }
            }
        }
        actionError?.let { message ->
            item {
                Column(Modifier.fillMaxWidth()) {
                    Text(message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    Spacer(Modifier.height(6.dp))
                    McaButton(onClick = onDismissActionError, style = McaButtonStyle.Outlined) { Text("知道了") }
                }
            }
        }
        when (state) {
            LibraryUiState.Idle, LibraryUiState.Loading -> item {
                Text(
                    text = "正在读取资源库…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is LibraryUiState.Failed -> item {
                Surface(
                    shape = McaShape.Card,
                    color = McaTonal.fill(),
                    border = McaHairline.stroke(),
                ) {
                    Column(Modifier.fillMaxWidth().padding(14.dp)) {
                        Text(
                            text = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = if (state.code == "gateway_not_running") {
                                "启动本地网关后即可读取资源库。"
                            } else {
                                "${state.message}（错误码：${state.code}）"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(10.dp))
                        McaButton(
                            onClick = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                            style = McaButtonStyle.Primary,
                        ) {
                            Text(if (state.code == "gateway_not_running") "前往概览" else "重试")
                        }
                    }
                }
            }
            is LibraryUiState.Ready -> {
                val visible = state.entries.filter { kindFilter == null || it.kind == kindFilter }
                item {
                    Row(
                        modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        McaButton(
                            onClick = { onKindFilter(null) },
                            style = if (kindFilter == null) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        ) { Text("全部") }
                        LibraryKind.entries.forEach { kind ->
                            McaButton(
                                onClick = { onKindFilter(kind) },
                                style = if (kindFilter == kind) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                            ) { Text(kind.label) }
                        }
                    }
                }
                if (visible.isEmpty()) {
                    item {
                        Text(
                            text = if (state.entries.isEmpty()) {
                                "本机资源库里还没有条目。可以点右上「新增」创建一条；从桌面发现/导入仍在桌面端。"
                            } else {
                                "该分类下没有条目。"
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                } else {
                    item {
                        Text(
                            text = "共 ${visible.size} 条",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    items(visible.size) { index ->
                        val entry = visible[index]
                        LibraryRow(
                            entry = entry,
                            deleting = deleting == entry.id,
                            onDelete = { pendingDelete = entry },
                        )
                    }
                }
            }
        }
    }

    if (showCreate) {
        LibraryCreateDialog(
            saving = saving,
            problems = saveProblems,
            error = actionError,
            onDismiss = {
                showCreate = false
                onClearSaveProblems()
            },
            onSave = { draft ->
                onCreate(draft) { ok ->
                    if (ok) {
                        showCreate = false
                        onClearSaveProblems()
                    }
                }
            },
        )
    }

    pendingDelete?.let { entry ->
        McaDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("删除资源库条目", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "将从「${entry.kind.label}」里删除「${entry.name}」。删除不可撤销，" +
                        "已经下发到桌面端的副本不会自动移除。",
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val target = entry
                        pendingDelete = null
                        onDelete(target.kind, target.id)
                    },
                    style = McaButtonStyle.Primary,
                    enabled = deleting == null,
                ) { Text(if (deleting != null) "删除中…" else "删除") }
            },
            dismissButton = {
                McaButton(onClick = { pendingDelete = null }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }
}

@Composable
private fun LibraryRow(entry: LibraryEntry, deleting: Boolean, onDelete: () -> Unit) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = entry.name,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = entry.kind.label,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaButton(
                    onClick = onDelete,
                    style = McaButtonStyle.Outlined,
                    enabled = !deleting,
                ) { Text(if (deleting) "删除中…" else "删除") }
            }
        }
    }
}
