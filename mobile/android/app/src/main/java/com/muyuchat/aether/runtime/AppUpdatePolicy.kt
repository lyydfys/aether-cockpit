package com.muyuchat.aether.runtime

/**
 * 应用更新检查的本地策略与状态（移植自 MCA 的 `AppUpdateOwner.kt` + `AppUpdateState`，
 * **去掉下载/安装**：本仓库只做检查与提示）。
 *
 * 去重与节流（与 MCA 同值）：
 * - 自动检查最多 24 小时一次（[APP_UPDATE_CHECK_INTERVAL_MS]）；
 * - 失败后 15 分钟内不再自动重试（[APP_UPDATE_FAILURE_BACKOFF_MS]）；
 * - ETag 命中 304 时用本机缓存，不重复拉整包。
 */
internal const val APP_UPDATE_PREFERENCES = "aether_app_updates_v1"
internal const val APP_UPDATE_AUTO_CHECK_KEY = "auto_check_enabled"
internal const val APP_UPDATE_LAST_CHECKED_KEY = "last_checked_at"
internal const val APP_UPDATE_LAST_FAILURE_KEY = "last_failure_at"
internal const val APP_UPDATE_ETAG_KEY = "etag"
internal const val APP_UPDATE_RELEASE_JSON_KEY = "release_json"
internal const val APP_UPDATE_DISMISSED_VERSION_KEY = "dismissed_update_version"
internal const val APP_UPDATE_CHECK_INTERVAL_MS = 24L * 60L * 60L * 1000L
internal const val APP_UPDATE_FAILURE_BACKOFF_MS = 15L * 60L * 1000L

/** 关于页 Star 卡的"已关闭"标记（关掉后不再出现）。 */
internal const val APP_STAR_DISMISSED_KEY = "about_star_dismissed"

/**
 * 关于页展示的开源许可名。
 *
 * 与 `ui/SettingsAboutSection.kt` 的 `AppLicenseName` 同值——runtime 不反向依赖 ui，
 * 所以这里独立一份；两处都由测试钉住（改一处必须同步另一处）。
 */
internal const val APP_LICENSE_NAME = "Apache License 2.0"

/** 更新状态（MCA 的 8 态里只留"检查"相关的 5 个）。 */
enum class AppUpdateStatus {
    IDLE,
    CHECKING,
    UP_TO_DATE,
    AVAILABLE,
    ERROR,
}

/** 关于页要展示的更新状态（字段与 MCA `AppUpdateState` 同名同义）。 */
data class AppUpdateState(
    val currentVersionName: String = "",
    val currentVersionCode: Long = 0L,
    val autoCheckEnabled: Boolean = true,
    val status: AppUpdateStatus = AppUpdateStatus.IDLE,
    val latestVersionName: String? = null,
    val latestTitle: String? = null,
    val releaseNotes: String = "",
    val releaseUrl: String = GITHUB_RELEASE_PAGE_URL,
    val lastCheckedAtMillis: Long = 0L,
    val message: String? = null,
    /** 上一版本的 versionCode（取不到为 null → 对应行整行不显示）。 */
    val previousVersionCode: String? = null,
    /** 适用设备描述（例如 `Android arm64-v8a, minSdk 26`）。 */
    val applicableDevices: String? = null,
    /** 许可证名（例如 `Apache-2.0`）。 */
    val license: String? = null,
    /** 当前平台匹配资产的字节数；无匹配资产为 null → 显示「未找到本平台安装包」。 */
    val assetsBytes: Long? = null,
    /** release 的发布时间（ISO8601 原文）；取不到为 null。 */
    val publishedAt: String? = null,
)

/** 一次检查的解析结果：状态 + 是否有可展示的新版。 */
internal data class AppUpdateResolution(
    val state: AppUpdateState,
    /** 非空表示「有正式稳定版且比当前新」。 */
    val availableVersion: ReleaseVersion?,
)

/**
 * 自动检查该不该跑（纯函数）：
 * - 关掉自动检查 → 不跑；
 * - 24h 内已经查过 → 不跑；
 * - 15 分钟前刚失败过 → 不跑（避免离线时反复撞墙）。
 */
internal fun shouldAutoCheckAppUpdate(
    nowMillis: Long,
    lastCheckedAtMillis: Long,
    lastFailureAtMillis: Long,
    autoCheckEnabled: Boolean = true,
): Boolean {
    if (!autoCheckEnabled) return false
    if (lastCheckedAtMillis > 0L && nowMillis - lastCheckedAtMillis < APP_UPDATE_CHECK_INTERVAL_MS) {
        return false
    }
    if (lastFailureAtMillis > 0L && nowMillis - lastFailureAtMillis < APP_UPDATE_FAILURE_BACKOFF_MS) {
        return false
    }
    return true
}

