package com.muyuchat.aether.runtime

import java.util.Locale

/**
 * 概览页 KPI（与桌面 `overview.js:157-164` 的四格一一对应）。
 *
 * 桌面四格是：可用账号 / 今日请求 / 今日 Token / 会话，**成本不是独立的格**，
 * 它是「今日 Token」的注解之一（`overview.js:160-163`）。这里照抄这个层级，
 * 免得手机上把成本抬成主数字、与桌面读起来不是一张表。
 *
 * 口径（两页必须完全一致，否则同一台手机上会打架）：
 * - 请求数 = `summary.calls`，注解 `失败 N · 成功率 X%`
 * - Token  = `input + output + cache_read + cache_write`（桌面 `overview.js:148-149`）
 * - 会话   = `GET /api/sessions/stats` 的 `count`
 */
data class OverviewStats(
    val accounts: String,
    val calls: String,
    val tokens: String,
    val sessions: String,
    /** 顺序固定：可用账号 / 今日请求 / 今日 Token / 会话。 */
    val captions: List<String>,
    /** 快照时间（毫秒），聚合链路卡头部显示「更新于 HH:MM」。 */
    val updatedAtMs: Long = 0L,
)

private const val DASH = "—"
private const val READ_FAIL = "读不到"

/**
 * 与「用量」页完全一致的计数格式化（同一口径，不各写一套）。
 * 1000 → `1.0K`，1_000_000 → `1.0M`，其余原样。
 */
internal fun formatUsageCount(value: Long): String = when {
    value >= 1_000_000 -> String.format(Locale.US, "%.1fM", value / 1_000_000.0)
    value >= 1_000 -> String.format(Locale.US, "%.1fK", value / 1_000.0)
    else -> value.toString()
}

/**
 * 概览页的金额格式（桌面 `components.js` 的 `fmtMoney`：概览固定 USD，不跟随计价币种）。
 * 0 → `$0`；其余 4 位小数（小额不能被四舍五入成 0）。
 */
internal fun formatUsageCost(value: Double): String {
    val safe = if (value.isFinite()) value else 0.0
    return if (safe <= 0.0) "\$0" else String.format(Locale.US, "\$%.4f", safe)
}

/**
 * 测试渠道判定：与桌面 `overview.js:30` 的 `isTest()` 同一套规则
 * （`mock` 前缀 + `openai-compat`）。
 */
internal fun isTestProviderId(id: String): Boolean =
    id.startsWith("mock") || id == "openai-compat"

/** 成功率百分比；没有请求时返回 null（不写 0%，那是假事实）。 */
internal fun usageSuccessRatePercent(calls: Long, errors: Long): Double? {
    if (calls <= 0L) {
        return null
    }
    return (((calls - errors).toDouble() / calls.toDouble()) * 100.0).coerceIn(0.0, 100.0)
}

/** 缓存命中率百分比；分母（输入 + 缓存读 + 缓存写）为 0 时返回 null。 */
internal fun usageCacheHitRatePercent(input: Long, cacheRead: Long, cacheWrite: Long): Double? {
    val denominator = input + cacheRead + cacheWrite
    if (denominator <= 0L) {
        return null
    }
    return ((cacheRead.toDouble() / denominator.toDouble()) * 100.0).coerceIn(0.0, 100.0)
}

internal fun percentText(value: Double?): String =
    if (value == null) DASH else String.format(Locale.US, "%.1f%%", value)

/**
 * @param usage `period=today` 的汇总；`null` 表示这次没读到
 * @param providers `/api/providers` 列表；`null` 表示这次没读到
 * @param sessions `GET /api/sessions/stats` 的会话计数；`null` 表示这次没读到
 * @param nowMs 快照时间；测试可注入固定值
 */
