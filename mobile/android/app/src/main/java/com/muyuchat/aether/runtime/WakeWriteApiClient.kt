package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/** 唤醒任务草稿。服务端按 `account_id` 从账号池取账号，**不需要传凭据**。 */
data class WakeDraft(
    val accountId: String,
    val model: String = "",
    val everyMinutes: Int = 60,
    val maxTokens: Int = 16,
) {
    /** 间隔与 token 上限都按服务端区间先夹紧，避免"填了却被静默改掉"。 */
    fun normalized(): WakeDraft {
        val minutes = everyMinutes.coerceIn(MIN_WAKE_MINUTES, MAX_WAKE_MINUTES)
        val tokens = if (maxTokens <= 0) 16 else maxTokens.coerceAtMost(64)
        return copy(
            accountId = accountId.trim(),
            model = model.trim(),
            everyMinutes = minutes,
            maxTokens = tokens,
        )
    }

    companion object {
        const val MIN_WAKE_MINUTES = 15
        const val MAX_WAKE_MINUTES = 7 * 24 * 60
    }
}

data class WakeDraftProblems(
    val accountId: String? = null,
    val everyMinutes: String? = null,
) {
    val hasProblem: Boolean get() = accountId != null || everyMinutes != null
}

internal fun validateWakeDraft(draft: WakeDraft): WakeDraftProblems {
    val normalized = draft.normalized()
    return WakeDraftProblems(
        accountId = if (draft.accountId.isBlank()) "请选择账号" else null,
        everyMinutes = if (draft.everyMinutes < WakeDraft.MIN_WAKE_MINUTES ||
            draft.everyMinutes > WakeDraft.MAX_WAKE_MINUTES
        ) {
            "间隔需在 15 分钟到 7 天之间"
        } else {
            null
        },
    )
}

internal fun buildWakeCreateJson(rawDraft: WakeDraft): String {
    val draft = rawDraft.normalized()
    val problems = validateWakeDraft(draft)
    if (problems.hasProblem) {
        throw ManagementApiException("invalid_draft", problems.accountId ?: problems.everyMinutes!!)
    }
    return JSONObject()
        .put("account_id", draft.accountId)
        .put("model", draft.model)
        .put("every_minutes", draft.everyMinutes.coerceIn(WakeDraft.MIN_WAKE_MINUTES, WakeDraft.MAX_WAKE_MINUTES))
        .put("max_tokens", draft.maxTokens)
        .toString()
}

// 间隔的人话解释复用 WakeApiClient.wakeIntervalLabel()，不在这里重复定义。

data class CheckinCounts(
    val newSuccess: Int = 0,
    val already: Int = 0,
    val failed: Int = 0,
    val unavailable: Int = 0,
    val notRun: Int = 0,
    val noAccount: Int = 0,
    val pending: Int = 0,
)

data class CheckinFailure(
    val accountId: String,
    val label: String,
    val message: String,
)

data class CheckinItem(
    val providerId: String,
    val providerName: String,
    val title: String,
    val status: String,
    val accounts: Int,
    val newSuccess: Int,
    val failed: Int,
    val failures: List<CheckinFailure> = emptyList(),
)

data class CheckinSummary(
    val generatedAt: Long = 0,
    val since: Long = 0,
    val counts: CheckinCounts = CheckinCounts(),
    val items: List<CheckinItem> = emptyList(),
)

internal fun parseCheckinSummary(body: String): CheckinSummary {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的签到汇总格式不正确")
    }
    val counts = root.optJSONObject("counts")?.let {
        CheckinCounts(
            newSuccess = it.optInt("new_success", 0),
            already = it.optInt("already", 0),
            failed = it.optInt("failed", 0),
            unavailable = it.optInt("unavailable", 0),
            notRun = it.optInt("not_run", 0),
            noAccount = it.optInt("no_account", 0),
            pending = it.optInt("pending", 0),
        )
    } ?: CheckinCounts()
    val array = root.optJSONArray("items")
    val items = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val failuresArray = item.optJSONArray("failures")
            val failures = if (failuresArray == null) {
                emptyList()
            } else {
                (0 until failuresArray.length()).mapNotNull { fIndex ->
                    val failure = failuresArray.optJSONObject(fIndex) ?: return@mapNotNull null
                    CheckinFailure(
                        accountId = failure.optString("account_id").trim(),
                        label = failure.optString("label").trim(),
                        message = failure.optString("message").trim(),
                    )
                }
            }
            CheckinItem(
                providerId = item.optString("provider_id").trim(),
                providerName = item.optString("provider_name").trim(),
                title = item.optString("title").trim(),
                status = item.optString("status").trim(),
                accounts = item.optInt("accounts", 0),
                newSuccess = item.optInt("new_success", 0),
                failed = item.optInt("failed", 0),
                failures = failures,
            )
        }
    }
    return CheckinSummary(
        generatedAt = root.optLong("generated_at", 0).coerceAtLeast(0),
        since = root.optLong("since", 0).coerceAtLeast(0),
        counts = counts,
        items = items,
    )
}

/**
 * 汇总的一句话结论。
 *
 * 口径必须写出来（"本次"与"今日已完成"是两件事），不能只报一个数字；
 * 全是 0 时如实说"暂无"，不给一句"全部成功"。
 */
internal fun checkinSummaryLine(summary: CheckinSummary): String {
    val c = summary.counts
    val parts = mutableListOf<String>()
    if (c.newSuccess > 0) parts += "本次成功 ${c.newSuccess}"
    if (c.already > 0) parts += "今日已完成 ${c.already}"
    if (c.failed > 0) parts += "失败 ${c.failed}"
    if (c.unavailable > 0) parts += "不可用 ${c.unavailable}"
    if (c.pending > 0) parts += "排队 ${c.pending}"
    if (c.noAccount > 0) parts += "无账号 ${c.noAccount}"
    if (c.notRun > 0) parts += "未运行 ${c.notRun}"
    if (parts.isEmpty()) {
        return "本次暂无签到记录"
    }
    return parts.joinToString(" · ")
}

/** 唤醒任务的新建 + 签到汇总读取。 */
class WakeWriteApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 20_000,
) {
    /** 新建/更新唤醒任务（同账号重复提交会更新已有任务——这是服务端语义，页面要说清楚）。 */
    fun createWake(status: GatewayStatus, draft: WakeDraft): Boolean {
        val body = buildWakeCreateJson(draft)
        val text = request(status, "POST", "/api/wake", body)
        return parseWakeWriteOk(text)
    }

    fun deleteWake(status: GatewayStatus, id: String) {
        request(status, "DELETE", "/api/wake/$id", null)
    }

    fun fetchCheckinSummary(status: GatewayStatus): CheckinSummary =
        parseCheckinSummary(request(status, "GET", "/api/scheduler/checkin-summary", null))

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
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
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

/** 写接口回 `{"ok":true}`；没有 ok 字段时视为成功（服务端只回任务体）。 */
internal fun parseWakeWriteOk(body: String): Boolean {
    if (body.isBlank()) {
        return true
    }
    return try {
        val root = JSONObject(body.trim())
        !root.has("ok") || root.optBoolean("ok", true)
    } catch (_: JSONException) {
        true
    }
}
