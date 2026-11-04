package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 配置漂移（`GET /api/drift`）。
 *
 * 返回体是按 Agent id 分组的对象；每项的 `detail` 里拼着**绝对路径 + 配置行原文**
 * （可能就是 `api_key = "…"`）。所以这组测试的核心是：**只取 kind**，
 * 路径与配置行原文一律不得进入手机端模型。
 */
class DriftApiTest {
    private val body = """
        {"claude-code":{"kind":"unwired","field":"model","want":"claude-sonnet-4-6",
                        "detail":"网关地址已被改回官方：C:/Users/x/.claude/settings.json（ANTHROPIC_BASE_URL=https://api.anthropic.com）"},
         "codex":{"kind":"removed","field":"model","want":"gpt-5",
                  "detail":"配置文件已被删除：C:/Users/x/.codex/config.toml（api_key = \"sk-secret-123\"）"}}
    """.trimIndent()

    @Test
    fun parsesAgentAndKind() {
        val report = parseDrift(body)
        assertEquals(2, report.entries.size)
        // 按 agentId 排序，输出稳定
        assertEquals("claude-code", report.entries[0].agentId)
        assertEquals("unwired", report.entries[0].kind)
        assertEquals("removed", report.entries[1].kind)
    }

    @Test
    fun detailAndPathsNeverEnterTheModel() {
        val report = parseDrift(body)
        val rendered = buildString {
            append(driftSummaryLine(report))
            report.entries.forEach { append(driftKindLabel(it.kind)); append(it.agentId) }
        }
        listOf(
            "C:/Users",
            "ANTHROPIC_BASE_URL",
            "api.anthropic.com",
            "sk-secret-123",
            "settings.json",
            "config.toml",
        ).forEach { leak ->
            assertTrue("敏感内容「$leak」不该出现在手机端：$rendered", !rendered.contains(leak))
        }
    }

    @Test
    fun summaryCountsAndPointsToDesktop() {
        val report = parseDrift(body)
        val line = driftSummaryLine(report)
        assertTrue(line.contains("2"))
        assertTrue("修复在桌面端：$line", line.contains("桌面端"))
    }

    @Test
    fun emptyMeansNoDrift() {
        val report = parseDrift("{}")
        assertTrue(report.entries.isEmpty())
        assertTrue(driftSummaryLine(report).contains("没有检测到"))
    }

    @Test
    fun unknownKindIsShownAsIs() {
        val report = parseDrift("""{"weird":{"kind":"brand-new-kind"}}""")
        assertEquals("brand-new-kind", report.entries[0].kind)
        assertEquals("brand-new-kind", driftKindLabel("brand-new-kind"))
    }

    @Test
    fun kindLabelsAreHumanReadable() {
        assertEquals("配置文件已被删除", driftKindLabel("removed"))
        assertEquals("网关地址被改回官方", driftKindLabel("unwired"))
        assertEquals("模型设置被改动", driftKindLabel("model"))
        assertEquals("与上次写入的不一致", driftKindLabel("edited"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "")) {
            val error = runCatching { parseDrift(b) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
