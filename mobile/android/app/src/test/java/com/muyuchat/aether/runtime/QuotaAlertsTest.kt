package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 额度提醒：`GET /api/alerts`（`gateway.go:874` → `quota_alert.go:225-244`）。
 *
 * 回包：`{"alerts":[{key,kind,provider_id,provider,account_id,account,window,used,resets_at,message,at}],
 * "rejected":{callerKeyId:次数}}`；桌面卡片在 `internal/web/ui2/js/views/gateway.js:86-100`。
 */
class QuotaAlertsTest {
    @Test
    fun probesAccessAdminSurfaceWithSession() {
        val running = GatewayStatus(
            state = GatewayState.RUNNING,
            addr = "127.0.0.1:8080",
            adminSession = "session-token",
        )
        // /api/alerts 在 /api/ 管理面：必须带会话凭据，否则 401
        assertTrue(probeNeedsSession("/api/alerts"))
        assertEquals("Bearer session-token", probeAuthorization("/api/alerts", running))
        // 匿名端点仍然一个字节都不带
        assertFalse(probeNeedsSession("/healthz"))
        assertEquals(null, probeAuthorization("/healthz", running))
    }

    @Test
    fun parsesAlertsAndRejected() {
        val report = parseQuotaAlerts(
            """
            {"alerts":[
              {"key":"claude|a1|5h|expiring","kind":"expiring","provider_id":"claude-sub",
               "provider":"Claude","account_id":"a1","account":"主力","window":"5h",
               "used":0.12,"resets_at":1762300000,"message":"窗口即将重置但还剩 88%","at":1762290000},
              {"key":"codex|a2|week|exhausted","kind":"exhausted","provider_id":"codex",
               "used":1.0,"message":"额度已用尽"}],
             "rejected":{"k1":3,"k2":0}}
            """.trimIndent(),
        )
        assertEquals(2, report.alerts.size)
        val first = report.alerts[0]
        assertEquals("expiring", first.kind)
        assertEquals("claude-sub", first.providerId)
        assertEquals("Claude", first.provider)
        assertEquals("主力", first.account)
        assertEquals("5h", first.window)
        assertEquals(0.12, first.used, 1e-9)
        assertEquals(1762300000L, first.resetsAt)
        assertTrue(first.message.contains("即将重置"))
        // rejected 只收正数
        assertEquals(mapOf("k1" to 3), report.rejected)
    }

    @Test
    fun emptyAndMissingFieldsAreTolerated() {
        assertEquals(0, parseQuotaAlerts("""{"alerts":[],"rejected":{}}""").alerts.size)
        assertEquals(0, parseQuotaAlerts("{}").alerts.size)
        // 缺 kind 的条目丢弃（没有 kind 无法决定 chip）
        assertEquals(0, parseQuotaAlerts("""{"alerts":[{"key":"x"}]}""").alerts.size)
        // used 缺省保持 0，且超出 0..1 的值被夹住
        val clamped = parseQuotaAlerts("""{"alerts":[{"kind":"low","used":1.8}]}""").alerts[0]
        assertEquals(1.0, clamped.used, 1e-9)
        val negative = parseQuotaAlerts("""{"alerts":[{"kind":"low","used":-3}]}""").alerts[0]
        assertEquals(0.0, negative.used, 1e-9)
    }

    @Test
    fun brokenBodiesAreInvalidResponse() {
        for (body in listOf("not json", "", "<html>401</html>", "[1,2]")) {
            val error = runCatching { parseQuotaAlerts(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun kindLabelsMatchDesktopAndUnknownFallsBackToLow() {
        assertEquals("即将重置", quotaAlertKindLabel("expiring"))
        assertEquals("已用尽", quotaAlertKindLabel("exhausted"))
        assertEquals("额度偏低", quotaAlertKindLabel("low"))
        // 桌面 else 分支把未知 kind 当「额度偏低」
        assertEquals("额度偏低", quotaAlertKindLabel("something-else"))
        assertEquals("即将重置", quotaAlertKindLabel("  EXPIRING "))
        // 只有 expiring 是 amber，其余一律 red
        assertTrue(quotaAlertKindIsAmber("expiring"))
        assertFalse(quotaAlertKindIsAmber("exhausted"))
        assertFalse(quotaAlertKindIsAmber("low"))
    }

    @Test
    fun rowsAreCappedAtThreeLikeDesktop() {
        val alerts = (1..5).map {
            QuotaAlert(
                key = "k$it", kind = "low", providerId = "p$it", provider = "P$it",
                accountId = "a$it", account = "", window = "", used = 0.0,
                resetsAt = 0L, message = "m$it", at = 0L,
            )
        }
        assertEquals(3, quotaAlertRows(alerts).size)
        assertEquals(listOf("P1", "P2", "P3"), quotaAlertRows(alerts).map { it.provider })
        // 少于 3 条时原样返回
        assertEquals(1, quotaAlertRows(alerts.take(1)).size)
    }

    @Test
    fun lineShowsChannelAccountWindowAndUsage() {
        val alert = QuotaAlert(
            key = "k", kind = "expiring", providerId = "claude-sub", provider = "Claude",
            accountId = "a1", account = "主力", window = "5h", used = 0.12,
            resetsAt = 0L, message = "窗口即将重置但还剩 88%", at = 0L,
        )
        val line = quotaAlertLine(alert)
        assertTrue(line, line.startsWith("Claude · 主力 · 5h"))
        assertTrue(line, line.contains("已用 12%"))
        assertTrue(line, line.contains("窗口即将重置但还剩 88%"))
        // used 缺省（0）时不显示「已用 0%」——不编一个假读数
        val noUsage = alert.copy(used = 0.0)
        assertFalse(quotaAlertLine(noUsage).contains("已用"))
        // 渠道名缺失时退回 provider_id
        assertTrue(quotaAlertLine(alert.copy(provider = "")).startsWith("claude-sub"))
    }

    @Test
    fun countLabelMatchesDesktopChip() {
        assertEquals("3 条", quotaAlertCountLabel(3))
        assertEquals("1 条", quotaAlertCountLabel(1))
    }
}
