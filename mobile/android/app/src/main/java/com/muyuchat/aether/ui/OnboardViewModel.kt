package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.CustomProviderDraft
import com.muyuchat.aether.runtime.CustomProviderProtocol
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.OnboardAuthApiClient
import com.muyuchat.aether.runtime.OnboardAuthSession
import com.muyuchat.aether.runtime.OnboardDeviceSession
import com.muyuchat.aether.runtime.ProviderFormField
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.onboardDeviceKind
import com.muyuchat.aether.runtime.onboardManualSchema
import com.muyuchat.aether.runtime.onboardMethodBlockedBy
import com.muyuchat.aether.runtime.onboardMethodLabel
import com.muyuchat.aether.runtime.onboardMethods
import com.muyuchat.aether.runtime.onboardMissingRequired
import com.muyuchat.aether.runtime.onboardParseTokenInput
import com.muyuchat.aether.runtime.validateCustomProviderDraft
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 接入页的本地状态：搜索词 + 自定义渠道表单。
 *
 * 为什么单独一个 ViewModel：`OnboardPage` 的 providers/accounts 来自 ChannelsViewModel，
 * 而搜索与表单是页面本地的，不该把参数塞进 GatewayHome（写集也不允许改它）。
 *
 * 凭据安全：apiKey 只存在于本 ViewModel 的表单状态与一次请求体里；
 * 不打印、不写剪贴板、不落盘，表单关闭即丢弃。
 */
class OnboardViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ManagementApiClient()

    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    /**
     * 筛选与排序（对标 MCA 模型推荐页的「筛选 · N / 排序」工具行）。
     *
     * 与搜索词分开两个 State：搜索是输入过程，筛选是一次性提交的草稿（面板里点「应用」才生效），
     * 两者叠加但互不覆盖。
     */
    private val _filters = MutableStateFlow(OnboardQuery())
    internal val filters: StateFlow<OnboardQuery> = _filters.asStateFlow()

    private val _draft = MutableStateFlow<CustomProviderDraft?>(null)

    /** null = 表单关闭；非 null = 正在编辑。 */
    val draft: StateFlow<CustomProviderDraft?> = _draft.asStateFlow()

    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()

    private val _formError = MutableStateFlow<String?>(null)
    val formError: StateFlow<String?> = _formError.asStateFlow()

    /** 保存成功的回执，由页面展示一次后清掉。 */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    /**
     * 每个渠道的接入向导（浏览器授权 / 设备码 / Token·JSON / 手动凭据）。
     *
     * 单独一个 controller 而不是把十几个 State 塞进本类：向导有自己的轮询 Job 生命周期，
     * 关弹窗就要停轮询，和搜索/表单这些页面本地状态不是一回事。
     */
    internal val connect: OnboardConnectController = OnboardConnectController(
        viewModelScope = viewModelScope,
        repository = repository,
        client = OnboardAuthApiClient(),
    )

    fun setQuery(value: String) {
        _query.value = value
    }

    /** 应用一次「筛选与排序」面板的草稿结果。 */
    internal fun setFilters(value: OnboardQuery) {
        _filters.value = value
    }

    /** 清空筛选、保留排序（MCA 面板「重置」的口径：重置只清条件，不回退排序）。 */
    internal fun clearFilters() {
        _filters.value = OnboardQuery(sort = _filters.value.sort)
    }

    fun openForm() {
        _formError.value = null
        // 表单第 1 项给默认值：名称默认「自定义渠道」。
        _draft.value = CustomProviderDraft(name = "自定义渠道")
    }

    fun dismissForm() {
        _draft.value = null
        _formError.value = null
    }

    fun updateName(value: String) = updateDraft { it.copy(name = value) }

    fun updateBaseUrl(value: String) = updateDraft { it.copy(baseUrl = value) }

    fun updateApiKey(value: String) = updateDraft { it.copy(apiKey = value) }

    fun updateModels(value: String) = updateDraft { it.copy(modelsText = value) }

    fun updateProtocol(value: CustomProviderProtocol) = updateDraft {
        it.copy(protocol = value)
    }

    fun dismissNotice() {
        _notice.value = null
    }

    /**
     * 保存自定义渠道。校验失败不出网；失败把服务端原文放进 [formError]，表单保留输入。
     *
     * @param existingIds 渠道列表里已有的 id：与服务端内置渠道重名会被当成「编辑内置渠道」，
     *   本地拦下（表单不提交 id，此参数只用于防御性校验）。
     * @param onSaved 成功后刷新渠道列表（复用 OnboardPage 的 onRefresh）。
     */
    fun save(existingIds: Set<String>, onSaved: () -> Unit) {
        val draft = _draft.value ?: return
        if (_saving.value) {
            return
        }
        val problem = validateCustomProviderDraft(draft, existingIds)
        if (problem != null) {
            _formError.value = problem
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _formError.value = "网关未启动"
            return
        }
        _formError.value = null
        _saving.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.saveCustomProvider(status, draft) }
                _draft.value = null
                _notice.value = "已添加「${draft.name.trim()}」"
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                // 只贴服务端原文，不包一层「保存失败」。
                _formError.value = e.message
            } catch (_: Exception) {
                _formError.value = "保存失败，请重试"
            } finally {
                _saving.value = false
            }
        }
    }

    private fun updateDraft(block: (CustomProviderDraft) -> CustomProviderDraft) {
        _draft.value = _draft.value?.let(block)
    }
}

