package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.GitHubRelease
import com.muyuchat.aether.runtime.GitHubReleaseAsset
import com.muyuchat.aether.runtime.UpdatePlatform
import com.muyuchat.aether.runtime.assetPriority
import com.muyuchat.aether.runtime.choosePlatformAsset
import com.muyuchat.aether.runtime.updateAvailableLabel
import com.muyuchat.aether.runtime.updateDownloadTarget
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 更新包按平台匹配（用户要求「手机推手机、桌面推桌面」）。
 *
 * Android 优先级：`arm64-v8a` > `arm64` > `universal` > `armeabi-v7a` > 其它 `.apk`；
 * 桌面按扩展名 + 名字里的平台关键字；匹配不到就回退 Release 页面并给出说明。
 */
class UpdateAssetPlatformTest {
    private fun asset(name: String, size: Long = 1_000L) =
        GitHubReleaseAsset(name = name, downloadUrl = "https://github.com/x/y/releases/download/v1/$name", sizeBytes = size)

    private fun release(vararg assets: GitHubReleaseAsset) = GitHubRelease(
        tagName = "v1.2.3",
        name = "v1.2.3",
        htmlUrl = "https://github.com/lyydfys/aether-cockpit/releases/tag/v1.2.3",
        body = "",
        publishedAt = "",
        draft = false,
        prerelease = false,
        assets = assets.toList(),
    )

    @Test
    fun androidPrefersArm64ThenUniversal() {
        val assets = listOf(
            asset("aether-1.2.3-universal.apk", 40),
            asset("aether-1.2.3-arm64-v8a.apk", 30),
            asset("aether-1.2.3-armeabi-v7a.apk", 20),
            asset("aether-1.2.3-windows-x64.exe", 50),
        )
        assertEquals("aether-1.2.3-arm64-v8a.apk", choosePlatformAsset(assets, UpdatePlatform.ANDROID)?.name)

        // 没有 arm64 时退到 universal，而不是随便挑一个 apk
        val noArm = assets.filterNot { it.name.contains("arm64") }
        assertEquals("aether-1.2.3-universal.apk", choosePlatformAsset(noArm, UpdatePlatform.ANDROID)?.name)
    }

    @Test
    fun androidFallsBackToAnyApk() {
        val assets = listOf(asset("aether-release.apk", 10), asset("aether-windows.exe", 99))
        assertEquals("aether-release.apk", choosePlatformAsset(assets, UpdatePlatform.ANDROID)?.name)
    }

    @Test
    fun desktopPlatformsNeverPickAnApk() {
        val assets = listOf(
            asset("aether-1.2.3-arm64-v8a.apk", 30),
            asset("aether-1.2.3-windows-amd64-setup.exe", 90),
            asset("aether-1.2.3-macos-arm64.dmg", 80),
            asset("aether-1.2.3-linux-x86_64.AppImage", 70),
        )
        assertEquals("aether-1.2.3-windows-amd64-setup.exe", choosePlatformAsset(assets, UpdatePlatform.WINDOWS)?.name)
        assertEquals("aether-1.2.3-macos-arm64.dmg", choosePlatformAsset(assets, UpdatePlatform.MACOS)?.name)
        assertEquals("aether-1.2.3-linux-x86_64.AppImage", choosePlatformAsset(assets, UpdatePlatform.LINUX)?.name)
        // 每个平台都挑不到别人的包
        assertFalse(choosePlatformAsset(assets, UpdatePlatform.WINDOWS)!!.name.endsWith(".apk"))
    }

    @Test
    fun noMatchingAssetFallsBackToReleasePage() {
        val onlyDesktop = release(asset("aether-1.2.3-windows-setup.exe"))
        val target = updateDownloadTarget(onlyDesktop, UpdatePlatform.ANDROID)!!
        assertFalse("没有 apk 时必须走 Release 页面", target.isDirectAsset)
        assertTrue("落点应是 release 页面", target.url.contains("/releases/tag/"))
        assertTrue("要说明原因", target.note!!.contains("未找到Android安装包"))
        assertTrue(target.note!!.contains("Release 页面"))
    }

    @Test
    fun matchingAssetDownloadsDirectly() {
        val target = updateDownloadTarget(release(asset("aether-1.2.3-arm64-v8a.apk")), UpdatePlatform.ANDROID)!!
        assertTrue(target.isDirectAsset)
        assertTrue(target.url.endsWith(".apk"))
        assertNull("直链时不该再提示回退", target.note)
    }

    @Test
    fun unknownPlatformNeverMatches() {
        assertEquals(-1, assetPriority("aether.apk", UpdatePlatform.UNKNOWN))
        assertNull(choosePlatformAsset(listOf(asset("aether.apk")), UpdatePlatform.UNKNOWN))
        assertNull(updateDownloadTarget(null, UpdatePlatform.ANDROID))
    }

    @Test
    fun statusLabelSaysWhetherThePlatformHasAPackage() {
        assertTrue(updateAvailableLabel("1.2.3", hasPlatformAsset = true).contains("含本平台安装包"))
        assertTrue(updateAvailableLabel("1.2.3", hasPlatformAsset = false).contains("本平台无安装包"))
        assertTrue(updateAvailableLabel("1.2.3", true).startsWith("发现 v1.2.3"))
    }
}
