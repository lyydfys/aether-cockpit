package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道清单离线缓存的编解码。
 *
 * 两个方向的承诺：
 * - 写入只落"展示必需字段"（id/name/description/brandColor/ready/off/hasOAuth/chat/
 *   supportedAuths/capabilities/family/accountCount/readyCount），**不落** `accounts` 与
 *   `authFormSchema`；
 * - 读取必须容忍字段增减与畸形数据：坏 JSON → 空列表（页面照常显示空态，不崩），
 *   缺字段 → 默认值，缺 id → 丢该条。
 */
class OnboardProviderCacheTest {

    private fun provider(
        id: String,
        name: String = id,
        description: String = "desc",
        ready: Boolean = true,
        off: Boolean = false,
        chat: Boolean = true,
        hasOAuth: Boolean = true,
        brandColor: String = "#123456",
        family: String = "anthropic",
        accountCount: Int = 2,
        readyCount: Int = 1,
        supportedAuths: List<String> = listOf("oauth_pkce"),
        capabilities: Map<String, Boolean> = mapOf("quota" to true, "chat" to true),
    ) = ProviderSummary(
        id = id,
        name = name,
        description = description,
        accountCount = accountCount,
        readyCount = readyCount,
        ready = ready,
        off = off,
        brandColor = brandColor,
        family = family,
        supportedAuths = supportedAuths,
        capabilities = capabilities,
        hasOAuth = hasOAuth,
        chat = chat,
    )

    @Test
    fun roundTripKeepsEveryDisplayField() {
        val source = listOf(
            provider("anthropic", name = "Anthropic", description = "Claude 渠道"),
            provider("cursor", chat = false, hasOAuth = false, off = true, ready = false),
        )
        val decoded = parseCachedProviders(encodeCachedProviders(source))
        assertEquals(source.size, decoded.size)
        assertEquals(source[0].id, decoded[0].id)
        assertEquals("Anthropic", decoded[0].name)
        assertEquals("Claude 渠道", decoded[0].description)
        assertEquals("#123456", decoded[0].brandColor)
        assertEquals("anthropic", decoded[0].family)
        assertEquals(2, decoded[0].accountCount)
        assertEquals(1, decoded[0].readyCount)
        assertTrue(decoded[0].ready)
        assertTrue(decoded[0].hasOAuth)
        assertTrue(decoded[0].chat)
        assertEquals(listOf("oauth_pkce"), decoded[0].supportedAuths)
        assertEquals(mapOf("quota" to true, "chat" to true), decoded[0].capabilities)
        // 布尔位要能往返：cursor 是"仅账号/额度"那类，chat=false 必须保住，
        // 否则离线时会被误标成可对话转发。
        assertFalse(decoded[1].chat)
        assertFalse(decoded[1].hasOAuth)
        assertTrue(decoded[1].off)
        assertFalse(decoded[1].ready)
    }

    @Test
    fun encodedJsonNeverCarriesAccountsOrAuthSchema() {
        // 账号清单与登录表单不进缓存：前者可能带敏感标注，后者离线也用不上。
        val json = encodeCachedProviders(listOf(provider("p")))
        val item = JSONArray(json).getJSONObject(0)
        assertFalse(item.has("accounts"))
        assertFalse(item.has("authFormSchema"))
        assertFalse(item.has("modelIds"))
        // 该有的展示字段一个不能少。
        for (key in listOf("id", "name", "description", "brandColor", "ready", "off", "hasOAuth", "chat", "supportedAuths", "capabilities")) {
            assertTrue("缺少字段 $key", item.has(key))
        }
    }

    @Test
    fun blankIdsAreDroppedOnEncode() {
        val json = encodeCachedProviders(listOf(provider("ok"), provider("   ")))
        assertEquals(1, JSONArray(json).length())
    }

