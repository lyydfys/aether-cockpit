package com.muyuchat.aether.service

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.IOException
import java.net.HttpURLConnection
import java.net.Socket
import java.net.URL

@RunWith(AndroidJUnit4::class)
class GatewayServiceInstrumentedTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @Before
    fun prepare() {
        if (Build.VERSION.SDK_INT >= 33 &&
            context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            val pfd = InstrumentationRegistry.getInstrumentation().uiAutomation
                .executeShellCommand("pm grant ${context.packageName} ${Manifest.permission.POST_NOTIFICATIONS}")
            ParcelFileDescriptor.AutoCloseInputStream(pfd).use { it.readBytes() }
        }
        Log.i(TAG, "notification permission ready")
    }

    @After
    fun ensureStopped() {
        context.stopService(Intent(context, AetherGatewayService::class.java))
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            val state = GatewayServiceController.status.value.state
            if (state != GatewayState.RUNNING &&
                state != GatewayState.STARTING &&
                state != GatewayState.STOPPING
            ) {
                Log.i(TAG, "after-test state=$state")
                return
            }
            Thread.sleep(50)
        }
        Log.i(TAG, "after-test still busy: ${GatewayServiceController.status.value}")
    }

    @Test
    fun serviceShowsNotificationServesBothScopesAndStops() {
        launchActivity()
        Log.i(TAG, "activity launch requested")
        context.startForegroundService(
            Intent(context, AetherGatewayService::class.java).setAction(AetherGatewayService.ACTION_START),
        )
        Log.i(TAG, "service start requested")
        val running = awaitState(GatewayState.RUNNING, 20_000)
        Log.i(TAG, "running addr=${running.addr} admin=${running.adminSession.length} caller=${running.callerKey.length}")
        assertTrue("addr=${running.addr}", running.addr.isNotEmpty())
        assertEquals(43, running.adminSession.length)
        assertEquals(43, running.callerKey.length)

        val manager = context.getSystemService(NotificationManager::class.java)
        val notification = awaitNotification(manager, running.addr, 10_000)
        assertNotNull("foreground notification missing", notification)
        val text = notification!!.notification.extras
            .getCharSequence(Notification.EXTRA_TEXT)
            .toString()
        Log.i(TAG, "notification text=$text")
        assertTrue("notification=$text", text.contains(running.addr))

        assertEquals(200, status("http://${running.addr}/v1/models", running.callerKey))
        assertEquals(401, status("http://${running.addr}/v1/models", running.adminSession))
        assertEquals(200, status("http://${running.addr}/api/providers", running.adminSession))
        assertEquals(401, status("http://${running.addr}/api/providers", running.callerKey))
        Log.i(TAG, "http matrix ok")

        // Same path as the notification's stop action.
        context.startService(
            Intent(context, AetherGatewayService::class.java).setAction(AetherGatewayService.ACTION_STOP),
        )
        Log.i(TAG, "service stop requested")
        awaitState(GatewayState.STOPPED, 15_000)
        assertTrue("notification not removed", awaitNotificationGone(manager, 10_000))
        assertPortClosed(running.addr)
        Log.i(TAG, "stopped and port released")
    }

    private fun launchActivity() {
        val pfd = InstrumentationRegistry.getInstrumentation().uiAutomation
            .executeShellCommand("am start -W -n ${context.packageName}/.MainActivity")
        val output = ParcelFileDescriptor.AutoCloseInputStream(pfd).use {
            it.readBytes().decodeToString()
        }
        Log.i(TAG, "am start: ${output.trim().replace('\n', ' ')}")
    }

    private fun awaitState(want: GatewayState, timeoutMs: Long): GatewayStatus {
        val deadline = System.currentTimeMillis() + timeoutMs
        var last = GatewayServiceController.status.value
        while (System.currentTimeMillis() < deadline) {
            last = GatewayServiceController.status.value
            if (last.state == want) {
                return last
            }
            Thread.sleep(50)
        }
        fail("state ${last.state} (${last.errorCode}) did not reach $want")
        throw AssertionError("unreachable")
    }

    private fun awaitNotification(
        manager: NotificationManager,
        expected: String,
        timeoutMs: Long,
    ): android.service.notification.StatusBarNotification? {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            val found = manager.activeNotifications
                .firstOrNull { it.id == AetherGatewayService.NOTIFICATION_ID }
            if (found != null) {
                val text = found.notification.extras
                    .getCharSequence(Notification.EXTRA_TEXT)
                    ?.toString()
                    .orEmpty()
                if (text.contains(expected)) {
                    return found
                }
            }
            Thread.sleep(50)
        }
        return null
    }

    private fun awaitNotificationGone(manager: NotificationManager, timeoutMs: Long): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            val present = manager.activeNotifications
                .any { it.id == AetherGatewayService.NOTIFICATION_ID }
            if (!present) {
                return true
            }
            Thread.sleep(50)
        }
        return false
    }

    private fun status(url: String, token: String): Int {
        val connection = URL(url).openConnection() as HttpURLConnection
        connection.connectTimeout = 5_000
        connection.readTimeout = 5_000
        connection.setRequestProperty("Authorization", "Bearer $token")
        return try {
            connection.responseCode
        } finally {
            connection.disconnect()
        }
    }

    private fun assertPortClosed(addr: String) {
        val host = addr.substringBeforeLast(':')
        val port = addr.substringAfterLast(':').toInt()
        try {
            Socket(host, port).use { fail("port $port still accepts connections") }
        } catch (_: IOException) {
            // expected: listener released
        }
    }

    private companion object {
        const val TAG = "AetherTest"
    }
}
