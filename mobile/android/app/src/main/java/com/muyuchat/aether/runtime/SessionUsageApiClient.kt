package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 会话用量分桶（`sessions_usage_summary.go:18-26` `sessionUsageTotals`）。
 *
 * `tokens` 是服务端算好的 **input + cache_read + cache_write + output**（`:50`），
 * 不要自己再相加，否则缓存会被重复计。
 */
data class SessionUsageTotals(
    val input: Long,
    val cacheRead: Long,
    val cacheWrite: Long,
    val output: Long,
    val tokens: Long,
    val requests: Long,
    val cost: Double,
)

/** 按日期一行（`sessions_usage_summary.go:28-31`）。 */
data class SessionUsageDay(
    val date: String,
    val totals: SessionUsageTotals,
)

/** 按模型一行（`sessions_usage_summary.go:33-36`）。 */
data class SessionUsageModel(
    val name: String,
    val totals: SessionUsageTotals,
)

/** 按实例（Agent）一行（`sessions_usage_summary.go:38-43`）。 */
data class SessionUsageAgent(
    val id: String,
    val name: String,
    val sessions: Int,
    val totals: SessionUsageTotals,
)

/** `GET /api/sessions/usage` 的整包（`sessions_usage_summary.go:215-223`）。 */
data class SessionUsage(
    val from: String,
    val to: String,
    val days: Int,
    val agent: String,
    val files: Int,
    val sessions: Int,
    val totals: SessionUsageTotals,
    val unpriced: Long,
    val series: List<SessionUsageDay>,
    val models: List<SessionUsageModel>,
    val agents: List<SessionUsageAgent>,
    val lastSync: Long,
)

/**
 * 会话用量客户端。
 *
 * 端点：`GET /api/sessions/usage?days=7&agent=codex`（`gateway.go:1146` →
 * `sessions_usage_summary.go:72-224`）。只读，不碰任何会话文件。
 */
class SessionUsageApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun fetchUsage(status: GatewayStatus, days: Int, agent: String): SessionUsage {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}${sessionUsagePath(days, agent)}").openConnection() as HttpURLConnection)
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
            return parseSessionUsage(body)
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

/**
 * `/api/sessions/usage` 的查询串。
 *
 * 服务端按 `days`（1..90，`sessions_usage_summary.go:82-91`）和 `agent`
 * （`:92` `agentFilter`）过滤；空 agent = 全部实例。
 */
internal fun sessionUsagePath(days: Int, agent: String): String {
    val safeDays = days.coerceIn(1, 90)
    val trimmed = agent.trim()
    return if (trimmed.isEmpty()) {
        "/api/sessions/usage?days=$safeDays"
    } else {
        "/api/sessions/usage?days=$safeDays&agent=${URLEncoder.encode(trimmed, "UTF-8")}"
    }
}

/** 解析 `/api/sessions/usage`；缺 `series/models/agents` 当空数组，坏 JSON 抛 invalid_response。 */
internal fun parseSessionUsage(body: String): SessionUsage {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "会话用量返回的数据格式不正确")
    }
    return SessionUsage(
        from = root.optString("from").trim(),
        to = root.optString("to").trim(),
        days = root.optInt("days", 0).coerceAtLeast(0),
        agent = root.optString("agent").trim(),
        files = root.optInt("files", 0).coerceAtLeast(0),
        sessions = root.optInt("sessions", 0).coerceAtLeast(0),
        totals = parseSessionUsageTotals(root),
        unpriced = root.optLong("unpriced", 0L).coerceAtLeast(0L),
        series = root.optJSONArray("series")?.let { array ->
            (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                val date = item.optString("date").trim()
                if (date.isEmpty()) {
                    return@mapNotNull null
                }
                SessionUsageDay(date = date, totals = parseSessionUsageTotals(item))
            }
        }.orEmpty(),
        models = root.optJSONArray("models")?.let { array ->
            (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                val name = item.optString("name").trim()
                if (name.isEmpty()) {
                    return@mapNotNull null
                }
                SessionUsageModel(name = name, totals = parseSessionUsageTotals(item))
            }
        }.orEmpty(),
        agents = root.optJSONArray("agents")?.let { array ->
            (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                val id = item.optString("id").trim()
                if (id.isEmpty()) {
                    return@mapNotNull null
                }
                SessionUsageAgent(
                    id = id,
                    name = item.optString("name").trim().ifBlank { id },
                    sessions = item.optInt("sessions", 0).coerceAtLeast(0),
                    totals = parseSessionUsageTotals(item),
                )
            }
        }.orEmpty(),
        lastSync = root.optLong("last_sync", 0L).coerceAtLeast(0L),
    )
}

private fun parseSessionUsageTotals(item: JSONObject): SessionUsageTotals = SessionUsageTotals(
    input = item.optLong("input", 0L).coerceAtLeast(0L),
    cacheRead = item.optLong("cache_read", 0L).coerceAtLeast(0L),
    cacheWrite = item.optLong("cache_write", 0L).coerceAtLeast(0L),
    output = item.optLong("output", 0L).coerceAtLeast(0L),
    tokens = item.optLong("tokens", 0L).coerceAtLeast(0L),
    requests = item.optLong("requests", 0L).coerceAtLeast(0L),
    cost = item.optDouble("cost", 0.0).let { if (it.isFinite() && it > 0.0) it else 0.0 },
)
