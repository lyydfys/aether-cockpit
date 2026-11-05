package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AppUpdateStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** 关于页的文案与格式化（纯函数）。 */
class AboutSectionTest {
    @Test
    fun versionAndRuntimeLabelsUseDashForMissingValues() {
        assertEquals("1.2.3 (42)", aboutVersionLabel("1.2.3", 42L))
        // null / 空白 / 负 code 一律「—」，不抛异常
        assertEquals("— (—)", aboutVersionLabel(null, null))
        assertEquals("— (—)", aboutVersionLabel("   ", -1L))
        assertEquals("Android 16 (API 36) · Pixel", aboutRuntimeLabel("16", 36, "Pixel"))
        assertEquals("Android — (API —) · —", aboutRuntimeLabel(null, 0, " "))
        assertEquals("arm64-v8a, armeabi-v7a", aboutAbiLabel(listOf("arm64-v8a", " ", "armeabi-v7a")))
        assertEquals("—", aboutAbiLabel(null))
        assertEquals("—", aboutGatewayVersionLabel(null))
        assertEquals("网关未启动或版本未上报", aboutGatewayVersionNote(null))
        assertEquals(null, aboutGatewayVersionNote("1.0.0"))
    }

    @Test
    fun updateStatusLabelsMatchMcaPresentation() {
        assertEquals("尚未检查", appUpdateStatusLabel(AppUpdateStatus.IDLE))
        assertEquals("检查中…", appUpdateStatusLabel(AppUpdateStatus.CHECKING))
        assertEquals("已是最新版本", appUpdateStatusLabel(AppUpdateStatus.UP_TO_DATE))
        assertEquals("有新版本", appUpdateStatusLabel(AppUpdateStatus.AVAILABLE))
        assertEquals("检查失败", appUpdateStatusLabel(AppUpdateStatus.ERROR))
    }

    @Test
    fun releaseNotesAreTruncatedAtMcaLimit() {
        assertEquals("", aboutReleaseNotesText(""))
        assertEquals("短内容", aboutReleaseNotesText(" 短内容 "))
        val long = "x".repeat(1500)
        val text = aboutReleaseNotesText(long)
        assertEquals(1200, text.length)
        assertTrue("截断后应带省略号", text.endsWith("..."))
    }

    @Test
    fun checkedAtLabelIsRelative() {
        val now = 1_000_000_000_000L
        assertEquals("尚未检查", aboutCheckedAtLabel(0L, now))
        assertEquals("刚刚", aboutCheckedAtLabel(now - 30_000L, now))
        assertEquals("3 分钟前", aboutCheckedAtLabel(now - 3 * 60_000L, now))
        assertEquals("5 小时前", aboutCheckedAtLabel(now - 5 * 3_600_000L, now))
        assertEquals("3 天前", aboutCheckedAtLabel(now - 3 * 86_400_000L, now))
        // 时钟回拨不出现「-N 分钟前」
        assertEquals("刚刚", aboutCheckedAtLabel(now + 60_000L, now))
    }

    @Test
    fun buildTypeAndCopyrightAreStableCopy() {
        assertEquals("debug", buildTypeLabel(debuggable = true))
        assertEquals("release", buildTypeLabel(debuggable = false))
        assertEquals("© 2026 Aether Cockpit · Apache License 2.0", aboutCopyrightLine())
        assertEquals("Aether Cockpit", AppDisplayName)
        assertEquals("Apache License 2.0", AppLicenseName)
    }
}
