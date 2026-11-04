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
    /** 空 = 不限制模型（服务端语义）；非空是精确白名单。 */
    val models: List<String> = emptyList(),
)

/** `GET /api/caller-keys/models` 的可选模型（含重定向虚拟名）。 */
data class CallerModelOption(
    val id: String,
    val name: String,
    val provider: String,
)

data class CallerModelCatalog(
    val models: List<CallerModelOption>,
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

    /** 模型选择器数据；只读，不需要管理员会话之外的额外参数。 */
    fun fetchModels(status: GatewayStatus): CallerModelCatalog =
        parseCallerModelCatalog(request(status, "/api/caller-keys/models", "GET", null))

    /**
     * 只改该 key 的模型白名单。
     *
     * 服务端 `models-key` 的动作是"原子字段更新"：它只把 `models` 写到已有
     * callerKey，不会碰 name/limit/off/secret。所以请求体只携带 key+models；
     * 不能把整条 key 当新对象覆盖（服务端也没有整对象 update 动作）。
     */
    fun saveModels(status: GatewayStatus, key: String, models: List<String>): CallerKeysPayload =
        parseCallerKeys(
            request(
                status,
                "/api/caller-keys/models-key",
                "POST",
                buildCallerKeyModelsJson(key, models),
            ),
        )

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

internal fun buildCallerKeyModelsJson(key: String, models: List<String>): String {
    val array = org.json.JSONArray()
    models.map { it.trim() }.filter { it.isNotBlank() }.distinct().sorted().forEach { array.put(it) }
    return JSONObject()
        .put("key", key)
        .put("models", array)
        .toString()
}

internal fun parseCallerModelCatalog(body: String): CallerModelCatalog {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的模型清单格式不正确")
    }
    val array = root.optJSONArray("models") ?: org.json.JSONArray()
    val models = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isBlank()) return@mapNotNull null
        CallerModelOption(
            id = id,
            name = item.optString("name").trim().ifBlank { id },
            provider = item.optString("provider").trim(),
        )
    }
    return CallerModelCatalog(models)
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
            models = item.optJSONArray("models")?.let { models ->
                (0 until models.length()).mapNotNull { index ->
                    models.optString(index).trim().ifBlank { null }
                }
            } ?: emptyList(),
        )
    }
    val secret = root.optString("secret").trim().ifBlank { null }
    return CallerKeysPayload(keys = keys, secret = secret)
}
