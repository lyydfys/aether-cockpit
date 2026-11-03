package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.UsageApiClient
import com.muyuchat.aether.runtime.UsageSummary
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

enum class UsagePeriod(val api: String, val label: String) {
    TODAY("today", "今日"),
    WEEK("7d", "7 天"),
}

sealed interface UsageUiState {
    data object Idle : UsageUiState
    data object Loading : UsageUiState
    data class Ready(val summary: UsageSummary) : UsageUiState
    data class Failed(val code: String, val message: String) : UsageUiState
}

class UsageViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = UsageApiClient()
    private val periodFlow = MutableStateFlow(UsagePeriod.TODAY)
    private val refresh = MutableStateFlow(0)

    val period: StateFlow<UsagePeriod> = periodFlow.asStateFlow()

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<UsageUiState> = combine(repository.status, periodFlow, refresh) { status, period, _ ->
        Triple(status, period, Unit)
    }.flatMapLatest { (status, period, _) ->
        flow {
            if (!status.running) {
                emit(UsageUiState.Idle)
                return@flow
            }
            emit(UsageUiState.Loading)
            try {
                val summary = withContext(Dispatchers.IO) { client.fetchUsage(status, period.api) }
                emit(UsageUiState.Ready(summary))
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                emit(UsageUiState.Failed(e.code, e.message))
            } catch (_: Exception) {
                emit(UsageUiState.Failed("unknown", "无法读取本机用量，请重试"))
            }
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), UsageUiState.Idle)

    fun setPeriod(value: UsagePeriod) {
        periodFlow.value = value
    }

    fun refresh() {
        refresh.value++
    }
}
