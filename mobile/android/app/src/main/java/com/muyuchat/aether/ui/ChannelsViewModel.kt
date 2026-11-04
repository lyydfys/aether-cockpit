package com.muyuchat.aether.ui

import android.app.Application
import android.content.ContentResolver
import android.net.Uri
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.ACCOUNT_BUNDLE_MAX_BYTES
import com.muyuchat.aether.runtime.AccountBundleException
import com.muyuchat.aether.runtime.AccountBundlePreview
import com.muyuchat.aether.runtime.AccountCheckin
import com.muyuchat.aether.runtime.AccountQuotaApiClient
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.AccountSettingsPatch
import com.muyuchat.aether.runtime.BundleImportResult
import com.muyuchat.aether.runtime.ChannelProbe
import com.muyuchat.aether.runtime.ChannelToggleApiClient
import com.muyuchat.aether.runtime.ChannelProbeApiClient
import com.muyuchat.aether.runtime.CustomChannel
import com.muyuchat.aether.runtime.CustomChannelDraft
import com.muyuchat.aether.runtime.CustomChannelsApiClient
import com.muyuchat.aether.runtime.validateCustomChannelBaseUrl
import com.muyuchat.aether.runtime.validateCustomChannelDraft
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.ProviderModels
import com.muyuchat.aether.runtime.ProviderModelsApiClient
import com.muyuchat.aether.runtime.PresetsView
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.QUOTA_UNSUPPORTED_COPY
import com.muyuchat.aether.runtime.parseAccountBundle
import com.muyuchat.aether.runtime.quotaReadFailure
import com.muyuchat.aether.runtime.withQuotaRead
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import java.io.IOException

sealed interface ChannelsUiState {
    data object Idle : ChannelsUiState
    data object Loading : ChannelsUiState

    /**
     * 页面的两份数据一起发：渠道（提供模型/启停/探测）+ 账号（额度卡片主体）。
     * 渠道启停、账号启停、签到成功后都用**服务端确认过的**结果就地更新，
     * 不回到 [Loading]——否则整页重建会把用户刚点的那张卡甩出屏幕。
     */
    data class Ready(
        val providers: List<ProviderSummary>,
        val accounts: List<AccountQuotaRow> = emptyList(),
        val accountAction: AccountActionUiState = AccountActionUiState.Idle,
        val batch: BatchUiState = BatchUiState.Idle,
        /** 每个账号"上一次读取额度"的瞬态结果；只有 `quotaReadAt == 0` 时页面才用得上。 */
        val quotaReadOutcomes: Map<String, QuotaReadOutcome> = emptyMap(),
        /** 账号详情/标签/备注/预设的进行态（失败贴在对话框里，不贴卡片）。 */
        val accountEdit: AccountEditUiState = AccountEditUiState.Idle,
        /** 预设列表的加载态。 */
        val presets: PresetsUiState = PresetsUiState.Idle,
    ) : ChannelsUiState

    data class Failed(val code: String, val message: String) : ChannelsUiState
}

/** 账号级动作（停用·启用、立即签到、读额度）的进行/失败态，只影响对应的那张卡。 */
sealed interface AccountActionUiState {
    data object Idle : AccountActionUiState
    data class Busy(val accountId: String, val kind: AccountActionKind) : AccountActionUiState

    /**
     * 失败必须带上 [kind]：三种动作共用一条错误通道，但**页面处置不同**——
     * 只有 [AccountActionKind.QUOTA] 的失败会进额度区（`读取失败：…`），
     * 启停/签到的失败仍走卡片底部那条通用红字，否则会把开关失败误标成"读取失败"。
     */
    data class Failed(
        val accountId: String,
        val code: String,
        val message: String,
        val kind: AccountActionKind,
    ) : AccountActionUiState
}

enum class AccountActionKind {
    TOGGLE,
    CHECKIN,

    /** 单账号「读一次额度」（`POST /api/accounts/{id}/refresh`）。 */
    QUOTA,

    /** 立即唤醒（`POST /api/wake/run`；桌面 `channels.js:427`）。 */
    WAKE,

    /** 删除账号（`DELETE /api/accounts/{id}`；桌面 `channels.js:315`）。 */
    DELETE,
}

/** 删除成功后的收尾步骤。 */
internal enum class AccountDeleteStep {
    /** 先把这一行从列表里移除（用户立刻看到生效，不闪烁）。 */
    RemoveRow,

    /** 再整页重拉：纠正 `readyCount`、概览「可用账号」、路由组成员数这些联动数字。 */
    RefreshAll,
}

/**
 * 删除成功后的收尾顺序：**先移除、后刷新**。
 *
 * 为什么必须这个顺序：只删行会让卡片数字停在旧值（渠道 readyCount / 概览可用账号 / 路由组人数）；
 * 只整页刷新会让用户等一个网络往返才看到变化。先移除给即时反馈，再刷新让服务端数字追上来——
 * 中间那一瞬间列表里已经没有这条，不会出现"行还在但数字变了"的闪烁。
 */
internal fun accountDeleteFollowUp(): List<AccountDeleteStep> =
    listOf(AccountDeleteStep.RemoveRow, AccountDeleteStep.RefreshAll)

/**
 * 批量动作类别。**端点不同，别混**：
 * - [REFRESH] 走真批量端点 `POST /api/accounts/batch`（`action:"refresh"`）；
 * - [ENABLE]/[DISABLE] 服务端没有批量启停，逐个打 `POST /api/accounts/{id}/settings`；
 * - [CHECKIN] 服务端没有批量签到，逐个打 `POST /api/accounts/{id}/checkin`。
 */
enum class BatchKind {
    REFRESH,
    ENABLE,
    DISABLE,
    CHECKIN,
}

/** 批量动作进行/结果的真实计数（全部来自逐条回包，不推断、不美化）。 */
sealed interface BatchUiState {
    data object Idle : BatchUiState
    data class Running(val kind: BatchKind, val total: Int) : BatchUiState
    data class Done(
        val kind: BatchKind,
        val ok: Int,
        val failed: Int,
        val total: Int,
        val firstError: String,
    ) : BatchUiState

    data class Failed(val kind: BatchKind, val code: String, val message: String) : BatchUiState
}

/**
 * 账号级编辑动作（对齐桌面 channels.js 的账号详情/标签/备注/预设）。
 *
 * 与 [AccountActionKind] 分开：卡片动作的失败贴在卡片上，编辑动作的失败要贴在**对话框里**
 * （用户正对着那个表单），所以用独立状态流与独立文案。
 */
enum class AccountEditKind {
    DETAILS,
    TAGS,
    NOTE,
    MAPPING,
    PRESETS,
}

/** 一次账号编辑的进行/失败态；成功后回到 [Idle]（卡片已用服务端回包就地更新）。 */
sealed interface AccountEditUiState {
    data object Idle : AccountEditUiState
    data class Saving(val accountId: String, val kind: AccountEditKind) : AccountEditUiState
    data class Failed(
        val accountId: String,
        val kind: AccountEditKind,
        val code: String,
        val message: String,
    ) : AccountEditUiState
}

/** `GET /api/presets` 的加载态（预设弹窗打开时才拉一次）。 */
sealed interface PresetsUiState {
    data object Idle : PresetsUiState
    data object Loading : PresetsUiState
    data class Ready(val view: PresetsView) : PresetsUiState
    data class Failed(val code: String, val message: String) : PresetsUiState
}

sealed interface ChannelsAction {
    data object Refresh : ChannelsAction

    /** [disabled] 是**目标状态**（true = 停用），直接来自开关的 checked 取反。 */
    data class SetAccountDisabled(val accountId: String, val disabled: Boolean) : ChannelsAction

    data class CheckinAccount(val accountId: String) : ChannelsAction

