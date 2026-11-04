package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.MoveDirection
import com.muyuchat.aether.runtime.buildProvidersArrangeJson
import com.muyuchat.aether.runtime.moveProvider
import com.muyuchat.aether.runtime.moveProviderOrder
import com.muyuchat.aether.runtime.providersArrangePath
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道排序（`POST /api/providers/arrange`，`gateway.go:882`）。
 *
 * 手机端用**上移/下移**代替桌面拖拽——这是平台适配，不是缺功能：
 * 触屏长列表在 1.3× 字号下拖拽容易误触，上移/下移可点、可读屏、可撤销。
 * 服务端只保留 `provider.Get(id)` 认识的 id，未知 id 静默丢弃（`provider_switch_api.go:102-121`）。
 *
 * **方向约定（以 UI 为准，已定死）**：
 * `↑` Up = 在数组里索引 **-1**（更靠前）；`↓` Down = 索引 **+1**（更靠后）。
 * 新代码一律用 `moveProvider(order, id, MoveDirection.*)`，不要传裸 delta。
 */
class ChannelsProviderOrderTest {
    private val order = listOf("claude-sub", "codex", "cursor", "trae-cn")

    @Test
    fun moveUpAndDownShiftByOne() {
        // codex 在 index 1：
        assertEquals(
            "↓ 下移一格 → codex 到 index 2",
            listOf("claude-sub", "cursor", "codex", "trae-cn"),
            moveProvider(order, "codex", MoveDirection.Down),
        )
        assertEquals(
            "↑ 上移一格 → codex 到 index 0",
            listOf("codex", "claude-sub", "cursor", "trae-cn"),
            moveProvider(order, "codex", MoveDirection.Up),
        )
    }

    @Test
    fun legacyDeltaWrapperKeepsTheSameDirection() {
        // 兼容层：裸 delta 的正负必须与方向一致（+1 = 下移），避免旧调用点方向反了
        assertEquals(
            moveProvider(order, "codex", MoveDirection.Down),
            moveProviderOrder(order, "codex", +1),
        )
        assertEquals(
            moveProvider(order, "codex", MoveDirection.Up),
            moveProviderOrder(order, "codex", -1),
        )
    }

    @Test
    fun edgesAreClampedInsteadOfThrowing() {
        // 首行再上移：原样返回（按钮置灰由 UI 负责，函数本身不抛）
        assertEquals(order, moveProvider(order, "claude-sub", MoveDirection.Up))
        // 末行再下移：原样返回
        assertEquals(order, moveProvider(order, "trae-cn", MoveDirection.Down))
    }

    @Test
    fun unknownIdAndZeroDeltaAreNoOps() {
        assertEquals(order, moveProvider(order, "not-a-provider", MoveDirection.Down))
        assertEquals(order, moveProvider(order, "codex", MoveDirection.Up).let { moveProvider(it, "codex", MoveDirection.Down) })
        assertEquals(order, moveProviderOrder(order, "codex", 0))
        assertEquals(emptyList<String>(), moveProvider(emptyList(), "x", MoveDirection.Up))
    }

    @Test
    fun movingKeepsEveryEntryExactlyOnce() {
        val moved = moveProvider(order, "cursor", MoveDirection.Up)
        assertEquals("不能丢项", order.size, moved.size)
        assertEquals("不能重复", order.toSet(), moved.toSet())
    }

    @Test
    fun requestCarriesTheFullOrderAsJsonArray() {
        val body = JSONObject(buildProvidersArrangeJson(order))
        val array = body.getJSONArray("order")
        assertEquals(order.size, array.length())
        assertEquals("claude-sub", array.getString(0))
        assertEquals("trae-cn", array.getString(3))
        assertEquals("路径必须与 gateway 一致", "/api/providers/arrange", providersArrangePath())
        // 只提交顺序，不带别的字段（服务端只读 order）
        assertTrue(body.length() == 1)
    }
}
