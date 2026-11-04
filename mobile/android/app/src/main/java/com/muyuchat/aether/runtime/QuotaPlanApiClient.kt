package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.TimeUnit

/**
 * 订阅计划与到期（`GET /api/usage/quotas`）。
 *
 * 契约（`quotaCard`）：返回体是 `{ "<user>": {provider,name,icon,user,alias,plan,until,balance,…} }`。
 * 手机端此前只用过 `/api/usage/quotas/history`（读数点），所以**订阅计划与到期日完全看不到**；
 * 本客户端补的就是这两个字段。
 *
 * 只取 `provider / plan / until / balance`——`quotaCard` 里没有 cookie/token，
 * `user` 是账号标签（渠道页本来就在显示），这里只作为分组键，不额外渲染邮箱类完整标识。
 */
data class QuotaPlan(
    val provider: String,
    val plan: String,
    val untilMs: Long,
    val balance: String,
)

class QuotaPlanApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun fetchPlans(status: GatewayStatus): List<QuotaPlan> {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/usage/quotas").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                throw ManagementApiException("http_$code", "读取订阅信息失败（HTTP $code）")
            }
            return parseQuotaPlans(connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) })
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

/**
 * 解析订阅卡片。
 *
 * **真实形状是数组**：`handleUsageQuotas` 直接 `writeJSON(cards)`，即 `[{provider,plan,until,balance,…}]`。
 * （`/api/login/usage` 那种"以 user 为键的对象"是另一个接口，这里作为兼容分支处理。）
 *
 * 两种形状都只取 `provider/plan/until/balance`，`user` 一律不保留。
 */
internal fun parseQuotaPlans(body: String): List<QuotaPlan> {
    val trimmed = body.trim()
    val cards = ArrayList<JSONObject>()
    try {
        when {
            trimmed.startsWith("[") -> {
                val array = org.json.JSONArray(trimmed)
                for (i in 0 until array.length()) {
                    array.optJSONObject(i)?.let { cards.add(it) }
                }
            }
            trimmed.startsWith("{") -> {
                val root = JSONObject(trimmed)
                val keys = root.keys()
                while (keys.hasNext()) {
                    root.optJSONObject(keys.next())?.let { cards.add(it) }
                }
            }
            else -> throw JSONException("unexpected body")
        }
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "订阅信息返回格式不正确")
    }

    val plans = ArrayList<QuotaPlan>()
    cards.forEach { card ->
        val plan = card.optString("plan").trim()
        val until = card.optLong("until", 0L)
        if (plan.isEmpty() && until <= 0L) {
            return@forEach // 这张卡没有订阅信息，别拿空格子占屏幕
        }
        plans.add(
            QuotaPlan(
                provider = card.optString("provider").trim(),
                plan = plan,
                untilMs = until,
                balance = card.optString("balance").trim(),
            ),
        )
    }
    return plans.sortedWith(compareBy({ it.provider }, { it.untilMs }))
}

/**
 * 到期日只写日期，不写"还剩多少天"——剩余天数会随时间变化，
 * 页面上的静态文字很容易变成谎话；日期不会。
 */
internal fun quotaPlanLine(plan: QuotaPlan, nowMs: Long): String {
    val provider = plan.provider.ifBlank { "未知渠道" }
    val head = if (plan.plan.isBlank()) provider else "$provider · ${plan.plan}"
    if (plan.untilMs <= 0L) {
        return "$head · 没有到期日"
    }
    val days = TimeUnit.MILLISECONDS.toDays(plan.untilMs - nowMs)
    val date = java.time.Instant.ofEpochMilli(plan.untilMs)
        .atZone(java.time.ZoneId.systemDefault())
        .toLocalDate()
        .toString()
    return when {
        days < 0 -> "$head · 已于 $date 到期"
        days == 0L -> "$head · 今天（$date）到期"
        else -> "$head · $date 到期"
    }
}

/** 没有订阅信息时的结论。 */
internal fun quotaPlanSummary(plans: List<QuotaPlan>): String =
    if (plans.isEmpty()) "本机账号没有订阅计划或到期日" else "共 ${plans.size} 个账号有订阅信息"
