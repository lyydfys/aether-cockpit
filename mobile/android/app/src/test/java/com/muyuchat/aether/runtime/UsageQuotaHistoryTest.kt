package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度历史（`GET /api/usage/quotas/history`）与周期估算（`/history/holds`）的数据层。
 *
 * 契约来自 `internal/core/gateway/quota_history.go`：
 * - `view(days)`（`:317-376`）→ `[{provider,user,lines:[{name,points:[{at,left,start?,resetsAt?}]}]}]`，
 *   `at/start/resetsAt` 是 RFC3339，`left` 是**剩余百分比**；
 * - `quotaHistoryHolds`（`:381-522`）→ `[{provider,user,windows:[{name,holdsWhy?|holds?}]}]`，
 *   `holdsWhy` 只有五个取值：low / credits / log / none / clock。
 */
class UsageQuotaHistoryTest {

    private fun point(atMs: Long, left: Double, start: String = "", resetsAt: String = "") =
        QuotaHistoryPoint(
            at = java.time.Instant.ofEpochMilli(atMs).toString(),
            atMs = atMs,
            left = left,
            start = start,
            resetsAt = resetsAt,
        )

    @Test
    fun parsesHistoryAccountsAndLines() {
        val accounts = parseQuotaHistory(
            """
            [{"provider":"anthropic","user":"alice","lines":[
               {"name":"week","points":[
                 {"at":"2026-11-01T00:00:00Z","left":90},
                 {"at":"2026-11-02T00:00:00Z","left":40,"start":"2026-11-01T00:00:00Z","resetsAt":"2026-11-08T00:00:00Z"}
               ]},
               {"name":"5h","points":[{"at":"2026-11-02T00:00:00Z","left":12.5}]}
             ]},
             {"provider":"","user":"x","lines":[{"name":"week","points":[]}]}]
            """.trimIndent(),
        )
        assertEquals(1, accounts.size)
        val account = accounts.single()
        assertEquals("anthropic", account.provider)
        assertEquals("alice", account.user)
        // 窗口按名字排序（服务端也排，这里再保证一次）。
        assertEquals(listOf("5h", "week"), account.lines.map { it.name })
        val week = account.lines.first { it.name == "week" }
        assertEquals(2, week.points.size)
        assertEquals(90.0, week.points[0].left, 1e-9)
        assertEquals(40.0, week.points[1].left, 1e-9)
        assertEquals("2026-11-01T00:00:00Z", week.points[1].start)
        assertEquals("2026-11-08T00:00:00Z", week.points[1].resetsAt)
    }

    @Test
    fun dropsBrokenPointsAndEmptyContainers() {
        val accounts = parseQuotaHistory(
            """
            [{"provider":"p","user":"u","lines":[
               {"name":"week","points":[
                 {"at":"not-a-time","left":50},
                 {"at":"2026-11-01T00:00:00Z"},
                 {"at":"2026-11-02T00:00:00Z","left":150},
                 {"at":"2026-11-03T00:00:00Z","left":-20}
               ]},
               {"name":"","points":[{"at":"2026-11-01T00:00:00Z","left":10}]},
               {"name":"empty","points":[]}
             ]},
             {"provider":"p2","user":"u","lines":[{"name":"week","points":[]}]}]
            """.trimIndent(),
        )
        // 第二个账号没有任何有效点 → 整条丢掉；第一条只留两个合法点。
        assertEquals(1, accounts.size)
        val week = accounts.single().lines.single()
        assertEquals("week", week.name)
        assertEquals(2, week.points.size)
        // 150 夹到 100、-20 夹到 0：曲线纵轴固定 0..100，超出去的点不能把线画出画布。
        assertEquals(100.0, week.points[0].left, 1e-9)
        assertEquals(0.0, week.points[1].left, 1e-9)
    }

    @Test
    fun normalizeSortsByTimeAndKeepsLastForDuplicateTimestamp() {
        val points = listOf(
            point(3_000L, 30.0),
            point(1_000L, 10.0),
            point(3_000L, 35.0),
        )
        val normalized = normalizeQuotaPoints(points)
        assertEquals(listOf(1_000L, 3_000L), normalized.map { it.atMs })
        // 同毫秒后者覆盖前者：服务端按发生顺序写，后来的读数才是真的。
        assertEquals(35.0, normalized.last().left, 1e-9)
        assertTrue(normalizeQuotaPoints(emptyList()).isEmpty())
        assertEquals(1, normalizeQuotaPoints(listOf(point(1_000L, 10.0))).size)
    }

    @Test
    fun consumedPercentMeasuresDropAndClampsResets() {
        // 90% → 40%：消耗 50 个百分点。
        assertEquals(
            50.0,
            quotaConsumedPercent(listOf(point(1_000L, 90.0), point(2_000L, 40.0))),
            1e-9,
        )
        // 只有一个点：谈不上"期间消耗"。
        assertEquals(0.0, quotaConsumedPercent(listOf(point(1_000L, 90.0))), 1e-9)
        assertEquals(0.0, quotaConsumedPercent(emptyList()), 1e-9)
        // 中途重置（剩余变多）→ 按 0 夹住，不显示"消耗了 -60%"。
        assertEquals(
            0.0,
            quotaConsumedPercent(listOf(point(1_000L, 10.0), point(2_000L, 70.0))),
            1e-9,
        )
    }

