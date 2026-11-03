package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.RoutePreview
import com.muyuchat.aether.runtime.RoutingApiClient
import com.muyuchat.aether.runtime.RoutingCatalogModel
import com.muyuchat.aether.runtime.RoutingGroup
import com.muyuchat.aether.runtime.RoutingOverview
import com.muyuchat.aether.runtime.RoutingSavePayload
import com.muyuchat.aether.runtime.addRoutingMember
import com.muyuchat.aether.runtime.filterRoutingCatalog
import com.muyuchat.aether.runtime.moveRoutingMember
import com.muyuchat.aether.runtime.removeRoutingMember
import com.muyuchat.aether.runtime.routingGroupMembers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.withContext

sealed interface RoutePreviewState {
    data object Idle : RoutePreviewState
    data object Loading : RoutePreviewState
    data class Ready(val preview: RoutePreview) : RoutePreviewState
    data class Failed(val message: String) : RoutePreviewState
}

sealed interface RouteSaveState {
    data object Idle : RouteSaveState
    data object Saving : RouteSaveState

    /** Save accepted; the surface now shows the group the server echoed back. */
    data object Saved : RouteSaveState
    data class Failed(val message: String) : RouteSaveState

    /** HTTP 409 from /api/groups/save: another group already uses the requested id. */
    data class Conflict(val message: String) : RouteSaveState
}

sealed interface RoutingUiState {
    data object Idle : RoutingUiState
    data object Loading : RoutingUiState
    data class Ready(val overview: RoutingOverview) : RoutingUiState
    data class Failed(val code: String, val message: String) : RoutingUiState
}

class RoutingViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = RoutingApiClient()
    private val refresh = MutableStateFlow(0)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<RoutingUiState> = combine(repository.status, refresh) { status, _ -> status }
        .flatMapLatest { status ->
            flow {
                if (!status.running) {
                    emit(RoutingUiState.Idle)
                    return@flow
                }
                emit(RoutingUiState.Loading)
                try {
                    emit(RoutingUiState.Ready(withContext(Dispatchers.IO) { client.fetchGroups(status) }))
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    emit(RoutingUiState.Failed(e.code, e.message))
                } catch (_: Exception) {
                    emit(RoutingUiState.Failed("unknown", "无法读取本机路由组，请重试"))
                }
            }
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), RoutingUiState.Idle)

    fun refresh() {
        refresh.value++
    }

    private val _selected = MutableStateFlow<RoutingGroup?>(null)
    val selected: StateFlow<RoutingGroup?> = _selected.asStateFlow()
    private val _preview = MutableStateFlow<RoutePreviewState>(RoutePreviewState.Idle)
    val preview: StateFlow<RoutePreviewState> = _preview.asStateFlow()
    private var previewJob: Job? = null

    /** Full `models` catalog from the last successful read, used to label and pick members. */
    val catalog: StateFlow<List<RoutingCatalogModel>> = state
        .map { snapshot -> (snapshot as? RoutingUiState.Ready)?.overview?.models.orEmpty() }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    private val _memberQuery = MutableStateFlow("")
    val memberQuery: StateFlow<String> = _memberQuery.asStateFlow()

    val filteredCatalog: StateFlow<List<RoutingCatalogModel>> =
        combine(catalog, memberQuery) { models, query -> filterRoutingCatalog(models, query) }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    /** Editable group name; kept apart from [selected] so a failed save cannot look applied. */
    private val _draftName = MutableStateFlow("")
    val draftName: StateFlow<String> = _draftName.asStateFlow()

    /** Editable member ids, seeded from the saved group on select and only replaced by a server echo. */
    private val _draftMembers = MutableStateFlow<List<String>>(emptyList())
    val draftMembers: StateFlow<List<String>> = _draftMembers.asStateFlow()

    fun select(group: RoutingGroup) {
        _selected.value = group
        resetDraft(group)
        loadPreview(group)
    }

    fun retryPreview() {
        // Retrying the read-only diagnosis must not throw away an unsaved member draft.
        _selected.value?.let(::loadPreview)
    }

    fun dismissPreview() {
        previewJob?.cancel()
        saveJob?.cancel()
        _selected.value = null
        _preview.value = RoutePreviewState.Idle
        _saveState.value = RouteSaveState.Idle
        resetDraft(null)
    }

    fun setMemberQuery(value: String) {
        _memberQuery.value = value
    }

    fun setDraftName(value: String) {
        _draftName.value = value
    }

    fun addMember(id: String) {
        if (_selected.value == null) {
            return
        }
        _draftMembers.value = addRoutingMember(_draftMembers.value, id)
    }

    fun removeMember(id: String) {
        if (_selected.value == null) {
            return
        }
        _draftMembers.value = removeRoutingMember(_draftMembers.value, id)
    }

    fun moveMember(id: String, up: Boolean) {
        if (_selected.value == null) {
            return
        }
        _draftMembers.value = moveRoutingMember(_draftMembers.value, id, up)
    }

    private fun loadPreview(group: RoutingGroup) {
        previewJob?.cancel()
        val status = repository.status.value
        if (!status.running) {
            _preview.value = RoutePreviewState.Failed("网关未启动")
            return
        }
        _preview.value = RoutePreviewState.Loading
        previewJob = viewModelScope.launch {
            try {
                _preview.value = RoutePreviewState.Ready(
                    withContext(Dispatchers.IO) { client.resolve(status, group.id) },
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _preview.value = RoutePreviewState.Failed(e.message)
            } catch (_: Exception) {
                _preview.value = RoutePreviewState.Failed("无法读取路由诊断，请重试")
            }
        }
    }

    private fun resetDraft(group: RoutingGroup?) {
        _draftName.value = group?.name.orEmpty()
        _draftMembers.value = group?.let(::routingGroupMembers).orEmpty()
        _memberQuery.value = ""
    }

    private val _saveState = MutableStateFlow<RouteSaveState>(RouteSaveState.Idle)
    val saveState: StateFlow<RouteSaveState> = _saveState.asStateFlow()
    private var saveJob: Job? = null

    fun saveStrategy(routing: String) {
        val group = _selected.value ?: return
        val status = repository.status.value
        if (!status.running) {
            _saveState.value = RouteSaveState.Failed("网关未启动")
            return
        }
        saveJob?.cancel()
        _saveState.value = RouteSaveState.Saving
        saveJob = viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.saveGroup(
                        status,
                        RoutingSavePayload(
                            id = group.id,
                            from = group.id,
                            name = group.name,
                            members = routingGroupMembers(group),
                            routing = routing,
                        ),
                    )
                }
                _saveState.value = RouteSaveState.Idle
                dismissPreview()
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _saveState.value = saveFailure(e)
            } catch (_: Exception) {
                _saveState.value = RouteSaveState.Failed("保存失败，请重试")
            }
        }
    }

    /**
     * Saves the member/name draft with the saved group id as both `id` and `from`
     * (groups_action_api.go:207-219). The response is the full `/api/groups` payload, so the
     * surface is rebuilt from the server echo instead of from the draft.
     */
    fun saveDraft() {
        val group = _selected.value ?: return
        val status = repository.status.value
        if (!status.running) {
            _saveState.value = RouteSaveState.Failed("网关未启动")
            return
        }
        val payload = RoutingSavePayload(
            id = group.id,
            from = group.id,
            name = _draftName.value.trim(),
            members = _draftMembers.value,
            routing = group.routing.ifBlank { group.routingEffective },
        )
        saveJob?.cancel()
        _saveState.value = RouteSaveState.Saving
        saveJob = viewModelScope.launch {
            try {
                val saved = withContext(Dispatchers.IO) { client.saveGroup(status, payload) }
                applySavedGroup(saved, group.id)
                _saveState.value = RouteSaveState.Saved
                refresh()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                // The draft stays untouched on failure: nothing is reported as applied.
                _saveState.value = saveFailure(e)
            } catch (_: Exception) {
                _saveState.value = RouteSaveState.Failed("保存失败，请重试")
            }
        }
    }

    private fun applySavedGroup(saved: RoutingOverview, savedId: String) {
        val group = saved.groups.firstOrNull { it.id == savedId } ?: return
        _selected.value = group
        resetDraft(group)
    }

    /** 409 is a name conflict, shown as such instead of being retried or rewritten. */
    private fun saveFailure(error: ManagementApiException): RouteSaveState =
        if (error.code == "http_409") {
            RouteSaveState.Conflict(error.message)
        } else {
            RouteSaveState.Failed(error.message)
        }
}
