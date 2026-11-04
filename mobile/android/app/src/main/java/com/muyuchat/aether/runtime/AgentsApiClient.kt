package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * Agent 接入（`GET /api/state`、`GET|POST /api/agents/cli`、`GET /api/agent-models/{id}`）。
 *
 * 桌面端 Agent 接入视图（`misc.js:10`）同时读三样东西，这里一一对应：
 * - `GET /api/state` → `agents[]`（`aether_api.go:141-171`，字段见 `:43-56`）与 `profiles[]`（`:59-63`）；
 * - `GET /api/agents/cli` → 本机 CLI 巡检（`agents_cli.go:182-195`，条目见 `:44-53`）；
 * - `GET /api/agent-models/{id}` → 某 agent 的模型清单（`agents_api.go:133-137`，条目见 `:20-27`）。
 *
 * **平台边界**：手机运行时把 `PlatformCapabilities` 传成零值（`internal/mobile/runtime.go:196`），
 * 所以 CLI 巡检会返回 501 + `code=platform_unsupported`（`platform.go:38-43`）。
 * 这里把该状态原样暴露成 [ManagementApiException.code]，页面照着说"仅 Windows"，不伪造空列表。
 *
 * 写入（connect / disconnect / stage / 应用模型）会改**本机 agent 的配置文件**，
 * 手机端不提供，因此本客户端没有任何 POST 写模型的方法。
 */
data class AgentSummary(
    val id: String,
    val name: String,
    /** 配置文件路径。手机端照样显示：它是"这一行对应哪个文件"的唯一说明，且不点可写。 */
    val path: String,
    val detected: Boolean,
    val wired: Boolean,
    /** 当前模型（未接入时为空串）。 */
    val model: String,
    /** 可见模型数（`models.shown`）。 */
    val shownModels: Int,
    /** 清单总数（`models.listed`）。 */
    val listedModels: Int,
)

data class AgentProfile(val id: String, val name: String)

data class AgentsOverview(
    val agents: List<AgentSummary>,
    val profiles: List<AgentProfile>,
)

data class AgentCliInfo(
    val id: String,
    val name: String,
    val installed: Boolean,
    val version: String,
    val installCmd: String,
    val updateCmd: String,
)

data class AgentCliReport(
    val agents: List<AgentCliInfo>,
    val platform: String,
    val checkedAtSec: Long,
)

data class AgentModelOption(
    val id: String,
    val name: String,
    val inUse: Boolean,
    val hidden: Boolean,
)

data class AgentModelsView(
    val agentId: String,
    val models: List<AgentModelOption>,
)

class AgentsApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun fetchOverview(status: GatewayStatus): AgentsOverview =
        parseAgentsOverview(request(status, "/api/state", "GET"))

    fun fetchCli(status: GatewayStatus): AgentCliReport =
        parseAgentCli(request(status, "/api/agents/cli", "GET"))

    /** 清掉服务端 60 秒缓存并重新巡检（`agents_cli.go:239-243`）。 */
    fun refreshCli(status: GatewayStatus): AgentCliReport =
        parseAgentCli(request(status, "/api/agents/cli", "POST", "{}"))

    fun fetchModels(status: GatewayStatus, agentId: String): AgentModelsView {
        val encoded = URLEncoder.encode(agentId, "UTF-8")
        return parseAgentModels(agentId, request(status, "/api/agent-models/$encoded", "GET"))
    }

    private fun request(status: GatewayStatus, path: String, method: String, body: String? = null): String {
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
                // 501 的 {error, code} 是平台边界（仅 Windows），必须原样带到页面。
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(4_096) }
                        ?.let { JSONObject(it) }
                }.getOrNull()
                throw ManagementApiException(
                    code = detail?.optString("code")?.trim().orEmpty().ifBlank { "http_$code" },
                    message = detail?.optString("error")?.trim().orEmpty().ifBlank { "管理接口返回 $code" }.take(200),
                )
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

internal fun parseAgentsOverview(body: String): AgentsOverview {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "Agent 状态返回格式不正确")
    }
    val agents = ArrayList<AgentSummary>()
    val array = root.optJSONArray("agents")
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                continue
            }
            val models = item.optJSONObject("models")
            agents.add(
                AgentSummary(
                    id = id,
                    name = item.optString("name").trim().ifBlank { id },
                    path = item.optString("path").trim(),
                    detected = item.optBoolean("detected", false),
                    wired = item.optBoolean("wired", false),
                    model = agentModelValue(item.optJSONArray("fields")),
                    shownModels = models?.optInt("shown", 0)?.coerceAtLeast(0) ?: 0,
                    listedModels = models?.optInt("listed", 0)?.coerceAtLeast(0) ?: 0,
                ),
            )
        }
    }
    val profiles = ArrayList<AgentProfile>()
    val profileArray = root.optJSONArray("profiles")
    if (profileArray != null) {
        for (i in 0 until profileArray.length()) {
            val item = profileArray.optJSONObject(i) ?: continue
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                continue
            }
            profiles.add(AgentProfile(id = id, name = item.optString("name").trim().ifBlank { id }))
        }
    }
    return AgentsOverview(agents = agents, profiles = profiles)
}

