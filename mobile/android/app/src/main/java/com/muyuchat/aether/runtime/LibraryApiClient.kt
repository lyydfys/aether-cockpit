package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** 资源库条目所属的仓库。四个仓库的删除端点各不相同，不能混用。 */
enum class LibraryKind(val label: String) {
    PROMPT("指令集"),
    SKILL("技能"),
    MCP("MCP"),
    SUBAGENT("子代理"),
}

/**
 * 一条资源库条目。
 *
 * 只有 id / 名称 / 时间：**没有** `config`、也没有 `content` 字段。
 * 这不是省事，是结构性的保证——MCP 的 `config` 里可能带 env 密钥（如 `API_KEY`），
 * 不该出现在手机上。
 */
data class LibraryEntry(
    val id: String,
    val name: String,
    val kind: LibraryKind,
    val updatedAt: Long,
)

data class LibrarySnapshot(val entries: List<LibraryEntry>)

/**
 * 资源库只读投影 + 删除。
 *
 * 契约（`library_api.go:handleLibrary`）：`GET /api/library` 会同时下发多套字段。
 * 这里**只**读其中三套轻量/无密钥的：
 * - `prompts[]`    → 指令集（含 content，本类不解析）
 * - `skills_raw[]` → 技能（含 content，本类不解析）
 * - `mcp[]`        → MCP 的轻量行（id/name/kind，**不含 config**）
 * - `subagents[]`  → 子代理的轻量行
 *
 * ⚠ 刻意**不读** `mcp_servers[]`：那份带完整 `config`（含 env 密钥），服务端只掩掉了
 * OAuth 的 `clientSecret`，其余原样下发。手机端不解析它，等于不给密钥落地的机会。
 */
class LibraryApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    fun fetchLibrary(status: GatewayStatus): LibrarySnapshot =
        parseLibrary(request(status, "GET", "/api/library", null))

    fun deleteEntry(status: GatewayStatus, kind: LibraryKind, id: String) {
        request(status, "DELETE", libraryDeletePath(kind, id), null)
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

/** 四个仓库的删除端点不同：子代理删的是 `agents`，不是 `subagents`。 */
internal fun libraryDeletePath(kind: LibraryKind, id: String): String {
    val segment = when (kind) {
        LibraryKind.PROMPT -> "prompts"
        LibraryKind.SKILL -> "skills"
        LibraryKind.MCP -> "mcp"
        LibraryKind.SUBAGENT -> "agents"
    }
    return "/api/library/$segment/" + URLEncoder.encode(id, Charsets.UTF_8.name())
}

internal fun parseLibrary(body: String): LibrarySnapshot {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的资源库数据格式不正确")
    }
    val entries = mutableListOf<LibraryEntry>()
    entries += libraryRows(root.optJSONArray("prompts"), LibraryKind.PROMPT)
    entries += libraryRows(root.optJSONArray("skills_raw"), LibraryKind.SKILL)
    entries += libraryRows(root.optJSONArray("mcp"), LibraryKind.MCP)
    entries += libraryRows(root.optJSONArray("subagents"), LibraryKind.SUBAGENT)
    return LibrarySnapshot(entries = entries)
}

/**
 * 解析一组行。只取 id / name / updated_at：即使数组里带着 `config` 或 `content`，
 * 也不会被搬进 [LibraryEntry]。
 */
private fun libraryRows(array: JSONArray?, kind: LibraryKind): List<LibraryEntry> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        val name = item.optString("name").trim().ifBlank { item.optString("title").trim() }
        LibraryEntry(
            id = id,
            name = name.ifBlank { id },
            kind = kind,
            updatedAt = item.optLong("updated_at", item.optLong("created_at", 0L)),
        )
    }
}
