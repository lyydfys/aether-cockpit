package com.muyuchat.aether

import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Hard-lock contract test ensuring zero external/competitor/reference project
 * names appear anywhere in the Android mobile project or repository sources.
 */
class NoExternalProjectNamesContractTest {

    private fun resolveRepoRoot(): File {
        var cur: File? = File(System.getProperty("user.dir") ?: ".").canonicalFile
        while (cur != null) {
            if (File(cur, "go.mod").exists() && File(cur, "mobile").exists()) {
                return cur
            }
            cur = cur.parentFile
        }
        return File(System.getProperty("user.dir") ?: ".").canonicalFile
    }

    @Test
    fun `repository contains no external project names`() {
        val root = resolveRepoRoot()
        val tokens = listOf(
            "dsh" + "-go",
            "dsh" + "go",
            "ccch1m" + "neyyy",
            "mag" + "pie",
            "yet" + "one",
            "sub2" + "api",
            "wei-" + "shaw",
            "cockpit" + "-tools",
            "cockpit" + "_tools",
            "cockpit" + "tools",
            "Cockpit" + " Tools",
            "cc-" + "switch",
            "cc_" + "switch",
            "cc" + "switch",
            "CC " + "Switch",
            "cli" + "proxy" + "api",
            "cli" + "proxy",
            "new-" + "api",
            "one-" + "api",
            "velo" + "era",
            "opencode-" + "agy-" + "auth",
            "\\bcle" + "wd\\b",
            "lite" + "llm",
            "prompt" + "foo",
            "agent" + "gateway",
            "one-" + "hub",
            "super-" + "api",
            "chat" + "ni" + "o",
            "cherry" + " studio",
            "next" + "chat",
            "lobe-" + "chat",
            "open-" + "webui",
            "libre" + "chat",
        )
        val regex = Regex(tokens.joinToString("|"), RegexOption.IGNORE_CASE)
        val skipDirs = setOf(".git", "node_modules", ".gradle", "build", "bin", "out")
        val textExts = setOf(
            "go", "md", "js", "mjs", "ts", "css", "html", "json", "jsonc", "sh", "ps1",
            "yml", "yaml", "kt", "kts", "xml", "txt", "properties", "toml", "mod", "sum",
            "gradle", "proto", "sql", "py", "rs", "java", "svg", "env", "mk"
        )
        val textNames = setOf(
            "NOTICE", "LICENSE", "VERSION", "Dockerfile", "Makefile", "Procfile",
            ".gitignore", ".gitattributes", ".editorconfig"
        )

        // 署名文件豁免：NOTICE / LICENSE / README / CHANGELOG 必须能承载上游项目的
        // 版权与许可声明（MIT、LGPL-3.0 均要求保留归属）。硬锁要的是产品代码、脚本、
        // 配置零外部项目名，而不是抹掉法律要求的致谢。
        // 与 scripts/check-no-external-project-names.sh、internal/core/gateway 的
        // Go 契约测试保持同一份名单。
        val attributionFiles = setOf(
            "NOTICE", "NOTICE.md", "LICENSE", "LICENSE.md",
            "CHANGELOG.md", "README.md", "README.en.md"
        )

        val violations = mutableListOf<String>()

        fun scanDir(dir: File) {
            if (violations.size >= 50) return
            val children = dir.listFiles() ?: return
            for (child in children) {
                if (violations.size >= 50) return
                val name = child.name
                if (child.isDirectory) {
                    if (name in skipDirs) continue
                    val relDir = child.relativeTo(root).invariantSeparatorsPath
                    regex.find(relDir)?.let { m ->
                        violations.add("DIR $relDir matched '${m.value}'")
                    }
                    scanDir(child)
                } else {
                    val relFile = child.relativeTo(root).invariantSeparatorsPath
                    regex.find(relFile)?.let { m ->
                        violations.add("FILE $relFile matched '${m.value}'")
                    }
                    val ext = child.extension.lowercase()
                    if (ext !in textExts && name !in textNames) continue
                    // 署名文件只查路径、不查内容（见上方 attributionFiles 说明）。
                    if (relFile in attributionFiles) continue
                    val text = runCatching { child.readText(Charsets.UTF_8) }.getOrNull() ?: continue
                    text.lineSequence().forEachIndexed { idx, line ->
                        if (violations.size < 50) {
                            regex.find(line)?.let { m ->
                                violations.add("$relFile:${idx + 1} matched '${m.value}'")
                            }
                        }
                    }
                }
            }
        }

        scanDir(root)

        assertTrue(
            "Forbidden external project names found (${violations.size}):\n" +
                violations.joinToString("\n"),
            violations.isEmpty()
        )
    }
}
