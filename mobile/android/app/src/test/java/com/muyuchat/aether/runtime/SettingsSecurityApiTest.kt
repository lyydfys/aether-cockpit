package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 设置页的「局域网与访问控制」只读呈现。
 *
 * 为什么只读：移动运行时在 `Start` 里只绑 127.0.0.1，非回环直接返回
 * `listen_host_must_be_loopback`；所以手机端把 `lan` 打开**不会**真的产生局域网监听，
 * 那是个假开关。桌面端才有意义。
 */
class SettingsSecurityApiTest {
    @Test
    fun parsesLanCorsAndAllowIps() {
        val view = parseSettingsSecurity(
            """{"lan":true,"lan_addr":"0.0.0.0:8081",
                "cors_origins":["https://a.example"],"allow_ips":["192.168.1.0/24"]}""",
        )
        assertTrue(view.lan)
        assertEquals("0.0.0.0:8081", view.lanAddr)
        assertEquals(listOf("https://a.example"), view.corsOrigins)
        assertEquals(listOf("192.168.1.0/24"), view.allowIps)
    }

    @Test
    fun missingFieldsMeanNotConfigured() {
        val view = parseSettingsSecurity("""{}""")
        assertFalse(view.lan)
        assertEquals("", view.lanAddr)
        assertTrue(view.corsOrigins.isEmpty())
        assertTrue(view.allowIps.isEmpty())
        // 空列表要写"未设置"，不能显示 "[]"
        val lines = settingsSecurityLines(view)
        assertTrue(lines.any { it.contains("未设置") })
        assertTrue(lines.none { it.contains("[]") })
    }

    @Test
    fun lanLineSaysItDoesNotApplyOnPhone() {
        val view = parseSettingsSecurity("""{"lan":false}""")
        val lines = settingsSecurityLines(view)
        val lanLine = lines.first { it.startsWith("局域网共享") }
        assertTrue("要写明仅回环不生效：$lanLine", lanLine.contains("回环"))
        assertFalse(lanLine.contains("已开启"))
    }

    @Test
    fun enabledLanIsShownAsDesktopOnly() {
        val view = parseSettingsSecurity("""{"lan":true,"lan_addr":"0.0.0.0:9000"}""")
        val lanLine = settingsSecurityLines(view).first { it.startsWith("局域网共享") }
        // 即便配置文件里是"开"，手机上也要标成"仅桌面端生效"
        assertTrue("必须标仅桌面端：$lanLine", lanLine.contains("仅桌面端"))
    }

    @Test
    fun listsAreEnumerated() {
        val view = parseSettingsSecurity(
            """{"cors_origins":["https://a","https://b"],"allow_ips":["10.0.0.1"]}""",
        )
        val lines = settingsSecurityLines(view)
        assertTrue(lines.any { it.contains("https://a") && it.contains("https://b") })
        assertTrue(lines.any { it.contains("10.0.0.1") })
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (body in listOf("not-json", "")) {
            val error = runCatching { parseSettingsSecurity(body) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
