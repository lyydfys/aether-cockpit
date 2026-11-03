package com.muyuchat.aether.runtime

import org.json.JSONObject

/**
 * Narrow JNI surface over the Go c-shared runtime.
 *
 * The Go library exports plain C functions; `libaether_jni.so` resolves them
 * with dlopen and adapts them to these four static methods. Both libraries are
 * packaged in the APK's arm64-v8a lib directory.
 */
class NativeBridge private constructor() {
    companion object {
        init {
            System.loadLibrary("aether_jni")
        }

        @JvmStatic
        external fun nativeBridgeVersion(): String

        @JvmStatic
        external fun nativeStart(configJson: String): String

        @JvmStatic
        external fun nativeGetStatus(): String

        @JvmStatic
        external fun nativeStop(): String
    }
}

/** Runtime status mirrored from the Go `RuntimeStatus` JSON contract. */
data class RuntimeSnapshot(
    val state: String,
    val addr: String,
    val startedAt: Long,
    val errorCode: String,
    val adminSession: String,
    val callerKey: String,
) {
    val running: Boolean get() = state == "running"

    companion object {
        fun parse(json: String): RuntimeSnapshot {
            val obj = JSONObject(json)
            return RuntimeSnapshot(
                state = obj.optString("state"),
                addr = obj.optString("addr"),
                startedAt = obj.optLong("startedAt"),
                errorCode = obj.optString("errorCode"),
                adminSession = obj.optString("adminSession"),
                callerKey = obj.optString("callerKey"),
            )
        }
    }
}

internal fun jsonQuote(value: String): String = buildString {
    append('"')
    for (ch in value) {
        when (ch) {
            '\\' -> append("\\\\")
            '"' -> append("\\\"")
            '\n' -> append("\\n")
            '\r' -> append("\\r")
            '\t' -> append("\\t")
            else -> if (ch < ' ') append("\\u%04x".format(ch.code)) else append(ch)
        }
    }
    append('"')
}

/**
 * Builds the exact JSON accepted by Go's RuntimeConfig decoder. The decoder
 * rejects unknown fields, so this builder is the single source of truth.
 */
internal fun buildRuntimeConfig(
    dataDir: String,
    cacheDir: String,
    tempDir: String,
    listenHost: String = "127.0.0.1",
    port: Int = 0,
    accountKey: String? = null,
): String = buildString {
    append('{')
    append("\"dataDir\":").append(jsonQuote(dataDir)).append(',')
    append("\"cacheDir\":").append(jsonQuote(cacheDir)).append(',')
    append("\"tempDir\":").append(jsonQuote(tempDir)).append(',')
    append("\"listenHost\":").append(jsonQuote(listenHost)).append(',')
    append("\"port\":").append(port).append(',')
    append("\"callerKeyRequired\":true,")
    if (!accountKey.isNullOrBlank()) {
        append("\"accountKey\":").append(jsonQuote(accountKey)).append(',')
    }
    append("\"platform\":").append(jsonQuote("android"))
    append('}')
}
