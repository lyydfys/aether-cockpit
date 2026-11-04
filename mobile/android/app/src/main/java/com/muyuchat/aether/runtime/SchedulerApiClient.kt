package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/** 一条签到任务的状态（对应 Go 侧 `scheduler.JobStatus`）。 */
data class SchedulerJob(
    val providerId: String,
    val taskId: String,
    val title: String,
    val cron: String,
    val nextRun: Long,
    val lastRun: Long,
    val lastResult: String,
    val pending: Int,
    val todayDone: Int,
    val todayTotal: Int,
)

/** 调度器运行信息（对应 Go 侧 `scheduler.RuntimeInfo`）。 */
data class SchedulerInfo(
    val jobs: Int,
    val completedToday: Int,
    val uptimeSec: Long,
    val wakeCount: Int,
    val nextFireAt: Long,
)

/**
 * `GET /api/scheduler` 的状态。
 *
 * `enabled=false` 时服务端会给 `reason`（例如「调度器未启动」），页面必须**原样显示**，
 * 不能把它渲染成一个空任务列表——那会让用户以为"今天没有任务"，而不是"调度器没跑"。
 */
data class SchedulerState(
    val enabled: Boolean,
    val reason: String?,
    val info: SchedulerInfo?,
    val jobs: List<SchedulerJob>,
)

/** `POST /api/scheduler/run` 的结果：**入队**语义，不是"已完成"。 */
data class SchedulerRunResult(val queued: Int, val skipped: Int, val note: String)

/**
 * 签到调度（Stage 6 最后一块 UI）。
 *
 * 契约（已核到 `internal/core/gateway/scheduler_api.go`）：
 * - `GET /api/scheduler` → `{enabled, reason?, info, jobs}`
 * - `POST /api/scheduler/run` → `{queued, skipped, note}`，**入队即返回**
 *
 * 手机端调度仍是进程内尽力而为（系统省电策略会停掉网关进程），页面文案必须如实说明。
 */
class SchedulerApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun fetchState(status: GatewayStatus): SchedulerState =
        parseSchedulerState(request(status, "GET", "/api/scheduler", null))

    fun runAll(status: GatewayStatus): SchedulerRunResult =
        parseSchedulerRunResult(request(status, "POST", "/api/scheduler/run", "{}"))

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

internal fun parseSchedulerState(body: String): SchedulerState {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的签到状态格式不正确")
    }
    val enabled = root.optBoolean("enabled", false)
    val reason = root.optString("reason").trim().ifEmpty { null }
    val infoJson = root.optJSONObject("info")
    val info = infoJson?.let {
        SchedulerInfo(
            jobs = it.optInt("jobs", 0).coerceAtLeast(0),
            completedToday = it.optInt("completed_today", 0).coerceAtLeast(0),
            uptimeSec = it.optLong("uptime_sec", 0L).coerceAtLeast(0L),
            wakeCount = it.optInt("wake_count", 0).coerceAtLeast(0),
            nextFireAt = it.optLong("next_fire_at", 0L).coerceAtLeast(0L),
        )
    }
    val array = root.optJSONArray("jobs")
    val jobs = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val providerId = item.optString("provider_id").trim()
            if (providerId.isEmpty()) {
                return@mapNotNull null
            }
            SchedulerJob(
                providerId = providerId,
                taskId = item.optString("task_id").trim(),
                title = item.optString("title").trim().ifBlank { providerId },
                cron = item.optString("cron").trim(),
                nextRun = item.optLong("next_run", 0L),
                lastRun = item.optLong("last_run", 0L),
                lastResult = item.optString("last_result").trim(),
                pending = item.optInt("pending", 0).coerceAtLeast(0),
                todayDone = item.optInt("today_done", 0).coerceAtLeast(0),
                todayTotal = item.optInt("today_total", 0).coerceAtLeast(0),
            )
        }
    }
    return SchedulerState(enabled = enabled, reason = reason, info = info, jobs = jobs)
}

internal fun parseSchedulerRunResult(body: String): SchedulerRunResult {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的签到触发结果格式不正确")
    }
    val queued = root.optInt("queued", 0).coerceAtLeast(0)
    val skipped = root.optInt("skipped", 0).coerceAtLeast(0)
    val note = root.optString("note").trim().ifEmpty {
        // 服务端没给文案时，用**入队**口径兜底：绝不说"已完成"。
        if (queued == 0 && skipped == 0) {
            "当前没有可入队的账号"
        } else {
            "已入队 $queued 个账号" + if (skipped > 0) "；今日已完成 $skipped 个，已跳过" else ""
        }
    }
    return SchedulerRunResult(queued = queued, skipped = skipped, note = note)
}

/**
 * 状态摘要：未接线时原样返回 reason，接线后显示今日完成数。
 * 不用"任务正常"这类含糊说法——要么有数字，要么有原因。
 */
internal fun schedulerSummaryLine(state: SchedulerState): String {
    if (!state.enabled) {
        return state.reason?.ifBlank { null } ?: "调度器未启动"
    }
    val total = state.info?.jobs ?: state.jobs.size
    val done = state.info?.completedToday ?: state.jobs.sumOf { it.todayDone }
    return "今日完成 $done / $total 个任务"
}
