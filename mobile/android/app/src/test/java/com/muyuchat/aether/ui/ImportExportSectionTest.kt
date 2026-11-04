package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 对话框/导入导出区块的排版与文案合同。
 *
 * 覆盖三件事：
 *  1. 对话框内容限高 320dp：软键盘弹起时确认按钮仍留在窗口内；
 *  2. 失败文案把「状态词」和「错误原文」分开，页面只给状态词染 error；
 *  3. 空态必须说清为什么空 + 下一步，不许写「暂无数据」。
 */
class ImportExportSectionTest {
    @Test
    fun dialogContentHeightLeavesRoomForTheKeyboard() {
        assertEquals(320f, DialogContentMaxHeight.value)
        assertTrue(DialogContentMaxHeight.value <= 320f)
    }

    @Test
    fun gatewayHintIsOnlyShownWhenGatewayIsDown() {
        assertNull(importExportGatewayHint(running = true, action = "导出账号包"))
        val hint = importExportGatewayHint(running = false, action = "导出账号包")
        assertTrue(hint != null && hint.contains("概览") && hint.contains("导出账号包"))
    }

    @Test
    fun gatewayHintFallsBackWhenActionIsBlank() {
        val hint = importExportGatewayHint(running = false, action = "  ")
        assertTrue(hint != null && hint.contains("继续"))
    }

    @Test
    fun accountExportFailureCopySeparatesStatusFromRawMessage() {
        val offline = accountExportFailureCopy("gateway_not_running", "")
        assertEquals("网关未启动", offline.status)
        assertTrue(offline.detail.contains("概览"))

        val weak = accountExportFailureCopy("weak_passphrase", "口令至少 8 位")
        assertEquals("口令不符合要求", weak.status)
        assertEquals("口令至少 8 位", weak.detail)

        // 字段缺失时给兜底，不显示空句号。
        val blank = accountExportFailureCopy("weak_passphrase", " ")
        assertTrue(blank.detail.isNotBlank())

        val unknown = accountExportFailureCopy("boom", "")
        assertEquals("导出失败", unknown.status)
        assertTrue(unknown.detail.contains("boom"))
        assertTrue(unknown.detail.contains("错误码"))
    }

    @Test
    fun backupImportFailureCopyCoversKnownAndUnknownCodes() {
        val offline = backupImportFailureCopy("gateway_not_running", "")
        assertEquals("网关未启动", offline.status)
        assertTrue(offline.detail.contains("概览"))

        val weak = backupImportFailureCopy("weak_passphrase", "口令至少 8 位")
        assertEquals("口令不符合要求", weak.status)
        assertEquals("口令至少 8 位", weak.detail)

        val noData = backupImportFailureCopy("no_data", "")
        assertEquals("没有读到备份内容", noData.status)
        assertTrue(noData.detail.contains("重新选择"))

        val unknown = backupImportFailureCopy("", "服务端 500")
        assertEquals("导入失败", unknown.status)
        assertTrue(unknown.detail.contains("服务端 500"))
        assertTrue(unknown.detail.contains("unknown"))
    }

    @Test
    fun backupExportFailureCopyOnlyOffersOverviewForGatewayOffline() {
        val offline = backupExportFailureCopy("gateway_not_running", "")
        assertEquals("网关未启动", offline.status)
        assertTrue(offline.offerOverview)

        val weak = backupExportFailureCopy("weak_passphrase", "短")
        assertEquals("口令不符合要求", weak.status)
        assertFalse(weak.offerOverview)

        val unknown = backupExportFailureCopy("boom", "")
        assertEquals("生成失败", unknown.status)
        assertFalse(unknown.offerOverview)
        assertTrue(unknown.detail.contains("boom"))
    }

    @Test
    fun bundlePreviewEmptyStateExplainsWhyAndGivesNextStep() {
        val copy = bundlePreviewEmptyDetail()
        assertTrue(copy.contains("没有账号"))
        assertTrue(copy.contains("桌面端"))
        assertTrue(copy.contains("重新导出"))
        assertFalse(copy.contains("暂无数据"))
    }

    @Test
    fun exportedAtHandlesMissingAndPresentTimestamps() {
        assertEquals("", formatExportedAt(0L))
        assertEquals("", formatExportedAt(-1L))
        assertTrue(Regex("""\d{4}-\d{2}-\d{2} \d{2}:\d{2}""").matches(formatExportedAt(1_800_000_000L)))
    }

    @Test
    fun healthReportFailureCopyKeepsRawReasonOutOfTheStatusWord() {
        val copy = healthReportFailureCopy("磁盘已满")
        assertEquals("导出失败", copy.status)
        assertEquals("磁盘已满", copy.detail)

        // 没有原文时也要有可读兜底，不能只显示「导出失败」一个词。
        assertTrue(healthReportFailureCopy("  ").detail.contains("缓存"))
    }
}
