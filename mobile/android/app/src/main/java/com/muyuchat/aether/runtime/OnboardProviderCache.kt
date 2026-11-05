package com.muyuchat.aether.runtime

import android.content.Context
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

/**
 * 渠道清单的离线缓存（「网关没启动时也要能看到已接入的渠道」）。
 *
 * 为什么需要：`OnboardPage` 的渠道列表来自 `GET /api/providers`（经 `ChannelsViewModel`）；
 * 网关没运行 → 请求失败 → 列表空 → 页面显示「0 个渠道」，用户上次看到的东西凭空消失。
 * 这里把**上次成功拉到**的精简清单落盘，启动时先渲染它，联网成功后再覆盖。
 *
 * 持久化方式：复用仓库既有先例 `runtime/AccountKeyStore.kt:24` 的
 * `context.getSharedPreferences(..., MODE_PRIVATE)` + `org.json`，**不引入新依赖**。
 *
 * 只缓存展示必需字段：id / name / description / brandColor / ready / off /
 * hasOAuth / chat / supportedAuths / capabilities / family / accountCount / readyCount。
 * 刻意**不缓存** `accounts`（账号清单可能含敏感标注）与 `authFormSchema`（离线也用不上，
 * 接入操作本来就要求网关在运行）。
 */
data class CachedProviders(
    val providers: List<ProviderSummary> = emptyList(),
    val savedAtMs: Long = 0L,
) {
    val isEmpty: Boolean get() = providers.isEmpty()
}

/** SharedPreferences 名与两个键（键带 `_v1`：结构变了就换键，别让老数据反序列化成半截对象）。 */
internal const val ONBOARD_CACHE_PREFS = "aether_onboard_cache"
internal const val ONBOARD_CACHE_KEY = "providers_v1"
internal const val ONBOARD_CACHE_SAVED_AT_KEY = "providers_saved_at_v1"

/**
 * 精简清单 → JSON 数组字符串。
 *
 * 用 `put` 逐个写而不是直接序列化对象：`ProviderSummary` 里含 `accounts` 与
 * `authFormSchema` 这类不该落盘/用不上的字段，显式挑选才不会有"以后加了个敏感字段
 * 就被默默写进磁盘"的风险。
 */
internal fun encodeCachedProviders(providers: List<ProviderSummary>): String {
    val array = JSONArray()
    providers.forEach { provider ->
        val id = provider.id.trim()
        if (id.isEmpty()) {
            return@forEach
        }
        val item = JSONObject()
        item.put("id", id)
        item.put("name", provider.name)
        item.put("description", provider.description)
        item.put("brandColor", provider.brandColor)
        item.put("family", provider.family)
        item.put("ready", provider.ready)
        item.put("off", provider.off)
        item.put("hasOAuth", provider.hasOAuth)
        item.put("chat", provider.chat)
        item.put("accountCount", provider.accountCount)
        item.put("readyCount", provider.readyCount)
        val auths = JSONArray()
        provider.supportedAuths.forEach { auths.put(it) }
        item.put("supportedAuths", auths)
        val caps = JSONObject()
        provider.capabilities.forEach { (key, value) -> caps.put(key, value) }
        item.put("capabilities", caps)
        array.put(item)
    }
    return array.toString()
}

/**
 * JSON → 精简清单。**任何畸形都只会少一条，不会崩**：
 * 坏 JSON / 非数组 → 空列表；缺字段 → 默认值（布尔 false、字符串空）；缺 id → 丢该条。
 *
 * 刻意不用 `getString` 硬取：缓存是跨版本读取的，字段增减必须能容忍
 * （以后加了字段老缓存照样能读，删了字段也只是回落默认值）。
 */
