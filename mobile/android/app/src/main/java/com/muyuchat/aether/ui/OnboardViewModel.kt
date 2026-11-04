package com.muyuchat.aether.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.muyuchat.aether.runtime.CustomProviderDraft
import com.muyuchat.aether.runtime.CustomProviderProtocol
import com.muyuchat.aether.runtime.GatewayRuntimeRepository
import com.muyuchat.aether.runtime.ManagementApiClient
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.OnboardAuthApiClient
import com.muyuchat.aether.runtime.OnboardAuthSession
import com.muyuchat.aether.runtime.OnboardDeviceSession
import com.muyuchat.aether.runtime.ProviderFormField
import com.muyuchat.aether.runtime.ProviderSaveApiClient
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.onboardDeviceKind
import com.muyuchat.aether.runtime.onboardManualSchema
import com.muyuchat.aether.runtime.onboardMethodBlockedBy
import com.muyuchat.aether.runtime.onboardMethodLabel
import com.muyuchat.aether.runtime.onboardMethods
import com.muyuchat.aether.runtime.onboardMissingRequired
import com.muyuchat.aether.runtime.onboardParseTokenInput
import com.muyuchat.aether.runtime.parseProviderModelList
import com.muyuchat.aether.runtime.validateCustomProviderDraft
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 接入页的本地状态：搜索词 + 自定义渠道表单。
 *
 * 为什么单独一个 ViewModel：`OnboardPage` 的 providers/accounts 来自 ChannelsViewModel，
 * 而搜索与表单是页面本地的，不该把参数塞进 GatewayHome（写集也不允许改它）。
 *
 * 凭据安全：apiKey 只存在于本 ViewModel 的表单状态与一次请求体里；
 * 不打印、不写剪贴板、不落盘，表单关闭即丢弃。
 */
class OnboardViewModel(application: Application) : AndroidViewModel(application) {
    private val repository = GatewayRuntimeRepository(application)
    private val client = ManagementApiClient()

    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    /**
     * 筛选与排序（对标 MCA 模型推荐页的「筛选 · N / 排序」工具行）。
     *
     * 与搜索词分开两个 State：搜索是输入过程，筛选是一次性提交的草稿（面板里点「应用」才生效），
     * 两者叠加但互不覆盖。
     */
    private val _filters = MutableStateFlow(OnboardQuery())
    internal val filters: StateFlow<OnboardQuery> = _filters.asStateFlow()

    private val _draft = MutableStateFlow<CustomProviderDraft?>(null)

    /** null = 表单关闭；非 null = 正在编辑。 */
    val draft: StateFlow<CustomProviderDraft?> = _draft.asStateFlow()

    private val _saving = MutableStateFlow(false)
    val saving: StateFlow<Boolean> = _saving.asStateFlow()

    private val _formError = MutableStateFlow<String?>(null)
    val formError: StateFlow<String?> = _formError.asStateFlow()

    /** 「获取模型」的进行中/结果状态（只作用于自定义渠道表单）。 */
    private val _modelsFetch = MutableStateFlow<ModelsFetchUiState>(ModelsFetchUiState.Idle)
    val modelsFetch: StateFlow<ModelsFetchUiState> = _modelsFetch.asStateFlow()

    /** 保存成功的回执，由页面展示一次后清掉。 */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    /**
     * 每个渠道的接入向导（浏览器授权 / 设备码 / Token·JSON / 手动凭据）。
     *
     * 单独一个 controller 而不是把十几个 State 塞进本类：向导有自己的轮询 Job 生命周期，
     * 关弹窗就要停轮询，和搜索/表单这些页面本地状态不是一回事。
     */
    internal val connect: OnboardConnectController = OnboardConnectController(
        viewModelScope = viewModelScope,
        repository = repository,
        client = OnboardAuthApiClient(),
    )

    fun setQuery(value: String) {
        _query.value = value
    }

    /** 应用一次「筛选与排序」面板的草稿结果。 */
    internal fun setFilters(value: OnboardQuery) {
        _filters.value = value
    }

    /** 清空筛选、保留排序（MCA 面板「重置」的口径：重置只清条件，不回退排序）。 */
    internal fun clearFilters() {
        _filters.value = OnboardQuery(sort = _filters.value.sort)
    }

    fun openForm() {
        _formError.value = null
        _modelsFetch.value = ModelsFetchUiState.Idle
        // 表单第 1 项给默认值：名称默认「自定义渠道」。
        _draft.value = CustomProviderDraft(name = "自定义渠道")
    }

    fun dismissForm() {
        _draft.value = null
        _formError.value = null
        _modelsFetch.value = ModelsFetchUiState.Idle
    }

    fun updateName(value: String) = updateDraft { it.copy(name = value) }

    fun updateBaseUrl(value: String) = updateDraft { it.copy(baseUrl = value) }

