package com.muyuchat.aether.runtime

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 资源库「从仓库安装 / 检查更新 / 更新全部」的数据层。
 *
 * 端点与会写哪里（`internal/core/gateway`）：
 * - `POST /api/library/skills/probe`（`gateway.go:1041` → `library_skills_install.go:346`）
 * - `POST /api/library/skills/install`（`gateway.go:1042` → `:380`）
 * - `POST /api/library/skills/check`（`gateway.go:1043` → `library_skills_update.go:122`）
 * - `POST /api/library/skills/update-all`（`gateway.go:1045` → `:156`）
 *
 * **都写网关自有库**：install → `g.library.SaveSkill`（install:168），update →
 * `g.library.BackupContent` + `SaveSkill`（update:107-116）；只有请求体 `agents` 非空时
 * 才额外写 agent 目录（install:178-192），所以手机端恒传空数组——这条也在下面锁住。
 */
class LibrarySkillsInstallApiTest {

    @Test
    fun installJsonAlwaysSendsEmptyAgents() {
        val json = JSONObject(buildSkillInstallJson("  owner/repo  ", listOf(" a/SKILL.md ", "", "b/SKILL.md", "a/SKILL.md")))
        assertEquals("owner/repo", json.getString("source"))
        // 去空、去重、保序。
        val paths = json.getJSONArray("paths")
        assertEquals(2, paths.length())
        assertEquals("a/SKILL.md", paths.getString(0))
        assertEquals("b/SKILL.md", paths.getString(1))
        // 关键：agents 恒为空数组 → 服务端不会写 agent 目录。
        assertEquals(0, json.getJSONArray("agents").length())
    }

    @Test
    fun sourceJsonIsTrimmed() {
        assertEquals("owner/repo", JSONObject(buildSkillSourceJson("  owner/repo \n")).getString("source"))
    }

    @Test
    fun parsesProbeCandidates() {
        val probe = parseSkillProbe(
            """
            {"source":"owner/repo","kind":"github","candidates":[
              {"name":"pdf","path":"skills/pdf/SKILL.md","description":"PDF 处理","have":false},
              {"path":"skills/x/SKILL.md","have":true},
              {"name":"bad"}
            ]}
            """.trimIndent(),
        )
        assertEquals("owner/repo", probe.source)
        assertEquals("github", probe.kind)
        // 第三条没有 path → 丢掉（不是可安装项）。
        assertEquals(2, probe.candidates.size)
        assertEquals("pdf", probe.candidates[0].name)
        assertEquals("skills/pdf/SKILL.md", probe.candidates[0].path)
        assertEquals("PDF 处理", probe.candidates[0].description)
        assertFalse(probe.candidates[0].have)
        // 缺 name 时回退成**父目录名**，have 照实读。
        // 为什么是 "x" 而不是 "SKILL.md"：服务端也是这样取名的——`probeGitHubSkills` 用的是
        // `path.Base(path.Dir(p))`（library_skills_install.go:213-217），`skills/x/SKILL.md`
        // 的父目录是 `skills/x`，所以技能名是 `x`。取文件名会让所有技能都叫 `SKILL.md`。
        assertEquals("x", probe.candidates[1].name)
        assertTrue(probe.candidates[1].have)
    }

    @Test
    fun skillNameFromPathMirrorsServerRule() {
        // 与服务端同一条规则：父目录名优先。
        assertEquals("pdf", skillNameFromPath("skills/pdf/SKILL.md"))
        assertEquals("nested", skillNameFromPath("a/b/nested/SKILL.md"))
        // 根目录的 SKILL.md：服务端会回退成仓库名（客户端拿不到仓库名），
        // 这里退成文件名——只有这一种情况会显示 "SKILL.md"，且列表里能看出来。
        assertEquals("SKILL.md", skillNameFromPath("SKILL.md"))
        // 无目录段的怪路径沿用同一兜底，不抛异常。
        assertEquals("odd", skillNameFromPath("odd"))
    }

    @Test
    fun emptyProbeIsNotAnError() {
        val probe = parseSkillProbe("""{"source":"owner/repo","kind":"github","candidates":[]}""")
        assertTrue(probe.candidates.isEmpty())
    }

    @Test
    fun parsesInstallResultWithProblems() {
        val result = parseSkillInstallResult(
            """
            {"skills":[],"problems":[],"result":{
              "installed":["pdf","docx"],"had":["json"],"applied":[],
              "skipped":[{"what":"skill:big","error":"文件超过 2 MiB"}]
            }}
            """.trimIndent(),
        )
        assertEquals(listOf("pdf", "docx"), result.installed)
        assertEquals(listOf("json"), result.had)
        assertTrue(result.applied.isEmpty())
        assertEquals(1, result.skipped.size)
        assertEquals("skill:big", result.skipped.single().what)
        assertEquals("文件超过 2 MiB", result.skipped.single().error)
        assertEquals("已安装 2 个 · 已存在 1 个 · 跳过 1 个 · 写入 agent 0 个", skillInstallSummary(result))
    }

