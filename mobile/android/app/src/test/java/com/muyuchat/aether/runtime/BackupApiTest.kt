package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 备份导出的纯逻辑。
 *
 * 三条规则必须有测试锁：
 * 1. 口令强度不够时**在本地就拒绝**，不要等服务端 400 才知道；
 * 2. 落盘文件名**不能**包含口令，也不能包含任何账号/服务商信息；
 * 3. 响应里没有 `data` 时必须报错，绝不能生成一个空备份文件。
 */
class BackupApiTest {
    @Test
    fun parsesInlineExportResponse() {
        val blob = "AETHER2" + "x".repeat(40)
        val export = parseBackupExport(
            """{"data":"${blob}","bytes":47,"created_at":1800000000}""",
        )
        assertEquals(blob, export.base64)
        assertEquals(47, export.bytes)
        assertEquals(1_800_000_000L, export.createdAt)
    }

    @Test
    fun emptyDataIsRejected() {
        // 服务端没给 data（例如落盘分支）时，不能"成功"生成一个空文件。
        for (body in listOf("""{"bytes":0}""", """{"data":""}""")) {
            val error = runCatching { parseBackupExport(body) }.exceptionOrNull()
            assertTrue("body=$body 应报 invalid_response", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
        val malformed = runCatching { parseBackupExport("not-json") }.exceptionOrNull()
        assertTrue(malformed is ManagementApiException)
    }

    @Test
    fun passphraseMustBeAtLeastEightCharacters() {
        assertNull(validateBackupPassphrase("correct-horse"))
        assertEquals("口令不能为空", validateBackupPassphrase(""))
        assertEquals("口令不能为空", validateBackupPassphrase("   "))
        assertNotNull("短口令必须本地拒绝", validateBackupPassphrase("abc1234"))
        // 8 个字符是下限，刚好 8 位通过。
        assertNull(validateBackupPassphrase("12345678"))
    }

    @Test
    fun filenameCarriesNoPassphraseAndLooksLikeTheDesktopOne() {
        val name = backupFileName(1_800_000_000_000L)
        assertTrue(name.startsWith("aether-backup-"))
        assertTrue(name.endsWith(".aether-backup"))
        // 桌面端用 20260102-150405 这种时间戳；手机端保持同名风格便于识别。
        assertTrue("时间戳格式应与桌面一致: $name", Regex("""aether-backup-\d{8}-\d{6}\.aether-backup""").matches(name))
    }

    @Test
    fun buildExportPayloadOnlyCarriesPassphraseAndInlineFlag() {
        val json = org.json.JSONObject(buildInlineExportJson("s3cret-pass"))
        assertEquals("s3cret-pass", json.getString("pass"))
        assertTrue(json.getBoolean("inline"))
        // 不要把账号、路径之类的额外信息塞进请求体。
        assertEquals(2, json.length())
    }
}
