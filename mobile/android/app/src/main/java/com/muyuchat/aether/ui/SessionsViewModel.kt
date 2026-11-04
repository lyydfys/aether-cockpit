package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AgentOption
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.SessionContentSearch
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.SessionDetailApiClient
import com.muyuchat.aether.runtime.SessionSearchApiClient
import com.muyuchat.aether.runtime.SessionStats
import com.muyuchat.aether.runtime.SessionStatsApiClient
import com.muyuchat.aether.runtime.SessionTranscriptPart
import com.muyuchat.aether.runtime.SessionUsage
import com.muyuchat.aether.runtime.SessionUsageApiClient
import com.muyuchat.aether.runtime.SessionUsageView
import com.muyuchat.aether.runtime.SessionList
import com.muyuchat.aether.runtime.SessionRow
import com.muyuchat.aether.runtime.SessionsApiClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface SessionsUiState {
    data object Idle : SessionsUiState

    data object Loading : SessionsUiState

    data class Ready(
        val sessions: List<SessionRow>,
        val agents: List<AgentOption>,
        val sampledAtMs: Long,
        /** 会话用量（今日/近 7 天/近 30 天各一份，页面切周期不再请求）。 */
        val usage: SessionUsageUiState = SessionUsageUiState.Idle,
    ) : SessionsUiState

    data class Failed(val code: String, val message: String) : SessionsUiState
}

/**
 * 会话用量（桌面 `sessions.js:210` `renderUsage` 的数据）。
 *
 * 服务端一次只回一个时间窗（`sessions_usage_summary.go:82-91`），所以这里把
 * 今日/近 7 天/近 30 天各取一份一起带下去，页面切周期是纯本地切换。
 */
sealed interface SessionUsageUiState {
    data object Idle : SessionUsageUiState
    data object Loading : SessionUsageUiState
    data class Ready(val byDays: Map<Int, SessionUsage>) : SessionUsageUiState
    data class Failed(val code: String, val message: String) : SessionUsageUiState
}

/** 桌面的时间范围控件就是这三档（`sessions.js:263`）。 */
internal val SESSION_USAGE_PERIODS: List<Int> = listOf(1, 7, 30)

/**
 * 单个会话详情（桌面 `sessions.js:403-478` 的详情抽屉）。
 *
 * 数据来自 `GET /api/sessions/one?key=…` 与 `GET /api/sessions/transcript?…`：
 * 用量拿不到不算失败（桌面 `.catch(() => {})` 吞掉，`sessions.js:457`），正文拿不到才算失败。
 */
sealed interface SessionDetailUiState {
    data object Idle : SessionDetailUiState
    data class Loading(val sessionKey: String) : SessionDetailUiState
    data class Ready(
        val sessionKey: String,
        /** 用量视图；`null` 表示这条会话没有可用量数据（或取用量的请求失败了）。 */
        val usage: SessionUsageView?,
        val parts: List<SessionTranscriptPart>,
        /** `file` = 从会话文件读到；`gateway` = 网关自己的会话没有落文件。 */
        val source: String,
        val cut: Boolean,
    ) : SessionDetailUiState
    data class Failed(val sessionKey: String, val code: String, val message: String) : SessionDetailUiState
}

/** 「复制 Markdown」的一次性结果：页面拿它写剪贴板，写完调用 dismissSessionMarkdown 丢掉。 */
sealed interface SessionMarkdownUiState {
    data object Idle : SessionMarkdownUiState
    data class Ready(val sessionKey: String, val markdown: String) : SessionMarkdownUiState
    data class Failed(val sessionKey: String, val code: String, val message: String) : SessionMarkdownUiState
}

/**
 * 会话页状态。**只读**：不改 Agent 的记录，也不做任何"同步"。
 *
 * 手机上列表必然为空（见 [SessionsApiClient] 的口径说明），所以空态是正常终态，
 * 不是错误——`Ready(emptyList())` 与 `Failed` 必须分开，页面文案也不同。
 */
sealed interface SessionStatsUiState {
    data object Idle : SessionStatsUiState
    data object Running : SessionStatsUiState
    data class Ready(val stats: SessionStats) : SessionStatsUiState
    data class Failed(val code: String, val message: String) : SessionStatsUiState
}