internal fun parseCachedProviders(json: String?): List<ProviderSummary> {
    val text = json?.trim().orEmpty()
    if (text.isEmpty()) {
        return emptyList()
    }
    val array = try {
        JSONArray(text)
    } catch (_: JSONException) {
        return emptyList()
    }
    val out = ArrayList<ProviderSummary>(array.length())
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            continue
        }
        out.add(
            ProviderSummary(
                id = id,
                name = item.optString("name").trim().ifBlank { id },
                description = item.optString("description").trim(),
                accountCount = cachedCountOf(item, "accountCount"),
                readyCount = cachedCountOf(item, "readyCount"),
                ready = item.optBoolean("ready", false),
                off = item.optBoolean("off", false),
                brandColor = item.optString("brandColor").trim(),
                family = item.optString("family").trim(),
                supportedAuths = stringListOf(item.optJSONArray("supportedAuths")),
                capabilities = booleanMapOf(item.optJSONObject("capabilities")),
                hasOAuth = item.optBoolean("hasOAuth", false),
                chat = item.optBoolean("chat", true),
            ),
        )
    }
    return out
}

/**
 * 缓存里的计数只认数字类型：手工改坏/跨版本写入的字符串（"3"）按默认值 0 处理，
 * 不静默把脏数据当真实计数（与 `optInt` 的字符串强转语义相反，测试锁的就是这个契约）。
 */
private fun cachedCountOf(item: JSONObject, key: String): Int {
    val value = item.opt(key) ?: return 0
    val number = when (value) {
        is Int -> value
        is Long -> value.toInt()
        is Double -> value.toInt()
        else -> 0
    }
    return number.coerceAtLeast(0)
}

private fun stringListOf(array: JSONArray?): List<String> {
    if (array == null) {        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().ifBlank { null }
    }
}

private fun booleanMapOf(json: JSONObject?): Map<String, Boolean> {
    if (json == null) {
        return emptyMap()
    }
    val out = LinkedHashMap<String, Boolean>()
    json.keys().forEach { key ->
        val name = key.trim()
        if (name.isNotEmpty()) {
            out[name] = json.optBoolean(name, false)
        }
    }
    return out
}

/** 渠道清单缓存的读写（SharedPreferences；同步 IO，调用方自己切 IO 线程）。 */
class OnboardProviderCache(context: Context) {
    private val prefs = context.applicationContext
        .getSharedPreferences(ONBOARD_CACHE_PREFS, Context.MODE_PRIVATE)

    /** 读缓存；没有/坏数据 → 空（页面照常显示"0 个渠道"，不会崩）。 */
    fun load(): CachedProviders {
        val raw = prefs.getString(ONBOARD_CACHE_KEY, null)
        val providers = parseCachedProviders(raw)
        return CachedProviders(
            providers = providers,
            savedAtMs = if (providers.isEmpty()) 0L else prefs.getLong(ONBOARD_CACHE_SAVED_AT_KEY, 0L),
        )
    }

    /**
     * 写缓存。空清单**不覆盖**已有缓存：网关刚启动、还没拉到数据时不要用空数组抹掉上次的清单。
     */
    fun save(providers: List<ProviderSummary>, nowMs: Long = System.currentTimeMillis()) {
        if (providers.isEmpty()) {
            return
        }
        prefs.edit()
            .putString(ONBOARD_CACHE_KEY, encodeCachedProviders(providers))
            .putLong(ONBOARD_CACHE_SAVED_AT_KEY, nowMs)
            .apply()
    }
}

/** 离线说明抬头：`上次拉取于 MM/dd HH:mm`；时间缺失时只说"上次拉取"。 */
internal fun onboardCacheSavedAtLabel(savedAtMs: Long): String {
    if (savedAtMs <= 0L) {
        return "上次拉取"
    }
    val formatter = java.time.format.DateTimeFormatter.ofPattern("MM/dd HH:mm", java.util.Locale.US)
    val text = runCatching {
        java.time.Instant.ofEpochMilli(savedAtMs)
            .atZone(java.time.ZoneId.systemDefault())
            .format(formatter)
    }.getOrNull().orEmpty()
    return if (text.isBlank()) "上次拉取" else "上次拉取于 $text"
}

/**
 * 离线横幅文案：说清"这是什么数据 + 为什么不能接入 + 下一步"。
 *
 * 不写"暂无数据"——那会让用户以为清单丢了；也不假装数据是实时的。
 */
internal fun onboardOfflineBanner(savedAtMs: Long, providerCount: Int): String {
    val prefix = onboardCacheSavedAtLabel(savedAtMs)
    return "当前显示的是$prefix 的渠道清单（$providerCount 个），网关未运行。" +
        "接入操作需要网关在跑：启动网关后会刷新成最新清单。"
}
