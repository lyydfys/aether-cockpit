package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 自检（`GET /api/diagnose`）。
 *
 * 契约：`{items:[{level,title,detail,suggestion}], ok, warns, errors, summary}`。
 * 已核 handler（`diag.go`）：**不含凭据**，只含监听地址、渠道 ID、计数与 spec 错误串。
 *
 * 三条口径：
 * 1. 这是**一次真实检查**的结果，结论行写「刚刚检查」，不写"系统正常"这种持续状态；
 * 2. `error` 必须优先显示，且带服务端给的 suggestion（可执行的下一步）；
 * 3. 未知 level 不能静默丢弃（宁可显示原文，也不要让它消失）。
 */
class DiagnoseApiTest {
    private val body = """
        {"ok":3,"warns":1,"errors":1,"summary":"1 个错误需要处理",
         "items":[
           {"level":"ok","title":"监听地址安全","detail":"127.0.0.1:7860（仅本机可访问）"},
           {"level":"ok","title":"已注册 32 个渠道","detail":"antigravity, workbuddy-cn"},
           {"level":"ok","title":"会话状态","detail":"12 条快照"},
           {"level":"warn","title":"未配置虚拟模型组","suggestion":"在路由页建一个组"},
           {"level":"error","title":"全局没有可用账号","detail":"0/2 账号可用","suggestion":"先在渠道页导入或登录账号"}
         ]}
    """.trimIndent()

    @Test
    fun parsesReportAndItems() {
        val report = parseDiagnose(body)
        assertEquals(3, report.ok)
        assertEquals(1, report.warns)
        assertEquals(1, report.errors)
        assertEquals(5, report.items.size)
        val error = report.items.first { it.level == "error" }
        assertEquals("全局没有可用账号", error.title)
        assertTrue(error.suggestion.contains("导入或登录"))
    }

    @Test
    fun errorsComeFirstInDisplayOrder() {
        val report = parseDiagnose(body)
        // 展示顺序：error → warn → ok，同级保持服务端顺序
        val ordered = diagnoseDisplayOrder(report)
        assertEquals("error", ordered.first().level)
        assertEquals("ok", ordered.last().level)
    }

    @Test
    fun summaryLineIsPastTenseAndCounts() {
        val report = parseDiagnose(body)
        val line = diagnoseSummaryLine(report)
        assertTrue("要写成一次检查：$line", line.contains("刚刚检查"))
        assertTrue(line.contains("1"))
        assertTrue(line.contains("3"))
        // 不允许"系统正常"这种持续断言
        assertTrue(!line.contains("系统正常"))
    }

    @Test
    fun cleanReportSaysAllPassed() {
        val report = parseDiagnose("""{"ok":5,"warns":0,"errors":0,"items":[{"level":"ok","title":"a"}]}""")
        val line = diagnoseSummaryLine(report)
        assertTrue(line.contains("刚刚检查"))
        assertTrue(line.contains("无需处理"))
    }

    @Test
    fun unknownLevelIsKeptNotDropped() {
        val report = parseDiagnose(
            """{"ok":0,"warns":0,"errors":0,"items":[{"level":"info","title":"自定义项","detail":"x"}]}""",
        )
        assertEquals(1, report.items.size)
        assertEquals("info", report.items[0].level)
    }

    @Test
    fun malformedBodyRaisesInvalidResponse() {
        for (b in listOf("not-json", "")) {
            val error = runCatching { parseDiagnose(b) }.exceptionOrNull()
            assertTrue(error is ManagementApiException)
            assertEquals("invalid_response", (error as ManagementApiException).code)
        }
    }
}
