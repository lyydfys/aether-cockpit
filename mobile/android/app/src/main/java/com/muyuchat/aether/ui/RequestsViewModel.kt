package com.muyuchat.aether.ui

import android.app.Application
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import androidx.core.content.FileProvider
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.RequestRow
import com.muyuchat.aether.runtime.RequestsApiClient
import com.muyuchat.aether.runtime.RequestsCsvApiClient
import com.muyuchat.aether.runtime.RequestsPage
import com.muyuchat.aether.runtime.csvDataRows
import com.muyuchat.aether.runtime.requestsCsvFileName
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

data class RequestsQuery(
    val offset: Int = 0,
    val failedOnly: Boolean = false,
)

sealed interface RequestsUiState {
    data object Idle : RequestsUiState
    data object Loading : RequestsUiState
    data class Ready(val page: RequestsPage) : RequestsUiState
    data class Failed(val code: String, val message: String) : RequestsUiState
}

/** CSV 导出的页面状态。 */
sealed interface RequestsExportUiState {
    data object Idle : RequestsExportUiState
    data object Exporting : RequestsExportUiState

    data class Ready(val fileName: String, val rows: Int, val bytes: Long, val shareUri: Uri) : RequestsExportUiState

    data class Failed(val code: String, val message: String) : RequestsExportUiState
}

class RequestsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = RequestsApiClient()
    private val csvClient = RequestsCsvApiClient()
    private val _query = MutableStateFlow(RequestsQuery())
    val query: StateFlow<RequestsQuery> = _query.asStateFlow()
    private val refresh = MutableStateFlow(0)
    private val _selected = MutableStateFlow<RequestRow?>(null)
    val selected: StateFlow<RequestRow?> = _selected.asStateFlow()
    private val _export = MutableStateFlow<RequestsExportUiState>(RequestsExportUiState.Idle)
    val export: StateFlow<RequestsExportUiState> = _export.asStateFlow()
    private val pageSize = 20

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<RequestsUiState> = combine(repository.status, _query, refresh) { status, q, _ ->
        status to q
    }.flatMapLatest { (status, q) ->
        flow {
            if (!status.running) {
                emit(RequestsUiState.Idle)
                return@flow
            }
            emit(RequestsUiState.Loading)
            try {
                val page = withContext(Dispatchers.IO) {
                    client.fetchRequests(status, q.offset, pageSize, q.failedOnly)
                }
                emit(RequestsUiState.Ready(page))
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                emit(RequestsUiState.Failed(e.code, e.message))
            } catch (_: Exception) {
                emit(RequestsUiState.Failed("unknown", "无法读取本机请求账本，请重试"))
            }
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), RequestsUiState.Idle)

    fun setFailedOnly(value: Boolean) {
        _query.value = RequestsQuery(offset = 0, failedOnly = value)
    }

    fun nextPage() {
        val page = (state.value as? RequestsUiState.Ready)?.page ?: return
        if (page.offset + page.limit >= page.total) {
            return
        }
        _query.value = _query.value.copy(offset = page.offset + page.limit)
    }

    fun prevPage() {
        val current = _query.value
        if (current.offset <= 0) {
            return
        }
        _query.value = current.copy(offset = (current.offset - pageSize).coerceAtLeast(0))
    }

    fun refresh() {
        refresh.value++
    }

    fun select(row: RequestRow) {
        _selected.value = row
    }

    fun dismissDetail() {
        _selected.value = null
    }

    /** 导出当前筛选下的全部记录（不是当前这一页）。 */
    fun exportCsv() {
        if (_export.value is RequestsExportUiState.Exporting) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _export.value = RequestsExportUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _export.value = RequestsExportUiState.Exporting
        val failedOnly = _query.value.failedOnly
        viewModelScope.launch {
            _export.value = try {
                val bytes = withContext(Dispatchers.IO) { csvClient.fetchCsv(status, "today", failedOnly) }
                withContext(Dispatchers.IO) { writeAndShareUri(bytes) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                RequestsExportUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                RequestsExportUiState.Failed("unknown", "导出请求台账失败，请重试")
            }
        }
    }

    private fun writeAndShareUri(bytes: ByteArray): RequestsExportUiState.Ready {
        val context = getApplication<Application>()
        // 复用既有 file_paths.xml 已暴露的 backups/ 目录，不新开 provider 路径。
        val dir = File(context.cacheDir, "backups")
        if (!dir.exists() && !dir.mkdirs()) {
            throw IllegalStateException("无法创建导出目录")
        }
        dir.listFiles { f -> f.isFile && f.name.startsWith("aether-requests-") }?.forEach { it.delete() }
        val file = File(dir, requestsCsvFileName(System.currentTimeMillis()))
        file.writeBytes(bytes)
        file.setReadable(true, true)
        file.setWritable(true, true)
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
        return RequestsExportUiState.Ready(
            fileName = file.name,
            rows = csvDataRows(bytes),
            bytes = bytes.size.toLong(),
            shareUri = uri,
        )
    }

    fun shareIntent(ready: RequestsExportUiState.Ready): Intent {
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "text/csv"
            putExtra(Intent.EXTRA_STREAM, ready.shareUri)
            putExtra(Intent.EXTRA_SUBJECT, ready.fileName)
            clipData = ClipData.newRawUri(ready.fileName, ready.shareUri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        return Intent.createChooser(send, "分享请求台账 CSV")
    }

    fun dismissExport() {
        _export.value = RequestsExportUiState.Idle
    }
}
