package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.TotpApiClient
import com.muyuchat.aether.runtime.TotpCode
import com.muyuchat.aether.runtime.TotpEntry
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** 2FA 页面状态。条目与验证码分开保存：拿不到码不能把条目也藏起来。 */
sealed interface TotpUiState {
    data object Idle : TotpUiState

    data object Loading : TotpUiState

    data class Ready(
        val entries: List<TotpEntry>,
        val codes: List<TotpCode>,
        val sampledAtMs: Long,
    ) : TotpUiState

    data class Failed(val code: String, val message: String) : TotpUiState
}

/**
 * 2FA（TOTP）页面状态持有者。
 *
 * 页面不自己算 TOTP：验证码只由 Go 侧本机计算后下发，Remaining 也以服务端为准，
 * 这样「下一秒变码」的边界只有一个实现。页面只负责按固定间隔重新拉取。
 *
 * 明文 seed 只在 [add] 的入参里出现一次，永不进入任何 StateFlow、日志或错误文案。
 */
class TotpViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = TotpApiClient()

    private val _state = MutableStateFlow<TotpUiState>(TotpUiState.Idle)
    val state: StateFlow<TotpUiState> = _state.asStateFlow()

    private val _adding = MutableStateFlow(false)
    val adding: StateFlow<Boolean> = _adding.asStateFlow()
    private val _addError = MutableStateFlow<String?>(null)
    val addError: StateFlow<String?> = _addError.asStateFlow()
    private val _deleting = MutableStateFlow<String?>(null)
    val deleting: StateFlow<String?> = _deleting.asStateFlow()
    private val _actionError = MutableStateFlow<String?>(null)
    val actionError: StateFlow<String?> = _actionError.asStateFlow()

    private var loadJob: Job? = null

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            loadJob?.cancel()
            _state.value = TotpUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        loadJob?.cancel()
        if (_state.value !is TotpUiState.Ready) {
            _state.value = TotpUiState.Loading
        }
        loadJob = viewModelScope.launch {
            val next = try {
                val entries = withContext(Dispatchers.IO) { client.fetchEntries(status) }
                val codes = withContext(Dispatchers.IO) { client.fetchCodes(status) }
                TotpUiState.Ready(entries, codes, System.currentTimeMillis())
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                TotpUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                TotpUiState.Failed("unknown", "读取 2FA 条目失败，请重试")
            }
            _state.value = next
        }
    }

    fun dismissAddError() {
        _addError.value = null
    }

    fun dismissActionError() {
        _actionError.value = null
    }

    /**
     * 新增一条。成功与否只看服务端回读：失败把服务端原文放进 [addError]，不动列表。
     *
     * [TotpAddRequest] 带桌面的位数 / 周期 / 算法三个字段（`twofa.js:106-108`）；
     * 服务端 `POST /api/2fa` 接受 digits 4-10、period 5-300、SHA1/SHA256/SHA512。
     */
    internal fun add(input: TotpAddRequest) {
        if (_adding.value) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _addError.value = "网关未启动"
            return
        }
        if (input.secret.isBlank()) {
            _addError.value = "密钥不能为空"
            return
        }
        _addError.value = null
        _adding.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.addEntry(
                        status = status,
                        issuer = input.issuer,
                        account = input.account,
                        note = input.note,
                        secret = input.secret,
                        digits = input.digits,
                        period = input.period,
                        algorithm = input.algorithm,
                    )
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _addError.value = e.message
            } catch (_: Exception) {
                _addError.value = "添加 2FA 条目失败，请重试"
            } finally {
                _adding.value = false
            }
        }
    }

    fun delete(id: String) {
        if (_deleting.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        _deleting.value = id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.deleteEntry(status, id) }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "删除 2FA 条目失败，请重试"
            } finally {
                _deleting.value = null
            }
        }
    }
}
