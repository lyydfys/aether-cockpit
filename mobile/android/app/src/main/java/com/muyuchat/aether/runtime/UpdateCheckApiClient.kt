package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.util.Locale

/**
 * GitHub Release 更新检查（逻辑与文案移植自 MCA 的 `GitHubReleaseUpdate.kt`，去掉了
 * APK 下载/安装那一半：本仓库只做"检查 + 提示 + 打开 Release 页"）。
 *
 * 端点固定为官方仓库的 `releases/latest`；请求头与 MCA 相同：
 * `Accept: application/vnd.github+json`、`X-GitHub-Api-Version: 2022-11-28`、带 `User-Agent`。
 */
internal const val GITHUB_RELEASE_API_URL =
    "https://api.github.com/repos/lyydfys/aether-cockpit/releases/latest"
internal const val GITHUB_RELEASE_PAGE_URL =
    "https://github.com/lyydfys/aether-cockpit/releases/latest"
internal const val GITHUB_RELEASE_REPOSITORY = "lyydfys/aether-cockpit"
internal const val GITHUB_REPO_PAGE_URL = "https://github.com/lyydfys/aether-cockpit"
internal const val GITHUB_ISSUES_URL = "https://github.com/lyydfys/aether-cockpit/issues"
/** 「赞助支持」格子的落点：GitHub Sponsors 主页（不是仓库页）。 */
internal const val GITHUB_SPONSORS_URL = "https://github.com/sponsors/lyydfys"
internal const val GITHUB_LICENSE_URL =
    "https://github.com/lyydfys/aether-cockpit/blob/main/LICENSE"
internal const val GITHUB_PRIVACY_URL =
    "https://github.com/lyydfys/aether-cockpit/blob/main/PRIVACY.md"
internal const val APP_UPDATE_USER_AGENT = "Aether-Cockpit-Android"

/** 当前安装版本（版本名 + 版本号）。 */
internal data class CurrentAppVersion(
    val versionName: String,
    val versionCode: Long,
)

internal data class GitHubReleaseAsset(
    val name: String,
    val downloadUrl: String,
    val sizeBytes: Long,
)

internal data class GitHubRelease(
    val tagName: String,
    val name: String,
    val htmlUrl: String,
    val body: String,
    val publishedAt: String,
    val draft: Boolean,
    val prerelease: Boolean,
    val assets: List<GitHubReleaseAsset>,
)

/** 语义化版本（`major.minor.patch` + 预发布段），可比较。 */
internal data class ReleaseVersion(
    val major: Long,
    val minor: Long,
    val patch: Long,
    val preRelease: List<String> = emptyList(),
) : Comparable<ReleaseVersion> {
    override fun compareTo(other: ReleaseVersion): Int {
        compareValuesBy(this, other, ReleaseVersion::major, ReleaseVersion::minor, ReleaseVersion::patch)
            .takeIf { it != 0 }
            ?.let { return it }
        if (preRelease.isEmpty() && other.preRelease.isEmpty()) return 0
        if (preRelease.isEmpty()) return 1
        if (other.preRelease.isEmpty()) return -1
        val length = maxOf(preRelease.size, other.preRelease.size)
        for (index in 0 until length) {
            val left = preRelease.getOrNull(index) ?: return -1
            val right = other.preRelease.getOrNull(index) ?: return 1
            if (left == right) continue
            val leftNumber = left.toLongOrNull()
            val rightNumber = right.toLongOrNull()
            return when {
                leftNumber != null && rightNumber != null -> leftNumber.compareTo(rightNumber)
                leftNumber != null -> -1
                rightNumber != null -> 1
                else -> left.compareTo(right)
            }
        }
        return 0
    }

    override fun toString(): String = buildString {
        append(major).append('.').append(minor).append('.').append(patch)
        if (preRelease.isNotEmpty()) append('-').append(preRelease.joinToString("."))
    }
}

