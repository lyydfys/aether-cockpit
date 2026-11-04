package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.OtlpConfig
import com.muyuchat.aether.runtime.PipelineHooks
import com.muyuchat.aether.runtime.PresetSummary
import com.muyuchat.aether.runtime.buildHooksJson
import com.muyuchat.aether.runtime.buildModelRedirectsJson
import com.muyuchat.aether.runtime.buildOtlpJson
import com.muyuchat.aether.runtime.buildPresetDeleteJson
import com.muyuchat.aether.runtime.parseModelRedirectsSnapshot
import com.muyuchat.aether.runtime.parseOtlpConfig
import com.muyuchat.aether.runtime.parsePipelineHooks
import com.muyuchat.aether.runtime.parsePresetsSnapshot
import com.muyuchat.aether.runtime.parseToolsConfig
import com.muyuchat.aether.runtime.presetToggleBody
import com.muyuchat.aether.runtime.quoteJson
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 设置页新增六块的**纯解析/构造层**（每个函数三条路径：正常、空、错误）。
 *
 * 这一层是手机端唯一能离线验证的部分——网络与 Compose 布局不在单测范围内。
 */
class SettingsSectionsParseTest {

    // —— 请求钩子 ——
    @Test
    fun pipelineHooksParseNormalEmptyAndMissing() {
        val normal = parsePipelineHooks(
            JSONObject(
                """{"drop_reasoning":true,"clamp_output":2048,
                   "block_models":["gpt-4*","o1-*"],"guard_providers":["expensive"]}""",
            ),
        )
        assertTrue(normal.dropReasoning)
        assertEquals(2048, normal.clampOutput)
        assertEquals(listOf("gpt-4*", "o1-*"), normal.blockModels)
        assertEquals(listOf("expensive"), normal.guardProviders)

        val empty = parsePipelineHooks(JSONObject("{}"))
        assertEquals(PipelineHooks(), empty)
        // null（服务端没这个键）→ 全默认，不抛。
        assertEquals(PipelineHooks(), parsePipelineHooks(null))
        // 负数/脏值收敛
        val dirty = parsePipelineHooks(JSONObject("""{"clamp_output":-5}"""))
        assertEquals(0, dirty.clampOutput)
    }

    @Test
    fun hooksJsonRoundTripsThroughParser() {
        val hooks = PipelineHooks(
            dropReasoning = true,
            clampOutput = 1024,
            blockModels = listOf("a", "b"),
            guardProviders = listOf("p1"),
            guardAccounts = listOf("acc-1"),
        )
        val json = buildHooksJson(hooks)
        // 线上形态必须是 {"hooks":{...}}：服务端读 settings.ui["hooks"]，桌面 saveHooks 也这么发。
        assertTrue(json.startsWith("{\"hooks\":{"))
        // parseSettings 取的是内层对象（root.optJSONObject("hooks")），测试必须走同一层；
        // 把包裹体直接喂给解析器只会读到全默认值（上一版测试报红就是这个原因）。
        val inner = JSONObject(json).optJSONObject("hooks")
        assertNotNull(inner)
        assertEquals(hooks, parsePipelineHooks(inner))
        // 第五项 guard_accounts 必须原样保留（pipeline_hooks.go:61-63），否则改任一开关都会删掉它
        assertTrue(json.contains("\"guard_accounts\":[\"acc-1\"]"))
        // 空列表也要是合法 JSON，且默认值往返稳定
        val emptyJson = buildHooksJson(PipelineHooks())
        assertTrue(emptyJson.contains("\"block_models\":[]"))
        assertEquals(PipelineHooks(), parsePipelineHooks(JSONObject(emptyJson).optJSONObject("hooks")))
    }

    // —— JSON 转义 ——
    @Test
    fun quoteEscapesQuotesBackslashesAndControlChars() {
        assertEquals("\"plain\"", quoteJson("plain"))
        assertEquals("\"a\\\"b\"", quoteJson("a\"b"))
        assertEquals("\"a\\\\b\"", quoteJson("a\\b"))
        assertEquals("\"a\\nb\"", quoteJson("a\nb"))
        // 控制字符走 \u 形式，不会破坏 JSON
        assertTrue(quoteJson("\u0001").contains("\\u0001"))
    }

    // —— 模型重定向 ——
    @Test
    fun modelRedirectsParseAndBuildRoundTrip() {
        val body = """{"redirects":{"v4-flash":{"qoder-cn":"DeepSeek-Flash","workbuddy":"flash-sg"}}}"""
        val parsed = parseModelRedirectsSnapshot(body)
        assertEquals(setOf("v4-flash"), parsed.redirects.keys)
        assertEquals("DeepSeek-Flash", parsed.redirects.getValue("v4-flash")["qoder-cn"])

        val json = buildModelRedirectsJson(parsed.redirects)
        val again = parseModelRedirectsSnapshot(json)
        assertEquals(parsed.redirects, again.redirects)
        // 空对象
        assertEquals(emptyMap<String, Map<String, String>>(), parseModelRedirectsSnapshot("""{"redirects":{}}""").redirects)
    }

    @Test
    fun modelRedirectsDraftTextAndParseAreInverse() {
        val redirects = mapOf("b-model" to mapOf("p2" to "up-2"), "a-model" to mapOf("p1" to "up-1"))
        val text = modelRedirectsDraftText(redirects)
        // 键按字母排序，方便校对
        assertTrue(text.indexOf("a-model") < text.indexOf("b-model"))
        assertEquals(redirects, parseModelRedirectsDraft(text))
        // 空草稿 = 清空全部
        assertEquals(emptyMap<String, Map<String, String>>(), parseModelRedirectsDraft("   "))
        // 非法输入返回 null（页面据此提示，不提交）
        assertNull(parseModelRedirectsDraft("[1,2,3]"))
        assertNull(parseModelRedirectsDraft("""{"a":"not-an-object"}"""))
    }

