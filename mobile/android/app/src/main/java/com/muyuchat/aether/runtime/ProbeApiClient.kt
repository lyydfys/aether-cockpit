package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

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

    /** 凭据只由 [probeAuthorization] 决定，调用点不可能"顺手"把 Bearer 带到匿名端点。 */
    private fun probe(
        status: GatewayStatus,
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
