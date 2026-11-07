package com.muyuchat.aether.runtime

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.Locale

/* ==========================================================================
 * App 内更新：挑 APK → 下载（进度/校验/取消）→ 调起系统安装器
 *
 * 与「检查更新」（UpdateCheckApiClient.kt）分开：那边只解析 Release，
 * 这里只做本机能力（文件/网络/安装），不碰 UI。
 * ========================================================================== */

/** 设备支持的第 i 个 ABI 在资产名里可能出现的写法（按优先级）。 */
internal fun abiAliases(abi: String): List<String> {
    val value = abi.trim().lowercase(Locale.US)
    return when (value) {
        "arm64-v8a", "arm64" -> listOf("arm64-v8a", "arm64")
        "armeabi-v7a", "armeabi" -> listOf("armeabi-v7a", "armv7", "armeabi")
        "x86_64" -> listOf("x86_64", "x86-64")
        "x86" -> listOf("x86")
        else -> listOf(value).filter { it.isNotBlank() }
    }
}

/**
 * 从 Release 资产里挑 Android 安装包。
 *
 * 规则（用户要求：优先当前 ABI，其次 `android` + `.apk`，都没有就返回 null）：
 * 1. 按 `abis` 顺序逐个找**文件名里带该 ABI**的 `.apk`（arm64-v8a → arm64 → …）；
 * 2. 名字里带 `universal` 的 `.apk`（通常全 ABI 通用）；
 * 3. 名字里带 `android` 的 `.apk`；
 * 4. 任意 `.apk`（沿用 [choosePlatformAsset] 的打分：arm64 > universal > 其他）。
 *
 * 不选 `*.sha256` / `*.txt` / 含 `source` 的资产——它们不是安装包。
 */
internal fun chooseAndroidApkAsset(
    assets: List<GitHubReleaseAsset>,
    abis: List<String>,
): GitHubReleaseAsset? {
    val apks = assets.filter { asset ->
        val name = asset.name.lowercase(Locale.US)
        name.endsWith(".apk") && !name.contains("source")
    }
    if (apks.isEmpty()) {
        return null
    }
    for (abi in abis) {
        for (alias in abiAliases(abi)) {
            apks.firstOrNull { it.name.lowercase(Locale.US).contains(alias) }?.let { return it }
        }
    }
    apks.firstOrNull { it.name.lowercase(Locale.US).contains("universal") }?.let { return it }
    apks.firstOrNull { it.name.lowercase(Locale.US).contains("android") }?.let { return it }
    return choosePlatformAsset(apks, UpdatePlatform.ANDROID)
}

/** `SHA256SUMS.txt` / `sha256sums.txt` 之类资产；没有就返回 null（跳过校验，不假成功）。 */
internal fun findSha256Asset(assets: List<GitHubReleaseAsset>): GitHubReleaseAsset? =
    assets.firstOrNull { it.name.lowercase(Locale.US).contains("sha256") && !it.name.lowercase(Locale.US).endsWith(".apk") }

/**
 * 解析 `sha256sum` 格式：`<64 位十六进制>  <文件名>`（支持 `*文件名` 与路径前缀）。
 * 返回 文件名 → 小写十六进制。
 */
internal fun parseSha256Sums(text: String): Map<String, String> {
    val out = LinkedHashMap<String, String>()
    text.lineSequence().forEach { rawLine ->
        val line = rawLine.trim()
        if (line.isEmpty() || line.startsWith("#")) {
            return@forEach
        }
        val match = Regex("^([0-9a-fA-F]{64})\\s+[* ]?(.+)$").matchEntire(line) ?: return@forEach
        val hash = match.groupValues[1].lowercase(Locale.US)
        val name = match.groupValues[2].trim().substringAfterLast('/').substringAfterLast('\\')
        if (name.isNotBlank()) {
            out[name] = hash
        }
    }
    return out
}

/** 资产名 → 本机文件名（去掉路径分隔符，防止被资产名写出目录）。 */
internal fun safeApkFileName(assetName: String): String {
    // 注意：先把分隔符/空白统一换成 `_`，再取最后一段。反过来（先截最后一段）
    // 会让 "a b/c.apk" 变成 "c.apk"，丢掉 "a b" 这部分——测试钉住的是前者。
    val cleaned = assetName.trim().replace(Regex("[\\\\/:*?\"<>|\\s]"), "_")
    return if (cleaned.endsWith(".apk", ignoreCase = true) && cleaned.length > 4) {
        cleaned
    } else {
        "update.apk"
    }
}

/** 校验文件的 SHA-256（流式，不整包读进内存）。 */
internal fun sha256Of(file: File): String {
    val digest = MessageDigest.getInstance("SHA-256")
    file.inputStream().use { input ->
        val buffer = ByteArray(64 * 1024)
        while (true) {
            val read = input.read(buffer)
            if (read < 0) {
                break
            }
            digest.update(buffer, 0, read)
        }
    }
    return digest.digest().joinToString("") { "%02x".format(it) }
}