/* ========================================================================== */
/* 接入向导（渠道卡片点开后）：四类手机端能做的接入方式                        */
/* ========================================================================== */

/** 一个可选的接入方式；[blockedReason] 非空表示手机端做不了，按钮置灰并显示原因。 */
internal data class OnboardMethodOption(
    val id: String,
    val label: String,
    val blockedReason: String? = null,
) {
    val enabled: Boolean get() = blockedReason == null
}

/** 接入向导的全部界面状态。 */
internal data class OnboardConnectState(
    val providerId: String,
    val providerName: String,
    val providerDescription: String = "",
    val methods: List<OnboardMethodOption> = emptyList(),
    val active: String = "manual_input",
    val label: String = "",
    val busy: Boolean = false,
    val statusLine: String = "",
    val auth: OnboardAuthSession? = null,
    val device: OnboardDeviceSession? = null,
    val callbackInput: String = "",
    val tokenInput: String = "",
    val manualSchema: List<ProviderFormField> = emptyList(),
    val manualValues: Map<String, String> = emptyMap(),
    /** Windsurf 邮箱 + 密码登录（`POST /api/provider/windsurf/login`）；密码只在表单与请求体里。 */
    val windsurfEmail: String = "",
    val windsurfPassword: String = "",
    val error: String? = null,
    val finished: Boolean = false,
) {
    val currentMethod: OnboardMethodOption?
        get() = methods.firstOrNull { it.id == active }
}

/**
 * 接入向导的编排：发起 → 轮询 → 入库，全部经本机网关，不依赖桌面。
 *
 * 轮询一律用可取消 Job：关弹窗/切方式/取消授权都会立刻停掉，
 * 不会在后台继续打服务端（桌面 `onboard.js:220` 的 `stop()` 同语义）。
 */
