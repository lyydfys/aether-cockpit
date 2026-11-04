package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.RequestsApiClient
import com.muyuchat.aether.runtime.latestRequestLine
import com.muyuchat.aether.runtime.latestRequestUnavailableLine
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.OverviewLinksState
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.UsageApiClient
import com.muyuchat.aether.runtime.overviewLinks
import com.muyuchat.aether.runtime.overviewStats
import com.muyuchat.aether.runtime.GatewayStatus
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Typed page state for the Gateway overview. */
data class OverviewUiState(
    val status: GatewayStatus = GatewayStatus(),
    val notice: String = "",
)

/** User intents accepted by the overview page. */
sealed interface OverviewAction {
    data object Start : OverviewAction
    data object Stop : OverviewAction
    data object NotificationDenied : OverviewAction
}

/**
 * Route state holder between the Composable and the gateway runtime. The
 * controller remains the only writer of [GatewayStatus]; this ViewModel only
 * combines it with page-local notice state and forwards lifecycle intents.
 */
class OverviewViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val notice = MutableStateFlow("")

    val state: StateFlow<OverviewUiState> = combine(repository.status, notice) { status, message ->
        OverviewUiState(status = status, notice = message)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.Eagerly,
        initialValue = OverviewUiState(status = repository.status.value),
    )

    // —— 统计卡：用量与账号分别拉，一侧失败不影响另一侧 ——
    //
    // 关键点：**跟网关状态绑定**，不能只在进页面时拉一次——App 冷启动时概览先渲染，
    // 那时网关还没起，只拉一次会永远停在"读不到"。网关一变成运行中，这里自动重拉。
    private val usageClient = UsageApiClient()
    private val managementClient = ManagementApiClient()
    private val statsTrigger = MutableStateFlow(0)
    private val requestsClient = RequestsApiClient()

    @OptIn(ExperimentalCoroutinesApi::class)
    val stats: StateFlow<OverviewStats?> = combine(repository.status, statsTrigger) { status, tick ->
        status to tick
    }.flatMapLatest { (status, _) ->
        flow {
            if (!status.running) {
                emit(null)
                return@flow
            }
            var usageError: String? = null
            var accountsError: String? = null
            val usage = runCatching {
                withContext(Dispatchers.IO) { usageClient.fetchUsage(status, "today") }
            }.onFailure { usageError = "读不到用量" }.getOrNull()
            val enabled = runCatching {
                withContext(Dispatchers.IO) {
                    managementClient.fetchProviders(status).sumOf { it.readyCount }
                }
            }.onFailure { accountsError = "读不到账号" }.getOrNull()
            emit(
                overviewStats(
                    usage = usage,
                    enabledAccounts = enabled,
                    usageError = usageError,
                    accountsError = accountsError,
                ),
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /**
     * 概览新增的两块：三步接入的完成状态 + 聚合链路。
     *
     * 与 [stats] 共用 `statsTrigger` / 网关状态：网关一启动就重拉；渠道和用量分别失败，
     * 一侧失败不会把另一侧判成空（[OverviewLinksState.providersLoaded] /
     * [OverviewLinksState.usageLoaded] 分开表达）。
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val links: StateFlow<OverviewLinksState?> =
        combine(repository.status, statsTrigger) { status, tick -> status to tick }
            .flatMapLatest { (status, _) ->
                flow {
                    if (!status.running) {
                        emit(OverviewLinksState())
                        return@flow
                    }
                    val providers = runCatching {
                        withContext(Dispatchers.IO) { managementClient.fetchProviders(status) }
                    }.getOrNull()
                    val usage = runCatching {
                        withContext(Dispatchers.IO) { usageClient.fetchUsage(status, "today") }
                    }.getOrNull()
                    emit(overviewLinks(providers, usage))
                }
            }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /**
     * 「最近一次模型请求」：概览页用它回答"这台手机的网关到底被客户端用过没有"。
     * 与 stats 共用 statsTrigger，刷新时一起更新。
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val latest: StateFlow<String?> = combine(repository.status, statsTrigger) { status, tick -> status to tick }
        .flatMapLatest { (status, _) ->
            flow {
                if (!status.running) {
                    emit(null)
                    return@flow
                }
                val page = runCatching {
                    withContext(Dispatchers.IO) { requestsClient.fetchRequests(status, 0, 1, false) }
                }.getOrNull()
                emit(
                    if (page == null) {
                        latestRequestUnavailableLine()
                    } else {
                        latestRequestLine(page.rows, System.currentTimeMillis())
                    },
                )
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)
    /** 手动重拉（下拉/点刷新时用；不做轮询）。 */
    fun refreshStats() {
        statsTrigger.value++
    }

    fun onAction(action: OverviewAction) {
        when (action) {
            OverviewAction.Start -> {
                notice.value = ""
                repository.start()
            }
            OverviewAction.Stop -> {
                notice.value = ""
                repository.stop()
            }
            OverviewAction.NotificationDenied -> {
                notice.value = "需要通知权限，前台服务才能在系统通知中显示运行状态"
            }
        }
    }
}