/** 解析语义化版本；非法输入返回 `null`（调用方据此判「版本号无法识别」）。 */
internal fun parseReleaseVersionOrNull(raw: String?): ReleaseVersion? {
    val value = raw?.trim().orEmpty()
    val match = Regex(
        "^[vV]?(\\d+)\\.(\\d+)\\.(\\d+)(?:-([0-9A-Za-z.-]+))?(?:\\+[0-9A-Za-z.-]+)?$",
    ).matchEntire(value) ?: return null
    val preRelease = match.groupValues[4]
        .takeIf { it.isNotBlank() }
        ?.split('.')
        .orEmpty()
    if (preRelease.any { it.isBlank() }) return null
    return ReleaseVersion(
        major = match.groupValues[1].toLongOrNull() ?: return null,
        minor = match.groupValues[2].toLongOrNull() ?: return null,
        patch = match.groupValues[3].toLongOrNull() ?: return null,
        preRelease = preRelease,
    )
}

/**
 * 版本比较纯函数（测试友好的门面）：**`<0` 表示 current 更低（有新版）**。
 *
 * 两边任一解析失败时按"无法比较"处理，返回 0（不制造假更新）。
 */
internal fun compareVersions(current: String, latest: String): Int {
    val a = parseReleaseVersionOrNull(current) ?: return 0
    val b = parseReleaseVersionOrNull(latest) ?: return 0
    return a.compareTo(b)
}

/** 是否有新版：`latest` 严格大于 `current`，且两边都是合法版本。 */
internal fun isUpdateAvailable(current: String, latest: String): Boolean {
    val a = parseReleaseVersionOrNull(current) ?: return false
    val b = parseReleaseVersionOrNull(latest) ?: return false
    return b > a
}

/** 解析 `releases/latest`；缺 `tag_name` 直接抛（与 MCA 的 `require` 一致）。 */
internal fun parseGitHubReleaseJson(raw: String): GitHubRelease {
    val root = try {
        JSONObject(raw)
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "GitHub release 返回的数据格式不正确")
    }
    val tagName = root.optString("tag_name").trim()
    if (tagName.isBlank()) {
        throw ManagementApiException("invalid_response", "GitHub Release 缺少 tag_name。")
    }
    val assetsJson = root.optJSONArray("assets") ?: JSONArray()
    val assets = buildList {
        for (index in 0 until assetsJson.length()) {
            val asset = assetsJson.optJSONObject(index) ?: continue
            val name = asset.optString("name").trim()
            val url = asset.optString("browser_download_url").trim()
            if (name.isBlank() || url.isBlank()) continue
            add(
                GitHubReleaseAsset(
                    name = name,
                    downloadUrl = url,
                    sizeBytes = asset.optLong("size", 0L).coerceAtLeast(0L),
                ),
            )
        }
    }
    return GitHubRelease(
        tagName = tagName,
        name = root.optString("name", tagName).trim().ifBlank { tagName },
        htmlUrl = root.optString("html_url", GITHUB_RELEASE_PAGE_URL).trim(),
        body = root.optString("body", ""),
        publishedAt = root.optString("published_at", ""),
        draft = root.optBoolean("draft", false),
        prerelease = root.optBoolean("prerelease", false),
        assets = assets,
    )
}

/* ==========================================================================
 * 更新资产按平台匹配（Release assets[] → 本平台安装包）
 *
 * 规则（用户要求「手机推手机、桌面推桌面」）：
 *  - Android：优先 `arm64-v8a`，其次 `universal`，再次任意 `.apk`；
 *  - Windows：`.exe` / `.msi`（名字里带 windows/win/setup 更优先）；
 *  - macOS：`.dmg` / `.zip` 且名字带 macos/darwin/mac；
 *  - Linux：`.AppImage` / `.deb` / `.rpm` / `.tar.gz` 且名字带 linux。
 * 都匹配不到时返回 null，调用方回退到 Release 页面并明确提示。
 * ========================================================================== */

