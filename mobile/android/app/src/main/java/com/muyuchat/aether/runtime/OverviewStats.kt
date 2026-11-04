package com.muyuchat.aether.runtime

import java.util.Locale

/**
 * 概览页四张统计卡的数据。
 *
 * 两张卡来自 `/api/usage?period=today`，另外两张/一张来自 `/api/providers`。
 * 两个端点**分别失败**：一侧读不到只影响它自己的卡（值显示 `—`、说明写"读不到"），
 * 绝不退回「Stage N 接入」这种阶段占位——那是假信息。
 */
data class OverviewStats(
    val calls: String,
    val tokens: String,
    val cost: String,
    val accounts: String,
    /** 依次对应 今日请求 / 今日 Token / 今日成本 / 已启用账号。 */
    val captions: List<String>,
)

/** 与「用量」页完全一致的两个格式化函数（同一口径，不各写一套）。 */
internal fun formatUsageCount(value: Long): String = when {
    value >= 1_000_000 -> String.format(Locale.US, "%.1fM", value / 1_000_000.0)
    value >= 1_000 -> String.format(Locale.US, "%.1fK", value / 1_000.0)
    else -> value.toString()
}

internal fun formatUsageCost(value: Double): String =
    if (value <= 0.0) "$0" else String.format(Locale.US, "$%.4f", value)

private const val DASH = "—"

/**
 * @param usage `period=today` 的汇总；`null` 表示这次没读到
 * @param enabledAccounts 已启用（ready）账号数；`null` 表示这次没读到
 */
internal fun overviewStats(
    usage: UsageSummary?,
    enabledAccounts: Int?,
    usageError: String? = null,
    accountsError: String? = null,
): OverviewStats {
    val todayCaption = "今日"
    val usageMissing = usageError != null || usage == null
    val accountsMissing = accountsError != null || enabledAccounts == null

    val calls = if (usageMissing) DASH else formatUsageCount(usage!!.calls)
    // 与「用量」页一致：Token 只算输入 + 输出，不含缓存命中/写入。
    val tokens = if (usageMissing) DASH else formatUsageCount(usage!!.inputTokens + usage.outputTokens)
    val cost = if (usageMissing) DASH else formatUsageCost(usage!!.cost)
    val accounts = if (accountsMissing) DASH else (enabledAccounts ?: 0).toString()

    val readFail = "读不到"
    return OverviewStats(
        calls = calls,
        tokens = tokens,
        cost = cost,
        accounts = accounts,
        captions = listOf(
            if (usageMissing) readFail else todayCaption,
            if (usageMissing) readFail else todayCaption,
            if (usageMissing) readFail else todayCaption,
            if (accountsMissing) readFail else "已启用",
        ),
    )
}