    /** 单账号读一次额度：只用这一个账号的 id，不牵动别的卡片。 */
    data class ReadAccountQuota(val accountId: String) : ChannelsAction

    /** 立即唤醒：`POST /api/wake/run`，发一个最小请求提前触发额度重置。 */
    data class WakeAccount(val accountId: String) : ChannelsAction

    /** 删除账号：`DELETE /api/accounts/{id}`，只移除本机记录。 */
    data class DeleteAccount(val accountId: String) : ChannelsAction

    /**
     * 渠道顺序：`POST /api/providers/arrange {"order":[…]}`。
     *
     * 手机端用上移/下移代替桌面拖拽（触屏长列表 + 1.3× 字号下拖拽容易误触），
     * 提交的是**完整顺序**，服务端只保留认识的 id。
     */
    data class ArrangeProviders(val order: List<String>) : ChannelsAction

    /** 账号详情/设置：只把改动过的字段带上去（服务端缺省=不修改）。 */
    data class UpdateAccountDetails(val accountId: String, val patch: AccountSettingsPatch) : ChannelsAction

    /** 标签：传完整列表，空列表 = 清空。 */
    data class SetAccountTags(val accountId: String, val tags: List<String>) : ChannelsAction

    /** 备注：空字符串 = 清空。 */
    data class SetAccountNote(val accountId: String, val note: String) : ChannelsAction

    /**
     * 账号级模型映射（`POST /api/provider/accountmapping`）。
     *
     * `mapping` 是**整表替换**的目标状态（不是增量），空 map = 清除映射。
     */
    data class SetAccountMapping(
        val providerId: String,
        val accountId: String,
        val mapping: Map<String, String>,
    ) : ChannelsAction

    /** 打开预设弹窗前拉一次 `GET /api/presets`。 */
    data object LoadPresets : ChannelsAction

    /** 把预设挂到渠道上；空 ids = 解绑该渠道全部预设。 */
    data class AttachPresets(val providerId: String, val ids: List<String>) : ChannelsAction

    data class BatchRefresh(val ids: List<String>) : ChannelsAction
    data class BatchSetDisabled(val ids: List<String>, val disabled: Boolean) : ChannelsAction
    data class BatchCheckin(val ids: List<String>) : ChannelsAction
    data object DismissBatch : ChannelsAction
}

/** Local-only bundle preview; nothing is written to the account pool. */
sealed interface BundlePreviewUiState {
    data object Idle : BundlePreviewUiState
    data object Working : BundlePreviewUiState
    data class Ready(val preview: AccountBundlePreview) : BundlePreviewUiState
    data class Failed(val code: String, val message: String) : BundlePreviewUiState
}

/** Explicit import action; writes real accounts through the local gateway. */
sealed interface BundleImportUiState {
    data object Idle : BundleImportUiState
    data object Working : BundleImportUiState
    data class Done(val result: BundleImportResult) : BundleImportUiState
    data class Failed(val code: String, val message: String) : BundleImportUiState
}

/** Qoder browser login; the gateway polls its own device session. */
sealed interface QoderLoginUiState {
    data object Idle : QoderLoginUiState
    data object Starting : QoderLoginUiState
    data class Waiting(val id: String, val loginUrl: String, val expiresIn: Int) : QoderLoginUiState
    data class Done(val account: String) : QoderLoginUiState
    data class Failed(val code: String, val message: String) : QoderLoginUiState
}

/**
 * Read-only channel projection. The admin session stays inside the status flow
 * used for the request; it never enters [ChannelsUiState].
 */
sealed interface ProviderModelsUiState {
    data object Loading : ProviderModelsUiState
    data class Ready(val models: ProviderModels) : ProviderModelsUiState
    data class Failed(val code: String, val message: String) : ProviderModelsUiState
}

/**
 * 上一次「读取额度」的结果（**瞬态**，只在内存里，不落盘）。
 *
 * 用途只有一个：`quota.read_at == 0` 时区分"从来没点过"与"点了但上游没给额度"——
 * 真机上就出现过 `ok` + `readAt=无`（凭据续期 45s 后没读到），而界面上和"没点过"长得一样。
 */
enum class QuotaReadOutcome {
    /** 这次真的读到了额度（回包里带 `read_at`）。 */
    OK_WITH_QUOTA,

    /** 请求成功回来，但没读到额度（`quota_read=false` / 回包没有 `read_at`）。 */
    OK_WITHOUT_QUOTA,

    /** 这次读取失败（网络/HTTP/上游错误），失败原因另有红字通道显示。 */
    FAILED,
}

class ChannelsViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ManagementApiClient()
    private val refresh = MutableStateFlow(0)

    /**
     * 就地更新的渠道列表：启停成功后服务端已在响应体里回了完整渠道列表，
     * 直接用它可**避免走 Loading**——否则整页刷新会把用户刚点的那一行甩到屏外。
     * 置 null 表示"下次走真实拉取"。
     */
    private val providerOverride = MutableStateFlow<List<ProviderSummary>?>(null)

    /** 账号与额度：`GET /api/accounts`，与渠道列表同一次刷新里取。 */
    private val accountsClient = AccountQuotaApiClient()
    private val accountsOverride = MutableStateFlow<List<AccountQuotaRow>?>(null)
    private val accountAction = MutableStateFlow<AccountActionUiState>(AccountActionUiState.Idle)

    /** 批量动作的进行/结果态；页面据此显示「成功 N / 失败 M（原因）」。 */
    private val batch = MutableStateFlow<BatchUiState>(BatchUiState.Idle)
    private var batchJob: Job? = null

    /**
     * 每个账号"上一次读取额度"的结果（瞬态）。
     *
     * 只有 `quota.read_at == 0` 时才被页面用上：用来把"点了但没读到"与"从来没点过"分开。
     * 不做时间戳推断、不落盘——重启或重进页面后自然消失，页面就退回"从未读过"。
     */
    private val quotaReadOutcomes = MutableStateFlow<Map<String, QuotaReadOutcome>>(emptyMap())

    /** 账号编辑（详情/标签/备注/预设）的进行态；一次只允许一个编辑动作。 */
    private val accountEdit = MutableStateFlow<AccountEditUiState>(AccountEditUiState.Idle)

    /** 预设列表；打开弹窗时才拉。 */
    private val presets = MutableStateFlow<PresetsUiState>(PresetsUiState.Idle)
    private data class ChannelsInput(
        val status: GatewayStatus,
        val providers: List<ProviderSummary>?,
        val accounts: List<AccountQuotaRow>?,
        val action: AccountActionUiState,
        val batch: BatchUiState,
        val readOutcomes: Map<String, QuotaReadOutcome>,
        val edit: AccountEditUiState,
        val presets: PresetsUiState,
    )

    /** 五个"就地状态"先合成一路：`combine` 的强类型重载最多 5 个 flow。 */
    private data class CachedInputs(
        val providers: List<ProviderSummary>?,
        val accounts: List<AccountQuotaRow>?,
        val action: AccountActionUiState,
        val batch: BatchUiState,
        val readOutcomes: Map<String, QuotaReadOutcome>,
    )

    /** 再叠上"编辑态/预设态"这一层（同样是 5 个 flow 上限内的 3 个）。 */
    private data class EditInputs(
        val cached: CachedInputs,
        val edit: AccountEditUiState,
        val presets: PresetsUiState,
    )

    private val cachedInputs: Flow<CachedInputs> =
        combine(providerOverride, accountsOverride, accountAction, batch, quotaReadOutcomes) { providers, accounts, action, batchState, outcomes ->
            CachedInputs(providers, accounts, action, batchState, outcomes)
        }

    private val editInputs: Flow<EditInputs> =
        combine(cachedInputs, accountEdit, presets) { cached, edit, presetsState ->
            EditInputs(cached, edit, presetsState)
        }

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<ChannelsUiState> =
        combine(repository.status, refresh, editInputs) { status, _, inputs ->
            ChannelsInput(
                status = status,
                providers = inputs.cached.providers,
                accounts = inputs.cached.accounts,
                action = inputs.cached.action,
                batch = inputs.cached.batch,
                readOutcomes = inputs.cached.readOutcomes,
                edit = inputs.edit,
                presets = inputs.presets,
            )
        }
            .flatMapLatest { input ->
            flow {
                if (!input.status.running) {
                    emit(ChannelsUiState.Idle)
                    return@flow
                }
                // 两份数据都在手上就直接发 Ready：不发 Loading，列表不被重建，滚动位置得以保留。
                val cachedProviders = input.providers
                val cachedAccounts = input.accounts
                if (cachedProviders != null && cachedAccounts != null) {
                    emit(
                        ChannelsUiState.Ready(
                            cachedProviders,
                            cachedAccounts,
                            input.action,
                            input.batch,
                            input.readOutcomes,
                    input.edit,
                    input.presets,
                        ),
                    )
                    return@flow
                }
                emit(ChannelsUiState.Loading)
                try {
                    val (providers, accounts) = withContext(Dispatchers.IO) {
                        val p = cachedProviders ?: client.fetchProviders(input.status)
                        val a = cachedAccounts ?: accountsClient.fetchAccounts(input.status)
                        p to a
                    }
                    emit(
                        ChannelsUiState.Ready(
                            providers,
                            accounts,
                            input.action,
                            input.batch,
                            input.readOutcomes,
                            input.edit,
                            input.presets,
                        ),
                    )
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    emit(ChannelsUiState.Failed(e.code, e.message))
                } catch (_: Exception) {
                    emit(ChannelsUiState.Failed("unknown", "无法读取本机渠道与账号，请重试"))
                }
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), ChannelsUiState.Idle)

    private val _bundlePreview = MutableStateFlow<BundlePreviewUiState>(BundlePreviewUiState.Idle)
    val bundlePreview: StateFlow<BundlePreviewUiState> = _bundlePreview.asStateFlow()
    private val _bundleImport = MutableStateFlow<BundleImportUiState>(BundleImportUiState.Idle)
    val bundleImport: StateFlow<BundleImportUiState> = _bundleImport.asStateFlow()
    private var bundleJob: Job? = null
    private var bundleRequest = 0
    private var importJob: Job? = null
    private var importRequest = 0
    private var sealedBytes: ByteArray? = null
    private val _qoderLogin = MutableStateFlow<QoderLoginUiState>(QoderLoginUiState.Idle)
    val qoderLogin: StateFlow<QoderLoginUiState> = _qoderLogin.asStateFlow()
    private var qoderJob: Job? = null
    private var qoderRequest = 0

    // —— 渠道连通性探测：服务端探、手机端只发起与读结果 ——
    private val probeClient = ChannelProbeApiClient()
    private val _probes = MutableStateFlow<Map<String, ChannelProbe>>(emptyMap())
    val probes: StateFlow<Map<String, ChannelProbe>> = _probes.asStateFlow()
    private val _probing = MutableStateFlow<String?>(null)
    val probing: StateFlow<String?> = _probing.asStateFlow()
    private var probeJob: Job? = null

    /** 只读上次探测结果；不主动触发（避免一进页面就打上游）。 */
    fun refreshProbes() {
        val status = repository.status.value
        if (!status.running) {
            _probes.value = emptyMap()
            return
        }
        viewModelScope.launch {
            val report = runCatching {
                withContext(Dispatchers.IO) { probeClient.fetchStatus(status) }
            }.getOrNull() ?: return@launch
            _probes.value = report.byProvider().mapValues { it.value.first() }
        }
    }

    /**
     * 对某个渠道发起一次探测。
     *
     * 服务端是**异步**的（回 `{reading:true}`，结果走 status 轮询），所以这里先发起、
     * 再按 1.2 秒轮询直到 `reading=false`；超时也如实停在"仍在探测"，不假装完成。
     */
    fun testProvider(providerId: String) {
        if (providerId.isBlank() || _probing.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            return
        }
        _probing.value = providerId
        probeJob?.cancel()
        probeJob = viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { probeClient.startCheck(status, providerId) }
                var waited = 0
                while (waited < 24) {
                    delay(1_200)
                    waited++
                    val report = runCatching {
                        withContext(Dispatchers.IO) { probeClient.fetchStatus(status) }
                    }.getOrNull() ?: continue
                    _probes.value = report.byProvider().mapValues { it.value.first() }
                    if (!report.reading) {
                        break
                    }
                }
            } finally {
                _probing.value = null
            }
        }
    }

    // ——— 渠道启停：写操作，必须二次确认；成功由服务端返回确认，不乐观更新 ———
    private val toggleClient = ChannelToggleApiClient()
    private val _toggling = MutableStateFlow<String?>(null)
    val toggling: StateFlow<String?> = _toggling.asStateFlow()
    private val _toggleError = MutableStateFlow<Pair<String, String>?>(null)
    val toggleError: StateFlow<Pair<String, String>?> = _toggleError.asStateFlow()

    fun dismissToggleError() {
        _toggleError.value = null
    }

    fun toggleProvider(providerId: String, enable: Boolean) {
        if (providerId.isBlank() || _toggling.value != null) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _toggleError.value = "gateway_not_running" to "网关未启动"
            return
        }
        _toggling.value = providerId
        _toggleError.value = null
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { toggleClient.toggle(status, providerId, enable) }
                // 只有服务端确认后才算成功。列表优先用响应体里的完整渠道列表就地更新；
                // 拿不到（解析失败）才回退到整页刷新。
                val providers = result.providers
                if (providers != null) {
                    // 先把账号那半也锁进 override，否则 Ready 会因缺一半而退回 Loading 重建整页。
                    lockCurrentRows()
                    providerOverride.value = providers
                } else {
                    onAction(ChannelsAction.Refresh)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _toggleError.value = e.code to e.message
            } catch (_: Exception) {
                _toggleError.value = "unknown" to "切换渠道失败，请重试"
            } finally {
                _toggling.value = null
            }
        }
    }
    // ——— 渠道模型清单：只发 id，不带 key；服务端没有 vendor 端点时退回内置清单 ———
    private val modelsClient = ProviderModelsApiClient()
    private val _models = MutableStateFlow<Map<String, ProviderModelsUiState>>(emptyMap())
    val models: StateFlow<Map<String, ProviderModelsUiState>> = _models.asStateFlow()

    fun loadModels(providerId: String) {
        if (providerId.isBlank()) {
            return
        }
        if (_models.value[providerId] is ProviderModelsUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _models.value = _models.value + (providerId to ProviderModelsUiState.Failed("gateway_not_running", "网关未启动"))
            return
        }
        _models.value = _models.value + (providerId to ProviderModelsUiState.Loading)
        viewModelScope.launch {
            val next = try {
                val result = withContext(Dispatchers.IO) { modelsClient.fetchModels(status, providerId) }
                ProviderModelsUiState.Ready(result)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                ProviderModelsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                ProviderModelsUiState.Failed("unknown", "读取模型清单失败，请重试")
            }
            _models.value = _models.value + (providerId to next)
        }
    }

    fun dismissModels(providerId: String) {
        _models.value = _models.value - providerId
    }
    fun onAction(action: ChannelsAction) {
        when (action) {
            ChannelsAction.Refresh -> invalidate()
            is ChannelsAction.SetAccountDisabled -> setAccountDisabled(action.accountId, action.disabled)
            is ChannelsAction.CheckinAccount -> checkinAccount(action.accountId)
            is ChannelsAction.ReadAccountQuota -> readAccountQuota(action.accountId)
            is ChannelsAction.WakeAccount -> wakeAccount(action.accountId)
            is ChannelsAction.DeleteAccount -> deleteAccount(action.accountId)
            is ChannelsAction.ArrangeProviders -> arrangeProviders(action.order)
            is ChannelsAction.UpdateAccountDetails -> updateAccountDetails(action.accountId, action.patch)
            is ChannelsAction.SetAccountTags -> setAccountTags(action.accountId, action.tags)
            is ChannelsAction.SetAccountNote -> setAccountNote(action.accountId, action.note)
            is ChannelsAction.SetAccountMapping ->
                setAccountMapping(action.providerId, action.accountId, action.mapping)
            ChannelsAction.LoadPresets -> loadPresets()
            is ChannelsAction.AttachPresets -> attachPresets(action.providerId, action.ids)
            is ChannelsAction.BatchRefresh -> runBatch(BatchKind.REFRESH, action.ids)
            is ChannelsAction.BatchSetDisabled ->
                runBatch(if (action.disabled) BatchKind.DISABLE else BatchKind.ENABLE, action.ids)
            is ChannelsAction.BatchCheckin -> runBatch(BatchKind.CHECKIN, action.ids)
            ChannelsAction.DismissBatch -> dismissBatch()
        }
    }

    /**
     * 批量动作总入口。
     *
     * 三条路径的端点是**不同**的，见 [BatchKind]；结果只从逐条回包数出来，
     * 全成功也不写"全部成功"，永远显示「成功 N / 总数」。
     */
    fun runBatch(kind: BatchKind, ids: List<String>) {
        if (ids.isEmpty() || batch.value is BatchUiState.Running) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            batch.value = BatchUiState.Failed(kind, "gateway_not_running", "网关未启动")
            return
        }
        batchJob?.cancel()
        batch.value = BatchUiState.Running(kind, ids.size)
        batchJob = viewModelScope.launch {
            try {
                val outcome = withContext(Dispatchers.IO) {
                    when (kind) {
                        BatchKind.REFRESH -> accountsClient.batchRefresh(status, ids)
                        BatchKind.ENABLE -> accountsClient.batchSetDisabled(status, ids, disabled = false)
                        BatchKind.DISABLE -> accountsClient.batchSetDisabled(status, ids, disabled = true)
                        BatchKind.CHECKIN -> accountsClient.batchCheckin(status, ids)
                    }
                }
                if (outcome.updatedRows.isNotEmpty()) {
                    mergeAccountRows(outcome.updatedRows)
                }
                if (outcome.checkins.isNotEmpty()) {
                    mergeCheckinRows(outcome.checkins)
                }
                if (kind == BatchKind.REFRESH) {
                    // 额度读数变了但批量回包不带新值：重拉账号列表（渠道列表留在 override，不整页重建）。
                    refetchAccounts()
                }
                batch.value = BatchUiState.Done(
                    kind = kind,
                    ok = outcome.ok,
                    failed = outcome.failed,
                    total = outcome.total,
                    firstError = outcome.firstError,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                batch.value = BatchUiState.Failed(kind, e.code, e.message)
            } catch (_: Exception) {
                batch.value = BatchUiState.Failed(kind, "unknown", "批量操作失败，请重试")
            }
        }
    }

    fun dismissBatch() {
        batchJob?.cancel()
        batchJob = null
        batch.value = BatchUiState.Idle
    }

    /** 丢掉所有就地缓存，下次真实拉取（导入账号/登录成功后必须走这条，否则新账号不出现）。 */
    private fun invalidate() {
        providerOverride.value = null
        accountsOverride.value = null
        refresh.value++
    }

    // ——— 账号卡片上的两个写操作：账号启停、立即签到 ———

    /**
     * 账号级停用/启用。
     *
     * 只有一个真端点：`POST /api/accounts/{id}/settings` `{"disabled":bool}`
     * （`accounts_api.go:71-133`，回 `{"account":<snapshot>}`）。服务端确认后就地替换那一行，
     * 不做乐观更新——失败时开关必须停在原位并把服务端原文显示出来。
     */
    fun setAccountDisabled(accountId: String, disabled: Boolean) {
        if (accountId.isBlank() || accountAction.value is AccountActionUiState.Busy) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "gateway_not_running",
                "网关未启动",
                AccountActionKind.TOGGLE,
            )
            return
        }
        accountAction.value = AccountActionUiState.Busy(accountId, AccountActionKind.TOGGLE)
        viewModelScope.launch {
            try {
                val updated = withContext(Dispatchers.IO) { accountsClient.setDisabled(status, accountId, disabled) }
                mergeAccountRow(updated)
                accountAction.value = AccountActionUiState.Idle
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountAction.value = AccountActionUiState.Failed(accountId, e.code, e.message, AccountActionKind.TOGGLE)
            } catch (_: Exception) {
                accountAction.value = AccountActionUiState.Failed(
                    accountId,
                    "unknown",
                    "切换账号状态失败，请重试",
                    AccountActionKind.TOGGLE,
                )
            }
        }
    }

    /**
     * 立即签到：`POST /api/accounts/{id}/checkin`（`admin.go:543-612`）。
     * 服务端回今日签到卡；回包没带状态时退回一次账号重拉，不猜结果。
     */
    fun checkinAccount(accountId: String) {
        if (accountId.isBlank() || accountAction.value is AccountActionUiState.Busy) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "gateway_not_running",
                "网关未启动",
                AccountActionKind.CHECKIN,
            )
            return
        }
        accountAction.value = AccountActionUiState.Busy(accountId, AccountActionKind.CHECKIN)
        viewModelScope.launch {
            try {
                val ack = withContext(Dispatchers.IO) { accountsClient.checkin(status, accountId) }
                val current = currentAccountRows().firstOrNull { it.accountId == accountId }
                if (ack != null && current != null) {
                    mergeAccountRow(current.copy(checkin = ack))
                } else if (current == null) {
                    // 没有缓存行可改：只让账号列表重拉（渠道列表仍在 override 里，不会整页重建）。
                    refetchAccounts()
                }
                accountAction.value = AccountActionUiState.Idle
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountAction.value = AccountActionUiState.Failed(accountId, e.code, e.message, AccountActionKind.CHECKIN)
            } catch (_: Exception) {
                accountAction.value = AccountActionUiState.Failed(
                    accountId,
                    "unknown",
                    "签到失败，请重试",
                    AccountActionKind.CHECKIN,
                )
            }
        }
    }

    /**
     * 单账号「读一次额度」。
     *
     * 端点：`POST /api/accounts/{id}/refresh`（`auth_api.go:290-353`，续期凭证 + 同步读额度）。
     * 回包自带 `quota` 快照，所以成功后**就地**把那一行的额度字段换掉，不整页重建。
     *
     * 同一时刻只允许一个账号在读取：其它卡片的「读取额度」按钮据此禁用。
     * 渠道 `quota_supported == false` 时一次请求都不发，直接给「该渠道不提供额度接口」，
     * 与"还没读过"在文案上区分开。
     */
    fun readAccountQuota(accountId: String) {
        if (accountId.isBlank() || accountAction.value is AccountActionUiState.Busy) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "gateway_not_running",
                "网关未启动",
                AccountActionKind.QUOTA,
            )
            return
        }
        val row = currentAccountRows().firstOrNull { it.accountId == accountId }
        if (row != null && !row.quotaSupported) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "quota_unsupported",
                QUOTA_UNSUPPORTED_COPY,
                AccountActionKind.QUOTA,
            )
            return
        }
        accountAction.value = AccountActionUiState.Busy(accountId, AccountActionKind.QUOTA)
        // 一次性诊断日志：真机点过按钮却既没"最近读数"也没"读取失败"时，
        // 只能靠这三行判断「请求到底有没有发出去 / 回包到没到」。日志不加任何新行为。
        Log.i(QUOTA_READ_LOG_TAG, quotaReadLogLine(QuotaReadLog.Start(accountId)))
        val startedAtMs = System.currentTimeMillis()
        viewModelScope.launch {
            try {
                val read = withContext(Dispatchers.IO) { accountsClient.readQuota(status, accountId) }
                if (row != null) {
                    mergeAccountRow(row.withQuotaRead(read))
                } else {
                    // 没有可比对的行（列表还没到）：让它去重拉，别丢掉读到的结果。
                    refetchAccounts()
                }
                // "这次到底读到没有"只认回包里的 read_at：服务端读到额度时一定会写它
                //（`usage_quota_api.go:473-475` 在 ReadAt==0 时补 now），没有就是没读到。
                val readAtSec = read.snapshot?.readAt ?: 0L
                recordQuotaReadOutcome(
                    accountId,
                    if (readAtSec > 0L) QuotaReadOutcome.OK_WITH_QUOTA else QuotaReadOutcome.OK_WITHOUT_QUOTA,
                )
                Log.i(
                    QUOTA_READ_LOG_TAG,
                    quotaReadLogLine(
                        QuotaReadLog.Ok(
                            accountId = accountId,
                            elapsedMs = System.currentTimeMillis() - startedAtMs,
                            hasReadAt = readAtSec > 0L,
                        ),
                    ),
                )
                val failure = quotaReadFailure(read)
                accountAction.value = if (failure == null) {
                    AccountActionUiState.Idle
                } else {
                    AccountActionUiState.Failed(accountId, failure.first, failure.second, AccountActionKind.QUOTA)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                recordQuotaReadOutcome(accountId, QuotaReadOutcome.FAILED)
                Log.i(
                    QUOTA_READ_LOG_TAG,
                    quotaReadLogLine(
                        QuotaReadLog.Fail(
                            accountId = accountId,
                            code = e.code,
                            elapsedMs = System.currentTimeMillis() - startedAtMs,
                            errorType = null,
                        ),
                    ),
                )
                accountAction.value = AccountActionUiState.Failed(accountId, e.code, e.message, AccountActionKind.QUOTA)
            } catch (e: Exception) {
                recordQuotaReadOutcome(accountId, QuotaReadOutcome.FAILED)
                Log.i(
                    QUOTA_READ_LOG_TAG,
                    quotaReadLogLine(
                        QuotaReadLog.Fail(
                            accountId = accountId,
                            code = "unknown",
                            elapsedMs = System.currentTimeMillis() - startedAtMs,
                            // 只记类型名：异常 message 里可能带 URL / 主机名。
                            errorType = e::class.simpleName.orEmpty(),
                        ),
                    ),
                )
                accountAction.value = AccountActionUiState.Failed(
                    accountId,
                    "unknown",
                    "读取额度失败，请重试",
                    AccountActionKind.QUOTA,
                )
            }
        }
    }

    /**
     * 立即唤醒：`POST /api/wake/run {"account_id":…}`（桌面 `channels.js:427`）。
     *
     * 服务端不返回账号快照，所以成功后重拉一次账号（额度读数会变）。
     * 失败进 [AccountActionKind.WAKE]，卡片按通用红字显示，不误标成"读取失败"。
     */
    fun wakeAccount(accountId: String) {
        if (accountId.isBlank() || accountAction.value is AccountActionUiState.Busy) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "gateway_not_running",
                "网关未启动",
                AccountActionKind.WAKE,
            )
            return
        }
        accountAction.value = AccountActionUiState.Busy(accountId, AccountActionKind.WAKE)
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { accountsClient.wakeAccount(status, accountId) }
                accountAction.value = AccountActionUiState.Idle
                reloadAccountRows(status)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountAction.value = AccountActionUiState.Failed(accountId, e.code, e.message, AccountActionKind.WAKE)
            } catch (_: Exception) {
                accountAction.value = AccountActionUiState.Failed(
                    accountId,
                    "unknown",
                    "唤醒失败，请重试",
                    AccountActionKind.WAKE,
                )
            }
        }
    }

    /**
     * 删除账号：`DELETE /api/accounts/{id}`（桌面 `channels.js:313-317`）。
     *
     * 成功后按 [accountDeleteFollowUp] 收尾：**先移除行**（即时反馈），**再整页重拉**
     * （纠正 `readyCount` / 概览「可用账号」/ 路由组成员数）。失败保留行并给通用红字。
     * 二次确认由页面负责——这里只执行，不重复问。
     */
    fun deleteAccount(accountId: String) {
        if (accountId.isBlank() || accountAction.value is AccountActionUiState.Busy) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId,
                "gateway_not_running",
                "网关未启动",
                AccountActionKind.DELETE,
            )
            return
        }
        accountAction.value = AccountActionUiState.Busy(accountId, AccountActionKind.DELETE)
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { accountsClient.deleteAccount(status, accountId) }
                // 顺序不能反：先移除行是即时反馈，再整页重拉纠正联动数字
                //（`readyCount`、概览「可用账号」、路由组成员数）。见 [accountDeleteFollowUp]。
                accountDeleteFollowUp().forEach { step ->
                    when (step) {
                        AccountDeleteStep.RemoveRow -> removeAccountRow(accountId)
                        AccountDeleteStep.RefreshAll -> invalidate()
                    }
                }
                accountAction.value = AccountActionUiState.Idle
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountAction.value = AccountActionUiState.Failed(accountId, e.code, e.message, AccountActionKind.DELETE)
            } catch (_: Exception) {
                accountAction.value = AccountActionUiState.Failed(
                    accountId,
                    "unknown",
                    "删除失败，请重试",
                    AccountActionKind.DELETE,
                )
            }
        }
    }

    /** 从当前渲染的行里移除一条（先锁住 override，避免与 Ready 快照不同步）。 */
    private fun removeAccountRow(accountId: String) {
        lockCurrentRows()
        accountsOverride.value = currentAccountRows().filterNot { it.accountId == accountId }
    }

    /**
     * 渠道顺序：`POST /api/providers/arrange`（`gateway.go:882`）。
     *
     * 成功后整页重拉（顺序由服务端回包决定，本地不猜）；失败贴服务端原文。
     * 手机端入口是上移/下移（见 `moveProviderOrder`），不是拖拽。
     */
    fun arrangeProviders(order: List<String>) {
        if (order.isEmpty()) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountAction.value = AccountActionUiState.Failed(
                accountId = "",
                code = "gateway_not_running",
                message = "网关未启动",
                kind = AccountActionKind.TOGGLE,
            )
            return
        }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { accountsClient.arrangeProviders(status, order) }
                invalidate()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountAction.value = AccountActionUiState.Failed("", e.code, e.message, AccountActionKind.TOGGLE)
            } catch (_: Exception) {
                accountAction.value = AccountActionUiState.Failed(
                    "",
                    "unknown",
                    "保存渠道顺序失败，请重试",
                    AccountActionKind.TOGGLE,
                )
            }
        }
    }

    /** 唤醒后重拉账号行：服务端没有回快照，只能重新取一次（失败静默，不覆盖已有数据）。 */
    private suspend fun reloadAccountRows(status: GatewayStatus) {
        try {
            val rows = withContext(Dispatchers.IO) { accountsClient.fetchAccounts(status) }
            lockCurrentRows()
            accountsOverride.value = rows
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // 拉不到就保持现状：唤醒已经成功，别用失败提示误导用户。
        }
    }

    // ——— 账号详情 / 标签 / 备注 / 预设（对齐桌面 channels.js 的账号级入口） ———

    /**
     * 账号详情/设置：只把**改动过的字段**发给服务端（`AccountSettingsPatch` 里为 null 的一律不带）。
     * 回包 `{"account":snapshot}` 直接替换那一行，卡片状态立刻一致。
     */
    fun updateAccountDetails(accountId: String, patch: AccountSettingsPatch) {
        if (patch.isEmpty) {
            accountEdit.value = AccountEditUiState.Failed(
                accountId,
                AccountEditKind.DETAILS,
                "empty_patch",
                "没有改动任何字段",
            )
            return
        }
        runAccountEdit(
            accountId = accountId,
            kind = AccountEditKind.DETAILS,
            block = { status -> accountsClient.updateAccountSettings(status, accountId, patch) },
            apply = { row -> mergeAccountRow(row) },
        )
    }

    /** 标签：传完整列表（空列表 = 清空），成功用服务端回显的列表就地更新。 */
    fun setAccountTags(accountId: String, tags: List<String>) {
        runAccountEdit(
            accountId = accountId,
            kind = AccountEditKind.TAGS,
            block = { status -> accountsClient.setAccountTags(status, accountId, tags) },
            apply = { ack ->
                currentAccountRows().firstOrNull { it.accountId == accountId }?.let { row ->
                    mergeAccountRow(row.copy(tags = ack))
                }
            },
        )
    }

    /** 备注：空字符串 = 清空（服务端 `SetNote` 直接覆盖，不区分"没改"）。 */
    fun setAccountNote(accountId: String, note: String) {
        runAccountEdit(
            accountId = accountId,
            kind = AccountEditKind.NOTE,
            block = { status -> accountsClient.setAccountNote(status, accountId, note) },
            apply = { ack ->
                currentAccountRows().firstOrNull { it.accountId == accountId }?.let { row ->
                    mergeAccountRow(row.copy(note = ack))
                }
            },
        )
    }

    /**
     * 账号级模型映射：**整表替换**（`POST /api/provider/accountmapping` → `SetModelMapping`，
     * `provider_api.go:698-717` + `env.go:585-602`）。
     *
     * 提交的 `mapping` 就是用户编辑完的**完整最终状态**（空 map = 清除映射），不是增量；
     * 保存成功后先本地回填这一行，再由 [runAccountEdit] 刷新服务端真值。
     */
    fun setAccountMapping(providerId: String, accountId: String, mapping: Map<String, String>) {
        runAccountEdit(
            accountId = accountId,
            kind = AccountEditKind.MAPPING,
            block = { status -> accountsClient.saveAccountMapping(status, providerId, accountId, mapping) },
            apply = {
                currentAccountRows().firstOrNull { it.accountId == accountId }?.let { row ->
                    mergeAccountRow(row.copy(modelMapping = mapping.filterValues { value -> value.isNotBlank() }))
                }
            },
        )
    }

    /** 挂载预设到渠道；`ids` 为空数组时服务端会解绑该渠道的全部预设。 */
    fun attachPresets(providerId: String, ids: List<String>) {        runAccountEdit(
            accountId = providerId,
            kind = AccountEditKind.PRESETS,
            block = { status -> accountsClient.attachProviderPresets(status, providerId, ids) },
            apply = { view -> presets.value = PresetsUiState.Ready(view) },
        )
    }

    /** 预设列表：打开预设弹窗时拉一次。 */
    fun loadPresets() {
        if (presets.value is PresetsUiState.Loading) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            presets.value = PresetsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        presets.value = PresetsUiState.Loading
        viewModelScope.launch {
            try {
                val view = withContext(Dispatchers.IO) { accountsClient.fetchPresets(status) }
                presets.value = PresetsUiState.Ready(view)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                presets.value = PresetsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                presets.value = PresetsUiState.Failed("unknown", "读取预设失败，请重试")
            }
        }
    }

    /**
     * 账号级编辑的公共壳：单飞（卡片动作或另一个编辑在进行时直接返回）；
     * 失败只落到 [accountEdit]（对话框里显示），不污染卡片上的动作错误。
     */
    private fun <T> runAccountEdit(
        accountId: String,
        kind: AccountEditKind,
        block: (GatewayStatus) -> T,
        apply: (T) -> Unit,
    ) {
        if (
            accountId.isBlank() ||
            accountAction.value is AccountActionUiState.Busy ||
            accountEdit.value is AccountEditUiState.Saving
        ) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            accountEdit.value = AccountEditUiState.Failed(accountId, kind, "gateway_not_running", "网关未启动")
            return
        }
        accountEdit.value = AccountEditUiState.Saving(accountId, kind)
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { block(status) }
                apply(result)
                accountEdit.value = AccountEditUiState.Idle
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                accountEdit.value = AccountEditUiState.Failed(accountId, kind, e.code, e.message)
            } catch (_: Exception) {
                accountEdit.value = AccountEditUiState.Failed(accountId, kind, "unknown", "保存失败，请重试")
            }
        }
    }

    /** 记下这个账号"上一次读取额度"的结果（瞬态，只在内存里，不落盘、不推断时间）。 */
    private fun recordQuotaReadOutcome(accountId: String, outcome: QuotaReadOutcome) {
        quotaReadOutcomes.value = quotaReadOutcomes.value + (accountId to outcome)
    }

    /** 当前已渲染的账号行（override 优先，其次最近一次 Ready 的快照）。 */
    private fun currentAccountRows(): List<AccountQuotaRow> =
        accountsOverride.value ?: (state.value as? ChannelsUiState.Ready)?.accounts.orEmpty()

    /**
     * 就地更新前，先把当前已渲染的渠道/账号锁进 override。
     *
     * 只锁一份会让 [ChannelsUiState.Ready] 的两半不同步，从而退回 Loading 把整页重建。
     */
    private fun lockCurrentRows() {
        val ready = state.value as? ChannelsUiState.Ready ?: return
        if (providerOverride.value == null) {
            providerOverride.value = ready.providers
        }
        if (accountsOverride.value == null) {
            accountsOverride.value = ready.accounts
        }
    }

    private fun mergeAccountRow(updated: AccountQuotaRow) {
        mergeAccountRows(listOf(updated))
    }

    /** 批量启停的逐条回包快照一次性合并；只替换命中的行，其余原样保留。 */
    private fun mergeAccountRows(updated: List<AccountQuotaRow>) {
        lockCurrentRows()
        val rows = accountsOverride.value.orEmpty()
        if (rows.isEmpty()) {
            // 手上没有可比对的行：让它去重拉，而不是把列表替换成几条。
            refetchAccounts()
            return
        }
        val byId = updated.associateBy { it.accountId }
        accountsOverride.value = rows.map { row -> byId[row.accountId] ?: row }
    }

    /** 批量签到回包里的今日卡逐条合并。 */
    private fun mergeCheckinRows(checkins: Map<String, AccountCheckin>) {
        lockCurrentRows()
        val rows = accountsOverride.value.orEmpty()
        if (rows.isEmpty()) {
            refetchAccounts()
            return
        }
        accountsOverride.value = rows.map { row ->
            val ack = checkins[row.accountId]
            if (ack == null) row else row.copy(checkin = ack)
        }
    }

    /** 丢弃账号缓存并重拉；渠道缓存保留，因此不会整页回到 Loading。 */
    private fun refetchAccounts() {
        accountsOverride.value = null
        refresh.value++
    }

    fun previewAccountBundle(uri: Uri, passphrase: String) {
        val request = ++bundleRequest
        bundleJob?.cancel()
        importJob?.cancel()
        importRequest++
        sealedBytes?.fill(0)
        sealedBytes = null
        _bundleImport.value = BundleImportUiState.Idle
        _bundlePreview.value = BundlePreviewUiState.Working
        bundleJob = viewModelScope.launch {
            val outcome = withContext(Dispatchers.IO) {
                runCatching {
                    val bytes = getApplication<Application>().contentResolver.readBundleBytes(uri)
                    bytes to parseAccountBundle(bytes, passphrase)
                }
            }
            if (request != bundleRequest) {
                outcome.getOrNull()?.first?.fill(0)
                return@launch
            }
            outcome.fold(
                onSuccess = { (bytes, preview) ->
                    sealedBytes = bytes
                    _bundlePreview.value = BundlePreviewUiState.Ready(preview)
                },
                onFailure = { error ->
                    if (error is CancellationException) {
                        throw error
                    }
                    _bundlePreview.value = when (error) {
                        is AccountBundleException -> BundlePreviewUiState.Failed(error.code, error.message)
                        else -> BundlePreviewUiState.Failed("file_unreadable", "无法读取所选文件")
                    }
                },
            )
        }
    }

    fun importAccountBundle(passphrase: String) {
        val sealed = sealedBytes
        if (sealed == null) {
            _bundleImport.value = BundleImportUiState.Failed("no_bundle", "请先选择并预览账号包")
            return
        }
        if (_bundleImport.value is BundleImportUiState.Working) {
            return
        }
        val request = ++importRequest
        val status = repository.status.value
        _bundleImport.value = BundleImportUiState.Working
        importJob = viewModelScope.launch {
            val outcome = withContext(Dispatchers.IO) {
                runCatching { client.importAccountBundle(status, sealed, passphrase) }
            }
            if (request != importRequest) {
                return@launch
            }
            outcome.fold(
                onSuccess = { result ->
                    sealedBytes?.fill(0)
                    sealedBytes = null
                    _bundleImport.value = BundleImportUiState.Done(result)
                    invalidate()
                },
                onFailure = { error ->
                    if (error is CancellationException) {
                        throw error
                    }
                    _bundleImport.value = when (error) {
                        is ManagementApiException -> BundleImportUiState.Failed(error.code, error.message)
                        else -> BundleImportUiState.Failed("unknown", "导入账号包失败，请重试")
                    }
                },
            )
        }
    }

    fun startQoderLogin() {
        val current = _qoderLogin.value
        if (current is QoderLoginUiState.Starting || current is QoderLoginUiState.Waiting) {
            return
        }
        val status = repository.status.value
        val request = ++qoderRequest
        qoderJob?.cancel()
        _qoderLogin.value = QoderLoginUiState.Starting
        qoderJob = viewModelScope.launch {
            val started = withContext(Dispatchers.IO) {
                runCatching { client.startQoderLogin(status) }
            }
            if (request != qoderRequest) {
                return@launch
            }
            val failure = started.exceptionOrNull()
            if (failure is CancellationException) {
                throw failure
            }
            val ticket = started.getOrNull()
            if (ticket == null) {
                _qoderLogin.value = qoderFailure(failure)
                return@launch
            }
            if (ticket.state != "pending" && ticket.state != "done") {
                _qoderLogin.value = QoderLoginUiState.Failed(
                    ticket.state,
                    "登录会话未能启动",
                )
                return@launch
            }
            _qoderLogin.value = QoderLoginUiState.Waiting(ticket.id, ticket.loginUrl, ticket.expiresIn)
            pollQoderLogin(status, ticket.id, ticket.interval, request)
        }
    }

    fun cancelQoderLogin() {
        val waiting = _qoderLogin.value as? QoderLoginUiState.Waiting ?: return
        qoderRequest++
        qoderJob?.cancel()
        qoderJob = null
        _qoderLogin.value = QoderLoginUiState.Idle
        viewModelScope.launch {
            withContext(Dispatchers.IO) {
                runCatching { client.cancelQoderLogin(repository.status.value, waiting.id) }
            }
        }
    }

    fun dismissQoderLogin() {
        qoderRequest++
        qoderJob?.cancel()
        qoderJob = null
        _qoderLogin.value = QoderLoginUiState.Idle
    }

    private suspend fun pollQoderLogin(
        status: GatewayStatus,
        id: String,
        intervalSeconds: Int,
        request: Int,
    ) {
        val deadline = System.currentTimeMillis() + 11 * 60 * 1000L
        var failures = 0
        while (System.currentTimeMillis() < deadline) {
            delay(intervalSeconds.coerceIn(1, 10) * 1000L)
            val outcome = withContext(Dispatchers.IO) {
                runCatching { client.pollQoderLogin(status, id) }
            }
            if (request != qoderRequest) {
                return
            }
            val failure = outcome.exceptionOrNull()
            if (failure is CancellationException) {
                throw failure
            }
            val poll = outcome.getOrNull()
            if (poll == null) {
                failures++
                if (failures >= 5) {
                    _qoderLogin.value = qoderFailure(failure)
                    return
                }
                delay(2_000)
                continue
            }
            failures = 0
            when (poll.state) {
                "pending" -> Unit
                "done" -> {
                    _qoderLogin.value = QoderLoginUiState.Done(poll.account)
                    invalidate()
                    return
                }
                "failed", "expired" -> {
                    _qoderLogin.value = QoderLoginUiState.Failed(
                        poll.state,
                        poll.error.ifBlank { "登录未完成，请重试" },
                    )
                    return
                }
                else -> {
                    _qoderLogin.value = QoderLoginUiState.Failed("unknown_state", "登录状态异常")
                    return
                }
            }
        }
        _qoderLogin.value = QoderLoginUiState.Failed("login_expired", "登录超时，请重新开始")
    }

    private fun qoderFailure(error: Throwable?): QoderLoginUiState.Failed =
        when (error) {
            is ManagementApiException -> QoderLoginUiState.Failed(error.code, error.message)
            else -> QoderLoginUiState.Failed("unknown", "Qoder 登录失败，请重试")
        }

    fun dismissBundlePreview() {
        bundleRequest++
        importRequest++
        bundleJob?.cancel()
        importJob?.cancel()
        bundleJob = null
        importJob = null
        sealedBytes?.fill(0)
        sealedBytes = null
        _bundleImport.value = BundleImportUiState.Idle
        _bundlePreview.value = BundlePreviewUiState.Idle
    }
}

