package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** GET /healthz 的应答：正文恒为 ok，只是文本/JSON 两种形状。 */
data class HealthzProbe(
    val status: String,
    val plainText: Boolean,
)

/** GET /api/health 的应答（移动鉴权里显式放行，匿名可读）。 */
data class HealthProbe(
    val status: String,
    val uptimeSec: Int,
    val providers: Int,
    val accounts: Int,
    val version: String,
    val revision: String,
)

/**
 * GET /readyz 的应答。HTTP 503 也是合法应答（网关活着但没就绪），
 * 因此 [httpStatus] 与 [ready]/[reasons] 一起交给页面，不能当成网络失败吞掉。
 */
data class ReadyzProbe(
    val httpStatus: Int,
    val ready: Boolean,
    val models: Int,
    val providers: Int,
    val providersInUse: Int,
    val accounts: Int,
    val usableAccounts: Int,
    val uptimeSec: Int,
    val reasons: List<String>,
)

/** GET /version 的构建信息（不含任何配置或凭据）。 */
data class VersionInfo(
    val version: String,
    val go: String,
    val revision: String,
    val buildTime: String,
    val buildTimeSource: String,
    val uptimeSec: Int,
)

/** GET /metrics 的全文摘要：只数行 + 留前几行，刻意不解析成指标对象图。 */
data class MetricsDigest(
    val lineCount: Int,
    val charCount: Int,
    val preview: List<String>,
)

/** GET /api/runtime 的应答（会话端点）。 */
data class RuntimeInfo(
    val uptimeSec: Int,
    val goVersion: String,
    val goroutines: Int,
    val heapMb: Int,
    val stateRecords: Int,
    val models: Int,
    val tun: Boolean,
    val publicAddr: String,
)

/** 单个端点的探测结果：一个端点失败只影响它自己那一格。 */
sealed interface ProbeOutcome<out T> {
    data class Ok<T>(val value: T) : ProbeOutcome<T>
    data class Failed(val code: String, val message: String) : ProbeOutcome<Nothing>
}

/** 匿名组：移动运行时里不带任何凭据就能读的端点。 */
data class AnonymousProbeReport(
    val healthz: ProbeOutcome<HealthzProbe>,
    val health: ProbeOutcome<HealthProbe>,
)

/** 会话组：移动运行时里必须带 Bearer 会话凭据的端点。 */
data class SessionProbeReport(
    val readyz: ProbeOutcome<ReadyzProbe>,
    val version: ProbeOutcome<VersionInfo>,
    val metrics: ProbeOutcome<MetricsDigest>,
    val runtime: ProbeOutcome<RuntimeInfo>,
)

/**
 * 这个探针端点要不要会话凭据——**分组就是鉴权面，只在这一处表态**。
 *
 * 移动模式（`cfg.AdminSessionKey != ""`）下 `mobileAdminPath` 把 `/readyz`、`/version`、
 * `/metrics` 也算作管理面（admin_mobile.go），`/api/` 之下除 `/api/health` 一律要会话
 * （gateway.go 的鉴权分支）；`/healthz` 更是被移动运行时在进入 Gateway 之前就接管了
 * （internal/mobile/runtime.go）。桌面端 `/readyz`、`/version`、`/metrics` 是匿名的，
 * 但本 App 只连移动运行时，所以按移动真实面取值。
 */
internal fun probeNeedsSession(path: String): Boolean = when (path) {
    "/readyz", "/version", "/metrics", "/api/runtime" -> true
    // 额度提醒（`GET /api/alerts`，`gateway.go:874`）落在 `/api/` 管理面：必须带会话凭据。
    "/api/alerts" -> true
    // Codex 身份对照（`GET /api/providers/codex/identity`，`gateway.go:1150`）同样在 `/api/` 面。
    "/api/providers/codex/identity" -> true
    // 渠道网络策略（`GET /api/channels/net`，`gateway.go:1205`）也是管理面。
    "/api/channels/net" -> true
    // 网络连通诊断（`GET /api/network/diag`，`gateway.go:1213`）也是管理面。
    "/api/network/diag" -> true
    // /healthz 与 /api/health：移动运行时里显式匿名。
    else -> false
}

/**
 * 这个路径实际要发的 Authorization 头；null 表示**一个字节的凭据都不发**。
 *
 * 刻意不做"401 后补一次 Bearer"的降级：那是在猜鉴权面。该带的直接带对，不该带的一个字
 * 都不带——401 出现就是真异常，页面原样显示服务端 code/error。
 */
internal fun probeAuthorization(path: String, status: GatewayStatus): String? =
    if (probeNeedsSession(path)) "Bearer ${status.adminSession}" else null

