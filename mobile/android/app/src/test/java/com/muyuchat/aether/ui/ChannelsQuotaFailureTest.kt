package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.parseAccounts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度区那三行在三种状态下的样子：
 * 读取失败 → `读取失败：<quota_error 原文>`；成功 → `最近读数 刚刚`；没错误也没读过 → `最近读数 从未读过`。
 *
 * `quota_error` 是服务端原文（`readQuotaFor` 失败 → `noteQuotaFailure()` `usage_quota_api.go:466`
 * → 回包 `quota_error` `auth_api.go:349-351`，以及 `accountView.quota_error` `admin.go:390`），
 * 所以这里只做"原样贴出"，不做改写；**没有原文就不出这一行**（绝不自己编一句"读取失败"）。
 */
class ChannelsQuotaFailureTest {
    private val now = 1_762_300_000_000L

    /** 读取失败：服务端给了原文 → 原样带前缀显示。 */
    @Test
    fun failureRendersTheServerTextVerbatim() {
        val line = quotaReadFailureLabel("upstream 401: OAuth token revoked")
        assertEquals("读取失败：upstream 401: OAuth token revoked", line)
        // 原文一字不改（不摘要、不加括号、不翻译）
        assertTrue(line!!.contains("OAuth token revoked"))
    }

    /** 中文原文同样原样贴出；长文本也不截断到看不清（截断上限在解析层已经做过）。 */
    @Test
    fun chineseAndLongTextsAreNotRewritten() {
        val chinese = "读取额度失败：上游返回 429（当前账号在手机网络不可用）"
        assertEquals("读取失败：$chinese", quotaReadFailureLabel(chinese))
    }

    /** 没有原文（空 / 只有空白）→ 不出这一行，不编造。 */
    @Test
    fun blankErrorProducesNoLineAtAll() {
        assertNull(quotaReadFailureLabel(""))
        assertNull(quotaReadFailureLabel("   "))
        assertNull(quotaReadFailureLabel("\n\t"))
    }

    /** 成功：最近读数是相对时间，且没有"读取失败"行。 */
    @Test
    fun successShowsRelativeTimeAndNoFailureLine() {
        val readAtSec = (now - 5_000L) / 1000L
        val row = row(quotaReadAt = readAtSec, quotaError = "")
        assertEquals("刚刚", quotaReadAtLabel(row.quotaReadAt, now))
        assertNull(quotaReadFailureLabel(row.quotaError))
    }

    /** 没错误也没读过：只有「最近读数 从未读过」，没有"读取失败"行。 */
    @Test
    fun neverReadWithoutErrorOnlySaysNeverRead() {
        val row = row(quotaReadAt = 0L, quotaError = "")
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(row.quotaReadAt, now))
        assertNull(quotaReadFailureLabel(row.quotaError))
    }

    /** 失败但确实从来没读成功过：两行同时出现——这才分得清"点了失败"与"没点"。 */
    @Test
    fun failedAttemptKeepsNeverReadPlusTheReason() {
        val row = row(quotaReadAt = 0L, quotaError = "上游 500")
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(row.quotaReadAt, now))
        assertEquals("读取失败：上游 500", quotaReadFailureLabel(row.quotaError))
        // 两句说的是两件事，不能互相顶掉
        assertNotEquals(quotaReadAtLabel(row.quotaReadAt, now), quotaReadFailureLabel(row.quotaError))
    }

    /** 端到端：`/api/accounts` 的 `quota_error` 一路带到这张卡上。 */
    @Test
    fun accountPayloadCarriesQuotaErrorIntoTheLabel() {
        val rows = parseAccounts(
            """
            {"accounts":[{"account_id":"acc-1","quota_supported":true,"quota_error":"OAuth 已失效，需要重新登录"}]}
            """.trimIndent(),
        )
        assertEquals("读取失败：OAuth 已失效，需要重新登录", quotaReadFailureLabel(rows[0].quotaError))
        assertEquals(QUOTA_READ_NEVER_COPY, quotaReadAtLabel(rows[0].quotaReadAt, now))
    }

    private fun row(quotaReadAt: Long = 0L, quotaError: String = "") = AccountQuotaRow(
        accountId = "acc-1",
        providerId = "claude-sub",
        label = "主力订阅",
        disabled = false,
        resetAt = 0L,
        lastUsed = 0L,
        quotaSupported = true,
        quotaError = quotaError,
        checkinSupported = false,
        checkin = null,
        quotaWindows = emptyList(),
        quotaTotal = 0,
        quotaUsed = 0,
        quotaUnit = "",
        quotaMessage = "",
        quotaReadAt = quotaReadAt,
    )
}
