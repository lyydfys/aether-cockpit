package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * 脱敏「试一试」与「最近的脱敏记录」（`POST /api/masking/preview`、`GET /api/masking/records`）。
 *
 * 契约（`masking_api.go`）：
 * - preview 请求体 `{text}`，响应 `{text, hits[], off, blocked}`（`masking_api.go:439-456`）；
 * - records 响应 `{records:[{key,t,agent,provider,model,what,n,blocked,values[]}], more}`
 *   （`masking_api.go:270-337`，记录视图见 `:128-140`）。
 *
 * **原文不落盘**：记录里只有类型、长度、首字符与占位符（`masking_api.go:30`），
 * 所以这里只映射"命中几处、命中哪些类型"，一个原文值都不读。
 * 预览同样不写任何地方：引擎的 `Preview()` 明确"不记录、不写入内存映射"（`redact.go:870-872`）。
 */
data class MaskingPreviewHit(
    val kind: String,
    val placeholder: String,
    val action: String,
    val count: Int,
)

data class MaskingPreview(
    val text: String,
    val hits: List<MaskingPreviewHit>,
    val blocked: Boolean,
    /** 脱敏总开关当前是关闭的：结果只是"规则演示"，真发请求不会被改写。 */
    val off: Boolean,
)

data class MaskingRecord(
    val key: String,
    val atMs: Long,
    val provider: String,
    val agent: String,
    val model: String,
    /** `n` = 这条请求被脱敏的处数。 */
    val hitCount: Int,
    /** `values[].k` 去重后的命中类型（不看 p/h/l，那些字段不含原文也不展示）。 */
    val kinds: List<String>,
    val blocked: Boolean,
)

data class MaskingRecords(
    val records: List<MaskingRecord>,
    val more: Boolean,
)

/**
 * `/api/masking/records?limit=N`（默认 50 = 桌面 `misc.js:340` 的 `limit: '50'`）。
 * 服务端支持筛选（`masking_api.go:22`：`limit&before&kind&by&blocked&agent&provider`）。
 * 手机端把 kind / blocked / agent / provider 接出来给页面用；`by=model` 服务端目前直接返回空
 * （`masking_api.go:308` 「目前只有规则命中」），所以界面不提供 by 选项。
 */
internal fun maskingRecordsPath(
    limit: Int,
    kind: String = "",
    blockedOnly: Boolean = false,
    agent: String = "",
    provider: String = "",
    before: String = "",
): String {
    val out = StringBuilder("/api/masking/records?limit=").append(limit.coerceIn(1, 200))
    // 注意：服务端按 `h.Kind` 精确比较（`masking_api.go:319-327`），而 Kind 是大写
    // （`redact.go:1241` 的 `"EMAIL"`），所以这里**不能** lowercase，否则永远筛不到。
    val safeKind = kind.trim()
    if (safeKind.isNotEmpty()) {
        out.append("&kind=").append(java.net.URLEncoder.encode(safeKind, "UTF-8"))
    }
    if (blockedOnly) {
        out.append("&blocked=1")
    }
    if (agent.isNotBlank()) {
        out.append("&agent=").append(java.net.URLEncoder.encode(agent.trim(), "UTF-8"))
    }
    if (provider.isNotBlank()) {
        out.append("&provider=").append(java.net.URLEncoder.encode(provider.trim(), "UTF-8"))
    }
    if (before.isNotBlank()) {
        out.append("&before=").append(java.net.URLEncoder.encode(before.trim(), "UTF-8"))
    }
    return out.toString()
}

/** 脱敏汇总（`GET /api/masking/summary?days=7|30`，桌面 `misc.js:346-356` 的四块 KPI）。 */
data class MaskingTally(val name: String, val count: Int)

data class MaskingSummary(
    val requests: Int,
    val blocked: Int,
    val distinct: Int,
    val kinds: List<MaskingTally> = emptyList(),
    val providers: List<MaskingTally> = emptyList(),
    val agents: List<MaskingTally> = emptyList(),
)

/** 一次命中详情里的一轮消息（`GET /api/masking/sent?key=` 的 `body` 里解出来）。 */
data class MaskingSentMessage(val role: String, val content: String)

data class MaskingSent(
    val messages: List<MaskingSentMessage>,
    /** 服务端原样回的那段 JSON（解析不出结构时兜底展示）。 */
    val rawBody: String,
)

internal fun maskingSummaryPath(days: Int): String =
    "/api/masking/summary?days=" + if (days >= 30) 30 else 7

/**
 * 脱敏类型的中文标签（桌面 `misc.js:321-327` 的 `REDACT_KINDS`）。
 *
 * 注意 kind 本身是**大写**（`redact.go:1241` 「EMAIL」），筛选参数要传原值，只有显示用标签。
 */
