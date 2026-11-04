package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.buildDraftModelsJson
import com.muyuchat.aether.runtime.parseProviderModels
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 自定义渠道「获取模型」（`POST /api/provider/models {base,key}`）。
 *
 * 契约来自 `internal/core/gateway/provider_detect.go:212-307`：
 * 服务端 GET `{base}/models`，回 `{count, models[], dropped[], source?}`；400/502 回 `{error}`。
 * 这里锁三件事：什么时候可点、成功后怎么合并、失败怎么把原文与建议一起给出来。
 */
class OnboardModelsFetchTest {
    @Test
    fun fetchNeedsBothBaseUrlAndApiKey() {
        assertTrue(canFetchDraftModels("https://api.x.com/v1", "sk-1"))
        assertFalse(canFetchDraftModels("", "sk-1"))
        assertFalse(canFetchDraftModels("https://api.x.com/v1", ""))
        assertFalse(canFetchDraftModels("   ", "  "))
    }

    @Test
    fun disabledHintSaysExactlyWhatIsMissing() {
        assertNull("都填了就不需要提示", draftModelsFetchHint("https://api.x.com/v1", "sk-1"))
        assertEquals("先填 Base URL 与 API Key", draftModelsFetchHint("", ""))
        assertEquals("先填 Base URL", draftModelsFetchHint("", "sk-1"))
        assertEquals("先填 API Key", draftModelsFetchHint("https://api.x.com/v1", ""))
    }

    @Test
    fun mergeKeepsHandWrittenModelsAndOnlyAppendsNewOnes() {
        // 手填的排在前面，探测到的只补缺（去重、保序）
        assertEquals(
            "my-model, gpt-4o-mini, claude-sonnet-4",
            mergeFetchedModels("my-model", listOf("gpt-4o-mini", "claude-sonnet-4")),
        )
        // 已有同名不重复
        assertEquals(
            "gpt-4o-mini, claude-sonnet-4",
            mergeFetchedModels("gpt-4o-mini", listOf("gpt-4o-mini", "claude-sonnet-4")),
        )
        // 手填为空 → 直接用探测结果
        assertEquals("a, b", mergeFetchedModels("", listOf("a", "b")))
        // 探测结果为空 → 手填原样保留
        assertEquals("my-model", mergeFetchedModels("my-model", emptyList()))
        // 手填里有空白/重复的清理
        assertEquals("a, b", mergeFetchedModels(" a ,  a , b ", emptyList()))
    }

    @Test
    fun failureLineKeepsServerTextAndAddsAnActionableHint() {
        // 404：怀疑 Base URL 少了 /v1
        val notFound = modelsFetchErrorLine("供应商返回 HTTP 404: not found")
        assertTrue("要保留服务端原文", notFound.contains("供应商返回 HTTP 404"))
        assertTrue("要给出可操作建议", notFound.contains("/v1"))

        // 401/403：怀疑 Key
        assertTrue(modelsFetchErrorLine("供应商返回 HTTP 401: unauthorized").contains("API Key"))
        assertTrue(modelsFetchErrorLine("HTTP 403 Forbidden").contains("API Key"))

        // 连接失败
        assertTrue(modelsFetchErrorLine("连接失败: dial tcp: timeout").contains("网络"))

        // 服务端只有"先填 Base URL"这条硬要求
        assertTrue(modelsFetchErrorLine("先填一个 Base URL，或先保存渠道").contains("Base URL"))

        // 空消息也要给出建议，不能是空串
        val empty = modelsFetchErrorLine("   ")
        assertTrue(empty.isNotBlank())
        assertTrue(empty.contains("Base URL") || empty.contains("网关"))
    }

    @Test
    fun emptyModelListIsExplainedWithoutCallingItAFailure() {
        val line = modelsFetchEmptyLine()
        assertTrue(line.contains("没有返回模型"))
        assertTrue("要说清仍可手填", line.contains("手动填写"))
    }

    @Test
    fun draftRequestCarriesOnlyTrimmedBaseAndKey() {
        val json = JSONObject(buildDraftModelsJson("  https://api.x.com/v1/  ", "  sk-1  "))
        assertEquals("https://api.x.com/v1", json.getString("base"))
        assertEquals("sk-1", json.getString("key"))
        // 草稿没保存：不带 id，也不带 chat（base 已足够）
        assertFalse(json.has("id"))
        assertFalse(json.has("chat"))
        assertFalse("绝不能带出别的凭据字段", json.has("apiKey"))
    }

    @Test
    fun responseParsingHandlesRealShapeAndBadPayloads() {
        // 真实回包
        val ok = parseProviderModels(
            """{"count":2,"models":["gpt-4o-mini","claude-sonnet-4"],"dropped":[],"source":"live"}""",
        )
        assertEquals(listOf("gpt-4o-mini", "claude-sonnet-4"), ok.models)
        assertEquals("live", ok.source)

        // 空列表（不是错误）
        assertTrue(parseProviderModels("""{"count":0,"models":[],"dropped":[]}""").models.isEmpty())

        // 缺失 models 字段 → 明确报错，不静默返回空
        val missing = runCatching { parseProviderModels("""{"count":0}""") }.exceptionOrNull()
        assertTrue(missing is ManagementApiException)
        assertTrue((missing as ManagementApiException).message!!.contains("models"))

        // 非 JSON → 明确报错
        val bad = runCatching { parseProviderModels("not json") }.exceptionOrNull()
        assertTrue(bad is ManagementApiException)
        assertEquals("invalid_response", (bad as ManagementApiException).code)

        // 数组里混入空串/空白：丢掉
        assertEquals(
            listOf("a", "b"),
            parseProviderModels("""{"models":["a","","  ","b"]}""").models,
        )
    }
}