internal fun overviewStats(
    usage: UsageSummary?,
    providers: List<ProviderSummary>?,
    sessions: Int?,
    usageError: String? = null,
    accountsError: String? = null,
    sessionsError: String? = null,
    nowMs: Long = System.currentTimeMillis(),
    /**
     * `/api/providers` 的账号口径汇总（`account_total` / `ready_total` /
     * `unloaded_accounts`）。为 null 时按老口径从渠道行自行推算。
     */
    accountTotals: ProviderAccountTotals? = null,
): OverviewStats {
    val usageMissing = usageError != null || usage == null
    val accountsMissing = accountsError != null || providers == null
    val sessionsMissing = sessionsError != null || sessions == null

    val readyAccounts = accountTotals?.readyTotal ?: (providers?.sumOf { it.readyCount } ?: 0)
    val realAccounts = providers?.filterNot { isTestProviderId(it.id) }?.sumOf { it.accountCount } ?: 0
    val testAccounts = providers?.filter { isTestProviderId(it.id) }?.sumOf { it.accountCount } ?: 0
    // 主数字是**磁盘真实账号数**，不是"进池能用的账号数"。
    // 渠道未注册时账号仍在 accounts.json 里：旧口径只数内存池，界面会显示 0，
    // 用户看到的就是"接入的渠道被清零了"。
    val unloadedAccounts = accountTotals?.unloaded?.size ?: 0
    val totalAccounts = accountTotals?.accountTotal ?: ((realAccounts + testAccounts) + unloadedAccounts)

    val calls = if (usageMissing) DASH else formatUsageCount(usage!!.calls)
    val inputTotal = if (usageMissing) 0L else {
        usage!!.inputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
    }
    val tokens = if (usageMissing) DASH else formatUsageCount(inputTotal + usage!!.outputTokens)
    val sessionsValue = if (sessionsMissing) DASH else formatUsageCount((sessions ?: 0).toLong())

    val accountsCaption = if (accountsMissing) {
        READ_FAIL
    } else {
        buildString {
            append("其中 ")
            append(readyAccounts)
            append(" 个可用")
            if (unloadedAccounts > 0) {
                // 渠道未注册的账号：明确说"未就绪"，而不是让它们从数字里消失。
                append(" · ")
                append(unloadedAccounts)
                append(" 个渠道未就绪")
            }
            append(" · ")
            append(realAccounts)
            append(" 个真实登录态 · ")
            append(testAccounts)
            append(" 个测试渠道")
        }
    }
    val callsCaption = if (usageMissing) {
        READ_FAIL
    } else {
        "失败 ${formatUsageCount(usage!!.errors)} · 成功率 " +
            percentText(usageSuccessRatePercent(usage.calls, usage.errors))
    }
    val tokensCaption = if (usageMissing) {
        READ_FAIL
    } else {
        val rate = usageCacheHitRatePercent(
            input = usage!!.inputTokens,
            cacheRead = usage.cacheReadTokens,
            cacheWrite = usage.cacheWriteTokens,
        )
        buildString {
            append("缓存 ")
            append(formatUsageCount(usage.cacheReadTokens))
            append(" · 命中 ")
            append(percentText(rate))
            append(" · 成本 ")
            append(formatUsageCost(usage.cost))
            if (usage.unpriced > 0L) {
                append(" · 未计价 ")
                append(formatUsageCount(usage.unpriced))
                append(" 次")
            }
        }
    }
    val sessionsCaption = if (sessionsMissing) READ_FAIL else "本机记录 · 只读"

    return OverviewStats(
        accounts = if (accountsMissing) DASH else totalAccounts.toString(),
        calls = calls,
        tokens = tokens,
        sessions = sessionsValue,
        captions = listOf(accountsCaption, callsCaption, tokensCaption, sessionsCaption),
        updatedAtMs = nowMs,
    )
}

/* ============================================================================
 * 额度余量（桌面 overview.js:563-609 的 quotaCard）
 * ========================================================================== */

/** 额度池显示名：桌面 `overview.js:9` 的 POOL 映射。 */
private val QUOTA_POOL_LABEL = mapOf(
    "Claude and GPT models" to "Claude",
    "Gemini Models" to "Gemini",
)

