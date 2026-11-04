package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 客户端只读列表。
 *
 * 契约（`handleClients` + `clients.Status`）：`{clients:[{id,name,kind,path,detected,managed?,backups[],note?}],available,targets,wsl}`。
 * **不含任何凭据**——`path` 是桌面文件路径（手机上没有这个路径），所以本轮**不展示 path**，
 * 免得让人以为手机上能去那里改配置。
 */
class ClientsApiTest {
    @Test
    fun parsesClientsAndAvailability() {
        val snapshot = parseClients(
            """{"available":true,
                "clients":[
                  {"id":"claude","name":"Claude Code","kind":"claude","path":"C:/Users/x/.claude.json",
                   "detected":true,"backups":[{"file":"a","time":1}],"note":"需要设置 ANTHROPIC_BASE_URL"},
                  {"id":"codex","name":"Codex","kind":"codex","path":"C:/Users/x/.codex/config.toml",
                   "detected":false,"backups":[]}],
                "targets":[{"id":"wsl-ubuntu","label":"Ubuntu"}],"wsl":{}}""",
        )
        assertTrue(snapshot.available)
        assertEquals(2, snapshot.clients.size)
        assertEquals(1, snapshot.targets)
        val claude = snapshot.clients[0]
        assertEquals("Claude Code", claude.name)
        assertEquals("claude", claude.kind)
        assertTrue(claude.detected)
        assertEquals(1, claude.backups)
        assertTrue(claude.note.contains("ANTHROPIC_BASE_URL"))
        assertFalse(snapshot.clients[1].detected)
        assertEquals(0, snapshot.clients[1].backups)
    }

    @Test
    fun pathIsNotCarriedIntoTheModel() {
        // 桌面路径在手机上无意义，不该出现在模型里（也就不会被顺手显示出来）。
        val snapshot = parseClients(
            """{"available":true,"clients":[{"id":"a","name":"A","kind":"k","path":"C:/Users/x/secret.json","detected":true}]}""",
        )
        val text = clientSummaryLine(snapshot) + snapshot.clients.joinToString { it.name + it.kind }
        assertFalse("不能把桌面路径带出来：$text", text.contains("C:/Users"))
    }

    @Test
    fun summaryReportsDetectedCountAndDesktopOnly() {
        val snapshot = parseClients(
            """{"available":true,"clients":[
                 {"id":"a","name":"A","kind":"k","detected":true},
                 {"id":"b","name":"B","kind":"k","detected":false}]}""",
        )
        val line = clientSummaryLine(snapshot)
        assertTrue(line.contains("2"))
        assertTrue(line.contains("1"))
        assertTrue("要说明注入仅桌面端：$line", line.contains("仅桌面端"))
    }

    @Test
    fun unavailableBackendSaysSo() {
        val snapshot = parseClients("""{"available":false,"clients":[]}""")
        assertFalse(snapshot.available)
        assertTrue(snapshot.clients.isEmpty())
        assertTrue(clientSummaryLine(snapshot).contains("不可用"))
    }

    @Test
    fun emptyListIsHonest() {
        val snapshot = parseClients("""{"available":true,"clients":[]}""")
        assertTrue(clientSummaryLine(snapshot).contains("没有可接入的客户端"))
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseClients(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
