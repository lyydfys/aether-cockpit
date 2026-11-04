package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号级模型映射（`POST /api/provider/accountmapping`）的数据层。
 *
 * 契约核实（写死在这里，避免以后又"猜"一遍）：
 * - 请求体 `{id, account, mapping}`（`internal/core/gateway/provider_api.go:699-703`）；
 * - 服务端 `e.SetModelMapping(body.Mapping)`（`:710`）→
 *   `internal/provider/env.go:585-602`：先清洗（去空、去 `k==v`），再**整体赋值**
 *   `e.modelMapping = clean`；空 map 直接置 nil。
 *
 * 所以这是**整表替换**：客户端必须提交完整目标映射，不能只发改动项。
 * 同时它**只改这一个字段**——同一次调用不碰 priority/weight/note/tags 等
 * （`handleProviderAccountMapping` 只调 SetModelMapping 后 `saveAccountsNow()`）。
 */
class AccountMappingApiTest {

    @Test
    fun mappingBodyCarriesProviderAccountAndWholeMap() {
        val json = JSONObject(
            buildAccountMappingJson(
                providerId = " anthropic ",
                accountId = " acc-1 ",
                mapping = linkedMapOf("b-model" to "b-upstream", "a-model" to "a-upstream"),
            ),
        )
        assertEquals("anthropic", json.getString("id"))
        assertEquals("acc-1", json.getString("account"))
        val mapping = json.getJSONObject("mapping")
        // 键排序后再写，序列化结果稳定（便于日志比对，也不影响服务端语义）。
        assertEquals(setOf("a-model", "b-model"), mapping.keys().asSequence().toSet())
        assertEquals("a-upstream", mapping.getString("a-model"))
        // 整表都发出去了，不是增量。
        assertEquals(2, mapping.length())
    }

    @Test
    fun mappingBodyOnlyTouchesTheMappingField() {
        // 服务端只调 SetModelMapping（provider_api.go:710），不读其它账号字段；
        // 客户端这边也不许把 note/tags/priority 之类混进请求体——否则以后服务端加了
        // "整体覆盖"语义就会误伤。这里锁住"请求体只有三个键"。
        val json = JSONObject(
            buildAccountMappingJson("p", "a", mapOf("m" to "u")),
        )
        assertEquals(setOf("id", "account", "mapping"), json.keys().asSequence().toSet())
        assertFalse(json.has("note"))
        assertFalse(json.has("tags"))
        assertFalse(json.has("priority"))
        assertFalse(json.has("allowed_models"))
    }

    @Test
    fun emptyMappingSerializesAsEmptyObjectForClearing() {
        // 空映射必须真的发 `{}`（清除语义），不能省掉 mapping 键——
        // 省掉就等于"没提交"，服务端解析成 nil map，行为与清除不同。
        val json = JSONObject(buildAccountMappingJson("p", "a", emptyMap()))
        assertTrue(json.has("mapping"))
        assertEquals(0, json.getJSONObject("mapping").length())
    }

    @Test
    fun mappingBodyTrimsAndDropsEmptyEntries() {
        val json = JSONObject(
            buildAccountMappingJson(
                "p",
                "a",
                mapOf(
                    "  a  " to "  upstream  ",
                    "" to "orphan",
                    "noTarget" to "   ",
                ),
            ),
        )
        val mapping = json.getJSONObject("mapping")
        assertEquals(setOf("a"), mapping.keys().asSequence().toSet())
        assertEquals("upstream", mapping.getString("a"))
    }

    @Test
    fun parseLinesAcceptsKeyValuePairs() {
        val parsed = parseAccountMappingLines(
            """
            # 注释行不算
            gpt-4o=gpt-4o-2024-11-20
              claude-3-5  =  claude-sonnet-4

            坏行没有等号
            =没有键
            只有键=
            same=same
            dup=first
            dup=second
            """.trimIndent(),
        )
        assertEquals("gpt-4o-2024-11-20", parsed["gpt-4o"])
        assertEquals("claude-sonnet-4", parsed["claude-3-5"])
        // `k == v` 服务端会当没配（env.go:590），这里也不收，避免"已配置 N 条"虚高。
        assertFalse(parsed.containsKey("same"))
        // 同键后者覆盖前者。
        assertEquals("second", parsed["dup"])
        // 没有等号/空键/空值都不收。
        assertEquals(3, parsed.size)
    }

