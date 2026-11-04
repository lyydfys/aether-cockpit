package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/**
 * 脱敏「试一试」+「最近的脱敏记录」的数据层。
 *
 * 覆盖三条路径：正常、空、字段缺失/格式错。重点断言"只读得进命中与类型，
 * 原文一个字段都不读"，以及记录时间/命中数的展示口径。
 */
class MaskingApiTest {

    @Test
    fun parsesPreviewTextHitsAndFlags() {
        val preview = parseMaskingPreview(
            """
            {"text":"联系 me@example.com 或 key=sk-live-123",
             "hits":[{"placeholder":"EMAIL_1","kind":"EMAIL","by":"rule","action":"mask","head":"m","len":16,"n":2},
                     {"placeholder":"SECRET_1","kind":"SECRET","by":"rule","action":"mask","head":"s","len":11,"n":1}],
             "off":false,"blocked":false}
            """.trimIndent(),
        )
        assertEquals("联系 me@example.com 或 key=sk-live-123", preview.text)
        assertEquals(2, preview.hits.size)
        assertEquals("EMAIL", preview.hits[0].kind)
        assertEquals("EMAIL_1", preview.hits[0].placeholder)
        assertEquals("mask", preview.hits[0].action)
        assertEquals(2, preview.hits[0].count)
        assertFalse(preview.blocked)
        assertFalse(preview.off)
        assertEquals("命中 3 处 · EMAIL、SECRET", maskingHitSummary(preview.hits))
    }

    @Test
    fun previewWithoutHitsIsHonestAboutIt() {
        val preview = parseMaskingPreview("""{"text":"没有任何敏感信息","hits":[],"off":true,"blocked":false}""")
        assertTrue(preview.hits.isEmpty())
        assertEquals("没有命中任何脱敏规则", maskingHitSummary(preview.hits))
        // 开关关闭时预览仍可用，但必须提示"真请求不会被改写"。
        assertTrue(maskingPreviewOffNote(preview).contains("关闭"))
        // 没命中拦截时不给拦截提示。
        assertEquals("", maskingPreviewBlockedNote(preview))
    }

    @Test
    fun blockedPreviewExplains403() {
        val preview = parseMaskingPreview("""{"text":"xxx","hits":[],"off":false,"blocked":true}""")
        val note = maskingPreviewBlockedNote(preview)
        assertTrue(note, note.contains("拦截"))
        assertTrue(note, note.contains("403"))
    }

    @Test
    fun malformedPreviewIsAnExplicitError() {
        for (body in listOf("not json", "", "[]")) {
            val error = runCatching { parseMaskingPreview(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    @Test
    fun parsesRecordsWithDistinctKindsAndCounts() {
        val records = parseMaskingRecords(
            """
            {"records":[
              {"key":"k1","t":1760000000000,"agent":"claude_code","provider":"qoder","model":"qwen3-max",
               "what":"chat","n":3,"blocked":false,
               "values":[{"p":"EMAIL_1","k":"EMAIL","a":"mask","b":"rule","h":"m","l":16,"n":2},
                         {"p":"EMAIL_2","k":"EMAIL","a":"mask","b":"rule","h":"x","l":14,"n":1},
                         {"p":"SECRET_1","k":"SECRET","a":"mask","b":"rule","h":"s","l":11,"n":1}]},
              {"key":"k2","t":1760000100000,"n":0,"blocked":true,"values":[]}
            ],"more":true}
            """.trimIndent(),
        )
        assertEquals(2, records.records.size)
        assertTrue(records.more)
        val first = records.records[0]
        assertEquals("k1", first.key)
        assertEquals(3, first.hitCount)
        assertEquals(listOf("EMAIL", "SECRET"), first.kinds)
        assertEquals("3 处 · 2 类", maskingRecordHitLabel(first))
        assertEquals("qoder · claude_code · qwen3-max", maskingRecordSourceLine(first))
        // 被拦下的记录要把"已拦截"写进右栏。
        val second = records.records[1]
        assertTrue(second.blocked)
        assertTrue(maskingRecordHitLabel(second).contains("已拦截"))
        assertEquals("0 处 · 已拦截", maskingRecordHitLabel(second))
    }

    @Test
    fun emptyRecordsStillParse() {
        val records = parseMaskingRecords("""{"records":[],"more":false}""")
        assertTrue(records.records.isEmpty())
        assertFalse(records.more)
        // 空态必须说清"为什么空 + 下一步"，不许写"暂无数据"。
        val detail = maskingRecordsEmptyDetail()
        assertTrue(detail, detail.contains("为什么空"))
        assertTrue(detail, detail.contains("下一步"))
        assertFalse(detail, detail.contains("暂无数据"))
    }

    @Test
    fun recordTimestampUsesLocalWallClock() {
        val atMs = Instant.parse("2026-11-04T10:20:00Z").toEpochMilli()
        assertEquals("11-04 18:20", maskingTimestamp(atMs, ZoneId.of("Asia/Shanghai")))
        assertEquals("11-04 10:20", maskingTimestamp(atMs, ZoneId.of("UTC")))
        // 缺失时间不编造，写"时间未知"。
        assertEquals("时间未知", maskingTimestamp(0L, ZoneId.of("UTC")))
    }

    @Test
    fun recordWithoutSourceFieldsKeepsLineEmpty() {
        val records = parseMaskingRecords("""{"records":[{"key":"k","t":1,"n":1}]}""")
        val record = records.records.single()
        assertEquals("", maskingRecordSourceLine(record))
        assertTrue(record.kinds.isEmpty())
        assertEquals("1 处", maskingRecordHitLabel(record))
    }
}
