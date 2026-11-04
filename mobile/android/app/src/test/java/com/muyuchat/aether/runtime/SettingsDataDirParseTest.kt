package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Test

/** `dir` 是 settingsView 的只读字段；有值/空串/缺字段三种形态都不能改变旧构造点。 */
class SettingsDataDirParseTest {
    @Test
    fun parsesDirWhenPresent() {
        val summary = parseSettings("""{"dir":"/data/user/0/com.muyuchat.aether/files"}""")
        assertEquals("/data/user/0/com.muyuchat.aether/files", summary.dataDir)
    }

    @Test
    fun blankDirStaysBlank() {
        assertEquals("", parseSettings("""{"dir":""}""").dataDir)
        assertEquals("", parseSettings("""{"dir":"   "}""").dataDir)
    }

    @Test
    fun missingDirDefaultsToEmpty() {
        assertEquals("", parseSettings("{}").dataDir)
    }
}
