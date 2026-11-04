package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.LibraryApiClient
import com.muyuchat.aether.runtime.LibraryDraft
import com.muyuchat.aether.runtime.LibraryDraftProblems
import com.muyuchat.aether.runtime.LibraryEntry
import com.muyuchat.aether.runtime.LibraryKind
import com.muyuchat.aether.runtime.LibrarySnapshot
import com.muyuchat.aether.runtime.LibraryWriteApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.validateLibraryDraft
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
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
}
