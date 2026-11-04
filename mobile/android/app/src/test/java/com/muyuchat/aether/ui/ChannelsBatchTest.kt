package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountCheckin
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.AccountQuotaWindow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度页工具条（筛选芯片）与批量选择/汇总的纯逻辑。
 *
 * 这些函数只吃 `/api/accounts` 的行数据，不碰 Compose，也不发请求；
 * 「成功 N / 失败 M」的文案只由回包计数决定，因此可以逐字钉住。
 */
class ChannelsBatchTest {
    private fun row(
        accountId: String,
        providerId: String,
        label: String,
        disabled: Boolean = false,
        quotaSupported: Boolean = true,
        windows: List<AccountQuotaWindow> = emptyList(),
        total: Int = 0,
        used: Int = 0,
        checkinSupported: Boolean = false,
        checkin: AccountCheckin? = null,
    ) = AccountQuotaRow(
        accountId = accountId,
        providerId = providerId,
        label = label,
        disabled = disabled,
        resetAt = 0L,
        lastUsed = 0L,
        quotaSupported = quotaSupported,
        quotaError = "",
        checkinSupported = checkinSupported,
        checkin = checkin,
        quotaWindows = windows,
        quotaTotal = total,
        quotaUsed = used,
        quotaUnit = "",
        quotaMessage = "",
    )

    private val window = AccountQuotaWindow(
        name = "5 小时",
        usedPercent = 20.0,
        resetsAt = 0L,
        unit = "次",
        amount = 0L,
        limit = 0L,
    )

    // 四类样本：有窗口的启用号 / 只有总量的停用号 / 不提供额度的启用号 / 不提供额度的停用号
    private val a1 = row("acc-1", "claude-sub", "主力订阅", windows = listOf(window), checkinSupported = true)
    private val a2 = row(
        "acc-2",
        "trae-cn",
        "备用",
        disabled = true,
        total = 100,
        used = 40,
        checkinSupported = true,
    )
    private val a3 = row("acc-3", "workbuddy-cn", "签到号", quotaSupported = false, checkinSupported = true)
    private val a4 = row("acc-4", "qoder-cn", "老号", disabled = true, quotaSupported = false)
    private val all = listOf(a1, a2, a3, a4)

    @Test
    fun chipLabelsMatchTheSpec() {
        assertEquals("全部", accountFilterLabel(AccountFilter.ALL))
        assertEquals("仅启用", accountFilterLabel(AccountFilter.ENABLED))
        assertEquals("仅停用", accountFilterLabel(AccountFilter.DISABLED))
        assertEquals("有额度窗口", accountFilterLabel(AccountFilter.WITH_QUOTA))
    }

    @Test
    fun eachFilterReturnsItsOwnSlice() {
        assertEquals(listOf("acc-1", "acc-2", "acc-3", "acc-4"), ids(filterAccountRows(all, AccountFilter.ALL, "")))
        assertEquals(listOf("acc-1", "acc-3"), ids(filterAccountRows(all, AccountFilter.ENABLED, "")))
        assertEquals(listOf("acc-2", "acc-4"), ids(filterAccountRows(all, AccountFilter.DISABLED, "")))
        // 「有额度窗口」只认真的带回窗口读数的账号：acc-2 有 total/used 但没有窗口，不算
        assertEquals(listOf("acc-1"), ids(filterAccountRows(all, AccountFilter.WITH_QUOTA, "")))
    }

    @Test
    fun filterAndSearchStack() {
        // 仅启用 + 搜渠道 id
        assertEquals(listOf("acc-3"), ids(filterAccountRows(all, AccountFilter.ENABLED, "workbuddy")))
        // 仅停用 + 搜 label
        assertEquals(listOf("acc-2"), ids(filterAccountRows(all, AccountFilter.DISABLED, "备用")))
        // 两个条件都过不了 → 空
        assertTrue(filterAccountRows(all, AccountFilter.ENABLED, "备用").isEmpty())
        assertTrue(filterAccountRows(all, AccountFilter.WITH_QUOTA, "qoder").isEmpty())
        // 搜索为空串时只剩芯片条件
        assertEquals(4, filterAccountRows(all, AccountFilter.ALL, "   ").size)
    }

