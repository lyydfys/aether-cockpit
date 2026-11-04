package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.LibraryApiClient
import com.muyuchat.aether.runtime.LibraryDraft
import com.muyuchat.aether.runtime.LibraryDraftProblems
import com.muyuchat.aether.runtime.LibraryEntry
import com.muyuchat.aether.runtime.LibraryKind
import com.muyuchat.aether.runtime.LibraryMcpAuthApiClient
import com.muyuchat.aether.runtime.LibrarySnapshot
import com.muyuchat.aether.runtime.LibraryWriteApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.SkillCheckReport
import com.muyuchat.aether.runtime.SkillInstallResult
import com.muyuchat.aether.runtime.SkillProbe
import com.muyuchat.aether.runtime.SkillUpdateResult
import com.muyuchat.aether.runtime.skillInstallSummary
import com.muyuchat.aether.runtime.skillUpdateSummary
import com.muyuchat.aether.runtime.validateLibraryDraft
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface LibraryUiState {
    data object Idle : LibraryUiState

    data object Loading : LibraryUiState

    data class Ready(val entries: List<LibraryEntry>, val sampledAtMs: Long) : LibraryUiState

    data class Failed(val code: String, val message: String) : LibraryUiState
}

/**
 * 资源库页状态：只读列表 + 删除。
 *
 * 不做「发现」「下发给 Agent」——那两条在手机上要么扫不到（以 `home` 为根），
 * 要么会写进 `~/.claude.json`，都超出本卡范围。
 */
class LibraryViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = LibraryApiClient()
    private val writeClient = LibraryWriteApiClient()

    private val _state = MutableStateFlow<LibraryUiState>(LibraryUiState.Idle)
    val state: StateFlow<LibraryUiState> = _state.asStateFlow()

    private val _kindFilter = MutableStateFlow<LibraryKind?>(null)
    val kindFilter: StateFlow<LibraryKind?> = _kindFilter.asStateFlow()

    private val _deleting = MutableStateFlow<String?>(null)
    val deleting: StateFlow<String?> = _deleting.asStateFlow()

    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()
    private val _saveProblems = MutableStateFlow<LibraryDraftProblems?>(null)
    val saveProblems: StateFlow<LibraryDraftProblems?> = _saveProblems.asStateFlow()

    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    private val _actionError = MutableStateFlow<String?>(null)
    val actionError: StateFlow<String?> = _actionError.asStateFlow()

    private var loadJob: Job? = null

    fun setKindFilter(kind: LibraryKind?) {
        _kindFilter.value = kind
    }

    fun dismissNotice() {
        _notice.value = null
    }

    fun dismissActionError() {
        _actionError.value = null
    }

    /**
     * 新增一条资源（只支持指令集 / 技能 / 子代理）。
     *
     * MCP 在这里不可达：`LibraryKind.MCP` 会被 [LibraryWriteApiClient] 直接拒绝，
     * 因为列表下发的是掩码后的 config，回写等于把 `***` 存成真实值。
     */
    fun create(draft: LibraryDraft, onDone: (Boolean) -> Unit = {}) {
        if (_saving.value) {
            return
        }
        val normalized = draft.normalized()
        val problems = validateLibraryDraft(normalized)
        _saveProblems.value = problems
        if (problems.hasProblem) {
            onDone(false)
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            onDone(false)
            return
        }
        _actionError.value = null
        _notice.value = null
        _saving.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { writeClient.save(status, normalized) }
                _notice.value = "已保存到本机资源库"
                refresh()
                onDone(true)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = if (e.code == "mcp_desktop_only") {
                    "MCP 配置只能在桌面端编辑"
                } else {
                    "保存失败：${e.message}（错误码：${e.code}）"
                }
                onDone(false)
            } catch (_: Exception) {
                _actionError.value = "保存失败，请重试"
                onDone(false)
            } finally {
                _saving.value = false
            }
        }
    }

    fun clearSaveProblems() {
        _saveProblems.value = null
    }

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            loadJob?.cancel()
            _state.value = LibraryUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        loadJob?.cancel()
        if (_state.value !is LibraryUiState.Ready) {
            _state.value = LibraryUiState.Loading
        }
        loadJob = viewModelScope.launch {
            _state.value = try {
                val snapshot: LibrarySnapshot = withContext(Dispatchers.IO) { client.fetchLibrary(status) }
                LibraryUiState.Ready(snapshot.entries, System.currentTimeMillis())
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                LibraryUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                LibraryUiState.Failed("unknown", "读取资源库失败，请重试")
            }
        }
    }

    fun delete(kind: LibraryKind, id: String) {
        if (_deleting.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        _notice.value = null
        _deleting.value = id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.deleteEntry(status, kind, id) }
                _notice.value = "已删除「${kind.label}」条目；删除不可撤销"
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "删除失败，请重试"
            } finally {
                _deleting.value = null
            }
        }
    }

    // ——— MCP 登录 / 登出（桌面 library.js:795 `drawer('登录 ' + it.name)`） ———

    /**
     * 服务端在网关进程里起 `127.0.0.1:<port>/callback` 的回调监听（`mcp_signin.go:336-351`），
     * 而网关就跑在手机本机上，所以系统浏览器授权完回跳回环地址即可命中——这条在手机上真能走通。
     */
    private val mcpAuthClient = LibraryMcpAuthApiClient()
    private val _mcpAuth = MutableStateFlow<McpAuthUiState>(McpAuthUiState.Idle)
    val mcpAuth: StateFlow<McpAuthUiState> = _mcpAuth.asStateFlow()
    private var mcpPollJob: Job? = null

    fun startMcpSignin(name: String) {
        if (name.isBlank() || _mcpAuth.value is McpAuthUiState.Starting) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _mcpAuth.value = McpAuthUiState.Failed(name, "gateway_not_running", "网关未启动")
            return
        }
        mcpPollJob?.cancel()
        _mcpAuth.value = McpAuthUiState.Starting(name)
        viewModelScope.launch {
            try {
                val start = withContext(Dispatchers.IO) { mcpAuthClient.start(status, name) }
                if (start.alreadySignedIn) {
                    _mcpAuth.value = McpAuthUiState.Done(name, alreadySignedIn = true)
                    return@launch
                }
                if (start.id.isBlank() || start.url.isBlank()) {
                    _mcpAuth.value = McpAuthUiState.Failed(name, "invalid_response", "服务端没有给出授权地址")
                    return@launch
                }
                _mcpAuth.value = McpAuthUiState.Waiting(name, start.id, start.url)
                pollMcpSignin(status, start.id, name)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _mcpAuth.value = McpAuthUiState.Failed(name, e.code, e.message)
            } catch (_: Exception) {
                _mcpAuth.value = McpAuthUiState.Failed(name, "unknown", "发起 MCP 登录失败，请重试")
            }
        }
    }

    /** 轮询到 `done` / `failed` / `canceled` 为止；最多等 10 分钟（与服务端 flow 超时一致）。 */
    private suspend fun pollMcpSignin(status: GatewayStatus, id: String, name: String) {
        mcpPollJob?.cancel()
        mcpPollJob = viewModelScope.launch {
            val deadline = System.currentTimeMillis() + 10 * 60 * 1000L
            while (System.currentTimeMillis() < deadline) {
                delay(2_000)
                val poll = try {
                    withContext(Dispatchers.IO) { mcpAuthClient.poll(status, id) }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    _mcpAuth.value = McpAuthUiState.Failed(name, e.code, e.message)
                    return@launch
                } catch (_: Exception) {
                    _mcpAuth.value = McpAuthUiState.Failed(name, "unknown", "查询 MCP 登录状态失败")
                    return@launch
                }
                when (poll.state) {
                    "done" -> {
                        _mcpAuth.value = McpAuthUiState.Done(name, alreadySignedIn = false)
                        refresh()
                        return@launch
                    }
                    "failed" -> {
                        _mcpAuth.value = McpAuthUiState.Failed(
                            name,
                            "signin_failed",
                            poll.error.ifBlank { "登录失败" },
                        )
                        return@launch
                    }
                    "canceled" -> {
                        _mcpAuth.value = McpAuthUiState.Idle
                        return@launch
                    }
                    else -> Unit
                }
            }
            _mcpAuth.value = McpAuthUiState.Failed(name, "timeout", "登录超时（10 分钟），请重试")
        }
    }

    fun cancelMcpSignin() {
        val current = _mcpAuth.value
        mcpPollJob?.cancel()
        if (current !is McpAuthUiState.Waiting) {
            _mcpAuth.value = McpAuthUiState.Idle
            return
        }
        val status = repository.status.value
        _mcpAuth.value = McpAuthUiState.Idle
        if (!status.running) {
            return
        }
        viewModelScope.launch {
            runCatching { withContext(Dispatchers.IO) { mcpAuthClient.cancel(status, current.id) } }
            _notice.value = "已取消「${current.name}」的登录"
        }
    }

    fun signOutMcp(name: String) {
        val status = repository.status.value
        if (!status.running || name.isBlank()) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { mcpAuthClient.signOut(status, name) }
                _notice.value = "已登出「$name」：本机 MCP 令牌已删除"
                _mcpAuth.value = McpAuthUiState.Idle
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "登出失败，请重试"
            }
        }
    }

    fun dismissMcpAuth() {
        mcpPollJob?.cancel()
        _mcpAuth.value = McpAuthUiState.Idle
    }

    // ——— MCP 工具开关（桌面 library.js:490-511） ———

    private val _mcpTools = MutableStateFlow<McpToolsUiState>(McpToolsUiState.Idle)
    val mcpTools: StateFlow<McpToolsUiState> = _mcpTools.asStateFlow()

    /** 读工具清单（桌面「读取工具清单」，`library.js:501-505`）。 */
    fun loadMcpTools(name: String, knownDisabled: List<String>) {
        if (name.isBlank()) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _mcpTools.value = McpToolsUiState.Failed(name, "gateway_not_running", "网关未启动")
            return
        }
        _mcpTools.value = McpToolsUiState.Loading(name)
        viewModelScope.launch {
            try {
                val list = withContext(Dispatchers.IO) { client.fetchMcpTools(status, name) }
                _mcpTools.value = McpToolsUiState.Ready(
                    name = name,
                    tools = list.tools,
                    disabled = knownDisabled.toSet(),
                    why = list.why,
                    detail = list.detail,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _mcpTools.value = McpToolsUiState.Failed(name, e.code, e.message)
            } catch (_: Exception) {
                _mcpTools.value = McpToolsUiState.Failed(name, "unknown", "读取工具清单失败，请重试")
            }
        }
    }

    fun toggleMcpTool(tool: String) {
        val current = _mcpTools.value as? McpToolsUiState.Ready ?: return
        val disabled = if (tool in current.disabled) current.disabled - tool else current.disabled + tool
        _mcpTools.value = current.copy(disabled = disabled)
    }

    /** 保存（桌面「保存工具开关」，`library.js:494-497`）：只写本机库里的 `DisabledTools`。 */
    fun saveMcpTools() {
        val current = _mcpTools.value as? McpToolsUiState.Ready ?: return
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.saveMcpDisabledTools(status, current.name, current.disabled.toList())
                }
                _notice.value = if (current.disabled.isEmpty()) {
                    "已保存「${current.name}」：所有工具都启用"
                } else {
                    "已保存「${current.name}」：停用 ${current.disabled.size} 个工具"
                }
                _mcpTools.value = McpToolsUiState.Idle
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = e.message
            } catch (_: Exception) {
                _actionError.value = "保存工具开关失败，请重试"
            }
        }
    }

    fun dismissMcpTools() {
        _mcpTools.value = McpToolsUiState.Idle
    }

    /* ---------------- 技能：从仓库安装 / 检查更新 / 更新全部 ---------------- */

    private val _skillInstall = MutableStateFlow<SkillInstallUiState>(SkillInstallUiState.Idle)
    val skillInstall: StateFlow<SkillInstallUiState> = _skillInstall.asStateFlow()

    private val _skillCheck = MutableStateFlow<SkillCheckUiState>(SkillCheckUiState.Idle)
    val skillCheck: StateFlow<SkillCheckUiState> = _skillCheck.asStateFlow()

    private val _skillUpdate = MutableStateFlow<SkillUpdateUiState>(SkillUpdateUiState.Idle)
    val skillUpdate: StateFlow<SkillUpdateUiState> = _skillUpdate.asStateFlow()

    /** 探测仓库里的技能（不发写操作）。 */
    fun probeSkills(source: String) {
        val trimmed = source.trim()
        if (trimmed.isEmpty()) {
            _skillInstall.value = SkillInstallUiState.Failed("先填仓库地址（例如 owner/repo）")
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _skillInstall.value = SkillInstallUiState.Failed("网关未启动")
            return
        }
        _skillInstall.value = SkillInstallUiState.Probing
        viewModelScope.launch {
            _skillInstall.value = try {
                SkillInstallUiState.Probed(withContext(Dispatchers.IO) { client.probeSkills(status, trimmed) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SkillInstallUiState.Failed(e.message)
            } catch (_: Exception) {
                SkillInstallUiState.Failed("探测失败，请检查仓库地址或网络")
            }
        }
    }

    /** 安装选中的技能：只写网关库（`agents` 恒空），成功后刷新列表。 */
    fun installSkills(source: String, paths: List<String>) {
        if (paths.isEmpty()) {
            _skillInstall.value = SkillInstallUiState.Failed("先探测并勾选要安装的技能")
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _skillInstall.value = SkillInstallUiState.Failed("网关未启动")
            return
        }
        _skillInstall.value = SkillInstallUiState.Installing
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { client.installSkills(status, source, paths) }
                _skillInstall.value = SkillInstallUiState.Done(result)
                _notice.value = skillInstallSummary(result)
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _skillInstall.value = SkillInstallUiState.Failed(e.message)
            } catch (_: Exception) {
                _skillInstall.value = SkillInstallUiState.Failed("安装失败，请重试")
            }
        }
    }

    /** 检查更新：服务端逐个比对仓库内容，结果落在 `skills[].check`。 */
    fun checkSkills() {
        val status = repository.status.value
        if (!status.running) {
            _skillCheck.value = SkillCheckUiState.Failed("网关未启动")
            return
        }
        _skillCheck.value = SkillCheckUiState.Checking
        viewModelScope.launch {
            _skillCheck.value = try {
                SkillCheckUiState.Ready(withContext(Dispatchers.IO) { client.checkSkills(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SkillCheckUiState.Failed(e.message)
            } catch (_: Exception) {
                SkillCheckUiState.Failed("检查更新失败，请重试")
            }
        }
    }

    /** 更新全部：服务端只处理"从仓库安装"的技能，成功后刷新列表。 */
    fun updateAllSkills() {
        val status = repository.status.value
        if (!status.running) {
            _skillUpdate.value = SkillUpdateUiState.Failed("网关未启动")
            return
        }
        _skillUpdate.value = SkillUpdateUiState.Running
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { client.updateAllSkills(status) }
                _skillUpdate.value = SkillUpdateUiState.Done(result)
                _notice.value = skillUpdateSummary(result)
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _skillUpdate.value = SkillUpdateUiState.Failed(e.message)
            } catch (_: Exception) {
                _skillUpdate.value = SkillUpdateUiState.Failed("更新失败，请重试")
            }
        }
    }

    fun dismissSkillActions() {
        if (_skillInstall.value !is SkillInstallUiState.Probing &&
            _skillInstall.value !is SkillInstallUiState.Installing
        ) {
            _skillInstall.value = SkillInstallUiState.Idle
        }
        if (_skillCheck.value !is SkillCheckUiState.Checking) {
            _skillCheck.value = SkillCheckUiState.Idle
        }
        if (_skillUpdate.value !is SkillUpdateUiState.Running) {
            _skillUpdate.value = SkillUpdateUiState.Idle
        }
    }
}

