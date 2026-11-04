package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号级编辑端点（对齐桌面 channels.js）的请求体/回包契约：
 *
 * - 设置：`POST /api/accounts/{id}/settings`（`gateway.go:1074` → `accounts_api.go:71-134`），
 *   字段全是 `*T`，**缺省 = 不修改**；
 * - 标签：`POST /api/accounts/{id}/tags`（`accounts_api.go:19-44`），`{"tags":[…]}`
 *   —— 空数组就是清空；
 * - 备注：`POST /api/accounts/{id}/note`（`accounts_api.go:46-69`），`{"note":"…"}`
 *   —— 空字符串就是清空；
 * - 预设：`GET /api/presets` + `POST /api/presets/attach`（`request_presets.go:340-343`、`:575-652`）。
 */
class AccountEditApiTest {
    @Test
    fun pathsPointAtTheAccountScopedEndpoints() {
        assertEquals("/api/accounts/acc-1/tags", accountTagsPath("acc-1"))
        assertEquals("/api/accounts/acc-1/note", accountNotePath("acc-1"))
        assertEquals("/api/accounts/a%2Fb/tags", accountTagsPath("a/b"))
        assertEquals("/api/presets", presetsPath())
        assertEquals("/api/presets/attach", presetsAttachPath())
    }

    @Test
    fun settingsJsonCarriesOnlyChangedFields() {
        // 只改优先级 → 其它字段一个都不许出现（缺省 = 不修改）
        val priorityOnly = buildAccountSettingsJson(AccountSettingsPatch(priority = 2))
        assertTrue(priorityOnly, priorityOnly.contains("\"priority\":2"))
        for (absent in listOf("disabled", "weight", "concurrency", "allowed_models")) {
            assertTrue("$absent 不该出现：$priorityOnly", !priorityOnly.contains("\"$absent\""))
        }
        // 三个都改
        val multi = buildAccountSettingsJson(
            AccountSettingsPatch(disabled = true, weight = 7, concurrency = -1),
        )
        assertTrue(multi, multi.contains("\"disabled\":true"))
        assertTrue(multi, multi.contains("\"weight\":7"))
        assertTrue(multi, multi.contains("\"concurrency\":-1"))
        // 没有改动时补丁是空的（调用方据此拦住"空保存"）
        assertTrue(AccountSettingsPatch().isEmpty)
    }

    @Test
    fun clearingTheWhitelistSendsAnEmptyArray() {
        val body = buildAccountSettingsJson(AccountSettingsPatch(clearAllowedModels = true))
        assertEquals("""{"allowed_models":[]}""", body)
    }

    @Test
    fun tagsJsonAlwaysSendsTheWholeList() {
        assertEquals("""{"tags":["主力","临期"]}""", buildAccountTagsJson(listOf("主力", "临期")))
        // 空列表必须真的发出去：那才是"清空标签"
        assertEquals("""{"tags":[]}""", buildAccountTagsJson(emptyList()))
        // 空白项被清掉
        assertEquals("""{"tags":["a"]}""", buildAccountTagsJson(listOf(" a ", "  ")))
    }

    @Test
    fun noteJsonCanClearWithAnEmptyString() {
        assertEquals("""{"note":"月底重置"}""", buildAccountNoteJson("月底重置"))
        // 空串 = 清空，不是"不修改"
        assertEquals("""{"note":""}""", buildAccountNoteJson(""))
    }

    @Test
    fun tagInputSplitsOnCommasAndWhitespace() {
        assertEquals(listOf("主力", "临期", "备用"), parseTagInput("主力, 临期,备用"))
        assertEquals(listOf("主力", "临期"), parseTagInput("主力，临期"))
        // 去重 + 空白过滤
        assertEquals(listOf("主力"), parseTagInput(" 主力  , 主力 ,, "))
        // 最多 8 个（桌面 tagDialog 的 slice(0, 8)）
        val many = parseTagInput((1..12).joinToString(",") { "t$it" })
        assertEquals(MAX_ACCOUNT_TAGS, many.size)
        assertEquals("t1", many.first())
        assertEquals("t8", many.last())
        assertEquals(emptyList<String>(), parseTagInput("   "))
    }

