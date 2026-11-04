package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * `models` 解析（概览「聚合链路」推断端点用）。
 *
 * 服务端 `/api/providers`（`gateway.go:809`）的 `models` 是**对象数组**（`admin.go:54`
 * 的 `providerView` 内嵌 manifest），每项含 `id`。这里只取 id 字符串，给
 * [providerEndpointFamilies] 做 anthropic/gemini/openai 推断。
 */
class ProviderModelIdsTest {
    @Test
    fun readsIdsFromObjectArray() {
        val array = JSONArray(
            """[{"id":"claude-sonnet-4","name":"Sonnet"},{"id":"gpt-5-codex"}]""",
        )
        assertEquals(listOf("claude-sonnet-4", "gpt-5-codex"), parseModelIds(array))
    }

    @Test
    fun toleratesStringArrayAndDropsBlanksAndDuplicates() {
        val array = JSONArray("""["claude-a","  ","claude-a","gemini-2.5"]""")
        assertEquals(listOf("claude-a", "gemini-2.5"), parseModelIds(array))
    }

    @Test
    fun missingIdOrNullArrayYieldsEmptyList() {
        assertEquals(emptyList<String>(), parseModelIds(null))
        assertEquals(emptyList<String>(), parseModelIds(JSONArray("""[{"name":"no-id"}]""")))
        // 空数组不崩
        assertTrue(parseModelIds(JSONArray("[]")).isEmpty())
    }

    @Test
    fun idsKeepServiceOrderSoFamilyInferenceIsStable() {
        val ids = parseModelIds(JSONArray("""[{"id":"gemini-2.5"},{"id":"claude-x"}]"""))
        assertEquals(listOf("gemini-2.5", "claude-x"), ids)
        // 端点推断对顺序不敏感，但两个族都要在
        assertEquals(setOf("gemini", "anthropic"), providerEndpointFamilies(ids))
    }
}
