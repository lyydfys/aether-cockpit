package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.PresetDraft
import com.muyuchat.aether.runtime.TOOLS_KEY_SENTINEL
import com.muyuchat.aether.runtime.ToolsDraft
import com.muyuchat.aether.runtime.ToolsEndpointDraft
import com.muyuchat.aether.runtime.buildPresetSaveJson
import com.muyuchat.aether.runtime.buildToolsJson
import com.muyuchat.aether.runtime.effectiveToolKey
import com.muyuchat.aether.runtime.jsonErrorMessage
import com.muyuchat.aether.runtime.parseJsonObjectOrError
import com.muyuchat.aether.runtime.parseToolsConfig
import com.muyuchat.aether.runtime.presetDraftFrom
import com.muyuchat.aether.runtime.validatePresetDraft
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 工具服务编辑 + 预设新建/编辑的**读写往返**。
 *
 * 重点不是"函数能跑"，而是**写出去的 JSON 与读回来解析的键对得上**
 * （上轮 hooks 往返就是这么抓出真 bug 的）。所有断言都基于服务端真实结构：
 *  - 工具：`settings.Tools` 子对象 + 密钥哨兵 `******`（`tools_api.go:75-170`）
 *  - 预设：`body` 对象 / `headers` 字符串对象（`request_presets.go:23-33`、`:345-354`）
 */
class SettingsToolsPresetsTest {

    // —— 密钥哨兵 ——
    @Test
    fun sentinelKeepsStoredKeyWhenFieldIsBlank() {
        // 用户不动 Key → 回传哨兵（服务端据此保留旧值）
        assertEquals(TOOLS_KEY_SENTINEL, effectiveToolKey("", saved = true))
        assertEquals(TOOLS_KEY_SENTINEL, effectiveToolKey("   ", saved = true))
        // 用户填了新值 → 用新值
        assertEquals("sk-new", effectiveToolKey("sk-new", saved = true))
        // 从未存过且留空 → 空串（不会把哨兵本身当 Key 存下来）
        assertEquals("", effectiveToolKey("", saved = false))
        // 用户误把哨兵粘进来 → 仍然按哨兵处理
        assertEquals(TOOLS_KEY_SENTINEL, effectiveToolKey(TOOLS_KEY_SENTINEL, saved = true))
    }

    // —— 工具服务写 → 读往返 ——
    @Test
    fun toolsJsonRoundTripsThroughParser() {
        val draft = ToolsDraft(
            webSearchProvider = "searxng",
            webSearchEndpoint = "http://127.0.0.1:8080",
            webSearchApiKey = "",
            webSearchMaxResults = 99,
            webSearchKeyOrder = "order",
            image = ToolsEndpointDraft(baseUrl = "https://img", apiKey = "", model = "gpt-image-1"),
            audio = ToolsEndpointDraft(baseUrl = "https://a", apiKey = "sk-audio", model = "whisper-1"),
            video = ToolsEndpointDraft(),
            realtime = ToolsEndpointDraft(baseUrl = "https://rt", model = "gpt-4o-realtime"),
        )
        val json = buildToolsJson(
            draft,
            savedKeys = mapOf("web_search" to true, "image" to true, "audio" to false),
        )
        // 线上形态：{"tools":{...}}（服务端读 settings.Tools）
        assertTrue(json.startsWith("{\"tools\":{"))
        // 留空的已存 Key → 哨兵；用户填的新 Key → 新值
        assertTrue(json.contains("\"******\""))
        assertTrue(json.contains("sk-audio"))
        // 返回条数被夹到 1..20
        assertTrue(json.contains("\"max_results\":20"))

        // 读回来：parseSettings 取的是内层 tools 对象
        val inner = JSONObject(json).optJSONObject("tools")
        assertNotNull(inner)
        val parsed = parseToolsConfig(inner)
        assertEquals("searxng", parsed.webSearchProvider)
        assertEquals("http://127.0.0.1:8080", parsed.webSearchEndpoint)
        assertEquals(20, parsed.webSearchMaxResults)
        assertEquals("order", parsed.webSearchKeyOrder)
        // 哨兵在 GET 语义里就是"已保存"：服务端因此保留旧 Key；
        // 请求体不写 key_count（服务端写侧会把它归零，tools_api.go:118），所以这里读回是 0。
        assertEquals(0, parsed.webSearchKeyCount)
        assertTrue(parsed.imageKeySaved)
        assertTrue(parsed.audioKeySaved)
        assertTrue(!parsed.videoKeySaved)
        assertEquals("gpt-image-1", parsed.imageModel)
        assertEquals("whisper-1", parsed.audioModel)
        assertEquals("gpt-4o-realtime", parsed.realtimeModel)
    }

