package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * GitHub 更新检查的纯逻辑：用例移植自 MCA `GitHubReleaseUpdateTest.kt` 的版本/解析部分
 * （`releaseVersionParsingAndSemVerOrderingFollowExpectedPrecedence`、
 * `invalidReleaseVersionsAndChecksumsAreRejected`、`releaseJsonParsesMetadataAndDropsMalformedAssets`、
 * `releaseJsonRequiresTagName`、`githubUrlsAreRestrictedToHttpsGithubHosts`），
 * 并补上本仓库自己的节流/去重规则。
 */
class UpdateCheckTest {
    @Test
    fun versionParsingFollowsSemVerPrecedence() {
        // 前缀 v/V、大小写、首尾空白都接受；build metadata 忽略
        assertEquals(
            ReleaseVersion(1, 2, 3, listOf("rc", "2")),
            parseReleaseVersionOrNull("  v1.2.3-rc.2+build.9 "),
        )
        assertEquals("1.2.3-rc.2", parseReleaseVersionOrNull("V1.2.3-rc.2").toString())
        assertEquals(ReleaseVersion(2, 0, 0), parseReleaseVersionOrNull("v2.0.0+android"))
        assertEquals(ReleaseVersion(1, 2, 3), parseReleaseVersionOrNull("1.2.3"))
        // SemVer 的预发布排序链（与 MCA 用例逐条一致）
        assertTrue(parseReleaseVersionOrNull("1.0.0-alpha")!! < parseReleaseVersionOrNull("1.0.0-alpha.1")!!)
        assertTrue(parseReleaseVersionOrNull("1.0.0-alpha.1")!! < parseReleaseVersionOrNull("1.0.0-alpha.beta")!!)
        assertTrue(parseReleaseVersionOrNull("1.0.0-alpha.beta")!! < parseReleaseVersionOrNull("1.0.0-beta")!!)
        assertTrue(parseReleaseVersionOrNull("1.0.0-beta")!! < parseReleaseVersionOrNull("1.0.0-beta.2")!!)
        assertTrue(parseReleaseVersionOrNull("1.0.0-beta.2")!! < parseReleaseVersionOrNull("1.0.0-beta.11")!!)
        assertTrue(parseReleaseVersionOrNull("1.0.0-beta.11")!! < parseReleaseVersionOrNull("1.0.0")!!)
    }

    @Test
    fun invalidVersionsAreRejected() {
        for (raw in listOf(null, "", "   ", "1.2", "1.2.3.4", "abc", "v1.2.3-", "1.2.3-rc..1")) {
            assertNull("应判非法：<$raw>", parseReleaseVersionOrNull(raw))
        }
    }

    @Test
    fun compareVersionsFacadeMatchesOrdering() {
        // 相等
        assertEquals(0, compareVersions("1.2.3", "v1.2.3"))
        assertEquals(0, compareVersions("1.2.3", "1.2.3+build.1"))
        // latest 更高 → 负数
        assertTrue("1.2.3 < 1.3.0", compareVersions("1.2.3", "1.3.0") < 0)
        assertTrue("1.2.3 < 2.0.0", compareVersions("1.2.3", "2.0.0") < 0)
        // current 更高 → 正数
        assertTrue("1.3.0 > 1.2.3", compareVersions("1.3.0", "1.2.3") > 0)
        // 预发布低于正式版
        assertTrue(compareVersions("1.0.0", "1.0.0-rc.1") > 0)
        // 非法输入不制造假更新：返回 0
        assertEquals(0, compareVersions("", "1.2.3"))
        assertEquals(0, compareVersions("1.2.3", "abc"))
        assertFalse(isUpdateAvailable("1.2.3", "abc"))
        assertTrue(isUpdateAvailable("1.2.3", "1.3.0"))
        assertFalse(isUpdateAvailable("1.3.0", "1.2.3"))
        assertFalse(isUpdateAvailable("1.2.3", "1.2.3"))
    }