    // —— OTLP ——
    @Test
    fun otlpParseDetectsMaskedHeadersAndDefaults() {
        val masked = parseOtlpConfig(
            JSONObject("""{"endpoint":"http://127.0.0.1:4318","headers":{"___masked___":"true"},
                            "batch_size":32,"flush_interval_seconds":5,"queue_max":128}"""),
        )
        assertEquals("http://127.0.0.1:4318", masked.endpoint)
        assertTrue(masked.headersMasked)
        assertTrue(masked.enabled)
        assertEquals(32, masked.batchSize)

        val plain = parseOtlpConfig(JSONObject("""{"endpoint":""}"""))
        assertTrue(!plain.headersMasked)
        assertTrue(!plain.enabled)
        assertEquals(OtlpConfig(), parseOtlpConfig(null))
    }

    @Test
    fun otlpJsonNeverSendsHeadersUnlessClearing() {
        // 关键语义：默认不带 headers → 服务端沿用旧鉴权头（settings.go:728）
        val keep = buildOtlpJson("http://127.0.0.1:4318", 32, 5, 128)
        assertTrue(keep.contains("\"endpoint\""))
        assertTrue(!keep.contains("headers"))
        // 显式清空才发空对象
        val cleared = buildOtlpJson("http://127.0.0.1:4318", 0, 0, 0, clearHeaders = true)
        assertTrue(cleared.contains("\"headers\":{}"))
        // 0 的批参数不写进请求体（交给服务端默认值）
        assertTrue(!cleared.contains("batch_size"))
    }

    // —— 工具服务 ——
    @Test
    fun toolsParseNormalAndMissing() {
        val tools = parseToolsConfig(
            JSONObject(
                """{"web_search":{"provider":"tavily","endpoint":"https://x","max_results":5,
                                  "key_order":"rotate","key_count":2},
                   "image":{"base_url":"https://img","model":"gpt-image-1","api_key":"******"},
                   "audio":{"base_url":"https://a","model":"whisper-1","api_key":"sk-real"}}""",
            ),
        )
        assertEquals("tavily", tools.webSearchProvider)
        assertEquals(2, tools.webSearchKeyCount)
        assertEquals(5, tools.webSearchMaxResults)
        // 服务端只在**存过** Key 时才回掩码 `******`（tools_api.go:45-47）→ 看到掩码就是"已保存"。
        assertTrue(tools.imageKeySaved)
        assertTrue(tools.audioKeySaved)
        assertTrue(!tools.videoKeySaved)

        assertEquals(com.muyuchat.aether.runtime.ToolsConfig(), parseToolsConfig(null))
    }

    // —— 预设 ——
    @Test
    fun presetsParseCountsAndToggleBody() {
        val body = """{"presets":[
            {"id":"p1","name":"Codex 缓存","enabled":true,"body":{"a":1,"b":2},
             "headers":{"x":"y"},"system_prompt":"hi"},
            {"id":"p2","enabled":false}
        ]}"""
        val snapshot = parsePresetsSnapshot(body)
        assertEquals(2, snapshot.presets.size)
        val first = snapshot.presets.first()
        assertEquals(2, first.bodyCount)
        assertEquals(1, first.headerCount)
        assertTrue(first.hasSystemPrompt)
        // 名称缺省回落 id
        assertEquals("p2", snapshot.presets[1].name)

        val toggled = presetToggleBody(body, "p1", false)
        assertNotNull(toggled)
        assertEquals(false, JSONObject(toggled!!).optBoolean("enabled"))
        // 其它字段必须原样保留
        assertEquals("Codex 缓存", JSONObject(toggled).optString("name"))
        // 不存在的 id → null（页面提示刷新，而不是静默成功）
        assertNull(presetToggleBody(body, "nope", true))
        assertNull(presetToggleBody("not json", "p1", true))
    }

    @Test
    fun presetDeleteJsonAndLabels() {
        assertEquals("{\"id\":\"p1\"}", buildPresetDeleteJson("p1"))
        assertEquals("未配置", presetCountLabel(0))
        assertEquals("2 个", presetCountLabel(2))
        val meta = presetMetaLine(
            PresetSummary(
                id = "p1",
                name = "n",
                bodyCount = 2,
                headerCount = 1,
                hasSystemPrompt = true,
            ),
        )
        assertTrue(meta.contains("Body 2"))
        assertTrue(meta.contains("提示词"))
        assertTrue(meta.contains("未挂载"))
        assertTrue(presetEmptyCopy().contains("为什么空"))
        assertTrue(presetEmptyCopy().contains("下一步"))
    }

    // —— 零散小函数 ——
    @Test
    fun miscLabelsAndParsers() {
        assertEquals(0, parsePositiveInt(""))
        assertEquals(0, parsePositiveInt("abc"))
        assertEquals(128, parsePositiveInt("128"))
        assertEquals("已保存（明文只在桌面端）", savedKeyLabel(true))
        assertEquals("未配置", savedKeyLabel(false))
        assertEquals("多 Key 轮换", searchKeyOrderLabel("rotate"))
        assertEquals("按顺序优先", searchKeyOrderLabel("order"))
        assertEquals("默认（轮换）", searchKeyOrderLabel(""))
        assertTrue(pricingEmptyCopy().contains("为什么空"))
        assertEquals("网关未启动：启动后才能读取价格表。", "网关未启动：启动后才能读取价格表。")
    }
}