    @Test
    fun missingFieldsFallBackToDefaults() {
        // 老缓存（或以后删掉字段）只给 id：其余全部回落默认值，不抛异常。
        val decoded = parseCachedProviders("""[{"id":"only-id"}]""")
        assertEquals(1, decoded.size)
        assertEquals("only-id", decoded[0].id)
        // name 缺失回退成 id（列表里不能出现无标题卡片）。
        assertEquals("only-id", decoded[0].name)
        assertEquals("", decoded[0].description)
        assertFalse(decoded[0].ready)
        assertFalse(decoded[0].off)
        assertFalse(decoded[0].hasOAuth)
        // chat 默认 true：老数据没有这个字段时不能把所有渠道误标成"仅账号/额度"。
        assertTrue(decoded[0].chat)
        assertTrue(decoded[0].supportedAuths.isEmpty())
        assertTrue(decoded[0].capabilities.isEmpty())
        assertEquals(0, decoded[0].accountCount)
        assertEquals(0, decoded[0].readyCount)
    }

    @Test
    fun entriesWithoutIdAreSkipped() {
        val decoded = parseCachedProviders("""[{"id":"a"},{"name":"no-id"},{"id":"  "},{"id":"b"}]""")
        assertEquals(listOf("a", "b"), decoded.map { it.id })
    }

    @Test
    fun brokenJsonReturnsEmptyInsteadOfCrashing() {
        for (body in listOf("", "   ", "not json", "{", "{}", "[", "null", """{"providers":[]}""")) {
            assertTrue("body=$body", parseCachedProviders(body).isEmpty())
        }
        assertTrue(parseCachedProviders(null).isEmpty())
    }

    @Test
    fun wrongTypesInsideItemsAreTolerated() {
        // 手工改坏/跨版本的典型形态：数字写成字符串、数组写成对象。
        val decoded = parseCachedProviders(
            """[{"id":"p","ready":"yes","accountCount":"3","supportedAuths":"oauth","capabilities":[1,2]}]""",
        )
        assertEquals(1, decoded.size)
        assertEquals("p", decoded.single().id)
        // 类型不对时落到默认值（optBoolean/optInt/optJSONArray 都是这个语义），不抛。
        assertFalse(decoded.single().ready)
        assertEquals(0, decoded.single().accountCount)
        assertTrue(decoded.single().supportedAuths.isEmpty())
        assertTrue(decoded.single().capabilities.isEmpty())
    }

    @Test
    fun emptyListEncodesToEmptyArray() {
        assertEquals(0, JSONArray(encodeCachedProviders(emptyList())).length())
        // 空数组读回来也是空（这是"确实没有渠道"，不是坏数据）。
        assertTrue(parseCachedProviders("[]").isEmpty())
    }

    @Test
    fun savedAtLabelIsHonestWhenTimestampMissing() {
        assertEquals("上次拉取", onboardCacheSavedAtLabel(0L))
        // 负数/未来值都不该崩，也不该显示 1970 年这种荒唐时间。
        assertEquals("上次拉取", onboardCacheSavedAtLabel(-1L))
        val label = onboardCacheSavedAtLabel(1_760_000_000_000L)
        assertTrue(label.startsWith("上次拉取于 "))
        assertTrue(label.length > "上次拉取于 ".length)
    }

    @Test
    fun offlineBannerSaysWhatItIsAndWhyConnectIsBlocked() {
        val banner = onboardOfflineBanner(1_760_000_000_000L, providerCount = 12)
        // 说清三件事：这不是实时数据、网关没运行、接入要先启动网关。
        assertTrue(banner.contains("12 个"))
        assertTrue(banner.contains("网关未运行"))
        assertTrue(banner.contains("接入操作需要网关在跑"))
        // 不能出现"暂无数据"这种让人以为清单丢了的说法。
        assertFalse(banner.contains("暂无数据"))
        // 没有时间戳时也不出现空括号。
        val noTime = onboardOfflineBanner(0L, providerCount = 3)
        assertTrue(noTime.contains("上次拉取"))
        assertFalse(noTime.contains("上次拉取于  "))
    }

    @Test
    fun cachePaperTrailRoundTripsThroughJsonObject() {
        // 缓存整体是"数组 + 时间戳"两个键，这里确认编码产物能被独立解析回来
        // （SharedPreferences 里存的就是这两个键）。
        val encoded = encodeCachedProviders(listOf(provider("a"), provider("b")))
        val array = JSONArray(encoded)
        assertEquals(2, array.length())
        array.getJSONObject(0).let { item: JSONObject ->
            assertEquals("a", item.optString("id"))
        }
    }
}
