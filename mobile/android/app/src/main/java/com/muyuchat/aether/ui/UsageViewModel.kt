package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PricingCatalog
import com.muyuchat.aether.runtime.QuotaHistoryAccount
import com.muyuchat.aether.runtime.UsageApiClient
import com.muyuchat.aether.runtime.UsageDetailApiClient
import com.muyuchat.aether.runtime.UsageHeatmap
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
import kotlinx.coroutines.launch
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

/**
 * 用量细分（热力图 / 配额历史 / 价格表）。
 *
 * 与 [UsageUiState] 分开：这三块是**独立的三次只读请求**，任何一个失败都不该把总览打掉；
 * 页面上也各占一节，各自显示"没有数据 / 读不到"。
 */
sealed interface UsageDetailUiState {
    data object Idle : UsageDetailUiState
    data object Loading : UsageDetailUiState

    data class Ready(
        val heatmap: UsageHeatmap,
        val quotaHistory: List<QuotaHistoryAccount>,
        val pricing: PricingCatalog,
        val errors: Map<String, String>,
    ) : UsageDetailUiState

    data class Failed(val code: String, val message: String) : UsageDetailUiState
}

class UsageViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = UsageApiClient()
    private val detailClient = UsageDetailApiClient()
    private val periodFlow = MutableStateFlow(UsagePeriod.TODAY)
    private val refresh = MutableStateFlow(0)

    val period: StateFlow<UsagePeriod> = periodFlow.asStateFlow()

    private val _detail = MutableStateFlow<UsageDetailUiState>(UsageDetailUiState.Idle)
    val detail: StateFlow<UsageDetailUiState> = _detail.asStateFlow()

    /** 三块分别取；某一节失败时只在该节写错误，不连坐。 */
    fun refreshDetail() {
        val status = repository.status.value
        if (!status.running) {
            _detail.value = UsageDetailUiState.Idle
            return
        }
        _detail.value = UsageDetailUiState.Loading
        viewModelScope.launch {
            val errors = linkedMapOf<String, String>()
            val heatmap = runCatching {
                withContext(Dispatchers.IO) { detailClient.fetchHeatmap(status) }
            }.onFailure { errors["heatmap"] = it.message ?: "读取失败" }.getOrNull()
            val quotaHistory = runCatching {
                withContext(Dispatchers.IO) { detailClient.fetchQuotaHistory(status) }
            }.onFailure { errors["quota"] = it.message ?: "读取失败" }.getOrNull()
            val pricing = runCatching {
                withContext(Dispatchers.IO) { detailClient.fetchPricing(status) }
            }.onFailure { errors["pricing"] = it.message ?: "读取失败" }.getOrNull()
            _detail.value = if (heatmap == null && quotaHistory == null && pricing == null) {
                UsageDetailUiState.Failed("detail_unavailable", "用量细分暂时读不到，请重试")
            } else {
                UsageDetailUiState.Ready(
                    heatmap = heatmap ?: UsageHeatmap(emptyList(), "", ""),
                    quotaHistory = quotaHistory.orEmpty(),
                    pricing = pricing ?: PricingCatalog("USD", 0.0, emptyList(), emptyList()),
                    errors = errors,
                )
            }
        }
    }

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
