package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AgentCliInfo
import com.muyuchat.aether.runtime.AgentsApiClient
import com.muyuchat.aether.runtime.AnonymousProbeReport
import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.CallerKeysApiClient
import com.muyuchat.aether.runtime.ChannelProbeApiClient
import com.muyuchat.aether.runtime.ChannelProbeReport
import com.muyuchat.aether.runtime.ClientEntry
import com.muyuchat.aether.runtime.ClientsApiClient
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ProbeApiClient
import com.muyuchat.aether.runtime.ChannelNetRow
import com.muyuchat.aether.runtime.CodexIdentityReport
import com.muyuchat.aether.runtime.NetDiagReport
import com.muyuchat.aether.runtime.parseNetDiagInput
import com.muyuchat.aether.runtime.QuotaAlertsReport
import com.muyuchat.aether.runtime.SessionProbeReport
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.SettingsSummary
import com.muyuchat.aether.runtime.failedAnonymousReport
import com.muyuchat.aether.runtime.failedSessionReport
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 探针工作区的状态。
 *
 * 对齐桌面 `js/views/gateway.js` 的 8 个区块：额度提醒（无端点，见汇报）、API 服务(:101-114)、
 * 接入片段(:116-120)、调用方密钥(:202-257)、Codex 身份对照（无端点，见汇报）、
 * 渠道探测(:122-127)、版本与探针(:129-134)、本机 Agent 版本(:177-200)。
 */
sealed interface ProbeUiState {
    data object Idle : ProbeUiState
    data object Loading : ProbeUiState

    data class Ready(
        val anonymous: AnonymousProbeReport,
        val session: SessionProbeReport,
        val settings: SettingsSummary?,
        val clients: List<ClientEntry>,
        val clientsAvailable: Boolean,
        val callerKeys: List<CallerKeySummary>,
        val channels: ChannelProbeReport,
        val agents: List<AgentCliInfo>,
        val agentsCheckedAt: Long,
        val sampledAtMs: Long,
        val probingChannels: Boolean = false,
    ) : ProbeUiState

    data class Failed(val code: String, val message: String) : ProbeUiState
}

/**
 * 额度提醒状态（桌面 `gateway.js:86-100`）。
 *
 * `Ready` 里为空列表时桌面**整卡不渲染**，所以页面也按"有提醒才画"处理；
 * 读失败单独成一个状态，原样贴服务端文案，不伪装成"没有提醒"。
 */
sealed interface QuotaAlertsUiState {
    data object Idle : QuotaAlertsUiState
    data object Loading : QuotaAlertsUiState
    data class Ready(val report: QuotaAlertsReport) : QuotaAlertsUiState
    data class Failed(val code: String, val message: String) : QuotaAlertsUiState
}

/** Codex 请求身份对照（桌面 `gateway.js:141-175`，只读观测）。 */
sealed interface CodexIdentityUiState {
    data object Idle : CodexIdentityUiState
    data object Loading : CodexIdentityUiState
    data class Ready(val report: CodexIdentityReport) : CodexIdentityUiState
    data class Failed(val code: String, val message: String) : CodexIdentityUiState
}

/** 渠道网络只读汇总（桌面 `channelnet.js:63-77` 是单渠道抽屉；手机端只读列表，不写）。 */
sealed interface ChannelNetUiState {
    data object Idle : ChannelNetUiState
    data object Loading : ChannelNetUiState
    data class Ready(val rows: List<ChannelNetRow>) : ChannelNetUiState
    data class Failed(val code: String, val message: String) : ChannelNetUiState
}

/**
 * 网络连通诊断（桌面 `channelnet.js:113-129`）。
 *
 * [Failed.local] 区分"本地校验拦下"与"服务端/网络失败"——前者不发请求，也就没有服务端原文。
 */
sealed interface NetDiagUiState {
    data object Idle : NetDiagUiState
    data class Running(val target: String) : NetDiagUiState
    data class Ready(val report: NetDiagReport) : NetDiagUiState
    data class Failed(
        val target: String,
        val code: String,
        val message: String,
        val local: Boolean,
    ) : NetDiagUiState
}

