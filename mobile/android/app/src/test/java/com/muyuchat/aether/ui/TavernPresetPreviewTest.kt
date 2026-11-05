package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.buildTavernPreviewJson
import com.muyuchat.aether.runtime.parseTavernPresetPreview
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 酒馆预设导入：手机侧的**预览层**（解析在网关，见 Go `preset_import.go`）。
 *
 * 这里不重复实现解析，只验证：
 *  - 请求体把文件原文安全地转义进 `json` 字段（预设里全是引号/换行/中文）；
 *  - 响应的映射/丢弃/提示词统计被正确读出来；
 *  - 32KB 溢出时**不允许导入提示词**（宁可只导参数，也不截断人设）。
 */
class TavernPresetPreviewTest {

    @Test
    fun previewJsonEscapesRawPresetText() {
        val raw = """{"name":"预设\"A\"","prompts":[{"content":"第一行\n第二行"}]}"""
        val json = buildTavernPreviewJson(raw, includeSystemPrompt = true)
        val root = JSONObject(json)
        // 原文必须原样可还原（转义正确），否则服务端收到的是坏 JSON
        assertEquals(raw, root.optString("json"))
        assertEquals("auto", root.optString("format"))
        assertTrue(root.optBoolean("include_system_prompt"))
        assertFalse(JSONObject(buildTavernPreviewJson(raw, false)).optBoolean("include_system_prompt"))
    }

    @Test
    fun previewParsesMappedDroppedAndPromptStats() {
        val body = """
            {
              "format": "chat",
              "name": "哭包庇护所",
              "body": {"temperature": 0.85, "top_p": 0.95, "top_k": 40, "max_tokens": 8192},
              "system_prompt": "主提示\n\n第二段",
              "prompt_included": true,
              "prompt_segments": 3,
              "prompt_chars": 1234,
              "prompt_bytes": 2468,
              "prompt_limit": 32768,
              "prompt_overflow": false,
              "mapped": [
                {"key": "temperature", "source": "temperature"},
                {"key": "top_k", "source": "top_k"},
                {"key": "max_tokens", "source": "openai_max_tokens"}
              ],
              "dropped": [
                {"key": "top_a", "group": "upstream", "reason": "非 OpenAI 标准参数，严格上游会 400"},
                {"key": "stream_openai", "group": "client", "reason": "流式开关由网关按请求头决定"},
                {"key": "chat_completion_source", "group": "gateway", "reason": "网关自管字段"},
                {"key": "verbosity", "group": "unknown", "reason": "未识别的字段"}
              ]
            }
        """.trimIndent()
        val preview = parseTavernPresetPreview(body)
        assertEquals("chat", preview.format)
        assertEquals("哭包庇护所", preview.name)
        // bodyJson 必须是可再解析的 JSON 文本（原样写回，不做字段白名单）
        val bodyJson = JSONObject(preview.bodyJson)
        assertEquals(0.85, bodyJson.optDouble("temperature"), 0.0)
        assertEquals(40, bodyJson.optInt("top_k"))
        assertEquals(8192, bodyJson.optInt("max_tokens"))
        assertEquals(3, preview.promptSegments)
        assertEquals(1234, preview.promptChars)
        assertFalse(preview.promptOverflow)
        assertTrue(preview.canImportPrompt)
        assertEquals(3, preview.mapped.size)
        assertEquals("openai_max_tokens", preview.mapped.last().source)
        val groups = preview.dropped.map { it.group }
        assertEquals(listOf("upstream", "client", "gateway", "unknown"), groups)
        assertTrue(preview.dropped.first().reason.contains("400"))
    }

    @Test
    fun overflowingPromptCannotBeImported() {
        val body = """
            {
              "format": "chat", "name": "大预设",
              "body": {"temperature": 1.0},
              "system_prompt": "很长的提示词",
              "prompt_included": true,
              "prompt_segments": 90,
              "prompt_chars": 120000,
              "prompt_bytes": 360000,
              "prompt_limit": 32768,
              "prompt_overflow": true,
              "mapped": [], "dropped": []
            }
        """.trimIndent()
        val preview = parseTavernPresetPreview(body)
        assertTrue(preview.promptOverflow)
        // 关键：溢出时不允许导入提示词（只能导采样参数，绝不截断）
        assertFalse(preview.canImportPrompt)
        assertTrue(tavernPromptSummary(preview).contains("只能导入采样参数"))
        assertTrue(tavernPromptSummary(preview).contains("120000"))
    }

    @Test
    fun promptSummaryAndGroupLabelsAreReadable() {
        val normal = parseTavernPresetPreview(
            """{"prompt_segments":3,"prompt_chars":1234,"system_prompt":"x","prompt_overflow":false}""",
        )
        assertEquals("将写入 system_prompt：3 段（共 1234 字）", tavernPromptSummary(normal))

        val noPrompt = parseTavernPresetPreview("""{"system_prompt":"","prompt_overflow":false}""")
        assertFalse(noPrompt.canImportPrompt)

        assertEquals("Chat Completion 预设", tavernFormatLabel("chat"))
        assertEquals("Text Generation 预设", tavernFormatLabel("text"))
        assertTrue(tavernFormatLabel("whatever").contains("未识别"))
        assertEquals("网关自管（写进预设会被拒绝）", tavernDropGroupLabel("gateway"))
        assertEquals("上游不通用（发了会 400 或被忽略）", tavernDropGroupLabel("upstream"))
        assertEquals("客户端行为（不是请求参数）", tavernDropGroupLabel("client"))
        assertEquals("未识别字段", tavernDropGroupLabel("unknown"))
    }
}