private fun ContentResolver.readBundleBytes(uri: Uri): ByteArray {
    val stream = try {
        openInputStream(uri)
    } catch (_: IOException) {
        null
    } catch (_: SecurityException) {
        null
    } ?: throw AccountBundleException("file_unreadable", "无法读取所选文件")
    stream.use { input ->
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(8 * 1024)
        while (true) {
            val read = input.read(buffer)
            if (read < 0) {
                break
            }
            out.write(buffer, 0, read)
            if (out.size() > ACCOUNT_BUNDLE_MAX_BYTES) {
                throw AccountBundleException("file_too_large", "账号包超过 8 MB")
            }
        }
        return out.toByteArray()
    }
}

internal fun filterProviders(providers: List<ProviderSummary>, query: String): List<ProviderSummary> {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return providers
    }
    return providers.filter { provider ->
        provider.name.contains(needle, ignoreCase = true) ||
            provider.id.contains(needle, ignoreCase = true) ||
            provider.description.contains(needle, ignoreCase = true) ||
            provider.accounts.any { account ->
                account.id.contains(needle, ignoreCase = true) ||
                    account.label.contains(needle, ignoreCase = true)
            }
    }
}

// ——— 「读取额度」的一次性诊断日志 ———
//
// 背景：真机上点过「读取额度」后按钮确实进过「读取中…」，但既没出现「最近读数 刚刚」，
// 也没出现「读取失败：…」。网关日志不记 HTTP，外部无法取证，所以这里补三行日志，
// 只为回答一个问题：**请求发出去了吗、回包到了吗**。
//
// 红线：日志里只允许出现脱敏后的账号前缀、耗时、错误码与异常**类型名**。
// 不记完整账号 id、不记 URL、不记任何凭据/token、不记响应体。

