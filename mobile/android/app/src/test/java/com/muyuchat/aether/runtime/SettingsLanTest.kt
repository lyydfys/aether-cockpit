package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsLanTest {
    @Test
    fun toggleBodyOnlySendsOn() {
        val on = JSONObject(buildLanToggleJson(true))
        assertEquals(1, on.length())
        assertTrue(on.getBoolean("on"))
        assertFalse(JSONObject(buildLanToggleJson(false)).getBoolean("on"))
    }

    @Test
    fun readbackUsesServerView() {
        assertTrue(parseLanEnabled("""{"lan":true,"lanURLs":["http://192.168.1.5:42689/v1"]}"""))
        assertFalse(parseLanEnabled("""{"lan":false,"lanURLs":[]}"""))
        val error = runCatching { parseLanEnabled("not json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
    }

    @Test
    fun settingsErrorBodyIsSurfaced() {
        assertEquals(
            "先在网关页创建一个调用方密钥（或设置主 API Key），否则远程请求会全部被拒绝",
            parseSettingsError("""{"error":"先在网关页创建一个调用方密钥（或设置主 API Key），否则远程请求会全部被拒绝"}"""),
        )
        assertEquals(null, parseSettingsError("not json"))
    }
}
