package com.muyuchat.aether.runtime

import java.io.BufferedInputStream
import java.io.InputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.nio.charset.StandardCharsets
import kotlin.concurrent.thread
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * 真机回归：`/readyz` 的 **HTTP 503** 是"网关活着但没就绪"的合法应答，不是网络故障。
 *
 * 发现于真机 98a37aa7：`accounts=0` 时 `/readyz` 回 503，页面显示「无法连接本机网关 / network_error」。
 * 根因是 `HttpURLConnection` 只在**非错误状态码**时才允许读 `inputStream`；对 503 调
 * `getInputStream()` 会先抛 `IOException`，于是被最外层的 `catch (IOException)` 折成 network_error，
 * 503 的 JSON 正文根本没被读到。
 *
 * `jdk.httpserver` 不在 Android 单测的 boot classpath 上，所以这里用裸 [ServerSocket] 起一个
 * 只答一次的 fixture——与 `SettingsRedactRulesHttpTest` 同一套路。
 */
class ProbeApiHttpTest {
    private lateinit var server: ServerSocket
    private lateinit var serverThread: Thread
    private lateinit var status: GatewayStatus
    private val paths = mutableListOf<String>()

    @Before
    fun startFixture() {
        server = ServerSocket(0, 8, InetAddress.getByName("127.0.0.1"))
        serverThread = thread(isDaemon = true, name = "probe-http-fixture") {
            while (!server.isClosed) {
                try {
                    server.accept().use { socket -> serve(socket) }
                } catch (_: SocketException) {
                    break
                }
            }
        }
        status = GatewayStatus(
            state = GatewayState.RUNNING,
            addr = "127.0.0.1:${server.localPort}",
            adminSession = "fixture-session",
        )
    }

    @After
    fun stopFixture() {
        server.close()
        serverThread.join(2_000)
    }

    @Test
    fun notReady503IsAParsedOutcomeNotANetworkError() {
        val report = ProbeApiClient().fetchSession(status)

        val readyz = report.readyz
        assertTrue(
            "503 必须被解析成 ReadyzProbe，实际是 $readyz",
            readyz is ProbeOutcome.Ok,
        )
        val value = (readyz as ProbeOutcome.Ok).value
        assertEquals(503, value.httpStatus)
        assertEquals(false, value.ready)
        assertEquals(0, value.accounts)
        assertEquals(listOf("还没有任何账号：先在渠道页添加账号"), value.reasons)

        // 另外三个端点是正常 200，不能被 503 连坐。
        assertTrue(report.version is ProbeOutcome.Ok)
        assertTrue(report.metrics is ProbeOutcome.Ok)
        assertTrue(report.runtime is ProbeOutcome.Ok)
    }

    @Test
    fun sessionRequestsCarryBearerAndAnonymousRequestsDoNot() {
        ProbeApiClient().fetchSession(status)
        ProbeApiClient().fetchAnonymous(status)

        val sessionPaths = listOf("/readyz", "/version", "/metrics", "/api/runtime")
        val anonymousPaths = listOf("/healthz", "/api/health")
        sessionPaths.forEach { path ->
            assertTrue("$path 应该被请求过", paths.contains(path))
            assertTrue("$path 应该带 Bearer", authByPath.getValue(path) == "Bearer fixture-session")
        }
        anonymousPaths.forEach { path ->
            assertTrue("$path 应该被请求过", paths.contains(path))
            assertTrue("$path 不得带任何凭据", authByPath[path] == null)
        }
    }

    private val authByPath = mutableMapOf<String, String?>()

    private fun serve(socket: Socket) {
        val input = BufferedInputStream(socket.getInputStream())
        val requestLine = readAsciiLine(input) ?: return
        val parts = requestLine.split(' ')
        if (parts.size < 2) return
        val path = parts[1]

        var authorization: String? = null
        while (true) {
            val line = readAsciiLine(input) ?: break
            if (line.isEmpty()) break
            val separator = line.indexOf(':')
            if (separator > 0 && line.substring(0, separator).trim().equals("authorization", true)) {
                authorization = line.substring(separator + 1).trim()
            }
        }
        synchronized(paths) {
            paths += path
            authByPath[path] = authorization
        }

        val (code, body) = when (path) {
            "/readyz" -> 503 to """
                {"ready":false,"models":0,"providers":62,"providers_in_use":0,
                 "accounts":0,"usable_accounts":0,"uptime_sec":39,
                 "reasons":["还没有任何账号：先在渠道页添加账号"]}
            """.trimIndent().replace("\n", "")
            "/version" -> 200 to """{"version":"dev","go":"go1.27.3","revision":"abc","build_time":"t","build_time_source":"vcs","uptime_sec":39}"""
            "/metrics" -> 200 to "aether_uptime_seconds 39\n"
            "/api/runtime" -> 200 to """{"uptime_sec":39,"go_version":"go1.27.3","goroutines":7,"heap_mb":3,"state_records":0,"models":0,"tun":false,"public_addr":"http://127.0.0.1:1"}"""
            "/healthz" -> 200 to """{"status":"ok"}"""
            "/api/health" -> 200 to """{"status":"ok","uptime_sec":39,"providers":62,"accounts":0,"version":"dev","revision":"abc"}"""
            else -> 404 to """{"error":"not found"}"""
        }
        val bytes = body.toByteArray(StandardCharsets.UTF_8)
        val head = buildString {
            append("HTTP/1.1 $code ${if (code == 200) "OK" else "Service Unavailable"}\r\n")
            append("Content-Type: application/json\r\n")
            append("Content-Length: ${bytes.size}\r\n")
            append("Connection: close\r\n\r\n")
        }.toByteArray(StandardCharsets.US_ASCII)
        socket.getOutputStream().apply {
            write(head)
            write(bytes)
            flush()
        }
    }

    private fun readAsciiLine(input: InputStream): String? {
        val line = StringBuilder()
        while (true) {
            val value = input.read()
            if (value < 0) return if (line.isEmpty()) null else line.toString()
            if (value == '\n'.code) break
            if (value != '\r'.code) line.append(value.toChar())
        }
        return line.toString()
    }
}
