package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.HookEntry
import com.muyuchat.aether.runtime.LibraryDraft
import com.muyuchat.aether.runtime.LibraryDraftProblems
import com.muyuchat.aether.runtime.LibraryEntry
import com.muyuchat.aether.runtime.LibraryKind
import com.muyuchat.aether.runtime.skillCheckSummary
import com.muyuchat.aether.runtime.skillInstallSummary
import com.muyuchat.aether.runtime.skillProblemLines
import com.muyuchat.aether.runtime.skillUpdateSummary
import com.muyuchat.aether.runtime.updatableSkillNames
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch

/**
 * Hook 页签的独立 id：**不进 [LibraryKind]**。
 * 只读场景没有删除/新建路径，混进 kind 会牵动 libraryDeletePath 等映射。
 */
private const val HookTab = "HOOK"

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
    /** 技能仓库动作（桌面 `library.js:266-274` 的三个按钮 + `:632-694` 安装抽屉）。 */
    skillInstall: SkillInstallUiState = SkillInstallUiState.Idle,
    skillCheck: SkillCheckUiState = SkillCheckUiState.Idle,
    skillUpdate: SkillUpdateUiState = SkillUpdateUiState.Idle,
    onProbeSkills: ((String) -> Unit)? = null,
    onInstallSkills: ((String, List<String>) -> Unit)? = null,
    onCheckSkills: (() -> Unit)? = null,
    onUpdateAllSkills: (() -> Unit)? = null,
    onDismissSkillActions: () -> Unit = {},
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var pendingDelete by remember { mutableStateOf<LibraryEntry?>(null) }
    var showCreate by remember { mutableStateOf(false) }
    var showSkillInstall by remember { mutableStateOf(false) }
    var skillSource by remember { mutableStateOf("") }
    var skillPicked by remember { mutableStateOf(setOf<String>()) }
    var updateAllConfirm by remember { mutableStateOf(false) }
    var mcpTarget by remember { mutableStateOf<String?>(null) }
    var toolsTarget by remember { mutableStateOf<String?>(null) }
    // 桌面默认停在「指令」页签（library.js:42 `state.tab = 'prompts'`）。
    var tab by rememberSaveable { mutableStateOf(LibraryKind.PROMPT.name) }
    var libQuery by rememberSaveable { mutableStateOf("") }
    // 只读 Hook 详情（桌面 library.js:402-411 的行点开）；不提供编辑入口。
    var hookDetail by remember { mutableStateOf<HookEntry?>(null) }
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
        contentPadding = bubbleSafeContentPadding(),
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
                    DeepPageTitle(
                        title = "资源库",
                        // 1.3× 下再长会挤压标题：页脚已有「不下发给 Agent」的完整说明。
                        subtitle = "查看 / 新增 / 删除",
                    )
                }
                // 「新增」由各页签分区标题上的「新建指令 / 新建技能…」按钮承担（桌面也是分区建）。
                // 头部重复一个「新增」会在 1.3× 把副标题挤成省略号，这里不再重复放。
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
        // 桌面 KPI 行（library.js:118-125）：指令 / 技能 / MCP 服务器 / 子代理。
        val readyEntries = (state as? LibraryUiState.Ready)?.entries.orEmpty()
        item {
            DeepCard {
                LibraryKpiRow("指令", readyEntries.count { it.kind == LibraryKind.PROMPT }, "库中条目")
                LibraryKpiRow("技能", readyEntries.count { it.kind == LibraryKind.SKILL }, "库中条目")
                LibraryKpiRow("MCP 服务器", readyEntries.count { it.kind == LibraryKind.MCP }, "库中条目")
                LibraryKpiRow("子代理", readyEntries.count { it.kind == LibraryKind.SUBAGENT }, "库中条目")
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
                // 桌面页签（library.js:9 / :127-139）：指令 / 技能 / MCP 服务器 / 子代理，带计数。
                item {
                    Row(
                        modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        LibraryKind.entries.forEach { kind ->
                            val count = state.entries.count { it.kind == kind }
                            McaButton(
                                onClick = { tab = kind.name },
                                style = if (tab == kind.name) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                            ) { Text("${libraryTabLabel(kind)} $count", maxLines = 1) }
                        }
                        // 第 5 页签：Hooks（只读）。独立 id，不复用 LibraryKind。
                        McaButton(
                            onClick = { tab = HookTab },
                            style = if (tab == HookTab) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        ) { Text("Hooks ${state.hooks.size}", maxLines = 1) }
                    }
                }
                if (tab == HookTab) {
                    item {
                        Text(
                            text = "新建与编辑只在桌面端：Hook 需要写进各 agent 的配置文件，手机上没有那些目录。" +
                                "这里显示的是库中已同步的条目（在手机上不会触发）。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    item { DeepSectionTitle(text = "Hooks", trailing = "${state.hooks.size} 个") }
                    if (state.hooks.isEmpty()) {
                        item {
                            DeepStateCard(
                                title = "还没有 Hook",
                                detail = "事件触发时执行命令；保存时会写入所选 agent 的配置。" +
                                    "新建只在桌面端；手机上即使有条目也不会触发。",
                            )
                        }
                    } else {
                        items(state.hooks.size) { index ->
                            val hook = state.hooks[index]
                            HookRow(hook = hook, onClick = { hookDetail = hook })
                        }
                    }
                    item {
                        Text(
                            text = "Hook 是只读视图：新建、编辑、删除、写入 agent 都只在桌面端。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                } else {
                item { SearchField(libQuery, { libQuery = it }, placeholder = "搜索名称…") }
                val currentKind = LibraryKind.fromName(tab)
                val visible = state.entries
                    .filter { it.kind == currentKind }
                    .filter { libQuery.isBlank() || it.name.contains(libQuery.trim(), ignoreCase = true) }
                item {
                    DeepSectionTitle(
                        text = librarySectionTitle(currentKind),
                        trailing = libraryCountLabel(visible.size),
                        action = {
                            McaButton(
                                onClick = { showCreate = true },
                                style = McaButtonStyle.Outlined,
                                enabled = status.running && !saving,
                            ) { Text(libraryCreateLabel(currentKind)) }
                        },
                    )
                }
                // 技能区的仓库动作（桌面 `library.js:266-274`）：从仓库安装 / 检查更新 / 更新全部。
                // 这三个都只写网关自有库（见 runtime/LibraryApiClient.kt 的契约注释）。
                // 回调未接线时整条工具条不出现——宁可没有按钮，也不留一排按不动的灰按钮。
                if (currentKind == LibraryKind.SKILL && onProbeSkills != null) {
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .horizontalScroll(rememberScrollState()),
                                horizontalArrangement = Arrangement.spacedBy(8.dp),
                            ) {
                                McaButton(
                                    onClick = {
                                        showSkillInstall = true
                                        skillPicked = emptySet()
                                        onDismissSkillActions()
                                    },
                                    style = McaButtonStyle.Outlined,
                                    size = McaButtonSize.CardAction,
                                    enabled = status.running &&
                                        onProbeSkills != null &&
                                        skillInstall !is SkillInstallUiState.Installing,
                                ) { Text("从仓库安装", maxLines = 1) }
                                McaButton(
                                    onClick = { onCheckSkills?.invoke() },
                                    style = McaButtonStyle.Outlined,
                                    size = McaButtonSize.CardAction,
                                    enabled = status.running &&
                                        onCheckSkills != null &&
                                        skillCheck !is SkillCheckUiState.Checking,
                                ) {
                                    Text(
                                        text = if (skillCheck is SkillCheckUiState.Checking) "检查中…" else "检查更新",
                                        maxLines = 1,
                                    )
                                }
                                McaButton(
                                    onClick = { updateAllConfirm = true },
                                    style = McaButtonStyle.Outlined,
                                    size = McaButtonSize.CardAction,
                                    enabled = status.running &&
                                        onUpdateAllSkills != null &&
                                        skillUpdate !is SkillUpdateUiState.Running,
                                ) {
                                    Text(
                                        text = if (skillUpdate is SkillUpdateUiState.Running) "更新中…" else "更新全部",
                                        maxLines = 1,
                                    )
                                }
                            }
                            SkillActionPanel(skillInstall, skillCheck, skillUpdate)
                        }
                    }
                }
                if (visible.isEmpty()) {
                    val copy = libraryEmptyCopy(
                        hasAnyEntry = state.entries.isNotEmpty(),
                        kindLabel = libraryTabLabel(currentKind),
                    )
                    item {
                        DeepStateCard(
                            title = copy.title,
                            detail = copy.detail,
                            actionLabel = copy.action,
                            onAction = {
                                if (status.running) showCreate = true else onRefresh()
                            },
                        )
                    }
                } else {
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
                // 脚注只讲库内条目（「本机已有（未入库）」在手机上恒空，已从页面移除）。
                item {
                    Text(
                        text = "这里是网关自有的库：条目存在 App 自己的数据目录里，新增与删除都是真实写入。" +
                            "MCP 的完整配置可能含密钥，不在页面上显示。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                }
            }
        }
    }

    hookDetail?.let { hook ->
        HookDetailDialog(hook = hook, onDismiss = { hookDetail = null })
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

    // 从仓库安装（桌面 `library.js:632-694` 的抽屉）：探测 → 勾选 → 安装。
    // 安装只写网关自有库，不提"装完写进 agent"的选项（`agents` 恒空）。
    if (showSkillInstall) {
        SkillInstallDialog(
            source = skillSource,
            onSourceChange = { skillSource = it },
            install = skillInstall,
            picked = skillPicked,
            onToggle = { path ->
                skillPicked = if (skillPicked.contains(path)) skillPicked - path else skillPicked + path
            },
            onProbe = { onProbeSkills?.invoke(skillSource) },
            onInstall = {
                val probe = (skillInstall as? SkillInstallUiState.Probed)?.probe
                onInstallSkills?.invoke(probe?.source?.ifBlank { skillSource } ?: skillSource, skillPicked.toList())
            },
            onDismiss = {
                showSkillInstall = false
                skillPicked = emptySet()
                onDismissSkillActions()
            },
        )
    }

    if (updateAllConfirm) {
        McaDialog(
            onDismissRequest = { updateAllConfirm = false },
            title = { Text("更新全部技能？", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "会把所有「从仓库安装」的技能重新拉一遍远端内容并覆盖本地库条目；" +
                        "本地改过的技能会被服务端拒绝（除非显式替换）。更新前服务端会自动备份上一版。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        updateAllConfirm = false
                        onUpdateAllSkills?.invoke()
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("更新") }
            },
            dismissButton = {
                McaButton(onClick = { updateAllConfirm = false }, style = McaButtonStyle.Outlined) {
                    Text("取消")
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

/** 桌面页签文案（`library.js:9`）：指令 / 技能 / MCP 服务器 / 子代理。 */
internal fun libraryTabLabel(kind: LibraryKind): String = when (kind) {
    LibraryKind.PROMPT -> "指令"
    LibraryKind.SKILL -> "技能"
    LibraryKind.MCP -> "MCP 服务器"
    LibraryKind.SUBAGENT -> "子代理"
}

/** 桌面区块标题（`library.js:175` / `:263` / `:314` / `:359`）。 */
internal fun librarySectionTitle(kind: LibraryKind): String = when (kind) {
    LibraryKind.PROMPT -> "共享指令集"
    LibraryKind.SKILL -> "库中技能"
    LibraryKind.MCP -> "库中服务器"
    LibraryKind.SUBAGENT -> "库中子代理"
}

/** 桌面区块内的主操作（`library.js:177` / `:275` / `:316` / `:361`）。 */
internal fun libraryCreateLabel(kind: LibraryKind): String = when (kind) {
    LibraryKind.PROMPT -> "新建指令"
    LibraryKind.SKILL -> "新建技能"
    LibraryKind.MCP -> "添加服务器"
    LibraryKind.SUBAGENT -> "新建子代理"
}

/** KPI 一行（桌面 `.kpi`，`library.js:33-35`）。 */
@Composable
private fun LibraryKpiRow(label: String, value: Int, detail: String) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = label,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "$detail $value",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = value.toString(),
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
}

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
        // 头部「新增」已移除（避免 1.3× 挤掉标题）：引导到分区标题上的「新建」按钮。
        detail = "可以点分区标题旁的「新建」创建一条；从桌面 Agent 配置里「发现 / 导入」仍在桌面端做。",
        action = "新增条目",
    )
}

@Composable
private fun HookRow(hook: HookEntry, onClick: () -> Unit) {
    DeepCard(modifier = Modifier.clickable(onClick = onClick)) {
        Row(verticalAlignment = Alignment.Top) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = hook.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.height(6.dp))
                // 桌面 library.js:405-409：event chip → matcher(mono) → command(mono) → agents chip。
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    HookChip(hook.event.ifBlank { "—" })
                    if (hook.matcher.isNotBlank()) {
                        HookMono(hook.matcher)
                    }
                    if (hook.command.isNotBlank()) {
                        HookMono(hook.command)
                    }
                    if (hook.agents.isNotEmpty()) {
                        HookChip("→ " + hook.agents.joinToString("、"))
                    }
                }
                if (hook.agents.isEmpty()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "未写入任何 agent",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            if (hook.timeout > 0) {
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "${hook.timeout}s",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                )
            }
        }
    }
}

/** 只读小 chip（event / agents）。 */
@Composable
private fun HookChip(text: String) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
        )
    }
}

