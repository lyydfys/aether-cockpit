package com.muyuchat.aether.ui

import com.muyuchat.aether.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道品牌图标映射（桌面 `internal/web/ui2/js/brandicons.js` 的 `ICONS`）。
 *
 * 口径：桌面有图的 id 必须拿到专属图标；桌面有映射但图标无法转 VectorDrawable 的
 * （渐变/遮罩/位图）与完全未知的 id 一律回落到 [FALLBACK_PROVIDER_ICON]，绝不手绘。
 */
class ProviderIconsTest {
    @Test
    fun aliasesShareTheSameBrandIcon() {
        // 桌面把它们指向同一张 svg：openai.svg
        assertEquals(providerIcon("openai").res, providerIcon("codex").res)
        assertEquals(providerIcon("openai").res, providerIcon("openai-compat").res)
        assertEquals(R.drawable.ic_brand_openai, providerIcon("codex").res)
        // trae 四个别名同图
        assertEquals(providerIcon("trae-cn").res, providerIcon("trae-intl").res)
        assertEquals(providerIcon("trae-cn").res, providerIcon("trae-solo").res)
        // 智谱系别名同图
        assertEquals(providerIcon("zhipu").res, providerIcon("zcode-cn").res)
        assertEquals(R.drawable.ic_brand_zai, providerIcon("zai").res)
        // vertex 不同于 google/gemini
        assertEquals(R.drawable.ic_brand_googlecloud, providerIcon("vertex").res)
        assertEquals(R.drawable.ic_brand_gemini, providerIcon("google").res)
    }

    @Test
    fun everyBrandedProviderIdResolvesToItsOwnIcon() {
        assertTrue(BRANDED_PROVIDER_IDS.isNotEmpty())
        for (id in BRANDED_PROVIDER_IDS) {
            val icon = providerIcon(id)
            assertTrue("$id 不该回落到通用图标", icon.res != FALLBACK_PROVIDER_ICON.res)
        }
        // 品牌数（去重后的图标）应该覆盖大部分：桌面 40 张 svg，转换成功 37 张（含 2 张自绘底板）
        val distinct = BRANDED_PROVIDER_IDS.map { providerIcon(it).res }.toSet()
        assertTrue("distinct=${distinct.size}", distinct.size >= 30)
    }

    @Test
    fun genuinelyUnbrandedChannelsStillFallBack() {
        // 只剩真正没有品牌图的两个：typesafe / commandcode（仓库里没有它们的 SVG，
        // 又不可能凭空画一个，宁可用字母贴片也不给错图）。
        for (id in SKIPPED_PROVIDER_IDS) {
            assertEquals("$id 必须回落", FALLBACK_PROVIDER_ICON.res, providerIcon(id).res)
        }
        // antigravity / azure / workbuddy 已补齐（前两者用 vector、后者用 PNG 位图），
        // 这里改成显式断言它们**不再**回退，防止以后被误删接线。
        assertTrue(providerIcon("antigravity").res != FALLBACK_PROVIDER_ICON.res)
        assertTrue(providerIcon("azure").res != FALLBACK_PROVIDER_ICON.res)
        assertTrue(providerIcon("workbuddy-cn").res != FALLBACK_PROVIDER_ICON.res)
    }

    @Test
    fun unknownBlankAndNullNeverCrashAndFallBack() {
        for (id in listOf(null, "", "   ", "no-such-provider", "OPENAI-COMPAT-9000")) {
            assertEquals(FALLBACK_PROVIDER_ICON.res, providerIcon(id).res)
        }
        // 大小写与空白容错
        assertEquals(R.drawable.ic_brand_deepseek, providerIcon("  DeepSeek ").res)
        assertEquals(R.drawable.ic_brand_openai, providerIcon("CoDeX").res)
    }

    @Test
    fun monoFlagCoversTheDesktopMonoSetPlusNearBlackMarks() {
        // 桌面 brandicons.js 的 MONO 集合必须为 true（深色下要 tint）
        for (id in listOf("claude-sub", "cursor", "devin", "github-copilot", "kimi-code", "lmstudio", "ollama", "openai")) {
            assertTrue("$id 应是单色标", providerIcon(id).mono)
        }
        // 纯白/近黑填充的标同样要 tint，否则深色主题下看不见
        assertTrue(providerIcon("kiro").mono)
        assertTrue(providerIcon("windsurf").mono)
        // 自带上色底板的彩色标不 tint
        assertFalse(providerIcon("qoder-cn").mono)
        assertFalse(providerIcon("zai").mono)
        // 桌面映射里没有裸 "gemini" 这个 id（只有 google / gemini-compat），所以用 google 验彩色标
        assertFalse(providerIcon("google").mono)
        // 而裸 "gemini" 不是桌面认的 provider_id，按未知 id 回落
        assertEquals(FALLBACK_PROVIDER_ICON.res, providerIcon("gemini").res)
        // 回落图标默认也要 tint
        assertTrue(FALLBACK_PROVIDER_ICON.mono)
    }

    @Test
    fun desktopMappingIsFullyCovered() {
        // brandicons.js 里有映射的 id = 能出图的 + 明确跳过的；漏一个这里就会失败
        val union = BRANDED_PROVIDER_IDS + SKIPPED_PROVIDER_IDS
        val desktopKeys = setOf(
            "claude-sub", "openai", "openai-compat", "codex", "xai", "google", "gemini-compat",
            "vertex", "deepseek", "moonshot", "moonshot-cn", "kimi-code", "mistral", "cline",
            "volcengine", "openrouter", "ollama", "ollama-cloud", "qwen", "qwen-cn",
            "qwen-token-plan", "bailian-token-plan", "baidu-qianfan", "minimax", "minimax-cn",
            "minimax-code", "minimax-code-global", "opencode-go", "xiaomi", "github-copilot",
            "cursor", "windsurf", "zed", "trae-cn", "trae-intl", "trae-solo-cn", "trae-solo",
            "codebuddy", "codebuddy-intl", "codebuddy-cn", "opencode", "lmstudio", "vllm",
            "typesafe", "commandcode", "groq", "zhipu", "zai", "zcode", "zcode-cn",
            "tencent-cloud", "bedrock", "azure", "antigravity", "antigravity-ide", "qoder-cn",
            "qoder-intl", "kiro", "workbuddy-cn", "workbuddy-intl", "siliconflow", "stepfun",
            "devin", "open-design",
            // 手机侧额外认的别名（桌面用 workbuddy-cn/-intl，这里两种写法都收）
            "workbuddy", "azure-openai",
        )
        val missing = desktopKeys - union
        val extra = union - desktopKeys
        assertTrue("桌面有、手机没有: $missing", missing.isEmpty())
        assertTrue("手机多出桌面没有的: $extra", extra.isEmpty())
    }
}
