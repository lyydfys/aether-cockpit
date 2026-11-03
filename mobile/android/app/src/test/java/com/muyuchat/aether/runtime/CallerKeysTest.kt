package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CallerKeysTest {
    @Test
    fun parsesKeyListWithoutSecrets() {
        val payload = parseCallerKeys(
            """
            {
              "keys": [
                {"id":"ck_1","name":"手机测试","masked":"ae12…9f","off":false,
                 "limit":null,"models":[],"createdAt":1793600000,
                 "used":{"tokens":42,"cost":0.01}}
              ],
              "accountNames":["a"]
            }
            """.trimIndent(),
        )
        assertEquals(1, payload.keys.size)
        val key = payload.keys[0]
        assertEquals("ck_1", key.id)
        assertEquals("手机测试", key.name)
        assertEquals("ae12…9f", key.masked)
        assertEquals(false, key.off)
        assertEquals(42L, key.usedTokens)
        assertNull(payload.secret)
    }

    @Test
    fun createdSecretIsOnlyTakenFromResponse() {
        val payload = parseCallerKeys("""{"keys":[],"accountNames":[],"secret":"0123456789abcdef"}""")
        assertEquals("0123456789abcdef", payload.secret)
        val none = parseCallerKeys("""{"keys":[],"accountNames":[]}""")
        assertNull(none.secret)
    }

    @Test
    fun removeBodyCarriesOnlyKey() {
        val remove = JSONObject(buildCallerKeyActionJson("remove", key = "ck_9"))
        assertEquals(1, remove.length())
        assertEquals("ck_9", remove.getString("key"))
        val on = JSONObject(buildCallerKeyActionJson("on-key", key = "ck_9"))
        assertEquals("ck_9", on.getString("key"))
    }

    @Test
    fun actionBodiesCarryOnlyNeededFields() {
        val add = JSONObject(buildCallerKeyActionJson("add-key", name = "phone"))
        assertEquals(1, add.length())
        assertEquals("phone", add.getString("name"))

        val off = JSONObject(buildCallerKeyActionJson("off-key", key = "ck_1"))
        assertEquals(1, off.length())
        assertEquals("ck_1", off.getString("key"))

        val error = runCatching { parseCallerKeys("not json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
    }
}
