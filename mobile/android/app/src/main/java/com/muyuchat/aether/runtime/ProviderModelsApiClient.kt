package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 某渠道的模型清单（`POST /api/provider/models {id}`）。
 *
 * 契约（`provider_detect.go`）：
 * - 成功 → `{count, models:["…"], dropped:[…], source:"registry"|"vendor"}`
 * - 400 缺 base/id、502 vendor 连不上
 *
 * 只传 `{"id":…}`：**不带 key**。服务端会从账号池里取 base_url/api_key；
 * 没有 vendor 端点时退回内置清单（`source=registry`），所以离线也能给出结果。
 */
data class ProviderModels(
    val models: List<String>,
    val source: String,
)

class ProviderModelsApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 30_000,
    /** 上游目录刷新更慢（桌面同样给 30s 量级）；单独留一个读超时。 */
    private val refreshReadTimeoutMs: Int = 35_000,
) {
    fun fetchModels(status: GatewayStatus, providerId: String): ProviderModels {
        val id = providerId.trim()
        validateChannelId(id)?.let { code -> throw ManagementApiException(code, "渠道 id 不合法") }
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}${providerModelsPath()}").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            connection.outputStream.use { it.write(buildProviderModelsJson(id).toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "读取模型清单失败（HTTP $code）" }.take(160))
            }
            return parseProviderModels(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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

    /**
     * 从上游刷新某个渠道的模型目录：`POST /api/providers/{id}/models/refresh`
     * （桌面 `channels.js:1056` 的「从上游刷新」按钮）。
     *
     * 回包 `{provider_id, models:N, source, fetched}`；返回 N（>=0）。
     * 400 表示渠道不支持 / allowlist 模式 / 没有可用账号（`provider_catalog.go`
     * 的 handleRefreshProviderModels 把服务端原因原样放在 error 里）。
     */
    fun refreshModels(status: GatewayStatus, providerId: String): Int {
        val id = providerId.trim()
        validateChannelId(id)?.let { code -> throw ManagementApiException(code, "渠道 id 不合法") }
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}${providerModelsRefreshPath(id)}").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        // 上游目录刷新要给上游留时间：与桌面一致放在 30s 量级。
        connection.readTimeout = refreshReadTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            // 桌面同样发 `{}`（channels.js:1056 `api.post(..., {})`）。
            connection.outputStream.use { it.write("{}".toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "刷新模型目录失败（HTTP $code）" }.take(160))
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return runCatching { JSONObject(body.trim()).optInt("models", 0) }.getOrDefault(0)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    /**
     * 保存「暴露的模型 + 倍率」（桌面 `channels.js:1178-1191` 的保存按钮）。
     *
     * 请求体见 [buildProviderModelSelectionJson]；服务端回完整 providers 视图，这里不解析，
     * 由调用方重新拉一次渠道/模型清单（与桌面 `await load()` 同口径）。
     */
    fun saveSelection(
        status: GatewayStatus,
        providerId: String,
        models: List<String>,
        rates: Map<String, Double>,
    ) {
        val id = providerId.trim()
        validateChannelId(id)?.let { code -> throw ManagementApiException(code, "渠道 id 不合法") }
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/provider/save").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val body = buildProviderModelSelectionJson(id, models, rates)
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "保存模型选择失败（HTTP $code）" }.take(160))
            }
            // 回包是完整 providers 视图；读掉即可，避免连接悬挂。
            connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }
}

/** `POST /api/providers/{id}/models/refresh`（桌面 `channels.js:1056`）。 */
internal fun providerModelsRefreshPath(providerId: String): String =
    "/api/providers/" + java.net.URLEncoder.encode(providerId.trim(), Charsets.UTF_8.name()) + "/models/refresh"

/**
 * 保存「暴露的模型 + 倍率」：`POST /api/provider/save {id, models, model_rates}`
 * （桌面 `channels.js:1178-1191`；服务端 `provider_api.go:227-233` → `saveProviderModelSelection`）。
 *
 * - `models` 是**完整的目标暴露集合**（空数组 = 一个都不暴露，桌面「清空」同义）；
 * - `model_rates` 只放用户填了正数的项（服务端 `v > 0` 才收，0/缺省=不展示）。
 */
internal fun buildProviderModelSelectionJson(
    providerId: String,
    models: List<String>,
    rates: Map<String, Double>,
): String {
    val json = JSONObject()
    json.put("id", providerId.trim())
    json.put("models", org.json.JSONArray(models.map { it.trim() }.filter { it.isNotEmpty() }))
    val rateJson = JSONObject()
    rates.forEach { (model, rate) ->
        val key = model.trim()
        if (key.isNotEmpty() && rate > 0.0) {
            rateJson.put(key, rate)
        }
    }
    json.put("model_rates", rateJson)
    return json.toString()
}

internal fun providerModelsPath(): String = "/api/provider/models"

/** 只发 id——**绝不能**把 api_key 拼进 body（服务端自己会从账号池取）。 */
internal fun buildProviderModelsJson(providerId: String): String =
    JSONObject().put("id", providerId).toString()

internal fun parseProviderModels(body: String): ProviderModels {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "渠道返回的模型清单格式不正确")
    }
    val array = root.optJSONArray("models") ?: throw ManagementApiException(
        "invalid_response",
        "渠道返回的模型清单里没有 models 字段",
    )
    val models = ArrayList<String>(array.length())
    for (index in 0 until array.length()) {
        val id = array.optString(index).trim()
        if (id.isNotEmpty()) {
            models.add(id)
        }
    }
    return ProviderModels(models = models, source = root.optString("source").trim())
}

/** 展示用：默认只列前 [limit] 个，其余折成一句"还有 N 个"。 */
internal data class ProviderModelsPreview(val shown: List<String>, val more: Int)

internal fun providerModelsPreview(models: List<String>, limit: Int = 20): ProviderModelsPreview {
    if (models.size <= limit) {
        return ProviderModelsPreview(shown = models, more = 0)
    }
    return ProviderModelsPreview(shown = models.take(limit), more = models.size - limit)
}

/** 结论行：数量 + 来源（registry 说明这是内置清单，不是实时拉取）。 */
internal fun providerModelsSummary(providerModels: ProviderModels): String {
    if (providerModels.models.isEmpty()) {
        return "这个渠道没有可用模型"
    }
    val origin = when (providerModels.source) {
        "registry" -> " · 内置清单"
        "vendor" -> " · 供应商实时"
        else -> ""
    }
    return "${providerModels.models.size} 个模型$origin"
}
