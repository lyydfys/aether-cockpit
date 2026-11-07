package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 桌面端 × 手机端接口契约对齐（本文件锁住"参数级"差异）。
 *
 * 用户已经因为"桌面端带参数、手机端漏参数"撞了三次，所以把关键路径做成纯函数，
 * 这里逐字节断言。桌面依据都写在断言旁边，方便以后再查。
 */
class ApiContractParityTest {

    @Test
    fun maskingRecordsUsesDesktopLimitByDefault() {
        // 桌面 `misc.js:340`：`const q = new URLSearchParams({ limit: '50' })`
        assertEquals("/api/masking/records?limit=50", maskingRecordsPath(50))
        // 服务端上限 200（`masking_api.go:22` 的注释），越界要夹住而不是原样发。
        assertEquals("/api/masking/records?limit=200", maskingRecordsPath(999))
        assertEquals("/api/masking/records?limit=1", maskingRecordsPath(0))
    }

    @Test
    fun checkinSummaryAlwaysCarriesSince() {
        // 桌面 `scheduler.js:249`：`scheduler/checkin-summary?since=` + (since || 0)
        assertEquals("/api/scheduler/checkin-summary?since=0", checkinSummaryPath(0))
        assertEquals("/api/scheduler/checkin-summary?since=1700000000", checkinSummaryPath(1_700_000_000))
        // 负数/脏值按 0（今天 0 点）处理，不发负时间戳。
        assertEquals("/api/scheduler/checkin-summary?since=0", checkinSummaryPath(-5))
    }

    @Test
    fun modelDirectoryRefreshMatchesDesktopEndpoint() {
        // 桌面 `channels.js:1056`：`api.post('providers/' + id + '/models/refresh', {})`
        assertEquals("/api/providers/workbuddy-cn/models/refresh", providerModelsRefreshPath("workbuddy-cn"))
        // id 含特殊字符要编码，不能拼出越权路径。
        val encoded = providerModelsRefreshPath("a/../b")
        assertEquals(true, encoded.contains("%2F") || encoded.contains("%2E"))
    }

    @Test
    fun providersListCanAskForEmptyChannels() {
        // 桌面 `onboard.js:51`：`providers?include_empty=1`（未接入渠道也要出现）
        assertEquals("/api/providers?include_empty=1", providersPath(includeEmpty = true))
        assertEquals("/api/providers", providersPath(includeEmpty = false))
    }

    @Test
    fun maskingRecordsCarriesDesktopFilters() {
        // 桌面 `misc.js:340-345`：limit + kind + by + agent + provider + blocked
        assertEquals(
            "/api/masking/records?limit=50&kind=EMAIL",
            maskingRecordsPath(limit = 50, kind = "EMAIL"),
        )
        assertEquals(
            "/api/masking/records?limit=50&blocked=1",
            maskingRecordsPath(limit = 50, blockedOnly = true),
        )
        assertEquals(
            "/api/masking/records?limit=50&provider=a%2Fb",
            maskingRecordsPath(limit = 50, provider = "a/b"),
        )
        // 无筛选时逐字节等于旧行为（避免动到既有调用方）。
        assertEquals("/api/masking/records?limit=50", maskingRecordsPath(50))
    }

    @Test
    fun maskingSummaryAndSentPathsMatchDesktop() {
        // 桌面 `misc.js:348`：summary?days=7|30；`:552`：sent?key=
        assertEquals("/api/masking/summary?days=7", maskingSummaryPath(7))
        assertEquals("/api/masking/summary?days=30", maskingSummaryPath(30))
        assertEquals("/api/masking/summary?days=7", maskingSummaryPath(6))
        assertEquals("/api/masking/sent?key=a%2Fb", maskingSentPath("a/b"))
    }

    @Test
    fun modelSelectionBodyMatchesProviderSaveContract() {
        // 桌面 `channels.js:1178-1191` → `POST /api/provider/save {id, models, model_rates}`
        val body = org.json.JSONObject(
            buildProviderModelSelectionJson(
                providerId = "deepseek",
                models = listOf("deepseek-chat", "deepseek-reasoner"),
                rates = mapOf("deepseek-reasoner" to 2.5, "deepseek-chat" to 0.0),
            ),
        )
        assertEquals("deepseek", body.getString("id"))
        val models = body.getJSONArray("models")
        assertEquals(2, models.length())
        assertEquals("deepseek-chat", models.getString(0))
        val rates = body.getJSONObject("model_rates")
        // 0 = 未配置：不要写进请求体，否则等于把用户没填的项显式置 0。
        assertEquals(1, rates.length())
        assertEquals(2.5, rates.getDouble("deepseek-reasoner"), 0.0001)
    }

