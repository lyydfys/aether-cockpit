package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 2FA（TOTP）条目。**这里永远不出现明文 seed**：服务端只回 `secret_tail`（末 4 位），
 * 验证码也只由服务端本机计算后下发布。
 */
data class TotpEntry(
    val id: String,
    val issuer: String,
    val account: String,
    val note: String,
    val digits: Int,
    val period: Int,
    val algorithm: String,
    val createdAt: Long,
    val secretTail: String,
)

/** `/api/2fa/codes` 的一条结果；[error] 非空表示服务端解不开这条（例如换了设备没 key）。 */
data class TotpCode(
    val id: String,
    val code: String,
    val remaining: Int,
    val error: String,
)

/**
 * 2FA 管理面。契约（已核到 `internal/core/gateway/twofa_api.go`）：
 *
 * - `GET /api/2fa` → `{entries:[{id,issuer,account,note,digits,period,algorithm,created_at,secret_tail}]}`
 * - `GET /api/2fa/codes` → `{entries:[{…同上, code?, remaining?, error?}]}`
 * - `POST /api/2fa {issuer,account,note,secret,digits,period,algorithm}` → `{entry:{…}}`；
 *   非法 base32 / 不支持的算法 400，`error` 为服务端原文
 * - `DELETE /api/2fa/{id}` → `{deleted:id}`；不存在 404
 *
 * 全部是管理面接口，必须带 `Authorization: Bearer <adminSession>`，且凭据只走 header。
 */
class TotpApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchEntries(status: GatewayStatus): List<TotpEntry> =
        parseTotpEntries(request(status, "GET", "/api/2fa", null))

    fun fetchCodes(status: GatewayStatus): List<TotpCode> =
        parseTotpCodes(request(status, "GET", "/api/2fa/codes", null))

    /** 新增一条；返回服务端回读的条目（不含明文 seed）。 */
    fun addEntry(
        status: GatewayStatus,
        issuer: String,
        account: String,
        note: String,
        secret: String,
        digits: Int = 6,
        period: Int = 30,
        algorithm: String = "SHA1",
    ): TotpEntry = parseTotpEntry(
        request(
            status,
            "POST",
            "/api/2fa",
            buildTotpAddJson(issuer, account, note, secret, digits, period, algorithm),
        ),
    )

    fun deleteEntry(status: GatewayStatus, id: String) {
        val path = "/api/2fa/" + URLEncoder.encode(id, Charsets.UTF_8.name())
        request(status, "DELETE", path, null)
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

internal fun buildTotpAddJson(
    issuer: String,
    account: String,
    note: String,
    secret: String,
    digits: Int,
    period: Int,
    algorithm: String,
): String = JSONObject()
    .put("issuer", issuer.trim())
    .put("account", account.trim())
    .put("note", note.trim())
    .put("secret", secret.trim())
    .put("digits", digits)
    .put("period", period)
    .put("algorithm", algorithm.trim().ifBlank { "SHA1" })
    .toString()

internal fun parseTotpEntries(body: String): List<TotpEntry> {
    val root = totpRoot(body)
    val array = root.optJSONArray("entries") ?: return emptyList()
    return totpEntries(array)
}

internal fun parseTotpEntry(body: String): TotpEntry {
    val root = totpRoot(body)
    val item = root.optJSONObject("entry")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的 2FA 条目格式不正确")
    return totpEntry(item)
        ?: throw ManagementApiException("invalid_response", "管理接口返回的 2FA 条目格式不正确")
}

internal fun parseTotpCodes(body: String): List<TotpCode> {
    val root = totpRoot(body)
    val array = root.optJSONArray("entries") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        TotpCode(
            id = id,
            code = item.optString("code").trim(),
            remaining = item.optInt("remaining", 0).coerceAtLeast(0),
            error = item.optString("error").trim(),
        )
    }
}

private fun totpRoot(body: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "管理接口返回的 2FA 数据格式不正确")
}

private fun totpEntries(array: JSONArray): List<TotpEntry> = (0 until array.length()).mapNotNull { index ->
    array.optJSONObject(index)?.let { totpEntry(it) }
}

private fun totpEntry(item: JSONObject): TotpEntry? {
    val id = item.optString("id").trim()
    if (id.isEmpty()) {
        return null
    }
    val algorithm = item.optString("algorithm").trim().ifBlank { "SHA1" }
    return TotpEntry(
        id = id,
        issuer = item.optString("issuer").trim(),
        account = item.optString("account").trim(),
        note = item.optString("note").trim(),
        // 服务端 `totp.Digits/Period` 的归一范围是 digits 4-10、period 5-300；
        // 这里按同一区间做下限保护，不要收窄（否则会把合法的 4 位 / 5 秒配置改掉）。
        digits = item.optInt("digits", 6).coerceIn(4, 10),
        period = item.optInt("period", 30).coerceIn(5, 300),
        algorithm = algorithm,
        createdAt = item.optLong("created_at", 0L),
        secretTail = item.optString("secret_tail").trim(),
    )
}
