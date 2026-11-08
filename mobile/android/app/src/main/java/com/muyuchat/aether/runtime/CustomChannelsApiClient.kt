package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 自定义渠道（`custom-channels`）：把「地址 + 模型名」注册成一个**新渠道行**。
 *
 * 两个端点的分工（后端已把「自定义供应商」并进来）：
 * - 只建渠道定义 → `POST /api/custom-channels`（渠道进 provider 注册表，id = `custom-<slug>`）；
 * - 同时建账号 → `POST /api/provider/save`（后端 `provider_api.go:206-252` 对未注册 id
 *   先 `upsertCustomChannelFromDraft` 再 `createAccount`，一次请求两件事都做完）。
 *   草稿填了 `apiKey` 时本客户端就走这条路（见 [CustomChannelDraft.createsAccount]）。
 * 两条路径都**不把密钥写进渠道定义**：密钥存在账号里（加密），删渠道不删账号。
 *
 * 字段以 `internal/core/gateway/custom_channels.go` 的 `CustomChannel` 为准：
 * `id / name / base_url / protocol / models / headers / created_at`。
 */
data class CustomChannel(
    val id: String,
    val name: String,
    val baseUrl: String,
    val protocol: String,
    val models: List<String>,
    val headers: Map<String, String>,
    val createdAt: Long,
)

/** 协议选项；`value` 直接进请求体（服务端只认 openai/anthropic/responses）。 */
data class CustomChannelProtocol(val value: String, val label: String)

internal val CUSTOM_CHANNEL_PROTOCOLS: List<CustomChannelProtocol> = listOf(
    CustomChannelProtocol("openai", "OpenAI"),
    CustomChannelProtocol("anthropic", "Anthropic"),
    CustomChannelProtocol("responses", "Responses"),
)

/** 桌面 `customchannels.js:23-28` 的 protocolLabel 同口径。 */
internal fun customChannelProtocolLabel(raw: String): String = when (raw.trim().lowercase()) {
    "anthropic", "messages" -> "Anthropic"
    "responses" -> "Responses"
    else -> "OpenAI"
}

/** 自定义渠道表单草稿。`id` 留空表示由服务端/本地按名称生成 slug。 */
data class CustomChannelDraft(
    val name: String = "",
    val id: String = "",
    val baseUrl: String = "",
    val protocol: String = "openai",
    val modelsText: String = "",
    val headersText: String = "",
    /**
     * 可选 API Key：填了就走 `POST /api/provider/save`，**一次请求同时建渠道 + 建账号**
     * （后端 `provider_api.go:206-252` 对未注册 id 先 `upsertCustomChannelFromDraft`
     * 再 `createAccount`）。留空则只建渠道定义（`POST /api/custom-channels`）。
     *
     * 密钥本身不进渠道配置：它存在账号里（加密），删渠道不删账号。
     */
    val apiKey: String = "",
) {
    /** 是否要「建渠道 + 建账号」一步到位。 */
    val createsAccount: Boolean get() = apiKey.isNotBlank()
}

/** 探测结果：`POST /api/custom-channels/probe` 的解析结果。 */
data class CustomChannelProbe(
    val ok: Boolean,
    val models: List<String>,
    val error: String,
    val status: Int,
)

/** Base URL 归一：去空白、去尾部 `/`（服务端 `normalizeCustomChannel` 同口径）。 */
internal fun normalizeCustomChannelBaseUrl(raw: String): String = raw.trim().trimEnd('/')

/** 服务端 `validCustomBaseURL` 同口径：必须是 http(s) 绝对地址、不含空白。 */
internal fun validateCustomChannelBaseUrl(raw: String): String? {
    val url = raw.trim()
    if (url.isEmpty()) {
        return "请填写 Base URL"
    }
    if (!url.startsWith("http://") && !url.startsWith("https://")) {
        return "Base URL 必须以 http:// 或 https:// 开头"
    }
    if (url.any { it == ' ' || it == '\t' || it == '\n' || it == '\r' }) {
        return "Base URL 不能包含空白字符"
    }
    return null
}

/**
 * 服务端 `slugifyCustomID` 的 Kotlin 镜像：
 * 小写 → 非 [a-z0-9] 收敛成单个 `-` → 去首尾 `-` → 截断 40 字符。
 */
/**
 * 把用户输入变成渠道 id 片段。
 *
 * 允许：ASCII 字母数字 + 中日韩文字；其余折成单个 `-`。
 * 放中文进来是因为「公司中转」这类名字此前会被清空，只能回退成 `relay-<hash>`，
 * 用户看到模型前缀 `custom-relay-3f2a1b/xxx` 完全认不出是自己的渠道。
 * 与后端 `slugifyCustomID`（internal/core/gateway/custom_channels.go）保持同一规则。
 */
