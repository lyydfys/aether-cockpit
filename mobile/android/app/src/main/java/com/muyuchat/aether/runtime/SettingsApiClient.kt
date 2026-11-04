package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL

/**
 * Only allowlisted, already-masked settings leave this layer. Secrets
 * (webdav/otlp headers/tools keys/remote quota ciphertext) are never mapped.
 */
data class RedactRule(
    val id: String,
    val name: String,
    val kind: String,
    val action: String,
    val enabled: Boolean,
)

data class RedactDetail(
    val enabled: Boolean,
    val rules: List<RedactRule>,
    val actions: Map<String, String>,
)

/** 用户自定义规则。prefix 与 regex 二选一，服务端 NormalizeKind 后再回读。 */
data class RedactCustomRule(
    val kind: String,
    val prefix: String = "",
    val regex: String = "",
)

data class SettingsSummary(
    val addr: String,
    val port: Int,
    val lanEnabled: Boolean,
    val lanUrls: List<String>,
    val proxyMasked: String,
    val corsOrigins: List<String>,
    val allowIPs: List<String>,
    val redactEnabled: Boolean,
    val redactPersonal: Boolean,
    val redactActions: List<String>,
    val redactRuleCount: Int,
    val redactAllowCount: Int,
    val desktopApp: Boolean,
    val customRules: List<RedactCustomRule> = emptyList(),
    // —— 桌面 settings.js「常规」tab 的字段（`settingsView()` 由 `maskSettings` + `settings.ui` 合并而来，
    //    `lan.go:213-221`；这些键都在里面，不是盲猜）——
    /** `settings.ui.theme`；空 = 跟随系统。 */
    val uiTheme: String = "",
    /** `monitor_minutes`：渠道自动探测（桌面 `settings.js:198` / `:207-209`）。 */
    val monitorMinutes: Int = 0,
    /** `keep_awake`：桌面宿主防休眠开关（手机端桌面专属）。 */
    val keepAwake: Boolean = false,
    /** `wsl_disabled`：WSL 接入关闭态（手机端恒不可用）。 */
    val wslDisabled: Boolean = false,
    /** `autostart.supported`：桌面宿主注册表/LaunchAgent 能力，手机端为 false。 */
    val autostartSupported: Boolean = false,
    /** —— 隐私（`settings.js:448-487`）：三个键都由 settingsView 回传 —— */
    /** `settings.ui.redactShape`：保留格式的假值（`masking_api.go:489` 读的就是这个键）。 */
    val redactShape: Boolean = false,
    /** `privacy_mask.enabled`：响应回填。 */
    val privacyMaskEnabled: Boolean = false,
    /** `settings.ui.hooks`：请求管线钩子（`pipeline_hooks.go:44` 读同一个键）。 */
    val hooks: PipelineHooks = PipelineHooks(),
    /** `otlp`：OTLP 导出（headers 会被服务端整体打码，只回 `headersMasked`）。 */
    val otlp: OtlpConfig = OtlpConfig(),
    /** `tools`：网络搜索 / 图像 / 音频 / 视频 / 实时语音（密钥只回「是否已保存」）。 */
    val tools: ToolsConfig = ToolsConfig(),
)

/** 请求管线钩子（桌面 `settings.js:468-485` 四项 + 服务端第五项 `guard_accounts`）。 */
data class PipelineHooks(
    val dropReasoning: Boolean = false,
    /** 0 = 不限制。 */
    val clampOutput: Int = 0,
    val blockModels: List<String> = emptyList(),
    val guardProviders: List<String> = emptyList(),
    /**
     * `guard_accounts`：服务端 `syncHooks` 也认这个键（`pipeline_hooks.go:61-63`）。
     *
     * 桌面保存用 `{...hooksCfg}` 整体回写，会**原样保留**它；手机端若只回写前四项，
     * 用户点一次「丢弃推理增量」就会把它悄悄删掉。所以必须带着往返（页面不提供编辑入口）。
     */
    val guardAccounts: List<String> = emptyList(),
)

/** 模型重定向（桌面「模型目录」tab，`settings.js:400-446`）：虚拟名 → {渠道: 上游模型名}。 */
data class ModelRedirects(val redirects: Map<String, Map<String, String>> = emptyMap())

/**
 * OTLP 导出配置（桌面 `settings.js:504-540`）。
 *
 * 语义已核：保存时 `headers` **未携带**或为打码形态（`{"___masked___":"true"}`）→ 服务端沿用旧值
 * （`settings.go:515-516`、`:716-729`）。所以客户端只写 endpoint/批参数是安全的；
 * 要清空 headers 就显式发 `{}`。
 */
data class OtlpConfig(
    val endpoint: String = "",
    val batchSize: Int = 0,
    val flushSeconds: Int = 0,
    val queueMax: Int = 0,
    val headersMasked: Boolean = false,
) {
    val enabled: Boolean get() = endpoint.isNotBlank()
}

/** 工具服务（桌面 `settings.js:275-398`）：只读投影，密钥只回「是否已保存」。 */
data class ToolsConfig(
    val webSearchProvider: String = "",
    val webSearchEndpoint: String = "",
    val webSearchMaxResults: Int = 0,
    val webSearchKeyOrder: String = "",
    val webSearchKeyCount: Int = 0,
    val imageBaseUrl: String = "",
    val imageModel: String = "",
    val imageKeySaved: Boolean = false,
    val audioBaseUrl: String = "",
    val audioModel: String = "",
    val audioKeySaved: Boolean = false,
    val videoBaseUrl: String = "",
    val videoModel: String = "",
    val videoKeySaved: Boolean = false,
    val realtimeBaseUrl: String = "",
    val realtimeModel: String = "",
    val realtimeKeySaved: Boolean = false,
)

