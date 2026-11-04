package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** 会话筛选项（服务端 `agents` 映射）。 */
data class AgentOption(val id: String, val label: String)

/**
 * 一条会话。`directory` 只是**末级目录名**，不是桌面全路径——全路径里带用户名，
 * 不该出现在手机上（见 [sessionDirectoryLabel]）。
 */
data class SessionRow(
    val key: String,
    val id: String,
    val agent: String,
    val agentName: String,
    val title: String,
    val directory: String,
    val lastAt: Long,
    val requests: Int,
    val messages: Int,
    val toolCalls: Int,
    val cost: Double,
    /** 会话文件字节数；桌面行右侧显示 `fmtSize(size)`（`sessions.js:200`）。缺失时为 0。 */
    val sizeBytes: Long = 0L,
)

data class SessionList(
    val sessions: List<SessionRow>,
    val agents: List<AgentOption>,
)

/**
 * 会话只读投影。
 *
 * ⚠ 产品口径：会话数据来自各 Agent 写在**用户目录**的会话文件
 * （`internal/core/sessions/scanner.go` 以 `os.UserHomeDir()` 为根扫 `.claude`/`.codex`/`.gemini`）。
 * Android 应用只能读自己的沙箱，所以手机上这个列表**必然是空的**——这不是故障，
 * 而且本页与 Windows 数据没有任何同步通路。页面必须如实这么写。
 *
 * 契约：`GET /api/sessions?agent&q&limit`（管理面，Bearer）→
 * `{"sessions":[sessionUsageView…],"agents":{"<id>":"<名>"}}`。
 */
class SessionsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchSessions(
        status: GatewayStatus,
        agent: String = "",
        query: String = "",
        limit: Int = 200,
    ): SessionList {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val path = "/api/sessions?" + sessionFilterQuery(agent, query, limit)
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
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
            return parseSessions(body)
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

/** 组装查询串；`limit` 夹到服务端接受的 1..1000，空筛选项不发送。 */
internal fun sessionFilterQuery(agent: String, query: String, limit: Int): String {
    val parts = mutableListOf<String>()
    val trimmedAgent = agent.trim()
    if (trimmedAgent.isNotEmpty()) {
        parts += "agent=" + URLEncoder.encode(trimmedAgent, Charsets.UTF_8.name())
    }
    val trimmedQuery = query.trim()
    if (trimmedQuery.isNotEmpty()) {
        parts += "q=" + URLEncoder.encode(trimmedQuery, Charsets.UTF_8.name())
    }
    parts += "limit=" + limit.coerceIn(1, 1000)
    return parts.joinToString("&")
}

/**
 * 只保留末级目录名。
 *
 * 目的是不把桌面全路径（含用户名）带到手机上；而当路径就是家目录本身时，
 * 末级段正好是用户名，所以这种情况必须返回空串。
 */
internal fun sessionDirectoryLabel(cwd: String): String {
    val parts = cwd.replace('\\', '/').split('/').filter { it.isNotBlank() }
    if (parts.size < 2) {
        return ""
    }
    val parent = parts[parts.size - 2]
    if (parent == "Users" || parent == "home") {
        return ""
    }
    return parts.last()
}

internal fun parseSessions(body: String): SessionList {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的会话数据格式不正确")
    }
    val sessions = root.optJSONArray("sessions")
    val rows = if (sessions == null) {
        emptyList()
    } else {
        (0 until sessions.length()).mapNotNull { index ->
            sessions.optJSONObject(index)?.let { parseSessionRow(it) }
        }
    }
    val agentsJson = root.optJSONObject("agents")
    val agents = if (agentsJson == null) {
        emptyList()
    } else {
        agentsJson.keys().asSequence().map { id ->
            AgentOption(id = id, label = agentsJson.optString(id).trim().ifBlank { id })
        }.sortedBy { it.label }.toList()
    }
    return SessionList(sessions = rows, agents = agents)
}

private fun parseSessionRow(item: JSONObject): SessionRow? {
    val key = item.optString("key").trim()
    if (key.isEmpty()) {
        return null
    }
    val agent = item.optString("agent").trim()
    return SessionRow(
        key = key,
        id = item.optString("id").trim(),
        agent = agent,
        agentName = item.optString("name").trim().ifBlank { agent },
        title = item.optString("title").trim(),
        directory = sessionDirectoryLabel(item.optString("cwd")),
        lastAt = item.optLong("last", 0L),
        requests = item.optInt("requests", 0).coerceAtLeast(0),
        messages = item.optInt("messages", 0).coerceAtLeast(0),
        toolCalls = item.optInt("tool_calls", 0).coerceAtLeast(0),
        cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
        sizeBytes = item.optLong("size", 0L).coerceAtLeast(0L),
    )
}
