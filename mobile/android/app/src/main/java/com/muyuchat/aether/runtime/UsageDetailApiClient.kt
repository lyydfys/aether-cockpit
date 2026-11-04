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
     * 额度历史（`GET /api/usage/quotas/history?days=45`，`gateway.go:1002` →
     * `usage_quota_api.go:553` → `quota_history.go:317 view(days)`）。
     *
     * `days` 服务端夹在 1..90，默认 45。回包是
     * `[{provider,user,lines:[{name,points:[{at,left,start?,resetsAt?}]}]}]`。
     */
    fun fetchQuotaHistory(status: GatewayStatus, days: Int = 45): List<QuotaHistoryAccount> =
        parseQuotaHistory(get(status, "/api/usage/quotas/history?days=${days.coerceIn(1, 90)}"))

    /**
     * 某个已完成周期的"窗口有多大"估算（`GET /api/usage/quotas/history/holds`，`gateway.go:1003`
     * → `usage_quota_api.go:571` → `quota_history.go:381`）。
     *
     * 四个参数缺一不可（服务端任缺其一就回空数组）；`from`/`to` 必须是 RFC3339。
     */
    fun fetchQuotaHolds(
        status: GatewayStatus,
        provider: String,
        user: String,
        from: String,
        to: String,
    ): List<QuotaHoldAccount> =
        parseQuotaHolds(
            get(
                status,
                "/api/usage/quotas/history/holds?provider=${enc(provider)}" +
                    "&user=${enc(user)}&from=${enc(from)}&to=${enc(to)}",
            ),
        )

    private fun enc(value: String): String =
        java.net.URLEncoder.encode(value, Charsets.UTF_8.name())

    /**
     * 厂商级「立即签到」：`POST /api/usage/{vendor}-checkin`（`gateway.go:1011-1014` →
     * `checkin_api.go:180 handleUsageCheckIn`）。
     *
     * 请求体为空；回包是 `[{user,outcome,credit,streak,msg,again}]`（`checkinPressResult`）。
     * 服务端会把该厂商下**所有可用账号**逐个打一次（`vendorProviders` 里的渠道），
     * 今天已确定的账号直接带旧结果返回、不再请求上游（`:211-223`）。
     */
    fun checkinVendor(status: GatewayStatus, vendor: CheckinVendor): List<VendorCheckinResult> =
        parseVendorCheckins(post(status, vendor.apiPath, "{}"))

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

/* ============================================================================
 * 额度历史 + 周期估算（holds）
 *
 * 端点：
 * - `GET /api/usage/quotas/history?days=45`   `gateway.go:1002` → `usage_quota_api.go:553`
 *   → `quota_history.go:317`：`[{provider,user,lines:[{name,points:[{at,left,…}]}]}]`
 * - `GET /api/usage/quotas/history/holds?provider&user&from&to`  `gateway.go:1003` → `:571`
 *   → `quota_history.go:381`：`[{provider,user,windows:[{name,holdsWhy?|holds?}]}]`
 *
 * `left` 是**剩余百分比**（0..100），不是绝对值——曲线越高越宽裕，这一点决定了纵轴刻度。
 * ========================================================================== */

/** 一个读数点：`at` 是 RFC3339 时间，`left` 是剩余百分比（0..100）。 */
data class QuotaHistoryPoint(
    val at: String,
    val atMs: Long,
    val left: Double,
    val start: String,
    val resetsAt: String,
)

/** 一条窗口曲线（`lines[].name` = 窗口名，如 `5h` / `week`）。 */
data class QuotaHistoryLine(val name: String, val points: List<QuotaHistoryPoint>)

/** 一个账号的曲线集合。 */
data class QuotaHistoryAccount(
    val provider: String,
    val user: String,
    val lines: List<QuotaHistoryLine>,
)

/** holds 里的估算值（`quota_history.go:507-514`）。 */
data class QuotaHoldEstimate(
    val tokens: Long,
    val cost: Double,
    val priced: Boolean,
    val used: Double,
    val routedTokens: Long,
    val routedCalls: Long,
    val cacheRead: Long,
    val since: String,
)

