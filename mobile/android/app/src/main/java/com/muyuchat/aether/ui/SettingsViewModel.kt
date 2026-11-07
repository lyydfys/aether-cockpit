package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.RedactDetail
import com.muyuchat.aether.runtime.RedactCustomRule
import com.muyuchat.aether.runtime.RedactRule
import com.muyuchat.aether.runtime.mergeRedactRules
import com.muyuchat.aether.runtime.SettingsApiClient
import com.muyuchat.aether.runtime.CallerKeySummary
import com.muyuchat.aether.runtime.CallerKeysApiClient
import com.muyuchat.aether.runtime.effectiveAction
import com.muyuchat.aether.runtime.PreferredPortStore
import com.muyuchat.aether.runtime.validatePreferredPortInput
import com.muyuchat.aether.runtime.validateProxyInput
import com.muyuchat.aether.runtime.validateRedactRules
import com.muyuchat.aether.runtime.SettingsSummary
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
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

sealed interface RedactDetailUiState {
    data object Idle : RedactDetailUiState
    data object Loading : RedactDetailUiState
    data class Ready(
        val detail: RedactDetail,
        val customRules: List<RedactCustomRule> = emptyList(),
    ) : RedactDetailUiState
    data class Failed(val message: String) : RedactDetailUiState
}

sealed interface CallerKeysUiState {
    data object Idle : CallerKeysUiState
    data object Loading : CallerKeysUiState
    data class Ready(val keys: List<CallerKeySummary>) : CallerKeysUiState
    data class Failed(val message: String) : CallerKeysUiState
}

sealed interface SettingsSaveState {
    data object Idle : SettingsSaveState
    data object Saving : SettingsSaveState
    data class Failed(val message: String) : SettingsSaveState
}

sealed interface SettingsUiState {
    data object Idle : SettingsUiState
    data object Loading : SettingsUiState
    data class Ready(val settings: SettingsSummary) : SettingsUiState
    data class Failed(val code: String, val message: String) : SettingsUiState
}

class SettingsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = SettingsApiClient()
    private val callerKeysClient = CallerKeysApiClient()
    private val refresh = MutableStateFlow(0)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<SettingsUiState> = combine(repository.status, refresh) { status, _ -> status }
        .flatMapLatest { status ->
            flow {
                if (!status.running) {
                    emit(SettingsUiState.Idle)
                    return@flow
                }
                emit(SettingsUiState.Loading)
                try {
                    emit(SettingsUiState.Ready(withContext(Dispatchers.IO) { client.fetchSettings(status) }))
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    emit(SettingsUiState.Failed(e.code, e.message))
                } catch (_: Exception) {
                    emit(SettingsUiState.Failed("unknown", "无法读取本机设置，请重试"))
                }
            }
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), SettingsUiState.Idle)

    fun refresh() {
        refresh.value++
    }

    private val _corsSaving = MutableStateFlow(false)
    val corsSaving: StateFlow<Boolean> = _corsSaving.asStateFlow()
    private val _corsError = MutableStateFlow<String?>(null)
    val corsError: StateFlow<String?> = _corsError.asStateFlow()

    fun saveCorsOrigins(origins: List<String>) {
        val status = repository.status.value
        if (!status.running) {
            _corsError.value = "网关未启动"
            return
        }
        _corsError.value = null
        _corsSaving.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.setCorsOrigins(status, origins) }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _corsError.value = e.message
            } catch (_: Exception) {
                _corsError.value = "保存 CORS 来源失败，请重试"
            } finally {
                _corsSaving.value = false
            }
        }
    }

    private val _ipsSaving = MutableStateFlow(false)
    val ipsSaving: StateFlow<Boolean> = _ipsSaving.asStateFlow()
    private val _ipsError = MutableStateFlow<String?>(null)
    val ipsError: StateFlow<String?> = _ipsError.asStateFlow()

    fun saveAllowIPs(ips: List<String>) {
        val status = repository.status.value
        if (!status.running) {
            _ipsError.value = "网关未启动"
            return
        }
        _ipsError.value = null
        _ipsSaving.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.setAllowIPs(status, ips) }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _ipsError.value = e.message
            } catch (_: Exception) {
                _ipsError.value = "保存 IP 白名单失败，请重试"
            } finally {
                _ipsSaving.value = false
            }
        }
    }

    private val _callerKeys = MutableStateFlow<CallerKeysUiState>(CallerKeysUiState.Idle)
    val callerKeys: StateFlow<CallerKeysUiState> = _callerKeys.asStateFlow()
    private val _callerSecret = MutableStateFlow<String?>(null)
    val callerSecret: StateFlow<String?> = _callerSecret.asStateFlow()
    private val _keysError = MutableStateFlow<String?>(null)
    val keysError: StateFlow<String?> = _keysError.asStateFlow()

    fun loadCallerKeys() {
        val status = repository.status.value
        if (!status.running) {
            _callerKeys.value = CallerKeysUiState.Failed("网关未启动")
            return
        }
        _callerKeys.value = CallerKeysUiState.Loading
        viewModelScope.launch {
            _callerKeys.value = try {
                CallerKeysUiState.Ready(withContext(Dispatchers.IO) { callerKeysClient.fetch(status) }.keys)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                CallerKeysUiState.Failed(e.message)
            } catch (_: Exception) {
                CallerKeysUiState.Failed("无法读取调用方密钥，请重试")
            }
        }
    }

    fun createCallerKey(name: String) {
        val status = repository.status.value
        if (!status.running) {
            _keysError.value = "网关未启动"
            return
        }
        _keysError.value = null
        viewModelScope.launch {
            try {
                val payload = withContext(Dispatchers.IO) {
                    callerKeysClient.action(status, "add-key", name = name)
                }
                _callerKeys.value = CallerKeysUiState.Ready(payload.keys)
                if (!payload.secret.isNullOrBlank()) {
                    _callerSecret.value = payload.secret
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _keysError.value = e.message
            } catch (_: Exception) {
                _keysError.value = "创建调用方密钥失败，请重试"
            }
        }
    }

    fun setCallerKeyOff(key: String, off: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _keysError.value = "网关未启动"
            return
        }
        _keysError.value = null
        viewModelScope.launch {
            try {
                val payload = withContext(Dispatchers.IO) {
                    callerKeysClient.action(status, if (off) "off-key" else "on-key", key = key)
                }
                _callerKeys.value = CallerKeysUiState.Ready(payload.keys)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _keysError.value = e.message
            } catch (_: Exception) {
                _keysError.value = "更新调用方密钥失败，请重试"
            }
        }
    }

    fun removeCallerKey(key: String) {
        val status = repository.status.value
        if (!status.running) {
            _keysError.value = "网关未启动"
            return
        }
        _keysError.value = null
        viewModelScope.launch {
            try {
                val payload = withContext(Dispatchers.IO) {
                    callerKeysClient.action(status, "remove", key = key)
                }
                _callerKeys.value = CallerKeysUiState.Ready(payload.keys)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _keysError.value = e.message
            } catch (_: Exception) {
                _keysError.value = "删除调用方密钥失败，请重试"
            }
        }
    }

    fun dismissCallerSecret() {
        _callerSecret.value = null
    }

    private val _proxySaving = MutableStateFlow(false)
    val proxySaving: StateFlow<Boolean> = _proxySaving.asStateFlow()
    private val _proxyError = MutableStateFlow<String?>(null)
    val proxyError: StateFlow<String?> = _proxyError.asStateFlow()

    fun saveProxy(input: String) {
        val validation = validateProxyInput(input)
        if (validation != null) {
            _proxyError.value = validation
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _proxyError.value = "网关未启动"
            return
        }
        val desired = input.trim()
        _proxyError.value = null
        _proxySaving.value = true
        viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.setProxy(status, desired) }
                val ok = if (desired.isEmpty()) readback.isEmpty() else readback.isNotEmpty()
                if (!ok) {
                    _proxyError.value = if (desired.isEmpty()) {
                        "服务端回读仍为已设置代理"
                    } else {
                        "服务端回读为空，代理未保存"
                    }
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _proxyError.value = e.message
            } catch (_: Exception) {
                _proxyError.value = "保存代理失败，请重试"
            } finally {
                _proxySaving.value = false
            }
        }
    }

    private val _lanSaving = MutableStateFlow(false)
    val lanSaving: StateFlow<Boolean> = _lanSaving.asStateFlow()
    private val _lanError = MutableStateFlow<String?>(null)
    val lanError: StateFlow<String?> = _lanError.asStateFlow()

    fun toggleLan(on: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _lanError.value = "网关未启动"
            return
        }
        _lanError.value = null
        _lanSaving.value = true
        viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.setLan(status, on) }
                if (readback != on) {
                    _lanError.value = "服务端回读为${if (readback) "开启" else "关闭"}，与请求不一致"
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _lanError.value = e.message
            } catch (_: Exception) {
                _lanError.value = "保存局域网开关失败，请重试"
            } finally {
                _lanSaving.value = false
            }
        }
    }

    private val _redactDetail = MutableStateFlow<RedactDetailUiState>(RedactDetailUiState.Idle)
    val redactDetail: StateFlow<RedactDetailUiState> = _redactDetail.asStateFlow()
    private val _redactError = MutableStateFlow<String?>(null)
    val redactError: StateFlow<String?> = _redactError.asStateFlow()
    private val _redactSaving = MutableStateFlow(false)
    val redactSaving: StateFlow<Boolean> = _redactSaving.asStateFlow()

    /**
     * 每成功完成一次写入（服务端回读替换了本地列表）就前进一格。
     * 规则编辑器用它判断「这次保存到底成没成」，而不是用 `redactSaving` 的中间态推断——
     * Compose 可能一次都观察不到 `saving=true` 的那一帧，推断会把成功当成没保存完。
     */
    private val _redactWriteVersion = MutableStateFlow(0)
    val redactWriteVersion: StateFlow<Int> = _redactWriteVersion.asStateFlow()
    private var redactJob: Job? = null

    fun openRedactDetail() {
        redactJob?.cancel()
        _redactError.value = null
        val status = repository.status.value
        if (!status.running) {
            _redactDetail.value = RedactDetailUiState.Failed("网关未启动")
            return
        }
        _redactDetail.value = RedactDetailUiState.Loading
        redactJob = viewModelScope.launch {
            _redactDetail.value = fetchRedactDetail(status)
        }
    }

    fun closeRedactDetail() {
        redactJob?.cancel()
        _redactDetail.value = RedactDetailUiState.Idle
        _redactError.value = null
    }

    private suspend fun fetchRedactDetail(
        status: com.muyuchat.aether.runtime.GatewayStatus,
    ): RedactDetailUiState = try {
        RedactDetailUiState.Ready(
            detail = withContext(Dispatchers.IO) { client.fetchRedactDetail(status) },
            customRules = withContext(Dispatchers.IO) { client.fetchCustomRules(status) },
        )
    } catch (e: CancellationException) {
        throw e
    } catch (e: ManagementApiException) {
        RedactDetailUiState.Failed(e.message)
    } catch (_: Exception) {
        RedactDetailUiState.Failed("无法读取脱敏动作，请重试")
    }

    fun cycleRedactAction(rule: RedactRule) {
        if (rejectWhileRedactSaving()) return
        val detail = (_redactDetail.value as? RedactDetailUiState.Ready)?.detail ?: return
        val status = repository.status.value
        if (!status.running) {
            _redactError.value = "网关未启动"
            return
        }
        val next = when (effectiveAction(detail, rule)) {
            "mask" -> "warn"
            "warn" -> "block"
            else -> "mask"
        }
        val updated = detail.actions + (rule.kind to next)
        _redactError.value = null
        redactJob?.cancel()
        redactJob = viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.setRedactActions(status, updated) }
                if (readback[rule.kind] != next) {
                    _redactError.value = "回读动作与请求不一致"
                } else {
                    _redactDetail.value = fetchRedactDetail(status)
                    refresh()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _redactError.value = e.message
            } catch (_: Exception) {
                _redactError.value = "保存脱敏动作失败，请重试"
            }
        }
    }

    /** 新增/删除走整体替换；只有服务端回读成功才替换本地列表。 */
    fun addRedactRule(rule: RedactCustomRule) {
        if (rejectWhileRedactSaving()) return
        val ready = (_redactDetail.value as? RedactDetailUiState.Ready) ?: return
        // 实际提交的是整表（已有规则 + 新规则），所以必须按整表校验，
        // 否则第 101 条会在本地放行、到服务端才被「规则最多 100 条」打回。
        validateRedactRules(ready.customRules + rule)?.let {
            _redactError.value = it
            return
        }
        saveRedactRuleList(ready.customRules + rule)
    }

    fun deleteRedactRule(at: Int) {
        if (rejectWhileRedactSaving()) return
        val ready = (_redactDetail.value as? RedactDetailUiState.Ready) ?: return
        if (at !in ready.customRules.indices) return
        saveRedactRuleList(ready.customRules.filterIndexed { index, _ -> index != at })
    }

    /** 编辑只改第 at 条，was 取当前界面值；服务端不一致会返回 409 并要求刷新。 */
    fun editRedactRule(at: Int, rule: RedactCustomRule) {
        if (rejectWhileRedactSaving()) return
        val ready = (_redactDetail.value as? RedactDetailUiState.Ready) ?: return
        val was = ready.customRules.getOrNull(at) ?: return
        val next = ready.customRules.toMutableList().also { it[at] = rule }
        // 单条编辑同样是整表落盘，先按服务端口径整体校验一次（序号也是整表序号）。
        validateRedactRules(next)?.let {
            _redactError.value = it
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _redactError.value = "网关未启动"
            return
        }
        _redactError.value = null
        _redactSaving.value = true
        redactJob = viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.editRedactRule(status, at, was, rule) }
                val current = (_redactDetail.value as? RedactDetailUiState.Ready)?.customRules ?: emptyList()
                _redactDetail.value = ready.copy(customRules = mergeRedactRules(current, readback, null))
                _redactWriteVersion.value += 1
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _redactError.value = e.message
                if (e.code == "http_409") {
                    _redactError.value = "${e.message}（已刷新）"
                    _redactDetail.value = fetchRedactDetail(status)
                }
            } catch (_: Exception) {
                _redactError.value = "保存自定义规则失败，请重试"
            } finally {
                _redactSaving.value = false
            }
        }
    }

    private fun rejectWhileRedactSaving(): Boolean {
        if (!_redactSaving.value) return false
        _redactError.value = "正在保存，请稍候"
        return true
    }

    private fun saveRedactRuleList(next: List<RedactCustomRule>) {
        val status = repository.status.value
        if (!status.running) {
            _redactError.value = "网关未启动"
            return
        }
        val ready = (_redactDetail.value as? RedactDetailUiState.Ready) ?: return
        _redactError.value = null
        _redactSaving.value = true
        redactJob = viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.replaceRedactRules(status, next) }
                _redactDetail.value = ready.copy(customRules = mergeRedactRules(ready.customRules, readback, null))
                _redactWriteVersion.value += 1
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _redactError.value = e.message
                _redactDetail.value = ready.copy(customRules = mergeRedactRules(ready.customRules, null, e.message))
            } catch (_: Exception) {
                _redactError.value = "保存自定义规则失败，请重试"
                _redactDetail.value = ready
            } finally {
                _redactSaving.value = false
            }
        }
    }

    private val _saveState = MutableStateFlow<SettingsSaveState>(SettingsSaveState.Idle)
    val saveState: StateFlow<SettingsSaveState> = _saveState.asStateFlow()
    private var saveJob: Job? = null

    private val preferredPortStore = PreferredPortStore(getApplication())

    /** 当前生效的首选端口；0 = 每次交给系统分配空闲端口。 */
    private val _preferredPort = MutableStateFlow(runCatching { preferredPortStore.load() }.getOrDefault(0))
    val preferredPort: StateFlow<Int> = _preferredPort.asStateFlow()
    private val _preferredPortSaving = MutableStateFlow(false)
    val preferredPortSaving: StateFlow<Boolean> = _preferredPortSaving.asStateFlow()
    private val _preferredPortError = MutableStateFlow<String?>(null)
    val preferredPortError: StateFlow<String?> = _preferredPortError.asStateFlow()
    private val _preferredPortNotice = MutableStateFlow<String?>(null)
    val preferredPortNotice: StateFlow<String?> = _preferredPortNotice.asStateFlow()

    /**
     * 保存首选端口。**不做运行中热切换**：移动运行时没有 `SetPortSwitcher`，
     * `POST /api/settings/port` 在手机端必然 400。这里只落盘，下次启动由
     * [com.muyuchat.aether.runtime.GatewayServiceController] 交给 `nativeStart`。
     * 因此文案必须说清「下次启动生效」，不能让用户以为已经切了。
     */
    fun savePreferredPort(raw: String) {
        if (_preferredPortSaving.value) {
            _preferredPortError.value = "正在保存，请稍候"
            return
        }
        val input = validatePreferredPortInput(raw)
        if (input.error != null || input.port == null) {
            _preferredPortError.value = input.error ?: "端口格式不正确"
            return
        }
        _preferredPortError.value = null
        _preferredPortNotice.value = null
        _preferredPortSaving.value = true
        viewModelScope.launch {
            try {
                val saved = withContext(Dispatchers.IO) { preferredPortStore.save(input.port) }
                _preferredPort.value = saved
                _preferredPortNotice.value = if (saved == 0) {
                    "已保存：下次启动交给系统分配空闲端口"
                } else {
                    "已保存：下次启动使用端口 $saved（当前运行中的端口不变）"
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _preferredPortError.value = e.message?.take(120) ?: "保存首选端口失败，请重试"
            } finally {
                _preferredPortSaving.value = false
            }
        }
    }

    private val _personalSaving = MutableStateFlow(false)
    val personalSaving: StateFlow<Boolean> = _personalSaving.asStateFlow()
    private val _personalError = MutableStateFlow<String?>(null)
    val personalError: StateFlow<String?> = _personalError.asStateFlow()

    private val _gatewayRestarting = MutableStateFlow(false)
    val gatewayRestarting: StateFlow<Boolean> = _gatewayRestarting.asStateFlow()
    private val _gatewayRestartNotice = MutableStateFlow<String?>(null)
    val gatewayRestartNotice: StateFlow<String?> = _gatewayRestartNotice.asStateFlow()

    /**
     * 重启网关（停一次再起一次，复用局域网切换那条路径）。
     *
     * 出网身份档位只写 `settings.json`，运行中的渠道要等下一次
     * `loadSettings → applyWorkBuddyIdentity` 才换档——所以「保存 + 重启」
     * 才是完整动作。重启失败要如实报出来，不假装已生效。
     */
    fun restartGateway() {
        if (_gatewayRestarting.value) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _gatewayRestartNotice.value = "网关未启动：启动后新档位会自动生效"
            return
        }
        _gatewayRestartNotice.value = null
        _gatewayRestarting.value = true
        viewModelScope.launch {
            try {
                val started = repository.restart()
                _gatewayRestartNotice.value = if (started.running) {
                    "网关已重启（${started.addr}）：出网身份已按设置重新应用"
                } else {
                    "重启失败：${started.errorCode.ifBlank { "unknown" }}，请到概览页手动启动网关"
                }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                _gatewayRestartNotice.value = "重启失败，请到概览页手动启动网关"
            } finally {
                _gatewayRestarting.value = false
                refresh()
            }
            // 提示只活 8 秒：再次进设置页不该看到上一次的「已重启」。
            scheduleRestartNoticeClear()
        }
    }

    private fun scheduleRestartNoticeClear() {
        val current = _gatewayRestartNotice.value ?: return
        viewModelScope.launch {
            delay(8_000)
            if (_gatewayRestartNotice.value == current) {
                _gatewayRestartNotice.value = null
            }
        }
    }

    /**
     * 个人信息脱敏开关：写 `POST /api/settings {"redactPersonal":bool}`，
     * 服务端按 key 合并后经 syncRedactPrefs 落到引擎；回读必须与请求一致，
     * 不一致要报出来，失败保留旧值（不本地改写）。
     */
    fun toggleRedactPersonal(enabled: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _personalError.value = "网关未启动"
            return
        }
        _personalError.value = null
        _personalSaving.value = true
        viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.setRedactPersonal(status, enabled) }
                if (readback != enabled) {
                    _personalError.value = "服务端回读为${if (readback) "开启" else "关闭"}，与请求不一致"
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _personalError.value = e.message
            } catch (_: Exception) {
                _personalError.value = "保存个人信息脱敏开关失败，请重试"
            } finally {
                _personalSaving.value = false
            }
        }
    }

    fun toggleRedact(enabled: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _saveState.value = SettingsSaveState.Failed("网关未启动")
            return
        }
        saveJob?.cancel()
        _saveState.value = SettingsSaveState.Saving
        saveJob = viewModelScope.launch {
            try {
                val readback = withContext(Dispatchers.IO) { client.setRedactEnabled(status, enabled) }
                if (readback != enabled) {
                    _saveState.value = SettingsSaveState.Failed(
                        "服务端回读为${if (readback) "开启" else "关闭"}，与请求不一致",
                    )
                } else {
                    _saveState.value = SettingsSaveState.Idle
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _saveState.value = SettingsSaveState.Failed(e.message)
            } catch (_: Exception) {
                _saveState.value = SettingsSaveState.Failed("保存失败，请重试")
            }
        }
    }
}
