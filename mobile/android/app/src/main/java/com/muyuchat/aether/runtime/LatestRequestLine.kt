package com.muyuchat.aether.runtime

/**
 * 概览页的「最近一次模型请求」。
 *
 * 目的：让用户在 App 里直接看到**这台手机的网关到底有没有被客户端用过**，
 * 而不是只能翻文档或去请求页数条目。
 *
 * 口径：
 * - 没有记录 → 「还没有客户端连过本机网关」（陈述事实，不写"未验证/未知"）；
 * - 读不到 → 明说读不到（错误码由调用方另行展示）；
 * - 有记录 → 取**时间最新的一条**，失败也要如实显示失败状态与错误类型。
 */
internal fun latestRequestLine(rows: List<RequestRow>, nowMs: Long): String {
    val latest = rows.maxByOrNull { it.timeMs } ?: return "还没有客户端连过本机网关"
    val whenText = relativeTimeLabel(nowMs, latest.timeMs)
    val model = latest.model.ifBlank { "未知模型" }
    val outcome = when {
        latest.ok -> "成功"
        latest.status > 0 -> "失败（HTTP ${latest.status}）"
        else -> "失败"
    }
    val error = if (!latest.ok && latest.errorType.isNotBlank()) " · ${latest.errorType}" else ""
    return "最近一次：$whenText · $model · $outcome$error"
}

/** 读不到时的文案：不冒充"没有请求"。 */
internal fun latestRequestUnavailableLine(): String = "读不到请求记录"