/** 一个请求预设（桌面 `settings.js:847-884`）。 */
data class PresetSummary(
    val id: String,
    val name: String,
    val description: String = "",
    val enabled: Boolean = true,
    val bodyCount: Int = 0,
    val headerCount: Int = 0,
    val hasSystemPrompt: Boolean = false,
    val attachedProviders: Int = 0,
    val attachedRoutes: Int = 0,
)

class SettingsApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    fun fetchSettings(status: GatewayStatus): SettingsSummary {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}/api/settings").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", "管理接口返回 $code")
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseSettings(body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    fun fetchRedactDetail(status: GatewayStatus): RedactDetail {
        val body = requestJson(status, "/api/settings/redact-actions", "GET", null)
        return parseRedactDetail(body)
    }

    /** GET /api/settings 是自定义规则的唯一读取入口；redact-actions 只返回内置规则。 */
    fun fetchCustomRules(status: GatewayStatus): List<RedactCustomRule> =
        parseSettings(requestJson(status, "/api/settings", "GET", null)).customRules

    /* ---------------- 桌面「常规 / 隐私 / 模型目录」三个 tab 的写操作 ----------------
     * 全部走 `POST /api/settings`（`settings_ui.go` 的合法键表：`ui` / `privacy_mask` /
     * `keep_awake` / `wsl_disabled` / `monitor_minutes` 都是保留键，非保留键合并进 `settings.ui`）。
     * 每个方法都回读完整设置，失败时由 requestJson 抛出服务端原文。
     */

    /** 主题：`settings.ui.theme`（`auto` / `light` / `dark`）。 */
    fun setTheme(status: GatewayStatus, theme: String): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildThemeJson(theme)))

    /** 渠道自动探测间隔（分钟，0 = 关闭）。 */
    fun setMonitorMinutes(status: GatewayStatus, minutes: Int): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildMonitorMinutesJson(minutes)))

    /** 防休眠（桌面宿主能力；手机端只做展示与置灰）。 */
    fun setKeepAwake(status: GatewayStatus, enabled: Boolean): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildKeepAwakeJson(enabled)))

    /** WSL 接入开关（桌面宿主能力；手机端置灰）。 */
    fun setWslEnabled(status: GatewayStatus, enabled: Boolean): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildWslDisabledJson(!enabled)))

    /** 保留格式的假值（`settings.ui.redactShape`）。 */
    fun setRedactShape(status: GatewayStatus, enabled: Boolean): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildRedactShapeJson(enabled)))

    /** 响应回填（`privacy_mask.enabled`）。 */
    fun setPrivacyMask(status: GatewayStatus, enabled: Boolean): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildPrivacyMaskJson(enabled)))

    /** 请求钩子四项一次写回（桌面 `saveHooks` 同口径：整对象覆盖）。 */
    fun saveHooks(status: GatewayStatus, hooks: PipelineHooks): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildHooksJson(hooks)))

    /** 模型重定向（`GET /api/model-redirects`）。 */
    fun fetchModelRedirects(status: GatewayStatus): ModelRedirects =
        parseModelRedirectsSnapshot(requestJson(status, "/api/model-redirects", "GET", null))

    /** 模型重定向保存（`POST /api/model-redirects {redirects}`）。 */
    fun saveModelRedirects(status: GatewayStatus, redirects: Map<String, Map<String, String>>): ModelRedirects =
        parseModelRedirectsSnapshot(
            requestJson(status, "/api/model-redirects", "POST", buildModelRedirectsJson(redirects)),
        )

    /** 刷新全部渠道模型目录（`POST /api/providers/models/refresh-all`，桌面 `settings.js:430`）。 */
    fun refreshAllModelCatalogs(status: GatewayStatus): String {
        val body = requestJson(status, "/api/providers/models/refresh-all", "POST", "{}")
        val root = runCatching { org.json.JSONObject(body.trim()) }.getOrNull()
        val refreshed = root?.optInt("refreshed", 0) ?: 0
        val failed = root?.optInt("failed", 0) ?: 0
        return if (failed > 0) "模型目录：$refreshed 个渠道成功，$failed 个失败" else "模型目录：$refreshed 个渠道已刷新"
    }

    /** OTLP 保存（不带 headers → 服务端沿用旧鉴权头；`clearHeaders` 显式清空）。 */
    fun saveOtlp(status: GatewayStatus, otlp: OtlpConfig, clearHeaders: Boolean = false): SettingsSummary =
        parseSettings(
            requestJson(
                status,
                "/api/settings",
                "POST",
                buildOtlpJson(otlp.endpoint, otlp.batchSize, otlp.flushSeconds, otlp.queueMax, clearHeaders),
            ),
        )

    /** 预设列表（`GET /api/presets`）。 */
    fun fetchPresets(status: GatewayStatus): PresetsSnapshot =
        parsePresetsSnapshot(requestJson(status, "/api/presets", "GET", null))

    /** 启停预设：整对象回写（只改 enabled）。 */
    fun togglePreset(status: GatewayStatus, snapshot: PresetsSnapshot, id: String, enabled: Boolean): PresetsSnapshot {
        val payload = presetToggleBody(snapshot.rawBody, id, enabled)
            ?: throw ManagementApiException("preset_missing", "预设「$id」已不存在，请刷新列表")
        return parsePresetsSnapshot(requestJson(status, "/api/presets/save", "POST", payload))
    }

    /** 删除预设（二次确认在页面上，不在这里）。 */
    fun deletePreset(status: GatewayStatus, id: String): PresetsSnapshot =
        parsePresetsSnapshot(requestJson(status, "/api/presets/delete", "POST", buildPresetDeleteJson(id)))

    /** 网络搜索测试（桌面 `settings.js:297-306`）→ 一行人类可读结果；失败抛服务端原文。 */
    fun testWebSearch(status: GatewayStatus): String {
        val body = requestJson(
            status,
            "/api/tools/web-search",
            "POST",
            "{\"query\":\"Aether Cockpit\",\"max_results\":3}",
        )
        val root = runCatching { org.json.JSONObject(body.trim()) }.getOrNull()
        val results = root?.optJSONArray("results")
        val provider = root?.optString("provider").orEmpty().trim()
        val first = results?.optJSONObject(0)?.optString("title").orEmpty().trim()
        return buildString {
            append("搜索成功")
            if (provider.isNotEmpty()) append(" · ").append(provider)
            append(" · ").append(results?.length() ?: 0).append(" 条")
            if (first.isNotEmpty()) append(" · ").append(first)
        }
    }

    /** 图像服务测试（桌面 `settings.js:355-362`）→ 服务端 summary 或通用成功文案。 */
    fun testImageService(status: GatewayStatus): String {
        val body = requestJson(status, "/api/tools/image-test", "POST", "{}")
        val root = runCatching { org.json.JSONObject(body.trim()) }.getOrNull()
        return root?.optString("summary").orEmpty().trim().ifBlank { "图像测试成功" }
    }

    fun replaceRedactRules(status: GatewayStatus, rules: List<RedactCustomRule>): List<RedactCustomRule> =
        parseSettings(requestJson(status, "/api/settings/redact-rules", "POST", buildRedactRulesJson(rules))).customRules

    /**
     * 工具服务保存（`POST /api/settings {tools}`）。`savedKeys` 表示哪些子服务的 Key 已存在
     * （来自 GET 的掩码判定），用于决定"留空"时是否回传哨兵 `******`。
     */
    fun saveTools(
        status: GatewayStatus,
        draft: ToolsDraft,
        savedKeys: Map<String, Boolean>,
    ): SettingsSummary =
        parseSettings(requestJson(status, "/api/settings", "POST", buildToolsJson(draft, savedKeys)))

    /** 预设保存（新建 id 传空，服务端按名称生成；编辑传原 id）。 */
    fun savePreset(status: GatewayStatus, draft: PresetDraft): PresetsSnapshot {
        val payload = buildPresetSaveJson(draft)
            ?: throw ManagementApiException("invalid_preset", "预设内容不合法，未提交")
        return parsePresetsSnapshot(requestJson(status, "/api/presets/save", "POST", payload))
    }

    fun editRedactRule(
        status: GatewayStatus,
        at: Int,
        was: RedactCustomRule,
        rule: RedactCustomRule,
    ): List<RedactCustomRule> = parseSettings(
        requestJson(status, "/api/settings/redact-rule", "POST", buildRedactRuleJson(at, was, rule)),
    ).customRules

    fun setProxy(status: GatewayStatus, proxy: String): String {
        val body = requestJson(status, "/api/settings", "POST", buildProxyJson(proxy))
        return parseProxyMasked(body)
    }

    fun setCorsOrigins(status: GatewayStatus, origins: List<String>): List<String> {
        val body = requestJson(status, "/api/settings/cors", "POST", buildCorsJson(origins))
        return parseCorsOrigins(body)
    }

    fun setAllowIPs(status: GatewayStatus, ips: List<String>): List<String> {
        val body = requestJson(status, "/api/settings/allowips", "POST", buildAllowIpsJson(ips))
        return parseAllowIPs(body)
    }

    fun setLan(status: GatewayStatus, on: Boolean): Boolean {
        val body = requestJson(status, "/api/settings/lan", "POST", buildLanToggleJson(on))
        return parseLanEnabled(body)
    }

    fun setRedactActions(status: GatewayStatus, actions: Map<String, String>): Map<String, String> {
        val body = requestJson(status, "/api/settings/redact-actions", "POST", buildRedactActionsJson(actions))
        return parseRedactActions(body)
    }

    /**
     * 个人信息脱敏开关走 UI 偏好透传：`redactPersonal` 不在 reservedSettingKeys 里，
     * `POST /api/settings` 会按 key 合并进 settings.ui，再经 syncRedactPrefs 落到引擎。
     * 没有 `/api/settings/redact-personal` 这个路由，别去造。
     */
    fun setRedactPersonal(status: GatewayStatus, enabled: Boolean): Boolean {
        val body = requestJson(status, "/api/settings", "POST", buildRedactPersonalJson(enabled))
        return parseRedactPersonal(body)
    }

    private fun requestJson(status: GatewayStatus, path: String, method: String, body: String?): String {
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
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { parseSettingsError(it) }
                }.getOrNull()
                throw ManagementApiException("http_$code", detail?.take(200) ?: "管理接口返回 $code")
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

    fun setRedactEnabled(status: GatewayStatus, enabled: Boolean): Boolean {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val body = buildRedactToggleJson(enabled)
        val connection = (URL("http://${status.addr}/api/settings/redact-actions").openConnection() as HttpURLConnection)
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
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException("http_$code", "管理接口返回 $code")
            }
            val response = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return parseRedactEnabled(response)
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