internal val maskingKindLabels: Map<String, String> = mapOf(
    "API_KEY" to "密钥",
    "TOKEN" to "令牌",
    "SECRET" to "口令/鉴权头",
    "PASSWORD" to "密码",
    "PRIVATE_KEY" to "私钥",
    "JWT" to "JWT",
    "EMAIL" to "邮箱",
    "PHONE" to "手机号",
    "ID" to "身份证",
    "CARD" to "银行卡",
    "SSN" to "SSN",
    "PASSPORT" to "护照",
    "IBAN" to "IBAN",
    "BIRTHDAY" to "生日",
    "MAC" to "MAC",
    "SERIAL" to "序列号",
    "HOME" to "主目录用户名",
    "BUCKET" to "云存储桶",
    "USERHOST" to "用户名@主机",
    "IP_ADDR" to "公网 IP",
)

/** 未知类型原样显示（后端加了新类型时不至于显示空白）。 */
internal fun maskingKindLabel(kind: String): String = maskingKindLabels[kind] ?: kind

internal fun maskingSentPath(key: String): String =
    "/api/masking/sent?key=" + java.net.URLEncoder.encode(key.trim(), "UTF-8")

class MaskingApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun preview(status: GatewayStatus, text: String): MaskingPreview =
        parseMaskingPreview(
            request(
                status = status,
                path = "/api/masking/preview",
                method = "POST",
                body = JSONObject().put("text", text).toString(),
            ),
        )

    /**
     * 最近的脱敏记录（支持桌面同款筛选 `kind / blocked / agent / provider / before`）。
     *
     * 默认条数对齐桌面 `misc.js:340` 的 `limit: '50'`；服务端上限 200（`masking_api.go:282`）。
     */
    fun fetchRecords(
        status: GatewayStatus,
        limit: Int = 50,
        kind: String = "",
        blockedOnly: Boolean = false,
        agent: String = "",
        provider: String = "",
        before: String = "",
    ): MaskingRecords =
        parseMaskingRecords(
            request(
                status = status,
                path = maskingRecordsPath(limit, kind, blockedOnly, agent, provider, before),
                method = "GET",
            ),
        )

    /** 脱敏汇总：桌面隐私页顶部四块 KPI（`misc.js:346-356`）。 */
    fun fetchSummary(status: GatewayStatus, days: Int = 7): MaskingSummary =
        parseMaskingSummary(request(status = status, path = maskingSummaryPath(days), method = "GET"))

    /** 清空脱敏记录：`POST /api/masking/clear`（桌面 `misc.js:463`）。 */
    fun clearRecords(status: GatewayStatus) {
        request(status = status, path = "/api/masking/clear", method = "POST", body = "{}")
    }

    /** 一次命中的"发出去的那段内容"：`GET /api/masking/sent?key=`（桌面 `misc.js:552`）。 */
    fun fetchSent(status: GatewayStatus, key: String): MaskingSent =
        parseMaskingSent(request(status = status, path = maskingSentPath(key), method = "GET"))

    private fun request(status: GatewayStatus, path: String, method: String, body: String? = null): String {
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
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(4_096) }
                        ?.let { JSONObject(it) }
                }.getOrNull()
                throw ManagementApiException(
                    code = detail?.optString("code")?.trim().orEmpty().ifBlank { "http_$code" },
                    message = detail?.optString("error")?.trim().orEmpty().ifBlank { "管理接口返回 $code" }.take(200),
                )
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

internal fun parseMaskingPreview(body: String): MaskingPreview {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "脱敏预览返回格式不正确")
    }
    val hits = ArrayList<MaskingPreviewHit>()
    val array = root.optJSONArray("hits")
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            hits.add(
                MaskingPreviewHit(
                    kind = item.optString("kind").trim(),
                    placeholder = item.optString("placeholder").trim(),
                    action = item.optString("action").trim(),
                    count = item.optInt("n", 1).coerceAtLeast(1),
                ),
            )
        }
    }
    return MaskingPreview(
        text = root.optString("text"),
        hits = hits,
        blocked = root.optBoolean("blocked", false),
        off = root.optBoolean("off", false),
    )
}

internal fun parseMaskingRecords(body: String): MaskingRecords {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "脱敏记录返回格式不正确")
    }
    val array = root.optJSONArray("records")
    val records = ArrayList<MaskingRecord>()
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val kinds = LinkedHashSet<String>()
            val values = item.optJSONArray("values")
            if (values != null) {
                for (j in 0 until values.length()) {
                    val value = values.optJSONObject(j) ?: continue
                    val kind = value.optString("k").trim()
                    if (kind.isNotEmpty()) {
                        kinds.add(kind)
                    }
                }
            }
            records.add(
                MaskingRecord(
                    key = item.optString("key").trim(),
                    atMs = item.optLong("t", 0L).coerceAtLeast(0L),
                    provider = item.optString("provider").trim(),
                    agent = item.optString("agent").trim(),
                    model = item.optString("model").trim(),
                    hitCount = item.optInt("n", 0).coerceAtLeast(0),
                    kinds = kinds.toList(),
                    blocked = item.optBoolean("blocked", false),
                ),
            )
        }
    }
    return MaskingRecords(records = records, more = root.optBoolean("more", false))
}

