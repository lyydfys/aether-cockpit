package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.Locale

/**
 * 账本一行（`/api/usage/requests` 的 `rows[]`）。
 *
 * **只读、只映射安全列**：不保存也不暴露请求/回复正文、caller key、Authorization。
 * 新增字段全部带默认值，既有构造点（测试、`RequestsViewModel`）不用改。
 */
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
    /** 协议种类（`kind`）：`openai_chat` / `anthropic_messages` / `openai_responses` 等。 */
    val kind: String = "",
    /** 请求里写的模型名（`req`），与 `model`（实际服务模型）可能不同。 */
    val requestedModel: String = "",
    val providerId: String = "",
    val accountId: String = "",
    val accountName: String = "",
    /** 客户端标识（`agent` / `agentName`）：codex、claude 等。 */
    val agent: String = "",
    /** 会话标识。桌面详情会展示它；只读展示，不落盘。 */
    val session: String = "",
    val stream: Boolean = false,
    val ttftMs: Long = 0L,
    val cacheRead: Long = 0L,
    val cacheWrite: Long = 0L,
    val reasoningTokens: Long = 0L,
    /** 是否命中价格表（`priced`）；false 时费用显示「未计价」。 */
    val priced: Boolean = false,
    val pricingModel: String = "",
    /** 上游/网关给的错误正文（`err`）。只在详情页原文展示，绝不进列表摘要。 */
    val errorMessage: String = "",
    val tries: Int = 0,
    val handedOn: Boolean = false,
    val retried: Boolean = false,
    val routeId: Long = 0L,
    val fastPath: Boolean = false,
    val items: Int = 0,
    val previousId: Boolean = false,
    val autoContinued: Boolean = false,
    val trimmedItems: Int = 0,
    /** 上游静默截断：模型只看到末尾碎片。 */
    val upstreamTruncated: Boolean = false,
    val estimatedTokens: Long = 0L,
    val purpose: String = "",
) {
    val ok: Boolean get() = status in 200..299
}

/**
 * 逐日序列点，来自 `/api/usage/requests` 的 `series[]`（`ledger.go:387-399`）。
 *
 * `time` 是 `YYYY-MM-DD`（`bucket=hour` 时是 `YYYY-MM-DDTHH`），也是 `chartBy.model`
 * 每个点的 `t`，两边按这个键对表。
 */
data class RequestsSeriesPoint(
    val time: String,
    val label: String,
    val calls: Long,
    val errors: Long,
    val cost: Double,
)

/** `chartBy.model[].points[]` 的一个点（`ledger.go:409-413`）：`t` 日期、`c` 调用数、`m` 成本。 */
data class RequestsModelSeriesPoint(
    val time: String,
    val calls: Long,
    val cost: Double,
)

/**
 * `chartBy.model[]` 的一条模型序列（`ledger.go:404-413`）。
 *
 * 服务端只给调用量前 5 的模型，其余并成一条 `其他`（`ledger.go:599-641`）；
 * 每条 `points` 的天数与 `series[]` 对齐，可以直接按 `time` 对表。
 */
data class RequestsModelSeries(
    val name: String,
    val points: List<RequestsModelSeriesPoint>,
)

/** `/api/usage/requests` 的一页 + 该周期的服务端汇总（汇总不受本地筛选影响，与桌面一致）。 */
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
    val inputTokens: Long = 0L,
    val outputTokens: Long = 0L,
    val cacheRead: Long = 0L,
    val cacheWrite: Long = 0L,
    val reasoningTokens: Long = 0L,
    val unpriced: Long = 0L,
    /** 逐日序列（图表用）；老响应没有这两个字段时是空列表，页面按"没有趋势"处理。 */
    val series: List<RequestsSeriesPoint> = emptyList(),
    val modelSeries: List<RequestsModelSeries> = emptyList(),
)

/** 账本允许的周期白名单：与 `ledgerRange` 一致，其余一律回落 today。 */
private val REQUESTS_PERIODS = setOf("today", "7d", "30d", "all")

/** 不传 period 时的旧行为：只看今天。 */
internal const val REQUESTS_DEFAULT_PERIOD = "today"

/** 服务端 `ledgerPageMax`。桌面 logs.js 一次取 500 行再做本地筛选，这里对齐。 */
internal const val REQUESTS_LIMIT_MAX = 500

/**
 * 只读请求账本。只映射安全列：请求/回复正文、会话正文与 caller key 永不进入本模型。
 */
class RequestsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    /**
     * @param period `today` / `7d` / `30d` / `all`；非法值按 `today` 处理（与旧行为一致）。
     * @param query  服务端关键词过滤（`q=`）；空串表示不过滤。
     */
    fun fetchRequests(
        status: GatewayStatus,
        offset: Int,
        limit: Int,
        failedOnly: Boolean,
        period: String = REQUESTS_DEFAULT_PERIOD,
        query: String = "",
    ): RequestsPage {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val path = buildRequestsPath(offset, limit, failedOnly, period, query)
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

/**
 * 拼 `/api/usage/requests` 的路径与查询串。
 *
 * 参数顺序固定为 period → offset → limit → failed → q：不传新参数时逐字节等于旧实现，
 * 避免动到既有调用方的行为。
 */
internal fun buildRequestsPath(
    offset: Int,
    limit: Int,
    failedOnly: Boolean,
    period: String = REQUESTS_DEFAULT_PERIOD,
    query: String = "",
): String {
    val safePeriod = period.trim().lowercase(Locale.US).takeIf { it in REQUESTS_PERIODS } ?: REQUESTS_DEFAULT_PERIOD
    val out = StringBuilder("/api/usage/requests")
    out.append("?period=").append(safePeriod)
    out.append("&offset=").append(offset.coerceAtLeast(0))
    out.append("&limit=").append(limit.coerceIn(1, REQUESTS_LIMIT_MAX))
    if (failedOnly) {
        out.append("&failed=1")
    }
    val keyword = query.trim()
    if (keyword.isNotEmpty()) {
        out.append("&q=").append(URLEncoder.encode(keyword, "UTF-8"))
    }
    return out.toString()
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
            kind = item.optString("kind").trim(),
            requestedModel = item.optString("req").trim(),
            providerId = item.optString("providerId").trim(),
            accountId = item.optString("accountId").trim(),
            accountName = item.optString("accountName").trim(),
            agent = item.optString("agentName").trim().ifBlank { item.optString("agent").trim() },
            session = item.optString("session").trim(),
            stream = item.optBoolean("stream", false),
            ttftMs = item.optLong("ttft_ms", 0L).coerceAtLeast(0L),
            cacheRead = item.optLong("cache_read", 0L).coerceAtLeast(0L),
            cacheWrite = item.optLong("cache_write", 0L).coerceAtLeast(0L),
            reasoningTokens = item.optLong("reasoning", 0L).coerceAtLeast(0L),
            priced = item.optBoolean("priced", false),
            pricingModel = item.optString("pricing_model").trim(),
            errorMessage = item.optString("err").trim(),
            tries = item.optInt("tries", 0).coerceAtLeast(0),
            handedOn = item.optBoolean("handed_on", false),
            retried = item.optBoolean("retried", false),
            routeId = item.optLong("route_id", 0L).coerceAtLeast(0L),
            fastPath = item.optBoolean("fast_path", false),
            items = item.optInt("items", 0).coerceAtLeast(0),
            previousId = item.optBoolean("previous_id", false),
            autoContinued = item.optBoolean("auto_continued", false),
            trimmedItems = item.optInt("trimmed_items", 0).coerceAtLeast(0),
            upstreamTruncated = item.optBoolean("upstream_truncated", false),
            estimatedTokens = item.optLong("est_tokens", 0L).coerceAtLeast(0L),
            purpose = item.optString("purpose").trim(),
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
        inputTokens = root.optLong("input", 0L).coerceAtLeast(0L),
        outputTokens = root.optLong("output", 0L).coerceAtLeast(0L),
        cacheRead = root.optLong("cache_read", 0L).coerceAtLeast(0L),
        cacheWrite = root.optLong("cache_write", 0L).coerceAtLeast(0L),
        reasoningTokens = root.optLong("reasoning", 0L).coerceAtLeast(0L),
        unpriced = root.optLong("unpriced", 0L).coerceAtLeast(0L),
        series = parseRequestsSeries(root.optJSONArray("series")),
        modelSeries = parseRequestsModelSeries(root.optJSONObject("chartBy")),
    )
}

/** `series[]`：逐日（或逐小时）汇总。字段名见 `ledger.go:387-399`。 */
internal fun parseRequestsSeries(array: JSONArray?): List<RequestsSeriesPoint> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val time = item.optString("time").trim()
        if (time.isEmpty()) {
            return@mapNotNull null
        }
        RequestsSeriesPoint(
            time = time,
            label = item.optString("label").trim().ifBlank { time },
            calls = item.optLong("calls", 0L).coerceAtLeast(0L),
            errors = item.optLong("errors", 0L).coerceAtLeast(0L),
            cost = item.optDouble("cost", 0.0).coerceAtLeast(0.0),
        )
    }
}

/** `chartBy.model[]`：`{model:[{name, points:[{t,c,m}]}]}`（`ledger.go:404-413, 595`）。 */
internal fun parseRequestsModelSeries(chartBy: JSONObject?): List<RequestsModelSeries> {
    val array = chartBy?.optJSONArray("model") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val name = item.optString("name").trim()
        if (name.isEmpty()) {
            return@mapNotNull null
        }
        val points = ArrayList<RequestsModelSeriesPoint>()
        val pointArray = item.optJSONArray("points")
        if (pointArray != null) {
            for (i in 0 until pointArray.length()) {
                val point = pointArray.optJSONObject(i) ?: continue
                val time = point.optString("t").trim()
                if (time.isEmpty()) {
                    continue
                }
                points.add(
                    RequestsModelSeriesPoint(
                        time = time,
                        calls = point.optLong("c", 0L).coerceAtLeast(0L),
                        cost = point.optDouble("m", 0.0).coerceAtLeast(0.0),
                    ),
                )
            }
        }
        RequestsModelSeries(name = name, points = points)
    }
}
