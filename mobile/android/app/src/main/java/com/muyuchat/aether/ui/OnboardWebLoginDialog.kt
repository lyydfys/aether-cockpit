package com.muyuchat.aether.ui

import android.annotation.SuppressLint
import android.webkit.CookieManager
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import java.net.URI

/**
 * 应用内 OAuth 登录窗（用户明确要求：不跳外部浏览器）。
 *
 * 网关 `StartPKCE` 绑 `127.0.0.1`，`redirect_uri = http://127.0.0.1:<port>/oauth/callback`；
 * WebView 里跳到这个回环地址时，**HTTP 请求会直接打到本机网关**，授权就完成了——
 * 不需要对外暴露任何端口，也不需要 App Links / deeplink 配置。
 */

/** 回环主机（授权回调只可能落在这几个上）。**精确匹配**，不做后缀匹配。 */
private val LoopbackHosts = setOf("127.0.0.1", "localhost", "::1")

/** 允许在 WebView 里加载的 scheme：只放行 http/https，其余（intent/file/javascript/data）一律拦。 */
private val AllowedSchemes = setOf("http", "https")

private fun parseHost(raw: String): Pair<String, String>? {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) {
        return null
    }
    return try {
        val uri = URI(trimmed)
        val scheme = uri.scheme?.lowercase() ?: return null
        val host = uri.host?.lowercase() ?: return null
        scheme to host
    } catch (_: Exception) {
        null
    }
}

/** 取 URL 的 host（小写）；解析不出来返回 null。 */
internal fun authUrlHost(raw: String): String? = parseHost(raw)?.second

/**
 * 是否命中 OAuth 回调：回环主机 + 路径 `/oauth/callback`。
 *
 * 例：`http://127.0.0.1:8765/oauth/callback?code=abc` → true；
 * `http://127.0.0.1.evil.com/oauth/callback`（后缀伪装）→ **false**。
 */
internal fun isOauthCallbackUrl(raw: String): Boolean {
    val (scheme, host) = parseHost(raw) ?: return false
    if (scheme !in AllowedSchemes) {
        return false
    }
    if (host !in LoopbackHosts) {
        return false
    }
    val path = try {
        URI(raw.trim()).path.orEmpty()
    } catch (_: Exception) {
        ""
    }
    return path.endsWith("/oauth/callback")
}

/**
 * WebView 导航白名单：只放行「该渠道的授权域名」与「本机回环」。
 *
 * - 回环：**精确匹配** `127.0.0.1` / `localhost` / `::1`（所以 `http://127.0.0.1.evil.com`
 *   和 `evil-127.0.0.1.com` 都会被拦）；
 * - 渠道授权域名：[providerHosts] 精确匹配，或它的子域（`accounts.google.com` 之于 `google.com`）；
 * - scheme 只允许 http/https：`intent://`、`file://`、`javascript:`、`data:` 全部拒绝。
 *
 * [providerHosts] 里请不要塞回环地址——回环走的是上面的精确匹配分支。
 */
internal fun isAllowedAuthUrl(raw: String, providerHosts: Set<String>): Boolean {
    val (scheme, host) = parseHost(raw) ?: return false
    if (scheme !in AllowedSchemes) {
        return false
    }
    if (host in LoopbackHosts) {
        return true
    }
    val allowed = providerHosts.mapNotNull { it.trim().lowercase().takeIf(String::isNotEmpty) }
    return allowed.any { it == host || host.endsWith(".$it") }
}

/**
 * 登录状态的隐私说明（页面顶部小字）：告诉用户 cookie 会被保存，并指出清除入口。
 *
 * 用户明确要求「下次不用重新授权」，所以登录态是**默认保留**的；
 * 同时必须让用户知道这件事，并给一个显式出口——透明比"偷偷存"重要。
 */
internal fun webLoginPersistNotice(): String =
    "登录状态会保存在本机，方便下次直接使用；可在右上角「清除」"

/** 用户主动清除后的提示。 */
internal fun webLoginClearedNotice(): String =
    "已清除该站登录状态，下次需要重新登录"

/**
 * 全屏 WebView 登录窗。
 *
 * @param url 网关 `POST /api/auth/start` 返回的 `authorize_url`
 * @param providerHosts 允许导航的授权域名（不含回环）
 * @param onCancel 取消：关窗 + 取消服务端会话（与现有「取消授权」同一语义）
 * @param onCallbackReached 命中 `127.0.0.1/oauth/callback`：关窗并立即催一次轮询
 * @param onFinish 顶栏「完成」：回调页没自动跳回时的兜底，同样催一次轮询
 */