/** 预览结论：命中几处、命中哪些类型；一处都没命中时直说。 */
internal fun maskingHitSummary(hits: List<MaskingPreviewHit>): String {
    if (hits.isEmpty()) {
        return "没有命中任何脱敏规则"
    }
    val places = hits.sumOf { it.count }
    val kinds = hits.map { it.kind }.filter { it.isNotEmpty() }.distinct()
    val kindText = if (kinds.isEmpty()) "类型未知" else kinds.joinToString("、")
    return "命中 $places 处 · $kindText"
}

/** 一条记录的右栏：处数 + 类型数；被拦下的记录标出来源。 */
internal fun maskingRecordHitLabel(record: MaskingRecord): String = buildString {
    append("${record.hitCount} 处")
    if (record.kinds.isNotEmpty()) {
        append(" · ${record.kinds.size} 类")
    }
    if (record.blocked) {
        append(" · 已拦截")
    }
}

/** 记录的左栏副行：来源与模型；都没有时为空串（不占一行）。 */
internal fun maskingRecordSourceLine(record: MaskingRecord): String = buildList {
    if (record.provider.isNotBlank()) {
        add(record.provider)
    }
    if (record.agent.isNotBlank()) {
        add(record.agent)
    }
    if (record.model.isNotBlank()) {
        add(record.model)
    }
}.joinToString(" · ")

/** 汇总解析：`{requests, blocked, distinct, kinds:[{name,n}], providers:[…], agents:[…]}`。 */
internal fun parseMaskingSummary(body: String): MaskingSummary {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "脱敏汇总返回格式不正确")
    }
    fun tally(key: String): List<MaskingTally> {
        val array = root.optJSONArray(key) ?: return emptyList()
        return (0 until array.length()).mapNotNull { i ->
            val item = array.optJSONObject(i) ?: return@mapNotNull null
            val name = item.optString("name").trim()
            if (name.isEmpty()) {
                null
            } else {
                MaskingTally(name = name, count = item.optInt("n", 0).coerceAtLeast(0))
            }
        }
    }
    return MaskingSummary(
        requests = root.optInt("requests", 0).coerceAtLeast(0),
        blocked = root.optInt("blocked", 0).coerceAtLeast(0),
        distinct = root.optInt("distinct", 0).coerceAtLeast(0),
        kinds = tally("kinds"),
        providers = tally("providers"),
        agents = tally("agents"),
    )
}

/**
 * 「这条命中发出去的内容」解析。
 *
 * 服务端回 `{"body": "<json 字符串>"}`（`masking_api.go:429-433`），`body` 里是
 * `{"messages":[{"role","content"}]}`；没有记录时回 `{}`。解析不出结构时保留原文。
 */
internal fun parseMaskingSent(body: String): MaskingSent {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "命中详情返回格式不正确")
    }
    val raw = root.optString("body").trim()
    if (raw.isEmpty()) {
        return MaskingSent(messages = emptyList(), rawBody = "")
    }
    val messages = ArrayList<MaskingSentMessage>()
    runCatching {
        val inner = JSONObject(raw)
        val array = inner.optJSONArray("messages")
        if (array != null) {
            for (i in 0 until array.length()) {
                val item = array.optJSONObject(i) ?: continue
                val content = item.optString("content")
                if (content.isNotEmpty()) {
                    messages.add(
                        MaskingSentMessage(
                            role = item.optString("role").trim().ifBlank { "user" },
                            content = content,
                        ),
                    )
                }
            }
        }
    }
    return MaskingSent(messages = messages, rawBody = raw)
}

/** 记录时间：只写本地 `MM-dd HH:mm`，不写"几分钟前"（历史记录不该随时间漂移）。 */
internal fun maskingTimestamp(atMs: Long, zone: ZoneId = ZoneId.systemDefault()): String {
    if (atMs <= 0L) {
        return "时间未知"
    }
    val formatter = DateTimeFormatter.ofPattern("MM-dd HH:mm")
    return Instant.ofEpochMilli(atMs).atZone(zone).format(formatter)
}

/** 记录为空：为什么空 + 下一步（不写"暂无数据"）。 */
internal fun maskingRecordsEmptyDetail(): String =
    "为什么空：脱敏记录只在**请求真的经过网关**时产生，而且只留在本机内存里（网关重启即清空）。" +
        "下一步：先开启「脱敏引擎」并发出一次请求，再回来刷新；禁用规则也不会产生记录。"

/** 开关关闭时预览仍然有效，但必须说清"真发请求不会被改写"。 */
internal fun maskingPreviewOffNote(preview: MaskingPreview): String =
    if (!preview.off) {
        ""
    } else {
        "注意：脱敏引擎当前是关闭的，下面只是规则演示；真发出去的请求不会被改写。"
    }

/** 命中拦截规则时的提示（与桌面同语义：预览也换成占位符，不会出现原文）。 */
internal fun maskingPreviewBlockedNote(preview: MaskingPreview): String =
    if (!preview.blocked) {
        ""
    } else {
        "这段文本命中「拦截」规则：真发请求会被拒绝（403），预览里已换成占位符。"
    }
