package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 模型重定向（`GET /api/model-redirects`）。
 *
 * 契约（`model_redirect.go`）：`{"redirects": {"虚拟名": {"渠道id": "上游模型名"}}}`。
 * 它回答的是"我把某个虚拟模型名在某个渠道上换成哪个上游模型"——排障时最容易被忽略的一层。
 * 只读：改映射要写配置（`POST` 同路径），留在桌面端。
 */
data class ModelRedirect(
    val virtualName: String,
    val targets: List<RedirectTarget>,
)

data class RedirectTarget(val provider: String, val upstream: String)

class ModelRedirectApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun fetchRedirects(status: GatewayStatus): List<ModelRedirect> {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/model-redirects").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                throw ManagementApiException("http_$code", "读取模型重定向失败（HTTP $code）")
            }
            return parseModelRedirects(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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

internal fun parseModelRedirects(body: String): List<ModelRedirect> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "模型重定向返回格式不正确")
    }
    val redirects = root.optJSONObject("redirects") ?: return emptyList()
    val out = ArrayList<ModelRedirect>()
    val virtualNames = redirects.keys()
    while (virtualNames.hasNext()) {
        val virtualName = virtualNames.next().trim()
        if (virtualName.isEmpty()) {
            continue
        }
        val targets = redirects.optJSONObject(virtualName) ?: continue
        val list = ArrayList<RedirectTarget>()
        val providers = targets.keys()
        while (providers.hasNext()) {
            val provider = providers.next().trim()
            val upstream = targets.optString(provider).trim()
            if (provider.isEmpty() || upstream.isEmpty()) {
                continue
            }
            list.add(RedirectTarget(provider = provider, upstream = upstream))
        }
        if (list.isEmpty()) {
            continue
        }
        list.sortBy { it.provider }
        out.add(ModelRedirect(virtualName = virtualName, targets = list))
    }
    out.sortBy { it.virtualName }
    return out
}

/** 一行：`虚拟名 → 渠道/上游（还有 N 个渠道）`。 */
internal fun modelRedirectLine(redirect: ModelRedirect): String {
    val first = redirect.targets.firstOrNull() ?: return "${redirect.virtualName} → 未配置"
    val head = "${redirect.virtualName} → ${first.provider}/${first.upstream}"
    val more = redirect.targets.size - 1
    return if (more > 0) "$head（还有 $more 个渠道）" else head
}

internal fun modelRedirectSummary(redirects: List<ModelRedirect>): String =
    if (redirects.isEmpty()) "没有配置模型重定向" else "共 ${redirects.size} 条重定向"
