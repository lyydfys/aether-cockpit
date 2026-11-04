package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 一条插件（请求改写）规则。页面展示**只有安全字段**：id/kind/name/enabled/note/范围；
 * [raw] 保留服务端原样返回的规则 JSON，编辑时做底稿，避免只回写可见字段把 kind 专有参数清空。
 */
data class PluginRule(
    val id: String,
    val kind: String,
    val name: String,
    val enabled: Boolean,
    val note: String = "",
    val providers: List<String> = emptyList(),
    val models: List<String> = emptyList(),
    val raw: String = "",
)

/** 内置插件说明（只列表，不带任何配置）。 */
data class BuiltinPlugin(
    val id: String,
    val name: String,
    val desc: String,
    val on: Boolean,
)

data class PluginsSnapshot(
    val rules: List<PluginRule>,
    val builtins: List<BuiltinPlugin>,
    val total: Int,
)

/**
 * 插件列表（只读）。
 *
 * 说明两件事：
 * 1. `/api/plugins` 的 `ruleToMap` **不含 env/headers**，所以这里可以安全展示；
 * 2. 供应商自定义图标**没有列表接口**（只有 `POST /api/icons` 与 `GET /api/icons/{name}`），
 *    页面如实写"只能在桌面端管理"，不做假的图标清单。
 */
class PluginsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    fun fetchPlugins(status: GatewayStatus): PluginsSnapshot =
        parsePlugins(get(status, "/api/plugins"))

    /** 启用/停用或改可见字段：用服务端原 JSON 做底稿，只覆盖 [json] 里出现的键。 */
    fun savePlugin(status: GatewayStatus, json: String): PluginRule =
        parseSavedRule(post(status, "/api/plugins", json))

    fun deletePlugin(status: GatewayStatus, id: String) {
        delete(status, "/api/plugins/" + java.net.URLEncoder.encode(id, Charsets.UTF_8.name()))
    }

    private fun get(status: GatewayStatus, path: String): String =
        request(status, "GET", path, null)

    private fun post(status: GatewayStatus, path: String, body: String): String =
        request(status, "POST", path, body)

    private fun delete(status: GatewayStatus, path: String) {
        request(status, "DELETE", path, null)
    }

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
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(160))
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

internal fun parsePlugins(body: String): PluginsSnapshot {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的插件列表格式不正确")
    }
    val rules = parseRuleArray(root.optJSONArray("plugins"))
    val builtins = parseBuiltinArray(root.optJSONArray("builtin"))
    return PluginsSnapshot(
        rules = rules,
        builtins = builtins,
        total = root.optInt("total", rules.size).coerceAtLeast(0),
    )
}

private fun parseRuleArray(array: JSONArray?): List<PluginRule> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        PluginRule(
            id = id,
            kind = item.optString("kind").trim(),
            name = item.optString("name").trim().ifBlank { id },
            enabled = item.optBoolean("enabled", false),
            note = item.optString("note").trim(),
            providers = stringList(item.optJSONArray("providers")),
            models = stringList(item.optJSONArray("models")),
            raw = item.toString(),
        )
    }
}

private fun parseBuiltinArray(array: JSONArray?): List<BuiltinPlugin> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        BuiltinPlugin(
            id = id,
            name = item.optString("name").trim().ifBlank { id },
            desc = item.optString("desc").trim(),
            on = item.optBoolean("on", false),
        )
    }
}

private fun stringList(array: JSONArray?): List<String> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().takeIf { it.isNotEmpty() }
    }
}

/** 一行结论：启用状态 + 生效范围。没有范围就写"全部渠道"，不留空。 */
internal fun pluginRuleSummary(rule: PluginRule): String {
    val state = if (rule.enabled) "启用" else "已停用"
    val scope = when {
        rule.providers.isNotEmpty() -> "渠道 ${rule.providers.joinToString("、")}"
        rule.models.isNotEmpty() -> "模型 ${rule.models.joinToString("、")}"
        else -> "全部渠道"
    }
    return "$state · $scope"
}

internal fun pluginsSummaryLine(snapshot: PluginsSnapshot): String {
    if (snapshot.rules.isEmpty()) {
        return "本机还没有自定义插件规则"
    }
    val enabled = snapshot.rules.count { it.enabled }
    return "共 ${snapshot.rules.size} 条规则 · 其中 $enabled 条启用"
}

/** `POST /api/plugins` 的回包：`{rule:{…}}`。 */
internal fun parseSavedRule(body: String): PluginRule {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的插件规则格式不正确")
    }
    val item = root.optJSONObject("rule")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的插件规则格式不正确")
    val id = item.optString("id").trim()
    if (id.isEmpty()) {
        throw ManagementApiException("invalid_response", "管理接口返回的插件规则缺少 id")
    }
    return PluginRule(
        id = id,
        kind = item.optString("kind").trim(),
        name = item.optString("name").trim().ifBlank { id },
        enabled = item.optBoolean("enabled", false),
        note = item.optString("note").trim(),
        providers = stringList(item.optJSONArray("providers")),
        models = stringList(item.optJSONArray("models")),
        raw = item.toString(),
    )
}

/**
 * 用服务端原 JSON 做底稿，只覆盖手机端提供编辑的可见字段。
 * kind 专有字段（temperature/prompt/words/from/to…）原样保留——否则一次"改名保存"就把参数清空。
 */
internal fun pluginRuleWithEdits(
    rule: PluginRule,
    name: String,
    note: String,
    providers: List<String>,
    models: List<String>,
    enabled: Boolean,
): String {
    val json = runCatching { JSONObject(rule.raw) }.getOrElse { JSONObject() }
    json.put("id", rule.id)
    json.put("kind", rule.kind)
    json.put("name", name.trim().ifBlank { rule.id })
    json.put("enabled", enabled)
    if (note.isBlank()) json.remove("note") else json.put("note", note.trim())
    if (providers.isEmpty()) json.remove("providers") else json.put("providers", JSONArray(providers))
    if (models.isEmpty()) json.remove("models") else json.put("models", JSONArray(models))
    return json.toString()
}

/** 只切启停：其余字段一个不丢。 */
internal fun pluginRuleToggleJson(rule: PluginRule, enabled: Boolean): String {
    val json = runCatching { JSONObject(rule.raw) }.getOrElse { JSONObject() }
    json.put("id", rule.id)
    json.put("kind", rule.kind)
    json.put("enabled", enabled)
    return json.toString()
}
