package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.MoveDirection
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 用量页卡片排序的纯逻辑（第 1 步）。
 *
 * 为什么排序放在客户端：`POST /api/usage/arrange`（`gateway.go:1009` →
 * `usage_quota_api.go:584-592`）是**空实现**——只把 `order` 原样回显，注释写明
 * "卡片/面板排序属于前端本地偏好（localStorage），服务端不需要持久化"。
 * 所以顺序由客户端持有，服务端不参与。这批断言钉住：默认顺序、移动语义、越界、持久化键名。
 */
class UsageCardOrderTest {

    @Test
    fun defaultOrderIsTheEnumOrderAndCoversEveryCard() {
        assertEquals(
            listOf("SCOPE", "KPI", "TREND", "BREAKDOWN", "CHECKIN", "QUOTA_HISTORY", "CALENDAR"),
            DefaultUsageCardOrder,
        )
        // 默认顺序覆盖所有卡片，不重不漏：缺一个就意味着那张卡永远不渲染。
        assertEquals(UsageCardKey.entries.size, DefaultUsageCardOrder.size)
        assertEquals(DefaultUsageCardOrder.toSet(), UsageCardKey.entries.map { it.name }.toSet())
    }

    @Test
    fun movingUpAndDownSwapsNeighbours() {
        val order = DefaultUsageCardOrder
        // TREND 上移一格 → 与 KPI 交换。
        assertEquals(
            listOf("SCOPE", "TREND", "KPI", "BREAKDOWN", "CHECKIN", "QUOTA_HISTORY", "CALENDAR"),
            moveUsageCard(order, "TREND", MoveDirection.Up),
        )
        // TREND 下移一格 → 与 BREAKDOWN 交换。
        assertEquals(
            listOf("SCOPE", "KPI", "BREAKDOWN", "TREND", "CHECKIN", "QUOTA_HISTORY", "CALENDAR"),
            moveUsageCard(order, "TREND", MoveDirection.Down),
        )
        // 移动不改变长度、也不丢卡片。
        val moved = moveUsageCard(order, "CALENDAR", MoveDirection.Up)
        assertEquals(order.size, moved.size)
        assertEquals(order.toSet(), moved.toSet())
    }

    @Test
    fun boundaryAndUnknownKeyAreNoOps() {
        val order = DefaultUsageCardOrder
        // 首行上移、末行下移：原样返回（不抛异常、不循环到另一端）。
        assertEquals(order, moveUsageCard(order, order.first(), MoveDirection.Up))
        assertEquals(order, moveUsageCard(order, order.last(), MoveDirection.Down))
        // 未知 key 原样返回。
        assertEquals(order, moveUsageCard(order, "NOPE", MoveDirection.Up))
        assertEquals(order, moveUsageCard(order, "", MoveDirection.Down))
    }

    @Test
    fun canMoveMirrorsTheBoundariesForGreyingOut() {
        val order = DefaultUsageCardOrder
        // 首行：↑ 不能动、↓ 可以动（按钮置灰而不是隐藏）。
        assertFalse(canMoveUsageCard(order, order.first(), MoveDirection.Up))
        assertTrue(canMoveUsageCard(order, order.first(), MoveDirection.Down))
        // 末行：↓ 不能动、↑ 可以动。
        assertFalse(canMoveUsageCard(order, order.last(), MoveDirection.Down))
        assertTrue(canMoveUsageCard(order, order.last(), MoveDirection.Up))
        // 中间的卡两个方向都能动。
        assertTrue(canMoveUsageCard(order, "TREND", MoveDirection.Up))
        assertTrue(canMoveUsageCard(order, "TREND", MoveDirection.Down))
        // 未知 key 两个方向都不能动。
        assertFalse(canMoveUsageCard(order, "NOPE", MoveDirection.Up))
        assertFalse(canMoveUsageCard(order, "NOPE", MoveDirection.Down))
    }

    @Test
    fun savedOrderKeepsUserIntentButDropsUnknownKeys() {
        // 用户把日历拖到最前：原样保留用户顺序（不按默认顺序重排）。
        val saved = listOf("CALENDAR", "SCOPE", "KPI", "TREND", "BREAKDOWN", "CHECKIN", "QUOTA_HISTORY")
        assertEquals(saved, normalizeUsageCardOrder(saved))
        // 旧版本存下的未知 key 丢掉；重复项去重（保留首次出现的位置）。
        val dirty = listOf("TREND", "TREND", "GONE_CARD", "KPI")
        assertEquals(
            listOf("TREND", "KPI", "SCOPE", "BREAKDOWN", "CHECKIN", "QUOTA_HISTORY", "CALENDAR"),
            normalizeUsageCardOrder(dirty),
        )
    }

    @Test
    fun missingCardsAreAppendedSoNewCardsNeverDisappear() {
        // 老用户只存了三张卡：新增的卡片必须补在末尾，否则它们永远不显示。
        val normalized = normalizeUsageCardOrder(listOf("KPI", "TREND"))
        assertEquals("KPI", normalized[0])
        assertEquals("TREND", normalized[1])
        assertEquals(DefaultUsageCardOrder.size, normalized.size)
        assertTrue(normalized.contains("CALENDAR"))
        assertTrue(normalized.contains("CHECKIN"))
    }

    @Test
    fun emptyOrAllInvalidOrderFallsBackToDefault() {
        assertEquals(DefaultUsageCardOrder, normalizeUsageCardOrder(null))
        assertEquals(DefaultUsageCardOrder, normalizeUsageCardOrder(emptyList()))
        assertEquals(DefaultUsageCardOrder, normalizeUsageCardOrder(listOf("nope", "", " ")))
    }

    @Test
    fun persistenceKeyIsStable() {
        // 改这个字符串 = 老用户的顺序静默清零，所以用断言把它钉住。
        assertEquals("usage_card_order", USAGE_CARD_ORDER_SAVED_KEY)
    }

    @Test
    fun labelsCoverEveryCardAndEchoUnknownKeys() {
        assertEquals("时间范围", usageCardLabel("SCOPE"))
        assertEquals("KPI 四格", usageCardLabel("KPI"))
        assertEquals("趋势", usageCardLabel("TREND"))
        assertEquals("用量分布", usageCardLabel("BREAKDOWN"))
        assertEquals("渠道签到", usageCardLabel("CHECKIN"))
        assertEquals("额度历史", usageCardLabel("QUOTA_HISTORY"))
        assertEquals("用量日历", usageCardLabel("CALENDAR"))
        // 未知 key 原样回显（弹窗里宁可显示怪名字，也不要空行）。
        assertEquals("WEIRD", usageCardLabel("WEIRD"))
        // 每个枚举项都有非空标签。
        assertTrue(UsageCardKey.entries.all { it.label.isNotBlank() })
    }
}
