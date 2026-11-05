package com.muyuchat.aether.runtime

import android.content.Context
import org.json.JSONObject
import java.io.File

/**
 * 「局域网共享」偏好：手机端唯一真实的绑定开关。
 *
 * 语义（与 Go `internal/mobile.RuntimeConfig.ListenHost` 对齐）：
 *  - 关闭（默认）→ `listenHost = 127.0.0.1`，只服务本机；
 *  - 开启 → `listenHost = 0.0.0.0`，对话接口(`/v1`)与**管理接口(`/api`)**
 *    同时对同网络设备开放；管理面仍然要求 `Authorization: Bearer <AdminSession>`。
 *
 * 绑定发生在 `nativeStart`，所以改这个值必须重启一次网关运行时（不是热切换）。
 * 存储放在 `noBackupFilesDir/aether/lan-sharing.json`：它不是凭据，但属于
 * "本机运行配置"，和端口一样不进云备份更好解释。
 */
internal fun parseLanSharing(json: String?): Boolean {
    if (json.isNullOrBlank()) {
        return false
    }
    val raw = runCatching { JSONObject(json.trim()).opt("lanSharing") }.getOrNull() ?: return false
    // 只认真正的布尔：字符串 "true"/数字 1 一律当损坏，回落到关闭（安全默认）。
    return raw as? Boolean ?: false
}

internal fun buildLanSharingJson(on: Boolean): String =
    JSONObject().put("lanSharing", on).toString()

/** 启动时交给 Go 的监听 host：只有这两个值被接受。 */
internal const val LOOPBACK_LISTEN_HOST = "127.0.0.1"
internal const val LAN_LISTEN_HOST = "0.0.0.0"

internal fun lanListenHost(enabled: Boolean): String =
    if (enabled) LAN_LISTEN_HOST else LOOPBACK_LISTEN_HOST

/** 只做 IO；判定在 [parseLanSharing] 里，便于脱离 Android 测试。 */
internal class LanSharingStore(context: Context) {
    private val file = File(File(context.noBackupFilesDir, "aether"), "lan-sharing.json")

    fun load(): Boolean = parseLanSharing(
        runCatching { if (file.isFile) file.readText(Charsets.UTF_8) else null }.getOrNull(),
    )

    fun save(on: Boolean) {
        file.parentFile?.mkdirs()
        val temp = File(file.parentFile, "${file.name}.tmp")
        temp.writeText(buildLanSharingJson(on), Charsets.UTF_8)
        if (!temp.renameTo(file)) {
            file.writeText(buildLanSharingJson(on), Charsets.UTF_8)
            temp.delete()
        }
    }
}
