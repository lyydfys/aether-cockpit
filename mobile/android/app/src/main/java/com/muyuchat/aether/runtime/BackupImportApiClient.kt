package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/** 一次备份导入的结果（服务端 `imported` 标志 + 需要重填凭据的渠道）。 */
data class BackupImportResult(
    val settings: Boolean = false,
    val library: Boolean = false,
    val accounts: Boolean = false,
    val specs: Boolean = false,
    val profiles: Int = 0,
    val needKey: List<String> = emptyList(),
)

/**
 * 备份导入。
 *
 * 口径（读 `applyBundle` 得到，不是猜的）：
 * - **设置会被覆盖**（隐私脱敏 / 价格表 / 虚拟组 / UI / 禁用渠道 / 排序 / CORS / IP 白名单 / 配置档 / 预设）；
 * - **资源库按 ID 合并**，不删除本机多出来的条目；
 * - **账号池不清空**：只更新匹配到的账号标签；
 * - 凭据不随备份回来，`NeedKey` 会列出要重新填的渠道。
 *
 * 字段名是 `pass`（不是 `passphrase`），照 handler 抄。
 */
class BackupImportApiClient(
    private val connectTimeoutMs: Int = 10_000,
    private val readTimeoutMs: Int = 60_000,
) {
    fun importBackup(status: GatewayStatus, base64: String, pass: String, library: Boolean = false): BackupImportResult {
        val problem = validateBackupImportPassphrase(pass)
        if (problem != null) {
            throw ManagementApiException("weak_passphrase", problem)
        }
        val body = buildBackupImportJson(base64, pass.trim(), library)
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/backup/import").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "导入失败（HTTP $code）" }.take(200))
            }
            return parseBackupImportResult(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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
                throw ManagementApiException("invalid_response", "管理接口响应过大")
            }
        }
    }
}

internal fun validateBackupImportPassphrase(raw: String): String? {
    val pass = raw.trim()
    if (pass.isEmpty()) {
        return "口令不能为空"
    }
    if (pass.length < 8) {
        return "口令至少 8 位：备份文件离开手机后可以被离线尝试"
    }
    return null
}

internal fun buildBackupImportJson(base64: String, pass: String, library: Boolean = false): String {
    val data = base64.trim()
    if (data.isEmpty()) {
        throw ManagementApiException("no_data", "没有读到备份内容")
    }
    val json = JSONObject().put("data", data).put("pass", pass)
    if (library) {
        json.put("library", true)
    }
    return json.toString()
}

internal fun parseBackupImportResult(body: String): BackupImportResult {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的导入结果格式不正确")
    }
    val imported = root.optJSONObject("imported")
    val needKeyArray = root.optJSONArray("NeedKey")
    val needKey = if (needKeyArray == null) {
        emptyList()
    } else {
        (0 until needKeyArray.length()).mapNotNull { index ->
            needKeyArray.optString(index).trim().takeIf { it.isNotEmpty() }
        }
    }
    return BackupImportResult(
        settings = imported?.optBoolean("settings", false) ?: false,
        library = imported?.optBoolean("library", false) ?: root.optBoolean("Library", false),
        accounts = imported?.optBoolean("accounts", false) ?: false,
        specs = imported?.optBoolean("specs", false) ?: false,
        profiles = root.optInt("Profiles", 0).coerceAtLeast(0),
        needKey = needKey,
    )
}

/** 导入结论：说清**哪些被覆盖**、哪些被合并、哪些还要重填凭据。 */
internal fun backupImportSummary(result: BackupImportResult): String {
    val parts = mutableListOf<String>()
    if (result.settings) parts += "设置已覆盖"
    if (result.library) parts += "资源库已按 ID 合并"
    if (result.accounts) parts += "匹配账号的标签已更新"
    if (result.specs) parts += "渠道 spec 已写回"
    if (result.profiles > 0) parts += "${result.profiles} 个配置档"
    if (parts.isEmpty()) {
        return "这次备份里没有可应用到本机的内容"
    }
    val head = parts.joinToString(" · ")
    return if (result.needKey.isEmpty()) {
        head
    } else {
        "$head；这些渠道需要重新填凭据：${result.needKey.joinToString("、")}"
    }
}
