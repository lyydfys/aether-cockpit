package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountCheckin
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.AccountQuotaWindow
import com.muyuchat.aether.runtime.QUOTA_UNSUPPORTED_COPY
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号卡片的纯展示计算：额度百分比、无数据文案、签到口径、时间格式、搜索。
 *
 * 这些函数只吃 [AccountQuotaRow]（字段来自 `/api/accounts`），不碰 Compose 运行时，
 * 所以能在 JVM 单测里直接验证「不会出现 0%」「缺时间不显示」这类红线。
 */
class ChannelsAccountCardTest {
    private fun row(
        accountId: String = "acc-1",
        providerId: String = "claude-sub",
        label: String = "主力订阅",
        disabled: Boolean = false,
        resetAt: Long = 0L,
        lastUsed: Long = 0L,
        quotaSupported: Boolean = true,
        quotaError: String = "",
        checkinSupported: Boolean = false,
        checkin: AccountCheckin? = null,
        windows: List<AccountQuotaWindow> = emptyList(),
        total: Int = 0,
        used: Int = 0,
        unit: String = "",
        message: String = "",
        quotaReadAt: Long = 0L,
    ) = AccountQuotaRow(
        accountId = accountId,
        providerId = providerId,
        label = label,
        disabled = disabled,
        resetAt = resetAt,
        lastUsed = lastUsed,
        quotaSupported = quotaSupported,
        quotaError = quotaError,
        checkinSupported = checkinSupported,
        checkin = checkin,
        quotaWindows = windows,
        quotaTotal = total,
        quotaUsed = used,
        quotaUnit = unit,
        quotaMessage = message,
        quotaReadAt = quotaReadAt,
    )

    private fun window(name: String = "5 小时", used: Double = 37.5, resetsAt: Long = 0L) =
        AccountQuotaWindow(
            name = name,
            usedPercent = used,
            resetsAt = resetsAt,
            unit = "次",
            amount = 0L,
            limit = 0L,
        )

    @Test
    fun windowRemainingIsHundredMinusUsed() {
        assertEquals(63, windowRemainingPercent(window(used = 37.5)))
        assertEquals(100, windowRemainingPercent(window(used = 0.0)))
        assertEquals(0, windowRemainingPercent(window(used = 100.0)))
        // 越界值夹回区间：不显示负剩余，也不显示 >100%
        assertEquals(0, windowRemainingPercent(window(used = 150.0)))
        assertEquals(100, windowRemainingPercent(window(used = -30.0)))
    }

    @Test
    fun singlePercentNeedsATotal() {
        assertEquals(75, accountRemainingPercent(row(total = 200, used = 50)))
        assertEquals(0, accountRemainingPercent(row(total = 200, used = 260)))
        assertNull(accountRemainingPercent(row(total = 0, used = 0)))
    }

    /** 红线：`quota_supported == false` 只能写「该渠道不提供额度接口」，绝不能画成 0%。 */
    @Test
    fun unsupportedQuotaNeverRendersZeroPercent() {
        val unsupported = row(quotaSupported = false, total = 0, used = 0)
        assertEquals(QUOTA_UNSUPPORTED_COPY, accountQuotaNote(unsupported))
        // 支持额度、但真的读到 0% 时才允许出现 0
        val exhausted = row(quotaSupported = true, total = 100, used = 100)
        assertEquals(0, accountRemainingPercent(exhausted))
        assertNull(accountQuotaNote(exhausted))
    }

    @Test
    fun quotaNoteExplainsWhyThereIsNoBar() {
        // 有窗口 → 不再叠一句话
        assertNull(accountQuotaNote(row(windows = listOf(window()))))
        // 有 total/used → 有总条，也不叠
        assertNull(accountQuotaNote(row(total = 100, used = 40)))
        // 有错误原文 → 错误单独一行显示，不走"没有窗口"的旁白
        assertNull(accountQuotaNote(row(quotaError = "upstream 401")))
        // 服务端给了说明 → 原样用
        assertEquals("积分制渠道，无重置窗口", accountQuotaNote(row(message = "积分制渠道，无重置窗口")))
        // 支持额度、但一次都没读过 → 提示可以主动读一次（与"上游不提供"区分）
        assertTrue(accountQuotaNote(row())!!.contains("还没读过"))
        // 读过了但没有窗口 → 另一种说法，且带上读取时间
        val readButEmpty = accountQuotaNote(row(quotaReadAt = 1762290000L))!!
        assertTrue(readButEmpty.contains("上次读取没有返回可用窗口"))
    }

