package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsRedactPersonalTest {
    @Test
    fun bodyCarriesOnlyRedactPersonalKey() {
        // 服务端 applyUIPrefs 按键合并；多带键会污染 settings.ui，所以只发这一个。
        val on = JSONObject(buildRedactPersonalJson(true))
        assertEquals(1, on.length())
        assertTrue(on.has("redactPersonal"))
        assertTrue(on.getBoolean("redactPersonal"))
        val off = JSONObject(buildRedactPersonalJson(false))
        assertEquals(1, off.length())
        assertFalse(off.getBoolean("redactPersonal"))
    }

    @Test
    fun bodyDoesNotUseStructuredOrReservedKeys() {
        // privacy_mask 是整结构体替换、且在 reservedSettingKeys 里，走它会清字段或存不下。
        for (enabled in listOf(true, false)) {
            val body = JSONObject(buildRedactPersonalJson(enabled))
            assertFalse(body.has("privacy_mask"))
            assertFalse(body.has("redact"))
            assertFalse(body.has("port"))
            assertFalse(body.has("lan"))
        }
    }

    @Test
    fun readbackUsesServerValue() {
        // POST /api/settings 的响应就是 settingsView，顶层带 redactPersonal。
        assertTrue(parseRedactPersonal("""{"redactPersonal":true,"redact":true,"lan":false}"""))
        assertFalse(parseRedactPersonal("""{"redactPersonal":false,"settings":{"redactPersonal":false}}"""))
    }

    @Test
    fun missingKeyOrBadJsonIsRejectedNotDefaulted() {
        // 缺键不能静默当成 false —— 那会让"保存成功但没生效"看起来正常。
        val missing = runCatching { parseRedactPersonal("""{"redact":true}""") }.exceptionOrNull()
        assertTrue("缺 redactPersonal 必须报错", missing is ManagementApiException)
        val malformed = runCatching { parseRedactPersonal("not json") }.exceptionOrNull()
        assertTrue(malformed is ManagementApiException)
    }

    @Test
    fun summaryReadsPersonalFlagFromSameKey() {
        val summary = parseSettings("""{"redact":true,"redactPersonal":true}""")
        assertTrue(summary.redactEnabled)
        assertTrue(summary.redactPersonal)
    }
}
