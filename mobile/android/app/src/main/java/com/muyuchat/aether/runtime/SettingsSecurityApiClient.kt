package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 设置里的「局域网 / CORS / IP 白名单」只读视图。
 *
 * **为什么只读**：移动运行时在 `Start` 里只绑 127.0.0.1，非回环直接返回
 * `listen_host_must_be_loopback`（见 `internal/mobile/runtime.go`）。所以手机端把 `lan`
 * 打开**不会**真的产生局域网监听——那不是"少个开关"，而是开关本身就是假的。
 * 桌面端改这些字段才有意义。
 */
data class SettingsSecurity(
    val lan: Boolean,
    val lanAddr: String,
    val corsOrigins: List<String>,
    val allowIps: List<String>,
)

class SettingsSecurityApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    fun fetch(status: GatewayStatus): SettingsSecurity =
        parseSettingsSecurity(get(status, "/api/settings"))

    private fun get(status: GatewayStatus, path: String): String {
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

internal fun parseSettingsSecurity(body: String): SettingsSecurity {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    // 真机上这几个字段嵌在 settings 子对象里还是顶层，取决于 handler 的展开方式——
    // 两种都认，缺了就按"未设置"处理，不猜。
    val node = root.optJSONObject("settings") ?: root
    return SettingsSecurity(
        lan = node.optBoolean("lan", false) || root.optBoolean("lan", false),
        lanAddr = node.optString("lan_addr").trim().ifBlank { root.optString("lan_addr").trim() },
        corsOrigins = stringList(node.optJSONArray("cors_origins") ?: root.optJSONArray("cors_origins")),
        allowIps = stringList(node.optJSONArray("allow_ips") ?: root.optJSONArray("allow_ips")),
    )
}

private fun stringList(array: JSONArray?): List<String> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().takeIf { it.isNotEmpty() }
    }
}

/**
 * 逐行说明。三条口径：
 * 1. 局域网共享**永远**标「仅桌面端生效」——手机端运行时只绑回环，开了也不会有监听；
 * 2. 空列表写「未设置」，不显示 `[]`；
 * 3. 不回传任何令牌（本模型里根本没有那些字段）。
 */
internal fun settingsSecurityLines(view: SettingsSecurity): List<String> {
    val lanText = if (view.lan) {
        "局域网共享：配置里为开启，但仅桌面端生效（手机端只监听回环）"
    } else {
        "局域网共享：关闭 · 手机端只监听回环，开关在这里不生效"
    }
    val lanAddrLine = if (view.lanAddr.isNotBlank()) {
        "局域网地址：${view.lanAddr} · 仅桌面端生效"
    } else {
        "局域网地址：未设置"
    }
    val corsLine = if (view.corsOrigins.isEmpty()) {
        "CORS 允许来源：未设置"
    } else {
        "CORS 允许来源：${view.corsOrigins.joinToString("、")} · 仅桌面端生效"
    }
    val ipLine = if (view.allowIps.isEmpty()) {
        "IP 白名单：未设置"
    } else {
        "IP 白名单：${view.allowIps.joinToString("、")} · 仅桌面端生效"
    }
    return listOf(lanText, lanAddrLine, corsLine, ipLine)
}
