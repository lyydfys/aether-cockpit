package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 概览下半部分两张卡（额度余量 / 渠道健康）的映射规则，逐条对齐桌面
 * `overview.js:546-609`：额度按剩余升序取前 6 条、渠道健康取前 8 条并区分成功/失败文案。
 *
 * 顺带钉住金额与重置时间的格式（桌面 `format.js:fmtCost` / `fmtReset` 同一套）。
 */
class OverviewExtrasTest {

    private fun window(
        name: String,
        used: Double = 0.0,
        amount: Long? = null,
        limit: Long? = null,
        resetsAt: String = "",
        pool: String = "",
        family: String = "",
    ) = QuotaWindow(name, used, amount, limit, resetsAt, pool, family)

    private fun group(provider: String, name: String = provider, vararg windows: QuotaWindow) =
        QuotaGroup(provider = provider, name = name, user = "", windows = windows.toList())

    @Test
    fun quotaRowsSortByRemainingAndKeepOnlyUsedWindows() {
        val rows = overviewQuotaRows(
            quotas = listOf(
                group(
                    "claude",
                    "Claude 订阅",
                    window("5 小时", used = 0.2),
                    window("周额度", used = 0.9),
                    window("没动过", used = 0.0),
                ),
            ),
            allowedProviders = setOf("claude"),
        )
        assertEquals(2, rows.size)
        // 剩余最少的排最前（桌面 rows.sort((a,b)=>a.rem-b.rem)）
        assertEquals("Claude 订阅 · 周额度", rows[0].name)
        assertEquals(10.0, rows[0].remainingPct, 0.0001)
        assertEquals("Claude 订阅 · 5 小时", rows[1].name)
        assertEquals(80.0, rows[1].remainingPct, 0.0001)
    }

    @Test
    fun quotaRowsDropFamilyDuplicateAndForeignProviders() {
        val rows = overviewQuotaRows(
            quotas = listOf(
                group(
                    "claude",
                    "Claude",
                    window("pool 总计", used = 0.5, pool = "Claude and GPT models"),
                    window("family 明细", used = 0.5, pool = "Claude and GPT models", family = "claude"),
                ),
                group("gemini", "Gemini", window("日额度", used = 0.1)),
            ),
            allowedProviders = setOf("claude", "gemini"),
        )
        // family 窗口与同池 pool 窗口重复 → 丢弃；pool 名按桌面映射成 Claude
        assertEquals(2, rows.size)
        assertEquals("Claude · Claude · pool 总计", rows[0].name)
        assertEquals("Gemini · 日额度", rows[1].name)

        val filtered = overviewQuotaRows(
            quotas = listOf(group("claude", "Claude", window("周", used = 0.5))),
            allowedProviders = setOf("other"),
        )
        assertTrue(filtered.isEmpty())
    }

    @Test
    fun quotaRowsUseAmountLimitWhenPresentAndCapAtSix() {
        val many = (1..9).map { index ->
            group("p$index", "渠道 $index", window("周", amount = index.toLong() * 10, limit = 100))
        }
        val rows = overviewQuotaRows(many, allowedProviders = many.map { it.provider }.toSet())
        assertEquals(6, rows.size)
        // amount/limit 优先：已用 90% 的排最前（剩余 10），前 6 条覆盖 10..60
        assertEquals(10.0, rows[0].remainingPct, 0.0001)
        assertEquals(60.0, rows[5].remainingPct, 0.0001)
    }

    @Test
    fun quotaResetFallsBackToRawTextWhenUnparseable() {
        assertEquals("", formatQuotaReset(""))
        // ISO 8601 解析成本地 MM/dd HH:mm；具体时刻随系统时区，形态必须稳定
        assertTrue(
            formatQuotaReset("2026-11-05T10:00:00+08:00"),
            Regex("""\d{2}/\d{2} \d{2}:\d{2}""").matches(formatQuotaReset("2026-11-05T10:00:00+08:00")),
        )
        // 认不出来的原样返回，不编时间
        assertEquals("下周一", formatQuotaReset("下周一"))
    }

