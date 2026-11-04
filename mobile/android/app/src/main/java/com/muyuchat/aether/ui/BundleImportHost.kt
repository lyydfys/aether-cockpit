package com.muyuchat.aether.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue

/** 口令弹窗当前是「预览」还是「导入」（本文件自有的小枚举，不复用别处的私有类型）。 */
private enum class BundleStage { PREVIEW, IMPORT }

/**
 * 账号包导入的「一站式宿主」：文件选择、口令输入、预览/导入调用都收在这一个文件里。
 *
 * 为什么要有它：额度页与接入渠道页**都需要**导入入口，但口令弹窗那套状态原先长在
 * `GatewayHome` 的 channels 分支里，第二个页面拿不到。这里把它整体搬出来，
 * 调用方只需要三行：
 * ```
 * val bundleImport = rememberBundleImportHost(
 *     onPreview = vm::previewAccountBundle,   // (Uri, String) -> Unit
 *     onImport = vm::importAccountBundle,     // (String) -> Unit
 * )
 * bundleImport.onPick()        // 选文件按钮
 * bundleImport.onPromptImport() // 「导入账号包」按钮
 * bundleImport.Host()          // 挂在页面根上，渲染口令弹窗
 * ```
 *
 * 逻辑与文案都沿用原实现：预览要口令、导入要口令，口令只用于本次解密，不保存不上传。
 */
internal class BundleImportHostState(
    private val onPreview: (Uri, String) -> Unit,
    private val onImport: (String) -> Unit,
) {
    private var pending by mutableStateOf<Uri?>(null)
    private var purpose by mutableStateOf<BundleStage?>(null)

    /** 选中的文件 URI 交给预览（额度页/接入页点「选择账号包」）。 */
    fun acceptPicked(uri: Uri?) {
        if (uri == null) {
            return
        }
        pending = uri
        purpose = BundleStage.PREVIEW
    }

    /** 直接进入导入（接入页「导入账号包」）：口令用到上一次预览过的包，没有就只弹预览。 */
    fun promptImport() {
        purpose = if (pending == null) BundleStage.PREVIEW else BundleStage.IMPORT
    }

    /** 口令弹窗确认。 */
    fun confirm(passphrase: String) {
        val current = purpose
        when (current) {
            BundleStage.PREVIEW -> pending?.let { onPreview(it, passphrase) }
            BundleStage.IMPORT -> onImport(passphrase)
            null -> Unit
        }
        purpose = null
        pending = null
    }

    fun dismiss() {
        purpose = null
        pending = null
    }

    @Composable
    fun Host() {
        val active = purpose ?: return
        val importing = active == BundleStage.IMPORT
        BundlePassphraseDialog(
            onConfirm = ::confirm,
            onDismiss = ::dismiss,
            title = if (importing) "确认导入账号包" else "输入账号包口令",
            detail = if (importing) {
                "口令只用于本次导入，不会保存或上传。导入会把账号写入本机账号池。"
            } else {
                "口令只用于本次本地解密，不会保存或上传。"
            },
            confirmLabel = if (importing) "确认导入" else "解密预览",
        )
    }
}

/**
 * 创建宿主状态。文件选择器必须用 `rememberLauncherForActivityResult` 注册在组合里，
 * 所以放在这里而不是状态类内部。
 */
@Composable
internal fun rememberBundleImportHost(
    onPreview: (Uri, String) -> Unit,
    onImport: (String) -> Unit,
): BundleImportHostHandle {
    var host by remember { mutableStateOf<BundleImportHostState?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        host?.acceptPicked(uri)
    }
    if (host == null) {
        host = BundleImportHostState(onPreview, onImport)
    }
    return BundleImportHostHandle(host!!, picker::launch)
}

/** 调用方拿到的句柄：选文件 / 进入导入 / 渲染弹窗。 */
internal class BundleImportHostHandle(
    private val state: BundleImportHostState,
    private val launchPicker: (Array<String>) -> Unit,
) {
    fun onPick() = launchPicker(arrayOf("*/*"))

    fun onPromptImport() = state.promptImport()

    @Composable
    fun Host() = state.Host()
}
