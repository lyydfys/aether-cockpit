package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.util.Locale

/** 用量热力图上的一天。 */
data class UsageHeatmapDay(
    val date: String,
    val calls: Int,
    val tokens: Long,
    val costUsd: Double,
)

data class UsageHeatmap(
    val days: List<UsageHeatmapDay>,
    val from: String,
    val to: String,
) {
    /**
     * 按"今天往前 N 天"铺满。
     *
     * 服务端只回有记录的天；图上必须补齐，否则日期会跳格、看起来像"连着几天没用"。
     * 补齐出来的天标记 `placeholder=true`，页面上据此显示空底纹而不是假装有数据。
     */
    fun recentDays(count: Int, today: String): List<StripDay> {
        val byDate = days.associateBy { it.date }
        val end = runCatching { LocalDate.parse(today) }.getOrNull() ?: return emptyList()
        return (count - 1 downTo 0).map { back ->
            val date = end.minusDays(back.toLong()).format(DateTimeFormatter.ISO_LOCAL_DATE)
            val hit = byDate[date]
            if (hit == null) {
                StripDay(date = date, calls = 0, tokens = 0, costUsd = 0.0, placeholder = true)
            } else {
                StripDay(hit.date, hit.calls, hit.tokens, hit.costUsd, placeholder = false)
            }
        }
    }
}

data class StripDay(
    val date: String,
    val calls: Int,
    val tokens: Long,
    val costUsd: Double,
    val placeholder: Boolean,
)

data class PriceRow(
    val pattern: String,
    val inputPerM: Double,
    val outputPerM: Double,
    val cachedReadPerM: Double,
    val cachedWritePerM: Double,
    val source: String,
)

data class PricingCatalog(
    val currency: String,
    val usdCny: Double,
    val defaults: List<PriceRow>,
    val overrides: List<PriceRow>,
)

/**
 * 用量细分的只读客户端：热力图 / 价格表。
 *
 * 两个都是纯读接口，且**全部走管理会话**（caller key 读不到）。价格表在这里只读，
 * 编辑留在桌面端：改错单价会直接改掉账本口径，不该在手机上顺手改。
 */
class UsageDetailApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun fetchHeatmap(status: GatewayStatus): UsageHeatmap =
        parseUsageHeatmap(get(status, "/api/usage/heatmap"))

    fun fetchPricing(status: GatewayStatus): PricingCatalog =
        parsePricing(get(status, "/api/pricing"))

    /**
     * 额度窗口（`GET /api/usage/quotas`，桌面概览页的「额度余量」卡读它）。
     *
     * 契约：`[{provider,name,user,windows:[{name,used,amount,limit,resetsAt,pool,family}]}]`
     * （`usage_quota_api.go:33-75`）。`used` 的三种口径由 [quotaUsedPercent] 归一。
     */
    fun fetchQuotas(status: GatewayStatus): List<QuotaGroup> =
        parseQuotas(get(status, "/api/usage/quotas"))

    /**
     * 切换计价币种：桌面 `usage.js:78` → `POST /api/pricing/save {price_currency}`，
     * 回最新价格表（含 currency / usd_cny），页面据此重画所有金额。
     */
    fun saveCurrency(status: GatewayStatus, currency: String): PricingCatalog =
        parsePricing(post(status, "/api/pricing/save", "{\"price_currency\":\"$currency\"}"))

    private fun post(status: GatewayStatus, path: String, body: String): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
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

    private fun get(status: GatewayStatus, path: String): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
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

internal fun parseUsageHeatmap(body: String): UsageHeatmap {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的热力图格式不正确")
    }
    val array = root.optJSONArray("days") ?: JSONArray()
    val days = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val date = item.optString("date").trim()
        if (date.isEmpty()) {
            return@mapNotNull null
        }
        UsageHeatmapDay(
            date = date,
            calls = item.optInt("calls", 0).coerceAtLeast(0),
            tokens = item.optLong("tokens", 0L).coerceAtLeast(0L),
            costUsd = item.optDouble("cost", 0.0).takeIf { it.isFinite() } ?: 0.0,
        )
    }
    return UsageHeatmap(days = days, from = root.optString("from"), to = root.optString("to"))
}

internal fun parsePricing(body: String): PricingCatalog {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的价格表格式不正确")
    }
    val defaults = priceRows(root.optJSONArray("defaults"), sourceFallback = "official")
    val overrides = ArrayList<PriceRow>()
    val overrideObject = root.optJSONObject("overrides")
    if (overrideObject != null) {
        val names = overrideObject.keys().asSequence().toList().sorted()
        for (name in names) {
            val price = overrideObject.optJSONObject(name) ?: continue
            overrides += priceRow(name, price, sourceFallback = "custom")
        }
    }
    return PricingCatalog(
        currency = root.optString("currency").trim().ifBlank { "USD" }.uppercase(Locale.US),
        usdCny = root.optDouble("usd_cny", 0.0).takeIf { it.isFinite() } ?: 0.0,
        defaults = defaults,
        overrides = overrides,
    )
}

private fun priceRows(array: JSONArray?, sourceFallback: String): List<PriceRow> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val pattern = item.optString("pattern").trim()
        if (pattern.isEmpty()) {
            return@mapNotNull null
        }
        val price = item.optJSONObject("price") ?: return@mapNotNull null
        priceRow(pattern, price, item.optString("source").trim().ifBlank { sourceFallback })
    }
}

