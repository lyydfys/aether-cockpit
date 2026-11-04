package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.util.Base64

/** One channel row from `/api/providers`; secrets are deliberately absent. */
data class ProviderSummary(
    val id: String,
    val name: String,
    val description: String,
    val accountCount: Int,
    val readyCount: Int,
    val ready: Boolean,
    val off: Boolean,
    val brandColor: String,
    val accounts: List<AccountSummary> = emptyList(),
    /** 渠道族（`family`）；服务端可能为空。 */
    val family: String = "",
    /**
     * 该渠道声明的登录方式（`supported_auths`，`provider/manifest.go:132`）。
     *
     * 取值：`local_import` / `oauth_pkce` / `device_code` / `manual_input`。
     * `xxx_device` 不在这里 —— 桌面是按 id 前缀补的（`onboard.js:207-216`）。
     * `webview_login` 也不在这里：全仓没有渠道声明它（`AuthWebViewLogin` 常量无使用点），
     * 桌面 `onboard.js:777-784` 对它只开系统浏览器，属两端一致的死分支。
     */
    val supportedAuths: List<String> = emptyList(),
    /** 能力位图（`capabilities`）：chat / check_in / quota / local_import / local_inject / … */
    val capabilities: Map<String, Boolean> = emptyMap(),
    /** 手动凭据表单（`auth_form_schema`）：手动与 Token 两种接入据此决定字段名。 */
    val authFormSchema: List<ProviderFormField> = emptyList(),
    /** 是否声明了 OAuth 参数（服务端能代跑 PKCE / 设备码）。 */
    val hasOAuth: Boolean = false,
    /**
     * 渠道声明的模型 id（`models[].id`，`admin.go:54` 的 `providerView` 内嵌 manifest）。
     *
     * 概览页「聚合链路」用它推断这个渠道汇入哪些端点（桌面 `overview.js:364-374`
     * 的 `familiesOf`：id 里含 `claude` → Anthropic、含 `gemini` → Gemini、其余 → OpenAI）。
     * 只留 id 字符串——完整模型清单不在这里解析，避免把大对象带进 UI 状态。
     */
    val modelIds: List<String> = emptyList(),
)

/** 渠道自定义登录表单的一项（对应 `provider.FormFieldSchema`）。 */
data class ProviderFormField(
    val key: String,
    val label: String,
    val type: String,
    val placeholder: String = "",
    val required: Boolean = false,
    val help: String = "",
    val options: List<String> = emptyList(),
)

/** One quota window of an account row; `used` may be a fraction or a percentage. */
data class QuotaWindowSummary(
    val name: String,
    val used: Double,
    val amount: Long?,
    val limit: Long?,
    val unit: String,
    val resetsAt: String,
)

data class AccountQuotaSummary(
    val plan: String,
    val windows: List<QuotaWindowSummary>,
    val used: Int,
    val total: Int,
    val unit: String,
    val cooling: Boolean,
    val error: String,
)

/** Account row parsed from `keyList`; `masked` is never read into this model. */
data class AccountSummary(
    val id: String,
    val label: String,
    val enabled: Boolean,
    val active: Boolean,
    val restWhy: String,
    val restUntilMs: Long,
    val quota: AccountQuotaSummary?,
)

data class BundleImportFailure(
    val label: String,
    val error: String,
)

data class BundleImportResult(
    val imported: List<String>,
    val skipped: Int,
    val failed: List<BundleImportFailure>,
    val total: Int,
)

data class QoderLoginStart(
    val id: String,
    val state: String,
    val loginUrl: String,
    val interval: Int,
    val expiresIn: Int,
)

data class QoderLoginPoll(
    val state: String,
    val error: String,
    val account: String,
    val interval: Int,
)

class ManagementApiException(
    val code: String,
    override val message: String,
) : Exception(message)

/**
 * Narrow management client for the mobile runtime. It uses the ephemeral admin
 * session from the running gateway, keeps it in the Authorization header (never
 * a URL/query) and maps only non-sensitive provider fields.
 */
class ManagementApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchProviders(status: GatewayStatus): List<ProviderSummary> {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/providers").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", errorMessage(connection, code))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseProviders(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    /**
     * 自定义渠道保存：`POST /api/provider/save`。
     *
     * 服务端对**未注册**的 id 走 `customCoreProvider`（`provider_api.go:234-265`）：
     * 按「哪个 Base URL 字段非空」把账号落到内置协议渠道（openai-compat / codex /
     * claude-sub / gemini-compat / typesafe），不动态注册新渠道。成功后列表里表现为
     * 那个内置渠道多一个账号；调用方刷新列表即可看到账号数 +1。
     */
    fun saveCustomProvider(status: GatewayStatus, draft: CustomProviderDraft) {
        ProviderSaveApiClient().save(status, draft)
    }

    fun importAccountBundle(
        status: GatewayStatus,
        sealed: ByteArray,
        passphrase: String,
    ): BundleImportResult {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/accounts/import/bundle").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = 30_000
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            connection.outputStream.use { out ->
                out.write(importRequestBody(sealed, passphrase).toByteArray(Charsets.UTF_8))
            }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", errorMessage(connection, code))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseImportResult(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun startQoderLogin(status: GatewayStatus, site: String = "qoder-cn"): QoderLoginStart {
        val connection = managementConnection(status, "/api/provider/qoder/device", "POST")
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        try {
            connection.outputStream.use { out ->
                out.write(JSONObject().put("site", site).toString().toByteArray(Charsets.UTF_8))
            }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", errorMessage(connection, code))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseQoderLoginStart(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun pollQoderLogin(status: GatewayStatus, id: String): QoderLoginPoll {
        val encoded = java.net.URLEncoder.encode(id, Charsets.UTF_8.name())
        val connection = managementConnection(status, "/api/provider/qoder/device/$encoded", "GET")
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", errorMessage(connection, code))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseQoderLoginPoll(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun cancelQoderLogin(status: GatewayStatus, id: String) {
        val encoded = java.net.URLEncoder.encode(id, Charsets.UTF_8.name())
        val connection = managementConnection(status, "/api/provider/qoder/device/$encoded/cancel", "POST")
        connection.doOutput = true
        try {
            connection.outputStream.use { }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", errorMessage(connection, code))
            }
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun managementConnection(
        status: GatewayStatus,
        path: String,
        method: String,
        readTimeout: Int = readTimeoutMs,
    ): HttpURLConnection {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeout
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        return connection
    }

    private fun errorMessage(connection: HttpURLConnection, code: Int): String {
        val body = runCatching {
            connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it, 64 * 1024) }
        }.getOrNull()
        val detail = runCatching {
            if (body.isNullOrBlank()) "" else org.json.JSONObject(body).optString("error")
        }.getOrNull().orEmpty()
        return detail.ifBlank { "管理接口返回 $code" }.take(160)
    }

    private fun readLimited(reader: Reader, limit: Int = 8 * 1024 * 1024): String {
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

internal fun parseProviders(body: String): List<ProviderSummary> {
    val array = try {
        val trimmed = body.trimStart()
        when {
            // 真实管理接口返回 {"providers":[...], ...}；保留数组形态兼容旧测试/调用方。
            trimmed.startsWith("{") -> JSONObject(trimmed).optJSONArray("providers")
                ?: throw JSONException("missing providers")
            trimmed.startsWith("[") -> JSONArray(trimmed)
            else -> throw JSONException("unexpected body")
        }
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的渠道数据格式不正确")
    }
    val out = ArrayList<ProviderSummary>(array.length())
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            continue
        }
        val name = item.optString("display_name").trim()
            .ifBlank { item.optString("name").trim() }
            .ifBlank { id }
        out += ProviderSummary(
            id = id,
            name = name,
            description = item.optString("description").trim(),
            accountCount = item.optInt("account_count", 0).coerceAtLeast(0),
            readyCount = item.optInt("ready_count", 0).coerceAtLeast(0),
            ready = item.optBoolean("ready", false),
            off = item.optBoolean("off", false),
            brandColor = item.optString("brand_color").trim(),
            accounts = parseAccounts(item.optJSONArray("keyList")),
            family = item.optString("family").trim(),
            supportedAuths = parseStringArray(item.optJSONArray("supported_auths")),
            capabilities = parseBoolMap(item.optJSONObject("capabilities")),
            authFormSchema = parseAuthFormSchema(item.optJSONArray("auth_form_schema")),
            hasOAuth = item.optBoolean("oauth", false),
            modelIds = parseModelIds(item.optJSONArray("models")),
        )
    }
    return out
}

private fun parseStringArray(array: JSONArray?): List<String> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().ifBlank { null }
    }
}

/**
 * 渠道声明的模型 id。服务端 `models` 是**对象数组**（每个含 `id`），也可能给字符串数组，
 * 两种都吃；缺 `id` 的条目直接丢掉（概览端点推断只认 id）。
 */
internal fun parseModelIds(array: JSONArray?): List<String> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index)
        if (item != null) {
            item.optString("id").trim().ifBlank { null }
        } else {
            array.optString(index).trim().ifBlank { null }
        }
    }.distinct()
}

private fun parseBoolMap(obj: JSONObject?): Map<String, Boolean> {    if (obj == null) {
        return emptyMap()
    }
    val out = LinkedHashMap<String, Boolean>(obj.length())
    obj.keys().forEach { key -> out[key] = obj.optBoolean(key, false) }
    return out
}

/** `auth_form_schema`：key / label / type / placeholder / required / help / options。 */
private fun parseAuthFormSchema(array: JSONArray?): List<ProviderFormField> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val key = item.optString("key").trim()
        if (key.isEmpty()) {
            return@mapNotNull null
        }
        ProviderFormField(
            key = key,
            label = item.optString("label").trim().ifBlank { key },
            type = item.optString("type").trim().ifBlank { "text" },
            placeholder = item.optString("placeholder").trim(),
            required = item.optBoolean("required", false),
            help = item.optString("help").trim(),
            options = parseStringArray(item.optJSONArray("options")),
        )
    }
}