sealed interface SessionContentUiState {
    data object Idle : SessionContentUiState
    data object Running : SessionContentUiState
    data class Ready(val result: SessionContentSearch) : SessionContentUiState
    data class Failed(val code: String, val message: String) : SessionContentUiState
}

class SessionsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = SessionsApiClient()

    private val _state = MutableStateFlow<SessionsUiState>(SessionsUiState.Idle)
    val state: StateFlow<SessionsUiState> = _state.asStateFlow()

    private val _agentFilter = MutableStateFlow("")
    val agentFilter: StateFlow<String> = _agentFilter.asStateFlow()

    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    private var loadJob: Job? = null

    fun setAgentFilter(agent: String) {
        if (_agentFilter.value == agent) {
            return
        }
        _agentFilter.value = agent
        refresh()
    }

    fun setQuery(query: String) {
        if (_query.value == query) {
            return
        }
        _query.value = query
        // 搜索只影响列表：不要把三个时间窗的用量重扫一遍。
        load(includeUsage = false)
    }

    // ——— 会话内容检索：只回答"哪些会话提到过"，**不展示正文片段** ———
    private val searchClient = SessionSearchApiClient()
    private val _contentSearch = MutableStateFlow<SessionContentUiState>(SessionContentUiState.Idle)
    val contentSearch: StateFlow<SessionContentUiState> = _contentSearch.asStateFlow()

    fun searchContent() {
        if (_contentSearch.value is SessionContentUiState.Running) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _contentSearch.value = SessionContentUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        val needle = _query.value.trim()
        if (needle.isEmpty()) {
            _contentSearch.value = SessionContentUiState.Failed("query_empty", "先在上面输入要搜索的词")
            return
        }
        _contentSearch.value = SessionContentUiState.Running
        viewModelScope.launch {
            _contentSearch.value = try {
                val result = withContext(Dispatchers.IO) {
                    searchClient.search(status, _agentFilter.value, needle)
                }
                SessionContentUiState.Ready(result)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SessionContentUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                SessionContentUiState.Failed("unknown", "会话检索失败，请重试")
            }
        }
    }

    fun dismissContentSearch() {
        _contentSearch.value = SessionContentUiState.Idle
    }
    // ——— 会话统计：只取计数，不解析 top/shape 里的会话级明细 ———
    private val statsClient = SessionStatsApiClient()
    private val _stats = MutableStateFlow<SessionStatsUiState>(SessionStatsUiState.Idle)
    val stats: StateFlow<SessionStatsUiState> = _stats.asStateFlow()

    fun refreshStats(days: Int = 7) {
        val status = repository.status.value
        if (!status.running) {
            _stats.value = SessionStatsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _stats.value = SessionStatsUiState.Running
        viewModelScope.launch {
            _stats.value = try {
                val result = withContext(Dispatchers.IO) { statsClient.fetchStats(status, days) }
                SessionStatsUiState.Ready(result)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SessionStatsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                SessionStatsUiState.Failed("unknown", "读取会话统计失败，请重试")
            }
        }
    }
    fun refresh() {
        load(includeUsage = true)
    }

    /**
     * 会话用量客户端：一次取三个时间窗（见 [SESSION_USAGE_PERIODS]）。
     *
     * 只读端点 `GET /api/sessions/usage`；三个窗口全失败才算失败，部分成功就如实显示已有的。
     */
    private val usageClient = SessionUsageApiClient()

    private suspend fun loadUsageSnapshot(status: GatewayStatus, agent: String): SessionUsageUiState {
        val byDays = LinkedHashMap<Int, SessionUsage>()
        var lastError: ManagementApiException? = null
        for (days in SESSION_USAGE_PERIODS) {
            try {
                byDays[days] = withContext(Dispatchers.IO) { usageClient.fetchUsage(status, days, agent) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                lastError = e
            } catch (_: Exception) {
                lastError = ManagementApiException("unknown", "读取会话用量失败，请重试")
            }
        }
        return when {
            byDays.isNotEmpty() -> SessionUsageUiState.Ready(byDays)
            lastError != null -> SessionUsageUiState.Failed(lastError!!.code, lastError!!.message)
            else -> SessionUsageUiState.Idle
        }
    }

    private fun load(includeUsage: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            loadJob?.cancel()
            _state.value = SessionsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        loadJob?.cancel()
        if (_state.value !is SessionsUiState.Ready) {
            _state.value = SessionsUiState.Loading
        }
        val agent = _agentFilter.value
        val query = _query.value
        loadJob = viewModelScope.launch {
            _state.value = try {
                val list: SessionList = withContext(Dispatchers.IO) {
                    client.fetchSessions(status, agent = agent, query = query)
                }
                val usage = if (includeUsage) {
                    loadUsageSnapshot(status, agent)
                } else {
                    (_state.value as? SessionsUiState.Ready)?.usage ?: SessionUsageUiState.Idle
                }
                SessionsUiState.Ready(list.sessions, list.agents, System.currentTimeMillis(), usage)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SessionsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                SessionsUiState.Failed("unknown", "读取会话失败，请重试")
            }
        }
    }

    // ——— 会话详情 / 复制 Markdown（桌面 sessions.js:403-478） ———

    private val detailClient = SessionDetailApiClient()
    private val _detail = MutableStateFlow<SessionDetailUiState>(SessionDetailUiState.Idle)
    val detail: StateFlow<SessionDetailUiState> = _detail.asStateFlow()
    private var detailJob: Job? = null

    private val _markdown = MutableStateFlow<SessionMarkdownUiState>(SessionMarkdownUiState.Idle)
    val markdown: StateFlow<SessionMarkdownUiState> = _markdown.asStateFlow()

    /**
     * 打开一条会话：用量与正文各取一次。
     *
     * 用量失败只让用量区空着（桌面也是这样，`sessions.js:457` 吞掉错误），
     * 正文失败才是整个详情失败——因为详情的主体是正文。
     */
    fun openSessionDetail(agent: String, id: String) {
        if (agent.isBlank() || id.isBlank()) {
            return
        }
        detailJob?.cancel()
        val sessionKey = "$agent:$id"
        val status = repository.status.value
        if (!status.running) {
            _detail.value = SessionDetailUiState.Failed(sessionKey, "gateway_not_running", "网关未启动")
            return
        }
        _detail.value = SessionDetailUiState.Loading(sessionKey)
        detailJob = viewModelScope.launch {
            val transcript = try {
                withContext(Dispatchers.IO) { detailClient.fetchTranscript(status, agent, id) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _detail.value = SessionDetailUiState.Failed(sessionKey, e.code, e.message)
                return@launch
            } catch (_: Exception) {
                _detail.value = SessionDetailUiState.Failed(sessionKey, "unknown", "读取会话正文失败，请重试")
                return@launch
            }
            val usage = try {
                withContext(Dispatchers.IO) { detailClient.fetchSession(status, agent, id) }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                null
            }
            _detail.value = SessionDetailUiState.Ready(
                sessionKey = sessionKey,
                usage = usage,
                parts = transcript.parts,
                source = transcript.source,
                cut = transcript.cut,
            )
        }
    }

    fun dismissSessionDetail() {
        detailJob?.cancel()
        _detail.value = SessionDetailUiState.Idle
    }

    /** 桌面是「下载 Markdown」（`sessions.js:412`）；手机上取回原文交给页面复制。 */
    fun loadSessionMarkdown(agent: String, id: String) {
        if (agent.isBlank() || id.isBlank()) {
            return
        }
        val sessionKey = "$agent:$id"
        val status = repository.status.value
        if (!status.running) {
            _markdown.value = SessionMarkdownUiState.Failed(sessionKey, "gateway_not_running", "网关未启动")
            return
        }
        viewModelScope.launch {
            try {
                val text = withContext(Dispatchers.IO) { detailClient.fetchMarkdown(status, agent, id) }
                if (text.isBlank()) {
                    _markdown.value = SessionMarkdownUiState.Failed(sessionKey, "empty", "这个会话没有可导出的正文")
                } else {
                    _markdown.value = SessionMarkdownUiState.Ready(sessionKey, text)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _markdown.value = SessionMarkdownUiState.Failed(sessionKey, e.code, e.message)
            } catch (_: Exception) {
                _markdown.value = SessionMarkdownUiState.Failed(sessionKey, "unknown", "读取 Markdown 失败，请重试")
            }
        }
    }

    fun dismissSessionMarkdown() {
        _markdown.value = SessionMarkdownUiState.Idle
    }
}
