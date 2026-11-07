package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * `GET /api/providers/workbuddy/identity`：**运行中进程真正在用的**出网身份。
 *
 * 客户端不再镜像 Go 的 UA / 版本 / 间隔常量——那些值一律以这个接口为准，
 * Go 升版本时页面自动跟着变。
 *
 * 接口不可用时要如实降级（网关未启动、旧网关 404 等），**绝不回退本地常量**：
 * 回退就等于又造了一份会过期的镜像，正是这次要修掉的问题。
 */
class WorkBuddyIdentityApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetch(status: GatewayStatus): WorkBuddyIdentityState =
        parseWorkBuddyIdentity(request(status, "/api/providers/workbuddy/identity"))

    private fun request(status: GatewayStatus, path: String): String {
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
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { parseSettingsError(it) }
                }.getOrNull()
                // 404 单独留出 code：旧网关没有这个接口，页面要显示"升级网关"而不是"网关没启动"。
                throw ManagementApiException("http_$code", detail?.take(200) ?: "管理接口返回 $code")
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

    private fun readLimited(reader: Reader, limit: Int = 256 * 1024): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return out.toString()
            }
            out.append(buffer, 0, read)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "出网身份接口响应过大")
            }
        }
    }
}

/**
 * 解析 `{"identity":"cli","ua":"…","product":"SaaS","ide_version":"5.9.3",
 * "cli_version":"2.165.0","stainless":true,"stainless_version":"6.25.0",
 * "pace_ms":350,"machine_headers":false,"available":[…],"saved":"desktop"}`。
 *
 * 必填只有 `identity`：它缺失/为空说明契约没兑现，直接报 invalid_response
 * （宁可不显示，也不拿本地常量冒充）。数值字段缺失按 0/false，UI 显示「未上报」。
 */
internal fun parseWorkBuddyIdentity(body: String): WorkBuddyIdentityState {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "出网身份接口返回的格式不正确")
    }
    val raw = root.optString("identity").trim()
    if (raw.isEmpty()) {
        throw ManagementApiException("invalid_response", "出网身份接口返回缺少 identity")
    }
    return WorkBuddyIdentityState(
        effective = WorkBuddyIdentity.fromWireOrNull(raw),
        effectiveRaw = raw,
        userAgent = root.optString("ua").trim(),
        product = root.optString("product").trim(),
        ideVersion = root.optString("ide_version").trim(),
        cliVersion = root.optString("cli_version").trim(),
        stainless = root.optBoolean("stainless", false),
        stainlessVersion = root.optString("stainless_version").trim(),
        paceMs = root.optInt("pace_ms", 0).coerceAtLeast(0),
        machineHeaders = root.optBoolean("machine_headers", false),
        available = parseWorkBuddyIdentityOptions(root.optJSONArray("available")),
        saved = WorkBuddyIdentity.fromWireOrNull(root.optString("saved")),
    )
}

/** `available` 里的 wire 值；认不出的直接丢弃（不猜新档位）。 */
internal fun parseWorkBuddyIdentityOptions(array: JSONArray?): List<WorkBuddyIdentity> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        WorkBuddyIdentity.fromWireOrNull(array.optString(index))
    }.distinct()
}
