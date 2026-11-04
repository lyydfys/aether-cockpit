package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import com.muyuchat.aether.R

/**
 * 渠道品牌图标：桌面端 `internal/web/ui2/js/brandicons.js` 的 `ICONS` 映射，原样搬到手机端。
 *
 * 图标来源：`internal/web/ui2/assets/icons` 里的 svg → `res/drawable/ic_brand_*.xml`
 * （由 `VectorDrawable` 转换，见汇报里的转换说明）。桌面端取不到图时会回退成
 * 「品牌色 + 首字母」，手机端没有这一套，统一回退到通用渠道图标 [FALLBACK_PROVIDER_ICON]。
 *
 * @property res 图标资源；`mono = true` 的是单色标（桌面 `brandicons.js` 的 `MONO`
 *   集合里那些纯黑/`currentColor` 的标），在深色主题下要按主题色 tint，否则会整块消失。
 */
internal data class ProviderIcon(
    @DrawableRes val res: Int,
    val mono: Boolean,
)

/** 未知渠道的兜底：桌面端"回退为品牌色字母块"，手机端用通用渠道图标（不编造品牌）。 */
internal val FALLBACK_PROVIDER_ICON: ProviderIcon =
    ProviderIcon(R.drawable.ic_ws_channels, mono = true)

/**
 * 单色标（需要 tint）。前 15 个与桌面 `brandicons.js:MONO` 一一对应；
 * `kiro`/`windsurf` 是纯白/近黑填充，同样必须 tint 才能在两种主题下都看得见。
 */
private val MONO_BRANDS: Set<String> = setOf(
    "claude", "cursor", "devin", "githubcopilot", "kimi", "lmstudio", "moonshot",
    "ollama", "openai", "open-design", "opencode", "siliconflow", "stepfun",
    "tencentcloud", "xai", "kiro", "windsurf",
)

/**
 * provider_id → 品牌图标。键与 `brandicons.js` 的 `ICONS` 完全一致（含所有别名）。
 *
 * 已跳过（图标含渐变/遮罩，无法转 VectorDrawable）：`antigravity`(mask+filter+use)、
 * `azure`(linearGradient)、`workbuddy`(linearGradient+filter+clipPath)；另有
 * `typesafe`、`commandcode` 在桌面端本来就是 PNG 位图。这 5 个品牌回落到通用图标。
 */
internal fun providerIcon(providerId: String?): ProviderIcon {
    val key = providerId?.trim()?.lowercase().orEmpty()
    return when (key) {
        "claude-sub" -> brand("claude")
        "openai", "openai-compat", "codex" -> brand("openai")
        "xai" -> brand("xai")
        "google", "gemini-compat" -> brand("gemini")
        "vertex" -> brand("googlecloud")
        "deepseek" -> brand("deepseek")
        "moonshot", "moonshot-cn", "kimi-code" -> brand("kimi")
        "mistral" -> brand("mistral")
        "cline" -> brand("cline")
        "volcengine" -> brand("volcengine")
        "openrouter" -> brand("openrouter")
        "ollama", "ollama-cloud" -> brand("ollama")
        "qwen", "qwen-cn", "qwen-token-plan" -> brand("qwen")
        "bailian-token-plan" -> brand("alibaba")
        "baidu-qianfan" -> brand("baidu")
        "minimax", "minimax-cn", "minimax-code", "minimax-code-global" -> brand("minimax")
        "opencode-go", "opencode" -> brand("opencode")
        "xiaomi" -> brand("xiaomi")
        "github-copilot" -> brand("githubcopilot")
        "cursor" -> brand("cursor")
        "windsurf" -> brand("windsurf")
        "zed" -> brand("zed")
        "trae-cn", "trae-intl", "trae-solo-cn", "trae-solo" -> brand("trae")
        "codebuddy", "codebuddy-intl", "codebuddy-cn" -> brand("codebuddy")
        "lmstudio" -> brand("lmstudio")
        "vllm" -> brand("vllm")
        "groq" -> brand("groq")
        "zhipu", "zai", "zcode", "zcode-cn" -> brand("zai")
        "tencent-cloud" -> brand("tencentcloud")
        "bedrock" -> brand("bedrock")
        "qoder-cn", "qoder-intl" -> brand("qoder")
        "kiro" -> brand("kiro")
        "siliconflow" -> brand("siliconflow")
        "stepfun" -> brand("stepfun")
        "devin" -> brand("devin")
        "open-design" -> brand("open-design")
        // 未转换（渐变/遮罩/位图）与未知 id 一律回落，绝不手绘
        else -> FALLBACK_PROVIDER_ICON
    }
}

