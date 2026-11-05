package com.muyuchat.aether.ui

import android.content.Intent
import android.app.Application
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.lazy.items
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
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Checkbox
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.CallerKeysApiClient
import com.muyuchat.aether.runtime.CallerModelCatalog
import com.muyuchat.aether.runtime.CallerModelOption
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.MaskingApiClient
import com.muyuchat.aether.runtime.MaskingPreview
import com.muyuchat.aether.runtime.MaskingRecords
import com.muyuchat.aether.runtime.RedactCustomRule
import com.muyuchat.aether.runtime.RedactRule
import com.muyuchat.aether.runtime.effectiveAction
import com.muyuchat.aether.runtime.maskingHitSummary
import com.muyuchat.aether.runtime.maskingPreviewBlockedNote
import com.muyuchat.aether.runtime.maskingPreviewOffNote
import com.muyuchat.aether.runtime.maskingRecordHitLabel
import com.muyuchat.aether.runtime.maskingRecordSourceLine
import com.muyuchat.aether.runtime.maskingRecordsEmptyDetail
import com.muyuchat.aether.runtime.maskingTimestamp
import com.muyuchat.aether.runtime.MaskingRecord
import com.muyuchat.aether.runtime.validateRedactCustomRule
import com.muyuchat.aether.runtime.RemoteQuotaDraft
import com.muyuchat.aether.runtime.RemoteQuotaSourceRow
import com.muyuchat.aether.runtime.ProfileRow
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.profileClientLabel
import com.muyuchat.aether.runtime.SettingsSummary
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

