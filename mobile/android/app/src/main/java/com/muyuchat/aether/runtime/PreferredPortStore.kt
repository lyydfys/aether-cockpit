package com.muyuchat.aether.runtime

import android.content.Context
import org.json.JSONObject
import java.io.File

/**
 * 「首选端口」：手机端唯一能真实兑现的端口设置。
 *
 * 移动运行时从不注册 `Gateway.SetPortSwitcher`，所以 `POST /api/settings/port`
 * 在手机端必定返回 400「当前运行模式不支持热切换端口」。能做到的是：
 * 记住一个期望端口，**下次启动**把它交给 `nativeStart`
 * （`buildRuntimeConfig(port = …)`；Go 侧 `RuntimeConfig.Port == 0` 表示系统分配）。
 *
 * 存储放在 `noBackupFilesDir/aether/preferred-port.json`：端口不是凭据，
 * 但它属于"本机运行配置"，跟账号/设置放同一个不进云备份的目录更好解释。
 */
data class PreferredPortInput(
    val port: Int?,
    val error: String?,
)

/** 0 = 交给系统分配。存储缺失或损坏都回落到 0，绝不猜一个端口。 */
internal fun parsePreferredPort(json: String?): Int {
    if (json.isNullOrBlank()) {
        return 0
    }
    val raw = runCatching { JSONObject(json.trim()).opt("port") }.getOrNull() ?: return 0
    val port = when (raw) {
        is Int -> raw
        // JSONObject 把整数字面量解析成 Int 或 Long；小数/字符串一律不认。
        is Long -> if (raw in Int.MIN_VALUE..Int.MAX_VALUE) raw.toInt() else return 0
        else -> return 0
    }
    return if (port in 1..65535) port else 0
}

internal fun buildPreferredPortJson(port: Int): String =
    JSONObject().put("port", port.coerceIn(0, 65535)).toString()

/**
 * 校验用户输入。返回 [PreferredPortInput.port] 为可提交的值（0 = 系统分配），
 * 或 [PreferredPortInput.error] 为人类可读原因——两者互斥。
 */
internal fun validatePreferredPortInput(raw: String): PreferredPortInput {
    val text = raw.trim()
    if (text.isEmpty()) {
        return PreferredPortInput(port = 0, error = null)
    }
    val value = text.toIntOrNull()
    if (value == null || value !in 0..65535) {
        return PreferredPortInput(port = null, error = "端口必须是 1-65535 的数字")
    }
    // 显式 0 与空输入同义：交给系统分配。
    return PreferredPortInput(port = value, error = null)
}

/** 只做 IO；所有判定都在上面的纯函数里，方便脱离 Android 测试。 */
internal class PreferredPortStore(context: Context) {
    private val file = File(File(context.noBackupFilesDir, "aether"), "preferred-port.json")

    fun load(): Int = parsePreferredPort(
        runCatching { if (file.isFile) file.readText(Charsets.UTF_8) else null }.getOrNull(),
    )

    /** 返回实际写下的值（0 = 系统分配）。写失败会抛出，由调用方显示真实原因。 */
    fun save(port: Int): Int {
        val normalized = if (port in 1..65535) port else 0
        file.parentFile?.mkdirs()
        val temp = File(file.parentFile, "${file.name}.tmp")
        temp.writeText(buildPreferredPortJson(normalized), Charsets.UTF_8)
        // 原子替换：半写状态下次读回来会被当成损坏并回落到系统分配，
        // 但那样用户就失去了设置又不明白为什么，所以宁可先写临时文件再改名。
        if (!temp.renameTo(file)) {
            file.writeText(buildPreferredPortJson(normalized), Charsets.UTF_8)
            temp.delete()
        }
        return normalized
    }
}