internal fun buildCorsJson(origins: List<String>): String =
    JSONObject().put("origins", JSONArray(origins)).toString()

internal fun buildAllowIpsJson(ips: List<String>): String =
    JSONObject().put("ips", JSONArray(ips)).toString()

internal fun parseCorsOrigins(body: String): List<String> = parseStringList(body, "corsOrigins")

internal fun parseAllowIPs(body: String): List<String> = parseStringList(body, "allowIPs")

private fun parseStringList(body: String, key: String): List<String> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    val array = root.optJSONArray(key) ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().ifBlank { null }
    }
}

internal fun buildProxyJson(proxy: String): String =
    JSONObject().put("proxy", proxy.trim()).toString()

internal fun parseProxyMasked(body: String): String {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的代理设置格式不正确")
    }
    if (!root.has("proxy")) {
        throw ManagementApiException("invalid_response", "管理接口返回的代理设置缺少 proxy")
    }
    return root.optString("proxy").trim()
}

internal fun validateProxyInput(input: String): String? {
    val value = input.trim()
    if (value.isEmpty()) {
        return null
    }
    if (value.any { it.isWhitespace() }) {
        return "代理地址不能包含空格"
    }
    if (value.contains('@')) {
        return "手机端不保存带凭据的代理地址；请在 Windows 端配置"
    }
    val uri = runCatching { URI(value) }.getOrNull() ?: return "代理地址格式不正确"
    val scheme = uri.scheme?.lowercase() ?: return "代理地址需要 http://、https:// 或 socks5://"
    if (scheme !in setOf("http", "https", "socks5")) {
        return "只支持 http、https 或 socks5 代理"
    }
    if (uri.host.isNullOrBlank() || uri.port !in 1..65535) {
        return "代理地址需要主机和端口，例如 socks5://127.0.0.1:7890"
    }
    return null
}

