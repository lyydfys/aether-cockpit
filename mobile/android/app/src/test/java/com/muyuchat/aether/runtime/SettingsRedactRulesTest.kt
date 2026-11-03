package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsRedactRulesTest {
    @Test
    fun parseSettingsKeepsCustomRuleShapeAndOrder() {
        val body = """
            {"redactRules":[
              {"kind":"API_KEY","prefix":"oc_sk_"},
              {"kind":"email","regex":"[a-z]+@example\\.com"},
              {"kind":"","prefix":"skip"},
              {"kind":"empty"}
            ]}
        """.trimIndent()
        val summary = parseSettings(body)
        assertEquals(
            listOf(
                RedactCustomRule("API_KEY", prefix = "oc_sk_"),
                RedactCustomRule("email", regex = "[a-z]+@example\\.com"),
            ),
            summary.customRules,
        )
        assertEquals(4, summary.redactRuleCount)
    }

    @Test
    fun replaceBodySendsRulesArrayWithOnlyTheChosenMatcher() {
        val json = JSONObject(
            buildRedactRulesJson(
                listOf(
                    RedactCustomRule("API_KEY", prefix = "oc_sk_"),
                    RedactCustomRule("email", regex = "a@b"),
                ),
            ),
        )
        val rules = json.getJSONArray("rules")
        assertEquals(2, rules.length())
        assertEquals("oc_sk_", rules.getJSONObject(0).getString("prefix"))
        assertTrue(!rules.getJSONObject(0).has("regex"))
        assertEquals("a@b", rules.getJSONObject(1).getString("regex"))
        assertTrue(!rules.getJSONObject(1).has("prefix"))
    }

    @Test
    fun editBodyCarriesAtAndOriginalWasForConflictDetection() {
        val json = JSONObject(
            buildRedactRuleJson(
                at = 3,
                was = RedactCustomRule("OLD", prefix = "old_"),
                rule = RedactCustomRule("NEW", prefix = "new_"),
            ),
        )
        assertEquals(3, json.getInt("at"))
        val was = json.getJSONObject("was")
        assertEquals("OLD", was.getString("kind"))
        assertEquals("old_", was.getString("prefix"))
        assertTrue(!was.has("regex"))
        val rule = json.getJSONObject("rule")
        assertEquals("NEW", rule.getString("kind"))
        assertEquals("new_", rule.getString("prefix"))
        assertTrue(!rule.has("regex"))
    }

    @Test
    fun failedSaveKeepsPreviouslyLoadedRules() {
        val old = listOf(RedactCustomRule("OLD", prefix = "old_"))
        assertEquals(old, mergeRedactRules(old, readback = null, error = "规则已被改动，请刷新后重试"))
    }

    @Test
    fun readbackReplacesLocalRules() {
        val old = listOf(RedactCustomRule("OLD", prefix = "old_"))
        val server = listOf(RedactCustomRule("SERVER", regex = "x+"))
        assertEquals(server, mergeRedactRules(old, readback = server, error = null))
    }
}