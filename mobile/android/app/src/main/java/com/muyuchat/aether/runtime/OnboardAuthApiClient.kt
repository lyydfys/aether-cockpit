package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.Locale

/**
 * 手机端原生接入渠道所需的全部端点：浏览器授权（OAuth PKCE）、设备码、Token/JSON、手动凭据。
 *
 * 端点逐个核对自 `internal/core/gateway/gateway.go`：
 *  - `POST /api/auth/start`（gateway.go:1109）→ `{session}`，`auth_api.go:22-78`
 *  - `GET  /api/auth/{id}`（:1110）→ `{session}`，轮询状态
 *  - `POST /api/auth/{id}/callback`（:1111）→ 浏览器回调不到本机时，粘贴回调地址或裸 code
 *  - `POST /api/auth/{id}/cancel`（:1112）
 *  - `POST /api/auth/{id}/commit`（:1113）→ 凭证落成账号
 *  - `POST /api/provider/{kind}/device` + `GET /api/provider/{kind}/device/{id}` + `.../cancel`
 *    （:833-871，kind ∈ trae/kiro/zed/codebuddy/workbuddy/qoder/minimax）
 *  - `POST /api/accounts`（gateway.go:959，`handleAddAccount`）→ 手动凭据 / Token JSON 共用
 *
 * 安全：这里只搬运凭据，**不打印、不落盘、不写剪贴板**；设备码与授权 URL 可以进剪贴板
 * （它们不是凭据），由页面决定。
 */

/** `auth.Session` 的只读投影（`internal/core/auth/coordinator.go` 的 Session 结构）。 */
data class OnboardAuthSession(
    val id: String,
    val providerId: String,
    val method: String,
    /** `pending` / `success` / `error` / `cancelled` / `expired` */
    val status: String,
    val authorizeUrl: String = "",
    val redirectUri: String = "",
    val userCode: String = "",
    val verificationUri: String = "",
    val intervalSeconds: Int = 0,
    val error: String = "",
) {
    val isSuccess: Boolean get() = status.equals("success", ignoreCase = true)
    val isTerminal: Boolean get() = isSuccess || isError
    val isError: Boolean get() = status.equals("error", ignoreCase = true) ||
        status.equals("cancelled", ignoreCase = true) ||
        status.equals("expired", ignoreCase = true)
}

/** 设备码会话（`POST /api/provider/{kind}/device` 的应答）。 */
data class OnboardDeviceSession(
    val id: String,
    val userCode: String = "",
    val verificationUri: String = "",
    val loginUrl: String = "",
    val intervalSeconds: Int = 0,
) {
    /** 有设备码就贴码，否则给登录页。 */
    val hasUserCode: Boolean get() = userCode.isNotBlank()
}

/** 设备码轮询结果：`done` 表示已入库（服务端自己建账号，无需 commit）。 */
data class OnboardDevicePoll(val state: String, val error: String = "") {
    val isDone: Boolean get() = state.equals("done", ignoreCase = true)
    val isFailed: Boolean get() = state.equals("failed", ignoreCase = true) ||
        state.equals("expired", ignoreCase = true) ||
        state.equals("unknown", ignoreCase = true)
}

class OnboardAuthApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 25_000,
) {
    /* ---------------- 浏览器授权 / 通用设备码 ---------------- */

    fun startAuth(status: GatewayStatus, providerId: String, method: String): OnboardAuthSession {
        val body = JSONObject()
            .put("provider_id", providerId.trim())
            .put("method", method.trim())
            .toString()
        return parseAuthSession(post(status, "/api/auth/start", body))
    }

    fun getAuth(status: GatewayStatus, id: String): OnboardAuthSession =
        parseAuthSession(get(status, "/api/auth/" + encodePath(id)))

    /** 浏览器无法回调本机时用：粘贴完整回调地址，或只贴 code（`auth_api.go:98-121`）。 */
    fun submitCallback(status: GatewayStatus, id: String, callbackUrl: String): OnboardAuthSession {
        val body = JSONObject().put("callback_url", callbackUrl.trim()).toString()
        return parseAuthSession(post(status, "/api/auth/" + encodePath(id) + "/callback", body))
    }

    fun cancelAuth(status: GatewayStatus, id: String) {
        post(status, "/api/auth/" + encodePath(id) + "/cancel", "{}")
    }

    /** 把认证成功的凭证落成账号；失败文案直接用服务端原文（`auth_api.go:127-187`）。 */
    fun commitAuth(status: GatewayStatus, id: String, label: String) {
        val body = JSONObject().put("label", label.trim()).toString()
        post(status, "/api/auth/" + encodePath(id) + "/commit", body)
    }

    /* ---------------- 渠道各自的设备码 / 登录会话 ---------------- */

    fun startDevice(status: GatewayStatus, kind: String, site: String): OnboardDeviceSession {
        val body = JSONObject()
        if (site.isNotBlank()) {
            body.put("site", site.trim())
        }
        return parseDeviceSession(post(status, "/api/provider/${kind.trim()}/device", body.toString()))
    }

    fun pollDevice(status: GatewayStatus, kind: String, id: String): OnboardDevicePoll =
        parseDevicePoll(get(status, "/api/provider/${kind.trim()}/device/" + encodePath(id)))

    fun cancelDevice(status: GatewayStatus, kind: String, id: String) {
        post(status, "/api/provider/${kind.trim()}/device/" + encodePath(id) + "/cancel", "{}")
    }

    /* ---------------- 手动凭据 / Token ---------------- */

    /**
     * `POST /api/accounts`（gateway.go:959）：内置渠道走 `provider_id` + `credentials`。
     * 服务端会把 credentials 里的字段原样写进账号；不校验未知字段。
     */
    fun addAccount(
        status: GatewayStatus,
        providerId: String,
        label: String,
        credentials: Map<String, String>,
    ) {
        val creds = JSONObject()
        credentials.forEach { (key, value) -> creds.put(key, value) }
        val body = JSONObject()
            .put("provider_id", providerId.trim())
            .put("label", label.trim())
            .put("credentials", creds)
            .toString()
        post(status, "/api/accounts", body)
    }

    /**
     * Windsurf 邮箱 + 密码登录：`POST /api/provider/windsurf/login {"email","password"}`
     * （`gateway.go:820`，桌面 `onboard.js:561-582`）。
     *
     * 密码只作为本次请求体出现一次：不打印、不落盘、不进剪贴板；
     * 服务端换 token 后自己建账号，成功后刷新渠道列表即可看到。
     */
    fun loginWindsurf(status: GatewayStatus, email: String, password: String) {
        val body = JSONObject()
            .put("email", email.trim())
            .put("password", password)
            .toString()
        post(status, "/api/provider/windsurf/login", body)
    }

    /* ---------------- HTTP ---------------- */

    private fun get(status: GatewayStatus, path: String): String = request(status, "GET", path, null)

    private fun post(status: GatewayStatus, path: String, body: String): String =
        request(status, "POST", path, body)

    private fun request(status: GatewayStatus, method: String, path: String, body: String?): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)
                        ?.use { it.readText().take(2_048) }
                        ?.let { parseSettingsError(it) }
                }.getOrNull()
                throw ManagementApiException("http_$code", detail?.take(200) ?: "管理接口返回 $code")
            }
            return connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 2 * 1024 * 1024): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return out.toString()
            }
            out.append(buffer, 0, read)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "管理接口响应过大")
            }
        }
    }
}

/* ========================================================================== */
/* 纯函数（单测覆盖）：方法判定、字段名、Token 粘贴解析                        */
/* ========================================================================== */

/**
 * 某渠道支持哪些接入方式。**逐行照抄**桌面 `onboard.js:207-216`：
 * `supported_auths` 打底，再按 id 前缀补设备码类方法，最后兜底 `manual_input`。
 */
internal fun onboardMethods(provider: ProviderSummary): List<String> {
    val id = provider.id.trim().lowercase(Locale.US)
    val methods = ArrayList<String>(provider.supportedAuths)
    if (id.startsWith("minimax-code")) methods += "minimax_device"
    if (id.startsWith("qoder-")) methods += "qoder_device"
    if (id.startsWith("workbuddy-")) methods += "workbuddy_device"
    if (id.startsWith("codebuddy-")) methods += "codebuddy_device"
    if (id == "zed") methods += "zed_device"
    if (id == "kiro") methods += "kiro_device"
    if (id.startsWith("trae-")) methods += "trae_device"
    if (id == "windsurf") methods += "windsurf_login"
    if (methods.isEmpty()) methods += "manual_input"
    return methods.distinct()
}

