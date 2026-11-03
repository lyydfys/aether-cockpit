package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL

/**
 * Only allowlisted, already-masked settings leave this layer. Secrets
 * (webdav/otlp headers/tools keys/remote quota ciphertext) are never mapped.
 */
data class RedactRule(
    val id: String,
    val name: String,
    val kind: String,
    val action: String,
    val enabled: Boolean,
)

data class RedactDetail(
    val enabled: Boolean,
    val rules: List<RedactRule>,
    val actions: Map<String, String>,
)

/** 用户自定义规则。prefix 与 regex 二选一，服务端 NormalizeKind 后再回读。 */
data class RedactCustomRule(
    val kind: String,
    val prefix: String = "",
    val regex: String = "",
)

data class SettingsSummary(
    val addr: String,
    val port: Int,
    val lanEnabled: Boolean,
    val lanUrls: List<String>,
    val proxyMasked: String,
    val corsOrigins: List<String>,
    val allowIPs: List<String>,
    val redactEnabled: Boolean,
    val redactPersonal: Boolean,
    val redactActions: List<String>,
    val redactRuleCount: Int,
    val redactAllowCount: Int,
    val desktopApp: Boolean,
    val customRules: List<RedactCustomRule> = emptyList(),
)

class SettingsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchSettings(status: GatewayStatus): SettingsSummary {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/settings").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", "管理接口返回 $code")
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseSettings(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun fetchRedactDetail(status: GatewayStatus): RedactDetail {
        val body = requestJson(status, "/api/settings/redact-actions", "GET", null)
        return parseRedactDetail(body)
    }

    /** GET /api/settings 是自定义规则的唯一读取入口；redact-actions 只返回内置规则。 */
    fun fetchCustomRules(status: GatewayStatus): List<RedactCustomRule> =
        parseSettings(requestJson(status, "/api/settings", "GET", null)).customRules

    fun replaceRedactRules(status: GatewayStatus, rules: List<RedactCustomRule>): List<RedactCustomRule> =
        parseSettings(requestJson(status, "/api/settings/redact-rules", "POST", buildRedactRulesJson(rules))).customRules

    fun editRedactRule(
        status: GatewayStatus,
        at: Int,
        was: RedactCustomRule,
        rule: RedactCustomRule,
    ): List<RedactCustomRule> = parseSettings(
        requestJson(status, "/api/settings/redact-rule", "POST", buildRedactRuleJson(at, was, rule)),
    ).customRules

    fun setProxy(status: GatewayStatus, proxy: String): String {
        val body = requestJson(status, "/api/settings", "POST", buildProxyJson(proxy))
        return parseProxyMasked(body)
    }

    fun setCorsOrigins(status: GatewayStatus, origins: List<String>): List<String> {
        val body = requestJson(status, "/api/settings/cors", "POST", buildCorsJson(origins))
        return parseCorsOrigins(body)
    }

    fun setAllowIPs(status: GatewayStatus, ips: List<String>): List<String> {
        val body = requestJson(status, "/api/settings/allowips", "POST", buildAllowIpsJson(ips))
        return parseAllowIPs(body)
    }

    fun setLan(status: GatewayStatus, on: Boolean): Boolean {
        val body = requestJson(status, "/api/settings/lan", "POST", buildLanToggleJson(on))
        return parseLanEnabled(body)
    }

    fun setRedactActions(status: GatewayStatus, actions: Map<String, String>): Map<String, String> {
        val body = requestJson(status, "/api/settings/redact-actions", "POST", buildRedactActionsJson(actions))
        return parseRedactActions(body)
    }

    /**
     * 个人信息脱敏开关走 UI 偏好透传：`redactPersonal` 不在 reservedSettingKeys 里，
     * `POST /api/settings` 会按 key 合并进 settings.ui，再经 syncRedactPrefs 落到引擎。
     * 没有 `/api/settings/redact-personal` 这个路由，别去造。
     */
    fun setRedactPersonal(status: GatewayStatus, enabled: Boolean): Boolean {
        val body = requestJson(status, "/api/settings", "POST", buildRedactPersonalJson(enabled))
        return parseRedactPersonal(body)
    }

    private fun requestJson(status: GatewayStatus, path: String, method: String, body: String?): String {
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
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
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

    fun setRedactEnabled(status: GatewayStatus, enabled: Boolean): Boolean {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val body = buildRedactToggleJson(enabled)
        val connection = (URL("http://${status.addr}/api/settings/redact-actions").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", "管理接口返回 $code")
            }
            val response = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseRedactEnabled(response)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 4 * 1024 * 1024): String {
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

internal fun buildCorsJson(origins: List<String>): String =
    JSONObject().put("origins", JSONArray(origins)).toString()

internal fun buildAllowIpsJson(ips: List<String>): String =
    JSONObject().put("ips", JSONArray(ips)).toString()

internal fun parseCorsOrigins(body: String): List<String> = parseStringList(body, "corsOrigins")

internal fun parseAllowIPs(body: String): List<String> = parseStringList(body, "allowIPs")

private fun parseStringList(body: String, key: String): List<String> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    val array = root.optJSONArray(key) ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().ifBlank { null }
    }
}

internal fun buildProxyJson(proxy: String): String =
    JSONObject().put("proxy", proxy.trim()).toString()

internal fun parseProxyMasked(body: String): String {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的代理设置格式不正确")
    }
    if (!root.has("proxy")) {
        throw ManagementApiException("invalid_response", "管理接口返回的代理设置缺少 proxy")
    }
    return root.optString("proxy").trim()
}

