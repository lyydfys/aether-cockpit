package com.muyuchat.aether.ui

import android.app.Application
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AccountExport
import com.muyuchat.aether.runtime.AccountExportApiClient
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.accountExportFileName
import com.muyuchat.aether.runtime.validateAccountExportPassphrase
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Base64

/** 账号包导出的页面状态。没有"已备份到云端"这种状态，只有"已生成，等你去分享"。 */
sealed interface AccountExportUiState {
    data object Idle : AccountExportUiState

    data object Exporting : AccountExportUiState

    data class Ready(val fileName: String, val accounts: Int, val bytes: Long, val shareUri: Uri) : AccountExportUiState

    data class Failed(val code: String, val message: String) : AccountExportUiState
}

/**
 * 把本机账号导出成 `.aether-accounts` 交给桌面端。
 *
 * 与 [BackupViewModel] 的区别：备份导的是设置/虚拟组；**这里导的是真实凭证**。
 * 所以：口令只在请求体里出现一次，文件写在 cache 目录、导出前清掉上一份，口令不进文件名。
 */
class AccountExportViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = AccountExportApiClient()

    private val _state = MutableStateFlow<AccountExportUiState>(AccountExportUiState.Idle)
    val state: StateFlow<AccountExportUiState> = _state.asStateFlow()

    fun reset() {
        _state.value = AccountExportUiState.Idle
    }

    /** 页面输入时就能提示，不必等发请求。 */
    fun passphraseProblem(raw: String): String? = validateAccountExportPassphrase(raw)

    fun export(passphrase: String) {
        if (_state.value is AccountExportUiState.Exporting) {
            return
        }
        val problem = validateAccountExportPassphrase(passphrase)
        if (problem != null) {
            _state.value = AccountExportUiState.Failed("weak_passphrase", problem)
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = AccountExportUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = AccountExportUiState.Exporting
        viewModelScope.launch {
            _state.value = try {
                val export: AccountExport = withContext(Dispatchers.IO) {
                    client.export(status, passphrase.trim())
                }
                withContext(Dispatchers.IO) { writeAndShareUri(export) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                AccountExportUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                AccountExportUiState.Failed("unknown", "导出账号包失败，请重试")
            }
        }
    }

    private fun writeAndShareUri(export: AccountExport): AccountExportUiState.Ready {
        val context = getApplication<Application>()
        // 复用 file_paths.xml 里已暴露的 backups/ 目录，避免为一个文件再开一条 provider 路径。
        val dir = File(context.cacheDir, "backups")
        if (!dir.exists() && !dir.mkdirs()) {
            throw IllegalStateException("无法创建导出目录")
        }
        // 上一份账号包是临时产物：连同上次进程留下的同前缀文件一起清掉。
        dir.listFiles { f -> f.isFile && f.name.startsWith("aether-accounts-") }?.forEach { it.delete() }

        val name = export.filename.ifBlank { accountExportFileName(System.currentTimeMillis()) }
        // 文件名兜底也不许带路径分隔符：它是服务端给的字符串，不能直接当路径用。
        val safeName = name.replace('/', '_').replace('\\', '_')
        val file = File(dir, safeName)
        val bytes = Base64.getDecoder().decode(export.base64)
        file.writeBytes(bytes)
        // 保留默认 0600；不要把 owner 的读权限也去掉（否则 FileProvider 读不到）。
        file.setReadable(true, true)
        file.setWritable(true, true)
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
        return AccountExportUiState.Ready(
            fileName = file.name,
            accounts = export.accounts,
            bytes = bytes.size.toLong(),
            shareUri = uri,
        )
    }

    /** 分享意图：只授权读，且只给这一个 URI。 */
    fun shareIntent(ready: AccountExportUiState.Ready): Intent {
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "application/octet-stream"
            putExtra(Intent.EXTRA_STREAM, ready.shareUri)
            putExtra(Intent.EXTRA_SUBJECT, ready.fileName)
            clipData = ClipData.newRawUri(ready.fileName, ready.shareUri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        return Intent.createChooser(send, "分享账号包")
    }
}
