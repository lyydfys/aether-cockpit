package com.muyuchat.aether.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.service.AetherGatewayService
import com.muyuchat.aether.service.GatewayOverlayController
import com.muyuchat.aether.service.OverlayColors
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHaptics
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.delay
import androidx.compose.ui.layout.ContentScale

internal data class NotReadyNotice(
    val title: String,
    val detail: String,
)

/** 「10 关于」的分区号：更多页的「关于」行与设置直达都必须用它，别按标题匹配。 */
internal const val SETTINGS_ABOUT_GROUP_ID = "10"

internal data class OverlayFlags(
    val routing: Boolean = false,
    val settings: Boolean = false,
    val probe: Boolean = false,
    val totp: Boolean = false,
    val wake: Boolean = false,
    val library: Boolean = false,
    val backup: Boolean = false,
    val agents: Boolean = false,
    val clients: Boolean = false,
    val plugins: Boolean = false,
) {
    val anyOpen: Boolean
        get() = routing || settings || probe || totp || wake ||
            library || backup || agents || clients || plugins
}

private enum class ActiveSubpage {
    NONE,
    BACKUP,
    DESKTOP_ONLY,
    CLIENTS,
    AGENTS,
    PLUGINS,
    ONBOARD,
    LIBRARY,
    WAKE,
    TOTP,
    PROBE,
    SETTINGS,
    ROUTING,
}

