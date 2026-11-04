package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.CUSTOM_PROVIDER_FORM_PROTOCOLS
import com.muyuchat.aether.runtime.CustomProviderDraft
import com.muyuchat.aether.runtime.CustomProviderProtocol
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.onboardDeviceKind
import com.muyuchat.aether.runtime.onboardMethodBlockedBy
import com.muyuchat.aether.runtime.onboardMethodLabel
import com.muyuchat.aether.runtime.onboardMethods
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.launch

/**
 * 「接入渠道」页：1:1 对标桌面 `internal/web/ui2/js/views/onboard.js`。
 *
 * - 标题/副标题照抄桌面（`onboard.js:40-41`）；
 * - 渠道列表照抄桌面卡片（`onboard.js:181-196`）；搜索过滤对齐桌面 `paintGrid`
 *   的 `display_name / family / id / description`（`onboard.js:172-178`）；
 * - 手机端新增：A–Z 首字母索引（桌面是 grid-3 全量铺，手机单列需要索引）；
 * - **接入方式全部在手机本机完成**：浏览器授权（`/api/auth` 系列端点，回环回调 127.0.0.1）、
 *   设备码（`/api/auth/start {device_code}` 与 `/api/provider/{kind}/device`）、
 *   手动凭据（`POST /api/accounts`）、Token / JSON（同端点）。
 *   账号包只是桌面迁移的次要通道，**不是接入前置**。
 */
