package com.muyuchat.aether.runtime

import android.content.Context
import org.json.JSONObject
import java.io.File

/**
 * 「用户希望网关在跑」的持久化意图。
 *
 * 为什么需要它：网关是被 force-stop / 系统回收杀掉之后，Android **不会**自动把
 * 前台 Service 拉起来（`am force-stop` 会把包置为 stopped 状态，直到用户再次点开
 * App）。而手机上所有渠道/账号/设置页面读的都是 App 内 loopback 上的网关，
 * 网关没起来 → 列表全空 → 用户看起来就是"接入的渠道全没了"。
 *
 * 所以这里记一条"用户上次是主动开着网关的"意图，[com.muyuchat.aether.MainActivity]
 * 在 `onStart` 里据此静默恢复。**只在 App 进前台时判定**：不加 `BOOT_COMPLETED`
 * 之类的接收器，绝不在用户没打开 App 的时候偷偷起服务。
 *
 * 存储放在 `noBackupFilesDir/aether/gateway-wanted.json`，与 `lan-sharing.json` /
 * `preferred-port.json` 同款：属于"本机运行意图"，不进云备份。
 *
 * [failures] 是**连续**自动启动失败计数（端口占用、绑 0.0.0.0 失败等）。失败时保留
 * `wanted=true` 供下次再试，但连败达到 [AUTO_START_MAX_FAILURES] 次就把 `wanted`
 * 复位，避免每次开 App 都白试一遍。
 */
internal data class GatewayWanted(
    val wanted: Boolean = false,
    /** 连续自动启动失败次数；成功一次立刻清零。 */
    val failures: Int = 0,
)

/** 连续失败到这个次数就放弃自动恢复，把意图复位为"用户不想开"。 */
internal const val AUTO_START_MAX_FAILURES = 3

/** 判定"该不该在 App 进前台时自动恢复网关"。 */
internal fun shouldAutoStart(wanted: GatewayWanted, state: GatewayState): Boolean {
    if (!wanted.wanted) {
        return false
    }
    // RUNNING 无需重复启动；STARTING/STOPPING 是过渡态，别去抢
    // （start() 内部本来也会挡掉 RUNNING/STARTING，这里提前返回更干净）。
    return state == GatewayState.STOPPED || state == GatewayState.FAILED
}

/**
 * 一次自动启动尝试结束后的意图状态。
 *
 * - 起来了 → 清零失败计数，意图保持；
 * - 没起来 → 保留意图并 +1，**但达到上限就复位**，不无限重试。
 */
internal fun afterAutoStart(previous: GatewayWanted, running: Boolean): GatewayWanted =
    if (running) {
        GatewayWanted(wanted = true, failures = 0)
    } else if (!previous.wanted || previous.failures + 1 >= AUTO_START_MAX_FAILURES) {
        // 用户已经关掉（wanted=false）就不要再把意图"复活"；
        // 达到连败上限后也保持关闭，否则下一次失败会把 wanted 又翻回 true，形成无限重试。
        GatewayWanted(wanted = false, failures = 0)
    } else {
        GatewayWanted(wanted = true, failures = previous.failures + 1)
    }

// 只认真正的布尔与整数：字符串 "true" / 浮点一律当损坏，回落到安全默认（不自动启动）。
internal fun parseGatewayWanted(json: String?): GatewayWanted {
    if (json.isNullOrBlank()) {
        return GatewayWanted()
    }
    val root = runCatching { JSONObject(json.trim()) }.getOrNull() ?: return GatewayWanted()
    val wanted = root.opt("wanted") as? Boolean ?: false
    val failures = (root.opt("failures") as? Number)?.toInt()?.coerceIn(0, AUTO_START_MAX_FAILURES) ?: 0
    return GatewayWanted(wanted = wanted, failures = failures)
}

internal fun buildGatewayWantedJson(state: GatewayWanted): String =
    JSONObject()
        .put("wanted", state.wanted)
        .put("failures", state.failures.coerceIn(0, AUTO_START_MAX_FAILURES))
        .toString()

/** 只做 IO；判定在 [parseGatewayWanted] / [shouldAutoStart] 里，便于脱离 Android 测试。 */
internal class GatewayWantedStore(context: Context) {
    private val file = File(File(context.noBackupFilesDir, "aether"), "gateway-wanted.json")

    fun load(): GatewayWanted = parseGatewayWanted(
        runCatching { if (file.isFile) file.readText(Charsets.UTF_8) else null }.getOrNull(),
    )

    /** 原子替换：先写 `.tmp` 再改名，进程被杀不会留下半截 JSON。 */
    fun save(state: GatewayWanted) {
        val json = buildGatewayWantedJson(state)
        val parent = file.parentFile
        parent?.mkdirs()
        val temp = File(parent, "${file.name}.tmp")
        temp.writeText(json, Charsets.UTF_8)
        if (!temp.renameTo(file)) {
            file.writeText(json, Charsets.UTF_8)
            temp.delete()
        }
    }
}