    @Test
    fun cycleRangeComesFromTheNewestCompleteReading() {
        val points = listOf(
            point(1_000L, 90.0, start = "2026-11-01T00:00:00Z", resetsAt = "2026-11-08T00:00:00Z"),
            point(2_000L, 80.0, start = "2026-11-02T00:00:00Z", resetsAt = "2026-11-09T00:00:00Z"),
        )
        assertEquals(
            "2026-11-02T00:00:00Z" to "2026-11-09T00:00:00Z",
            quotaCycleRange(points),
        )
        // 最新点没有起止时间 → 退回上一个带时间的点。
        val partial = listOf(
            point(1_000L, 90.0, start = "2026-11-01T00:00:00Z", resetsAt = "2026-11-08T00:00:00Z"),
            point(2_000L, 85.0),
        )
        assertEquals("2026-11-01T00:00:00Z" to "2026-11-08T00:00:00Z", quotaCycleRange(partial))
        // 全都没有起止时间（或起止不合法）→ null，宁可不估算。
        assertNull(quotaCycleRange(listOf(point(1_000L, 90.0))))
        assertNull(
            quotaCycleRange(
                listOf(point(1_000L, 90.0, start = "2026-11-08T00:00:00Z", resetsAt = "2026-11-01T00:00:00Z")),
            ),
        )
    }

    @Test
    fun parsesHoldsEstimateAndReasons() {
        val accounts = parseQuotaHolds(
            """
            [{"provider":"anthropic","user":"alice","windows":[
               {"name":"week","holds":{"tokens":1234567,"cost":12.345,"priced":true,"used":42.5,
                 "routed":{"tokens":524288,"calls":37,"cacheRead":1024},
                 "since":"2026-11-02T00:00:00Z"}},
               {"name":"5h","holdsWhy":"low"},
               {"name":"month","holdsWhy":"credits"},
               {"name":"","holdsWhy":"none"}
             ]},
             {"provider":"","user":"x","windows":[{"name":"week","holdsWhy":"log"}]}]
            """.trimIndent(),
        )
        assertEquals(1, accounts.size)
        val windows = accounts.single().windows
        // 空名字的窗口丢掉。
        assertEquals(listOf("week", "5h", "month"), windows.map { it.name })
        val estimate = windows[0].estimate
        assertEquals(1234567L, estimate?.tokens)
        assertEquals(12.345, estimate?.cost ?: 0.0, 1e-9)
        assertEquals(true, estimate?.priced)
        assertEquals(42.5, estimate?.used ?: 0.0, 1e-9)
        assertEquals(524288L, estimate?.routedTokens)
        assertEquals(37L, estimate?.routedCalls)
        assertEquals(1024L, estimate?.cacheRead)
        // 只有原因的窗口：estimate 必须是 null（不能凭空造一个 0 Token 的估算）。
        assertEquals(null, windows[1].estimate)
        assertEquals("low", windows[1].why)
        assertEquals("credits", windows[2].why)
    }

    @Test
    fun holdsWhyLabelsAreHonestAboutGaps() {
        val low = quotaHoldsWhyLabel("low")
        val credits = quotaHoldsWhyLabel("credits")
        val log = quotaHoldsWhyLabel("log")
        val none = quotaHoldsWhyLabel("none")
        val clock = quotaHoldsWhyLabel("clock")
        // 五个原因都要说清"为什么估不出来"，而不是含糊的"无法估算"。
        assertTrue(low.contains("5%"))
        assertTrue(credits.contains("credits"))
        assertTrue(log.contains("日志"))
        assertTrue(none.contains("没有"))
        assertTrue(clock.contains("起止"))
        // 大写也认；未知代码原样回显；空串给兜底文案。
        assertEquals(none, quotaHoldsWhyLabel("NONE"))
        assertEquals("weird", quotaHoldsWhyLabel("weird"))
        assertEquals("未说明原因", quotaHoldsWhyLabel(""))
    }

    @Test
    fun accountLabelSkipsBlankHalves() {
        assertEquals("anthropic · alice", quotaAccountLabel("anthropic", "alice"))
        assertEquals("anthropic", quotaAccountLabel("anthropic", ""))
        assertEquals("alice", quotaAccountLabel("", "alice"))
        assertEquals("", quotaAccountLabel("", ""))
        assertEquals("anthropic · alice", quotaAccountLabel("  anthropic ", " alice "))
    }

    @Test
    fun isoMillisParsesRfc3339AndGivesUpQuietly() {
        assertEquals(0L, parseIsoMillis(""))
        assertEquals(0L, parseIsoMillis("not a time"))
        val ms = parseIsoMillis("2026-11-01T00:00:00Z")
        assertTrue(ms > 0L)
        // 带偏移的写法也要认（服务端用 UTC，但别处可能给 +08:00）。
        assertEquals(ms, parseIsoMillis("2026-11-01T08:00:00+08:00"))
    }

    @Test
    fun malformedBodiesAreExplicitErrors() {
        for (body in listOf("not json", "", "{}")) {
            assertTrue(runCatching { parseQuotaHistory(body) }.exceptionOrNull() is ManagementApiException)
            assertTrue(runCatching { parseQuotaHolds(body) }.exceptionOrNull() is ManagementApiException)
        }
        // 空数组是合法回包（就是"没有读数"），不是错误。
        assertTrue(parseQuotaHistory("[]").isEmpty())
        assertTrue(parseQuotaHolds("[]").isEmpty())
    }

    @Test
    fun emptyHistoryHasAnHonestHint() {
        val hint = quotaHistoryEmptyHint()
        assertTrue(hint.contains("刷新"))
        assertTrue(hint.contains("曲线"))
    }
}
