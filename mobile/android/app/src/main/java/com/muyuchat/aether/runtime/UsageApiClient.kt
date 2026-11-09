package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.Locale

/**
 * 用量分布的一行（渠道 / 模型 / 调用方共用同一形状）。
 *
 * 契约逐字段对齐桌面 `usage.js` 的 `rankRow`：名称、失败数、调用数、Token、缓存率、费用。
 * 六个字段全部来自 `GET /api/usage` 的 `providers[] / models[] / agents[]`
 * （`usage.go:628-662`：每行都带 calls/errors/input/output/cache_read/cache_write/cost）。
 */
data class UsageBreakdownRow(
    val id: String,
    val name: String,
    val calls: Long,
    val errors: Long,
    val inputTokens: Long,
    val outputTokens: Long,
    val cacheReadTokens: Long,
    val cacheWriteTokens: Long,
    val cost: Double,
) {
    /** 含缓存的 Token 总量：与桌面 `input + output + cache_read + cache_write` 同口径。 */
    val totalTokens: Long get() = inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens

    val cacheInput: Long get() = inputTokens + cacheReadTokens + cacheWriteTokens
}

data class UsageModelRow(
    val id: String,
    val name: String,
    val calls: Long,
    val inputTokens: Long,
    val outputTokens: Long,
    val cost: Double,
    val errors: Long = 0,
    val cacheReadTokens: Long = 0,
    val cacheWriteTokens: Long = 0,
)

data class UsageDayPoint(
    val label: String,
    val calls: Long,
    val errors: Long,
    val cost: Double,
)

data class UsageProviderRow(
    val id: String,
    val name: String,
    val calls: Long,
    val errors: Long,
    val cost: Double,
    val inputTokens: Long = 0,
    val outputTokens: Long = 0,
    val cacheReadTokens: Long = 0,
    val cacheWriteTokens: Long = 0,
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
    /** `/api/usage` 的 `providers` 数组；概览聚合链路按它汇总 calls/errors/cost。 */
    val providers: List<UsageProviderRow> = emptyList(),
    /** 费用按价格表算不出来的请求数（桌面「未计价 N 次」）。 */
    val unpriced: Long = 0,
    /** 调用方（客户端）维度：桌面用量分布的第三个 tab。 */
    val agents: List<UsageBreakdownRow> = emptyList(),
) {
    /** 含缓存的 Token 总量（桌面 `usage.js:90-91` 的口径）。 */
    val totalTokens: Long get() = inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens
}

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

    /**
     * 清空用量聚合：`DELETE /api/usage`（`usage.go` 的 `handleUsageClear`）。
     *
     * 只清按天汇总的格子；逐条账本（`/api/usage/requests`）和请求日志（`/api/logs`）
     * 是另外两套数据，各自独立清理。
     */
    fun clearUsage(status: GatewayStatus): Int {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/usage").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "DELETE"
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
            return runCatching { JSONObject(body).optInt("cleared", 0) }.getOrDefault(0)
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
                errors = item.optLong("errors", 0L).coerceAtLeast(0L),
                cacheReadTokens = item.optLong("cache_read", 0L).coerceAtLeast(0L),
                cacheWriteTokens = item.optLong("cache_write", 0L).coerceAtLeast(0L),
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
    val providers = root.optJSONArray("providers")
    val providerRows = if (providers == null) {
        emptyList()
    } else {
        (0 until providers.length()).mapNotNull { index ->
            val item = providers.optJSONObject(index) ?: return@mapNotNull null
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                return@mapNotNull null
            }
            UsageProviderRow(
                id = id,
                name = item.optString("name").trim().ifBlank { id },
                calls = item.optLong("calls", 0L).coerceAtLeast(0L),
                errors = item.optLong("errors", 0L).coerceAtLeast(0L),
                cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
                inputTokens = item.optLong("input", 0L).coerceAtLeast(0L),
                outputTokens = item.optLong("output", 0L).coerceAtLeast(0L),
                cacheReadTokens = item.optLong("cache_read", 0L).coerceAtLeast(0L),
                cacheWriteTokens = item.optLong("cache_write", 0L).coerceAtLeast(0L),
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
        providers = providerRows,
        unpriced = root.optLong("unpriced", 0L).coerceAtLeast(0L),
        agents = parseBreakdownRows(root.optJSONArray("agents")),
    )
}

/** `providers[] / models[] / agents[]` 是同一套行结构，统一解析。 */
internal fun parseBreakdownRows(array: org.json.JSONArray?): List<UsageBreakdownRow> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        UsageBreakdownRow(
            id = id,
            name = item.optString("name").trim().ifBlank { id },
            calls = item.optLong("calls", 0L).coerceAtLeast(0L),
            errors = item.optLong("errors", 0L).coerceAtLeast(0L),
            inputTokens = item.optLong("input", 0L).coerceAtLeast(0L),
            outputTokens = item.optLong("output", 0L).coerceAtLeast(0L),
            cacheReadTokens = item.optLong("cache_read", 0L).coerceAtLeast(0L),
            cacheWriteTokens = item.optLong("cache_write", 0L).coerceAtLeast(0L),
            cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
        )
    }
}

/**
 * 金额格式化（与桌面 `format.js:fmtCost` 逐条对齐）：
 * CNY 时先按汇率换算；0 → `$0`；不足 1 分 6 位；不足 1 元 4 位；其余 2 位。
 *
 * 只在「用量」页使用——桌面概览页的金额是固定 USD（`overview.js` 用 `fmtMoney`，不读币种）。
 */
internal fun formatUsageAmount(costUsd: Double, currency: String, usdCny: Double): String {
    val safeCost = if (costUsd.isFinite()) costUsd else 0.0
    val rate = if (usdCny.isFinite() && usdCny > 0) usdCny else 7.2
    val cny = currency.equals("CNY", ignoreCase = true)
    val value = if (cny) safeCost * rate else safeCost
    val symbol = if (cny) "¥" else "$"
    if (value == 0.0) {
        return symbol + "0"
    }
    val text = when {
        kotlin.math.abs(value) < 0.01 -> String.format(Locale.US, "%.6f", value)
        kotlin.math.abs(value) < 1.0 -> String.format(Locale.US, "%.4f", value)
        else -> String.format(Locale.US, "%.2f", value)
    }
    return symbol + text
}
