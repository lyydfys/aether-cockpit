package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ExtraModelAction
import com.muyuchat.aether.runtime.buildExtraModelJson
import com.muyuchat.aether.runtime.providerExtraModelsPath
import com.muyuchat.aether.runtime.validateExtraModelName
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * 渠道额外模型（`POST /api/providers/{id}/models/extra`，`gateway.go:1139`）。
 *
 * 契约（`provider_extra_models.go:111`）：路径 `{id}` = 渠道 id，请求体
 * `{model, action:"add"|"remove"}`（缺省按 add），未知 action → 400。
 * 桌面入口 `channels.js:1018-1034`：「添加缺失模型」+ 每模型「删除」（action=remove）。
 */
class ChannelsExtraModelTest {
    @Test
    fun pathCarriesEncodedProviderId() {
        assertEquals("/api/providers/claude-sub/models/extra", providerExtraModelsPath("claude-sub"))
        // 渠道 id 里出现分隔符时必须编码，否则路径会被拆坏
        assertEquals(
            "/api/providers/a%2Fb/models/extra",
            providerExtraModelsPath("a/b"),
        )
    }

    @Test
    fun requestBodyMatchesHandlerFields() {
        val add = JSONObject(buildExtraModelJson("  deepseek-v4.1-flash  ", ExtraModelAction.Add))
        assertEquals("deepseek-v4.1-flash", add.getString("model"))
        assertEquals("add", add.getString("action"))
        assertEquals("只发 handler 认的两个字段", 2, add.length())

        val remove = JSONObject(buildExtraModelJson("gpt-4o-mini", ExtraModelAction.Remove))
        assertEquals("remove", remove.getString("action"))
    }

    @Test
    fun modelNameValidation() {
        assertNull(validateExtraModelName("deepseek-v4.1-flash"))
        assertNull("首尾空格会被 trim 掉", validateExtraModelName("  gpt-4o  "))
        assertEquals("请输入模型名", validateExtraModelName("   "))
        assertEquals("模型名里不能有空格", validateExtraModelName("gpt 4o"))
    }
}
