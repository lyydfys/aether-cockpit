package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** 一次账号包导出：`data` 是 AETHER2 密封密文的 base64。 */
data class AccountExport(
    val base64: String,
    val accounts: Int,
    val filename: String,
)

/**
 * 手机端导出账号包（把本机账号交回桌面）。
 *
 * 契约（`accounts_bundle.go`，本轮已实测可用）：
 * `POST /api/accounts/export/bundle {"passphrase":"…"}` →
 * `{format,version,accounts,encrypted:true,filename,data:<base64>}`；口令至少 8 位，明文导出被拒。
 *
 * 与"备份导出"的区别：备份导的是设置/虚拟组/资源库；**账号包导的是真实凭证**（AETHER2 密封）。
 * 所以这里额外做两件事：本地先挡弱口令、并且对 `encrypted=false` 的响应直接拒绝。
 */
class AccountExportApiClient(
    private val connectTimeoutMs: Int = 10_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun export(status: GatewayStatus, passphrase: String): AccountExport {
        val problem = validateAccountExportPassphrase(passphrase)
        if (problem != null) {
            throw ManagementApiException("weak_passphrase", problem)
        }
        return parseAccountExport(request(status, buildAccountExportJson(passphrase.trim())))
    }

    private fun request(status: GatewayStatus, body: String): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/accounts/export/bundle").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        // 口令只走请求体；管理会话走 header。
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
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

    private fun readLimited(reader: Reader, limit: Int = 16 * 1024 * 1024): String {
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

/** 口令强度：8 位是下界。账号包离开手机后就是一份可离线爆破的密文。 */
internal fun validateAccountExportPassphrase(raw: String): String? {
    val pass = raw.trim()
    if (pass.isEmpty()) {
        return "口令不能为空"
    }
    if (pass.length < 8) {
        return "口令至少 8 位：账号包离开手机后可以被离线尝试"
    }
    return null
}

internal fun buildAccountExportJson(passphrase: String): String = JSONObject()
    .put("passphrase", passphrase)
    .toString()

/** 服务端没给 filename 时的兜底；与桌面同风格，**不含口令、不含账号信息**。 */
internal fun accountExportFileName(nowMs: Long): String {
    val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date(nowMs))
    return "aether-accounts-$stamp.aether-accounts"
}

internal fun parseAccountExport(body: String): AccountExport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的账号包格式不正确")
    }
    if (!root.optBoolean("encrypted", false)) {
        // 明文账号包不允许落盘：宁可失败，也不把一份未加密的凭证交给用户去分享。
        throw ManagementApiException("invalid_response", "服务端返回的不是加密账号包，已拒绝保存")
    }
    val data = root.optString("data").trim()
    if (data.isEmpty()) {
        throw ManagementApiException("invalid_response", "服务端没有返回账号包内容：未生成文件")
    }
    val accounts = root.optInt("accounts", 0).coerceAtLeast(0)
    val filename = root.optString("filename").trim().ifBlank {
        accountExportFileName(System.currentTimeMillis())
    }
    return AccountExport(base64 = data, accounts = accounts, filename = filename)
}
