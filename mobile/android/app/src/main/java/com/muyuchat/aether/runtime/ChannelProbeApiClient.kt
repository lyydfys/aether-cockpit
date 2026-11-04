package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/** 一次渠道探测样本（服务端 probeAccounts 的结果，只读）。 */
data class ChannelProbe(
    val providerId: String,
    val accountId: String,
    val label: String,
    val model: String,
    val ok: Boolean,
    val code: Int,
    val ms: Long,
    val at: Long,
    val error: String,
)

data class ChannelProbeReport(
    val entries: List<ChannelProbe>,
    val reading: Boolean,
    val checkedAt: Long,
) {
    /**
     * 按渠道聚合，供"每行显示一行结论"。
     *
     * 聚合规则：同一渠道多条样本时取**最差的一条**（有失败就显示失败），
     * 否则多账号渠道里一个失败会被成功样本盖掉。
     */
    fun byProvider(): Map<String, List<ChannelProbe>> = entries
        .groupBy { it.providerId }
        .mapValues { (_, list) -> list.sortedWith(compareBy({ it.ok }, { it.at })).take(1) }
}

/**
 * 渠道连通性探测。
 *
 * 用 `POST /api/channels/check` + `GET /api/channels/status`，**不用** `/api/provider/test`：
 * 后者的请求体是带凭据的 draft，手机端不该为了"测一下"而持有账号凭据。
 * 探测由 Go 侧用账号池里的账号完成，Kotlin 只发起与读结果。
 */
class ChannelProbeApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun fetchStatus(status: GatewayStatus): ChannelProbeReport =
        parseChannelProbeStatus(request(status, "GET", "/api/channels/status", null))

    /** 发起探测（异步）；返回服务端是否已开始（reading=true）。 */
    fun startCheck(status: GatewayStatus, providerId: String): Boolean {
        val body = buildChannelCheckJson(providerId.trim(), "")
        val text = request(status, "POST", "/api/channels/check", body)
        return try {
            JSONObject(text).optBoolean("reading", false)
        } catch (_: JSONException) {
            throw ManagementApiException("invalid_response", "管理接口返回的探测受理结果格式不正确")
        }
    }

    private fun request(status: GatewayStatus, method: String, path: String, body: String?): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        }
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

/** 只带 provider（可选 account）；凭据由服务端从账号池取，手机端不参与。 */
internal fun buildChannelCheckJson(providerId: String, accountId: String): String {
    val json = JSONObject()
    if (providerId.isNotBlank()) {
        json.put("provider", providerId.trim())
    }
    if (accountId.isNotBlank()) {
        json.put("account", accountId.trim())
    }
    return json.toString()
}

internal fun parseChannelProbeStatus(body: String): ChannelProbeReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的渠道探测结果格式不正确")
    }
    val array = root.optJSONArray("entries")
    val entries = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val providerId = item.optString("provider").trim()
            if (providerId.isEmpty()) {
                return@mapNotNull null
            }
            ChannelProbe(
                providerId = providerId,
                accountId = item.optString("account").trim(),
                label = item.optString("label").trim(),
                model = item.optString("model").trim(),
                ok = item.optBoolean("ok", false),
                code = item.optInt("code", 0).coerceAtLeast(0),
                ms = item.optLong("ms", 0L).coerceAtLeast(0L),
                at = item.optLong("at", 0L).coerceAtLeast(0L),
                error = item.optString("error").trim(),
            )
        }
    }
    return ChannelProbeReport(
        entries = entries,
        reading = root.optBoolean("reading", false),
        checkedAt = root.optLong("checkedAt", 0L).coerceAtLeast(0L),
    )
}

/** 一行结论：**只写这次探测的真实结果**，不写"已连接"这种持续状态。 */
internal fun channelProbeSummary(probe: ChannelProbe): String {
    val head = if (probe.ok) "成功" else "失败"
    val parts = mutableListOf(head)
    if (probe.code > 0) {
        parts += probe.code.toString()
    }
    if (probe.ms > 0) {
        parts += "${probe.ms}ms"
    }
    val base = parts.joinToString(" · ")
    if (!probe.ok && probe.error.isNotBlank()) {
        return "$base · ${probe.error.take(80)}"
    }
    return base
}

/** 探测时间：服务端给的是 unix 秒。时钟回拨按"刚刚"，不显示负数。 */
internal fun channelProbeAgeLabel(atSec: Long, nowSec: Long): String {
    if (atSec <= 0L) {
        return "刚刚"
    }
    val delta = nowSec - atSec
    return when {
        delta < 60L -> "刚刚"
        delta < 3_600L -> "${delta / 60L} 分钟前"
        delta < 86_400L -> "${delta / 3_600L} 小时前"
        else -> "${delta / 86_400L} 天前"
    }
}
