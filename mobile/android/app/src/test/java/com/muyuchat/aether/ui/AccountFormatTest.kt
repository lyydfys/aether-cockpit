package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountQuotaSummary
import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.QuotaWindowSummary
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class AccountFormatTest {
    private val shanghai = ZoneId.of("Asia/Shanghai")

    private fun account(
        enabled: Boolean = true,
        active: Boolean = false,
        restWhy: String = "",
        restUntilMs: Long = 0L,
        quota: AccountQuotaSummary? = null,
        label: String = "主账号",
        id: String = "acct-1",
    ) = AccountSummary(id, label, enabled, active, restWhy, restUntilMs, quota)

    @Test
    fun statusPrefersDisabledThenRestThenQuotaErrorThenActive() {
        assertEquals("已停用，不参与路由", accountStatusText(account(enabled = false), 0L, shanghai))

        val restAt = Instant.parse("2026-11-02T00:30:00Z").toEpochMilli()
        assertEquals(
            "冷却中 · 08:30 恢复",
            accountStatusText(account(restWhy = "rate", restUntilMs = restAt), 1L, shanghai),
        )
        assertEquals(
            "额度用尽 · 08:30 恢复",
            accountStatusText(account(restWhy = "quota", restUntilMs = restAt), 1L, shanghai),
        )

        val failed = account(
            restUntilMs = restAt,
            quota = AccountQuotaSummary("", emptyList(), 0, 0, "", false, "凭证已失效"),
        )
        assertEquals("凭证已失效", accountStatusText(failed, restAt + 1, shanghai))

        assertEquals("当前使用中", accountStatusText(account(active = true), 0L, shanghai))
        assertEquals("可用", accountStatusText(account(), 0L, shanghai))
    }

    @Test
    fun quotaBadgePrefersWindowThenTotalThenPlan() {
        val windows = AccountQuotaSummary(
            plan = "Pro",
            windows = listOf(
                QuotaWindowSummary("5h", 0.42, null, null, "", ""),
                QuotaWindowSummary("7d", 10.0, 120, 500, "requests", ""),
            ),
            used = 0, total = 0, unit = "", cooling = false, error = "",
        )
        assertEquals("5h 42%", accountQuotaBadge(account(quota = windows)))

        val totals = windows.copy(windows = emptyList(), used = 120, total = 500, unit = "requests")
        assertEquals("120 / 500 requests", accountQuotaBadge(account(quota = totals)))

        val planOnly = windows.copy(windows = emptyList())
        assertEquals("Pro", accountQuotaBadge(account(quota = planOnly)))
        assertEquals("", accountQuotaBadge(account()))
    }

    @Test
    fun windowPercentAcceptsFractionPercentageAndAmounts() {
        assertEquals(42, windowPercent(0.42, null, null))
        assertEquals(42, windowPercent(42.0, null, null))
        assertEquals(24, windowPercent(0.0, 120, 500))
        assertEquals(3, windowPercent(3.0, null, null))
        assertEquals(100, windowPercent(150.0, null, null))
        assertEquals(0, windowPercent(-1.0, null, null))
        assertEquals(0, windowPercent(0.0, null, null))
    }

    @Test
    fun labelFallsBackToTheAccountId() {
        assertEquals("主账号", accountDisplayName(account()))
        assertEquals("账号 acct-7", accountDisplayName(account(label = "", id = "acct-7")))
    }
}
