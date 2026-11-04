package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URLEncoder
import java.net.URL

/**
 * 会话**内容**检索。
 *
 * 契约（`sessions_api.go` + `sessions/search.go`）：
 * `GET /api/sessions/search?agent=&q=` → `{hits:[{id,before,match,after}], searched, total, cut, unread}`
 *
 * **停止条件命中的是一处口子**：`before/match/after` 是命中位置前后各 48 字符的
 * **会话正文片段**。按「请求/会话正文默认不落手机、也不在手机展示」的口径，
 * 这个 DTO **故意不解析这三个字段**——解析进来就等于给了"顺手渲染正文"的机会。
 *
 * 于是手机端只能回答：**哪些会话里提到过这个词、一共多少处**。够用，且不越界。
 */
data class SessionContentHit(val sessionId: String)

data class SessionContentSearch(
    val hits: List<SessionContentHit>,
    val searched: Int,
    val total: Int,
    val cut: Boolean,
)

class SessionSearchApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun search(status: GatewayStatus, agent: String, query: String): SessionContentSearch {
        val needle = query.trim()
        if (needle.isEmpty()) {
            throw ManagementApiException("query_empty", "请输入要搜索的词")
        }
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val path = buildString {
            append("/api/sessions/search?q=")
            append(URLEncoder.encode(needle, "UTF-8"))
            if (agent.isNotBlank()) {
                append("&agent=")
                append(URLEncoder.encode(agent, "UTF-8"))
            }
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
                throw ManagementApiException("http_$code", "会话检索失败（HTTP $code）")
            }
            return parseSessionContentSearch(
                connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) },
            )
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

internal fun parseSessionContentSearch(body: String): SessionContentSearch {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "会话检索返回格式不正确")
    }
    val array = root.optJSONArray("hits")
    val hits = ArrayList<SessionContentHit>()
    if (array != null) {
        for (index in 0 until array.length()) {
            val item = array.optJSONObject(index) ?: continue
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                continue
            }
            // 注意：这里**只**取 id。before / match / after 一律不读。
            hits.add(SessionContentHit(sessionId = id))
        }
    }
    return SessionContentSearch(
        hits = hits,
        searched = root.optInt("searched", 0).coerceAtLeast(0),
        total = root.optInt("total", 0).coerceAtLeast(0),
        cut = root.optBoolean("cut", false),
    )
}

/** 结论行：只说"哪些会话提到过、多少处"，绝不贴正文片段。 */
internal fun sessionContentSearchLine(result: SessionContentSearch): String {
    if (result.hits.isEmpty()) {
        return if (result.searched > 0) {
            "在 ${result.searched} 个会话的正文里没找到"
        } else {
            "本机还没有可搜索的会话"
        }
    }
    val head = "在 ${result.searched} 个会话里命中 ${result.hits.size} 个（共 ${result.total} 处）"
    val tail = when {
        result.cut -> " · 已达检索上限，只扫了最近一部分"
        else -> ""
    }
    return head + tail
}

/** 会话 id 只展示短前缀，避免把整串 id 铺满一行。 */
internal fun shortenSessionId(id: String): String =
    if (id.length <= 8) id else id.take(8) + "…"
