package com.muyuchat.aether.runtime

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.Socket
import java.net.URL
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class NativeBridgeInstrumentedTest {
    @Test
    fun startsServesBothCredentialScopesAndStops() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val suffix = UUID.randomUUID().toString()
        val dataDir = File(context.noBackupFilesDir, "aether-it-$suffix")
        val cacheDir = File(context.filesDir, "aether-it-cache-$suffix")
        val tempDir = File(context.cacheDir, "aether-it-tmp-$suffix")

        assertEquals("mobilebridge/2", NativeBridge.nativeBridgeVersion())
        val config = buildRuntimeConfig(dataDir.absolutePath, cacheDir.absolutePath, tempDir.absolutePath)
        val start = JSONObject(NativeBridge.nativeStart(config))
        assertEquals("running", start.optString("state"))
        val addr = start.optString("addr")
        val admin = start.optString("adminSession")
        val caller = start.optString("callerKey")
        assertTrue("addr=$addr", addr.isNotEmpty())
        assertEquals("admin session length", 43, admin.length)
        assertEquals("caller key length", 43, caller.length)
        assertNotEquals(admin, caller)

        try {
            assertEquals(401, status("http://$addr/v1/models", null))
            assertEquals(200, status("http://$addr/v1/models", caller))
            assertEquals(401, status("http://$addr/v1/models", admin))
            assertEquals(401, status("http://$addr/api/providers", null))
            assertEquals(401, status("http://$addr/api/providers", caller))
            assertEquals(200, status("http://$addr/api/providers", admin))
        } finally {
            val stop = JSONObject(NativeBridge.nativeStop())
            assertEquals("stopped", stop.optString("state"))
            val host = addr.substringBeforeLast(':')
            val port = addr.substringAfterLast(':').toInt()
            try {
                Socket(host, port).use { fail("port $port still accepts connections") }
            } catch (_: IOException) {
                // expected: listener released
            }
            dataDir.deleteRecursively()
            cacheDir.deleteRecursively()
            tempDir.deleteRecursively()
        }

        assertEquals("stopped", JSONObject(NativeBridge.nativeGetStatus()).optString("state"))
    }

    private fun status(url: String, token: String?): Int {
        val connection = URL(url).openConnection() as HttpURLConnection
        connection.connectTimeout = 3_000
        connection.readTimeout = 3_000
        if (token != null) {
            connection.setRequestProperty("Authorization", "Bearer $token")
        }
        return try {
            connection.responseCode
        } finally {
            connection.disconnect()
        }
    }
}