internal fun slugifyCustomChannelId(raw: String): String {
    val out = StringBuilder()
    var lastDash = false
    for (ch in raw.trim().lowercase()) {
        val keep = ch in 'a'..'z' || ch in '0'..'9' ||
            ch.code in 0x2E80..0x9FFF ||   // CJK 部首扩展、汉字
            ch.code in 0x3040..0x30FF ||   // 日文假名
            ch.code in 0xAC00..0xD7AF      // 韩文音节
        if (keep) {
            out.append(ch)
            lastDash = false
        } else if (!lastDash && out.isNotEmpty()) {
            out.append('-')
            lastDash = true
        }
    }
    // 按字符（不是字节）截断，避免切断多字节字符。
    return out.toString().trim('-').take(40).trim('-')
}

/** 手填 ID 的本地校验（留空合法 = 自动生成）。 */
internal fun validateCustomChannelId(raw: String): String? {
    val id = raw.trim()
    if (id.isEmpty()) {
        return null
    }
    if (slugifyCustomChannelId(id).isEmpty()) {
        return "渠道 ID 至少要包含一个字母或数字"
    }
    return null
}

/** 模型清单：逗号（含全角/顿号）或换行分隔；去重保序（桌面 `splitModels` 同口径）。 */
internal fun parseCustomChannelModels(raw: String): List<String> {
    val out = ArrayList<String>()
    raw.split(',', '，', '、', '\n').forEach { token ->
        val model = token.trim()
        if (model.isNotEmpty() && !out.contains(model)) {
            out.add(model)
        }
    }
    return out
}

/** Headers 解析结果；[error] 非空时带**行号**（1-based）。 */
internal data class CustomChannelHeaders(
    val headers: Map<String, String> = emptyMap(),
    val error: String? = null,
)

private val CUSTOM_HEADER_KEY = Regex("^[A-Za-z0-9-]{1,64}$")

private val MANAGED_HEADER_NAMES = setOf(
    "authorization", "host", "content-length", "x-api-key", "api-key",
)

/**
 * 每行 `Key: Value`，`#` 开头为注释；**只按第一个冒号切**，值里允许冒号。
 *
 * 与桌面 `customchannels.js:42-54` 的宽松解析不同：格式错要报**行号**，
 * 不能静默丢掉（用户会以为请求头已经生效）。
 */
internal fun parseCustomChannelHeaders(text: String): CustomChannelHeaders {
    val out = LinkedHashMap<String, String>()
    val lines = text.split('\n')
    for (index in lines.indices) {
        val raw = lines[index].trim()
        if (raw.isEmpty() || raw.startsWith("#")) {
            continue
        }
        val lineNo = index + 1
        val colon = raw.indexOf(':')
        if (colon <= 0) {
            return CustomChannelHeaders(error = "第 $lineNo 行格式错误：应为「Key: Value」")
        }
        val key = raw.substring(0, colon).trim()
        val value = raw.substring(colon + 1).trim()
        if (key.isEmpty()) {
            return CustomChannelHeaders(error = "第 $lineNo 行缺少请求头名")
        }
        if (!CUSTOM_HEADER_KEY.matches(key)) {
            return CustomChannelHeaders(
                error = "第 $lineNo 行请求头名「$key」不合法（只允许字母/数字/连字符）",
            )
        }
        if (key.lowercase() in MANAGED_HEADER_NAMES) {
            return CustomChannelHeaders(
                error = "第 $lineNo 行请求头「$key」由网关管理，不能在自定义渠道里覆盖",
            )
        }
        out[key] = value
    }
    return CustomChannelHeaders(headers = out)
}

/** headers 反解成编辑用文本（桌面 `headersToText`）。 */
internal fun customChannelHeadersToText(headers: Map<String, String>): String =
    headers.entries.joinToString("\n") { (k, v) -> "$k: $v" }

/**
 * 保存请求体：`{name, base_url, protocol, models, headers, id?}`。
 *
 * 注意**没有 api_key**：密钥只存在账号里；探测时才用 [buildCustomChannelProbeJson] 临时传。
 */
