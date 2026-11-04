package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 插件列表（只读）。
 *
 * 契约（`handlePlugins` + `ruleToMap`）：`{plugins:[{id,kind,name,enabled,note?,providers?,models?}],builtin:[{id,name,desc,on}],total}`
 * **不含 env/headers**——那是 MCP 的 config，不是这里的插件规则。
 *
 * 图标那边**没有列表接口**（只有 `POST /api/icons` 与 `GET /api/icons/{name}`），
 * 所以本轮不做图标清单，页面上如实写"只能在桌面端管理"。
 */
class PluginsApiTest {
    @Test
    fun parsesRulesAndBuiltins() {
        val snapshot = parsePlugins(
            """{"total":2,"builtin":[{"id":"param_override","name":"参数覆盖","desc":"统一改写参数","on":true},
                                     {"id":"word_guard","name":"关键词拦截","desc":"命中词拦下","on":false}],
                "plugins":[
                  {"id":"r1","kind":"param","name":"温度归零","enabled":true,"note":"仅测试","providers":["workbuddy-cn"]},
                  {"id":"r2","kind":"model","name":"模型重写","enabled":false,"models":["claude-*"]}]}""",
        )
        assertEquals(2, snapshot.total)
        assertEquals(2, snapshot.rules.size)
        assertEquals(2, snapshot.builtins.size)
        val rule = snapshot.rules[0]
        assertEquals("r1", rule.id)
        assertEquals("param", rule.kind)
        assertEquals("温度归零", rule.name)
        assertTrue(rule.enabled)
        assertEquals("仅测试", rule.note)
        assertEquals(listOf("workbuddy-cn"), rule.providers)
        assertEquals(listOf("claude-*"), snapshot.rules[1].models)
        assertEquals(false, snapshot.builtins[1].on)
    }

    @Test
    fun ruleSummaryNamesTargetsOrSaysAllChannels() {
        val scoped = PluginRule("r", "param", "范围", true, "", listOf("workbuddy-cn"), emptyList())
        assertTrue(pluginRuleSummary(scoped).contains("workbuddy-cn"))
        val global = PluginRule("r", "param", "范围", true, "", emptyList(), emptyList())
        assertTrue("没有范围时要写全渠道，不能空着：${pluginRuleSummary(global)}", pluginRuleSummary(global).contains("全部渠道"))
    }

    @Test
    fun disabledRuleIsMarkedInSummary() {
        val rule = PluginRule("r", "param", "范围", false, "", emptyList(), emptyList())
        assertTrue(pluginRuleSummary(rule).contains("已停用"))
    }

    @Test
    fun emptyListIsHonest() {
        val snapshot = parsePlugins("""{"total":0,"plugins":[],"builtin":[]}""")
        assertTrue(snapshot.rules.isEmpty())
        assertTrue(snapshot.builtins.isEmpty())
        assertTrue(pluginsSummaryLine(snapshot).contains("没有"))
    }

    @Test
    fun summaryLineCountsEnabled() {
        val snapshot = parsePlugins(
            """{"total":3,"builtin":[],"plugins":[
                 {"id":"a","kind":"k","name":"A","enabled":true},
                 {"id":"b","kind":"k","name":"B","enabled":true},
                 {"id":"c","kind":"k","name":"C","enabled":false}]}""",
        )
        val line = pluginsSummaryLine(snapshot)
        assertTrue(line.contains("3"))
        assertTrue(line.contains("2"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parsePlugins(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    /**
     * 安全回归：手机端只开放「名称/说明/范围/启用」四个可见字段，
     * 保存时必须用服务端原 JSON 做底稿——否则一次改名就把 kind 专有字段（温度/提示词）清空。
     */
    @Test
    fun editsKeepKindSpecificFields() {
        val snapshot = parsePlugins(
            """{"builtin":[],"plugins":[{"id":"r1","kind":"param_override","name":"温度归零","enabled":true,
                 "temperature":0.2,"top_p":0.9,"max_tokens":128,"prompt":"keep-me","providers":["a"]}]}""",
        )
        val rule = snapshot.rules.single()
        assertTrue("规则原 JSON 必须带到模型里", rule.raw.contains("temperature"))

        val toggled = pluginRuleToggleJson(rule, false)
        assertTrue(toggled.contains("\"temperature\":0.2"))
        assertTrue(toggled.contains("\"prompt\":\"keep-me\""))
        assertTrue(toggled.contains("\"enabled\":false"))

        val edited = pluginRuleWithEdits(
            rule = rule,
            name = "改名后",
            note = "",
            providers = listOf("b", "c"),
            models = emptyList(),
            enabled = true,
        )
        assertTrue(edited.contains("\"temperature\":0.2"))
        assertTrue(edited.contains("\"top_p\":0.9"))
        assertTrue(edited.contains("\"max_tokens\":128"))
        assertTrue(edited.contains("\"prompt\":\"keep-me\""))
        assertTrue(edited.contains("\"providers\":[\"b\",\"c\"]"))
        assertTrue("说明为空时应移除该键", !edited.contains("\"note\""))
    }

    @Test
    fun parsesSavedRuleResponse() {
        val rule = parseSavedRule(
            """{"rule":{"id":"r9","kind":"model_map","name":"映射","enabled":true,"from":"a","to":"b"}}""",
        )
        assertEquals("r9", rule.id)
        assertEquals("model_map", rule.kind)
        assertTrue(rule.enabled)
        assertTrue(rule.raw.contains("\"from\":\"a\""))
        val error = runCatching { parseSavedRule("""{"ok":true}""") }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
    }
}
