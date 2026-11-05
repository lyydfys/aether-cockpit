package com.muyuchat.aether.ui

import androidx.compose.ui.unit.dp
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 关于页视觉常量必须与 MCA 逐项一致（用户要求「1:1 复刻 MCA」）。
 *
 * 依据 `E:\MAC\feature\settings\src\main\java\com\muyuchat\feature\settings\SettingsAboutScreen.kt`：
 *  - :120 外层 padding horizontal 16 / vertical 12；:121 区间距 spacedBy(12)
 *  - :299 应用卡 padding horizontal 20 / vertical 22；:294 shape 12
 *  - :378 更新卡 padding 16；:375 shape 8；:379 区间距 10
 *  - :306 品牌标 64dp
 *  - :323 版本胶囊 padding 10/6
 *
 * 这些数字一旦漂移，"1:1" 就不成立——所以用测试钉住，而不是靠注释自觉。
 */
class AboutMcaVisualContractTest {
    @Test
    fun sectionGapMatchesMca() {
        assertEquals("MCA :121 spacedBy(12)", 12.dp, AboutMcaSectionGap)
    }

    @Test
    fun heroCardPaddingMatchesMca() {
        assertEquals("MCA :299 horizontal 20", 20.dp, AboutMcaHeroPaddingH)
        assertEquals("MCA :299 vertical 22", 22.dp, AboutMcaHeroPaddingV)
        assertEquals("MCA :294 shape 12dp", 12.dp, AboutMcaHeroShapeDp)
        assertEquals("MCA :306 品牌标 64dp", 64.dp, AboutMcaLogoSize)
    }

    @Test
    fun updateCardPaddingMatchesMca() {
        assertEquals("MCA :378 padding 16", 16.dp, AboutMcaCardPadding)
        assertEquals("MCA :375 shape 8dp", 8.dp, AboutMcaCardShapeDp)
        assertEquals("MCA :379 区间距 10", 10.dp, AboutMcaCardGap)
    }

    @Test
    fun versionPillMatchesMca() {
        assertEquals("MCA :323 horizontal 10", 10.dp, AboutMcaPillPaddingH)
        assertEquals("MCA :323 vertical 6", 6.dp, AboutMcaPillPaddingV)
        // MCA 用 RoundedCornerShape(50)：百分比圆角，不是 dp
        assertEquals("MCA :323 shape 50%", 50, AboutMcaPillCornerPercent)
    }

    @Test
    fun logoIsTheMcaSizeNotTheOverviewCardSize() {
        // 用户明确要 1:1，不要为了"和概览卡 38dp 一致"而妥协
        assertEquals(64.dp, AboutMcaLogoSize)
        androidx.compose.ui.unit.Dp(38f).let { overviewSize ->
            assertEquals(true, AboutMcaLogoSize > overviewSize)
        }
    }

    @Test
    fun heroCardIsLooserThanTheUpdateCard() {
        // MCA 的层次：应用卡是大标题区（20/22），更新卡是信息区（16）——不能反过来
        assertEquals(true, AboutMcaHeroPaddingH > AboutMcaCardPadding)
        assertEquals(true, AboutMcaHeroPaddingV > AboutMcaCardPadding)
    }
}