internal class OnboardConnectController(
    private val viewModelScope: kotlinx.coroutines.CoroutineScope,
    private val repository: GatewayRuntimeRepository,
    private val client: OnboardAuthApiClient,
) {
    private val _state = MutableStateFlow<OnboardConnectState?>(null)
    val state: StateFlow<OnboardConnectState?> = _state.asStateFlow()

    private var pollJob: Job? = null

    fun open(provider: ProviderSummary) {
        stopPolling()
        val options = onboardMethods(provider).map { method ->
            OnboardMethodOption(
                id = method,
                label = onboardMethodLabel(method),
                blockedReason = onboardMethodBlockedBy(method, provider),
            )
        }
        val withToken = options + OnboardMethodOption("token_json", onboardMethodLabel("token_json"))
        _state.value = OnboardConnectState(
            providerId = provider.id,
            providerName = provider.name.ifBlank { provider.id },
            providerDescription = provider.description,
            methods = withToken,
            active = withToken.firstOrNull { it.enabled }?.id ?: "token_json",
            manualSchema = onboardManualSchema(provider),
        )
    }

    fun close() {
        stopPolling()
        val current = _state.value ?: return
        current.auth?.takeIf { !it.isTerminal }?.let { session ->
            val status = repository.status.value
            if (status.running) {
                viewModelScope.launch {
                    runCatching { withContext(Dispatchers.IO) { client.cancelAuth(status, session.id) } }
                }
            }
        }
        _state.value = null
    }

    fun selectMethod(method: String) {
        stopPolling()
        val current = _state.value ?: return
        _state.value = current.copy(
            active = method,
            auth = null,
            device = null,
            callbackInput = "",
            tokenInput = "",
            statusLine = "",
            error = null,
            finished = false,
            busy = false,
        )
    }

    fun setLabel(value: String) = mutate { it.copy(label = value) }

    fun setCallbackInput(value: String) = mutate { it.copy(callbackInput = value) }

    fun setTokenInput(value: String) = mutate { it.copy(tokenInput = value) }

    fun setManualValue(key: String, value: String) = mutate {
        it.copy(manualValues = it.manualValues + (key to value))
    }

    fun setWindsurfEmail(value: String) = mutate { it.copy(windsurfEmail = value) }

    fun setWindsurfPassword(value: String) = mutate { it.copy(windsurfPassword = value) }

    fun dismissError() = mutate { it.copy(error = null) }

    /** 浏览器授权 / 通用设备码：`POST /api/auth/start`。 */
    fun startAuth(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val method = current.active
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null, statusLine = "正在发起授权…", finished = false) }
        viewModelScope.launch {
            try {
                val session = withContext(Dispatchers.IO) {
                    client.startAuth(status, current.providerId, method)
                }
                mutate {
                    it.copy(
                        busy = false,
                        auth = session,
                        statusLine = if (session.isSuccess) "授权成功，确认备注名即可入库。" else "等待授权…",
                    )
                }
                if (!session.isSuccess) {
                    startPolling(session.id)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, statusLine = "", error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, statusLine = "", error = "无法发起授权，请重试") }
            }
        }
    }

    /** 浏览器回调不到本机时：粘贴完整回调地址（或裸 code）。 */
    fun submitCallback() {
        val current = _state.value ?: return
        val session = current.auth ?: return
        val value = current.callbackInput.trim()
        if (value.isEmpty()) {
            mutate { it.copy(error = "先粘贴回调地址或 code") }
            return
        }
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                val next = withContext(Dispatchers.IO) {
                    client.submitCallback(status, session.id, value)
                }
                mutate {
                    it.copy(
                        busy = false,
                        auth = next,
                        statusLine = if (next.isSuccess) {
                            "授权成功，确认备注名即可入库。"
                        } else {
                            "尚未成功：${next.error.ifBlank { next.status }}"
                        },
                    )
                }
                if (next.isSuccess) {
                    stopPolling()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "授权失败，请重试") }
            }
        }
    }

    /** 授权成功后把凭证落成账号。 */
    fun commitAuth(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val session = current.auth ?: return
        if (!session.isSuccess) {
            mutate { it.copy(error = "认证尚未成功，当前状态：${session.status}") }
            return
        }
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.commitAuth(status, session.id, current.label.ifBlank { current.providerName })
                }
                mutate { it.copy(busy = false, statusLine = "账号已接入", finished = true) }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "入库失败，请重试") }
            }
        }
    }

    fun cancelAuth() {
        stopPolling()
        val current = _state.value ?: return
        val session = current.auth ?: return
        val status = repository.status.value
        if (status.running) {
            viewModelScope.launch {
                runCatching { withContext(Dispatchers.IO) { client.cancelAuth(status, session.id) } }
            }
        }
        mutate { it.copy(auth = null, statusLine = "已取消", busy = false) }
    }

    /** 渠道各自的登录会话（trae / kiro / zed / codebuddy / workbuddy / minimax）。 */
    fun startDevice(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val kind = onboardDeviceKind(current.active) ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null, statusLine = "正在创建登录会话…", finished = false) }
        viewModelScope.launch {
            try {
                val session = withContext(Dispatchers.IO) {
                    client.startDevice(status, kind, current.providerId)
                }
                mutate { it.copy(busy = false, device = session, statusLine = "等待浏览器登录…") }
                startDevicePolling(kind, session.id, onSaved)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, statusLine = "", error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, statusLine = "", error = "无法开始登录，请重试") }
            }
        }
    }

    fun cancelDevice() {
        stopPolling()
        val current = _state.value ?: return
        val kind = onboardDeviceKind(current.active)
        val session = current.device
        val status = repository.status.value
        if (kind != null && session != null && status.running) {
            viewModelScope.launch {
                runCatching {
                    withContext(Dispatchers.IO) { client.cancelDevice(status, kind, session.id) }
                }
            }
        }
        mutate { it.copy(device = null, statusLine = "已取消", busy = false) }
    }

    /** Token / JSON：一个输入框，纯 token 或完整 JSON 都收（`onboard.js:356-419`）。 */
    fun submitTokenJson(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        val items = try {
            onboardParseTokenInput(current.tokenInput, current.manualSchema)
        } catch (e: ManagementApiException) {
            mutate { it.copy(error = e.message) }
            return
        }
        if (items.isEmpty()) {
            mutate { it.copy(error = "先粘贴 Token 或 JSON") }
            return
        }
        val missing = items.mapIndexedNotNull { index, creds ->
            val gap = onboardMissingRequired(creds, current.manualSchema)
            if (gap.isEmpty()) null else "第 ${index + 1} 项缺少 ${gap.joinToString("、")}"
        }
        if (missing.isNotEmpty()) {
            mutate { it.copy(error = missing.first()) }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            var ok = 0
            val failures = mutableListOf<String>()
            for ((index, creds) in items.withIndex()) {
                val label = current.label.ifBlank { current.providerName } +
                    if (items.size > 1) " #${index + 1}" else ""
                try {
                    withContext(Dispatchers.IO) {
                        client.addAccount(status, current.providerId, label, creds)
                    }
                    ok++
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    failures += "第 ${index + 1} 项：${e.message}"
                } catch (_: Exception) {
                    failures += "第 ${index + 1} 项：接入失败"
                }
            }
            mutate {
                it.copy(
                    busy = false,
                    tokenInput = "",
                    statusLine = if (ok > 0) "已接入 $ok 个账号" else "",
                    error = failures.firstOrNull(),
                    finished = ok > 0,
                )
            }
            if (ok > 0) {
                onSaved()
            }
        }
    }

    /** 手动凭据：按渠道表单填字段，落 `POST /api/accounts`。 */
    fun submitManual(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        val schema = current.manualSchema
        val required = schema.firstOrNull { it.required && current.manualValues[it.key].isNullOrBlank() }
        if (required != null) {
            mutate { it.copy(error = "请填写「${required.label.ifBlank { required.key }}」") }
            return
        }
        val creds = current.manualValues.filterValues { it.isNotBlank() }
        if (creds.isEmpty()) {
            mutate { it.copy(error = "请至少填写一个字段") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.addAccount(
                        status,
                        current.providerId,
                        current.label.ifBlank { current.providerName },
                        creds,
                    )
                }
                mutate {
                    it.copy(busy = false, statusLine = "账号已接入", manualValues = emptyMap(), finished = true)
                }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "接入失败，请重试") }
            }
        }
    }

    /**
     * Windsurf 邮箱 + 密码登录：`POST /api/provider/windsurf/login`。
     *
     * 密码只进请求体：不打印、不落盘、不进剪贴板；成功后清空密码字段并刷新列表。
     */
    fun submitWindsurf(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        if (current.windsurfEmail.isBlank() || current.windsurfPassword.isBlank()) {
            mutate { it.copy(error = "请填写邮箱和密码") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.loginWindsurf(status, current.windsurfEmail, current.windsurfPassword)
                }
                mutate {
                    it.copy(
                        busy = false,
                        statusLine = "Windsurf 账号已接入",
                        windsurfPassword = "",
                        finished = true,
                    )
                }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "登录失败，请重试") }
            }
        }
    }

    /* ---------------- 轮询 ---------------- */

    private fun startPolling(sessionId: String) {
        stopPolling()
        val status = repository.status.value
        val interval = ((_state.value?.auth?.intervalSeconds ?: 0).takeIf { it > 0 } ?: 2) * 1_000L
        pollJob = viewModelScope.launch {
            while (true) {
                delay(interval.coerceAtLeast(1_500L))
                try {
                    val next = withContext(Dispatchers.IO) { client.getAuth(status, sessionId) }
                    if (next.isSuccess) {
                        stopPolling()
                        mutate { it.copy(auth = next, statusLine = "授权成功，确认备注名即可入库。") }
                        return@launch
                    }
                    if (next.isError) {
                        stopPolling()
                        mutate {
                            it.copy(
                                auth = next,
                                statusLine = "",
                                error = "授权失败：${next.error.ifBlank { next.status }}",
                            )
                        }
                        return@launch
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    stopPolling()
                    mutate { it.copy(error = "轮询失败：${e.message}") }
                    return@launch
                }
            }
        }
    }

    private fun startDevicePolling(kind: String, sessionId: String, onSaved: () -> Unit) {
        stopPolling()
        val status = repository.status.value
        val interval = ((_state.value?.device?.intervalSeconds ?: 0).takeIf { it > 0 } ?: 2) * 1_000L
        pollJob = viewModelScope.launch {
            while (true) {
                delay(interval.coerceAtLeast(2_000L))
                try {
                    val poll = withContext(Dispatchers.IO) { client.pollDevice(status, kind, sessionId) }
                    if (poll.isDone) {
                        stopPolling()
                        mutate { it.copy(statusLine = "账号已接入", finished = true, device = null) }
                        onSaved()
                        return@launch
                    }
                    if (poll.isFailed) {
                        stopPolling()
                        mutate { it.copy(statusLine = "", error = "登录失败：${poll.error.ifBlank { poll.state }}") }
                        return@launch
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    stopPolling()
                    mutate { it.copy(error = "轮询失败：${e.message}") }
                    return@launch
                }
            }
        }
    }

    fun stopPolling() {
        pollJob?.cancel()
        pollJob = null
    }

    private fun mutate(block: (OnboardConnectState) -> OnboardConnectState) {
        _state.value = _state.value?.let(block)
    }
}
