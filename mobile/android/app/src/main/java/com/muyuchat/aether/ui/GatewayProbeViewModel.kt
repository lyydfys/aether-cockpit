package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.AnonymousProbeReport
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ProbeApiClient
import com.muyuchat.aether.runtime.SessionProbeReport
import com.muyuchat.aether.runtime.failedAnonymousReport
import com.muyuchat.aether.runtime.failedSessionReport
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.withContext

sealed interface ProbeUiState {
    data object Idle : ProbeUiState
    data object Loading : ProbeUiState

    /**
     * 匿名组与会话组分开持有：一组失败不会影响另一组的展示，
     * 页面也就可以分开写「匿名端点」与「会话端点（需会话凭据）」两块。
     */
    data class Ready(
        val anonymous: AnonymousProbeReport,
        val session: SessionProbeReport,
        val sampledAtMs: Long,
    ) : ProbeUiState

    data class Failed(val code: String, val message: String) : ProbeUiState
}

/**
 * 探针工作区的 ViewModel。
 *
 * 探针只代表"当前这一刻"：网关一停就回到 `Failed("gateway_not_running", "网关未启动")`，
 * 既不留上一次的结果，也不再发任何请求——旧数据在这张卡上比空态更容易误导人。
 */
class GatewayProbeViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ProbeApiClient()
    private val refresh = MutableStateFlow(0)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<ProbeUiState> = combine(repository.status, refresh) { status, _ -> status }
        .flatMapLatest { status ->
            flow {
                if (!status.running) {
                    emit(ProbeUiState.Failed("gateway_not_running", "网关未启动"))
                    return@flow
                }
                emit(ProbeUiState.Loading)
                try {
                    emit(
                        withContext(Dispatchers.IO) {
                            // 两组各自兜底：匿名组炸了不影响会话组的卡片，反之亦然。
                            val anonymous = try {
                                client.fetchAnonymous(status)
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                failedAnonymousReport(e)
                            }
                            val session = try {
                                client.fetchSession(status)
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                failedSessionReport(e)
                            }
                            ProbeUiState.Ready(
                                anonymous = anonymous,
                                session = session,
                                sampledAtMs = System.currentTimeMillis(),
                            )
                        },
                    )
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    emit(ProbeUiState.Failed(e.code, e.message))
                } catch (_: Exception) {
                    emit(ProbeUiState.Failed("unknown", "探针检查失败，请重试"))
                }
            }
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), ProbeUiState.Idle)

    fun refresh() {
        refresh.value++
    }
}