    @Test
    fun selectionTogglesAndSelectsAllVisible() {
        assertEquals(listOf("acc-1"), toggleSelection(emptyList(), "acc-1"))
        assertEquals(listOf("acc-1", "acc-2"), toggleSelection(listOf("acc-1"), "acc-2"))
        assertEquals(listOf("acc-2"), toggleSelection(listOf("acc-1", "acc-2"), "acc-1"))
        // 同一个 id 先加后减回到空集，不会留下重复项
        assertEquals(emptyList<String>(), toggleSelection(toggleSelection(emptyList(), "acc-1"), "acc-1"))

        val visible = filterAccountRows(all, AccountFilter.ENABLED, "")
        assertFalse(allVisibleSelected(emptyList(), visible))
        assertTrue(allVisibleSelected(listOf("acc-1", "acc-3"), visible))
        // 选中项被筛选隐藏后，"已选"只算可见的那些
        assertEquals(listOf("acc-1"), visibleSelection(listOf("acc-1", "acc-2"), visible))
        assertEquals(emptyList<String>(), visibleSelection(listOf("acc-2"), visible))
        // 空列表不算"全选"
        assertFalse(allVisibleSelected(listOf("acc-1"), emptyList()))
    }

    @Test
    fun batchCheckinOnlyTargetsCheckinCapableAccounts() {
        val enabled = filterAccountRows(all, AccountFilter.ENABLED, "")
        // acc-3 支持签到、acc-1 也支持 → 两个
        assertEquals(listOf("acc-1", "acc-3"), batchCheckinTargets(enabled, ids(enabled)))
        // 只选了一个不支持签到的账号 → 没有目标
        val disabledOnly = filterAccountRows(all, AccountFilter.DISABLED, "")
        assertEquals(emptyList<String>(), batchCheckinTargets(disabledOnly, listOf("acc-4")))
        // acc-2 支持签到 → 一个
        assertEquals(listOf("acc-2"), batchCheckinTargets(disabledOnly, listOf("acc-2", "acc-4")))
        // 没有目标时给出的文案必须说清"不会发请求"
        assertTrue(batchCheckinEmptyHint().contains("不支持签到"))
        assertTrue(batchCheckinEmptyHint().contains("不会发任何请求"))
    }

    @Test
    fun batchLabelsComeFromTheSameSource() {
        assertEquals("刷新额度", batchKindLabel(BatchKind.REFRESH))
        assertEquals("批量启用", batchKindLabel(BatchKind.ENABLE))
        assertEquals("批量停用", batchKindLabel(BatchKind.DISABLE))
        assertEquals("一键签到", batchKindLabel(BatchKind.CHECKIN))
    }

    /** 全成功也只写「成功 N / 总数」，不出现"全部成功"这种无法核实的断言。 */
    @Test
    fun allSucceededSummaryStillShowsCounts() {
        val text = batchSummaryText(BatchKind.REFRESH, ok = 3, failed = 0, total = 3, firstError = "")
        assertEquals("刷新额度：成功 3 / 3", text)
        assertTrue(!text.contains("全部成功"))
    }

    @Test
    fun partialFailureSummaryCarriesTheFirstReason() {
        val text = batchSummaryText(
            BatchKind.ENABLE,
            ok = 2,
            failed = 1,
            total = 3,
            firstError = "账号不存在: acc-9",
        )
        assertEquals("批量启用：成功 2 / 3，失败 1（第一个失败原因：账号不存在: acc-9）", text)
    }

    @Test
    fun allFailedSummaryLeadsWithFailureCount() {
        val text = batchSummaryText(BatchKind.CHECKIN, ok = 0, failed = 3, total = 3, firstError = "上游 429")
        assertEquals("一键签到：失败 3 / 3（第一个失败原因：上游 429）", text)
        // 没有原因时也不能出现空的括号
        assertEquals(
            "批量停用：失败 2 / 2",
            batchSummaryText(BatchKind.DISABLE, ok = 0, failed = 2, total = 2, firstError = ""),
        )
    }

    @Test
    fun emptyBatchHasItsOwnCopy() {
        assertEquals(
            "刷新额度：没有可执行的账号",
            batchSummaryText(BatchKind.REFRESH, ok = 0, failed = 0, total = 0, firstError = ""),
        )
    }

    private fun ids(rows: List<AccountQuotaRow>): List<String> = rows.map { it.accountId }
}