internal fun buildLanToggleJson(on: Boolean): String =
    JSONObject().put("on", on).toString()

internal fun parseLanEnabled(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的局域网状态格式不正确")
    }
    if (!root.has("lan")) {
        throw ManagementApiException("invalid_response", "管理接口返回的局域网状态缺少 lan")
    }
    return root.optBoolean("lan", false)
}

internal fun parseSettingsError(body: String): String? = runCatching {
    JSONObject(body.trim()).optString("error").trim().ifBlank { null }
}.getOrNull()

internal fun buildRedactActionsJson(actions: Map<String, String>): String {
    val map = JSONObject()
    actions.forEach { (kind, action) -> map.put(kind, action) }
    return JSONObject().put("actions", map).toString()
}

internal fun buildRedactRulesJson(rules: List<RedactCustomRule>): String {
    val array = JSONArray()
    rules.forEach { array.put(customRuleJson(it)) }
    return JSONObject().put("rules", array).toString()
}

internal fun buildRedactRuleJson(at: Int, was: RedactCustomRule, rule: RedactCustomRule): String =
    JSONObject()
        .put("at", at)
        .put("was", customRuleJson(was))
        .put("rule", customRuleJson(rule))
        .toString()

private fun customRuleJson(rule: RedactCustomRule): JSONObject = JSONObject().apply {
    put("kind", rule.kind.trim())
    if (rule.prefix.isNotBlank()) put("prefix", rule.prefix.trim()) else put("regex", rule.regex.trim())
}

/** 写入失败时不本地覆盖旧列表；只有成功回读才切换。 */
internal fun mergeRedactRules(
    current: List<RedactCustomRule>,
    readback: List<RedactCustomRule>?,
    error: String?,
): List<RedactCustomRule> = if (error == null && readback != null) readback else current

internal fun parseRedactDetail(body: String): RedactDetail {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏详情格式不正确")
    }
    val array = root.optJSONArray("rules")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的脱敏详情缺少 rules")
    val rules = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val kind = item.optString("kind").trim()
        if (kind.isBlank()) {
            return@mapNotNull null
        }
        RedactRule(
            id = item.optString("id").trim().ifBlank { kind },
            name = item.optString("name").trim().ifBlank { kind },
            kind = kind,
            action = item.optString("action").trim().ifBlank { "mask" },
            enabled = item.optBoolean("enabled", true),
        )
    }
    val overrides = root.optJSONObject("actions")
    val actions = if (overrides == null) {
        emptyMap()
    } else {
        overrides.keys().asSequence().mapNotNull { kind ->
            val action = overrides.optString(kind).trim()
            if (action in setOf("mask", "warn", "block")) kind to action else null
        }.toMap()
    }
    return RedactDetail(
        enabled = root.optBoolean("enabled", false),
        rules = rules,
        actions = actions,
    )
}

internal fun parseRedactActions(body: String): Map<String, String> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏动作格式不正确")
    }
    val overrides = root.optJSONObject("actions")
        ?: throw ManagementApiException("invalid_response", "管理接口返回的脱敏动作缺少 actions")
    return overrides.keys().asSequence().mapNotNull { kind ->
        val action = overrides.optString(kind).trim()
        if (action in setOf("mask", "warn", "block")) kind to action else null
    }.toMap()
}

internal fun effectiveAction(detail: RedactDetail, rule: RedactRule): String =
    detail.actions[rule.kind] ?: rule.action

internal fun buildRedactToggleJson(enabled: Boolean): String =
    JSONObject().put("enabled", enabled).toString()

/**
 * 只带 `redactPersonal` 一个键：服务端 applyUIPrefs 是按键合并，
 * 不会像 privacy_mask 那样整表替换，所以不会清掉没回显的字段。
 */
internal fun buildRedactPersonalJson(enabled: Boolean): String =
    JSONObject().put("redactPersonal", enabled).toString()

/** 从 POST /api/settings 的 settingsView 回读个人信息脱敏开关。 */
internal fun parseRedactPersonal(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    if (!root.has("redactPersonal")) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置缺少 redactPersonal")
    }
    return root.optBoolean("redactPersonal", false)
}

internal fun parseRedactEnabled(body: String): Boolean {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏状态格式不正确")
    }
    if (!root.has("enabled")) {
        throw ManagementApiException("invalid_response", "管理接口返回的脱敏状态缺少 enabled")
    }
    return root.optBoolean("enabled", false)
}

