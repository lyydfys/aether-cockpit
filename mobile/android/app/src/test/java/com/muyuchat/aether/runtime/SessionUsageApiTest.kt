package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 会话用量的查询串与解析。
 *
 * 端点：`GET /api/sessions/usage?days=7&agent=codex`（`gateway.go:1146` →
 * `sessions_usage_summary.go:72-224`）。`days` 服务端夹在 1..90（`:82-91`），
 * `agent` 空 = 全部实例（`:92`）。
 */
class SessionUsageApiTest {
    @Test
    fun pathCarriesDaysAndOptionalAgent() {
        assertEquals("/api/sessions/usage?days=7", sessionUsagePath(7, ""))
        assertEquals("/api/sessions/usage?days=1", sessionUsagePath(1, "  "))
        assertEquals("/api/sessions/usage?days=30&agent=codex", sessionUsagePath(30, "codex"))
        // agent 里的特殊字符要编码，不能拼出第二个查询参数
        assertEquals("/api/sessions/usage?days=7&agent=a%26b%3Dc", sessionUsagePath(7, "a&b=c"))
        // 服务端窗口上限 90 天，越界先夹住
        assertEquals("/api/sessions/usage?days=90", sessionUsagePath(365, ""))
        assertEquals("/api/sessions/usage?days=1", sessionUsagePath(0, ""))
    }

    @Test
    fun parsesTotalsBucketsAndWindow() {
        val usage = parseSessionUsage(
            """
            {"from":"2026-10-29","to":"2026-11-04","days":7,"agent":"codex","files":12,"sessions":3,
             "input":1000,"cache_read":2000,"cache_write":500,"output":300,"tokens":3800,
             "requests":42,"cost":0.1234,"unpriced":2,"last_sync":1762290000,
             "series":[
               {"date":"2026-11-03","input":600,"cache_read":1000,"cache_write":0,"output":100,"tokens":1700,"requests":20,"cost":0.06},
               {"date":"2026-11-04","input":400,"cache_read":1000,"cache_write":500,"output":200,"tokens":2100,"requests":22,"cost":0.0634}
             ],
             "models":[{"name":"claude-sonnet","tokens":3800,"requests":42,"cost":0.1234}],
             "agents":[{"id":"codex","name":"Codex CLI","sessions":3,"tokens":3800,"requests":42,"cost":0.1234}]}
            """.trimIndent(),
        )
        assertEquals("2026-10-29", usage.from)
        assertEquals("2026-11-04", usage.to)
        assertEquals(7, usage.days)
        assertEquals("codex", usage.agent)
        assertEquals(12, usage.files)
        assertEquals(3, usage.sessions)
        assertEquals(3800L, usage.totals.tokens)
        assertEquals(42L, usage.totals.requests)
        assertEquals(0.1234, usage.totals.cost, 1e-9)
        assertEquals(2L, usage.unpriced)
        assertEquals(1762290000L, usage.lastSync)
        assertEquals(2, usage.series.size)
        assertEquals("2026-11-03", usage.series[0].date)
        assertEquals(1700L, usage.series[0].totals.tokens)
        assertEquals("claude-sonnet", usage.models[0].name)
        assertEquals(3800L, usage.models[0].totals.tokens)
        assertEquals("codex", usage.agents[0].id)
        assertEquals("Codex CLI", usage.agents[0].name)
        assertEquals(3, usage.agents[0].sessions)
    }

    /** 缺 `series/models/agents` 当空数组；缺数字当 0——但坏 JSON 必须报错，不能返回空对象。 */
    @Test
    fun missingCollectionsAreEmptyButBrokenJsonIsAnError() {
        val empty = parseSessionUsage("""{"from":"2026-11-04","to":"2026-11-04","days":1}""")
        assertTrue(empty.series.isEmpty())
        assertTrue(empty.models.isEmpty())
        assertTrue(empty.agents.isEmpty())
        assertEquals(0L, empty.totals.tokens)
        assertEquals(0.0, empty.totals.cost, 1e-9)

        for (body in listOf("not json", "", "[]")) {
            val error = runCatching { parseSessionUsage(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    /** 负数/脏值一律夹成 0，页面不会出现负 Token。 */
    @Test
    fun negativeOrJunkNumbersAreClamped() {
        val usage = parseSessionUsage(
            """{"tokens":-5,"requests":-1,"cost":-2.5,"unpriced":-3,
                "series":[{"date":"2026-11-04","tokens":-10}]}""",
        )
        assertEquals(0L, usage.totals.tokens)
        assertEquals(0L, usage.totals.requests)
        assertEquals(0.0, usage.totals.cost, 1e-9)
        assertEquals(0L, usage.unpriced)
        assertEquals(0L, usage.series[0].totals.tokens)
    }

    /** 缺 `date` / `id` / `name` 的坏行被丢弃，不让页面渲染空标题。 */
    @Test
    fun rowsWithoutIdentityAreDropped() {
        val usage = parseSessionUsage(
            """{"series":[{"date":""},{"date":"2026-11-04"}],
                "models":[{"name":""},{"name":"m1"}],
                "agents":[{"id":""},{"id":"codex"}]}""",
        )
        assertEquals(1, usage.series.size)
        assertEquals(1, usage.models.size)
        assertEquals(1, usage.agents.size)
        // 实例名缺省回退到 id
        assertEquals("codex", usage.agents[0].name)
    }
}