/**
 * 网关探针客户端：按移动运行时的真实鉴权面分成两组，两组的可用性彼此独立。
 *
 * 匿名组（/healthz、/api/health）**不发 Authorization**：这两个端点在移动运行时里本来就是
 * 匿名可读的（`/healthz` 由移动运行时在进入 Gateway 前接管，`/api/health` 在
 * `mobileAdminPath` 里显式 `return false`）。给它们带会话凭据，等于把一个只在内存里的
 * 管理凭据扩散到本可匿名的地方。
 *
 * 会话组（/readyz、/version、/metrics、/api/runtime）**必须**带
 * `Authorization: Bearer <adminSession>`：移动运行时把前三个也算管理面（回环不等于可信），
 * `/api/runtime` 落在 `/api/` 之下。带错或漏带就是 401，页面如实显示服务端文案。
 *
 * 每个端点的失败都折成该端点自己的 [ProbeOutcome.Failed]，一组探测不会连坐另一组。
 */
class ProbeApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
) {
    /** 两个匿名端点。失败原因只记录在该端点自己的 [ProbeOutcome.Failed] 里。 */
    fun fetchAnonymous(status: GatewayStatus): AnonymousProbeReport {
        if (!status.running || status.addr.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        return AnonymousProbeReport(
            healthz = probeOf { parseHealthz(probe(status, "/healthz").body) },
            health = probeOf { parseHealth(probe(status, "/api/health").body) },
        )
    }

    /** 四个会话端点；没有会话凭据时视为网关未启动，一次请求都不发。 */
    fun fetchSession(status: GatewayStatus): SessionProbeReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        return SessionProbeReport(
            // 503 是"活着但没就绪"的合法应答，必须解析而不是当网络失败。
            readyz = probeOf {
                val response = probe(status, "/readyz", acceptedCodes = setOf(HTTP_NOT_READY))
                parseReadyz(response.body, response.status)
            },
            version = probeOf { parseVersion(probe(status, "/version").body) },
            metrics = probeOf { parseMetrics(probe(status, "/metrics").body) },
            runtime = probeOf { parseRuntime(probe(status, "/api/runtime").body) },
        )
    }

    /**
     * 额度提醒：`GET /api/alerts`（`gateway.go:874` → `quota_alert.go:225-244`），
     * 桌面把它放在网关页第一块（`internal/web/ui2/js/views/gateway.js:86-100`）。
     *
     * 数据来自网关里**仍然成立**的额度窗口（`ActiveQuotaAlerts()` 会过滤掉已重置的），
     * 手机网关同样有账号池与额度读数，所以不是桌面专属数据。
     */
    fun fetchQuotaAlerts(status: GatewayStatus): QuotaAlertsReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        return parseQuotaAlerts(probe(status, "/api/alerts").body)
    }

    /**
     * Codex 请求身份对照：`GET /api/providers/codex/identity?limit=5`
     * （`gateway.go:1150` → `identity_api.go:107-127`；桌面卡片 `gateway.js:141-175`）。
     */
    fun fetchCodexIdentity(status: GatewayStatus, limit: Int = 5): CodexIdentityReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val safe = limit.coerceIn(1, 20)
        return parseCodexIdentity(probe(status, "/api/providers/codex/identity?limit=$safe").body)
    }

    /**
     * 渠道网络策略：`GET /api/channels/net`（`gateway.go:1205` → `channel_net.go:306-324`）。
     *
     * 桌面把它放在**单个渠道**的详情抽屉里（`channelnet.js:63-77`，含保存表单）；
     * 手机端只做只读汇总（不提供 POST 保存——那属于渠道页的写操作）。
     */
    fun fetchChannelNet(status: GatewayStatus): List<ChannelNetRow> {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        return parseChannelNetRows(probe(status, "/api/channels/net").body)
    }

    /**
     * 网络连通诊断：`GET /api/network/diag?host=…&port=…`（`gateway.go:1213` → `netdiag.go:81-99`）。
     *
     * 服务端单次最长 25 秒；host 非法会 400，所以调用方先用 [parseNetDiagInput] 本地拦。
     */
    fun fetchNetDiag(status: GatewayStatus, host: String, port: String): NetDiagReport {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val safePort = port.trim().ifEmpty { "443" }
        val path = "/api/network/diag?host=${URLEncoder.encode(host.trim(), Charsets.UTF_8.name())}" +
            "&port=${URLEncoder.encode(safePort, Charsets.UTF_8.name())}"
        return parseNetDiag(probe(status, path).body)
    }

    /** 凭据只由 [probeAuthorization] 决定，调用点不可能"顺手"把 Bearer 带到匿名端点。 */
    private fun probe(        status: GatewayStatus,
        path: String,
        acceptedCodes: Set<Int> = emptySet(),
    ): HttpTextResult = getText(status.addr, path, probeAuthorization(path, status), acceptedCodes)

    /**
     * [acceptedCodes] 里的状态码不算失败；其余非 2xx 一律抛 [ManagementApiException]，
     * 并把服务端的 error 文案原样带到界面上。
     */
    private fun getText(
        addr: String,
        path: String,
        bearer: String?,
        acceptedCodes: Set<Int> = emptySet(),
    ): HttpTextResult {
        val connection = (URL("http://$addr$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        // 同一个 helper 既取 JSON 也取 Prometheus 纯文本，所以不限定 Accept。
        connection.setRequestProperty("Accept", "*/*")
        if (bearer != null) {
            connection.setRequestProperty("Authorization", bearer)
        }
        try {
            val code = connection.responseCode
            val accepted = code in 200..299 || code in acceptedCodes
            // 读哪个流只看状态码类别，不能看 accepted：HttpURLConnection 对 ≥400 的应答
            // 调 getInputStream() 会先抛 IOException（"Server returned HTTP response code"），
            // 于是被下面的 catch 折成 network_error —— /readyz 的 503 就是这样被误报成
            // 「无法连接本机网关」的，服务端的 readyz JSON 一个字都没读到。
            val stream = if (code in 200..299) connection.inputStream else connection.errorStream
            val body = stream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it) }.orEmpty()
            if (!accepted) {
                throw ManagementApiException("http_$code", errorDetail(code, body))
            }
            return HttpTextResult(code, body)
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机网关")
        } finally {
            connection.disconnect()
        }
    }

    private fun errorDetail(code: Int, body: String): String {
        val fromJson = runCatching { JSONObject(body.trim()).optString("error").trim() }.getOrNull().orEmpty()
        if (fromJson.isNotBlank()) {
            return fromJson.take(160)
        }
        return body.trim().take(120).ifBlank { "网关返回 $code" }
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
                throw ManagementApiException("invalid_response", "网关响应过大")
            }
        }
    }
}

