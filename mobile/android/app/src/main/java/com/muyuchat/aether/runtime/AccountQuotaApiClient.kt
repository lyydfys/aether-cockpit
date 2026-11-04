package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 订阅窗口读数（`internal/provider/contract.go:209-225` `QuotaWindow`）。
 *
 * [usedPercent] 是**百分比**（契约注释写明 `Used float64 // 0-100`），不是绝对值；
 * 卡片的「剩余 %」= 100 - used。[amount]/[limit] 只在渠道真的给了绝对值时才有意义。
 */
data class AccountQuotaWindow(
    val name: String,
    val usedPercent: Double,
    val resetsAt: Long,
    val unit: String,
    val amount: Long,
    val limit: Long,
)

/**
 * 今日签到卡（`internal/core/gateway/checkin_api.go:23-30` `quotaCheckin`）。
 * outcome 取值见 `provider.Checkin*`：claimed / done / own-app / inactive / ineligible / failed。
 */
data class AccountCheckin(
    val day: String,
    val outcome: String,
    val credit: Double,
    val streak: Int,
    val message: String,
    val again: Boolean,
)

/**
 * `GET /api/accounts` 里的一条账号（`AccountSnapshot` + `accountView` 视图字段）。
 *
 * 字段出处：
 * - account_id / provider_id / label / disabled / reset_at / last_used / quota
 *   → `internal/provider/env.go:796-818`（`AccountSnapshot`）
 * - quota_supported / quota_error / checkin_supported / checkin
 *   → `internal/core/gateway/admin.go:356-370`（`accountView`）
 * - quota.total / quota.used / quota.unit / quota.windows / quota.message
 *   → `internal/provider/contract.go:228-252`（`QuotaStatus`）与 `:209-225`（`QuotaWindow`）
 */
data class AccountQuotaRow(
    val accountId: String,
    val providerId: String,
    val label: String,
    val disabled: Boolean,
    val resetAt: Long,
    val lastUsed: Long,
    val quotaSupported: Boolean,
    val quotaError: String,
    val checkinSupported: Boolean,
    val checkin: AccountCheckin?,
    val quotaWindows: List<AccountQuotaWindow>,
    val quotaTotal: Int,
    val quotaUsed: Int,
    val quotaUnit: String,
    val quotaMessage: String,
)

/**
 * 账号与额度客户端。
 *
 * 复用管理面的口径（与 [ManagementApiClient] 同一套）：HTTP 只打 `http://<addr>`，
 * 会话凭据只走 `Authorization: Bearer <adminSession>` 头，错误统一抛
 * [ManagementApiException]（code 用 `http_<status>` / `network_error` / `invalid_response`），
 * 响应体按上限读取。没有再引一层 HTTP 库。
 *
 * 端点：
 * - `GET  /api/accounts`（`gateway.go:844` → `handleAccounts`）
 * - `POST /api/accounts/{id}/settings` `{"disabled":bool}`（`gateway.go:1053` → `handleAccountSettings`，回 `{"account":snapshot}`）
 * - `POST /api/accounts/{id}/checkin`（`gateway.go:847` → `handleCheckIn`，回 `{"checkin":{…},"outcome":…}`）
 */
class AccountQuotaApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
    /** 签到要打上游，服务端给 90s 预算（`admin.go:578`），读超时必须比它长。 */
    private val checkinReadTimeoutMs: Int = 100_000,
) {
    fun fetchAccounts(status: GatewayStatus): List<AccountQuotaRow> {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        val response = request(status, "GET", "/api/accounts", body = null, readTimeoutMs)
        return parseAccounts(response)
    }

    /**
     * 账号级启用/停用（`accountView` 的 `disabled`）。
     *
     * 服务端确认后回最新快照；调用方据此**就地**更新卡片（不做乐观更新）。
     */
    fun setDisabled(status: GatewayStatus, accountId: String, disabled: Boolean): AccountQuotaRow {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code ->
            throw ManagementApiException(code, "账号 id 不合法")
        }
        val response = request(
            status = status,
            method = "POST",
            path = accountSettingsPath(accountId),
            body = buildAccountDisabledJson(disabled),
            timeoutMs = readTimeoutMs,
        )
        return parseAccountSnapshot(response)
    }

    /** 立即签到；返回服务端回的今日签到卡（可能为 null，表示服务端没给状态）。 */
    fun checkin(status: GatewayStatus, accountId: String): AccountCheckin? {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code ->
            throw ManagementApiException(code, "账号 id 不合法")
        }
        val response = request(
            status = status,
            method = "POST",
            path = accountCheckinPath(accountId),
            body = "{}",
            timeoutMs = checkinReadTimeoutMs,
        )
        return parseCheckinAck(response)
    }

    /** 网关没跑 / 没有会话凭据时一次请求都不发。 */
    private fun guardOf(status: GatewayStatus): ManagementApiException? = when {
        !status.running || status.addr.isBlank() -> ManagementApiException("gateway_not_running", "网关未启动")
        status.adminSession.isBlank() -> ManagementApiException("gateway_not_running", "网关未启动")
        else -> null
    }

    private fun request(
        status: GatewayStatus,
        method: String,
        path: String,
        body: String?,
        timeoutMs: Int,
    ): String {
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = timeoutMs
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
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it) }
                        ?.let { JSONObject(it.trim()).optString("error").trim() }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            val response = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return response
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

