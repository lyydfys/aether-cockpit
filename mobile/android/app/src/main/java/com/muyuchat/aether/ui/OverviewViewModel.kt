package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.ChannelProbeApiClient
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.OverviewExtras
import com.muyuchat.aether.runtime.OverviewLinksState
import com.muyuchat.aether.runtime.OverviewStats
import com.muyuchat.aether.runtime.RequestsApiClient
import com.muyuchat.aether.runtime.SessionStatsApiClient
import com.muyuchat.aether.runtime.UsageApiClient
import com.muyuchat.aether.runtime.UsageDetailApiClient
import com.muyuchat.aether.runtime.latestRequestLine
import com.muyuchat.aether.runtime.latestRequestUnavailableLine
import com.muyuchat.aether.runtime.overviewHealthRows
import com.muyuchat.aether.runtime.overviewLinks
import com.muyuchat.aether.runtime.overviewQuotaRows
import com.muyuchat.aether.runtime.overviewStats
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
import kotlinx.coroutines.withTimeoutOrNull

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

    // —— KPI：用量 / 渠道 / 会话三路分别拉，一侧失败不影响另一侧 ——
    //
    // 关键点：**跟网关状态绑定**，不能只在进页面时拉一次——App 冷启动时概览先渲染，
    // 那时网关还没起，只拉一次会永远停在"读不到"。网关一变成运行中，这里自动重拉。
    private val usageClient = UsageApiClient()
    private val managementClient = ManagementApiClient()
    private val sessionsClient = SessionStatsApiClient()
    private val detailClient = UsageDetailApiClient()
    private val probeClient = ChannelProbeApiClient()
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
            var sessionsError: String? = null
            val usage = runCatching {
                withContext(Dispatchers.IO) { usageClient.fetchUsage(status, "today") }
            }.onFailure { usageError = "读不到用量" }.getOrNull()
            val providers = runCatching {
                withContext(Dispatchers.IO) { managementClient.fetchProviders(status) }
            }.onFailure { accountsError = "读不到账号" }.getOrNull()
            // 会话统计在冷启动/多 WSL 环境下可能很慢：桌面端用 3s 兜底（overview.js:39/61），
            // 这里同样只等 3s，超时按"读不到"处理，不拖住整张 KPI。
            val sessions = withTimeoutOrNull(3_000) {
                runCatching {
                    withContext(Dispatchers.IO) { sessionsClient.fetchStats(status, 7).sessions }
                }.onFailure { sessionsError = "读不到会话" }.getOrNull()
            }
            if (sessions == null) {
                sessionsError = "读不到会话"
            }
            emit(
                overviewStats(
                    usage = usage,
                    providers = providers,
                    sessions = sessions,
                    usageError = usageError,
                    accountsError = accountsError,
                    sessionsError = sessionsError,
                ),
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /**
     * 概览下半部分的两张卡（桌面 `overview.js:172` 的「额度余量 / 渠道健康」）。
     *
     * 与 [stats] 共用 `statsTrigger` / 网关状态：网关一启动就重拉；两卡分别失败，
     * 一侧失败不会把另一侧判成空。
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val extras: StateFlow<OverviewExtras?> =
        combine(repository.status, statsTrigger) { status, tick -> status to tick }
            .flatMapLatest { (status, _) ->
                flow {
                    if (!status.running) {
                        emit(OverviewExtras())
                        return@flow
                    }
                    val providers = runCatching {
                        withContext(Dispatchers.IO) { managementClient.fetchProviders(status) }
                    }.getOrNull()
                    val allowed = providers?.map { it.id }?.toSet() ?: emptySet()
                    val names = providers?.associate { it.id to it.name } ?: emptyMap()

                    var quotaError: String? = null
                    val quotas = runCatching {
                        withContext(Dispatchers.IO) { detailClient.fetchQuotas(status) }
                    }.onFailure { quotaError = it.message ?: "读取失败" }.getOrNull()

                    var healthError: String? = null
                    val probes = runCatching {
                        withContext(Dispatchers.IO) { probeClient.fetchStatus(status) }
                    }.onFailure { healthError = it.message ?: "读取失败" }.getOrNull()

                    emit(
                        OverviewExtras(
                            quotas = quotas?.let { overviewQuotaRows(it, allowed) },
                            quotaError = quotaError,
                            health = probes?.let {
                                overviewHealthRows(
                                    entries = it.entries,
                                    allowedProviders = allowed,
                                    displayName = { id -> names[id] ?: id },
                                )
                            },
                            healthCheckedAt = probes?.checkedAt ?: 0L,
                            healthError = healthError,
                        ),
                    )
                }
            }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /**
     * 三步接入的完成状态 + 聚合链路。
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
     * 「最近一次模型请求」。
     *
     * 注意：桌面概览页**没有**这张卡（`overview.js` 的 4 个区块里没有它，ledger 只喂 KPI 数字），
     * 所以概览页已不再渲染。这里保留数据流是因为 `GatewayHome` 仍在读它——
     * 调用点在写集外；若要彻底删除，需同时改 `GatewayHome.kt` 的 `latestRequest =` 实参。
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

    /**
     * 局域网共享的**用户意图**（持久化值）。真正生效的值看 `status.lanEnabled`：
     * 网关重启后才会真正改监听地址，所以开关位置以意图为准、地址以状态为准。
     */
    val lanSharing: StateFlow<Boolean> = repository.lanSharing

    /** 局域网切换失败原因（重启超时 / 未按预期绑定）；成功后自动清空。 */
    val lanError: StateFlow<String?> = repository.lanError

    /** 切换局域网共享：内部会重启网关并按真实绑定结果回写 [lanSharing]。 */
    fun setLanSharing(enabled: Boolean) {
        viewModelScope.launch { repository.setLanSharing(enabled) }
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
