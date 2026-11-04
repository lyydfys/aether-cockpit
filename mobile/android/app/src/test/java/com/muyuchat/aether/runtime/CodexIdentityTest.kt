package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Codex 请求身份对照：`GET /api/providers/codex/identity?limit=5`
 * （`gateway.go:1150` → `identity_api.go:107-127`；桌面卡片 `internal/web/ui2/js/views/gateway.js:141-175`）。
 *
 * 回包：`{"expected":{user_agent,originator,session_id},"items":[{time,user_agent,originator,
 * session_id,chatgpt_account_id,openai_beta,stream,official_like,mismatches[]}],"note":…}`
 */
class CodexIdentityTest {
    @Test
    fun identityEndpointNeedsSession() {
        // /api/ 管理面：不带 Bearer 会 401
        assertTrue(probeNeedsSession("/api/providers/codex/identity"))
    }

    @Test
    fun parsesExpectedItemsAndNote() {
        val report = parseCodexIdentity(
            """
            {"expected":{"user_agent":"codex_cli_rs/<version>","originator":"codex_cli_rs","session_id":"UUID"},
             "items":[
               {"time":1762290000,"user_agent":"codex_cli_rs/0.48.0","originator":"codex_cli_rs",
                "session_id":"0123456789abcdef","stream":true,"official_like":true},
               {"time":1762289000,"user_agent":"curl/8.5","official_like":false,
                "mismatches":["originator 缺失","UA 不是 codex_cli_rs 前缀"]}],
             "note":"仅观测下游请求头，不改写客户端身份。"}
            """.trimIndent(),
        )
        assertEquals("codex_cli_rs/<version>", report.expected["user_agent"])
        assertEquals(2, report.items.size)
        val ok = report.items[0]
        assertTrue(ok.officialLike)
        assertTrue(ok.stream)
        assertEquals("codex_cli_rs/0.48.0", ok.userAgent)
        assertEquals("0123456789abcdef", ok.sessionId)
        val bad = report.items[1]
        assertFalse(bad.officialLike)
        assertEquals(2, bad.mismatches.size)
        assertTrue(report.note.contains("不改写客户端身份"))
    }

    @Test
    fun parsesEmptyItemsAndMissingFields() {
        val empty = parseCodexIdentity("""{"items":[],"note":"仅观测"}""")
        assertTrue(empty.items.isEmpty())
        assertTrue(empty.expected.isEmpty())
        // 缺字段的条目仍是合法样本（UA 全空 → 显示「（无身份头）」）
        val partial = parseCodexIdentity("""{"items":[{"time":1}]}""")
        assertEquals(1, partial.items.size)
        assertEquals("（无身份头）", codexIdentityBits(partial.items[0]))
        assertFalse(partial.items[0].officialLike)
        assertTrue(partial.items[0].mismatches.isEmpty())
    }

    @Test
    fun brokenBodiesAreInvalidResponse() {
        for (body in listOf("not json", "", "<html>401</html>", "[1,2]")) {
            val error = runCatching { parseCodexIdentity(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun expectedLineMatchesDesktopWording() {
        val report = parseCodexIdentity(
            """{"expected":{"user_agent":"codex_cli_rs/<version>","originator":"codex_cli_rs","session_id":"UUID"}}""",
        )
        // 桌面 gateway.js:154：期望：User-Agent … · originator … · session_id
        assertEquals(
            "期望：User-Agent codex_cli_rs/<version> · originator codex_cli_rs · session_id",
            codexIdentityExpectedLine(report),
        )
        // expected 缺失时整行不输出（不编一个假期望）
        assertEquals("", codexIdentityExpectedLine(parseCodexIdentity("""{"items":[]}""")))
    }

    @Test
    fun bitsShowTruncatedSessionAndFallback() {
        val item = CodexIdentity(
            time = 0L,
            userAgent = "codex_cli_rs/0.48.0",
            originator = "codex_cli_rs",
            sessionId = "0123456789abcdef",
            chatgptAccountId = "",
            openaiBeta = "",
            stream = true,
            officialLike = true,
            mismatches = emptyList(),
        )
        // 截断规则来自桌面 `gateway.js:157-159`：**UA 与 originator 原样**，只有 session 取
        // `String(it.session_id).slice(0, 8)`（`:159`）。这里逐字对齐，别把 UA 也当成截断字段。
        assertEquals("UA codex_cli_rs/0.48.0 · originator codex_cli_rs · session 01234567", codexIdentityBits(item))
        assertEquals("（无身份头）", codexIdentityBits(item.copy(userAgent = "", originator = "", sessionId = "")))
        // 只有 UA 时也能拼；UA 本身**不截断**（换成一个短 UA 只是为了让期望好读）
        assertEquals(
            "UA curl/8.5",
            codexIdentityBits(item.copy(userAgent = "curl/8.5", originator = "", sessionId = "")),
        )
        // 反向确认：长 UA 原样保留，不会被截成 8 个字符
        val longUa = codexIdentityBits(item.copy(originator = "", sessionId = ""))
        assertTrue(longUa, longUa.contains("codex_cli_rs/0.48.0"))
        assertFalse(longUa.contains("session "))
    }

    @Test
    fun statusLabelsAndMismatchLineMatchDesktop() {
        val ok = CodexIdentity(0L, "ua", "o", "s", "", "", true, officialLike = true, mismatches = emptyList())
        assertEquals("官方形状", codexIdentityStatusLabel(ok))
        assertEquals("有差异", codexIdentityStatusLabel(ok.copy(officialLike = false)))
        // 差异用「；」连接（gateway.js:163）
        assertEquals("A；B", codexIdentityMismatchLine(ok.copy(mismatches = listOf("A", "B"))))
        assertEquals("", codexIdentityMismatchLine(ok))
    }

    @Test
    fun emptyCopyIsVerbatimFromDesktop() {
        assertEquals(
            "还没有收到 Responses 请求；客户端发起一次对话后这里会出现身份对照。",
            CODEX_IDENTITY_EMPTY_COPY,
        )
    }
}
