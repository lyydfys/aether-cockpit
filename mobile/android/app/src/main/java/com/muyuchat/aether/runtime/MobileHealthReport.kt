package com.muyuchat.aether.runtime

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * 本机自检摘要（**纯函数**）。
 *
 * 输入全部是**页面已经拿到的 DTO**（`GatewayStatus` + `SettingsSummary` + `SettingsSecurity`），
 * 不新发任何请求——这样"导出摘要"永远不会因为多打几个接口而拖慢或改变现场。
 *
 * 输出必须能安全地发给别人/贴到 issue 里，所以：
 * - **不含**密钥、令牌、调用方 key；
 * - **不含**绝对路径、账号标识（邮箱）、代理地址原文（代理只写"已设置/未设置"）；
 * - 局域网 URL 与监听地址是**本机回环/内网地址**，属于诊断必要信息，保留。
 */
data class HealthSnapshot(
    val generatedAtMs: Long,
    val appVersion: String,
    val running: Boolean,
    val listenAddr: String,
    val settings: SettingsSummary?,
    val security: SettingsSecurity?,
)

private val healthTimeFormat: DateTimeFormatter =
    DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss", java.util.Locale.US)

private fun healthTime(ms: Long): String =
    Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault()).format(healthTimeFormat)

/** 把快照渲染成一段可分享的纯文本。行序固定，便于 diff。 */
internal fun buildHealthReport(snapshot: HealthSnapshot): String {
    val lines = ArrayList<String>(24)
    lines += "Aether Cockpit 本机自检摘要"
    lines += "生成时间：${healthTime(snapshot.generatedAtMs)}"
    lines += "应用版本：${snapshot.appVersion.ifBlank { "未知" }}"
    lines += ""
    lines += "【网关】"
    lines += "状态：${if (snapshot.running) "运行中" else "未启动"}"
    lines += "监听地址：${snapshot.listenAddr.ifBlank { "未启动" }}"
    val settings = snapshot.settings
    if (settings == null) {
        lines += "配置：未读取"
    } else {
        lines += "首选端口：${settings.port}"
        lines += "局域网共享：${if (settings.lanEnabled) "开启" else "关闭"}"
        lines += "局域网地址：${if (settings.lanUrls.isEmpty()) "未设置" else settings.lanUrls.joinToString(", ")}"
        lines += "全局代理：${if (settings.proxyMasked.isBlank()) "未设置" else "已设置"}"
        lines += "CORS 允许来源：${if (settings.corsOrigins.isEmpty()) "未设置" else "${settings.corsOrigins.size} 个"}"
        lines += "IP 白名单：${if (settings.allowIPs.isEmpty()) "未设置" else "${settings.allowIPs.size} 个"}"
        lines += ""
        lines += "【脱敏】"
        lines += "总开关：${if (settings.redactEnabled) "开" else "关"}"
        lines += "个人字段：${if (settings.redactPersonal) "开" else "关"}"
        lines += "规则数：${settings.redactRuleCount} · 白名单：${settings.redactAllowCount}"
        lines += "动作：${if (settings.redactActions.isEmpty()) "未设置" else settings.redactActions.joinToString("/")}"
    }
    val security = snapshot.security
    if (security != null) {
        lines += ""
        lines += "【访问控制（只读判定）】"
        lines += "局域网共享配置位：${if (security.lan) "开" else "关"}（手机端只绑回环，开关不生效）"
    }
    lines += ""
    lines += "说明：本摘要只含计数与开关状态，不含密钥、令牌、账号标识或文件路径。"
    return lines.joinToString("\n")
}

/** 导出文件名：固定前缀 + 时间戳，避免覆盖。 */
internal fun healthReportFileName(nowMs: Long): String {
    val stamp = Instant.ofEpochMilli(nowMs).atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss", java.util.Locale.US))
    return "health-$stamp.txt"
}
