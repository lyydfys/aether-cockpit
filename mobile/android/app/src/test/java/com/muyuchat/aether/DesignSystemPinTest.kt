package com.muyuchat.aether

import java.io.File
import java.util.Properties
import org.junit.Assert.assertTrue
import org.junit.Test

class DesignSystemPinTest {

    @Test
    fun `vendor lock and version catalog pin the same design system`() {
        val lock = Properties().apply {
            findUp("vendor/mca-designsystem.lock").inputStream().use { load(it) }
        }
        val version = lock.getProperty("coordinate").substringAfterLast(':')
        val catalog = findUp("gradle/libs.versions.toml").readText()
        assertTrue(
            "catalog is missing mcaDesignSystem = \"$version\"",
            catalog.contains("mcaDesignSystem = \"$version\""),
        )
        assertTrue(
            "catalog is missing the designsystem module",
            catalog.contains("module = \"com.muyuchat.mca:designsystem\""),
        )
    }

    @Test
    fun `pinned design system classes are on the unit test classpath`() {
        for (type in listOf("McaThemeKt", "McaButtonKt", "McaIcons")) {
            Class.forName("com.muyuchat.core.designsystem.$type", false, javaClass.classLoader)
        }
    }

    private fun findUp(relativePath: String): File {
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val candidate = File(dir, relativePath)
            if (candidate.exists()) return candidate
            dir = dir.parentFile
        }
        error("not found upward from ${File("").absolutePath}: $relativePath")
    }
}
