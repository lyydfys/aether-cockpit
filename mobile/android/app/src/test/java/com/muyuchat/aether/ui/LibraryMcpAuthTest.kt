package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** MCP 登录流程的状态文案（桌面 `library.js:790` `mcpLogin`）。 */
class LibraryMcpAuthTest {
    @Test
    fun stateLabelsCoverEveryBranch() {
        assertEquals("未开始", mcpAuthStateLabel(McpAuthUiState.Idle))
        assertEquals("申请授权地址…", mcpAuthStateLabel(McpAuthUiState.Starting("github")))
        assertEquals(
            "等待浏览器授权",
            mcpAuthStateLabel(McpAuthUiState.Waiting("github", "flow-1", "https://auth.example")),
        )
        assertEquals("登录成功", mcpAuthStateLabel(McpAuthUiState.Done("github", alreadySignedIn = false)))
        assertEquals("已登录", mcpAuthStateLabel(McpAuthUiState.Done("github", alreadySignedIn = true)))
        assertEquals(
            "登录失败：授权被拒绝",
            mcpAuthStateLabel(McpAuthUiState.Failed("github", "signin_failed", "授权被拒绝")),
        )
    }

    @Test
    fun labelsAreDistinctSoTheUiNeverShowsTwoWhereMoreOne() {
        val labels = listOf(
            mcpAuthStateLabel(McpAuthUiState.Idle),
            mcpAuthStateLabel(McpAuthUiState.Starting("github")),
            mcpAuthStateLabel(McpAuthUiState.Waiting("github", "f", "u")),
            mcpAuthStateLabel(McpAuthUiState.Done("github", false)),
            mcpAuthStateLabel(McpAuthUiState.Failed("github", "signin_failed", "授权被拒绝")),
        )
        assertEquals(labels.size, labels.toSet().size)
        assertTrue(labels.all { it.isNotBlank() })
    }
}