internal fun buildCustomChannelJson(draft: CustomChannelDraft): String {
    val json = JSONObject()
        .put("name", draft.name.trim())
        .put("base_url", normalizeCustomChannelBaseUrl(draft.baseUrl))
        .put("protocol", draft.protocol.trim().ifBlank { "openai" })
    val models = parseCustomChannelModels(draft.modelsText)
    if (models.isNotEmpty()) {
        json.put("models", JSONArray(models))
    }
    val parsedHeaders = parseCustomChannelHeaders(draft.headersText)
    if (parsedHeaders.headers.isNotEmpty()) {
        json.put("headers", JSONObject(parsedHeaders.headers))
    }
    val id = draft.id.trim()
    if (id.isNotEmpty()) {
        json.put("id", id)
    }
    return json.toString()
}

/**
 * 「建渠道 + 建账号」的请求体：`POST /api/provider/save`
 * `{id, name, key, <chat|responses|anthropic>, models?, headers?}`
 * （`providerDraft` 字段见 `provider_api.go:23-92`）。
 *
 * 协议映射：openai → `chat`，anthropic → `anthropic`，responses → `responses`
 * —— 与 `upsertCustomChannelFromDraft`（`provider_api.go:207-215`）的判断顺序一致。
 */
internal fun buildCustomChannelProviderSaveJson(draft: CustomChannelDraft): String {
    val url = normalizeCustomChannelBaseUrl(draft.baseUrl)
    val json = JSONObject()
        .put("name", draft.name.trim())
        .put("key", draft.apiKey.trim())
    val id = slugifyCustomChannelId(draft.id.ifBlank { draft.name })
    if (id.isNotEmpty()) {
        json.put("id", id)
    }
    when (draft.protocol.trim().lowercase()) {
        "anthropic", "messages" -> json.put("anthropic", url)
        "responses" -> json.put("responses", url)
        else -> json.put("chat", url)
    }
    val models = parseCustomChannelModels(draft.modelsText)
    if (models.isNotEmpty()) {
        json.put("models", JSONArray(models))
    }
    val parsedHeaders = parseCustomChannelHeaders(draft.headersText)
    if (parsedHeaders.headers.isNotEmpty()) {
        json.put("headers", JSONObject(parsedHeaders.headers))
    }
    return json.toString()
}

/** 探测请求体：`{base_url, api_key?}`；Key 只用于这一次探测，不落库。 */internal fun buildCustomChannelProbeJson(baseUrl: String, apiKey: String): String {
    val json = JSONObject().put("base_url", normalizeCustomChannelBaseUrl(baseUrl))
    val key = apiKey.trim()
    if (key.isNotEmpty()) {
        json.put("api_key", key)
    }
    return json.toString()
}

/** 表单校验：返回第一条人类可读的问题；null 表示可提交。 */
internal fun validateCustomChannelDraft(draft: CustomChannelDraft): String? {
    if (draft.name.trim().isEmpty()) {
        return "请填写渠道名称"
    }
    validateCustomChannelId(draft.id)?.let { return it }
    validateCustomChannelBaseUrl(draft.baseUrl)?.let { return it }
    if (draft.protocol.trim().lowercase() !in CUSTOM_CHANNEL_PROTOCOLS.map { it.value }) {
        return "协议只能是 openai / anthropic / responses"
    }
    parseCustomChannelHeaders(draft.headersText).error?.let { return it }
    // 填了 Key 就等于要建账号：Key 不能是带空白的半截字符串。
    val key = draft.apiKey.trim()
    if (draft.apiKey.isNotBlank() && (key.isEmpty() || key.any { it.isWhitespace() })) {
        return "API Key 不能包含空白字符"
    }
    return null
}

/** `GET /api/custom-channels` 的解析；坏 JSON 由上层归一成 `invalid_response`。 */
internal fun parseCustomChannels(body: String): List<CustomChannel> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: org.json.JSONException) {
        throw ManagementApiException("invalid_response", "自定义渠道返回的数据格式不正确")
    }
    val array = root.optJSONArray("channels") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        val name = item.optString("name").trim()
        if (id.isEmpty() && name.isEmpty()) {
            return@mapNotNull null
        }
        val models = item.optJSONArray("models")?.let { list ->
            (0 until list.length()).mapNotNull { i ->
                list.optString(i).trim().takeIf { it.isNotEmpty() }
            }
        }.orEmpty()
        val headers = LinkedHashMap<String, String>()
        item.optJSONObject("headers")?.let { obj ->
            obj.keys().forEach { key ->
                val value = obj.optString(key)
                if (key.isNotBlank()) {
                    headers[key.trim()] = value
                }
            }
        }
        CustomChannel(
            id = id,
            name = name.ifBlank { id },
            baseUrl = item.optString("base_url").trim(),
            protocol = customChannelProtocolValue(item.optString("protocol")),
            models = models,
            headers = headers,
            createdAt = item.optLong("created_at", 0L),
        )
    }
}

