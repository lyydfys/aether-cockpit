package com.muyuchat.aether.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.abs

/**
 * 悬浮球 / 系统外显 / 后台常驻的纯文案与状态判定。
 *
 * 不测 WindowManager（需要真机与权限），但把"必须先有网关、再要权限"、
 * "不许承诺永不被杀"这两条合同钉在单测里。
 */
class GatewayBubbleTest {
    @Test
    fun stoppedOrbHasNoLight() {
        // 用户规格：停止网关时「就是一个圆 + 品牌标识，没有灯光」。
        assertEquals(0f, orbRingAlpha(running = false, phase = 0f))
        assertEquals(0f, orbRingAlpha(running = false, phase = 0.5f))
        assertEquals(0f, orbRingAlpha(running = false, phase = 1f))
        assertEquals(0f, orbGlowAlpha(running = false, phase = 0.5f))
    }

    @Test
    fun runningOrbBreathesInsideTheRingRange() {
        // 运行态：环亮度在 0.35 ↔ 0.85 之间来回，超界要被夹住（不能亮成实心、也不能灭掉）。
        assertEquals(OrbRingAlphaLow, orbRingAlpha(running = true, phase = 0f))
        assertEquals(OrbRingAlphaHigh, orbRingAlpha(running = true, phase = 1f))
        val mid = orbRingAlpha(running = true, phase = 0.5f)
        assertTrue(mid > OrbRingAlphaLow && mid < OrbRingAlphaHigh)
        assertEquals(OrbRingAlphaLow, orbRingAlpha(running = true, phase = -3f))
        assertEquals(OrbRingAlphaHigh, orbRingAlpha(running = true, phase = 9f))
        // 内发光极淡，且有上限；与环反相（环最亮时发光最弱）。
        assertTrue(orbGlowAlpha(running = true, phase = 0f) <= OrbGlowAlphaMax)
        assertEquals(0f, orbGlowAlpha(running = true, phase = 1f), 1e-6f)
    }

    @Test
    fun orbKeepsTheSameDiameterInBothStates() {
        // 「不许扩大圆形」：运行态的灯光只画在圆内，直径与停止态完全一致。
        assertEquals(OrbDiameter, orbDiameter(running = true))
        assertEquals(OrbDiameter, orbDiameter(running = false))
        assertEquals(OrbDiameter, orbDiameter(running = true))
        assertEquals(48f, OrbDiameter.value)
        // 灯环宽度在用户给的 1.5–2dp 区间内。
        assertTrue(OrbRingWidth.value >= 1.5f && OrbRingWidth.value <= 2f)
        // 环的呼吸周期 1.6s。
        assertEquals(1600, OrbRingPeriodMs)
    }

    @Test
    fun orbGlyphIsNeverTinted() {
        // 图标永远品牌本色：单色 tint 会把气泡/星芒涂平（上一轮线上事故）。
        assertFalse(orbTintsGlyph())
    }

    @Test
    fun overlayNeedsGatewayBeforePermission() {
        assertEquals(OverlayAvailability.NeedGateway, overlayAvailability(running = false, permissionGranted = false))
        assertEquals(OverlayAvailability.NeedGateway, overlayAvailability(running = false, permissionGranted = true))
        assertEquals(OverlayAvailability.NeedPermission, overlayAvailability(running = true, permissionGranted = false))
        assertEquals(OverlayAvailability.Available, overlayAvailability(running = true, permissionGranted = true))
    }

    @Test
    fun overlayActionLabelMatchesVisibility() {
        assertEquals("app 外显", overlayActionLabel(visible = false))
        assertEquals("关闭 app 外显", overlayActionLabel(visible = true))
    }

    @Test
    fun overlayActionBecomesReturnToAppOutsideTheApp() {
        // 外显打开后，同一个按钮按宿主切换语义：
        // 应用内保持原样「关闭 app 外显」，应用外变成「返回 app」（把 App 拉回前台）。
        assertEquals("关闭 app 外显", overlayActionLabel(visible = true, inApp = true))
        assertEquals("返回 app", overlayActionLabel(visible = true, inApp = false))
        // 外显没开时永远是入口「app 外显」，和宿主无关。
        assertEquals("app 外显", overlayActionLabel(visible = false, inApp = true))
        assertEquals("app 外显", overlayActionLabel(visible = false, inApp = false))
    }

