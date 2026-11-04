package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 会话统计（`GET /api/sessions/stats?days=N`）。
 *
 * 契约（`sessionsStatsView`）：`{count, median, p90, days, messages, top:{…}, shape:{…}}`。
 *
 * 这里**只取 5 个数值**：`count / messages / median / p90 / days`。
 * `top` 与 `shape` 里装的是 `sessionUsageView` 列表（可能带标题、目录等会话元信息），
 * 手机端不解析——本轮只要"有多少会话、多少消息、token 分布"，不需要会话级明细。
 */
data class SessionStats(
    val sessions: Int,
    val messages: Int,
    val medianTokens: Long,
    val p90Tokens: Long,
    val days: Int,
)

class SessionStatsApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun fetchStats(status: GatewayStatus, days: Int = 7): SessionStats {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val window = days.coerceIn(1, 90)
        val connection = (URL("http://${status.addr}/api/sessions/stats?days=$window").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                throw ManagementApiException("http_$code", "读取会话统计失败（HTTP $code）")
            }
            return parseSessionStats(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 2 * 1024 * 1024): String {
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

internal fun parseSessionStats(body: String): SessionStats {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "会话统计返回格式不正确")
    }
    return SessionStats(
        sessions = root.optInt("count", 0).coerceAtLeast(0),
        messages = root.optInt("messages", 0).coerceAtLeast(0),
        medianTokens = root.optLong("median", 0L).coerceAtLeast(0L),
        p90Tokens = root.optLong("p90", 0L).coerceAtLeast(0L),
        days = root.optInt("days", 0).coerceAtLeast(0),
    )
}

/** 一行结论：只说计数与分布，不引用任何会话内容。 */
internal fun sessionStatsLine(stats: SessionStats): String {
    if (stats.sessions == 0) {
        // 服务端在没有样本时会回 days=0，别写成"最近 0 天"。
        return if (stats.days > 0) "最近 ${stats.days} 天没有会话记录" else "本机没有会话记录"
    }
    return "最近 ${stats.days} 天：${stats.sessions} 个会话 · ${stats.messages} 条消息"
}

/** 第二行：token 分布（中位数与 P90）；无数据时不编造 0。 */
internal fun sessionStatsTokensLine(stats: SessionStats): String {
    if (stats.sessions == 0) {
        return "没有 token 样本"
    }
    if (stats.medianTokens <= 0L && stats.p90Tokens <= 0L) {
        return "这批会话还没有 token 统计"
    }
    return "每条会话 token：中位数 ${stats.medianTokens} · P90 ${stats.p90Tokens}"
}
