package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 一个桌面客户端条目。**故意不含 `path`**：那是桌面文件路径
 * （`C:/Users/…/.claude.json`、`\\\\wsl.localhost\\…`），手机上既不存在也没法改，
 * 显示出来只会让人以为"点进去能编辑"。
 */
data class ClientEntry(
    val id: String,
    val name: String,
    val kind: String,
    val detected: Boolean,
    val backups: Int,
    val note: String,
)

data class ClientsSnapshot(
    val clients: List<ClientEntry>,
    val available: Boolean,
    val targets: Int,
)

/**
 * 客户端只读列表。
 *
 * `/api/clients` 的返回体**不含凭据**（已核 `clients.Status`：id/name/kind/path/detected/managed/backups/note），
 * 所以可以安全展示；但"把配置注入到客户端"要写桌面文件，属**仅 Windows**，手机端不提供按钮。
 */
class ClientsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun fetchClients(status: GatewayStatus): ClientsSnapshot =
        parseClients(get(status, "/api/clients"))

    private fun get(status: GatewayStatus, path: String): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
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

internal fun parseClients(body: String): ClientsSnapshot {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的客户端列表格式不正确")
    }
    val array = root.optJSONArray("clients")
    val clients = if (array == null) {
        emptyList()
    } else {
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val id = item.optString("id").trim()
            if (id.isEmpty()) {
                return@mapNotNull null
            }
            val backupsArray = item.optJSONArray("backups")
            ClientEntry(
                id = id,
                name = item.optString("name").trim().ifBlank { id },
                kind = item.optString("kind").trim(),
                detected = item.optBoolean("detected", false),
                backups = backupsArray?.length() ?: 0,
                note = item.optString("note").trim(),
            )
        }
    }
    val targetsArray: JSONArray? = root.optJSONArray("targets")
    return ClientsSnapshot(
        clients = clients,
        available = root.optBoolean("available", false),
        targets = targetsArray?.length() ?: 0,
    )
}

/** 一句话结论：可用性 + 检测到几个 + 明确"注入仅桌面端"。 */
internal fun clientSummaryLine(snapshot: ClientsSnapshot): String {
    if (!snapshot.available) {
        return "客户端注入在本机不可用（仅桌面端提供）"
    }
    if (snapshot.clients.isEmpty()) {
        return "本机没有可接入的客户端 · 注入仅桌面端"
    }
    val detected = snapshot.clients.count { it.detected }
    val extra = if (snapshot.targets > 0) " · ${snapshot.targets} 个额外目标" else ""
    return "共 ${snapshot.clients.size} 个客户端，检测到 $detected 个$extra · 注入仅桌面端"
}
