package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** 「关于」字段的纯格式化合同：空值不许抛出，也不许编造版本。 */
class SettingsAboutSectionTest {
    @Test
    fun versionShowsNameAndCode() {
        assertEquals("1.2.3 (42)", aboutVersionLabel("1.2.3", 42L))
        assertEquals("— (—)", aboutVersionLabel("", -1L))
        assertEquals("— (—)", aboutVersionLabel(null, null))
    }

    @Test
    fun runtimeFallsBackForMissingBuildFields() {
        assertEquals(
            "Android 16 (API 36) · Pixel",
            aboutRuntimeLabel("16", 36, "Pixel"),
        )
        assertEquals("Android — (API —) · —", aboutRuntimeLabel(null, 0, " "))
    }

    @Test
    fun abiListIsCommaSeparatedAndBlankSafe() {
        assertEquals("arm64-v8a, x86_64", aboutAbiLabel(listOf("arm64-v8a", "x86_64")))
        assertEquals("—", aboutAbiLabel(emptyList()))
        assertEquals("—", aboutAbiLabel(null))
    }

    @Test
    fun gatewayVersionIsHonestWhenMissing() {
        assertEquals("—", aboutGatewayVersionLabel(null))
        assertEquals("—", aboutGatewayVersionLabel("  "))
        assertEquals("—", aboutGatewayVersionLabel(gatewayVersionOf(com.muyuchat.aether.runtime.GatewayStatus())))
        assertEquals("网关未启动或版本未上报", aboutGatewayVersionNote(null))
        assertNull(aboutGatewayVersionNote("1.0"))
    }
}
