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
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
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
class ChannelsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ManagementApiClient()
    private val refresh = MutableStateFlow(0)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<ChannelsUiState> = combine(repository.status, refresh) { status, _ -> status }
        .flatMapLatest { status ->
            flow {
                if (!status.running) {
                    emit(ChannelsUiState.Idle)
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

    fun onAction(action: ChannelsAction) {
        when (action) {
            ChannelsAction.Refresh -> refresh.value++
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
