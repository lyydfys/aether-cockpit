package com.muyuchat.aether.runtime

import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** 允许透传的周期：与 `ledgerRange` 的白名单一致，其余一律回落 today。 */
private val REQUESTS_CSV_PERIODS = setOf("today", "7d", "30d", "all")

/**
 * 请求台账导出（CSV）。
 *
 * 用 `GET /api/usage/requests.csv`：**直接回 CSV 字节**。
 * 不用 `POST /api/usage/requests/export`——它把文件写到服务端 `~/Downloads` 并返回路径，
 * 在手机上那个路径没有意义。
 *
 * 也不做"查看正文"：`/api/usage/requests/content` 目前恒返回 `{found:false}` 的桩，
 * 没有可展示的内容，硬做只会造出一个假功能。
 */
class RequestsCsvApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun fetchCsv(status: GatewayStatus, period: String, failedOnly: Boolean): ByteArray {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val path = buildRequestsCsvPath(period, failedOnly)
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "text/csv, application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", "导出失败（HTTP $code）${detail.take(120)}".trim())
            }
            val bytes = connection.inputStream.use { readAll(it) }
            validateRequestsCsv(bytes)
            return bytes
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readAll(input: InputStream, limit: Int = 16 * 1024 * 1024): ByteArray {
        val buffer = ByteArray(8 * 1024)
        val out = java.io.ByteArrayOutputStream()
        while (true) {
            val read = input.read(buffer)
            if (read < 0) {
                return out.toByteArray()
            }
            out.write(buffer, 0, read)
            if (out.size() > limit) {
                throw ManagementApiException("invalid_response", "导出内容过大")
            }
        }
    }
}

internal fun buildRequestsCsvPath(period: String, failedOnly: Boolean): String {
    val safe = period.trim().lowercase(Locale.US).takeIf { it in REQUESTS_CSV_PERIODS } ?: "today"
    return "/api/usage/requests.csv?period=$safe" + if (failedOnly) "&failed=1" else ""
}

internal fun requestsCsvFileName(nowMs: Long): String {
    val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date(nowMs))
    return "aether-requests-$stamp.csv"
}

/**
 * 导出结果校验。
 *
 * 三条底线：
 * 1. 空内容不落盘（用户会以为"导出成功但文件是空的"）；
 * 2. 服务端出错时回的 JSON 不能被当成 CSV 存下来；
 * 3. 表头必须存在（第一行含 `time` 列），否则说明回来的不是这份台账。
 */
internal fun validateRequestsCsv(bytes: ByteArray) {
    if (bytes.size < 2) {
        throw ManagementApiException("empty_csv", "没有可导出的记录")
    }
    val head = bytes.copyOfRange(0, minOf(bytes.size, 64)).toString(Charsets.UTF_8).trimStart()
    if (head.startsWith("{") || head.startsWith("[")) {
        throw ManagementApiException("invalid_response", "服务端返回的是错误信息而不是 CSV")
    }
    val firstLine = String(bytes, Charsets.UTF_8).lineSequence().firstOrNull().orEmpty()
    if (!firstLine.contains("time")) {
        throw ManagementApiException("invalid_response", "导出的 CSV 缺少表头")
    }
}

/** CSV 的数据行数（不含表头）——用于在页面上如实告诉用户"导出了多少条"。 */
internal fun csvDataRows(bytes: ByteArray): Int {
    val text = String(bytes, Charsets.UTF_8)
    return text.lineSequence().count { it.isNotBlank() }.minus(1).coerceAtLeast(0)
}
