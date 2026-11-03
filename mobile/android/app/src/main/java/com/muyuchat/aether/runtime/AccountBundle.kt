package com.muyuchat.aether.runtime

import org.json.JSONException
import org.json.JSONObject
import java.security.GeneralSecurityException
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec

/**
 * Read-only preview of a desktop `.aether-accounts` bundle. Only channel and
 * label metadata is kept; the credentials map is never read into the model.
 */
data class BundleAccountPreview(
    val providerId: String,
    val label: String,
    val disabled: Boolean,
)

data class AccountBundlePreview(
    val exportedAtSec: Long,
    val accounts: List<BundleAccountPreview>,
) {
    val providerCount: Int = accounts.map { it.providerId }.filter { it.isNotBlank() }.distinct().size
}

class AccountBundleException(
    val code: String,
    override val message: String,
) : Exception(message)

internal const val ACCOUNT_BUNDLE_MAX_BYTES = 8 * 1024 * 1024
private const val KDF_ITERATIONS = 600_000
private const val SALT_BYTES = 16
private const val NONCE_BYTES = 12
private const val TAG_BYTES = 16
private val SEAL_MAGIC = "AETHER1".toByteArray(Charsets.US_ASCII)

/**
 * Decrypts the desktop seal format (AETHER1 | salt | nonce | AES-256-GCM)
 * with PBKDF2-HMAC-SHA256 and maps a bundle without exposing credentials.
 */
internal fun parseAccountBundle(file: ByteArray, passphrase: String): AccountBundlePreview {
    if (file.size > ACCOUNT_BUNDLE_MAX_BYTES) {
        throw AccountBundleException("file_too_large", "账号包超过 8 MB")
    }
    if (file.isEmpty()) {
        throw AccountBundleException("not_sealed", "账号包为空")
    }
    if (!file.startsWith(SEAL_MAGIC)) {
        throw AccountBundleException("not_sealed", "不是加密账号包；请从桌面端导出 .aether-accounts")
    }
    if (passphrase.isBlank()) {
        throw AccountBundleException("no_passphrase", "请输入导出时设置的口令")
    }
    if (file.size < SEAL_MAGIC.size + SALT_BYTES + NONCE_BYTES + TAG_BYTES) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：头部不完整")
    }

    val offset = SEAL_MAGIC.size
    val salt = file.copyOfRange(offset, offset + SALT_BYTES)
    val nonce = file.copyOfRange(offset + SALT_BYTES, offset + SALT_BYTES + NONCE_BYTES)
    val sealed = file.copyOfRange(offset + SALT_BYTES + NONCE_BYTES, file.size)

    val plain = try {
        decrypt(sealed, salt, nonce, passphrase)
    } catch (e: AEADBadTagException) {
        throw AccountBundleException("bad_passphrase", "口令不对，无法解开账号包")
    } catch (_: GeneralSecurityException) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏或无法解密")
    }
    try {
        return readBundle(plain)
    } finally {
        plain.fill(0)
    }
}

private fun decrypt(
    sealed: ByteArray,
    salt: ByteArray,
    nonce: ByteArray,
    passphrase: String,
): ByteArray {
    val chars = passphrase.toCharArray()
    val spec = PBEKeySpec(chars, salt, KDF_ITERATIONS, 256)
    val key = try {
        SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
    } finally {
        spec.clearPassword()
        chars.fill('\u0000')
    }
    return try {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(
            Cipher.DECRYPT_MODE,
            SecretKeySpec(key, "AES"),
            GCMParameterSpec(128, nonce),
        )
        cipher.doFinal(sealed)
    } finally {
        key.fill(0)
    }
}

private fun readBundle(plain: ByteArray): AccountBundlePreview {
    val root = try {
        JSONObject(String(plain, Charsets.UTF_8))
    } catch (_: JSONException) {
        throw AccountBundleException("corrupt_bundle", "账号包内容损坏")
    }
    if (root.optString("format") != "aether-accounts" || root.optInt("version", 0) != 1) {
        throw AccountBundleException("corrupt_bundle", "不支持的账号包格式或版本")
    }
    val array = root.optJSONArray("accounts")
        ?: throw AccountBundleException("corrupt_bundle", "账号包缺少账号列表")
    val accounts = ArrayList<BundleAccountPreview>(array.length())
    for (index in 0 until array.length()) {
        val item = array.optJSONObject(index) ?: continue
        accounts += BundleAccountPreview(
            providerId = item.optString("provider_id").trim(),
            label = item.optString("label").trim(),
            disabled = item.optBoolean("disabled", false),
        )
    }
    return AccountBundlePreview(
        exportedAtSec = root.optLong("created_at", 0L).coerceAtLeast(0L),
        accounts = accounts,
    )
}

private fun ByteArray.startsWith(prefix: ByteArray): Boolean {
    if (size < prefix.size) {
        return false
    }
    for (index in prefix.indices) {
        if (this[index] != prefix[index]) {
            return false
        }
    }
    return true
}
