package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

data class UsageModelRow(
    val id: String,
    val name: String,
    val calls: Long,
    val inputTokens: Long,
    val outputTokens: Long,
    val cost: Double,
)

data class UsageDayPoint(
    val label: String,
    val calls: Long,
    val errors: Long,
    val cost: Double,
)

data class UsageSummary(
    val period: String,
    val calls: Long,
    val errors: Long,
    val inputTokens: Long,
    val outputTokens: Long,
    val cacheReadTokens: Long,
    val cacheWriteTokens: Long,
    val cost: Double,
    val models: List<UsageModelRow>,
    val series: List<UsageDayPoint>,
)

/** Read-only projection of `/api/usage`; no prompts or request bodies are mapped. */
class UsageApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchUsage(status: GatewayStatus, period: String): UsageSummary {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val query = URLEncoder.encode(period, Charsets.UTF_8.name())
        val connection = (URL("http://${status.addr}/api/usage?period=$query").openConnection() as HttpURLConnection)
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
            return parseUsage(body)
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

internal fun parseUsage(body: String): UsageSummary {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的用量数据格式不正确")
    }
    if (!root.has("calls")) {
        throw ManagementApiException("invalid_response", "管理接口返回的用量数据格式不正确")
    }
    val models = root.optJSONArray("models")
    val modelRows = if (models == null) {
        emptyList()
    } else {
        (0 until models.length()).mapNotNull { index ->
            val item = models.optJSONObject(index) ?: return@mapNotNull null
            val id = item.optString("id").trim().ifBlank { item.optString("model").trim() }
            if (id.isEmpty()) {
                return@mapNotNull null
            }
            UsageModelRow(
                id = id,
                name = item.optString("name").trim().ifBlank { id },
                calls = item.optLong("calls", 0L).coerceAtLeast(0L),
                inputTokens = item.optLong("input", 0L).coerceAtLeast(0L),
                outputTokens = item.optLong("output", 0L).coerceAtLeast(0L),
                cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
            )
        }
    }
    val series = root.optJSONArray("series")
    val points = if (series == null) {
        emptyList()
    } else {
        (0 until series.length()).mapNotNull { index ->
            val item = series.optJSONObject(index) ?: return@mapNotNull null
            UsageDayPoint(
                label = item.optString("label").trim(),
                calls = item.optLong("calls", 0L).coerceAtLeast(0L),
                errors = item.optLong("errors", 0L).coerceAtLeast(0L),
                cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
            )
        }
    }
    return UsageSummary(
        period = root.optString("period").trim(),
        calls = root.optLong("calls", 0L).coerceAtLeast(0L),
        errors = root.optLong("errors", 0L).coerceAtLeast(0L),
        inputTokens = root.optLong("input", 0L).coerceAtLeast(0L),
        outputTokens = root.optLong("output", 0L).coerceAtLeast(0L),
        cacheReadTokens = root.optLong("cache_read", 0L).coerceAtLeast(0L),
        cacheWriteTokens = root.optLong("cache_write", 0L).coerceAtLeast(0L),
        cost = root.optDouble("cost", 0.0).coerceAtLeast(0.0),
        models = modelRows,
        series = points,
    )
}
