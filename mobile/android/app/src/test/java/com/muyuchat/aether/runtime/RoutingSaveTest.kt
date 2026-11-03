package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class RoutingSaveTest {
    @Test
    fun saveBodyCarriesSnapshotIdAndMembers() {
        val body = buildSavePayloadJson(
            RoutingSavePayload(
                id = "auto/DeepSeek-V4-Flash",
                from = "auto/DeepSeek-V4-Flash",
                name = "DeepSeek-V4-Flash",
                members = listOf("qoder-cn/DeepSeek-V4-Flash", "trae-cn/DeepSeek-V4-Flash"),
                routing = "rotate",
            ),
        )
        val json = JSONObject(body)
        assertEquals("auto/DeepSeek-V4-Flash", json.getString("id"))
        assertEquals("auto/DeepSeek-V4-Flash", json.getString("from"))
        assertEquals("DeepSeek-V4-Flash", json.getString("name"))
        assertEquals("rotate", json.getString("routing"))
        assertEquals(2, json.getJSONArray("members").length())
        assertEquals("qoder-cn/DeepSeek-V4-Flash", json.getJSONArray("members").getString(0))
    }

    @Test
    fun saveStrategiesMatchKernelValues() {
        assertEquals(
            listOf("order", "smart", "rotate", "pace", "fast"),
            routingSaveStrategies(),
        )
    }
}
