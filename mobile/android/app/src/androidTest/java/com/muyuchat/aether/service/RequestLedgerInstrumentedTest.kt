package com.muyuchat.aether.service

import android.Manifest
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
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.net.HttpURLConnection
import java.net.URL

/**
 * Drives the real loopback gateway with one fake account so the ledger/usage
 * pages have non-empty, real rows. The upstream rejection is expected; no
 * network success is fabricated.
 */
@RunWith(AndroidJUnit4::class)
class RequestLedgerInstrumentedTest {
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
        val pfd = InstrumentationRegistry.getInstrumentation().uiAutomation
            .executeShellCommand("am start -W -n ${context.packageName}/.MainActivity")
        ParcelFileDescriptor.AutoCloseInputStream(pfd).use { it.readBytes() }
    }

    @After
    fun stopGateway() {
        context.stopService(Intent(context, AetherGatewayService::class.java))
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            val state = GatewayServiceController.status.value.state
            if (state != GatewayState.RUNNING && state != GatewayState.STARTING && state != GatewayState.STOPPING) {
                return
            }
            Thread.sleep(50)
        }
    }

    @Test
    fun fakeAccountRejectionAppearsInUsageAndRequestLedger() {
        context.startForegroundService(
            Intent(context, AetherGatewayService::class.java).setAction(AetherGatewayService.ACTION_START),
        )
        val running = awaitRunning(20_000)
        val base = "http://${running.addr}"

        val addCode = postJson(
            "$base/api/keys/add",
            running.adminSession,
            """{"id":"claude-sub","key":"fixture-invalid-key","name":"ledger-test"}""",
        )
        assertTrue("add account status=$addCode", addCode == 200)

        val chatCode = postJson(
            "$base/v1/chat/completions",
            running.callerKey,
            """{"model":"claude-sub/claude-sonnet-4-6","messages":[{"role":"user","content":"ping"}],"max_tokens":1,"stream":false}""",
        )
        assertTrue("chat should be rejected upstream, got $chatCode", chatCode >= 400)

        val usage = awaitJson("$base/api/usage?period=today", running.adminSession, 20_000) {
            it.optLong("calls", 0L) >= 1L
        }
        assertTrue("usage calls=${usage.optLong("calls")}", usage.optLong("calls") >= 1L)
        assertTrue("usage errors=${usage.optLong("errors")}", usage.optLong("errors") >= 1L)

        val ledger = awaitJson("$base/api/usage/requests?period=today&limit=20", running.adminSession, 20_000) {
            (it.optJSONArray("rows")?.length() ?: 0) >= 1
        }
        val rows = ledger.getJSONArray("rows")
        var failed = 0
        for (i in 0 until rows.length()) {
            val row = rows.getJSONObject(i)
            if (row.optInt("status", 0) >= 400) {
                failed++
                Log.i(TAG, "failed row model=${row.optString("model")} provider=${row.optString("providerName")} status=${row.optInt("status")}")
            }
        }
        assertTrue("no failed ledger row in ${rows.length()} rows", failed >= 1)
        Log.i(TAG, "usage calls=${usage.optLong("calls")} errors=${usage.optLong("errors")} rows=${rows.length()}")
    }

    private fun awaitRunning(timeoutMs: Long): GatewayStatus {
        val deadline = System.currentTimeMillis() + timeoutMs
        var last = GatewayServiceController.status.value
        while (System.currentTimeMillis() < deadline) {
            last = GatewayServiceController.status.value
            if (last.state == GatewayState.RUNNING) {
                return last
            }
            if (last.state == GatewayState.FAILED) {
                fail("gateway failed: ${last.errorCode}")
            }
            Thread.sleep(100)
        }
        fail("gateway did not reach RUNNING (${last.state}/${last.errorCode})")
        throw AssertionError("unreachable")
    }

    private fun postJson(url: String, token: String, body: String): Int {
        val connection = URL(url).openConnection() as HttpURLConnection
        connection.connectTimeout = 5_000
        connection.readTimeout = 15_000
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer $token")
        return try {
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            val code = connection.responseCode
            (if (code in 200..299) connection.inputStream else connection.errorStream)
                ?.use { it.readBytes() }
            code
        } finally {
            connection.disconnect()
        }
    }

    private fun awaitJson(
        url: String,
        token: String,
        timeoutMs: Long,
        predicate: (JSONObject) -> Boolean,
    ): JSONObject {
        val deadline = System.currentTimeMillis() + timeoutMs
        var last = JSONObject()
        while (System.currentTimeMillis() < deadline) {
            last = getJson(url, token)
            if (predicate(last)) {
                return last
            }
            Thread.sleep(500)
        }
        fail("condition not met: ${last.toString().take(200)}")
        throw AssertionError("unreachable")
    }

    private fun getJson(url: String, token: String): JSONObject {
        val connection = URL(url).openConnection() as HttpURLConnection
        connection.connectTimeout = 5_000
        connection.readTimeout = 10_000
        connection.setRequestProperty("Authorization", "Bearer $token")
        return try {
            val body = connection.inputStream.bufferedReader().use { it.readText() }
            JSONObject(body)
        } finally {
            connection.disconnect()
        }
    }

    private companion object {
        const val TAG = "AetherTest"
    }
}
