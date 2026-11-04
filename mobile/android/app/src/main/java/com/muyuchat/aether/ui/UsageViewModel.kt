package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PricingCatalog
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

/** 时间范围：与桌面 `usage.js:59` 的三个分段一一对应（今日 / 近 7 天 / 近 30 天）。 */
enum class UsagePeriod(val api: String, val label: String) {
    TODAY("today", "今日"),
    WEEK("7d", "近 7 天"),
    MONTH("30d", "近 30 天"),
}

/** 用量分布的分类维度：桌面 `usage.js:10` 的 CLASSES（渠道 / 模型 / 调用方）。 */
enum class UsageBreakdown(val label: String) {
    PROVIDER("渠道"),
    MODEL("模型"),
    AGENT("调用方"),
}

sealed interface UsageUiState {
    data object Idle : UsageUiState
    data object Loading : UsageUiState
    data class Ready(val summary: UsageSummary) : UsageUiState
    data class Failed(val code: String, val message: String) : UsageUiState
}

/**
 * 用量细分（热力图）。
 *
 * 与 [UsageUiState] 分开：热力图是**独立的一次只读请求**，它失败不该把总览打掉。
 * 价格表不在这一层——桌面用量页没有价格表卡，只有计价币种切换（[UsageViewModel.pricing]）。
 */
sealed interface UsageDetailUiState {
    data object Idle : UsageDetailUiState
    data object Loading : UsageDetailUiState
    data class Ready(val heatmap: UsageHeatmap, val errors: Map<String, String>) : UsageDetailUiState
    data class Failed(val code: String, val message: String) : UsageDetailUiState
}

class UsageViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = UsageApiClient()
    private val detailClient = UsageDetailApiClient()
    private val periodFlow = MutableStateFlow(UsagePeriod.TODAY)
    private val breakdownFlow = MutableStateFlow(UsageBreakdown.PROVIDER)
    private val refresh = MutableStateFlow(0)
    private val currencyTick = MutableStateFlow(0)

    val period: StateFlow<UsagePeriod> = periodFlow.asStateFlow()
    val breakdown: StateFlow<UsageBreakdown> = breakdownFlow.asStateFlow()

    private val _detail = MutableStateFlow<UsageDetailUiState>(UsageDetailUiState.Idle)
    val detail: StateFlow<UsageDetailUiState> = _detail.asStateFlow()

    private val _currencyError = MutableStateFlow<String?>(null)
    val currencyError: StateFlow<String?> = _currencyError.asStateFlow()

    /**
     * 价格表（币种 + 汇率）。
     *
     * 桌面 `usage.js:25-32` 也是每次加载都取 `/api/pricing`：它决定所有金额的符号与换算，
     * 取不到就退回 USD/7.2 的默认值——**不猜**用户在设置里存的是什么。
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val pricing: StateFlow<PricingCatalog?> =
        combine(repository.status, currencyTick) { status, tick -> status to tick }
            .flatMapLatest { (status, _) ->
                flow {
                    if (!status.running) {
                        emit(null)
                        return@flow
                    }
                    emit(
                        runCatching {
                            withContext(Dispatchers.IO) { detailClient.fetchPricing(status) }
                        }.getOrNull(),
                    )
                }
            }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /** 热力图：独立一次只读请求。 */
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
            _detail.value = if (heatmap == null) {
                UsageDetailUiState.Failed("detail_unavailable", "用量日历暂时读不到，请重试")
            } else {
                UsageDetailUiState.Ready(heatmap = heatmap, errors = errors)
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

    fun setBreakdown(value: UsageBreakdown) {
        breakdownFlow.value = value
    }

    /**
     * 切换计价币种（桌面 `usage.js:78` → `POST /api/pricing/save`）。
     * 失败只在页面留一行真实原因，不改本地展示口径。
     */
    fun setCurrency(currency: String) {
        val status = repository.status.value
        if (!status.running) {
            _currencyError.value = "网关未启动：启动后才能切换计价币种"
            return
        }
        viewModelScope.launch {
            try {
                val updated = withContext(Dispatchers.IO) { detailClient.saveCurrency(status, currency) }
                _currencyError.value = null
                currencyTick.value++
                if (updated.currency.isBlank()) {
                    _currencyError.value = "服务端未返回币种，已按原样展示"
                }
            } catch (e: ManagementApiException) {
                _currencyError.value = "切换币种失败：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                _currencyError.value = "切换币种失败，请重试"
            }
        }
    }

    fun refresh() {
        refresh.value++
        currencyTick.value++
    }
}
