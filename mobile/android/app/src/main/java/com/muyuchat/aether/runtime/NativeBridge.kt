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
    /** 主监听是否绑在 0.0.0.0（局域网共享开启）。 */
    val lanEnabled: Boolean = false,
    /** 枚举到的局域网地址（`http://ip:port`）；没有可用地址时为空。 */
    val lanUrls: List<String> = emptyList(),
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
                lanEnabled = obj.optBoolean("lanEnabled", false),
                lanUrls = parseLanUrls(obj),
            )
        }

        /** `lanUrls` 是字符串数组；缺失/类型不对一律当空列表，不猜地址。 */
        internal fun parseLanUrls(obj: JSONObject): List<String> {
            val array = obj.optJSONArray("lanUrls") ?: return emptyList()
            return (0 until array.length()).mapNotNull { index ->
                array.optString(index).trim().ifBlank { null }
            }
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
    lanAddressHint: String = "",
): String = buildString {
    append('{')
    append("\"dataDir\":").append(jsonQuote(dataDir)).append(',')
    append("\"cacheDir\":").append(jsonQuote(cacheDir)).append(',')
    append("\"tempDir\":").append(jsonQuote(tempDir)).append(',')
    append("\"listenHost\":").append(jsonQuote(listenHost)).append(',')
    append("\"port\":").append(port).append(',')
    append("\"callerKeyRequired\":true,")
    if (lanAddressHint.isNotBlank()) {
        // 宿主探测到的局域网 IPv4（逗号分隔）：Android 上 Go 枚举不到网卡，
        // 靠这个字段才能展示 http://192.168.x.x:<port>。
        append("\"lanAddressHint\":").append(jsonQuote(lanAddressHint.trim())).append(',')
    }
    if (!accountKey.isNullOrBlank()) {
        append("\"accountKey\":").append(jsonQuote(accountKey)).append(',')
    }
    append("\"platform\":").append(jsonQuote("android"))
    append('}')
}
