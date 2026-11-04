package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 多实例档案（`GET /api/profiles`）的解析。
 *
 * 契约（`internal/core/gateway/profiles_api.go:18-30`）：
 * `[{id,name,clients:[{client_id,model,origin}],updated_at}]`。
 * 手机端**只读**：激活要写桌面客户端配置文件（`profiles_api.go:14`），Android 上没有那些目录。
 */
class ProfilesApiTest {

    @Test
    fun parsesProfilesWithClients() {
        val rows = parseProfiles(
            """
            [{"id":"p-1","name":"省钱档","updated_at":1730500000,
              "clients":[{"client_id":"claude_code","model":"qoder-cn/auto"},
                         {"client_id":"codex_cli","model":"gpt-4o","origin":"http://127.0.0.1:8787"}]}]
            """.trimIndent(),
        )
        assertEquals(1, rows.size)
        val profile = rows.single()
        assertEquals("p-1", profile.id)
        assertEquals("省钱档", profile.name)
        assertEquals(1730500000L, profile.updatedAt)
        assertEquals(2, profile.clients.size)
        assertEquals("claude_code", profile.clients[0].clientId)
        assertEquals("qoder-cn/auto", profile.clients[0].model)
        assertEquals("", profile.clients[0].origin)
        assertEquals("http://127.0.0.1:8787", profile.clients[1].origin)
    }

    @Test
    fun missingIdFallsBackToNameAndEmptyClientsSurvive() {
        // id 由名称派生（profiles_api.go:32 profileID），老数据可能只给 name。
        val rows = parseProfiles("""[{"name":"高质量档","clients":[]}]""")
        assertEquals("高质量档", rows.single().id)
        assertEquals("高质量档", rows.single().name)
        assertTrue(rows.single().clients.isEmpty())
        // 既没 id 也没 name 的条目丢掉（列表里没法指代它）。
        assertTrue(parseProfiles("""[{"updated_at":1}]""").isEmpty())
    }

    @Test
    fun skipsClientsWithoutClientId() {
        val rows = parseProfiles(
            """[{"id":"p","name":"n","clients":[{"model":"m"},{"client_id":"","model":"m"},{"client_id":"ok"}]}]""",
        )
        assertEquals(listOf("ok"), rows.single().clients.map { it.clientId })
    }

    @Test
    fun clientLabelOmitsBlankParts() {
        assertEquals(
            "claude_code · qoder-cn/auto",
            profileClientLabel(ProfileClientRow("claude_code", "qoder-cn/auto", "")),
        )
        assertEquals("claude_code", profileClientLabel(ProfileClientRow("claude_code", "", "")))
        assertEquals(
            "claude_code · m · http://x",
            profileClientLabel(ProfileClientRow("claude_code", "m", "http://x")),
        )
    }

    @Test
    fun malformedBodyIsExplicitError() {
        for (body in listOf("not json", "", "{}")) {
            val error = runCatching { parseProfiles(body) }.exceptionOrNull()
            assertTrue(
                "body=<$body> 应抛 ManagementApiException，实际=${error?.javaClass?.name ?: "没有抛异常（被当成合法回包了）"}",
                error is ManagementApiException,
            )
        }
        // 空数组是合法回包（就是"没有档案"）。
        assertTrue("[] 是合法回包，应解析成空列表", parseProfiles("[]").isEmpty())
    }
}