internal fun parseSettings(body: String): SettingsSummary {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的设置格式不正确")
    }
    fun stringList(name: String): List<String> {
        val array = root.optJSONArray(name) ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            array.optString(index).trim().ifBlank { null }
        }
    }
    return SettingsSummary(
        addr = root.optString("addr").trim(),
        port = root.optInt("port", 0).coerceAtLeast(0),
        lanEnabled = root.optBoolean("lan", false),
        lanUrls = stringList("lanURLs"),
        proxyMasked = root.optString("proxy").trim(),
        corsOrigins = stringList("corsOrigins"),
        allowIPs = stringList("allowIPs"),
        redactEnabled = root.optBoolean("redact", false),
        redactPersonal = root.optBoolean("redactPersonal", false),
        redactActions = stringList("redactActions"),
        redactRuleCount = root.optJSONArray("redactRules")?.length() ?: 0,
        redactAllowCount = root.optJSONArray("redactAllow")?.length() ?: 0,
        desktopApp = root.optBoolean("desktop_app", false),
        customRules = parseCustomRules(root),
        uiTheme = root.optString("theme").trim(),
        monitorMinutes = root.optInt("monitor_minutes", 0).coerceAtLeast(0),
        keepAwake = root.optBoolean("keep_awake", false),
        wslDisabled = root.optBoolean("wsl_disabled", false),
        autostartSupported = root.optJSONObject("autostart")?.optBoolean("supported", false) ?: false,
        redactShape = root.optBoolean("redactShape", false),
        privacyMaskEnabled = root.optJSONObject("privacy_mask")?.optBoolean("enabled", false) ?: false,
        hooks = parsePipelineHooks(root.optJSONObject("hooks")),
        otlp = parseOtlpConfig(root.optJSONObject("otlp")),
        tools = parseToolsConfig(root.optJSONObject("tools")),
    )
}

/* ---------------- OTLP / 工具服务 / 预设的解析与构造（纯函数，单测覆盖） ---------------- */

internal fun parseOtlpConfig(obj: org.json.JSONObject?): OtlpConfig {
    if (obj == null) {
        return OtlpConfig()
    }
    val headers = obj.optJSONObject("headers")
    return OtlpConfig(
        endpoint = obj.optString("endpoint").trim(),
        batchSize = obj.optInt("batch_size", 0).coerceAtLeast(0),
        flushSeconds = obj.optInt("flush_interval_seconds", 0).coerceAtLeast(0),
        queueMax = obj.optInt("queue_max", 0).coerceAtLeast(0),
        headersMasked = headers != null && headers.has("___masked___"),
    )
}

/**
 * OTLP 保存 JSON：**不带 headers**（服务端据此沿用旧值，`settings.go:728`）。
 * 要清空鉴权头时传 `clearHeaders = true`（显式发空对象）。
 */
internal fun buildOtlpJson(
    endpoint: String,
    batchSize: Int,
    flushSeconds: Int,
    queueMax: Int,
    clearHeaders: Boolean = false,
): String = buildString {
    append("{\"otlp\":{\"endpoint\":")
    append(quoteJson(endpoint.trim()))
    if (batchSize > 0) append(",\"batch_size\":").append(batchSize)
    if (flushSeconds > 0) append(",\"flush_interval_seconds\":").append(flushSeconds)
    if (queueMax > 0) append(",\"queue_max\":").append(queueMax)
    if (clearHeaders) append(",\"headers\":{}")
    append("}}")
}

internal fun parseToolsConfig(obj: org.json.JSONObject?): ToolsConfig {
    if (obj == null) {
        return ToolsConfig()
    }
    fun keySaved(name: String): Boolean {
        val sub = obj.optJSONObject(name) ?: return false
        val key = sub.optString("api_key").trim()
        // 服务端只在**存过** Key 时才回掩码 `******`（`tools_api.go:45-47`），
        // 所以"看到掩码"= 已保存。早前把掩码判成"未保存"，会让页面显示"未配置"
        // 并让 effectiveToolKey 失去哨兵依据（读写不对称，必测项）。
        return key.isNotEmpty()
    }
    val web = obj.optJSONObject("web_search")
    val image = obj.optJSONObject("image")
    val audio = obj.optJSONObject("audio")
    val video = obj.optJSONObject("video")
    val realtime = obj.optJSONObject("realtime")
    return ToolsConfig(
        webSearchProvider = web?.optString("provider").orEmpty().trim(),
        webSearchEndpoint = web?.optString("endpoint").orEmpty().trim(),
        webSearchMaxResults = web?.optInt("max_results", 0) ?: 0,
        webSearchKeyOrder = web?.optString("key_order").orEmpty().trim(),
        webSearchKeyCount = web?.optInt("key_count", 0) ?: 0,
        imageBaseUrl = image?.optString("base_url").orEmpty().trim(),
        imageModel = image?.optString("model").orEmpty().trim(),
        imageKeySaved = keySaved("image"),
        audioBaseUrl = audio?.optString("base_url").orEmpty().trim(),
        audioModel = audio?.optString("model").orEmpty().trim(),
        audioKeySaved = keySaved("audio"),
        videoBaseUrl = video?.optString("base_url").orEmpty().trim(),
        videoModel = video?.optString("model").orEmpty().trim(),
        videoKeySaved = keySaved("video"),
        realtimeBaseUrl = realtime?.optString("base_url").orEmpty().trim(),
        realtimeModel = realtime?.optString("model").orEmpty().trim(),
        realtimeKeySaved = keySaved("realtime"),
    )
}

/** 预设列表的只读投影 + 原始响应（启停要把整对象回写，见 [presetToggleBody]）。 */
data class PresetsSnapshot(
    val presets: List<PresetSummary> = emptyList(),
    val rawBody: String = "",
)