    @Test
    fun presetAttachBodyIsProviderScoped() {
        val body = buildPresetAttachJson("claude-sub", listOf("a", " b ", "", "a"))
        assertTrue(body, body.contains("\"scope\":\"provider\""))
        assertTrue(body, body.contains("\"key\":\"claude-sub\""))
        assertTrue(body, body.contains("\"ids\":[\"a\",\"b\"]"))
        // 空 ids = 解绑该渠道全部预设（服务端 delete ProviderPresets[key]）
        assertTrue(buildPresetAttachJson("p", emptyList()).contains("\"ids\":[]"))
    }

    @Test
    fun parsesPresetsView() {
        val view = parsePresets(
            """
            {"presets":[{"id":"fast","name":"极速","description":"跳过推理","enabled":true,
                         "system_prompt":"你是助手","body":{"temperature":0},"headers":{"x-a":"1"}},
                        {"id":"off","name":"停用预设","enabled":false}],
             "provider_presets":{"claude-sub":["fast"],"trae-cn":[]},
             "route_presets":{}}
            """.trimIndent(),
        )
        assertEquals(2, view.presets.size)
        val fast = view.presets[0]
        assertEquals("fast", fast.id)
        assertEquals("极速", fast.name)
        assertTrue(fast.enabled)
        assertTrue(fast.hasSystemPrompt)
        assertEquals(1, fast.bodyCount)
        assertEquals(1, fast.headerCount)
        assertEquals("停用预设", view.presets[1].name)
        assertTrue(!view.presets[1].enabled)
        assertEquals(listOf("fast"), view.providerPresets["claude-sub"])
        assertEquals(emptyList<String>(), view.providerPresets["trae-cn"])

        for (body in listOf("not json", "{}", """{"presets":"oops"}""")) {
            val error = runCatching { parsePresets(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }

    @Test
    fun parsesTagsAndNoteAcks() {
        assertEquals(listOf("主力", "临期"), parseTagsAck("""{"account_id":"a","tags":["主力","临期"]}"""))
        assertEquals(emptyList<String>(), parseTagsAck("""{"account_id":"a","tags":[]}"""))
        assertEquals("月底重置", parseNoteAck("""{"account_id":"a","note":"月底重置"}"""))
        assertEquals("", parseNoteAck("""{"account_id":"a","note":""}"""))

        for (body in listOf("not json", "{}", """{"account_id":"a"}""")) {
            val error = runCatching { parseTagsAck(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
        for (body in listOf("not json", "{}", """{"account_id":"a"}""")) {
            val error = runCatching { parseNoteAck(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    /** 列表快照要带出详情字段（否则"只发改动字段"没有比较基准）。 */
    @Test
    fun accountPayloadCarriesTheDetailFields() {
        val rows = parseAccounts(
            """
            {"accounts":[{"account_id":"acc-1","provider_id":"claude-sub","label":"主力",
                          "priority":2,"weight":7,"concurrency":3,
                          "allowed_models":["claude-x"],"tags":["主力","临期"],"note":"月底重置"},
                         {"account_id":"acc-2"}]}
            """.trimIndent(),
        )
        val full = rows[0]
        assertEquals(2, full.priority)
        assertEquals(7, full.weight)
        assertEquals(3, full.concurrency)
        assertEquals(listOf("claude-x"), full.allowedModels)
        assertEquals(listOf("主力", "临期"), full.tags)
        assertEquals("月底重置", full.note)
        // concurrency 缺省 = 跟随全局（null），不能和 0 混
        assertNull(rows[1].concurrency)
        assertEquals(0, rows[1].priority)
        assertEquals("", rows[1].note)
    }
}