    fun updateApiKey(value: String) = updateDraft { it.copy(apiKey = value) }

    fun updateModels(value: String) = updateDraft { it.copy(modelsText = value) }

    /**
     * 「获取模型」：拿表单里的 Base URL + API Key 直接问服务端要清单
     * （`POST /api/provider/models {base,key}`，**不需要先保存渠道**）。
     *
     * 成功：把清单**合并**进模型字段（手填的不覆盖，只补缺的）；失败：贴服务端原文 + 一句可操作建议。
     * apiKey 只进请求体一次，不打印、不落盘、不写剪贴板。
     */
    fun fetchModels() {
        val draft = _draft.value ?: return
        if (!canFetchDraftModels(draft.baseUrl, draft.apiKey)) {
            _modelsFetch.value = ModelsFetchUiState.Failed(draftModelsFetchHint(draft.baseUrl, draft.apiKey).orEmpty())
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _modelsFetch.value = ModelsFetchUiState.Failed("网关未启动")
            return
        }
        if (_modelsFetch.value is ModelsFetchUiState.Loading) {
            return
        }
        _modelsFetch.value = ModelsFetchUiState.Loading
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    ProviderSaveApiClient().fetchModels(status, draft.baseUrl, draft.apiKey)
                }
                val fetched = result.models
                val merged = mergeFetchedModels(draft.modelsText, fetched)
                if (merged != draft.modelsText) {
                    updateDraft { it.copy(modelsText = merged) }
                }
                _modelsFetch.value = if (fetched.isEmpty()) {
                    ModelsFetchUiState.Failed(modelsFetchEmptyLine())
                } else {
                    ModelsFetchUiState.Ready(count = fetched.size, source = result.source)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                _modelsFetch.value = ModelsFetchUiState.Failed(modelsFetchErrorLine(e.message.orEmpty()))
            } catch (_: Exception) {
                _modelsFetch.value = ModelsFetchUiState.Failed(modelsFetchErrorLine(""))
            }
        }
    }

    fun updateProtocol(value: CustomProviderProtocol) = updateDraft {
        it.copy(protocol = value)
    }

    fun dismissNotice() {
        _notice.value = null
    }

    /**
     * 保存自定义渠道。校验失败不出网；失败把服务端原文放进 [formError]，表单保留输入。
     *
     * @param existingIds 渠道列表里已有的 id：与服务端内置渠道重名会被当成「编辑内置渠道」，
     *   本地拦下（表单不提交 id，此参数只用于防御性校验）。
     * @param onSaved 成功后刷新渠道列表（复用 OnboardPage 的 onRefresh）。
     */
    fun save(existingIds: Set<String>, onSaved: () -> Unit) {
        val draft = _draft.value ?: return
        if (_saving.value) {
            return
        }
        val problem = validateCustomProviderDraft(draft, existingIds)
        if (problem != null) {
            _formError.value = problem
            return
        }
        val status = repository.status.value
        if (!status.running) {
            _formError.value = "网关未启动"
            return
        }
        _formError.value = null
        _saving.value = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { client.saveCustomProvider(status, draft) }
                _draft.value = null
                _notice.value = "已添加「${draft.name.trim()}」"
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                // 只贴服务端原文，不包一层「保存失败」。
                _formError.value = e.message
            } catch (_: Exception) {
                _formError.value = "保存失败，请重试"
            } finally {
                _saving.value = false
            }
        }
    }

    private fun updateDraft(block: (CustomProviderDraft) -> CustomProviderDraft) {
        _draft.value = _draft.value?.let(block)
    }
}

/* ========================================================================== */
/* 接入向导（渠道卡片点开后）：四类手机端能做的接入方式                        */
/* ========================================================================== */

/** 一个可选的接入方式；[blockedReason] 非空表示手机端做不了，按钮置灰并显示原因。 */
internal data class OnboardMethodOption(
    val id: String,
    val label: String,
    val blockedReason: String? = null,
) {
    val enabled: Boolean get() = blockedReason == null
}

/** 接入向导的全部界面状态。 */
internal data class OnboardConnectState(
    val providerId: String,
    val providerName: String,
    val providerDescription: String = "",
    val methods: List<OnboardMethodOption> = emptyList(),
    val active: String = "manual_input",
    val label: String = "",
    val busy: Boolean = false,
    val statusLine: String = "",
    val auth: OnboardAuthSession? = null,
    val device: OnboardDeviceSession? = null,
    val callbackInput: String = "",
    val tokenInput: String = "",
    val manualSchema: List<ProviderFormField> = emptyList(),
    val manualValues: Map<String, String> = emptyMap(),
    /** Windsurf 邮箱 + 密码登录（`POST /api/provider/windsurf/login`）；密码只在表单与请求体里。 */
    val windsurfEmail: String = "",
    val windsurfPassword: String = "",
    val error: String? = null,
    val finished: Boolean = false,
) {
    val currentMethod: OnboardMethodOption?
        get() = methods.firstOrNull { it.id == active }
}

