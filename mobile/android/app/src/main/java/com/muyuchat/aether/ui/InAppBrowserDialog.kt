package com.muyuchat.aether.ui

/**
 * 移植自 MCA `E:\MAC\feature\chat\src\main\java\com\muyuchat\feature\chat\InternalBrowserDialog.kt`
 * （465 行）。结构与行序、文案、常量值逐段对照原文件。
 *
 * 允许的差异（仅三类）：
 * 1. 包名/import；
 * 2. 本仓库缺失的符号用本地等价实现补齐（`McaIcons.*` → 本仓库矢量；`BrowserTask*`
 *    状态机在本文件内以最小等价结构实现，字段名与分支保持同名）；
 * 3. 品牌文案（原文未出现 MCA 品牌，故无需替换）。
 */

import android.annotation.SuppressLint
import android.graphics.Bitmap
import android.net.http.SslError
import android.os.Build
import android.webkit.SslErrorHandler
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.muyuchat.aether.R
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import java.net.URI
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

/** App 内打开链接的入口：由 `GatewayHome` 提供，页面层只调用它。 */
internal val LocalInAppBrowser = staticCompositionLocalOf<(String) -> Unit> { { } }

private const val DEFAULT_SEARCH_URL_TEMPLATE = "https://www.google.com/search?q={query}"

// ——— 本地等价实现（MCA 的 BrowserTask* 状态机 / 策略对象） ———

internal enum class BrowserTaskPhase {
    QUEUED, SEARCHING, AWAITING_CONFIRMATION, NAVIGATING, COMPLETED, CANCELLED, BLOCKED, FAILED;

    val isTerminal: Boolean get() = this == CANCELLED || this == BLOCKED || this == FAILED
}

internal data class BrowserTaskEvent(
    val phase: BrowserTaskPhase,
    val url: String? = null,
    val message: String? = null,
)

internal data class BrowserTask(
    val initialUrl: String,
    val currentUrl: String? = null,
    val pendingUrl: String? = null,
    val query: String? = null,
    val phase: BrowserTaskPhase = BrowserTaskPhase.QUEUED,
    val approvedOrigins: Set<String> = emptySet(),
    val events: List<BrowserTaskEvent> = emptyList(),
    val message: String? = null,
    val windowVisible: Boolean = true,
)

/** URL 策略：与 MCA `InternalBrowserUrlPolicy` 同名同义。 */
internal object InternalBrowserUrlPolicy {
    fun parseHttpsUrl(rawUrl: String): URI? = runCatching {
        val uri = URI(rawUrl)
        if (!uri.scheme.equals("https", ignoreCase = true)) null else uri
    }.getOrNull()

    fun normalizeInitialUrl(rawUrl: String): String? = parseHttpsUrl(rawUrl)?.toString()

    fun allowedHost(rawUrl: String): String? =
        parseHttpsUrl(rawUrl)?.host?.lowercase()?.removePrefix("www.")

    /** 注册域（取后两段），与 MCA「含子域」语义一致。 */
    fun allowedOrigin(rawUrl: String): String? {
        val host = allowedHost(rawUrl) ?: return null
        val parts = host.split('.').filter { it.isNotBlank() }
        return if (parts.size >= 2) parts.takeLast(2).joinToString(".") else host
    }

    fun allowsNavigation(
        initialUrl: String,
        nextUrl: String,
        additionalApprovedOrigins: Set<String> = emptySet(),
    ): Boolean {
        val initialOrigin = allowedOrigin(initialUrl) ?: return false
        val nextOrigin = allowedOrigin(nextUrl) ?: return false
        return nextOrigin == initialOrigin || additionalApprovedOrigins.contains(nextOrigin)
    }

    fun buildSearchUrl(template: String, query: String): String? {
        val cleanedQuery = query.trim()
        if (cleanedQuery.isBlank() || cleanedQuery.length > 512) return null
        if (template.count { it == '{' } != 1 || template.count { it == '}' } != 1) return null
        if (!template.contains("{query}")) return null
        val base = template.replace("{query}", "query-placeholder")
        val normalized = normalizeInitialUrl(base) ?: return null
        val encoded = URLEncoder.encode(cleanedQuery, StandardCharsets.UTF_8.name())
        return normalized.replace("query-placeholder", encoded)
    }
}

