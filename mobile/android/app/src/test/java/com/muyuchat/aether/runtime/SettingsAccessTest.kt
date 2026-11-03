package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class SettingsAccessTest {
    @Test
    fun corsBodyCarriesWholeList() {
        val json = JSONObject(buildCorsJson(listOf("http://localhost:3000", "chrome-extension://abc")))
        val origins = json.getJSONArray("origins")
        assertEquals(1, json.length())
        assertEquals(2, origins.length())
        assertEquals("http://localhost:3000", origins.getString(0))
    }

    @Test
    fun allowIpsBodyCarriesWholeList() {
        val json = JSONObject(buildAllowIpsJson(listOf("127.0.0.1", "10.0.0.0/8")))
        val ips = json.getJSONArray("ips")
        assertEquals(1, json.length())
        assertEquals(2, ips.length())
        assertEquals("10.0.0.0/8", ips.getString(1))
    }

    @Test
    fun readbackParsesSettingsView() {
        val body = """{"corsOrigins":["http://a.test"],"allowIPs":["10.0.0.0/8"],"lan":false}"""
        assertEquals(listOf("http://a.test"), parseCorsOrigins(body))
        assertEquals(listOf("10.0.0.0/8"), parseAllowIPs(body))
        assertEquals(emptyList<String>(), parseCorsOrigins("""{"corsOrigins":[]}"""))
    }
}
