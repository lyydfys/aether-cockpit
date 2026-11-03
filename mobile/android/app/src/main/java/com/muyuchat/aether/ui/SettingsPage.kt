package com.muyuchat.aether.ui

import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.heightIn
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
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
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
import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.RedactCustomRule
import com.muyuchat.aether.runtime.RedactRule
import com.muyuchat.aether.runtime.effectiveAction
import com.muyuchat.aether.runtime.validateRedactCustomRule
import com.muyuchat.aether.runtime.SettingsSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

@Composable
internal fun SettingsPage(
    status: GatewayStatus,
    state: SettingsUiState,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    saveState: SettingsSaveState = SettingsSaveState.Idle,
    onToggleRedact: (Boolean) -> Unit = {},
    personalSaving: Boolean = false,
    personalError: String? = null,
    onToggleRedactPersonal: (Boolean) -> Unit = {},
    lanSaving: Boolean = false,
    lanError: String? = null,
    onToggleLan: (Boolean) -> Unit = {},
    proxySaving: Boolean = false,
    proxyError: String? = null,
    onSaveProxy: (String) -> Unit = {},
    corsSaving: Boolean = false,
    corsError: String? = null,
    onSaveCors: (List<String>) -> Unit = {},
    ipsSaving: Boolean = false,
    ipsError: String? = null,
    onSaveAllowIPs: (List<String>) -> Unit = {},
    callerKeys: CallerKeysUiState = CallerKeysUiState.Idle,
    callerSecret: String? = null,
    keysError: String? = null,
    onLoadCallerKeys: () -> Unit = {},
    onCreateCallerKey: (String) -> Unit = {},
    onToggleCallerKey: (CallerKeySummary) -> Unit = {},
    onRemoveCallerKey: (String) -> Unit = {},
    onDismissCallerSecret: () -> Unit = {},
    redactDetail: RedactDetailUiState = RedactDetailUiState.Idle,
    redactError: String? = null,
    onOpenRedactDetail: () -> Unit = {},
    onCloseRedactDetail: () -> Unit = {},
    onCycleRedactAction: (RedactRule) -> Unit = {},
    redactSaving: Boolean = false,
    redactWriteVersion: Int = 0,
    onAddRedactRule: (RedactCustomRule) -> Unit = {},
    onEditRedactRule: (Int, RedactCustomRule) -> Unit = { _, _ -> },
    onDeleteRedactRule: (Int) -> Unit = {},
    preferredPort: Int = 0,
    preferredPortSaving: Boolean = false,
    preferredPortError: String? = null,
    preferredPortNotice: String? = null,
    onSavePreferredPort: (String) -> Unit = {},
    modifier: Modifier = Modifier,
) {
    LaunchedEffect(Unit) { onLoadCallerKeys() }
    var redactCustomFirst by remember { mutableStateOf(false) }
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
                        text = "设置",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "本机配置 · 只读",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新设置",
                    onClick = onRefresh,
                    enabled = state is SettingsUiState.Ready || state is SettingsUiState.Failed,
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
        // 首选端口是**本机运行配置**，不是网关状态：必须始终可编辑。
        // 否则首选端口被占用 → 启动失败 → 设置页因「网关未启动」拒绝渲染 → 用户再也改不回来那个端口。
        item {
            SettingsSection("本机运行配置") {
                PreferredPortRow(
                    preferredPort = preferredPort,
                    saving = preferredPortSaving,
                    error = preferredPortError,
                    notice = preferredPortNotice,
                    onSave = onSavePreferredPort,
                )
            }
        }
        when (state) {
            SettingsUiState.Idle -> item {
                SettingsStateCard(
                    title = "网关未启动",
                    detail = "启动本地网关后可读取本机设置。",
                    actionLabel = "前往概览",
                    onAction = onOpenOverview,
                )
            }
            SettingsUiState.Loading -> item { SettingsLoadingCard() }
            is SettingsUiState.Failed -> item {
                SettingsErrorCard(state.code, state.message, onRefresh)
            }
            is SettingsUiState.Ready -> {
                item {
                    SettingsSections(
                        settings = state.settings,
                        status = status,
                        saveState = saveState,
                        onToggleRedact = onToggleRedact,
                        personalSaving = personalSaving,
                        personalError = personalError,
                        onToggleRedactPersonal = onToggleRedactPersonal,
                        onOpenRedactDetail = {
                            redactCustomFirst = false
                            onOpenRedactDetail()
                        },
                        onOpenRedactRules = {
                            redactCustomFirst = true
                            onOpenRedactDetail()
                        },
                        lanSaving = lanSaving,
                        lanError = lanError,
                        onToggleLan = onToggleLan,
                        proxySaving = proxySaving,
                        proxyError = proxyError,
                        onSaveProxy = onSaveProxy,
                        corsSaving = corsSaving,
                        corsError = corsError,
                        onSaveCors = onSaveCors,
                        ipsSaving = ipsSaving,
                        ipsError = ipsError,
                        onSaveAllowIPs = onSaveAllowIPs,
                    )
                }
                item {
                    CallerKeysSection(
                        state = callerKeys,
                        error = keysError,
                        onCreate = onCreateCallerKey,
                        onToggle = onToggleCallerKey,
                        onRemove = onRemoveCallerKey,
                    )
                }
            }
        }
        item {
            Text(
                text = "脱敏引擎开关经 /api/settings/redact-actions 仅合并 enabled；个人信息开关经 /api/settings 按 key 合并 redactPersonal；动作覆盖按 kind 整表回写；密钥、OTLP Headers、WebDAV 密码与远程额度密文不在本页展示。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    if (callerSecret != null) {
        CallerSecretDialog(secret = callerSecret, onDismiss = onDismissCallerSecret)
    }
    if (redactDetail != RedactDetailUiState.Idle) {
        RedactDetailDialog(
            state = redactDetail,
            error = redactError,
            onCycle = onCycleRedactAction,
            onDismiss = onCloseRedactDetail,
            saving = redactSaving,
            writeVersion = redactWriteVersion,
            customFirst = redactCustomFirst,
            onAdd = onAddRedactRule,
            onEdit = onEditRedactRule,
            onDelete = onDeleteRedactRule,
        )
    }
}

@Composable
private fun RedactDetailDialog(
    state: RedactDetailUiState,
    error: String?,
    onCycle: (RedactRule) -> Unit,
    onDismiss: () -> Unit,
    saving: Boolean,
    writeVersion: Int,
    customFirst: Boolean,
    onAdd: (RedactCustomRule) -> Unit,
    onEdit: (Int, RedactCustomRule) -> Unit,
    onDelete: (Int) -> Unit,
) {
    var editor by remember { mutableStateOf<Pair<Int, RedactCustomRule>?>(null) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = if (customFirst) "自定义规则" else "脱敏动作",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 420.dp)
                    .verticalScroll(rememberScrollState()),
            ) {
                when (state) {
                    RedactDetailUiState.Idle, RedactDetailUiState.Loading -> Text(
                        text = "正在读取脱敏动作…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is RedactDetailUiState.Failed -> Text(
                        text = state.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is RedactDetailUiState.Ready -> {
                        if (!customFirst) {
                            Text(
                                text = "脱敏引擎：${if (state.detail.enabled) "开启" else "关闭"} · 点动作按钮循环 脱敏 → 告警 → 阻断",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (saving) {
                                Spacer(Modifier.height(4.dp))
                                Text(
                                    text = "正在保存并回读服务端…",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.primary,
                                )
                            }
                            Spacer(Modifier.height(6.dp))
                            state.detail.rules.forEach { rule ->
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(vertical = 4.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Column(Modifier.weight(1f)) {
                                        Text(
                                            text = rule.name,
                                            style = MaterialTheme.typography.bodyMedium,
                                            color = MaterialTheme.colorScheme.onSurface,
                                        )
                                        Text(
                                            text = "${rule.kind} · ${if (rule.enabled) "启用" else "停用"} · 默认 ${actionLabel(rule.action)}",
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                    Spacer(Modifier.width(8.dp))
                                    Text(
                                        text = actionLabel(effectiveAction(state.detail, rule)),
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.primary,
                                    )
                                    Spacer(Modifier.width(8.dp))
                                    McaButton(
                                        onClick = { onCycle(rule) },
                                        style = McaButtonStyle.Outlined,
                                    ) { Text("切换") }
                                }
                            }
                            Spacer(Modifier.height(10.dp))
                        }
                        Spacer(Modifier.height(8.dp))
                        Text(
                            text = "自定义规则 · ${state.customRules.size}",
                            style = MaterialTheme.typography.titleSmall,
                            fontWeight = FontWeight.Bold,
                        )
                        if (state.customRules.isEmpty()) {
                            Text(
                                text = "暂无自定义规则",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        state.customRules.forEachIndexed { index, rule ->
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = 4.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Column(Modifier.weight(1f)) {
                                    Text(
                                        text = rule.kind,
                                        style = MaterialTheme.typography.bodyMedium,
                                    )
                                    Text(
                                        text = if (rule.prefix.isNotBlank()) "前缀 · ${rule.prefix}" else "正则 · ${rule.regex}",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                McaButton(
                                    onClick = { editor = index to rule },
                                    style = McaButtonStyle.Outlined,
                                ) { Text("编辑") }
                                Spacer(Modifier.width(4.dp))
                                McaButton(
                                    onClick = { onDelete(index) },
                                    style = McaButtonStyle.Outlined,
                                ) { Text("删除") }
                            }
                        }
                        Spacer(Modifier.height(4.dp))
                        McaButton(
                            onClick = { editor = -1 to RedactCustomRule("") },
                            style = McaButtonStyle.Outlined,
                        ) { Text("添加规则") }
                        Spacer(Modifier.height(4.dp))
                        McaButton(
                            onClick = { editor = -1 to RedactRuleTemplate.PhoneRule.toRule() },
                            style = McaButtonStyle.Outlined,
                        ) { Text("手机号脱敏模板") }
                        Text(
                            text = "模板会预填 kind=phone、prefix=+86；保存后由 Go 网关执行脱敏。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (customFirst && saving) {
                            Spacer(Modifier.height(4.dp))
                            Text(
                                text = "正在保存并回读服务端…",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.primary,
                            )
                        }
                    }
                }
                if (error != null) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(8.dp))
                Text(
                    text = if (customFirst) {
                        "自定义规则整体回写；前缀/正则二选一；编辑时若服务端规则已变动会要求刷新后重试。"
                    } else {
                        "只读说明：动作覆盖按 kind 整表回写，回读不一致会报错；不会清空其它 kind。"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("关闭") }
        },
    )
    editor?.let { (at, initial) ->
        RedactRuleEditorDialog(
            initial = initial,
            editing = at >= 0,
            writeVersion = writeVersion,
            saveError = error,
            onDismiss = { editor = null },
            onSave = { rule ->
                if (at >= 0) onEdit(at, rule) else onAdd(rule)
            },
        )
    }
}

/**
 * 规则编辑器。点保存后**不立刻关闭**：服务端 400/409/网络失败时，用户刚打的字必须还在
 * （原实现无条件 `editor = null`，错误只能落进已经关掉的父对话框）。关闭只在
 * [redactEditorOutcome] 判到 `Close`（写完计数前进）时发生。
 */
@Composable
private fun RedactRuleEditorDialog(
    initial: RedactCustomRule,
    editing: Boolean,
    writeVersion: Int,
    saveError: String?,
    onDismiss: () -> Unit,
    onSave: (RedactCustomRule) -> Unit,
) {
    var kind by remember(initial) { mutableStateOf(initial.kind) }
    var prefix by remember(initial) { mutableStateOf(initial.prefix) }
    var regex by remember(initial) { mutableStateOf(initial.regex) }
    var validation by remember(initial) { mutableStateOf<String?>(null) }
    var submitted by remember(initial) { mutableStateOf(false) }
    // 点保存那一刻的写完计数。ViewModel 每成功完成一次写入就 +1，所以
    // currentVersion > baselineVersion 是「这次保存成了」的权威信号。
    var baselineVersion by remember(initial) { mutableIntStateOf(0) }
    val outcome = redactEditorOutcome(
        submitting = submitted,
        baselineVersion = baselineVersion,
        currentVersion = writeVersion,
        error = saveError,
    )
    LaunchedEffect(outcome) {
        if (outcome == RedactEditorOutcome.Close) onDismiss()
    }
    // ViewModel 每次开始新一次写入都会清空 error，所以这里无条件透出即可；
    // 不能挂在 submitted 上——失败后 submitted 保持 true，错误才不会跟着消失。
    val shownError = validation ?: saveError
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (editing) "编辑自定义规则" else "添加自定义规则", fontWeight = FontWeight.Bold) },
        text = {
            Column {
                Text(
                    text = "kind 必填；前缀和正则二选一，同时填写时按前缀保存。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                RedactRuleField("规则类型（kind）", kind) { kind = it }
                RedactRuleField("前缀 prefix", prefix) { prefix = it }
                RedactRuleField("正则 regex", regex) { regex = it }
                shownError?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                }
                if (outcome == RedactEditorOutcome.Wait) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "正在保存并回读服务端…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    val candidate = RedactCustomRule(
                        kind.trim(),
                        prefix = prefix.trim(),
                        regex = regex.trim(),
                    )
                    // 只拦能精确镜像的约束（空内容、长度、条数）；正则可否编译由服务端判定。
                    val problem = validateRedactCustomRule(candidate)
                    if (problem != null) {
                        validation = problem
                    } else {
                        validation = null
                        baselineVersion = writeVersion
                        submitted = true
                        onSave(candidate)
                    }
                },
                style = McaButtonStyle.Primary,
                enabled = outcome != RedactEditorOutcome.Wait,
            ) { Text("保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

@Composable
private fun RedactRuleField(label: String, value: String, onValueChange: (String) -> Unit) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(4.dp))
        Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 10.dp),
            )
        }
    }
}