/** 账号 id 直接进路径段，必须先编码（id 里可能有 `/` 或空格以外的怪字符）。 */
internal fun accountSettingsPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/settings"

internal fun accountCheckinPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/checkin"

internal fun encodeAccountSegment(accountId: String): String =
    URLEncoder.encode(accountId.trim(), "UTF-8").replace("+", "%20")

/** 只把 `disabled` 放进 body：这个端点只改启用位，不带别的字段。 */
internal fun buildAccountDisabledJson(disabled: Boolean): String =
    JSONObject().put("disabled", disabled).toString()

/** 空 id / 带路径分隔符的 id 在发请求前就拒掉。 */
internal fun validateAccountId(accountId: String): String? {
    val trimmed = accountId.trim()
    return when {
        trimmed.isEmpty() -> "account_id_empty"
        trimmed.any { it.isWhitespace() || it in "/?#&=%" } -> "account_id_invalid"
        else -> null
    }
}

private fun objectOrThrow(body: String, label: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "$label 返回的数据格式不正确")
}

/** `GET /api/accounts` → `{"accounts":[…]}`；空数组是合法结果，缺字段不是。 */
internal fun parseAccounts(body: String): List<AccountQuotaRow> {
    val root = objectOrThrow(body, "账号列表")
    val array = root.optJSONArray("accounts")
        ?: throw ManagementApiException("invalid_response", "账号列表缺少 accounts 字段")
    if (array.length() == 0) {
        return emptyList()
    }
    val rows = (0 until array.length()).mapNotNull { index ->
        array.optJSONObject(index)?.let(::parseAccountObject)
    }
    // 有元素却一条都解析不出来 = 格式错了，不能静默当成"还没有账号"。
    if (rows.isEmpty()) {
        throw ManagementApiException("invalid_response", "账号列表返回的数据格式不正确")
    }
    return rows
}

/** `POST /api/accounts/{id}/settings` → `{"account":<snapshot>}`。 */
internal fun parseAccountSnapshot(body: String): AccountQuotaRow {
    val root = objectOrThrow(body, "账号设置")
    val account = root.optJSONObject("account")
        ?: throw ManagementApiException("invalid_response", "账号设置没有返回账号快照")
    return parseAccountObject(account)
        ?: throw ManagementApiException("invalid_response", "账号快照缺少 account_id")
}

/** `POST /api/accounts/{id}/checkin` → `{"checkin":{…},"outcome":…}`；没有 checkin 时回 null。 */
internal fun parseCheckinAck(body: String): AccountCheckin? {
    val root = objectOrThrow(body, "签到结果")
    return root.optJSONObject("checkin")?.let(::parseCheckin)
}

internal fun parseCheckin(item: JSONObject): AccountCheckin = AccountCheckin(
    day = item.optString("day").trim(),
    outcome = item.optString("outcome").trim(),
    credit = item.optDouble("credit", 0.0).coerceAtLeast(0.0),
    streak = item.optInt("streak", 0).coerceAtLeast(0),
    message = item.optString("msg").trim().take(200),
    again = item.optBoolean("again", false),
)

private fun parseAccountObject(item: JSONObject): AccountQuotaRow? {
    val accountId = item.optString("account_id").trim()
    if (accountId.isEmpty()) {
        return null
    }
    val quota = item.optJSONObject("quota")
    val windows = quota?.optJSONArray("windows")?.let { array ->
        (0 until array.length()).mapNotNull { index -> array.optJSONObject(index)?.let(::parseQuotaWindow) }
    }.orEmpty()
    return AccountQuotaRow(
        accountId = accountId,
        providerId = item.optString("provider_id").trim(),
        label = item.optString("label").trim().ifBlank { accountId },
        disabled = item.optBoolean("disabled", false),
        resetAt = item.optLong("reset_at", 0L).coerceAtLeast(0L),
        lastUsed = item.optLong("last_used", 0L).coerceAtLeast(0L),
        // 老版本没有 quota_supported 字段时，按"有 quota 对象"推断，避免误报「不提供额度数据」。
        quotaSupported = if (item.has("quota_supported")) {
            item.optBoolean("quota_supported", false)
        } else {
            quota != null
        },
        quotaError = item.optString("quota_error").trim().take(400),
        checkinSupported = item.optBoolean("checkin_supported", false),
        checkin = item.optJSONObject("checkin")?.let(::parseCheckin),
        quotaWindows = windows,
        quotaTotal = quota?.optInt("total", 0)?.coerceAtLeast(0) ?: 0,
        quotaUsed = quota?.optInt("used", 0)?.coerceAtLeast(0) ?: 0,
        quotaUnit = quota?.optString("unit").orEmpty().trim(),
        quotaMessage = quota?.optString("message").orEmpty().trim().take(200),
    )
}

private fun parseQuotaWindow(item: JSONObject): AccountQuotaWindow = AccountQuotaWindow(
    name = item.optString("name").trim(),
    // 契约写明是 0-100 的百分比；上游万一给越界值就夹回区间，不显示 >100% / 负剩余。
    usedPercent = item.optDouble("used", 0.0).coerceIn(0.0, 100.0),
    resetsAt = item.optLong("resets_at", 0L).coerceAtLeast(0L),
    unit = item.optString("unit").trim(),
    amount = item.optLong("amount", 0L).coerceAtLeast(0L),
    limit = item.optLong("limit", 0L).coerceAtLeast(0L),
)