@Composable
internal fun OnboardPage(
    status: GatewayStatus,
    providers: List<ProviderSummary>,
    accounts: List<AccountQuotaRow>,
    qoderLoginState: QoderLoginUiState,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onPickBundle: () -> Unit,
    onImportBundle: () -> Unit,
    bundleHost: @Composable () -> Unit,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: OnboardViewModel = viewModel(),
) {
    LaunchedRefresh(status, onRefresh)

    val query by viewModel.query.collectAsStateWithLifecycle()
    val draft by viewModel.draft.collectAsStateWithLifecycle()
    val saving by viewModel.saving.collectAsStateWithLifecycle()
    val formError by viewModel.formError.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    val connect by viewModel.connect.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()

    val filtered = filterOnboardProviders(providers, query)
    val groups = groupOnboardProviders(filtered)
    val rows: List<OnboardListRow> = buildList {
        groups.forEach { group ->
            add(OnboardListRow.Letter(group.letter))
            group.providers.forEach { add(OnboardListRow.Provider(it)) }
        }
    }
    // 字母条点击跳转：前 6 个 item 固定（MobileHeader / 页头 / 搜索 / 自定义入口 / 区块标题 / 字母条）。
    val rowIndexByLetter: Map<String, Int> = rows.withIndex()
        .filter { it.value is OnboardListRow.Letter }
        .associate { (rows[it.index] as OnboardListRow.Letter).letter to (OnboardRowsBase + it.index) }

    LazyColumn(
        state = listState,
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
                Box(Modifier.weight(1f)) {
                    // 页面标题统一走 DeepPageTitle（文案照抄桌面 onboard.js:40-41）
                    DeepPageTitle(
                        title = "接入渠道",
                        subtitle = "多平台登录：本机导入、浏览器授权、设备码、手动凭据",
                    )
                }
                McaIconButton(
                    contentDescription = "刷新渠道",
                    onClick = onRefresh,
                    enabled = status.running,
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
            SearchField(
                query = query,
                onQueryChange = viewModel::setQuery,
                placeholder = "搜索渠道名或描述",
            )
        }
        item {
            OnboardCustomProviderCard(
                notice = notice,
                canAdd = status.running,
                onAdd = viewModel::openForm,
                onDismissNotice = viewModel::dismissNotice,
            )
        }
        item { DeepSectionTitle(text = "按渠道接入", trailing = "${filtered.size} 个渠道") }
        if (providers.isEmpty()) {
            item {
                DeepStateCard(
                    title = "还没有可接入的渠道",
                    detail = "网关启动后会带回可用渠道清单；如果是刚启动，点右上角刷新一次。",
                    actionLabel = "刷新渠道",
                    onAction = onRefresh,
                )
            }
        } else if (groups.isEmpty()) {
            item {
                DeepStateCard(
                    title = "没有匹配的渠道",
                    detail = "换个关键词试试；搜索只匹配渠道名、描述与 id。",
                    actionLabel = "清除搜索",
                    onAction = { viewModel.setQuery("") },
                )
            }
        } else {
            item {
                OnboardLetterBar(
                    letters = groups.map { it.letter },
                    onJump = { letter ->
                        val target = rowIndexByLetter[letter] ?: return@OnboardLetterBar
                        scope.launch { listState.animateScrollToItem(target) }
                    },
                )
            }
            items(rows.size) { index ->
                when (val row = rows[index]) {
                    is OnboardListRow.Letter -> OnboardLetterHeader(row.letter)
                    is OnboardListRow.Provider -> OnboardProviderCard(
                        provider = row.provider,
                        accountCount = accounts.count { it.providerId == row.provider.id },
                        onConnect = { viewModel.connect.open(row.provider) },
                    )
                }
            }
        }

        // ——— 账号包：**不是接入前置**，只做桌面迁移的次要通道 ———
        item { DeepSectionTitle(text = "账号包（次要：桌面迁移用）") }
        item {
            DeepCard {
                DeepCardBody(
                    "接入渠道**不需要**账号包：上面每个渠道卡片里都能直接用浏览器授权、设备码、" +
                        "手动凭据或 Token 接入。这里只保留桌面↔手机的迁移通道。",
                )
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(onClick = onImportBundle, style = McaButtonStyle.Outlined) {
                        Text("导入账号包")
                    }
                    McaButton(onClick = onPickBundle, style = McaButtonStyle.Outlined) {
                        Text("选择账号包")
                    }
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "导入要选文件并输入口令；口令只用于本次解密，不保存、不上传。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        item { AccountExportSection(status = status) }
    }

    connect?.let { current ->
        OnboardConnectDialog(
            state = current,
            qoderLoginState = qoderLoginState,
            onStartQoderLogin = onStartQoderLogin,
            onOpenQoderLogin = onOpenQoderLogin,
            onCancelQoderLogin = onCancelQoderLogin,
            onDismissQoderLogin = onDismissQoderLogin,
            onSelectMethod = viewModel.connect::selectMethod,
            onLabel = viewModel.connect::setLabel,
            onCallbackInput = viewModel.connect::setCallbackInput,
            onTokenInput = viewModel.connect::setTokenInput,
            onManualValue = viewModel.connect::setManualValue,
            onWindsurfEmail = viewModel.connect::setWindsurfEmail,
            onWindsurfPassword = viewModel.connect::setWindsurfPassword,
            onDismissError = viewModel.connect::dismissError,
            onStartAuth = { viewModel.connect.startAuth(onRefresh) },
            onSubmitCallback = { viewModel.connect.submitCallback() },
            onCommitAuth = { viewModel.connect.commitAuth(onRefresh) },
            onCancelAuth = viewModel.connect::cancelAuth,
            onStartDevice = { viewModel.connect.startDevice(onRefresh) },
            onCancelDevice = viewModel.connect::cancelDevice,
            onSubmitTokenJson = { viewModel.connect.submitTokenJson(onRefresh) },
            onSubmitManual = { viewModel.connect.submitManual(onRefresh) },
            onSubmitWindsurf = { viewModel.connect.submitWindsurf(onRefresh) },
            onOpenCustomForm = {
                viewModel.connect.close()
                onDismissQoderLogin()
                viewModel.openForm()
            },
            onOpenUrl = onOpenQoderLogin,
            onDismiss = {
                viewModel.connect.close()
                onDismissQoderLogin()
            },
        )
    }
    draft?.let { current ->
        OnboardCustomProviderDialog(
            draft = current,
            saving = saving,
            error = formError,
            onNameChange = viewModel::updateName,
            onBaseUrlChange = viewModel::updateBaseUrl,
            onApiKeyChange = viewModel::updateApiKey,
            onModelsChange = viewModel::updateModels,
            onProtocolChange = viewModel::updateProtocol,
            onDismiss = viewModel::dismissForm,
            onSave = {
                viewModel.save(
                    existingIds = providers.map { it.id }.toSet(),
                    onSaved = onRefresh,
                )
            },
        )
    }
    // 口令弹窗（预览/导入共用一份逻辑，见 BundleImportHost.kt）
    bundleHost()
}

/** 固定 item 数：MobileHeader / 页头 / 搜索 / 自定义入口 / 区块标题 / 字母条。 */
private const val OnboardRowsBase = 6

/** 列表行：字母分组头 或 渠道卡。 */
internal sealed interface OnboardListRow {
    data class Letter(val letter: String) : OnboardListRow
    data class Provider(val provider: ProviderSummary) : OnboardListRow
}

/** 首屏拉一次（与其它页一致：进入就刷新）。 */
@Composable
private fun LaunchedRefresh(status: GatewayStatus, onRefresh: () -> Unit) {
    androidx.compose.runtime.LaunchedEffect(status.addr, status.running) {
        if (status.running) {
            onRefresh()
        }
    }
}

/** 一个渠道卡片：名称 + 描述 + 已有账号数 + 接入按钮（桌面 `onboard.js:181-196`）。 */
@Composable
private fun OnboardProviderCard(
    provider: ProviderSummary,
    accountCount: Int,
    onConnect: () -> Unit,
) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = provider.name.ifBlank { provider.id },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (provider.description.isNotBlank()) {
                    Text(
                        text = provider.description,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = onboardProviderMeta(provider, accountCount),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = onConnect,
                style = McaButtonStyle.Outlined,
            ) { Text(if (accountCount > 0) "再加一个" else "接入") }
        }
    }
}