/**
 * 探针工作区的 ViewModel。
 *
 * 与桌面 `gateway.js:22-52` 同策略：各块各自兜底（`Promise.all(... .catch(() => ({})))`），
 * 一个端点/一块数据失败不会让整页变空。只有网关没起来才是整页失败。
 */
class GatewayProbeViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val probeClient = ProbeApiClient()
    private val settingsClient = SettingsApiClient()
    private val clientsClient = ClientsApiClient()
    private val callerKeysClient = CallerKeysApiClient()
    private val channelClient = ChannelProbeApiClient()
    private val agentsClient = AgentsApiClient()

    private val _state = MutableStateFlow<ProbeUiState>(ProbeUiState.Idle)
    val state: StateFlow<ProbeUiState> = _state.asStateFlow()

    // —— 额度提醒（桌面 gateway.js:86-100）：与整页探测分开取，失败不影响其它块 ——
    private val _alerts = MutableStateFlow<QuotaAlertsUiState>(QuotaAlertsUiState.Idle)
    val alerts: StateFlow<QuotaAlertsUiState> = _alerts.asStateFlow()

    // —— Codex 请求身份对照（桌面 gateway.js:141-175）：同样独立取，只读观测 ——
    private val _identity = MutableStateFlow<CodexIdentityUiState>(CodexIdentityUiState.Idle)
    val identity: StateFlow<CodexIdentityUiState> = _identity.asStateFlow()

    // —— 渠道网络（桌面 channelnet.js:63-77）：只读汇总，不提供 POST 保存 ——
    private val _channelNet = MutableStateFlow<ChannelNetUiState>(ChannelNetUiState.Idle)
    val channelNet: StateFlow<ChannelNetUiState> = _channelNet.asStateFlow()

    // —— 网络连通诊断（桌面 channelnet.js:113-129）：用户填 host，服务端最长跑 25 秒 ——
    private val _netDiag = MutableStateFlow<NetDiagUiState>(NetDiagUiState.Idle)
    val netDiag: StateFlow<NetDiagUiState> = _netDiag.asStateFlow()

    /** 本地校验 → 发请求；结果留在状态里（页面不清空，方便对比重测）。 */
    fun runNetDiag(rawHost: String) {
        if (_netDiag.value is NetDiagUiState.Running) {
            return
        }
        val input = parseNetDiagInput(rawHost)
        val target = if (input.port == "443") input.host else "${input.host}:${input.port}"
        if (input.error.isNotEmpty()) {
            // 本地拦下：不发请求，直接把原因摆出来
            _netDiag.value = NetDiagUiState.Failed(target, "invalid_input", input.error, local = true)
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _netDiag.value = NetDiagUiState.Failed(target, "gateway_not_running", "网关未启动", local = false)
            return
        }
        _netDiag.value = NetDiagUiState.Running(target)
        viewModelScope.launch {
            _netDiag.value = try {
                val report = withContext(Dispatchers.IO) {
                    probeClient.fetchNetDiag(status, input.host, input.port)
                }
                NetDiagUiState.Ready(report)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                NetDiagUiState.Failed(target, e.code, e.message, local = false)
            } catch (_: Exception) {
                NetDiagUiState.Failed(target, "unknown", "网络诊断失败，请重试", local = false)
            }
        }
    }

    private fun loadChannelNet(status: GatewayStatus) {
        viewModelScope.launch {
            _channelNet.value = ChannelNetUiState.Loading
            _channelNet.value = try {
                val rows = withContext(Dispatchers.IO) { probeClient.fetchChannelNet(status) }
                ChannelNetUiState.Ready(rows)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                ChannelNetUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                ChannelNetUiState.Failed("unknown", "读取渠道网络失败，请重试")
            }
        }
    }

    fun refreshIdentity() {
        val status = repository.status.value
        if (!status.running) {
            _identity.value = CodexIdentityUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        loadIdentity(status)
    }

    private fun loadIdentity(status: GatewayStatus) {
        viewModelScope.launch {
            _identity.value = CodexIdentityUiState.Loading
            _identity.value = try {
                val report = withContext(Dispatchers.IO) { probeClient.fetchCodexIdentity(status) }
                CodexIdentityUiState.Ready(report)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                CodexIdentityUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                CodexIdentityUiState.Failed("unknown", "读取 Codex 身份对照失败，请重试")
            }
        }
    }

    private fun loadQuotaAlerts(status: GatewayStatus) {
        viewModelScope.launch {
            _alerts.value = QuotaAlertsUiState.Loading
            _alerts.value = try {
                val report = withContext(Dispatchers.IO) { probeClient.fetchQuotaAlerts(status) }
                QuotaAlertsUiState.Ready(report)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                QuotaAlertsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                QuotaAlertsUiState.Failed("unknown", "读取额度提醒失败，请重试")
            }
        }
    }

    fun refresh() {
        load(userInitiated = true)
    }

    private fun load(userInitiated: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _state.value = ProbeUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        val current = _state.value
        if (current is ProbeUiState.Ready && !userInitiated) {
            return
        }
        if (current !is ProbeUiState.Ready) {
            _state.value = ProbeUiState.Loading
        }
        loadQuotaAlerts(status)
        loadIdentity(status)
        loadChannelNet(status)
        viewModelScope.launch {
            _state.value = withContext(Dispatchers.IO) { collect(status, current) }
        }
    }

    /** 各块独立取，失败就留空/留旧值，绝不让一块拖垮整页。 */
    private fun collect(status: GatewayStatus, previous: ProbeUiState): ProbeUiState {
        val before = previous as? ProbeUiState.Ready
        val anonymous = try {
            probeClient.fetchAnonymous(status)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failedAnonymousReport(e)
        }
        val session = try {
            probeClient.fetchSession(status)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failedSessionReport(e)
        }
        val settings = runCatching { settingsClient.fetchSettings(status) }.getOrNull() ?: before?.settings
        val clients = runCatching { clientsClient.fetchClients(status) }.getOrNull()
        val keys = runCatching { callerKeysClient.fetch(status) }.getOrNull()?.keys ?: before?.callerKeys.orEmpty()
        val channels = runCatching { channelClient.fetchStatus(status) }.getOrNull()
            ?: before?.channels
            ?: ChannelProbeReport(entries = emptyList(), reading = false, checkedAt = 0L)
        val agents = runCatching { agentsClient.fetchCli(status) }.getOrNull()
        return ProbeUiState.Ready(
            anonymous = anonymous,
            session = session,
            settings = settings,
            clients = clients?.clients ?: before?.clients.orEmpty(),
            clientsAvailable = clients?.available ?: before?.clientsAvailable ?: false,
            callerKeys = keys,
            channels = channels,
            agents = agents?.agents ?: before?.agents.orEmpty(),
            agentsCheckedAt = agents?.checkedAtSec ?: before?.agentsCheckedAt ?: 0L,
            sampledAtMs = System.currentTimeMillis(),
            probingChannels = before?.probingChannels ?: false,
        )
    }

    /**
     * 「全部探测」（桌面 `gateway.js:303-317`）：POST /api/channels/check 之后轮询
     * /api/channels/status，直到 `reading` 变假；最多等 40×800ms，与桌面同上限。
     */
    fun probeChannels() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = ProbeUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        val current = _state.value as? ProbeUiState.Ready ?: return
        if (current.probingChannels) {
            return
        }
        _state.value = current.copy(probingChannels = true)
        viewModelScope.launch {
            var latest = current
            try {
                withContext(Dispatchers.IO) { channelClient.startCheck(status, "") }
                for (round in 0 until 40) {
                    delay(800)
                    val snapshot = runCatching {
                        withContext(Dispatchers.IO) { channelClient.fetchStatus(status) }
                    }.getOrNull() ?: break
                    latest = latest.copy(channels = snapshot)
                    _state.value = latest.copy(probingChannels = !snapshot.reading)
                    if (!snapshot.reading) {
                        break
                    }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // 探测失败不把页面打回 Failed：其它块仍然有效，只是没有新结果。
            } finally {
                val settle = (_state.value as? ProbeUiState.Ready) ?: latest
                _state.value = settle.copy(probingChannels = false)
            }
        }
    }
}
