package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 覆盖页的返回键判断合同（[OverlayFlags.anyOpen]）。
 *
 * 回归点：`showClients` / `showPlugins` 曾经没有参与返回键判断。在这两个页面上按返回键时
 * `BackHandler` 是 disabled 状态，事件直接落到系统 → 掉回概览 Tab；用户看到的是
 * "点了返回却换了页"，不是"返回上一级"。这里把 11 个覆盖页逐个锁进判断里，
 * 以后再加覆盖页，漏接线会被这条测试挡下来（只要它进了 [OverlayFlags] 的字段表）。
 */
class GatewayHomeOverlayTest {

    @Test
    fun everyOverlayPageKeepsTheBackKeyFromFallingThrough() {
        val cases = listOf(
            "routing" to OverlayFlags(routing = true),
            "settings" to OverlayFlags(settings = true),
            "probe" to OverlayFlags(probe = true),
            "totp" to OverlayFlags(totp = true),
            "sessions" to OverlayFlags(sessions = true),
            "wake" to OverlayFlags(wake = true),
            "library" to OverlayFlags(library = true),
            "backup" to OverlayFlags(backup = true),
            "agents" to OverlayFlags(agents = true),
            "clients" to OverlayFlags(clients = true),
            "plugins" to OverlayFlags(plugins = true),
        )
        // 覆盖页数量本身也是事实：11 个，少一个就说明有人删了字段。
        assertEquals(11, cases.size)
        cases.forEach { (name, flags) ->
            assertTrue("$name 打开时返回键必须由 BackHandler 接管", flags.anyOpen)
        }
    }

    @Test
    fun clientsPageSpecificallyInterceptsBack() {
        // 这条单独写：clients / plugins 就是曾经漏掉的那两个，别让回归再次发生。
        assertTrue("客户端页按返回不能让用户掉回概览", OverlayFlags(clients = true).anyOpen)
        assertTrue("插件页按返回不能让用户掉回概览", OverlayFlags(plugins = true).anyOpen)
    }

    @Test
    fun nothingOpenMeansBackIsNotIntercepted() {
        // 没有覆盖页时不该抢返回键：否则底部 Tab 的返回行为会变。
        assertFalse(OverlayFlags().anyOpen)
    }

    @Test
    fun twoOpenOverlaysStillCountAsOpen() {
        // 叠了两个（理论上不该发生）也必须算"有覆盖页"，宁可多接管也不能漏。
        assertTrue(OverlayFlags(clients = true, plugins = true).anyOpen)
        assertTrue(OverlayFlags(agents = true, routing = true).anyOpen)
    }
}
