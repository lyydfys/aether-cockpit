package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsWriteTest {
    @Test
    fun toggleBodyOnlySendsEnabled() {
        val on = JSONObject(buildRedactToggleJson(true))
        assertEquals(1, on.length())
        assertTrue(on.getBoolean("enabled"))
        val off = JSONObject(buildRedactToggleJson(false))
        assertEquals(1, off.length())
        assertFalse(off.getBoolean("enabled"))
    }

    @Test
    fun readbackUsesServerValue() {
        val body = """{"enabled":true,"rules":["keys"],"actions":{"keys":"mask"},"redact":true}"""
        assertTrue(parseRedactEnabled(body))
        assertFalse(parseRedactEnabled("""{"enabled":false,"rules":[]}"""))
        val error = runCatching { parseRedactEnabled("not json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
    }
}