    @Test
    fun parseLinesKeepsValueSideEquality() {
        // 只按第一个 '=' 切：值里再有等号就原样留在值里，不静默截断。
        val parsed = parseAccountMappingLines("route=upstream?x=1")
        assertEquals("upstream?x=1", parsed["route"])
    }

    @Test
    fun formatLinesRoundTripsThroughParse() {
        val mapping = linkedMapOf("b" to "2", "a" to "1")
        val text = formatAccountMappingLines(mapping)
        // 键排序，保证每次打开对话框顺序一致。
        assertEquals("a=1\nb=2", text)
        assertEquals(mapping.toSortedMap(), parseAccountMappingLines(text).toSortedMap())
        // 空映射 → 空文本（对话框是空的，不是一行 "="）。
        assertEquals("", formatAccountMappingLines(emptyMap()))
    }

    @Test
    fun parseModelMappingReadsSnapshotField() {
        val mapping = parseModelMapping(
            JSONObject("""{"gpt-4o":"gpt-4o-2024-11-20","  spaced  ":"  upstream  ","":"skip"}"""),
        )
        assertEquals("gpt-4o-2024-11-20", mapping["gpt-4o"])
        // 键值都 trim。
        assertEquals("upstream", mapping["spaced"])
        assertFalse(mapping.containsKey(""))
        // omitempty：没有这个字段时是空 map（不是 null），UI 不用区分两种空。
        assertTrue(parseModelMapping(null).isEmpty())
    }

    @Test
    fun mappingSurvivesJsonRoundTripFromServerShape() {
        // 真实回包形状（这次逐个核过 handler，不再猜）：
        // - `GET /api/accounts`（`admin.go:412`）→ `{"accounts": [<AccountSnapshot 全字段内联>, …]}`，
        //   `model_mapping` 来自 `internal/provider/env.go:813`；
        // - `POST /api/accounts/{id}/settings`（`accounts_api.go:157`）→ `{"account": <Snapshot>}`。
        // 上一条挂掉正是因为我拿"裸快照"喂了快照解析（它要求外层 `account` 键），
        // 而且列表端点也不是裸数组（外面还有 `accounts` 键）——两处都是实测结构。
        val rows = parseAccounts(
            """
            {"accounts":[
              {"account_id":"acc-1","provider_id":"p","label":"主号",
               "model_mapping":{"gpt-4o":"gpt-4o-2024-11-20"},"disabled":false},
              {"account_id":"acc-2","provider_id":"p"}
            ]}
            """.trimIndent(),
        )
        assertEquals(2, rows.size)
        assertEquals(mapOf("gpt-4o" to "gpt-4o-2024-11-20"), rows[0].modelMapping)
        // 没配映射的账号：空 map（对话框初始文本就是空）。
        assertTrue(rows[1].modelMapping.isEmpty())
        assertEquals("", formatAccountMappingLines(rows[1].modelMapping))
        // 往返：服务端读回的映射 → 对话框文本 → 再解析回来，必须与原始 map 等价。
        assertEquals(
            rows[0].modelMapping.toSortedMap(),
            parseAccountMappingLines(formatAccountMappingLines(rows[0].modelMapping)).toSortedMap(),
        )
    }

    @Test
    fun settingsEndpointWrapsSnapshotUnderAccountKey() {
        // `handleAccountSettings` 的回包是 `{"account": <Snapshot>}`（`accounts_api.go:157`）。
        // 这条锁住"哪个端点用哪个解析函数"：裸快照喂给 `parseAccountSnapshot` 必须报错，
        // 而不是静默返回一个空 mapping（静默才是真危险）。
        val row = parseAccountSnapshot(
            """
            {"account":{"account_id":"acc-1","provider_id":"p","label":"主号",
              "model_mapping":{"gpt-4o":"gpt-4o-2024-11-20"},"disabled":false}}
            """.trimIndent(),
        )
        assertEquals(mapOf("gpt-4o" to "gpt-4o-2024-11-20"), row.modelMapping)
        assertTrue(
            runCatching { parseAccountSnapshot("""{"account_id":"acc-1","provider_id":"p"}""") }
                .exceptionOrNull() is ManagementApiException,
        )
    }
}
