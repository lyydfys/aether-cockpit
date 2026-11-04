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

/** 一次 inline 导出的结果：已经是密封好的密文（base64），客户端只负责落盘与分享。 */
data class BackupExport(
    val base64: String,
    val bytes: Int,
    val createdAt: Long,
)

/**
 * 备份导出（只做导出，不做导入）。
 *
 * 为什么只做导出：导入要处理「覆盖本机设置」「口令错误」「来自更高版本的备份」这些语义，
 * 在手机上误操作代价太大；桌面端已有完整的导入流程，手机端如实指向它。
 *
 * 契约：`POST /api/backup/export {"pass":"…","inline":true}` →
 * `{"data":"<base64 的密封密文>","bytes":N,"created_at":<unix>}`。
 * 密文格式与桌面导出的 `.aether-backup` **完全一致**（同一个 `sealBackup`），
 * 所以手机导出的文件可以直接在桌面导入。
 */
class BackupApiClient(
    private val connectTimeoutMs: Int = 10_000,
    private val readTimeoutMs: Int = 30_000,
) {
    fun exportInline(status: GatewayStatus, passphrase: String): BackupExport {
        val problem = validateBackupPassphrase(passphrase)
        if (problem != null) {
            throw ManagementApiException("weak_passphrase", problem)
        }
        return parseBackupExport(request(status, "/api/backup/export", buildInlineExportJson(passphrase)))
    }

    private fun request(status: GatewayStatus, path: String, body: String): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        // 口令只走请求体；管理会话仍走 header。
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

    private fun readLimited(reader: Reader, limit: Int = 64 * 1024 * 1024): String {
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

/**
 * 口令强度：本地先挡一次。
 *
 * 8 位是**下界而不是建议值**——服务端只拒绝空口令，弱口令会被照单全收；
 * 备份文件离开设备后就是一份可离线爆破的密文，所以这里必须比服务端更严。
 */
internal fun validateBackupPassphrase(raw: String): String? {
    val pass = raw.trim()
    if (pass.isEmpty()) {
        return "口令不能为空"
    }
    if (pass.length < 8) {
        return "口令至少 8 位：备份离开手机后可以被离线尝试"
    }
    return null
}

internal fun buildInlineExportJson(passphrase: String): String = JSONObject()
    .put("pass", passphrase)
    .put("inline", true)
    .toString()

/** 与桌面导出同名风格，便于用户辨认；**不含口令**，也不含账号信息。 */
internal fun backupFileName(nowMs: Long): String {
    val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date(nowMs))
    return "aether-backup-$stamp.aether-backup"
}

internal fun parseBackupExport(body: String): BackupExport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的备份格式不正确")
    }
    val data = root.optString("data").trim()
    if (data.isEmpty()) {
        throw ManagementApiException("invalid_response", "服务端没有返回备份内容：未生成文件")
    }
    return BackupExport(
        base64 = data,
        bytes = root.optInt("bytes", 0).coerceAtLeast(0),
        createdAt = root.optLong("created_at", 0L),
    )
}
