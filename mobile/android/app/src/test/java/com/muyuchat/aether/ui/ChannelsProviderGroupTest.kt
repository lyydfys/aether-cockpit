package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountQuotaRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 渠道与模型选择界面的纯逻辑（与桌面逐项对齐后的口径）：
 *
 * - 模型清单搜索（弹窗可滚动 + 搜索框固定，过滤逻辑在这里）；
 * - 「分组标签」筛选（桌面 `channels.js:136/172-176`）——桌面**没有**按厂商折叠账号，
 *   同类归并靠标签，所以手机端也只提供标签筛选。
 */
class ChannelsProviderGroupTest {

    private fun row(
        accountId: String,
        providerId: String,
        tags: List<String> = emptyList(),
        disabled: Boolean = false,
    ) = AccountQuotaRow(
        accountId = accountId,
        providerId = providerId,
        label = accountId,
        disabled = disabled,
        resetAt = 0L,
        lastUsed = 0L,
        quotaSupported = false,
        quotaError = "",
        checkinSupported = false,
        checkin = null,
        quotaWindows = emptyList(),
        quotaTotal = 0,
        quotaUsed = 0,
        quotaUnit = "",
        quotaMessage = "",
        tags = tags,
    )

    @Test
    fun tagCountsAreAggregatedAndSorted() {
        val rows = listOf(
            row("a", "workbuddy-cn", tags = listOf("备用", "主力")),
            row("b", "workbuddy-cn", tags = listOf("主力")),
            row("c", "deepseek"),
            // 空白标签忽略，重复标签只算一次（同一账号同标签不重复计数）。
            row("d", "deepseek", tags = listOf("  ", "主力", "主力")),
        )
        assertEquals(
            listOf("主力" to 3, "备用" to 1),
            accountTagCounts(rows),
        )
        assertTrue(accountTagCounts(listOf(row("x", "deepseek"))).isEmpty())
    }

    @Test
    fun tagFilterMatchesDesktopSemantics() {
        val rows = listOf(
            row("a", "workbuddy-cn", tags = listOf("备用")),
            row("b", "workbuddy-cn", tags = listOf("主力")),
        )
        assertEquals(listOf("a"), filterAccountsByTag(rows, "备用").map { it.accountId })
        // null / 空白 = 不过滤（桌面 `filter.tag` 为空时不缩小列表）。
        assertEquals(rows, filterAccountsByTag(rows, null))
        assertEquals(rows, filterAccountsByTag(rows, "  "))
        assertTrue(filterAccountsByTag(rows, "不存在").isEmpty())
    }

    @Test
    fun filtersComposeWithChipAndQuery() {
        val rows = listOf(
            row("a", "workbuddy-cn", tags = listOf("备用")),
            row("b", "workbuddy-cn", tags = listOf("备用"), disabled = true),
            row("c", "deepseek", tags = listOf("主力")),
        )
        // 芯片（仅停用）+ 标签 + 搜索 三者叠加，顺序不影响结果。
        val byChip = filterAccountRows(rows, AccountFilter.DISABLED, "")
        assertEquals(listOf("b"), filterAccountsByTag(byChip, "备用").map { it.accountId })
        val searched = filterAccountRows(rows, AccountFilter.ALL, "deepseek")
        assertEquals(emptyList<AccountQuotaRow>(), filterAccountsByTag(searched, "备用"))
    }

    @Test
    fun modelSearchFiltersCaseInsensitively() {
        val models = listOf("deepseek-v4-flash", "DeepSeek-V4.1", "gpt-5.3-codex")
        assertEquals(models, filterProviderModels(models, ""))
        assertEquals(models, filterProviderModels(models, "   "))
        assertEquals(
            listOf("deepseek-v4-flash", "DeepSeek-V4.1"),
            filterProviderModels(models, "deepseek"),
        )
        assertEquals(listOf("gpt-5.3-codex"), filterProviderModels(models, "CODEX"))
        assertTrue(filterProviderModels(models, "nope").isEmpty())
    }

    @Test
    fun modelRateShowsPlainNumbersAndEmptyForUnset() {
        // 桌面 `channels.js:1113`：倍率输入框留空 = ×1；2.5 原样显示，2.0 不显示成 "2.0"。
        assertEquals("", formatModelRate(0.0))
        assertEquals("", formatModelRate(-1.0))
        assertEquals("1", formatModelRate(1.0))
        assertEquals("2", formatModelRate(2.0))
        assertEquals("2.5", formatModelRate(2.5))
    }

    @Test
    fun bindingDateAndAutoImportTimeAreHumanReadable() {
        // unix 秒 → 日期；0 = 未上报，不显示 1970。
        assertEquals("未上报", accountBindingTime(0))
        assertTrue(accountBindingTime(1_800_000_000).matches(Regex("""\d{4}-\d{2}-\d{2}""")))
        assertEquals("从未扫描", autoImportTimeLabel(0))
        assertTrue(autoImportTimeLabel(1_800_000_000).matches(Regex("""\d{2}-\d{2} \d{2}:\d{2}""")))
    }
}
