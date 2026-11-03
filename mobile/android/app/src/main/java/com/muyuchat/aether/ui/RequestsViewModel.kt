package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.RequestRow
import com.muyuchat.aether.runtime.RequestsApiClient
import com.muyuchat.aether.runtime.RequestsPage
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
import kotlinx.coroutines.withContext

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

class RequestsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = RequestsApiClient()
    private val _query = MutableStateFlow(RequestsQuery())
    val query: StateFlow<RequestsQuery> = _query.asStateFlow()
    private val refresh = MutableStateFlow(0)
    private val _selected = MutableStateFlow<RequestRow?>(null)
    val selected: StateFlow<RequestRow?> = _selected.asStateFlow()
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
}
