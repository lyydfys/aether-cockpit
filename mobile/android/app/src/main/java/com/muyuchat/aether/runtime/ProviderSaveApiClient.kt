package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 自定义渠道的「接口类型」。
 *
 * **服务端没有 `type` 字段**（`providerDraft` 的 json tag 里没有它，已逐行核对
 * `provider_api.go:23-92`）：`handleProviderSave` 按「哪一个 base URL 字段非空」
 * 选择内核协议渠道（`provider_api.go:125-155`）。所以这里的「类型」只是 UI 选项，
 * 落到请求体时写的是对应协议的 base URL 字段，绝不发送 `type`。
 */
enum class CustomProviderProtocol(
    /** 请求体里承载 Base URL 的字段名（服务端真字段）。 */
    val jsonField: String,
    /** LocalProviderSwitch 风格的接口类型文案。 */
    val label: String,
    /** 服务端最终把账号挂到哪个内置渠道（用于成功回执）。 */
    val coreProvider: String,
) {
    OPENAI_COMPAT("chat", "OpenAI 兼容", "openai-compat"),
    ANTHROPIC_COMPAT("anthropic", "Anthropic 兼容", "claude-sub"),
}

/** 表单只列这两种（用户点名 LocalProviderSwitch 的二选一）；服务端另支持 responses/gemini/decide，未在表单暴露。 */
internal val CUSTOM_PROVIDER_FORM_PROTOCOLS: List<CustomProviderProtocol> =
    listOf(CustomProviderProtocol.OPENAI_COMPAT, CustomProviderProtocol.ANTHROPIC_COMPAT)

/**
 * 自定义渠道草稿。真实字段：`name` / `key`（API Key）/ 协议字段（`chat` 或 `anthropic`）/
 * 可选 `models` 数组。`id` 留空（服务端空 id 必走「自定义供应商」分支；
 * 若拿内置渠道 id 提交，服务端会去改那个内置渠道的账号，所以默认不发 id）。
 */
data class CustomProviderDraft(
    val id: String = "",
    val name: String = "自定义渠道",
    val baseUrl: String = "",
    val apiKey: String = "",
    val protocol: CustomProviderProtocol = CustomProviderProtocol.OPENAI_COMPAT,
    /** 逗号分隔的模型清单（选填）；提交时切成去重数组。 */
    val modelsText: String = "",
)

/** 渠道 ID 若手动提供，只允许小写 slug（默认不用）。 */
internal val CUSTOM_PROVIDER_ID_PATTERN = Regex("^[a-z0-9][a-z0-9._-]{0,63}$")

/** 内置渠道 ID：自定义 id 与它们撞名时，服务端会改内置渠道账号，必须在本地拦下。 */
internal fun customProviderIdConflict(id: String, existingIds: Set<String>): Boolean =
    existingIds.any { it.equals(id.trim(), ignoreCase = true) }

/** Base URL 归一：去首尾空白、去掉尾部所有 `/`（`trimEndpoint` 同口径，`provider_api.go:116-123`）。 */
internal fun normalizeProviderBaseUrl(raw: String): String = raw.trim().trimEnd('/')

/** 逗号分隔模型清单 → 去空白、去重、保序（服务端 `cleanProviderModels` 同口径）。 */
internal fun parseProviderModelList(raw: String): List<String> {
    val out = ArrayList<String>()
    raw.split(',', '，', '\n').forEach { token ->
        val model = token.trim()
        if (model.isNotEmpty() && !out.contains(model)) {
            out.add(model)
        }
    }
    return out
}

/**
 * 表单校验。返回第一条人类可读的问题；null 表示可以提交。
 *
 * LocalProviderSwitch 顺序：名称 → 接口类型 → Base URL → API Key → 模型（选填）。
 * 必填项与服务端硬性要求一致：至少一个 Base URL；API Key 按桌面「手动凭据」口径必填。
 */
internal fun validateCustomProviderDraft(
    draft: CustomProviderDraft,
    existingIds: Set<String> = emptySet(),
): String? {
    val name = draft.name.trim()
    val id = draft.id.trim()
    val baseUrl = normalizeProviderBaseUrl(draft.baseUrl)
    val apiKey = draft.apiKey.trim()
    if (name.isEmpty()) {
        return "请填写渠道名称"
    }
    if (id.isNotEmpty()) {
        if (!CUSTOM_PROVIDER_ID_PATTERN.matches(id)) {
            return "渠道 ID 只能用小写字母、数字、点、下划线或连字符，长度 1-64"
        }
        if (customProviderIdConflict(id, existingIds)) {
            return "渠道 ID「$id」与内置渠道重名；换一个 ID，否则会改到内置渠道的账号"
        }
    }
    if (draft.protocol !in CUSTOM_PROVIDER_FORM_PROTOCOLS) {
        return "接口类型只支持 OpenAI 兼容 / Anthropic 兼容"
    }
    if (baseUrl.isEmpty()) {
        return "请填写 Base URL"
    }
    if (!baseUrl.startsWith("http://") && !baseUrl.startsWith("https://")) {
        return "Base URL 必须以 http:// 或 https:// 开头"
    }
    if (apiKey.isEmpty()) {
        return "请填写 API Key"
    }
    return null
}

/**
 * 请求体：`{name,key,<chat|anthropic>:baseUrl,models?}`。
 *
 * 服务端没有 `type` / `baseURL` / `apiKey` 这些键；`id` 留空时不会误撞内置渠道。
 */
internal fun buildProviderSaveJson(draft: CustomProviderDraft): String {
    val json = JSONObject()
        .put("name", draft.name.trim())
        .put("key", draft.apiKey.trim())
        .put(draft.protocol.jsonField, normalizeProviderBaseUrl(draft.baseUrl))
    val id = draft.id.trim()
    if (id.isNotEmpty()) {
        json.put("id", id)
    }
    val models = parseProviderModelList(draft.modelsText)
    if (models.isNotEmpty()) {
        json.put("models", JSONArray(models))
    }
    return json.toString()
}

/**
 * `POST /api/provider/save` 的最小客户端：只发自定义渠道需要的字段，不调 detect、
 * 不改任何其它渠道（`detect` 是可选探测，保存流程不依赖它，见 provider_detect.go）。
 *
 * 安全：apiKey 只作为请求体字段出现一次；不打印、不落盘、不写剪贴板。
 */
class ProviderSaveApiClient(
    private val connectTimeoutMs: Int = 8_000,
    private val readTimeoutMs: Int = 20_000,
) {
    fun save(status: GatewayStatus, draft: CustomProviderDraft) {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val body = buildProviderSaveJson(draft)
        val connection = (URL("http://${status.addr}/api/provider/save").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            // 成功回包是完整渠道列表（respondProviders）；调用方刷新列表取回。
            connection.inputStream.bufferedReader(Charsets.UTF_8).use { drain(it) }
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun drain(reader: Reader) {
        val buffer = CharArray(8 * 1024)
        var total = 0
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return
            }
            total += read
            if (total > 4 * 1024 * 1024) {
                throw ManagementApiException("invalid_response", "管理接口响应过大")
            }
        }
    }
}
