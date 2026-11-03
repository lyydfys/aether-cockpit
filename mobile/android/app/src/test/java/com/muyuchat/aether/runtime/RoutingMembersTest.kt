package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Pure JVM coverage of the member/rename editor: catalog parse, list edits and payload shape. */
class RoutingMembersTest {
    @Test
    fun parsesCatalogModelsFromGroupsPayload() {
        val overview = parseRouting(
            """
            {
              "groups": [],
              "models": [
                {"id":"qoder/qwen3-max","name":"Qwen3 Max","provider":"qoder","providerName":"Qoder",
                 "providerOff":false,"creditRate":1.5},
                {"id":"trae-cn/deepseek-v4","name":"DeepSeek V4","provider":"trae-cn","providerName":"Trae CN",
                 "providerOff":true,"creditRate":0}
              ],
              "deciders": [], "pools": [], "templates": [], "found": true
            }
            """.trimIndent(),
        )
        assertEquals(2, overview.models.size)
        assertEquals(2, overview.modelCount)
        val first = overview.models[0]
        assertEquals("qoder/qwen3-max", first.id)
        assertEquals("Qwen3 Max", first.name)
        assertEquals("Qoder", first.providerName)
        assertFalse(first.providerOff)
        assertEquals("trae-cn/deepseek-v4", overview.models[1].id)
        assertTrue(overview.models[1].providerOff)
    }

    @Test
    fun catalogFallsBackWhenDisplayFieldsAreMissing() {
        val overview = parseRouting(
            """
            {"groups":[],"models":[{"id":"only/provider-model"},{"name":"no id"},{"id":""}]}
            """.trimIndent(),
        )
        assertEquals(1, overview.models.size)
        assertEquals("only/provider-model", overview.models[0].id)
        // Name falls back to the upstream model, provider name to the id prefix.
        assertEquals("provider-model", overview.models[0].name)
        assertEquals("only", overview.models[0].providerName)
        assertFalse(overview.models[0].providerOff)
    }

    @Test
    fun savedMembersSurviveWhenMemberInfoOmitsThem() {
        val overview = parseRouting(
            """
            {
              "groups": [
                {"id":"fast-chat","name":"Fast Chat",
                 "members":["qoder/qwen3-max","gone/model-x"],
                 "memberInfo":[{"id":"qoder/qwen3-max","name":"Qwen3 Max","provider":"qoder","providerName":"Qoder"}],
                 "routing":"order","routingEffective":"priority","warnings":[],"ready":true,"auto":false},
                {"id":"legacy","name":"Legacy","memberInfo":[
                  {"id":"qoder/qwen3-max","name":"Qwen3 Max","provider":"qoder","providerName":"Qoder"}],
                 "routing":"order","routingEffective":"priority","warnings":[],"ready":true,"auto":false}
              ],
              "models": []
            }
            """.trimIndent(),
        )
        val group = overview.groups[0]
        assertEquals(listOf("qoder/qwen3-max", "gone/model-x"), group.members)
        assertEquals(1, group.memberInfo.size)
        // Editing must keep the member the catalog could not describe, not silently drop it.
        assertEquals(listOf("qoder/qwen3-max", "gone/model-x"), routingGroupMembers(group))
        // Payloads without a `members` array still edit the memberInfo the server did resolve.
        assertEquals(listOf("qoder/qwen3-max"), routingGroupMembers(overview.groups[1]))
    }

    @Test
    fun addMemberAppendsOnceAndIgnoresRepeats() {
        val once = addRoutingMember(emptyList(), "qoder/qwen3-max")
        assertEquals(listOf("qoder/qwen3-max"), once)

        val twice = addRoutingMember(once, "qoder/qwen3-max")
        assertEquals(1, twice.size)
        assertEquals(once, twice)

        val padded = addRoutingMember(once, "  trae-cn/deepseek-v4  ")
        assertEquals(listOf("qoder/qwen3-max", "trae-cn/deepseek-v4"), padded)

        assertEquals(once, addRoutingMember(once, "   "))
    }

    @Test
    fun removeMemberDropsOnlyThatId() {
        val current = listOf("a/1", "b/2", "a/3")
        assertEquals(listOf("a/1", "a/3"), removeRoutingMember(current, "b/2"))
        assertEquals(current, removeRoutingMember(current, "missing/9"))
        assertTrue(removeRoutingMember(listOf("a/1"), "a/1").isEmpty())
    }

