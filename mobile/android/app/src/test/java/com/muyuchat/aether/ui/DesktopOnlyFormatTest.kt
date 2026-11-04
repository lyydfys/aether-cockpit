package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.UnsupportedCapability
import com.muyuchat.aether.runtime.byDesignNotSupported
import com.muyuchat.aether.runtime.desktopOnlyCapabilities
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「仅 Windows」说明页的文案合同。
 *
 * 两个区块的计数、尾标都必须来自 [desktopOnlyCapabilities] / [byDesignNotSupported]：
 * 以后加一条能力，标题里的数字要跟着变，而不是永远写着 11。
 */
class DesktopOnlyFormatTest {

    @Test
    fun tailLabelSeparatesDesktopOnlyFromByDesign() {
        assertEquals("仅 Windows", capabilityTailLabel(UnsupportedCapability("t", "r")))
        assertEquals(
            "按设计不做",
            capabilityTailLabel(UnsupportedCapability("t", "r", isByDesign = true)),
        )
    }

    @Test
    fun sectionTitlesCarryTheRealCount() {
        assertEquals("需要桌面端（11 项）", desktopOnlySectionTitle(11))
        assertEquals("需要桌面端（12 项）", desktopOnlySectionTitle(12))
        assertEquals("按设计不做（1 项）", byDesignSectionTitle(1))
        assertEquals(1, byDesignCount(byDesignNotSupported()))
        assertEquals(0, byDesignCount(UnsupportedCapability("t", "r")))
    }

    @Test
    fun sourceListIsStillElevenPlusOne() {
        val desktopOnly = desktopOnlyCapabilities()
        assertEquals(11, desktopOnly.size)
        // “按设计不做”不能混进仅 Windows 列表：两件事的原因完全不同。
        assertTrue(desktopOnly.none { it.isByDesign })
        assertTrue(byDesignNotSupported().isByDesign)
        // 每一条都必须有标题和原因，原因不能只是把标题抄一遍。
        desktopOnly.forEach { item ->
            assertTrue(item.title.isNotBlank())
            assertTrue("「${item.title}」缺原因", item.reason.isNotBlank())
            assertFalse(item.reason.trim() == item.title.trim())
        }
    }

    @Test
    fun introLineUsesTheCountedNumber() {
        assertTrue(desktopOnlyIntroLine(11).contains("11 项"))
        assertTrue(desktopOnlyIntroLine(3).contains("3 项"))
        // 口径必须点明"不是还没做，是做不了"，避免读成 roadmap。
        assertTrue(desktopOnlyIntroLine(11).contains("做不了"))
    }
}
