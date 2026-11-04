package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.accountPath
import com.muyuchat.aether.runtime.buildWakeRunJson
import com.muyuchat.aether.runtime.wakeRunPath
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 三个"之前被判成手机端没端点"的账号动作：刷新凭据 / 立即唤醒 / 删除账号。
 *
 * 端点（`internal/core/gateway/gateway.go` 已核）：
 *  - 刷新：`POST /api/accounts/{id}/refresh`（`:1122`），桌面 `channels.js:340`
 *  - 立即唤醒：`POST /api/wake/run {"account_id":…}`，桌面 `channels.js:427`
 *  - 删除：`DELETE /api/accounts/{id}`（`:873`），桌面 `channels.js:315`
 */
class ChannelsAccountActionsTest {
    @Test
    fun refreshModeFollowsDesktopCapabilities() {
        // 桌面 `capabilities.js:1-5`：quota 优先，其次 refresh，都没有 → null
        assertEquals(AccountRefreshMode.Quota, accountRefreshMode(mapOf("quota" to true)))
        assertEquals(AccountRefreshMode.Credentials, accountRefreshMode(mapOf("refresh" to true)))
        assertEquals(
            "两种能力都有时按额度型（桌面同优先级）",
            AccountRefreshMode.Quota,
            accountRefreshMode(mapOf("quota" to true, "refresh" to true)),
        )
        assertEquals(
            "有自定义余额也算额度型",
            AccountRefreshMode.Quota,
            accountRefreshMode(emptyMap(), hasCustomQuota = true),
        )
        assertNull("都没有 → 不显示刷新", accountRefreshMode(emptyMap()))
        // 只有 chat 能力不算可刷新
        assertNull(accountRefreshMode(mapOf("chat" to true)))
    }

    @Test
    fun refreshLabelsAndSuccessNotesMatchDesktop() {
        assertEquals("刷新额度", accountRefreshLabel(AccountRefreshMode.Quota))
        assertEquals("刷新凭据", accountRefreshLabel(AccountRefreshMode.Credentials))
        assertEquals("额度已刷新", accountRefreshSuccessNote(AccountRefreshMode.Quota))
        assertEquals("凭据已刷新", accountRefreshSuccessNote(AccountRefreshMode.Credentials))
    }

    @Test
    fun wakeCopyUsesDesktopTooltipWording() {
        assertEquals("立即唤醒", accountWakeLabel())
        assertEquals("发一个最小请求，提前触发额度重置", accountWakeHint())
    }

    @Test
    fun deleteConfirmCopyMatchesDesktop() {
        assertEquals("删除账号「主力号」？", accountDeleteConfirmTitle("主力号"))
        assertEquals("只移除本机记录，渠道本身不动。", accountDeleteConfirmBody())
        // 说明里必须点出"渠道不受影响"，否则用户不敢点
        assertTrue(accountDeleteConfirmBody().contains("渠道本身不动"))
    }

    @Test
    fun deleteAndWakePathsMatchGatewayRoutes() {
        assertEquals("/api/accounts/a1", accountPath("a1"))
        // 危险字符必须编码，别拼出越权路径
        assertTrue(accountPath("a/../b").contains("%2F") || accountPath("a/../b").contains("%2E"))
        assertEquals("/api/wake/run", wakeRunPath())
        val body = JSONObject(buildWakeRunJson("a1"))
        assertEquals("a1", body.getString("account_id"))
        assertEquals(1, body.length())
    }

    @Test
    fun menuShowsWakeOnlyForChatChannels() {
        // chat=false → 不出现「立即唤醒」
        val noChat = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0, chat = false)
        assertFalse(noChat.any { it.action == AccountMoreAction.Wake })
        // chat=true → 出现，且说明用桌面 tooltip 原意
        val chat = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0, chat = true)
        val wake = chat.first { it.action == AccountMoreAction.Wake }
        assertEquals("立即唤醒", wake.label)
        assertEquals("发一个最小请求，提前触发额度重置", wake.detail)
    }

    @Test
    fun menuShowsDeleteByDefaultWithConfirmWording() {
        val items = accountMoreMenuItems(hasNote = false, tagCount = 0, presetCount = 0)
        val delete = items.first { it.action == AccountMoreAction.Delete }
        assertTrue("删除必须是危险项（渲染成错误色 + 二次确认）", delete.danger)
        assertEquals("删除账号", delete.label)
        assertEquals("只移除本机记录，渠道本身不动", delete.detail)
        // 删除排在最后（与桌面菜单一致）
        assertEquals(AccountMoreAction.Delete, items.last().action)
    }

    @Test
    fun refreshEntryUsesModeSpecificLabel() {
        val quota = accountMoreMenuItems(
            hasNote = false, tagCount = 0, presetCount = 0,
            refreshMode = AccountRefreshMode.Quota,
        ).first { it.action == AccountMoreAction.Refresh }
        assertEquals("刷新额度", quota.label)
        assertTrue(quota.detail.contains("额度"))

        val credentials = accountMoreMenuItems(
            hasNote = false, tagCount = 0, presetCount = 0,
            refreshMode = AccountRefreshMode.Credentials,
        ).first { it.action == AccountMoreAction.Refresh }
        assertEquals("刷新凭据", credentials.label)
        assertTrue(credentials.detail.contains("凭据") || credentials.detail.contains("续期"))
    }
}