    @Test
    fun callerKeyCopyIsHiddenOutsideAppOrWithoutKey() {
        // 读不到 caller key 时不渲染（绝不显示一把空的）；
        // showCopy=false 是宿主显式关闭复制的开关——系统悬浮窗现在传 true，
        // 因为 Android 10+ 限制的是后台**读**剪贴板，写不受影响。
        assertFalse(showCallerKeyCopy(showCopy = false, callerKey = "aek_abc"))
        assertFalse(showCallerKeyCopy(showCopy = true, callerKey = ""))
        assertFalse(showCallerKeyCopy(showCopy = true, callerKey = "   "))
        assertTrue(showCallerKeyCopy(showCopy = true, callerKey = "aek_abc"))
    }

    @Test
    fun copyButtonsReportSuccessAndFailure() {
        // 写入成功 → 「已复制…」；写入失败 → 「复制失败」，不静默吞掉。
        assertEquals("复制地址", addressCopyLabel(copied = false, failed = false))
        assertEquals("已复制", addressCopyLabel(copied = true, failed = false))
        assertEquals("复制失败", addressCopyLabel(copied = false, failed = true))
        assertEquals("复制密钥", callerKeyCopyLabel(copied = false, failed = false))
        assertEquals("已复制密钥", callerKeyCopyLabel(copied = true, failed = false))
        assertEquals("复制失败", callerKeyCopyLabel(copied = false, failed = true))
    }

    @Test
    fun actionGridPacksTwoPerRowAndKeepsLoneCell() {
        // 固定顺序切成每行最多 2 颗：4 颗 → 2×2；3 颗 → [2,1]（第二行只占前半格）；
        // 2 颗 → 一行两颗；1 颗 → 一行一颗。少一颗时后面的自动补位，不会塌成整行。
        assertEquals(
            listOf(listOf("a", "b"), listOf("c", "d")),
            bubbleActionRows(listOf("a", "b", "c", "d")),
        )
        assertEquals(
            listOf(listOf("a", "b"), listOf("c")),
            bubbleActionRows(listOf("a", "b", "c")),
        )
        assertEquals(listOf(listOf("a", "b")), bubbleActionRows(listOf("a", "b")))
        assertEquals(listOf(listOf("a")), bubbleActionRows(listOf("a")))
        assertEquals(emptyList<List<String>>(), bubbleActionRows(emptyList<String>()))
    }

    @Test
    fun callerKeyCopyButtonReportsSuccessOnce() {
        assertEquals("复制密钥", callerKeyCopyLabel(copied = false))
        assertEquals("已复制密钥", callerKeyCopyLabel(copied = true))
    }

    @Test
    fun permissionCopyStatesWhyAndWhereToGrant() {
        val copy = overlayPermissionRequestCopy()
        assertTrue(copy.contains("显示在其他应用上层"))
        assertTrue(copy.contains("退出"))
        assertTrue(copy.contains("app 外显"))
    }

    @Test
    fun backgroundCopyDoesNotPromiseImmortality() {
        val copy = gatewayBackgroundNote()
        assertTrue(copy.contains("前台服务"))
        assertTrue(copy.contains("通常"))
        assertTrue(copy.contains("系统省电策略"))
        assertTrue(copy.contains("停止后悬浮球会变灰"))
        assertFalse(copy.contains("永不"))
        assertFalse(copy.contains("永久"))
        assertFalse(copy.contains("保证"))
    }

    @Test
    fun stoppedOrbPlateContrastsWithBrandGlyph() {
        // 用户规格：停止态 = 浅灰圆 + 品牌本色图标（不做 tint）。这里锁"底板与品牌蓝"的亮度差，
        // 保证 1.3× 下气泡轮廓看得清（不再出现"灰色实心方块"）。
        val plateLight = Color(0xFFF1F3F4) // MCA 浅色 surfaceVariant
        val brandBlue = Color(0xFF1A73E8) // 品牌气泡色
        assertNotEquals("底板色不能等于品牌气泡色", plateLight, brandBlue)
        val delta = abs(brandBlue.luminance() - plateLight.luminance())
        assertTrue("底板与品牌蓝亮度差 $delta 太小（要求 ≥0.20），停止态会看不出气泡", delta >= 0.20f)
    }
}
