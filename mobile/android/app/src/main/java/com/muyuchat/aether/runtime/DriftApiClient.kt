package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 配置漂移（`GET /api/drift`）。
 *
 * 契约（`drift_api.go`）：返回体是**以 Agent id 为键的对象**：
 * `{"<agentId>":{"kind":"removed|unwired|created|edited|model","field":"model","want":"…","detail":"…"}}`。
 *
 * **停止条件命中**：`detail` 里拼的是**绝对路径 + 配置行原文**（`driftFromChanges` 直接把
 * `first.Path` 与 `ln.Text` 写进去）。配置文件的一行很可能就是 `api_key = "…"`。
 *
 * 所以这里**只解析 `kind`**：把漂移归成「删除 / 改回官方 / 新建 / 模型改动 / 其他」五类，
 * `detail` 与 `want` 一律不读。手机端只回答"哪几个客户端漂移了、属于哪类"，
 * 详情与修复都在桌面端。
 */
data class DriftEntry(val agentId: String, val kind: String)

data class DriftReport(val entries: List<DriftEntry>)

class DriftApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun fetchDrift(status: GatewayStatus): DriftReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/drift").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                throw ManagementApiException("http_$code", "读取配置漂移失败（HTTP $code）")
            }
            return parseDrift(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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

internal fun parseDrift(body: String): DriftReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "配置漂移返回格式不正确")
    }
    val entries = ArrayList<DriftEntry>()
    val keys = root.keys()
    while (keys.hasNext()) {
        val agentId = keys.next().trim()
        if (agentId.isEmpty()) {
            continue
        }
        val item = root.optJSONObject(agentId) ?: continue
        // 只取 kind；detail / want / field 一律不读（detail 含路径与配置行原文）。
        val kind = item.optString("kind").trim().ifBlank { "edited" }
        entries.add(DriftEntry(agentId = agentId, kind = kind))
    }
    entries.sortBy { it.agentId }
    return DriftReport(entries = entries)
}

/** 漂移类型的用户可读说法。未知类型也照实显示，不静默丢弃。 */
internal fun driftKindLabel(kind: String): String = when (kind.lowercase()) {
    "removed" -> "配置文件已被删除"
    "created" -> "出现新建的配置文件"
    "unwired" -> "网关地址被改回官方"
    "model" -> "模型设置被改动"
    "edited" -> "与上次写入的不一致"
    else -> kind
}

/** 结论行：只报数量；同时说明"详情在桌面端"。 */
internal fun driftSummaryLine(report: DriftReport): String {
    if (report.entries.isEmpty()) {
        return "没有检测到配置漂移"
    }
    return "检测到 ${report.entries.size} 个客户端配置漂移 · 详情与修复请在桌面端"
}