    @Test
    fun bindingParsesOfficialAccountAndPause() {
        val body = """
            {"account_id":"a1","provider_id":"workbuddy-cn","binding":
             {"official":true,"account_id":"u-42","email":"a@b.c","plan":"pro","expires_at":1800000000},
             "official":true,"paused":true,"pause_reason":"手动暂停","paused_at":1700000000}
        """.trimIndent()
        val binding = parseAccountBinding(body)
        assertEquals(true, binding.supported)
        assertEquals(true, binding.bound)
        assertEquals("u-42", binding.boundAccountId)
        assertEquals("pro", binding.plan)
        assertEquals(true, binding.paused)
        assertEquals("手动暂停", binding.pauseReason)

        // 没有绑定字段 = 未绑定（仍然 supported）。
        val empty = parseAccountBinding("""{"account_id":"a1","binding":{"official":false}}""")
        assertEquals(true, empty.supported)
        assertEquals(false, empty.bound)
    }

    @Test
    fun autoImportStatusParsesServerShape() {
        val status = parseAutoImportStatus(
            """{"enabled":true,"running":false,"interval_sec":300,"seen":4,
               "last_scan":1700000000,"last_added":["Cursor","Trae"],"last_error":"读不到目录"}""",
        )
        assertEquals(true, status.enabled)
        assertEquals(300, status.intervalSec)
        assertEquals(4, status.seen)
        assertEquals(listOf("Cursor", "Trae"), status.lastAdded)
        assertEquals("读不到目录", status.lastError)
    }

    @Test
    fun maskingSummaryAndSentParseServerShape() {
        val summary = parseMaskingSummary(
            """{"requests":12,"blocked":3,"distinct":5,
               "kinds":[{"name":"email","n":7},{"name":"key","n":2}],
               "providers":[{"name":"workbuddy-cn","n":9}],"agents":[{"name":"claude","n":4}]}""",
        )
        assertEquals(12, summary.requests)
        assertEquals(3, summary.blocked)
        assertEquals(listOf(MaskingTally("email", 7), MaskingTally("key", 2)), summary.kinds)
        assertEquals(listOf(MaskingTally("workbuddy-cn", 9)), summary.providers)

        val sent = parseMaskingSent(
            """{"body":"{\"messages\":[{\"role\":\"user\",\"content\":\"key=sk-***\"}]}"}""",
        )
        assertEquals(1, sent.messages.size)
        assertEquals("user", sent.messages[0].role)
        assertEquals("key=sk-***", sent.messages[0].content)

        // 没有记录时服务端回 `{}`：空列表 + 空原文，不抛异常。
        val none = parseMaskingSent("{}")
        assertEquals(0, none.messages.size)
        assertEquals("", none.rawBody)
    }

    @Test
    fun modelSelectionMarkerDistinguishesAllOffFromOldPayload() {
        // `admin.go:189-197`：aetherModelView 恒输出 on；全关 = 有标记但无 on 项。
        val allOff = org.json.JSONArray("""[{"id":"a","on":false},{"id":"b","on":false}]""")
        assertEquals(true, hasModelOnMarker(allOff))
        assertEquals(emptyList<String>(), parseEnabledModelIds(allOff))
        // 老字符串形态没有 on 键 → 视为"未知"，页面按全部暴露显示。
        assertEquals(false, hasModelOnMarker(org.json.JSONArray("""["a","b"]""")))
        // 倍率只收 >0。
        val rates = parseModelRates(
            org.json.JSONArray("""[{"id":"a","credit_rate":2.5},{"id":"b","credit_rate":0}]"""),
        )
        assertEquals(mapOf("a" to 2.5), rates)
    }

    @Test
    fun maskingKindLabelsFollowDesktopMap() {
        // 桌面 `misc.js:321-327`：显示中文，筛选值仍是大写 kind。
        assertEquals("邮箱", maskingKindLabel("EMAIL"))
        assertEquals("手机号", maskingKindLabel("PHONE"))
        // 后端新增的类型没有中文标签时原样显示，不要变空。
        assertEquals("NEW_KIND", maskingKindLabel("NEW_KIND"))
    }
}
