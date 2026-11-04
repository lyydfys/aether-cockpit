package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 单个会话的用量视图（`sessions_usage_api.go` 的 `sessionUsageView`）。
 *
 * 桌面端在会话详情抽屉里读它：`views/sessions.js:443` `api.get('sessions/one?key=agent:id')`，
 * 渲染合计/输入/缓存/输出/费用/请求数六个 chip（`:445-455`）。
 */
data class SessionUsageView(
    val key: String,
    val id: String,
    val agent: String,
    val name: String,
    val title: String,
    val cwd: String,
    val start: Long,
    val last: Long,
    val input: Long,
    val output: Long,
    val cacheRead: Long,
    val cacheWrite: Long,
    val cost: Double,
    val priced: Boolean,
    val unpriced: Boolean,
    val requests: Int,
    val messages: Int,
    val toolCalls: Int,
    val models: List<SessionModelUsage>,
    val resume: String,
    val wsl: String,
    val deletable: Boolean,
    val gateway: Boolean,
) {
    /** 桌面的「合计 Token」口径：input + cache_read + cache_write + output（`sessions.js:445`）。 */
    val totalTokens: Long get() = input + cacheRead + cacheWrite + output

    /** 桌面「输入」chip 含缓存：input + cache_read + cache_write（`sessions.js:450`）。 */
    val inputWithCache: Long get() = input + cacheRead + cacheWrite
}

/** 会话里用到的单个模型；桌面只取 `model` 字段拼一行（`sessions.js:455`）。 */
data class SessionModelUsage(
    val model: String,
    val input: Long,
    val output: Long,
    val cacheRead: Long,
    val cacheWrite: Long,
    val cost: Double,
)

/** 一条正文片段（`sessions.TranscriptPart`）：`text`/`content`/`body` 三选一 + `role`/`kind` + `at`。 */
data class SessionTranscriptPart(
    val role: String,
    val text: String,
    val at: Long,
)

/**
 * transcript 回包（`GET /api/sessions/transcript?agent=&id=`，`sessions_api.go:203-219`）。
 *
 * `source == "gateway"` 表示网关自己的会话**没有落文件**；桌面据此给不同的空态文案
 * （`sessions.js:462-464`）。`cut` 表示服务端截断过。
 */
data class SessionTranscript(
    val parts: List<SessionTranscriptPart>,
    val source: String,
    val cut: Boolean,
)

/**
 * 会话详情客户端：`/api/sessions/one`、`/api/sessions/transcript`、`/api/sessions/markdown`。
 *
 * 桌面依据 `views/sessions.js:403-478`（详情抽屉）。三个端点都只读本机会话文件。
 * 桌面还有「在终端重开」（`sessions/terminal`）与「删除（可恢复）」（`sessions/delete`）：
 * 前者要桌面终端、后者要移动桌面会话文件，手机上都不做。
 */
class SessionDetailApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 20_000,
) {
    /** `GET /api/sessions/one?key=<agent>:<id>`；404 表示这条会话已经不在索引里。 */
    fun fetchSession(status: GatewayStatus, agent: String, id: String): SessionUsageView {
        guard(status)
        val body = request(status, sessionOnePath(agent, id))
        return parseSessionUsageView(body)
    }

    fun fetchTranscript(status: GatewayStatus, agent: String, id: String): SessionTranscript {
        guard(status)
        val body = request(status, sessionTranscriptPath(agent, id))
        return parseSessionTranscript(body)
    }

    /** `GET /api/sessions/markdown?...`：服务端按 text/markdown 回原文（桌面上是"下载"）。 */
    fun fetchMarkdown(status: GatewayStatus, agent: String, id: String): String {
        guard(status)
        return request(status, sessionMarkdownPath(agent, id))
    }

    private fun guard(status: GatewayStatus) {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
    }

    private fun request(status: GatewayStatus, path: String): String {
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json, text/markdown, text/plain")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            val stream = if (code == HttpURLConnection.HTTP_OK) connection.inputStream else connection.errorStream
            val text = stream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it) }.orEmpty()
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching { JSONObject(text.trim()).optString("error").trim() }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            return text
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 8 * 1024 * 1024): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return out.toString()
            }
            out.append(buffer, 0, read)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "会话内容过大，已停止读取")
            }
        }
    }
}