/** `METHOD_LABEL`（`onboard.js:9-24`），键与文案全量照抄。 */
internal val ONBOARD_METHOD_LABELS: Map<String, String> = mapOf(
    "local_import" to "本机导入",
    "oauth_pkce" to "浏览器授权",
    "device_code" to "设备码",
    "manual_input" to "手动凭据",
    "webview_login" to "浏览器登录",
    "minimax_device" to "MiniMax 设备码",
    "qoder_device" to "Qoder 登录",
    "workbuddy_device" to "WorkBuddy 登录",
    "codebuddy_device" to "CodeBuddy 登录",
    "zed_device" to "Zed 登录",
    "kiro_device" to "Kiro 登录",
    "trae_device" to "Trae 登录",
    "windsurf_login" to "Windsurf 登录",
    "token_json" to "Token / JSON",
)

internal fun onboardMethodLabel(method: String): String = ONBOARD_METHOD_LABELS[method] ?: method

/** `xxx_device` → 服务端路由里的渠道段名（`/api/provider/{kind}/device`）。 */
internal fun onboardDeviceKind(method: String): String? = when (method) {
    "minimax_device" -> "minimax"
    "qoder_device" -> "qoder"
    "workbuddy_device" -> "workbuddy"
    "codebuddy_device" -> "codebuddy"
    "zed_device" -> "zed"
    "kiro_device" -> "kiro"
    "trae_device" -> "trae"
    else -> null
}

/**
 * 手机端做不了的方式与原因（如实说明，不给假按钮）。
 *
 * 判断依据：
 *  - `local_import`：服务端要读桌面客户端的凭证文件（`auth_api.go:190-288` 的
 *    `DiscoverLocalAccounts`），手机上不存在那些路径；
 *  - `webview_login`：桌面用内置 WebView 走网页登录（`onboard.js:777-784`），手机端
 *    没有对应 WebView 容器，且这条链路不在 AndroidManifest 写集里。
 *
 * `windsurf_login` 已开：手机上就是一个 POST（邮箱 + 密码 → token），
 * 走 `OnboardAuthApiClient.loginWindsurf`，不再列入「做不了」。
 */
internal fun onboardUnsupportedReason(method: String): String? = when (method) {
    "local_import" -> "需要桌面客户端的本机凭证文件，手机上没有那些路径"
    "webview_login" -> "需要桌面内置 WebView 网页登录，手机端未启用"
    else -> null
}

/** `oauth_pkce` / `device_code` 需要渠道声明了 OAuth 参数，否则服务端会 400。 */
internal fun onboardAuthNeedsOAuth(method: String): Boolean =
    method == "oauth_pkce" || method == "device_code"

/** 某种方式是否可用；null 表示可用，非 null 是不可用原因。 */
internal fun onboardMethodBlockedBy(method: String, provider: ProviderSummary): String? {
    onboardUnsupportedReason(method)?.let { return it }
    if (onboardAuthNeedsOAuth(method) && !provider.hasOAuth) {
        return "该渠道未声明 OAuth 参数（Manifest.OAuth 为空）"
    }
    return null
}

/**
 * Token 落到哪个凭据字段（桌面 `onboard.js:421-428` 的 `tokenKeyOf`）：
 * 表单里先找 api_key / access_token / token / session_key / cookie，
 * 再取第一个 password 字段，最后回落 access_token。
 */
internal fun onboardTokenKey(schema: List<ProviderFormField>): String {
    val keys = schema.map { it.key }
    for (candidate in listOf("api_key", "access_token", "token", "session_key", "cookie")) {
        if (candidate in keys) {
            return candidate
        }
    }
    return schema.firstOrNull { it.type == "password" }?.key ?: "access_token"
}

/** 表单必填字段里缺了哪些（桌面 `onboard.js:396-397`）。 */
internal fun onboardMissingRequired(
    creds: Map<String, String>,
    schema: List<ProviderFormField>,
): List<String> = schema.filter { it.required && creds[it.key].isNullOrBlank() }
    .map { it.label.ifBlank { it.key } }

/**
 * 把粘贴的 Token / JSON 折成凭据表（桌面 `onboard.js:356-452`）：
 *  - 纯文本 → 单个凭据（字段名由 [onboardTokenKey] 决定）；
 *  - `{...}` 或 `[{...}]` → 逐个对象；
 *  - 嵌套对象只在 `tokens/credentials/auth/data` 下摊平，其余整体序列化成字符串；
 *  - 别名：accessToken→access_token、refreshToken→refresh_token、apiKey→api_key 等。
 */