private fun actionLabel(action: String): String = when (action) {
    "mask" -> "脱敏"
    "warn" -> "告警"
    "block" -> "阻断"
    else -> action
}



@Composable
private fun CallerKeysSection(
    state: CallerKeysUiState,
    error: String?,
    onCreate: (String) -> Unit,
    onToggle: (CallerKeySummary) -> Unit,
    onRemove: (String) -> Unit,
) {
    var creating by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var deleting by remember { mutableStateOf<CallerKeySummary?>(null) }
    var confirmName by remember { mutableStateOf("") }
    Column {
        Text(
            text = "调用方密钥",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.height(6.dp))
        Surface(shape = McaShape.Card, color = McaTonal.fill()) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 14.dp, vertical = 8.dp),
            ) {
                when (state) {
                    CallerKeysUiState.Idle, CallerKeysUiState.Loading -> Text(
                        text = "正在读取调用方密钥…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    is CallerKeysUiState.Failed -> Text(
                        text = state.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    is CallerKeysUiState.Ready -> {
                        if (state.keys.isEmpty()) {
                            Text(
                                text = "还没有调用方密钥；局域网共享需要一个启用的密钥。",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        } else {
                            state.keys.forEach { key ->
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(vertical = 6.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Column(Modifier.weight(1f)) {
                                        Text(
                                            text = key.name.ifBlank { key.id },
                                            style = MaterialTheme.typography.bodyMedium,
                                            color = MaterialTheme.colorScheme.onSurface,
                                        )
                                        Text(
                                            text = "${key.masked} · ${if (key.off) "已停用" else "启用"} · ${key.usedTokens} tokens · $${"%.4f".format(java.util.Locale.US, key.usedCost)}",
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                    Spacer(Modifier.width(8.dp))
                                    McaButton(
                                        onClick = { onToggle(key) },
                                        style = McaButtonStyle.Outlined,
                                    ) { Text(if (key.off) "启用" else "停用") }
                                    Spacer(Modifier.width(6.dp))
                                    McaButton(
                                        onClick = {
                                            confirmName = ""
                                            deleting = key
                                        },
                                        style = McaButtonStyle.Outlined,
                                    ) { Text("删除") }
                                }
                            }
                        }
                    }
                }
                if (error != null) {
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(6.dp))
                McaButton(onClick = { creating = true }, style = McaButtonStyle.Primary) {
                    Text("新建密钥")
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "明文只在创建时展示一次，不写日志/截图/剪贴板；本机仅保存加密密文。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
    val target = deleting
    if (target != null) {
        McaDialog(
            onDismissRequest = { deleting = null },
            title = { Text("删除调用方密钥", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) },
            text = {
                Column {
                    Text(
                        text = "将删除「${target.name.ifBlank { target.id }}」；使用该密钥的客户端会立即被拒绝。输入名称确认：",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(10.dp))
                    Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
                        BasicTextField(
                            value = confirmName,
                            onValueChange = { confirmName = it },
                            singleLine = true,
                            textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 12.dp, vertical = 10.dp),
                        )
                    }
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val id = target.id
                        deleting = null
                        onRemove(id)
                    },
                    style = McaButtonStyle.Primary,
                    enabled = confirmName.trim() == target.name.ifBlank { target.id },
                ) { Text("删除") }
            },
            dismissButton = {
                McaButton(onClick = { deleting = null }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }
    if (creating) {
        McaDialog(
            onDismissRequest = { creating = false },
            title = { Text("新建调用方密钥", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) },
            text = {
                Column {
                    Text(
                        text = "名称仅用于本机识别；留空则使用默认名称。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(10.dp))
                    Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
                        BasicTextField(
                            value = name,
                            onValueChange = { name = it },
                            singleLine = true,
                            textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 12.dp, vertical = 10.dp),
                        )
                    }
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        creating = false
                        onCreate(name)
                        name = ""
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("创建") }
            },
            dismissButton = {
                McaButton(onClick = { creating = false }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }
}

@Composable
private fun CallerSecretDialog(secret: String, onDismiss: () -> Unit) {
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("调用方密钥（仅显示一次）", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) },
        text = {
            Column {
                Text(
                    text = "关闭后无法再次查看；请自行抄写到使用方。不要截图。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                Spacer(Modifier.height(10.dp))
                Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
                    Text(
                        text = secret,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(12.dp),
                    )
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) { Text("已抄写，关闭") }
        },
    )
}



@Composable
private fun AccessListRow(
    label: String,
    current: List<String>,
    saving: Boolean,
    error: String?,
    placeholder: String,
    onSave: (List<String>) -> Unit,
) {
    var editing by remember { mutableStateOf(false) }
    var draft by remember(current) { mutableStateOf(current) }
    var entry by remember { mutableStateOf("") }
    SettingsToggleRow(
        label = label,
        value = if (current.isEmpty()) "未配置" else "${current.size} 项",
        actionLabel = "管理",
        enabled = !saving,
        onAction = {
            draft = current
            entry = ""
            editing = true
        },
    )
    if (error != null) {
        Text(
            text = error,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )
    }
    if (editing) {
        McaDialog(
            onDismissRequest = { editing = false },
            title = {
                Text(
                    text = label,
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                )
            },
            text = {
                Column(
                    modifier = Modifier
                        .heightIn(max = 380.dp)
                        .verticalScroll(rememberScrollState()),
                ) {
                    if (draft.isEmpty()) {
                        Text(
                            text = "当前为空",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    draft.forEach { item ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(vertical = 3.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                text = item,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurface,
                                modifier = Modifier.weight(1f),
                            )
                            McaButton(
                                onClick = { draft = draft - item },
                                style = McaButtonStyle.Outlined,
                            ) { Text("移除") }
                        }
                    }
                    Spacer(Modifier.height(8.dp))
                    Surface(
                        shape = McaShape.CardAction,
                        color = MaterialTheme.colorScheme.surface,
                        border = McaHairline.stroke(),
                    ) {
                        BasicTextField(
                            value = entry,
                            onValueChange = { entry = it },
                            singleLine = true,
                            textStyle = MaterialTheme.typography.bodyMedium.copy(
                                color = MaterialTheme.colorScheme.onSurface,
                            ),
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 12.dp, vertical = 10.dp),
                            decorationBox = { inner ->
                                if (entry.isEmpty()) {
                                    Text(
                                        text = placeholder,
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                inner()
                            },
                        )
                    }
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = "保存为整表替换；服务端会校验格式并拒绝非法条目。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val value = entry.trim()
                        val next = if (value.isNotEmpty() && value !in draft) draft + value else draft
                        onSave(next)
                        editing = false
                    },
                    style = McaButtonStyle.Primary,
                    enabled = !saving,
                ) { Text(if (saving) "保存中…" else "保存") }
            },
            dismissButton = {
                McaButton(onClick = { editing = false }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }
}

/**
 * 首选端口。手机端**不做**运行中热切换——移动运行时从不注册 `Gateway.SetPortSwitcher`，
 * `POST /api/settings/port` 在移动模式必定 400。这里只落盘一个期望值，下次启动
 * 由 `GatewayServiceController` 交给 `nativeStart`，所以文案必须写「下次启动生效」，
 * 不能让用户以为保存完端口就变了。
 */
@Composable
private fun PreferredPortRow(
    preferredPort: Int,
    saving: Boolean,
    error: String?,
    notice: String?,
    onSave: (String) -> Unit,
) {
    var editing by remember { mutableStateOf(false) }
    var draft by remember(preferredPort) {
        mutableStateOf(if (preferredPort > 0) preferredPort.toString() else "")
    }
    Column(Modifier.padding(vertical = 5.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "首选端口",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(10.dp))
            Text(
                text = if (preferredPort > 0) preferredPort.toString() else "系统分配",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.width(10.dp))
            McaButton(
                onClick = { editing = !editing },
                style = McaButtonStyle.Outlined,
            ) { Text(if (editing) "收起" else "设置") }
        }
        if (editing) {
            Spacer(Modifier.height(6.dp))
            Surface(
                shape = McaShape.CardAction,
                color = MaterialTheme.colorScheme.surface,
                border = McaHairline.stroke(),
            ) {
                BasicTextField(
                    value = draft,
                    onValueChange = { draft = it.filter { ch -> ch.isDigit() }.take(5) },
                    singleLine = true,
                    textStyle = MaterialTheme.typography.bodyMedium.copy(
                        color = MaterialTheme.colorScheme.onSurface,
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 12.dp, vertical = 10.dp),
                )
            }
            Spacer(Modifier.height(4.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = "留空 = 每次交给系统分配；范围 1-65535",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.weight(1f),
                )
                Spacer(Modifier.width(8.dp))
                McaButton(
                    onClick = { onSave(draft) },
                    style = McaButtonStyle.Primary,
                    enabled = !saving,
                ) { Text(if (saving) "保存中…" else "保存") }
            }
        }
        notice?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }
        error?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        Text(
            text = "手机端不支持运行中热切换端口：保存后下次启动生效，当前运行中的端口不变；" +
                "端口被占用时启动会失败并给出稳定错误码（listen_failed）。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun SettingsSections(
    settings: SettingsSummary,
    status: GatewayStatus,
    saveState: SettingsSaveState,
    onToggleRedact: (Boolean) -> Unit,
    personalSaving: Boolean,
    personalError: String?,
    onToggleRedactPersonal: (Boolean) -> Unit,
    onOpenRedactDetail: () -> Unit,
    onOpenRedactRules: () -> Unit,
    lanSaving: Boolean,
    lanError: String?,
    onToggleLan: (Boolean) -> Unit,
    proxySaving: Boolean,
    proxyError: String?,
    onSaveProxy: (String) -> Unit,
    corsSaving: Boolean,
    corsError: String?,
    onSaveCors: (List<String>) -> Unit,
    ipsSaving: Boolean,
    ipsError: String?,
    onSaveAllowIPs: (List<String>) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SettingsSection("网关") {
            SettingsRow("监听地址", settings.addr.ifBlank { status.addr.ifBlank { "—" } })
            SettingsRow(
                "当前端口",
                when {
                    settings.port > 0 -> settings.port.toString()
                    status.addr.substringAfterLast(':', "").isNotBlank() ->
                        status.addr.substringAfterLast(':')
                    else -> "—"
                },
            )
            SettingsToggleRow(
                label = "局域网共享",
                value = if (settings.lanEnabled) "开启" else "关闭",
                actionLabel = if (settings.lanEnabled) "关闭" else "开启",
                enabled = !lanSaving,
                onAction = { onToggleLan(!settings.lanEnabled) },
            )
            if (lanError != null) {
                Text(
                    text = lanError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            var proxyEditing by remember { mutableStateOf(false) }
            var proxyDraft by remember { mutableStateOf("") }
            SettingsToggleRow(
                label = "全局代理",
                value = settings.proxyMasked.ifBlank { "未设置" },
                actionLabel = if (proxyEditing) "收起" else "设置",
                enabled = !proxySaving,
                onAction = {
                    proxyDraft = ""
                    proxyEditing = !proxyEditing
                },
            )
            if (proxyEditing) {
                Surface(
                    shape = McaShape.CardAction,
                    color = MaterialTheme.colorScheme.surface,
                    border = McaHairline.stroke(),
                ) {
                    BasicTextField(
                        value = proxyDraft,
                        onValueChange = { proxyDraft = it },
                        singleLine = true,
                        textStyle = MaterialTheme.typography.bodyMedium.copy(
                            color = MaterialTheme.colorScheme.onSurface,
                        ),
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 12.dp, vertical = 10.dp),
                        decorationBox = { inner ->
                            if (proxyDraft.isEmpty()) {
                                Text(
                                    text = "socks5://127.0.0.1:7890",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            inner()
                        },
                    )
                }
                Spacer(Modifier.height(6.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { onSaveProxy(proxyDraft) },
                        style = McaButtonStyle.Primary,
                        enabled = !proxySaving,
                    ) { Text(if (proxySaving) "保存中…" else "保存") }
                    McaButton(
                        onClick = { onSaveProxy("") },
                        style = McaButtonStyle.Outlined,
                        enabled = !proxySaving && settings.proxyMasked.isNotBlank(),
                    ) { Text("清除") }
                    McaButton(
                        onClick = { proxyEditing = false },
                        style = McaButtonStyle.Outlined,
                    ) { Text("取消") }
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "仅保存无凭据的 http/https/socks5 地址；带 user:pass@ 的代理请在 Windows 端配置。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (proxyError != null) {
                Text(
                    text = proxyError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            AccessListRow(
                label = "CORS 来源",
                current = settings.corsOrigins,
                saving = corsSaving,
                error = corsError,
                placeholder = "http://localhost:3000",
                onSave = onSaveCors,
            )
            AccessListRow(
                label = "IP 白名单",
                current = settings.allowIPs,
                saving = ipsSaving,
                error = ipsError,
                placeholder = "10.0.0.0/8",
                onSave = onSaveAllowIPs,
            )
        }
        SettingsSection("隐私与脱敏") {
            SettingsToggleRow(
                label = "脱敏引擎",
                value = if (settings.redactEnabled) "开启" else "关闭",
                actionLabel = if (settings.redactEnabled) "关闭" else "开启",
                enabled = saveState !is SettingsSaveState.Saving,
                onAction = { onToggleRedact(!settings.redactEnabled) },
            )
            if (saveState is SettingsSaveState.Failed) {
                Text(
                    text = saveState.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            SettingsToggleRow(
                label = "个人信息",
                value = if (settings.redactPersonal) "开启" else "关闭",
                actionLabel = if (settings.redactPersonal) "关闭" else "开启",
                enabled = !personalSaving,
                onAction = { onToggleRedactPersonal(!settings.redactPersonal) },
            )
            if (personalError != null) {
                Text(
                    text = personalError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            SettingsToggleRow(
                label = "内置动作",
                value = if (settings.redactActions.isEmpty()) "默认" else "${settings.redactActions.size} 项覆盖",
                actionLabel = "查看",
                enabled = true,
                onAction = onOpenRedactDetail,
            )
            SettingsToggleRow(
                label = "自定义规则",
                value = "${settings.redactRuleCount} 条",
                actionLabel = "管理",
                enabled = true,
                onAction = onOpenRedactRules,
            )
            SettingsRow("白名单", "${settings.redactAllowCount} 条")
        }
        SettingsSection("平台") {
            SettingsRow("本机运行形态", if (settings.desktopApp) "桌面端" else "Android 独立运行")
            SettingsRow("仅 Windows 能力", "不在手机端提供")
        }
    }
}

private fun countLabel(count: Int): String = if (count == 0) "未配置" else "$count 项"

@Composable
private fun SettingsSection(title: String, content: @Composable () -> Unit) {
    Column {
        Text(
            text = title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.height(6.dp))
        Surface(shape = McaShape.Card, color = McaTonal.fill()) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 14.dp, vertical = 8.dp),
            ) {
                content()
            }
        }
    }
}

@Composable
private fun SettingsToggleRow(
    label: String,
    value: String,
    actionLabel: String,
    enabled: Boolean,
    onAction: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(10.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.width(10.dp))
        McaButton(
            onClick = onAction,
            style = McaButtonStyle.Outlined,
            enabled = enabled,
        ) { Text(actionLabel) }
    }
}

@Composable
private fun SettingsRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(10.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
        )
    }
}

@Composable
private fun SettingsStateCard(
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
private fun SettingsLoadingCard() {
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
                text = "正在读取本机设置…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun SettingsErrorCard(code: String, message: String, onRetry: () -> Unit) {
    Surface(shape = McaShape.Sheet, color = MaterialTheme.colorScheme.errorContainer) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = "读取设置失败",
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
