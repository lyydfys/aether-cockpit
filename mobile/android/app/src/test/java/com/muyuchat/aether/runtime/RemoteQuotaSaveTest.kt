package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** 远程额度源：Key 留空必须省略字段（服务端按 name+url 沿用旧密文）。 */
class RemoteQuotaSaveTest {
    @Test
    fun blankKeyIsOmittedAndNewKeyIsSent() {
        val body = JSONObject(
            buildRemoteQuotasJson(
                listOf(
                    RemoteQuotaDraft("书房", "http://192.168.1.5:7888/", "", true),
                    RemoteQuotaDraft("办公室", "https://x.example/", " new-key ", false),
                ),
            ),
        )
        val sources = body.getJSONArray("sources")
        assertEquals("http://192.168.1.5:7888", sources.getJSONObject(0).getString("url"))
        assertFalse("留空 Key 不能写空串/假值", sources.getJSONObject(0).has("key"))
        assertEquals("new-key", sources.getJSONObject(1).getString("key"))
        assertFalse(sources.getJSONObject(1).getBoolean("enabled"))
    }

    @Test
    fun settingsParsesMaskedSourcesWithHasKey() {
        val summary = parseSettings(
            """{"remote_quotas":[{"name":"书房","url":"http://a:1","enabled":true,"hasKey":true},
                {"name":"无 Key","url":"http://b:2","enabled":false,"hasKey":false}]}""",
        )
        assertEquals(2, summary.remoteQuotas.size)
        assertTrue(summary.remoteQuotas[0].hasKey)
        assertFalse(summary.remoteQuotas[1].enabled)
    }

    @Test
    fun missingRemoteQuotasMeansEmpty() {
        assertTrue(parseSettings("{}").remoteQuotas.isEmpty())
    }

    @Test
    fun testResultMapsOkAndFailureText() {
        assertEquals(
            "连接成功 · 17 个渠道 / 2 个账号",
            parseRemoteQuotaTestResult("""{"ok":true,"providers":17,"accounts":2}""").message,
        )
        val failed = parseRemoteQuotaTestResult("""{"ok":false,"error":"连不上远程实例"}""")
        assertFalse(failed.ok)
        assertEquals("连不上远程实例", failed.message)
    }

    @Test
    fun providerResultMapsCreatedAndDuplicate() {
        assertEquals("已注册为 openai-compat 上游", parseRemoteQuotaProviderResult("""{"ok":true,"created":true}""").message)
        assertEquals("该实例已注册过", parseRemoteQuotaProviderResult("""{"ok":true,"created":false}""").message)
        assertFalse(parseRemoteQuotaProviderResult("""{"ok":false,"error":"没有保存访问 Key"}""").ok)
    }
}
