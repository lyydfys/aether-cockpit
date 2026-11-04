package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** 模型白名单：解析现有 key + 目录，且保存体只含 key/models。 */
class CallerKeysModelsTest {
    @Test
    fun parsesWhitelistFromKeyObject() {
        val payload = parseCallerKeys(
            """
            {"keys":[{"id":"ck_1","name":"手机","masked":"aek_****1",
              "off":false,"models":["gpt-5","claude-sonnet-4-6"],
              "used":{"tokens":12,"cost":0.01}}]}
            """.trimIndent(),
        )
        assertEquals(listOf("gpt-5", "claude-sonnet-4-6"), payload.keys.single().models)
    }

    @Test
    fun missingOrEmptyModelsMeansUnrestricted() {
        assertEquals(emptyList<String>(), parseCallerKeys("""{"keys":[{"id":"a"}]}""").keys.single().models)
        assertEquals(emptyList<String>(), parseCallerKeys("""{"keys":[{"id":"a","models":[]}]}""").keys.single().models)
    }

    @Test
    fun catalogParsesIdNameProviderAndSkipsBlankIds() {
        val catalog = parseCallerModelCatalog(
            """{"models":[{"id":"gpt-5","name":"GPT-5","provider":"openai"},
                         {"id":" ","name":"bad"},{"id":"redirect-x","provider":"redirect"}],
                "accounts":[]}""",
        )
        assertEquals(2, catalog.models.size)
        assertEquals("GPT-5", catalog.models[0].name)
        assertEquals("redirect", catalog.models[1].provider)
    }

    @Test
    fun saveBodyOnlyCarriesKeyAndWhitelist() {
        val body = JSONObject(
            buildCallerKeyModelsJson(
                key = "ck_1",
                models = listOf("b", "a", "b", " "),
            ),
        )
        assertEquals("ck_1", body.getString("key"))
        assertFalse("不能把 name/limit/off/secret 一起提交覆盖", body.has("name"))
        assertFalse(body.has("limit"))
        val models = body.getJSONArray("models")
        assertEquals(2, models.length())
        assertEquals("a", models.getString(0))
        assertEquals("b", models.getString(1))
    }

    @Test
    fun emptyWhitelistIsExplicitEmptyArray() {
        val body = JSONObject(buildCallerKeyModelsJson("ck_1", emptyList()))
        assertTrue(body.getJSONArray("models").length() == 0)
    }
}
