package com.muyuchat.aether.ui

import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.os.Build
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.APP_STAR_DISMISSED_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_AUTO_CHECK_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_DISMISSED_VERSION_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_ETAG_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_LAST_CHECKED_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_LAST_FAILURE_KEY
import com.muyuchat.aether.runtime.APP_UPDATE_PREFERENCES
import com.muyuchat.aether.runtime.APP_UPDATE_RELEASE_JSON_KEY
import com.muyuchat.aether.runtime.AppUpdateState
import com.muyuchat.aether.runtime.AppUpdateStatus
import com.muyuchat.aether.runtime.GITHUB_RELEASE_PAGE_URL
import com.muyuchat.aether.runtime.GITHUB_ISSUES_URL
import com.muyuchat.aether.runtime.GITHUB_LICENSE_URL
import com.muyuchat.aether.runtime.GITHUB_PRIVACY_URL
import com.muyuchat.aether.runtime.GITHUB_REPO_PAGE_URL
import com.muyuchat.aether.runtime.GITHUB_SPONSORS_URL
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.GitHubReleaseClient
import com.muyuchat.aether.runtime.GitHubReleaseFetchResult
import com.muyuchat.aether.runtime.parseGitHubReleaseJson
import com.muyuchat.aether.runtime.resolveAppUpdate
import com.muyuchat.aether.runtime.shouldAnnounceUpdate
import com.muyuchat.aether.runtime.shouldAutoCheckAppUpdate
import com.muyuchat.aether.runtime.formatUpdateAssetSize
import com.muyuchat.aether.runtime.ApkInstaller
import com.muyuchat.aether.runtime.GitHubRelease
import com.muyuchat.aether.runtime.GitHubReleaseAsset
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.UpdateDownloader
import com.muyuchat.aether.runtime.UpdateInstallPhase
import com.muyuchat.aether.runtime.cachedApkMatches
import com.muyuchat.aether.runtime.chooseAndroidApkAsset
import com.muyuchat.aether.runtime.findSha256Asset
import com.muyuchat.aether.runtime.updateDownloadErrorText
import com.muyuchat.aether.runtime.updateDownloadProgressText
import com.muyuchat.aether.runtime.updateInstallActionLabel
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import java.io.File
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaIconPlate
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaPlateSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaSwitch
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaDialog
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Surface
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.role


/** 仓库根 LICENSE 核到的是 Apache License 2.0；不要在页面里另写别的许可名。 */
internal const val AppLicenseName: String = "Apache License 2.0"

/** 兼容旧文案常量（升级通道说明）。 */
internal const val AppUpdateChannelCopy: String = "GitHub Releases（手动检查 + 自动静默检查）"

/** 关于页顶部身份。 */
internal const val AppDisplayName: String = "Aether Cockpit"
internal const val AppTagline: String = "本机网关 + 多账号渠道管理"

/** 构建类型文案（debug/release）。 */
/**
 * 关于页视觉常量：**逐项抄自 MCA** `feature/settings/.../SettingsAboutScreen.kt`。
 *
 * 依据行号（E:\MAC 源码）：
 *  - :120 外层 padding horizontal 16 / vertical 12，:121 区间距 spacedBy(12)
 *  - :294 应用卡 shape RoundedCornerShape(12)，:299 padding horizontal 20 / vertical 22
 *  - :306 品牌标 size 64dp，:310 应用名 headlineSmall
 *  - :375 更新卡 shape RoundedCornerShape(8)，:378 padding 16，:379 区间距 spacedBy(10)
 *  - :489 链接卡 shape 12 / :494 padding horizontal 12 · vertical 16
 *  - :323 版本胶囊 shape RoundedCornerShape(50) + labelMedium + padding 10/6
 */
/**
 * 关于页四宫格的格子。
 *
 * MCA 的真实行为（`E:\MAC .../SettingsAboutScreen.kt:192-224`）：
 * 主作者 / 开源仓库 / 意见反馈都是 `onOpenWebPage(URL)`（**跳外部浏览器**），
 * 只有「赞助支持」是 `showSponsor = true`（App 内抽屉）。
 * 用户要求「不跳外部浏览器」，所以这里统一改成 **App 内弹窗**（链接 + 说明 + 复制）。
 */
internal enum class AboutTile { AUTHOR, REPOSITORY, SPONSOR, FEEDBACK }

/**
 * 弹窗主按钮的行为。
 *
 * - [OPEN]：在 App 内的浏览器窗口打开（Custom Tabs；不可用时回退 ACTION_VIEW）
 * - [SHARE]：走系统分享面板（`ACTION_SEND`）
 */
internal enum class AboutTilePrimaryAction { OPEN, SHARE }

/** 一格点击后要展示的内容（纯数据，可单测）。 */
internal data class AboutTileAction(
    val tile: AboutTile,
    val title: String,
    val body: String,
    /** 要展示/复制的链接；`null` 表示这一格没有链接（目前没有这种格子）。 */
    val link: String?,
    val linkHost: String?,
    /** 主按钮行为（四格目前都是 OPEN；分享走赞助弹窗里的独立按钮）。 */
    val primaryAction: AboutTilePrimaryAction = AboutTilePrimaryAction.OPEN,
) {
    /** 是否有可复制内容（弹窗据此决定是否渲染「复制链接」按钮）。 */
    val canCopy: Boolean get() = !link.isNullOrBlank()
}

/**
 * 四格各自的 App 内行为。
 *
 * 断言点（单测）：`link` 只允许 `https://github.com/...`，且**任何格子都不携带"打开浏览器"的语义**——
 * 页面层只弹 `McaDialog`，不会构造 `Intent.ACTION_VIEW`。
 */
/**
 * 在 App 内的浏览器窗口打开链接（`InAppBrowserPage`）。
 *
 * 只用 `ACTION_VIEW` 会整屏跳到浏览器 App、按返回不一定回到本 App；
 * 所以四宫格链接统一走 App 内 WebView 页（`LocalInAppBrowser`），不引入
 * `androidx.browser` 依赖。
 */
/** 系统分享面板：正文 = 项目名 + 仓库链接（MCA `shareProject` 同款）。 */
internal fun shareProjectText(): String = "$AppDisplayName\n$GITHUB_REPO_PAGE_URL"

