package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 本机自检摘要：**纯函数**，输入是页面已有 DTO。
 *
 * 最重要的不是"能生成"，而是**生成出来的东西能安全发给别人**：
 * 不能有密钥、令牌、账号标识、文件路径、代理原文。
 */
class MobileHealthReportTest {
    private val settings = SettingsSummary(
        addr = "127.0.0.1:40213",
        port = 40213,
        lanEnabled = false,
        lanUrls = emptyList(),
        proxyMasked = "socks5://user:secret@10.0.0.2:1080",
        corsOrigins = listOf("http://localhost:3000"),
        allowIPs = listOf("192.168.1.9"),
        redactEnabled = true,
        redactPersonal = true,
        redactActions = listOf("mask", "drop"),
        redactRuleCount = 12,
        redactAllowCount = 3,
        desktopApp = false,
        customRules = emptyList(),
    )

    private val security = SettingsSecurity(lan = false, lanAddr = "", corsOrigins = emptyList(), allowIps = emptyList())

    private val snapshot = HealthSnapshot(
        generatedAtMs = 1_767_225_600_000L,
        appVersion = "0.1.0",
        running = true,
        listenAddr = "127.0.0.1:40213",
        settings = settings,
        security = security,
    )

    @Test
    fun includesCoreFacts() {
        val report = buildHealthReport(snapshot)
        assertTrue(report.contains("0.1.0"))
        assertTrue(report.contains("运行中"))
        assertTrue(report.contains("127.0.0.1:40213"))
        assertTrue(report.contains("首选端口：40213"))
        assertTrue(report.contains("规则数：12"))
    }

    @Test
    fun proxyValueIsNeverQuoted() {
        val report = buildHealthReport(snapshot)
        // 代理只写"已设置"，绝不带用户名/口令/主机
        assertTrue(report.contains("全局代理：已设置"))
        listOf("socks5", "user:secret", "10.0.0.2", "1080").forEach {
            assertTrue("代理原文「$it」不该出现在摘要里", !report.contains(it))
        }
    }

    @Test
    fun noCredentialsOrIdentifiersLeak() {
        val report = buildHealthReport(snapshot)
        // 常见泄漏形态一律禁止
        listOf("@", "api_key", "Bearer", "sk-", "token", "C:/", "C:\\").forEach {
            assertTrue("「$it」不该出现在摘要里：$report", !report.contains(it))
        }
        // CORS/IP 白名单只报数量，不报值
        assertTrue(report.contains("CORS 允许来源：1 个"))
        assertTrue(report.contains("IP 白名单：1 个"))
        assertTrue(!report.contains("localhost:3000"))
        assertTrue(!report.contains("192.168.1.9"))
    }

    @Test
    fun stoppedGatewayIsHonest() {
        val report = buildHealthReport(snapshot.copy(running = false, listenAddr = ""))
        assertTrue(report.contains("状态：未启动"))
        assertTrue(report.contains("监听地址：未启动"))
    }

    @Test
    fun missingSettingsSayNotRead() {
        val report = buildHealthReport(snapshot.copy(settings = null))
        assertTrue(report.contains("配置：未读取"))
        // 没有数据就别编默认值
        assertTrue(!report.contains("首选端口"))
    }

    @Test
    fun fileNamesAreUniqueAndDated() {
        val a = healthReportFileName(1_767_225_600_000L)
        val b = healthReportFileName(1_767_225_601_000L)
        assertTrue(a.startsWith("health-") && a.endsWith(".txt"))
        assertTrue("文件名要能区分不同时刻：$a / $b", a != b)
    }
}