internal fun parsePresetsSnapshot(body: String): PresetsSnapshot {
    val root = try {
        org.json.JSONObject(body.trim())
    } catch (_: org.json.JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的预设格式不正确")
    }
    val array = root.optJSONArray("presets") ?: return PresetsSnapshot(rawBody = body)
    val list = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        PresetSummary(
            id = id,
            name = item.optString("name").trim().ifBlank { id },
            description = item.optString("description").trim(),
            enabled = item.optBoolean("enabled", true),
            bodyCount = item.optJSONObject("body")?.length() ?: 0,
            headerCount = item.optJSONObject("headers")?.length() ?: 0,
            hasSystemPrompt = item.optString("system_prompt").trim().isNotEmpty(),
            attachedProviders = 0,
            attachedRoutes = 0,
        )
    }
    return PresetsSnapshot(presets = list, rawBody = body)
}

/**
 * 启停预设：服务端 `presets/save` 收的是**整个预设对象**，所以从原始响应里取出那一条，
 * 只翻转 `enabled` 后原样回写（不改 body/headers/提示词）。找不到返回 null。
 */
internal fun presetToggleBody(rawBody: String, id: String, enabled: Boolean): String? {
    val root = runCatching { org.json.JSONObject(rawBody.trim()) }.getOrNull() ?: return null
    val array = root.optJSONArray("presets") ?: return null
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        if (item.optString("id").trim() != id) {
            continue
        }
        val copy = org.json.JSONObject(item.toString())
        copy.put("enabled", enabled)
        return copy.toString()
    }
    return null
}

/** 删除预设的请求体（桌面 `settings.js:877`）。 */
internal fun buildPresetDeleteJson(id: String): String = "{\"id\":${quoteJson(id)}}"

/* ============================================================================
 * 工具服务编辑（桌面 `settings.js:275-398`）
 *
 * 密钥哨兵语义已核（`tools_api.go:75-170`）：
 *  - 子对象只在**请求里出现**时才应用（`if in.X != nil`）；
 *  - `api_key == "******"` → 保留旧值；`api_key == ""` 且存在旧值 → **也保留**；
 *  - 多 Key：带 `api_keys` 且含 `******` → 保留旧列表；不带 → 保留旧列表。
 * 所以"用户没改 Key"就原样回传哨兵，绝不会清掉已存 Key。
 * ========================================================================== */

/** 密钥哨兵：GET 回显的打码值，回传它表示"保留已存值"。 */
internal const val TOOLS_KEY_SENTINEL: String = "******"

data class ToolsEndpointDraft(
    val baseUrl: String = "",
    val apiKey: String = "",
    val model: String = "",
)

data class ToolsDraft(
    val webSearchProvider: String = "tavily",
    val webSearchEndpoint: String = "",
    val webSearchApiKey: String = "",
    /** 多 Key（每行一个）；非空即替换现有列表。 */
    val webSearchApiKeys: List<String> = emptyList(),
    val webSearchMaxResults: Int = 5,
    val webSearchKeyOrder: String = "rotate",
    val image: ToolsEndpointDraft = ToolsEndpointDraft(),
    val audio: ToolsEndpointDraft = ToolsEndpointDraft(),
    val video: ToolsEndpointDraft = ToolsEndpointDraft(),
    val realtime: ToolsEndpointDraft = ToolsEndpointDraft(),
)

/**
 * Key 字段实际提交值：
 *  - 用户输入新值 → 用新值；
 *  - 留空且服务端已存过 → 回传哨兵 `******`（保留）；
 *  - 留空且从未存过 → 空串（服务端无旧值可留，结果仍是空）。
 */
internal fun effectiveToolKey(typed: String, saved: Boolean): String {
    val value = typed.trim()
    return when {
        value.isNotEmpty() && value != TOOLS_KEY_SENTINEL -> value
        saved -> TOOLS_KEY_SENTINEL
        else -> ""
    }
}

/** 组装 `POST /api/settings {tools}`；密钥字段一律走 [effectiveToolKey]。 */
internal fun buildToolsJson(
    draft: ToolsDraft,
    savedKeys: Map<String, Boolean> = emptyMap(),
): String {
    fun part(name: String, value: ToolsEndpointDraft): String {
        val key = effectiveToolKey(value.apiKey, savedKeys[name] == true)
        return buildString {
            append(quoteJson(name)).append(":{")
            append("\"base_url\":").append(quoteJson(value.baseUrl.trim()))
            append(",\"api_key\":").append(quoteJson(key))
            append(",\"model\":").append(quoteJson(value.model.trim()))
            append("}")
        }
    }
    val webKeys = draft.webSearchApiKeys.filter { it.isNotBlank() }
    return buildString {
        append("{\"tools\":{\"web_search\":{")
        append("\"provider\":").append(quoteJson(draft.webSearchProvider.trim()))
        append(",\"endpoint\":").append(quoteJson(draft.webSearchEndpoint.trim()))
        append(",\"api_key\":")
            .append(quoteJson(effectiveToolKey(draft.webSearchApiKey, savedKeys["web_search"] == true)))
        append(",\"max_results\":").append(draft.webSearchMaxResults.coerceIn(1, 20))
        append(",\"key_order\":").append(quoteJson(draft.webSearchKeyOrder.trim().ifBlank { "rotate" }))
        if (webKeys.isNotEmpty()) {
            append(",\"api_keys\":[").append(webKeys.joinToString(",") { quoteJson(it) }).append("]")
        }
        append("},")
        append(part("image", draft.image)).append(",")
        append(part("audio", draft.audio)).append(",")
        append(part("video", draft.video)).append(",")
        append(part("realtime", draft.realtime))
        append("}}")
    }
}