internal fun shareProject(context: android.content.Context) {
    val send = android.content.Intent(android.content.Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(android.content.Intent.EXTRA_TEXT, shareProjectText())
    }
    val chooser = android.content.Intent.createChooser(send, "分享 $AppDisplayName").apply {
        addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    runCatching { context.startActivity(chooser) }
}

internal fun aboutTileAction(tile: AboutTile): AboutTileAction = when (tile) {
    AboutTile.AUTHOR -> AboutTileAction(
        tile = tile,
        title = "主作者",
        body = "Aether Cockpit 由 lyydfys 维护：一个本地优先的 AI 算力聚合网关。",
        link = "https://github.com/lyydfys",
        linkHost = "github.com",
    )
    AboutTile.REPOSITORY -> AboutTileAction(
        tile = tile,
        title = "开源仓库",
        body = "源码、发布包与完整文档都在 GitHub。欢迎 Star、提 Issue 或直接提 PR。",
        link = GITHUB_REPO_PAGE_URL,
        linkHost = "github.com",
    )
    AboutTile.SPONSOR -> AboutTileAction(
        tile = tile,
        title = "赞助支持",
        body = "项目目前靠个人时间维护。赞助用于覆盖构建机、签名证书与测试设备成本。",
        // 本项目 GitHub Sponsors 页面（MCA 对应 `onOpenDonatePage`）。
        link = GITHUB_SPONSORS_URL,
        linkHost = "github.com",
    )
    AboutTile.FEEDBACK -> AboutTileAction(
        tile = tile,
        title = "意见反馈",
        body = "遇到问题或有建议，请在仓库的 Issues 里描述复现步骤；我们会逐条看。",
        link = GITHUB_ISSUES_URL,
        linkHost = "github.com",
    )
}

/**
 * Star 教程的步骤文案（纯数据，可单测）。
 *
 * 要求「可执行且准确」：每条都是用户在 GitHub 页面上真能做的动作，
 * 不写"打开网页按提示操作"这类废话。
 */
internal fun aboutStarGuideSteps(repoUrl: String = GITHUB_REPO_PAGE_URL): List<String> = listOf(
    "有 GitHub 账号就先登录；没有账号的话，在 github.com 右上角 Sign up 注册一个（邮箱验证即可，免费）。",
    "打开本项目仓库页：$repoUrl",
    "在页面右上角的 Star / Starred 按钮上点一下——图标是一颗星；已经点过会显示 Starred，再点一次是取消。",
    "可选：点 Watch → Custom → 勾选 Releases，这样有新版本发布时会收到邮件提醒。",
)

/** 教程结尾那句（避免用户以为点 Star 会消耗什么）。 */
internal fun aboutStarGuideFooter(): String = "点 Star 不消耗任何资源，只是让项目被更多人看到。"

internal val AboutMcaSectionGap: Dp = 12.dp
internal val AboutMcaHeroPaddingH: Dp = 20.dp
internal val AboutMcaHeroPaddingV: Dp = 22.dp
internal val AboutMcaCardPadding: Dp = 16.dp
internal val AboutMcaCardGap: Dp = 10.dp
internal val AboutMcaHeroShapeDp: Dp = 12.dp
internal val AboutMcaCardShapeDp: Dp = 8.dp
internal val AboutMcaLogoSize: Dp = 64.dp
internal val AboutMcaPillPaddingH: Dp = 10.dp
internal val AboutMcaPillPaddingV: Dp = 6.dp

/** 版本号 pill 的圆角：按百分比 50 → 正圆端胶囊（MCA 样板 shape=50）。 */
internal val AboutMcaPillCornerPercent: Int = 50

/** 链接卡内边距：MCA 样板 padding 12/16（vertical 12 / horizontal 16）。 */
internal val AboutMcaLinkPaddingH: Dp = 16.dp
internal val AboutMcaLinkPaddingV: Dp = 12.dp

/**
 * 「赞赏码」展示尺寸：用户要求每张 140–160dp 见方，这里取 148dp。
 *
 * 为什么不取满 160dp：360dp 手机上弹窗正文可用宽度约 250–270dp，
 * 148dp 图 + 左右各 [AboutSponsorQrFramePadding] 外框 = 160dp 单张居中；
 * 两列排布需要 332dp 以上的可用宽度，只有平板/横屏/大窗才会走两列。
 * 图片本身按 `ContentScale.Fit` 等比缩放，不会被拉伸。
 */
internal val AboutSponsorQrImageSize: Dp = 148.dp

/** 二维码外框内边距：留出这段空隙后，外框圆角永远切不到码的边缘。 */
internal val AboutSponsorQrFramePadding: Dp = 6.dp

/** 两张二维码之间的间距（两列横排与单列竖排共用）。 */
internal val AboutSponsorQrGap: Dp = 12.dp

/** 走「两列」所需的最小可用宽度 =（图 + 左右外框）× 2 + 列间距。 */
internal val AboutSponsorQrTwoUpMinWidth: Dp =
    (AboutSponsorQrImageSize + AboutSponsorQrFramePadding * 2) * 2 + AboutSponsorQrGap

internal fun buildTypeLabel(debuggable: Boolean): String = if (debuggable) "debug" else "release"

internal fun aboutCopyrightLine(): String = "© 2026 Aether Cockpit · $AppLicenseName"

/**
 * 更新状态文案（移植 MCA `SettingsRuntimeScreen.kt` 的 `appUpdateStatusPresentation` 五个态）。
 */
internal fun appUpdateStatusLabel(status: AppUpdateStatus): String = when (status) {
    AppUpdateStatus.IDLE -> "尚未检查"
    AppUpdateStatus.CHECKING -> "检查中…"
    AppUpdateStatus.UP_TO_DATE -> "已是最新版本"
    AppUpdateStatus.AVAILABLE -> "有新版本"
    AppUpdateStatus.ERROR -> "检查失败"
}

/** Release notes 截断：MCA 是 1200 字符（`SettingsRuntimeScreen.kt:365-369`），同值。 */
internal fun aboutReleaseNotesText(notes: String, limit: Int = 1200): String {
    val value = stripMarkdownEmphasis(notes).trim()
    return if (value.length > limit) value.take(limit - 3).trimEnd() + "..." else value
}

/**
 * 去掉 Release notes 里的 Markdown 强调标记（`**粗体**`、`*斜体*`、`__粗体__`、`` `代码` ``）。
 *
 * 手机端不做完整 Markdown 渲染（成本高、收益低），但**绝不能把 `**` 原样显示给用户**。
 * 只处理成对的强调符；落单的 `*` 保持原样（那可能是列表符号或乘法）。
 */
internal fun stripMarkdownEmphasis(raw: String): String {
    if (raw.isEmpty()) return raw
    return raw
        .replace(Regex("\\*\\*(.+?)\\*\\*"), "$1")
        .replace(Regex("__(.+?)__"), "$1")
        .replace(Regex("`([^`]+)`"), "$1")
        .replace(Regex("(?<!\\*)\\*([^*\\n]+)\\*(?!\\*)"), "$1")
}

/** 「上次检查」的相对时间。 */
internal fun aboutCheckedAtLabel(lastCheckedAtMillis: Long, nowMillis: Long): String {
    if (lastCheckedAtMillis <= 0L) return "尚未检查"
    val delta = nowMillis - lastCheckedAtMillis
    if (delta < 0L) return "刚刚"
    val minutes = delta / 60_000L
    return when {
        minutes < 1L -> "刚刚"
        minutes < 60L -> "$minutes 分钟前"
        minutes < 24L * 60L -> "${minutes / 60L} 小时前"
        else -> "${minutes / (24L * 60L)} 天前"
    }
}

/**
 * 关于页字段（版本/运行环境/网关版本）的纯格式化，单测不依赖 Android 运行时。
 *
 * `version` / `model` / `abis` 都可能因系统裁剪为空，空值统一显示「—」，不抛异常。
 */
internal fun aboutVersionLabel(versionName: String?, versionCode: Long?): String {
    val name = versionName?.trim().orEmpty().ifBlank { "—" }
    val code = versionCode?.takeIf { it >= 0 }?.toString() ?: "—"
    return "$name ($code)"
}

internal fun aboutRuntimeLabel(release: String?, sdkInt: Int?, model: String?): String {
    val releaseText = release?.trim().orEmpty().ifBlank { "—" }
    val sdkText = sdkInt?.takeIf { it > 0 }?.toString() ?: "—"
    val modelText = model?.trim().orEmpty().ifBlank { "—" }
    return "Android $releaseText (API $sdkText) · $modelText"
}

internal fun aboutAbiLabel(abis: List<String>?): String =
    abis?.map { it.trim() }?.filter { it.isNotBlank() }?.joinToString(", ")
        ?.ifBlank { null }
        ?: "—"

/** GatewayStatus 目前没有版本字段；有值才显示，没有就如实写「—」。 */
internal fun aboutGatewayVersionLabel(version: String?): String =
    version?.trim().orEmpty().ifBlank { "—" }

/** 网关版本为空时，页面必须说明为什么是「—」，不能留一个像加载失败的符号。 */
internal fun aboutGatewayVersionNote(version: String?): String? =
    if (version.isNullOrBlank()) "网关未启动或版本未上报" else null

/**
 * 网关版本是可选能力：当前 [GatewayStatus] 没有版本字段，统一返回 null，
 * 页面显示「—」并注明原因。以后网关上报版本时只改这一个函数。
 */
@Suppress("UNUSED_PARAMETER")
internal fun gatewayVersionOf(status: GatewayStatus): String? = null

/**
 * 「关于」区块：1:1 参考 MCA 的关于页结构（`feature/settings/SettingsRuntimeScreen.kt` 的
 * `AppUpdateCard` + 设置页条目范式），更新检查逻辑与文案照搬 MCA `AppUpdateOwner.kt`：
 *
 * - 顶部：应用图标 + 名称 + 版本 + 一行简介；
 * - 中部：应用更新（自动检查开关、当前/最新版本、状态、上次检查、检查更新、Release 摘要）；
 * - 关于条目：开源许可 / 隐私政策 / 项目主页 / 问题反馈（GitHub 链接，只读跳转）；
 * - 底部：版权 + 构建类型。
 *
 * 与 MCA 的差异：MCA 在"关于"里提供 APK 下载与安装（下载校验 + 系统安装器）；本仓库
 * 只做"检查 + 提示 + 打开 Release 页"，不下载、不安装（见汇报的简化点）。
 */
@Composable
internal fun SettingsAboutSection(
    status: GatewayStatus,
    /** 关掉关于页（面包屑右侧 ✕）。为空时 ✕ 置灰——调用方必须提供才会真正关闭。 */
    onClose: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    // ——— 块 0：面包屑顶栏（设置中心 / 关于）+ 关闭。设置页在这一组不再渲染自己的返回页头。 ———
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            painter = painterResource(R.drawable.ic_back),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(20.dp),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = "设置中心",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
        Text(
            text = " / ",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = "关于",
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            modifier = Modifier.weight(1f),
        )
        AetherHeaderIconButton(
            iconRes = R.drawable.ic_close,
            contentDescription = "关闭关于",
            onClick = { onClose?.invoke() },
            enabled = onClose != null,
        )
    }

    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    // 所有外部链接都交给 App 内 WebView 打开（不经过任何 Intent）。
    val openUrl: (String) -> Unit = LocalInAppBrowser.current
    // 四宫格的 App 内弹窗（用户要求：不跳外部浏览器）。
    var showSponsor by remember { mutableStateOf(false) }
    var showStarGuide by remember { mutableStateOf(false) }
    val clipboard = LocalClipboardManager.current

    val packageInfo = remember(context) {
        runCatching { context.packageManager.getPackageInfo(context.packageName, 0) }.getOrNull()
    }
    val versionName = packageInfo?.versionName.orEmpty().ifBlank { "0.0.0" }
    val versionCode = packageInfo?.let {
        if (Build.VERSION.SDK_INT >= 28) it.longVersionCode else it.versionCode.toLong()
    } ?: 0L
    val debugBuild = remember(context) {
        (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0
    }

    val prefs = remember(context) {
        context.getSharedPreferences(APP_UPDATE_PREFERENCES, Context.MODE_PRIVATE)
    }
    var state by remember { mutableStateOf(loadPersistedUpdateState(prefs, versionName, versionCode)) }
    var checking by remember { mutableStateOf(false) }
    var manualError by remember { mutableStateOf<String?>(null) }
    var dismissedVersion by remember {
        mutableStateOf(prefs.getString(APP_UPDATE_DISMISSED_VERSION_KEY, null))
    }

    // ——— App 内更新：APK 下载 + 系统安装器（不再跳 GitHub 网页） ———
    val lifecycleOwner = LocalLifecycleOwner.current
    val updateDir = remember(context) { File(context.cacheDir, "updates") }
    val downloader = remember { UpdateDownloader(updateDir) }
    // 缓存的 release 也要能直接点「下载」：重启 App 后不必先手动检查一次。
    var latestRelease by remember {
        mutableStateOf(
            prefs.getString(APP_UPDATE_RELEASE_JSON_KEY, null)
                ?.takeIf { it.isNotBlank() }
                ?.let { runCatching { parseGitHubReleaseJson(it) }.getOrNull() },
        )
    }
    var installPhase by remember { mutableStateOf(UpdateInstallPhase.IDLE) }
    /** 已下载 / 总字节；总字节未知时第二项为 -1。 */
    var downloadProgress by remember { mutableStateOf(0L to -1L) }
    var installError by remember { mutableStateOf<String?>(null) }
    var downloadedApk by remember { mutableStateOf<File?>(null) }
    var awaitingInstallPermission by remember { mutableStateOf(false) }
    var downloadJob by remember { mutableStateOf<Job?>(null) }

    val androidApkAsset: GitHubReleaseAsset? = remember(latestRelease) {
        latestRelease?.let { chooseAndroidApkAsset(it.assets, Build.SUPPORTED_ABIS.toList()) }
    }
    val sha256Asset: GitHubReleaseAsset? = remember(latestRelease) {
        latestRelease?.let { findSha256Asset(it.assets) }
    }

    // 安装：权限未授予 → 跳「安装未知应用」设置页，回来后自动重试一次（见下面的 Lifecycle 监听）。
    fun launchInstaller(apk: File) {
        if (!ApkInstaller.canInstallPackages(context)) {
            awaitingInstallPermission = true
            runCatching { context.startActivity(ApkInstaller.unknownSourcesSettingsIntent(context)) }
                .onFailure { installError = "无法打开「安装未知应用」设置页" }
            return
        }
        installError = null
        if (!ApkInstaller.install(context, apk)) {
            installError = "系统里没有可用的安装器"
        }
    }

    // 下载并安装：已下载同一版本（文件名 + 大小匹配）就直接装，不重复下载。
    fun startUpdate() {
        val asset = androidApkAsset
        if (asset == null) {
            installPhase = UpdateInstallPhase.FAILED
            installError = "该版本没有 Android 安装包"
            return
        }
        val cached = downloader.fileFor(asset).takeIf { cachedApkMatches(it, asset) }
        if (cached != null) {
            downloadedApk = cached
            installPhase = UpdateInstallPhase.DOWNLOADED
            launchInstaller(cached)
            return
        }
        downloadJob?.cancel()
        downloader.resetCancel()
        installError = null
        installPhase = UpdateInstallPhase.DOWNLOADING
        downloadProgress = 0L to asset.sizeBytes
        downloadJob = scope.launch {
            try {
                // 有 SHA256SUMS 就必须校验；拉校验文件失败也要报错，不能"当没有"。
                val expected = sha256Asset?.let { sums ->
                    withContext(Dispatchers.IO) {
                        downloader.fetchSha256Sums(sums)[asset.name]
                    }
                }
                val file = downloader.download(asset, expected) { done, total ->
                    downloadProgress = done to total
                }
                downloadedApk = file
                installPhase = UpdateInstallPhase.DOWNLOADED
                launchInstaller(file)
            } catch (e: ManagementApiException) {
                if (e.code == "cancelled") {
                    installPhase = UpdateInstallPhase.IDLE
                    installError = null
                } else {
                    installPhase = UpdateInstallPhase.FAILED
                    installError = updateDownloadErrorText(e.code, e.message)
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                installPhase = UpdateInstallPhase.FAILED
                installError = "下载失败：${e.message ?: "未知错误"}"
            }
        }
    }

    // 从系统设置返回：权限已授予就重试一次安装；仍被拒则停在「已下载，可重新安装」。
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME && awaitingInstallPermission) {
                awaitingInstallPermission = false
                val apk = downloadedApk
                if (apk == null) {
                    return@LifecycleEventObserver
                }
                if (ApkInstaller.canInstallPackages(context)) {
                    if (!ApkInstaller.install(context, apk)) {
                        installError = "系统里没有可用的安装器"
                    }
                } else {
                    installPhase = UpdateInstallPhase.DOWNLOADED
                    installError = "未授予「安装未知应用」权限；安装包已下载，可再次点「安装」"
                }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    suspend fun runCheck(manual: Boolean) {
        if (checking) {
            return
        }
        checking = true
        if (manual) {
            manualError = null
            state = state.copy(status = AppUpdateStatus.CHECKING, message = null)
        }
        try {
            val result = withContext(Dispatchers.IO) {
                GitHubReleaseClient().fetchLatest(prefs.getString(APP_UPDATE_ETAG_KEY, null))
            }
            val now = System.currentTimeMillis()
            val release = when (result) {
                is GitHubReleaseFetchResult.Fetched -> {
                    prefs.edit()
                        .putString(APP_UPDATE_RELEASE_JSON_KEY, result.rawJson)
                        .putString(APP_UPDATE_ETAG_KEY, result.etag)
                        .putLong(APP_UPDATE_LAST_CHECKED_KEY, now)
                        .remove(APP_UPDATE_LAST_FAILURE_KEY)
                        .apply()
                    result.release
                }
                is GitHubReleaseFetchResult.NotModified -> {
                    prefs.edit()
                        .putLong(APP_UPDATE_LAST_CHECKED_KEY, now)
                        .putString(APP_UPDATE_ETAG_KEY, result.etag)
                        .remove(APP_UPDATE_LAST_FAILURE_KEY)
                        .apply()
                    val cached = prefs.getString(APP_UPDATE_RELEASE_JSON_KEY, null)
                    if (cached.isNullOrBlank()) {
                        throw IllegalStateException("GitHub 返回 304，但本机没有缓存的 Release。")
                    }
                    parseGitHubReleaseJson(cached)
                }
            }
            state = resolveAppUpdate(
                release = release,
                currentVersionName = versionName,
                autoCheckEnabled = prefs.getBoolean(APP_UPDATE_AUTO_CHECK_KEY, true),
                lastCheckedAtMillis = now,
            ).state
            // 发布版本换了就清空上一次的下载态；同一次发布重复检查保留"已下载"。
            if (latestRelease?.tagName != release.tagName) {
                installPhase = UpdateInstallPhase.IDLE
                installError = null
                downloadedApk = null
            }
            latestRelease = release
        } catch (e: Exception) {
            if (manual) {
                manualError = e.message ?: "更新检查失败。"
                state = state.copy(status = AppUpdateStatus.ERROR, message = e.message)
            } else {
                // 静默检查失败：只记退避时间，不打扰（不弹、不写错误态）
                prefs.edit().putLong(APP_UPDATE_LAST_FAILURE_KEY, System.currentTimeMillis()).apply()
            }
        } finally {
            checking = false
        }
    }

    // 自动检查：最多 24h 一次，失败 15 分钟内不重试（与 MCA 同值）
    LaunchedEffect(Unit) {
        if (
            state.status == AppUpdateStatus.IDLE &&
            shouldAutoCheckAppUpdate(
                nowMillis = System.currentTimeMillis(),
                lastCheckedAtMillis = prefs.getLong(APP_UPDATE_LAST_CHECKED_KEY, 0L),
                lastFailureAtMillis = prefs.getLong(APP_UPDATE_LAST_FAILURE_KEY, 0L),
                autoCheckEnabled = prefs.getBoolean(APP_UPDATE_AUTO_CHECK_KEY, true),
            )
        ) {
            runCheck(manual = false)
        }
    }

    // ——— 应用（1:1 复刻 MCA `SettingsAboutScreen.kt:290-311`：
    //     Card + surface 底色 + elevation 1dp + shape 12dp + 内边距 20/22 + 品牌标 64dp；
    //     这里**刻意**不使用本仓库的 `DeepCard`（那会带 hairline 描边，与 MCA 不一致）。
    //     也**没有段落标题**：MCA 原页的卡片之间只隔间距，不插 DeepSectionTitle。）
    Card(
        modifier = modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
        shape = RoundedCornerShape(AboutMcaHeroShapeDp),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = AboutMcaHeroPaddingH,
                    vertical = AboutMcaHeroPaddingV,
                ),
            // MCA `:300-301`：hero 卡内部**整体居中**，不是左对齐的图文行。
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            // 1. 应用图标（MCA `:303-307`：64dp，居中）
            // Hero 应用标 = **真正的启动图标**（与桌面 launcher 完全一致，改 launcher 时自动跟随）。
            // 不加圆角遮罩/圆形裁剪：启动图标本身自带形状。
            AppLauncherIcon(size = AboutMcaLogoSize)
            // 2. 应用名：大字加粗（MCA `:308-312`）
            Text(
                text = AppDisplayName,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                textAlign = TextAlign.Center,
            )
            // 3. 一句话简介（MCA `:313-317`）
            Text(
                text = AppTagline,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                textAlign = TextAlign.Center,
            )
            // 4. 一行三件套：版本胶囊 + 检查更新 + 更新记录（MCA `:318-341`）
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Surface(
                    shape = RoundedCornerShape(AboutMcaPillCornerPercent),
                    color = MaterialTheme.colorScheme.surfaceVariant,
                ) {
                    Text(
                        text = "v$versionName",
                        style = MaterialTheme.typography.labelMedium,
                        fontFamily = FontFamily.Monospace,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        modifier = Modifier.padding(
                            horizontal = AboutMcaPillPaddingH,
                            vertical = AboutMcaPillPaddingV,
                        ),
                    )
                }
                McaButton(
                    onClick = { scope.launch { runCheck(manual = true) } },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                    enabled = !checking,
                ) { Text(if (checking) "检查中…" else "检查更新", maxLines = 1) }
                McaButton(
                    onClick = { openUrl(GITHUB_RELEASE_PAGE_URL) },
                    style = McaButtonStyle.Text,
                    size = McaButtonSize.CardAction,
                ) { Text("更新记录", maxLines = 1) }
            }
            // 5. 两行居中文本：最新版本 / 状态（MCA `:342-354`）
            val latest = state.latestVersionName
            if (!latest.isNullOrBlank() && latest != versionName) {
                Text(
                    text = "最新版本 v$latest",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            Text(
                text = appUpdateStatusLabel(if (checking) AppUpdateStatus.CHECKING else state.status),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
    Spacer(Modifier.height(AboutMcaSectionGap))

    // ——— 应用更新（开关行统一「左标题+说明 / 右 McaSwitch」横排；MCA 原页同样没有段落标题） ———
    // 1:1 复刻 MCA `:371-379`：Card + shape 8dp + padding 16dp。
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
        shape = RoundedCornerShape(AboutMcaCardShapeDp),
    ) {
        Column(modifier = Modifier.padding(AboutMcaCardPadding)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = "自动检查更新",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = "每天最多一次，失败后 15 分钟内不重试。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                )
            }
            Spacer(Modifier.width(12.dp))
            McaSwitch(
                checked = prefs.getBoolean(APP_UPDATE_AUTO_CHECK_KEY, true),
                onCheckedChange = { enabled ->
                    prefs.edit().putBoolean(APP_UPDATE_AUTO_CHECK_KEY, enabled).apply()
                    state = state.copy(autoCheckEnabled = enabled)
                },
                contentDescription = "自动检查更新",
            )
        }
        Spacer(Modifier.height(8.dp))
        Text(
            text = "从官方 GitHub Releases 检查稳定版更新；更新包在 App 内下载，下载完交给系统安装器安装。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        AboutRow("当前版本", aboutVersionLabel(versionName, versionCode))
        AboutRow("最新版本", state.latestVersionName ?: "尚未检查")
        AboutRow(
            "状态",
            appUpdateStatusLabel(if (checking) AppUpdateStatus.CHECKING else state.status),
        )
        if (state.lastCheckedAtMillis > 0L) {
            AboutRow(
                "上次检查",
                aboutCheckedAtLabel(state.lastCheckedAtMillis, System.currentTimeMillis()),
            )
        }
        state.message?.takeIf { it.isNotBlank() }?.let { message ->
            Text(
                text = message,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        manualError?.let { error ->
            Text(
                text = "检查更新失败：$error",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        val latest = state.latestVersionName
        if (
            state.status == AppUpdateStatus.AVAILABLE &&
            shouldAnnounceUpdate(latest, dismissedVersion)
        ) {
            Spacer(Modifier.height(6.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = "发现新版本 $latest${state.latestTitle?.takeIf { it.isNotBlank() }?.let { "（$it）" }.orEmpty()}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.weight(1f),
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                McaButton(
                    onClick = {
                        dismissedVersion = latest
                        prefs.edit().putString(APP_UPDATE_DISMISSED_VERSION_KEY, latest).apply()
                    },
                    style = McaButtonStyle.Text,
                ) { Text("稍后") }
            }
        }
        val notes = aboutReleaseNotesText(state.releaseNotes)
        if (notes.isNotEmpty() && state.status != AppUpdateStatus.IDLE) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = notes,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        // ——— 块 2 明细：安装包字节数 + 元信息 `·` 行（取不到就整行不显示） ———
        state.assetsBytes?.let { bytes ->
            Text(
                text = "安装包：${formatUpdateAssetSize(bytes)}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } ?: Text(
            text = "安装包：未找到本平台安装包",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        @Composable
        fun metaLine(text: String?) {
            if (!text.isNullOrBlank()) {
                Text(
                    text = "· $text",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        metaLine(state.publishedAt?.let { "发布于 $it" })
        metaLine(state.previousVersionCode?.let { "上一版本 v${state.latestVersionName ?: "-"} (versionCode $it)" })
        metaLine("本版本 v$versionName (versionCode $versionCode)")
        metaLine(state.applicableDevices)
        metaLine(state.license)

        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = { scope.launch { runCheck(manual = true) } },
                style = McaButtonStyle.Outlined,
                enabled = !checking,
            ) { Text(if (checking) "检查中…" else "检查更新") }
        }
        // ——— App 内更新：下载 + 进度 + 安装（不再跳 GitHub 网页） ———
        if (state.status == AppUpdateStatus.AVAILABLE) {
            Spacer(Modifier.height(10.dp))
            when {
                androidApkAsset == null -> {
                    Text(
                        text = "该版本没有 Android 安装包，可前往发布页手动下载。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(6.dp))
                    McaButton(
                        onClick = { openUrl(state.releaseUrl) },
                        style = McaButtonStyle.Outlined,
                    ) { Text("前往发布页") }
                }
                installPhase == UpdateInstallPhase.DOWNLOADING -> {
                    Text(
                        text = updateDownloadProgressText(downloadProgress.first, downloadProgress.second),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(6.dp))
                    val total = downloadProgress.second
                    val fraction = if (total > 0L) {
                        (downloadProgress.first.toFloat() / total.toFloat()).coerceIn(0f, 1f)
                    } else {
                        0f
                    }
                    AboutDownloadProgressBar(fraction)
                    Spacer(Modifier.height(8.dp))
                    McaButton(
                        onClick = {
                            downloader.cancel()
                            downloadJob?.cancel()
                            downloadJob = null
                            installPhase = UpdateInstallPhase.IDLE
                            installError = null
                        },
                        style = McaButtonStyle.Outlined,
                    ) { Text("取消下载") }
                }
                else -> {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        McaButton(
                            onClick = {
                                if (installPhase == UpdateInstallPhase.DOWNLOADED) {
                                    downloadedApk?.let { launchInstaller(it) }
                                } else {
                                    startUpdate()
                                }
                            },
                            style = McaButtonStyle.Primary,
                        ) { Text(updateInstallActionLabel(installPhase)) }
                        McaButton(
                            onClick = { openUrl(state.releaseUrl) },
                            style = McaButtonStyle.Text,
                        ) { Text("发布页") }
                    }
                    if (installPhase == UpdateInstallPhase.DOWNLOADED) {
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "已下载，可重新安装；在系统安装界面点取消不会报错。",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    installError?.let { message ->
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = message,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                }
            }
            // 保留浏览器出口（次要，不是主按钮）。
            Spacer(Modifier.height(6.dp))
            Text(
                text = "也可以到发布页手动下载",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.clickable {
                    openUrl(state.releaseUrl.ifBlank { GITHUB_RELEASE_PAGE_URL })
                },
            )
        }
        }
    }
    Spacer(Modifier.height(AboutMcaSectionGap))

    // ——— 链接：2×2 四宫格（MCA `:190-220` 的 AboutLinkCard 排布；上轮曾按 lead 决定保留行式，
    //     用户贴出 MCA 截图后按截图改回 2×2：每格 图标 + 标题 + 一行小字，shape 12 + padding 12/16。）
    Row(
        modifier = Modifier.padding(top = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AboutMcaLinkTile(
            title = "主作者",
            subtitle = "lyydfys",
            iconRes = R.drawable.ic_person,
            modifier = Modifier.weight(1f),
            onClick = {
                    val target = aboutTileAction(AboutTile.AUTHOR).link.orEmpty()
                    android.util.Log.d("AetherAbout", "tile click AUTHOR, url=$target")
                    openUrl(target)
                },
        )
        AboutMcaLinkTile(
            title = "开源仓库",
            subtitle = "GitHub",
            iconRes = R.drawable.ic_code,
            modifier = Modifier.weight(1f),
            onClick = { openUrl(aboutTileAction(AboutTile.REPOSITORY).link.orEmpty()) },
        )
    }
    Spacer(Modifier.height(12.dp))
    Row(
        modifier = Modifier.padding(top = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AboutMcaLinkTile(
            title = "赞助支持",
            subtitle = "请我喝杯咖啡",
            iconRes = R.drawable.ic_favorite,
            modifier = Modifier.weight(1f),
            onClick = { showSponsor = true },
        )
        AboutMcaLinkTile(
            title = "意见反馈",
            subtitle = "Issues",
            iconRes = R.drawable.ic_chat,
            modifier = Modifier.weight(1f),
            onClick = { openUrl(aboutTileAction(AboutTile.FEEDBACK).link.orEmpty()) },
        )
    }
    Spacer(Modifier.height(AboutMcaSectionGap))

    // ——— 块 4：Star 提示卡（MCA `AboutStarBanner`：浅蓝底 + ★ + 两行文案 + ✕ + 两个文字链接） ———
    val starDismissed = remember {
        mutableStateOf(prefs.getBoolean(APP_STAR_DISMISSED_KEY, false))
    }
    if (!starDismissed.value) {
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.primaryContainer,
            ),
            elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
            shape = RoundedCornerShape(AboutMcaHeroShapeDp),
        ) {
            Column(modifier = Modifier.padding(AboutMcaCardPadding)) {
                Row(verticalAlignment = Alignment.Top) {
                    // 星标用矢量图标（ic_star，与四宫格同一套资源），不再用文字星号。
                    Icon(
                        painter = painterResource(R.drawable.ic_star),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.size(20.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Column(Modifier.weight(1f)) {
                        Text(
                            text = "如果 $AppDisplayName 帮到了你",
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = MaterialTheme.colorScheme.onSurface,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text = "请在 GitHub 点个 Star，这对我们意义重大",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    AetherHeaderIconButton(
                        iconRes = R.drawable.ic_close,
                        contentDescription = "关闭 Star 提示",
                        onClick = {
                            prefs.edit().putBoolean(APP_STAR_DISMISSED_KEY, true).apply()
                            starDismissed.value = true
                        },
                    )
                }
                Spacer(Modifier.height(6.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    McaButton(
                        onClick = { showStarGuide = true },
                        style = McaButtonStyle.Text,
                        size = McaButtonSize.CardAction,
                    ) { Text("怎么点 Star?", maxLines = 1) }
                    McaButton(
                        onClick = { openUrl(GITHUB_REPO_PAGE_URL) },
                        style = McaButtonStyle.Text,
                        size = McaButtonSize.CardAction,
                    ) { Text("去点 Star", maxLines = 1) }
                }
            }
        }
        Spacer(Modifier.height(AboutMcaSectionGap))
    }

    // ——— 块 5：版本信息卡（键值行；"自动检查"是**文字状态**，开关移到卡下方单独一行） ———
    val autoCheckOn = prefs.getBoolean(APP_UPDATE_AUTO_CHECK_KEY, true)
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
        shape = RoundedCornerShape(AboutMcaHeroShapeDp),
    ) {
        Column(modifier = Modifier.padding(AboutMcaCardPadding)) {
            AboutMcaKeyValueRow("当前版本", "v$versionName")
            AboutMcaKeyValueRow("最新版本", "v${state.latestVersionName ?: versionName}")
            val checkedAt = aboutCheckedAtLabel(state.lastCheckedAtMillis, System.currentTimeMillis())
            AboutMcaKeyValueRow("上次检查", checkedAt)
            AboutMcaKeyValueRow("自动检查", if (autoCheckOn) "已开启" else "已关闭")
            AboutMcaKeyValueRow("开源许可", AppLicenseName)
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "自动检查更新",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = "每天最多一次，失败后 15 分钟内不重试。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                    )
                }
                Spacer(Modifier.width(12.dp))
                McaSwitch(
                    checked = autoCheckOn,
                    onCheckedChange = { enabled ->
                        prefs.edit().putBoolean(APP_UPDATE_AUTO_CHECK_KEY, enabled).apply()
                        state = state.copy(autoCheckEnabled = enabled)
                    },
                    contentDescription = "自动检查更新",
                )
            }
        }
    }
    Spacer(Modifier.height(AboutMcaSectionGap))

    // ——— 四宫格 App 内弹窗：链接 + 说明 + 复制（不打开任何外部浏览器） ———
    if (showStarGuide) {
        AboutStarGuideDialog(onDismiss = { showStarGuide = false })
    }
    // 「赞助支持」按 MCA `AboutSponsorDialog`（`SettingsAboutScreen.kt:617-748`）1:1 复刻，
    // 其余三格仍走通用「链接 + 说明 + 复制」弹窗。
    if (showSponsor) {
        AboutSponsorDialog(onDismiss = { showSponsor = false })
    }
    // 四宫格各格现在直接动作（AUTHOR/REPOSITORY/FEEDBACK → Custom Tabs，SPONSOR → 赞助抽屉），
    // 旧的「通用链接弹窗」（activeTile + McaDialog）已随本节重构移除。

    // ——— 底部（不塞进卡片：和基准页的页脚说明同一层级） ———
    Text(
        text = aboutCopyrightLine(),
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Text(
        text = "构建类型：${buildTypeLabel(debugBuild)}" + if (status.running) "" else " · 网关未启动",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

/**
 * 版本信息卡的一行：左标签 + 右对齐值（MCA 版本信息区同款）。
 *
 * 值用 `bodyMedium` 右对齐，长值单行省略——1.3× 下也不会挤成竖排。
 */
@Composable
private fun AboutMcaKeyValueRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.widthIn(min = 84.dp),
        )
        Spacer(Modifier.width(12.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.End,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
    }
}

/**
 * Star 教程弹窗：结构对齐 MCA `AboutStarGuideDialog`（`SettingsAboutScreen.kt:750+`）。
 *
 * 标题行（图标 + 标题 + 关闭）→ 可滚动正文（编号步骤 + 结尾一句 + 仓库链接与复制）→ 关闭。
 * 步骤来自 [aboutStarGuideSteps]（纯数据，有单测）；**不跳浏览器**，只复制链接。
 */
@Composable
private fun AboutStarGuideDialog(onDismiss: () -> Unit) {
    val openUrl: (String) -> Unit = LocalInAppBrowser.current
    val clipboard = LocalClipboardManager.current
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Icon(
                    painter = painterResource(R.drawable.ic_star),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.size(20.dp),
                )
                Text(
                    text = "怎么点 Star?",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.weight(1f),
                )
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_close,
                    contentDescription = "关闭 Star 教程",
                    onClick = onDismiss,
                )
            }
        },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 560.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                aboutStarGuideSteps().forEachIndexed { index, step ->
                    Row {
                        Text(
                            text = "${index + 1}.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.widthIn(min = 18.dp),
                        )
                        Text(
                            text = step,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                    }
                }
                Text(
                    text = GITHUB_REPO_PAGE_URL,
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                McaButton(
                    onClick = { clipboard.setText(AnnotatedString(GITHUB_REPO_PAGE_URL)) },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                ) { Text("复制仓库链接", maxLines = 1) }
                Text(
                    text = aboutStarGuideFooter(),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {},
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/**
 * 赞助支持弹窗：1:1 复刻 MCA `AboutSponsorDialog`（`SettingsAboutScreen.kt:617-748`）。
 *
 * 结构对齐：标题行（Logo 40dp + 「赞助支持」+「如果 Aether Cockpit 帮到了你」+ 关闭按钮）→
 * 可滚动正文（说明 → 四个动作按钮 → 自愿声明 → Star 教程入口 → 离线赞赏码开关）→ 底部「关闭」。
 * 差异（见汇报）：
 *  - MCA 用 `FlowRow`，这里用两行 `Row`（仓库 Compose 版本上避免额外 ExperimentalLayoutApi）；
 *  - MCA 有「分享项目」，本仓库不跳外部（`ACTION_SEND` 会拉起系统分享面板），改成**复制仓库链接**；
 *  - 赞赏码区域默认**折叠**（[AboutSponsorQrPanel]）：点一行「赞赏码」才展开两张图，
 *    避免弹窗一打开就被大图撑满；MCA 是同款折叠思路，只是它的入口文案叫「离线赞赏码」；
 *  - 「去赞助 / 去点 Star / 意见反馈」在 MCA 里是外部跳转，这里一律**复制链接**在 App 内完成。
 */
@Composable
private fun AboutSponsorDialog(onDismiss: () -> Unit) {
    val openUrl: (String) -> Unit = LocalInAppBrowser.current
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    val sponsor = aboutTileAction(AboutTile.SPONSOR)
    fun copy(link: String) {
        clipboard.setText(AnnotatedString(link))
    }
    // 赞赏码默认折叠：弹窗刚打开时只显示一行入口，点一下才展开两张码。
    // 用 `remember` 而不是 rememberSaveable：每次重新打开弹窗都回到折叠态（更符合"默认折叠"）。
    var qrExpanded by remember { mutableStateOf(false) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                McaIconPlate(size = 40.dp, shape = McaShape.Badge) {
                    Icon(
                        painter = painterResource(R.drawable.ic_favorite),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.size(22.dp),
                    )
                }
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "赞助支持",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                    )
                    Text(
                        text = "如果 $AppDisplayName 帮到了你",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_close,
                    contentDescription = "关闭赞助支持",
                    onClick = onDismiss,
                )
            }
        },
        text = {
            Column(
                modifier = Modifier
                    .heightIn(max = 560.dp)
                    .verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    text = "开源项目的持续维护离不开每一份支持。可以点 Star、分享项目，或前往 GitHub 赞助页支持持续开发。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                // 按 MCA `:685-705`：四个动作**真的打开/分享**，不是复制。
                // 打开走 Custom Tabs（App 内窗口），分享走系统分享面板。
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { openUrl(sponsor.link.orEmpty()) },
                        style = McaButtonStyle.Primary,
                        size = McaButtonSize.CardAction,
                    ) { Text("去赞助", maxLines = 1) }
                    McaButton(
                        onClick = { openUrl(GITHUB_REPO_PAGE_URL) },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("去点 Star", maxLines = 1) }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { shareProject(context) },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("分享项目", maxLines = 1) }
                    McaButton(
                        onClick = { openUrl(GITHUB_ISSUES_URL) },
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("意见反馈", maxLines = 1) }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { copy(sponsor.link.orEmpty()) },
                        style = McaButtonStyle.Text,
                        size = McaButtonSize.CardAction,
                    ) { Text("复制赞助链接", maxLines = 1) }
                }
                Text(
                    text = sponsor.link.orEmpty(),
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = "如需直接赞赏，可通过 GitHub Sponsors 页面选择档位。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text(
                    text = "赞助完全自愿，不影响任何功能。感谢每一份支持！",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text(
                    text = "复制链接后粘贴到浏览器打开：本项目不内嵌外部浏览器，也不会自动跳转。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                // 离线赞赏码：按 MCA「离线赞赏码」的位置放在正文最末，默认折叠。
                AboutSponsorQrPanel(
                    expanded = qrExpanded,
                    onToggle = { qrExpanded = !qrExpanded },
                )
            }
        },
        confirmButton = {},
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/**
 * 「赞赏码」折叠区：一行入口 + 展开后的支付宝/微信两张离线赞赏码。
 *
 * 交互：整行可点（`clickable` + `Role.Button`），箭头展开时旋转 90° 指下、收起时指右。
 * 展开状态由调用方持有（[AboutSponsorDialog] 的 `qrExpanded`），每次重开弹窗都回到折叠态。
 *
 * 排版：用 [BoxWithConstraints] 读**实际可用宽度**，够 [AboutSponsorQrTwoUpMinWidth] 才两列，
 * 否则单列竖排。1.0× / 1.3× 字体下都只会「整体换行」，不会把「支付宝」压成单字竖排——
 * 每行标注都带 `maxLines = 1` + `softWrap = false`，最坏情况是省略号，永远不是竖排。
 */
@Composable
private fun AboutSponsorQrPanel(
    expanded: Boolean,
    onToggle: () -> Unit,
) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(AboutMcaHeroShapeDp),
        color = MaterialTheme.colorScheme.surfaceVariant,
    ) {
        Column(modifier = Modifier.fillMaxWidth()) {
            Row(
                // 点击挂在 Row 上（与 AboutMcaLinkTile 同一套写法：挂 Card/Surface 会被
                // M3 的 surface/ripple 层吞掉），semantics 显式声明 Button 角色。
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(onClick = onToggle)
                    .semantics { role = androidx.compose.ui.semantics.Role.Button }
                    .padding(horizontal = 12.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "赞赏码",
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = if (expanded) "点击收起" else "支付宝 / 微信 · 点击展开",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        softWrap = false,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Spacer(Modifier.width(8.dp))
                Icon(
                    painter = painterResource(R.drawable.ic_chevron),
                    contentDescription = if (expanded) "收起赞赏码" else "展开赞赏码",
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier
                        .size(18.dp)
                        .rotate(if (expanded) 90f else 0f),
                )
            }
            if (expanded) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(start = 12.dp, end = 12.dp, bottom = 12.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    BoxWithConstraints(Modifier.fillMaxWidth()) {
                        if (maxWidth >= AboutSponsorQrTwoUpMinWidth) {
                            // 两列：只有平板 / 横屏 / 大窗才够宽，此时两张码并排。
                            Row(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalArrangement = Arrangement.spacedBy(AboutSponsorQrGap),
                                verticalAlignment = Alignment.Top,
                            ) {
                                AboutSponsorQrItem(
                                    imageRes = R.drawable.ic_sponsor_alipay,
                                    label = "支付宝",
                                    description = "支付宝赞赏码",
                                    modifier = Modifier.weight(1f),
                                )
                                AboutSponsorQrItem(
                                    imageRes = R.drawable.ic_sponsor_wechat,
                                    label = "微信",
                                    description = "微信赞赏码",
                                    modifier = Modifier.weight(1f),
                                )
                            }
                        } else {
                            // 单列：手机竖屏（含 1.3× 字体）走这里，两张码上下居中排列。
                            Column(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalAlignment = Alignment.CenterHorizontally,
                                verticalArrangement = Arrangement.spacedBy(AboutSponsorQrGap),
                            ) {
                                AboutSponsorQrItem(
                                    imageRes = R.drawable.ic_sponsor_alipay,
                                    label = "支付宝",
                                    description = "支付宝赞赏码",
                                )
                                AboutSponsorQrItem(
                                    imageRes = R.drawable.ic_sponsor_wechat,
                                    label = "微信",
                                    description = "微信赞赏码",
                                )
                            }
                        }
                    }
                    Text(
                        text = "GitHub 访问不稳定时，可直接扫码赞赏。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/**
 * 单张赞赏码：外框 + 等比缩放的码图 + 下方标注。
 *
 * 圆角只加在**外框** [Surface] 上；图片自身再留 [AboutSponsorQrFramePadding] 的白边，
 * 所以圆角弧线永远切不到二维码本体（三个定位角完整）。`ContentScale.Fit` 保证
 * 任何可用宽度下都不被拉伸——二维码一旦非等比变形就扫不出来。
 */
@Composable
private fun AboutSponsorQrItem(
    imageRes: Int,
    label: String,
    description: String,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier,
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Surface(
            shape = RoundedCornerShape(AboutMcaCardShapeDp),
            color = MaterialTheme.colorScheme.surface,
        ) {
            Image(
                painter = painterResource(imageRes),
                contentDescription = description,
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .padding(AboutSponsorQrFramePadding)
                    .size(AboutSponsorQrImageSize),
            )
        }
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.Center,
            maxLines = 1,
            softWrap = false,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * 2×2 宫格里的一格：1:1 复刻 MCA `AboutLinkCard`
 * （`SettingsAboutScreen.kt:482-511`：`Card`(surface + elevation 1 + shape 12) →
 * `Column`(padding 12/16, 居中, spacedBy 6) → `Icon`(primary 着色) + 标题 SemiBold + 一行小字居中）。
 */
@Composable
private fun AboutMcaLinkTile(
    title: String,
    subtitle: String,
    iconRes: Int,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    Card(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = 116.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
        shape = RoundedCornerShape(AboutMcaHeroShapeDp),
    ) {
        Column(
            // 点击挂在**内部 Column** 上（放在 Card 上会被 M3 Card 的 surface/ripple 层吞掉，
            // 真机上表现为"点了没反应"且 uiautomator 里 clickable=false）。
            // semantics 显式声明 Role.Button，保证无障碍与 UI 层级都能识别为可点击。
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onClick)
                .semantics { role = androidx.compose.ui.semantics.Role.Button }
                .padding(horizontal = 12.dp, vertical = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Icon(
                painter = painterResource(iconRes),
                contentDescription = null,
                tint = MaterialTheme.colorScheme.primary,
                modifier = Modifier.size(24.dp),
            )
            Text(
                text = title,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** 从 SharedPreferences 还原上次检查状态（含缓存里的 release）。 */
private fun loadPersistedUpdateState(
    prefs: android.content.SharedPreferences,
    versionName: String,
    versionCode: Long,
): AppUpdateState {
    val base = AppUpdateState(
        currentVersionName = versionName,
        currentVersionCode = versionCode,
        autoCheckEnabled = prefs.getBoolean(APP_UPDATE_AUTO_CHECK_KEY, true),
        lastCheckedAtMillis = prefs.getLong(APP_UPDATE_LAST_CHECKED_KEY, 0L),
    )
    val raw = prefs.getString(APP_UPDATE_RELEASE_JSON_KEY, null)?.takeIf { it.isNotBlank() } ?: return base
    return runCatching {
        resolveAppUpdate(
            release = parseGitHubReleaseJson(raw),
            currentVersionName = versionName,
            autoCheckEnabled = base.autoCheckEnabled,
            lastCheckedAtMillis = base.lastCheckedAtMillis,
        ).state
    }.getOrElse { base }
}

/**
 * 4dp 高的下载进度条。
 *
 * 不用 M3 `LinearProgressIndicator`：该 API 在 Compose 版本间取值/λ 形态变过，
 * 两条 Box 拼出来最稳，颜色也自动跟随主题。
 */
@Composable
private fun AboutDownloadProgressBar(fraction: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(4.dp)
            .clip(RoundedCornerShape(2.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(fraction.coerceIn(0f, 1f))
                .fillMaxHeight()
                .background(MaterialTheme.colorScheme.primary),
        )
    }
}

/** 一行「标签 + 值」；行高对齐 DeepRowVertical，值单行省略，1.0×/1.3× 都不会退化成单字竖排。 */@Composable
private fun AboutRow(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(12.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** 可点条目：左标题（bodyMedium）+ 右侧值（labelSmall）+ chevron 图标，与目录行同一套收边。 */
@Composable
private fun AboutLinkRow(label: String, value: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.weight(1f),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.width(12.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.width(6.dp))
        Icon(
            painter = painterResource(R.drawable.ic_chevron),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(McaIconSize.S),
        )
    }
}

/**
 * `Drawable → ImageBitmap`：只用 android.graphics API（不引 core-ktx 的 `toBitmap`）。
 *
 * 启动图标尺寸一般 108–192px，这里按固有尺寸绘制；`intrinsicWidth/Height` 为 0 时退到 192px。
 */
private fun drawableToImageBitmap(drawable: android.graphics.drawable.Drawable): ImageBitmap {
    val width = drawable.intrinsicWidth.takeIf { it > 0 } ?: 192
    val height = drawable.intrinsicHeight.takeIf { it > 0 } ?: 192
    val bitmap = android.graphics.Bitmap.createBitmap(width, height, android.graphics.Bitmap.Config.ARGB_8888)
    val canvas = android.graphics.Canvas(bitmap)
    drawable.setBounds(0, 0, canvas.width, canvas.height)
    drawable.draw(canvas)
    return bitmap.asImageBitmap()
}

/**
 * 应用启动图标（与桌面 launcher 一致）。
 *
 * 取法：`applicationInfo.icon`（资源 id）→ `painterResource`。
 * 部分 ROM 会把 adaptive icon 的 xml 返回给 painter 导致渲染失败，所以有两条兜底：
 * 1. `packageManager.getApplicationIcon(packageName)` → `drawable.toBitmap().asImageBitmap()`；
 * 2. 再不行 → 仓库既有的 `ic_brand_mark`。
 * 全部 `runCatching` 包住，绝不因为图标崩页面。
 */
@Composable
private fun AppLauncherIcon(size: Dp) {
    val context = LocalContext.current
    val appIconId = remember(context) {
        runCatching { context.applicationInfo.icon }.getOrDefault(0)
    }
    var bitmapFallback by remember(context) { mutableStateOf<ImageBitmap?>(null) }
    LaunchedEffect(appIconId) {
        // 统一走 Bitmap：部分 ROM 的 launcher icon 是 adaptive-icon XML，
        // `painterResource(appIconId)` 会直接抛异常把整页打崩（真机复现）。
        bitmapFallback = runCatching {
            drawableToImageBitmap(context.packageManager.getApplicationIcon(context.packageName))
        }.getOrNull()
    }
    when {
        bitmapFallback != null -> Image(
            bitmap = bitmapFallback!!,
            contentDescription = null,
            modifier = Modifier.size(size),
        )
        else -> Icon(
            painter = painterResource(R.drawable.ic_brand_mark),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.size(size),
        )
    }
}