private fun parseAccounts(array: JSONArray?): List<AccountSummary> {
    if (array == null) {
        return emptyList()
    }
    val out = ArrayList<AccountSummary>(array.length())
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            continue
        }
        val rest = item.optJSONObject("rest")
        out += AccountSummary(
            id = id,
            label = item.optString("name").trim(),
            enabled = item.optBoolean("on", true),
            active = item.optBoolean("active", false),
            restWhy = rest?.optString("why").orEmpty().trim(),
            restUntilMs = rest?.optLong("until", 0L)?.coerceAtLeast(0L) ?: 0L,
            quota = parseAccountQuota(item.optJSONObject("quota")),
        )
    }
    return out
}

private fun parseAccountQuota(item: JSONObject?): AccountQuotaSummary? {
    if (item == null) {
        return null
    }
    val windows = item.optJSONArray("windows")
    val list = if (windows == null) {
        emptyList()
    } else {
        (0 until windows.length()).mapNotNull { index ->
            val window = windows.optJSONObject(index) ?: return@mapNotNull null
            QuotaWindowSummary(
                name = window.optString("name").trim(),
                used = window.optDouble("used", 0.0),
                amount = optionalLong(window, "amount"),
                limit = optionalLong(window, "limit"),
                unit = window.optString("unit").trim(),
                resetsAt = window.optString("resetsAt").trim(),
            )
        }
    }
    return AccountQuotaSummary(
        plan = item.optString("plan").trim(),
        windows = list,
        used = item.optInt("used", 0).coerceAtLeast(0),
        total = item.optInt("total", 0).coerceAtLeast(0),
        unit = item.optString("unit").trim(),
        cooling = item.optBoolean("cooling", false),
        error = item.optString("error").trim(),
    )
}

private fun optionalLong(item: JSONObject, key: String): Long? {
    if (!item.has(key) || item.isNull(key)) {
        return null
    }
    return item.optLong(key, 0L)
}

internal fun importRequestBody(sealed: ByteArray, passphrase: String): String =
    JSONObject()
        .put("passphrase", passphrase)
        .put("data", Base64.getEncoder().encodeToString(sealed))
        .toString()

internal fun parseImportResult(body: String): BundleImportResult {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的导入结果格式不正确")
    }
    val importedArray = root.optJSONArray("imported")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的导入结果格式不正确")
    val imported = ArrayList<String>(importedArray.length())
    for (index in 0 until importedArray.length()) {
        val label = importedArray.optString(index).trim()
        if (label.isNotEmpty()) {
            imported += label
        }
    }
    val failedArray = root.optJSONArray("failed")
    val failed = if (failedArray == null) {
        emptyList()
    } else {
        (0 until failedArray.length()).mapNotNull { index ->
            val item = failedArray.optJSONObject(index) ?: return@mapNotNull null
            BundleImportFailure(
                label = item.optString("label").trim(),
                error = item.optString("error").trim().take(160),
            )
        }
    }
    val skipped = root.optJSONArray("skipped")?.length() ?: 0
    val total = if (root.has("total")) {
        root.optInt("total", imported.size + skipped + failed.size).coerceAtLeast(0)
    } else {
        imported.size + skipped + failed.size
    }
    return BundleImportResult(imported, skipped, failed, total)
}

internal fun parseQoderLoginStart(body: String): QoderLoginStart {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的登录会话格式不正确")
    }
    val id = root.optString("id").trim()
    val state = root.optString("state").trim()
    val loginUrl = root.optString("login_url").trim()
    if (id.isEmpty() || state.isEmpty() || loginUrl.isEmpty()) {
        throw ManagementApiException("invalid_response", "管理接口返回的登录会话格式不正确")
    }
    return QoderLoginStart(
        id = id,
        state = state,
        loginUrl = loginUrl,
        interval = root.optInt("interval", 1).coerceIn(1, 10),
        expiresIn = root.optInt("expires_in", 0).coerceAtLeast(0),
    )
}

internal fun parseQoderLoginPoll(body: String): QoderLoginPoll {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的登录状态格式不正确")
    }
    val state = root.optString("state").trim()
    if (state.isEmpty()) {
        throw ManagementApiException("invalid_response", "管理接口返回的登录状态格式不正确")
    }
    return QoderLoginPoll(
        state = state,
        error = root.optString("error").trim().take(160),
        account = root.optString("account").trim(),
        interval = root.optInt("interval", 1).coerceIn(1, 10),
    )
}

/** Parses the channel's #RRGGBB brand accent; nil when the manifest has none. */
internal fun parseBrandColor(value: String): Long? {
    val hex = value.trim().removePrefix("#")
    if (hex.length != 6) {
        return null
    }
    val rgb = hex.toLongOrNull(16) ?: return null
    return 0xFF000000L or rgb
}