/** 已下载的包还能不能直接用：文件在、大小与服务端一致（大小未知时只看存在）。 */
internal fun cachedApkMatches(file: File?, asset: GitHubReleaseAsset): Boolean {
    if (file == null || !file.isFile) {
        return false
    }
    if (!file.name.equals(safeApkFileName(asset.name), ignoreCase = true)) {
        return false
    }
    return asset.sizeBytes <= 0L || file.length() == asset.sizeBytes
}

/**
 * APK 下载器：`HttpURLConnection` 流式写盘（仓库没有 OkHttp 依赖）。
 *
 * 只允许 GitHub 官方域名（[isTrustedGitHubUrl]），下载到调用方给的目录，
 * 先写 `.part` 再改名——中途失败不会留下一个"看起来完整"的包。
 */
internal class UpdateDownloader(private val directory: File) {
    @Volatile
    private var cancelled = false

    fun cancel() {
        cancelled = true
    }

    fun resetCancel() {
        cancelled = false
    }

    fun fileFor(asset: GitHubReleaseAsset): File = File(directory, safeApkFileName(asset.name))

    /**
     * 拉 `SHA256SUMS.txt` 并解析。Release 里没有校验文件和它下载失败不是一回事：
     * 这里失败就抛错（调用方据此拒绝安装未校验的包，而不是"假装校验过了"）。
     */
    suspend fun fetchSha256Sums(asset: GitHubReleaseAsset): Map<String, String> =
        withContext(Dispatchers.IO) {
            if (!isTrustedGitHubUrl(asset.downloadUrl)) {
                throw ManagementApiException("untrusted_url", "校验文件地址不是 GitHub 官方域名")
            }
            val connection = (URL(asset.downloadUrl).openConnection() as HttpURLConnection)
            connection.connectTimeout = 20_000
            connection.readTimeout = 30_000
            connection.requestMethod = "GET"
            connection.setRequestProperty("User-Agent", APP_UPDATE_USER_AGENT)
            try {
                val code = connection.responseCode
                if (code != HttpURLConnection.HTTP_OK) {
                    throw ManagementApiException("checksum_unavailable", "校验文件下载失败：HTTP $code")
                }
                val text = connection.inputStream.bufferedReader(Charsets.UTF_8).use { reader ->
                    val buffer = CharArray(8 * 1024)
                    val out = StringBuilder()
                    while (true) {
                        val n = reader.read(buffer)
                        if (n < 0) break
                        out.append(buffer, 0, n)
                        if (out.length > 2L * 1024L * 1024L) {
                            throw ManagementApiException("checksum_unavailable", "校验文件异常（超过 2MB）")
                        }
                    }
                    out.toString()
                }
                parseSha256Sums(text)
            } catch (e: ManagementApiException) {
                throw e
            } catch (_: IOException) {
                throw ManagementApiException("checksum_unavailable", "校验文件下载失败：网络不可用")
            } finally {
                connection.disconnect()
            }
        }

