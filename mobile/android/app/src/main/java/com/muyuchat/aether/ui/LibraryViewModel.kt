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
