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
    fun stoppedBubbleNeverShowsAHalo() {
        assertEquals(0f, gatewayHaloAlpha(running = false, animatedAlpha = 0.9f))
        assertEquals(0f, gatewayHaloAlpha(running = false, animatedAlpha = Float.NaN))
        assertEquals(0.55f, gatewayHaloAlpha(running = true, animatedAlpha = 0.55f))
        assertEquals(1f, gatewayHaloAlpha(running = true, animatedAlpha = 2f))
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
    fun stoppedOrbKeepsPlateAndGlyphDistinct() {
        // 线上 bug：停止态整块灰方块。根因是"图形色 == 底板色"或"用带背景的图去 tint"。
        // 这条合同锁住取色：底板与气泡必须是两个可区分的颜色，星芒用底板色挖亮。
        val light = stoppedOrbColors(
            surfaceVariant = Color(0xFFF1F3F4), // MCA 浅色 surfaceVariant
            onSurfaceVariant = Color(0xFF5F6368), // MCA 浅色 onSurfaceVariant
        )
        assertNotEquals("底板色与图形色不能相同，否则图标糊成实心块", light.plate, light.glyph)
        assertEquals("星芒用底板色挖亮，停止态才认得出气泡轮廓", light.plate, light.star)

        val dark = stoppedOrbColors(
            surfaceVariant = Color(0xFF303134), // MCA 深色 surfaceVariant
            onSurfaceVariant = Color(0xFF9AA0A6), // MCA 深色 onSurfaceVariant
        )
        assertNotEquals(dark.plate, dark.glyph)
        assertEquals(dark.plate, dark.star)

        // 亮度差要够大：1.3× 字号放大后仍能看出"深灰气泡 + 浅灰底板"。
        for ((name, c) in listOf("light" to light, "dark" to dark)) {
            val delta = abs(c.glyph.luminance() - c.plate.luminance())
            assertTrue("$name 亮度差 $delta 太小（要求 ≥0.20），停止态会看不出气泡轮廓", delta >= 0.20f)
        }
    }
}
