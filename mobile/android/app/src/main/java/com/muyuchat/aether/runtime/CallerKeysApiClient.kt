package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

data class CallerKeySummary(
    val id: String,
    val name: String,
    val masked: String,
    val off: Boolean,
    val usedTokens: Long,
    val usedCost: Double,
)

/** secret 只在创建/轮换/复制那一次响应里出现，界面关闭后即丢弃。 */
data class CallerKeysPayload(
    val keys: List<CallerKeySummary>,
    val secret: String?,
)

class CallerKeysApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetch(status: GatewayStatus): CallerKeysPayload =
        parseCallerKeys(request(status, "/api/caller-keys", "GET", null))

    fun action(
        status: GatewayStatus,
        action: String,
        key: String? = null,
        name: String? = null,
    ): CallerKeysPayload = parseCallerKeys(
        request(status, "/api/caller-keys/$action", "POST", buildCallerKeyActionJson(action, key, name)),
    )

    private fun request(status: GatewayStatus, path: String, method: String, body: String?): String {
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
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { parseSettingsError(it) }
                }.getOrNull()
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

internal fun buildCallerKeyActionJson(
    action: String,
    key: String? = null,
    name: String? = null,
): String {
    val json = JSONObject()
    when (action) {
        "add-key" -> if (!name.isNullOrBlank()) {
            json.put("name", name.trim())
        }
        else -> if (!key.isNullOrBlank()) {
            json.put("key", key)
        }
    }
    return json.toString()
}

internal fun parseCallerKeys(body: String): CallerKeysPayload {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的调用方密钥格式不正确")
    }
    val array = root.optJSONArray("keys")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的调用方密钥缺少 keys")
    val keys = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val used = item.optJSONObject("used")
        CallerKeySummary(
            id = item.optString("id").trim(),
            name = item.optString("name").trim(),
            masked = item.optString("masked").trim(),
            off = item.optBoolean("off", false),
            usedTokens = used?.optLong("tokens", 0L)?.coerceAtLeast(0L) ?: 0L,
            usedCost = used?.optDouble("cost", 0.0)?.coerceAtLeast(0.0) ?: 0.0,
        )
    }
    val secret = root.optString("secret").trim().ifBlank { null }
    return CallerKeysPayload(keys = keys, secret = secret)
}