@Composable
fun GatewayHome(viewModel: OverviewViewModel = viewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    // 返回手势的底层快照：进下一层之前抓当前屏，只保留最近一帧（见 BackSnapshot）。
    val rootView = LocalView.current
    val backSnapshot = remember { BackSnapshot() }
    // App 内网页（关于页的链接、Release 页等）：不走任何 Intent。
    var inAppUrl by remember { mutableStateOf<String?>(null) }
    var selectedTab by rememberSaveable { mutableStateOf(MobileTab.OVERVIEW) }
    // 预测返回手势正在收尾（已提交、动画未结束）。
    //
    // 这个标志用来**抑制 AnimatedContent 的页面转场**：手势提交时 onBack() 会同时改
    // activeSubpage，若外层再播一次"右滑 + 淡出"，两段右移位移叠在同一份内容上，
    // 真机上表现为"滑出去之后又弹回来一下"（只在转场时长与预测动画错开的设备上明显）。
    // 手势期间位移完全交给预测动画，转场直接 None；非手势返回仍走正常转场。
    var backGestureSettling by remember { mutableStateOf(false) }
    var notReady by remember { mutableStateOf<NotReadyNotice?>(null) }
    var showRouting by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }
    var settingsInitialGroup by remember { mutableStateOf<String?>(null) }
    // 设置页当前是否停在某个分组（二级页）。由 SettingsPage.onLevelChange 上报，
    // 用于两件事：① 返回手势的底层渲染"设置首页"；② handleBack 先弹回设置首页而不是直接退出。
    var settingsInGroup by remember { mutableStateOf(false) }
    var settingsPopSignal by remember { mutableIntStateOf(0) }
    var showProbe by remember { mutableStateOf(false) }
    var showTotp by remember { mutableStateOf(false) }
    var showWake by remember { mutableStateOf(false) }
    var showLibrary by remember { mutableStateOf(false) }
    var showOnboard by remember { mutableStateOf(false) }
    var showPlugins by remember { mutableStateOf(false) }
    var showClients by remember { mutableStateOf(false) }
    var showAgents by remember { mutableStateOf(false) }
    var showDesktopOnly by remember { mutableStateOf(false) }
    var showBackup by remember { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var copied by remember { mutableStateOf(false) }
    // 地址写入剪贴板失败时功能栏显示「复制失败」，1.5s 后复位（不静默吞掉）。
    var copyFailed by remember { mutableStateOf(false) }
    val overlayVisible by GatewayOverlayController.visible.collectAsStateWithLifecycle()
    // 系统窗 → 页面球的位置回传：关外显后页面球落在系统球最后所在的位置，不跳回默认角落。
    val overlayPosition by GatewayOverlayController.position.collectAsStateWithLifecycle()
    // 页面球 → 系统窗的交接位置（用户拖动页面球松手时更新）。
    var inAppBubblePosition by remember { mutableStateOf(BubblePosition.BottomEnd) }
    LaunchedEffect(overlayPosition) {
        overlayPosition?.let { inAppBubblePosition = it }
    }
    val tabStateHolder = rememberSaveableStateHolder()
    // 「上一页」快照层用**独立**的 saveable 命名空间：它和当前层可能在同一帧里渲染
    // 同一个 tab（AnimatedContent 切换时新旧内容并存），共用一个 holder 会直接抛
    // `Key OVERVIEW was used multiple times` 并让 App 崩溃（真机复现：点 Tab 即闪退）。
    val previousTabStateHolder = rememberSaveableStateHolder()

    val overlayColors = OverlayColors(
        primary = MaterialTheme.colorScheme.primary.toArgb(),
        onPrimary = MaterialTheme.colorScheme.onPrimary.toArgb(),
        surface = MaterialTheme.colorScheme.surface.toArgb(),
        onSurface = MaterialTheme.colorScheme.onSurface.toArgb(),
        surfaceVariant = MaterialTheme.colorScheme.surfaceVariant.toArgb(),
        onSurfaceVariant = MaterialTheme.colorScheme.onSurfaceVariant.toArgb(),
        outline = MaterialTheme.colorScheme.outlineVariant.toArgb(),
        outlineVariant = MaterialTheme.colorScheme.outlineVariant.toArgb(),
        error = MaterialTheme.colorScheme.error.toArgb(),
        onError = MaterialTheme.colorScheme.onError.toArgb(),
        background = MaterialTheme.colorScheme.background.toArgb(),
    )

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

    fun toggleOverlay() {
        if (!state.status.running) {
            return
        }
        val action = if (overlayVisible) {
            AetherGatewayService.ACTION_HIDE_OVERLAY
        } else {
            AetherGatewayService.ACTION_SHOW_OVERLAY
        }
        val intent = Intent(context, AetherGatewayService::class.java).apply {
            this.action = action
            if (action == AetherGatewayService.ACTION_SHOW_OVERLAY) {
                putExtra(OverlayColors.EXTRA_PRIMARY, overlayColors.primary)
                putExtra(OverlayColors.EXTRA_ON_PRIMARY, overlayColors.onPrimary)
                putExtra(OverlayColors.EXTRA_SURFACE, overlayColors.surface)
                putExtra(OverlayColors.EXTRA_ON_SURFACE, overlayColors.onSurface)
                putExtra(OverlayColors.EXTRA_SURFACE_VARIANT, overlayColors.surfaceVariant)
                putExtra(OverlayColors.EXTRA_ON_SURFACE_VARIANT, overlayColors.onSurfaceVariant)
                putExtra(OverlayColors.EXTRA_OUTLINE, overlayColors.outline)
                putExtra(OverlayColors.EXTRA_OUTLINE_VARIANT, overlayColors.outlineVariant)
                putExtra(OverlayColors.EXTRA_ERROR, overlayColors.error)
                putExtra(OverlayColors.EXTRA_ON_ERROR, overlayColors.onError)
                putExtra(OverlayColors.EXTRA_BACKGROUND, overlayColors.background)
                // 交接位置：系统窗里的球落在页面球现在的位置，眼睛看不到"换了一颗球"。
                putExtra(GatewayOverlayController.EXTRA_OVERLAY_X, inAppBubblePosition.xFraction)
                putExtra(GatewayOverlayController.EXTRA_OVERLAY_Y, inAppBubblePosition.yFraction)
            }
        }
        runCatching { context.startService(intent) }
    }

    val url = if (state.status.running && state.status.addr.isNotBlank()) {
        "http://${state.status.addr}/v1"
    } else {
        ""
    }

    LaunchedEffect(copied, copyFailed) {
        if (copied || copyFailed) {
            delay(1500)
            copied = false
            copyFailed = false
        }
    }

    val overlays = OverlayFlags(
        routing = showRouting,
        settings = showSettings,
        probe = showProbe,
        totp = showTotp,
        wake = showWake,
        library = showLibrary,
        backup = showBackup,
        agents = showAgents,
        clients = showClients,
        plugins = showPlugins,
    )

    val activeSubpage = when {
        showBackup -> ActiveSubpage.BACKUP
        showDesktopOnly -> ActiveSubpage.DESKTOP_ONLY
        showClients -> ActiveSubpage.CLIENTS
        showAgents -> ActiveSubpage.AGENTS
        showPlugins -> ActiveSubpage.PLUGINS
        showOnboard -> ActiveSubpage.ONBOARD
        showLibrary -> ActiveSubpage.LIBRARY
        showWake -> ActiveSubpage.WAKE
        showTotp -> ActiveSubpage.TOTP
        showProbe -> ActiveSubpage.PROBE
        showSettings -> ActiveSubpage.SETTINGS
        showRouting -> ActiveSubpage.ROUTING
        else -> ActiveSubpage.NONE
    }

    val anySubpageOpen = overlays.anyOpen || showOnboard || showDesktopOnly

    val handleBack: () -> Unit = {
        if (notReady != null) {
            notReady = null
        } else if (showRouting) {
            showRouting = false
        } else if (showSettings) {
            // 与返回手势露出的那一页一致：先回设置首页，再退到 tab 层。
            // - 从外部直达某组（settingsInitialGroup != null）：清掉它 → 设置首页；
            // - 在设置页内部进了分组（settingsInGroup=true，initialGroup 为 null）：
            //   用 popSignal 让 SettingsPage 弹回首页。
            // 直达优先：从更多页点「关于/预设」进来时 initialGroup 非空，
            // 返回应关掉整个设置覆盖层回到来源页（更多），而不是先弹设置首页。
            if (settingsInitialGroup != null) {
                settingsInitialGroup = null
                showSettings = false
            } else if (settingsInGroup) {
                settingsPopSignal++
            } else {
                showSettings = false
                // 已退到 tab 层：快照没用了，立刻释放（只留一帧，且不留到下次导航）。
                backSnapshot.clear()
            }
        } else if (showProbe) {
            showProbe = false
        } else if (showTotp) {
            showTotp = false
        } else if (showWake) {
            showWake = false
        } else if (showLibrary) {
            showLibrary = false
        } else if (showBackup) {
            showBackup = false
        } else if (showAgents) {
            showAgents = false
        } else if (showClients) {
            showClients = false
        } else if (showPlugins) {
            showPlugins = false
        } else if (showOnboard) {
            showOnboard = false
        } else if (showDesktopOnly) {
            showDesktopOnly = false
        } else {
            // 覆盖层都关完了：非概览 tab 一律回概览；停在概览时不会走到这里（enabled=false）。
            RootBackPolicy.targetTab(selectedTab)?.let { selectedTab = it }
        }
    }

    // App 内浏览器：独占整页（不套 Scaffold，避免出现底栏/悬浮球与网页并存）。
    // 用顶层 if + return，而不是在 Scaffold 的 content lambda 里 `return@Scaffold`
    // （Scaffold 的内容 lambda 非内联，标号返回会被编译器拒绝）。
    //
    // 打开浏览器前记住当时聚焦的设置分组：浏览器关闭后要回到**同一页**（关于页），
    // 而不是回落设置首页——覆盖期间 SettingsPage 被卸载，内部 activeGroup 会丢。
    var settingsGroupBeforeBrowser by remember { mutableStateOf<String?>(null) }
    android.util.Log.d("AetherBrowser", "early return check, inAppUrl=$inAppUrl")
    if (inAppUrl != null) {
        InAppBrowserDialog(
            initialUrl = inAppUrl.orEmpty(),
            onDismiss = { inAppUrl = null },
        )
        return
    }
    LaunchedEffect(inAppUrl) {
        if (inAppUrl == null && settingsInitialGroup == null && settingsGroupBeforeBrowser != null) {
            settingsInitialGroup = settingsGroupBeforeBrowser
            settingsGroupBeforeBrowser = null
        }
    }

    CompositionLocalProvider(
        LocalInAppBrowser provides { url: String ->
            android.util.Log.d("AetherBrowser", "open requested: $url")
            settingsGroupBeforeBrowser = settingsInitialGroup
            inAppUrl = url
        },
    ) {
    // 观测：根页是否渲染 + 当前 tab / 覆盖页（只打日志，不改布局）。
    LaunchedEffect(selectedTab, activeSubpage) {
        android.util.Log.d("AetherHome", "root rendered, tab=$selectedTab, subpage=$activeSubpage")
    }
    AetherBackdrop {
        Scaffold(
            containerColor = Color.Transparent,
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
                        showWake = false
                        showLibrary = false
                        showBackup = false
                        showAgents = false
                        showClients = false
                        showPlugins = false
                        showOnboard = false
                        showDesktopOnly = false
                    },
                )
            },
        ) { innerPadding ->
            val modifier = Modifier
                .padding(innerPadding)
                .imePadding()

            GatewayBubble(
                status = state.status,
                overlayVisible = overlayVisible,
                copied = copied,
                onPrimaryAction = { requestPrimaryAction() },
                onCopy = {
                    if (url.isNotEmpty()) {
                        val ok = writeClipboard(context, "Aether 网关地址", url)
                        copied = ok
                        copyFailed = !ok
                    }
                },
                onToggleOverlay = { toggleOverlay() },
                modifier = Modifier.fillMaxSize(),
                contentInsets = innerPadding,
                copyFailed = copyFailed,
                // 外显打开后系统窗接管同一颗球：页面球隐藏，避免两个悬浮球同时出现。
                visible = !overlayVisible,
                externalPosition = if (overlayVisible) null else overlayPosition,
                onPositionChange = { inAppBubblePosition = it },
            ) {
                // 底层 Tab 栈抽成独立 lambda：它既是本页内容，也能作为返回手势的"上一页"。
                // 纯结构搬移——状态读取、viewModel 调用、装饰（recede 缩放/透明度）全部原样。
                // 关键：**任何时刻只组合一份**——
                //   anySubpageOpen = true  → previous 渲染它，content 只渲染覆盖层；
                //   anySubpageOpen = false → previous 为 null，content 渲染它。
                // 这样手势期间底层是真实页面，而不是纯色遮罩。
                val tabLayer: @Composable (asPrevious: Boolean, tab: MobileTab) -> Unit = { asPrevious, tab ->
                    val recedeScale by animateFloatAsState(
                        targetValue = if (anySubpageOpen) 0.965f else 1f,
                        animationSpec = spring(
                            dampingRatio = 0.86f,
                            stiffness = Spring.StiffnessMediumLow,
                        ),
                        label = "tabRecedeScale",
                    )
                    val recedeAlpha by animateFloatAsState(
                        targetValue = if (anySubpageOpen) 0.55f else 1f,
                        animationSpec = tween(durationMillis = 220),
                        label = "tabRecedeAlpha",
                    )

                    Box(modifier = Modifier.fillMaxSize()) {
                        // 底层：5 个主 Tab（通过 SaveableStateProvider 驻留滚动位置，配合 Recede 景深退居）
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .graphicsLayer {
                                    // 作为"返回目标页"渲染时不要叠加 recede：
                                    // 缩放交给 PredictiveBackHost 的 0.96→1.0，压暗交给它的 0.32→0，
                                    // 否则会和 0.965 / 0.55 叠成"双重退化"，露出的一页比真实页面更淡。
                                    scaleX = if (asPrevious) 1f else recedeScale
                                    scaleY = if (asPrevious) 1f else recedeScale
                                    alpha = if (asPrevious) 1f else recedeAlpha
                                },
                        ) {
                            AnimatedContent(
                                targetState = tab,
                                transitionSpec = {
                                    fadeIn(tween(180, easing = FastOutSlowInEasing)) togetherWith
                                        fadeOut(tween(130, easing = FastOutSlowInEasing)) using
                                        SizeTransform(clip = false)
                                },
                                label = "primaryTabTransition",
                            ) { currentTab ->
                                // 快照层与当前层各用各的 holder，避免同一 key 注册两次。
                                val holder = if (asPrevious) previousTabStateHolder else tabStateHolder
                                holder.SaveableStateProvider(currentTab.name) {
                                    when (currentTab) {
                                        MobileTab.OVERVIEW -> {
                                            val overviewStats by viewModel.stats.collectAsStateWithLifecycle()
                                            val overviewLatest by viewModel.latest.collectAsStateWithLifecycle()
                                            // 局域网共享：意图（持久化）与真实绑定结果（status.lanEnabled）分开取，
                                            // 切换会重启网关，所以地址以 status 为准、开关位置以意图为准。
                                            val lanSharing by viewModel.lanSharing.collectAsStateWithLifecycle()
                                            val lanError by viewModel.lanError.collectAsStateWithLifecycle()
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
                                                onGotoSettings = { showSettings = true },
                                                onGotoOnboard = { showOnboard = true },
                                                onGotoProbe = { showProbe = true },
                                                lanSharing = lanSharing,
                                                onToggleLanSharing = viewModel::setLanSharing,
                                                lanError = lanError,
                                                modifier = modifier,
                                            )
                                        }
                                        MobileTab.CHANNELS -> {
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
                                            val bundleImport = rememberBundleImportHost(
                                                onPreview = channelsViewModel::previewAccountBundle,
                                                onImport = channelsViewModel::importAccountBundle,
                                            )
                                            ChannelsPage(
                                                status = state.status,
                                                state = channelsState,
                                                bundleState = bundleState,
                                                bundleImportState = bundleImportState,
                                                qoderLoginState = qoderLoginState,
                                                onAction = channelsViewModel::onAction,
                                                onOpenOverview = { selectedTab = MobileTab.OVERVIEW },
                                                onStartQoderLogin = channelsViewModel::startQoderLogin,
                                                onOpenQoderLogin = { loginUrl ->
                                                    runCatching {
                                                        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(loginUrl)))
                                                    }.isSuccess
                                                },
                                                onCancelQoderLogin = channelsViewModel::cancelQoderLogin,
                                                onDismissQoderLogin = channelsViewModel::dismissQoderLogin,
                                                onPickBundle = bundleImport::onPick,
                                                onImportBundle = bundleImport::onPromptImport,
                                                onDismissBundle = channelsViewModel::dismissBundlePreview,
                                                probes = channelProbes,
                                                probing = channelProbing,
                                                onTestProvider = channelsViewModel::testProvider,
                                                toggling = channelsToggling,
                                                models = channelsModels,
                                                onLoadModels = channelsViewModel::loadModels,
                                                onDismissModels = channelsViewModel::dismissModels,
                                                onToggleProvider = channelsViewModel::toggleProvider,
                                                onOpenOnboard = { showOnboard = true },
                                                // 渠道卡的「预设」弹窗在空列表时给一条去设置新建的捷径。
                                                // 只设 initialGroup 不会打开设置层，必须同时 showSettings = true。
                                                onOpenPresets = {
                                                    selectedTab = MobileTab.MORE
                                                    settingsInitialGroup = "04"
                                                    showSettings = true
                                                },
                                                // WorkBuddy 渠道卡的「出网身份」→ 设置 → 网络与共享（02）。
                                                onOpenWorkBuddyIdentity = {
                                                    selectedTab = MobileTab.MORE
                                                    settingsInitialGroup = "02"
                                                    showSettings = true
                                                },
                                                modifier = modifier,
                                            )
                                            bundleImport.Host()
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
                                        }
                                        MobileTab.USAGE -> {
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
                                        MobileTab.REQUESTS -> {
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
                                        MobileTab.MORE -> MorePage(
                                            status = state.status,
                                            query = query,
                                            onQueryChange = { query = it },
                                            onWorkspace = { entry ->
                                                when (val action = entry.action) {
                                                    is WorkspaceAction.Tab -> selectedTab = action.tab
                                                    WorkspaceAction.Routing -> showRouting = true
                                                    WorkspaceAction.Settings -> {
                                                        settingsInitialGroup = null
                                                        showSettings = true
                                                    }
                                                    WorkspaceAction.Probe -> showProbe = true
                                                    WorkspaceAction.Totp -> showTotp = true
                                                    WorkspaceAction.Wake -> showWake = true
                                                    WorkspaceAction.Library -> showLibrary = true
                                                    WorkspaceAction.Onboard -> showOnboard = true
                                                    WorkspaceAction.Plugins -> showPlugins = true
                                                    WorkspaceAction.Clients -> showClients = true
                                                    WorkspaceAction.Agents -> showAgents = true
                                                    WorkspaceAction.DesktopOnly -> showDesktopOnly = true
                                                    is WorkspaceAction.Pending -> notReady = notReadyNoticeForWorkspace(
                                                        title = entry.title,
                                                        subtitle = entry.subtitle,
                                                    )
                                                }
                                            },
                                            onSettings = { group ->
                                                if (!group.available) {
                                                    notReady = notReadyNoticeForSettingsGroup(group)
                                                } else {
                                                    when (val route = group.route) {
                                                        SettingsRoute.Backup -> showBackup = true
                                                        SettingsRoute.About -> {
                                                            settingsInitialGroup = SETTINGS_ABOUT_GROUP_ID
                                                            showSettings = true
                                                        }
                                                        is SettingsRoute.Partition -> {
                                                            settingsInitialGroup = route.id
                                                            showSettings = true
                                                        }
                                                    }
                                                }
                                            },
                                            // 「设置」行进设置首页（导航列表），不落到「01 常规」
                                            onOpenSettingsHome = {
                                                settingsInitialGroup = null
                                                showSettings = true
                                            },
                                            // 「关于」行直达「10 关于」，不经过设置首页
                                            onOpenAbout = {
                                                settingsInitialGroup = SETTINGS_ABOUT_GROUP_ID
                                                showSettings = true
                                            },
                                            modifier = modifier,
                                        )
                                    }
                                }
                            }
                        }
                    }
                }

                // 预测性返回只包裹页面内容层（不再连带把底栏和悬浮球一起拖走）。
                // previous = 真实上一页：设置内进组时用抓到的「设置首页」帧，
                // 其它覆盖页用真实 Tab 栈；两者都没有才退回纯色遮罩。
                val snapshotForGesture = if (showSettings && settingsInGroup) backSnapshot.image else null
                PredictiveBackHost(
                    // 概览是唯一根部：概览时交系统退出；其它 tab 一律回概览。
                    enabled = RootBackPolicy.shouldHandleBack(
                        anySubpageOpen = anySubpageOpen,
                        selectedTab = selectedTab,
                    ),
                    onBack = handleBack,
                    // 覆盖页真的关掉之后才归零（不是动画一结束就归零）。
                    open = anySubpageOpen,
                    // 手势收尾期间让 AnimatedContent 让位：位移已由预测动画承担，
                    // 再叠一次转场会出现"滑出去又弹回来"。
                    onSettlingChanged = { backGestureSettling = it },
                    previous = when {
                        snapshotForGesture != null -> ({
                            Image(
                                bitmap = snapshotForGesture,
                                contentDescription = null,
                                contentScale = ContentScale.Crop,
                                modifier = Modifier.fillMaxSize(),
                            )
                        })
                        anySubpageOpen -> ({ tabLayer(true, selectedTab) })
                        selectedTab != MobileTab.OVERVIEW -> ({
                            tabLayer(true, MobileTab.OVERVIEW)
                        })
                        else -> null
                    },
                ) { _ ->
                    Box(modifier = Modifier.fillMaxSize()) {
                        // 根页（没有覆盖页）时由 content 渲染 Tab 栈，保证有内容可看。
                        if (!anySubpageOpen) {
                            tabLayer(false, selectedTab)
                        }

                        // 顶层：二级深度页面平滑右进右出转场
                        AnimatedContent(
                            targetState = activeSubpage,
                            transitionSpec = {
                                // 预测返回手势收尾中：转场必须让位。
                                // 手势已经把内容右移出去了，此时若再播一次"滑出 + 淡出"，
                                // 两段位移叠加 → "滑出去又弹回来"。直接 None，让预测动画独占。
                                if (backGestureSettling) {
                                    EnterTransition.None togetherWith ExitTransition.None
                                } else if (targetState != ActiveSubpage.NONE) {
                                    (slideInHorizontally(
                                        animationSpec = tween(260, easing = FastOutSlowInEasing),
                                        initialOffsetX = { fullWidth -> (fullWidth * 0.22f).toInt() },
                                    ) + fadeIn(tween(200))) togetherWith
                                        fadeOut(tween(150))
                                } else {
                                    fadeIn(tween(120)) togetherWith
                                        (slideOutHorizontally(
                                            animationSpec = tween(220, easing = FastOutSlowInEasing),
                                            targetOffsetX = { fullWidth -> (fullWidth * 0.22f).toInt() },
                                        ) + fadeOut(tween(180)))
                                } using SizeTransform(clip = false)
                            },
                            label = "subpageTransition",
                        ) { subpage ->
                            if (subpage != ActiveSubpage.NONE) {
                                Box(modifier = Modifier.fillMaxSize()) {
                                    when (subpage) {
                                        ActiveSubpage.BACKUP -> {
                                            val backupViewModel: BackupViewModel = viewModel()
                                            val backupState by backupViewModel.state.collectAsStateWithLifecycle()
                                            BackupPage(
                                                status = state.status,
                                                state = backupState,
                                                onExport = backupViewModel::export,
                                                onShare = { ready ->
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
                                        ActiveSubpage.DESKTOP_ONLY -> {
                                            DesktopOnlyPage(
                                                status = state.status,
                                                onBack = { showDesktopOnly = false },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.CLIENTS -> {
                                            ClientsPage(
                                                status = state.status,
                                                onBack = { showClients = false },
                                                onOpenOverview = {
                                                    showClients = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.AGENTS -> {
                                            AgentsPage(
                                                status = state.status,
                                                onBack = { showAgents = false },
                                                onOpenOverview = {
                                                    showAgents = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.PLUGINS -> {
                                            PluginsPage(
                                                status = state.status,
                                                onBack = { showPlugins = false },
                                                onOpenOverview = {
                                                    showPlugins = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.ONBOARD -> {
                                            val onboardViewModel: ChannelsViewModel = viewModel()
                                            val onboardState by onboardViewModel.state.collectAsStateWithLifecycle()
                                            val onboardQoder by onboardViewModel.qoderLogin.collectAsStateWithLifecycle()
                                            val onboardBundle = rememberBundleImportHost(
                                                onPreview = onboardViewModel::previewAccountBundle,
                                                onImport = onboardViewModel::importAccountBundle,
                                            )
                                            OnboardPage(
                                                status = state.status,
                                                providers = (onboardState as? ChannelsUiState.Ready)?.providers.orEmpty(),
                                                accounts = (onboardState as? ChannelsUiState.Ready)?.accounts.orEmpty(),
                                                qoderLoginState = onboardQoder,
                                                onStartQoderLogin = onboardViewModel::startQoderLogin,
                                                onOpenQoderLogin = { loginUrl ->
                                                    runCatching {
                                                        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(loginUrl)))
                                                    }.isSuccess
                                                },
                                                onCancelQoderLogin = onboardViewModel::cancelQoderLogin,
                                                onDismissQoderLogin = onboardViewModel::dismissQoderLogin,
                                                onPickBundle = onboardBundle::onPick,
                                                onImportBundle = onboardBundle::onPromptImport,
                                                bundleHost = { onboardBundle.Host() },
                                                onRefresh = { onboardViewModel.onAction(ChannelsAction.Refresh) },
                                                onBack = { showOnboard = false },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.LIBRARY -> {
                                            val libraryViewModel: LibraryViewModel = viewModel()
                                            val libraryState by libraryViewModel.state.collectAsStateWithLifecycle()
                                            val libraryKind by libraryViewModel.kindFilter.collectAsStateWithLifecycle()
                                            val libraryDeleting by libraryViewModel.deleting.collectAsStateWithLifecycle()
                                            val librarySaving by libraryViewModel.saving.collectAsStateWithLifecycle()
                                            val librarySaveProblems by libraryViewModel.saveProblems.collectAsStateWithLifecycle()
                                            val libraryNotice by libraryViewModel.notice.collectAsStateWithLifecycle()
                                            val libraryActionError by libraryViewModel.actionError.collectAsStateWithLifecycle()
                                            val libraryMcpAuth by libraryViewModel.mcpAuth.collectAsStateWithLifecycle()
                                            val libraryMcpTools by libraryViewModel.mcpTools.collectAsStateWithLifecycle()
                                            val librarySkillInstall by libraryViewModel.skillInstall.collectAsStateWithLifecycle()
                                            val librarySkillCheck by libraryViewModel.skillCheck.collectAsStateWithLifecycle()
                                            val librarySkillUpdate by libraryViewModel.skillUpdate.collectAsStateWithLifecycle()
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
                                                mcpAuth = libraryMcpAuth,
                                                onStartMcpSignin = libraryViewModel::startMcpSignin,
                                                onCancelMcpSignin = libraryViewModel::cancelMcpSignin,
                                                onSignOutMcp = libraryViewModel::signOutMcp,
                                                onDismissMcpAuth = libraryViewModel::dismissMcpAuth,
                                                mcpTools = libraryMcpTools,
                                                onLoadMcpTools = libraryViewModel::loadMcpTools,
                                                onToggleMcpTool = libraryViewModel::toggleMcpTool,
                                                onSaveMcpTools = libraryViewModel::saveMcpTools,
                                                onDismissMcpTools = libraryViewModel::dismissMcpTools,
                                                skillInstall = librarySkillInstall,
                                                skillCheck = librarySkillCheck,
                                                skillUpdate = librarySkillUpdate,
                                                onProbeSkills = libraryViewModel::probeSkills,
                                                onInstallSkills = libraryViewModel::installSkills,
                                                onCheckSkills = libraryViewModel::checkSkills,
                                                onUpdateAllSkills = libraryViewModel::updateAllSkills,
                                                onDismissSkillActions = libraryViewModel::dismissSkillActions,
                                                onBack = { showLibrary = false },
                                                onOpenOverview = {
                                                    showLibrary = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.WAKE -> {
                                            val wakeViewModel: WakeViewModel = viewModel()
                                            val wakeState by wakeViewModel.state.collectAsStateWithLifecycle()
                                            val wakeTriggering by wakeViewModel.triggering.collectAsStateWithLifecycle()
                                            val wakeEnabledUpdating by wakeViewModel.enabledUpdating.collectAsStateWithLifecycle()
                                            val wakeProviderRunning by wakeViewModel.providerRunning.collectAsStateWithLifecycle()
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
                                                enabledUpdating = wakeEnabledUpdating,
                                                onSetEnabled = wakeViewModel::setEnabled,
                                                providerRunning = wakeProviderRunning,
                                                onRunSchedulerProvider = wakeViewModel::runSchedulerProvider,
                                                onRefreshAccounts = wakeViewModel::refreshAccountsAndSummary,
                                                onBack = { showWake = false },
                                                onOpenOverview = {
                                                    showWake = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.TOTP -> {
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
                                        ActiveSubpage.PROBE -> {
                                            val probeViewModel: GatewayProbeViewModel = viewModel()
                                            val probeState by probeViewModel.state.collectAsStateWithLifecycle()
                                            val probeAlerts by probeViewModel.alerts.collectAsStateWithLifecycle()
                                            val probeIdentity by probeViewModel.identity.collectAsStateWithLifecycle()
                                            val probeChannelNet by probeViewModel.channelNet.collectAsStateWithLifecycle()
                                            val probeNetDiag by probeViewModel.netDiag.collectAsStateWithLifecycle()
                                            GatewayProbePage(
                                                status = state.status,
                                                state = probeState,
                                                onRefresh = probeViewModel::refresh,
                                                alerts = probeAlerts,
                                                identity = probeIdentity,
                                                channelNet = probeChannelNet,
                                                netDiag = probeNetDiag,
                                                onRunNetDiag = probeViewModel::runNetDiag,
                                                onRefreshIdentity = probeViewModel::refreshIdentity,
                                                onOpenUsage = {
                                                    showProbe = false
                                                    selectedTab = MobileTab.USAGE
                                                },
                                                onBack = { showProbe = false },
                                                onOpenOverview = {
                                                    showProbe = false
                                                    selectedTab = MobileTab.OVERVIEW
                                                },
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.SETTINGS -> {
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
                                            val gatewayRestarting by settingsViewModel.gatewayRestarting.collectAsStateWithLifecycle()
                                            val gatewayRestartNotice by settingsViewModel.gatewayRestartNotice.collectAsStateWithLifecycle()
                                            SettingsPage(
                                                status = state.status,
                                                state = settingsState,
                                                onRefresh = settingsViewModel::refresh,
                                                initialGroup = settingsInitialGroup,
                                                // 上报层级：返回手势据此决定底层渲染"设置首页"还是 tab 层。
                                                // 停在设置首页时立刻抓一帧——它就是"下一个二级页"的上一页。
                                                onLevelChange = { inGroup ->
                                                    settingsInGroup = inGroup
                                                    if (!inGroup) {
                                                        backSnapshot.capture(rootView)
                                                    }
                                                },
                                                popSignal = settingsPopSignal,
                                                onBack = {
                                                    // 用户口径：从更多页直达的组（关于/预设）按返回**直接回更多**，
                                                    // 不先弹设置首页；设置首页/组内返回仍走 handleBack 的两段式。
                                                    settingsInitialGroup = null
                                                    showSettings = false
                                                },
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
                                                gatewayRestarting = gatewayRestarting,
                                                gatewayRestartNotice = gatewayRestartNotice,
                                                onRestartGateway = settingsViewModel::restartGateway,
                                                modifier = modifier,
                                            )
                                        }
                                        ActiveSubpage.ROUTING -> {
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
                                        ActiveSubpage.NONE -> Unit
                                    }
                                }
                            }
                        }
                    }

                    notReady?.let { notice ->
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
        }
    }
    }
}

@Composable
internal fun MobileHeader(
    status: GatewayStatus,
    onSearch: (() -> Unit)? = null,
    showBrand: Boolean = false,
) {
    if (showBrand) {
        BrandHeader(status = status, onSearch = onSearch)
    }
}

@Composable
private fun BrandHeader(
    status: GatewayStatus,
    onSearch: (() -> Unit)?,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(
            modifier = Modifier
                .size(38.dp)
                .aetherSpecularRim(cornerRadius = 19.dp),
            shape = CircleShape,
            color = AetherColors.cardSurface(),
            shadowElevation = if (isAetherDarkTheme()) 0.dp else 1.dp,
        ) {
            Box(contentAlignment = Alignment.Center) {
                Image(
                    painter = painterResource(R.drawable.ic_brand_mark),
                    contentDescription = null,
                    modifier = Modifier.size(24.dp),
                )
            }
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = "Aether Cockpit",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onBackground,
                )
                Spacer(Modifier.width(8.dp))
                AetherStatusDot(
                    color = if (status.running) AetherColors.success() else AetherColors.textTertiary(),
                    pulsing = status.running,
                    dotSize = 7.dp,
                )
            }
        }
        if (onSearch != null) {
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_search,
                contentDescription = "搜索全部功能",
                onClick = onSearch,
            )
        } else {
            Spacer(Modifier.size(40.dp))
        }
    }
}

@Composable
internal fun statusAccent(status: GatewayStatus): Color = when (status.state) {
    GatewayState.FAILED -> AetherColors.danger()
    GatewayState.RUNNING, GatewayState.STARTING, GatewayState.STOPPING ->
        MaterialTheme.colorScheme.primary
    GatewayState.STOPPED -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f)
}

internal fun notReadyNoticeForWorkspace(title: String, subtitle: String): NotReadyNotice =
    NotReadyNotice(
        title = "「$title」还没有手机页面",
        detail = "$subtitle。这一项在桌面端可用，手机端尚未移植；" +
            "目录里标「已接入」的入口才是现在能打开的。",
    )

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

/**
 * 旗舰级底部导航栏：
 * - 顶部微光发丝分界线 + 纯净卡片底托；
 * - 选中态采用柔和同心胶囊底托 + 描边切实心（Outlined -> Filled）双态图标 + 物理弹簧微放大；
 * - 切换时提供细腻的触觉反馈。
 */
@Composable
private fun GatewayBottomBar(selected: MobileTab, onSelect: (MobileTab) -> Unit) {
    val dark = isAetherDarkTheme()
    val haptics = androidx.compose.ui.platform.LocalHapticFeedback.current
    val pillColor = if (dark) {
        MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.42f)
    } else {
        MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.72f)
    }

    Surface(
        color = AetherColors.cardSurface(),
        shadowElevation = if (dark) 0.dp else 6.dp,
    ) {
        Column(modifier = Modifier.navigationBarsPadding()) {
            Box(
                Modifier
                    .fillMaxWidth()
                    .height(0.6.dp)
                    .background(
                        if (dark) {
                            Color.White.copy(alpha = 0.08f)
                        } else {
                            MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.45f)
                        },
                    ),
            )
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(64.dp)
                    .padding(horizontal = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                MobileTab.entries.forEach { tab ->
                    val isSelected = tab == selected
                    val iconScale by animateFloatAsState(
                        targetValue = if (isSelected) 1.06f else 1.0f,
                        animationSpec = spring(
                            dampingRatio = 0.72f,
                            stiffness = Spring.StiffnessMedium,
                        ),
                        label = "navIconScale",
                    )
                    val contentColor = if (isSelected) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    }

                    Column(
                        modifier = Modifier
                            .weight(1f)
                            .height(56.dp)
                            .clip(McaShape.CardAction)
                            .clickable(
                                interactionSource = remember { MutableInteractionSource() },
                                indication = null,
                            ) {
                                haptics.performHapticFeedback(
                                    androidx.compose.ui.hapticfeedback.HapticFeedbackType.TextHandleMove,
                                )
                                onSelect(tab)
                            }
                            .semantics {
                                role = Role.Tab
                                this.selected = isSelected
                                contentDescription = tab.label
                            },
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.Center,
                    ) {
                        Box(
                            modifier = Modifier
                                .width(52.dp)
                                .height(28.dp)
                                .clip(CircleShape)
                                .background(
                                    if (isSelected) pillColor else Color.Transparent,
                                    CircleShape,
                                ),
                            contentAlignment = Alignment.Center,
                        ) {
                            Icon(
                                painter = painterResource(
                                    if (isSelected) tab.iconFilledRes() else tab.iconRes(),
                                ),
                                contentDescription = null,
                                tint = contentColor,
                                modifier = Modifier
                                    .size(21.dp)
                                    .graphicsLayer {
                                        scaleX = iconScale
                                        scaleY = iconScale
                                    },
                            )
                        }
                        Spacer(Modifier.height(3.dp))
                        Text(
                            text = tab.label,
                            style = MaterialTheme.typography.labelSmall,
                            fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Medium,
                            color = contentColor,
                            maxLines = 1,
                        )
                    }
                }
            }
        }
    }
}

@DrawableRes
internal fun MobileTab.iconRes(): Int = when (this) {
    MobileTab.OVERVIEW -> R.drawable.ic_nav_overview
    MobileTab.CHANNELS -> R.drawable.ic_ws_channels
    MobileTab.USAGE -> R.drawable.ic_ws_usage
    MobileTab.REQUESTS -> R.drawable.ic_ws_logs
    MobileTab.MORE -> R.drawable.ic_more
}

@DrawableRes
internal fun MobileTab.iconFilledRes(): Int = when (this) {
    MobileTab.OVERVIEW -> R.drawable.ic_nav_overview_filled
    MobileTab.CHANNELS -> R.drawable.ic_ws_channels_filled
    MobileTab.USAGE -> R.drawable.ic_ws_usage_filled
    MobileTab.REQUESTS -> R.drawable.ic_ws_logs_filled
    MobileTab.MORE -> R.drawable.ic_more_filled
}
