package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.AccountQuotaRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 账号详情表单 → 请求补丁 的纯逻辑。
 *
 * 红线：**只把改动过的字段发出去**（服务端缺省=不修改），所以"没动过"必须得到空补丁；
 * 清空并发要转成 `-1`（跟随全局），清空白名单要转成真的空数组。
 */
class ChannelsAccountEditTest {
    private fun row(
        priority: Int = 1,
        weight: Int = 5,
        concurrency: Int? = 2,
        disabled: Boolean = false,
        allowedModels: List<String> = listOf("m1", "m2"),
    ) = AccountQuotaRow(
        accountId = "acc-1",
        providerId = "claude-sub",
        label = "主力",
        disabled = disabled,
        resetAt = 0L,
        lastUsed = 0L,
        quotaSupported = true,
        quotaError = "",
        checkinSupported = false,
        checkin = null,
        quotaWindows = emptyList(),
        quotaTotal = 0,
        quotaUsed = 0,
        quotaUnit = "",
        quotaMessage = "",
        priority = priority,
        weight = weight,
        concurrency = concurrency,
        allowedModels = allowedModels,
    )

    /** 什么都没动 → 空补丁（调用方据此禁用保存，也不会发请求）。 */
    @Test
    fun untouchedFormProducesAnEmptyPatch() {
        val patch = accountSettingsPatch(
            row = row(),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertTrue(patch.isEmpty)
        assertNull(patch.priority)
        assertNull(patch.weight)
        assertNull(patch.concurrency)
        assertNull(patch.disabled)
        assertFalse(patch.clearAllowedModels)
    }

    /** 只改一个字段 → 补丁里只有那一个字段。 */
    @Test
    fun onlyTheChangedFieldIsIncluded() {
        val patch = accountSettingsPatch(
            row = row(),
            priorityText = "3",
            weightText = "5",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertEquals(3, patch.priority)
        assertNull(patch.weight)
        assertNull(patch.concurrency)
        assertNull(patch.disabled)
        assertFalse(patch.isEmpty)

        val toggled = accountSettingsPatch(
            row = row(),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "2",
            disabledDraft = true,
            clearAllowedModels = false,
        )
        assertEquals(true, toggled.disabled)
        assertNull(toggled.priority)
    }

    /** 并发清空 = 跟随全局（-1）；原本就跟随全局时不算改动。 */
    @Test
    fun clearingConcurrencyMeansFollowGlobal() {
        val fromSet = accountSettingsPatch(
            row = row(concurrency = 2),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "   ",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertEquals(-1, fromSet.concurrency)

        val alreadyGlobal = accountSettingsPatch(
            row = row(concurrency = null),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertNull(alreadyGlobal.concurrency)
        assertTrue(alreadyGlobal.isEmpty)
    }

    /** 白名单只在"用户点了清空 + 原本非空"时才发 []。 */
    @Test
    fun whitelistIsClearedOnlyWhenAskedAndPresent() {
        val asked = accountSettingsPatch(
            row = row(),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = true,
        )
        assertTrue(asked.clearAllowedModels)

        // 本来就没有白名单 → 不产生改动
        val nothingToClear = accountSettingsPatch(
            row = row(allowedModels = emptyList()),
            priorityText = "1",
            weightText = "5",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = true,
        )
        assertFalse(nothingToClear.clearAllowedModels)
        assertTrue(nothingToClear.isEmpty)
    }

    /** 权重缺省（服务端 0）按 1 算基线，否则一打开详情就会"看起来改过"。 */
    @Test
    fun weightBaselineTreatsZeroAsOne() {
        val patch = accountSettingsPatch(
            row = row(weight = 0),
            priorityText = "1",
            weightText = "1",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertNull(patch.weight)

        val clamped = accountSettingsPatch(
            row = row(weight = 0),
            priorityText = "1",
            weightText = "5000",
            concurrencyText = "2",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        // 超出 1..1000 夹回上限，而不是原样发出去
        assertEquals(1000, clamped.weight)
    }

    /** 非法输入不会崩，也不会被当成 0 发出去。 */
    @Test
    fun garbageInputIsIgnored() {
        val patch = accountSettingsPatch(
            row = row(),
            priorityText = "abc",
            weightText = "",
            concurrencyText = "-",
            disabledDraft = false,
            clearAllowedModels = false,
        )
        assertNull(patch.priority)
        assertNull(patch.weight)
        assertNull(patch.concurrency)
    }

    /** 「复制配置」只含接入地址/渠道/账号名，绝不带凭据。 */
    @Test
    fun copyConfigJsonHasNoCredentials() {
        val json = accountConfigJson("http://127.0.0.1:8787/v1", "claude-sub", "主力订阅")
        assertTrue(json, json.contains("\"base_url\":\"http://127.0.0.1:8787/v1\""))
        assertTrue(json, json.contains("\"provider\":\"claude-sub\""))
        assertTrue(json, json.contains("\"account\":\"主力订阅\""))
        for (forbidden in listOf("key", "token", "Bearer", "secret", "password", "cookie")) {
            assertTrue("不能含 $forbidden：$json", !json.contains(forbidden, ignoreCase = true))
        }
    }
}