private data class BrowserNavigationKey(val taskId: String, val navigationId: Long, val url: String)
private data class BrowserHistoryCommand(val url: String)

internal fun BrowserTaskPhase.browserLabel(): String = when (this) {
    BrowserTaskPhase.QUEUED -> "等待窗口"
    BrowserTaskPhase.SEARCHING -> "搜索中"
    BrowserTaskPhase.AWAITING_CONFIRMATION -> "待批准"
    BrowserTaskPhase.NAVIGATING -> "访问中"
    BrowserTaskPhase.COMPLETED -> "已加载"
    BrowserTaskPhase.CANCELLED -> "已取消"
    BrowserTaskPhase.BLOCKED -> "已拦截"
    BrowserTaskPhase.FAILED -> "加载失败"
}

/**
 * App 内浏览器入口（替代旧 `InAppBrowserPage` 的调用点）。
 * MCA 原文件同样有两层：无状态便捷入口 + 带 task 的完整实现。
 */
@Composable
internal fun InAppBrowserDialog(
    initialUrl: String,
    onDismiss: () -> Unit,
    searchUrlTemplate: String = DEFAULT_SEARCH_URL_TEMPLATE,
) {
    InAppBrowserContent(
        initialUrl = initialUrl,
        onDismiss = onDismiss,
        searchUrlTemplate = searchUrlTemplate,
    )
}

@SuppressLint("SetJavaScriptEnabled")
@Composable
internal fun InAppBrowserContent(
    initialUrl: String,
    onDismiss: () -> Unit,
    searchUrlTemplate: String = DEFAULT_SEARCH_URL_TEMPLATE,
) {
    var task by remember(initialUrl) {
        mutableStateOf(BrowserTask(initialUrl = initialUrl, currentUrl = initialUrl, phase = BrowserTaskPhase.NAVIGATING))
    }
    var navigationId by remember(initialUrl) { mutableIntStateOf(0) }
    val currentUrl = task.currentUrl ?: task.initialUrl

    fun emit(phase: BrowserTaskPhase, url: String? = null, message: String? = null, query: String? = null) {
        task = task.copy(
            phase = phase,
            currentUrl = url ?: task.currentUrl,
            message = message,
            query = query ?: task.query,
            events = (task.events + BrowserTaskEvent(phase, url ?: task.currentUrl, message)).takeLast(2),
        )
        if (phase == BrowserTaskPhase.NAVIGATING) navigationId += 1
    }

    Dialog(
        onDismissRequest = {
            emit(BrowserTaskPhase.CANCELLED)
            onDismiss()
        },
        properties = DialogProperties(
            usePlatformDefaultWidth = false,
            dismissOnBackPress = false,
            dismissOnClickOutside = false,
        ),
    ) {
        Surface(
            modifier = Modifier.fillMaxSize().padding(horizontal = 8.dp, vertical = 14.dp),
            color = MaterialTheme.colorScheme.background,
            shape = MaterialTheme.shapes.medium,
        ) {
            InternalBrowserContentBody(
                task = task,
                initialUrl = initialUrl,
                searchUrlTemplate = searchUrlTemplate,
                onDismiss = onDismiss,
                onTaskChange = { next -> task = next },
                navigationKey = BrowserNavigationKey("aether:web-preview", navigationId.toLong(), currentUrl),
            )
        }
    }
}

