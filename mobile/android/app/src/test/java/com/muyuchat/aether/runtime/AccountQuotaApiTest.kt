package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * `/api/accounts` 的解析与写操作契约。
 *
 * 字段出处：
 * - `internal/provider/env.go:796-818`（`AccountSnapshot`：account_id / provider_id / label /
 *   disabled / reset_at / last_used / quota）
 * - `internal/core/gateway/admin.go:356-370`（`accountView`：quota_supported / quota_error /
 *   checkin_supported / checkin）
 * - `internal/provider/contract.go:209-252`（`QuotaWindow` 的 used 是 0-100 百分比、`QuotaStatus`）
 * - 写操作：`gateway.go:1053`（`POST /api/accounts/{id}/settings`）与 `gateway.go:847`
 *   （`POST /api/accounts/{id}/checkin`）
 */
class AccountQuotaApiTest {
    private val accountsBody = """
        {
          "accounts": [
            {
              "account_id": "acc-1", "provider_id": "claude-sub", "label": "主力订阅",
              "state": "ready", "reset_at": 1762300000, "last_used": 1762290000,
              "inflight": 0, "failures": 0, "disabled": false,
              "quota": {
                "total": 0, "used": 0, "remaining": 0, "plan": "Pro", "read_at": 1762280000,
                "windows": [
                  {"name": "5 小时", "used": 37.5, "resets_at": 1762303600, "unit": "次"},
                  {"name": "每周", "used": 100, "unit": "次"}
                ]
              },
              "quota_supported": true, "checkin_supported": true,
              "checkin": {"day": "2026-11-04", "outcome": "claimed", "credit": 50, "streak": 3}
            },
            {
              "account_id": "acc-2", "provider_id": "trae-cn", "label": "备用",
              "disabled": true, "quota_supported": false, "quota_error": "upstream 401"
            }
          ]
        }
    """.trimIndent()

    @Test
    fun parsesAccountsWithQuotaWindowsAndCheckin() {
        val rows = parseAccounts(accountsBody)
        assertEquals(2, rows.size)

        val main = rows[0]
        assertEquals("acc-1", main.accountId)
        assertEquals("claude-sub", main.providerId)
        assertEquals("主力订阅", main.label)
        assertTrue(!main.disabled)
        assertEquals(1762300000L, main.resetAt)
        assertEquals(1762290000L, main.lastUsed)
        assertTrue(main.quotaSupported)
        assertTrue(main.quotaMessage.isBlank())
        assertEquals(2, main.quotaWindows.size)
        assertEquals("5 小时", main.quotaWindows[0].name)
        assertEquals(37.5, main.quotaWindows[0].usedPercent, 0.001)
        assertEquals(1762303600L, main.quotaWindows[0].resetsAt)
        assertEquals("次", main.quotaWindows[0].unit)
        assertTrue(main.checkinSupported)
        val checkin = main.checkin
        assertNotNull(checkin)
        assertEquals("2026-11-04", checkin!!.day)
        assertEquals("claimed", checkin.outcome)
        assertEquals(50.0, checkin.credit, 0.001)
        assertEquals(3, checkin.streak)

        val backup = rows[1]
        assertTrue(backup.disabled)
        assertTrue(!backup.quotaSupported)
        assertEquals("upstream 401", backup.quotaError)
        assertTrue(backup.quotaWindows.isEmpty())
        assertNull(backup.checkin)
    }

    @Test
    fun emptyAccountListIsAValidResult() {
        assertTrue(parseAccounts("""{"accounts":[]}""").isEmpty())
    }

