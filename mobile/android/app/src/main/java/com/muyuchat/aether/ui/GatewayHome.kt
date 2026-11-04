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
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
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
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import kotlinx.coroutines.delay

/**
 * 「这个入口在手机上还没有页面」时的说明内容。
 *
 * 桌面端每一个入口都是可用页面，所以手机端**不再为未接入项开整页占位**：
 * 点到时用一条对话框说清"这里还没有、去哪儿看"，然后回到原页面。
 */
internal data class NotReadyNotice(
    val title: String,
    val detail: String,
)

private enum class BundlePassphrasePurpose { PREVIEW, IMPORT }

/**
 * Mobile route shell matching the approved concept: five primary tabs, Overview
 * as the only wired workspace, and a searchable "更多" directory for the 17
 * Windows workspaces and 9 settings groups.
 *
 * Wired pages read the real controller status. 手机端每个入口要么进真实页面，要么标
 * "仅 Windows" 说明（见 [DesktopOnlyPage]）；实在没有页面的入口只弹一条对话框说明，
 * 不撑成一整页占位。
 */
@Composable
fun GatewayHome(viewModel: OverviewViewModel = viewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    var selectedTab by rememberSaveable { mutableStateOf(MobileTab.OVERVIEW) }
    var notReady by remember { mutableStateOf<NotReadyNotice?>(null) }
    var showRouting by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }
    var showProbe by remember { mutableStateOf(false) }
    var showTotp by remember { mutableStateOf(false) }
    var showSessions by remember { mutableStateOf(false) }
    var showWake by remember { mutableStateOf(false) }
    var showLibrary by remember { mutableStateOf(false) }
    var showPlugins by remember { mutableStateOf(false) }
    var showClients by remember { mutableStateOf(false) }
    var showDesktopOnly by remember { mutableStateOf(false) }
    var showBackup by remember { mutableStateOf(false) }
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

    BackHandler(enabled = notReady != null || showRouting || showSettings || showProbe || showTotp || showSessions || showWake || showLibrary || showBackup || selectedTab != MobileTab.OVERVIEW) {
        if (notReady != null) {
            notReady = null
        } else if (showRouting) {
            showRouting = false
        } else if (showSettings) {
            showSettings = false
        } else if (showProbe) {
            showProbe = false
        } else if (showTotp) {
            showTotp = false
        } else if (showSessions) {
            showSessions = false
        } else if (showWake) {
            showWake = false
        } else if (showLibrary) {
            showLibrary = false
        } else if (showBackup) {
            showBackup = false
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
                    notReady = null
                    showRouting = false
                    showSettings = false
                    showProbe = false
                    showTotp = false
                    showSessions = false
                    showWake = false
                    showLibrary = false
                    showBackup = false
                },
            )
        },
    ) { innerPadding ->
        // safeDrawing 不含 IME inset：键盘弹出时页面底部输入框会被遮住，
        // 这里补一次 imePadding（对话框是独立 window，另有自己的处理）。
        val modifier = Modifier.padding(innerPadding).imePadding()
        when {
            showBackup -> {
                val backupViewModel: BackupViewModel = viewModel()
                val backupState by backupViewModel.state.collectAsStateWithLifecycle()
                BackupPage(
                    status = state.status,
                    state = backupState,
                    onExport = backupViewModel::export,
                    onShare = { ready ->
                        // 分享意图由 ViewModel 组装（只有它知道 FileProvider authority），
                        // 外包一层 runCatching：没有可处理 SEND 的应用时不能崩。
                        runCatching {
                            context.startActivity(
                                Intent.createChooser(
                                    backupViewModel.shareIntent(ready),
                                    "分享备份文件",
                                ),
                            )
                        }
                    },
                    onReset = backupViewModel::reset,
                    onBack = { showBackup = false },
                    onOpenOverview = {
                        showBackup = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
            showDesktopOnly -> {
                DesktopOnlyPage(
                    status = state.status,
                    onBack = { showDesktopOnly = false },
                    modifier = modifier,
                )
            }            showClients -> {
                ClientsPage(
                    status = state.status,
                    onBack = { showClients = false },
                    onOpenOverview = {
                        showClients = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }            showPlugins -> {
                PluginsPage(
                    status = state.status,
                    onBack = { showPlugins = false },
                    onOpenOverview = {
                        showPlugins = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }            showLibrary -> {
                val libraryViewModel: LibraryViewModel = viewModel()
                val libraryState by libraryViewModel.state.collectAsStateWithLifecycle()
                val libraryKind by libraryViewModel.kindFilter.collectAsStateWithLifecycle()
                val libraryDeleting by libraryViewModel.deleting.collectAsStateWithLifecycle()
                val librarySaving by libraryViewModel.saving.collectAsStateWithLifecycle()
                val librarySaveProblems by libraryViewModel.saveProblems.collectAsStateWithLifecycle()
                val libraryNotice by libraryViewModel.notice.collectAsStateWithLifecycle()
                val libraryActionError by libraryViewModel.actionError.collectAsStateWithLifecycle()
                LibraryPage(
                    status = state.status,
                    state = libraryState,
                    kindFilter = libraryKind,
                    deleting = libraryDeleting,
                    notice = libraryNotice,
                    actionError = libraryActionError,
                    onKindFilter = libraryViewModel::setKindFilter,
                    onRefresh = libraryViewModel::refresh,
                    onDelete = libraryViewModel::delete,
                    onDismissNotice = libraryViewModel::dismissNotice,
                    onDismissActionError = libraryViewModel::dismissActionError,
                    saving = librarySaving,
                    saveProblems = librarySaveProblems,
                    onCreate = libraryViewModel::create,
                    onClearSaveProblems = libraryViewModel::clearSaveProblems,
                    onBack = { showLibrary = false },
                    onOpenOverview = {
                        showLibrary = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
            showWake -> {
                val wakeViewModel: WakeViewModel = viewModel()
                val wakeState by wakeViewModel.state.collectAsStateWithLifecycle()
                val wakeTriggering by wakeViewModel.triggering.collectAsStateWithLifecycle()
                val wakeNotice by wakeViewModel.notice.collectAsStateWithLifecycle()
                val wakeActionError by wakeViewModel.actionError.collectAsStateWithLifecycle()
                val wakeScheduler by wakeViewModel.scheduler.collectAsStateWithLifecycle()
                val wakeCheckinRunning by wakeViewModel.checkinRunning.collectAsStateWithLifecycle()
                val wakeCheckinNote by wakeViewModel.checkinNote.collectAsStateWithLifecycle()
                val wakeCheckinSummary by wakeViewModel.checkinSummary.collectAsStateWithLifecycle()
                val wakeAccounts by wakeViewModel.accounts.collectAsStateWithLifecycle()
                val wakeCreating by wakeViewModel.creating.collectAsStateWithLifecycle()
                val wakeCreateProblems by wakeViewModel.createProblems.collectAsStateWithLifecycle()
                WakePage(
                    status = state.status,
                    state = wakeState,
                    triggering = wakeTriggering,
                    notice = wakeNotice,
                    actionError = wakeActionError,
                    onRefresh = wakeViewModel::refresh,
                    onRun = wakeViewModel::runTask,
                    onDismissNotice = wakeViewModel::dismissNotice,
                    onDismissActionError = wakeViewModel::dismissActionError,
                    scheduler = wakeScheduler,
                    checkinRunning = wakeCheckinRunning,
                    checkinNote = wakeCheckinNote,
                    onRefreshScheduler = wakeViewModel::refreshScheduler,
                    onRunCheckin = wakeViewModel::runCheckin,
                    onDismissCheckinNote = wakeViewModel::dismissCheckinNote,
                    checkinSummary = wakeCheckinSummary,
                    accounts = wakeAccounts,
                    creating = wakeCreating,
                    createProblems = wakeCreateProblems,
                    onCreateWake = wakeViewModel::createWake,
                    onClearCreateProblems = wakeViewModel::clearCreateProblems,
                    onDeleteWake = wakeViewModel::deleteWake,
                    onRefreshAccounts = wakeViewModel::refreshAccountsAndSummary,
                    onBack = { showWake = false },
                    onOpenOverview = {
                        showWake = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
            showSessions -> {
                val sessionsViewModel: SessionsViewModel = viewModel()
                val sessionsState by sessionsViewModel.state.collectAsStateWithLifecycle()
                val agentFilter by sessionsViewModel.agentFilter.collectAsStateWithLifecycle()
                val sessionQuery by sessionsViewModel.query.collectAsStateWithLifecycle()
                val sessionsContentSearch by sessionsViewModel.contentSearch.collectAsStateWithLifecycle()
                val sessionsStats by sessionsViewModel.stats.collectAsStateWithLifecycle()
                SessionsPage(
                    status = state.status,
                    state = sessionsState,
                    agentFilter = agentFilter,
                    query = sessionQuery,
                    onAgentChange = sessionsViewModel::setAgentFilter,
                    onQueryChange = sessionsViewModel::setQuery,
                    onRefresh = sessionsViewModel::refresh,
                    stats = sessionsStats,
                    onRefreshStats = { sessionsViewModel.refreshStats() },
                    contentSearch = sessionsContentSearch,
                    onSearchContent = sessionsViewModel::searchContent,
                    onDismissContentSearch = sessionsViewModel::dismissContentSearch,
                    onBack = { showSessions = false },
                    onOpenOverview = {
                        showSessions = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
            showTotp -> {
                val totpViewModel: TotpViewModel = viewModel()
                val totpState by totpViewModel.state.collectAsStateWithLifecycle()
                val totpAdding by totpViewModel.adding.collectAsStateWithLifecycle()
                val totpAddError by totpViewModel.addError.collectAsStateWithLifecycle()
                val totpDeleting by totpViewModel.deleting.collectAsStateWithLifecycle()
                val totpActionError by totpViewModel.actionError.collectAsStateWithLifecycle()
                TotpPage(
                    status = state.status,
                    state = totpState,
                    adding = totpAdding,
                    addError = totpAddError,
                    deleting = totpDeleting,
                    actionError = totpActionError,
                    onRefresh = totpViewModel::refresh,
                    onAdd = totpViewModel::add,
                    onDelete = totpViewModel::delete,
                    onDismissAddError = totpViewModel::dismissAddError,
                    onDismissActionError = totpViewModel::dismissActionError,
                    onBack = { showTotp = false },
                    onOpenOverview = {
                        showTotp = false
                        selectedTab = MobileTab.OVERVIEW
                    },
                    modifier = modifier,
                )
            }
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
                    onOpenBackup = {
                        showSettings = false
                        showBackup = true
                    },
                    onShareReport = { intent ->
                        runCatching { context.startActivity(Intent.createChooser(intent, "分享自检摘要")) }
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
            selectedTab == MobileTab.OVERVIEW -> {
                val overviewStats by viewModel.stats.collectAsStateWithLifecycle()
                val overviewLatest by viewModel.latest.collectAsStateWithLifecycle()
                LaunchedEffect(Unit) { viewModel.refreshStats() }
                OverviewPage(
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
                    stats = overviewStats,
                    latestRequest = overviewLatest,
                    modifier = modifier,
                )
            }
            selectedTab == MobileTab.CHANNELS -> {
                val channelsViewModel: ChannelsViewModel = viewModel()
                val channelsState by channelsViewModel.state.collectAsStateWithLifecycle()
                val bundleState by channelsViewModel.bundlePreview.collectAsStateWithLifecycle()
                val bundleImportState by channelsViewModel.bundleImport.collectAsStateWithLifecycle()
                val qoderLoginState by channelsViewModel.qoderLogin.collectAsStateWithLifecycle()
                LaunchedEffect(Unit) { channelsViewModel.refreshProbes() }
                val channelProbes by channelsViewModel.probes.collectAsStateWithLifecycle()
                val channelProbing by channelsViewModel.probing.collectAsStateWithLifecycle()
                val channelsToggling by channelsViewModel.toggling.collectAsStateWithLifecycle()
                val channelsModels by channelsViewModel.models.collectAsStateWithLifecycle()
                val channelsToggleError by channelsViewModel.toggleError.collectAsStateWithLifecycle()
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
                    probes = channelProbes,
                    probing = channelProbing,
                    onTestProvider = channelsViewModel::testProvider,
                    toggling = channelsToggling,
                    models = channelsModels,
                    onLoadModels = channelsViewModel::loadModels,
                    onDismissModels = channelsViewModel::dismissModels,
                    onToggleProvider = channelsViewModel::toggleProvider,
                    modifier = modifier,
                )
                val purpose = askPassphrase
                // 启停失败：必须让用户看到错误码，而不是点了没反应。
                channelsToggleError?.let { (code, message) ->
                    McaDialog(
                        onDismissRequest = channelsViewModel::dismissToggleError,
                        title = { Text("切换渠道失败", fontWeight = FontWeight.Bold) },
                        text = {
                            Text(
                                text = "$message（错误码：$code）。",
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        },
                        confirmButton = {
                            McaButton(
                                onClick = channelsViewModel::dismissToggleError,
                                style = McaButtonStyle.Outlined,
                            ) { Text("知道了") }
                        },
                    )
                }
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
                val usageDetail by usageViewModel.detail.collectAsStateWithLifecycle()
                LaunchedEffect(Unit) { usageViewModel.refreshDetail() }
                UsagePage(
                    status = state.status,
                    state = usageState,
                    period = usagePeriod,
                    onPeriod = usageViewModel::setPeriod,
                    onRefresh = usageViewModel::refresh,
                    onOpenOverview = { selectedTab = MobileTab.OVERVIEW },
                    detail = usageDetail,
                    onRefreshDetail = usageViewModel::refreshDetail,
                    modifier = modifier,
                )
            }
            selectedTab == MobileTab.REQUESTS -> {
                val requestsViewModel: RequestsViewModel = viewModel()
                val requestsState by requestsViewModel.state.collectAsStateWithLifecycle()
                val requestsQuery by requestsViewModel.query.collectAsStateWithLifecycle()
                val requestsExport by requestsViewModel.export.collectAsStateWithLifecycle()
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
                    exportState = requestsExport,
                    onExportCsv = requestsViewModel::exportCsv,
                    onShareCsv = {
                        (requestsExport as? RequestsExportUiState.Ready)?.let { ready ->
                            runCatching { context.startActivity(requestsViewModel.shareIntent(ready)) }
                        }
                    },
                    onDismissExport = requestsViewModel::dismissExport,
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
                        WorkspaceAction.Totp -> showTotp = true
                        WorkspaceAction.Sessions -> showSessions = true
                        WorkspaceAction.Wake -> showWake = true
                        WorkspaceAction.Library -> showLibrary = true
                        WorkspaceAction.Plugins -> showPlugins = true
                        WorkspaceAction.Clients -> showClients = true
                        WorkspaceAction.DesktopOnly -> showDesktopOnly = true
                        is WorkspaceAction.Pending -> notReady = notReadyNoticeForWorkspace(
                            title = entry.title,
                            subtitle = entry.subtitle,
                        )
                    }
                },
                onSettings = { group ->
                    if (group.available) {
                        // 02/06 的内容就在设置页里，别再弹「后续阶段」——那是设置页有功能、目录不入口。
                        showSettings = true
                    } else {
                        notReady = notReadyNoticeForSettingsGroup(group)
                    }
                },
                modifier = modifier,
            )
            // 所有 MobileTab 都有上面的分支；这里只为穷尽性，正常运行不可达。
            else -> Unit
        }
        notReady?.let { notice ->
            // 不撑整页：桌面端没有占位页这种形态，手机端也不该有。
            McaDialog(
                onDismissRequest = { notReady = null },
                title = {
                    Text(
                        text = notice.title,
                        style = MaterialTheme.typography.titleLarge,
                        fontWeight = FontWeight.Bold,
                    )
                },
                text = {
                    Text(
                        text = notice.detail,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                },
                confirmButton = {
                    McaButton(onClick = { notReady = null }, style = McaButtonStyle.Primary) {
                        Text("知道了")
                    }
                },
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

/** 未接入的工作区入口：说清"这里没有手机页面"，并指到真正能用的地方。 */
internal fun notReadyNoticeForWorkspace(title: String, subtitle: String): NotReadyNotice =
    NotReadyNotice(
        title = "「$title」还没有手机页面",
        detail = "$subtitle。这一项在桌面端可用，手机端尚未移植；" +
            "目录里标「已接入」的入口才是现在能打开的。",
    )

/** 未接入的设置分组：如实说"设置页里没有这一组"，并点出手机端真正能改的是哪几组。 */
internal fun notReadyNoticeForSettingsGroup(group: SettingsGroup): NotReadyNotice {
    val available = settingsGroups().filter { it.available }.joinToString("、") { it.title }
    val scope = if (available.isEmpty()) {
        "手机端设置页目前没有可改的分组"
    } else {
        "手机端设置页目前只有${available}"
    }
    return NotReadyNotice(
        title = "${group.index} ${group.title}",
        detail = "${group.subtitle}。这一组今天不在手机端设置页里：$scope；" +
            "这一组请到桌面端的设置里改。",
    )
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
