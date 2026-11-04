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
    /** 渠道声明的模型 id，用来推断它汇入哪些端点（桌面 `overview.js:364-374`）。 */
    val modelIds: List<String> = emptyList(),
)

/**
 * 桌面 `overview.js:364-374` 的 `familiesOf` **逐字复刻**：
 * 遍历 `provider.models[].id`（小写后）——含 `claude` 记 anthropic，否则含 `gemini` 记 gemini，
 * 其余记 openai；一个模型都没有时给 `openai`（桌面 `:372`）。
 */
internal fun providerEndpointFamilies(modelIds: List<String>): Set<String> {
    val families = LinkedHashSet<String>()
    for (raw in modelIds) {
        val id = raw.trim().lowercase()
        when {
            id.contains("claude") -> families += "anthropic"
            id.contains("gemini") -> families += "gemini"
            else -> families += "openai"
        }
    }
    if (families.isEmpty()) {
        families += "openai"
    }
    return families
}

/** 端点展示名（桌面端点标签 `EP_LABEL`）：OpenAI / Anthropic / Gemini。 */
internal fun providerEndpointLabel(modelIds: List<String>): String {
    val label = mapOf("openai" to "OpenAI", "anthropic" to "Anthropic", "gemini" to "Gemini")
    val order = listOf("openai", "anthropic", "gemini")
    return order.filter { it in providerEndpointFamilies(modelIds) }
        .map { label[it] ?: it }
        .joinToString(" / ")
}

/** 一个渠道里最紧的那条额度窗口。 */
data class LowestQuotaWindow(
    val remainingPct: Double,
    /** 窗口名（已经拼成 `渠道 · 池 · 窗口`，与额度卡一致）。 */
    val windowName: String,
    val resetNote: String,
)

/**
 * 桌面 `overview.js:376-386`：按渠道过滤额度窗口，取 `1 - used` 的**最小值**。
 *
 * 口径与桌面一致：
 * - 只认这条渠道自己的窗口（`OverviewQuotaRow.providerId == providerId`）；
 * - 相等时保留先出现的那个（桌面用 `left < rem` 严格小于，不替换）；
 * - **没有任何窗口时返回 `null`**（桌面此时写「该渠道没有可读的额度窗口。」，不画 0%）。
 */
internal fun lowestQuotaWindow(
    rows: List<OverviewQuotaRow>,
    providerId: String,
): LowestQuotaWindow? {
    if (providerId.isBlank()) {
        return null
    }
    var best: LowestQuotaWindow? = null
    for (row in rows) {
        if (row.providerId != providerId) {
            continue
        }
        if (best == null || row.remainingPct < best.remainingPct) {
            best = LowestQuotaWindow(
                remainingPct = row.remainingPct,
                windowName = row.name,
                resetNote = row.resetNote,
            )
        }
    }
    return best
}

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
    /**
     * 步骤标识（**不要**用下标映射跳转目标：将来插步骤会静默错位）。
     * 取值：`settings` / `onboard` / `probe`（桌面 `overview.js:183-185` 的三个 navigate 目标）。
     */
    val id: String,
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
        id = "settings",
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
        id = "onboard",
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
        id = "probe",
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

/** 三步接入的 id（桌面 `overview.js:183-185` 的三个 navigate 目标）。 */
internal const val ONBOARDING_STEP_SETTINGS: String = "settings"
internal const val ONBOARDING_STEP_ONBOARD: String = "onboard"
internal const val ONBOARDING_STEP_PROBE: String = "probe"

/** 步骤按钮文案，与桌面逐字一致：去设置 / 去接入 / 去网关页（`overview.js:183-185`）。 */
internal fun onboardingStepActionLabel(stepId: String): String = when (stepId) {
    ONBOARDING_STEP_SETTINGS -> "去设置"
    ONBOARDING_STEP_ONBOARD -> "去接入"
    else -> "去网关页"
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