private data class HttpTextResult(val status: Int, val body: String)

private const val METRICS_PREVIEW_LINES = 5

/** /readyz 的"活着但没就绪"：503 与 200 是同一份 JSON，靠 ready/reasons 区分。 */
private const val HTTP_NOT_READY = 503

/** 探测一个端点；异常绝不外溢，只标记这一格失败。 */
private fun <T> probeOf(block: () -> T): ProbeOutcome<T> = try {
    ProbeOutcome.Ok(block())
} catch (e: ManagementApiException) {
    ProbeOutcome.Failed(e.code, e.message)
} catch (_: Exception) {
    ProbeOutcome.Failed("unknown", "探测该端点时出现未预期的错误")
}

/** 整组探测意外中断时，把整组折成失败，另一组仍然照常展示。 */
internal fun failedAnonymousReport(error: Throwable): AnonymousProbeReport {
    val failed = failureOf(error)
    return AnonymousProbeReport(healthz = failed, health = failed)
}

/** 同 [failedAnonymousReport]，用于会话组。 */
internal fun failedSessionReport(error: Throwable): SessionProbeReport {
    val failed = failureOf(error)
    return SessionProbeReport(readyz = failed, version = failed, metrics = failed, runtime = failed)
}

private fun failureOf(error: Throwable): ProbeOutcome.Failed = when (error) {
    is ManagementApiException -> ProbeOutcome.Failed(error.code, error.message)
    else -> ProbeOutcome.Failed("unknown", "探针检查失败，请重试")
}

private fun objectOrThrow(body: String, label: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "$label 返回的数据格式不正确")
}

/** 缺字段一律当格式错误抛出来——静默返回空对象会让页面显示一脸假"正常"。 */
private fun requireKeys(root: JSONObject, label: String, keys: List<String>) {
    val missing = keys.filterNot { root.has(it) && !root.isNull(it) }
    if (missing.isNotEmpty()) {
        throw ManagementApiException("invalid_response", "$label 缺少字段：${missing.joinToString("、")}")
    }
}

/**
 * 一条额度提醒（`quota_alert.go:4-17` 的 `QuotaAlert`）。
 *
 * `kind` 只有三种：`low`（额度偏低）/ `expiring`（即将重置）/ `exhausted`（已用尽）。
 * 桌面按 kind 上不同颜色：expiring = amber，其余 = red（`gateway.js:95-96`）。
 */
data class QuotaAlert(
    val key: String,
    val kind: String,
    val providerId: String,
    val provider: String,
    val accountId: String,
    val account: String,
    val window: String,
    /** 已用比例（0–1，服务端口径）；服务端可能不给。 */
    val used: Double,
    val resetsAt: Long,
    val message: String,
    val at: Long,
)

/** `/api/alerts` 整包：`{"alerts":[…],"rejected":{callerKeyId:被拒次数}}`（`quota_alert.go:225-244`）。 */
data class QuotaAlertsReport(
    val alerts: List<QuotaAlert>,
    /** 今天被限流拒绝的次数，按调用方 key id。桌面只用它回答"今天被拒了多少次"。 */
    val rejected: Map<String, Int>,
)

/** 解析 `/api/alerts`：`alerts` 缺省当空数组；`rejected` 只收数值项，脏数据丢掉。 */
internal fun parseQuotaAlerts(body: String): QuotaAlertsReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "额度提醒返回的数据格式不正确")
    }
    val alerts = root.optJSONArray("alerts")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val kind = item.optString("kind").trim().lowercase()
            if (kind.isEmpty()) {
                return@mapNotNull null
            }
            QuotaAlert(
                key = item.optString("key").trim(),
                kind = kind,
                providerId = item.optString("provider_id").trim(),
                provider = item.optString("provider").trim(),
                accountId = item.optString("account_id").trim(),
                account = item.optString("account").trim(),
                window = item.optString("window").trim(),
                used = item.optDouble("used", 0.0).let { if (it.isFinite()) it.coerceIn(0.0, 1.0) else 0.0 },
                resetsAt = item.optLong("resets_at", 0L).coerceAtLeast(0L),
                message = item.optString("message").trim(),
                at = item.optLong("at", 0L).coerceAtLeast(0L),
            )
        }
    }.orEmpty()
    val rejected = LinkedHashMap<String, Int>()
    root.optJSONObject("rejected")?.let { obj ->
        for (key in obj.keys()) {
            val value = obj.optInt(key, 0)
            if (value > 0) {
                rejected[key] = value
            }
        }
    }
    return QuotaAlertsReport(alerts = alerts, rejected = rejected)
}