    @Test
    fun toolsJsonOmitsEmptyMultiKeysAndNeverInventsKeys() {
        val json = buildToolsJson(ToolsDraft(webSearchApiKeys = listOf("  ", "")), emptyMap())
        assertTrue(!json.contains("api_keys"))
        // 没存过 Key 且没填 → 空串，不是哨兵
        assertTrue(json.contains("\"api_key\":\"\""))
    }

    @Test
    fun multiKeysAreIncludedWhenProvided() {
        val json = buildToolsJson(
            ToolsDraft(webSearchApiKeys = listOf("k1", "k2")),
            mapOf("web_search" to true),
        )
        assertTrue(json.contains("\"api_keys\":[\"k1\",\"k2\"]"))
    }

    // —— 预设校验（含行号） ——
    @Test
    fun presetDraftValidationCoversNameBodyHeadersAndId() {
        assertNull("合法草稿应通过", validatePresetDraft(PresetDraft(name = "ok")))

        val blankName = validatePresetDraft(PresetDraft(name = " "))
        assertTrue("名称全空白必须被拒，实际：$blankName", blankName != null && blankName.contains("名称不能为空"))

        // Body 非法 → 带行号
        val badBody = validatePresetDraft(PresetDraft(name = "x", bodyJson = "{\n\"a\": 1\n,,}"))
        assertTrue("Body 非法必须被拒，实际：$badBody", badBody != null && badBody.contains("Body"))
        assertTrue("Body 错误必须带行号，实际：$badBody", badBody!!.contains("行"))

        // 数组不是对象
        val arrayHeaders = validatePresetDraft(PresetDraft(name = "x", headersJson = "[1,2]"))
        assertTrue("数组必须被拒，实际：$arrayHeaders", arrayHeaders != null && arrayHeaders.contains("JSON 对象"))

        // Headers 的值必须是字符串（服务端 map[string]string）
        val nonStringHeader = validatePresetDraft(PresetDraft(name = "x", headersJson = """{"n":1}"""))
        assertTrue(
            "Headers 非字符串值必须被拒，实际：$nonStringHeader",
            nonStringHeader != null && nonStringHeader.contains("必须是字符串"),
        )

        // 非法 id 本地就拦（服务端会 400）
        val badId = validatePresetDraft(PresetDraft(name = "x", id = "Bad_ID"))
        assertTrue("非法 id 必须被拒，实际：$badId", badId != null && badId.contains("连字符"))
        // 合法 slug 放行
        assertNull("合法 slug 应通过", validatePresetDraft(PresetDraft(name = "x", id = "codex-cache-1")))
    }

