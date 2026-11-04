package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 自定义渠道：字段必须逐字对齐服务端 `providerDraft`（provider_api.go:23-92）。
 *
 * 已核实的真实字段：`name` / `key`（API Key）/ `chat` 或 `anthropic`（Base URL）/
 * 可选 `models` 数组；**没有** `type`、`apiKey`、`baseURL`、`note`。
 * 因此「接口类型」只是 UI 选项，提交时落到协议字段。
 */
class ProviderSaveApiTest {
    @Test
    fun buildsOpenAiCompatibleBodyWithRealFieldNames() {
        val json = buildProviderSaveJson(
            CustomProviderDraft(
                name = " 我的中转 ",
                baseUrl = "https://api.example.com/v1///",
                apiKey = " sk-test ",
                protocol = CustomProviderProtocol.OPENAI_COMPAT,
                modelsText = "gpt-4o-mini, gpt-4o, gpt-4o-mini",
            ),
        )
        assertTrue(json.contains("\"name\":\"我的中转\""))
        assertTrue(json.contains("\"key\":\"sk-test\""))
        assertTrue(json.contains("\"chat\":\"https://api.example.com/v1\""))
        assertTrue(json.contains("\"models\":[\"gpt-4o-mini\",\"gpt-4o\"]"))
        // 不能自造服务端没有的键
        assertFalse(json.contains("\"type\""))
        assertFalse(json.contains("\"apiKey\""))
        assertFalse(json.contains("\"baseURL\""))
        assertFalse(json.contains("\"id\""))
    }

    @Test
    fun anthropicTypeLandsOnAnthropicFieldOnly() {
        val json = buildProviderSaveJson(
            CustomProviderDraft(
                name = "Claude 中转",
                baseUrl = "https://api.anthropic.example.com",
                apiKey = "sk-ant-xxx",
                protocol = CustomProviderProtocol.ANTHROPIC_COMPAT,
            ),
        )
        assertTrue(json.contains("\"anthropic\":\"https://api.anthropic.example.com\""))
        assertFalse(json.contains("\"chat\""))
        assertFalse(json.contains("\"models\""))
    }

    @Test
    fun onlyTwoTypesAreOfferedToTheForm() {
        assertEquals(
            listOf("OpenAI 兼容", "Anthropic 兼容"),
            CUSTOM_PROVIDER_FORM_PROTOCOLS.map { it.label },
        )
        assertEquals(listOf("chat", "anthropic"), CUSTOM_PROVIDER_FORM_PROTOCOLS.map { it.jsonField })
    }

    @Test
    fun baseUrlIsTrimmedAndModelsAreDeduped() {
        assertEquals("https://x/y", normalizeProviderBaseUrl("  https://x/y/  "))
        assertEquals(listOf("a", "b"), parseProviderModelList(" a, b ，a\n"))
        assertTrue(parseProviderModelList("  ").isEmpty())
    }

    @Test
    fun requiredFieldsMatchDesktopManualCredential() {
        val ok = CustomProviderDraft(name = "自定义渠道", baseUrl = "https://x", apiKey = "sk-1")
        assertNull(validateCustomProviderDraft(ok))

        assertNotNullMessage("请填写渠道名称", ok.copy(name = " "))
        assertNotNullMessage("请填写 Base URL", ok.copy(baseUrl = ""))
        assertNotNullMessage("Base URL 必须以 http:// 或 https:// 开头", ok.copy(baseUrl = "api.example.com"))
        assertNotNullMessage("请填写 API Key", ok.copy(apiKey = "  "))
    }

    @Test
    fun idIsOptionalButMustNotCollideWithBuiltInProviders() {
        val ok = CustomProviderDraft(name = "自定义渠道", baseUrl = "https://x", apiKey = "sk-1")
        assertNull(validateCustomProviderDraft(ok.copy(id = "")))
        assertNull(validateCustomProviderDraft(ok.copy(id = "my-relay")))
        assertNotNullMessage(
            "渠道 ID 只能用小写字母、数字、点、下划线或连字符，长度 1-64",
            ok.copy(id = "My Relay"),
        )
        assertTrue(
            validateCustomProviderDraft(ok.copy(id = "claude"), setOf("claude"))!!
                .contains("与内置渠道重名"),
        )
    }

    private fun assertNotNullMessage(expected: String, draft: CustomProviderDraft) {
        val problem = validateCustomProviderDraft(draft)
        assertEquals(expected, problem)
    }
}
