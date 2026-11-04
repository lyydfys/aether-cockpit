package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 单账号「读一次额度」的契约。
 *
 * 端点：`POST /api/accounts/{id}/refresh`（`gateway.go:1116` → `auth_api.go:290-353`
 * `handleTokenRefresh`）。回包恒带 `quota_supported / quota_read / quota`，
 * 额度读失败时带 `quota_error`，凭据续期失败时带 `warning`（`auth_api.go:339-352`）。
 */
class AccountQuotaReadTest {
    @Test
    fun parsesSuccessfulReadWithWindows() {
        val read = parseQuotaRead(
            "acc-1",
            """
            {"refreshed":true,"refresh_supported":true,
             "quota":{"total":0,"used":0,"remaining":0,"read_at":1762300000,
                      "windows":[{"name":"5 小时","used":37.5,"resets_at":1762303600,"unit":"次"}]},
             "quota_supported":true,"quota_read":true}
            """.trimIndent(),
        )
        assertEquals("acc-1", read.accountId)
        assertTrue(read.quotaSupported)
        assertTrue(read.quotaRead)
        assertEquals("", read.quotaError)
        assertEquals("", read.warning)
        val snapshot = read.snapshot
        assertEquals(1, snapshot!!.windows.size)
        assertEquals("5 小时", snapshot.windows[0].name)
        assertEquals(37.5, snapshot.windows[0].usedPercent, 0.001)
        assertEquals(1762303600L, snapshot.windows[0].resetsAt)
        // read_at 是"读过了"的证据，卡片据此区分「没读过」与「读了没窗口」
        assertEquals(1762300000L, snapshot.readAt)
        assertNull(quotaReadFailure(read))
    }

    @Test
    fun parsesUpstreamFailureWithTheOriginalText() {
        val read = parseQuotaRead(
            "acc-1",
            """{"refreshed":false,"refresh_supported":false,"quota":null,
                "quota_supported":true,"quota_read":false,"quota_error":"上游 429"}""",
        )
        assertTrue(read.quotaSupported)
        assertTrue(!read.quotaRead)
        assertEquals("上游 429", read.quotaError)
        assertNull(read.snapshot)
        // 失败原因必须原样到界面上，错误码固定为 quota_read_failed
        val failure = quotaReadFailure(read)
        assertEquals("quota_read_failed", failure?.first)
        assertEquals("上游 429", failure?.second)
    }

    @Test
    fun parsesUnsupportedChannelWithoutPretending() {
        val read = parseQuotaRead(
            "acc-2",
            """{"refreshed":false,"refresh_supported":false,"quota_supported":false,"quota_read":false}""",
        )
        assertTrue(!read.quotaSupported)
        assertTrue(!read.quotaRead)
        val failure = quotaReadFailure(read)
        assertEquals("quota_unsupported", failure?.first)
        assertEquals(QUOTA_UNSUPPORTED_COPY, failure?.second)
        // 「不提供额度接口」与「没读过额度」必须是两句不同的话
        assertTrue(QUOTA_UNSUPPORTED_COPY.contains("接口"))
    }

    @Test
    fun credentialWarningSurfacesEvenWhenQuotaWasRead() {
        val read = parseQuotaRead(
            "acc-3",
            """
            {"refreshed":false,"refresh_supported":true,
             "quota":{"read_at":1762300000,"windows":[]},
             "quota_supported":true,"quota_read":true,
             "warning":"凭据续期失败：invalid_grant"}
            """.trimIndent(),
        )
        assertTrue(read.quotaRead)
        assertEquals("凭据续期失败：invalid_grant", read.warning)
        val failure = quotaReadFailure(read)
        assertEquals("credential_refresh_warning", failure?.first)
        assertEquals("凭据续期失败：invalid_grant", failure?.second)
    }