/** logcat 标签：`adb logcat -s AetherQuotaRead` 只看这一条链路。 */
internal const val QUOTA_READ_LOG_TAG = "AetherQuotaRead"

/** 一次读取的日志事件（字段都已经是"可以进日志"的形态）。 */
internal sealed interface QuotaReadLog {
    val accountId: String

    /** 发起请求。 */
    data class Start(override val accountId: String) : QuotaReadLog

    /** 回包到了；[hasReadAt] 表示这次是否真的读到了额度（服务端 `quota_read`）。 */
    data class Ok(
        override val accountId: String,
        val elapsedMs: Long,
        val hasReadAt: Boolean,
    ) : QuotaReadLog

    /** 没拿到回包或回包不可用；[errorType] 只放异常类型名（message 可能带 URL/主机名）。 */
    data class Fail(
        override val accountId: String,
        val code: String,
        val elapsedMs: Long,
        val errorType: String?,
    ) : QuotaReadLog
}

/**
 * 账号 id 脱敏：只留前 6 位 + `…`。
 *
 * 完整账号 id 是账号池主键，日志里不出现；连前缀也再过一道 [scrubLogText]，
 * 免得 id 本身带 "token" 之类的字样把日志变成"像凭据"的文本。
 */
internal fun maskAccountId(accountId: String): String {
    val trimmed = accountId.trim()
    if (trimmed.isEmpty()) {
        return "…"
    }
    return scrubLogText(trimmed.take(6)) + "…"
}