/** matcher / command 等机器可读字段：等宽字体。 */
@Composable
private fun HookMono(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall.copy(fontFamily = FontFamily.Monospace),
        color = MaterialTheme.colorScheme.onSurface,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
    )
}

/** Hook 详情：只读展示，不提供编辑/删除（桌面是在抽屉里编辑）。 */
@Composable
private fun HookDetailDialog(hook: HookEntry, onDismiss: () -> Unit) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = hook.name,
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column(
                Modifier.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                HookDetailLine("事件", hook.event.ifBlank { "—" })
                HookDetailLine("Matcher", hook.matcher.ifBlank { "—" }, mono = true)
                HookDetailLine("命令", hook.command.ifBlank { "—" }, mono = true)
                HookDetailLine(
                    "超时",
                    if (hook.timeout > 0) "${hook.timeout}s" else "未设置",
                )
                HookDetailLine(
                    "写入的 agent",
                    if (hook.agents.isEmpty()) "未写入任何 agent" else hook.agents.joinToString("、"),
                )
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

@Composable
private fun HookDetailLine(label: String, value: String, mono: Boolean = false) {
    Column {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = value,
            style = if (mono) {
                MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace)
            } else {
                MaterialTheme.typography.bodySmall
            },
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
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
        // 动作按钮独占一行：1.3× 下「工具 / 登录 / 删除」与长标题同排会互相挤压并溢出，
        // 下来一行后两档都不截断；仍保留横滚兜底（极端长文案时滚动而不是换行）。
        Spacer(Modifier.height(10.dp))
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (mcpToolsEnabled) {
                McaButton(
                    onClick = onMcpTools,
                    style = McaButtonStyle.Outlined,
                ) { Text("工具") }
            }
            if (mcpSigninEnabled) {
                McaButton(
                    onClick = onMcpSignin,
                    style = McaButtonStyle.Outlined,
                ) { Text("登录") }
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

/**
 * 技能仓库动作的结果面板：三段状态里**只显示当前那一段**（后一个动作会覆盖前一个）。
 *
 * 失败一律贴服务端原文（`SkillProblem.error` 就是服务端返回的字符串），不重写成"操作失败"。
 */
@Composable
private fun SkillActionPanel(
    install: SkillInstallUiState,
    check: SkillCheckUiState,
    update: SkillUpdateUiState,
) {
    val variant = MaterialTheme.colorScheme.onSurfaceVariant
    when {
        install is SkillInstallUiState.Probing -> Text(
            text = "正在探测仓库…",
            style = MaterialTheme.typography.bodySmall,
            color = variant,
        )
        install is SkillInstallUiState.Installing -> Text(
            text = "正在安装…（只写网关自有库）",
            style = MaterialTheme.typography.bodySmall,
            color = variant,
        )
        install is SkillInstallUiState.Failed -> Text(
            text = install.message,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        install is SkillInstallUiState.Done -> Column(
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = skillInstallSummary(install.result),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            skillProblemLines(install.result.skipped).take(6).forEach { line ->
                Text(
                    text = line,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
        check is SkillCheckUiState.Checking -> Text(
            text = "正在检查更新…（服务端逐个比对远端内容）",
            style = MaterialTheme.typography.bodySmall,
            color = variant,
        )
        check is SkillCheckUiState.Failed -> Text(
            text = check.message,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        check is SkillCheckUiState.Ready -> Column(
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = skillCheckSummary(check.report),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            updatableSkillNames(check.report).take(8).forEach { name ->
                Text(
                    text = "· $name",
                    style = MaterialTheme.typography.labelSmall,
                    color = variant,
                )
            }
            check.report.rows
                .filter { it.status.equals("unknown", ignoreCase = true) && it.error.isNotBlank() }
                .take(4)
                .forEach { row ->
                    Text(
                        text = "${row.name}：${row.error}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
        }
        update is SkillUpdateUiState.Running -> Text(
            text = "正在更新全部技能…",
            style = MaterialTheme.typography.bodySmall,
            color = variant,
        )
        update is SkillUpdateUiState.Failed -> Text(
            text = update.message,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
        update is SkillUpdateUiState.Done -> Column(
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = skillUpdateSummary(update.result),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            skillProblemLines(update.result.unupdated).take(6).forEach { line ->
                Text(
                    text = line,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (update.result.gone.isNotEmpty()) {
                Text(
                    text = "来源已删除：${update.result.gone.joinToString("、")}",
                    style = MaterialTheme.typography.labelSmall,
                    color = variant,
                )
            }
        }
    }
}

/**
 * 「从仓库安装」对话框（桌面 `library.js:632-694`）：填来源 → 探测 → 勾选 → 安装。
 *
 * 与桌面的差异：**没有「安装后写入 agent」那一栏**。服务端只有 `agents` 非空时才写
 * agent 目录（library_skills_install.go:178-192），手机端不做这件事，所以客户端恒传空数组。
 */
@Composable
private fun SkillInstallDialog(
    source: String,
    onSourceChange: (String) -> Unit,
    install: SkillInstallUiState,
    picked: Set<String>,
    onToggle: (String) -> Unit,
    onProbe: () -> Unit,
    onInstall: () -> Unit,
    onDismiss: () -> Unit,
) {
    val probing = install is SkillInstallUiState.Probing
    val installing = install is SkillInstallUiState.Installing
    val variant = MaterialTheme.colorScheme.onSurfaceVariant
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("从仓库安装技能", fontWeight = FontWeight.Bold) },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 420.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = "来源（GitHub owner/repo、GitLab 地址）",
                    style = MaterialTheme.typography.labelSmall,
                    color = variant,
                )
                Surface(
                    shape = McaShape.CardAction,
                    color = MaterialTheme.colorScheme.surface,
                    border = McaHairline.stroke(),
                ) {
                    BasicTextField(
                        value = source,
                        onValueChange = onSourceChange,
                        singleLine = true,
                        textStyle = MaterialTheme.typography.bodyMedium.copy(
                            color = MaterialTheme.colorScheme.onSurface,
                        ),
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 12.dp, vertical = 10.dp),
                    )
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = onProbe,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = source.isNotBlank() && !probing && !installing,
                    ) { Text(if (probing) "探测中…" else "探测") }
                    McaButton(
                        onClick = onInstall,
                        style = McaButtonStyle.Primary,
                        size = McaButtonSize.CardAction,
                        enabled = picked.isNotEmpty() && !installing && install is SkillInstallUiState.Probed,
                    ) { Text(if (installing) "安装中…" else "安装 ${picked.size} 个") }
                }
                when (install) {
                    SkillInstallUiState.Idle -> Text(
                        text = "先探测，再勾选要装的技能。安装只写网关自有库，不改桌面 agent 配置。",
                        style = MaterialTheme.typography.bodySmall,
                        color = variant,
                    )
                    SkillInstallUiState.Probing -> Text(
                        text = "正在探测仓库…",
                        style = MaterialTheme.typography.bodySmall,
                        color = variant,
                    )
                    is SkillInstallUiState.Probed -> {
                        val probe = install.probe
                        Text(
                            text = "${probe.source.ifBlank { source }} · ${probe.candidates.size} 个技能",
                            style = MaterialTheme.typography.labelSmall,
                            color = variant,
                        )
                        if (probe.candidates.isEmpty()) {
                            Text(
                                text = "仓库里没有找到 SKILL.md。下一步：确认仓库里确实有技能目录，或换一个来源。",
                                style = MaterialTheme.typography.bodySmall,
                                color = variant,
                            )
                        }
                        probe.candidates.forEach { candidate ->
                            val selected = picked.contains(candidate.path)
                            McaButton(
                                onClick = { onToggle(candidate.path) },
                                style = if (selected) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                                size = McaButtonSize.CardAction,
                                enabled = !candidate.have,
                            ) {
                                Text(
                                    text = when {
                                        candidate.have -> "${candidate.name}（已安装）"
                                        selected -> "✓ ${candidate.name}"
                                        else -> candidate.name
                                    },
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                    }
                    SkillInstallUiState.Installing -> Text(
                        text = "正在安装…",
                        style = MaterialTheme.typography.bodySmall,
                        color = variant,
                    )
                    is SkillInstallUiState.Failed -> Text(
                        text = install.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is SkillInstallUiState.Done -> Column(
                        verticalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        Text(
                            text = skillInstallSummary(install.result),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        skillProblemLines(install.result.skipped).take(6).forEach { line ->
                            Text(
                                text = line,
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.error,
                            )
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
