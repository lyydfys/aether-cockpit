package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ProviderBalanceDraft
import com.muyuchat.aether.runtime.buildProviderBalanceJson
import com.muyuchat.aether.runtime.parseHeaderLines
import com.muyuchat.aether.runtime.providerBalancePath
import com.muyuchat.aether.runtime.validateProviderBalanceDraft
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道级自定义余额（`POST /api/provider/balance`，`provider_balance.go:139`）。
 *
 * 服务端字段：`id / balanceURL / balancePath / balanceMethod / headers{} / balanceToken /
 * clearBalanceToken / clearBalance / account`。手机端把 `headers` 承载成「每行 `Key: Value`」的
 * 多行文本，解析失败要给**行号**（用户才能定位）。
 */
class ChannelsProviderBalanceTest {
    private fun draft(
        url: String = "https://api.example.com",
        path: String = "/v1/balance",
        method: String = "GET",
        headers: String = "",
        token: String = "",
    ) = ProviderBalanceDraft(
        providerId = "claude-sub",
        url = url,
        path = path,
        method = method,
        headersText = headers,
        token = token,
    )

    @Test
    fun headerLinesParseKeyValuePairs() {
        val parsed = parseHeaderLines(
            """
            Authorization: Bearer sk-1
            X-Org: aether

            # 注释行忽略
            """.trimIndent(),
        )
        assertTrue(parsed.ok)
        assertEquals("Bearer sk-1", parsed.headers["Authorization"])
        assertEquals("aether", parsed.headers["X-Org"])
        assertEquals(2, parsed.headers.size)
    }

    @Test
    fun headerValueMayContainColons() {
        val parsed = parseHeaderLines("Authorization: Bearer a:b:c")
        assertTrue(parsed.ok)
        assertEquals("Bearer a:b:c", parsed.headers["Authorization"])
    }

    @Test
    fun malformedHeaderReportsTheLineNumber() {
        val missingColon = parseHeaderLines("Authorization: ok\nX-Bad no colon")
        assertFalse(missingColon.ok)
        assertEquals("要指出第几行", 2, missingColon.errorLine)
        assertTrue(missingColon.error!!.contains("第 2 行"))
        assertTrue(missingColon.error.contains("冒号"))

        // 空键 / 空值也要带行号
        assertEquals(1, parseHeaderLines(": value").errorLine)
        assertEquals(1, parseHeaderLines("Key:").errorLine)
    }

    @Test
    fun draftValidationCoversUrlAndMethod() {
        assertNull("合法草稿不该报错", validateProviderBalanceDraft(draft()))
        assertNotNull("缺少 URL", validateProviderBalanceDraft(draft(url = "")))
        assertNotNull("URL 协议不对", validateProviderBalanceDraft(draft(url = "api.example.com")))
        assertNotNull("方法不支持", validateProviderBalanceDraft(draft(method = "DELETE")))
        assertNotNull("headers 坏格式", validateProviderBalanceDraft(draft(headers = "no colon")))
        assertEquals(
            "缺少渠道 id",
            validateProviderBalanceDraft(draft().copy(providerId = "")),
        )
    }

    @Test
    fun requestBodyCarriesOnlyProvidedFields() {
        val body = JSONObject(
            buildProviderBalanceJson(
                draft(
                    headers = "Authorization: Bearer sk-1\nX-Org: aether",
                    token = "sk-secret",
                ),
            ),
        )
        assertEquals("claude-sub", body.getString("id"))
        assertEquals("https://api.example.com", body.getString("balanceURL"))
        assertEquals("/v1/balance", body.getString("balancePath"))
        assertEquals("GET", body.getString("balanceMethod"))
        assertEquals("Bearer sk-1", body.getJSONObject("headers").getString("Authorization"))
        assertEquals("sk-secret", body.getString("balanceToken"))
        // 不清除时不能带 clearBalance
        assertFalse(body.has("clearBalance"))
        assertEquals("路径与 gateway 一致", "/api/provider/balance", providerBalancePath())
    }

    @Test
    fun emptyTokenMeansKeepTheSavedOne() {
        // 令牌留空 = 不改动（服务端哨兵语义），请求体里就不出现 balanceToken
        val body = JSONObject(buildProviderBalanceJson(draft(token = "")))
        assertFalse("留空不能发 balanceToken", body.has("balanceToken"))
        assertFalse("也不能发 clearBalanceToken", body.has("clearBalanceToken"))
        // 空 headers 同样不发，避免把已有配置清空
        assertFalse(body.has("headers"))
        // 空 path 也不发（服务端有默认）
        assertFalse(JSONObject(buildProviderBalanceJson(draft(path = ""))).has("balancePath"))
    }

    @Test
    fun clearSendsOnlyIdAndClearFlag() {
        val body = JSONObject(buildProviderBalanceJson(draft(), clear = true))
        assertEquals("claude-sub", body.getString("id"))
        assertTrue(body.getBoolean("clearBalance"))
        assertEquals("清除时不该带其它字段", 2, body.length())
    }
}