@SuppressLint("SetJavaScriptEnabled")
@Composable
internal fun OnboardWebLoginDialog(
    url: String,
    providerHosts: Set<String>,
    onCancel: () -> Unit,
    onCallbackReached: () -> Unit,
    onFinish: () -> Unit,
) {
    var blocked by remember { mutableStateOf<String?>(null) }
    var webView by remember { mutableStateOf<WebView?>(null) }
    var cleared by remember { mutableStateOf(false) }

    // cookie **默认保留**（用户要求下次不用重新授权）：这里只落盘，不清理。
    // CookieManager 是异步写盘的，进程被回收可能丢 cookie，所以关键节点显式 flush()。
    fun flushCookies() {
        runCatching { CookieManager.getInstance().flush() }
    }

    // 退到后台（含切走 App）时落盘一次，避免进程被回收丢登录态。
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_PAUSE || event == Lifecycle.Event.ON_STOP) {
                flushCookies()
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    DisposableEffect(Unit) {
        onDispose {
            // 只销毁 WebView 并落盘；**不删 cookie、不清缓存**（缓存留着下次更快）。
            webView?.let { view ->
                runCatching {
                    view.stopLoading()
                    view.destroy()
                }
            }
            webView = null
            flushCookies()
        }
    }

    Dialog(
        onDismissRequest = onCancel,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Surface(
            modifier = Modifier.fillMaxSize(),
            color = MaterialTheme.colorScheme.background,
        ) {
            Column(Modifier.fillMaxSize()) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text(
                        text = "登录授权",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f),
                    )
                    McaButton(onClick = onCancel, style = McaButtonStyle.Outlined) { Text("取消") }
                    // 次要动作：只有用户主动点才清登录态（默认是保留的）。
                    McaButton(
                        onClick = {
                            runCatching {
                                CookieManager.getInstance().removeAllCookies(null)
                                webView?.clearCache(true)
                                webView?.clearHistory()
                            }
                            cleared = true
                        },
                        style = McaButtonStyle.Outlined,
                    ) { Text("清除") }
                    McaButton(onClick = onFinish, style = McaButtonStyle.Primary) { Text("完成") }
                }
                Text(
                    text = if (cleared) webLoginClearedNotice() else webLoginPersistNotice(),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 16.dp),
                )
                if (blocked != null) {
                    Text(
                        text = "该页面不在预期域名内，已拦截",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp),
                    )
                }
                Spacer(Modifier.height(1.dp))
                AndroidView(
                    modifier = Modifier
                        .fillMaxWidth()
                        .weight(1f),
                    factory = { context ->
                        WebView(context).apply {
                            settings.javaScriptEnabled = true
                            settings.domStorageEnabled = true
                            settings.setSupportMultipleWindows(false)
                            // cookie 默认接受（显式设一次更稳）；OAuth 登录经常跨站
                            // （IdP → 授权域 → 回环），第三方 cookie 必须允许，否则登录态存不住。
                            CookieManager.getInstance().setAcceptCookie(true)
                            CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                            webViewClient = object : WebViewClient() {
                                override fun shouldOverrideUrlLoading(
                                    view: WebView,
                                    request: WebResourceRequest,
                                ): Boolean {
                                    val target = request.url?.toString().orEmpty()
                                    // 回调地址要**真正请求到本机网关**，所以放行（返回 false），
                                    // 完成信号靠 onPageStarted 判定。
                                    if (isOauthCallbackUrl(target)) {
                                        flushCookies()
                                        return false
                                    }
                                    return if (isAllowedAuthUrl(target, providerHosts)) {
                                        false
                                    } else {
                                        blocked = target
                                        true
                                    }
                                }

                                override fun onPageStarted(view: WebView?, url: String?, favicon: android.graphics.Bitmap?) {
                                    super.onPageStarted(view, url, favicon)
                                    if (url != null && isOauthCallbackUrl(url)) {
                                        onCallbackReached()
                                    }
                                }

                                override fun onPageFinished(view: WebView?, url: String?) {
                                    super.onPageFinished(view, url)
                                    // CookieManager 异步写盘：每页加载完就 flush，进程被回收也不丢登录态。
                                    flushCookies()
                                }
                            }
                            loadUrl(url)
                            webView = this
                        }
                    },
                )
            }
        }
    }
}
