package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

data class HistoryDay(val day: String, val requests: Int)

/** 一条失败轨迹（已从 routes 里筛出）。`fails` 是上游错误串，不含正文与凭据。 */
data class TraceFailure(
    val timeMs: Long,
    val model: String,
    val agent: String,
    val status: Int,
    val attempts: Int,
    val fails: List<String>,
)

data class HistorySnapshot(
    val days: List<HistoryDay>,
    val failures: List<TraceFailure>,
    val cut: Boolean,
)

/**
 * 「最近异常」只读视图的数据层。
 *
 * 注意：`/api/gateway/unrest` **不是**这个视图的一部分——它是"手动解除某账号的冷却/耗尽"，
 * 是**写操作**（POST {key}），手机端不做（见 `unrest_api.go`）。
 */
class GatewayHistoryApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun fetchHistory(status: GatewayStatus, day: String = ""): HistorySnapshot {
        val path = if (day.isBlank()) "/api/gateway/history" else "/api/gateway/history?day=$day"
        return parseHistoryRoutes(get(status, path))
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
                throw ManagementApiException("http_$code", "读取请求轨迹失败（HTTP $code）")
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

private fun rootOf(body: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "管理接口返回的请求轨迹格式不正确")
}

private fun parseDays(root: JSONObject): List<HistoryDay> {
    val array = root.optJSONArray("days") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val day = item.optString("day").trim()
        if (day.isEmpty()) {
            return@mapNotNull null
        }
        HistoryDay(day = day, requests = item.optInt("requests", 0))
    }
}

internal fun parseHistoryDays(body: String): List<HistoryDay> = parseDays(rootOf(body))

internal fun parseHistoryRoutes(body: String): HistorySnapshot {
    val root = rootOf(body)
    val days = parseDays(root)
    val array: JSONArray? = root.optJSONArray("routes")
    val failures = ArrayList<TraceFailure>()
    if (array != null) {
        for (index in 0 until array.length()) {
            val route = array.optJSONObject(index) ?: continue
            val triesArray = route.optJSONArray("tries")
            val fails = ArrayList<String>()
            var attempts = 0
            if (triesArray != null) {
                attempts = triesArray.length()
                for (t in 0 until triesArray.length()) {
                    val fail = triesArray.optJSONObject(t)?.optString("fail").orEmpty().trim()
                    if (fail.isNotEmpty()) {
                        fails.add(fail.take(120))
                    }
                }
            }
            val status = route.optInt("status", 0)
            if (fails.isEmpty() && status < 400) {
                continue
            }
            failures.add(
                TraceFailure(
                    timeMs = route.optLong("time", 0L),
                    model = route.optString("model").trim(),
                    agent = route.optString("agent").trim(),
                    status = status,
                    attempts = attempts,
                    fails = fails,
                ),
            )
        }
    }
    failures.sortByDescending { it.timeMs }
    return HistorySnapshot(days = days, failures = failures, cut = root.optBoolean("cut", false))
}

/** 结论行：历史口径，不写"当前正常"。 */
internal fun historySummaryLine(snapshot: HistorySnapshot): String {
    if (snapshot.days.isEmpty() && snapshot.failures.isEmpty()) {
        return "最近没有异常 · 本机还没有请求轨迹"
    }
    if (snapshot.failures.isEmpty()) {
        return "最近没有异常 · 轨迹缓冲里没有失败记录"
    }
    val head = "最近 ${snapshot.failures.size} 条失败记录（旧到新逐条可查）"
    return if (snapshot.cut) "$head · 缓冲已满，只保留最近的一部分" else head
}

/** 相对时间：读起来像人话，不显示毫秒时间戳。 */
internal fun relativeTimeLabel(nowMs: Long, thenMs: Long): String {
    if (thenMs <= 0L) {
        return "时间未知"
    }
    val diff = nowMs - thenMs
    if (diff < 60_000) {
        return "刚刚"
    }
    val minutes = diff / 60_000
    if (minutes < 60) {
        return "$minutes 分钟前"
    }
    val hours = minutes / 60
    if (hours < 24) {
        return "$hours 小时前"
    }
    return "${hours / 24} 天前"
}
