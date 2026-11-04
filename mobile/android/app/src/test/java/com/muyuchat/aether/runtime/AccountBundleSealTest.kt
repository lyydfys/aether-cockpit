package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.nio.ByteBuffer
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec
import javax.crypto.SecretKeyFactory

/**
 * 账号包解密必须同时认两种桌面密封格式。
 *
 * 背景（真实踩到的坑）：桌面端现在写 **AETHER2**（头部带 ver/iters/saltLen/nonceLen，
 * 且整段头部作为 GCM 的 AAD），而手机端解析器当初只认 **AETHER1**（固定 60 万次迭代、
 * 无长度前缀、无 AAD）。结果是用真实 Windows 账号导出的包在手机上直接被判
 * `not_sealed`——"账号包导入"这条最核心的跨设备通路等于坏的。
 *
 * 这里用**测试内自造**的密文覆盖两种格式（不放任何真实账号数据进仓库）。
 */
class AccountBundleSealTest {
    private val passphrase = "test-passphrase-2026"
    private val bundleJson = """
        {"format":"aether-accounts","version":1,"created_at":1793700000,
         "accounts":[
           {"provider_id":"demo-provider","label":"演示账号","disabled":false},
           {"provider_id":"other-provider","label":"第二个","disabled":true}
         ]}
    """.trimIndent().toByteArray(Charsets.UTF_8)

    @Test
    fun parsesDesktopV2Seal() {
        val blob = sealV2(bundleJson, passphrase, iters = 120_000)
        val preview = parseAccountBundle(blob, passphrase)
        assertEquals(1793700000L, preview.exportedAtSec)
        assertEquals(2, preview.accounts.size)
        assertEquals(2, preview.providerCount)
        assertEquals("demo-provider", preview.accounts[0].providerId)
        assertEquals("演示账号", preview.accounts[0].label)
        assertTrue(preview.accounts[1].disabled)
    }

    @Test
    fun stillParsesLegacyV1Seal() {
        // AETHER1 的迭代数是**写死**的 60 万（头部没有 iters 字段），所以测试密文也必须用它。
        val blob = sealV1(bundleJson, passphrase, iters = 600_000)
        val preview = parseAccountBundle(blob, passphrase)
        assertEquals(2, preview.accounts.size)
    }

    @Test
    fun wrongPassphraseOnV2IsReportedAsBadPassphrase() {
        val blob = sealV2(bundleJson, passphrase, iters = 120_000)
        val error = runCatching { parseAccountBundle(blob, "wrong-passphrase") }.exceptionOrNull()
        assertTrue("应报 bad_passphrase，实际 $error", error is AccountBundleException)
        assertEquals("bad_passphrase", (error as AccountBundleException).code)
    }

    @Test
    fun truncatedV2HeaderIsRejectedWithoutDecrypting() {
        val blob = sealV2(bundleJson, passphrase, iters = 120_000)
        // 砍到只剩 magic + 部分头部：这是**结构性**错误，必须在派生密钥之前就拒绝。
        // （如果只砍密文尾部，GCM 只会报 tag 失败，与"口令错"无法区分——那是密码学上的等价，不该假装能分辨。）
        val truncated = blob.copyOfRange(0, 10)
        val error = runCatching { parseAccountBundle(truncated, passphrase) }.exceptionOrNull()
        assertTrue("应报 corrupt_bundle，实际 $error", error is AccountBundleException)
        assertEquals("corrupt_bundle", (error as AccountBundleException).code)
    }

    @Test
    fun unsupportedVersionByteIsRejected() {
        val blob = sealV2(bundleJson, passphrase, iters = 120_000)
        val tampered = blob.copyOf()
        tampered[7] = 9 // ver 字节
        val error = runCatching { parseAccountBundle(tampered, passphrase) }.exceptionOrNull()
        assertTrue("应报 corrupt_bundle，实际 $error", error is AccountBundleException)
        assertEquals("corrupt_bundle", (error as AccountBundleException).code)
    }

    @Test
    fun sillyIterationCountIsRejectedBeforeKdf() {
        val blob = sealV2(bundleJson, passphrase, iters = 50_000) // 低于下限 100000
        val error = runCatching { parseAccountBundle(blob, passphrase) }.exceptionOrNull()
        assertTrue("应报 corrupt_bundle，实际 $error", error is AccountBundleException)
        assertEquals("corrupt_bundle", (error as AccountBundleException).code)
    }

    // —— 下面两个 seal 只用于造测试密文，模拟桌面端的两种写入格式 ——

    private fun sealV2(plain: ByteArray, pass: String, iters: Int): ByteArray {
        val salt = ByteArray(16) { (it + 1).toByte() }
        val nonce = ByteArray(12) { (it + 11).toByte() }
        val header = ByteBuffer.allocate(7 + 1 + 4 + 1 + salt.size + 1 + nonce.size).apply {
            put("AETHER2".toByteArray(Charsets.US_ASCII))
            put(1) // ver
            putInt(iters)
            put(salt.size.toByte())
            put(salt)
            put(nonce.size.toByte())
            put(nonce)
        }.array()
        val key = pbkdf2(pass, salt, iters)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        cipher.updateAAD(header)
        return header + cipher.doFinal(plain)
    }

    private fun sealV1(plain: ByteArray, pass: String, iters: Int): ByteArray {
        val salt = ByteArray(16) { (it + 3).toByte() }
        val nonce = ByteArray(12) { (it + 21).toByte() }
        val key = pbkdf2(pass, salt, iters)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        return "AETHER1".toByteArray(Charsets.US_ASCII) + salt + nonce + cipher.doFinal(plain)
    }

    private fun pbkdf2(pass: String, salt: ByteArray, iters: Int): ByteArray {
        val spec = PBEKeySpec(pass.toCharArray(), salt, iters, 256)
        return try {
            SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
        } finally {
            spec.clearPassword()
        }
    }
}