/** 更新包所属平台。 */
enum class UpdatePlatform {
    ANDROID,
    WINDOWS,
    MACOS,
    LINUX,
    UNKNOWN,
}

/** 资产名里出现这些片段的优先级打分（分数越高越优先）。 */
internal fun assetPriority(name: String, platform: UpdatePlatform): Int {
    val n = name.lowercase(Locale.US)
    return when (platform) {
        UpdatePlatform.ANDROID -> when {
            !n.endsWith(".apk") -> -1
            n.contains("arm64-v8a") -> 100
            n.contains("arm64") -> 90
            n.contains("universal") -> 80
            n.contains("armeabi-v7a") -> 60
            else -> 40
        }
        UpdatePlatform.WINDOWS -> when {
            !(n.endsWith(".exe") || n.endsWith(".msi") || n.endsWith(".zip")) -> -1
            n.contains("windows") || n.contains("win64") || n.contains("win") -> 70
            n.contains("setup") || n.contains("installer") -> 60
            else -> 30
        }
        UpdatePlatform.MACOS -> when {
            !(n.endsWith(".dmg") || n.endsWith(".zip") || n.endsWith(".pkg")) -> -1
            n.contains("macos") || n.contains("darwin") || n.contains("mac") -> 70
            n.contains("universal") -> 50
            else -> 30
        }
        UpdatePlatform.LINUX -> when {
            !(n.endsWith(".appimage") || n.endsWith(".deb") || n.endsWith(".rpm") || n.endsWith(".tar.gz")) -> -1
            n.contains("linux") -> 70
            else -> 30
        }
        UpdatePlatform.UNKNOWN -> -1
    }
}

/**
 * 从资产列表里挑出**当前平台**最合适的安装包；没有匹配项返回 null。
 *
 * 同分时取体积更大的（通常是完整包而不是调试/符号包），再同分取名字更短的（稳定命名）。
 */
internal fun choosePlatformAsset(
    assets: List<GitHubReleaseAsset>,
    platform: UpdatePlatform,
): GitHubReleaseAsset? {
    if (platform == UpdatePlatform.UNKNOWN) {
        return null
    }
    return assets
        .mapNotNull { asset ->
            val score = assetPriority(asset.name, platform)
            if (score < 0) null else Triple(asset, score, asset.sizeBytes)
        }
        .sortedWith(
            compareByDescending<Triple<GitHubReleaseAsset, Int, Long>> { it.second }
                .thenByDescending { it.third }
                .thenBy { it.first.name.length },
        )
        .firstOrNull()
        ?.first
}

/** 平台的中文名（用于按钮说明与状态行）。 */
internal fun updatePlatformLabel(platform: UpdatePlatform): String = when (platform) {
    UpdatePlatform.ANDROID -> "Android"
    UpdatePlatform.WINDOWS -> "Windows"
    UpdatePlatform.MACOS -> "macOS"
    UpdatePlatform.LINUX -> "Linux"
    UpdatePlatform.UNKNOWN -> "未知平台"
}

/**
 * 「去下载」按钮的落点。
 *
 * 有本平台资产 → 直接下安装包；没有 → 打开 Release 页面。
 */
internal data class UpdateDownloadTarget(
    val url: String,
    val isDirectAsset: Boolean,
    val note: String?,
)

internal fun updateDownloadTarget(
    release: GitHubRelease?,
    platform: UpdatePlatform,
): UpdateDownloadTarget? {
    if (release == null) {
        return null
    }
    val asset = choosePlatformAsset(release.assets, platform)
    return if (asset != null) {
        UpdateDownloadTarget(url = asset.downloadUrl, isDirectAsset = true, note = null)
    } else {
        UpdateDownloadTarget(
            url = release.htmlUrl.ifBlank { GITHUB_RELEASE_PAGE_URL },
            isDirectAsset = false,
            note = "未找到${updatePlatformLabel(platform)}安装包，将打开 Release 页面",
        )
    }
}

