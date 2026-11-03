package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn

/** Typed page state for the Gateway overview. */
data class OverviewUiState(
    val status: GatewayStatus = GatewayStatus(),
    val notice: String = "",
)

/** User intents accepted by the overview page. */
sealed interface OverviewAction {
    data object Start : OverviewAction
    data object Stop : OverviewAction
    data object NotificationDenied : OverviewAction
}

/**
 * Route state holder between the Composable and the gateway runtime. The
 * controller remains the only writer of [GatewayStatus]; this ViewModel only
 * combines it with page-local notice state and forwards lifecycle intents.
 */
class OverviewViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val notice = MutableStateFlow("")

    val state: StateFlow<OverviewUiState> = combine(repository.status, notice) { status, message ->
        OverviewUiState(status = status, notice = message)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.Eagerly,
        initialValue = OverviewUiState(status = repository.status.value),
    )

    fun onAction(action: OverviewAction) {
        when (action) {
            OverviewAction.Start -> {
                notice.value = ""
                repository.start()
            }
            OverviewAction.Stop -> {
                notice.value = ""
                repository.stop()
            }
            OverviewAction.NotificationDenied -> {
                notice.value = "需要通知权限，前台服务才能在系统通知中显示运行状态"
            }
        }
    }
}