    @Test
    fun releaseJsonParsesMetadataAndDropsMalformedAssets() {
        val release = parseGitHubReleaseJson(
            """
            {"tag_name":"v1.3.0","name":"Aether Cockpit 1.3.0",
             "html_url":"https://github.com/lyydfys/aether-cockpit/releases/tag/v1.3.0",
             "body":"## 变更\n- 修好了 X",
             "published_at":"2026-11-01T00:00:00Z","draft":false,"prerelease":false,
             "assets":[{"name":"app-release.apk","browser_download_url":"https://github.com/x/y.apk","size":123},
                       {"name":"","browser_download_url":"https://github.com/x/z","size":1},
                       {"name":"notes.txt","browser_download_url":""}]}
            """.trimIndent(),
        )
        assertEquals("v1.3.0", release.tagName)
        assertEquals("Aether Cockpit 1.3.0", release.name)
        assertEquals(1, release.assets.size)
        assertEquals("app-release.apk", release.assets[0].name)
        assertEquals(123L, release.assets[0].sizeBytes)
        assertFalse(release.draft)
        assertFalse(release.prerelease)
        // tag 缺失 → 明确报错（invalid_response），与 MCA 的 require 一致
        val error = runCatching { parseGitHubReleaseJson("""{"name":"x"}""") }.exceptionOrNull()
        assertTrue("缺 tag_name 应抛 ManagementApiException", error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
        // 坏 JSON 同理
        for (body in listOf("not json", "", "<html>404</html>")) {
            val e = runCatching { parseGitHubReleaseJson(body) }.exceptionOrNull()
            assertTrue("body=<$body>", e is ManagementApiException)
        }
    }

    @Test
    fun trustedUrlsAreHttpsGithubOnly() {
        for (url in listOf(
            "https://github.com/lyydfys/aether-cockpit/releases/tag/v1.3.0",
            "https://objects.githubusercontent.com/x",
            "https://api.github.com/repos/lyydfys/aether-cockpit/releases/latest",
        )) {
            assertTrue("应信任：$url", isTrustedGitHubUrl(url))
        }
        for (url in listOf(
            "http://github.com/x",
            "https://evil.example.com/x",
            "https://github.com@evil.example.com/x",
            "https://user:pass@github.com/x",
            "https://github.com:8443/x",
            "",
        )) {
            assertFalse("不该信任：$url", isTrustedGitHubUrl(url))
        }
    }

    @Test
    fun resolveOnlyAnnouncesNewerStableRelease() {
        val release = parseGitHubReleaseJson(
            """{"tag_name":"v1.3.0","html_url":"https://github.com/lyydfys/aether-cockpit/releases/tag/v1.3.0"}""",
        )
        val newer = resolveAppUpdate(release, "1.2.3", autoCheckEnabled = true, lastCheckedAtMillis = 1L)
        assertEquals(AppUpdateStatus.AVAILABLE, newer.state.status)
        assertEquals("1.3.0", newer.state.latestVersionName)
        assertEquals(null, newer.state.message)
        assertEquals(ReleaseVersion(1, 3, 0), newer.availableVersion)

        // 同版本 → 已是最新（不提示）
        val same = resolveAppUpdate(release, "1.3.0", autoCheckEnabled = true, lastCheckedAtMillis = 1L)
        assertEquals(AppUpdateStatus.UP_TO_DATE, same.state.status)
        assertNull(same.availableVersion)

        // 预发布不算稳定版，即使版本更高
        val pre = resolveAppUpdate(
            parseGitHubReleaseJson("""{"tag_name":"v2.0.0-rc.1"}"""),
            "1.2.3",
            autoCheckEnabled = true,
            lastCheckedAtMillis = 1L,
        )
        assertEquals(AppUpdateStatus.UP_TO_DATE, pre.state.status)
        assertEquals("当前最新发布不是正式稳定版。", pre.state.message)
        assertNull(pre.availableVersion)

        // 版本号无法识别 → ERROR，并说明原因
        val broken = resolveAppUpdate(
            parseGitHubReleaseJson("""{"tag_name":"nightly"}"""),
            "1.2.3",
            autoCheckEnabled = true,
            lastCheckedAtMillis = 1L,
        )
        assertEquals(AppUpdateStatus.ERROR, broken.state.status)
        assertEquals("GitHub Release 版本号无法识别。", broken.state.message)

        // 不可信 html_url 回退到固定 Release 页
        val untrusted = resolveAppUpdate(
            parseGitHubReleaseJson("""{"tag_name":"v1.3.0","html_url":"https://evil.example.com/x"}"""),
            "1.2.3",
            autoCheckEnabled = true,
            lastCheckedAtMillis = 1L,
        )
        assertEquals(GITHUB_RELEASE_PAGE_URL, untrusted.state.releaseUrl)
    }

    @Test
    fun autoCheckIsThrottledLikeMca() {
        // now 必须大于节流窗口：用 1_000_000 会让 now - 24h 变成负数，
        // 被实现当成"从未检查过"（0 是哨兵值），测试自己把断言跑歪。
        val now = 1_000L * 24L * 60L * 60L * 1000L
        // 从未检查过 → 该查
        assertTrue(shouldAutoCheckAppUpdate(now, 0L, 0L))
        // 刚查过（24h 内）→ 不查
        assertFalse(shouldAutoCheckAppUpdate(now, now - APP_UPDATE_CHECK_INTERVAL_MS / 2, 0L))
        // 超过 24h → 该查
        assertTrue(shouldAutoCheckAppUpdate(now, now - APP_UPDATE_CHECK_INTERVAL_MS - 1, 0L))
        // 15 分钟内失败过 → 不查（即使已过 24h）
        assertFalse(
            shouldAutoCheckAppUpdate(
                nowMillis = now,
                lastCheckedAtMillis = now - APP_UPDATE_CHECK_INTERVAL_MS - 1,
                lastFailureAtMillis = now - APP_UPDATE_FAILURE_BACKOFF_MS / 2,
            ),
        )
        // 失败超过 15 分钟 → 该查
        assertTrue(
            shouldAutoCheckAppUpdate(
                nowMillis = now,
                lastCheckedAtMillis = now - APP_UPDATE_CHECK_INTERVAL_MS - 1,
                lastFailureAtMillis = now - APP_UPDATE_FAILURE_BACKOFF_MS - 1,
            ),
        )
        // 关掉自动检查 → 永远不查
        assertFalse(shouldAutoCheckAppUpdate(now, 0L, 0L, autoCheckEnabled = false))
    }

    @Test
    fun sameVersionIsAnnouncedOnlyOnce() {
        assertTrue(shouldAnnounceUpdate("1.3.0", null))
        assertTrue(shouldAnnounceUpdate("1.3.0", "1.2.0"))
        assertFalse(shouldAnnounceUpdate("1.3.0", "1.3.0"))
        assertFalse(shouldAnnounceUpdate(" 1.3.0 ", "1.3.0"))
        assertFalse(shouldAnnounceUpdate(null, null))
        assertFalse(shouldAnnounceUpdate("", "1.3.0"))
    }
}
