package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 订阅计划与到期（`GET /api/usage/quotas`）。
 *
 * **真实形状是数组**：`handleUsageQuotas` 直接 `writeJSON(cards)` → `[{provider,plan,until,balance,…}]`。
 * 第一版按"以 user 为键的对象"解析，真机上直接 `invalid_response`——这个测试就是那次事故的回归锁。
 */
class QuotaPlanApiTest {
    /** 真实形状：数组。 */
    private val arrayBody = """
        [{"provider":"claude-sub","name":"Claude","user":"a@b.c","plan":"Max 5x","until":1767225600000,"balance":"12.5"},
         {"provider":"qoder-cn","name":"Qoder","user":"d@e.f"},
         {"provider":"cursor","name":"Cursor","user":"g@h.i","plan":"Pro","until":0}]
    """.trimIndent()

    /** 兼容形状：以 user 为键的对象（`/api/login/usage` 那种）。 */
    private val objectBody = """
        {"a@b.c":{"provider":"claude-sub","plan":"Max 5x","until":1767225600000,"balance":"12.5"}}
    """.trimIndent()

    @Test
    fun parsesRealArrayShape() {
        val plans = parseQuotaPlans(arrayBody)
        // qoder-cn 既无 plan 也无 until → 不占屏幕
        assertEquals(2, plans.size)
        assertTrue(plans.none { it.provider == "qoder-cn" })
        val claude = plans.first { it.provider == "claude-sub" }
        assertEquals("Max 5x", claude.plan)
        assertEquals(1767225600000L, claude.untilMs)
        assertEquals("12.5", claude.balance)
    }

    @Test
    fun alsoAcceptsObjectShape() {
        val plans = parseQuotaPlans(objectBody)
        assertEquals(1, plans.size)
        assertEquals("Max 5x", plans[0].plan)
    }

    @Test
    fun accountIdentifiersAreNotCarried() {
        val plans = parseQuotaPlans(arrayBody)
        val rendered = plans.joinToString(" ") { quotaPlanLine(it, 1767225600000L) } + quotaPlanSummary(plans)
        listOf("a@b.c", "d@e.f", "g@h.i").forEach {
            assertTrue("账号标识「$it」不该被渲染", !rendered.contains(it))
        }
    }

    @Test
    fun lineUsesDateNotCountdown() {
        val plans = parseQuotaPlans(arrayBody)
        val line = quotaPlanLine(plans.first { it.provider == "claude-sub" }, 1767225600000L)
        assertTrue(line.contains("claude-sub"))
        assertTrue(line.contains("Max 5x"))
        assertTrue(line.contains("到期"))
        assertTrue("不写倒计时：$line", !line.contains("还剩"))
    }

    @Test
    fun expiredAndUnknownUntilAreHonest() {
        val plans = parseQuotaPlans(arrayBody)
        val expired = quotaPlanLine(plans.first { it.provider == "claude-sub" }, 1798761600000L)
        assertTrue("已过期要说清楚：$expired", expired.contains("已于"))
        val noUntil = quotaPlanLine(plans.first { it.provider == "cursor" }, 1767225600000L)
        assertTrue(noUntil.contains("没有到期日"))
    }

    @Test
    fun emptySummary() {
        assertTrue(quotaPlanSummary(emptyList()).contains("没有订阅计划"))
    }

    @Test
    fun emptyArrayIsFineButGarbageIsNot() {
        assertTrue(parseQuotaPlans("[]").isEmpty())
        for (b in listOf("not-json", "", "123")) {
            val error = runCatching { parseQuotaPlans(b) }.exceptionOrNull()
            assertTrue("「$b」应被拒绝", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