    @Test
    fun snapshotIsMergedOntoTheRow() {
        val row = AccountQuotaRow(
            accountId = "acc-1",
            providerId = "claude-sub",
            label = "主力订阅",
            disabled = false,
            resetAt = 0L,
            lastUsed = 0L,
            quotaSupported = true,
            quotaError = "上一次的错误",
            checkinSupported = false,
            checkin = null,
            quotaWindows = emptyList(),
            quotaTotal = 0,
            quotaUsed = 0,
            quotaUnit = "",
            quotaMessage = "",
            quotaReadAt = 0L,
        )
        val read = parseQuotaRead(
            "acc-1",
            """
            {"quota_supported":true,"quota_read":true,
             "quota":{"total":200,"used":50,"unit":"次","read_at":1762400000,
                      "windows":[{"name":"每周","used":25}]}}
            """.trimIndent(),
        )
        val merged = row.withQuotaRead(read)
        assertEquals(1, merged.quotaWindows.size)
        assertEquals("每周", merged.quotaWindows[0].name)
        assertEquals(200, merged.quotaTotal)
        assertEquals(50, merged.quotaUsed)
        assertEquals("次", merged.quotaUnit)
        assertEquals(1762400000L, merged.quotaReadAt)
        // 读取成功 → 旧的 quota_error 必须被清掉，而不是继续挂着
        assertEquals("", merged.quotaError)
    }

    @Test
    fun unsupportedReadFlipsTheRowFlag() {
        val row = AccountQuotaRow(
            accountId = "acc-9",
            providerId = "workbuddy-cn",
            label = "签到号",
            disabled = false,
            resetAt = 0L,
            lastUsed = 0L,
            quotaSupported = true,
            quotaError = "",
            checkinSupported = true,
            checkin = null,
            quotaWindows = emptyList(),
            quotaTotal = 0,
            quotaUsed = 0,
            quotaUnit = "",
            quotaMessage = "",
        )
        val read = parseQuotaRead("acc-9", """{"quota_supported":false,"quota_read":false}""")
        val merged = row.withQuotaRead(read)
        assertTrue(!merged.quotaSupported)
    }

    @Test
    fun refreshPathEncodesTheAccountId() {
        assertEquals("/api/accounts/acc-1/refresh", accountRefreshPath("acc-1"))
        assertEquals("/api/accounts/a%2Fb/refresh", accountRefreshPath("a/b"))
    }

    @Test
    fun malformedBodyIsInvalidResponse() {
        for (body in listOf("not json", "", "{}")) {
            val error = runCatching { parseQuotaRead("acc-1", body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    /**
     * 备用路径的形状：万一没有单账号端点，就复用 `POST /api/accounts/batch` 传单元素 ids。
     * 这次没用上（单账号端点存在），但 body 形状钉住，免得将来退化。
     */
    @Test
    fun singleElementBatchBodyStaysAvailable() {
        val body = buildAccountBatchJson(BATCH_ACTION_REFRESH, listOf("acc-1"))
        assertTrue(body, body.contains("\"action\":\"refresh\""))
        assertTrue(body, body.contains("\"ids\":[\"acc-1\"]"))
    }

    /**
     * 客户端必须比服务端更晚放弃。
     *
     * 服务端最坏路径 = 凭据续期 45s（`auth_api.go:307-308`）+ 读额度 20s（`auth_api.go:336`）= 65s。
     * 之前客户端是 60s，比 65s 早，于是回包永远到不了、`quota_error` 永远为空——
     * 用户只看到"点了没变化"。所以常量必须 ≥ 90s，且不许为了"看起来通过"放到 120s 以上。
     */
    @Test
    fun clientTimeoutOutlastsTheServerWorstCase() {
        assertEquals(65_000, SERVER_QUOTA_READ_WORST_MS)
        assertTrue(
            "ACCOUNT_REFRESH_TIMEOUT_MS=$ACCOUNT_REFRESH_TIMEOUT_MS 必须 ≥ 90_000",
            ACCOUNT_REFRESH_TIMEOUT_MS >= 90_000,
        )
        assertTrue(
            "客户端超时必须晚于服务端最坏 65s",
            ACCOUNT_REFRESH_TIMEOUT_MS > SERVER_QUOTA_READ_WORST_MS,
        )
        assertTrue(
            "不要为了通过而把超时放到 120s 以上",
            ACCOUNT_REFRESH_TIMEOUT_MS < 120_000,
        )
    }
}
