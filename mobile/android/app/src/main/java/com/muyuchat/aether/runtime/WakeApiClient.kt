package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 一条唤醒任务。
 *
 * ⚠ `enabled=true` 只表示这条任务**被启用**，不表示它一定会按时跑：移动端的调度是
 * Go 进程内的定时器（`wake.go` 里 30 秒 tick），进程被系统杀掉或进入 Doze 就会停。
 * 页面上必须写「已启用（尽力而为）」，不能写「定时执行」。
 */
data class WakeTask(
    val id: String,
    val accountId: String,
    val providerId: String,
    val label: String,
    val model: String,
    val everyMinutes: Int,
    val maxTokens: Int,
    val enabled: Boolean,
    val createdAt: Long,
    val lastRun: Long,
    val lastModel: String,
    val lastResult: String,
    val lastOk: Boolean,
    val failures: Int,
    val nextRun: Long,
    val running: Boolean,
)

data class WakeList(val tasks: List<WakeTask>, val now: Long)

/**
 * 唤醒队列（自动化）只读投影 + 手动触发。
 *
 * 契约（已核到 `internal/core/gateway/wake.go`）：
 * - `GET /api/wake` → `{"tasks":[wakeTaskView…],"now":<unix 秒>}`
 * - `POST /api/wake/{id}/run` → **202** `{"started":true}`，异步执行；页面只能说"已提交"
 * - `POST /api/wake/run {account_id,model,max_tokens}` → 200 `{ok,result,model,ms,account_id}`，
 *   并发超限 429、缺 `account_id` 400（本页暂不提供这个入口，避免重复职责）
 */
class WakeApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    fun fetchTasks(status: GatewayStatus): WakeList =
        parseWakeTasks(request(status, "GET", "/api/wake", null))

    /** 触发一次。返回服务端是否真的接受了这次触发（202 且 started=true）。 */
    fun runTask(status: GatewayStatus, id: String): Boolean {
        val path = "/api/wake/" + URLEncoder.encode(id, Charsets.UTF_8.name()) + "/run"
        return parseWakeRunAck(request(status, "POST", path, ""))
    }

    private fun request(status: GatewayStatus, method: String, path: String, body: String?): String {
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
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        }
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

internal fun parseWakeTasks(body: String): WakeList {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的唤醒队列格式不正确")
    }
    val array = root.optJSONArray("tasks")
    val tasks = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            array.optJSONObject(index)?.let { parseWakeTask(it) }
        }
    }
    return WakeList(tasks = tasks, now = root.optLong("now", 0L))
}

private fun parseWakeTask(item: JSONObject): WakeTask? {
    val id = item.optString("id").trim()
    if (id.isEmpty()) {
        return null
    }
    return WakeTask(
        id = id,
        accountId = item.optString("account_id").trim(),
        providerId = item.optString("provider_id").trim(),
        label = item.optString("label").trim(),
        model = item.optString("model").trim(),
        everyMinutes = item.optInt("every_minutes", 0).coerceAtLeast(0),
        maxTokens = item.optInt("max_tokens", 0).coerceAtLeast(0),
        enabled = item.optBoolean("enabled", false),
        createdAt = item.optLong("created_at", 0L),
        lastRun = item.optLong("last_run", 0L),
        lastModel = item.optString("last_model").trim(),
        lastResult = item.optString("last_result").trim(),
        lastOk = item.optBoolean("last_ok", false),
        failures = item.optInt("failures", 0).coerceAtLeast(0),
        nextRun = item.optLong("next_run", 0L),
        running = item.optBoolean("running", false),
    )
}

/** 解析 202 的 `{"started":true}`。 */
internal fun parseWakeRunAck(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的触发结果格式不正确")
    }
    return root.optBoolean("started", false)
}

/** 间隔的人类可读形式；缺失或非法如实说"间隔未知"，不替用户猜一个值。 */
internal fun wakeIntervalLabel(minutes: Int): String {
    if (minutes <= 0) {
        return "间隔未知"
    }
    return when {
        minutes < 60 -> "每 $minutes 分钟"
        // 整天的说"每天 / 每 N 天"，比"每 1 天"顺口，也省得用户自己换算。
        minutes % 1_440 == 0 -> if (minutes == 1_440) "每天" else "每 ${minutes / 1_440} 天"
        minutes % 60 == 0 -> "每 ${minutes / 60} 小时"
        else -> "每 $minutes 分钟"
    }
}

/** 下次运行时间；过期显示"即将到期"，没有安排显示"未安排"。 */
internal fun wakeNextRunLabel(nextRun: Long, now: Long): String {
    if (nextRun <= 0L) {
        return "未安排"
    }
    val delta = nextRun - now
    return when {
        delta <= 0L -> "即将到期"
        delta < 3_600L -> "约 ${(delta + 59) / 60} 分钟后"
        delta < 86_400L -> "约 ${delta / 3_600} 小时后"
        else -> "约 ${delta / 86_400} 天后"
    }
}

/** 启用状态的唯一允许文案：不承诺送达。 */
internal fun wakeEnabledLabel(enabled: Boolean): String =
    if (enabled) "已启用（尽力而为）" else "已停用"

/** 上次执行结果；从未执行时如实说，不伪装成功。 */
internal fun wakeLastRunLabel(lastRun: Long, lastOk: Boolean, lastResult: String): String {
    if (lastRun <= 0L) {
        return "从未运行"
    }
    val detail = lastResult.ifBlank { "无详情" }
    return if (lastOk) "上次成功 · $detail" else "上次失败 · $detail"
}