/** `fields[]` 里 key=model 的当前值；没有就返回空串（不猜默认模型）。 */
private fun agentModelValue(fields: JSONArray?): String {
    if (fields == null) {
        return ""
    }
    for (i in 0 until fields.length()) {
        val field = fields.optJSONObject(i) ?: continue
        if (field.optString("key").trim() == "model") {
            return field.optString("value").trim()
        }
    }
    return ""
}

internal fun parseAgentCli(body: String): AgentCliReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "Agent CLI 巡检返回格式不正确")
    }
    val agents = ArrayList<AgentCliInfo>()
    val map = root.optJSONObject("agents")
    if (map != null) {
        val keys = map.keys()
        while (keys.hasNext()) {
            val id = keys.next()
            val item = map.optJSONObject(id) ?: continue
            agents.add(
                AgentCliInfo(
                    id = item.optString("id").trim().ifBlank { id },
                    name = item.optString("name").trim().ifBlank { id },
                    installed = item.optBoolean("installed", false),
                    version = item.optString("version").trim(),
                    installCmd = item.optString("install_cmd").trim(),
                    updateCmd = item.optString("update_cmd").trim(),
                ),
            )
        }
    }
    agents.sortBy { it.name }
    return AgentCliReport(
        agents = agents,
        platform = root.optString("platform").trim(),
        checkedAtSec = root.optLong("checked_at", 0L).coerceAtLeast(0L),
    )
}

internal fun parseAgentModels(agentId: String, body: String): AgentModelsView {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "Agent 模型清单返回格式不正确")
    }
    val models = ArrayList<AgentModelOption>()
    val array = root.optJSONArray("models")
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                continue
            }
            models.add(
                AgentModelOption(
                    id = id,
                    name = item.optString("name").trim().ifBlank { id },
                    inUse = item.optBoolean("inUse", false),
                    hidden = item.optBoolean("hidden", false),
                ),
            )
        }
    }
    return AgentModelsView(agentId = agentId, models = models)
}

/** 状态词：已接入 > 已检测到 > 未检测到。三态都来自服务端，不推断。 */
internal fun agentStatusLabel(agent: AgentSummary): String = when {
    agent.wired -> "已接入"
    agent.detected -> "已检测到"
    else -> "未检测到"
}

/**
 * 桌面清单的两个布尔字段逐项写出来（`misc.js:25` 用 `detected || wired` 过滤）：
 * detected = 本机装了 / wired = 已接入网关。两个都展示，避免"已接入"掩盖"没装"。
 */
internal fun agentPresenceLine(agent: AgentSummary): String {
    val installed = if (agent.detected) "已安装" else "未安装"
    val wired = if (agent.wired) "已接入" else "未接入"
    return "$installed · $wired"
}

/** 一行副文案：当前模型 + 可选模型数；未设置模型时直说。 */
internal fun agentModelLine(agent: AgentSummary): String {
    val head = if (agent.model.isBlank()) "当前模型未设置" else "当前模型 ${agent.model}"
    if (agent.listedModels <= 0) {
        return head
    }
    val shown = agent.shownModels.coerceAtMost(agent.listedModels)
    return "$head · 可选 $shown / 共 ${agent.listedModels}"
}

/** 桌面默认只看"装了或已接入"的条目；这里同一口径（可手动展开全部）。 */
internal fun agentsVisible(all: List<AgentSummary>, showAll: Boolean): List<AgentSummary> =
    if (showAll) all else all.filter { it.detected || it.wired }

/** 被收起的条目数（用于"已隐藏 N 个"提示）。 */
internal fun agentsHiddenCount(all: List<AgentSummary>, showAll: Boolean): Int =
    all.size - agentsVisible(all, showAll).size

/** 空态：为什么空 + 下一步。 */
internal fun agentsEmptyDetail(): String =
    "为什么空：没有任何 agent 被检测到或已接入。agent 的配置文件在桌面上的家目录里" +
        "（Claude Code、Codex CLI 等），手机上不存在这些目录，也不会凭空造一条。" +
        "下一步：在桌面端安装并接入后，到本页刷新查看模型清单；只看清单可以点「显示全部」。"

/** CLI 巡检不可用（501 platform_unsupported）：把服务端原话带出来，不写"空列表"。 */
internal fun agentCliUnsupportedDetail(reason: String): String =
    "为什么不可用：本机 CLI 巡检要扫描 PATH 上的可执行文件并执行 `--version`，" +
        "手机运行时没有这项能力，服务端直接返回拒绝——这不是「一个都没装」。" +
        "服务端原话：${reason.ifBlank { "当前平台无法执行该操作" }}"

/** CLI 巡检已装/未装两组的结论行。 */
internal fun agentCliSummaryLine(report: AgentCliReport): String {
    if (report.agents.isEmpty()) {
        return "巡检结果为空"
    }
    val installed = report.agents.count { it.installed }
    val platform = report.platform.ifBlank { "本机" }
    return "${report.agents.size} 个受巡检 CLI · 已安装 $installed（$platform）"
}

/** 模型清单为空：为什么空 + 下一步。 */
internal fun agentModelsEmptyDetail(): String =
    "为什么空：这个 agent 的模型清单为空或没有读回来。清单来自本机网关的模型目录，" +
        "模型目录为空时会这样。" +
        "下一步：先到「额度」页确认至少有一个可用模型，再回本页重试。"
