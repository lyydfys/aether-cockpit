package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsApiTest {
    @Test
    fun mapsOnlySafeFieldsAndNeverCarriesSecrets() {
        val summary = parseSettings(
            """
            {
              "addr":"127.0.0.1:42689","port":42689,"lan":false,
              "lanURLs":["http://192.168.1.5:42689/v1"],
              "proxy":"http://user:******@127.0.0.1:7890",
              "corsOrigins":["http://localhost:3000"],"allowIPs":["127.0.0.1"],
              "redact":true,"redactPersonal":false,
              "redactActions":["keys","jwt"],
              "redactRules":[{"name":"rule-a"},{"name":"rule-b"}],
              "redactAllow":["safe.example.com"],
              "desktop_app":false,"autostart":{"supported":false},
              "webdav":{"url":"https://dav.example.com","password":"******"},
              "otlp":{"endpoint":"https://otel.example.com","headers":{"x-token":"sk-secret"}},
              "tools":{"search":{"apiKey":"sk-secret"}},
              "remote_quotas":[{"key":"ciphertext"}]
            }
            """.trimIndent(),
        )
        assertEquals("127.0.0.1:42689", summary.addr)
        assertEquals(42689, summary.port)
        assertFalse(summary.lanEnabled)
        assertEquals(1, summary.lanUrls.size)
        assertEquals("http://user:******@127.0.0.1:7890", summary.proxyMasked)
        assertTrue(summary.redactEnabled)
        assertFalse(summary.redactPersonal)
        assertEquals(2, summary.redactActions.size)
        assertEquals(2, summary.redactRuleCount)
        assertEquals(1, summary.redactAllowCount)
        assertFalse(summary.desktopApp)
        assertFalse("summary must not carry secrets", summary.toString().contains("sk-secret"))
        assertFalse(summary.toString().contains("ciphertext"))
        assertFalse(summary.toString().contains("dav.example.com"))
    }

    @Test
    fun malformedBodiesAreRejected() {
        for (body in listOf("not json", "[]", "\"oops\"")) {
            val error = runCatching { parseSettings(body) }.exceptionOrNull()
            val type = error?.let { it::class.simpleName }
            assertTrue("body=$body -> $type", error is ManagementApiException || error is org.json.JSONException)
        }
    }
}
