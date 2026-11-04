package com.muyuchat.aether.ui

import android.app.Application
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.BackupApiClient
import com.muyuchat.aether.runtime.BackupImportApiClient
import com.muyuchat.aether.runtime.backupImportSummary
import com.muyuchat.aether.runtime.validateBackupImportPassphrase
import com.muyuchat.aether.runtime.BackupExport
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.backupFileName
import com.muyuchat.aether.runtime.validateBackupPassphrase
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Base64

/** 备份页状态：只有「空闲 / 生成中 / 已生成待分享 / 失败」四种，没有"已备份到云端"。 */
sealed interface BackupUiState {
    data object Idle : BackupUiState

    data object Exporting : BackupUiState

    data class Ready(val fileName: String, val bytes: Long, val shareUri: Uri) : BackupUiState

    data class Failed(val code: String, val message: String) : BackupUiState
}

/** 备份导入状态：没说"已恢复"，只有"导入完成 + 服务端回报了什么"。 */
sealed interface BackupImportUiState {
    data object Idle : BackupImportUiState
    data object Importing : BackupImportUiState

    data class Done(
        val summary: String,
        val needKey: List<String>,
        val rollbackFileName: String?,
    ) : BackupImportUiState

    data class Failed(val code: String, val message: String) : BackupImportUiState
}

/**
 * 备份导出。
 *
 * 流程：口令 → 请求服务端返回密封密文（base64）→ 写进 `cacheDir/backups/` 的临时文件 →
 * 交给 FileProvider 生成 `content://` URI → 页面调 `Intent.ACTION_SEND` 分享。
 *
 * 三条硬约束：
 * 1. 口令只进请求体，**不落盘、不进 State、不进文件名**；
 * 2. 文件写在 cache 目录（系统可回收），导出结束后清掉上一份，避免越攒越多；
 * 3. 导入不做——覆盖本机设置的语义不适合在手机上误触。
 */
class BackupViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = BackupApiClient()

    private val _state = MutableStateFlow<BackupUiState>(BackupUiState.Idle)
    val state: StateFlow<BackupUiState> = _state.asStateFlow()

    private var lastFile: File? = null

    // —— 导入：会**覆盖本机设置**，所以导入前先自动留一份底 ——
    private val importClient = BackupImportApiClient()
    private val _import = MutableStateFlow<BackupImportUiState>(BackupImportUiState.Idle)
    val import: StateFlow<BackupImportUiState> = _import.asStateFlow()

    fun resetImport() {
        _import.value = BackupImportUiState.Idle
    }

    fun importPassphraseProblem(raw: String): String? = validateBackupImportPassphrase(raw)

    /**
     * 导入备份。
     *
     * 顺序很重要：**先用同一个口令导出一份当前配置留底**，再导入。
     * 凭据不会随备份回来（服务端 `NeedKey` 会列出要重填的渠道）。
     */
    fun importBackup(bytes: ByteArray, passphrase: String) {
        if (_import.value is BackupImportUiState.Importing) {
            return
        }
        val problem = validateBackupImportPassphrase(passphrase)
        if (problem != null) {
            _import.value = BackupImportUiState.Failed("weak_passphrase", problem)
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _import.value = BackupImportUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        if (bytes.isEmpty()) {
            _import.value = BackupImportUiState.Failed("no_data", "没有读到备份内容")
            return
        }
        _import.value = BackupImportUiState.Importing
        viewModelScope.launch {
            try {
                // 1) 留底：失败也不阻止导入，但页面会说明"没留成"。
                val rollback = runCatching {
                    withContext(Dispatchers.IO) {
                        writeRollbackFile(client.exportInline(status, passphrase.trim()))
                    }
                }.getOrNull()
                // 2) 真正导入
                val result = withContext(Dispatchers.IO) {
                    importClient.importBackup(
                        status = status,
                        base64 = Base64.getEncoder().encodeToString(bytes),
                        pass = passphrase.trim(),
                    )
                }
                _import.value = BackupImportUiState.Done(
                    summary = backupImportSummary(result),
                    needKey = result.needKey,
                    rollbackFileName = rollback?.name,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _import.value = BackupImportUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                _import.value = BackupImportUiState.Failed("unknown", "导入失败，请重试")
            }
        }
    }

    /** 留底文件写在同一个 cache 目录，命名带上 `rollback-` 前缀，不与正常导出混淆。 */
    private fun writeRollbackFile(export: BackupExport): File {
        val dir = File(getApplication<Application>().cacheDir, "backups")
        if (!dir.exists() && !dir.mkdirs()) {
            throw IllegalStateException("无法创建备份目录")
        }
        dir.listFiles { f -> f.isFile && f.name.startsWith("rollback-") }?.forEach { it.delete() }
        val stamp = java.text.SimpleDateFormat("yyyyMMdd-HHmmss", java.util.Locale.US)
            .format(java.util.Date(System.currentTimeMillis()))
        val file = File(dir, "rollback-$stamp.aether-backup")
        file.writeBytes(Base64.getDecoder().decode(export.base64))
        file.setReadable(true, true)
        file.setWritable(true, true)
        return file
    }

    fun reset() {
        _state.value = BackupUiState.Idle
    }

    /** 校验口令但不发请求：页面在输入时就能给出提示。 */
    fun passphraseProblem(raw: String): String? = validateBackupPassphrase(raw)

    fun export(passphrase: String) {
        if (_state.value is BackupUiState.Exporting) {
            return
        }
        val problem = validateBackupPassphrase(passphrase)
        if (problem != null) {
            _state.value = BackupUiState.Failed("weak_passphrase", problem)
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = BackupUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = BackupUiState.Exporting
        viewModelScope.launch {
            _state.value = try {
                val export: BackupExport = withContext(Dispatchers.IO) {
                    client.exportInline(status, passphrase.trim())
                }
                withContext(Dispatchers.IO) { writeAndShareUri(export) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                BackupUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                BackupUiState.Failed("unknown", "生成备份失败，请重试")
            }
        }
    }

    private fun writeAndShareUri(export: BackupExport): BackupUiState.Ready {
        val dir = File(getApplication<Application>().cacheDir, "backups")
        if (!dir.exists() && !dir.mkdirs()) {
            throw IllegalStateException("无法创建备份目录")
        }
        // 上一批导出是临时产物：连同上次进程留下的同名前缀文件一起清掉，
        // 避免 cache 里堆一串含配置的密文（进程重建后 lastFile 记不到旧文件）。
        dir.listFiles { f -> f.isFile && f.name.startsWith("aether-backup-") }
            ?.forEach { it.delete() }
        lastFile?.delete()
        val file = File(dir, backupFileName(System.currentTimeMillis()))
        val bytes = Base64.getDecoder().decode(export.base64)
        file.writeBytes(bytes)
        // 只给本应用读：**不要**用 setReadable(false,false)，那会把 owner 的读权限也去掉，
        // FileProvider 代理读取时会直接 Permission denied，分享按钮等于坏的。
        // 正确做法是保留默认 0600（应用私有），需要时显式收紧到 owner-only。
        file.setReadable(true, true)
        file.setWritable(true, true)
        lastFile = file
        val uri = FileProvider.getUriForFile(
            getApplication(),
            "${getApplication<Application>().packageName}.fileprovider",
            file,
        )
        return BackupUiState.Ready(fileName = file.name, bytes = bytes.size.toLong(), shareUri = uri)
    }

    /** 分享意图：只授权读，且只给这一个 URI。 */
    fun shareIntent(ready: BackupUiState.Ready): Intent {
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "application/octet-stream"
            putExtra(Intent.EXTRA_STREAM, ready.shareUri)
            putExtra(Intent.EXTRA_SUBJECT, ready.fileName)
            clipData = ClipData.newRawUri(ready.fileName, ready.shareUri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        return Intent.createChooser(send, "分享备份文件")
    }
}