/* ============================================================================
 * 预设新建/编辑（桌面 `settings.js:847-931`；服务端 `presetSaveInput` request_presets.go:345-354）
 *
 * 结构已核：`body` 是 **JSON 对象**（`map[string]any`）、`headers` 是**字符串到字符串的对象**
 * （`map[string]string`），都不是字符串；`id` 留空由服务端按名称生成（非法 id 会 400）。
 * ========================================================================== */

data class PresetDraft(
    val id: String = "",
    val name: String = "",
    val description: String = "",
    val enabled: Boolean = true,
    val systemPrompt: String = "",
    val concatSystem: Boolean = false,
    val bodyJson: String = "{}",
    val headersJson: String = "{}",
)

/** 草稿校验：null = 通过；否则为人类可读原因（JSON 错误带行号）。 */
internal fun validatePresetDraft(draft: PresetDraft): String? {
    if (draft.name.trim().isEmpty()) {
        return "名称不能为空"
    }
    parseJsonObjectOrError(draft.bodyJson)?.let { return "Body $it" }
    val headersText = draft.headersJson.trim().ifEmpty { "{}" }
    parseJsonObjectOrError(headersText)?.let { return "Headers $it" }
    val headers = runCatching { org.json.JSONObject(headersText) }.getOrNull() ?: return null
    val keys = headers.keys()
    while (keys.hasNext()) {
        val key = keys.next()
        if (headers.opt(key) !is String) {
            return "Headers 的值必须是字符串（键「$key」不是）"
        }
    }
    // 服务端会拒非法 id（`slugify(id) != id` → 400），本地先拦一次，省一次往返。
    val id = draft.id.trim()
    if (id.isNotEmpty() && !Regex("^[a-z0-9][a-z0-9-]*$").matches(id)) {
        return "ID 只能包含小写字母、数字和连字符"
    }
    return null
}

/**
 * 解析 JSON 对象；失败返回「第 N 行附近：原因」。
 * `org.json` 的报错通常带 `at character K`，换算成行号比只给一句话有用。
 */
internal fun parseJsonObjectOrError(text: String): String? {
    val trimmed = text.trim().ifEmpty { "{}" }
    if (trimmed.startsWith("[") || trimmed.startsWith("\"")) {
        return "需要 JSON 对象，不是数组或字符串"
    }
    try {
        org.json.JSONObject(trimmed)
    } catch (e: org.json.JSONException) {
        return jsonErrorMessage(trimmed, e.message.orEmpty())
    }
    return null
}

/**
 * 把 JSON 解析错误翻成中文提示；**两种 `org.json` 报错格式都要认**：
 *
 *  - Android / 新版 org.json：`Missing value at 19 [character 7 line 3]`（方括号里直接给行列，最准）
 *  - 其它实现：`Unterminated object at character 5`（只给字符偏移，需要自己换算行号）
 *
 * 两者都收敛成「JSON 不合法（第 N 行附近）：<原因>」；识别不到位置时退化为不带行号的版本，
 * 但**绝不丢掉原因**（原因里可能就有用户写错的那个字符）。
 */
internal fun jsonErrorMessage(text: String, rawMessage: String): String {
    val bracket = Regex("""\[character (\d+) line (\d+)]""").find(rawMessage)
    val atCharacter = Regex("""at character (\d+)""").find(rawMessage)
    val atNumber = Regex("""\bat (\d+)\b""").find(rawMessage)

    fun lineOf(position: Int): Int {
        val safe = position.coerceIn(0, text.length)
        return text.take(safe).count { it == '\n' } + 1
    }

    val line = when {
        // 方括号形式自带行号，直接用（不用再猜偏移语义是 0 还是 1 基）。
        bracket != null -> bracket.groupValues[2].toIntOrNull()
        atCharacter != null -> atCharacter.groupValues[1].toIntOrNull()?.let(::lineOf)
        atNumber != null -> atNumber.groupValues[1].toIntOrNull()?.let(::lineOf)
        else -> null
    }

    var reason = rawMessage.replace(Regex("""\s*\[character \d+ line \d+]"""), "")
    Regex("""\sat\s+(character\s+)?\d+""").find(reason)?.let { match ->
        reason = reason.substring(0, match.range.first)
    }
    val clean = reason.trim().ifBlank { "无法解析" }
    return if (line == null) {
        "JSON 不合法：$clean"
    } else {
        "JSON 不合法（第 $line 行附近）：$clean"
    }
}

/** 组装 `POST /api/presets/save`；草稿不合法返回 null（页面据此禁用保存）。 */
internal fun buildPresetSaveJson(draft: PresetDraft): String? {
    if (validatePresetDraft(draft) != null) {
        return null
    }
    val body = draft.bodyJson.trim().ifEmpty { "{}" }
    val headers = draft.headersJson.trim().ifEmpty { "{}" }
    return buildString {
        append("{\"id\":").append(quoteJson(draft.id.trim()))
        append(",\"name\":").append(quoteJson(draft.name.trim()))
        append(",\"description\":").append(quoteJson(draft.description.trim()))
        append(",\"enabled\":").append(draft.enabled)
        append(",\"body\":").append(body)
        append(",\"headers\":").append(headers)
        append(",\"system_prompt\":").append(quoteJson(draft.systemPrompt))
        append(",\"concat_system\":").append(draft.concatSystem)
        append("}")
    }
}

/** 从 `GET /api/presets` 原始响应取一条预设用于编辑回填；找不到返回 null。 */
internal fun presetDraftFrom(rawBody: String, id: String): PresetDraft? {
    val root = runCatching { org.json.JSONObject(rawBody.trim()) }.getOrNull() ?: return null
    val array = root.optJSONArray("presets") ?: return null
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        if (item.optString("id").trim() != id) {
            continue
        }
        return PresetDraft(
            id = id,
            name = item.optString("name").trim(),
            description = item.optString("description").trim(),
            enabled = item.optBoolean("enabled", true),
            systemPrompt = item.optString("system_prompt"),
            concatSystem = item.optBoolean("concat_system", false),
            bodyJson = item.optJSONObject("body")?.toString(2) ?: "{}",
            headersJson = item.optJSONObject("headers")?.toString(2) ?: "{}",
        )
    }
    return null
}

