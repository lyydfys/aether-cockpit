package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountSummary
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlin.math.roundToInt

private val clockFormat = DateTimeFormatter.ofPattern("HH:mm")

internal fun accountDisplayName(account: AccountSummary): String =
    account.label.ifBlank { "账号 ${account.id}" }

internal fun accountStatusText(
    account: AccountSummary,
    nowMs: Long,
    zone: ZoneId = ZoneId.systemDefault(),
): String {
    if (!account.enabled) {
        return "已停用，不参与路由"
    }
    if (account.restWhy.isNotEmpty() && account.restUntilMs > nowMs) {
        val reason = when (account.restWhy) {
            "quota" -> "额度用尽"
            "rate" -> "冷却中"
            else -> "休息中"
        }
        val clock = Instant.ofEpochMilli(account.restUntilMs).atZone(zone).format(clockFormat)
        return "$reason · $clock 恢复"
    }
    val error = account.quota?.error.orEmpty()
    if (error.isNotEmpty()) {
        return error
    }
    if (account.quota?.cooling == true) {
        return "冷却中"
    }
    if (account.active) {
        return "当前使用中"
    }
    return "可用"
}

internal fun accountQuotaBadge(account: AccountSummary): String {
    val quota = account.quota ?: return ""
    val window = quota.windows.firstOrNull()
    if (window != null) {
        val name = window.name.ifBlank { "额度" }
        return "$name ${windowPercent(window.used, window.amount, window.limit)}%"
    }
    if (quota.total > 0) {
        val unit = if (quota.unit.isEmpty()) "" else " ${quota.unit}"
        return "${quota.used} / ${quota.total}$unit"
    }
    return quota.plan
}

/** Mirrors the desktop usedPct rules: 0-1 fraction, 0-100 percentage, or amount/limit. */
internal fun windowPercent(used: Double, amount: Long?, limit: Long?): Int {
    val percent = when {
        amount != null && limit != null && limit > 0 -> amount.toDouble() / limit * 100
        used <= 1.0 -> used * 100
        else -> used
    }
    return percent.coerceIn(0.0, 100.0).roundToInt()
}
