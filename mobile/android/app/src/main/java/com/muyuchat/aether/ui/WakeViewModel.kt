package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.CheckinSummary
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.SchedulerApiClient
import com.muyuchat.aether.runtime.SchedulerState
import com.muyuchat.aether.runtime.WakeApiClient
import com.muyuchat.aether.runtime.WakeList
import com.muyuchat.aether.runtime.WakeDraft
import com.muyuchat.aether.runtime.WakeDraftProblems
import com.muyuchat.aether.runtime.WakeTask
import com.muyuchat.aether.runtime.WakeWriteApiClient
import com.muyuchat.aether.runtime.validateWakeDraft
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface WakeUiState {
    data object Idle : WakeUiState

    data object Loading : WakeUiState

    data class Ready(
        val tasks: List<WakeTask>,
        val serverNow: Long,
        val sampledAtMs: Long,
    ) : WakeUiState

    data class Failed(val code: String, val message: String) : WakeUiState
}

/** 签到调度的页面侧状态（与唤醒任务分开：两者由不同端点提供，失败也要分开显示）。 */
sealed interface SchedulerUiState {
    data object Idle : SchedulerUiState

    data class Ready(val state: SchedulerState) : SchedulerUiState

    data class Failed(val code: String, val message: String) : SchedulerUiState
}

/**
 * 唤醒队列页状态。
 *
 * 只做「看状态 + 手动触发一次」：手动触发是 `POST /api/wake/{id}/run`，服务端异步执行并只回
 * 202 `{"started":true}`，所以这里把结果记成「已提交」，绝不说「已完成」。
 */
class WakeViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = WakeApiClient()

    private val _state = MutableStateFlow<WakeUiState>(WakeUiState.Idle)
    val state: StateFlow<WakeUiState> = _state.asStateFlow()

    private val _triggering = MutableStateFlow<String?>(null)
    val triggering: StateFlow<String?> = _triggering.asStateFlow()

    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    private val _actionError = MutableStateFlow<String?>(null)
    val actionError: StateFlow<String?> = _actionError.asStateFlow()

    // —— 唤醒任务新建 / 删除 + 签到汇总 ——
    private val writeClient = WakeWriteApiClient()
    private val managementClient = ManagementApiClient()
    private val _accounts = MutableStateFlow<List<AccountSummary>>(emptyList())
    val accounts: StateFlow<List<AccountSummary>> = _accounts.asStateFlow()
    private val _summary = MutableStateFlow<CheckinSummary?>(null)
    val checkinSummary: StateFlow<CheckinSummary?> = _summary.asStateFlow()
    private val _creating = MutableStateFlow(false)
    val creating: StateFlow<Boolean> = _creating.asStateFlow()
    private val _createProblems = MutableStateFlow<WakeDraftProblems?>(null)
    val createProblems: StateFlow<WakeDraftProblems?> = _createProblems.asStateFlow()

    /** 可选的账号（新建唤醒任务时用）；顺手读一次签到汇总。 */
    fun refreshAccountsAndSummary() {
        val status = repository.status.value
        if (!status.running) {
            _accounts.value = emptyList()
            _summary.value = null
            return
        }
        viewModelScope.launch {
            _accounts.value = runCatching {
                withContext(Dispatchers.IO) {
                    managementClient.fetchProviders(status).flatMap { it.accounts }
                }
            }.getOrDefault(emptyList())
            _summary.value = runCatching {
                withContext(Dispatchers.IO) { writeClient.fetchCheckinSummary(status) }
            }.getOrNull()
        }
    }

    /**
     * 新建唤醒任务。
     *
     * 服务端语义：同一账号重复提交是**更新**已有任务，不是再建一条——页面上必须说清楚。
     * 成功只说"已创建"，不承诺"会准点执行"（手机端是尽力而为）。
     */
    fun createWake(draft: WakeDraft, onDone: (Boolean) -> Unit = {}) {
        if (_creating.value) {
            return
        }
        val normalized = draft.normalized()
        val problems = validateWakeDraft(normalized)
        _createProblems.value = problems
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
        _creating.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { writeClient.createWake(status, normalized) }
                _notice.value = "已创建唤醒任务（同一账号再次保存是更新，不会重复建）"
                refresh()
                refreshAccountsAndSummary()
                onDone(true)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = "创建失败：${e.message}（错误码：${e.code}）"
                onDone(false)
            } catch (_: Exception) {
                _actionError.value = "创建失败，请重试"
                onDone(false)
            } finally {
                _creating.value = false
            }
        }
    }

    fun deleteWake(id: String) {
        val status = repository.status.value
        if (!status.running || id.isBlank()) {
            return
        }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { writeClient.deleteWake(status, id) }
                _notice.value = "已删除唤醒任务"
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = "删除失败：${e.message}（错误码：${e.code}）"
            } catch (_: Exception) {
                _actionError.value = "删除失败，请重试"
            }
        }
    }

    fun clearCreateProblems() {
        _createProblems.value = null
    }

    // —— 启停开关（桌面 scheduler.js:405-412）：局部更新，只发 enabled ——
    private val _enabledUpdating = MutableStateFlow<String?>(null)

    /** 正在改启停的任务 id（页面据此禁用开关，避免连点覆盖）。 */
    val enabledUpdating: StateFlow<String?> = _enabledUpdating.asStateFlow()

    fun setEnabled(task: WakeTask, enabled: Boolean) {
        val status = repository.status.value
        if (!status.running || task.id.isBlank()) {
            return
        }
        if (_enabledUpdating.value != null) {
            return
        }
        _enabledUpdating.value = task.id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { writeClient.setEnabled(status, task.id, enabled) }
                // 桌面文案逐字（scheduler.js:405-412）
                _notice.value = if (enabled) "已启用唤醒任务" else "已停用唤醒任务"
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _actionError.value = "更新失败：${e.message}"
            } catch (_: Exception) {
                _actionError.value = "更新失败，请重试"
            } finally {
                _enabledUpdating.value = null
            }
        }
    }

    // —— 签到调度：与唤醒任务分开的端点、分开的失败态 ——
    private val schedulerClient = SchedulerApiClient()
    private val _scheduler = MutableStateFlow<SchedulerUiState>(SchedulerUiState.Idle)
    val scheduler: StateFlow<SchedulerUiState> = _scheduler.asStateFlow()
    private val _checkinRunning = MutableStateFlow(false)
    val checkinRunning: StateFlow<Boolean> = _checkinRunning.asStateFlow()
    private val _checkinNote = MutableStateFlow<String?>(null)
    val checkinNote: StateFlow<String?> = _checkinNote.asStateFlow()

    private var loadJob: Job? = null

    /** 签到调度状态。未接线时服务端给 reason，这里原样保留，不伪造空任务。 */
    fun refreshScheduler() {
        val status = repository.status.value
        if (!status.running) {
            _scheduler.value = SchedulerUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        viewModelScope.launch {
            _scheduler.value = try {
                val state = withContext(Dispatchers.IO) { schedulerClient.fetchState(status) }
                SchedulerUiState.Ready(state)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SchedulerUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                SchedulerUiState.Failed("unknown", "读取签到状态失败，请重试")
            }
        }
    }

    /** 手动签到：服务端**入队**即返回，这里只显示它给的 note，不宣称"已完成"。 */
    fun runCheckin() {
        if (_checkinRunning.value) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _checkinNote.value = "网关未启动"
            return
        }
        _checkinRunning.value = true
        _checkinNote.value = null
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { schedulerClient.runAll(status) }
                _checkinNote.value = result.note
                refreshScheduler()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _checkinNote.value = e.message
            } catch (_: Exception) {
                _checkinNote.value = "触发签到失败，请重试"
            } finally {
                _checkinRunning.value = false
            }
        }
    }

    fun dismissCheckinNote() {
        _checkinNote.value = null
    }

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            loadJob?.cancel()
            _state.value = WakeUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        loadJob?.cancel()
        if (_state.value !is WakeUiState.Ready) {
            _state.value = WakeUiState.Loading
        }
        loadJob = viewModelScope.launch {
            _state.value = try {
                val list: WakeList = withContext(Dispatchers.IO) { client.fetchTasks(status) }
                WakeUiState.Ready(list.tasks, list.now, System.currentTimeMillis())
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                WakeUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                WakeUiState.Failed("unknown", "读取唤醒队列失败，请重试")
            }
        }
    }

    fun dismissNotice() {
        _notice.value = null
    }

    fun dismissActionError() {
        _actionError.value = null
    }

    fun runTask(id: String) {
        if (_triggering.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _actionError.value = "网关未启动"
            return
        }
        _actionError.value = null
        _notice.value = null
        _triggering.value = id
        viewModelScope.launch {
            try {
                val started = withContext(Dispatchers.IO) { client.runTask(status, id) }
                _notice.value = if (started) {
                    // 桌面原话「已触发唤醒」+「正在请求上游…」，括号注明异步（scheduler.js:413-426）
                    "已触发唤醒（服务端异步执行，正在请求上游…，结果在下次刷新显示）"
                } else {
                    "唤醒失败：服务端没有接受这次触发（started=false）"
                }
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                // 桌面文案：「唤醒失败：<原文>」（scheduler.js:413-426）
                _actionError.value = "唤醒失败：${e.message}"
            } catch (_: Exception) {
                _actionError.value = "唤醒失败：请重试"
            } finally {
                _triggering.value = null
            }
        }
    }
}