/**
 * 桌面 `gateway.js:95` 的 kind → chip 文案。
 *
 * 注意服务端只有 `low` / `expiring` / `exhausted` 三种；未知 kind 按「额度偏低」处理，
 * 与桌面的 `else` 分支一致。
 */
internal fun quotaAlertKindLabel(kind: String): String = when (kind.trim().lowercase()) {
    "expiring" -> "即将重置"
    "exhausted" -> "已用尽"
    else -> "额度偏低"
}

/** 桌面只给 expiring 上 amber，其余 red（`gateway.js:95-96`）。 */
internal fun quotaAlertKindIsAmber(kind: String): Boolean =
    kind.trim().lowercase() == "expiring"

/** 卡片右上角「N 条」；0 时不显示（桌面整卡都不显示）。 */
internal fun quotaAlertCountLabel(count: Int): String = "$count 条"

/** 桌面最多列 3 条（`gateway.js:93` 的 `alerts.slice(0, 3)`）。 */
internal fun quotaAlertRows(alerts: List<QuotaAlert>, limit: Int = 3): List<QuotaAlert> =
    alerts.take(limit.coerceAtLeast(0))

/**
 * 一条提醒的展示文案：渠道名（优先 `provider`，回退 id）+ 账号 + 窗口 + 剩余% + 服务端原文。
 *
 * 剩余% 由 `used` 折算（服务端口径 0–1）；`used` 缺省时**不显示百分比**，不退化成 0%。
 */
internal fun quotaAlertLine(alert: QuotaAlert): String = buildString {
    val channel = alert.provider.ifBlank { alert.providerId }
    if (channel.isNotBlank()) {
        append(channel)
    }
    if (alert.account.isNotBlank()) {
        if (isNotEmpty()) append(" · ")
        append(alert.account)
    }
    if (alert.window.isNotBlank()) {
        if (isNotEmpty()) append(" · ")
        append(alert.window)
    }
    if (alert.used > 0.0) {
        if (isNotEmpty()) append(" · ")
        append("已用 ").append(((alert.used * 100).toInt()).coerceIn(0, 100)).append("%")
    }
    if (alert.message.isNotBlank()) {
        if (isNotEmpty()) append(" — ")
        append(alert.message)
    }
}

/**
 * 一条 Codex 请求身份样本（`identity_api.go:4-14` 的 `codexIdentity`）。
 *
 * 只观测下游请求头，不改写客户端身份（服务端 note 原话）。
 */
data class CodexIdentity(
    val time: Long,
    val userAgent: String,
    val originator: String,
    val sessionId: String,
    val chatgptAccountId: String,
    val openaiBeta: String,
    val stream: Boolean,
    /** 是否与官方 CLI 形状一致。 */
    val officialLike: Boolean,
    val mismatches: List<String>,
)

/** `GET /api/providers/codex/identity` 整包（`identity_api.go:107-127`）。 */
data class CodexIdentityReport(
    /** 服务端给出的期望形状：user_agent / originator / session_id。 */
    val expected: Map<String, String>,
    val items: List<CodexIdentity>,
    val note: String,
)

/** Codex 身份对照（桌面 `gateway.js:141-175`，只读观测，不写任何客户端配置）。 */
internal fun parseCodexIdentity(body: String): CodexIdentityReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "Codex 身份对照返回的数据格式不正确")
    }
    val expected = LinkedHashMap<String, String>()
    root.optJSONObject("expected")?.let { obj ->
        for (key in obj.keys()) {
            val value = obj.optString(key).trim()
            if (value.isNotEmpty()) {
                expected[key] = value
            }
        }
    }
    val items = root.optJSONArray("items")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            CodexIdentity(
                time = item.optLong("time", 0L).coerceAtLeast(0L),
                userAgent = item.optString("user_agent").trim(),
                originator = item.optString("originator").trim(),
                sessionId = item.optString("session_id").trim(),
                chatgptAccountId = item.optString("chatgpt_account_id").trim(),
                openaiBeta = item.optString("openai_beta").trim(),
                stream = item.optBoolean("stream", false),
                officialLike = item.optBoolean("official_like", false),
                mismatches = item.optJSONArray("mismatches")?.let { arr ->
                    (0 until arr.length()).mapNotNull { i ->
                        arr.optString(i).trim().takeIf { it.isNotEmpty() }
                    }
                }.orEmpty(),
            )
        }
    }.orEmpty()
    return CodexIdentityReport(
        expected = expected,
        items = items,
        note = root.optString("note").trim(),
    )
}