/**
 * 错误码的日志形态。
 *
 * `ManagementApiException.code` 里有 `http_<status>`，直接打会把字样 "http" 带进日志，
 * 所以状态码统一写作 `status_<status>`：信息不丢，字样不带。
 */
internal fun logSafeCode(code: String): String {
    val trimmed = code.trim()
    val normalized = if (trimmed.startsWith("http_", ignoreCase = true)) {
        "status_" + trimmed.substring("http_".length)
    } else {
        trimmed
    }
    return scrubLogText(normalized).take(40)
}

/** 兜底脱敏：任何字段只要长得像凭据/URL 就把那一段抹掉。 */
private fun scrubLogText(value: String): String = value
    .replace("https", "***", ignoreCase = true)
    .replace("http", "***", ignoreCase = true)
    .replace("bearer", "***", ignoreCase = true)
    .replace("token", "***", ignoreCase = true)
    .replace("sk-", "***", ignoreCase = true)

/**
 * 拼一行日志。
 *
 * - `start account=3f9a2b…`
 * - `ok account=3f9a2b… ms=1234 readAt=有`
 * - `fail account=3f9a2b… kind=QUOTA code=network_error ms=90000 type=SocketTimeoutException`
 */
internal fun quotaReadLogLine(log: QuotaReadLog): String {
    val account = "account=" + maskAccountId(log.accountId)
    return when (log) {
        is QuotaReadLog.Start -> "start $account"
        is QuotaReadLog.Ok -> "ok $account ms=${log.elapsedMs} readAt=${if (log.hasReadAt) "有" else "无"}"
        is QuotaReadLog.Fail -> buildString {
            append("fail ").append(account)
            append(" kind=QUOTA code=").append(logSafeCode(log.code))
            append(" ms=").append(log.elapsedMs)
            if (!log.errorType.isNullOrBlank()) {
                append(" type=").append(scrubLogText(log.errorType).take(40))
            }
        }
    }
}

