package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ClientEntry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 客户端页的排版合同：状态词二值映射、副行只显示真的有的字段、空态说清为什么空。
 */
class ClientsPageLayoutTest {

    @Test
    fun detectedStateIsBinaryAndNeverInvented() {
        assertEquals("已检测到", clientDetectedLabel(true))
        assertEquals("未检测到", clientDetectedLabel(false))
    }

    @Test
    fun kindLineKeepsOnlyProvidedFields() {
        assertEquals("", clientKindLine(client(kind = "", backups = 0)))
        assertEquals("claude-code", clientKindLine(client(kind = "claude-code", backups = 0)))
        assertEquals("2 份备份", clientKindLine(client(kind = "", backups = 2)))
        assertEquals("codex · 1 份备份", clientKindLine(client(kind = "codex", backups = 1)))
    }

    @Test
    fun emptyStateSaysWhyAndWhatNext() {
        val detail = clientsEmptyDetail()
        assertTrue("要说清清单为什么是空的", detail.contains("空的"))
        assertTrue("要点出这些目录在桌面上", detail.contains("桌面"))
        assertTrue("要给出下一步动作", detail.contains("刷新"))
        // 不允许出现「暂无数据」这种把原因藏起来的写法。
        assertTrue(!detail.contains("暂无数据"))
    }

    private fun client(kind: String, backups: Int): ClientEntry = ClientEntry(
        id = "c1",
        name = "Claude Code",
        kind = kind,
        detected = false,
        backups = backups,
        note = "",
    )
}
