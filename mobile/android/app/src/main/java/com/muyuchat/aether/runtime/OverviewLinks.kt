package com.muyuchat.aether.runtime

/**
 * 概览页「聚合链路」的一行：一个上游渠道 + 它今天的真实用量。
 *
 * 字段来源（只取已核实的）：
 * - id / name / accountCount / readyCount / off ← `GET /api/providers`
 *   （`internal/core/gateway/admin.go:65-80`；手机解析见 `ManagementApiClient.parseProviders`）
 * - calls / errors / cost ← `GET /api/usage?period=today` 的 `providers[]`
 *   （`internal/core/gateway/usage.go:628-634` 与 `:662`）
 *
 * 没有任何字段是补零猜出来的：读不到用量时 [usageKnown] 为 false，UI 显示"读不到"。
 */
data class OverviewProviderLink(
    val id: String,
    val name: String,
    val accountCount: Int,
    val readyCount: Int,
    val off: Boolean,
    val calls: Long,
    val errors: Long,
    val cost: Double,
)

/** `/api/providers` + `/api/usage` 合并后的聚合链路状态；两边分别可失败。 */
data class OverviewLinksState(
    val providersLoaded: Boolean = false,
    val providers: List<OverviewProviderLink> = emptyList(),
    val totalAccounts: Int? = null,
    val usageLoaded: Boolean = false,
    val calls: Long = 0L,
)

/** 三步接入的一步。桌面文案原样保留；`status` 是这台手机上真实的完成判定。 */
data class OverviewOnboardingStep(
    val title: String,
    val description: String,
    val done: Boolean,
    val status: String,
)

/** 桌面端聚合链路的三个入站端点。 */
data class OverviewEndpoint(
    val label: String,
    val url: String,
)

/**
 * 合并渠道与今日用量。只有 `accountCount > 0` 的渠道会出现在链路里
 * （与桌面 `byProv` 按账号分组一致；空 vendor 行不画）。
 *
 * @param providers null = `/api/providers` 读不到；空列表 = 真的没有渠道
 * @param usage null = `/api/usage` 读不到；非 null 时缺某个渠道行按 0 次处理
 */
internal fun overviewLinks(providers: List<ProviderSummary>?, usage: UsageSummary?): OverviewLinksState {
    if (providers == null) {
        return OverviewLinksState(
            providersLoaded = false,
            providers = emptyList(),
            totalAccounts = null,
            usageLoaded = usage != null,
            calls = usage?.calls ?: 0L,
        )
    }
    val usageByProvider = usage?.providers.orEmpty().associateBy { it.id }
    val rows = providers.mapNotNull { provider ->
        if (provider.accountCount <= 0) {
            return@mapNotNull null
        }
        val used = usageByProvider[provider.id]
        OverviewProviderLink(
            id = provider.id,
            name = provider.name.ifBlank { provider.id },
            accountCount = provider.accountCount,
            readyCount = provider.readyCount,
            off = provider.off,
            calls = used?.calls ?: 0L,
            errors = used?.errors ?: 0L,
            cost = used?.cost ?: 0.0,
        )
    }
    return OverviewLinksState(
        providersLoaded = true,
        providers = rows,
        totalAccounts = providers.sumOf { it.accountCount },
        usageLoaded = usage != null,
        calls = usage?.calls ?: 0L,
    )
}

/**
 * 三步接入的完成判定（桌面 `overview.js:181-186` 的文案原样）：
 * 1. 设置网关密钥 —— 手机运行时只绑回环；网关运行即由本机会话接管，不需要另配密钥；
 * 2. 添加第一个渠道账号 —— `/api/providers` 的 account_count 之和 > 0；
 * 3. 复制接入片段 —— 用今日请求数证明客户端已经真的接入（复制动作本身不可观测，
 *    不伪造"已复制"；`usageKnown=false` 时显示读不到，不硬判完成）。
 */
internal fun overviewOnboardingSteps(
    running: Boolean,
    linksLoaded: Boolean,
    accountsKnown: Boolean,
    accountCount: Int?,
    usageKnown: Boolean,
    calls: Long,
): List<OverviewOnboardingStep> {
    val setKey = OverviewOnboardingStep(
        title = "设置网关密钥",
        description = "本机回环可免；开放局域网必填",
        done = running,
        status = if (running) {
            "已完成 · 本机回环免密钥"
        } else {
            "待完成 · 启动网关后使用本机会话"
        },
    )
    val addAccount = OverviewOnboardingStep(
        title = "添加第一个渠道账号",
        description = "订阅渠道按提示登录，或添加 OpenAI 兼容账号",
        done = accountsKnown && (accountCount ?: 0) > 0,
        status = when {
            accountsKnown && (accountCount ?: 0) > 0 -> "已完成 · 共 ${accountCount} 个账号"
            accountsKnown -> "待完成 · 还没有渠道账号"
            !running -> "待完成 · 启动网关后读取渠道账号"
            !linksLoaded -> "正在读取渠道账号…"
            else -> "待完成 · 读不到渠道账号，重新进入概览会再试"
        },
    )
    val copySnippet = OverviewOnboardingStep(
        title = "复制接入片段",
        description = "把网关地址与密钥填进客户端",
        done = usageKnown && calls > 0L,
        status = when {
            usageKnown && calls > 0L -> "已完成 · 客户端已接入（今日 ${formatUsageCount(calls)} 次请求）"
            !running -> "待完成 · 启动网关后复制地址并接入客户端"
            !linksLoaded -> "正在读取今日请求…"
            !usageKnown -> "待完成 · 读不到用量，暂时无法判定"
            else -> "待完成 · 复制地址后在客户端发一次请求"
        },
    )
    return listOf(setKey, addAccount, copySnippet)
}

/** 桌面 `overview.js:296-300` 的三个端点；baseUrl 为空（网关未启动）时 URL 为空串。 */
internal fun overviewEndpoints(baseUrl: String): List<OverviewEndpoint> {
    val base = baseUrl.trim().trimEnd('/')
    return listOf(
        OverviewEndpoint("OpenAI Base", if (base.isEmpty()) "" else "$base/v1"),
        OverviewEndpoint("Anthropic Base", base),
        OverviewEndpoint("Gemini Base", if (base.isEmpty()) "" else "$base/v1beta"),
    )
}
