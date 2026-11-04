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

/** 日志页的时间范围：与桌面 `logs.js:24` 的三个 tab 一一对应。 */
internal val RequestsPeriods: List<Pair<String, String>> = listOf(
    "today" to "今日",
    "7d" to "近 7 天",
    "30d" to "近 30 天",
)

/** 状态筛选：与桌面 `logs.js:170` 的「全部 / 成功 / 失败」一一对应。 */
internal object RequestsStatus {
    const val ALL = "all"
    const val OK = "ok"
    const val ERROR = "error"
}

/**
 * 查询条件。
 *
 * `failedOnly` 保留原语义（`GatewayHome` 直接读它），并始终由 `status` 派生，
 * 两处不会说出互相矛盾的话。
 */
data class RequestsQuery(
    val offset: Int = 0,
    val failedOnly: Boolean = false,
    val period: String = "7d",
    val status: String = RequestsStatus.ALL,
    val search: String = "",
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

/** 桌面 `logs.js:25` 的 PAGE_SIZE。 */
internal const val RequestsPageSize = 20

/** 桌面 `logs.js:120` 一次取 500 行再做本地状态筛选；服务端上限也是 500。 */
internal const val RequestsFetchLimit = 500

/** 状态筛选（纯函数，单测覆盖）：`all` 全留，`ok` 只留 2xx，`error` 只留失败。 */
internal fun requestsFilterRows(rows: List<RequestRow>, status: String): List<RequestRow> = when (status) {
    RequestsStatus.OK -> rows.filter { it.ok }
    RequestsStatus.ERROR -> rows.filter { !it.ok }
    else -> rows
}

/** 本地分页的页数；空列表也算 1 页（桌面的 `Math.max(1, ceil(...))`）。 */
internal fun requestsPageCount(total: Int, pageSize: Int = RequestsPageSize): Int {
    val safeSize = pageSize.coerceAtLeast(1)
    return ((total + safeSize - 1) / safeSize).coerceAtLeast(1)
}

/** 取第 [pageIndex] 页（0 起），越界收敛到合法范围，不抛。 */
internal fun requestsPageSlice(
    rows: List<RequestRow>,
    pageIndex: Int,
    pageSize: Int = RequestsPageSize,
): List<RequestRow> {
    val safeSize = pageSize.coerceAtLeast(1)
    val pages = requestsPageCount(rows.size, safeSize)
    val index = pageIndex.coerceIn(0, pages - 1)
    return rows.drop(index * safeSize).take(safeSize)
}

/** 时间范围的中文名，用于 KPI 说明行（「已持久化 · 近 7 天」）。 */
internal fun requestsPeriodLabel(period: String): String =
    RequestsPeriods.firstOrNull { it.first == period }?.second ?: period

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

    /** 已经成功载入过的周期；换周期要出骨架，自动刷新/搜索不出（桌面也是静默重载）。 */
    private var loadedPeriod: String? = null

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<RequestsUiState> = combine(repository.status, _query, refresh) { status, q, _ ->
        status to q
    }.flatMapLatest { (status, q) ->
        flow {
            if (!status.running) {
                loadedPeriod = null
                emit(RequestsUiState.Idle)
                return@flow
            }
            if (loadedPeriod != q.period) {
                emit(RequestsUiState.Loading)
            }
            try {
                val raw = withContext(Dispatchers.IO) {
                    client.fetchRequests(
                        status = status,
                        offset = 0,
                        limit = RequestsFetchLimit,
                        failedOnly = false,
                        period = q.period,
                        query = q.search,
                    )
                }
                val filtered = requestsFilterRows(raw.rows, q.status)
                val pages = requestsPageCount(filtered.size)
                val index = (q.offset / RequestsPageSize).coerceIn(0, pages - 1)
                loadedPeriod = q.period
                emit(
                    RequestsUiState.Ready(
                        raw.copy(
                            rows = requestsPageSlice(filtered, index),
                            offset = index * RequestsPageSize,
                            limit = RequestsPageSize,
                            total = filtered.size.toLong(),
                        ),
                    ),
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                emit(RequestsUiState.Failed(e.code, e.message))
            } catch (_: Exception) {
                emit(RequestsUiState.Failed("unknown", "无法读取本机请求账本，请重试"))
            }
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), RequestsUiState.Idle)

    /** 兼容 `GatewayHome` 的旧入口：失败/全部两态。 */
    fun setFailedOnly(value: Boolean) {
        _query.value = _query.value.copy(
            offset = 0,
            failedOnly = value,
            status = if (value) RequestsStatus.ERROR else RequestsStatus.ALL,
        )
    }

    fun setStatus(status: String) {
        val safe = status.takeIf { it == RequestsStatus.OK || it == RequestsStatus.ERROR } ?: RequestsStatus.ALL
        _query.value = _query.value.copy(
            offset = 0,
            status = safe,
            failedOnly = safe == RequestsStatus.ERROR,
        )
    }

    fun setPeriod(period: String) {
        val safe = RequestsPeriods.firstOrNull { it.first == period }?.first ?: "7d"
        if (safe == _query.value.period) {
            return
        }
        _query.value = _query.value.copy(offset = 0, period = safe)
    }

    fun setSearch(text: String) {
        if (text == _query.value.search) {
            return
        }
        _query.value = _query.value.copy(offset = 0, search = text)
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
        _query.value = current.copy(offset = (current.offset - RequestsPageSize).coerceAtLeast(0))
    }

    /** 首页（桌面分页条的「首页」）：一次到位，不靠连点上一页。 */
    fun firstPage() {
        if (_query.value.offset == 0) {
            return
        }
        _query.value = _query.value.copy(offset = 0)
    }

    /** 末页（桌面分页条的「末页」）：按当前筛选后的总条数算，越界由渲染侧收敛。 */
    fun lastPage() {
        val page = (state.value as? RequestsUiState.Ready)?.page ?: return
        val pages = requestsPageCount(page.total.toInt())
        val target = (pages - 1) * RequestsPageSize
        if (_query.value.offset == target) {
            return
        }
        _query.value = _query.value.copy(offset = target)
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

    /** 导出当前周期与筛选下的全部记录（不是当前这一页）。 */
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
        val current = _query.value
        viewModelScope.launch {
            _export.value = try {
                val bytes = withContext(Dispatchers.IO) {
                    csvClient.fetchCsv(status, current.period, current.failedOnly)
                }
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
