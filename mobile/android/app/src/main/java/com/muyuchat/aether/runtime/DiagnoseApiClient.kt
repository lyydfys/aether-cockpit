package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/** 一条自检结果。`level` 是服务端原值（ok / warn / error，也可能是别的）。 */
data class DiagnoseItem(
    val level: String,
    val title: String,
    val detail: String,
    val suggestion: String,
)

data class DiagnoseReport(
    val ok: Int,
    val warns: Int,
    val errors: Int,
    val summary: String,
    val items: List<DiagnoseItem>,
)

/**
 * 端到端自检（`GET /api/diagnose`）。
 *
 * 已核 `diag.go`：返回体**不含凭据**，只有监听地址、渠道 ID、账号计数与 spec 错误串。
 * 这是"为什么客户端连不上"的排查入口，页面上写成**一次检查**而不是"系统正常"。
 */
class DiagnoseApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun fetchDiagnose(status: GatewayStatus): DiagnoseReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/diagnose").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "自检失败（HTTP $code）" }.take(160))
            }
            return parseDiagnose(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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

internal fun parseDiagnose(body: String): DiagnoseReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的自检结果格式不正确")
    }
    val array = root.optJSONArray("items")
    val items = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            DiagnoseItem(
                level = item.optString("level").trim().ifBlank { "unknown" },
                title = item.optString("title").trim(),
                detail = item.optString("detail").trim(),
                suggestion = item.optString("suggestion").trim(),
            )
        }
    }
    return DiagnoseReport(
        ok = root.optInt("ok", 0).coerceAtLeast(0),
        warns = root.optInt("warns", 0).coerceAtLeast(0),
        errors = root.optInt("errors", 0).coerceAtLeast(0),
        summary = root.optString("summary").trim(),
        items = items,
    )
}

private fun levelRank(level: String): Int = when (level.lowercase()) {
    "error" -> 0
    "warn" -> 1
    "ok" -> 2
    else -> 3
}

/** 展示顺序：error 在最前，其次 warn，再 ok，其余按原顺序垫底。 */
internal fun diagnoseDisplayOrder(report: DiagnoseReport): List<DiagnoseItem> =
    report.items.sortedBy { levelRank(it.level) }

/** 结论行：**过去时**，只说这一次检查看到了什么。 */
internal fun diagnoseSummaryLine(report: DiagnoseReport): String {
    val head = "刚刚检查：通过 ${report.ok} 项"
    val tail = when {
        report.errors > 0 && report.warns > 0 -> " · ${report.errors} 个错误、${report.warns} 个警告需要处理"
        report.errors > 0 -> " · ${report.errors} 个错误需要处理"
        report.warns > 0 -> " · ${report.warns} 个警告"
        else -> " · 无需处理"
    }
    return head + tail
}
