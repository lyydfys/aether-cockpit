package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * 「首选端口」的持久化与校验。
 *
 * 背景：手机端**没有** Windows 那种运行中热切换（移动运行时从不注册
 * `Gateway.SetPortSwitcher`，`POST /api/settings/port` 在移动模式必然 400）。
 * 能做的是「用户设一个期望端口，下次 `nativeStart` 带上它」——
 * `RuntimeConfig.Port` 已存在，0 表示交给系统分配（`internal/mobile/runtime.go:45`）。
 *
 * 这里的纯函数是那条链路的唯一校验点，所以单独测：读写文件的部分只做 IO，
 * 判定全部落在这里。
 */
class PreferredPortTest {
    @Test
    fun absentOrBrokenStorageFallsBackToSystemAssigned() {
        // 0 = 系统分配。存储缺失/损坏时必须回落到这个安全值，不能猜一个端口。
        assertEquals(0, parsePreferredPort(null))
        assertEquals(0, parsePreferredPort(""))
        assertEquals(0, parsePreferredPort("not-json"))
        assertEquals(0, parsePreferredPort("{}"))
        assertEquals(0, parsePreferredPort("""{"port":"8080"}"""))
    }

    @Test
    fun storedPortRoundTrips() {
        assertEquals(8080, parsePreferredPort(buildPreferredPortJson(8080)))
        assertEquals(65535, parsePreferredPort(buildPreferredPortJson(65535)))
        assertEquals(0, parsePreferredPort(buildPreferredPortJson(0)))
    }

    @Test
    fun outOfRangeStoredValuesAreTreatedAsSystemAssigned() {
        assertEquals(0, parsePreferredPort("""{"port":-1}"""))
        assertEquals(0, parsePreferredPort("""{"port":70000}"""))
        assertEquals(0, parsePreferredPort("""{"port":1.5}"""))
    }

    @Test
    fun blankInputMeansSystemAssigned() {
        assertEquals(0, validatePreferredPortInput("").port)
        assertNull(validatePreferredPortInput("").error)
        assertEquals(0, validatePreferredPortInput("   ").port)
        assertNull(validatePreferredPortInput("   ").error)
    }

    @Test
    fun explicitZeroMeansSystemAssigned() {
        assertEquals(0, validatePreferredPortInput("0").port)
        assertNull(validatePreferredPortInput("0").error)
    }

    @Test
    fun validPortIsAccepted() {
        val result = validatePreferredPortInput("8080")
        assertEquals(8080, result.port)
        assertNull(result.error)
        assertEquals(65535, validatePreferredPortInput("65535").port)
        assertEquals(1, validatePreferredPortInput("1").port)
    }

    @Test
    fun nonNumericAndOutOfRangeAreRejectedWithAhumanMessage() {
        assertEquals("端口必须是 1-65535 的数字", validatePreferredPortInput("abc").error)
        assertEquals("端口必须是 1-65535 的数字", validatePreferredPortInput("80 80").error)
        assertNull(validatePreferredPortInput("abc").port)
        assertEquals("端口必须是 1-65535 的数字", validatePreferredPortInput("65536").error)
        assertEquals("端口必须是 1-65535 的数字", validatePreferredPortInput("-1").error)
    }

    /**
     * 端口被占用时 Go 侧返回 `listen_failed`；UI 必须能显示稳定错误码，
     * 而不是笼统的"启动失败"。这条锁住「错误码可透出」这个前提。
     */
    @Test
    fun startFailureKeepsTheStableErrorCode() {
        val snapshot = RuntimeSnapshot.parse(
            """{"state":"failed","addr":"","startedAt":0,"errorCode":"listen_failed","adminSession":"","callerKey":""}""",
        )
        assertEquals(false, snapshot.running)
        assertEquals("listen_failed", snapshot.errorCode)
        val status = GatewayStatus(state = GatewayState.FAILED, errorCode = snapshot.errorCode)
        assertEquals("启动失败 · listen_failed", notificationText(status))
    }
}