/**
 * 解析 release → 状态（文案逐字来自 MCA `AppUpdateOwner.resolveAppUpdate`）。
 *
 * 只有**正式稳定版**、且版本严格高于当前版本，才算「有新版」；草稿/预发布/版本号无法识别
 * 都不算，并给出与 MCA 相同的说明（不静默吞掉）。
 */
/**
 * 资产字节数 → 人类可读（MCA 关于页用 1 位小数 + 单位）。
 *
 * 例：`501,000,000 → 477.8 MB`、`1,200 → 1.2 KB`。0 或负数返回空串（调用方整行不显示）。
 */
internal fun formatUpdateAssetSize(bytes: Long): String {
    if (bytes <= 0L) return ""
    val kb = bytes / 1024.0
    val mb = kb / 1024.0
    val gb = mb / 1024.0
    return when {
        gb >= 1.0 -> String.format(java.util.Locale.US, "%.1f GB", gb)
        mb >= 1.0 -> String.format(java.util.Locale.US, "%.1f MB", mb)
        else -> String.format(java.util.Locale.US, "%.1f KB", kb)
    }
}

/**
 * 适用设备描述（用于关于页元信息行）。
 *
 * 输出是**逗号分隔**的 `Android, <首选 ABI>, minSdk <SDK_INT>`：ABI 取
 * `Build.SUPPORTED_ABIS` 的第一项，取不到就只写 `Android`，minSdk 缺失则整段不出现
 * （不会出现 `minSdk 0` 或 `null`）——**宁可短，不要瞎编**。
 */
internal fun buildApplicableDevicesLabel(
    abis: List<String> = emptyList(),
    sdkInt: Int = 0,
): String = buildList {
    add("Android")
    abis.firstOrNull()?.takeIf { it.isNotBlank() }?.let { add(it) }
    if (sdkInt > 0) add("minSdk $sdkInt")
}.joinToString(", ")

internal fun resolveAppUpdate(
    release: GitHubRelease,
    currentVersionName: String,
    autoCheckEnabled: Boolean,
    lastCheckedAtMillis: Long,
): AppUpdateResolution {
    val version = parseReleaseVersionOrNull(release.tagName)
    val current = parseReleaseVersionOrNull(currentVersionName) ?: ReleaseVersion(0, 0, 0)
    val newer = version != null && version > current
    val notStable = release.draft || release.prerelease || version?.preRelease?.isNotEmpty() == true
    val status = when {
        notStable -> AppUpdateStatus.UP_TO_DATE
        version == null -> AppUpdateStatus.ERROR
        newer -> AppUpdateStatus.AVAILABLE
        else -> AppUpdateStatus.UP_TO_DATE
    }
    val releaseUrl = release.htmlUrl
        .takeIf(::isTrustedGitHubUrl)
        ?: GITHUB_RELEASE_PAGE_URL
    val message = when {
        status == AppUpdateStatus.ERROR -> "GitHub Release 版本号无法识别。"
        notStable -> "当前最新发布不是正式稳定版。"
        else -> null
    }
    return AppUpdateResolution(
        state = AppUpdateState(
            currentVersionName = currentVersionName,
            autoCheckEnabled = autoCheckEnabled,
            status = status,
            latestVersionName = version?.toString() ?: release.tagName,
            latestTitle = release.name,
            releaseNotes = release.body,
            releaseUrl = releaseUrl,
            lastCheckedAtMillis = lastCheckedAtMillis,
            message = message,
            // 以下四项来自 release 资产 / 构建信息；取不到就保持 null（UI 整行不显示）。
            applicableDevices = buildApplicableDevicesLabel(),
            license = APP_LICENSE_NAME,
            assetsBytes = choosePlatformAsset(release.assets, UpdatePlatform.ANDROID)?.sizeBytes,
            publishedAt = release.publishedAt.takeIf { it.isNotBlank() },
        ),
        availableVersion = if (status == AppUpdateStatus.AVAILABLE) version else null,
    )
}

/**
 * 同一版本只提醒一次（MCA 用内存里的 `dismissedUpdateVersion`；这里落盘，
 * 这样重开 App 也不会再弹同一个版本 —— 这是本次唯一的"加固点"）。
 */
internal fun shouldAnnounceUpdate(
    latestVersionName: String?,
    dismissedVersion: String?,
): Boolean {
    val latest = latestVersionName?.trim().orEmpty()
    if (latest.isEmpty()) return false
    return latest != dismissedVersion?.trim().orEmpty()
}