/* ---------------- 新增字段的解析与 JSON 构造（纯函数，单测覆盖） ---------------- */

internal fun parsePipelineHooks(obj: org.json.JSONObject?): PipelineHooks {
    if (obj == null) {
        return PipelineHooks()
    }
    fun list(key: String): List<String> {
        val array = obj.optJSONArray(key) ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            array.optString(index).trim().ifBlank { null }
        }
    }
    return PipelineHooks(
        dropReasoning = obj.optBoolean("drop_reasoning", false),
        clampOutput = obj.optInt("clamp_output", 0).coerceAtLeast(0),
        blockModels = list("block_models"),
        guardProviders = list("guard_providers"),
        // 服务端第五个键：不读出来，回写时就会把它抹掉（`pipeline_hooks.go:61-63`）。
        guardAccounts = list("guard_accounts"),
    )
}

internal fun buildThemeJson(theme: String): String =
    "{\"ui\":{\"theme\":${quoteJson(theme.trim())}}}"

internal fun buildMonitorMinutesJson(minutes: Int): String =
    "{\"monitor_minutes\":${minutes.coerceAtLeast(0)}}"

internal fun buildKeepAwakeJson(enabled: Boolean): String = "{\"keep_awake\":$enabled}"

internal fun buildWslDisabledJson(disabled: Boolean): String = "{\"wsl_disabled\":$disabled}"

internal fun buildRedactShapeJson(enabled: Boolean): String = "{\"redactShape\":$enabled}"

internal fun buildPrivacyMaskJson(enabled: Boolean): String = "{\"privacy_mask\":{\"enabled\":$enabled}}"

/**
 * 钩子保存体：**必须是 `{"hooks":{...}}` 包裹**（服务端读 `settings.ui["hooks"]`，
 * 桌面 `saveHooks` 也发 `{hooks: cfg}`）。键名照 `pipeline_hooks.go:26-34` 的注释逐字：
 * `drop_reasoning` / `clamp_output` / `block_models` / `guard_providers` / `guard_accounts`。
 *
 * 注意：第五项 `guard_accounts` 必须一起写回，否则手机端改任意开关都会把它删掉。
 */
internal fun buildHooksJson(hooks: PipelineHooks): String {
    val blocks = hooks.blockModels.joinToString(",") { quoteJson(it) }
    val guards = hooks.guardProviders.joinToString(",") { quoteJson(it) }
    val accounts = hooks.guardAccounts.joinToString(",") { quoteJson(it) }
    return "{\"hooks\":{\"drop_reasoning\":${hooks.dropReasoning}," +
        "\"clamp_output\":${hooks.clampOutput.coerceAtLeast(0)}," +
        "\"block_models\":[$blocks],\"guard_providers\":[$guards]," +
        "\"guard_accounts\":[$accounts]}}"
}

internal fun buildModelRedirectsJson(redirects: Map<String, Map<String, String>>): String {
    val body = redirects.entries.sortedBy { it.key }.joinToString(",") { (virtual, targets) ->
        val inner = targets.entries.sortedBy { it.key }.joinToString(",") { (provider, model) ->
            "${quoteJson(provider)}:${quoteJson(model)}"
        }
        "${quoteJson(virtual)}:{$inner}"
    }
    return "{\"redirects\":{$body}}"
}

internal fun parseModelRedirectsSnapshot(body: String): ModelRedirects {
    val root = try {
        org.json.JSONObject(body.trim())
    } catch (_: org.json.JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的模型重定向格式不正确")
    }
    val redirects = root.optJSONObject("redirects") ?: return ModelRedirects()
    val out = LinkedHashMap<String, Map<String, String>>()
    val keys = redirects.keys()
    while (keys.hasNext()) {
        val virtual = keys.next().trim()
        if (virtual.isEmpty()) {
            continue
        }
        val targets = redirects.optJSONObject(virtual) ?: continue
        val inner = LinkedHashMap<String, String>()
        val innerKeys = targets.keys()
        while (innerKeys.hasNext()) {
            val provider = innerKeys.next().trim()
            if (provider.isEmpty()) {
                continue
            }
            inner[provider] = targets.optString(provider).trim()
        }
        out[virtual] = inner
    }
    return ModelRedirects(redirects = out)
}

/** 极简 JSON 字符串转义（只处理控制字符与引号/反斜杠，够用于键名与模型名）。 */
internal fun quoteJson(value: String): String {
    val out = StringBuilder(value.length + 2)
    out.append('"')
    for (ch in value) {
        when (ch) {
            '"' -> out.append("\\\"")
            '\\' -> out.append("\\\\")
            '\n' -> out.append("\\n")
            '\r' -> out.append("\\r")
            '\t' -> out.append("\\t")
            else -> if (ch < ' ') out.append(String.format("\\u%04x", ch.code)) else out.append(ch)
        }
    }
    out.append('"')
    return out.toString()
}

private fun parseCustomRules(root: JSONObject): List<RedactCustomRule> {
    val array = root.optJSONArray("redactRules") ?: return emptyList()
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val kind = item.optString("kind").trim()
        val prefix = item.optString("prefix").trim()
        val regex = item.optString("regex").trim()
        if (kind.isBlank() || (prefix.isBlank() && regex.isBlank())) null
        else RedactCustomRule(kind, prefix = prefix, regex = regex)
    }
}