    @Test
    fun installResultWithoutResultIsAllEmpty() {
        val result = parseSkillInstallResult("""{"skills":[]}""")
        assertTrue(result.installed.isEmpty())
        assertTrue(result.skipped.isEmpty())
        assertEquals("已安装 0 个 · 写入 agent 0 个", skillInstallSummary(result))
    }

    @Test
    fun parsesCheckReportFromLibraryView() {
        val report = parseSkillChecks(
            """
            {"skills":[
              {"name":"z-old","check":{"status":"update"}},
              {"name":"a-new","check":{"status":"current"}},
              {"name":"m-gone","check":{"status":"gone","error":"404"}},
              {"name":"b-unknown","check":{"status":"unknown","error":"网络超时"}},
              {"name":"no-check"},
              {"name":"empty-status","check":{"status":""}}
            ]}
            """.trimIndent(),
        )
        // 只保留带 check.status 的；按名字排序。
        assertEquals(listOf("a-new", "b-unknown", "m-gone", "z-old"), report.rows.map { it.name })
        assertEquals(listOf("z-old"), updatableSkillNames(report))
        assertEquals("可更新 1 · 最新 1 · 来源已删除 1 · 检查失败 1", skillCheckSummary(report))
        assertEquals("检查失败", skillCheckLabel(report.rows[1].status))
        assertEquals("网络超时", report.rows[1].error)
    }

    @Test
    fun checkReportEmptySaysWhy() {
        val report = parseSkillChecks("""{"skills":[]}""")
        assertTrue(report.rows.isEmpty())
        assertTrue(skillCheckSummary(report).contains("只有「从仓库安装」的技能"))
        assertTrue(updatableSkillNames(report).isEmpty())
        assertEquals("可更新", skillCheckLabel("UPDATE"))
        assertEquals("未检查", skillCheckLabel(""))
    }

    @Test
    fun parsesUpdateBulkResult() {
        val result = parseSkillUpdateResult(
            """
            {"skills":[],"result":{
              "updated":["a","c"],"gone":["gone-1"],
              "unupdated":[{"what":"skill:b","error":"b 在本地改过，确认替换后再更新"}]
            }}
            """.trimIndent(),
        )
        assertEquals(listOf("a", "c"), result.updated)
        assertEquals(listOf("gone-1"), result.gone)
        assertEquals(1, result.unupdated.size)
        assertEquals("已更新 2 个 · 未更新 1 个 · 来源已删除 1 个", skillUpdateSummary(result))
        assertEquals(
            listOf("skill:b：b 在本地改过，确认替换后再更新"),
            skillProblemLines(result.unupdated),
        )
    }

    @Test
    fun malformedBodiesAreExplicitErrors() {
        for (body in listOf("not json", "", "[]")) {
            assertTrue(runCatching { parseSkillProbe(body) }.exceptionOrNull() is ManagementApiException)
            assertTrue(runCatching { parseSkillInstallResult(body) }.exceptionOrNull() is ManagementApiException)
            assertTrue(runCatching { parseSkillChecks(body) }.exceptionOrNull() is ManagementApiException)
            assertTrue(runCatching { parseSkillUpdateResult(body) }.exceptionOrNull() is ManagementApiException)
        }
    }

    @Test
    fun problemLinesFallBackWhenServerOmitsFields() {
        val result = parseSkillUpdateResult("""{"result":{"unupdated":[{"what":"","error":""}]}}""")
        // 为什么是 1 行而不是 0 行：`unupdated` 的条数就是结论行里"未更新 N 个"的口径
        // （skillUpdateSummary 数的是折完之后的条数）。服务端明确列了一条失败却因为字段没填
        // 就把它丢掉，用户会看到"未更新 1 个"却一条明细都没有——失败原因被咽掉。
        // 所以字段全空时兜底成「未知项 / 未返回原因」：用户看到"有一项失败了，但服务端没说是哪项"。
        assertEquals(1, result.unupdated.size)
        assertEquals("未知项", result.unupdated.single().what)
        assertEquals("未返回原因", result.unupdated.single().error)
        // 结论行与明细行数必须对得上（这是上面那条规则的直接后果）。
        assertEquals("已更新 0 个 · 未更新 1 个", skillUpdateSummary(result))
        assertEquals(listOf("未知项：未返回原因"), skillProblemLines(result.unupdated))
    }

    @Test
    fun nullElementsAreDroppedButEmptyObjectsKept() {
        // 数组里的 null 连对象都不是 → 不算"一条失败"，跳过；
        // 空对象 {} 是一条服务端明确列出的失败 → 保留并兜底。
        val result = parseSkillUpdateResult("""{"result":{"unupdated":[null,{}]}}""")
        assertEquals(1, result.unupdated.size)
        assertEquals("未知项", result.unupdated.single().what)
    }
}