    @Test
    fun checkinCopyFollowsOutcome() {
        assertEquals("今日未签到", checkinLabel(null))
        assertEquals(
            "已签到 · +50 积分 · 连续 3 天",
            checkinLabel(
                AccountCheckin(day = "2026-11-04", outcome = "claimed", credit = 50.0, streak = 3, message = "", again = false),
            ),
        )
        assertEquals(
            "签到失败：上游 429",
            checkinLabel(
                AccountCheckin(day = "2026-11-04", outcome = "failed", credit = 0.0, streak = 0, message = "上游 429", again = false),
            ),
        )
        assertEquals("今天已签", checkinLabel(AccountCheckin("d", "done", 0.0, 0, "", false)))
        assertEquals("需在官方 App 签到", checkinLabel(AccountCheckin("d", "own-app", 0.0, 0, "", false)))
        assertEquals("今天没有活动", checkinLabel(AccountCheckin("d", "inactive", 0.0, 0, "", false)))
        assertEquals("今天不可签", checkinLabel(AccountCheckin("d", "ineligible", 0.0, 0, "", false)))
        assertEquals("签到状态：weird", checkinLabel(AccountCheckin("d", "weird", 0.0, 0, "", false)))
        // 小数积分不四舍五入成整数
        assertTrue(checkinLabel(AccountCheckin("d", "claimed", 50.5, 0, "", false)).contains("+50.5 积分"))
    }

    @Test
    fun settledCheckinDisablesTheButton() {
        val claimed = AccountCheckin("d", "claimed", 10.0, 1, "", false)
        val done = AccountCheckin("d", "done", 0.0, 0, "", false)
        val doneProvisional = AccountCheckin("d", "done", 0.0, 0, "", true)
        assertTrue(checkinSettled(claimed))
        assertTrue(checkinSettled(done))
        assertTrue(!checkinSettled(doneProvisional))
        assertTrue(!checkinSettled(null))

        assertEquals("已签到", checkinButtonLabel(claimed))
        assertEquals("再确认", checkinButtonLabel(doneProvisional))
        assertEquals("立即签到", checkinButtonLabel(null))
        assertEquals("立即签到", checkinButtonLabel(AccountCheckin("d", "failed", 0.0, 0, "x", false)))
    }

    @Test
    fun missingTimesAreHiddenInsteadOfInvented() {
        assertNull(accountTimeLabel(0L))
        assertNull(accountTimeLabel(-1L))
        val label = accountTimeLabel(1762290000L)
        assertTrue("label=$label", label != null && Regex("""\d{2}-\d{2} \d{2}:\d{2}""").matches(label))
    }

    @Test
    fun absoluteUsageNeedsBothNumbers() {
        assertEquals("1,234 / 10,000 次", absoluteUsage(1234L, 10000L, "次"))
        assertEquals("5 / 10", absoluteUsage(5L, 10L, ""))
        // 缺任一个绝对值就不显示（不能把 0 / 0 画成"用量 0"）
        assertEquals("", absoluteUsage(0L, 100L, "次"))
        assertEquals("", absoluteUsage(10L, 0L, "次"))
    }

    @Test
    fun filterAccountsMatchesLabelChannelAndId() {
        val accounts = listOf(
            row(accountId = "acc-1", providerId = "claude-sub", label = "主力订阅"),
            row(accountId = "acc-2", providerId = "trae-cn", label = "备用"),
        )
        assertEquals(listOf("acc-1"), filterAccounts(accounts, "主力").map { it.accountId })
        assertEquals(listOf("acc-2"), filterAccounts(accounts, "TRAE").map { it.accountId })
        assertEquals(listOf("acc-1"), filterAccounts(accounts, "acc-1").map { it.accountId })
        assertEquals(accounts, filterAccounts(accounts, "  "))
        assertTrue(filterAccounts(accounts, "没有这个").isEmpty())
    }
}
