package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RedactDetailTest {
    @Test
    fun parsesRulesAndOverrides() {
        val detail = parseRedactDetail(
            """
            {
              "enabled": true,
              "rules": [
                {"id":"keys","name":"API Keys","kind":"keys","action":"mask","enabled":true},
                {"id":"emails","name":"Emails","kind":"emails","action":"mask","enabled":true}
              ],
              "actions": {"emails":"warn"}
            }
            """.trimIndent(),
        )
        assertTrue(detail.enabled)
        assertEquals(2, detail.rules.size)
        assertEquals("emails", detail.rules[1].kind)
        assertEquals("warn", detail.actions["emails"])
        assertEquals("warn", effectiveAction(detail, detail.rules[1]))
        assertEquals("mask", effectiveAction(detail, detail.rules[0]))
    }

    @Test
    fun actionBodySendsWholeMap() {
        val body = buildRedactActionsJson(mapOf("emails" to "warn", "keys" to "mask"))
        val json = JSONObject(body)
        val actions = json.getJSONObject("actions")
        assertEquals(2, actions.length())
        assertEquals("warn", actions.getString("emails"))
        assertEquals(1, json.length())
    }

    @Test
    fun readbackEnforcesActionWhitelist() {
        assertEquals(mapOf("emails" to "warn"), parseRedactActions("""{"actions":{"emails":"warn","bad":"delete"}}"""))
        val error = runCatching { parseRedactActions("not json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertFalse(runCatching { parseRedactActions("""{"rules":[]}""") }.isSuccess)
    }
}
