package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 品牌图标覆盖：新补的 `azure`（两个 id 别名）必须落到真图标，不能退化到字母贴片。
 *
 * 本轮只补了 azure 一个品牌：`antigravity.svg` 与 `workbuddy.svg` 的主体都是
 * **渐变 + 滤镜/裁剪**构成的多色标识（antigravity 用 feGaussianBlur 做发光、
 * workbuddy 是渐变底 + 两个模糊圆 + 渐变 evenodd 主体），Android VectorDrawable
 * 不支持 gradient / mask / clipPath / filter，扁平化会把这几个品牌压成一块单色，
 * 等于给出一个**错误标识**，比字母贴片更糟——因此按「无法安全转换就跳过」处理。
 */
class ProviderIconsCoverageTest {
    @Test
    fun azureIdAliasesResolveToBrandVector() {
        val fallback = FALLBACK_PROVIDER_ICON.res
        assertNotEquals(fallback, providerIcon("azure").res)
        assertNotEquals(fallback, providerIcon("azure-openai").res)
        // 两个 id 指向同一张图。
        assertEquals(providerIcon("azure").res, providerIcon("azure-openai").res)
    }

    @Test
    fun azureIsMulticolorSoItMustNotBeMono() {
        // azure.svg 有 #114a8b / #0078d4 / #2892df 三层蓝色：多色品牌若进 MONO 会被 tint 成单色。
        assertTrue("azure 是多色品牌，mono 必须为 false", !providerIcon("azure").mono)
    }

    @Test
    fun antigravityIdsResolveToBitmapBrand() {
        val fallback = FALLBACK_PROVIDER_ICON.res
        listOf("antigravity", "antigravity-ide", "antigravityide").forEach { id ->
            val icon = providerIcon(id)
            assertNotEquals("$id 不应回退到兜底图标", fallback, icon.res)
            assertTrue("$id 是渐变品牌，mono 必须为 false", !icon.mono)
        }
        assertEquals(providerIcon("antigravity").res, providerIcon("antigravityide").res)
    }

    @Test
    fun workbuddyIdsResolveToBitmapBrand() {
        val fallback = FALLBACK_PROVIDER_ICON.res
        listOf("workbuddy", "workbuddy-cn", "workbuddy-intl").forEach { id ->
            val icon = providerIcon(id)
            assertNotEquals("$id 不应回退到兜底图标", fallback, icon.res)
            assertTrue("$id 是多色品牌，mono 必须为 false", !icon.mono)
        }
        assertEquals(providerIcon("workbuddy").res, providerIcon("workbuddy-cn").res)
    }

    @Test
    fun unknownProviderStillFallsBack() {
        assertEquals(FALLBACK_PROVIDER_ICON.res, providerIcon("no-such-provider").res)
        assertEquals(FALLBACK_PROVIDER_ICON.res, providerIcon(null).res)
        assertEquals(FALLBACK_PROVIDER_ICON.res, providerIcon("  ").res)
    }
}