/** 保存回包 `{channel:{...}}` 的解析。 */
internal fun parseCustomChannel(body: String): CustomChannel {
    val root = try {
        JSONObject(body.trim())
    } catch (_: org.json.JSONException) {
        throw ManagementApiException("invalid_response", "自定义渠道返回的数据格式不正确")
    }
    val item = root.optJSONObject("channel")
        ?: throw ManagementApiException("invalid_response", "自定义渠道返回里没有 channel")
    val list = parseCustomChannels(JSONObject().put("channels", JSONArray().put(item)).toString())
    return list.firstOrNull()
        ?: throw ManagementApiException("invalid_response", "自定义渠道返回里没有可用渠道")
}

/** `POST /api/custom-channels/probe` 的解析（服务端永远 200，用 ok 区分）。 */
internal fun parseCustomChannelProbe(body: String): CustomChannelProbe {
    val root = try {
        JSONObject(body.trim())
    } catch (_: org.json.JSONException) {
        throw ManagementApiException("invalid_response", "探测模型返回的数据格式不正确")
    }
    val models = root.optJSONArray("models")?.let { list ->
        (0 until list.length()).mapNotNull { i ->
            list.optString(i).trim().takeIf { it.isNotEmpty() }
        }
    }.orEmpty()
    return CustomChannelProbe(
        ok = root.optBoolean("ok", false),
        models = models,
        error = root.optString("error").trim(),
        status = root.optInt("status", 0),
    )
}

private fun customChannelProtocolValue(raw: String): String = when (raw.trim().lowercase()) {
    "anthropic", "messages" -> "anthropic"
    "responses" -> "responses"
    else -> "openai"
}

/**
 * `custom-channels` 的最小客户端。
 *
 * 安全：探测用的 apiKey 只作为一次请求体出现；不打印、不落盘、不写剪贴板。
 */
class CustomChannelsApiClient(
    private val connectTimeoutMs: Int = 7_000,
    private val readTimeoutMs: Int = 25_000,
) {
    fun list(status: GatewayStatus): List<CustomChannel> =
        parseCustomChannels(request(status, "GET", "/api/custom-channels", null))

    /**
     * 保存自定义渠道。
     *
     * - 填了 API Key → 走 `POST /api/provider/save`：后端对未注册 id 会先建/更新
     *   CustomChannel，再把账号挂到 `custom-<slug>`（`provider_api.go:206-252`），
     *   一次请求完成「建渠道 + 建账号」；
     * - 没填 Key → 只发 `POST /api/custom-channels`，只建渠道定义。
     *
     * 两条路径都会回一份渠道列表，这里统一重读一次，保证返回值是最新的完整定义
     * （`provider/save` 只回 providers 视图，不含 headers/base_url 的原始定义）。
     */
    fun save(status: GatewayStatus, draft: CustomChannelDraft): CustomChannel {
        if (!draft.createsAccount) {
            return parseCustomChannel(
                request(status, "POST", "/api/custom-channels", buildCustomChannelJson(draft)),
            )
        }
        request(
            status,
            "POST",
            "/api/provider/save",
            buildCustomChannelProviderSaveJson(draft),
        )
        val slug = slugifyCustomChannelId(draft.id.ifBlank { draft.name })
        return list(status).firstOrNull { it.id == slug } ?: CustomChannel(
            id = slug,
            name = draft.name.trim(),
            baseUrl = normalizeCustomChannelBaseUrl(draft.baseUrl),
            protocol = draft.protocol.trim().ifBlank { "openai" },
            models = parseCustomChannelModels(draft.modelsText),
            headers = parseCustomChannelHeaders(draft.headersText).headers,
            createdAt = 0L,
        )
    }

    fun delete(status: GatewayStatus, id: String) {
        request(
            status,
            "DELETE",
            "/api/custom-channels/" + URLEncoder.encode(id.trim(), Charsets.UTF_8.name()),
            null,
        )
    }

    fun probe(status: GatewayStatus, baseUrl: String, apiKey: String): CustomChannelProbe =
        parseCustomChannelProbe(
            request(
                status,
                "POST",
                "/api/custom-channels/probe",
                buildCustomChannelProbeJson(baseUrl, apiKey),
            ),
        )

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
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(400))
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

    private fun readLimited(reader: Reader, limit: Int = 1 shl 20): String {
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
