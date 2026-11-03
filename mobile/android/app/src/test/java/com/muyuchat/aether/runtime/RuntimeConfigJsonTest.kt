package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class RuntimeConfigJsonTest {
    @Test
    fun quotesPathsWithoutBreakingJson() {
        val json = buildRuntimeConfig(
            dataDir = "C:\\tmp\\aether \"data\"",
            cacheDir = "/data/user/0/com.muyuchat.aether/files/cache",
            tempDir = "/tmp/aether\nline",
        )
        assertTrue(json.startsWith("{"))
        assertTrue(json.contains("\"dataDir\":\"C:\\\\tmp\\\\aether \\\"data\\\"\""))
        assertTrue(json.contains("\"tempDir\":\"/tmp/aether\\nline\""))
        assertTrue(json.contains("\"port\":0"))
        assertTrue(json.contains("\"platform\":\"android\""))
    }

    @Test
    fun includesEveryRequiredRuntimeField() {
        val json = buildRuntimeConfig("/d", "/c", "/t", port = 4321)
        val required = listOf(
            "\"dataDir\"", "\"cacheDir\"", "\"tempDir\"",
            "\"listenHost\"", "\"port\"", "\"platform\"",
        )
        for (field in required) {
            assertTrue("missing $field in $json", json.contains(field))
        }
        assertTrue(json.contains("\"port\":4321"))
    }

    @Test
    fun accountKeyIsOptionalAndQuoted() {
        val without = buildRuntimeConfig("/d", "/c", "/t")
        assertTrue(!without.contains("accountKey"))

        val key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
        val withKey = buildRuntimeConfig("/d", "/c", "/t", accountKey = key)
        assertTrue(withKey.contains("\"accountKey\":\"$key\""))
    }
}
