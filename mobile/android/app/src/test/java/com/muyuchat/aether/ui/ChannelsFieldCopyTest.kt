package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号详情/设置里每个数字字段的「标签 / 说明 / 范围与默认值」。
 *
 * 用户反馈：弹窗里只有数字，没人知道 5 / 80 / 留空 是什么意思。
 * 桌面依据 `internal/web/ui2/js/views/channels.js`：
 *  - 优先级 `0 = 默认，1 = 首选`（`:476`），clamp `≥ 0`（`:504`）
 *  - 权重 `1 - 1000`（`:477`），clamp `1..1000`（`:505`）
 *  - 并发上限 `留空 = 跟随全局`（`:478`），空 → `-1`（`:506`）
 *  - 渠道并发上限 `跟随全局`（`:1065`）、计费倍率 `1`（`:1064`）
 *  - 价格单位 `美元 / 百万 tokens`（`:1038`）、模型倍率 `留空=未配置`（`:1057`）
 */
class ChannelsFieldCopyTest {
    private val allFields = AccountField.values().toList()

    @Test
    fun everyFieldHasLabelHelpAndRange() {
        allFields.forEach { field ->
            val copy = accountFieldCopy(field)
            assertTrue("$field 缺标签", copy.label.isNotBlank())
            assertTrue("$field 缺说明", copy.help.isNotBlank())
            assertTrue("$field 缺范围/默认值/单位", copy.range.isNotBlank())
            assertFalse("$field 的说明不能只是复述标签", copy.help == copy.label)
            assertFalse("$field 的范围不能只是复述标签", copy.range == copy.label)
        }
    }

    @Test
    fun helpStaysOneLineLong() {
        // 说明是一行小字，太长就会"挤成一坨"（1.3× 尤其明显）
        allFields.forEach { field ->
            val help = accountFieldCopy(field).help
            assertTrue("$field 的说明太长（${help.length} 字）", help.length <= 48)
        }
    }

    @Test
    fun rangeAlwaysCarriesDefaultUnitOrBound() {
        // 每个数字都要能回答"范围多大 / 默认是什么 / 单位是什么"
        allFields.forEach { field ->
            val range = accountFieldCopy(field).range
            val ok = range.contains("默认") || range.contains("范围") ||
                range.contains("留空") || range.contains("单位") || range.contains("-1")
            assertTrue("$field 的范围缺范围/默认/单位：$range", ok)
        }
    }

    @Test
    fun priorityCopyMatchesDesktopSemantics() {
        val copy = accountFieldCopy(AccountField.Priority)
        assertEquals("优先级", copy.label)
        assertTrue("要讲清「越小越先」", copy.help.contains("越小"))
        assertTrue("要讲清同类排队", copy.help.contains("排队"))
        assertTrue("要写出默认值 0", copy.range.contains("默认 0"))
        assertTrue("要写出 1 = 首选", copy.range.contains("首选"))
    }

    @Test
    fun weightAndConcurrencyCopyMatchDesktopBounds() {
        val weight = accountFieldCopy(AccountField.Weight)
        assertEquals("权重", weight.label)
        assertTrue(weight.help.contains("权重") || weight.help.contains("流量"))
        assertTrue("权重范围要与桌面一致 1–1000", weight.range.contains("1 – 1000") || weight.range.contains("1-1000"))
        assertTrue("权重默认 1", weight.range.contains("默认 1"))

        val concurrency = accountFieldCopy(AccountField.Concurrency)
        assertEquals("并发上限", concurrency.label)
        assertTrue(concurrency.help.contains("同时") && concurrency.help.contains("请求"))
        assertTrue("留空/-1 要等于跟随全局", concurrency.range.contains("跟随全局"))
        assertTrue("要写 -1", concurrency.range.contains("-1"))
        assertTrue("要写单位", concurrency.range.contains("单位"))
    }

    @Test
    fun usageAndCostSayWhereTheNumberComesFrom() {
        val tokens = accountFieldCopy(AccountField.TokenUsage)
        assertTrue("令牌用量要说明是本机账本", tokens.help.contains("本机账本"))
        assertTrue("令牌用量要写单位 tokens", tokens.range.contains("tokens"))

        val cost = accountFieldCopy(AccountField.Cost)
        assertTrue("费用要说明是估算", cost.help.contains("估算") || cost.help.contains("价格表"))
        assertTrue("未计价要说清", cost.help.contains("未计价"))
        assertTrue("费用要写单位 $", cost.range.contains("$"))

        val quota = accountFieldCopy(AccountField.QuotaUsage)
        assertTrue("额度用量要说明来源不是本机统计", quota.help.contains("渠道接口"))
    }

    @Test
    fun labelsMatchDesktopFieldNames() {
        assertEquals("优先级", accountFieldCopy(AccountField.Priority).label)
        assertEquals("权重", accountFieldCopy(AccountField.Weight).label)
        assertEquals("并发上限", accountFieldCopy(AccountField.Concurrency).label)
        assertEquals("启用", accountFieldCopy(AccountField.Enabled).label)
        assertEquals("账号级白名单", accountFieldCopy(AccountField.Whitelist).label)
    }
}
