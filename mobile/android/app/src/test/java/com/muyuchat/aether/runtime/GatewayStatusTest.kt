package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class GatewayStatusTest {
    private val running = GatewayStatus(
        state = GatewayState.RUNNING,
        addr = "127.0.0.1:43121",
        adminSession = "admin-secret-value",
        callerKey = "caller-secret-value",
    )

    @Test
    fun notificationShowsAddressButNeverKeys() {
        val text = notificationText(running)
        assertTrue(text.contains("127.0.0.1:43121"))
        assertFalse(text.contains("admin-secret-value"))
        assertFalse(text.contains("caller-secret-value"))
    }

    @Test
    fun labelsAreHonest() {
        assertEquals("启动失败 · start_failed", notificationText(GatewayStatus(state = GatewayState.FAILED, errorCode = "start_failed")))
        assertEquals("已停止", stateLabel(GatewayStatus()))
        assertEquals("运行中", stateLabel(running))
    }
}
