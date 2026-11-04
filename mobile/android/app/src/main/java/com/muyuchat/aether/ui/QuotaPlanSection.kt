package com.muyuchat.aether.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
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
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val CardPadding = 16.dp
private val RowGap = 12.dp
private val TightGap = 8.dp

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

    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(CardPadding),
            verticalArrangement = Arrangement.spacedBy(RowGap),
        ) {
            Text(
                text = "订阅与到期",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "本机账号的套餐与到期日（来自余额/订阅接口）。到期日只写日期，不写倒计时——" +
                    "模板文字不会随时间变化而说谎。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            McaButton(
                onClick = viewModel::refresh,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = running && state !is QuotaPlanUiState.Loading,
            ) { Text(if (state is QuotaPlanUiState.Loading) "读取中…" else "读取订阅信息") }
            when (val current = state) {
                QuotaPlanUiState.Idle -> Text(
                    text = quotaPlanIdleHint(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                QuotaPlanUiState.Loading -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(TightGap),
                ) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Text(
                        text = "正在读取订阅信息…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is QuotaPlanUiState.Failed -> Text(
                    text = sectionErrorLine(current.code, current.message, "订阅信息"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                is QuotaPlanUiState.Ready -> {
                    Text(
                        text = quotaPlanSummary(current.plans),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    if (current.plans.isEmpty()) {
                        Text(
                            text = quotaPlanEmptyHint(),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else {
                        val now = System.currentTimeMillis()
                        current.plans.forEach { plan ->
                            Column(
                                modifier = Modifier.fillMaxWidth(),
                                verticalArrangement = Arrangement.spacedBy(4.dp),
                            ) {
                                Text(
                                    text = quotaPlanLine(plan, now),
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
}

/** 还没点过读取：说清这一步会做什么，而不是留一片空白。 */
internal fun quotaPlanIdleHint(): String =
    "还没有读取。下一步：点「读取订阅信息」从本机接口拉一次（只读，不会修改任何账号）。"

/** 读到空列表：为什么空 + 下一步去哪儿。 */
internal fun quotaPlanEmptyHint(): String =
    "为什么空：只有返回了套餐或到期日的账号才会出现在这里，只报了余额的账号不会。" +
        "下一步：到「额度」页确认账号已登录且能读到额度，再回来重新读取。"