private fun priceRow(pattern: String, price: JSONObject, sourceFallback: String): PriceRow = PriceRow(
    pattern = pattern,
    inputPerM = price.optDouble("input_per_m", 0.0).takeIf { it.isFinite() } ?: 0.0,
    outputPerM = price.optDouble("output_per_m", 0.0).takeIf { it.isFinite() } ?: 0.0,
    cachedReadPerM = price.optDouble("cached_read_per_m", 0.0).takeIf { it.isFinite() } ?: 0.0,
    cachedWritePerM = price.optDouble("cached_write_per_m", 0.0).takeIf { it.isFinite() } ?: 0.0,
    source = sourceFallback,
)

/**
 * 金额格式化：全项目共用这一个，避免每个页面各写一套小数位。
 *
 * 规则：0 → `$0`；小于 1 分显示 4 位小数（否则小额全变成 0）；其余 2 位。
 * 未知币种不假装是美元，直接写 `金额 币种`。
 */
internal fun formatMoney(value: Double, currency: String): String {
    val safe = if (value.isFinite()) value else 0.0
    val symbol = when (currency.uppercase(Locale.US)) {
        "USD" -> "$"
        "CNY", "RMB" -> "¥"
        else -> ""
    }
    val text = when {
        safe == 0.0 -> "0"
        kotlin.math.abs(safe) < 0.01 -> String.format(Locale.US, "%.4f", safe)
        else -> String.format(Locale.US, "%.2f", safe)
    }
    return if (symbol.isEmpty()) {
        "$text ${currency.uppercase(Locale.US)}".trim()
    } else {
        "$symbol$text"
    }
}

/** token 数：过千用 k，过百万用 M，避免热力图格子被长数字撑破。 */
internal fun formatTokens(tokens: Long): String = when {
    tokens <= 0 -> "0"
    tokens < 1_000 -> tokens.toString()
    tokens < 1_000_000 -> String.format(Locale.US, "%.1fk", tokens / 1_000.0)
    else -> String.format(Locale.US, "%.1fM", tokens / 1_000_000.0)
}

/* ============================================================================
 * 额度窗口（概览页「额度余量」卡）
 * ========================================================================== */

data class QuotaWindow(
    val name: String,
    val used: Double,
    val amount: Long?,
    val limit: Long?,
    val resetsAt: String,
    val pool: String,
    val family: String,
)

data class QuotaGroup(
    val provider: String,
    val name: String,
    val user: String,
    val windows: List<QuotaWindow>,
)

internal fun parseQuotas(body: String): List<QuotaGroup> {
    val array = try {
        JSONArray(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的额度数据格式不正确")
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val provider = item.optString("provider").trim()
        if (provider.isEmpty()) {
            return@mapNotNull null
        }
        val windows = item.optJSONArray("windows")
        val rows = if (windows == null) {
            emptyList()
        } else {
            (0 until windows.length()).mapNotNull { w ->
                val win = windows.optJSONObject(w) ?: return@mapNotNull null
                val name = win.optString("name").trim()
                if (name.isEmpty()) {
                    return@mapNotNull null
                }
                QuotaWindow(
                    name = name,
                    used = win.optDouble("used", 0.0).takeIf { it.isFinite() } ?: 0.0,
                    amount = win.optLong("amount", 0L).takeIf { it > 0L },
                    limit = win.optLong("limit", 0L).takeIf { it > 0L },
                    resetsAt = win.optString("resetsAt").trim(),
                    pool = win.optString("pool").trim(),
                    family = win.optString("family").trim(),
                )
            }
        }
        QuotaGroup(
            provider = provider,
            name = item.optString("name").trim().ifBlank { provider },
            user = item.optString("user").trim(),
            windows = rows,
        )
    }
}

/**
 * 已用百分比（0–100）。后端三种口径并存（桌面 `format.js:usedPct` 同一套规则）：
 * `amount/limit` 优先，其次 `used` 为 0–1 小数或 0–100 百分数。
 */
internal fun quotaUsedPercent(window: QuotaWindow): Double {
    val amount = window.amount
    val limit = window.limit
    if (amount != null && limit != null && limit > 0L) {
        return ((amount.toDouble() / limit.toDouble()) * 100.0).coerceIn(0.0, 100.0)
    }
    val raw = window.used.takeIf { it.isFinite() } ?: 0.0
    return if (raw <= 1.0) (raw * 100.0).coerceIn(0.0, 100.0) else raw.coerceIn(0.0, 100.0)
}

/**
 * 重置时间：后端给 ISO 8601（`unixISO`），显示成本地 `MM/dd HH:mm`（桌面 `fmtReset` 同格式）。
 * 解析不了就原样返回——不编一个时间给自己看。
 */
internal fun formatQuotaReset(raw: String): String {
    val text = raw.trim()
    if (text.isEmpty()) {
        return ""
    }
    val parsed = runCatching { java.time.OffsetDateTime.parse(text) }
        .recoverCatching { java.time.Instant.parse(text).atZone(java.time.ZoneId.systemDefault()).toOffsetDateTime() }
        .recoverCatching {
            java.time.LocalDateTime.parse(text).atZone(java.time.ZoneId.systemDefault()).toOffsetDateTime()
        }
        .getOrNull() ?: return text
    return parsed.atZoneSameInstant(java.time.ZoneId.systemDefault())
        .format(java.time.format.DateTimeFormatter.ofPattern("MM/dd HH:mm", Locale.US))
}
