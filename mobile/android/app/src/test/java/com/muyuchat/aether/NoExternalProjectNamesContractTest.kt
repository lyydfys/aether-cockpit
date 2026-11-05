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