/** holds 里的一个窗口：要么有 `estimate`，要么有 `why`（说不清的原因代码）。 */
data class QuotaHoldWindow(
    val name: String,
    val why: String,
    val estimate: QuotaHoldEstimate?,
)

data class QuotaHoldAccount(
    val provider: String,
    val user: String,
    val windows: List<QuotaHoldWindow>,
)

internal fun parseQuotaHistory(body: String): List<QuotaHistoryAccount> {
    val array = try {
        JSONArray(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的额度历史格式不正确")
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val provider = item.optString("provider").trim()
        val user = item.optString("user").trim()
        if (provider.isEmpty()) {
            return@mapNotNull null
        }
        val lines = ArrayList<QuotaHistoryLine>()
        val lineArray = item.optJSONArray("lines")
        if (lineArray != null) {
            for (i in 0 until lineArray.length()) {
                val line = lineArray.optJSONObject(i) ?: continue
                val name = line.optString("name").trim()
                if (name.isEmpty()) {
                    continue
                }
                val points = ArrayList<QuotaHistoryPoint>()
                val pointArray = line.optJSONArray("points")
                if (pointArray != null) {
                    for (j in 0 until pointArray.length()) {
                        val point = pointArray.optJSONObject(j) ?: continue
                        val at = point.optString("at").trim()
                        val atMs = parseIsoMillis(at)
                        if (atMs <= 0L) {
                            continue
                        }
                        val left = point.optDouble("left", Double.NaN)
                        if (!left.isFinite()) {
                            continue
                        }
                        points.add(
                            QuotaHistoryPoint(
                                at = at,
                                atMs = atMs,
                                left = left.coerceIn(0.0, 100.0),
                                start = point.optString("start").trim(),
                                resetsAt = point.optString("resetsAt").trim(),
                            ),
                        )
                    }
                }
                if (points.isNotEmpty()) {
                    lines.add(QuotaHistoryLine(name = name, points = normalizeQuotaPoints(points)))
                }
            }
        }
        if (lines.isEmpty()) {
            null
        } else {
            QuotaHistoryAccount(
                provider = provider,
                user = user,
                lines = lines.sortedBy { it.name },
            )
        }
    }
}

internal fun parseQuotaHolds(body: String): List<QuotaHoldAccount> {
    val array = try {
        JSONArray(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的额度估算格式不正确")
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val provider = item.optString("provider").trim()
        if (provider.isEmpty()) {
            return@mapNotNull null
        }
        val windows = ArrayList<QuotaHoldWindow>()
        val windowArray = item.optJSONArray("windows")
        if (windowArray != null) {
            for (i in 0 until windowArray.length()) {
                val win = windowArray.optJSONObject(i) ?: continue
                val name = win.optString("name").trim()
                if (name.isEmpty()) {
                    continue
                }
                val holds = win.optJSONObject("holds")
                val estimate = if (holds == null) {
                    null
                } else {
                    val routed = holds.optJSONObject("routed")
                    QuotaHoldEstimate(
                        tokens = holds.optLong("tokens", 0L).coerceAtLeast(0L),
                        cost = holds.optDouble("cost", 0.0).takeIf { it.isFinite() }?.coerceAtLeast(0.0) ?: 0.0,
                        priced = holds.optBoolean("priced", false),
                        used = holds.optDouble("used", 0.0).takeIf { it.isFinite() }?.coerceIn(0.0, 100.0) ?: 0.0,
                        routedTokens = routed?.optLong("tokens", 0L)?.coerceAtLeast(0L) ?: 0L,
                        routedCalls = routed?.optLong("calls", 0L)?.coerceAtLeast(0L) ?: 0L,
                        cacheRead = routed?.optLong("cacheRead", 0L)?.coerceAtLeast(0L) ?: 0L,
                        since = holds.optString("since").trim(),
                    )
                }
                windows.add(
                    QuotaHoldWindow(
                        name = name,
                        why = win.optString("holdsWhy").trim(),
                        estimate = estimate,
                    ),
                )
            }
        }
        if (windows.isEmpty()) {
            null
        } else {
            QuotaHoldAccount(provider = provider, user = item.optString("user").trim(), windows = windows)
        }
    }
}

/**
 * 把曲线点按时间排序、同毫秒去重（后者覆盖前者）——服务端本就按顺序写，
 * 但手机端要拿它算"窗口内掉了多少"，顺序错了结论就反了。
 */
internal fun normalizeQuotaPoints(points: List<QuotaHistoryPoint>): List<QuotaHistoryPoint> {
    if (points.size <= 1) {
        return points
    }
    val byTime = LinkedHashMap<Long, QuotaHistoryPoint>()
    points.sortedBy { it.atMs }.forEach { byTime[it.atMs] = it }
    return byTime.values.toList()
}

/**
 * 窗口内消耗了多少个百分点（首点剩余 − 末点剩余）。
 *
 * 剩余从 90% 掉到 40% → 消耗 50。少于两个点时为 0（谈不上"趋势"）；
 * 中途重置会让差值变小甚至为负，这里按 0 夹住，不显示"消耗了 -30%"。
 */
internal fun quotaConsumedPercent(points: List<QuotaHistoryPoint>): Double {
    val normalized = normalizeQuotaPoints(points)
    if (normalized.size < 2) {
        return 0.0
    }
    val delta = normalized.first().left - normalized.last().left
    return if (delta.isFinite()) delta.coerceAtLeast(0.0) else 0.0
}

/**
 * 从最近一个点推出"这个窗口是哪一段"（holds 的 `from`/`to`）。
 *
 * 服务端只在读数里带了 `start`/`resetsAt` 时才给得出；缺一个就返回 null——
 * 与其编一个周期，不如让 UI 说"缺少窗口起止时间"。
 */
internal fun quotaCycleRange(points: List<QuotaHistoryPoint>): Pair<String, String>? {
    val normalized = normalizeQuotaPoints(points)
    val last = normalized.lastOrNull { it.start.isNotBlank() && it.resetsAt.isNotBlank() } ?: return null
    val fromMs = parseIsoMillis(last.start)
    val toMs = parseIsoMillis(last.resetsAt)
    if (fromMs <= 0L || toMs <= 0L || toMs <= fromMs) {
        return null
    }
    return last.start to last.resetsAt
}

/** RFC3339 → 毫秒；解析不了返回 0（调用方据此丢掉该点）。 */
internal fun parseIsoMillis(raw: String): Long {
    val text = raw.trim()
    if (text.isEmpty()) {
        return 0L
    }
    return runCatching { java.time.OffsetDateTime.parse(text).toInstant().toEpochMilli() }
        .recoverCatching { java.time.Instant.parse(text).toEpochMilli() }
        .getOrElse { 0L }
}

/**
 * `holdsWhy` 的人话（`quota_history.go:490-500` 的五个原因代码）。
 *
 * 这些是**诚实的拒绝**：数据不够就明说为什么估不出来，不给一个看着像真的数字。
 */
internal fun quotaHoldsWhyLabel(why: String): String = when (why.trim().lowercase(Locale.US)) {
    "low" -> "已用不足 5%，放大误差太大，不估算"
    "credits" -> "窗口内出现过 credits / extra usage，按百分比放大会失真"
    "log" -> "这段时间的请求日志不全，无法反推"
    "none" -> "这段时间没有经过本网关的调用"
    "clock" -> "缺少窗口起止时间（厂商没给 reset 时间）"
    else -> why.ifBlank { "未说明原因" }
}

/** 额度历史的空态：为什么空 + 下一步。 */
internal fun quotaHistoryEmptyHint(): String =
    "还没有额度读数。网关每次刷新额度时才记一个点（`/api/usage/quotas/refresh`），" +
        "先去渠道页刷新一次额度，这里才会出现曲线。"

/** 账号标题：`provider · user`，缺 user 时只显示 provider。 */
internal fun quotaAccountLabel(provider: String, user: String): String {
    val p = provider.trim()
    val u = user.trim()
    return when {
        p.isEmpty() -> u
        u.isEmpty() -> p
        else -> "$p · $u"
    }
}

/* ============================================================================
 * 厂商级「立即签到」（`POST /api/usage/{vendor}-checkin`）
 *
 * 端点：`gateway.go:1011-1014` → `checkin_api.go:180 handleUsageCheckIn(vendor)`
 * 请求体：空（handler 根本不读 body）
 * 回包：`[{user,outcome,credit,streak,msg,again}]`（`checkinPressResult`，`checkin_api.go:170-177`）
 *   `outcome` 取值：`claimed`（本次领到）/ `done`（今天已签）/ `failed`（无结论，稍后重试）
 *   ——`internal/provider/contract.go:188-192`
 * ========================================================================== */

/**
 * 支持的四个厂商。`providers` 与服务端 `vendorProviders`
 * （`checkin_api.go:11-16`）逐项一致：点一次会打到该厂商下**所有**可用账号。
 */
/** 渠道签到厂商。
 *  `public`：`UsageViewModel.checkinVendor` 与 `UsageDetailApiClient.checkinVendor` 是公开成员，
 *  暴露 internal 类型会触发 Kotlin 的「public ... exposes its internal ...」编译错误。 */
enum class CheckinVendor(
    val apiPath: String,
    val label: String,
    val providers: List<String>,
) {
    TRAE("/api/usage/trae-checkin", "Trae", listOf("trae-cn", "trae-solo-cn")),
    QODER("/api/usage/qoder-checkin", "Qoder", listOf("qoder-cn", "qoder-intl")),
    MINIMAX("/api/usage/minimax-checkin", "MiniMax", listOf("minimax-code", "minimax-code-global")),
    WORKBUDDY("/api/usage/workbuddy-checkin", "WorkBuddy", listOf("workbuddy-cn")),
}

/** 单账号签到结果（`checkinPressResult`）。 */
data class VendorCheckinResult(
    val user: String,
    val outcome: String,
    val credit: Double,
    val streak: Int,
    val message: String,
    val again: Boolean,
)

internal fun parseVendorCheckins(body: String): List<VendorCheckinResult> {
    val array = try {
        JSONArray(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "签到接口返回的数据格式不正确")
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val user = item.optString("user").trim()
        VendorCheckinResult(
            user = user.ifBlank { "未命名账号" },
            outcome = item.optString("outcome").trim(),
            credit = item.optDouble("credit", 0.0).takeIf { it.isFinite() } ?: 0.0,
            streak = item.optInt("streak", 0).coerceAtLeast(0),
            message = item.optString("msg").trim().take(300),
            again = item.optBoolean("again", false),
        )
    }
}

/** outcome → 人话（`contract.go:188-192`）。未知值原样回显，不硬翻成"成功"。 */
internal fun checkinOutcomeLabel(outcome: String): String = when (outcome.trim().lowercase(Locale.US)) {
    "claimed" -> "本次领到"
    "done" -> "今天已签"
    "failed" -> "失败"
    else -> outcome.trim().ifBlank { "无结论" }
}

/** 一行结果：`user · 本次领到 +1.5 天（连续 3 天）：msg`。 */
internal fun vendorCheckinLine(result: VendorCheckinResult): String {
    val parts = mutableListOf(result.user, checkinOutcomeLabel(result.outcome))
    if (result.credit > 0.0) {
        parts.add("+" + formatCheckinCredit(result.credit))
    }
    if (result.streak > 0) {
        parts.add("连续 ${result.streak} 天")
    }
    val head = parts.joinToString(" · ")
    return if (result.message.isBlank()) head else "$head：${result.message}"
}

/** 领取数额：整数不带小数点（与界面上其他金额同一习惯）。 */
internal fun formatCheckinCredit(credit: Double): String {
    if (!credit.isFinite() || credit <= 0.0) {
        return "0"
    }
    val text = String.format(Locale.US, "%.2f", credit).trimEnd('0').trimEnd('.')
    return text.ifEmpty { "0" }
}

/**
 * 结论汇总：`2 个账号：本次领到 1 · 今天已签 1 · 失败 0`。
 *
 * 空列表也说清楚：服务端返回空数组 = 这个厂商没有可用账号，不是"全部成功"。
 */
internal fun vendorCheckinSummary(results: List<VendorCheckinResult>): String {
    if (results.isEmpty()) {
        return "没有可签到的账号：这台机器的网关里没有该厂商的可用账号。"
    }
    fun count(outcome: String) = results.count { it.outcome.equals(outcome, ignoreCase = true) }
    return "${results.size} 个账号：本次领到 ${count("claimed")} · " +
        "今天已签 ${count("done")} · 失败 ${count("failed")}"
}