/* ========================================================================== */
/* 自定义渠道（custom-channels）：注册一个新渠道行（与「自定义供应商」不同）   */
/* ========================================================================== */

sealed interface CustomChannelsUiState {
    data object Idle : CustomChannelsUiState

    data object Loading : CustomChannelsUiState

    data class Ready(val channels: List<CustomChannel>) : CustomChannelsUiState

    data class Failed(val code: String, val message: String) : CustomChannelsUiState
}

sealed interface CustomChannelProbeUiState {
    data object Idle : CustomChannelProbeUiState

    data object Probing : CustomChannelProbeUiState

    data class Ok(val models: List<String>) : CustomChannelProbeUiState

    data class Failed(val message: String) : CustomChannelProbeUiState
}

/**
 * 自定义渠道的页面状态：列表 / 保存 / 删除 / 探测模型。
 *
 * 独立 ViewModel 的原因：ChannelsPage 的 `state` 由 GatewayHome 注入（写集不允许改它），
 * 这些只属于「自定义渠道」弹窗的临时状态塞不进 `ChannelsUiState`。
 */
internal class CustomChannelsViewModel(application: Application) : AndroidViewModel(application) {
    private val client = CustomChannelsApiClient()

    private val _state = MutableStateFlow<CustomChannelsUiState>(CustomChannelsUiState.Idle)
    val state: StateFlow<CustomChannelsUiState> = _state.asStateFlow()

    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()