/** 分组头：字母本身 + 该组条数；空组不会进列表（`groupOnboardProviders` 已滤掉）。 */
@Composable
private fun OnboardLetterHeader(letter: String) {
    Text(
        text = letter,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 4.dp, bottom = 0.dp),
        maxLines = 1,
    )
}

/**
 * 字母条：横向可滚动的 A–Z（+ #）。
 *
 * 1.3× 字号下的两条硬约束：
 *  - 每个字母 maxLines = 1，绝不换行；字号放大只会让格子变宽，横向滚动吃掉；
 *  - 条是列表里的普通 item（不是浮层），因此不会遮挡下面的卡片。
 */
@Composable
private fun OnboardLetterBar(letters: List<String>, onJump: (String) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        letters.forEach { letter ->
            Surface(
                shape = McaShape.CardAction,
                color = MaterialTheme.colorScheme.surface,
                border = McaHairline.stroke(),
                modifier = Modifier.clickable { onJump(letter) },
            ) {
                Box(
                    modifier = Modifier
                        .widthIn(min = 30.dp)
                        .heightIn(min = 34.dp)
                        .padding(horizontal = 6.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = letter,
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                    )
                }
            }
        }
    }
}

/** 自定义渠道入口卡：桌面对应「手动凭据」，保存走 `POST /api/provider/save`。 */
@Composable
private fun OnboardCustomProviderCard(
    notice: String?,
    canAdd: Boolean,
    onAdd: () -> Unit,
    onDismissNotice: () -> Unit,
) {
    DeepCard {
        DeepCardTitle("自定义渠道")
        Spacer(Modifier.height(4.dp))
        DeepCardBody(
            "桌面对应渠道抽屉里的「手动凭据」。按 LocalProviderSwitch 结构填名称 / 接口类型 / Base URL / API Key / 模型；" +
                "服务端没有 type 字段，接口类型会落到真实的 chat 或 anthropic 请求字段上。",
        )
        if (notice != null) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.height(4.dp))
            McaButton(onClick = onDismissNotice, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
                Text("知道了")
            }
        }
        Spacer(Modifier.height(10.dp))
        McaButton(
            onClick = onAdd,
            style = McaButtonStyle.Primary,
            enabled = canAdd,
        ) { Text("手动凭据 · 添加渠道") }
    }
}

/**
 * 自定义渠道表单：结构照 LocalProviderSwitch（名称 → 接口类型 → Base URL → API Key → 模型 → 保存/取消）。
 *
 * 字段以服务端为准（`providerDraft`，provider_api.go:23-92）：只有 `name` / `key` /
 * `chat` 或 `anthropic` / 可选 `models`；**没有** `type`、`apiKey`、`baseURL`、`note`。
 */
