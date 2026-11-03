package com.muyuchat.aether.runtime

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class AccountKeyException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Keeps the account envelope key in Android Keystore.
 *
 * The Keystore AES key never leaves the TEE/StrongBox; only the wrapped 32-byte
 * data key is stored in app-private prefs. The unwrapped key is handed to the Go
 * runtime at start and is never written to disk or logged.
 */
internal class AccountKeyStore(context: Context) {
    private val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun loadOrCreate(): String {
        val wrapped = prefs.getString(WRAPPED, null)
        return try {
            val key = if (wrapped == null) createDataKey() else unwrap(wrapped)
            Base64.encodeToString(key, Base64.NO_WRAP)
        } catch (e: Exception) {
            throw AccountKeyException("账号密钥不可用", e)
        }
    }

    private fun createDataKey(): ByteArray {
        val dek = ByteArray(32)
        SecureRandom().nextBytes(dek)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, loadKeystoreKey())
        val ciphertext = cipher.doFinal(dek)
        val packed = cipher.iv + ciphertext
        check(prefs.edit().putString(WRAPPED, Base64.encodeToString(packed, Base64.NO_WRAP)).commit()) {
            "无法保存账号密钥"
        }
        return dek
    }

    private fun unwrap(value: String): ByteArray {
        val packed = Base64.decode(value, Base64.NO_WRAP)
        require(packed.size > GCM_IV_BYTES) { "账号密钥密文不完整" }
        val iv = packed.copyOfRange(0, GCM_IV_BYTES)
        val ciphertext = packed.copyOfRange(GCM_IV_BYTES, packed.size)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, loadKeystoreKey(), GCMParameterSpec(128, iv))
        return cipher.doFinal(ciphertext).also { require(it.size == 32) { "账号密钥长度不正确" } }
    }

    private fun loadKeystoreKey(): SecretKey {
        val store = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val ALIAS = "aether-account-dek-wrap"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val PREFS = "aether_account_key"
        const val WRAPPED = "wrapped_dek"
        const val GCM_IV_BYTES = 12
    }
}
