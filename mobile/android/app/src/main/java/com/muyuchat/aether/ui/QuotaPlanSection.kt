package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.QuotaPlan
import com.muyuchat.aether.runtime.QuotaPlanApiClient
import com.muyuchat.aether.runtime.quotaPlanLine
import com.muyuchat.aether.runtime.quotaPlanSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface QuotaPlanUiState {
    data object Idle : QuotaPlanUiState
    data object Loading : QuotaPlanUiState
    data class Ready(val plans: List<QuotaPlan>) : QuotaPlanUiState
    data class Failed(val code: String, val message: String) : QuotaPlanUiState
}

class QuotaPlanViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = QuotaPlanApiClient()
    private val _state = MutableStateFlow<QuotaPlanUiState>(QuotaPlanUiState.Idle)
    val state: StateFlow<QuotaPlanUiState> = _state.asStateFlow()

    fun refresh() {
        if (_state.value is QuotaPlanUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _state.value = QuotaPlanUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = QuotaPlanUiState.Loading
        viewModelScope.launch {
            _state.value = try {
                QuotaPlanUiState.Ready(withContext(Dispatchers.IO) { client.fetchPlans(status) })
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                QuotaPlanUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                QuotaPlanUiState.Failed("unknown", "读取订阅信息失败，请重试")
            }
        }
    }
}

/**
 * 订阅计划与到期。
 *
 * 手机端以前只读了 `/api/usage/quotas/history`（读数点），所以"这个账号是什么套餐、哪天到期"
 * 一直看不到——这一块补的就是它。到期日只写日期，不写"还剩 N 天"（静态文字会过期）。
 */
@Composable
internal fun QuotaPlanSection(
    running: Boolean,
    modifier: Modifier = Modifier,
    viewModel: QuotaPlanViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Surface(shape = McaShape.Card, color = McaTonal.fill(), border = McaHairline.stroke(), modifier = modifier) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "订阅与到期",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "本机账号的套餐与到期日（来自余额/订阅接口）。到期日只写日期，不写倒计时——" +
                    "模板文字不会随时间变化而说谎。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                enabled = running && state !is QuotaPlanUiState.Loading,
            ) { Text(if (state is QuotaPlanUiState.Loading) "读取中…" else "读取订阅信息") }
            when (val current = state) {
                QuotaPlanUiState.Idle, QuotaPlanUiState.Loading -> Unit
                is QuotaPlanUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = if (current.code == "gateway_not_running") {
                            "网关未启动：启动后才能读取订阅信息。"
                        } else {
                            "${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                is QuotaPlanUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = quotaPlanSummary(current.plans),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    val now = System.currentTimeMillis()
                    current.plans.forEach { plan ->
                        Spacer(Modifier.height(6.dp))
                        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(1.dp)) {
                            Text(
                                text = "· ${quotaPlanLine(plan, now)}",
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurface,
                            )
                            if (plan.balance.isNotBlank()) {
                                Text(
                                    text = "余额：${plan.balance}",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
