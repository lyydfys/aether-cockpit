package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.QUOTA_UNSUPPORTED_COPY
import com.muyuchat.aether.runtime.parseQuotaRead
import com.muyuchat.aether.runtime.withQuotaRead
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 单卡「读取额度」入口的**文案与可用性**：
 * - 三种空态必须说三件不同的事（不提供接口 / 没读过 / 读过但没窗口）；
 * - 读取中按钮禁用且文案变「读取中…」；
 * - 读取成功后百分比条能直接用回包快照画出来。
 */
class ChannelsQuotaReadTest {
    /** 固定"现在"，相对时间才是确定的。 */
    private val NOW_MS = 1_762_300_000_000L

    private fun row(
        accountId: String = "acc-1",
        quotaSupported: Boolean = true,
        quotaReadAt: Long = 0L,
        windows: List<com.muyuchat.aether.runtime.AccountQuotaWindow> = emptyList(),
    ) = AccountQuotaRow(
        accountId = accountId,
        providerId = "claude-sub",
        label = "主力订阅",
        disabled = false,
        resetAt = 0L,
        lastUsed = 0L,
        quotaSupported = quotaSupported,
        quotaError = "",
        checkinSupported = false,
        checkin = null,
        quotaWindows = windows,
        quotaTotal = 0,
        quotaUsed = 0,
        quotaUnit = "",
        quotaMessage = "",
        quotaReadAt = quotaReadAt,
    )

    @Test
    fun unsupportedAndNeverReadAreDifferentSentences() {
        // ① 渠道没有额度接口
        val unsupported = accountQuotaNote(row(quotaSupported = false))!!
        assertEquals(QUOTA_UNSUPPORTED_COPY, unsupported)
        assertTrue(unsupported.contains("接口"))
        // ② 支持额度但没读过 → 额度区由「最近读数 从未读过」表达，不再叠一句说明
        assertNull(accountQuotaNote(row(quotaSupported = true, quotaReadAt = 0L)))
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(readAtSec = 0L, nowMs = NOW_MS))
        // ③ 读过了但上游没给窗口 → 另一种说法
        val readButEmpty = accountQuotaNote(row(quotaSupported = true, quotaReadAt = 1_762_290_000L))!!
        assertTrue(readButEmpty, readButEmpty.contains("上次读取没有返回可用窗口"))
        // 「不提供接口」与「从未读过」绝不是同一句
        assertTrue(unsupported != quotaReadAtLabel(readAtSec = 0L, nowMs = NOW_MS))
    }

    @Test
    fun readButtonLabelOnlyChangesWhileReading() {
        assertEquals("读取额度", quotaReadLabel(false))
        assertEquals("读取中…", quotaReadLabel(true))
    }

    @Test
    fun readButtonIsDisabledWhileAnyAccountIsBusy() {
        // 空闲 + 渠道有额度接口 → 可点
        assertTrue(quotaReadEnabled(quotaSupported = true, anyBusy = false))
        // 别的账号正在读取 → 这一张也要禁用（同一时刻只允许一个账号在读取）
        assertFalse(quotaReadEnabled(quotaSupported = true, anyBusy = true))
        // 渠道没有额度接口 → 根本不给按钮
        assertFalse(quotaReadEnabled(quotaSupported = false, anyBusy = false))
    }

    /** 成功回包 → 窗口解析 → 百分比条 → 卡片不再显示"没有窗口"。 */
    @Test
    fun successfulReadFeedsTheBarAndClearsTheEmptyState() {
        val read = parseQuotaRead(
            "acc-1",
            """
            {"quota_supported":true,"quota_read":true,
             "quota":{"read_at":1762400000,"windows":[{"name":"5 小时","used":37.5}]}}
            """.trimIndent(),
        )
        val snapshot = read.snapshot
        assertTrue(snapshot != null && snapshot.windows.isNotEmpty())
        // 37.5% 已用 → 剩余 63%，条与数字都直接用回包
        assertEquals(63, windowRemainingPercent(snapshot!!.windows[0]))

        val merged = row().withQuotaRead(read)
        assertNull(accountQuotaNote(merged))
        assertEquals(1762400000L, merged.quotaReadAt)
        assertTrue(merged.quotaWindows.isNotEmpty())
    }
}