/**
 * 桌面 `gateway.js:154` 的「期望：…」一行：把服务端 `expected` 拼成人话。
 *
 * 服务端字段名是 `user_agent` / `originator` / `session_id`；缺哪个就不写哪个。
 */
internal fun codexIdentityExpectedLine(report: CodexIdentityReport): String {
    if (report.expected.isEmpty()) {
        return ""
    }
    val parts = mutableListOf<String>()
    report.expected["user_agent"]?.let { parts += "User-Agent $it" }
    report.expected["originator"]?.let { parts += "originator $it" }
    report.expected["session_id"]?.let { parts += "session_id" }
    return if (parts.isEmpty()) "" else "期望：" + parts.joinToString(" · ")
}

/**
 * 桌面 `gateway.js:156-161`：`UA … · originator … · session <前 8 位>`，全空时「（无身份头）」。
 *
 * **截断只针对 session**（`gateway.js:159`：`String(it.session_id).slice(0, 8)`）；
 * UA（`:157`）与 originator（`:158`）在桌面都是原样拼进去的，不截断。
 */
internal fun codexIdentityBits(item: CodexIdentity): String {
    val bits = mutableListOf<String>()
    if (item.userAgent.isNotBlank()) bits += "UA ${item.userAgent}"
    if (item.originator.isNotBlank()) bits += "originator ${item.originator}"
    if (item.sessionId.isNotBlank()) bits += "session ${item.sessionId.take(8)}"
    return if (bits.isEmpty()) "（无身份头）" else bits.joinToString(" · ")
}

/** 桌面 `gateway.js:162`：`official_like` → 「官方形状」(green)，否则「有差异」(red)。 */
internal fun codexIdentityStatusLabel(item: CodexIdentity): String =
    if (item.officialLike) "官方形状" else "有差异"

/** 桌面 `gateway.js:163`：差异列表用「；」连接。 */
internal fun codexIdentityMismatchLine(item: CodexIdentity): String =
    item.mismatches.joinToString("；")

/** 桌面 `gateway.js:151` 的空态原话。 */
internal const val CODEX_IDENTITY_EMPTY_COPY: String =
    "还没有收到 Responses 请求；客户端发起一次对话后这里会出现身份对照。"

/**
 * 一条渠道的网络策略与实时在途数（`GET /api/channels/net`，`gateway.go:1205` → `channel_net.go:306-324`）。
 *
 * 零值表示"未单独配置，跟随默认"——展示时写「默认」，不要写成 0。
 */
data class ChannelNetRow(
    val providerId: String,
    val providerName: String,
    val baseUrl: String,
    /** 预热间隔（秒）；0 = 默认。 */
    val prewarmSec: Int,
    /** 并发上限；0 = 默认。 */
    val maxInflight: Int,
    /** 停顿超时（毫秒）；0 = 默认。 */
    val stallTimeoutMs: Int,
    /** 当前在途请求数（服务端实时值）。 */
    val inflight: Int,
)

/** 解析 `/api/channels/net`：`{"channels":[{provider_id,provider_name,base_url,prewarm_sec,max_inflight,stall_timeout_ms,inflight}]}`。 */
internal fun parseChannelNetRows(body: String): List<ChannelNetRow> {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "渠道网络返回的数据格式不正确")
    }
    return root.optJSONArray("channels")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val providerId = item.optString("provider_id").trim()
            if (providerId.isEmpty()) {
                return@mapNotNull null
            }
            ChannelNetRow(
                providerId = providerId,
                providerName = item.optString("provider_name").trim(),
                baseUrl = item.optString("base_url").trim(),
                prewarmSec = item.optInt("prewarm_sec", 0).coerceAtLeast(0),
                maxInflight = item.optInt("max_inflight", 0).coerceAtLeast(0),
                stallTimeoutMs = item.optInt("stall_timeout_ms", 0).coerceAtLeast(0),
                inflight = item.optInt("inflight", 0).coerceAtLeast(0),
            )
        }
    }.orEmpty()
}

/**
 * 策略一行（桌面 `channelnet.js:68-73` 的输入框/说明口径）：`预热 30s · 并发 4 · 停顿 1200ms`。
 *
 * 0 值写成「默认」——桌面输入框为空字符时也是"跟随默认"的意思，不是"关掉"。
 */
internal fun channelNetPolicyLine(row: ChannelNetRow): String {
    val parts = mutableListOf<String>()
    parts += if (row.prewarmSec > 0) "预热 ${row.prewarmSec}s" else "预热 默认"
    parts += if (row.maxInflight > 0) "并发 ${row.maxInflight}" else "并发 默认"
    if (row.stallTimeoutMs > 0) parts += "停顿 ${row.stallTimeoutMs}ms"
    return parts.joinToString(" · ")
}

/** 在途数文案（桌面 `channelnet.js:70` 的 chip「在途 N」）。 */
internal fun channelNetInflightLabel(row: ChannelNetRow): String = "在途 ${row.inflight}"

// ——— 网络连通诊断（`GET /api/network/diag`，`gateway.go:1213` → `netdiag.go:81-99`） ———