/** 从仓库安装技能的三段式状态：探测 → 勾选 → 安装。 */
sealed interface SkillInstallUiState {
    data object Idle : SkillInstallUiState
    data object Probing : SkillInstallUiState
    data class Probed(val probe: SkillProbe) : SkillInstallUiState
    data object Installing : SkillInstallUiState
    data class Done(val result: SkillInstallResult) : SkillInstallUiState
    data class Failed(val message: String) : SkillInstallUiState
}

/** 检查更新：服务端逐个比对后的 `skills[].check`。 */
sealed interface SkillCheckUiState {
    data object Idle : SkillCheckUiState
    data object Checking : SkillCheckUiState
    data class Ready(val report: SkillCheckReport) : SkillCheckUiState
    data class Failed(val message: String) : SkillCheckUiState
}

/** 更新全部。 */
sealed interface SkillUpdateUiState {
    data object Idle : SkillUpdateUiState
    data object Running : SkillUpdateUiState
    data class Done(val result: SkillUpdateResult) : SkillUpdateUiState
    data class Failed(val message: String) : SkillUpdateUiState
}

/** MCP 工具开关的页面状态（桌面 `library.js:490-511`）。 */
sealed interface McpToolsUiState {
    data object Idle : McpToolsUiState
    data class Loading(val name: String) : McpToolsUiState
    data class Ready(
        val name: String,
        val tools: List<String>,
        val disabled: Set<String>,
        val why: String,
        val detail: String,
    ) : McpToolsUiState
    data class Failed(val name: String, val code: String, val message: String) : McpToolsUiState
}

/**
 * MCP 登录流程的瞬态（桌面 `library.js:790` `mcpLogin(it)`）。
 *
 * `state` 取值与 `mcp_signin.go` 的 flow 一致：waiting / done / failed / canceled。
 */
sealed interface McpAuthUiState {
    data object Idle : McpAuthUiState
    data class Starting(val name: String) : McpAuthUiState
    data class Waiting(val name: String, val id: String, val url: String) : McpAuthUiState
    data class Done(val name: String, val alreadySignedIn: Boolean) : McpAuthUiState
    data class Failed(val name: String, val code: String, val message: String) : McpAuthUiState
}