/** 查询串与桌面逐字一致（`sessions.js:408` / `:443` / `:459`）。 */
internal fun sessionOnePath(agent: String, id: String): String =
    "/api/sessions/one?key=${encodeSessionQuery(agent + ":" + id)}"

internal fun sessionTranscriptPath(agent: String, id: String): String =
    "/api/sessions/transcript?agent=${encodeSessionQuery(agent)}&id=${encodeSessionQuery(id)}"

internal fun sessionMarkdownPath(agent: String, id: String): String =
    "/api/sessions/markdown?agent=${encodeSessionQuery(agent)}&id=${encodeSessionQuery(id)}"

internal fun encodeSessionQuery(value: String): String = URLEncoder.encode(value.trim(), "UTF-8")

internal fun parseSessionUsageView(body: String): SessionUsageView {
    val root = jsonObject(body, "会话用量")
    return SessionUsageView(
        key = root.optString("key").trim(),
        id = root.optString("id").trim(),
        agent = root.optString("agent").trim(),
        name = root.optString("name").trim(),
        title = root.optString("title").trim(),
        cwd = root.optString("cwd").trim(),
        start = root.optLong("start", 0L).coerceAtLeast(0L),
        last = root.optLong("last", 0L).coerceAtLeast(0L),
        input = root.optLong("input", 0L).coerceAtLeast(0L),
        output = root.optLong("output", 0L).coerceAtLeast(0L),
        cacheRead = root.optLong("cache_read", 0L).coerceAtLeast(0L),
        cacheWrite = root.optLong("cache_write", 0L).coerceAtLeast(0L),
        cost = root.optDouble("cost", 0.0).let { if (it.isFinite() && it > 0.0) it else 0.0 },
        priced = root.optBoolean("priced", false),
        unpriced = root.optBoolean("unpriced", false),
        requests = root.optInt("requests", 0).coerceAtLeast(0),
        messages = root.optInt("messages", 0).coerceAtLeast(0),
        toolCalls = root.optInt("tool_calls", 0).coerceAtLeast(0),
        models = root.optJSONArray("models")?.let { array ->
            (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                val model = item.optString("model").trim().ifBlank { item.optString("name").trim() }
                if (model.isEmpty()) {
                    return@mapNotNull null
                }
                SessionModelUsage(
                    model = model,
                    input = item.optLong("input", 0L).coerceAtLeast(0L),
                    output = item.optLong("output", 0L).coerceAtLeast(0L),
                    cacheRead = item.optLong("cache_read", 0L).coerceAtLeast(0L),
                    cacheWrite = item.optLong("cache_write", 0L).coerceAtLeast(0L),
                    cost = item.optDouble("cost", 0.0).let { if (it.isFinite() && it > 0.0) it else 0.0 },
                )
            }
        }.orEmpty(),
        resume = root.optString("resume").trim(),
        wsl = root.optString("wsl").trim(),
        deletable = root.optBoolean("deletable", false),
        gateway = root.optBoolean("gateway", false),
    )
}

internal fun parseSessionTranscript(body: String): SessionTranscript {
    val root = jsonObject(body, "会话正文")
    val parts = root.optJSONArray("parts")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            // 桌面按 text → content → body 依次取（`sessions.js:467`）
            val text = listOf("text", "content", "body")
                .firstNotNullOfOrNull { key -> item.optString(key).takeIf { it.isNotBlank() } }
                .orEmpty()
            if (text.isBlank()) {
                return@mapNotNull null
            }
            SessionTranscriptPart(
                role = item.optString("role").trim().ifBlank { item.optString("kind").trim() }
                    .ifBlank { "记录" },
                text = text,
                at = item.optLong("at", 0L).coerceAtLeast(0L),
            )
        }
    }.orEmpty()
    return SessionTranscript(
        parts = parts,
        source = root.optString("source").trim().ifBlank { "file" },
        cut = root.optBoolean("cut", false),
    )
}

private fun jsonObject(body: String, label: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "$label 返回的数据格式不正确")
}