    @Test
    fun quotaUsedPercentHandlesAllBackendShapes() {
        assertEquals(25.0, quotaUsedPercent(window("w", used = 0.25)), 0.0001)
        assertEquals(25.0, quotaUsedPercent(window("w", used = 25.0)), 0.0001)
        assertEquals(50.0, quotaUsedPercent(window("w", amount = 5, limit = 10)), 0.0001)
        // 桌面 format.js:usedPct：`raw <= 1 ? raw*100 : raw`——大于 1 就当已是百分数，
        // 所以 1.5 是 1.5%，不是 150%。这一档以前被误写成 100.0。
        assertEquals(1.5, quotaUsedPercent(window("w", used = 1.5)), 0.0001)
        // 真正的越界才收敛到 0–100
        assertEquals(100.0, quotaUsedPercent(window("w", used = 150.0)), 0.0001)
        assertEquals(0.0, quotaUsedPercent(window("w", used = -1.0)), 0.0001)
    }

    @Test
    fun healthRowsMirrorDesktopSuccessAndFailureText() {
        val rows = overviewHealthRows(
            entries = listOf(
                ChannelProbe("claude", "a1", "", "m", ok = true, code = 200, ms = 738, at = 1, error = ""),
                ChannelProbe("gemini", "a2", "主账号", "m", ok = false, code = 502, ms = 0, at = 2, error = "upstream timeout: connect refused after 10s"),
                ChannelProbe("other", "a3", "", "m", ok = true, code = 200, ms = 10, at = 3, error = ""),
            ),
            allowedProviders = setOf("claude", "gemini"),
            displayName = { id -> if (id == "claude") "Claude 官方" else id },
        )
        assertEquals(2, rows.size)
        assertEquals("Claude 官方", rows[0].name)
        assertEquals("200", rows[0].statusLabel)
        assertTrue(rows[0].ok)
        assertEquals("0.74 s", rows[0].detail)

        assertEquals("gemini · 主账号", rows[1].name)
        assertEquals("502", rows[1].statusLabel)
        assertTrue(!rows[1].ok)
        // 错误只截前 40 字（桌面 error.slice(0,40)）
        assertEquals(40, rows[1].detail.length)
    }

    @Test
    fun healthRowsCapAtEightAndTolerateEmptyCode() {
        val entries = (1..12).map { index ->
            ChannelProbe("p$index", "a", "", "m", ok = false, code = 0, ms = 0, at = 0, error = "boom")
        }
        val rows = overviewHealthRows(
            entries = entries,
            allowedProviders = entries.map { it.providerId }.toSet(),
            displayName = { it },
        )
        assertEquals(8, rows.size)
        // 没有 HTTP 码时不编数字，写「失败」
        assertEquals("失败", rows[0].statusLabel)
    }

    @Test
    fun costFormattingMatchesDesktopCurrencyRules() {
        assertEquals("$0", formatUsageAmount(0.0, "USD", 7.2))
        // 桌面 format.js:fmtCost：<0.01 六位、<1 四位、其余两位。0.0123 走四位那一档。
        assertEquals("$0.0123", formatUsageAmount(0.0123, "USD", 7.2))
        assertEquals("$0.001000", formatUsageAmount(0.001, "USD", 7.2))
        assertEquals("$0.5000", formatUsageAmount(0.5, "USD", 7.2))
        assertEquals("$12.00", formatUsageAmount(12.0, "USD", 7.2))
        // CNY 先乘汇率（桌面 fmtCost: currency==='CNY' ? usd*rate）；0.72 走 <1 的四位档
        assertEquals("¥0.7200", formatUsageAmount(0.1, "CNY", 7.2))
        assertEquals("¥72.00", formatUsageAmount(10.0, "CNY", 7.2))
        // 汇率非法时退回 7.2，不产出 NaN
        assertEquals("¥72.00", formatUsageAmount(10.0, "CNY", Double.NaN))
    }
}
