package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.ProviderSummary
import org.junit.Assert.assertEquals
import org.junit.Test

class ChannelsFilterTest {
    private val providers = listOf(
        ProviderSummary(
            "claude", "Claude", "Anthropic 订阅", 1, 1, true, false, "#D97757",
            listOf(AccountSummary("acct-9", "主力订阅", true, true, "", 0L, null)),
        ),
        ProviderSummary("custom", "Custom Relay", "本机中转", 0, 0, false, true, ""),
    )

    @Test
    fun filtersByNameIdAndDescription() {
        assertEquals(listOf("claude"), filterProviders(providers, "anthropic").map { it.id })
        assertEquals(listOf("claude"), filterProviders(providers, "CLAUDE").map { it.id })
        assertEquals(listOf("custom"), filterProviders(providers, "中转").map { it.id })
        assertEquals(providers, filterProviders(providers, "  "))
        assertEquals(emptyList<ProviderSummary>(), filterProviders(providers, "nope"))
    }

    @Test
    fun filtersByAccountNameAndId() {
        assertEquals(listOf("claude"), filterProviders(providers, "主力").map { it.id })
        assertEquals(listOf("claude"), filterProviders(providers, "acct-9").map { it.id })
    }
}
