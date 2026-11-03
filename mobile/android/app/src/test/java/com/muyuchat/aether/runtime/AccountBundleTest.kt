package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Base64

class AccountBundleTest {
    // Produced by a standalone Go program using the desktop seal format
    // (AETHER1 | salt16 | nonce12 | AES-256-GCM) and dummy credentials only.
    private val sealedFixture: ByteArray by lazy {
        val encoded = checkNotNull(javaClass.getResourceAsStream("/account-bundle.fixture.b64"))
            .bufferedReader()
            .use { it.readText() }
            .trim()
        Base64.getDecoder().decode(encoded)
    }

    @Test
    fun previewsGoSealedBundleWithoutCredentials() {
        val preview = parseAccountBundle(sealedFixture, "fixture-passphrase-2026")
        assertEquals(2, preview.accounts.size)
        assertEquals(2, preview.providerCount)
        assertEquals(1_793_600_000L, preview.exportedAtSec)

        val claude = preview.accounts[0]
        assertEquals("claude-sub", claude.providerId)
        assertEquals("测试账号 A", claude.label)
        assertFalse(claude.disabled)

        val qoder = preview.accounts[1]
        assertEquals("qoder-cn", qoder.providerId)
        assertEquals("测试账号 B", qoder.label)
        assertTrue(qoder.disabled)

        val shown = preview.accounts.joinToString("|") { it.toString() } + preview.toString()
        assertFalse(shown.contains("TOP-SECRET"))
        assertFalse(shown.contains("credentials"))
        assertFalse(shown.contains("api_key"))
    }

    @Test
    fun wrongPassphraseIsRejected() {
        val error = runCatching { parseAccountBundle(sealedFixture, "wrong-passphrase") }.exceptionOrNull()
        assertTrue(error is AccountBundleException)
        assertEquals("bad_passphrase", (error as AccountBundleException).code)
    }

    @Test
    fun plaintextAndTruncatedFilesAreRejected() {
        val plaintext = """{"format":"aether-accounts","version":1,"accounts":[]}""".toByteArray()
        assertEquals(
            "not_sealed",
            (runCatching { parseAccountBundle(plaintext, "whatever") }.exceptionOrNull() as AccountBundleException).code,
        )
        assertEquals(
            "corrupt_bundle",
            (runCatching { parseAccountBundle("AETHER1short".toByteArray(), "whatever") }
                .exceptionOrNull() as AccountBundleException).code,
        )
        assertEquals(
            "no_passphrase",
            (runCatching { parseAccountBundle(sealedFixture, "   ") }.exceptionOrNull() as AccountBundleException).code,
        )
    }

    @Test
    fun oversizeFilesAreRejectedBeforeDecryption() {
        val error = runCatching {
            parseAccountBundle(ByteArray(8 * 1024 * 1024 + 1), "whatever")
        }.exceptionOrNull()
        assertTrue(error is AccountBundleException)
        assertEquals("file_too_large", (error as AccountBundleException).code)
    }
}
