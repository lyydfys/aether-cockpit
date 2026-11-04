package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.SettingsSecurity
import com.muyuchat.aether.runtime.SettingsSecurityApiClient
import com.muyuchat.aether.runtime.settingsSecurityLines
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface SettingsSecurityUiState {
    data object Idle : SettingsSecurityUiState
    data object Loading : SettingsSecurityUiState
    data class Ready(val view: SettingsSecurity) : SettingsSecurityUiState
    data class Failed(val code: String, val message: String) : SettingsSecurityUiState
}

class SettingsSecurityViewModel(application: android.app.Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = SettingsSecurityApiClient()
    private val _state = MutableStateFlow<SettingsSecurityUiState>(SettingsSecurityUiState.Idle)
    val state: StateFlow<SettingsSecurityUiState> = _state.asStateFlow()

    fun refresh() {
        val status = repository.status.value
        if (!status.running) {
            _state.value = SettingsSecurityUiState.Idle
            return
        }
        _state.value = SettingsSecurityUiState.Loading
        viewModelScope.launch {
            _state.value = try {
                SettingsSecurityUiState.Ready(withContext(Dispatchers.IO) { client.fetch(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                SettingsSecurityUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                SettingsSecurityUiState.Failed("unknown", "读取局域网设置失败")
            }
        }
    }
}

/**
 * 「局域网与访问控制」只读小节。
 *
 * 这一节存在的意义就是把"手机上为什么没有局域网开关"说清楚，而不是给一个按了没反应的按钮。
 */
@Composable
internal fun SettingsSecuritySection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: SettingsSecurityViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }

    Column(modifier = modifier.fillMaxWidth()) {
        Spacer(Modifier.height(8.dp))
        Text(
            text = "局域网与访问控制",
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(4.dp))
        when (val current = state) {
            SettingsSecurityUiState.Idle -> Text(
                text = if (status.running) "正在读取…" else "网关未启动，读不到这些设置。",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            SettingsSecurityUiState.Loading -> Text(
                text = "正在读取…",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            is SettingsSecurityUiState.Failed -> Text(
                text = "读取失败：${current.message}（错误码：${current.code}）",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
            )
            is SettingsSecurityUiState.Ready -> {
                settingsSecurityLines(current.view).forEach { line ->
                    Text(
                        text = "· $line",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(vertical = 1.dp),
                    )
                }
            }
        }
    }
}
