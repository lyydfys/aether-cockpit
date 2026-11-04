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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
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
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaSwitch

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
    /** MCP 登录/登出（桌面 `library.js:795`）。需要外部接线；未接线时按钮不出现。 */
    mcpAuth: McpAuthUiState = McpAuthUiState.Idle,
    onStartMcpSignin: ((String) -> Unit)? = null,
    onCancelMcpSignin: () -> Unit = {},
    onSignOutMcp: (String) -> Unit = {},
    onDismissMcpAuth: () -> Unit = {},
    /** MCP 工具开关（桌面 `library.js:490-511`）；为 null 时不出「工具」按钮。 */
    mcpTools: McpToolsUiState = McpToolsUiState.Idle,
    onLoadMcpTools: ((String, List<String>) -> Unit)? = null,
    onToggleMcpTool: (String) -> Unit = {},
    onSaveMcpTools: () -> Unit = {},
    onDismissMcpTools: () -> Unit = {},
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var pendingDelete by remember { mutableStateOf<LibraryEntry?>(null) }
    var showCreate by remember { mutableStateOf(false) }
    var mcpTarget by remember { mutableStateOf<String?>(null) }
    var toolsTarget by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) { onRefresh() }
    // 登录流程结束（成功/失败）时把弹窗关掉，失败原因留在 flow 状态里给下一次打开看不如直接弹掉——
    // 所以只在 Done 时自动关，失败保留弹窗显示原因。
    LaunchedEffect(mcpAuth) {
        if (mcpAuth is McpAuthUiState.Done) {
            mcpTarget = null
            onDismissMcpAuth()
        }
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
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
                        style = MaterialTheme.typography.headlineSmall,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "查看 / 新增 / 删除 · 不下发给 Agent",
                        style = MaterialTheme.typography.bodySmall,
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
        item { DeepSectionTitle("手机端能做什么") }
        item {
            DeepCard {
                DeepCardBody(
                    "「发现」要以桌面用户目录为根扫描，「下发给 Agent」会写桌面端的配置文件，" +
                        "两者在手机上都不做。这里可以列出、新增和删除本机网关沙盒里的条目。" +
                        "MCP 的完整配置（可能包含密钥）不会显示在页面上，也不允许在手机上编辑。",
                )
            }
        }
        notice?.let { message ->
            item { DeepNotice(message, onDismiss = onDismissNotice) }
        }
        actionError?.let { message ->
            item { DeepNotice(message, isError = true, onDismiss = onDismissActionError) }
        }
        when (state) {
            LibraryUiState.Idle, LibraryUiState.Loading -> item {
                DeepCardBody("正在读取资源库…")
            }
            is LibraryUiState.Failed -> item {
                DeepStateCard(
                    title = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                    detail = if (state.code == "gateway_not_running") {
                        "启动本地网关后即可读取资源库。"
                    } else {
                        "${state.message}（错误码：${state.code}）"
                    },
                    tone = if (state.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
                    actionLabel = if (state.code == "gateway_not_running") "前往概览" else "重试",
                    onAction = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                )
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
                    val copy = libraryEmptyCopy(
                        hasAnyEntry = state.entries.isNotEmpty(),
                        kindLabel = kindFilter?.label,
                    )
                    item {
                        DeepStateCard(
                            title = copy.title,
                            detail = copy.detail,
                            actionLabel = copy.action,
                            onAction = if (state.entries.isEmpty()) {
                                { if (status.running) showCreate = true }
                            } else {
                                { onKindFilter(null) }
                            },
                        )
                    }
                } else {
                    item {
                        DeepSectionTitle(
                            text = "本机条目",
                            trailing = libraryCountLabel(visible.size),
                        )
                    }
                    items(visible.size) { index ->
                        val entry = visible[index]
                        LibraryRow(
                            entry = entry,
                            deleting = deleting == entry.id,
                            onDelete = { pendingDelete = entry },
                            mcpSigninEnabled = onStartMcpSignin != null &&
                                entry.kind == LibraryKind.MCP &&
                                mcpAuth !is McpAuthUiState.Starting,
                            onMcpSignin = {
                                mcpTarget = entry.name
                                onStartMcpSignin?.invoke(entry.name)
                            },
                            mcpToolsEnabled = onLoadMcpTools != null &&
                                entry.kind == LibraryKind.MCP &&
                                mcpTools !is McpToolsUiState.Loading,
                            onMcpTools = {
                                toolsTarget = entry.name
                                onLoadMcpTools?.invoke(entry.name, entry.disabledTools)
                            },
                        )
                    }
                }
            }
        }
    }

    // MCP 登录/登出弹窗（桌面 library.js:795）：手机上用系统浏览器打开授权页，回跳本机回环地址。
    if (mcpTarget != null) {
        McpAuthDialog(
            target = mcpTarget!!,
            auth = mcpAuth,
            onCancelWait = onCancelMcpSignin,
            onSignOut = onSignOutMcp,
            onDismiss = {
                mcpTarget = null
                onDismissMcpAuth()
            },
        )
    }

    // MCP 工具开关（桌面 library.js:490-511）
    if (toolsTarget != null) {
        McpToolsDialog(
            target = toolsTarget!!,
            state = mcpTools,
            onToggle = onToggleMcpTool,
            onSave = onSaveMcpTools,
            onDismiss = {
                toolsTarget = null
                onDismissMcpTools()
            },
        )
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
                    style = MaterialTheme.typography.bodySmall,
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

/** 列表计数文案。 */
internal fun libraryCountLabel(count: Int): String = "$count 条"

/** 空态文案：标题说清是哪一种空，正文说下一步能做什么。 */
internal data class LibraryEmptyCopy(val title: String, val detail: String, val action: String)

internal fun libraryEmptyCopy(hasAnyEntry: Boolean, kindLabel: String?): LibraryEmptyCopy {
    if (hasAnyEntry && !kindLabel.isNullOrBlank()) {
        return LibraryEmptyCopy(
            title = "「$kindLabel」下没有条目",
            detail = "其它分类里还有条目。切回「全部」可以看到完整列表。",
            action = "显示全部",
        )
    }
    return LibraryEmptyCopy(
        title = "资源库里还没有条目",
        detail = "可以点右上「新增」创建一条；从桌面 Agent 配置里「发现 / 导入」仍在桌面端做。",
        action = "新增条目",
    )
}

@Composable
private fun LibraryRow(
    entry: LibraryEntry,
    deleting: Boolean,
    onDelete: () -> Unit,
    mcpSigninEnabled: Boolean = false,
    onMcpSignin: () -> Unit = {},
    mcpToolsEnabled: Boolean = false,
    onMcpTools: () -> Unit = {},
) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = entry.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = entry.kind.label + if (entry.disabledTools.isNotEmpty()) {
                        " · 停用 ${entry.disabledTools.size} 个工具"
                    } else {
                        ""
                    },
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(8.dp))
            if (mcpToolsEnabled) {
                McaButton(
                    onClick = onMcpTools,
                    style = McaButtonStyle.Outlined,
                ) { Text("工具") }
                Spacer(Modifier.width(8.dp))
            }
            if (mcpSigninEnabled) {
                McaButton(
                    onClick = onMcpSignin,
                    style = McaButtonStyle.Outlined,
                ) { Text("登录") }
                Spacer(Modifier.width(8.dp))
            }
            McaButton(
                onClick = onDelete,
                style = McaButtonStyle.Outlined,
                enabled = !deleting,
            ) { Text(if (deleting) "删除中…" else "删除") }
        }
    }
}