    @Test
    fun jsonErrorMessageUnderstandsBothOrgJsonFormats() {
        // Android / 新版 org.json：`Missing value at 19 [character 7 line 3]`（方括号直接给行列）
        val bracket = jsonErrorMessage("{\n  \"a\": 1,\n  \"b\": ,\n}", "Missing value at 19 [character 7 line 3]")
        assertTrue("方括号格式应给出第 3 行，实际：$bracket", bracket.contains("第 3 行"))
        assertTrue("必须保留原因，实际：$bracket", bracket.contains("Missing value"))
        assertTrue("不应残留原始位置片段，实际：$bracket", !bracket.contains("[character"))

        // 其它实现：`Unterminated object at character 7`（只给偏移 → 自己换算行号）
        val atChar = jsonErrorMessage("{\n  \"a\": }", "Unterminated object at character 7")
        assertTrue("at character 格式应给出第 2 行，实际：$atChar", atChar.contains("第 2 行"))
        assertTrue("必须保留原因，实际：$atChar", atChar.contains("Unterminated object"))

        // 真实 org.json 走一遍：错误文案必须带行号（这就是上一版报红的那条）
        val real = parseJsonObjectOrError("{\n  \"a\": 1,\n  \"b\": ,\n}")
        assertTrue("真实输入必须解析失败，实际：$real", real != null)
        assertTrue("真实输入的错误必须带行号，实际：$real", real!!.contains("行"))

        // 完全没有位置信息：退化但不丢原因，也不编行号
        val plain = jsonErrorMessage("{}", "boom")
        assertTrue("无位置信息时仍要带原因，实际：$plain", plain.contains("boom"))
        assertTrue("无位置信息时不该编行号，实际：$plain", !plain.contains("行"))
        // 空报错文案也不能产出空提示
        assertTrue(jsonErrorMessage("{}", "").contains("无法解析"))
    }

    // —— 预设写 → 读往返 ——
    @Test
    fun presetSaveJsonKeepsBodyAndHeadersAsObjects() {
        val draft = PresetDraft(
            id = "",
            name = "Codex 缓存",
            description = "d",
            enabled = true,
            systemPrompt = "sys",
            concatSystem = true,
            bodyJson = """{"prompt_cache_key":"x"}""",
            headersJson = """{"x-client":"codex"}""",
        )
        val json = buildPresetSaveJson(draft)
        assertNotNull(json)
        // body/headers 必须是对象，不是字符串（服务端 map[string]any / map[string]string）
        assertTrue(json!!.contains("\"body\":{"))
        assertTrue(json.contains("\"headers\":{"))
        assertTrue(!json.contains("\"body\":\""))
        val parsed = JSONObject(json)
        assertEquals("Codex 缓存", parsed.optString("name"))
        assertEquals("sys", parsed.optString("system_prompt"))
        assertTrue(parsed.optBoolean("concat_system"))
        assertEquals("x", parsed.getJSONObject("body").optString("prompt_cache_key"))
        assertEquals("codex", parsed.getJSONObject("headers").optString("x-client"))

        // 非法草稿不允许产出请求体（页面据此禁用保存）
        assertNull(buildPresetSaveJson(PresetDraft(name = "", bodyJson = "{}")))
        assertNull(buildPresetSaveJson(PresetDraft(name = "x", bodyJson = "{")))
    }

    @Test
    fun presetDraftFromFillsEditFormAndHandlesMissing() {
        val raw = """{"presets":[{"id":"p1","name":"N","description":"D","enabled":false,
            "system_prompt":"S","concat_system":true,
            "body":{"a":1},"headers":{"h":"v"}}]}"""
        val draft = presetDraftFrom(raw, "p1")
        assertNotNull(draft)
        assertEquals("N", draft!!.name)
        assertEquals("D", draft.description)
        assertTrue(!draft.enabled)
        assertEquals("S", draft.systemPrompt)
        assertTrue(draft.concatSystem)
        // 回填的 JSON 文本必须能原样通过校验并再次序列化
        assertNull(validatePresetDraft(draft))
        val rebuilt = buildPresetSaveJson(draft)
        assertNotNull(rebuilt)
        assertEquals("v", JSONObject(rebuilt!!).getJSONObject("headers").optString("h"))
        // 找不到 → null（页面提示刷新）
        assertNull(presetDraftFrom(raw, "nope"))
        assertNull(presetDraftFrom("not json", "p1"))
    }
}