@SuppressLint("SetJavaScriptEnabled")
@Composable
private fun InternalBrowserContentBody(
    task: BrowserTask,
    initialUrl: String,
    searchUrlTemplate: String,
    onDismiss: () -> Unit,
    onTaskChange: (BrowserTask) -> Unit,
    navigationKey: BrowserNavigationKey,
) {
    val latestTask by rememberUpdatedState(task)
    var webView by remember { mutableStateOf<WebView?>(null) }
    var loadedNavigation by remember { mutableStateOf<BrowserNavigationKey?>(null) }
    var historyCommand by remember { mutableStateOf<BrowserHistoryCommand?>(null) }
    var activeHistoryCommand by remember { mutableStateOf<BrowserHistoryCommand?>(null) }
    // 用 $mutableStateListOf 而不是 remember{mutableStateOf<List>}：后者在 task.copy 重组时
    // 容易被整体替换丢失（goBack 曾因此直接关窗）。
    val navigationHistory = remember { androidx.compose.runtime.mutableStateListOf<String>() }
    var pageTitle by remember { mutableStateOf("") }
    var progress by remember { mutableIntStateOf(0) }
    var searchExpanded by rememberSaveable { mutableStateOf(false) }
    var searchQuery by rememberSaveable { mutableStateOf(task.query.orEmpty()) }
    var searchFocused by remember { mutableStateOf(false) }
    var localError by remember { mutableStateOf<String?>(null) }
    var rendererGone by remember { mutableStateOf(false) }
    var rendererEpoch by remember { mutableIntStateOf(0) }
    val disposedViews = remember { java.util.Collections.newSetFromMap(java.util.WeakHashMap<WebView, Boolean>()) }

    fun disposeView(view: WebView) {
        if (!disposedViews.add(view)) return
        runCatching { view.stopLoading() }
        view.webChromeClient = null
        runCatching { view.destroy() }
        if (webView === view) {
            webView = null
            loadedNavigation = null
        }
    }

    fun emit(phase: BrowserTaskPhase, url: String? = null, message: String? = null, query: String? = null) {
        // 关键：进入「待批准」时**不能**改 currentUrl——否则 LaunchedEffect 的 key 变化
        // 会重新进入导航分支，把确认条直接绕过（真机表现为"点了跨站链接就直接跳了"）。
        val awaiting = phase == BrowserTaskPhase.AWAITING_CONFIRMATION
        onTaskChange(
            task.copy(
                phase = phase,
                currentUrl = if (awaiting) task.currentUrl else (url ?: task.currentUrl),
                pendingUrl = if (awaiting) url else task.pendingUrl,
                message = message,
                query = query ?: task.query,
                events = (task.events + BrowserTaskEvent(phase, url ?: task.currentUrl, message)).takeLast(2),
            ),
        )
    }

    fun close() {
        webView?.stopLoading()
        historyCommand = null
        emit(BrowserTaskPhase.CANCELLED)
        onDismiss()
    }

    fun requestNavigation(url: String, userInitiated: Boolean, query: String? = null) {
        if (!latestTask.windowVisible || latestTask.phase == BrowserTaskPhase.CANCELLED) return
        webView?.stopLoading()
        // 跨域 → 先进入「待批准」，由确认条决定；同域直接导航。
        val approved = InternalBrowserUrlPolicy.allowsNavigation(
            initialUrl = initialUrl,
            nextUrl = url,
            additionalApprovedOrigins = latestTask.approvedOrigins,
        )
        if (!approved) {
            emit(BrowserTaskPhase.AWAITING_CONFIRMATION, url = url)
            return
        }
        emit(BrowserTaskPhase.NAVIGATING, url = url, query = query)
    }

    fun goBack() {
        // 先交给 WebView 自己的历史：我们不维护完整前进/后退栈，
        // 自维护的 navigationHistory 只是兜底（且会被 task copy 影响，见下方 remember 列表）。
        val web = webView
        if (web != null && web.canGoBack()) {
            web.goBack()
            return
        }
        if (navigationHistory.size <= 1) {
            close()
            return
        }
        val url = navigationHistory[navigationHistory.lastIndex - 1]
        historyCommand = BrowserHistoryCommand(url)
        requestNavigation(url, userInitiated = true)
    }

    fun submitSearch() {
        val url = InternalBrowserUrlPolicy.buildSearchUrl(searchUrlTemplate, searchQuery)
        if (url == null) {
            localError = "搜索地址或关键词无效"
            return
        }
        localError = null
        searchExpanded = false
        requestNavigation(url, userInitiated = true, query = searchQuery.trim())
    }

    BackHandler {
        // 三分支（顺序不能变）：
        // 1) 搜索框展开 → 只收搜索框/键盘，弹窗**不关**；
        //    这里必须判 searchExpanded 而不是 searchFocused——点 🔍 展开但没聚焦时
        //    searchFocused 仍是 false，会直接落到 goBack() → 无历史 → 关窗（真机 6a 失败的真因）。
        when {
            searchExpanded -> {
                searchExpanded = false
                searchFocused = false
            }
            webView?.canGoBack() == true -> webView?.goBack()
            else -> close()
        }
    }

    LaunchedEffect(rendererGone) {
        if (rendererGone && !task.phase.isTerminal) {
            rendererGone = false
            rendererEpoch += 1
        }
    }
    LaunchedEffect(navigationKey) {
        val view = webView ?: return@LaunchedEffect
        val url = task.currentUrl
        if (!task.windowVisible || task.phase == BrowserTaskPhase.AWAITING_CONFIRMATION || task.phase.isTerminal) {
            view.stopLoading()
            return@LaunchedEffect
        }
        if (url == null || (task.phase != BrowserTaskPhase.NAVIGATING && task.phase != BrowserTaskPhase.SEARCHING)) {
            return@LaunchedEffect
        }
        if (!InternalBrowserUrlPolicy.allowsNavigation(initialUrl, url, task.approvedOrigins)) {
            emit(BrowserTaskPhase.FAILED, message = "导航来源未获批准")
            return@LaunchedEffect
        }
        if (loadedNavigation == navigationKey) return@LaunchedEffect
        view.stopLoading()
        loadedNavigation = navigationKey
        localError = null
        progress = 0
        val command = historyCommand
        historyCommand = null
        activeHistoryCommand = command?.takeIf { it.url == url }
        view.loadUrl(url)
    }
    val loading = task.phase == BrowserTaskPhase.NAVIGATING || task.phase == BrowserTaskPhase.SEARCHING
    val currentUrl = task.currentUrl ?: task.initialUrl
    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_back,
                contentDescription = "返回",
                onClick = { goBack() },
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    pageTitle.ifBlank { InternalBrowserUrlPolicy.allowedHost(currentUrl).orEmpty() },
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    currentUrl,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.labelSmall,
                )
            }
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_search,
                contentDescription = "搜索网页",
                onClick = { searchExpanded = !searchExpanded },
            )
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_refresh,
                contentDescription = "刷新网页",
                onClick = {
                    if (task.phase == BrowserTaskPhase.FAILED || task.phase == BrowserTaskPhase.BLOCKED) {
                        requestNavigation(currentUrl, userInitiated = true)
                    } else {
                        requestNavigation(currentUrl, userInitiated = true)
                    }
                },
            )
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_close,
                contentDescription = "关闭网页",
                onClick = { close() },
            )
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(task.phase.browserLabel(), style = MaterialTheme.typography.labelMedium)
            task.query?.takeIf { it.isNotBlank() }?.let {
                Text(
                    it,
                    modifier = Modifier.weight(1f),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (task.query.isNullOrBlank()) {
                Box(modifier = Modifier.weight(1f))
            }
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_zoom_out,
                contentDescription = "缩小网页",
                onClick = { webView?.zoomOut() },
            )
            AetherHeaderIconButton(
                iconRes = R.drawable.ic_zoom_in,
                contentDescription = "放大网页",
                onClick = { webView?.zoomIn() },
            )
        }
        if (searchExpanded) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TextField(
                    value = searchQuery,
                    onValueChange = { searchQuery = it.take(512) },
                    modifier = Modifier.weight(1f).onFocusChanged { searchFocused = it.isFocused },
                    singleLine = true,
                    label = { Text("搜索关键词") },
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                    keyboardActions = KeyboardActions(onSearch = { submitSearch() }),
                )
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_search,
                    contentDescription = "开始搜索",
                    onClick = { submitSearch() },
                )
            }
        }
        if (task.phase == BrowserTaskPhase.AWAITING_CONFIRMATION) {
            Column(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Text("访问新的来源", style = MaterialTheme.typography.titleSmall)
                Text(
                    task.pendingUrl.orEmpty(),
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodySmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        style = McaButtonStyle.Text,
                        onClick = {
                            val next = task.copy(
                                approvedOrigins = task.approvedOrigins +
                                    listOfNotNull(InternalBrowserUrlPolicy.allowedOrigin(task.pendingUrl.orEmpty())),
                            )
                            onTaskChange(next)
                            val target = task.pendingUrl.orEmpty()
                            if (target.isNotBlank()) {
                                webView?.stopLoading()
                                webView?.loadUrl(target)
                            }
                        },
                    ) {
                        Icon(
                            painter = painterResource(R.drawable.ic_close),
                            contentDescription = null,
                            modifier = Modifier.size(16.dp),
                        )
                        Text("访问", modifier = Modifier.padding(start = 4.dp))
                    }
                    McaButton(
                        style = McaButtonStyle.Text,
                        onClick = { emit(BrowserTaskPhase.CANCELLED, message = "已取消此跳转") },
                    ) {
                        Icon(
                            painter = painterResource(R.drawable.ic_close),
                            contentDescription = null,
                            modifier = Modifier.size(16.dp),
                        )
                        Text("取消跳转", modifier = Modifier.padding(start = 4.dp))
                    }
                }
            }
        }
        task.events.takeLast(2).forEach { event ->
            Text(
                "${event.phase.browserLabel()} · ${event.url.orEmpty()}",
                modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (loading) {
            LinearProgressIndicator(
                progress = { progress.coerceIn(0, 100) / 100f },
                modifier = Modifier.fillMaxWidth().height(2.dp),
            )
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth()) {
            if (!rendererGone) {
                key(rendererEpoch, navigationKey.url) {
                    AndroidView(
                        modifier = Modifier.fillMaxSize(),
                        factory = { ctx ->
                            val ownedNavigation = navigationKey
                            val created = runCatching { WebView(ctx) }
                            if (created.isFailure) {
                                localError = "系统 WebView 不可用"
                                runCatching {
                                    ctx.startActivity(
                                        android.content.Intent(
                                            android.content.Intent.ACTION_VIEW,
                                            android.net.Uri.parse(currentUrl),
                                        ).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK),
                                    )
                                }
                                onDismiss()
                                return@AndroidView android.view.View(ctx)
                            }
                            created.getOrThrow().apply {
                                webView = this
                                loadedNavigation = null
                                settings.apply {
                                    javaScriptEnabled = true
                                    domStorageEnabled = true
                                    builtInZoomControls = true
                                    displayZoomControls = false
                                    allowFileAccess = false
                                    allowContentAccess = false
                                    allowUniversalAccessFromFileURLs = false
                                    allowFileAccessFromFileURLs = false
                                    mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
                                    setSupportMultipleWindows(false)
                                    setGeolocationEnabled(false)
                                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) safeBrowsingEnabled = true
                                    setSupportZoom(true)
                                    loadWithOverviewMode = true
                                    useWideViewPort = true
                                }
                                android.webkit.CookieManager.getInstance().setAcceptThirdPartyCookies(this, false)
                                setDownloadListener { _, _, _, _, _ -> localError = "网页下载已拦截" }
                                webViewClient = object : WebViewClient() {
                                    override fun shouldOverrideUrlLoading(
                                        view: WebView,
                                        request: WebResourceRequest,
                                    ): Boolean {
                                        if (!request.isForMainFrame) return false
                                        val url = request.url.toString()
                                        android.util.Log.d("AetherBrowser", "shouldOverrideUrlLoading: $url")
                                        val current = latestTask
                                        if (url == current.currentUrl && loadedNavigation?.url == url &&
                                            !current.phase.isTerminal
                                        ) {
                                            return false
                                        }
                                        view.stopLoading()
                                        val approved = InternalBrowserUrlPolicy.allowsNavigation(
                                            initialUrl = initialUrl,
                                            nextUrl = url,
                                            additionalApprovedOrigins = current.approvedOrigins,
                                        )
                                        if (!approved) {
                                            emit(BrowserTaskPhase.AWAITING_CONFIRMATION, url = url)
                                        } else {
                                            emit(BrowserTaskPhase.NAVIGATING, url = url)
                                        }
                                        return true
                                    }

                                    override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                                        // 不做"URL 不等于发起导航就早停"：MCA 每次导航都重建
                                        // ownedNavigation，我们（当前实现）没有等价 key，
                                        // 照抄那段会在重定向链上把加载停掉 → 白屏。
                                        progress = 0
                                    }

                                    override fun onPageFinished(view: WebView, url: String) {
                                        val expected = ownedNavigation
                                        if (url != expected.url || view.url != url) return
                                        if (activeHistoryCommand?.url == url && navigationHistory.size > 1) {
                                            navigationHistory.removeAt(navigationHistory.lastIndex)
                                        } else if (navigationHistory.lastOrNull() != url) {
                                            navigationHistory.add(url)
                                            while (navigationHistory.size > 100) navigationHistory.removeAt(0)
                                        }
                                        activeHistoryCommand = null
                                        progress = 100
                                        emit(BrowserTaskPhase.COMPLETED, url = url)
                                    }

                                    override fun onReceivedError(
                                        view: WebView,
                                        request: WebResourceRequest,
                                        error: WebResourceError,
                                    ) {
                                        if (request.isForMainFrame && request.url.toString() == ownedNavigation.url) {
                                            emit(
                                                BrowserTaskPhase.FAILED,
                                                message = "网页加载失败 (${error.errorCode})：${error.description}",
                                            )
                                        }
                                    }

                                    override fun onReceivedHttpError(
                                        view: WebView,
                                        request: WebResourceRequest,
                                        errorResponse: WebResourceResponse,
                                    ) {
                                        if (request.isForMainFrame && request.url.toString() == ownedNavigation.url) {
                                            emit(
                                                BrowserTaskPhase.FAILED,
                                                message = "网页返回 HTTP ${errorResponse.statusCode}",
                                            )
                                        }
                                    }

                                    override fun onReceivedSslError(
                                        view: WebView,
                                        handler: SslErrorHandler,
                                        error: SslError,
                                    ) {
                                        handler.cancel()
                                        emit(
                                            BrowserTaskPhase.FAILED,
                                            message = "HTTPS 证书校验失败 (${error.primaryError})",
                                        )
                                    }

                                    override fun onRenderProcessGone(
                                        view: WebView,
                                        detail: android.webkit.RenderProcessGoneDetail,
                                    ): Boolean {
                                        val wasCurrent = webView === view
                                        disposeView(view)
                                        if (wasCurrent) rendererGone = true
                                        emit(BrowserTaskPhase.FAILED, message = "网页渲染进程已停止")
                                        return true
                                    }
                                }
                                webChromeClient = object : WebChromeClient() {
                                    override fun onProgressChanged(view: WebView, newProgress: Int) {
                                        if (webView === view && view.url == latestTask.currentUrl && !latestTask.phase.isTerminal) {
                                            progress = newProgress
                                        }
                                    }

                                    override fun onReceivedTitle(view: WebView, title: String?) {
                                        android.util.Log.d("AetherBrowser", "onReceivedTitle: $title")
                                        if (webView === view && view.url == latestTask.currentUrl) {
                                            pageTitle = title.orEmpty()
                                        }
                                    }
                                }
                                loadUrl(currentUrl)
                            }
                        },
                        onRelease = { view -> if (view is WebView) disposeView(view) },
                    )
                }
            }
            (localError ?: task.message)?.let { message ->
                Surface(
                    modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(12.dp),
                    color = MaterialTheme.colorScheme.errorContainer,
                    shape = MaterialTheme.shapes.small,
                ) {
                    Text(
                        message,
                        modifier = Modifier.padding(12.dp),
                        color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
            if (loading && progress == 0) {
                CircularProgressIndicator(modifier = Modifier.align(Alignment.Center).size(28.dp))
            }
        }
    }
}
