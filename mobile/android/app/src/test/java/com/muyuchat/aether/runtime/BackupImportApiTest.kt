package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 备份导入。字段名是 **`pass`**（不是 `passphrase`）——照 handler 抄的，不猜。
 *
 * 覆盖语义（读 `applyBundle` 确认）：
 * - **设置会被覆盖**：隐私脱敏、价格表、虚拟组、UI、禁用渠道、排序、CORS、IP 白名单、配置档、预设；
 * - **资源库按 ID 合并**（同 ID 覆盖，其余保留）；
 * - **账号池不清空**：只按 `account_id`（或 provider+label）更新匹配到的账号标签；
 * - 凭据**不会随备份回来**（`NeedKey` 会列出需要重新填的渠道）。
 */
class BackupImportApiTest {
    @Test
    fun bodyUsesHandlerFieldNames() {
        val json = JSONObject(buildBackupImportJson(base64 = "QUJD", pass = "abcdefgh"))
        assertEquals("QUJD", json.getString("data"))
        assertEquals("abcdefgh", json.getString("pass"))
        assertTrue("不能把口令发成 passphrase 字段", !json.has("passphrase"))
        assertEquals(2, json.length())
    }

    @Test
    fun passphraseMustBeAtLeastEightChars() {
        assertNull(validateBackupImportPassphrase("abcdefgh"))
        assertEquals("口令不能为空", validateBackupImportPassphrase("   "))
        assertEquals("口令至少 8 位：备份文件离开手机后可以被离线尝试", validateBackupImportPassphrase("abc1234"))
    }

    @Test
    fun missingDataIsRejectedBeforeRequest() {
        val error = runCatching { buildBackupImportJson(base64 = "  ", pass = "abcdefgh") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("no_data", (error as ManagementApiException).code)
    }

    @Test
    fun parsesImportResult() {
        val result = parseBackupImportResult(
            """{"Added":0,"Replaced":0,"Profiles":2,"Agents":0,
                "NeedKey":["antigravity","workbuddy-cn"],"SignIn":[],"Plugins":[],
                "PluginErrors":{},"Library":true,"LibraryProblems":[],
                "imported":{"settings":true,"library":true,"accounts":true,"specs":false}}""",
        )
        assertTrue(result.settings)
        assertTrue(result.library)
        assertTrue(result.accounts)
        assertEquals(false, result.specs)
        assertEquals(2, result.profiles)
        assertEquals(listOf("antigravity", "workbuddy-cn"), result.needKey)
    }

    @Test
    fun summaryNamesOverwrittenPartsAndNeedKey() {
        val result = parseBackupImportResult(
            """{"Profiles":1,"NeedKey":["antigravity"],
                "imported":{"settings":true,"library":true,"accounts":false}}""",
        )
        val line = backupImportSummary(result)
        assertTrue("要说明设置被覆盖：$line", line.contains("设置"))
        assertTrue("要说明资源库合并：$line", line.contains("资源库"))
        assertTrue("要列出需要重填凭据的渠道：$line", line.contains("antigravity"))
        // 没有账号被更新时不要提账号
        assertTrue("accounts=false 时不该提账号：$line", !line.contains("账号标签"))
    }

    @Test
    fun emptyResultIsHonest() {
        val result = parseBackupImportResult("""{"imported":{}}""")
        assertTrue(backupImportSummary(result).contains("没有可应用"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseBackupImportResult(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