/**
 * 更新状态行文案：把"有没有本平台包"说清楚。
 *
 * 例：「发现 v1.2.3（含 Android 安装包）」/「发现 v1.2.3（本平台无安装包）」。
 */
internal fun updateAvailableLabel(version: String, hasPlatformAsset: Boolean): String =
    "发现 v$version（${if (hasPlatformAsset) "含本平台安装包" else "本平台无安装包"}）"

/** 只信任 GitHub 的 HTTPS 地址（与 MCA `isTrustedGitHubDownloadUrl` 同一份白名单）。 */
internal fun isTrustedGitHubUrl(raw: String): Boolean {    val uri = runCatching { URI(raw) }.getOrNull() ?: return false
    if (!uri.scheme.equals("https", ignoreCase = true)) return false
    if (!uri.userInfo.isNullOrBlank() || (uri.port != -1 && uri.port != 443)) return false
    val host = uri.host?.lowercase(Locale.US) ?: return false
    return host == "github.com" ||
        host == "objects.githubusercontent.com" ||
        host == "release-assets.githubusercontent.com" ||
        host == "github-releases.githubusercontent.com" ||
        host == "api.github.com"
}

/** GitHub 拉取结果：带 ETag 时服务端可能回 304，此时用本机缓存的 JSON。 */
internal sealed interface GitHubReleaseFetchResult {
    data class Fetched(
        val release: GitHubRelease,
        val rawJson: String,
        val etag: String?,
    ) : GitHubReleaseFetchResult

    data class NotModified(val etag: String?) : GitHubReleaseFetchResult
}

/**
 * GitHub 客户端（`HttpURLConnection` 版，本仓库没有 OkHttp 依赖）。
 *
 * 与 MCA 的差异：无 `OkHttp` 拦截器做重定向白名单校验——改为在拿到响应后校验体积，
 * 并只把固定端点写死在常量里（端点不可替换）。
 */
internal class GitHubReleaseClient(
    private val connectTimeoutMs: Int = 20_000,
    private val readTimeoutMs: Int = 30_000,
    private val endpoint: String = GITHUB_RELEASE_API_URL,
) {
    fun fetchLatest(etag: String? = null): GitHubReleaseFetchResult {
        require(endpoint == GITHUB_RELEASE_API_URL) {
            "GitHub Release endpoint is fixed to the official repository."
        }
        val connection = (URL(endpoint).openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/vnd.github+json")
        connection.setRequestProperty("X-GitHub-Api-Version", "2022-11-28")
        connection.setRequestProperty("User-Agent", APP_UPDATE_USER_AGENT)
        if (!etag.isNullOrBlank()) {
            connection.setRequestProperty("If-None-Match", etag)
        }
        try {
            val code = connection.responseCode
            if (code == HttpURLConnection.HTTP_NOT_MODIFIED) {
                return GitHubReleaseFetchResult.NotModified(connection.getHeaderField("ETag") ?: etag)
            }
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException(
                    "github_$code",
                    githubErrorMessage(code).take(160),
                )
            }
            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use {
                readLimited(it, MAX_RELEASE_JSON_BYTES)
            }
            return GitHubReleaseFetchResult.Fetched(
                release = parseGitHubReleaseJson(body),
                rawJson = body,
                etag = connection.getHeaderField("ETag"),
            )
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接 GitHub")
        } finally {
            connection.disconnect()
        }
    }

    private fun githubErrorMessage(code: Int): String = when (code) {
        404 -> "GitHub 更新检查失败：HTTP 404（仓库或 release 不存在）"
        403 -> "GitHub 更新检查失败：HTTP 403（可能是限流）"
        else -> "GitHub 更新检查失败：HTTP $code"
    }

    private fun readLimited(reader: Reader, limit: Long): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val n = reader.read(buffer)
            if (n < 0) {
                return out.toString()
            }
            out.append(buffer, 0, n)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "GitHub 更新响应过大。")
            }
        }
    }

    private companion object {
        const val MAX_RELEASE_JSON_BYTES = 1024L * 1024L
    }
}
