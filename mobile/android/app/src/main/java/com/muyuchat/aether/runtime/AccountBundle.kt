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

// —— 两种桌面密封格式 ——
//
// AETHER2（当前写入口径）：magic(7) | ver(u8=1) | iters(u32 BE) | saltLen(u8) | salt |
//                          nonceLen(u8) | nonce(12) | AES-256-GCM 密文
//   **整段头部（magic..nonce）是 GCM 的 AAD**，所以迭代数/盐长度被改一个字节都过不了完整性校验。
// AETHER1（旧口径，只读不写）：magic(7) | salt(16) | nonce(12) | 密文，迭代数固定 600000，无 AAD。
//
// 为什么必须两种都认：桌面端早就改成写 AETHER2 了，而手机端解析器当初只认 AETHER1，
// 结果是用真实 Windows 账号导出的包在手机上直接判 `not_sealed`——跨设备导入这条核心通路是坏的。
private val SEAL_MAGIC_V2 = "AETHER2".toByteArray(Charsets.US_ASCII)
private val SEAL_MAGIC_V1 = "AETHER1".toByteArray(Charsets.US_ASCII)
private const val SEAL_FORMAT_V2: Byte = 1
private const val SEAL_MIN_ITERS = 100_000
private const val SEAL_MAX_ITERS = 10_000_000
private const val SEAL_MIN_SALT = 16
private const val SEAL_MAX_SALT = 64
private const val SEAL_V1_ITERS = 600_000
private const val SALT_BYTES_V1 = 16
private const val NONCE_BYTES = 12
private const val TAG_BYTES = 16

internal fun parseAccountBundle(file: ByteArray, passphrase: String): AccountBundlePreview {
    if (file.size > ACCOUNT_BUNDLE_MAX_BYTES) {
        throw AccountBundleException("file_too_large", "账号包超过 8 MB")
    }
    if (file.isEmpty()) {
        throw AccountBundleException("not_sealed", "账号包为空")
    }
    if (passphrase.isBlank()) {
        throw AccountBundleException("no_passphrase", "请输入导出时设置的口令")
    }
    return when {
        file.startsWith(SEAL_MAGIC_V2) -> readV2(file, passphrase)
        file.startsWith(SEAL_MAGIC_V1) -> readV1(file, passphrase)
        else -> throw AccountBundleException("not_sealed", "不是加密账号包；请从桌面端导出 .aether-accounts")
    }
}

/** AETHER2：按头部声明的迭代数解，头部整体作为 AAD。 */
private fun readV2(file: ByteArray, passphrase: String): AccountBundlePreview {
    var pos = SEAL_MAGIC_V2.size
    // magic + ver + iters + saltLen
    if (file.size < pos + 1 + 4 + 1 + 1 + 1 + TAG_BYTES) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：头部不完整")
    }
    val ver = file[pos].toInt() and 0xFF
    pos += 1
    if (ver != SEAL_FORMAT_V2.toInt()) {
        throw AccountBundleException("corrupt_bundle", "不支持的账号包格式版本 $ver：可能来自更新的客户端")
    }
    val iters = readU32(file, pos)
    pos += 4
    if (iters < SEAL_MIN_ITERS || iters > SEAL_MAX_ITERS) {
        // 解密钥之前就拒绝：既防降级，也防"迭代数被改成天文数字拖死 CPU"。
        throw AccountBundleException("corrupt_bundle", "账号包被篡改：迭代数 $iters 不在允许区间")
    }
    val saltLen = file[pos].toInt() and 0xFF
    pos += 1
    if (saltLen < SEAL_MIN_SALT || saltLen > SEAL_MAX_SALT) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：salt 长度 $saltLen 不合法")
    }
    if (file.size < pos + saltLen + 1) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：salt 不完整")
    }
    val salt = file.copyOfRange(pos, pos + saltLen)
    pos += saltLen
    val nonceLen = file[pos].toInt() and 0xFF
    pos += 1
    if (nonceLen != NONCE_BYTES) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：nonce 长度 $nonceLen 不合法")
    }
    if (file.size < pos + nonceLen + TAG_BYTES) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：密文不完整")
    }
    val nonce = file.copyOfRange(pos, pos + nonceLen)
    pos += nonceLen
    val aad = file.copyOfRange(0, pos)
    val sealed = file.copyOfRange(pos, file.size)

    val plain = decrypt(sealed, salt, nonce, passphrase, iters, aad)
    try {
        return readBundle(plain)
    } finally {
        plain.fill(0)
        salt.fill(0)
        nonce.fill(0)
        aad.fill(0)
    }
}

/** AETHER1（旧包）：固定 60 万次迭代、无长度前缀、无 AAD。 */
private fun readV1(file: ByteArray, passphrase: String): AccountBundlePreview {
    val offset = SEAL_MAGIC_V1.size
    if (file.size < offset + SALT_BYTES_V1 + NONCE_BYTES + TAG_BYTES) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏：头部不完整")
    }
    val salt = file.copyOfRange(offset, offset + SALT_BYTES_V1)
    val nonce = file.copyOfRange(offset + SALT_BYTES_V1, offset + SALT_BYTES_V1 + NONCE_BYTES)
    val sealed = file.copyOfRange(offset + SALT_BYTES_V1 + NONCE_BYTES, file.size)
    val plain = decrypt(sealed, salt, nonce, passphrase, SEAL_V1_ITERS, aad = null)
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
    iters: Int,
    aad: ByteArray?,
): ByteArray {
    val chars = passphrase.toCharArray()
    val spec = PBEKeySpec(chars, salt, iters, 256)
    val key = try {
        SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
    } catch (_: GeneralSecurityException) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏或无法解密")
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
        if (aad != null) {
            cipher.updateAAD(aad)
        }
        cipher.doFinal(sealed)
    } catch (e: AEADBadTagException) {
        throw AccountBundleException("bad_passphrase", "口令不对，无法解开账号包")
    } catch (_: GeneralSecurityException) {
        throw AccountBundleException("corrupt_bundle", "账号包损坏或无法解密")
    } finally {
        key.fill(0)
    }
}

private fun readU32(buf: ByteArray, offset: Int): Int =
    ((buf[offset].toInt() and 0xFF) shl 24) or
        ((buf[offset + 1].toInt() and 0xFF) shl 16) or
        ((buf[offset + 2].toInt() and 0xFF) shl 8) or
        (buf[offset + 3].toInt() and 0xFF)

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
