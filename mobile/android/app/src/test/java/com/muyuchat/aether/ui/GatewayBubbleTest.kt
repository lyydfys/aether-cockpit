package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 悬浮球 / 系统外显 / 后台常驻的纯文案与状态判定。
 *
 * 不测 WindowManager（需要真机与权限），但把"必须先有网关、再要权限"、
 * "不许承诺永不被杀"这两条合同钉在单测里。
 */
class GatewayBubbleTest {
    @Test
    fun dragUnderEightDpIsAClickAndEightDpIsNot() {
        assertTrue(isBubbleClick(distancePx = 0f, density = 1f))
        assertTrue(isBubbleClick(distancePx = 7.9f, density = 1f))
        assertTrue(isBubbleClick(distancePx = 15.9f, density = 2f))
        assertFalse(isBubbleClick(distancePx = 8f, density = 1f))
        assertFalse(isBubbleClick(distancePx = 16f, density = 2f))
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
}
