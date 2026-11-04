package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AgentCliReport
import com.muyuchat.aether.runtime.AgentModelsView
import com.muyuchat.aether.runtime.AgentsApiClient
import com.muyuchat.aether.runtime.AgentsOverview
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface AgentsUiState {
    data object Idle : AgentsUiState
    data object Loading : AgentsUiState
    data class Ready(val overview: AgentsOverview) : AgentsUiState
    data class Failed(val code: String, val message: String) : AgentsUiState
}

sealed interface AgentCliUiState {
    data object Idle : AgentCliUiState
    data object Loading : AgentCliUiState
    data class Ready(val report: AgentCliReport) : AgentCliUiState

    /** 501 `platform_unsupported`：手机运行时没有这项能力，原话照抄给用户看。 */
    data class Unsupported(val reason: String) : AgentCliUiState
    data class Failed(val code: String, val message: String) : AgentCliUiState
}

sealed interface AgentModelsUiState {
    data object Idle : AgentModelsUiState
    data class Loading(val agentId: String) : AgentModelsUiState
    data class Ready(val view: AgentModelsView) : AgentModelsUiState
    data class Failed(val agentId: String, val message: String) : AgentModelsUiState
}

/**
 * Agent 接入页状态。
 *
 * 只读：接入 / 断开 / 写模型都会改**本机 agent 的配置文件**（`agents_api.go:14-18`），
 * 手机端没有任何写入口，所以这里也没有对应的动作方法——不是"忘了做"。
 */
class AgentsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = AgentsApiClient()

    private val _state = MutableStateFlow<AgentsUiState>(AgentsUiState.Idle)
    val state: StateFlow<AgentsUiState> = _state.asStateFlow()

    private val _cli = MutableStateFlow<AgentCliUiState>(AgentCliUiState.Idle)
    val cli: StateFlow<AgentCliUiState> = _cli.asStateFlow()

    private val _models = MutableStateFlow<AgentModelsUiState>(AgentModelsUiState.Idle)
    val models: StateFlow<AgentModelsUiState> = _models.asStateFlow()

    private val _showAll = MutableStateFlow(false)
    val showAll: StateFlow<Boolean> = _showAll.asStateFlow()

    fun toggleShowAll() {
        _showAll.value = !_showAll.value
    }

    /** 一次刷新同时拉「Agent 清单」与「CLI 巡检」：两者失败互不影响。 */
    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = AgentsUiState.Failed("gateway_not_running", "网关未启动")
            _cli.value = AgentCliUiState.Idle
            return
        }
        if (_state.value !is AgentsUiState.Ready) {
            _state.value = AgentsUiState.Loading
        }
        viewModelScope.launch {
            _state.value = try {
                AgentsUiState.Ready(withContext(Dispatchers.IO) { client.fetchOverview(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                AgentsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                AgentsUiState.Failed("unknown", "读取 Agent 清单失败，请重试")
            }
        }
        loadCli(refreshRemotely = false)
    }

    /** 清服务端缓存后重新巡检（`POST /api/agents/cli`）。 */
    fun refreshCli() {
        loadCli(refreshRemotely = true)
    }

    private fun loadCli(refreshRemotely: Boolean) {
        val status = repository.status.value
        if (!status.running) {
            _cli.value = AgentCliUiState.Idle
            return
        }
        _cli.value = AgentCliUiState.Loading
        viewModelScope.launch {
            _cli.value = try {
                val report = withContext(Dispatchers.IO) {
                    if (refreshRemotely) client.refreshCli(status) else client.fetchCli(status)
                }
                AgentCliUiState.Ready(report)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                if (e.code == "platform_unsupported") {
                    AgentCliUiState.Unsupported(e.message)
                } else {
                    AgentCliUiState.Failed(e.code, e.message)
                }
            } catch (_: Exception) {
                AgentCliUiState.Failed("unknown", "读取 CLI 巡检失败，请重试")
            }
        }
    }

    /** 打开某个 agent 的模型清单（只读）。 */
    fun openModels(agentId: String) {
        val status = repository.status.value
        if (!status.running) {
            _models.value = AgentModelsUiState.Failed(agentId, "网关未启动")
            return
        }
        _models.value = AgentModelsUiState.Loading(agentId)
        viewModelScope.launch {
            _models.value = try {
                AgentModelsUiState.Ready(withContext(Dispatchers.IO) { client.fetchModels(status, agentId) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                AgentModelsUiState.Failed(agentId, "${e.message}（错误码：${e.code}）")
            } catch (_: Exception) {
                AgentModelsUiState.Failed(agentId, "读取模型清单失败，请重试")
            }
        }
    }

    fun closeModels() {
        _models.value = AgentModelsUiState.Idle
    }
}