data class OverviewQuotaRow(
    val name: String,
    /** 剩余百分比（0–100，一位小数）。 */
    val remainingPct: Double,
    val resetNote: String,
    /**
     * 这条窗口属于哪个渠道（服务端 `/api/usage/quotas` 回包的 `provider` 字段，
     * 见 `usage_quota_api.go:59-61` 的 `quotaCard.Provider`）。
     *
     * 概览「聚合链路」按它过滤出单个渠道的窗口再算最低剩余（桌面 `overview.js:377`）。
     */
    val providerId: String = "",
)

/**
 * 额度行（与桌面同规则）：只保留「有已用量」的窗口；`family` 窗口在与同池 `pool`
 * 窗口重复时丢弃；按剩余升序取前 [limit] 条——桌面也是"最紧的排最前"。
 */
internal fun overviewQuotaRows(
    quotas: List<QuotaGroup>,
    allowedProviders: Set<String>,
    limit: Int = 6,
): List<OverviewQuotaRow> {
    val rows = mutableListOf<OverviewQuotaRow>()
    for (group in quotas) {
        if (group.provider !in allowedProviders) {
            continue
        }
        val poolRows = group.windows
            .filter { it.family.isEmpty() && it.pool.isNotEmpty() }
            .map { it.pool }
            .toSet()
        for (window in group.windows) {
            if (window.family.isNotEmpty() && window.pool.isNotEmpty() && window.pool in poolRows) {
                continue
            }
            val usedPct = quotaUsedPercent(window)
            if (usedPct <= 0.0) {
                continue
            }
            val remaining = ((100.0 - usedPct) * 10.0).let { kotlin.math.round(it) / 10.0 }
            val poolLabel = if (window.pool.isNotEmpty()) {
                QUOTA_POOL_LABEL[window.pool] ?: window.pool
            } else {
                ""
            }
            val name = buildString {
                append(group.name)
                append(" · ")
                if (poolLabel.isNotEmpty()) {
                    append(poolLabel)
                    append(" · ")
                }
                append(window.name)
            }
            rows += OverviewQuotaRow(
                name = name,
                remainingPct = remaining,
                resetNote = formatQuotaReset(window.resetsAt).ifEmpty { "未提供重置时间" },
                providerId = group.provider,
            )
        }
    }
    return rows.sortedBy { it.remainingPct }.take(limit)
}

/* ============================================================================
 * 渠道健康（桌面 overview.js:546-561 的 healthCard）
 * ========================================================================== */

data class OverviewHealthRow(
    val name: String,
    val statusLabel: String,
    val ok: Boolean,
    /** 成功时是耗时（`0.74 s`），失败时是错误前 40 字。 */
    val detail: String,
)

internal fun overviewHealthRows(
    entries: List<ChannelProbe>,
    allowedProviders: Set<String>,
    displayName: (String) -> String,
    limit: Int = 8,
): List<OverviewHealthRow> = entries
    .filter { it.providerId in allowedProviders }
    .take(limit)
    .map { probe ->
        val label = if (probe.label.isBlank()) "" else " · ${probe.label}"
        OverviewHealthRow(
            name = displayName(probe.providerId) + label,
            statusLabel = when {
                probe.ok -> (probe.code.takeIf { it > 0 } ?: 200).toString()
                probe.code > 0 -> probe.code.toString()
                else -> "失败"
            },
            ok = probe.ok,
            detail = if (probe.ok) {
                String.format(Locale.US, "%.2f s", probe.ms / 1000.0)
            } else {
                probe.error.take(40)
            },
        )
    }

/**
 * 概览页下半部分的两张卡（桌面 `overview.js:172` 的「额度余量 / 渠道健康」）。
 *
 * `null` 表示该卡这次没读到（页面据此显示"读不到"而不是空列表）；
 * 空列表是合法事实（真的没有可读窗口 / 真的还没探测过）。
 */
data class OverviewExtras(
    val quotas: List<OverviewQuotaRow>? = null,
    val quotaError: String? = null,
    val health: List<OverviewHealthRow>? = null,
    val healthCheckedAt: Long = 0L,
    val healthError: String? = null,
)