    private val _deleting = MutableStateFlow<String?>(null)
    val deleting: StateFlow<String?> = _deleting.asStateFlow()

    private val _probe = MutableStateFlow<CustomChannelProbeUiState>(CustomChannelProbeUiState.Idle)
    val probe: StateFlow<CustomChannelProbeUiState> = _probe.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    fun load(status: GatewayStatus) {
        if (!status.running) {
            _state.value = CustomChannelsUiState.Failed("gateway_not_running", "网关未启动")
            return
        }
        _state.value = CustomChannelsUiState.Loading
        viewModelScope.launch {
            _state.value = try {
                CustomChannelsUiState.Ready(
                    withContext(Dispatchers.IO) { client.list(status) },
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                CustomChannelsUiState.Failed(e.code, e.message)
            } catch (_: Exception) {
                CustomChannelsUiState.Failed("network_error", "读取自定义渠道失败，请重试")
            }
        }
    }

    /** 保存成功后重新拉列表；失败把服务端原文放进 [error]，表单保留输入。 */
    fun save(status: GatewayStatus, draft: CustomChannelDraft, onSaved: (CustomChannel) -> Unit) {
        if (_saving.value) {
            return
        }
        validateCustomChannelDraft(draft)?.let {
            _error.value = it
            return
        }
        _error.value = null
        _saving.value = true
        viewModelScope.launch {
            try {
                val saved = withContext(Dispatchers.IO) { client.save(status, draft) }
                _state.value = CustomChannelsUiState.Ready(
                    withContext(Dispatchers.IO) { client.list(status) },
                )
                onSaved(saved)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _error.value = e.message
            } catch (_: Exception) {
                _error.value = "保存失败，请重试"
            } finally {
                _saving.value = false
            }
        }
    }

    fun delete(status: GatewayStatus, id: String, onDeleted: () -> Unit) {
        if (_deleting.value != null) {
            return
        }
        _error.value = null
        _deleting.value = id
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.delete(status, id) }
                _state.value = CustomChannelsUiState.Ready(
                    withContext(Dispatchers.IO) { client.list(status) },
                )
                onDeleted()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _error.value = e.message
            } catch (_: Exception) {
                _error.value = "删除失败，请重试"
            } finally {
                _deleting.value = null
            }
        }
    }

    /** 显式探测：Key 只用于这一次请求，不保存。 */
    fun probe(status: GatewayStatus, baseUrl: String, apiKey: String) {
        validateCustomChannelBaseUrl(baseUrl)?.let {
            _probe.value = CustomChannelProbeUiState.Failed(it)
            return
        }
        _probe.value = CustomChannelProbeUiState.Probing
        viewModelScope.launch {
            _probe.value = try {
                val result = withContext(Dispatchers.IO) { client.probe(status, baseUrl, apiKey) }
                if (result.ok) {
                    CustomChannelProbeUiState.Ok(result.models)
                } else {
                    CustomChannelProbeUiState.Failed(
                        result.error.ifBlank { "探测失败：上游没有返回模型清单" },
                    )
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                CustomChannelProbeUiState.Failed(e.message)
            } catch (_: Exception) {
                CustomChannelProbeUiState.Failed("探测失败，请重试")
            }
        }
    }

    fun dismissProbe() {
        _probe.value = CustomChannelProbeUiState.Idle
    }

    fun clearError() {
        _error.value = null
    }
}

