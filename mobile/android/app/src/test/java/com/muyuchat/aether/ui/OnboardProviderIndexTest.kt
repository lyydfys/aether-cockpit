package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.ProviderSummary
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 接入页的搜索与首字母索引（纯函数）。
 *
 * 桌面口径（onboard.js:172-178）：`display_name / family / id / description` 子串匹配，
 * 大小写不敏感；手机端没有 family，取 name / id / description。
 */
class OnboardProviderIndexTest {
    @Test
    fun blankQueryKeepsServerOrder() {
        val providers = listOf(provider("b", "Beta"), provider("a", "Alpha"))
        assertSame(providers, filterOnboardProviders(providers, ""))
        assertSame(providers, filterOnboardProviders(providers, "   "))
    }

    @Test
    fun filtersByNameIdAndDescriptionCaseInsensitively() {
        val providers = listOf(
            provider("claude", "Claude Code", "Anthropic 官方客户端"),
            provider("qoder-cn", "Qoder CN", "Qoder 中国站"),
            provider("workbuddy", "WorkBuddy", "字节跳动"),
        )
        assertEquals(listOf("claude"), filterOnboardProviders(providers, "claude").map { it.id })
        assertEquals(listOf("claude"), filterOnboardProviders(providers, "anthropic").map { it.id })
        assertEquals(listOf("qoder-cn"), filterOnboardProviders(providers, "QODER").map { it.id })
        assertEquals(listOf("workbuddy"), filterOnboardProviders(providers, "字节").map { it.id })
        assertTrue(filterOnboardProviders(providers, "没有这个渠道").isEmpty())
    }

    @Test
    fun letterUsesNameThenIdAndBucketsNonLatin() {
        assertEquals("C", onboardProviderLetter(provider("x", "Claude Code")))
        assertEquals("Q", onboardProviderLetter(provider("x", "qoder")))
        assertEquals("#", onboardProviderLetter(provider("x", "中文渠道")))
        assertEquals("#", onboardProviderLetter(provider("x", "123 渠道")))
        // 名称为空时退回 id 首字母，不把整条丢进 #。
        assertEquals("Z", onboardProviderLetter(provider("zeta", "")))
    }

    @Test
    fun groupsAreAZThenHashAndEmptyGroupsAreDropped() {
        val providers = listOf(
            provider("c", "Claude Code"),
            provider("a", "Alma"),
            provider("z", "Zed"),
            provider("cn", "中文渠道"),
        )
        val groups = groupOnboardProviders(providers)
        assertEquals(listOf("A", "C", "Z", "#"), groups.map { it.letter })
        assertEquals(listOf("Alma"), groups[0].providers.map { it.name })
        // 组内保持服务端顺序
        assertEquals(listOf("Claude Code"), groups[1].providers.map { it.name })
        // 空组不出现（B/D... 不在结果里）
        assertTrue(groups.none { it.letter == "B" })
        // 没有可分组项时返回空列表
        assertTrue(groupOnboardProviders(emptyList()).isEmpty())
    }

    @Test
    fun groupingKeepsAllProviders() {
        val providers = (0 until 30).map { index -> provider("p$index", "Provider $index") }
        val flattened = groupOnboardProviders(providers).flatMap { it.providers }
        assertEquals(providers.size, flattened.size)
        assertEquals(providers.map { it.id }.toSet(), flattened.map { it.id }.toSet())
    }

    private fun provider(id: String, name: String, description: String = ""): ProviderSummary =
        ProviderSummary(
            id = id,
            name = name,
            description = description,
            accountCount = 0,
            readyCount = 0,
            ready = false,
            off = false,
            brandColor = "",
            accounts = emptyList(),
        )
}
