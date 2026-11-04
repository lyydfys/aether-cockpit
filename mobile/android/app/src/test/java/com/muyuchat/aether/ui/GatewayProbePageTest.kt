package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AnonymousProbeReport
import com.muyuchat.aether.runtime.ChannelProbeReport
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.HealthzProbe
import com.muyuchat.aether.runtime.MetricsDigest
import com.muyuchat.aether.runtime.ProbeOutcome
import com.muyuchat.aether.runtime.ReadyzProbe
import com.muyuchat.aether.runtime.RuntimeInfo
import com.muyuchat.aether.runtime.SessionProbeReport
import com.muyuchat.aether.runtime.VersionInfo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 网关与探针页的纯函数合同（对齐桌面 `js/views/gateway.js`）。
 *
 * 这些断言防的是"凭印象改文案/改单位"：接入片段里的地址拼法、端点徽标、
 * 时间戳量级判断都必须可复算。
 */
class GatewayProbePageTest {

    private val status = GatewayStatus(
        state = com.muyuchat.aether.runtime.GatewayState.RUNNING,
        addr = "127.0.0.1:8787",
        adminSession = "s",
    )

    @Test
    fun snippetUsesProtocolPathAndBaseUrl() {
        // 桌面 gateway.js:63-74：base = 入口 + 协议 path。
        assertTrue(probeSnippet("http://127.0.0.1:8787", "openai", "provider/model").contains("OPENAI_BASE_URL=\"http://127.0.0.1:8787/v1\""))
        assertTrue(probeSnippet("http://127.0.0.1:8787", "anthropic", "provider/model").contains("OPENAI_BASE_URL=\"http://127.0.0.1:8787\""))
        assertTrue(probeSnippet("http://127.0.0.1:8787", "gemini", "provider/model").contains("/v1beta/chat/completions"))
        assertTrue(probeSnippet("http://h:1", "openai", "gpt-5").contains("\"model\":\"gpt-5\""))
    }

    @Test
    fun snippetFallsBackToPlaceholderModel() {
        assertEquals("provider/model", probeSampleModel())
    }

    @Test
    fun baseUrlPrefersRuntimeThenSettingsThenStatus() {
        assertEquals("http://1.2.3.4:9000", probeBaseUrl(status, ready(runtimeAddr = "http://1.2.3.4:9000")))
        // runtime 没给 publicAddr 时退到 settings.addr，并补上 scheme（settings.addr 是 host:port）。
        assertEquals(
            "http://10.0.0.2:8080",
            probeBaseUrl(status, ready(runtimeAddr = "", settingsAddr = "10.0.0.2:8080")),
        )
        assertEquals("http://127.0.0.1:8787", probeBaseUrl(status, ready(runtimeAddr = "", settingsAddr = "")))
    }

    @Test
    fun readyzBadgeReflectsReadyFlagNotJustStatus() {
        // 桌面区分 200 但 ready=false 的情况（gateway.js:342-347 的 amber）。
        assertTrue(probeEndpointBad("/readyz", ready(readyzReady = false)))
        assertFalse(probeEndpointBad("/readyz", ready(readyzReady = true)))
        assertTrue(probeEndpointStatusText("/readyz", ready(readyzReady = false)).contains("未就绪"))
        assertTrue(probeEndpointStatusText("/readyz", ready(readyzReady = true)).contains("就绪"))
    }

    @Test
    fun failedEndpointShowsCodeInsteadOfHttpStatus() {
        val failed = ready(metricsFailed = true)
        assertTrue(probeEndpointBad("/metrics", failed))
        assertTrue(probeEndpointStatusText("/metrics", failed).contains("失败"))
        assertTrue(probeEndpointFields("/metrics", failed).isEmpty())
    }

    @Test
    fun endpointFieldsAreReusedFromParsedProbes() {
        assertTrue(probeEndpointFields("/healthz", ready()).any { it.label == "应答" })
        assertTrue(probeEndpointFields("/version", ready()).any { it.label == "版本" })
        assertTrue(probeEndpointFields("/readyz", ready()).any { it.label == "就绪" })
    }

    @Test
    fun timestampsAcceptBothSecondsAndMilliseconds() {
        assertTrue(probeDateAuto(1_800_000_000L).startsWith("20"))
        assertTrue(probeDateAuto(1_800_000_000_000L).startsWith("20"))
        assertTrue(probeDateAuto(1_800_000_000L).substring(0, 4) == probeDateAuto(1_800_000_000_000L).substring(0, 4))
        assertEquals("—", probeDateAuto(0))
        assertEquals("—", probeDate(0))
    }

    /* ---------------- fixtures ---------------- */

    private fun ready(
        runtimeAddr: String = "http://127.0.0.1:8787",
        settingsAddr: String = "127.0.0.1:8787",
        readyzReady: Boolean = true,
        metricsFailed: Boolean = false,
    ): ProbeUiState.Ready = ProbeUiState.Ready(
        anonymous = AnonymousProbeReport(
            healthz = ProbeOutcome.Ok(HealthzProbe(status = "ok", plainText = true)),
            health = ProbeOutcome.Ok(
                com.muyuchat.aether.runtime.HealthProbe(
                    status = "ok",
                    uptimeSec = 60,
                    providers = 1,
                    accounts = 1,
                    version = "1.0",
                    revision = "abc",
                ),
            ),
        ),
        session = SessionProbeReport(
            readyz = ProbeOutcome.Ok(
                ReadyzProbe(
                    httpStatus = 200,
                    ready = readyzReady,
                    models = 2,
                    providers = 1,
                    providersInUse = 1,
                    accounts = 1,
                    usableAccounts = 1,
                    uptimeSec = 60,
                    reasons = emptyList(),
                ),
            ),
            version = ProbeOutcome.Ok(
                VersionInfo(
                    version = "9.9",
                    revision = "r",
                    go = "go1.24",
                    buildTime = "2026-11-01T00:00:00Z",
                    buildTimeSource = "ldflags",
                    uptimeSec = 60,
                ),
            ),
            metrics = if (metricsFailed) {
                ProbeOutcome.Failed("http_500", "崩了")
            } else {
                ProbeOutcome.Ok(MetricsDigest(lineCount = 3, charCount = 90, preview = listOf("a")))
            },
            runtime = ProbeOutcome.Ok(
                RuntimeInfo(
                    goVersion = "go1.24",
                    goroutines = 10,
                    heapMb = 8,
                    stateRecords = 1,
                    models = 2,
                    tun = false,
                    publicAddr = runtimeAddr,
                    uptimeSec = 60,
                ),
            ),
        ),
        settings = com.muyuchat.aether.runtime.SettingsSummary(
            addr = settingsAddr,
            port = 8787,
            lanEnabled = false,
            lanUrls = emptyList(),
            proxyMasked = "",
            corsOrigins = emptyList(),
            allowIPs = emptyList(),
            redactEnabled = false,
            redactPersonal = false,
            redactActions = emptyList(),
            redactRuleCount = 0,
            redactAllowCount = 0,
            desktopApp = false,
        ),
        clients = emptyList(),
        clientsAvailable = true,
        callerKeys = emptyList(),
        channels = ChannelProbeReport(entries = emptyList(), reading = false, checkedAt = 0L),
        agents = emptyList(),
        agentsCheckedAt = 0L,
        sampledAtMs = 1_800_000_000_000L,
    )
}