internal fun validateProxyInput(input: String): String? {
    val value = input.trim()
    if (value.isEmpty()) {
        return null
    }
    if (value.any { it.isWhitespace() }) {
        return "代理地址不能包含空格"
    }
    if (value.contains('@')) {
        return "手机端不保存带凭据的代理地址；请在 Windows 端配置"
    }
    val uri = runCatching { URI(value) }.getOrNull() ?: return "代理地址格式不正确"
    val scheme = uri.scheme?.lowercase() ?: return "代理地址需要 http://、https:// 或 socks5://"
    if (scheme !in setOf("http", "https", "socks5")) {
        return "只支持 http、https 或 socks5 代理"
    }
    if (uri.host.isNullOrBlank() || uri.port !in 1..65535) {
        return "代理地址需要主机和端口，例如 socks5://127.0.0.1:7890"
    }
    return null
}

internal fun buildLanToggleJson(on: Boolean): String =
    JSONObject().put("on", on).toString()

internal fun parseLanEnabled(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的局域网状态格式不正确")
    }
    if (!root.has("lan")) {
        throw ManagementApiException("invalid_response", "管理接口返回的局域网状态缺少 lan")
    }
    return root.optBoolean("lan", false)
}

internal fun parseSettingsError(body: String): String? = runCatching {
    JSONObject(body.trim()).optString("error").trim().ifBlank { null }
}.getOrNull()

internal fun buildRedactActionsJson(actions: Map<String, String>): String {
    val map = JSONObject()
    actions.forEach { (kind, action) -> map.put(kind, action) }
    return JSONObject().put("actions", map).toString()
}

internal fun buildRedactRulesJson(rules: List<RedactCustomRule>): String {
    val array = JSONArray()
    rules.forEach { array.put(customRuleJson(it)) }
    return JSONObject().put("rules", array).toString()
}

internal fun buildRedactRuleJson(at: Int, was: RedactCustomRule, rule: RedactCustomRule): String =
    JSONObject()
        .put("at", at)
        .put("was", customRuleJson(was))
        .put("rule", customRuleJson(rule))
        .toString()

private fun customRuleJson(rule: RedactCustomRule): JSONObject = JSONObject().apply {
    put("kind", rule.kind.trim())
    if (rule.prefix.isNotBlank()) put("prefix", rule.prefix.trim()) else put("regex", rule.regex.trim())
}

/** 写入失败时不本地覆盖旧列表；只有成功回读才切换。 */
internal fun mergeRedactRules(
    current: List<RedactCustomRule>,
    readback: List<RedactCustomRule>?,
    error: String?,
): List<RedactCustomRule> = if (error == null && readback != null) readback else current

