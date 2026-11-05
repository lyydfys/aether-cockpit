package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R

/**
 * 渠道品牌图标映射与旗舰级底盘组件。
 *
 * 1. [providerIcon] 保持与桌面端 `brandicons.js` 的 100% 映射合同；
 * 2. 将 `cline` 纳入单色自适应染色集合，彻底解决深色模式下 `#18181B` 隐身问题；
 * 3. 新增 [ProviderBrandPlate]：有专属矢量标时渲染带微光倒角的精致底盘，
 *    属于跳过集（antigravity / azure / workbuddy 等）或自定义渠道时自动渲染
 *    8 色莫兰迪首字母徽章 ([AetherMonogramPlate])，并支持右下角在线状态呼吸点。
 */
internal data class ProviderIcon(
    @DrawableRes val res: Int,
    val mono: Boolean,
)

/** 未知渠道的兜底图标资源（保持单测合同）。 */
internal val FALLBACK_PROVIDER_ICON: ProviderIcon =
    ProviderIcon(R.drawable.ic_ws_channels, mono = true)

/**
 * 单色标（需要按主题色 tint）。
 * 包含桌面 `brandicons.js:MONO` 集合，以及 `kiro`、`windsurf`、`cline` 等近黑/纯白图标。
 */
private val MONO_BRANDS: Set<String> = setOf(
    "claude", "cursor", "devin", "githubcopilot", "kimi", "lmstudio", "moonshot",
    "ollama", "openai", "open-design", "opencode", "siliconflow", "stepfun",
    "tencentcloud", "xai", "kiro", "windsurf", "cline",
)

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

internal val SKIPPED_PROVIDER_IDS: Set<String> = setOf(
    "antigravity", "antigravity-ide", "azure", "typesafe", "commandcode",
    "workbuddy-cn", "workbuddy-intl",
)

/**
 * 旗舰级渠道品牌底盘：
 * - 若有专属品牌矢量图，渲染在带微光边框的 36dp 圆角底盘中；
 * - 若为跳过品牌或未知/自定义渠道，自动渲染 8 色莫兰迪首字徽章 ([AetherMonogramPlate])；
 * - 可选右下角在线状态小圆点（带实体描边防混色）。
 */
@Composable
internal fun ProviderBrandPlate(
    providerId: String?,
    modifier: Modifier = Modifier,
    providerName: String? = null,
    name: String? = providerName,
    plateSize: Dp = 36.dp,
    size: Dp = plateSize,
    glyphSize: Dp = 20.dp,
    online: Boolean? = null,
) {
    val spec = providerIcon(providerId)
    val dark = isAetherDarkTheme()
    val corner = RoundedCornerShape(10.dp)
    val resolvedName = name ?: providerName
    val resolvedSize = size

    Box(modifier = modifier.size(resolvedSize), contentAlignment = Alignment.Center) {
        if (spec.res == FALLBACK_PROVIDER_ICON.res) {
            val display = resolvedName?.takeIf { it.isNotBlank() }
                ?: providerId?.takeIf { it.isNotBlank() }
                ?: "A"
            AetherMonogramPlate(
                label = display,
                toneKey = providerId ?: display,
                size = resolvedSize,
                shape = corner,
            )
        } else {
            val isFullBleedLogo = spec.res == R.drawable.ic_brand_qoder || spec.res == R.drawable.ic_brand_zai
            Box(
                modifier = Modifier
                    .size(resolvedSize)
                    .clip(corner)
                    .background(AetherColors.subSurface(), corner)
                    .drawWithCache {
                        val strokePx = 0.6.dp.toPx()
                        val r = 10.dp.toPx()
                        val rimColor = if (dark) {
                            Color.White.copy(alpha = 0.10f)
                        } else {
                            Color(200, 210, 226).copy(alpha = 0.60f)
                        }
                        onDrawWithContent {
                            drawContent()
                            drawRoundRect(
                                color = rimColor,
                                topLeft = Offset(strokePx * 0.5f, strokePx * 0.5f),
                                size = this.size.copy(
                                    width = (this.size.width - strokePx).coerceAtLeast(0f),
                                    height = (this.size.height - strokePx).coerceAtLeast(0f),
                                ),
                                cornerRadius = CornerRadius(r, r),
                                style = Stroke(width = strokePx),
                            )
                        }
                    },
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    painter = painterResource(spec.res),
                    contentDescription = null,
                    tint = if (spec.mono) {
                        MaterialTheme.colorScheme.onSurface
                    } else {
                        Color.Unspecified
                    },
                    modifier = Modifier.size(if (isFullBleedLogo) (glyphSize + 4.dp) else glyphSize),
                )
            }
        }

        if (online != null) {
            val dotColor = if (online) AetherColors.success() else AetherColors.textTertiary()
            Box(
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .offset(x = 2.dp, y = 2.dp)
                    .size(11.dp)
                    .clip(CircleShape)
                    .background(AetherColors.cardSurface(), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Box(
                    modifier = Modifier
                        .size(7.dp)
                        .clip(CircleShape)
                        .background(dotColor, CircleShape),
                )
            }
        }
    }
}
