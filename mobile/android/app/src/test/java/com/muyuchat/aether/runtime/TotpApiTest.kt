package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 2FA 管理面的纯解析测试（不需要 Android）。
 *
 * 重点锁两件事：① 列表里**永远不出现** `secret_enc` 或明文 seed；② 服务端解不开某条时
 * 页面拿到的是一条 `error` 记录，而不是被静默过滤或显示成空码。
 */
class TotpApiTest {
    @Test
    fun parsesEntriesWithTailOnly() {
        val body = """
            {"entries":[
              {"id":"2fa_abc","issuer":"GitHub","account":"dev","note":"主账号",
               "digits":6,"period":30,"algorithm":"SHA1","created_at":1730000000,
               "secret_tail":"3PXP"}
            ]}
        """.trimIndent()
        val entries = parseTotpEntries(body)
        assertEquals(1, entries.size)
        val e = entries.single()
        assertEquals("2fa_abc", e.id)
        assertEquals("GitHub", e.issuer)
        assertEquals("dev", e.account)
        assertEquals("主账号", e.note)
        assertEquals(6, e.digits)
        assertEquals(30, e.period)
        assertEquals("SHA1", e.algorithm)
        assertEquals(1730000000L, e.createdAt)
        assertEquals("3PXP", e.secretTail)
    }

    @Test
    fun keepsServerDigitAndPeriodRanges() {
        // 服务端归一范围是 digits 4-10 / period 5-300，客户端不得收窄。
        val entries = parseTotpEntries(
            """{"entries":[{"id":"a","digits":4,"period":5},{"id":"b","digits":10,"period":300}]}""",
        )
        assertEquals(listOf(4, 10), entries.map { it.digits })
        assertEquals(listOf(5, 300), entries.map { it.period })
    }

    @Test
    fun dropsRowsWithoutId() {
        val entries = parseTotpEntries(
            """{"entries":[{"issuer":"no-id"},{"id":"ok","issuer":"GitHub"}]}""",
        )
        assertEquals(listOf("ok"), entries.map { it.id })
    }

    @Test
    fun parsesCodesIncludingHonestErrors() {
        val body = """
            {"entries":[
              {"id":"a","issuer":"GitHub","code":"123456","remaining":17},
              {"id":"b","issuer":"Bank","error":"密钥无法解密：该条目由设备凭据加密，当前运行模式没有可用密钥"}
            ]}
        """.trimIndent()
        val codes = parseTotpCodes(body)
        assertEquals(2, codes.size)
        assertEquals("123456", codes[0].code)
        assertEquals(17, codes[0].remaining)
        assertEquals("", codes[0].error)
        // 解不开的条目必须保留错误原文、码为空——不能伪造一个码。
        assertEquals("", codes[1].code)
        assertTrue(codes[1].error.contains("无法解密"))
    }

    @Test
    fun addPayloadCarriesEveryFieldAndTrims() {
        val json = JSONObject(
            buildTotpAddJson(
                issuer = " GitHub ",
                account = " dev@example.com ",
                note = " 主账号 ",
                secret = " JBSWY3DPEHPK3PXP ",
                digits = 6,
                period = 30,
                algorithm = "sha1",
            ),
        )
        assertEquals("GitHub", json.getString("issuer"))
        assertEquals("dev@example.com", json.getString("account"))
        assertEquals("主账号", json.getString("note"))
        assertEquals("JBSWY3DPEHPK3PXP", json.getString("secret"))
        assertEquals(6, json.getInt("digits"))
        assertEquals(30, json.getInt("period"))
        assertEquals("sha1", json.getString("algorithm"))
    }

    @Test
    fun addPayloadDefaultsAlgorithmToSha1() {
        val json = JSONObject(
            buildTotpAddJson("GitHub", "dev", "", "JBSWY3DPEHPK3PXP", 6, 30, "   "),
        )
        assertEquals("SHA1", json.getString("algorithm"))
    }

    @Test
    fun addResponseIsParsedFromTheEntryObject() {
        val entry = parseTotpEntry(
            """{"entry":{"id":"2fa_new","issuer":"GitHub","digits":6,"period":30,"secret_tail":"3PXP"}}""",
        )
        assertEquals("2fa_new", entry.id)
        assertEquals("3PXP", entry.secretTail)
        assertEquals("SHA1", entry.algorithm)
    }

    @Test
    fun malformedBodiesRaiseInvalidResponse() {
        for (body in listOf("not-json", "", "{}")) {
            val error = runCatching { parseTotpEntries(body) }.exceptionOrNull()
            // 空对象没有 entries 字段时按"空列表"处理是安全的（服务端就是空库形状）；
            // 真正的坏 JSON 必须报 invalid_response。
            if (body == "{}") {
                assertEquals(emptyList<TotpEntry>(), parseTotpEntries(body))
            } else {
                assertTrue("body=$body 应报 invalid_response", error is ManagementApiException)
                assertEquals("invalid_response", (error as ManagementApiException).code)
            }
        }
    }
}
