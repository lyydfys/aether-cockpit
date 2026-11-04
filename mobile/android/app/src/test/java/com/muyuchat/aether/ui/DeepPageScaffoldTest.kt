package com.muyuchat.aether.ui

import androidx.compose.ui.unit.LayoutDirection
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 悬浮球底部安全区合同：88dp（64 球 + 8 边 + 16 余量），只用于列表 contentPadding。
 */
class DeepPageScaffoldTest {
    @Test
    fun bubbleSafeBottomCoversTheBubblePlusEdge() {
        assertTrue(BubbleSafeBottom.value >= 80f)
        assertEquals(88f, BubbleSafeBottom.value, 0.001f)
        assertEquals(88f, bubbleSafeContentPadding().calculateBottomPadding().value, 0.001f)
        assertEquals(
            20f,
            bubbleSafeContentPadding().calculateLeftPadding(LayoutDirection.Ltr).value,
            0.001f,
        )
    }
}
