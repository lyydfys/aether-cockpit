package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Agent 接入页的数据层：`GET /api/state`（agents + profiles）、
 * `GET /api/agents/cli`（含 501 平台边界）、`GET /api/agent-models/{id}`。
 *
 * 三条路径都覆盖：正常、空、字段缺失/格式错。CLI 的 501 单独断言，
 * 因为手机运行时**一定**会走到它（`internal/mobile/runtime.go:196` 传零值能力表）。
 */
class AgentsApiTest {

    @Test
    fun parsesAgentsAndProfiles() {
        val overview = parseAgentsOverview(
            """
            {"agents":[
               {"id":"claude_code","name":"Claude Code","icon":"claude_code-color",
                "path":"/root/.claude/settings.json","wired":true,"detected":true,
                "fields":[{"key":"model","label":"model","value":"qwen3-max","options":[]},
                          {"key":"opus","label":"opus 档","value":"","options":[]}],
                "models":{"shown":12,"listed":14}},
               {"id":"codex_cli","name":"Codex CLI","path":"/root/.codex/config.toml",
                "fields":[{"key":"effort","label":"推理档位","value":"high","options":[]}],
                "models":{"shown":12,"listed":14}}
             ],
             "profiles":[{"id":"p1","name":"省钱档","updated_at":1760000000}],
             "clients":[],"catalog":"","served":3}
            """.trimIndent(),
        )
        assertEquals(2, overview.agents.size)
        val claude = overview.agents[0]
        assertEquals("claude_code", claude.id)
        assertEquals("Claude Code", claude.name)
        assertEquals("qwen3-max", claude.model)
        assertTrue(claude.detected)
        assertTrue(claude.wired)
        assertEquals(12, claude.shownModels)
        assertEquals(14, claude.listedModels)
        assertEquals("已接入", agentStatusLabel(claude))
        assertEquals("当前模型 qwen3-max · 可选 12 / 共 14", agentModelLine(claude))
        // 桌面清单的两个布尔字段要逐项展示：detected=已装、wired=已接入。
        assertEquals("已安装 · 已接入", agentPresenceLine(claude))

        // 没有 model 字段时不猜默认模型。
        val codex = overview.agents[1]
        assertEquals("", codex.model)
        assertFalse(codex.detected)
        assertEquals("未检测到", agentStatusLabel(codex))
        assertEquals("当前模型未设置 · 可选 12 / 共 14", agentModelLine(codex))
        assertEquals("未安装 · 未接入", agentPresenceLine(codex))

        assertEquals(1, overview.profiles.size)
        assertEquals("省钱档", overview.profiles[0].name)
    }

    @Test
    fun agentWithoutModelCatalogOmitsTheCount() {
        val overview = parseAgentsOverview(
            """{"agents":[{"id":"aider","name":"Aider","detected":true}],"profiles":[]}""",
        )
        val agent = overview.agents.single()
        assertEquals("已检测到", agentStatusLabel(agent))
        // listed=0 时不写 "0 / 0"，只写当前模型。
        assertEquals("当前模型未设置", agentModelLine(agent))
        assertTrue(overview.profiles.isEmpty())
    }

    @Test
    fun emptyStateExplainsWhyAndWhatNext() {
        val overview = parseAgentsOverview("""{"agents":[],"profiles":[]}""")
        assertTrue(overview.agents.isEmpty())
        val detail = agentsEmptyDetail()
        assertTrue(detail, detail.contains("为什么空"))
        assertTrue(detail, detail.contains("下一步"))
        assertFalse(detail, detail.contains("暂无数据"))
    }

    @Test
    fun malformedStateIsAnExplicitError() {
        for (body in listOf("not json", "", "[]")) {
            val error = runCatching { parseAgentsOverview(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    @Test
    fun defaultFilterHidesUndetectedAndUnwiredAgents() {
        val all = listOf(
            AgentSummary("a", "A", "", detected = true, wired = false, model = "", shownModels = 0, listedModels = 0),
            AgentSummary("b", "B", "", detected = false, wired = true, model = "", shownModels = 0, listedModels = 0),
            AgentSummary("c", "C", "", detected = false, wired = false, model = "", shownModels = 0, listedModels = 0),
        )
        assertEquals(listOf("a", "b"), agentsVisible(all, showAll = false).map { it.id })
        assertEquals(1, agentsHiddenCount(all, showAll = false))
        assertEquals(3, agentsVisible(all, showAll = true).size)
        assertEquals(0, agentsHiddenCount(all, showAll = true))
    }

    @Test
    fun parsesCliInspectionMap() {
        val report = parseAgentCli(
            """
            {"agents":{
               "claude":{"id":"claude","name":"Claude Code","installed":true,"path":"/usr/bin/claude",
                         "version":"2.0.1","method":"npm","install_cmd":"npm i -g @anthropic-ai/claude-code",
                         "update_cmd":"npm i -g @anthropic-ai/claude-code@latest"},
               "aider":{"id":"aider","name":"Aider","installed":false,"install_cmd":"pip install aider-chat"}
             },"checked_at":1760000000,"platform":"android"}
            """.trimIndent(),
        )
        assertEquals(2, report.agents.size)
        assertEquals("android", report.platform)
        assertEquals(1760000000L, report.checkedAtSec)
        // 按名称排序：Aider 在前。
        val aider = report.agents[0]
        assertFalse(aider.installed)
        assertEquals("pip install aider-chat", aider.installCmd)
        val claude = report.agents[1]
        assertTrue(claude.installed)
        assertEquals("2.0.1", claude.version)
        assertTrue(claude.updateCmd.contains("@latest"))
        assertEquals("2 个受巡检 CLI · 已安装 1（android）", agentCliSummaryLine(report))
    }

    @Test
    fun cliUnsupportedKeepsServerWording() {
        // 手机运行时走的就是这条：501 + platform_unsupported（platform.go:38-43）。
        val detail = agentCliUnsupportedDetail("本机 Agent CLI 巡检 仅支持 Windows 桌面端，当前平台无法执行该操作。")
        assertTrue(detail, detail.contains("仅支持 Windows 桌面端"))
        assertTrue(detail, detail.contains("为什么不可用"))
        // 不许把"平台不支持"说成"一个都没装"。
        assertFalse(detail, detail.contains("未安装 0"))
        val empty = agentCliUnsupportedDetail("")
        assertTrue(empty, empty.contains("当前平台无法执行该操作"))
    }

    @Test
    fun parsesAgentModelsWithCurrentAndHiddenFlags() {
        val view = parseAgentModels(
            "claude_code",
            """
            {"models":[{"id":"qwen3-max","name":"Qwen3 Max","inUse":true},
                       {"id":"deepseek-v4","name":"DeepSeek V4","hidden":true}],
             "ordered":false,"only":false,"own":false,"ownHidden":false}
            """.trimIndent(),
        )
        assertEquals("claude_code", view.agentId)
        assertEquals(2, view.models.size)
        assertTrue(view.models[0].inUse)
        assertFalse(view.models[0].hidden)
        assertTrue(view.models[1].hidden)
        assertFalse(view.models[1].inUse)
    }

    @Test
    fun emptyModelListExplainsWhyAndWhatNext() {
        val view = parseAgentModels("x", """{"models":[]}""")
        assertTrue(view.models.isEmpty())
        val detail = agentModelsEmptyDetail()
        assertTrue(detail, detail.contains("为什么空"))
        assertTrue(detail, detail.contains("下一步"))
        assertFalse(detail, detail.contains("暂无数据"))
        assertEquals("巡检结果为空", agentCliSummaryLine(AgentCliReport(emptyList(), "android", 0L)))
    }
}