/**
 * MCP 工具开关弹窗（桌面 `library.js:490-511`）：先读工具清单，再逐项开关，最后保存。
 *
 * 桌面的文案：没有工具时用服务端的 `why`/`detail` 解释（`:509`）；这里照抄。
 */
@Composable
private fun McpToolsDialog(
    target: String,
    state: McpToolsUiState,
    onToggle: (String) -> Unit,
    onSave: () -> Unit,
    onDismiss: () -> Unit,
) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("工具开关 · $target", fontWeight = FontWeight.Bold) },
        text = {
            Column {
                when (state) {
                    McpToolsUiState.Idle, is McpToolsUiState.Loading -> Text(
                        text = "正在读取工具清单…（需要这条 MCP 已登录）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is McpToolsUiState.Failed -> Text(
                        text = "${state.message}（错误码：${state.code}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is McpToolsUiState.Ready -> {
                        if (state.tools.isEmpty()) {
                            Text(
                                text = if (state.why.isNotBlank()) {
                                    "没有读到工具（${state.why}${if (state.detail.isNotBlank()) "：${state.detail}" else ""}）"
                                } else {
                                    "服务器没有返回工具清单。"
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            Text(
                                text = if (state.disabled.isEmpty()) {
                                    "未停用任何工具"
                                } else {
                                    "已停用 ${state.disabled.size} 个工具"
                                },
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            LazyColumn(modifier = Modifier.heightIn(max = 320.dp)) {
                                items(state.tools.size) { index ->
                                    val tool = state.tools[index]
                                    Row(
                                        modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Text(
                                            text = tool,
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurface,
                                            modifier = Modifier.weight(1f),
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                        McaSwitch(
                                            checked = tool !in state.disabled,
                                            onCheckedChange = { onToggle(tool) },
                                            contentDescription = "启用 $tool",
                                        )
                                    }
                                }
                            }
                            Spacer(Modifier.height(8.dp))
                            McaButton(onClick = onSave, style = McaButtonStyle.Primary) {
                                Text("保存工具开关")
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/**
 * MCP 登录弹窗：等待期间把授权链接显示出来并允许复制到浏览器（手机上没有可自动跳转的
 * 保证，复制链接是最可靠的一条），同时提供登出。
 */
@Composable
private fun McpAuthDialog(
    target: String,
    auth: McpAuthUiState,
    onCancelWait: () -> Unit,
    onSignOut: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val clipboard = LocalClipboardManager.current
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("登录 MCP 服务器 · $target", fontWeight = FontWeight.Bold) },
        text = {
            Column {
                when (auth) {
                    McpAuthUiState.Idle, is McpAuthUiState.Starting -> Text(
                        text = "正在向服务端申请授权地址…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is McpAuthUiState.Waiting -> {
                        Text(
                            text = "在浏览器里打开下面的链接完成授权；回调会回到本机网关（127.0.0.1）。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(8.dp))
                        Text(
                            text = auth.url,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(8.dp))
                        McaButton(
                            onClick = { clipboard.setText(AnnotatedString(auth.url)) },
                            style = McaButtonStyle.Outlined,
                        ) { Text("复制授权链接") }
                    }
                    is McpAuthUiState.Done -> Text(
                        text = if (auth.alreadySignedIn) "这个服务器已经登录过了。" else "登录成功，本机已保存令牌。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    is McpAuthUiState.Failed -> Text(
                        text = "${auth.message}（错误码：${auth.code}）",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(12.dp))
                McaButton(
                    onClick = { onSignOut(target) },
                    style = McaButtonStyle.Outlined,
                ) { Text("登出这个服务器") }
            }
        },
        confirmButton = {
            if (auth is McpAuthUiState.Waiting) {
                McaButton(onClick = onCancelWait, style = McaButtonStyle.Outlined) { Text("取消登录") }
            }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/** 登录流程的状态文案（纯函数，单测用）。 */
internal fun mcpAuthStateLabel(auth: McpAuthUiState): String = when (auth) {
    McpAuthUiState.Idle -> "未开始"
    is McpAuthUiState.Starting -> "申请授权地址…"
    is McpAuthUiState.Waiting -> "等待浏览器授权"
    is McpAuthUiState.Done -> if (auth.alreadySignedIn) "已登录" else "登录成功"
    is McpAuthUiState.Failed -> "登录失败：${auth.message}"
}
