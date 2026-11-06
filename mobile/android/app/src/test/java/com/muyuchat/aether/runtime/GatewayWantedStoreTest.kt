package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 「冷启动自动恢复网关」的数据层合同。
 *
 * 背景（真机 f9d98aa3 复现）：`am force-stop` 之后 Android 不会重拉前台 Service，
 * 而手机上所有页面读的都是 App 内 loopback 上的网关——网关没起，渠道/账号/设置
 * 全渲染成空，用户看到的就是"接入的渠道全没了"。数据一直在 `no_backup/aether/`。
 *
 * 这里的判定全部是纯函数，脱离 Android 可测；IO 只在 [GatewayWantedStore] 里。
 */
class GatewayWantedStoreTest {

    @Test
    fun defaultIsNotWanted() {
        // 首次安装 / 文件不存在 / 内容损坏 → 一律不自动启动（绝不擅自起服务）。
        assertEquals(GatewayWanted(wanted = false, failures = 0), GatewayWanted())
        assertEquals(GatewayWanted(), parseGatewayWanted(null))
        assertEquals(GatewayWanted(), parseGatewayWanted(""))
        assertEquals(GatewayWanted(), parseGatewayWanted("   "))
        assertEquals(GatewayWanted(), parseGatewayWanted("{坏掉的 json"))
        assertEquals(GatewayWanted(), parseGatewayWanted("[]"))
    }

    @Test
    fun parseRejectsNonBooleanWanted() {
        // 只认真正的布尔：字符串 "true" / 数字 1 都当损坏，回落到安全默认。
        assertFalse(parseGatewayWanted("""{"wanted":"true"}""").wanted)
        assertFalse(parseGatewayWanted("""{"wanted":1}""").wanted)
        assertTrue(parseGatewayWanted("""{"wanted":true}""").wanted)
    }

    @Test
    fun parseClampsFailuresIntoRange() {
        assertEquals(0, parseGatewayWanted("""{"wanted":true,"failures":-5}""").failures)
        assertEquals(
            AUTO_START_MAX_FAILURES,
            parseGatewayWanted("""{"wanted":true,"failures":999}""").failures,
        )
        // 非数字的 failures 当 0（不因为一个坏字段就不恢复）。
        val parsed = parseGatewayWanted("""{"wanted":true,"failures":"oops"}""")
        assertTrue(parsed.wanted)
        assertEquals(0, parsed.failures)
    }

    @Test
    fun jsonRoundTrips() {
        val state = GatewayWanted(wanted = true, failures = 2)
        assertEquals(state, parseGatewayWanted(buildGatewayWantedJson(state)))
        assertEquals(GatewayWanted(), parseGatewayWanted(buildGatewayWantedJson(GatewayWanted())))
    }

    @Test
    fun autoStartOnlyWhenWantedAndActionable() {
        val yes = GatewayWanted(wanted = true)
        val no = GatewayWanted(wanted = false)

        // 用户上次主动开着 + 当前已停止/失败 → 恢复。
        assertTrue(shouldAutoStart(yes, GatewayState.STOPPED))
        assertTrue(shouldAutoStart(yes, GatewayState.FAILED))

        // 已经在跑 → 不重复启动。
        assertFalse(shouldAutoStart(yes, GatewayState.RUNNING))

        // 过渡态不抢：STARTING/STOPPING 让别人收敛。
        assertFalse(shouldAutoStart(yes, GatewayState.STARTING))
        assertFalse(shouldAutoStart(yes, GatewayState.STOPPING))

        // 用户点了停止 → 任何状态都不恢复。
        GatewayState.entries.forEach { assertFalse(shouldAutoStart(no, it)) }
    }

    @Test
    fun successResetsFailureCounter() {
        val next = afterAutoStart(GatewayWanted(wanted = true, failures = 2), running = true)
        assertEquals(GatewayWanted(wanted = true, failures = 0), next)
    }

    @Test
    fun failureKeepsWantedUntilTheCapThenGivesUp() {
        // 第 1、2 次失败：保留意图，下次再试。
        assertEquals(GatewayWanted(true, 1), afterAutoStart(GatewayWanted(true, 0), false))
        assertEquals(GatewayWanted(true, 2), afterAutoStart(GatewayWanted(true, 1), false))
        // 第 3 次连续失败：复位，避免每次开 App 都白试一遍。
        assertEquals(GatewayWanted(false, 0), afterAutoStart(GatewayWanted(true, 2), false))
    }

    @Test
    fun repeatedFailuresNeverExceedTheCap() {
        var state = GatewayWanted(wanted = true, failures = 0)
        repeat(10) { state = afterAutoStart(state, running = false) }
        assertFalse(state.wanted)
        assertEquals(0, state.failures)
    }
}
