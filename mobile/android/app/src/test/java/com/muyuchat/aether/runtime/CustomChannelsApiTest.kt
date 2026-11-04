package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * 自定义渠道（custom-channels）的纯函数契约。
 *
 * 端点字段以 `internal/core/gateway/custom_channels.go` 的 `CustomChannel` 为准：
 * `id / name / base_url / protocol / models / headers / created_at`。
 */
class CustomChannelsApiTest {

    @Test
    fun parsesSavedChannels() {
        val body = """
            {"channels":[
              {"id":"my-relay","name":"公司中转","base_url":"https://relay.example.com/v1",
               "protocol":"anthropic","models":["claude-sonnet-4","gpt-4o"],
               "headers":{"X-Custom":"1"},"created_at":1700000000},
              {"id":"only-id"}
            ]}
        """.trimIndent()
        val channels = parseCustomChannels(body)
        assertEquals(2, channels.size)

        val first = channels[0]
        assertEquals("my-relay", first.id)
        assertEquals("公司中转", first.name)
        assertEquals("https://relay.example.com/v1", first.baseUrl)
        assertEquals("anthropic", first.protocol)
        assertEquals(listOf("claude-sonnet-4", "gpt-4o"), first.models)
        assertEquals(mapOf("X-Custom" to "1"), first.headers)
        assertEquals(1_700_000_000L, first.createdAt)

        // id 有、name 空：用 id 兜底展示。
        assertEquals("only-id", channels[1].name)
        assertEquals("openai", channels[1].protocol)
        assertTrue(channels[1].models.isEmpty())
    }

    @Test
    fun invalidListPayloadIsInvalidResponse() {
        try {
            parseCustomChannels("not json")
            fail("坏回包应抛 invalid_response")
        } catch (e: ManagementApiException) {
            assertEquals("invalid_response", e.code)
        }
        // 合法 JSON 但不是对象（数组）同样归一成 invalid_response。
        try {
            parseCustomChannels("[]")
            fail("非对象回包应抛 invalid_response")
        } catch (e: ManagementApiException) {
            assertEquals("invalid_response", e.code)
        }
    }

    @Test
    fun saveBodyHasNoApiKeyAndKeepsRealFields() {
        val draft = CustomChannelDraft(
            name = " 公司中转 ",
            id = "my-relay",
            baseUrl = " https://relay.example.com/v1/ ",
            protocol = "anthropic",
            modelsText = "claude-sonnet-4, gpt-4o，claude-sonnet-4\n",
            headersText = "X-Custom: 1\nX-Trace: a:b",
        )
        val json = JSONObject(buildCustomChannelJson(draft))
        assertEquals("公司中转", json.optString("name"))
        assertEquals("https://relay.example.com/v1", json.optString("base_url"))
        assertEquals("anthropic", json.optString("protocol"))
        assertEquals("my-relay", json.optString("id"))
        assertEquals(
            listOf("claude-sonnet-4", "gpt-4o"),
            (0 until json.getJSONArray("models").length()).map { json.getJSONArray("models").optString(it) },
        )
        assertEquals("a:b", json.getJSONObject("headers").optString("X-Trace"))
        // 密钥永远不在这里。
        assertTrue(!json.has("api_key"))
        assertTrue(!json.has("key"))
    }

    @Test
    fun saveBodyOmitsBlankIdAndEmptyCollections() {
        val json = JSONObject(
            buildCustomChannelJson(
                CustomChannelDraft(name = "随手起的名字", baseUrl = "https://x.example.com/v1"),
            ),
        )
        assertTrue(!json.has("id"))
        assertTrue(!json.has("models"))
        assertTrue(!json.has("headers"))
        assertEquals("openai", json.optString("protocol"))
    }

    @Test
    fun probeBodyCarriesKeyOnlyForThisRequest() {
        val withKey = JSONObject(
            buildCustomChannelProbeJson(" https://relay.example.com/v1/ ", " sk-test "),
        )
        assertEquals("https://relay.example.com/v1", withKey.optString("base_url"))
        assertEquals("sk-test", withKey.optString("api_key"))

        val without = JSONObject(buildCustomChannelProbeJson("https://relay.example.com/v1", "   "))
        assertTrue(!without.has("api_key"))
    }

    @Test
    fun slugMirrorsServerRules() {
        assertEquals("my-relay", slugifyCustomChannelId("  My Relay  "))
        assertEquals("a-b", slugifyCustomChannelId("a___b"))
        assertEquals("", slugifyCustomChannelId("！！！"))
        assertEquals(40, slugifyCustomChannelId("a".repeat(80)).length)
        assertNull(validateCustomChannelId(""))
        assertNull(validateCustomChannelId("My Relay"))
        assertEquals("渠道 ID 至少要包含一个字母或数字", validateCustomChannelId("！！！"))
    }

    @Test
    fun headersParseReportsLineNumbers() {
        val ok = parseCustomChannelHeaders(
            "# 注释跳过\nX-Custom: 1\nX-Trace: a:b\n",
        )
        assertNull(ok.error)
        assertEquals(mapOf("X-Custom" to "1", "X-Trace" to "a:b"), ok.headers)

        val missingColon = parseCustomChannelHeaders("X-Custom: 1\n坏行\n")
        assertTrue("应报第 2 行", missingColon.error!!.contains("第 2 行"))

        val managed = parseCustomChannelHeaders("Authorization: Bearer x\n")
        assertTrue("应拒绝网关管理的头", managed.error!!.contains("由网关管理"))

        val badName = parseCustomChannelHeaders("X Custom: 1\n")
        assertTrue("应拒绝非法头名", badName.error!!.contains("不合法"))
    }

    @Test
    fun draftValidationCoversRequiredFieldsAndHeaders() {
        assertNull(
            validateCustomChannelDraft(
                CustomChannelDraft(name = "中转", baseUrl = "https://relay.example.com/v1"),
            ),
        )
        assertEquals(
            "请填写渠道名称",
            validateCustomChannelDraft(CustomChannelDraft(baseUrl = "https://x/v1")),
        )
        assertEquals(
            "请填写 Base URL",
            validateCustomChannelDraft(CustomChannelDraft(name = "中转")),
        )
        assertTrue(
            validateCustomChannelDraft(
                CustomChannelDraft(name = "中转", baseUrl = "ftp://x"),
            )!!.contains("http:// 或 https://"),
        )
        assertTrue(
            validateCustomChannelDraft(
                CustomChannelDraft(name = "中转", baseUrl = "https://x/v1", headersText = "坏行"),
            )!!.contains("第 1 行"),
        )
        assertTrue(
            validateCustomChannelDraft(
                CustomChannelDraft(name = "中转", baseUrl = "https://x/v1", protocol = "ftp"),
            )!!.contains("openai / anthropic / responses"),
        )
    }

    @Test
    fun probeResultParsesOkAndFailure() {
        val ok = parseCustomChannelProbe("""{"ok":true,"models":["m1","m2"]}""")
        assertTrue(ok.ok)
        assertEquals(listOf("m1", "m2"), ok.models)
        assertEquals("", ok.error)

        val failed = parseCustomChannelProbe("""{"ok":false,"status":401,"error":"连不上上游: 401"}""")
        assertTrue(!failed.ok)
        assertEquals(401, failed.status)
        assertTrue(failed.error.contains("401"))
    }
}
