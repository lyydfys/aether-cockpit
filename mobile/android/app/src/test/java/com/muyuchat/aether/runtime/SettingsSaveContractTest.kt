package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** 保存体必须与 Go 端真实读取键一致；这三条是实际断链的回归锚点。 */
class SettingsSaveContractTest {
    @Test
    fun themeMustBeTopLevelBecauseUiIsReserved() {
        val body = JSONObject(buildThemeJson("dark"))
        assertEquals("dark", body.getString("theme"))
        assertFalse("applyUIPrefs 会跳过 reserved 的 ui，发嵌套对象等于没保存", body.has("ui"))
    }

    @Test
    fun monitorMinutesGoesToDedicatedEndpointBody() {
        assertEquals("{\"minutes\":30}", buildChannelMonitorJson(30))
        assertEquals("{\"minutes\":0}", buildChannelMonitorJson(-5))
    }

    @Test
    fun privacyToggleKeepsRulesAndSidecar() {
        val current = """
            {"privacy_mask":{"enabled":false,"rules":["keys","jwt"],
             "sidecar":{"command":"x"},"note":"keep"}}
        """.trimIndent()
        val body = JSONObject(buildPrivacyMaskJson(current, true))
        val privacy = body.getJSONObject("privacy_mask")
        assertTrue(privacy.getBoolean("enabled"))
        assertEquals(2, privacy.getJSONArray("rules").length())
        assertEquals("jwt", privacy.getJSONArray("rules").getString(1))
        assertEquals("x", privacy.getJSONObject("sidecar").getString("command"))
        assertEquals("keep", privacy.getString("note"))
    }

    @Test
    fun privacyToggleWithoutExistingObjectStillProducesEnabledObject() {
        val privacy = JSONObject(buildPrivacyMaskJson("{}", true)).getJSONObject("privacy_mask")
        assertTrue(privacy.getBoolean("enabled"))
    }
}