/** 服务端单次诊断最长 25 秒（`netdiag.go:96`），进行中必须把这件事告诉用户。 */
internal const val NET_DIAG_RUNNING_COPY: String = "诊断中…（最长约 25 秒）"

/**
 * 本地输入的解析结果：`host` + `port`（默认 443），或者一条**本地**校验错误。
 *
 * 规则照搬服务端（`netdiag.go:82-95`），能在本地拦的就不发请求：
 * - 空白 → 「先填上游域名」；
 * - host 含 `/ \ ? & @` 或空格 → 直接拒绝（服务端同样 400：「host 只能是域名或 IP」）；
 * - 支持 `host:port`（末尾一段纯数字才当端口）、`[v6]:port`、裸 IPv6（多冒号不当端口）；
 * - 端口必须是 1..65535 的数字。
 */
data class NetDiagInput(
    val host: String,
    val port: String,
    /** 非空表示本地校验失败，此时不要发请求。 */
    val error: String,
)

internal fun parseNetDiagInput(raw: String): NetDiagInput {
    val trimmed = raw.trim()
    if (trimmed.isEmpty()) {
        return NetDiagInput("", "443", "先填上游域名（例如 api.example.com 或 1.2.3.4:443）")
    }
    var host = trimmed
    var port = "443"

    // [::1]:443 —— 带方括号的 IPv6
    if (host.startsWith("[")) {
        val close = host.indexOf(']')
        if (close > 0) {
            val inside = host.substring(1, close)
            val rest = host.substring(close + 1)
            if (rest.startsWith(":")) {
                port = rest.substring(1).trim()
            } else if (rest.isNotEmpty()) {
                return NetDiagInput(inside, "443", "IPv6 地址后面只能跟端口，例如 [::1]:443")
            }
            host = inside
        }
    } else if (host.count { it == ':' } == 1) {
        // host:port —— 只有一段冒号、且冒号后全是数字才当端口；否则原样交给服务端
        val idx = host.lastIndexOf(':')
        val maybePort = host.substring(idx + 1)
        if (maybePort.isNotEmpty() && maybePort.all { it.isDigit() }) {
            port = maybePort
            host = host.substring(0, idx)
        }
    }

    host = host.trim()
    if (host.isEmpty()) {
        return NetDiagInput("", port, "缺少主机名")
    }
    // 与服务端同一套非法字符集（`netdiag.go:88`）
    if (host.any { it in "/\\?&@ " } || host.any { it.isWhitespace() }) {
        return NetDiagInput(host, port, "host 只能是域名或 IP（不能带 / \\ ? & @ 或空格）")
    }
    if (port.isNotEmpty() && (port.any { !it.isDigit() } || port.toIntOrNull() !in 1..65535)) {
        return NetDiagInput(host, "443", "端口必须是 1–65535 的数字")
    }
    return NetDiagInput(host, port.ifEmpty { "443" }, "")
}

/** 一个 IP 的探测结果（`netdiag.go` 的 `netDiagIPResult`）。 */
data class NetDiagIp(
    val ip: String,
    val ok: Boolean,
    val colo: String,
    val tcpMs: Long,
    val tcpError: String,
    val tlsMs: Long,
    val tlsError: String,
    val ttfbMs: Long,
    val httpStatus: Int,
    val httpError: String,
    val tlsVersion: String,
    val alpn: String,
    val verdict: String,
)

/** `GET /api/network/diag` 整包（`netdiag.go` 的 `netDiagResponse`）。 */
data class NetDiagReport(
    val host: String,
    val port: String,
    val systemDns: List<String>,
    val systemDnsError: String,
    val doh: Map<String, List<String>>,
    val dohErrors: Map<String, String>,
    val ips: List<NetDiagIp>,
    val summary: String,
    val elapsedMs: Long,
)

internal fun parseNetDiag(body: String): NetDiagReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "网络诊断返回的数据格式不正确")
    }
    val doh = LinkedHashMap<String, List<String>>()
    root.optJSONObject("doh")?.let { obj ->
        for (key in obj.keys()) {
            val arr = obj.optJSONArray(key) ?: continue
            val ips = (0 until arr.length()).mapNotNull { i ->
                arr.optString(i).trim().takeIf { it.isNotEmpty() }
            }
            if (ips.isNotEmpty()) {
                doh[key] = ips
            }
        }
    }
    val dohErrors = LinkedHashMap<String, String>()
    root.optJSONObject("doh_errors")?.let { obj ->
        for (key in obj.keys()) {
            val value = obj.optString(key).trim()
            if (value.isNotEmpty()) {
                dohErrors[key] = value
            }
        }
    }
    val ips = root.optJSONArray("ips")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val ip = item.optString("ip").trim()
            if (ip.isEmpty()) {
                return@mapNotNull null
            }
            NetDiagIp(
                ip = ip,
                ok = item.optBoolean("ok", false),
                colo = item.optString("colo").trim(),
                tcpMs = item.optLong("tcp_ms", 0L).coerceAtLeast(0L),
                tcpError = item.optString("tcp_error").trim(),
                tlsMs = item.optLong("tls_ms", 0L).coerceAtLeast(0L),
                tlsError = item.optString("tls_error").trim(),
                ttfbMs = item.optLong("ttfb_ms", 0L).coerceAtLeast(0L),
                httpStatus = item.optInt("http_status", 0),
                httpError = item.optString("http_error").trim(),
                tlsVersion = item.optString("tls_version").trim(),
                alpn = item.optString("alpn").trim(),
                verdict = item.optString("verdict").trim(),
            )
        }
    }.orEmpty()
    return NetDiagReport(
        host = root.optString("host").trim(),
        port = root.optString("port").trim().ifEmpty { "443" },
        systemDns = root.optJSONArray("system_dns")?.let { arr ->
            (0 until arr.length()).mapNotNull { i -> arr.optString(i).trim().takeIf { it.isNotEmpty() } }
        }.orEmpty(),
        systemDnsError = root.optString("system_dns_error").trim(),
        doh = doh,
        dohErrors = dohErrors,
        ips = ips,
        summary = root.optString("summary").trim(),
        elapsedMs = root.optLong("elapsed_ms", 0L).coerceAtLeast(0L),
    )
}