/** 接入弹窗 footer 里的主按钮要触发的动作（决定点下去调哪个回调）。 */
internal enum class OnboardPrimaryKind {
    /** 当前阶段没有 footer 主按钮（例如设备码已弹出、等用户去授权）。 */
    None,
    StartAuth,
    CommitAuth,
    SubmitCallback,
    StartDevice,
    SubmitTokenJson,
    SubmitManual,
    SubmitWindsurf,
}

/**
 * footer 主按钮的展示模型：文案 + 是否可点 + 置灰原因。
 *
 * 为什么要有 [hint]：必填项没填时按钮是**置灰**的，用户需要一个"为什么点不了"的说明，
 * 否则会以为是坏了（这正是「找不到确定按钮」那类投诉的变种）。
 */
internal data class OnboardPrimaryAction(
    val label: String = "",
    val enabled: Boolean = false,
    val hint: String? = null,
    val kind: OnboardPrimaryKind = OnboardPrimaryKind.None,
) {
    /** 没有主按钮时 footer 只留「关闭」。 */
    val visible: Boolean get() = kind != OnboardPrimaryKind.None
}

/** 当前接入方式在"该阶段"的主动作是谁；纯函数，供 footer 与单测共用。 */
internal fun onboardPrimaryKind(state: OnboardConnectState): OnboardPrimaryKind = when {
    state.active == "token_json" -> OnboardPrimaryKind.SubmitTokenJson
    state.active == "manual_input" -> OnboardPrimaryKind.SubmitManual
    state.active == "windsurf_login" -> OnboardPrimaryKind.SubmitWindsurf
    // PKCE / 通用设备码走 /api/auth 三段式：start → 轮询 → commit
    state.active == "oauth_pkce" || state.active == "device_code" -> {
        val auth = state.auth
        when {
            auth == null -> OnboardPrimaryKind.StartAuth
            auth.isSuccess -> OnboardPrimaryKind.CommitAuth
            auth.authorizeUrl.isNotBlank() -> OnboardPrimaryKind.SubmitCallback
            // 已经拿到设备码：正文里的「打开授权页面」才是主操作，footer 不再抢。
            else -> OnboardPrimaryKind.None
        }
    }
    // 渠道私有设备码（trae / kiro / zed …）：服务端轮询到状态就自己入库，无需 commit。
    onboardDeviceKind(state.active) != null ->
        if (state.device == null) OnboardPrimaryKind.StartDevice else OnboardPrimaryKind.None
    else -> OnboardPrimaryKind.None
}

/**
 * 计算 footer 主按钮：**必填缺失 → 置灰并给原因**，而不是让用户点了才报错。
 *
 * 规则（每种接入方式）：
 * - `token_json`：Token/JSON 输入为空 → 置灰「先粘贴 Access Token 或完整 JSON」；
 * - `manual_input`：`auth_form_schema` 里 `required` 的字段只要有空 → 置灰并列出还缺哪些；
 * - `windsurf_login`：邮箱或密码为空 → 置灰「请填写邮箱与密码」；
 * - 授权类（start / commit / 回调提交 / 开始登录）：只受 `busy` 影响；
 * - `busy` 时一律置灰，文案换成进行中（提交中… / 登录中… / 处理中…）。
 */
internal fun onboardPrimaryAction(state: OnboardConnectState): OnboardPrimaryAction {
    val kind = onboardPrimaryKind(state)
    if (kind == OnboardPrimaryKind.None) {
        return OnboardPrimaryAction()
    }
    val busyLabel = when (kind) {
        OnboardPrimaryKind.SubmitTokenJson, OnboardPrimaryKind.SubmitManual -> "提交中…"
        OnboardPrimaryKind.SubmitWindsurf -> "登录中…"
        OnboardPrimaryKind.CommitAuth -> "入库中…"
        else -> "处理中…"
    }
    if (state.busy) {
        return OnboardPrimaryAction(label = busyLabel, enabled = false, kind = kind)
    }
    return when (kind) {
        OnboardPrimaryKind.SubmitTokenJson -> {
            val blank = state.tokenInput.isBlank()
            OnboardPrimaryAction(
                label = "导入并接入",
                enabled = !blank,
                hint = if (blank) "先粘贴 Access Token 或完整 JSON" else null,
                kind = kind,
            )
        }
        OnboardPrimaryKind.SubmitManual -> {
            val missing = state.manualSchema.filter { it.required && state.manualValues[it.key].isNullOrBlank() }
            OnboardPrimaryAction(
                label = "接入",
                enabled = missing.isEmpty(),
                hint = if (missing.isEmpty()) null else "还需填写：" + missing.joinToString("、") { it.label },
                kind = kind,
            )
        }
        OnboardPrimaryKind.SubmitWindsurf -> {
            val blank = state.windsurfEmail.isBlank() || state.windsurfPassword.isBlank()
            OnboardPrimaryAction(
                label = "登录并接入",
                enabled = !blank,
                hint = if (blank) "请填写邮箱与密码" else null,
                kind = kind,
            )
        }
        OnboardPrimaryKind.StartAuth -> OnboardPrimaryAction("开始授权", true, kind = kind)
        OnboardPrimaryKind.CommitAuth -> OnboardPrimaryAction("完成接入", true, kind = kind)
        OnboardPrimaryKind.SubmitCallback -> OnboardPrimaryAction("我已授权，继续", true, kind = kind)
        OnboardPrimaryKind.StartDevice -> OnboardPrimaryAction("开始登录", true, kind = kind)
        OnboardPrimaryKind.None -> OnboardPrimaryAction()
    }
}