    /**
     * 下载 APK；`expectedSha256` 非空时校验（不匹配删文件并抛 `checksum_mismatch`）。
     * `onProgress(已下载字节, 总字节)`，总字节未知时为 -1。
     */
    suspend fun download(
        asset: GitHubReleaseAsset,
        expectedSha256: String?,
        onProgress: (downloaded: Long, total: Long) -> Unit,
    ): File = withContext(Dispatchers.IO) {
        if (!isTrustedGitHubUrl(asset.downloadUrl)) {
            throw ManagementApiException("untrusted_url", "下载地址不是 GitHub 官方域名，已拒绝")
        }
        if (!directory.exists() && !directory.mkdirs()) {
            throw ManagementApiException("io_error", "无法创建下载目录")
        }
        if (asset.sizeBytes > 0L) {
            // 留 32MB 余量：写盘还会产生临时文件与解压缓存，空间刚好够也会失败。
            val needed = asset.sizeBytes + 32L * 1024L * 1024L
            if (directory.usableSpace in 1 until needed) {
                throw ManagementApiException("no_space", "存储空间不足：需要约 ${formatBytesForHint(needed)}")
            }
        }
        cancelled = false
        val target = fileFor(asset)
        val part = File(directory, target.name + ".part")
        if (part.exists() && !part.delete()) {
            throw ManagementApiException("io_error", "无法清理上次残留的临时文件")
        }
        val connection = (URL(asset.downloadUrl).openConnection() as HttpURLConnection)
        connection.connectTimeout = 20_000
        connection.readTimeout = 60_000
        connection.instanceFollowRedirects = true
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/octet-stream")
        connection.setRequestProperty("User-Agent", APP_UPDATE_USER_AGENT)
        var downloaded = 0L
        try {
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                throw ManagementApiException(
                    "http_$code",
                    when (code) {
                        404 -> "安装包已不存在（HTTP 404），请重新检查更新"
                        in 500..599 -> "GitHub 服务端错误（HTTP $code），请稍后重试"
                        else -> "下载失败：HTTP $code"
                    },
                )
            }
            val total = connection.contentLengthLong.takeIf { it > 0L } ?: asset.sizeBytes
            connection.inputStream.use { input ->
                part.outputStream().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    var lastReported = 0L
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        if (cancelled) {
                            throw ManagementApiException("cancelled", "已取消下载")
                        }
                        val read = input.read(buffer)
                        if (read < 0) {
                            break
                        }
                        output.write(buffer, 0, read)
                        downloaded += read
                        if (downloaded - lastReported >= 128L * 1024L) {
                            lastReported = downloaded
                            onProgress(downloaded, total)
                        }
                    }
                    output.flush()
                }
            }
            onProgress(downloaded, total)
            if (total > 0L && downloaded != total) {
                throw ManagementApiException(
                    "incomplete_download",
                    "下载不完整：$downloaded / $total 字节",
                )
            }
            if (expectedSha256 != null) {
                val actual = sha256Of(part)
                if (!actual.equals(expectedSha256, ignoreCase = true)) {
                    throw ManagementApiException(
                        "checksum_mismatch",
                        "安装包校验失败（SHA-256 不一致），已删除，请重试",
                    )
                }
            }
            if (target.exists() && !target.delete()) {
                throw ManagementApiException("io_error", "无法覆盖旧安装包")
            }
            if (!part.renameTo(target)) {
                throw ManagementApiException("io_error", "无法保存安装包")
            }
            target
        } catch (e: ManagementApiException) {
            part.delete()
            throw e
        } catch (_: IOException) {
            part.delete()
            throw ManagementApiException("network_error", "下载中断：网络不可用")
        } finally {
            connection.disconnect()
        }
    }

    private fun formatBytesForHint(bytes: Long): String =
        String.format(Locale.US, "%.0f MB", bytes / 1024.0 / 1024.0)
}

/** 系统安装器与「安装未知应用」权限。 */
internal object ApkInstaller {
    /** Android 8.0+ 需要在系统设置里为本应用打开「安装未知应用」。 */
    fun canInstallPackages(context: Context): Boolean =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.packageManager.canRequestPackageInstalls()
        } else {
            true
        }

    fun unknownSourcesSettingsIntent(context: Context): Intent =
        Intent(
            Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
            Uri.parse("package:${context.packageName}"),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    /** @return false 表示系统里没有能处理安装 Intent 的 Activity（少见，需提示用户）。 */
    fun install(context: Context, apk: File): Boolean {
        if (!apk.isFile) {
            return false
        }
        val authority = "${context.packageName}.fileprovider"
        val uri = FileProvider.getUriForFile(context, authority, apk)
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        return runCatching {
            context.startActivity(intent)
            true
        }.getOrDefault(false)
    }
}

/** 更新卡的安装阶段（UI 状态机）。 */
enum class UpdateInstallPhase {
    /** 还没下载。 */
    IDLE,

    /** 正在下载。 */
    DOWNLOADING,

    /** 已下载，可安装。 */
    DOWNLOADED,

    /** 下载失败。 */
    FAILED,
}

/** 主按钮文案：跟着阶段走，不写"前往 GitHub"。 */
internal fun updateInstallActionLabel(phase: UpdateInstallPhase): String = when (phase) {
    UpdateInstallPhase.IDLE -> "下载并安装"
    UpdateInstallPhase.DOWNLOADING -> "下载中…"
    UpdateInstallPhase.DOWNLOADED -> "安装"
    UpdateInstallPhase.FAILED -> "重试下载"
}

/** 进度行：`下载中 45% · 12.3 MB / 27.0 MB`；总大小未知时只显示已下载。 */
internal fun updateDownloadProgressText(downloaded: Long, total: Long): String {
    val size = formatUpdateAssetSize(downloaded).ifBlank { "$downloaded B" }
    if (total <= 0L) {
        return "下载中 · 已下载 $size"
    }
    val percent = ((downloaded * 100L) / total).coerceIn(0L, 100L)
    return "下载中 $percent% · $size / ${formatUpdateAssetSize(total)}"
}

/** 下载失败的中文原因（错误码 → 可执行提示）。 */
internal fun updateDownloadErrorText(code: String, message: String?): String = when (code) {
    "cancelled" -> "已取消下载"
    "network_error" -> "网络不可用，下载中断；检查网络后重试"
    "no_space" -> message ?: "存储空间不足"
    "http_404" -> "安装包已不存在（HTTP 404），请重新检查更新"
    "checksum_mismatch" -> "安装包校验失败（SHA-256 不一致），已删除；请重试"
    "incomplete_download" -> "下载不完整；请重试"
    "untrusted_url" -> "下载地址不是 GitHub 官方域名，已拒绝"
    else -> message ?: "下载失败（$code）"
}
