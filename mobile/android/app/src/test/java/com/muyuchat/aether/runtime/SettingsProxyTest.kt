package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

class SettingsProxyTest {
    @Test
    fun acceptsCredentialFreeProxyUrls() {
        assertNull(validateProxyInput(""))
        assertNull(validateProxyInput("   "))
        assertNull(validateProxyInput("socks5://127.0.0.1:7890"))
        assertNull(validateProxyInput("http://proxy.local:8080"))
        assertNull(validateProxyInput("HTTPS://Proxy.Local:443"))
    }

    @Test
    fun rejectsCredentialedOrMalformedProxies() {
        assertNotNull(validateProxyInput("http://user:pass@proxy.local:8080"))
        assertNotNull(validateProxyInput("socks5://proxy.local"))
        assertNotNull(validateProxyInput("ftp://proxy.local:21"))
        assertNotNull(validateProxyInput("http://proxy local:8080"))
        assertNotNull(validateProxyInput("127.0.0.1:7890"))
    }

    @Test
    fun bodyAndReadbackUseProxyFieldOnly() {
        val json = JSONObject(buildProxyJson("socks5://127.0.0.1:7890"))
        assertEquals(1, json.length())
        assertEquals("socks5://127.0.0.1:7890", json.getString("proxy"))
        assertEquals("http://user:******@p:1", parseProxyMasked("""{"proxy":"http://user:******@p:1"}"""))
        assertEquals("", parseProxyMasked("""{"proxy":""}"""))
    }
}