/* ==========================================================================
 * 登录类按钮的文案（用户要求：文案要跟"是否在应用内"对齐，不能一个词打天下）
 *
 * 旧文案统一写「打开登录页面」，但 PKCE 现在在应用内 WebView 打开、设备码仍是外部浏览器，
 * 同一句话会让用户以为会跳出去（或反过来）。所以按场景分四种，集中在这里定义。
 * ========================================================================== */

/** 登录按钮的场景。 */
internal enum class OnboardAuthActionKind {
    /** PKCE：第一次在应用内打开登录页。 */
    InAppLogin,

    /** PKCE：已经打开过一次，再打开。 */
    InAppLoginReopen,

    /** 设备码 / 私有设备码：确认页必须在外部浏览器打开。 */
    ExternalBrowser,

    /** 手机端做不了、只能桌面端完成（不给会失败的按钮）。 */
    DesktopOnly,
}

/**
 * 登录按钮文案。
 *
 * - 应用内：**「在应用内登录」/「重新打开登录页」**（不跳浏览器，用户不会误以为离开了 App）；
 * - 外部浏览器：**「打开授权页面」**，配合 [onboardAuthButtonHint] 的「将在浏览器中打开」；
 * - 桌面端：**「该渠道需在桌面端登录」**。
 */
internal fun onboardAuthButtonLabel(kind: OnboardAuthActionKind): String = when (kind) {
    OnboardAuthActionKind.InAppLogin -> "在应用内登录"
    OnboardAuthActionKind.InAppLoginReopen -> "重新打开登录页"
    OnboardAuthActionKind.ExternalBrowser -> "打开授权页面"
    OnboardAuthActionKind.DesktopOnly -> "该渠道需在桌面端登录"
}

/** 按钮下方的补充提示：只有"会跳到外部浏览器"与"只能桌面端"需要提醒。 */
internal fun onboardAuthButtonHint(kind: OnboardAuthActionKind): String? = when (kind) {
    OnboardAuthActionKind.ExternalBrowser -> "将在浏览器中打开"
    OnboardAuthActionKind.DesktopOnly -> "手机端无法完成该登录，请改用 Token / JSON"
    OnboardAuthActionKind.InAppLogin, OnboardAuthActionKind.InAppLoginReopen -> null
}

/**
 * 把「接入方式 + 是否已打开过 + 被拦原因」归到上面四种场景。
 *
 * [blockedReason] 非空表示手机端做不了（`onboardMethodBlockedBy` 的返回值）→ 桌面端场景。
 */
internal fun onboardAuthActionKind(
    method: String,
    blockedReason: String? = null,
    reopened: Boolean = false,
): OnboardAuthActionKind = when {
    blockedReason != null -> OnboardAuthActionKind.DesktopOnly
    // PKCE 走本机回环回调，能在应用内 WebView 里走完
    method == "oauth_pkce" ->
        if (reopened) OnboardAuthActionKind.InAppLoginReopen else OnboardAuthActionKind.InAppLogin
    // 设备码 / 私有设备码：确认页是第三方站点，只能外部浏览器
    else -> OnboardAuthActionKind.ExternalBrowser
}

/* ==========================================================================
 * 「手机端必然失败的登录方式」统一表
 *
 * 教训（Trae）：授权码必须由 **Trae 桌面客户端**回传（`login_channel=native_ide` /
 * `auth_type=local`），浏览器/WebView 只能拿到 `isRedirect`，永远完成不了。
 * 这类渠道的登录按钮点了必失败，所以按 **provider id 前缀** 判（不解析错误文案——太脆），
 * 在构建方法列表时就标成 blocked，页面因此不会渲染那颗按钮。
 *
 * 以后新增同类渠道：只改 [onboardLoginDesktopRequired]，其余代码不用动。
 * ========================================================================== */

