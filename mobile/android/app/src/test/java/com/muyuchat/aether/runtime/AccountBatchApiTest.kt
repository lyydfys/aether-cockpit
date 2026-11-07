package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 批量端点契约（`POST /api/accounts/batch`，`gateway.go:1073` → `handleAccountBatch`，
 * 实现在 `accounts_api.go:136-226`：请求 `{action, ids}`，回 `{results:[{id,ok,error}],ok,total}`）。
 *
 * 服务端只认 delete / refresh / ready —— **没有**批量启停、**没有**批量签到；
 * 那两条走的是逐个单项端点，见 `AccountQuotaApiClient.batchSetDisabled/batchCheckin`。
 */
class AccountBatchApiTest {
    @Test
    fun batchPathIsTheAccountsBatchEndpoint() {
        assertEquals("/api/accounts/batch", accountsBatchPath())
    }

    @Test
    fun requestCarriesOnlyActionAndIds() {
        val body = buildAccountBatchJson(BATCH_ACTION_REFRESH, listOf("acc-1", "acc-2"))
        // JSONObject 不保证键序，所以逐段断言
        assertTrue(body, body.contains("\"action\":\"refresh\""))
        assertTrue(body, body.contains("\"ids\":[\"acc-1\",\"acc-2\"]"))
        // 不能夹带凭据字段
        assertTrue(body, !body.contains("key"))
        assertTrue(body, !body.contains("token"))
    }

    @Test
    fun emptyOrInvalidIdsAreRejectedBeforeTheRequest() {
        assertEquals("batch_ids_empty", validateBatchIds(emptyList()))
        assertEquals("account_id_invalid", validateBatchIds(listOf("a b")))
        assertEquals("account_id_invalid", validateBatchIds(listOf("ok-1", "../etc/passwd")))
        assertNull(validateBatchIds(listOf("acc-1", "acc-2")))
    }

    @Test
    fun parsesOkFailedCountsAndFirstError() {
        val outcome = parseAccountBatch(
            """
            {"results":[{"id":"acc-1","ok":true},
                        {"id":"acc-2","error":"账号不存在"},
                        {"id":"acc-3","error":"上游 429"}],
             "ok":1,"total":3}
            """.trimIndent(),
        )
        assertEquals(1, outcome.ok)
        assertEquals(2, outcome.failed)
        assertEquals(3, outcome.total)
        // 第一个失败原因取第一条失败的服务端原文（"账号不存在"），不是最后一条
        assertEquals("账号不存在", outcome.firstError)
        assertTrue(outcome.updatedRows.isEmpty())
        assertTrue(outcome.checkins.isEmpty())
    }

    @Test
    fun emptyResultSetIsAllowed() {
        val outcome = parseAccountBatch("""{"results":[],"ok":0,"total":0}""")
        assertEquals(0, outcome.ok)
        assertEquals(0, outcome.total)
        assertEquals("", outcome.firstError)
    }

    @Test
    fun countsThatDoNotMatchTheWireAreRejected() {
        // total 说 2 条，但只回了 1 条 → 不能拿它去凑「成功 N」
        val totalMismatch = runCatching {
            parseAccountBatch("""{"results":[{"id":"acc-1","ok":true}],"ok":1,"total":2}""")
        }.exceptionOrNull()
        assertTrue(totalMismatch is ManagementApiException)
        assertEquals("invalid_response", (totalMismatch as ManagementApiException).code)

        // ok 说 0，但结果里有一条 ok=true → 自相矛盾
        val okMismatch = runCatching {
            parseAccountBatch("""{"results":[{"id":"acc-1","ok":true}],"ok":0,"total":1}""")
        }.exceptionOrNull()
        assertTrue(okMismatch is ManagementApiException)
        assertEquals("invalid_response", (okMismatch as ManagementApiException).code)

        // 条目缺 id → 解析不完整
        val brokenEntry = runCatching {
            parseAccountBatch("""{"results":[{"ok":true}],"ok":1,"total":1}""")
        }.exceptionOrNull()
        assertTrue(brokenEntry is ManagementApiException)
        assertEquals("invalid_response", (brokenEntry as ManagementApiException).code)
    }

    @Test
    fun malformedPayloadIsInvalidResponse() {
        for (body in listOf("not json", "{}", """{"results":"oops"}""", "")) {
            val error = runCatching { parseAccountBatch(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    /** 批量删除是桌面 `channels.js:198` 的真实动作：`action:"delete"`（服务端只认 delete/refresh/ready）。 */
    @Test
    fun deleteActionUsesTheSameBatchEndpoint() {
        val body = buildAccountBatchJson(BATCH_ACTION_DELETE, listOf("acc-1"))
        assertTrue(body, body.contains("\"action\":\"delete\""))
        assertTrue(body, body.contains("\"ids\":[\"acc-1\"]"))
    }
}
