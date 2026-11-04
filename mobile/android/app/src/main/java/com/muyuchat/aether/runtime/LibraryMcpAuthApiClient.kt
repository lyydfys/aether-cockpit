package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 一次 MCP 登录（OAuth）的发起结果。
 *
 * 端点：`POST /api/library/mcp-signin` `{"name":"<服务器名>"}`
 * （`gateway.go:1006` → `mcp_signin.go:279-395`）。已经登录过时服务端直接回
 * `{"id":"","state":"done","signedIn":true}`（`:296-299`）。
 */
data class McpSigninStart(
    val id: String,
    val state: String,
    val url: String,
    val alreadySignedIn: Boolean,
)

/** 轮询状态（`GET /api/library/mcp-signin/{id}`，`mcp_signin.go:479-492`）：`state` 取值 waiting/done/failed/canceled。 */
data class McpSigninStatus(
    val id: String,
    val state: String,
    val url: String,
    val name: String,
    val error: String,
)

/**
 * MCP 登录/登出客户端（桌面 `library.js:795` `drawer('登录 ' + it.name)`）。
 *
 * 手机上这条路是**真的能走通**的：服务端在网关进程里起一个 `127.0.0.1:<port>/callback`
 * 回调监听（`mcp_signin.go:336-351`），而网关就跑在手机本机上，系统浏览器打开授权页后
 * 回跳回环地址即可命中同一个进程。
 */
class LibraryMcpAuthApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    fun start(status: GatewayStatus, name: String): McpSigninStart {
        guard(status)
        val response = request(status, "POST", mcpSigninPath(), buildMcpSigninJson(name), signedInOK = false)
        return parseMcpSigninStart(response.second)
    }

    fun poll(status: GatewayStatus, id: String): McpSigninStatus {
        guard(status)
        val response = request(status, "GET", mcpSigninPollPath(id), null, signedInOK = true)
        return parseMcpSigninStatus(response.second)
    }

    /** 取消：服务端把这次 flow 标成 `canceled`（`mcp_signin.go:494-509`）。 */
    fun cancel(status: GatewayStatus, id: String) {
        guard(status)
        request(status, "POST", mcpSigninCancelPath(id), "{}", signedInOK = true)
    }

    /** 登出：删本地令牌并尽力撤销服务商授权（`mcp_signin.go:511-...`）。 */
    fun signOut(status: GatewayStatus, name: String) {
        guard(status)
        request(status, "POST", mcpSignoutPath(), buildMcpSignoutJson(name), signedInOK = true)
    }

    private fun guard(status: GatewayStatus) {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
    }

    /**
     * 返回 `(status, body)`；[signedInOK] 为真时 404 也当"正常回包"读（轮询过期 flow 会回 404
     * + `{"error":…,"state":"failed"}`，`mcp_signin.go:481-485`）。
     */
    private fun request(
        status: GatewayStatus,
        method: String,
        path: String,
        body: String?,
        signedInOK: Boolean,
    ): Pair<Int, String> {
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
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
            val accepted = code == HttpURLConnection.HTTP_OK || (signedInOK && code == HttpURLConnection.HTTP_NOT_FOUND)
            val stream = if (accepted) connection.inputStream else connection.errorStream
            val text = stream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it) }.orEmpty()
            if (!accepted) {
                val detail = runCatching { JSONObject(text.trim()).optString("error").trim() }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            return code to text
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 1024 * 1024): String {
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

/** 端点路径（`gateway.go:1006-1010`）。 */
internal fun mcpSigninPath(): String = "/api/library/mcp-signin"

internal fun mcpSigninPollPath(id: String): String =
    "/api/library/mcp-signin/${encodeAccountSegment(id)}"

internal fun mcpSigninCancelPath(id: String): String =
    "/api/library/mcp-signin/${encodeAccountSegment(id)}/cancel"

internal fun mcpSignoutPath(): String = "/api/library/mcp-signout"

internal fun buildMcpSigninJson(name: String): String =
    JSONObject().put("name", name.trim()).toString()

internal fun buildMcpSignoutJson(name: String): String =
    JSONObject().put("name", name.trim()).toString()

internal fun parseMcpSigninStart(body: String): McpSigninStart {
    val root = jsonObjectOf(body, "MCP 登录")
    return McpSigninStart(
        id = root.optString("id").trim(),
        state = root.optString("state").trim().ifBlank { "waiting" },
        url = root.optString("url").trim(),
        alreadySignedIn = root.optBoolean("signedIn", false),
    )
}

internal fun parseMcpSigninStatus(body: String): McpSigninStatus {
    val root = jsonObjectOf(body, "MCP 登录状态")
    return McpSigninStatus(
        id = root.optString("id").trim(),
        state = root.optString("state").trim().ifBlank { "failed" },
        url = root.optString("url").trim(),
        name = root.optString("name").trim(),
        error = root.optString("error").trim().take(200),
    )
}

private fun jsonObjectOf(body: String, label: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "$label 返回的数据格式不正确")
}