@Composable
private fun OnboardCustomProviderDialog(
    draft: CustomProviderDraft,
    saving: Boolean,
    error: String?,
    onNameChange: (String) -> Unit,
    onBaseUrlChange: (String) -> Unit,
    onApiKeyChange: (String) -> Unit,
    onModelsChange: (String) -> Unit,
    onProtocolChange: (CustomProviderProtocol) -> Unit,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
) {
    var showKey by remember { mutableStateOf(false) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("自定义渠道", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                OnboardField("名称（必填）", draft.name, "例如 自定义渠道") { onNameChange(it) }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "接口类型",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    CUSTOM_PROVIDER_FORM_PROTOCOLS.forEach { protocol ->
                        McaChip(
                            selected = draft.protocol == protocol,
                            onClick = { onProtocolChange(protocol) },
                            label = {
                                Text(protocol.label, style = MaterialTheme.typography.labelSmall, maxLines = 1)
                            },
                        )
                    }
                }
                OnboardField(
                    label = "Base URL（必填）",
                    value = draft.baseUrl,
                    placeholder = "https://api.example.com/v1",
                ) { onBaseUrlChange(it) }
                OnboardField(
                    label = "API Key（必填）",
                    value = draft.apiKey,
                    placeholder = "sk-…",
                    password = !showKey,
                    trailing = {
                        McaButton(
                            onClick = { showKey = !showKey },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                        ) { Text(if (showKey) "隐藏" else "显示") }
                    },
                ) { onApiKeyChange(it) }
                OnboardField(
                    label = "模型（选填，逗号分隔）",
                    value = draft.modelsText,
                    placeholder = "gpt-4o-mini, claude-sonnet-4",
                ) { onModelsChange(it) }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "备注：服务端 providerDraft 没有备注字段，这里不提交（也不伪造成 name 或其它字段）。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "实际提交：name / key / " + draft.protocol.jsonField + "（Base URL）" +
                        (if (draft.modelsText.isBlank()) "" else " / models") +
                        "；不含 type、apiKey、baseURL。账号落到 " + draft.protocol.coreProvider + "。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                error?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = onSave,
                style = McaButtonStyle.Primary,
                enabled = !saving,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

@Composable
private fun OnboardField(
    label: String,
    value: String,
    placeholder: String = "",
    password: Boolean = false,
    trailing: (@Composable () -> Unit)? = null,
    onValueChange: (String) -> Unit,
) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (trailing != null) {
                Spacer(Modifier.width(8.dp))
                trailing()
            }
        }
        Spacer(Modifier.height(6.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
                decorationBox = { inner ->
                    Box {
                        if (value.isEmpty() && placeholder.isNotEmpty() && !password) {
                            Text(
                                text = placeholder,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        inner()
                    }
                },
            )
        }
    }
}

/**
 * 接入向导：**按该渠道真实支持的方式**列按钮，点开后直接在本机完成，不依赖桌面。
 *
 * 方式判定与桌面同源（`onboardMethods` = `onboard.js:207-216`）；手机端做不到的方式
 * 置灰并在下方逐条写明原因（`onboardMethodBlockedBy`），不给假按钮。
 */
@Composable
private fun OnboardConnectDialog(
    state: OnboardConnectState,
    qoderLoginState: QoderLoginUiState,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onSelectMethod: (String) -> Unit,
    onLabel: (String) -> Unit,
    onCallbackInput: (String) -> Unit,
    onTokenInput: (String) -> Unit,
    onManualValue: (String, String) -> Unit,
    onWindsurfEmail: (String) -> Unit,
    onWindsurfPassword: (String) -> Unit,
    onDismissError: () -> Unit,
    onStartAuth: () -> Unit,
    onSubmitCallback: () -> Unit,
    onCommitAuth: () -> Unit,
    onCancelAuth: () -> Unit,
    onStartDevice: () -> Unit,
    onCancelDevice: () -> Unit,
    onSubmitTokenJson: () -> Unit,
    onSubmitManual: () -> Unit,
    onSubmitWindsurf: () -> Unit,
    onOpenCustomForm: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onDismiss: () -> Unit,
) {
    val clipboard = androidx.compose.ui.platform.LocalClipboardManager.current
    val blocked = state.methods.filter { !it.enabled }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "接入 · ${state.providerName}",
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column(
                Modifier
                    .heightIn(max = 460.dp)
                    .verticalScroll(rememberScrollState()),
            ) {
                if (state.providerDescription.isNotBlank()) {
                    Text(
                        text = state.providerDescription,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                }
                OnboardField("账号备注（可选）", state.label, "例如 主力号") { onLabel(it) }
                Spacer(Modifier.height(8.dp))
                Text(
                    text = "接入方式",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    state.methods.forEach { method ->
                        McaButton(
                            onClick = { if (method.enabled) onSelectMethod(method.id) },
                            style = if (state.active == method.id) {
                                McaButtonStyle.Primary
                            } else {
                                McaButtonStyle.Outlined
                            },
                            size = McaButtonSize.CardAction,
                            enabled = method.enabled,
                        ) {
                            Text(method.label, maxLines = 1)
                        }
                    }
                }
                Spacer(Modifier.height(10.dp))
                when {
                    state.active == "oauth_pkce" || state.active == "device_code" -> OnboardAuthMethodBody(
                        state = state,
                        onStart = onStartAuth,
                        onOpenUrl = onOpenUrl,
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                        onCallbackInput = onCallbackInput,
                        onSubmitCallback = onSubmitCallback,
                        onCancel = onCancelAuth,
                        onCommit = onCommitAuth,
                    )
                    state.active == "qoder_device" -> QoderLoginSection(
                        state = qoderLoginState,
                        onStart = onStartQoderLogin,
                        onOpenUrl = onOpenQoderLogin,
                        onCancel = onCancelQoderLogin,
                        onDismiss = onDismissQoderLogin,
                    )
                    onboardDeviceKind(state.active) != null -> OnboardDeviceMethodBody(
                        state = state,
                        onStart = onStartDevice,
                        onOpenUrl = onOpenUrl,
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                        onCancel = onCancelDevice,
                    )
                    state.active == "windsurf_login" -> OnboardWindsurfBody(
                        state = state,
                        onEmail = onWindsurfEmail,
                        onPassword = onWindsurfPassword,
                        onSubmit = onSubmitWindsurf,
                    )
                    state.active == "token_json" -> OnboardTokenJsonBody(
                        state = state,
                        onTokenInput = onTokenInput,
                        onSubmit = onSubmitTokenJson,
                    )
                    state.active == "manual_input" -> OnboardManualBody(
                        state = state,
                        onManualValue = onManualValue,
                        onSubmit = onSubmitManual,
                        onOpenCustomForm = onOpenCustomForm,
                    )
                    else -> Text(
                        text = state.currentMethod?.blockedReason ?: "该方式在手机端未实现。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                state.statusLine.takeIf { it.isNotBlank() }?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
                state.error?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(4.dp))
                    McaButton(
                        onClick = onDismissError,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("知道了") }
                }
                if (blocked.isNotEmpty()) {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = "手机端做不到的方式",
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    blocked.forEach { method ->
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "${method.label}：${method.blockedReason}",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/** 浏览器授权 / 通用设备码：`/api/auth` 三段式（start → 轮询 → commit）。 */
@Composable
private fun OnboardAuthMethodBody(
    state: OnboardConnectState,
    onStart: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onCopy: (String) -> Unit,
    onCallbackInput: (String) -> Unit,
    onSubmitCallback: () -> Unit,
    onCancel: () -> Unit,
    onCommit: () -> Unit,
) {
    val auth = state.auth
    if (auth == null) {
        Text(
            text = "1. 打开授权页　2. 在浏览器完成登录　3. 回到这里自动完成",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        McaButton(onClick = onStart, style = McaButtonStyle.Primary, enabled = !state.busy) {
            Text("开始授权")
        }
        return
    }
    if (auth.isSuccess) {
        Text(
            text = "授权成功。确认备注名后点「完成接入」把它存成账号。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.primary,
        )
        Spacer(Modifier.height(8.dp))
        McaButton(onClick = onCommit, style = McaButtonStyle.Primary, enabled = !state.busy) {
            Text(if (state.busy) "入库中…" else "完成接入")
        }
        return
    }
    if (auth.userCode.isNotBlank()) {
        Text(
            text = "在浏览器里确认这段设备码：",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = auth.userCode,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onCopy(auth.userCode) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制设备码") }
            if (auth.verificationUri.isNotBlank()) {
                McaButton(
                    onClick = { onOpenUrl(auth.verificationUri) },
                    style = McaButtonStyle.Primary,
                    size = McaButtonSize.CardAction,
                ) { Text("打开授权页面") }
            }
        }
    } else if (auth.authorizeUrl.isNotBlank()) {
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onOpenUrl(auth.authorizeUrl) },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
            ) { Text("打开授权页面") }
            McaButton(
                onClick = { onCopy(auth.authorizeUrl) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制链接") }
        }
        Spacer(Modifier.height(10.dp))
        // 浏览器多半会落到 127.0.0.1:<回调端口> 的本地回环页面：能自动完成；
        // 万一浏览器没有把请求交给网关，就允许把地址栏内容贴回来。
        OnboardField(
            "回调地址（自动完成不了时）",
            state.callbackInput,
            "粘贴完整回调地址，或只贴 code",
        ) { onCallbackInput(it) }
        Spacer(Modifier.height(6.dp))
        McaButton(
            onClick = onSubmitCallback,
            style = McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
            enabled = !state.busy,
        ) { Text("我已授权，继续") }
    }
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onCancel, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
        Text("取消授权")
    }
}

/** 渠道各自的登录会话（trae / kiro / zed / codebuddy / workbuddy / minimax）。 */
@Composable
private fun OnboardDeviceMethodBody(
    state: OnboardConnectState,
    onStart: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onCopy: (String) -> Unit,
    onCancel: () -> Unit,
) {
    val device = state.device
    if (device == null) {
        Text(
            text = "打开登录页并完成登录，网关会自动把账号入库。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        McaButton(onClick = onStart, style = McaButtonStyle.Primary, enabled = !state.busy) {
            Text(if (state.busy) "创建中…" else "开始登录")
        }
        return
    }
    if (device.hasUserCode) {
        Text(
            text = "在浏览器里确认这段代码：",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = device.userCode,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onCopy(device.userCode) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制设备码") }
            if (device.verificationUri.isNotBlank()) {
                McaButton(
                    onClick = { onOpenUrl(device.verificationUri) },
                    style = McaButtonStyle.Primary,
                    size = McaButtonSize.CardAction,
                ) { Text("打开授权页面") }
            }
        }
    } else if (device.loginUrl.isNotBlank()) {
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onOpenUrl(device.loginUrl) },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
            ) { Text("打开登录页面") }
            McaButton(
                onClick = { onCopy(device.loginUrl) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制链接") }
        }
    }
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onCancel, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
        Text("取消登录")
    }
}

/** Token / JSON：纯 token 或完整 JSON 都收，逐项落 `POST /api/accounts`。 */
@Composable
private fun OnboardTokenJsonBody(
    state: OnboardConnectState,
    onTokenInput: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Text(
        text = "粘贴 access token / API Key，或完整 JSON（例如 Codex auth.json）；" +
            "支持 {tokens:{...}} 包装，也支持一次贴多份（数组）。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(6.dp))
    OnboardTextArea(
        value = state.tokenInput,
        placeholder = "sk-… 或 {\"access_token\":\"…\"}",
        onValueChange = onTokenInput,
    )
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onSubmit, style = McaButtonStyle.Primary, enabled = !state.busy) {
        Text(if (state.busy) "接入中…" else "导入并接入")
    }
}

/** 手动凭据：按渠道 `auth_form_schema` 生成表单（桌面 `onboard.js:297-354`）。 */
@Composable
private fun OnboardManualBody(
    state: OnboardConnectState,
    onManualValue: (String, String) -> Unit,
    onSubmit: () -> Unit,
    onOpenCustomForm: () -> Unit,
) {
    state.manualSchema.forEach { field ->
        OnboardField(
            label = field.label + if (field.required) "（必填）" else "（选填）",
            value = state.manualValues[field.key].orEmpty(),
            placeholder = field.placeholder,
            password = field.type == "password",
        ) { onManualValue(field.key, it) }
        if (field.help.isNotBlank()) {
            Text(
                text = field.help,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Spacer(Modifier.height(8.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        McaButton(onClick = onSubmit, style = McaButtonStyle.Primary, enabled = !state.busy) {
            Text(if (state.busy) "接入中…" else "接入")
        }
        McaButton(onClick = onOpenCustomForm, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
            Text("自定义渠道（Base URL）")
        }
    }
    Spacer(Modifier.height(6.dp))
    Text(
        text = "Base URL 型网关用上面「自定义渠道」：那里按 LocalProviderSwitch 结构填名称 / 接口类型 / Base URL / Key。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

/**
 * Windsurf 邮箱 + 密码登录（桌面 `onboard.js:561-582`，服务端 `POST /api/provider/windsurf/login`）。
 *
 * 密码是凭据：密码样式默认遮蔽、可临时显示；不进剪贴板、不打印、不落盘，
 * 只随一次请求体提交（见 `OnboardAuthApiClient.loginWindsurf`）。
 */
@Composable
private fun OnboardWindsurfBody(
    state: OnboardConnectState,
    onEmail: (String) -> Unit,
    onPassword: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    var showPassword by remember { mutableStateOf(false) }
    Text(
        text = "使用 Windsurf / Devin 账号邮箱密码登录；密码只用于本次请求，不保存、不写日志、不进剪贴板。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(6.dp))
    OnboardField("邮箱（必填）", state.windsurfEmail, "dev@example.com") { onEmail(it) }
    OnboardField(
        label = "密码（必填）",
        value = state.windsurfPassword,
        placeholder = "••••••",
        password = !showPassword,
        trailing = {
            McaButton(
                onClick = { showPassword = !showPassword },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text(if (showPassword) "隐藏" else "显示") }
        },
    ) { onPassword(it) }
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onSubmit, style = McaButtonStyle.Primary, enabled = !state.busy) {
        Text(if (state.busy) "登录中…" else "登录并接入")
    }
}

/** 多行输入框（Token / JSON 用）；48dp 最小高、字号放大不裁字。 */
@Composable
private fun OnboardTextArea(value: String, placeholder: String, onValueChange: (String) -> Unit) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 96.dp)
                .padding(12.dp),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty()) {
                        Text(
                            text = placeholder,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    inner()
                }
            },
        )
    }
}

/** 桌面 `onboard.js:209`：`qoder-` 前缀的渠道额外支持设备码登录。 */
internal fun hasQoderDeviceLogin(providerId: String): Boolean =
    providerId.trim().lowercase().startsWith("qoder-")

/** 渠道卡片副标题：账号数 + 该渠道在**手机端**真正可用的接入方式。 */
internal fun onboardProviderMeta(provider: ProviderSummary, accountCount: Int): String {
    val usable = onboardMethods(provider).filter { onboardMethodBlockedBy(it, provider) == null }
    val labels = usable.map { onboardMethodLabel(it) } + "Token / JSON"
    return "已有 $accountCount 个账号 · 可用：" + labels.joinToString(" / ")
}

/**
 * 搜索过滤：桌面 `paintGrid` 用 `display_name / family / id / description` 做子串匹配
 * （`onboard.js:172-178`）；手机端没有 family 字段，口径取 name / id / description。
 * 空白搜索返回原列表（保持服务端顺序）。
 */
internal fun filterOnboardProviders(providers: List<ProviderSummary>, query: String): List<ProviderSummary> {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return providers
    }
    return providers.filter { provider ->
        provider.name.contains(needle, ignoreCase = true) ||
            provider.id.contains(needle, ignoreCase = true) ||
            provider.description.contains(needle, ignoreCase = true)
    }
}

/** 首字母：A–Z 归对应字母，其余（中文、数字、符号）统一进「#」。 */
internal fun onboardProviderLetter(provider: ProviderSummary): String {
    val source = provider.name.trim().ifBlank { provider.id.trim() }
    val first = source.firstOrNull() ?: return "#"
    val upper = first.uppercaseChar()
    return if (upper in 'A'..'Z') upper.toString() else "#"
}

internal data class OnboardProviderGroup(
    val letter: String,
    val providers: List<ProviderSummary>,
)

/**
 * 按首字母分组：A–Z 升序、「#」排最后；**空组不出现**。
 * 组内保持服务端顺序（不打乱用户刚看过的渠道）。
 */
internal fun groupOnboardProviders(providers: List<ProviderSummary>): List<OnboardProviderGroup> {
    val byLetter = LinkedHashMap<String, MutableList<ProviderSummary>>()
    providers.forEach { provider ->
        val letter = onboardProviderLetter(provider)
        byLetter.getOrPut(letter) { mutableListOf() }.add(provider)
    }
    val orderedLetters = byLetter.keys.sortedWith(compareBy({ it == "#" }, { it }))
    return orderedLetters.map { letter -> OnboardProviderGroup(letter, byLetter.getValue(letter)) }
}
