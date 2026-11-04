package com.muyuchat.aether.ui

import android.app.Application
import android.content.ContentResolver
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.ACCOUNT_BUNDLE_MAX_BYTES
import com.muyuchat.aether.runtime.AccountBundleException
import com.muyuchat.aether.runtime.AccountBundlePreview
import com.muyuchat.aether.runtime.BundleImportResult
import com.muyuchat.aether.runtime.ChannelProbe
import com.muyuchat.aether.runtime.ChannelToggleApiClient
import com.muyuchat.aether.runtime.ChannelProbeApiClient
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ProviderModels
import com.muyuchat.aether.runtime.ProviderModelsApiClient
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.parseAccountBundle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
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
import java.io.ByteArrayOutputStream
import java.io.IOException

sealed interface ChannelsUiState {
    data object Idle : ChannelsUiState
    data object Loading : ChannelsUiState
    data class Ready(val providers: List<ProviderSummary>) : ChannelsUiState
    data class Failed(val code: String, val message: String) : ChannelsUiState
}

sealed interface ChannelsAction {
    data object Refresh : ChannelsAction
}

/** Local-only bundle preview; nothing is written to the account pool. */
sealed interface BundlePreviewUiState {
    data object Idle : BundlePreviewUiState
    data object Working : BundlePreviewUiState
    data class Ready(val preview: AccountBundlePreview) : BundlePreviewUiState
    data class Failed(val code: String, val message: String) : BundlePreviewUiState
}

/** Explicit import action; writes real accounts through the local gateway. */
sealed interface BundleImportUiState {
    data object Idle : BundleImportUiState
    data object Working : BundleImportUiState
    data class Done(val result: BundleImportResult) : BundleImportUiState
    data class Failed(val code: String, val message: String) : BundleImportUiState
}

/** Qoder browser login; the gateway polls its own device session. */
sealed interface QoderLoginUiState {
    data object Idle : QoderLoginUiState
    data object Starting : QoderLoginUiState
    data class Waiting(val id: String, val loginUrl: String, val expiresIn: Int) : QoderLoginUiState
    data class Done(val account: String) : QoderLoginUiState
    data class Failed(val code: String, val message: String) : QoderLoginUiState
}

/**
 * Read-only channel projection. The admin session stays inside the status flow
 * used for the request; it never enters [ChannelsUiState].
 */
sealed interface ProviderModelsUiState {
    data object Loading : ProviderModelsUiState
    data class Ready(val models: ProviderModels) : ProviderModelsUiState
    data class Failed(val code: String, val message: String) : ProviderModelsUiState
}

class ChannelsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ManagementApiClient()
    private val refresh = MutableStateFlow(0)

    /**
     * 就地更新的渠道列表：启停成功后服务端已在响应体里回了完整渠道列表，
     * 直接用它可**避免走 Loading**——否则整页刷新会把用户刚点的那一行甩到屏外。
     * 置 null 表示"下次走真实拉取"。
     */
    private val providerOverride = MutableStateFlow<List<ProviderSummary>?>(null)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<ChannelsUiState> =
        combine(repository.status, refresh, providerOverride) { status, _, override -> status to override }
            .flatMapLatest { (status, override) ->
            flow {
                if (!status.running) {
                    emit(ChannelsUiState.Idle)
                    return@flow
                }
                // 有就地结果就直接发 Ready：不发 Loading，列表不被重建，滚动位置得以保留。
                if (override != null) {
                    emit(ChannelsUiState.Ready(override))
                    return@flow
                }
                emit(ChannelsUiState.Loading)
                try {
                    val providers = withContext(Dispatchers.IO) { client.fetchProviders(status) }
                    emit(ChannelsUiState.Ready(providers))
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    emit(ChannelsUiState.Failed(e.code, e.message))
                } catch (_: Exception) {
                    emit(ChannelsUiState.Failed("unknown", "无法读取本机渠道，请重试"))
                }
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), ChannelsUiState.Idle)

    private val _bundlePreview = MutableStateFlow<BundlePreviewUiState>(BundlePreviewUiState.Idle)
    val bundlePreview: StateFlow<BundlePreviewUiState> = _bundlePreview.asStateFlow()
    private val _bundleImport = MutableStateFlow<BundleImportUiState>(BundleImportUiState.Idle)
    val bundleImport: StateFlow<BundleImportUiState> = _bundleImport.asStateFlow()
    private var bundleJob: Job? = null
    private var bundleRequest = 0
    private var importJob: Job? = null
    private var importRequest = 0
    private var sealedBytes: ByteArray? = null
    private val _qoderLogin = MutableStateFlow<QoderLoginUiState>(QoderLoginUiState.Idle)
    val qoderLogin: StateFlow<QoderLoginUiState> = _qoderLogin.asStateFlow()
    private var qoderJob: Job? = null
    private var qoderRequest = 0

    // —— 渠道连通性探测：服务端探、手机端只发起与读结果 ——
    private val probeClient = ChannelProbeApiClient()
    private val _probes = MutableStateFlow<Map<String, ChannelProbe>>(emptyMap())
    val probes: StateFlow<Map<String, ChannelProbe>> = _probes.asStateFlow()
    private val _probing = MutableStateFlow<String?>(null)
    val probing: StateFlow<String?> = _probing.asStateFlow()
    private var probeJob: Job? = null

    /** 只读上次探测结果；不主动触发（避免一进页面就打上游）。 */
    fun refreshProbes() {
        val status = repository.status.value
        if (!status.running) {
            _probes.value = emptyMap()
            return
        }
        viewModelScope.launch {
            val report = runCatching {
                withContext(Dispatchers.IO) { probeClient.fetchStatus(status) }
            }.getOrNull() ?: return@launch
            _probes.value = report.byProvider().mapValues { it.value.first() }
        }
    }

    /**
     * 对某个渠道发起一次探测。
     *
     * 服务端是**异步**的（回 `{reading:true}`，结果走 status 轮询），所以这里先发起、
     * 再按 1.2 秒轮询直到 `reading=false`；超时也如实停在"仍在探测"，不假装完成。
     */
    fun testProvider(providerId: String) {
        if (providerId.isBlank() || _probing.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            return
        }
        _probing.value = providerId
        probeJob?.cancel()
        probeJob = viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { probeClient.startCheck(status, providerId) }
                var waited = 0
                while (waited < 24) {
                    delay(1_200)
                    waited++
                    val report = runCatching {
                        withContext(Dispatchers.IO) { probeClient.fetchStatus(status) }
                    }.getOrNull() ?: continue
                    _probes.value = report.byProvider().mapValues { it.value.first() }
                    if (!report.reading) {
                        break
                    }
                }
            } finally {
                _probing.value = null
            }
        }
    }

    // ——— 渠道启停：写操作，必须二次确认；成功由服务端返回确认，不乐观更新 ———
    private val toggleClient = ChannelToggleApiClient()
    private val _toggling = MutableStateFlow<String?>(null)
    val toggling: StateFlow<String?> = _toggling.asStateFlow()
    private val _toggleError = MutableStateFlow<Pair<String, String>?>(null)
    val toggleError: StateFlow<Pair<String, String>?> = _toggleError.asStateFlow()

    fun dismissToggleError() {
        _toggleError.value = null
    }

    fun toggleProvider(providerId: String, enable: Boolean) {
        if (providerId.isBlank() || _toggling.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _toggleError.value = "gateway_not_running" to "网关未启动"
            return
        }
        _toggling.value = providerId
        _toggleError.value = null
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { toggleClient.toggle(status, providerId, enable) }
                // 只有服务端确认后才算成功。列表优先用响应体里的完整渠道列表就地更新；
                // 拿不到（解析失败）才回退到整页刷新。
                val providers = result.providers
                if (providers != null) {
                    providerOverride.value = providers
                } else {
                    onAction(ChannelsAction.Refresh)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _toggleError.value = e.code to e.message
            } catch (_: Exception) {
                _toggleError.value = "unknown" to "切换渠道失败，请重试"
            } finally {
                _toggling.value = null
            }
        }
    }
    // ——— 渠道模型清单：只发 id，不带 key；服务端没有 vendor 端点时退回内置清单 ———
    private val modelsClient = ProviderModelsApiClient()
    private val _models = MutableStateFlow<Map<String, ProviderModelsUiState>>(emptyMap())
    val models: StateFlow<Map<String, ProviderModelsUiState>> = _models.asStateFlow()

    fun loadModels(providerId: String) {
        if (providerId.isBlank()) {
            return
        }
        if (_models.value[providerId] is ProviderModelsUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _models.value = _models.value + (providerId to ProviderModelsUiState.Failed("gateway_not_running", "网关未启动"))
            return
        }
        _models.value = _models.value + (providerId to ProviderModelsUiState.Loading)
        viewModelScope.launch {
            val next = try {
                val result = withContext(Dispatchers.IO) { modelsClient.fetchModels(status, providerId) }
                ProviderModelsUiState.Ready(result)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                ProviderModelsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                ProviderModelsUiState.Failed("unknown", "读取模型清单失败，请重试")
            }
            _models.value = _models.value + (providerId to next)
        }
    }

    fun dismissModels(providerId: String) {
        _models.value = _models.value - providerId
    }
    fun onAction(action: ChannelsAction) {
        when (action) {
            ChannelsAction.Refresh -> {
                providerOverride.value = null
                refresh.value++
            }
        }
    }

    fun previewAccountBundle(uri: Uri, passphrase: String) {
        val request = ++bundleRequest
        bundleJob?.cancel()
        importJob?.cancel()
        importRequest++
        sealedBytes?.fill(0)
        sealedBytes = null
        _bundleImport.value = BundleImportUiState.Idle
        _bundlePreview.value = BundlePreviewUiState.Working
        bundleJob = viewModelScope.launch {
            val outcome = withContext(Dispatchers.IO) {
                runCatching {
                    val bytes = getApplication<Application>().contentResolver.readBundleBytes(uri)
                    bytes to parseAccountBundle(bytes, passphrase)
                }
            }
            if (request != bundleRequest) {
                outcome.getOrNull()?.first?.fill(0)
                return@launch
            }
            outcome.fold(
                onSuccess = { (bytes, preview) ->
                    sealedBytes = bytes
                    _bundlePreview.value = BundlePreviewUiState.Ready(preview)
                },
                onFailure = { error ->
                    if (error is CancellationException) {
                        throw error
                    }
                    _bundlePreview.value = when (error) {
                        is AccountBundleException -> BundlePreviewUiState.Failed(error.code, error.message)
                        else -> BundlePreviewUiState.Failed("file_unreadable", "无法读取所选文件")
                    }
                },
            )
        }
    }

    fun importAccountBundle(passphrase: String) {
        val sealed = sealedBytes
        if (sealed == null) {
            _bundleImport.value = BundleImportUiState.Failed("no_bundle", "请先选择并预览账号包")
            return
        }
        if (_bundleImport.value is BundleImportUiState.Working) {
            return
        }
        val request = ++importRequest
        val status = repository.status.value
        _bundleImport.value = BundleImportUiState.Working
        importJob = viewModelScope.launch {
            val outcome = withContext(Dispatchers.IO) {
                runCatching { client.importAccountBundle(status, sealed, passphrase) }
            }
            if (request != importRequest) {
                return@launch
            }
            outcome.fold(
                onSuccess = { result ->
                    sealedBytes?.fill(0)
                    sealedBytes = null
                    _bundleImport.value = BundleImportUiState.Done(result)
                    refresh.value++
                },
                onFailure = { error ->
                    if (error is CancellationException) {
                        throw error
                    }
                    _bundleImport.value = when (error) {
                        is ManagementApiException -> BundleImportUiState.Failed(error.code, error.message)
                        else -> BundleImportUiState.Failed("unknown", "导入账号包失败，请重试")
                    }
                },
            )
        }
    }

    fun startQoderLogin() {
        val current = _qoderLogin.value
        if (current is QoderLoginUiState.Starting || current is QoderLoginUiState.Waiting) {
            return
        }
        val status = repository.status.value
        val request = ++qoderRequest
        qoderJob?.cancel()
        _qoderLogin.value = QoderLoginUiState.Starting
        qoderJob = viewModelScope.launch {
            val started = withContext(Dispatchers.IO) {
                runCatching { client.startQoderLogin(status) }
            }
            if (request != qoderRequest) {
                return@launch
            }
            val failure = started.exceptionOrNull()
            if (failure is CancellationException) {
                throw failure
            }
            val ticket = started.getOrNull()
            if (ticket == null) {
                _qoderLogin.value = qoderFailure(failure)
                return@launch
            }
            if (ticket.state != "pending" && ticket.state != "done") {
                _qoderLogin.value = QoderLoginUiState.Failed(
                    ticket.state,
                    "登录会话未能启动",
                )
                return@launch
            }
            _qoderLogin.value = QoderLoginUiState.Waiting(ticket.id, ticket.loginUrl, ticket.expiresIn)
            pollQoderLogin(status, ticket.id, ticket.interval, request)
        }
    }

    fun cancelQoderLogin() {
        val waiting = _qoderLogin.value as? QoderLoginUiState.Waiting ?: return
        qoderRequest++
        qoderJob?.cancel()
        qoderJob = null
        _qoderLogin.value = QoderLoginUiState.Idle
        viewModelScope.launch {
            withContext(Dispatchers.IO) {
                runCatching { client.cancelQoderLogin(repository.status.value, waiting.id) }
            }
        }
    }

    fun dismissQoderLogin() {
        qoderRequest++
        qoderJob?.cancel()
        qoderJob = null
        _qoderLogin.value = QoderLoginUiState.Idle
    }

    private suspend fun pollQoderLogin(
        status: GatewayStatus,
        id: String,
        intervalSeconds: Int,
        request: Int,
    ) {
        val deadline = System.currentTimeMillis() + 11 * 60 * 1000L
        var failures = 0
        while (System.currentTimeMillis() < deadline) {
            delay(intervalSeconds.coerceIn(1, 10) * 1000L)
            val outcome = withContext(Dispatchers.IO) {
                runCatching { client.pollQoderLogin(status, id) }
            }
            if (request != qoderRequest) {
                return
            }
            val failure = outcome.exceptionOrNull()
            if (failure is CancellationException) {
                throw failure
            }
            val poll = outcome.getOrNull()
            if (poll == null) {
                failures++
                if (failures >= 5) {
                    _qoderLogin.value = qoderFailure(failure)
                    return
                }
                delay(2_000)
                continue
            }
            failures = 0
            when (poll.state) {
                "pending" -> Unit
                "done" -> {
                    _qoderLogin.value = QoderLoginUiState.Done(poll.account)
                    refresh.value++
                    return
                }
                "failed", "expired" -> {
                    _qoderLogin.value = QoderLoginUiState.Failed(
                        poll.state,
                        poll.error.ifBlank { "登录未完成，请重试" },
                    )
                    return
                }
                else -> {
                    _qoderLogin.value = QoderLoginUiState.Failed("unknown_state", "登录状态异常")
                    return
                }
            }
        }
        _qoderLogin.value = QoderLoginUiState.Failed("login_expired", "登录超时，请重新开始")
    }

    private fun qoderFailure(error: Throwable?): QoderLoginUiState.Failed =
        when (error) {
            is ManagementApiException -> QoderLoginUiState.Failed(error.code, error.message)
            else -> QoderLoginUiState.Failed("unknown", "Qoder 登录失败，请重试")
        }

    fun dismissBundlePreview() {
        bundleRequest++
        importRequest++
        bundleJob?.cancel()
        importJob?.cancel()
        bundleJob = null
        importJob = null
        sealedBytes?.fill(0)
        sealedBytes = null
        _bundleImport.value = BundleImportUiState.Idle
        _bundlePreview.value = BundlePreviewUiState.Idle
    }
}

private fun ContentResolver.readBundleBytes(uri: Uri): ByteArray {
    val stream = try {
        openInputStream(uri)
    } catch (_: IOException) {
        null
    } catch (_: SecurityException) {
        null
    } ?: throw AccountBundleException("file_unreadable", "无法读取所选文件")
    stream.use { input ->
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(8 * 1024)
        while (true) {
            val read = input.read(buffer)
            if (read < 0) {
                break
            }
            out.write(buffer, 0, read)
            if (out.size() > ACCOUNT_BUNDLE_MAX_BYTES) {
                throw AccountBundleException("file_too_large", "账号包超过 8 MB")
            }
        }
        return out.toByteArray()
    }
}

internal fun filterProviders(providers: List<ProviderSummary>, query: String): List<ProviderSummary> {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return providers
    }
    return providers.filter { provider ->
        provider.name.contains(needle, ignoreCase = true) ||
            provider.id.contains(needle, ignoreCase = true) ||
            provider.description.contains(needle, ignoreCase = true) ||
            provider.accounts.any { account ->
                account.id.contains(needle, ignoreCase = true) ||
                    account.label.contains(needle, ignoreCase = true)
            }
    }
}
