package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RoutingGroup
import com.muyuchat.aether.runtime.RoutingMember
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 路由组成员倍率（`credit_rate`）上屏的纯逻辑。
 *
 * 数据来源：`internal/core/gateway/groups_api.go:28`
 * `CreditRate float64 \`json:"credit_rate,omitempty"\``——**缺省即 0**，也就是"没配倍率"。
 * 桌面展示口径：`internal/web/ui2/js/views/misc.js:193`
 * `chip(m.providerName + ' · ' + m.name + (m.credit_rate ? '（×' + m.credit_rate + '）' : ''))`。
 * 这两条决定了下面所有断言：0 不显示、整数不带小数点、形态是"名字后括号"。
 */
class RoutingMemberChipTest {

    @Test
    fun creditLabelHidesUnsetAndBrokenRates() {
        // 0 = omitempty 缺省 = 没配倍率：不显示，绝不渲染成 `×0`（那会被读成"倍率是 0"）。
        assertEquals("", memberCreditLabel(0.0))
        // 负倍率/NaN/Inf 都是坏数据：宁可什么都不显示，也不要显示一个错乘数。
        assertEquals("", memberCreditLabel(-1.0))
        assertEquals("", memberCreditLabel(Double.NaN))
        assertEquals("", memberCreditLabel(Double.POSITIVE_INFINITY))
        assertEquals("", memberCreditLabel(Double.NEGATIVE_INFINITY))
    }

    @Test
    fun creditLabelMatchesDesktopFormatting() {
        // 整数倍率不带小数点：桌面 `'×' + 2` 得 "×2"，而不是 "×2.0"。
        assertEquals("×1", memberCreditLabel(1.0))
        assertEquals("×2", memberCreditLabel(2.0))
        // 小数原样保留（桌面 `'×' + 1.5` = "×1.5"）。
        assertEquals("×1.5", memberCreditLabel(1.5))
        assertEquals("×0.75", memberCreditLabel(0.75))
        assertEquals("×0.001", memberCreditLabel(0.001))
        // 很小的正数用小数字面量而不是科学计数法（`rate.toString()` 会印成 "1.0E-4"，读不出乘数）。
        assertEquals("×0.0001", memberCreditLabel(1.0e-4))
        // 小到 4 位小数都显示不出来（<0.00005）：不显示，而不是显示 "×0"。
        assertEquals("", memberCreditLabel(1.0e-6))
        // 四舍五入到 4 位：1.23456 → 1.2346（倍率精度到 4 位足够，也避免长尾小数撑爆 chip）。
        assertEquals("×1.2346", memberCreditLabel(1.23456))
    }

    @Test
    fun chipLabelJoinsProviderAndNameThenAppendsCredit() {
        // 桌面形态：`providerName · name（×1.5）`。
        assertEquals("OpenAI · gpt-4o（×1.5）", routingMemberChipLabel("OpenAI", "gpt-4o", 1.5))
        // 没配倍率：不带空括号（`（）` 比不显示更容易被误读）。
        assertEquals("OpenAI · gpt-4o", routingMemberChipLabel("OpenAI", "gpt-4o", 0.0))
        // 缺 provider 或 name 时用可用的那半，不留下孤零零的 "·"。
        assertEquals("gpt-4o（×2）", routingMemberChipLabel("", "gpt-4o", 2.0))
        assertEquals("OpenAI（×2）", routingMemberChipLabel("OpenAI", "", 2.0))
        // 两边都空：返回空串——只剩「（×2）」的 chip 读不出是什么，不如不显示。
        assertEquals("", routingMemberChipLabel("", "", 0.0))
        assertEquals("", routingMemberChipLabel("", "", 2.0))
        // 前后空格不算内容。
        assertEquals("OpenAI · gpt-4o", routingMemberChipLabel("  OpenAI  ", "  gpt-4o  ", 0.0))
    }

    @Test
    fun chipsFollowMemberOrderAndCarryCredit() {
        val group = group(
            members = listOf("openai/gpt-4o", "anthropic/claude"),
            info = listOf(
                member("openai/gpt-4o", providerName = "OpenAI", name = "gpt-4o", creditRate = 1.5),
                member("anthropic/claude", providerName = "Anthropic", name = "claude", creditRate = 0.0),
            ),
        )
        assertEquals(
            listOf("OpenAI · gpt-4o（×1.5）", "Anthropic · claude"),
            routingMemberChips(group),
        )
        assertEquals("", routingMemberOverflowLabel(group))
    }

    @Test
    fun unresolvedMembersFallBackToBareId() {
        // 没有 memberInfo 的成员：显示裸 id，且**不带倍率**（不知道就不猜）。
        val group = group(
            members = listOf("known/model", "orphan/id"),
            info = listOf(member("known/model", providerName = "P", name = "model", creditRate = 3.0)),
        )
        assertEquals(listOf("P · model（×3）", "orphan/id"), routingMemberChips(group))
        // 空 id 不进 chip 列表，也不进超额计数。
        val withBlank = group(members = listOf("", "a/b"), info = listOf(member("a/b", providerName = "P", name = "b")))
        assertEquals(listOf("P · b"), routingMemberChips(withBlank))
        assertEquals("", routingMemberOverflowLabel(withBlank))
    }

    @Test
    fun chipsAreCappedLikeDesktopAndReportOverflow() {
        // 桌面 `members.slice(0, 12)`：手机上同样封顶 12，多余的用 "+N" 交代。
        val ids = (1..15).map { "p/m$it" }
        val group = group(
            members = ids,
            info = ids.map { member(it, providerName = "P", name = it.substringAfter('/')) },
        )
        val chips = routingMemberChips(group)
        assertEquals(12, chips.size)
        assertEquals("P · m1", chips.first())
        assertEquals("P · m12", chips.last())
        assertEquals("+3", routingMemberOverflowLabel(group))
        // 自定义上限（详情页可能想列更多）也成立。
        assertEquals(3, routingMemberChips(group, limit = 3).size)
        assertEquals("+12", routingMemberOverflowLabel(group, limit = 3))
        // limit=0：一个都不显示，且全部算超额——避免"上限 0 + 不报超额"导致成员静默消失。
        assertTrue(routingMemberChips(group, limit = 0).isEmpty())
        assertEquals("+15", routingMemberOverflowLabel(group, limit = 0))
        // 负上限按 0 处理，不抛异常。
        assertTrue(routingMemberChips(group, limit = -5).isEmpty())
    }

    @Test
    fun emptyGroupProducesNothing() {
        val group = group(members = emptyList(), info = emptyList())
        assertTrue(routingMemberChips(group).isEmpty())
        assertEquals("", routingMemberOverflowLabel(group))
    }

    // ---- 构造辅助：只给要测的字段赋值，其余用默认值 ----

    private fun member(
        id: String,
        providerName: String = "",
        name: String = id,
        creditRate: Double = 0.0,
    ) = RoutingMember(
        id = id,
        name = name,
        providerName = providerName,
        providerOff = false,
        creditRate = creditRate,
    )

    private fun group(members: List<String>, info: List<RoutingMember>) = RoutingGroup(
        id = "g1",
        name = "组一",
        members = members,
        routing = "priority",
        routingEffective = "priority",
        warnings = emptyList(),
        ready = true,
        auto = false,
        disabled = false,
        memberInfo = info,
    )
}