private fun brand(name: String): ProviderIcon = ProviderIcon(
    res = drawableForBrand(name),
    mono = name in MONO_BRANDS,
)

@DrawableRes
private fun drawableForBrand(name: String): Int = when (name) {
    "claude" -> R.drawable.ic_brand_claude
    "openai" -> R.drawable.ic_brand_openai
    "xai" -> R.drawable.ic_brand_xai
    "gemini" -> R.drawable.ic_brand_gemini
    "googlecloud" -> R.drawable.ic_brand_googlecloud
    "deepseek" -> R.drawable.ic_brand_deepseek
    "kimi" -> R.drawable.ic_brand_kimi
    "mistral" -> R.drawable.ic_brand_mistral
    "cline" -> R.drawable.ic_brand_cline
    "volcengine" -> R.drawable.ic_brand_volcengine
    "openrouter" -> R.drawable.ic_brand_openrouter
    "ollama" -> R.drawable.ic_brand_ollama
    "qwen" -> R.drawable.ic_brand_qwen
    "alibaba" -> R.drawable.ic_brand_alibaba
    "baidu" -> R.drawable.ic_brand_baidu
    "minimax" -> R.drawable.ic_brand_minimax
    "opencode" -> R.drawable.ic_brand_opencode
    "xiaomi" -> R.drawable.ic_brand_xiaomi
    "githubcopilot" -> R.drawable.ic_brand_githubcopilot
    "cursor" -> R.drawable.ic_brand_cursor
    "windsurf" -> R.drawable.ic_brand_windsurf
    "zed" -> R.drawable.ic_brand_zed
    "trae" -> R.drawable.ic_brand_trae
    "codebuddy" -> R.drawable.ic_brand_codebuddy
    "lmstudio" -> R.drawable.ic_brand_lmstudio
    "vllm" -> R.drawable.ic_brand_vllm
    "groq" -> R.drawable.ic_brand_groq
    "zai" -> R.drawable.ic_brand_zai
    "tencentcloud" -> R.drawable.ic_brand_tencentcloud
    "bedrock" -> R.drawable.ic_brand_bedrock
    "qoder" -> R.drawable.ic_brand_qoder
    "kiro" -> R.drawable.ic_brand_kiro
    "siliconflow" -> R.drawable.ic_brand_siliconflow
    "stepfun" -> R.drawable.ic_brand_stepfun
    "devin" -> R.drawable.ic_brand_devin
    "open-design" -> R.drawable.ic_brand_open_design
    else -> FALLBACK_PROVIDER_ICON.res
}

/**
 * 桌面 `brandicons.js` 里**有专属图标**的 provider_id 全集（含别名）。
 *
 * 用于单测校验"桌面的映射一个都没漏"；不含 `antigravity`/`azure`/`workbuddy`/
 * `typesafe`/`commandcode`（这 5 个的回退在 [providerIcon] 的 else 分支里，另有单测断言）。
 */
internal val BRANDED_PROVIDER_IDS: Set<String> = setOf(
    "claude-sub",
    "openai", "openai-compat", "codex",
    "xai",
    "google", "gemini-compat",
    "vertex",
    "deepseek",
    "moonshot", "moonshot-cn", "kimi-code",
    "mistral",
    "cline",
    "volcengine",
    "openrouter",
    "ollama", "ollama-cloud",
    "qwen", "qwen-cn", "qwen-token-plan",
    "bailian-token-plan",
    "baidu-qianfan",
    "minimax", "minimax-cn", "minimax-code", "minimax-code-global",
    "opencode-go", "opencode",
    "xiaomi",
    "github-copilot",
    "cursor",
    "windsurf",
    "zed",
    "trae-cn", "trae-intl", "trae-solo-cn", "trae-solo",
    "codebuddy", "codebuddy-intl", "codebuddy-cn",
    "lmstudio",
    "vllm",
    "groq",
    "zhipu", "zai", "zcode", "zcode-cn",
    "tencent-cloud",
    "bedrock",
    "qoder-cn", "qoder-intl",
    "kiro",
    "siliconflow",
    "stepfun",
    "devin",
    "open-design",
)

/** 桌面有映射但图标无法转换（渐变/遮罩/位图）的 id：必须回落到通用图标。 */
internal val SKIPPED_PROVIDER_IDS: Set<String> = setOf(
    "antigravity", "antigravity-ide", "azure", "typesafe", "commandcode",
    "workbuddy-cn", "workbuddy-intl",
)
