package com.muyuchat.aether.runtime

import java.io.BufferedInputStream
import java.io.InputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.nio.charset.StandardCharsets
import kotlin.concurrent.thread
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Drives the real [SettingsApiClient] over loopback so the wire contract for
 * custom redaction rules is pinned independently of JSON-builder unit tests.
 * The admin session is a fake fixture value and must only appear in the
 * Authorization header, never in the request path/query.
 *
 * `jdk.httpserver` is not visible to Android's unit-test boot classpath, so
 * this fixture uses a plain one-connection-at-a-time [ServerSocket].
 */
class SettingsRedactRulesHttpTest {
    private data class Request(
        val method: String,
        val path: String,
        val authorization: String?,
        val contentType: String?,
        val body: String,
    )

    private lateinit var server: ServerSocket
    private lateinit var serverThread: Thread
    private val requests = mutableListOf<Request>()
    @Volatile private var responseCode = 200
    @Volatile private var responseBody = "{}"
    private lateinit var status: GatewayStatus

    @Before
    fun startServer() {
        server = ServerSocket(0, 4, InetAddress.getByName("127.0.0.1"))
        serverThread = thread(isDaemon = true, name = "redact-rules-http-fixture") {
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
            adminSession = "fixture-admin-session",
        )
    }

    @After
    fun stopServer() {
        server.close()
        serverThread.join(2_000)
    }

    @Test
    fun replacePostsWholeRuleArrayAndUsesServerReadback() {
        responseBody = """
            {"redactRules":[
              {"kind":"API_KEY","prefix":"oc_sk_"},
              {"kind":"email","regex":"[a-z]+@example\\\\.com"}
            ]}
        """.trimIndent()

        val readback = SettingsApiClient().replaceRedactRules(
            status,
            listOf(
                RedactCustomRule("API_KEY", prefix = " oc_sk_ "),
                RedactCustomRule("email", regex = "[a-z]+@example\\\\.com"),
            ),
        )

        val sent = requests.single()
        assertEquals("POST", sent.method)
        assertEquals("/api/settings/redact-rules", sent.path)
        assertEquals("Bearer fixture-admin-session", sent.authorization)
        assertEquals("application/json", sent.contentType)
        assertTrue("session must not leak into the URL", !sent.path.contains("fixture-admin-session"))

        val rules = JSONObject(sent.body).getJSONArray("rules")
        assertEquals(2, rules.length())
        assertEquals("API_KEY", rules.getJSONObject(0).getString("kind"))
        assertEquals("oc_sk_", rules.getJSONObject(0).getString("prefix"))
        assertTrue(!rules.getJSONObject(0).has("regex"))
        assertEquals("[a-z]+@example\\\\.com", rules.getJSONObject(1).getString("regex"))
        assertTrue(!rules.getJSONObject(1).has("prefix"))

        assertEquals(
            listOf(
                RedactCustomRule("API_KEY", prefix = "oc_sk_"),
                RedactCustomRule("email", regex = "[a-z]+@example\\\\.com"),
            ),
            readback,
        )
    }

    @Test
    fun editPostsAtWasAndRuleThenUsesServerReadback() {
        responseBody = """{"redactRules":[{"kind":"NEW","regex":"new_[0-9]+"}]}"""

        val readback = SettingsApiClient().editRedactRule(
            status = status,
            at = 1,
            was = RedactCustomRule("OLD", prefix = "old_"),
            rule = RedactCustomRule("NEW", regex = "new_[0-9]+"),
        )

        val sent = requests.single()
        assertEquals("POST", sent.method)
        assertEquals("/api/settings/redact-rule", sent.path)
        assertEquals("Bearer fixture-admin-session", sent.authorization)

        val body = JSONObject(sent.body)
        assertEquals(1, body.getInt("at"))
        assertEquals("OLD", body.getJSONObject("was").getString("kind"))
        assertEquals("old_", body.getJSONObject("was").getString("prefix"))
        assertTrue(!body.getJSONObject("was").has("regex"))
        assertEquals("NEW", body.getJSONObject("rule").getString("kind"))
        assertEquals("new_[0-9]+", body.getJSONObject("rule").getString("regex"))
        assertTrue(!body.getJSONObject("rule").has("prefix"))

        assertEquals(listOf(RedactCustomRule("NEW", regex = "new_[0-9]+")), readback)
    }

    @Test
    fun conflictPreservesServerMessageAndStatusCode() {
        responseCode = 409
        responseBody = """{"error":"规则已被其他客户端修改，请刷新后重试"}"""

        val error = runCatching {
            SettingsApiClient().editRedactRule(
                status = status,
                at = 0,
                was = RedactCustomRule("OLD", prefix = "old_"),
                rule = RedactCustomRule("NEW", prefix = "new_"),
            )
        }.exceptionOrNull()

        assertTrue(error is ManagementApiException)
        assertEquals("http_409", (error as ManagementApiException).code)
        assertEquals("规则已被其他客户端修改，请刷新后重试", error.message)
        assertEquals(1, requests.size)
    }

    @Test
    fun malformedSuccessBodyIsRejectedInsteadOfReturningEmptyRules() {
        responseBody = "not-json"

        val error = runCatching {
            SettingsApiClient().replaceRedactRules(status, listOf(RedactCustomRule("X", prefix = "x_")))
        }.exceptionOrNull()

        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }

    private fun serve(socket: Socket) {
        val input = BufferedInputStream(socket.getInputStream())
        val requestLine = readAsciiLine(input) ?: return
        val parts = requestLine.split(' ')
        if (parts.size < 2) return
        val method = parts[0]
        val path = parts[1]

        val headers = mutableMapOf<String, String>()
        while (true) {
            val line = readAsciiLine(input) ?: break
            if (line.isEmpty()) break
            val separator = line.indexOf(':')
            if (separator > 0) {
                headers[line.substring(0, separator).trim().lowercase()] =
                    line.substring(separator + 1).trim()
            }
        }
        val contentLength = headers["content-length"]?.toIntOrNull() ?: 0
        val body = ByteArray(contentLength).also { bytes ->
            var offset = 0
            while (offset < bytes.size) {
                val read = input.read(bytes, offset, bytes.size - offset)
                if (read < 0) break
                offset += read
            }
        }.toString(StandardCharsets.UTF_8)

        synchronized(requests) {
            requests += Request(
                method = method,
                path = path,
                authorization = headers["authorization"],
                contentType = headers["content-type"],
                body = body,
            )
        }

        val bytes = responseBody.toByteArray(StandardCharsets.UTF_8)
        val reason = if (responseCode == 200) "OK" else "Error"
        val head = buildString {
            append("HTTP/1.1 $responseCode $reason\r\n")
            append("Content-Type: application/json\r\n")
            append("Content-Length: ${bytes.size}\r\n")
            append("Connection: close\r\n")
            append("\r\n")
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
