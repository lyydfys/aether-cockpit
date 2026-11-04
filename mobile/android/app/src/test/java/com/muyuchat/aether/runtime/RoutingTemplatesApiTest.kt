package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 路由**组模板**（`GET /api/groups` 的 `templates` 字段）。
 *
 * 背景：卡片原本要做"决策器视图"，但核对 `groups_api.go` 后发现
 * `deciders` / `pools` 是**硬编码空数组**（`[]any{}`），没有真实内容可展示。
 * 而 `templates`（永不卡住 / 便宜优先 / 难的给强的）是真的，所以做的是它。
 */
class RoutingTemplatesApiTest {
    private val body = """
        {"groups":[],
         "models":[],
         "deciders":[],
         "pools":[],
         "templates":[
           {"kind":"steady","group":{"id":"template/steady","name":"永不卡住","routing":"order",
             "members":["a/x","b/y","c/z"],"auto":false}},
           {"kind":"thrifty","group":{"id":"template/thrifty","name":"便宜优先","routing":"order",
             "members":["a/x"],"auto":false},"need":"cheap"},
           {"kind":"smart","group":{"id":"template/smart","name":"难的给强的","routing":"",
             "members":[],"auto":false},"need":"providers"}
         ]}
    """.trimIndent()

    @Test
    fun parsesTemplatesWithMemberCounts() {
        val overview = parseRouting(body)
        assertEquals(3, overview.templates.size)
        val steady = overview.templates[0]
        assertEquals("steady", steady.kind)
        assertEquals("永不卡住", steady.name)
        assertEquals("order", steady.routing)
        assertEquals(3, steady.memberCount)
    }

    @Test
    fun missingMemberInfoIsDisclosed() {
        val overview = parseRouting(body)
        val thrifty = overview.templates[1]
        assertEquals(1, thrifty.memberCount)
        assertTrue(routingTemplateLine(thrifty).contains("仅 1 个成员"))
        assertTrue(routingTemplateLine(thrifty).contains("便宜"))
        val smart = overview.templates[2]
        assertTrue(routingTemplateLine(smart).contains("还没有成员"))
        assertTrue(routingTemplateLine(smart).contains("更多渠道"))
    }

    @Test
    fun healthyTemplateLineIsPlain() {
        val steady = parseRouting(body).templates[0]
        val line = routingTemplateLine(steady)
        assertTrue(line.contains("永不卡住"))
        assertTrue(line.contains("3 个成员"))
        assertTrue(!line.contains("还"))
    }

    @Test
    fun decidersAndPoolsAreNotModeled() {
        // 服务端把它们硬编码为空数组；不解析就不会做出"永远为空"的页面
        val overview = parseRouting(body)
        assertEquals(0, overview.groups.size)
        assertEquals(0, overview.models.size)
        // 模板仍在
        assertEquals(3, overview.templates.size)
    }

    @Test
    fun absentTemplatesIsFine() {
        assertTrue(parseRouting("""{"groups":[],"models":[]}""").templates.isEmpty())
    }

    @Test
    fun malformedBodyStillRaises() {
        val error = runCatching { parseRouting("not-json") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }
}
