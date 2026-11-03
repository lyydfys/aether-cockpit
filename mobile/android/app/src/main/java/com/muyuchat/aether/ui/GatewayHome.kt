package com.muyuchat.aether.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.annotation.DrawableRes
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.stateLabel
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.delay

internal data class PendingTarget(
    val title: String,
    val subtitle: String,
    val stage: String,
) {
    companion object {
        /** 未实现功能统一标的阶段，避免各处写死不同的"后续阶段"文案。 */
        const val NEXT_STAGE: String = "后续阶段"
    }
}

private enum class BundlePassphrasePurpose { PREVIEW, IMPORT }

/**
 * Mobile route shell matching the approved concept: five primary tabs, Overview
 * as the only wired workspace, and a searchable "更多" directory for the 17
 * Windows workspaces and 9 settings groups.
 *
 * Wired pages read the real controller status. Unwired entries open an explicit
 * stage placeholder; desktop-only entries stay disabled and are labelled
 * "仅 Windows" instead of pretending to work on the phone.
 */
@Composable
fun GatewayHome(viewModel: OverviewViewModel = viewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    var selectedTab by rememberSaveable { mutableStateOf(MobileTab.OVERVIEW) }
    var pending by remember { mutableStateOf<PendingTarget?>(null) }
    var showRouting by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }
    var showProbe by remember { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var copied by remember { mutableStateOf(false) }

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (granted) {
            viewModel.onAction(OverviewAction.Start)
        } else {
            viewModel.onAction(OverviewAction.NotificationDenied)
        }
    }

    fun requestPrimaryAction() {
        if (state.status.running) {
            viewModel.onAction(OverviewAction.Stop)
            return
        }
        val needsPermission = Build.VERSION.SDK_INT >= 33 &&
            context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        if (needsPermission) {
            permissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            viewModel.onAction(OverviewAction.Start)
        }
    }

    val url = if (state.status.running && state.status.addr.isNotBlank()) {
        "http://${state.status.addr}/v1"
    } else {
        ""
    }

    LaunchedEffect(copied) {
        if (copied) {
            delay(1500)
            copied = false
        }
    }

    BackHandler(enabled = pending != null || showRouting || showSettings || showProbe || selectedTab != MobileTab.OVERVIEW) {
        if (pending != null) {
            pending = null
        } else if (showRouting) {
            showRouting = false
        } else if (showSettings) {
            showSettings = false
        } else if (showProbe) {
            showProbe = false
        } else {
            selectedTab = MobileTab.OVERVIEW
        }
    }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        contentWindowInsets = WindowInsets.safeDrawing,
        bottomBar = {
            GatewayBottomBar(
                selected = selectedTab,
                onSelect = { tab ->
                    selectedTab = tab
                    pending = null
                    showRouting = false
                    showSettings = false
                    showProbe = false
                },
            )
        },
    ) { innerPadding ->
        val modifier = Modifier.padding(innerPadding)
        val target = pending
        when {
            target != null -> PendingPage(
                target = target,
                status = state.status,
                onBack = { pending = null },
                modifier = modifier,
            )
            showProbe -> {
                val probeViewModel: GatewayProbeViewModel = viewModel()
                val probeState by probeViewModel.state.collectAsStateWithLifecycle()
                GatewayProbePage(
                    status = state.status,
                    state = probeState,
                    onRefresh = probeViewModel::refresh,
                    onBack = { showProbe = false },
                    onOpenOverview = {
                        showProbe = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
            showSettings -> {
                val settingsViewModel: SettingsViewModel = viewModel()
                val settingsState by settingsViewModel.state.collectAsStateWithLifecycle()
                val settingsSave by settingsViewModel.saveState.collectAsStateWithLifecycle()
                val redactDetail by settingsViewModel.redactDetail.collectAsStateWithLifecycle()
                val redactSaving by settingsViewModel.redactSaving.collectAsStateWithLifecycle()
                val redactWriteVersion by settingsViewModel.redactWriteVersion.collectAsStateWithLifecycle()
                val lanSaving by settingsViewModel.lanSaving.collectAsStateWithLifecycle()
                val proxySaving by settingsViewModel.proxySaving.collectAsStateWithLifecycle()
                val proxyError by settingsViewModel.proxyError.collectAsStateWithLifecycle()
                val corsSaving by settingsViewModel.corsSaving.collectAsStateWithLifecycle()
                val corsError by settingsViewModel.corsError.collectAsStateWithLifecycle()
                val ipsSaving by settingsViewModel.ipsSaving.collectAsStateWithLifecycle()
                val ipsError by settingsViewModel.ipsError.collectAsStateWithLifecycle()
                val callerKeys by settingsViewModel.callerKeys.collectAsStateWithLifecycle()
                val callerSecret by settingsViewModel.callerSecret.collectAsStateWithLifecycle()
                val keysError by settingsViewModel.keysError.collectAsStateWithLifecycle()
                val lanError by settingsViewModel.lanError.collectAsStateWithLifecycle()
                val redactError by settingsViewModel.redactError.collectAsStateWithLifecycle()
                val personalSaving by settingsViewModel.personalSaving.collectAsStateWithLifecycle()
                val personalError by settingsViewModel.personalError.collectAsStateWithLifecycle()
                val preferredPort by settingsViewModel.preferredPort.collectAsStateWithLifecycle()
                val preferredPortSaving by settingsViewModel.preferredPortSaving.collectAsStateWithLifecycle()
                val preferredPortError by settingsViewModel.preferredPortError.collectAsStateWithLifecycle()
                val preferredPortNotice by settingsViewModel.preferredPortNotice.collectAsStateWithLifecycle()
                SettingsPage(
                    status = state.status,
                    state = settingsState,
                    onRefresh = settingsViewModel::refresh,
                    onBack = { showSettings = false },
                    onOpenOverview = {
                        showSettings = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    saveState = settingsSave,
                    onToggleRedact = settingsViewModel::toggleRedact,
                    personalSaving = personalSaving,
                    personalError = personalError,
                    onToggleRedactPersonal = settingsViewModel::toggleRedactPersonal,
                    lanSaving = lanSaving,
                    lanError = lanError,
                    onToggleLan = settingsViewModel::toggleLan,
                    proxySaving = proxySaving,
                    proxyError = proxyError,
                    onSaveProxy = settingsViewModel::saveProxy,
                    corsSaving = corsSaving,
                    corsError = corsError,
                    onSaveCors = settingsViewModel::saveCorsOrigins,
                    ipsSaving = ipsSaving,
                    ipsError = ipsError,
                    onSaveAllowIPs = settingsViewModel::saveAllowIPs,
                    callerKeys = callerKeys,
                    callerSecret = callerSecret,
                    keysError = keysError,
                    onLoadCallerKeys = settingsViewModel::loadCallerKeys,
                    onCreateCallerKey = settingsViewModel::createCallerKey,
                    onToggleCallerKey = { key ->
                        settingsViewModel.setCallerKeyOff(key.id, !key.off)
                    },
                    onRemoveCallerKey = settingsViewModel::removeCallerKey,
                    onDismissCallerSecret = settingsViewModel::dismissCallerSecret,
                    redactDetail = redactDetail,
                    redactError = redactError,
                    onOpenRedactDetail = settingsViewModel::openRedactDetail,
                    onCloseRedactDetail = settingsViewModel::closeRedactDetail,
                    onCycleRedactAction = settingsViewModel::cycleRedactAction,
                    redactSaving = redactSaving,
                    redactWriteVersion = redactWriteVersion,
                    onAddRedactRule = settingsViewModel::addRedactRule,
                    onEditRedactRule = settingsViewModel::editRedactRule,
                    onDeleteRedactRule = settingsViewModel::deleteRedactRule,
                    preferredPort = preferredPort,
                    preferredPortSaving = preferredPortSaving,
                    preferredPortError = preferredPortError,
                    preferredPortNotice = preferredPortNotice,
                    onSavePreferredPort = settingsViewModel::savePreferredPort,
                    modifier = modifier,
                )
            }
            showRouting -> {
                val routingViewModel: RoutingViewModel = viewModel()
                val routingState by routingViewModel.state.collectAsStateWithLifecycle()
                val routingSelected by routingViewModel.selected.collectAsStateWithLifecycle()
                val routingPreview by routingViewModel.preview.collectAsStateWithLifecycle()
                val routingSave by routingViewModel.saveState.collectAsStateWithLifecycle()
                val routingCatalog by routingViewModel.catalog.collectAsStateWithLifecycle()
                val routingFiltered by routingViewModel.filteredCatalog.collectAsStateWithLifecycle()
                val routingQuery by routingViewModel.memberQuery.collectAsStateWithLifecycle()
                val routingDraftName by routingViewModel.draftName.collectAsStateWithLifecycle()
                val routingDraftMembers by routingViewModel.draftMembers.collectAsStateWithLifecycle()
                RoutingPage(
                    status = state.status,
                    state = routingState,
                    onRefresh = routingViewModel::refresh,
                    onBack = { showRouting = false },
                    onOpenOverview = {
                        showRouting = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    selected = routingSelected,
                    preview = routingPreview,
                    saveState = routingSave,
                    onSelect = routingViewModel::select,
                    onRetryPreview = routingViewModel::retryPreview,
                    onDismissPreview = routingViewModel::dismissPreview,
                    onSaveStrategy = routingViewModel::saveStrategy,
                    catalog = routingCatalog,
                    filteredCatalog = routingFiltered,
                    memberQuery = routingQuery,
                    draftName = routingDraftName,
                    draftMembers = routingDraftMembers,
                    onMemberQueryChange = routingViewModel::setMemberQuery,
                    onAddMember = routingViewModel::addMember,
                    onRemoveMember = routingViewModel::removeMember,
                    onMoveMember = routingViewModel::moveMember,
                    onDraftNameChange = routingViewModel::setDraftName,
                    onSaveDraft = routingViewModel::saveDraft,
                    modifier = modifier,
                )
            }
            selectedTab == MobileTab.OVERVIEW -> OverviewPage(
                state = state,
                url = url,
                copied = copied,
                onPrimaryAction = { requestPrimaryAction() },
                onCopy = {
                    if (url.isNotEmpty()) {
                        clipboard.setText(AnnotatedString(url))
                        copied = true
                    }
                },
                onSearch = { selectedTab = MobileTab.MORE },
                modifier = modifier,
            )
            selectedTab == MobileTab.CHANNELS -> {
                val channelsViewModel: ChannelsViewModel = viewModel()
                val channelsState by channelsViewModel.state.collectAsStateWithLifecycle()
                val bundleState by channelsViewModel.bundlePreview.collectAsStateWithLifecycle()
                val bundleImportState by channelsViewModel.bundleImport.collectAsStateWithLifecycle()
                val qoderLoginState by channelsViewModel.qoderLogin.collectAsStateWithLifecycle()
                var pendingBundle by remember { mutableStateOf<Uri?>(null) }
                var askPassphrase by remember { mutableStateOf<BundlePassphrasePurpose?>(null) }
                val bundlePicker = rememberLauncherForActivityResult(
                    ActivityResultContracts.OpenDocument(),
                ) { uri ->
                    if (uri != null) {
                        pendingBundle = uri
                        askPassphrase = BundlePassphrasePurpose.PREVIEW
                    }
                }
                ChannelsPage(
                    status = state.status,
                    state = channelsState,
                    bundleState = bundleState,
                    bundleImportState = bundleImportState,
                    qoderLoginState = qoderLoginState,
                    onAction = channelsViewModel::onAction,
                    onOpenOverview = { selectedTab = MobileTab.OVERVIEW },
                    onStartQoderLogin = channelsViewModel::startQoderLogin,
                    onOpenQoderLogin = { url ->
                        runCatching {
                            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                        }.isSuccess
                    },
                    onCancelQoderLogin = channelsViewModel::cancelQoderLogin,
                    onDismissQoderLogin = channelsViewModel::dismissQoderLogin,
                    onPickBundle = { bundlePicker.launch(arrayOf("*/*")) },
                    onImportBundle = { askPassphrase = BundlePassphrasePurpose.IMPORT },
                    onDismissBundle = channelsViewModel::dismissBundlePreview,
                    modifier = modifier,
                )
                val purpose = askPassphrase
                if (purpose != null) {
                    val importing = purpose == BundlePassphrasePurpose.IMPORT
                    BundlePassphraseDialog(
                        onConfirm = { passphrase ->
                            when (purpose) {
                                BundlePassphrasePurpose.PREVIEW ->
                                    pendingBundle?.let { channelsViewModel.previewAccountBundle(it, passphrase) }
                                BundlePassphrasePurpose.IMPORT ->
                                    channelsViewModel.importAccountBundle(passphrase)
                            }
                            askPassphrase = null
                            pendingBundle = null
                        },
                        onDismiss = {
                            askPassphrase = null
                            pendingBundle = null
                        },
                        title = if (importing) "确认导入账号包" else "输入账号包口令",
                        detail = if (importing) {
                            "口令只用于本次导入，不会保存或上传。导入会把账号写入本机账号池。"
                        } else {
                            "口令只用于本次本地解密，不会保存或上传。"
                        },
                        confirmLabel = if (importing) "确认导入" else "解密预览",
                    )
                }
            }
            selectedTab == MobileTab.USAGE -> {
                val usageViewModel: UsageViewModel = viewModel()
                val usageState by usageViewModel.state.collectAsStateWithLifecycle()
                val usagePeriod by usageViewModel.period.collectAsStateWithLifecycle()
                UsagePage(
                    status = state.status,
                    state = usageState,
                    period = usagePeriod,
                    onPeriod = usageViewModel::setPeriod,
                    onRefresh = usageViewModel::refresh,
                    onOpenOverview = { selectedTab = MobileTab.OVERVIEW },
                    modifier = modifier,
                )
            }
            selectedTab == MobileTab.REQUESTS -> {
                val requestsViewModel: RequestsViewModel = viewModel()
                val requestsState by requestsViewModel.state.collectAsStateWithLifecycle()
                val requestsQuery by requestsViewModel.query.collectAsStateWithLifecycle()
                val requestDetail by requestsViewModel.selected.collectAsStateWithLifecycle()
                RequestsPage(
                    status = state.status,
                    state = requestsState,
                    failedOnly = requestsQuery.failedOnly,
                    onFailedOnly = requestsViewModel::setFailedOnly,
                    onRefresh = requestsViewModel::refresh,
                    onPrev = requestsViewModel::prevPage,
                    onNext = requestsViewModel::nextPage,
                    onOpenOverview = { selectedTab = MobileTab.OVERVIEW },
                    selected = requestDetail,
                    onSelect = requestsViewModel::select,
                    onDismissDetail = requestsViewModel::dismissDetail,
                    modifier = modifier,
                )
            }
            selectedTab == MobileTab.MORE -> MorePage(
                status = state.status,
                query = query,
                onQueryChange = { query = it },
                onWorkspace = { entry ->
                    when (val action = entry.action) {
                        is WorkspaceAction.Tab -> selectedTab = action.tab
                        WorkspaceAction.Routing -> showRouting = true
                        WorkspaceAction.Settings -> showSettings = true
                        WorkspaceAction.Probe -> showProbe = true
                        WorkspaceAction.DesktopOnly -> Unit
                        is WorkspaceAction.Pending -> pending = PendingTarget(
                            title = entry.title,
                            subtitle = entry.subtitle,
                            stage = action.stage,
                        )
                    }
                },
                onSettings = { group ->
                    if (group.available) {
                        // 02/06 的内容就在设置页里，别再弹「后续阶段」——那是设置页有功能、目录不入口。
                        showSettings = true
                    } else {
                        pending = PendingTarget(
                            title = "${group.index} ${group.title}",
                            subtitle = group.subtitle,
                            stage = PendingTarget.NEXT_STAGE,
                        )
                    }
                },
                modifier = modifier,
            )
            else -> PendingPage(
                target = PendingTarget(
                    title = selectedTab.label,
                    subtitle = selectedTab.subtitle,
                    stage = selectedTab.stage ?: "后续阶段",
                ),
                status = state.status,
                onBack = { selectedTab = MobileTab.OVERVIEW },
                modifier = modifier,
            )
        }
    }
}

@Composable
internal fun MobileHeader(status: GatewayStatus, onSearch: (() -> Unit)? = null) {
    BrandHeader(status = status, onSearch = onSearch)
}

@Composable
private fun BrandHeader(status: GatewayStatus, onSearch: (() -> Unit)?) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(
            modifier = Modifier.size(36.dp),
            shape = CircleShape,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            Box(contentAlignment = Alignment.Center) {
                Image(
                    painter = painterResource(R.drawable.ic_brand_avatar),
                    contentDescription = null,
                    modifier = Modifier.size(24.dp),
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = "Aether Cockpit",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(active = status.running)
                Spacer(Modifier.width(6.dp))
                Text(
                    text = if (status.running) "本机网关运行中 · 通知常驻" else "本机网关未启动",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (onSearch != null) {
            McaIconButton(contentDescription = "搜索全部功能", onClick = onSearch) {
                Icon(
                    painter = painterResource(R.drawable.ic_search),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(McaIconSize.Optical.Action),
                )
            }
        } else {
            Spacer(Modifier.size(48.dp))
        }
    }
}

@Composable
private fun StatusDot(active: Boolean) {
    Box(
        Modifier
            .size(7.dp)
            .clip(CircleShape)
            .background(
                if (active) {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.5f)
                },
            ),
    )
}

@Composable
internal fun statusAccent(status: GatewayStatus): Color = when (status.state) {
    GatewayState.FAILED -> MaterialTheme.colorScheme.error
    GatewayState.RUNNING, GatewayState.STARTING, GatewayState.STOPPING ->
        MaterialTheme.colorScheme.primary
    GatewayState.STOPPED -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f)
}

@Composable
private fun PendingPage(
    target: PendingTarget,
    status: GatewayStatus,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                McaIconButton(contentDescription = "返回", onClick = onBack) {
                    Icon(
                        painter = painterResource(R.drawable.ic_back),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurface,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
                Spacer(Modifier.width(4.dp))
                Text(
                    text = "返回",
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        item {
            Text(
                text = target.title,
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = target.subtitle,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        item {
            Surface(
                shape = McaShape.Sheet,
                color = MaterialTheme.colorScheme.primaryContainer,
            ) {
                Column(Modifier.padding(18.dp)) {
                    Text(
                        text = target.stage,
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "该工作区尚未接入",
                        style = MaterialTheme.typography.titleLarge,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "将在 ${target.stage} 接入；当前版本不会执行任何操作，也不会伪造成功状态。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f),
                    )
                }
            }
        }
        item {
            Text(
                text = "手机与 PC 的账号、会话和账本不会自动同步；WSL、Windows 客户端扫描、进程控制与隔离实例仅桌面端可用。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun GatewayBottomBar(selected: MobileTab, onSelect: (MobileTab) -> Unit) {
    Column {
        Box(
            Modifier
                .fillMaxWidth()
                .height(1.dp)
                .background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f)),
        )
        NavigationBar(
            containerColor = MaterialTheme.colorScheme.surface,
            tonalElevation = 0.dp,
        ) {
            MobileTab.entries.forEach { tab ->
                NavigationBarItem(
                    selected = tab == selected,
                    onClick = { onSelect(tab) },
                    icon = {
                        Icon(
                            painter = painterResource(tab.iconRes()),
                            contentDescription = null,
                            modifier = Modifier.size(McaIconSize.L),
                        )
                    },
                    label = { Text(tab.label) },
                    colors = bottomBarColors(),
                )
            }
        }
    }
}

@Composable
private fun bottomBarColors() = NavigationBarItemDefaults.colors(
    selectedIconColor = MaterialTheme.colorScheme.primary,
    selectedTextColor = MaterialTheme.colorScheme.primary,
    indicatorColor = Color.Transparent,
    unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
    unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
)

@DrawableRes
internal fun MobileTab.iconRes(): Int = when (this) {
    MobileTab.OVERVIEW -> R.drawable.ic_nav_overview
    MobileTab.CHANNELS -> R.drawable.ic_ws_channels
    MobileTab.USAGE -> R.drawable.ic_ws_usage
    MobileTab.REQUESTS -> R.drawable.ic_ws_logs
    MobileTab.MORE -> R.drawable.ic_more
}
