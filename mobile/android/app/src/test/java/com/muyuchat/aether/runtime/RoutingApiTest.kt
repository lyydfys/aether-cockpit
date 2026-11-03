package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RoutingApiTest {
    @Test
    fun parsesRealGroupsPayload() {
        val overview = parseRouting(
            """
            {
              "groups": [
                {"id":"fast-chat","name":"Fast Chat","members":["openai/gpt-5","claude-sub/claude-sonnet"],
                 "memberInfo":[{"id":"openai/gpt-5","name":"GPT-5","provider":"openai","providerName":"OpenAI","providerOff":false},
                               {"id":"claude-sub/claude-sonnet","name":"Sonnet","provider":"claude-sub","providerName":"Claude","providerOff":true}],
                 "routing":"order","routingEffective":"priority","warnings":["组级亲和 \"sticky\" 尚未实现，已忽略（渠道级亲和仍生效）"],
                 "ready":true,"auto":false},
                {"id":"auto/gpt-5","name":"gpt-5","members":[],"memberInfo":[],
                 "routing":"order","routingEffective":"priority","warnings":[],"ready":false,"auto":true,"disabled":true}
              ],
              "models": [{"id":"a/b"},{"id":"c/d"}],
              "deciders": [], "pools": [], "templates": [], "found": true
            }
            """.trimIndent(),
        )
        assertEquals(2, overview.modelCount)
        assertEquals(2, overview.groups.size)
        val group = overview.groups[0]
        assertEquals("fast-chat", group.id)
        assertEquals("Fast Chat", group.name)
        assertEquals("order", group.routing)
        assertEquals("priority", group.routingEffective)
        assertTrue(group.ready)
        assertFalse(group.auto)
        assertEquals(2, group.memberInfo.size)
        assertEquals("OpenAI", group.memberInfo[0].providerName)
        assertTrue(group.memberInfo[1].providerOff)
        assertEquals(1, group.warnings.size)
        assertTrue(overview.groups[1].auto)
        assertTrue(overview.groups[1].disabled)
        assertFalse(overview.groups[1].ready)
    }

    @Test
    fun emptyAndMalformedPayloads() {
        val empty = parseRouting("""{"groups":[],"models":[]}""")
        assertTrue(empty.groups.isEmpty())
        assertEquals(0, empty.modelCount)
        assertTrue(empty.groups.none { it.ready })

        for (body in listOf("not json", "{}", """{"groups":"oops","models":[]}""")) {
            val error = runCatching { parseRouting(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    @Test
    fun strategyLabelsAreHumanReadable() {
        assertEquals("按优先级", routingStrategyLabel("order"))
        assertEquals("智能", routingStrategyLabel("smart"))
        assertEquals("轮询", routingStrategyLabel("rotate"))
        assertEquals("用量节奏", routingStrategyLabel("pace"))
        assertEquals("快速", routingStrategyLabel("fast"))
        assertEquals("按优先级", routingStrategyLabel("unknown"))
    }
}