/** 只用桌面端客户端才能完成登录的渠道（目前是 Trae 四个渠道：`trae-` 前缀）。 */
internal fun onboardLoginDesktopRequired(providerId: String): Boolean =
    providerId.trim().lowercase().startsWith("trae-")

/**
 * Trae 的说明文案（逐字按用户给的原话，别改写意思）。
 *
 * 结构：**不能做什么 + 为什么 + 该走哪条路 + 怎么拿到凭据**。
 */
internal fun onboardTraeDesktopLoginNote(): String =
    "手机端无法完成 Trae 网页授权：授权码需要由 Trae 桌面客户端回传。" +
        "请改用「手动凭据 · Token」：在桌面端登录 Trae 后从本机导入，或粘贴 access token。"

/** 登录类方式（这些在"必须桌面端"的渠道上一律不可用）。 */
private val OnboardLoginMethods = setOf(
    "oauth_pkce",
    "device_code",
    "windsurf_login",
    "qoder_device",
)

/**
 * 纯函数：某登录方式在该渠道上是否**必然失败**；null 表示可以试。
 *
 * 只拦"登录类"方式——`manual_input`（手动凭据）、`token_json`（Token / JSON）
 * 这类不需要回调的方式必须继续可用，它们才是这些渠道真正的接入路径。
 */
internal fun onboardLoginMethodBlockedReason(method: String, providerId: String): String? = when {
    !onboardLoginDesktopRequired(providerId) -> null
    method in OnboardLoginMethods || method.endsWith("_device") -> onboardTraeDesktopLoginNote()
    else -> null
}

/* ==========================================================================
 * 自定义渠道「获取模型」（`POST /api/provider/models {base,key}`）
 *
 * 契约（`internal/core/gateway/provider_detect.go:212-307`）：
 *   请求 `{base?, chat?, key?, model?, id?}`；服务端 GET `{base}/models`，
 *   解析 `data[].id` 与 `models[].name`（去掉 `models/` 前缀），回
 *   `{count, models[], dropped[], source?}`；400 / 502 时回 `{error}`。
 *   **不需要先保存渠道**：base+key 直接传即可。
 * ========================================================================== */

/** 「获取模型」的状态。`public` 是因为 `OnboardViewModel.modelsFetch` 是公开属性，
 *  暴露 internal 类型会触发 Kotlin 的「public property exposes internal type」编译错误。 */
sealed interface ModelsFetchUiState {
    data object Idle : ModelsFetchUiState
    data object Loading : ModelsFetchUiState
    data class Ready(val count: Int, val source: String = "") : ModelsFetchUiState
    data class Failed(val message: String) : ModelsFetchUiState
}

/** 两个字段都填了才允许发探测（服务端没有 Base URL 直接 400）。 */
internal fun canFetchDraftModels(baseUrl: String, apiKey: String): Boolean =
    baseUrl.isNotBlank() && apiKey.isNotBlank()

/** 按钮置灰时告诉用户还差什么；null = 可以点。 */
internal fun draftModelsFetchHint(baseUrl: String, apiKey: String): String? = when {
    canFetchDraftModels(baseUrl, apiKey) -> null
    baseUrl.isBlank() && apiKey.isBlank() -> "先填 Base URL 与 API Key"
    baseUrl.isBlank() -> "先填 Base URL"
    else -> "先填 API Key"
}

/**
 * 合并模型清单：**手填的排前面且不被覆盖**，探测到的只补缺（去重、保序）。
 *
 * "获取"是辅助手段，不能把用户已经写好的模型清掉。
 */
internal fun mergeFetchedModels(existing: String, fetched: List<String>): String {
    val out = ArrayList<String>()
    parseProviderModelList(existing).forEach { model ->
        if (!out.contains(model)) {
            out.add(model)
        }
    }
    fetched.forEach { raw ->
        val model = raw.trim()
        if (model.isNotEmpty() && !out.contains(model)) {
            out.add(model)
        }
    }
    return out.joinToString(", ")
}

/** 失败文案：**服务端原文在前**，后面跟一句可操作建议（不吞掉原因）。 */
internal fun modelsFetchErrorLine(serverMessage: String): String {
    val raw = serverMessage.trim()
    val suggestion = when {
        raw.isEmpty() -> "获取不到模型：检查网关是否在运行、Base URL 是否可达"
        raw.contains("先填一个 Base URL") -> "先把 Base URL 填上"
        raw.contains("401") || raw.contains("403") || raw.contains("Unauthorized") -> "API Key 是否正确、是否已过期？"
        raw.contains("404") || raw.contains("Not Found") -> "Base URL 是否需要包含 /v1？"
        raw.contains("连接失败") -> "检查手机到该域名的网络，以及 Base URL 是否写错"
        else -> "检查 Base URL 是否包含 /v1、Key 是否有效"
    }
    return if (raw.isEmpty()) suggestion else "$raw（$suggestion）"
}

