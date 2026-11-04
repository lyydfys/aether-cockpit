package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 渠道启停。
 *
 * 契约（`provider_switch_api.go`）：
 * - `POST /api/provider/on`  body `{"id":"<providerId>"}` → 从 `DisabledProviders` 移除，并清掉自动下线标记
 * - `POST /api/provider/off` body `{"id":"<providerId>"}` → 加入 `DisabledProviders`
 * - 成功返回渠道列表（`respondProviders`）；400 缺 id / 404 未注册 / 500 保存失败
 *
 * 只改渠道级开关，**不碰账号**；也不需要账号级参数。
 */
data class ChannelToggleResult(
    val providerId: String,
    val enabled: Boolean,
    /**
     * 服务端在成功响应里回的就是**完整渠道列表**（`respondProviders` 直接转发
     * `handleProviders` 的字节，与 `GET /api/providers` 逐字节同构）。
     *
     * 有了它就能**就地更新**列表而不触发整页刷新——否则刷新会走 Loading，
     * 把用户刚操作的那一行甩到屏外。解析失败时为 null，调用方回退到刷新。
     */
    val providers: List<ProviderSummary>? = null,
)

class ChannelToggleApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun toggle(status: GatewayStatus, providerId: String, enable: Boolean): ChannelToggleResult {
        val id = providerId.trim()
        validateChannelId(id)?.let { code ->
            throw ManagementApiException(code, "渠道 id 不合法")
        }
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}${channelTogglePath(enable)}").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            connection.outputStream.use { it.write(buildChannelToggleJson(id).toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "切换渠道失败（HTTP $code）" }.take(160))
            }
            // 成功必须由**服务端返回**确认，不能点完就当成功。
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            parseChannelToggleAck(body)
            val providers = runCatching { parseProviders(body) }.getOrNull()
            return ChannelToggleResult(providerId = id, enabled = enable, providers = providers)
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

internal fun channelTogglePath(enable: Boolean): String =
    if (enable) "/api/provider/on" else "/api/provider/off"

/** 空 id、带空白、带路径分隔符一律拒绝——不给"猜一个 id"的机会。 */
internal fun validateChannelId(id: String): String? = when {
    id.isBlank() -> "channel_id_empty"
    id.any { it.isWhitespace() } -> "channel_id_invalid"
    id.contains('/') || id.contains('?') || id.contains('#') -> "channel_id_invalid"
    id.length > 128 -> "channel_id_invalid"
    else -> null
}

internal fun buildChannelToggleJson(id: String): String =
    JSONObject().put("id", id).toString()

/** 解析成功响应（渠道列表）：必须能解析出 JSON 对象，否则视为无效响应。 */
internal fun parseChannelToggleAck(body: String) {
    try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口没有返回渠道列表")
    }
}

internal fun channelToggleLabel(off: Boolean): String = if (off) "启用" else "停用"

internal fun channelToggleConfirmTitle(off: Boolean, name: String): String =
    if (off) "启用「$name」？" else "停用「$name」？"

/**
 * 确认文案必须说清后果：
 * - 停用 → 该渠道的模型会路由失败（不影响其它渠道）；
 * - 启用 → 会把"自动下线"标记一起清掉，否则巡检下一轮又会关回去（这是服务端行为，得说明）。
 */
internal fun channelToggleConfirmBody(off: Boolean): String =
    if (off) {
        "启用后这个渠道的账号会重新参与路由；如果某账号此前处于冷却/耗尽，仍要等它自己恢复。"
    } else {
        "停用后这个渠道的所有模型都会路由失败（其它渠道不受影响，账号数据不会删除）。" +
            "想恢复时再点「启用」。"
    }
