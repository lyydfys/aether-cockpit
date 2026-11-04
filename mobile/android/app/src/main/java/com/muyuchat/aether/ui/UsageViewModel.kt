package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PricingCatalog
import com.muyuchat.aether.runtime.CheckinVendor
import com.muyuchat.aether.runtime.QuotaHistoryAccount
import com.muyuchat.aether.runtime.QuotaHoldWindow
import com.muyuchat.aether.runtime.UsageApiClient
import com.muyuchat.aether.runtime.UsageDetailApiClient
import com.muyuchat.aether.runtime.UsageHeatmap
import com.muyuchat.aether.runtime.UsageSummary
import com.muyuchat.aether.runtime.VendorCheckinResult
import com.muyuchat.aether.runtime.quotaCycleRange
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
        loadQuotaHistory()
    }

    /* ---------------- 厂商级「立即签到」 ---------------- */

    private val _vendorCheckin = MutableStateFlow<VendorCheckinUiState>(VendorCheckinUiState.Idle)
    val vendorCheckin: StateFlow<VendorCheckinUiState> = _vendorCheckin.asStateFlow()

    /**
     * 打一次厂商级签到（`POST /api/usage/{vendor}-checkin`）。
     *
     * 会打到该厂商下**所有**可用账号（`vendorProviders`），所以页面要先二次确认。
     * 结果按账号逐条回来；今天已确定的账号服务端直接返回旧结果，不会重复打上游。
     */
    fun checkinVendor(vendor: CheckinVendor) {
        val status = repository.status.value
        if (!status.running) {
            _vendorCheckin.value = VendorCheckinUiState.Failed(vendor, "网关未启动")
            return
        }
        if (_vendorCheckin.value is VendorCheckinUiState.Running) {
            return
        }
        _vendorCheckin.value = VendorCheckinUiState.Running(vendor)
        viewModelScope.launch {
            _vendorCheckin.value = try {
                VendorCheckinUiState.Ready(
                    vendor = vendor,
                    results = withContext(Dispatchers.IO) { detailClient.checkinVendor(status, vendor) },
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                VendorCheckinUiState.Failed(vendor, e.message)
            } catch (_: Exception) {
                VendorCheckinUiState.Failed(vendor, "签到失败，请重试")
            }
        }
    }

    fun dismissVendorCheckin() {
        if (_vendorCheckin.value !is VendorCheckinUiState.Running) {
            _vendorCheckin.value = VendorCheckinUiState.Idle
        }
    }

    /* ---------------- 额度历史 + 周期估算（holds） ---------------- */

    private val _quotaHistory = MutableStateFlow<QuotaHistoryUiState>(QuotaHistoryUiState.Idle)
    val quotaHistory: StateFlow<QuotaHistoryUiState> = _quotaHistory.asStateFlow()

    private val _quotaHolds = MutableStateFlow<Map<String, QuotaHoldWindow>>(emptyMap())
    val quotaHolds: StateFlow<Map<String, QuotaHoldWindow>> = _quotaHolds.asStateFlow()

    /**
     * 读一次额度历史（`GET /api/usage/quotas/history?days=45`）。
     *
     * 历史是**独立的一次只读请求**，失败不影响上面的用量汇总——读不到就是读不到，
     * 状态留在 [QuotaHistoryUiState.Failed] 里由卡片自己说。
     */
    fun loadQuotaHistory(days: Int = 45) {
        val status = repository.status.value
        if (!status.running) {
            _quotaHistory.value = QuotaHistoryUiState.Idle
            return
        }
        _quotaHistory.value = QuotaHistoryUiState.Loading
        viewModelScope.launch {
            _quotaHistory.value = try {
                val accounts = withContext(Dispatchers.IO) { detailClient.fetchQuotaHistory(status, days) }
                QuotaHistoryUiState.Ready(accounts)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                QuotaHistoryUiState.Failed(e.message)
            } catch (_: Exception) {
                QuotaHistoryUiState.Failed("额度历史读取失败，请重试")
            }
            // 曲线到手后再逐个窗口要"周期估算"——没有读数就没有可估算的周期，所以串行。
            loadQuotaHolds()
        }
    }

    /**
     * 对有 `start`/`resetsAt` 的曲线逐个调 holds（`/api/usage/quotas/history/holds`）。
     *
     * 只有推得出周期（[quotaCycleRange]）的窗口才发请求：推不出来就说明厂商没给窗口起止，
     * 打过去也只会拿到空数组，白费一次网络。
     */
    private fun loadQuotaHolds() {
        val status = repository.status.value
        val ready = _quotaHistory.value as? QuotaHistoryUiState.Ready ?: return
        if (!status.running) {
            _quotaHolds.value = emptyMap()
            return
        }
        viewModelScope.launch {
            val result = linkedMapOf<String, QuotaHoldWindow>()
            for (account in ready.accounts) {
                for (line in account.lines) {
                    val range = quotaCycleRange(line.points) ?: continue
                    val window = runCatching {
                        withContext(Dispatchers.IO) {
                            detailClient.fetchQuotaHolds(
                                status = status,
                                provider = account.provider,
                                user = account.user,
                                from = range.first,
                                to = range.second,
                            )
                        }
                    }.getOrNull()?.firstOrNull()?.windows?.firstOrNull { it.name == line.name } ?: continue
                    result[quotaHoldKey(account.provider, account.user, line.name)] = window
                }
            }
            _quotaHolds.value = result
        }
    }
}

/** holds 缓存的键：provider|user|窗口名（同一账号可能有多条窗口曲线）。 */
internal fun quotaHoldKey(provider: String, user: String, window: String): String =
    "${provider.trim()}|${user.trim()}|${window.trim()}"

/** 额度历史的页面状态。 */
sealed interface QuotaHistoryUiState {
    data object Idle : QuotaHistoryUiState
    data object Loading : QuotaHistoryUiState
    data class Ready(val accounts: List<QuotaHistoryAccount>) : QuotaHistoryUiState
    data class Failed(val message: String) : QuotaHistoryUiState
}

/** 厂商级签到的页面状态（一次只跑一个厂商）。 */
sealed interface VendorCheckinUiState {
    data object Idle : VendorCheckinUiState
    data class Running(val vendor: CheckinVendor) : VendorCheckinUiState
    data class Ready(val vendor: CheckinVendor, val results: List<VendorCheckinResult>) : VendorCheckinUiState
    data class Failed(val vendor: CheckinVendor, val message: String) : VendorCheckinUiState
}
