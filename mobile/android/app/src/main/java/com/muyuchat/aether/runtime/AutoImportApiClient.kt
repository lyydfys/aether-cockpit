package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * 本机客户端自动导入（桌面 `settings.js:163-179` 的「自动导入」块）。
 *
 * 端点（`gateway.go:1018-1021`）：
 * - `GET  /api/importapps/auto`        状态
 * - `POST /api/importapps/auto/scan`   立即扫描
 * - `POST /api/importapps/auto/reset`  清除"已见过"记录
 *
 * **手机端通常不可用**：服务端三个写/读入口都先检查 `platform.WindowsAutoImport`
 * （`autoimport_api.go`），Android 的 `PlatformCapabilities{}` 全 false，所以会回
 * **501 `{code:"platform_unsupported"}`**。页面据此显示"手机端扫不到本机客户端"，
 * 而不是给一个永远为空的按钮。
 */
data class AutoImportStatus(
    val enabled: Boolean,
    val running: Boolean,
    val intervalSec: Int,
    val seen: Int,
    /** unix 秒；0 = 没扫过。 */
    val lastScan: Long,
    val lastAdded: List<String>,
    val lastError: String,
)

class AutoImportApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun fetchStatus(status: GatewayStatus): AutoImportStatus =
        parseRequest(status, "GET", autoImportPath(), null)

    fun scan(status: GatewayStatus): AutoImportStatus =
        parseRequest(status, "POST", autoImportScanPath(), "{}")

    fun reset(status: GatewayStatus): AutoImportStatus =
        parseRequest(status, "POST", autoImportResetPath(), "{}")

    private fun parseRequest(
        status: GatewayStatus,
        method: String,
        path: String,
        body: String?,
    ): AutoImportStatus {
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
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it) }
                }.getOrNull()
                // 501 的 code 是 platform_unsupported：页面据此显示"手机端不适用"。
                throw ManagementApiException(
                    code = detail?.optString("code")?.trim().orEmpty().ifBlank { "http_$code" },
                    message = detail?.optString("error")?.trim().orEmpty()
                        .ifBlank { "自动导入接口返回 $code" }.take(200),
                )
            }
            return parseAutoImportStatus(
                connection.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() },
            )
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }
}

internal fun autoImportPath(): String = "/api/importapps/auto"

internal fun autoImportScanPath(): String = "/api/importapps/auto/scan"

internal fun autoImportResetPath(): String = "/api/importapps/auto/reset"

/** 状态体：`{enabled,running,interval_sec,seen,last_scan,last_added[],last_error}`。 */
internal fun parseAutoImportStatus(body: String): AutoImportStatus {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "自动导入状态返回格式不正确")
    }
    val added = ArrayList<String>()
    root.optJSONArray("last_added")?.let { array ->
        for (i in 0 until array.length()) {
            val name = array.optString(i).trim()
            if (name.isNotEmpty()) {
                added.add(name)
            }
        }
    }
    return AutoImportStatus(
        enabled = root.optBoolean("enabled", false),
        running = root.optBoolean("running", false),
        intervalSec = root.optInt("interval_sec", 0).coerceAtLeast(0),
        seen = root.optInt("seen", 0).coerceAtLeast(0),
        lastScan = root.optLong("last_scan", 0L).coerceAtLeast(0L),
        lastAdded = added,
        lastError = root.optString("last_error").trim(),
    )
}