    @Test
    fun missingOrBrokenPayloadIsInvalidResponse() {
        for (body in listOf("not json", "{}", """{"accounts":"oops"}""", """{"accounts":[1,2]}""")) {
            val error = runCatching { parseAccounts(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun entriesWithoutAccountIdAreDroppedButAllBrokenIsAnError() {
        // 缺 account_id 的单条被丢掉，其余照常显示
        val rows = parseAccounts("""{"accounts":[{"account_id":"a"},{"provider_id":"no-id"}]}""")
        assertEquals(1, rows.size)
        assertEquals("a", rows[0].accountId)
        // 一条都解析不出来 → 报错，不能静默当成"还没有账号"
        val error = runCatching {
            parseAccounts("""{"accounts":[{"provider_id":"x"},{"label":"y"}]}""")
        }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }

    /** 老响应没有 quota_supported 时按"有 quota 对象"推断，避免误报「该渠道不提供额度数据」。 */
    @Test
    fun quotaSupportedFallsBackToQuotaPresence() {
        val withQuota = parseAccounts("""{"accounts":[{"account_id":"a","quota":{"total":100,"used":25}}]}""")
        assertTrue(withQuota[0].quotaSupported)
        assertEquals(100, withQuota[0].quotaTotal)
        assertEquals(25, withQuota[0].quotaUsed)

        val withoutQuota = parseAccounts("""{"accounts":[{"account_id":"a"}]}""")
        assertTrue(!withoutQuota[0].quotaSupported)
    }

    /** 上游给越界百分比时夹回 0-100，卡片上不能出现 >100% 或负剩余。 */
    @Test
    fun windowPercentIsClamped() {
        val rows = parseAccounts(
            """{"accounts":[{"account_id":"a","quota_supported":true,
               "quota":{"windows":[{"name":"w","used":150},{"name":"v","used":-20}]}}]}""",
        )
        assertEquals(100.0, rows[0].quotaWindows[0].usedPercent, 0.001)
        assertEquals(0.0, rows[0].quotaWindows[1].usedPercent, 0.001)
    }

    @Test
    fun accountSnapshotAckIsParsed() {
        val row = parseAccountSnapshot(
            """{"account":{"account_id":"acc-1","provider_id":"claude-sub","label":"主力","disabled":true}}""",
        )
        assertEquals("acc-1", row.accountId)
        assertTrue(row.disabled)
        // label 缺省时回退成 account_id，不留空标题
        val fallback = parseAccountSnapshot("""{"account":{"account_id":"acc-9"}}""")
        assertEquals("acc-9", fallback.label)

        for (body in listOf("""{"account":null}""", "not json", "{}")) {
            val error = runCatching { parseAccountSnapshot(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun checkinAckCarriesTodaysCard() {
        val ack = parseCheckinAck("""{"checkin":{"outcome":"failed","msg":"上游 429"},"outcome":"failed"}""")
        assertNotNull(ack)
        assertEquals("failed", ack!!.outcome)
        assertEquals("上游 429", ack.message)
        // 服务端没给状态就返回 null（调用方据此去重拉，不猜结果）
        assertNull(parseCheckinAck("""{"outcome":"claimed"}"""))
        for (body in listOf("not json", "")) {
            val error = runCatching { parseCheckinAck(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    @Test
    fun pathsEncodeTheAccountId() {
        assertEquals("/api/accounts/acc-1/settings", accountSettingsPath("acc-1"))
        assertEquals("/api/accounts/acc-1/checkin", accountCheckinPath("acc-1"))
        // id 里的斜杠与空格必须编码，不能拼出新路径
        assertEquals("/api/accounts/a%2Fb/settings", accountSettingsPath("a/b"))
        assertEquals("/api/accounts/a%20b/checkin", accountCheckinPath("a b"))
    }

    @Test
    fun invalidIdsAreRejectedBeforeAnyRequest() {
        assertEquals("account_id_empty", validateAccountId(""))
        assertEquals("account_id_empty", validateAccountId("   "))
        assertEquals("account_id_invalid", validateAccountId("a b"))
        assertEquals("account_id_invalid", validateAccountId("../etc/passwd"))
        assertEquals("account_id_invalid", validateAccountId("a?b"))
        assertNull(validateAccountId("acc-1"))
    }

    /** 只改启用位：body 里不许夹带别的东西。 */
    @Test
    fun disabledBodyCarriesOnlyTheFlag() {
        assertEquals("""{"disabled":true}""", buildAccountDisabledJson(true))
        assertEquals("""{"disabled":false}""", buildAccountDisabledJson(false))
        assertTrue(!buildAccountDisabledJson(true).contains("key"))
    }
}
