package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 手机端导出账号包（把本机账号交回桌面）。
 *
 * 三条红线都有断言：
 * 1. 口令强度本地先挡（服务端只拒空口令）；
 * 2. 落盘文件名**不含口令**，也不含账号信息；
 * 3. 服务端没给 data 时必须报错，**不能生成一个空包**（空包在桌面上导入会失败，还会让人以为导成功了）。
 */
class AccountExportApiTest {
    @Test
    fun parsesInlineExportResponse() {
        val payload = "AETHER2" + "x".repeat(40)
        val export = parseAccountExport(
            """{"format":"aether-accounts","version":1,"accounts":3,"encrypted":true,
               "filename":"aether-accounts-20261104-030000.aether-accounts","data":"$payload"}""",
        )
        assertEquals(payload, export.base64)
        assertEquals(3, export.accounts)
        assertEquals("aether-accounts-20261104-030000.aether-accounts", export.filename)
    }

    @Test
    fun missingDataIsRejected() {
        for (body in listOf("""{"accounts":0,"encrypted":true}""", """{"data":""}""")) {
            val error = runCatching { parseAccountExport(body) }.exceptionOrNull()
            assertTrue("body=$body 应报 invalid_response", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
        val malformed = runCatching { parseAccountExport("not-json") }.exceptionOrNull()
        assertTrue(malformed is ManagementApiException)
    }

    @Test
    fun unencryptedResponseIsRejected() {
        // 服务端如果回了 encrypted=false（理论上不该发生），手机端不能把明文当账号包存下来。
        val error = runCatching {
            parseAccountExport("""{"encrypted":false,"accounts":1,"data":"QUJD"}""")
        }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }

    @Test
    fun passphraseMustBeAtLeastEightChars() {
        assertNull(validateAccountExportPassphrase("correct-horse"))
        assertEquals("口令不能为空", validateAccountExportPassphrase("  "))
        assertEquals("口令至少 8 位：账号包离开手机后可以被离线尝试", validateAccountExportPassphrase("abc1234"))
    }

    @Test
    fun payloadOnlyCarriesPassphrase() {
        val json = JSONObject(buildAccountExportJson("s3cret-pass"))
        assertEquals("s3cret-pass", json.getString("passphrase"))
        assertEquals(1, json.length())
    }

    @Test
    fun fallbackFileNameHasNoPassphraseAndMatchesDesktopStyle() {
        val name = accountExportFileName(1_800_000_000_000L)
        assertTrue(Regex("""aether-accounts-\d{8}-\d{6}\.aether-accounts""").matches(name))
        // 不含口令字符（用常见口令片段做粗检）
        assertTrue(!name.contains("s3cret"))
    }
}
