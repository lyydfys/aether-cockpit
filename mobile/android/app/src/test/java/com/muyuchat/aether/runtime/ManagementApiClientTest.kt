package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ManagementApiClientTest {
    private val realShape = """
        {
          "providers": [
            {
              "id": "claude",
              "display_name": "Claude",
              "description": "Anthropic 订阅",
              "account_count": 2,
              "ready_count": 1,
              "ready": true,
              "off": false,
              "brand_color": "#D97757",
              "key": {"set": true, "masked": "sk-ant-****"},
              "keyList": [{"id": "a1", "name": "主账号", "masked": "sk-ant-****"}]
            },
            {
              "id": "custom",
              "name": "Custom",
              "account_count": 0,
              "ready": false,
              "off": true
            }
          ],
          "gateway": {"running": true}
        }
    """.trimIndent()

    @Test
    fun mapsProviderSummariesWithoutSecrets() {
        val providers = parseProviders(realShape)
        assertEquals(2, providers.size)
        assertEquals("Claude", providers[0].name)
        assertEquals(2, providers[0].accountCount)
        assertEquals(1, providers[0].readyCount)
        assertTrue(providers[0].ready)
        assertEquals("Custom", providers[1].name)
        assertTrue(providers[1].off)
        assertEquals("", providers[1].description)
        assertEquals("#D97757", providers[0].brandColor)
        assertEquals(0xFFD97757L, parseBrandColor(providers[0].brandColor))
        assertNull(parseBrandColor("#FFF"))
        assertNull(parseBrandColor("#GGGGGG"))
    }

    @Test
    fun acceptsLegacyArrayShapeAndRejectsMalformedBodies() {
        assertTrue(parseProviders("""[{"id":"ok","display_name":"OK"}]""").isNotEmpty())
        assertTrue(parseProviders("""{"providers":[]}""").isEmpty())
        for (body in listOf("not json", """{"gateway":{}}""", "{}")) {
            val error = runCatching { parseProviders(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun blankIdsAreSkipped() {
        val providers = parseProviders("""{"providers":[{"id":""},{"id":"ok","display_name":"OK"}]}""")
        assertEquals(listOf("ok"), providers.map { it.id })
    }

    @Test
    fun mapsAccountsFromKeyListWithoutSecrets() {
        val body = """
            {
              "providers": [
                {
                  "id": "claude",
                  "display_name": "Claude",
                  "account_count": 2,
                  "keyList": [
                    {
                      "id": "acct-1",
                      "name": "主账号",
                      "masked": "sk-ant-SECRET-1234",
                      "on": true,
                      "active": true,
                      "rest": {"key": "acct-1", "why": "rate", "until": 1793620800000},
                      "quota": {
                        "plan": "Pro",
                        "windows": [
                          {"name": "5h", "used": 0.42, "resetsAt": "2026-11-02T12:00:00Z"},
                          {"name": "7d", "amount": 120, "limit": 500, "unit": "requests"}
                        ],
                        "cooling": true
                      }
                    },
                    {"id": "acct-2", "name": "备用", "masked": "sk-ant-SECRET-5678", "on": false},
                    {"id": "", "name": "坏行"}
                  ]
                }
              ]
            }
        """.trimIndent()
        val accounts = parseProviders(body).single().accounts
        assertEquals(listOf("acct-1", "acct-2"), accounts.map { it.id })

        val primary = accounts[0]
        assertEquals("主账号", primary.label)
        assertTrue(primary.enabled)
        assertTrue(primary.active)
        assertEquals("rate", primary.restWhy)
        assertEquals(1_793_620_800_000L, primary.restUntilMs)
        assertEquals("Pro", primary.quota?.plan)
        assertEquals(true, primary.quota?.cooling)
        assertEquals(2, primary.quota?.windows?.size)
        assertEquals(0.42, primary.quota?.windows?.get(0)?.used ?: -1.0, 0.0001)
        assertEquals(120L, primary.quota?.windows?.get(1)?.amount)
        assertEquals(500L, primary.quota?.windows?.get(1)?.limit)

        val backup = accounts[1]
        assertEquals(false, backup.enabled)
        assertNull(backup.quota)
        assertEquals("", backup.restWhy)
        assertEquals(0L, backup.restUntilMs)

        // keyList[].masked 绝不能进入任何可展示字段。
        val shown = accounts.joinToString("|") { it.toString() } + accounts.joinToString("|") { it.quota.toString() }
        assertFalse(shown.contains("SECRET"))
        assertFalse(shown.contains("sk-ant"))
    }

    @Test
    fun parsesBundleImportResultAndFailures() {
        val result = parseImportResult(
            """
            {
              "imported": ["测试账号 A"],
              "skipped": ["重复账号"],
              "failed": [{"label": "坏账号", "error": "缺少凭证"}],
              "total": 3
            }
            """.trimIndent(),
        )
        assertEquals(listOf("测试账号 A"), result.imported)
        assertEquals(1, result.skipped)
        assertEquals(1, result.failed.size)
        assertEquals("坏账号", result.failed[0].label)
        assertEquals("缺少凭证", result.failed[0].error)
        assertEquals(3, result.total)
    }

    @Test
    fun rejectsMalformedImportResults() {
        for (body in listOf("not json", "{}", """{"imported":null}""")) {
            val error = runCatching { parseImportResult(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun importRequestBodyCarriesOnlyBase64SealedBytes() {
        val sealed = byteArrayOf(1, 2, 3, 4)
        val body = importRequestBody(sealed, "fixture-passphrase")
        val json = org.json.JSONObject(body)
        assertEquals("AQIDBA==", json.getString("data"))
        assertEquals("fixture-passphrase", json.getString("passphrase"))
        assertEquals(setOf("data", "passphrase"), json.keys().asSequence().toSet())
    }

    @Test
    fun parsesQoderLoginStartAndPollStates() {
        val start = parseQoderLoginStart(
            """
            {"id":"qoder_ab12","state":"pending",
             "login_url":"https://qoder.com/device/selectAccounts?nonce=x",
             "interval":1,"expires_in":600}
            """.trimIndent(),
        )
        assertEquals("qoder_ab12", start.id)
        assertEquals("pending", start.state)
        assertTrue(start.loginUrl.startsWith("https://qoder.com/device/selectAccounts"))
        assertEquals(1, start.interval)

        val pending = parseQoderLoginPoll(
            """{"id":"qoder_ab12","state":"pending","error":"","interval":1,"account":""}""",
        )
        assertEquals("pending", pending.state)
        assertEquals("", pending.account)

        val done = parseQoderLoginPoll(
            """{"id":"qoder_ab12","state":"done","account":"qoder_cn_abc","interval":1}""",
        )
        assertEquals("done", done.state)
        assertEquals("qoder_cn_abc", done.account)

        val failed = parseQoderLoginPoll(
            """{"id":"qoder_ab12","state":"failed","error":"用户取消"}""",
        )
        assertEquals("failed", failed.state)
        assertEquals("用户取消", failed.error)
    }

    @Test
    fun rejectsMalformedQoderLoginPayloads() {
        for (body in listOf("not json", "{}", """{"id":"","login_url":""}""")) {
            val error = runCatching { parseQoderLoginStart(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
        for (body in listOf("not json", "{}", """{"state":""}""")) {
            val error = runCatching { parseQoderLoginPoll(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun missingOrInvalidKeyListYieldsNoAccounts() {
        val body = """
            {"providers":[
              {"id":"a","display_name":"A"},
              {"id":"b","display_name":"B","keyList":"oops"},
              {"id":"c","display_name":"C","keyList":[]}
            ]}
        """.trimIndent()
        assertTrue(parseProviders(body).all { it.accounts.isEmpty() })
    }
}
