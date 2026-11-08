package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道模型清单（`POST /api/provider/models {id}`）。
 *
 * 契约：`{count, models:[…], dropped:[…], source:"registry"|"vendor"}`。
 * 关键约束：**body 里只有 id**，绝不带 api_key（服务端自己从账号池取）。
 */
class ProviderModelsApiTest {
    @Test
    fun pathIsStable() {
        assertEquals("/api/provider/models", providerModelsPath())
    }

    @Test
    fun bodyCarriesOnlyTheId() {
        assertEquals("""{"id":"openai"}""", buildProviderModelsJson("openai"))
        // 不许夹带任何凭据字段
        val body = buildProviderModelsJson("openai")
        listOf("key", "base", "chat", "token").forEach {
            assertTrue("body 不该含「$it」：$body", !body.contains(it))
        }
    }

    @Test
    fun parsesRegistryList() {
        val models = parseProviderModels(
            """{"count":2,"models":["gpt-5","gpt-5-mini"],"dropped":[],"source":"registry"}""",
        )
        assertEquals(2, models.models.size)
        assertEquals("gpt-5", models.models[0])
        assertEquals("registry", models.source)
        assertTrue(providerModelsSummary(models).contains("内置清单"))
    }

    @Test
    fun blankEntriesAreDropped() {
        val models = parseProviderModels("""{"models":["a","","  ","b"],"source":"vendor"}""")
        assertEquals(listOf("a", "b"), models.models)
        assertTrue(providerModelsSummary(models).contains("上游实时"))
    }

    @Test
    fun longListsAreFolded() {
        val models = (1..25).map { "m$it" }
        val preview = providerModelsPreview(models, limit = 20)
        assertEquals(20, preview.shown.size)
        assertEquals(5, preview.more)
        // 短清单不折叠
        val short = providerModelsPreview(models.take(3), limit = 20)
        assertEquals(3, short.shown.size)
        assertEquals(0, short.more)
    }

    @Test
    fun emptyListIsHonest() {
        val models = parseProviderModels("""{"models":[],"source":"registry"}""")
        assertTrue(providerModelsSummary(models).contains("没有可用模型"))
    }

    @Test
    fun missingModelsFieldIsInvalid() {
        val error = runCatching { parseProviderModels("""{"count":0}""") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
        for (b in listOf("not-json", "")) {
            assertTrue(runCatching { parseProviderModels(b) }.exceptionOrNull() is ManagementApiException)
        }
    }
}