/** 一步诊断的展示行：名称 + 成败 + 耗时（桌面 `channelnet.js:153-163` 的 `parts` 口径）。 */
data class NetDiagStep(val label: String, val ok: Boolean, val detail: String)

/**
 * 把回包拆成「系统 DNS / DoH / 每个 IP 的 TCP·TLS·首字节」三类可读步骤。
 *
 * 桌面只展示 `summary` + 每个 IP 的 `TCP/TLS/首字节` 三段时间（`channelnet.js:121-124,153-163`）；
 * 这里额外把 DNS / DoH 的成败也列出来——服务端已经算好，不列白不列。
 */
internal fun netDiagSteps(report: NetDiagReport): List<NetDiagStep> {
    val steps = mutableListOf<NetDiagStep>()
    steps += NetDiagStep(
        label = "系统 DNS",
        ok = report.systemDnsError.isEmpty() && report.systemDns.isNotEmpty(),
        detail = when {
            report.systemDnsError.isNotEmpty() -> report.systemDnsError
            report.systemDns.isEmpty() -> "没有解析结果"
            else -> report.systemDns.joinToString(", ")
        },
    )
    if (report.doh.isNotEmpty() || report.dohErrors.isNotEmpty()) {
        val okParts = report.doh.entries.map { "${it.key}: ${it.value.joinToString(", ")}" }
        val errParts = report.dohErrors.entries.map { "${it.key}: ${it.value}" }
        steps += NetDiagStep(
            label = "DoH 对照",
            ok = report.doh.isNotEmpty(),
            detail = (okParts + errParts).joinToString("；").ifEmpty { "未查询" },
        )
    }
    for (ip in report.ips) {
        // 失败的那一跳必须把**原因**带出来：桌面 `channelnet.js:163` 的 chip 写的是
        // `tcp_error || tls_error || http_error || '不可用'`；只给耗时等于把失败原因吞掉。
        val timings = netDiagIpLine(ip)
        val reason = if (ip.ok) "" else netDiagIpVerdict(ip)
        val detail = when {
            reason.isEmpty() -> timings
            timings == "—" -> reason
            else -> "$timings · $reason"
        }
        steps += NetDiagStep(
            label = "TCP ${ip.ip}",
            ok = ip.ok,
            detail = detail,
        )
    }
    return steps
}

/** 单个 IP 的耗时行：`TCP 120ms · TLS 300ms · 首字节 480ms`；都没有时「—」。 */
internal fun netDiagIpLine(ip: NetDiagIp): String {
    val parts = mutableListOf<String>()
    if (ip.tcpMs > 0) parts += "TCP ${ip.tcpMs}ms"
    if (ip.tlsMs > 0) parts += "TLS ${ip.tlsMs}ms"
    if (ip.ttfbMs > 0) parts += "首字节 ${ip.ttfbMs}ms"
    if (ip.tlsVersion.isNotEmpty()) parts += ip.tlsVersion
    if (ip.alpn.isNotEmpty()) parts += ip.alpn
    return if (parts.isEmpty()) "—" else parts.joinToString(" · ")
}

/** 不可用的原因：优先具体错误，其次服务端 verdict（桌面 `channelnet.js:163`）。 */
internal fun netDiagIpVerdict(ip: NetDiagIp): String = when {
    ip.ok -> "可用"
    ip.tcpError.isNotEmpty() -> ip.tcpError
    ip.tlsError.isNotEmpty() -> ip.tlsError
    ip.httpError.isNotEmpty() -> ip.httpError
    ip.verdict.isNotEmpty() -> ip.verdict
    else -> "不可用"
}

/**
 * /healthz 正文恒为 ok，但有两种真实形状：
 * - 移动运行时在进入 Gateway 之前注册了同名路由，回 `{"status":"ok"}`
 *   （internal/mobile/runtime.go 的 `/healthz` handler）；
 * - 桌面网关 `handleHealthz` 回 `text/plain` 的 `ok\n`。
 * 两种都是"活着"，所以都只读 status 一个字段。
 */
