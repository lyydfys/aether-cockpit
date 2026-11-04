package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

data class RoutingMember(
    val id: String,
    val name: String,
    val providerName: String,
    val providerOff: Boolean,
)

/** One selectable member model from the `models` array of GET /api/groups. */
data class RoutingCatalogModel(
    val id: String,
    val name: String,
    val providerName: String,
    val providerOff: Boolean,
)

data class RoutingGroup(
    val id: String,
    val name: String,
    /** Saved member ids; mirrors the group's `members` array and may be longer than [memberInfo]. */
    val members: List<String>,
    val routing: String,
    val routingEffective: String,
    val warnings: List<String>,
    val ready: Boolean,
    val auto: Boolean,
    val disabled: Boolean,
    val memberInfo: List<RoutingMember>,
) {
    val memberCount: Int get() = memberInfo.size
}

/**
 * 一个**组模板**（`templatesView`）：`模板/{kind}`，不是已保存的组。
 * 手机端只读展示——套用模板要写配置，留在桌面端。
 */
data class RoutingTemplate(
    val kind: String,
    val name: String,
    val routing: String,
    val memberCount: Int,
    /** 成员不够时服务端给的"还缺什么"：providers / cheap。 */
    val need: String,
    val of: String,
)

data class RoutingOverview(
    val groups: List<RoutingGroup>,
    val models: List<RoutingCatalogModel>,
    val templates: List<RoutingTemplate> = emptyList(),
) {
    val modelCount: Int get() = models.size
}

data class RouteBinding(
    val provider: String,
    val name: String,
    val upstreamModel: String,
    val ready: Boolean,
    val accounts: Int,
    val available: Int,
)

data class RoutePreview(
    val model: String,
    val bindings: List<RouteBinding>,
)

data class RoutingSavePayload(
    val id: String,
    val from: String,
    val name: String,
    val members: List<String>,
    val routing: String,
)

/** Read-only model routing groups from GET /api/groups. */
class RoutingApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchGroups(status: GatewayStatus): RoutingOverview {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/groups").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(160))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseRouting(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun saveGroup(status: GatewayStatus, payload: RoutingSavePayload): RoutingOverview {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val body = buildSavePayloadJson(payload)
        val connection = (URL("http://${status.addr}/api/groups/save").openConnection() as HttpURLConnection)
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
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            val response = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseRouting(response)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun resolve(status: GatewayStatus, model: String): RoutePreview {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL(resolveUrl(status.addr, model)).openConnection() as HttpURLConnection)
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
            return parseResolve(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
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

internal fun parseRouting(body: String): RoutingOverview {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的路由数据格式不正确")
    }
    val array = root.optJSONArray("groups")
    if (array == null) {
        throw ManagementApiException("invalid_response", "管理接口返回的路由数据格式不正确")
    }
    val groups = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val memberIds = item.optJSONArray("members")
        val members = if (memberIds == null) {
            emptyList()
        } else {
            (0 until memberIds.length()).mapNotNull { i ->
                memberIds.optString(i).trim().ifBlank { null }
            }
        }
        val infos = item.optJSONArray("memberInfo")
        val memberInfo = if (infos == null) {
            emptyList()
        } else {
            (0 until infos.length()).mapNotNull { i ->
                val member = infos.optJSONObject(i) ?: return@mapNotNull null
                RoutingMember(
                    id = member.optString("id").trim(),
                    name = member.optString("name").trim().ifBlank { member.optString("id").trim() },
                    providerName = member.optString("providerName").trim()
                        .ifBlank { member.optString("provider").trim() },
                    providerOff = member.optBoolean("providerOff", false),
                )
            }
        }
        val warnings = item.optJSONArray("warnings")
        RoutingGroup(
            id = item.optString("id").trim(),
            name = item.optString("name").trim().ifBlank { item.optString("id").trim() },
            members = members,
            routing = item.optString("routing").trim(),
            routingEffective = item.optString("routingEffective").trim(),
            warnings = if (warnings == null) {
                emptyList()
            } else {
                (0 until warnings.length()).mapNotNull { i -> warnings.optString(i).trim().ifBlank { null } }
            },
            ready = item.optBoolean("ready", false),
            auto = item.optBoolean("auto", false),
            disabled = item.optBoolean("disabled", false),
            memberInfo = memberInfo,
        )
    }
    return RoutingOverview(
        groups = groups,
        models = parseRoutingCatalog(root.optJSONArray("models")),
        templates = parseRoutingTemplates(root.optJSONArray("templates")),
    )
}

/**
 * `deciders` / `pools` 在服务端是**硬编码空数组**（groups_api.go 里 `[]any{}`），
 * 所以这里没有对应模型——不解析一个永远为空的字段，避免做出"空壳页面"。
 */
private fun parseRoutingTemplates(array: JSONArray?): List<RoutingTemplate> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val row = array.optJSONObject(index) ?: return@mapNotNull null
        val group = row.optJSONObject("group") ?: return@mapNotNull null
        val kind = row.optString("kind").trim().ifBlank { group.optString("id").trim() }
        val name = group.optString("name").trim()
        if (kind.isEmpty() && name.isEmpty()) {
            return@mapNotNull null
        }
        val members = group.optJSONArray("members")
        RoutingTemplate(
            kind = kind,
            name = name.ifBlank { kind },
            routing = group.optString("routing").trim(),
            memberCount = members?.length() ?: 0,
            need = row.optString("need").trim(),
            of = row.optString("of").trim(),
        )
    }
}