    @Test
    fun moveMemberSwapsNeighboursAndStopsAtEdges() {
        val current = listOf("a/1", "b/2", "c/3")
        assertEquals(listOf("b/2", "a/1", "c/3"), moveRoutingMember(current, "a/1", up = false))
        assertEquals(listOf("a/1", "c/3", "b/2"), moveRoutingMember(current, "b/2", up = false))
        assertEquals(listOf("b/2", "a/1", "c/3"), moveRoutingMember(current, "b/2", up = true))

        // Edges and unknown ids are no-ops, never reorderings.
        assertEquals(current, moveRoutingMember(current, "a/1", up = true))
        assertEquals(current, moveRoutingMember(current, "c/3", up = false))
        assertEquals(current, moveRoutingMember(current, "missing/9", up = true))
    }

    @Test
    fun catalogFilterMatchesIdNameAndProviderIgnoringCase() {
        val catalog = listOf(
            RoutingCatalogModel("qoder/qwen3-max", "Qwen3 Max", "Qoder", providerOff = false),
            RoutingCatalogModel("trae-cn/deepseek-v4", "DeepSeek V4", "Trae CN", providerOff = true),
        )
        assertEquals(catalog, filterRoutingCatalog(catalog, ""))
        assertEquals(catalog, filterRoutingCatalog(catalog, "   "))
        assertEquals(listOf("qoder/qwen3-max"), filterRoutingCatalog(catalog, "QWEN3").map { it.id })
        assertEquals(listOf("trae-cn/deepseek-v4"), filterRoutingCatalog(catalog, "trae").map { it.id })
        assertEquals(
            listOf("trae-cn/deepseek-v4"),
            filterRoutingCatalog(catalog, "deepseek v4").map { it.id },
        )
        assertTrue(filterRoutingCatalog(catalog, "no-such-model").isEmpty())
    }

    @Test
    fun chainProvidersCountOneChannelPerProvider() {
        // Same provider twice is still one fallback channel (groups_action_api.go:41-53).
        val members = listOf("qoder/qwen3-max", "qoder/qwen3-coder", "trae-cn/deepseek-v4")
        assertEquals(listOf("qoder", "trae-cn"), routingChainProviders(members))
        assertEquals(1, routingChainProviders(listOf("qoder/a", "qoder/b")).size)
        assertTrue(routingChainProviders(emptyList()).isEmpty())
        // Ids without a "provider/" prefix cannot become a channel.
        assertTrue(routingChainProviders(listOf("noslash")).isEmpty())
    }

    @Test
    fun savePayloadJsonCarriesIdFromNameAndMembers() {
        val body = buildSavePayloadJson(
            RoutingSavePayload(
                id = "fast-chat",
                from = "fast-chat",
                name = "Fast Chat Renamed",
                members = listOf("qoder/qwen3-max", "trae-cn/deepseek-v4"),
                routing = "rotate",
            ),
        )
        val json = JSONObject(body)
        assertEquals("fast-chat", json.getString("id"))
        assertEquals("fast-chat", json.getString("from"))
        assertEquals("Fast Chat Renamed", json.getString("name"))
        assertEquals("rotate", json.getString("routing"))
        assertEquals(2, json.getJSONArray("members").length())
        assertEquals("qoder/qwen3-max", json.getJSONArray("members").getString(0))
        assertEquals("trae-cn/deepseek-v4", json.getJSONArray("members").getString(1))
    }

    @Test
    fun emptyMembersStillProduceValidPayloadForServerToReject() {
        // The client never fakes success: an empty member list is sent and the kernel answers 400.
        val body = buildSavePayloadJson(
            RoutingSavePayload(
                id = "fast-chat",
                from = "fast-chat",
                name = "",
                members = emptyList(),
                routing = "order",
            ),
        )
        val json = JSONObject(body)
        assertEquals(0, json.getJSONArray("members").length())
        assertEquals("", json.getString("name"))
        assertEquals("order", json.getString("routing"))
        assertEquals(setOf("id", "from", "name", "routing", "members"), json.keys().asSequence().toSet())
    }
}
