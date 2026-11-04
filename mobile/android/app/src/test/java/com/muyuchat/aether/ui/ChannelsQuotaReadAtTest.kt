package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.parseAccounts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 卡片额度区的「最近读数」一行。
 *
 * 数值来自 `quota.read_at`（`internal/provider/contract.go:249`，服务端用
 * `time.Now().Unix()` 写入 → **Unix 秒**）。这一行是"点一次「读取额度」真的读到了"的直接证据，
 * 所以宁可写「从未读过」，也**不许编一个时间**。
 */
class ChannelsQuotaReadAtTest {
    /** 固定"现在"，相对时间才可断言。 */
    private val now = 1_762_300_000_000L

    private fun secondsAgo(seconds: Long): Long = (now - seconds * 1000L) / 1000L

    @Test
    fun zeroMeansNeverRead() {
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(readAtSec = 0L, nowMs = now))
        assertEquals("从未读过", QUOTA_READ_NEVER_COPY)
    }

    @Test
    fun missingOrBrokenValuesAlsoMeanNeverRead() {
        // 负数/脏值不能被当成"1970 年某时刻"显示成天前
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(readAtSec = -1L, nowMs = now))
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(readAtSec = Long.MIN_VALUE, nowMs = now))
    }

    @Test
    fun freshReadShowsJustNowThenMinutes() {
        assertEquals("刚刚", quotaReadAtLabel(secondsAgo(5), now))
        assertEquals("刚刚", quotaReadAtLabel(secondsAgo(59), now))
        assertEquals("5 分钟前", quotaReadAtLabel(secondsAgo(300), now))
        assertEquals("59 分钟前", quotaReadAtLabel(secondsAgo(59 * 60), now))
        assertEquals("2 小时前", quotaReadAtLabel(secondsAgo(2 * 3600), now))
        assertEquals("3 天前", quotaReadAtLabel(secondsAgo(3 * 86_400), now))
    }

    /** 复用 `relativeTimeLabel`：相对时间只应有一套算法。 */
    @Test
    fun matchesTheSharedRelativeFormatter() {
        val readAtSec = secondsAgo(4 * 3600)
        assertEquals(
            com.muyuchat.aether.runtime.relativeTimeLabel(now, readAtSec * 1000L),
            quotaReadAtLabel(readAtSec, now),
        )
    }

    /** 上游时钟比手机快一点点（read_at 在未来）时按「刚刚」处理，不抛出、不显示负数。 */
    @Test
    fun futureSkewIsStillJustNow() {
        assertEquals("刚刚", quotaReadAtLabel(readAtSec = now / 1000L + 30L, nowMs = now))
    }

    /** 端到端：`/api/accounts` 里 `quota` 缺失或没有 `read_at` → 0 → 「从未读过」。 */
    @Test
    fun accountsWithoutReadAtAreNeverRead() {
        val rows = parseAccounts(
            """
            {"accounts":[{"account_id":"acc-1","quota_supported":true},
                         {"account_id":"acc-2","quota_supported":true,"quota":{"total":100,"used":10}},
                         {"account_id":"acc-3","quota_supported":true,"quota":{"read_at":1762290000,"windows":[]}}]}
            """.trimIndent(),
        )
        assertEquals(0L, rows[0].quotaReadAt)
        assertEquals(0L, rows[1].quotaReadAt)
        assertEquals(1762290000L, rows[2].quotaReadAt)
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(rows[0].quotaReadAt, now))
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(rows[1].quotaReadAt, now))
        // 有读数的那个不再是"从未读过"
        assertNotEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(rows[2].quotaReadAt, now))
        assertTrue(quotaReadAtLabel(rows[2].quotaReadAt, now).isNotBlank())
    }
}
