package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class SettingsCallerModelsCopyTest {
    @Test
    fun whitelistSummaryDistinguishesUnrestrictedFromSelected() {
        assertEquals("模型白名单：不限制（编辑）", callerKeyModelsSummary(emptyList()))
        assertEquals("模型白名单：gpt-5（编辑）", callerKeyModelsSummary(listOf("gpt-5")))
        assertEquals("模型白名单：3 个模型（编辑）", callerKeyModelsSummary(listOf("a", "b", "c")))
    }
}