/** 端点正常返回但没有模型：不当成失败，提示仍可手填。 */
internal fun modelsFetchEmptyLine(): String = "该端点没有返回模型清单：仍可手动填写模型"

/**
 * 接入向导的编排：发起 → 轮询 → 入库，全部经本机网关，不依赖桌面。
 *
 * 轮询一律用可取消 Job：关弹窗/切方式/取消授权都会立刻停掉，
 * 不会在后台继续打服务端（桌面 `onboard.js:220` 的 `stop()` 同语义）。
 */
internal class OnboardConnectController(
    private val viewModelScope: kotlinx.coroutines.CoroutineScope,
    private val repository: GatewayRuntimeRepository,
    private val client: OnboardAuthApiClient,
) {
    private val _state = MutableStateFlow<OnboardConnectState?>(null)
    val state: StateFlow<OnboardConnectState?> = _state.asStateFlow()

    private var pollJob: Job? = null

    fun open(provider: ProviderSummary) {
        stopPolling()
        val options = onboardMethods(provider).map { method ->
            OnboardMethodOption(
                id = method,
                label = onboardMethodLabel(method),
                // 「手机端必然失败」的方式在这里统一拦下：页面因此永远不渲染点了会失败的按钮。
                blockedReason = onboardMethodBlockedBy(method, provider)
                    ?: onboardLoginMethodBlockedReason(method, provider.id),
            )
        }
        val withToken = options + OnboardMethodOption("token_json", onboardMethodLabel("token_json"))
        _state.value = OnboardConnectState(
            providerId = provider.id,
            providerName = provider.name.ifBlank { provider.id },
            providerDescription = provider.description,
            methods = withToken,
            // 登录类全部不可用时，主入口直接落到「Token / JSON」（唯一真能走通的路）。
            active = withToken.firstOrNull { it.enabled }?.id ?: "token_json",
            manualSchema = onboardManualSchema(provider),
        )
    }

    fun close() {
        stopPolling()
        val current = _state.value ?: return
        current.auth?.takeIf { !it.isTerminal }?.let { session ->
            val status = repository.status.value
            if (status.running) {
                viewModelScope.launch {
                    runCatching { withContext(Dispatchers.IO) { client.cancelAuth(status, session.id) } }
                }
            }
        }
        _state.value = null
    }

    fun selectMethod(method: String) {
        stopPolling()
        val current = _state.value ?: return
        _state.value = current.copy(
            active = method,
            auth = null,
            device = null,
            callbackInput = "",
            tokenInput = "",
            statusLine = "",
            error = null,
            finished = false,
            busy = false,
        )
    }

    fun setLabel(value: String) = mutate { it.copy(label = value) }

    fun setCallbackInput(value: String) = mutate { it.copy(callbackInput = value) }

    fun setTokenInput(value: String) = mutate { it.copy(tokenInput = value) }

    fun setManualValue(key: String, value: String) = mutate {
        it.copy(manualValues = it.manualValues + (key to value))
    }

    fun setWindsurfEmail(value: String) = mutate { it.copy(windsurfEmail = value) }

    fun setWindsurfPassword(value: String) = mutate { it.copy(windsurfPassword = value) }

    fun dismissError() = mutate { it.copy(error = null) }

    /** 浏览器授权 / 通用设备码：`POST /api/auth/start`。 */
    fun startAuth(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val method = current.active
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null, statusLine = "正在发起授权…", finished = false) }
        viewModelScope.launch {
            try {
                val session = withContext(Dispatchers.IO) {
                    client.startAuth(status, current.providerId, method)
                }
                mutate {
                    it.copy(
                        busy = false,
                        auth = session,
                        statusLine = if (session.isSuccess) "授权成功，确认备注名即可入库。" else "等待授权…",
                    )
                }
                if (!session.isSuccess) {
                    startPolling(session.id)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, statusLine = "", error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, statusLine = "", error = "无法发起授权，请重试") }
            }
        }
    }

    /** 浏览器回调不到本机时：粘贴完整回调地址（或裸 code）。 */
    fun submitCallback() {
        val current = _state.value ?: return
        val session = current.auth ?: return
        val value = current.callbackInput.trim()
        if (value.isEmpty()) {
            mutate { it.copy(error = "先粘贴回调地址或 code") }
            return
        }
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                val next = withContext(Dispatchers.IO) {
                    client.submitCallback(status, session.id, value)
                }
                mutate {
                    it.copy(
                        busy = false,
                        auth = next,
                        statusLine = if (next.isSuccess) {
                            "授权成功，确认备注名即可入库。"
                        } else {
                            "尚未成功：${next.error.ifBlank { next.status }}"
                        },
                    )
                }
                if (next.isSuccess) {
                    stopPolling()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "授权失败，请重试") }
            }
        }
    }

    /** 授权成功后把凭证落成账号。 */
    fun commitAuth(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val session = current.auth ?: return
        if (!session.isSuccess) {
            mutate { it.copy(error = "认证尚未成功，当前状态：${session.status}") }
            return
        }
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.commitAuth(status, session.id, current.label.ifBlank { current.providerName })
                }
                mutate { it.copy(busy = false, statusLine = "账号已接入", finished = true) }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "入库失败，请重试") }
            }
        }
    }

    fun cancelAuth() {
        stopPolling()
        val current = _state.value ?: return
        val session = current.auth ?: return
        val status = repository.status.value
        if (status.running) {
            viewModelScope.launch {
                runCatching { withContext(Dispatchers.IO) { client.cancelAuth(status, session.id) } }
            }
        }
        mutate { it.copy(auth = null, statusLine = "已取消", busy = false) }
    }

    /** 渠道各自的登录会话（trae / kiro / zed / codebuddy / workbuddy / minimax）。 */
    fun startDevice(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val kind = onboardDeviceKind(current.active) ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        mutate { it.copy(busy = true, error = null, statusLine = "正在创建登录会话…", finished = false) }
        viewModelScope.launch {
            try {
                val session = withContext(Dispatchers.IO) {
                    client.startDevice(status, kind, current.providerId)
                }
                mutate { it.copy(busy = false, device = session, statusLine = "等待浏览器登录…") }
                startDevicePolling(kind, session.id, onSaved)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, statusLine = "", error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, statusLine = "", error = "无法开始登录，请重试") }
            }
        }
    }

    fun cancelDevice() {
        stopPolling()
        val current = _state.value ?: return
        val kind = onboardDeviceKind(current.active)
        val session = current.device
        val status = repository.status.value
        if (kind != null && session != null && status.running) {
            viewModelScope.launch {
                runCatching {
                    withContext(Dispatchers.IO) { client.cancelDevice(status, kind, session.id) }
                }
            }
        }
        mutate { it.copy(device = null, statusLine = "已取消", busy = false) }
    }

    /** Token / JSON：一个输入框，纯 token 或完整 JSON 都收（`onboard.js:356-419`）。 */
    fun submitTokenJson(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        val items = try {
            onboardParseTokenInput(current.tokenInput, current.manualSchema)
        } catch (e: ManagementApiException) {
            mutate { it.copy(error = e.message) }
            return
        }
        if (items.isEmpty()) {
            mutate { it.copy(error = "先粘贴 Token 或 JSON") }
            return
        }
        val missing = items.mapIndexedNotNull { index, creds ->
            val gap = onboardMissingRequired(creds, current.manualSchema)
            if (gap.isEmpty()) null else "第 ${index + 1} 项缺少 ${gap.joinToString("、")}"
        }
        if (missing.isNotEmpty()) {
            mutate { it.copy(error = missing.first()) }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            var ok = 0
            val failures = mutableListOf<String>()
            for ((index, creds) in items.withIndex()) {
                val label = current.label.ifBlank { current.providerName } +
                    if (items.size > 1) " #${index + 1}" else ""
                try {
                    withContext(Dispatchers.IO) {
                        client.addAccount(status, current.providerId, label, creds)
                    }
                    ok++
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    failures += "第 ${index + 1} 项：${e.message}"
                } catch (_: Exception) {
                    failures += "第 ${index + 1} 项：接入失败"
                }
            }
            mutate {
                it.copy(
                    busy = false,
                    tokenInput = "",
                    statusLine = if (ok > 0) "已接入 $ok 个账号" else "",
                    error = failures.firstOrNull(),
                    finished = ok > 0,
                )
            }
            if (ok > 0) {
                onSaved()
            }
        }
    }

    /** 手动凭据：按渠道表单填字段，落 `POST /api/accounts`。 */
    fun submitManual(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        val schema = current.manualSchema
        val required = schema.firstOrNull { it.required && current.manualValues[it.key].isNullOrBlank() }
        if (required != null) {
            mutate { it.copy(error = "请填写「${required.label.ifBlank { required.key }}」") }
            return
        }
        val creds = current.manualValues.filterValues { it.isNotBlank() }
        if (creds.isEmpty()) {
            mutate { it.copy(error = "请至少填写一个字段") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.addAccount(
                        status,
                        current.providerId,
                        current.label.ifBlank { current.providerName },
                        creds,
                    )
                }
                mutate {
                    it.copy(busy = false, statusLine = "账号已接入", manualValues = emptyMap(), finished = true)
                }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "接入失败，请重试") }
            }
        }
    }

    /**
     * Windsurf 邮箱 + 密码登录：`POST /api/provider/windsurf/login`。
     *
     * 密码只进请求体：不打印、不落盘、不进剪贴板；成功后清空密码字段并刷新列表。
     */
    fun submitWindsurf(onSaved: () -> Unit) {
        val current = _state.value ?: return
        val status = repository.status.value
        if (!status.running) {
            mutate { it.copy(error = "网关未启动") }
            return
        }
        if (current.windsurfEmail.isBlank() || current.windsurfPassword.isBlank()) {
            mutate { it.copy(error = "请填写邮箱和密码") }
            return
        }
        mutate { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    client.loginWindsurf(status, current.windsurfEmail, current.windsurfPassword)
                }
                mutate {
                    it.copy(
                        busy = false,
                        statusLine = "Windsurf 账号已接入",
                        windsurfPassword = "",
                        finished = true,
                    )
                }
                onSaved()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                mutate { it.copy(busy = false, error = e.message) }
            } catch (_: Exception) {
                mutate { it.copy(busy = false, error = "登录失败，请重试") }
            }
        }
    }

    /* ---------------- 轮询 ---------------- */

    private fun startPolling(sessionId: String) {
        stopPolling()
        val status = repository.status.value
        val interval = ((_state.value?.auth?.intervalSeconds ?: 0).takeIf { it > 0 } ?: 2) * 1_000L
        pollJob = viewModelScope.launch {
            while (true) {
                delay(interval.coerceAtLeast(1_500L))
                try {
                    val next = withContext(Dispatchers.IO) { client.getAuth(status, sessionId) }
                    if (next.isSuccess) {
                        stopPolling()
                        mutate { it.copy(auth = next, statusLine = "授权成功，确认备注名即可入库。") }
                        return@launch
                    }
                    if (next.isError) {
                        stopPolling()
                        mutate {
                            it.copy(
                                auth = next,
                                statusLine = "",
                                error = "授权失败：${next.error.ifBlank { next.status }}",
                            )
                        }
                        return@launch
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    stopPolling()
                    mutate { it.copy(error = "轮询失败：${e.message}") }
                    return@launch
                }
            }
        }
    }

    private fun startDevicePolling(kind: String, sessionId: String, onSaved: () -> Unit) {
        stopPolling()
        val status = repository.status.value
        val interval = ((_state.value?.device?.intervalSeconds ?: 0).takeIf { it > 0 } ?: 2) * 1_000L
        pollJob = viewModelScope.launch {
            while (true) {
                delay(interval.coerceAtLeast(2_000L))
                try {
                    val poll = withContext(Dispatchers.IO) { client.pollDevice(status, kind, sessionId) }
                    if (poll.isDone) {
                        stopPolling()
                        mutate { it.copy(statusLine = "账号已接入", finished = true, device = null) }
                        onSaved()
                        return@launch
                    }
                    if (poll.isFailed) {
                        stopPolling()
                        mutate { it.copy(statusLine = "", error = "登录失败：${poll.error.ifBlank { poll.state }}") }
                        return@launch
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ManagementApiException) {
                    stopPolling()
                    mutate { it.copy(error = "轮询失败：${e.message}") }
                    return@launch
                }
            }
        }
    }

    fun stopPolling() {
        pollJob?.cancel()
        pollJob = null
    }

    /**
     * 应用内 WebView 登录的兜底：立刻拉一次授权状态，不等下一个轮询周期。
     *
     * 场景：WebView 里已经跳到 `127.0.0.1/oauth/callback`（网关已收下 code），
     * 但周期轮询还没轮到——用户点「完成」时用它把状态推进到 success，footer 随即出现「完成接入」。
     * 失败静默：周期轮询仍在跑，这里只是催一下。
     */
    fun nudgeAuthPoll() {
        val session = _state.value?.auth ?: return
        if (session.isSuccess || session.isError) {
            return
        }
        val status = repository.status.value
        if (!status.running) {
            return
        }
        viewModelScope.launch {
            try {
                val next = withContext(Dispatchers.IO) { client.getAuth(status, session.id) }
                when {
                    next.isSuccess -> {
                        stopPolling()
                        mutate { it.copy(auth = next, statusLine = "授权成功，确认备注名即可入库。") }
                    }
                    next.isError -> {
                        stopPolling()
                        mutate {
                            it.copy(
                                auth = next,
                                statusLine = "",
                                error = "授权失败：${next.error.ifBlank { next.status }}",
                            )
                        }
                    }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // 周期轮询还在跑，催一次失败不打扰用户。
            }
        }
    }

    private fun mutate(block: (OnboardConnectState) -> OnboardConnectState) {
        _state.value = _state.value?.let(block)
    }
}
