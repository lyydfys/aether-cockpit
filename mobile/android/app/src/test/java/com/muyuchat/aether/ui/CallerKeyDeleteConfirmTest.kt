package com.muyuchat.aether.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 删除调用方密钥的「输入名称确认」判定。
 *
 * 口径：只有**精确输入**目标名称（或它回落到 id）时才允许删除。
 * 这里是纯函数，页面只负责把结果接到按钮 enabled 上——
 * 判定逻辑不放 Composable 里，才好测。
 */
class CallerKeyDeleteConfirmTest {
    @Test
    fun exactNameEnablesDelete() {
        assertTrue(callerKeyDeleteConfirmed("my-key", name = "my-key", id = "ck_1234"))
    }

    @Test
    fun surroundingSpacesAreTolerated() {
        assertTrue(callerKeyDeleteConfirmed("  my-key  ", name = "my-key", id = "ck_1234"))
    }

    @Test
    fun blankNameFallsBackToId() {
        // 名称留空的密钥：界面显示的是 id，就必须输 id
        assertTrue(callerKeyDeleteConfirmed("ck_1234", name = "", id = "ck_1234"))
        assertFalse(callerKeyDeleteConfirmed("", name = "", id = "ck_1234"))
    }

    @Test
    fun partialOrWrongInputStaysDisabled() {
        assertFalse(callerKeyDeleteConfirmed("my", name = "my-key", id = "ck_1234"))
        assertFalse(callerKeyDeleteConfirmed("my-key2", name = "my-key", id = "ck_1234"))
        assertFalse(callerKeyDeleteConfirmed("CK_1234", name = "", id = "ck_1234"))
    }

    @Test
    fun emptyTargetNeverEnables() {
        assertFalse(callerKeyDeleteConfirmed("", name = "", id = ""))
        assertFalse(callerKeyDeleteConfirmed("anything", name = "", id = ""))
    }
}