internal fun parseRedactDetail(body: String): RedactDetail {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏详情格式不正确")
    }
    val array = root.optJSONArray("rules")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的脱敏详情缺少 rules")
    val rules = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val kind = item.optString("kind").trim()
        if (kind.isBlank()) {
            return@mapNotNull null
        }
        RedactRule(
            id = item.optString("id").trim().ifBlank { kind },
            name = item.optString("name").trim().ifBlank { kind },
            kind = kind,
            action = item.optString("action").trim().ifBlank { "mask" },
            enabled = item.optBoolean("enabled", true),
        )
    }
    val overrides = root.optJSONObject("actions")
    val actions = if (overrides == null) {
        emptyMap()
    } else {
        overrides.keys().asSequence().mapNotNull { kind ->
            val action = overrides.optString(kind).trim()
            if (action in setOf("mask", "warn", "block")) kind to action else null
        }.toMap()
    }
    return RedactDetail(
        enabled = root.optBoolean("enabled", false),
        rules = rules,
        actions = actions,
    )
}

internal fun parseRedactActions(body: String): Map<String, String> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏动作格式不正确")
    }
    val overrides = root.optJSONObject("actions")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的脱敏动作缺少 actions")
    return overrides.keys().asSequence().mapNotNull { kind ->
        val action = overrides.optString(kind).trim()
        if (action in setOf("mask", "warn", "block")) kind to action else null
    }.toMap()
}

internal fun effectiveAction(detail: RedactDetail, rule: RedactRule): String =
    detail.actions[rule.kind] ?: rule.action

internal fun buildRedactToggleJson(enabled: Boolean): String =
    JSONObject().put("enabled", enabled).toString()

/**
 * 只带 `redactPersonal` 一个键：服务端 applyUIPrefs 是按键合并，
 * 不会像 privacy_mask 那样整表替换，所以不会清掉没回显的字段。
 */
internal fun buildRedactPersonalJson(enabled: Boolean): String =
    JSONObject().put("redactPersonal", enabled).toString()

/** 从 POST /api/settings 的 settingsView 回读个人信息脱敏开关。 */
internal fun parseRedactPersonal(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    if (!root.has("redactPersonal")) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置缺少 redactPersonal")
    }
    return root.optBoolean("redactPersonal", false)
}

internal fun parseRedactEnabled(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏状态格式不正确")
    }
    if (!root.has("enabled")) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏状态缺少 enabled")
    }
    return root.optBoolean("enabled", false)
}

internal fun parseSettings(body: String): SettingsSummary {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    fun stringList(name: String): List<String> {
        val array = root.optJSONArray(name) ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            array.optString(index).trim().ifBlank { null }
        }
    }
    return SettingsSummary(
        addr = root.optString("addr").trim(),
        port = root.optInt("port", 0).coerceAtLeast(0),
        lanEnabled = root.optBoolean("lan", false),
        lanUrls = stringList("lanURLs"),
        proxyMasked = root.optString("proxy").trim(),
        corsOrigins = stringList("corsOrigins"),
        allowIPs = stringList("allowIPs"),
        redactEnabled = root.optBoolean("redact", false),
        redactPersonal = root.optBoolean("redactPersonal", false),
        redactActions = stringList("redactActions"),
        redactRuleCount = root.optJSONArray("redactRules")?.length() ?: 0,
        redactAllowCount = root.optJSONArray("redactAllow")?.length() ?: 0,
        desktopApp = root.optBoolean("desktop_app", false),
        customRules = parseCustomRules(root),
    )
}

private fun parseCustomRules(root: JSONObject): List<RedactCustomRule> {
    val array = root.optJSONArray("redactRules") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val kind = item.optString("kind").trim()
        val prefix = item.optString("prefix").trim()
        val regex = item.optString("regex").trim()
        if (kind.isBlank() || (prefix.isBlank() && regex.isBlank())) null
        else RedactCustomRule(kind, prefix = prefix, regex = regex)
    }
}