/** 一行说明：模板名 + 成员数；成员不够时说明"还缺什么"。 */
internal fun routingTemplateLine(template: RoutingTemplate): String {
    val head = when {
        template.memberCount >= 2 -> "${template.name} · ${template.memberCount} 个成员"
        template.memberCount == 1 -> "${template.name} · 仅 1 个成员"
        else -> "${template.name} · 还没有成员"
    }
    val missing = when (template.need) {
        "providers" -> if (template.of.isBlank()) " · 还需要更多渠道" else " · 仅 ${template.of} 可用"
        "cheap" -> " · 还没有便宜渠道"
        else -> ""
    }
    return head + missing
}

/** Parses the `models` array: `id` is `provider/upstreamModel` (groups_api.go:403-406). */
private fun parseRoutingCatalog(array: JSONArray?): List<RoutingCatalogModel> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        RoutingCatalogModel(
            id = id,
            name = item.optString("name").trim().ifBlank { id.substringAfter("/") },
            providerName = item.optString("providerName").trim()
                .ifBlank { item.optString("provider").trim() }
                .ifBlank { id.substringBefore("/") },
            providerOff = item.optBoolean("providerOff", false),
        )
    }
}

/** Saved member ids; falls back to [RoutingGroup.memberInfo] for payloads without `members`. */
internal fun routingGroupMembers(group: RoutingGroup): List<String> =
    group.members.ifEmpty { group.memberInfo.map { it.id } }

/** Query filter for the member picker: id, name and provider name, case-insensitive. */
internal fun filterRoutingCatalog(
    catalog: List<RoutingCatalogModel>,
    query: String,
): List<RoutingCatalogModel> {
    val needle = query.trim().lowercase()
    if (needle.isEmpty()) {
        return catalog
    }
    return catalog.filter { model ->
        model.id.lowercase().contains(needle) ||
            model.name.lowercase().contains(needle) ||
            model.providerName.lowercase().contains(needle)
    }
}

/** Appends [id] once; a repeated add returns the current list unchanged. */
internal fun addRoutingMember(current: List<String>, id: String): List<String> {
    val trimmed = id.trim()
    if (trimmed.isEmpty() || current.contains(trimmed)) {
        return current
    }
    return current + trimmed
}

internal fun removeRoutingMember(current: List<String>, id: String): List<String> {
    val trimmed = id.trim()
    return current.filterNot { it == trimmed }
}

/** Moves [id] one slot up or down; an unknown id or a list edge returns the list unchanged. */
internal fun moveRoutingMember(current: List<String>, id: String, up: Boolean): List<String> {
    val index = current.indexOf(id.trim())
    if (index < 0) {
        return current
    }
    val target = if (up) index - 1 else index + 1
    if (target !in current.indices) {
        return current
    }
    return current.toMutableList().apply {
        val moved = removeAt(index)
        add(target, moved)
    }
}

/**
 * Providers the kernel would turn into fallback channels, in member order.
 *
 * Mirrors `chainFromMembers` (groups_action_api.go:41-53): one provider per distinct `provider/`
 * prefix, so several models of the same provider still count as a single channel. The server also
 * drops providers that are not registered; that part is only visible after a save.
 */
internal fun routingChainProviders(members: List<String>): List<String> {
    val providers = LinkedHashSet<String>()
    for (member in members) {
        val provider = member.trim().substringBefore("/", missingDelimiterValue = "")
        if (provider.isNotEmpty()) {
            providers.add(provider)
        }
    }
    return providers.toList()
}

internal fun routingSaveStrategies(): List<String> =
    listOf("order", "smart", "rotate", "pace", "fast")

internal fun buildSavePayloadJson(payload: RoutingSavePayload): String {
    val json = JSONObject()
    json.put("id", payload.id)
    json.put("from", payload.from)
    json.put("name", payload.name)
    json.put("routing", payload.routing)
    json.put("members", JSONArray(payload.members))
    return json.toString()
}

internal fun resolveUrl(addr: String, model: String): String =
    "http://$addr/api/models/resolve?model=${URLEncoder.encode(model, "UTF-8")}"

internal fun parseResolve(body: String): RoutePreview {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的路由诊断格式不正确")
    }
    val array = root.optJSONArray("bindings")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的路由诊断格式不正确")
    val bindings = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        RouteBinding(
            provider = item.optString("provider").trim(),
            name = item.optString("name").trim().ifBlank { item.optString("provider").trim() },
            upstreamModel = item.optString("upstream_model").trim(),
            ready = item.optBoolean("ready", false),
            accounts = item.optInt("accounts", 0).coerceAtLeast(0),
            available = item.optInt("available", 0).coerceAtLeast(0),
        )
    }
    return RoutePreview(
        model = root.optString("model").trim(),
        bindings = bindings,
    )
}

internal fun routingStrategyLabel(value: String): String = when (value) {
    "order", "manual" -> "按优先级"
    "smart" -> "智能"
    "rotate" -> "轮询"
    "pace", "usage" -> "用量节奏"
    "fast" -> "快速"
    else -> "按优先级"
}
