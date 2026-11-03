package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

data class RequestRow(
    val id: String,
    val timeMs: Long,
    val model: String,
    val providerName: String,
    val status: Int,
    val errorType: String,
    val inputTokens: Long,
    val outputTokens: Long,
    val ms: Long,
    val cost: Double,
) {
    val ok: Boolean get() = status in 200..299
}

data class RequestsPage(
    val rows: List<RequestRow>,
    val offset: Int,
    val limit: Int,
    val total: Long,
    val avgMs: Long,
    val p95Ms: Long,
    val calls: Long,
    val errors: Long,
    val cost: Double,
)

/**
 * Read-only request ledger. Only safe columns are mapped: no request/response
 * bodies, session IDs or caller keys ever enter this model.
 */
class RequestsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchRequests(
        status: GatewayStatus,
        offset: Int,
        limit: Int,
        failedOnly: Boolean,
    ): RequestsPage {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val url = "http://${status.addr}/api/usage/requests" +
            "?period=today&offset=${offset.coerceAtLeast(0)}&limit=${limit.coerceIn(1, 100)}" +
            if (failedOnly) "&failed=1" else ""
        val connection = (URL(url).openConnection() as HttpURLConnection)
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
            return parseRequests(body)
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

internal fun parseRequests(body: String): RequestsPage {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的请求账本格式不正确")
    }
    val array = root.optJSONArray("rows")
    if (array == null || !root.has("total")) {
        throw ManagementApiException("invalid_response", "管理接口返回的请求账本格式不正确")
    }
    val rows = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val model = item.optString("model").trim().ifBlank { item.optString("req").trim() }
        RequestRow(
            id = item.optString("id").trim(),
            timeMs = item.optLong("t", 0L).coerceAtLeast(0L),
            model = model.ifBlank { "未记录模型" },
            providerName = item.optString("providerName").trim()
                .ifBlank { item.optString("providerId").trim() },
            status = item.optInt("status", 0),
            errorType = item.optString("err_type").trim(),
            inputTokens = item.optLong("in", 0L).coerceAtLeast(0L),
            outputTokens = item.optLong("out", 0L).coerceAtLeast(0L),
            ms = item.optLong("ms", 0L).coerceAtLeast(0L),
            cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
        )
    }
    return RequestsPage(
        rows = rows,
        offset = root.optInt("offset", 0).coerceAtLeast(0),
        limit = root.optInt("limit", 20).coerceAtLeast(1),
        total = root.optLong("total", 0L).coerceAtLeast(0L),
        avgMs = root.optLong("avg_ms", 0L).coerceAtLeast(0L),
        p95Ms = root.optLong("p95_ms", 0L).coerceAtLeast(0L),
        calls = root.optLong("calls", 0L).coerceAtLeast(0L),
        errors = root.optLong("errors", 0L).coerceAtLeast(0L),
        cost = root.optDouble("cost", 0.0).coerceAtLeast(0.0),
    )
}