internal fun onboardParseTokenInput(
    raw: String,
    schema: List<ProviderFormField>,
): List<Map<String, String>> {
    val text = raw.trim()
    if (text.isEmpty()) {
        return emptyList()
    }
    if (!text.startsWith("{") && !text.startsWith("[")) {
        val key = onboardTokenKey(schema)
        return listOf(mapOf(key to text))
    }
    val parsed = try {
        if (text.startsWith("[")) JSONArray(text) else null
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_json", "JSON 不合法")
    }
    if (parsed != null) {
        return (0 until parsed.length()).mapNotNull { index ->
            parsed.optJSONObject(index)?.let(::onboardNormalizeCreds)
        }
    }
    val obj = try {
        JSONObject(text)
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_json", "JSON 不合法")
    }
    return listOf(onboardNormalizeCreds(obj))
}

internal fun onboardNormalizeCreds(obj: JSONObject): Map<String, String> {
    val creds = LinkedHashMap<String, String>()
    fun walk(source: JSONObject) {
        source.keys().forEach { key ->
            val value = source.opt(key)
            when {
                value is JSONObject -> {
                    if (key in setOf("tokens", "credentials", "auth", "data")) {
                        walk(value)
                    } else {
                        creds[key] = value.toString()
                    }
                }
                value is JSONArray -> creds[key] = value.toString()
                value == null || value == JSONObject.NULL -> Unit
                value.toString().isEmpty() -> Unit
                else -> creds[key] = value.toString()
            }
        }
    }
    walk(obj)
    fun alias(from: String, to: String) {
        if (creds[to].isNullOrBlank() && !creds[from].isNullOrBlank()) {
            creds[to] = creds.getValue(from)
        }
    }
    alias("accessToken", "access_token")
    alias("refreshToken", "refresh_token")
    alias("idToken", "id_token")
    alias("apiKey", "api_key")
    alias("OPENAI_API_KEY", "api_key")
    alias("openai_api_key", "api_key")
    if (creds["access_token"].isNullOrBlank() && !creds["token"].isNullOrBlank()) {
        creds["access_token"] = creds.getValue("token")
    }
    return creds
}

/** 手动凭据的默认表单：服务端没给 `auth_form_schema` 时的兜底（`onboard.js:298-301`）。 */
internal val ONBOARD_DEFAULT_MANUAL_SCHEMA: List<ProviderFormField> = listOf(
    ProviderFormField(key = "api_key", label = "API Key", type = "password", required = true, placeholder = "sk-…"),
    ProviderFormField(key = "base_url", label = "Base URL（可选）", type = "text"),
)

internal fun onboardManualSchema(provider: ProviderSummary): List<ProviderFormField> =
    provider.authFormSchema.ifEmpty { ONBOARD_DEFAULT_MANUAL_SCHEMA }

/* ---------------- 解析 ---------------- */

private fun encodePath(value: String): String = URLEncoder.encode(value.trim(), "UTF-8")

private fun sessionObject(body: String): JSONObject {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的认证结果格式不正确")
    }
    return root.optJSONObject("session") ?: root
}

internal fun parseAuthSession(body: String): OnboardAuthSession {
    val obj = sessionObject(body)
    val id = obj.optString("id").trim()
    if (id.isEmpty()) {
        throw ManagementApiException("invalid_response", "管理接口返回的认证会话缺少 id")
    }
    return OnboardAuthSession(
        id = id,
        providerId = obj.optString("provider_id").trim(),
        method = obj.optString("method").trim(),
        status = obj.optString("status").trim().ifBlank { "pending" },
        authorizeUrl = obj.optString("authorize_url").trim(),
        redirectUri = obj.optString("redirect_uri").trim(),
        userCode = obj.optString("user_code").trim(),
        verificationUri = obj.optString("verification_uri").trim(),
        intervalSeconds = obj.optInt("interval", 0).coerceAtLeast(0),
        error = obj.optString("error").trim(),
    )
}

internal fun parseDeviceSession(body: String): OnboardDeviceSession {
    val obj = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设备码格式不正确")
    }
    val id = obj.optString("id").trim()
    if (id.isEmpty()) {
        throw ManagementApiException("invalid_response", "管理接口返回的设备码缺少 id")
    }
    return OnboardDeviceSession(
        id = id,
        userCode = obj.optString("user_code").trim(),
        verificationUri = obj.optString("verification_uri").trim(),
        loginUrl = obj.optString("login_url").trim(),
        intervalSeconds = obj.optInt("interval", 0).coerceAtLeast(0),
    )
}

internal fun parseDevicePoll(body: String): OnboardDevicePoll {
    val obj = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的登录状态格式不正确")
    }
    return OnboardDevicePoll(
        state = obj.optString("state").trim().ifBlank { "unknown" },
        error = obj.optString("error").trim(),
    )
}