@Composable
internal fun SettingsPage(
    status: GatewayStatus,
    state: SettingsUiState,
    onRefresh: () -> Unit,
    onBack: () -> Unit = {},
    /** 非空时只渲染该设置分组（01–08 与 10 关于），并在顶部给「返回 + 组标题」。由 SettingsRoute 分发。 */
    initialGroup: String? = null,
    onOpenOverview: () -> Unit,
    onOpenBackup: () -> Unit,
    onShareReport: (Intent) -> Unit = {},
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
    val listState = rememberLazyListState()
    // 聚焦某个分组时滚到顶部：LazyColumn 用 scrollToItem(0)（不是 ScrollState.animateScrollTo）。
    LaunchedEffect(initialGroup) {
        if (initialGroup != null) {
            listState.scrollToItem(0)
        }
    }
    LazyColumn(
        state = listState,
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        // ——— 聚焦模式：只渲染目标分组，顶部「‹ 返回 + 组标题」 ———
        if (initialGroup != null) {
            item {
                FocusedSettingsHeader(
                    title = settingsGroupTitle(initialGroup) ?: "设置",
                    onBack = onBack,
                )
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
                    val blocks = settingsBlocksFor(initialGroup)
                    if (blocks.isEmpty()) {
                        item {
                            SettingsStateCard(
                                title = "找不到设置分组",
                                detail = "分组「$initialGroup」不是设置页里的分区（01–08 与 10 关于）。",
                                actionLabel = "返回",
                                onAction = onBack,
                            )
                        }
                    } else {
                        blocks.forEach { block ->
                            item {
                                when (block.id) {
                                    "01" -> SettingsGeneralSection(status = status)
                                    "02" -> Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                                        SettingsSection("本机监听") {
                                            Text(
                                                text = "手机端特有：桌面版的端口由启动参数决定（设置里只读），手机端必须在页内改。",
                                                style = MaterialTheme.typography.bodySmall,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            )
                                            PreferredPortRow(
                                                preferredPort = preferredPort,
                                                saving = preferredPortSaving,
                                                error = preferredPortError,
                                                notice = preferredPortNotice,
                                                onSave = onSavePreferredPort,
                                            )
                                        }
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
                                        CallerKeysSection(
                                            status = status,
                                            state = callerKeys,
                                            error = keysError,
                                            onCreate = onCreateCallerKey,
                                            onToggle = onToggleCallerKey,
                                            onRemove = onRemoveCallerKey,
                                            onReload = onLoadCallerKeys,
                                        )
                                    }
                                    "03" -> Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                                        SettingsToolsSection(status = status)
                                        SettingsRemoteQuotasSection(status = status)
                                    }
                                    "04" -> SettingsPresetsSection(status = status)
                                    "05" -> SettingsPricingSection(status = status)
                                    "06" -> SettingsModelsSection(status = status)
                                    "07" -> SettingsPrivacySection(status = status)
                                    "08" -> SettingsObservabilitySection(status = status)
                                    "10" -> Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                                        SettingsAboutSection(status = status)
                                        SettingsProfilesSection(status = status)
                                    }
                                }
                            }
                        }
                    }
                }
            }
            return@LazyColumn
        }
        item { MobileHeader(status) }
        item {
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
                        title = "设置",
                        subtitle = "本机配置 · 只读",
                    )
                }
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_refresh,
                    contentDescription = "刷新设置",
                    onClick = onRefresh,
                    enabled = state is SettingsUiState.Ready || state is SettingsUiState.Failed,
                )
            }
        }
        // 首选端口是**本机运行配置**，不是网关状态：必须始终可编辑。
        // 否则首选端口被占用 → 启动失败 → 设置页因「网关未启动」拒绝渲染 → 用户再也改不回来那个端口。
        // 标题用「本机监听」：桌面把端口放在启动参数里（settings.js:216 的「监听地址」只读），
        // 手机端必须能在页内改，所以单列一节并注明这是手机端才有的。
        item {
            SettingsSection("本机监听") {
                Text(
                    text = "手机端特有：桌面版的端口由启动参数决定（设置里只读），手机端必须在页内改，" +
                        "否则端口被占用后没有入口改回来。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                PreferredPortRow(
                    preferredPort = preferredPort,
                    saving = preferredPortSaving,
                    error = preferredPortError,
                    notice = preferredPortNotice,
                    onSave = onSavePreferredPort,
                )
            }
        }
        item {
            HealthReportSection(
                status = status,
                settings = (state as? SettingsUiState.Ready)?.settings,
                onShare = onShareReport,
            )
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
                        status = status,
                        state = callerKeys,
                        error = keysError,
                        onCreate = onCreateCallerKey,
                        onToggle = onToggleCallerKey,
                        onRemove = onRemoveCallerKey,
                        onReload = onLoadCallerKeys,
                    )
                }
                // 块表驱动（顺序 = 现在屏幕上的顺序）：
                // 常规 → 定价 → 隐私 → 工具服务 → 预设 → 模型目录 → 可观测。
                settingsBlocksFor(initialGroup = null).forEach { block ->
                    item {
                        when (block.id) {
                            "01" -> SettingsGeneralSection(status = status)
                            "05" -> SettingsPricingSection(status = status)
                            "07" -> SettingsPrivacySection(status = status)
                            "03" -> SettingsToolsSection(status = status)
                            "04" -> SettingsPresetsSection(status = status)
                            "06" -> SettingsModelsSection(status = status)
                            "08" -> SettingsObservabilitySection(status = status)
                            "10" -> SettingsAboutSection(status = status)
                        }
                    }
                }
            }
        }
        // 桌面设置的第 9 个 tab「同步与备份」（settings.js:45 TABS）。
        // 手机端只做导出与导入（备份页）；云同步要 WebDAV 口令，桌面端才有。
        item {
            SettingsSection("同步与备份") {
                SettingsRow("配置备份", "口令密封导出与导入；不含账号凭证")
                McaButton(onClick = onOpenBackup, style = McaButtonStyle.Outlined) {
                    Text("打开备份…")
                }
                Text(
                    text = "云同步（WebDAV）只在桌面端提供：手机端没有填写远端地址与口令的入口，" +
                        "也不把口令存进本机设置。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
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
    // 删除规则会让这条规则立刻停止生效（可能要漏掉本该脱敏的内容），所以二次确认。
    var deleteTarget by remember { mutableStateOf<Int?>(null) }
    deleteTarget?.let { index ->
        McaDialog(
            onDismissRequest = { deleteTarget = null },
            title = {
                Text(
                    text = "删除这条脱敏规则？",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                )
            },
            text = {
                Text(
                    text = "删掉后它立刻不再生效，命中的内容会原样发给上游。" +
                        "如果只是暂时不想用，可以改成「只记录」而不是删除。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        deleteTarget = null
                        onDelete(index)
                    },
                    style = McaButtonStyle.Destructive,
                ) { Text("删除") }
            },
            dismissButton = {
                McaButton(onClick = { deleteTarget = null }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }
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
                            Spacer(Modifier.height(8.dp))
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
                            Spacer(Modifier.height(8.dp))
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
                                    onClick = { deleteTarget = index },
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
                    Spacer(Modifier.height(8.dp))
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
                    // BasicTextField 不吃 Compose 的触摸目标扩展，自己撑到 48dp；
                    // 垂直 padding 放在 heightIn 之后会把点按区压回一行字。
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
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
    status: GatewayStatus,
    state: CallerKeysUiState,
    error: String?,
    onCreate: (String) -> Unit,
    onToggle: (CallerKeySummary) -> Unit,
    onRemove: (String) -> Unit,
    onReload: () -> Unit,
) {
    var creating by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var deleting by remember { mutableStateOf<CallerKeySummary?>(null) }
    var confirmName by remember { mutableStateOf("") }
    var editingModels by remember { mutableStateOf<CallerKeySummary?>(null) }
    Column {
        DeepSectionTitle(
            text = "调用方密钥",
            trailing = when (state) {
                is CallerKeysUiState.Ready -> countLabel(state.keys.size)
                else -> null
            },
        )
        Spacer(Modifier.height(8.dp))
        // 空态不塞进「密钥卡」里再套一层卡：整块换成 DeepStateCard（一层 Sheet）。
        val isEmptyReady = state is CallerKeysUiState.Ready && state.keys.isEmpty()
        if (isEmptyReady) {
            DeepStateCard(
                title = "还没有调用方密钥",
                detail = "局域网共享需要一个启用的密钥；手机上可新建，明文只在创建时展示一次。",
                actionLabel = "新建密钥",
                onAction = { creating = true },
            )
        } else {
            DeepCard(contentPadding = PaddingValues(horizontal = DeepCardPadding)) {
                when (state) {
                    CallerKeysUiState.Idle, CallerKeysUiState.Loading -> Column(
                        Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
                    ) { DeepCardBody("正在读取调用方密钥…") }
                    is CallerKeysUiState.Failed -> Column(
                        Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
                    ) {
                        Text(
                            text = state.message,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                    is CallerKeysUiState.Ready -> state.keys.forEach { key ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(vertical = DeepRowVertical),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                // M2：1.3× 下「名称 + 掩码·状态·tokens·费用」会折行把卡片顶高。
                                // 单行 + 省略永不折行；被省略的细节在删除确认框里仍能看到完整名称。
                                Text(
                                    text = key.name.ifBlank { key.id },
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurface,
                                    maxLines = 1,
                                    softWrap = false,
                                    overflow = TextOverflow.Ellipsis,
                                )
                                Text(
                                    text = callerKeyMetaLine(key),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    maxLines = 1,
                                    softWrap = false,
                                    overflow = TextOverflow.Ellipsis,
                                )
                                Text(
                                    text = callerKeyModelsSummary(key.models),
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.primary,
                                    maxLines = 1,
                                    softWrap = false,
                                    overflow = TextOverflow.Ellipsis,
                                    modifier = Modifier
                                        .clickable { editingModels = key }
                                        .padding(top = 4.dp),
                                )
                            }
                            Spacer(Modifier.width(8.dp))
                            McaButton(
                                onClick = { onToggle(key) },
                                style = McaButtonStyle.Outlined,
                            ) { Text(if (key.off) "启用" else "停用") }
                            Spacer(Modifier.width(8.dp))
                            McaButton(
                                onClick = {
                                    confirmName = ""
                                    deleting = key
                                },
                                style = McaButtonStyle.Outlined,
                                // 删除是危险动作：只染动作文字，容器与其余行不动。
                                contentColor = MaterialTheme.colorScheme.error,
                            ) { Text("删除") }
                        }
                    }
                }
            if (error != null) {
                Column(Modifier.fillMaxWidth().padding(vertical = DeepRowVertical)) {
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
            Column(
                Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                McaButton(onClick = { creating = true }, style = McaButtonStyle.Primary) {
                    Text("新建密钥")
                }
                DeepCardBody("明文只在创建时展示一次，不写日志/截图/剪贴板；本机仅保存加密密文。")
            }
            }
        }
    }
    val target = deleting
    if (target != null) {
        McaDialog(
            onDismissRequest = { deleting = null },
            title = { Text("删除调用方密钥", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) },
            text = {
                // 内容限高 + 可滚动：软键盘弹起时对话框不会长到把按钮顶到键盘下面。
                Column(
                    modifier = Modifier
                        .heightIn(max = 132.dp)
                        .verticalScroll(rememberScrollState()),
                ) {
                    Text(
                        text = "将删除「${target.name.ifBlank { target.id }}」；使用该密钥的客户端会立即被拒绝。输入名称确认：",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(8.dp))
                    Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
                        BasicTextField(
                            value = confirmName,
                            onValueChange = { confirmName = it },
                            singleLine = true,
                            textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                            modifier = Modifier
                                .fillMaxWidth()
                                .heightIn(min = 48.dp)
                                .wrapContentHeight(Alignment.CenterVertically)
                                .padding(horizontal = 12.dp)
                                .semantics { contentDescription = "输入密钥名称以确认删除" },
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
                    style = McaButtonStyle.Outlined,
                    contentColor = MaterialTheme.colorScheme.error,
                    enabled = callerKeyDeleteConfirmed(confirmName, target.name, target.id),
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
                    Spacer(Modifier.height(8.dp))
                    Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
                        BasicTextField(
                            value = name,
                            onValueChange = { name = it },
                            singleLine = true,
                            textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                            modifier = Modifier
                                .fillMaxWidth()
                                .heightIn(min = 48.dp)
                                .wrapContentHeight(Alignment.CenterVertically)
                                .padding(horizontal = 12.dp)
                                .semantics { contentDescription = "密钥名称（可留空）" },
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
    val editing = editingModels
    if (editing != null) {
        CallerModelsDialog(
            status = status,
            key = editing,
            onDismiss = { editingModels = null },
            onSaved = {
                editingModels = null
                onReload()
            },
        )
    }
}

@Composable
private fun CallerModelsDialog(
    status: GatewayStatus,
    key: CallerKeySummary,
    onDismiss: () -> Unit,
    onSaved: () -> Unit,
) {
    val client = remember { CallerKeysApiClient() }
    val scope = rememberCoroutineScope()
    var catalog by remember(key.id) { mutableStateOf<CallerModelCatalog?>(null) }
    var selected by remember(key.id) { mutableStateOf(key.models.toSet()) }
    var loading by remember(key.id) { mutableStateOf(true) }
    var error by remember(key.id) { mutableStateOf<String?>(null) }
    var saving by remember(key.id) { mutableStateOf(false) }
    LaunchedEffect(key.id, status.addr) {
        loading = true
        error = null
        runCatching {
            withContext(Dispatchers.IO) {
                // 先 GET 完整 key 对象，拿到服务端当前白名单；再拉模型选择器。
                val fresh = client.fetch(status).keys.firstOrNull { it.id == key.id }
                    ?: throw ManagementApiException("key_missing", "密钥已不存在，请刷新列表")
                fresh to client.fetchModels(status)
            }
        }.onSuccess { (fresh, models) ->
            selected = fresh.models.toSet()
            catalog = models
        }.onFailure { error = it.message ?: "读取模型白名单失败" }
        loading = false
    }
    McaDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        title = {
            Text(
                text = "模型白名单 · ${key.name.ifBlank { key.id }}",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    text = "不勾选 = 不限制（全部模型）。这里只改白名单；名称、限流、启停状态保持不变。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                when {
                    loading -> Text(
                        text = "正在读取模型清单…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    error != null -> Text(
                        text = error ?: "",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    catalog != null -> {
                        Text(
                            text = if (selected.isEmpty()) "当前：不限制" else "当前已选 ${selected.size} 个模型",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.primary,
                        )
                        LazyColumn(Modifier.fillMaxWidth().heightIn(max = 360.dp)) {
                            items(catalog?.models.orEmpty(), key = { it.id }) { model ->
                                ModelWhitelistRow(
                                    model = model,
                                    checked = model.id in selected,
                                    onToggle = {
                                        selected = if (model.id in selected) {
                                            selected - model.id
                                        } else {
                                            selected + model.id
                                        }
                                    },
                                )
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    if (!saving) {
                        saving = true
                        scope.launch {
                            try {
                                withContext(Dispatchers.IO) {
                                    client.saveModels(status, key.id, selected.toList())
                                }
                                onSaved()
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: ManagementApiException) {
                                error = e.message
                            } catch (_: Exception) {
                                error = "保存模型白名单失败，请重试"
                            } finally {
                                saving = false
                            }
                        }
                    }
                },
                style = McaButtonStyle.Primary,
                enabled = !saving && catalog != null,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined, enabled = !saving) {
                Text("取消")
            }
        },
    )
}

@Composable
private fun ModelWhitelistRow(
    model: CallerModelOption,
    checked: Boolean,
    onToggle: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onToggle)
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = checked, onCheckedChange = { onToggle() })
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = model.name,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                softWrap = false,
                overflow = TextOverflow.Ellipsis,
            )
            if (model.provider.isNotBlank()) {
                Text(
                    text = model.provider,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    softWrap = false,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
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
                Spacer(Modifier.height(8.dp))
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
                                .padding(vertical = 8.dp),
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
                                .heightIn(min = 48.dp)
                                .wrapContentHeight(Alignment.CenterVertically)
                                .padding(horizontal = 12.dp)
                                .semantics { contentDescription = placeholder },
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
    Column(Modifier.padding(vertical = DeepRowVertical)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "首选端口",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (preferredPort > 0) preferredPort.toString() else "系统分配",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = { editing = !editing },
                style = McaButtonStyle.Outlined,
            ) { Text(if (editing) "收起" else "设置") }
        }
        if (editing) {
            Spacer(Modifier.height(8.dp))
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
                        .heightIn(min = 48.dp)
                        .wrapContentHeight(Alignment.CenterVertically)
                        .padding(horizontal = 12.dp)
                        .semantics { contentDescription = "首选端口" },
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
        SettingsSection("网络与共享") {
            // 桌面顺序（settings.js:212-226）：监听（局域网访问 / 监听地址）→ 网络（代理 / CORS / 允许的 IP）。
            // 手机端把「局域网访问」放在最前，并明确它是只读的桌面端开关。
            // 局域网共享在手机端**只读**：移动运行时 Start 里只绑 127.0.0.1，
            // 非回环直接返回 listen_host_must_be_loopback。开这个开关只会翻一个配置位，
            // 永远不会有局域网监听——那是假开关，所以这里不提供切换。
            SettingsToggleRow(
                label = "局域网访问",
                value = (if (settings.lanEnabled) "配置为开启" else "关闭") + " · 仅桌面端生效",
                actionLabel = "仅桌面端",
                enabled = false,
                onAction = {},
            )
            Text(
                text = "手机端网关只监听 127.0.0.1（启动时即拒绝非回环地址），所以这里开关不生效；" +
                    "需要局域网共享请在桌面端开启。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
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
            SettingsPathRow(
                label = "数据目录",
                value = settings.dataDir.ifBlank { "—" },
                emptyNote = if (settings.dataDir.isBlank()) "网关未上报" else null,
            )
            Text(
                text = "账号、库、备份与日志都存在这个目录；桌面端可改，手机端固定。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            SettingsSecuritySection(status = status)
            if (lanError != null) {
                Text(
                    text = lanError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            var proxyEditing by remember { mutableStateOf(false) }
            var proxyDraft by remember { mutableStateOf("") }
            var proxyClearConfirm by remember { mutableStateOf(false) }
            if (proxyClearConfirm) {
                McaDialog(
                    onDismissRequest = { proxyClearConfirm = false },
                    title = {
                        Text(
                            text = "清除全局代理？",
                            style = MaterialTheme.typography.titleLarge,
                            fontWeight = FontWeight.Bold,
                        )
                    },
                    text = {
                        Text(
                            text = "会从本机设置里删掉这个代理地址（${settings.proxyMasked.ifBlank { "未设置" }}），" +
                                "不影响账号与其它设置，保存后立即生效。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    },
                    confirmButton = {
                        McaButton(
                            onClick = {
                                proxyClearConfirm = false
                                onSaveProxy("")
                            },
                            style = McaButtonStyle.Destructive,
                        ) { Text("清除") }
                    },
                    dismissButton = {
                        McaButton(
                            onClick = { proxyClearConfirm = false },
                            style = McaButtonStyle.Outlined,
                        ) { Text("取消") }
                    },
                )
            }
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
                            .heightIn(min = 48.dp)
                            .wrapContentHeight(Alignment.CenterVertically)
                            .padding(horizontal = 12.dp)
                            .semantics { contentDescription = "代理地址" },
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
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { onSaveProxy(proxyDraft) },
                        style = McaButtonStyle.Primary,
                        enabled = !proxySaving,
                    ) { Text(if (proxySaving) "保存中…" else "保存") }
                    // 清除是不可撤销的配置删除：二次确认（桌面对「关闭云同步 / 从云端恢复」这类
                    // 破坏性操作也都用 confirmDialog，见 settings.js:674/679）。
                    McaButton(
                        onClick = { proxyClearConfirm = true },
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
        SettingsSection("隐私") {
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
        // 桌面脱敏视图的两张卡（misc.js:444 试一试 / :460 最近的脱敏记录）。
        // 两张卡自带 ViewModel，因此不需要在 GatewayHome 增参。
        MaskingTryoutSection()
        MaskingRecordsSection()
        SettingsSection("关于") {
            SettingsRow("本机运行形态", if (settings.desktopApp) "桌面端" else "Android 独立运行")
            SettingsRow("仅 Windows 能力", "不在手机端提供")
            // 桌面「关于」tab 还有版本号与更新通道（settings.js: about），手机端拿不到
            // GET /api/update 的结果就不写死版本号——见汇报里的"未做"一条。
            SettingsRow("版本与更新", "未接入：手机端走应用商店/系统安装，不读 /api/update")
        }
    }
}

private fun countLabel(count: Int): String = if (count == 0) "未配置" else "$count 项"

/**
 * 调用方密钥行副标题：掩码 · 状态 · 用量。
 * 纯函数，便于单测；缺字段时也要能拼出可读文案，不抛异常。
 */
internal fun callerKeyMetaLine(key: CallerKeySummary): String = buildString {
    append(key.masked.ifBlank { "掩码缺失" })
    append(" · ")
    append(if (key.off) "已停用" else "启用")
    append(" · ")
    append(key.usedTokens)
    append(" tokens · $")
    append("%.4f".format(java.util.Locale.US, key.usedCost))
}

/** 白名单摘要必须单行可读；空列表是服务端的“不限制”，不能写成 0 个。 */
internal fun callerKeyModelsSummary(models: List<String>): String = when {
    models.isEmpty() -> "模型白名单：不限制（编辑）"
    models.size == 1 -> "模型白名单：${models.first()}（编辑）"
    else -> "模型白名单：${models.size} 个模型（编辑）"
}

@Composable
private fun SettingsSection(title: String, content: @Composable () -> Unit) {
    Column {
        // 分组标题与条目之间拉开：标题上内距 12（配列表 12 凑出区块间 24），下内距 8。
        DeepSectionTitle(title)
        Spacer(Modifier.height(8.dp))
        DeepCard(contentPadding = PaddingValues(horizontal = DeepCardPadding)) {
            content()
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
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        // 右值单行右对齐，动作按钮固定最小宽：1.3× 下「端口 / 开关 / 状态」这些值
        // 与上一行严格落在同一条竖线上，也不会因为 3 字动作词把值挤成两行。
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.End,
            maxLines = 1,
            softWrap = false,
        )
        Spacer(Modifier.width(8.dp))
        McaButton(
            onClick = onAction,
            modifier = Modifier.widthIn(min = 64.dp),
            style = McaButtonStyle.Outlined,
            enabled = enabled,
        ) { Text(actionLabel, maxLines = 1) }
    }
}

@Composable
private fun SettingsRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.End,
            maxLines = 1,
            softWrap = false,
        )
    }
}

@Composable
private fun SettingsPathRow(label: String, value: String, emptyNote: String?) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.Top,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            modifier = Modifier.width(96.dp),
        )
        Spacer(Modifier.width(8.dp))
        Column(
            modifier = Modifier.weight(1f),
            horizontalAlignment = Alignment.End,
        ) {
            Text(
                text = value,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                textAlign = TextAlign.End,
                maxLines = 1,
                softWrap = false,
                overflow = TextOverflow.Ellipsis,
            )
            if (emptyNote != null) {
                Text(
                    text = emptyNote,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.End,
                )
            }
        }
    }
}

@Composable
private fun SettingsStateCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    DeepStateCard(
        title = title,
        detail = detail,
        tone = DeepTone.Info,
        actionLabel = actionLabel,
        onAction = onAction,
    )
}

@Composable
private fun SettingsLoadingCard() {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(22.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(12.dp))
            DeepCardBody("正在读取本机设置…")
        }
    }
}

@Composable
private fun SettingsErrorCard(code: String, message: String, onRetry: () -> Unit) {
    DeepStateCard(
        title = "读取设置失败",
        detail = "$message（错误码：$code）",
        tone = DeepTone.Danger,
        actionLabel = "重试",
        onAction = onRetry,
    )
}

/* ============================================================================
 * 脱敏「试一试」+「最近的脱敏记录」
 *
 * 桌面出处：views/misc.js:444（card '试一试'）与 :460（card '最近的脱敏记录'）。
 * 端点：POST /api/masking/preview（gateway.go:1157 → masking_api.go:439-456）、
 *       GET  /api/masking/records（gateway.go:1154 → masking_api.go:270-337）。
 *
 * 两条硬边界：
 * 1) 预览文本只在本机内存里走一趟：不写设置、不落盘、不进脱敏记录
 *    （服务端 Preview() 明确"不记录、不写入内存映射"，redact.go:870-872）。
 * 2) 记录只展示"命中几处 / 命中哪些类型"；原文、占位符原值、实际发出的文本一律不读
 *    （masking_api.go:30 的口径，也符合架构文档"不展示请求正文"）。
 * ========================================================================== */

/** 预览输入上限：只防误粘贴整篇日志，服务端本身允许 1MB。 */
private const val MaskingPreviewMaxChars = 4000

sealed interface MaskingPreviewUiState {
    data object Idle : MaskingPreviewUiState
    data object Running : MaskingPreviewUiState
    data class Ready(val preview: MaskingPreview) : MaskingPreviewUiState
    data class Failed(val code: String, val message: String) : MaskingPreviewUiState
}

sealed interface MaskingRecordsUiState {
    data object Idle : MaskingRecordsUiState
    data object Loading : MaskingRecordsUiState
    data class Ready(val records: MaskingRecords) : MaskingRecordsUiState
    data class Failed(val code: String, val message: String) : MaskingRecordsUiState
}

/** 两张脱敏卡共用的状态（预览草稿 + 记录列表）。草稿只活在内存，不写任何设置。 */
class MaskingToolsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = MaskingApiClient()

    private val _text = MutableStateFlow("")
    val text: StateFlow<String> = _text.asStateFlow()

    private val _preview = MutableStateFlow<MaskingPreviewUiState>(MaskingPreviewUiState.Idle)
    val preview: StateFlow<MaskingPreviewUiState> = _preview.asStateFlow()

    private val _records = MutableStateFlow<MaskingRecordsUiState>(MaskingRecordsUiState.Idle)
    val records: StateFlow<MaskingRecordsUiState> = _records.asStateFlow()

    fun setText(value: String) {
        _text.value = value.take(MaskingPreviewMaxChars)
    }

    fun runPreview() {
        val status = repository.status.value
        if (!status.running) {
            _preview.value = MaskingPreviewUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _preview.value = MaskingPreviewUiState.Running
        viewModelScope.launch {
            _preview.value = try {
                MaskingPreviewUiState.Ready(
                    withContext(Dispatchers.IO) { client.preview(status, _text.value) },
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                MaskingPreviewUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                MaskingPreviewUiState.Failed("unknown", "预览失败，请重试")
            }
        }
    }

    fun loadRecords() {
        val status = repository.status.value
        if (!status.running) {
            _records.value = MaskingRecordsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (_records.value !is MaskingRecordsUiState.Ready) {
            _records.value = MaskingRecordsUiState.Loading
        }
        viewModelScope.launch {
            _records.value = try {
                MaskingRecordsUiState.Ready(withContext(Dispatchers.IO) { client.fetchRecords(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                MaskingRecordsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                MaskingRecordsUiState.Failed("unknown", "读取脱敏记录失败，请重试")
            }
        }
    }
}

/** 试一试：输入 → 预览。整段文本只在本机内存与预览接口之间流动。 */
@Composable
internal fun MaskingTryoutSection(viewModel: MaskingToolsViewModel = viewModel()) {
    val text by viewModel.text.collectAsStateWithLifecycle()
    val preview by viewModel.preview.collectAsStateWithLifecycle()

    SettingsSection("试一试") {
        Text(
            text = "只预览，不发请求：这段文本不会写进设置、不会落盘，也不会进入下面的脱敏记录。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = text,
                onValueChange = viewModel::setText,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                    fontFamily = FontFamily.Monospace,
                ),
                minLines = 3,
                maxLines = 8,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 10.dp),
            )
        }
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            McaButton(
                onClick = viewModel::runPreview,
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
                enabled = text.isNotBlank(),
                loading = preview is MaskingPreviewUiState.Running,
            ) {
                Text(if (preview is MaskingPreviewUiState.Running) "预览中…" else "预览")
            }
            Spacer(Modifier.width(8.dp))
            Text(
                text = "最多 $MaskingPreviewMaxChars 字",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
        }
        Spacer(Modifier.height(8.dp))
        when (val current = preview) {
            MaskingPreviewUiState.Idle -> Text(
                text = "还没有预览。输入一段文本（密钥、邮箱、手机号都行）后点「预览」。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            MaskingPreviewUiState.Running -> Text(
                text = "正在本机预览…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            is MaskingPreviewUiState.Failed -> Text(
                text = if (current.code == "gateway_not_running") {
                    "网关未启动：启动后才能预览。"
                } else {
                    "预览失败：${current.message}（错误码：${current.code}）"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            is MaskingPreviewUiState.Ready -> {
                Text(
                    text = "脱敏后（只在本机计算，没有发出去）",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = current.preview.text.ifBlank { "（没有变化）" },
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    text = maskingHitSummary(current.preview.hits),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                val offNote = maskingPreviewOffNote(current.preview)
                if (offNote.isNotEmpty()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = offNote,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                val blockedNote = maskingPreviewBlockedNote(current.preview)
                if (blockedNote.isNotEmpty()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = blockedNote,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        }
    }
}

/** 最近的脱敏记录：只读，含时间与命中规则数；不展示任何原文。 */
@Composable
internal fun MaskingRecordsSection(viewModel: MaskingToolsViewModel = viewModel()) {
    val records by viewModel.records.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.loadRecords() }

    SettingsSection("最近的脱敏记录") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "只显示命中处数与类型，不显示原文",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = viewModel::loadRecords,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = records !is MaskingRecordsUiState.Loading,
            ) { Text(if (records is MaskingRecordsUiState.Loading) "读取中…" else "刷新") }
        }
        Spacer(Modifier.height(8.dp))
        when (val current = records) {
            MaskingRecordsUiState.Idle, MaskingRecordsUiState.Loading -> Row(
                verticalAlignment = Alignment.CenterVertically,
            ) {
                CircularProgressIndicator(
                    modifier = Modifier.size(16.dp),
                    strokeWidth = 2.dp,
                    color = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "正在读取脱敏记录…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is MaskingRecordsUiState.Failed -> Text(
                text = if (current.code == "gateway_not_running") {
                    "网关未启动：启动后才能读取脱敏记录。"
                } else {
                    "读取失败：${current.message}（错误码：${current.code}）"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            is MaskingRecordsUiState.Ready -> {
                val list = current.records.records
                if (list.isEmpty()) {
                    Text(
                        text = maskingRecordsEmptyDetail(),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    list.forEach { record -> MaskingRecordRow(record) }
                    if (current.records.more) {
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "只取最近 ${list.size} 条；更早的记录在网关重启后已清空。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun MaskingRecordRow(record: MaskingRecord) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = maskingTimestamp(record.atMs),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            val source = maskingRecordSourceLine(record)
            if (source.isNotEmpty()) {
                Text(
                    text = source,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            val kinds = record.kinds
            if (kinds.isNotEmpty()) {
                Text(
                    text = kinds.joinToString("、"),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(
            text = maskingRecordHitLabel(record),
            style = MaterialTheme.typography.labelMedium,
            color = if (record.blocked) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            maxLines = 1,
        )
    }
}

/* ========================================================================== */
/* 设置分组 → 区块（数据驱动分发，纯函数部分可单测）                           */
/* ========================================================================== */

/** 一个设置分区区块：id 与 `settingsGroups()` 的序号对齐。 */
internal data class SettingsBlockSpec(val id: String, val title: String)

/**
 * 分区块表（id 顺序 = 桌面 tab 顺序，不代表整页渲染顺序）。09「同步与备份」不在这里：
 * 它由 `SettingsRoute.Backup` 直接跳 `BackupPage`；10「关于」在这里是因为它仍要在设置页内渲染。
 * 整页渲染顺序见 [settingsBlocksFor] 的 null 分支（保持现有屏幕顺序）。
 */
internal val SETTINGS_PARTITION_BLOCKS: List<SettingsBlockSpec> = listOf(
    SettingsBlockSpec("01", "常规"),
    SettingsBlockSpec("02", "网络与共享"),
    SettingsBlockSpec("03", "工具服务"),
    SettingsBlockSpec("04", "预设"),
    SettingsBlockSpec("05", "定价"),
    SettingsBlockSpec("06", "模型目录"),
    SettingsBlockSpec("07", "隐私"),
    SettingsBlockSpec("08", "可观测"),
    // 「10 关于」不在这张分区表里也可达（`MorePage` 走 SettingsRoute.About 聚焦到 "10"），
    // 但又必须能渲染，所以一并列出；它没有设置读写，只有只读信息。
    SettingsBlockSpec("10", "关于"),
)

/** 整页渲染顺序（= 改造前 261-267 行的顺序，保持现状不变；「10 关于」追加在最后）。 */
private val SETTINGS_FULL_PAGE_ORDER = listOf("01", "05", "07", "03", "04", "06", "08", "10")

/**
 * 块表过滤：null → 整页 7 个区块（原顺序）；非空 → 只保留该分组。
 * 未知 id 返回空表（调用方渲染"找不到分组"，不崩）。
 */
internal fun settingsBlocksFor(initialGroup: String?): List<SettingsBlockSpec> {
    if (initialGroup == null) {
        return SETTINGS_FULL_PAGE_ORDER.mapNotNull { id ->
            SETTINGS_PARTITION_BLOCKS.firstOrNull { it.id == id }
        }
    }
    return SETTINGS_PARTITION_BLOCKS.filter { it.id == initialGroup }
}

/** 组标题：用 `settingsGroups()` 的标题，未知 id 返回 null。 */
internal fun settingsGroupTitle(initialGroup: String?): String? {
    if (initialGroup.isNullOrBlank()) {
        return null
    }
    return settingsGroups().firstOrNull { it.index == initialGroup }?.title
}

/** 聚焦模式的顶部：‹ 返回 + 组标题。 */
@Composable
private fun FocusedSettingsHeader(title: String, onBack: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        AetherHeaderIconButton(
            iconRes = R.drawable.ic_back,
            contentDescription = "返回",
            onClick = onBack,
            tint = MaterialTheme.colorScheme.onBackground,
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            DeepPageTitle(title = title)
        }
    }
}

/**
 * 只读的配置档案卡。`GET /api/profiles` 的真实回包是
 * `{"profiles":[...],"active":...}`；激活要写桌面客户端配置文件，手机端不做。
 */
@Composable
private fun SettingsProfilesSection(status: GatewayStatus) {
    val client = remember { SettingsApiClient() }
    var profiles by remember(status.addr) { mutableStateOf<List<ProfileRow>?>(null) }
    var error by remember(status.addr) { mutableStateOf<String?>(null) }
    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            profiles = emptyList()
            error = null
            return@LaunchedEffect
        }
        error = null
        runCatching {
            withContext(Dispatchers.IO) { client.fetchProfiles(status) }
        }.onSuccess { profiles = it }
            .onFailure { error = it.message ?: "读取配置档案失败" }
    }
    DeepCard {
        DeepCardTitle("配置档案（只读）")
        Spacer(Modifier.height(6.dp))
        Text(
            text = "激活配置要写桌面客户端配置文件，手机端不做；这里只展示每套档案的名字与客户端。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        when {
            error != null -> Text(
                text = error ?: "",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            profiles == null -> Text(
                text = "正在读取配置档案…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            profiles?.isEmpty() == true -> Text(
                text = "网关没有配置档案（桌面端可新建）。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> profiles.orEmpty().forEach { profile ->
                Column(
                    Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text(
                        text = profile.name,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        softWrap = false,
                        overflow = TextOverflow.Ellipsis,
                    )
                    if (profile.clients.isEmpty()) {
                        Text(
                            text = "没有客户端",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else {
                        profile.clients.forEach { clientRow ->
                            Text(
                                text = profileClientLabel(clientRow),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                softWrap = false,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }
            }
        }
    }
}

/* ---------------- 远程额度源（POST /api/settings/remote-quotas*） ---------------- */

@Composable
private fun SettingsRemoteQuotasSection(status: GatewayStatus) {
    val client = remember { SettingsApiClient() }
    var sources by remember(status.addr) { mutableStateOf<List<RemoteQuotaSourceRow>?>(null) }
    var error by remember(status.addr) { mutableStateOf<String?>(null) }
    var editingIndex by remember { mutableStateOf<Int?>(null) }
    var creating by remember { mutableStateOf(false) }
    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            sources = emptyList()
            error = null
            return@LaunchedEffect
        }
        error = null
        runCatching { withContext(Dispatchers.IO) { client.fetchSettings(status).remoteQuotas } }
            .onSuccess { sources = it }
            .onFailure { error = it.message ?: "读取远程额度源失败" }
    }
    DeepCard {
        DeepCardTitle("远程额度源")
        Spacer(Modifier.height(6.dp))
        Text(
            text = "读取其它 Aether 实例的用量；保存时整表替换。访问 Key 由网关加密保存，" +
                "GET 不回显密文。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        when {
            error != null -> Text(
                text = error ?: "",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            sources == null -> Text(
                text = "正在读取远程额度源…",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            sources?.isEmpty() == true -> Text(
                text = "没有远程额度源。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> sources.orEmpty().forEachIndexed { index, source ->
                Row(
                    modifier = Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            text = source.name.ifBlank { "远程实例" },
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurface,
                            maxLines = 1,
                            softWrap = false,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            text = source.url + " · " + if (source.enabled) "启用" else "停用" +
                                " · " + if (source.hasKey) "已保存 Key" else "无 Key",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            softWrap = false,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    McaButton(
                        onClick = { editingIndex = index },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("编辑") }
                }
            }
        }
        Spacer(Modifier.height(8.dp))
        McaButton(
            onClick = { creating = true },
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = status.running && sources != null,
        ) { Text("新增源") }
    }
    val list = sources.orEmpty()
    if (creating || editingIndex != null) {
        val index = editingIndex
        val original = index?.let { list.getOrNull(it) }
        RemoteQuotaEditDialog(
            status = status,
            source = original,
            allSources = list,
            index = index ?: list.size,
            isNew = original == null,
            onDismiss = {
                creating = false
                editingIndex = null
            },
            onSaved = {
                sources = it
                creating = false
                editingIndex = null
                error = null
            },
        )
    }
}

@Composable
private fun RemoteQuotaEditDialog(
    status: GatewayStatus,
    source: RemoteQuotaSourceRow?,
    allSources: List<RemoteQuotaSourceRow>,
    index: Int,
    isNew: Boolean,
    onDismiss: () -> Unit,
    onSaved: (List<RemoteQuotaSourceRow>) -> Unit,
) {
    val client = remember { SettingsApiClient() }
    val scope = rememberCoroutineScope()
    var name by remember(source?.url) { mutableStateOf(source?.name.orEmpty()) }
    var url by remember(source?.url) { mutableStateOf(source?.url.orEmpty()) }
    var key by remember(source?.url) { mutableStateOf("") }
    var enabled by remember(source?.url) { mutableStateOf(source?.enabled ?: true) }
    var saving by remember { mutableStateOf(false) }
    var testing by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf<String?>(null) }
    var confirmProvider by remember { mutableStateOf(false) }
    val current = RemoteQuotaDraft(name = name, url = url, key = key, enabled = enabled)
    fun draftsWithCurrent(): List<RemoteQuotaDraft> {
        val rows = allSources.map { RemoteQuotaDraft(it.name, it.url, "", it.enabled) }.toMutableList()
        if (isNew || index >= rows.size) rows.add(current) else rows[index] = current
        return rows
    }
    McaDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        title = {
            Text(
                text = if (isNew) "新增远程额度源" else "编辑远程额度源",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 460.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                RemoteQuotaField("名称", name, { name = it })
                RemoteQuotaField("地址", url, { url = it })
                RemoteQuotaField(
                    label = "访问 Key（留空即不改动）",
                    value = key,
                    onChange = { key = it },
                    password = true,
                )
                if (source?.hasKey == true) {
                    Text(
                        text = "该源已有保存的 Key；留空保存会沿用旧 Key。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (source != null && key.isBlank() &&
                    (name.trim() != source.name || url.trim().trimEnd('/') != source.url)
                ) {
                    Text(
                        text = "名称或地址已改且 Key 留空：服务端按旧 name+url 匹配，旧 Key 不会沿用，" +
                            "这条会变成无 Key。请重新填写 Key。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = enabled, onCheckedChange = { enabled = it })
                    Spacer(Modifier.width(8.dp))
                    Text("启用", style = MaterialTheme.typography.bodySmall)
                }
                if (message != null) {
                    Text(
                        text = message ?: "",
                        style = MaterialTheme.typography.bodySmall,
                        color = if (message?.startsWith("连接成功") == true) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
                McaButton(
                    onClick = {
                        if (!testing) {
                            testing = true
                            scope.launch {
                                message = try {
                                    withContext(Dispatchers.IO) {
                                        client.testRemoteQuota(status, name, url, key)
                                    }.message
                                } catch (e: ManagementApiException) {
                                    e.message
                                } catch (_: Exception) {
                                    "测试连接失败，请重试"
                                } finally {
                                    testing = false
                                }
                            }
                        }
                    },
                    style = McaButtonStyle.Outlined,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !testing && url.isNotBlank(),
                ) { Text(if (testing) "测试中…" else "测试连接") }
                McaButton(
                    onClick = { confirmProvider = true },
                    style = McaButtonStyle.Outlined,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !saving && !isNew && (source?.url ?: "").isNotBlank(),
                ) { Text("注册为 provider") }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    if (!saving && url.isNotBlank()) {
                        saving = true
                        scope.launch {
                            try {
                                val next = withContext(Dispatchers.IO) {
                                    client.saveRemoteQuotas(status, draftsWithCurrent())
                                }
                                onSaved(next)
                            } catch (e: ManagementApiException) {
                                message = e.message
                            } catch (_: Exception) {
                                message = "保存远程额度源失败，请重试"
                            } finally {
                                saving = false
                            }
                        }
                    }
                },
                style = McaButtonStyle.Primary,
                enabled = !saving && url.isNotBlank(),
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined, enabled = !saving) {
                Text("取消")
            }
        },
    )
    if (confirmProvider) {
        McaDialog(
            onDismissRequest = { confirmProvider = false },
            title = { Text("注册为 provider？", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    "会把「${name.ifBlank { "远程实例" }}」注册成本机 openai-compat 上游，" +
                        "使用该实例已保存的访问 Key；不会改动远程实例。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        confirmProvider = false
                        scope.launch {
                            message = try {
                                withContext(Dispatchers.IO) {
                                    client.useRemoteQuotaAsProvider(
                                        status,
                                        source?.name ?: name,
                                        source?.url ?: url,
                                    )
                                }.message
                            } catch (e: ManagementApiException) {
                                e.message
                            } catch (_: Exception) {
                                "注册 provider 失败，请重试"
                            }
                        }
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("确认注册") }
            },
            dismissButton = {
                McaButton(onClick = { confirmProvider = false }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }
}

@Composable
private fun RemoteQuotaField(
    label: String,
    value: String,
    onChange: (String) -> Unit,
    password: Boolean = false,
) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Surface(shape = McaShape.CardAction, color = MaterialTheme.colorScheme.surface, border = McaHairline.stroke()) {
            BasicTextField(
                value = value,
                onValueChange = onChange,
                singleLine = true,
                visualTransformation = if (password) PasswordVisualTransformation() else androidx.compose.ui.text.input.VisualTransformation.None,
                textStyle = MaterialTheme.typography.bodyMedium.copy(color = MaterialTheme.colorScheme.onSurface),
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp),
            )
        }
    }
}
