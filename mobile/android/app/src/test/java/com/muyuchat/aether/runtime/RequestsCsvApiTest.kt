package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 请求台账导出（CSV）。
 *
 * 为什么不用 `/api/usage/requests/export`：那个端点把文件写到**服务端**的 `~/Downloads`
 * 并返回路径——在手机上那个路径根本不可用。
 * 正确做法是 `GET /api/usage/requests.csv`：直接回 CSV 字节，我们落盘再分享。
 *
 * 另外：`/api/usage/requests/content` 目前是**恒返回 `{found:false}` 的桩**，
 * 所以本轮**不做**"查看正文"，只做导出。
 */
class RequestsCsvApiTest {
    @Test
    fun csvPathMirrorsTheSameFiltersAsTheList() {
        assertEquals(
            "/api/usage/requests.csv?period=today",
            buildRequestsCsvPath(period = "today", failedOnly = false),
        )
        assertEquals(
            "/api/usage/requests.csv?period=today&failed=1",
            buildRequestsCsvPath(period = "today", failedOnly = true),
        )
        // 周期白名单外的值回落到 today，避免把任意字符串拼进 query
        assertEquals(
            "/api/usage/requests.csv?period=today",
            buildRequestsCsvPath(period = "40d;drop", failedOnly = false),
        )
    }

    @Test
    fun fileNameHasNoSessionOrAccountInformation() {
        val name = requestsCsvFileName(1_800_000_000_000L)
        assertTrue(Regex("""aether-requests-\d{8}-\d{6}\.csv""").matches(name))
        assertTrue(!name.contains("session"))
        assertTrue(!name.contains("account"))
    }

    @Test
    fun emptyBodyIsRejectedInsteadOfSavingAnEmptyFile() {
        for (bytes in listOf(ByteArray(0), ByteArray(1))) {
            val error = runCatching { validateRequestsCsv(bytes) }.exceptionOrNull()
            assertTrue("len=${bytes.size} 应报 empty_csv", error is ManagementApiException)
            assertEquals("empty_csv", (error as ManagementApiException).code)
        }
    }

    @Test
    fun headerOnlyCsvIsStillAccepted() {
        // 服务端在没有记录时也会回一行表头；那是合法结果，不是错误。
        val csv = "time,day,id,agent,kind\n".toByteArray(Charsets.UTF_8)
        validateRequestsCsv(csv)
        // 表头不算数据行：导出的行数必须是 0，不能把表头也算成一条请求。
        assertEquals(0, csvDataRows(csv))
    }

    @Test
    fun countsDataRowsNotHeader() {
        val csv = ("time,day,id\n" +
            "2026-11-04,2026-11-04,r1\n" +
            "2026-11-04,2026-11-04,r2\n").toByteArray(Charsets.UTF_8)
        assertEquals(2, csvDataRows(csv))
    }

    @Test
    fun jsonErrorBodyIsNotWrittenAsCsv() {
        // 服务端出错时可能回 JSON；绝不能把它当 CSV 存下来给用户。
        val json = """{"error":"boom"}""".toByteArray(Charsets.UTF_8)
        val error = runCatching { validateRequestsCsv(json) }.exceptionOrNull()
        assertTrue(error is ManagementApiException)
        assertEquals("invalid_response", (error as ManagementApiException).code)
    }
}