internal fun parseHealthz(body: String): HealthzProbe {
    val text = body.trim()
    if (text.equals("ok", ignoreCase = true)) {
        return HealthzProbe(status = "ok", plainText = true)
    }
    val root = runCatching { JSONObject(text) }.getOrNull()
    val status = root?.optString("status")?.trim().orEmpty()
    if (status.equals("ok", ignoreCase = true)) {
        return HealthzProbe(status = "ok", plainText = false)
    }
    throw ManagementApiException("invalid_response", "存活探针返回的内容不是 ok")
}

/** /api/health（匿名）。字段见 admin.go 的 handleHealth。 */
internal fun parseHealth(body: String): HealthProbe {
    val label = "健康接口"
    val root = objectOrThrow(body, label)
    requireKeys(root, label, listOf("status", "uptime_sec", "providers", "accounts", "version", "revision"))
    val status = root.optString("status").trim()
    if (status.isEmpty()) {
        throw ManagementApiException("invalid_response", "$label 的 status 为空")
    }
    return HealthProbe(
        status = status,
        uptimeSec = root.optInt("uptime_sec", 0).coerceAtLeast(0),
        providers = root.optInt("providers", 0).coerceAtLeast(0),
        accounts = root.optInt("accounts", 0).coerceAtLeast(0),
        version = root.optString("version").trim(),
        revision = root.optString("revision").trim(),
    )
}

/** [httpStatus] 由调用方传入：200 与 503 都是同一份 JSON，差别只在 ready/reasons。 */
internal fun parseReadyz(body: String, httpStatus: Int = HttpURLConnection.HTTP_OK): ReadyzProbe {
    val label = "就绪探针"
    val root = objectOrThrow(body, label)
    requireKeys(
        root,
        label,
        listOf(
            "ready",
            "models",
            "providers",
            "providers_in_use",
            "accounts",
            "usable_accounts",
            "uptime_sec",
            "reasons",
        ),
    )
    val reasonsArray = root.optJSONArray("reasons")
        ?: throw ManagementApiException("invalid_response", "$label 的 reasons 不是字符串数组")
    val reasons = (0 until reasonsArray.length()).mapNotNull { index ->
        reasonsArray.optString(index).trim().ifBlank { null }
    }
    return ReadyzProbe(
        httpStatus = httpStatus,
        ready = root.optBoolean("ready", false),
        models = root.optInt("models", 0).coerceAtLeast(0),
        providers = root.optInt("providers", 0).coerceAtLeast(0),
        providersInUse = root.optInt("providers_in_use", 0).coerceAtLeast(0),
        accounts = root.optInt("accounts", 0).coerceAtLeast(0),
        usableAccounts = root.optInt("usable_accounts", 0).coerceAtLeast(0),
        uptimeSec = root.optInt("uptime_sec", 0).coerceAtLeast(0),
        reasons = reasons,
    )
}

internal fun parseVersion(body: String): VersionInfo {
    val label = "版本接口"
    val root = objectOrThrow(body, label)
    requireKeys(root, label, listOf("version", "go", "revision", "build_time", "build_time_source", "uptime_sec"))
    val version = root.optString("version").trim()
    if (version.isEmpty()) {
        throw ManagementApiException("invalid_response", "$label 的 version 为空")
    }
    return VersionInfo(
        version = version,
        go = root.optString("go").trim(),
        revision = root.optString("revision").trim(),
        buildTime = root.optString("build_time").trim(),
        buildTimeSource = root.optString("build_time_source").trim(),
        uptimeSec = root.optInt("uptime_sec", 0).coerceAtLeast(0),
    )
}

/** 只做全文统计：非空行数 + 前 5 行预览，不建指标对象图。 */
internal fun parseMetrics(body: String): MetricsDigest {
    val lines = body.lineSequence().map { it.trimEnd() }.filter { it.isNotBlank() }.toList()
    if (lines.isEmpty()) {
        throw ManagementApiException("invalid_response", "指标接口返回空文本")
    }
    return MetricsDigest(
        lineCount = lines.size,
        charCount = body.length,
        preview = lines.take(METRICS_PREVIEW_LINES).map { it.take(160) },
    )
}

internal fun parseRuntime(body: String): RuntimeInfo {
    val label = "运行时接口"
    val root = objectOrThrow(body, label)
    requireKeys(
        root,
        label,
        listOf(
            "uptime_sec",
            "go_version",
            "goroutines",
            "heap_mb",
            "state_records",
            "models",
            "tun",
            "public_addr",
        ),
    )
    return RuntimeInfo(
        uptimeSec = root.optInt("uptime_sec", 0).coerceAtLeast(0),
        goVersion = root.optString("go_version").trim(),
        goroutines = root.optInt("goroutines", 0).coerceAtLeast(0),
        heapMb = root.optInt("heap_mb", 0).coerceAtLeast(0),
        stateRecords = root.optInt("state_records", 0).coerceAtLeast(0),
        models = root.optInt("models", 0).coerceAtLeast(0),
        tun = root.optBoolean("tun", false),
        publicAddr = root.optString("public_addr").trim(),
    )
}
