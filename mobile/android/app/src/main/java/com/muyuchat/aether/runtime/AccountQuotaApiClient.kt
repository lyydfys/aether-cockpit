package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import kotlin.coroutines.cancellation.CancellationException

/**
 * 订阅窗口读数（`internal/provider/contract.go:209-225` `QuotaWindow`）。
 *
 * [usedPercent] 是**百分比**（契约注释写明 `Used float64 // 0-100`），不是绝对值；
 * 卡片的「剩余 %」= 100 - used。[amount]/[limit] 只在渠道真的给了绝对值时才有意义。
 */
data class AccountQuotaWindow(
    val name: String,
    val usedPercent: Double,
    val resetsAt: Long,
    val unit: String,
    val amount: Long,
    val limit: Long,
)

/**
 * 今日签到卡（`internal/core/gateway/checkin_api.go:23-30` `quotaCheckin`）。
 * outcome 取值见 `provider.Checkin*`：claimed / done / own-app / inactive / ineligible / failed。
 */
data class AccountCheckin(
    val day: String,
    val outcome: String,
    val credit: Double,
    val streak: Int,
    val message: String,
    val again: Boolean,
)

/**
 * `GET /api/accounts` 里的一条账号（`AccountSnapshot` + `accountView` 视图字段）。
 *
 * 字段出处：
 * - account_id / provider_id / label / disabled / reset_at / last_used / quota
 *   → `internal/provider/env.go:796-818`（`AccountSnapshot`）
 * - quota_supported / quota_error / checkin_supported / checkin
 *   → `internal/core/gateway/admin.go:356-370`（`accountView`）
 * - quota.total / quota.used / quota.unit / quota.windows / quota.message
 *   → `internal/provider/contract.go:228-252`（`QuotaStatus`）与 `:209-225`（`QuotaWindow`）
 */
data class AccountQuotaRow(
    val accountId: String,
    val providerId: String,
    val label: String,
    val disabled: Boolean,
    val resetAt: Long,
    val lastUsed: Long,
    val quotaSupported: Boolean,
    val quotaError: String,
    val checkinSupported: Boolean,
    val checkin: AccountCheckin?,
    val quotaWindows: List<AccountQuotaWindow>,
    val quotaTotal: Int,
    val quotaUsed: Int,
    val quotaUnit: String,
    val quotaMessage: String,
    /** `quota.read_at`（`contract.go:249`）：>0 表示这个账号**被读过**额度，用来区分"没读过"与"读了但没有窗口"。 */
    val quotaReadAt: Long = 0L,
    /** 账号级路由参数（`env.go:799-817`）：优先级 / 权重 / 并发上限 / 允许模型白名单。 */
    val priority: Int = 0,
    val weight: Int = 0,
    /** null = 跟随全局（服务端 `concurrency` 是 `*int`，见 `env.go:815`）。 */
    val concurrency: Int? = null,
    val allowedModels: List<String> = emptyList(),
    /**
     * 账号级模型映射（`AccountSnapshot.model_mapping`，`internal/provider/env.go:813`）：
     * 客户端/路由模型名 → 该账号真实的上游模型名。支持精确名与 `*` 通配。
     *
     * `GET /api/accounts` 会带这个字段（`admin.go:350` 把整个 `AccountSnapshot` 内联进回包），
     * 所以手机端**读得到当前值**；写用 `POST /api/provider/accountmapping`，
     * 服务端是**整表替换**（`SetModelMapping`，`env.go:585-602`），不是字段合并。
     */
    val modelMapping: Map<String, String> = emptyMap(),
    val tags: List<String> = emptyList(),
    val note: String = "",
)

/**
 * 账号设置的补丁：**只带要改的字段**。
 *
 * 服务端 `handleAccountSettings`（`accounts_api.go:82-95`）的字段全是 `*T`，缺省即"不修改"；
 * `concurrency = -1` 表示跟随全局（`:103-108`）。`clearAllowedModels` 单独建模，
 * 因为要把空数组**真的发出去**才等于清空白名单。
 */
data class AccountSettingsPatch(
    val disabled: Boolean? = null,
    val priority: Int? = null,
    val weight: Int? = null,
    val concurrency: Int? = null,
    val clearAllowedModels: Boolean = false,
) {
    val isEmpty: Boolean
        get() = disabled == null && priority == null && weight == null &&
            concurrency == null && !clearAllowedModels
}

/** 请求预设（`GET /api/presets` 的 `presets[]`；敏感 Header 服务端已脱敏，见 `request_presets.go:317-323`）。 */
data class RequestPreset(
    val id: String,
    val name: String,
    val description: String,
    val enabled: Boolean,
    val hasSystemPrompt: Boolean,
    val bodyCount: Int,
    val headerCount: Int,
)

/** `GET /api/presets` / `POST /api/presets/attach` 的回包（`presetsView`，`request_presets.go:333-337`）。 */
data class PresetsView(
    val presets: List<RequestPreset>,
    val providerPresets: Map<String, List<String>>,
)

/**
 * 一次「读额度」拿到的快照（`QuotaStatus`，`contract.go:228-252`）。
 * 只带卡片要渲染的字段，不搬整个对象图。
 */
data class AccountQuotaSnapshot(
    val windows: List<AccountQuotaWindow>,
    val total: Int,
    val used: Int,
    val unit: String,
    val message: String,
    val readAt: Long,
)

/**
 * 单账号「读一次额度」的结果。
 *
 * 端点：`POST /api/accounts/{id}/refresh`（`gateway.go:1116` → `auth_api.go:290-353`
 * `handleTokenRefresh`：「能续期凭证就续期，然后同步读一次额度」）。回包字段：
 * `refreshed / refresh_supported / quota / quota_supported / quota_read`，
 * 失败时另带 `quota_error`（额度读取失败）或 `warning`（凭据续期失败）。
 */
data class AccountQuotaRead(
    val accountId: String,
    val quotaSupported: Boolean,
    val quotaRead: Boolean,
    val quotaError: String,
    val warning: String,
    val snapshot: AccountQuotaSnapshot?,
)

/**
 * `POST /api/accounts/batch` 里的一条结果（`accounts_api.go:155-159` `batchResult`：
 * `{id, ok, error?}`，成功时 `error` 被 omitempty 省掉）。
 */
data class AccountBatchEntry(
    val id: String,
    val ok: Boolean,
    val error: String,
)

/**
 * 一次批量动作的真实回包汇总。
 *
 * [results] 是逐条真相（成功/失败都在里面），页面的「成功 N / 失败 M」只能从它数出来；
 * [updatedRows] 只用于启停（settings 回包带账号快照），[checkins] 只用于签到回包里的今日卡。
 */
data class AccountBatchOutcome(
    val results: List<AccountBatchEntry>,
    val updatedRows: List<AccountQuotaRow> = emptyList(),
    val checkins: Map<String, AccountCheckin> = emptyMap(),
) {
    val ok: Int get() = results.count { it.ok }
    val failed: Int get() = results.count { !it.ok }
    val total: Int get() = results.size

    /** 第一个失败原因（服务端原文）；全成功时为空串。 */
    val firstError: String
        get() = results.firstOrNull { !it.ok && it.error.isNotBlank() }?.error.orEmpty()
}

/**
 * 服务端单账号「读额度」的最坏路径（毫秒）：
 * 凭据续期 45s（`auth_api.go:307-308` 的 `context.WithTimeout(..., 45*time.Second)`）
 * 加读额度 20s（`auth_api.go:336` 的 `readQuotaFor(env, 20*time.Second)`）。
 * 客户端超时必须比它晚放弃，否则回包永远到不了，`quota_error` 也就永远不存在。
 */
internal const val SERVER_QUOTA_READ_WORST_MS = 45_000 + 20_000

/**
 * 单账号读额度的客户端读超时（毫秒）。
 *
 * Why 90：服务端最坏 65s（见 [SERVER_QUOTA_READ_WORST_MS]）+ 25s 余量，
 * 留给手机网络的 RTT / TLS 握手 / 本机网关中转。低于 65s 就是"客户端先放弃、服务端还在读"的黑洞；
 * 也不为了"看起来通过"放大到 120s 以上（用户会以为界面卡死）。
 */
internal const val ACCOUNT_REFRESH_TIMEOUT_MS = 90_000

/**
 * 账号与额度客户端。
 *
 * 复用管理面的口径（与 [ManagementApiClient] 同一套）：HTTP 只打 `http://<addr>`，
 * 会话凭据只走 `Authorization: Bearer <adminSession>` 头，错误统一抛
 * [ManagementApiException]（code 用 `http_<status>` / `network_error` / `invalid_response`），
 * 响应体按上限读取。没有再引一层 HTTP 库。
 *
 * 端点：
 * - `GET  /api/accounts`（`gateway.go:844` → `handleAccounts`）
 * - `POST /api/accounts/{id}/settings` `{"disabled":bool}`（`gateway.go:1053` → `handleAccountSettings`，回 `{"account":snapshot}`）
 * - `POST /api/accounts/{id}/checkin`（`gateway.go:847` → `handleCheckIn`，回 `{"checkin":{…},"outcome":…}`）
 */
class AccountQuotaApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 8_000,
    /** 签到要打上游，服务端给 90s 预算（`admin.go:578`），读超时必须比它长。 */
    private val checkinReadTimeoutMs: Int = 100_000,
    /** 批量刷新由服务端逐条串行执行（`accounts_api.go:186-189` 每条还 sleep 1s+），要给足时间。 */
    private val batchReadTimeoutMs: Int = 120_000,
    /** 单账号读额度：必须比服务端最坏路径（[SERVER_QUOTA_READ_WORST_MS]）更晚放弃。 */
    private val accountRefreshTimeoutMs: Int = ACCOUNT_REFRESH_TIMEOUT_MS,
) {
    fun fetchAccounts(status: GatewayStatus): List<AccountQuotaRow> {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        val response = request(status, "GET", "/api/accounts", body = null, readTimeoutMs)
        return parseAccounts(response)
    }

    /**
     * 账号级启用/停用（`accountView` 的 `disabled`）。
     *
     * 服务端确认后回最新快照；调用方据此**就地**更新卡片（不做乐观更新）。
     */
    fun setDisabled(status: GatewayStatus, accountId: String, disabled: Boolean): AccountQuotaRow {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code ->
            throw ManagementApiException(code, "账号 id 不合法")
        }
        val response = request(
            status = status,
            method = "POST",
            path = accountSettingsPath(accountId),
            body = buildAccountDisabledJson(disabled),
            timeoutMs = readTimeoutMs,
        )
        return parseAccountSnapshot(response)
    }

    /** 立即签到；返回服务端回的今日签到卡（可能为 null，表示服务端没给状态）。 */
    fun checkin(status: GatewayStatus, accountId: String): AccountCheckin? {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code ->
            throw ManagementApiException(code, "账号 id 不合法")
        }
        val response = request(
            status = status,
            method = "POST",
            path = accountCheckinPath(accountId),
            body = "{}",
            timeoutMs = checkinReadTimeoutMs,
        )
        return parseCheckinAck(response)
    }

    /**
     * 批量刷新额度：走**真批量端点** `POST /api/accounts/batch` `{"action":"refresh","ids":[…]}`
     * （`accounts_api.go:185-225`，服务端逐条执行并回 `{"results":[…],"ok":N,"total":M}`）。
     */
    fun batchRefresh(status: GatewayStatus, ids: List<String>): AccountBatchOutcome {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateBatchIds(ids)?.let { code -> throw ManagementApiException(code, "批量刷新没有可执行的账号") }
        val response = request(
            status = status,
            method = "POST",
            path = accountsBatchPath(),
            body = buildAccountBatchJson(BATCH_ACTION_REFRESH, ids),
            timeoutMs = batchReadTimeoutMs,
        )
        return parseAccountBatch(response)
    }

    /**
     * 批量删除账号：桌面 `channels.js:193-203` 的垃圾桶按钮，走**真批量端点**
     * `POST /api/accounts/batch {"action":"delete","ids":[…]}`（`accounts_api.go` 的
     * `handleAccountBatch` 认 delete/refresh/ready）。回 `{"results":[…],"ok":N,"total":M}`。
     */
    fun batchDelete(status: GatewayStatus, ids: List<String>): AccountBatchOutcome {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateBatchIds(ids)?.let { code -> throw ManagementApiException(code, "批量删除没有可执行的账号") }
        val response = request(
            status = status,
            method = "POST",
            path = accountsBatchPath(),
            body = buildAccountBatchJson(BATCH_ACTION_DELETE, ids),
            timeoutMs = batchReadTimeoutMs,
        )
        return parseAccountBatch(response)
    }

    /**
     * 批量启用/停用：服务端**没有**批量启停端点（`handleAccountBatch` 只认 delete/refresh/ready），
     * 所以这里按 id **顺序**逐个打 `POST /api/accounts/{id}/settings`，逐条记真实结果，
     * 并把每次回包的账号快照带回，供卡片就地更新。
     */
    fun batchSetDisabled(
        status: GatewayStatus,
        ids: List<String>,
        disabled: Boolean,
    ): AccountBatchOutcome {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateBatchIds(ids)?.let { code -> throw ManagementApiException(code, "批量操作没有可执行的账号") }
        val results = ArrayList<AccountBatchEntry>(ids.size)
        val rows = ArrayList<AccountQuotaRow>(ids.size)
        for (id in ids) {
            try {
                rows += setDisabled(status, id, disabled)
                results += AccountBatchEntry(id = id, ok = true, error = "")
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                results += AccountBatchEntry(id = id, ok = false, error = e.message)
            } catch (_: Exception) {
                results += AccountBatchEntry(id = id, ok = false, error = "未知错误")
            }
        }
        return AccountBatchOutcome(results = results, updatedRows = rows)
    }

    /**
     * 批量签到：同样没有批量端点，按 id 顺序逐个打 `POST /api/accounts/{id}/checkin`。
     * 调用方负责只传 `checkin_supported` 的账号；这里对每个 id 都如实计一次结果。
     */
    fun batchCheckin(status: GatewayStatus, ids: List<String>): AccountBatchOutcome {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateBatchIds(ids)?.let { code -> throw ManagementApiException(code, "批量签到没有可执行的账号") }
        val results = ArrayList<AccountBatchEntry>(ids.size)
        val checkins = LinkedHashMap<String, AccountCheckin>()
        for (id in ids) {
            try {
                val ack = checkin(status, id)
                results += AccountBatchEntry(id = id, ok = true, error = "")
                if (ack != null) {
                    checkins[id] = ack
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ManagementApiException) {
                results += AccountBatchEntry(id = id, ok = false, error = e.message)
            } catch (_: Exception) {
                results += AccountBatchEntry(id = id, ok = false, error = "未知错误")
            }
        }
        return AccountBatchOutcome(results = results, checkins = checkins)
    }

    /**
     * 单账号读一次额度。
     *
     * 走**真单账号端点** `POST /api/accounts/{id}/refresh`（`gateway.go:1116` →
     * `auth_api.go:290-353`）：服务端能续期凭证就续期，然后同步读一次额度，并把
     * `quota`（读后快照）/`quota_supported`/`quota_read`/`quota_error` 一起回给客户端。
     * 因此**不需要**再拉一次 `/api/accounts`，卡片可以直接用回包快照就地重画。
     */
    fun readQuota(status: GatewayStatus, accountId: String): AccountQuotaRead {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        val response = request(
            status = status,
            method = "POST",
            path = accountRefreshPath(accountId),
            body = "{}",
            timeoutMs = accountRefreshTimeoutMs,
        )
        return parseQuotaRead(accountId, response)
    }

    // ——— 账号详情/设置、标签、备注、预设（桌面 channels.js 那几个账号级入口） ———

    /**
     * 账号设置：`POST /api/accounts/{id}/settings`（`gateway.go:1074` → `accounts_api.go:71-134`）。
     *
     * **只发改动的字段**：服务端每个字段都是 `*T`，缺省即不修改（和桌面"全量提交"不同，
     * 手机端更保守——没动过的字段不碰）。回包是 `{"account":<snapshot>}`，就地更新卡片。
     */
    fun updateAccountSettings(
        status: GatewayStatus,
        accountId: String,
        patch: AccountSettingsPatch,
    ): AccountQuotaRow {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        if (patch.isEmpty) {
            throw ManagementApiException("empty_patch", "没有任何改动")
        }
        val response = request(
            status = status,
            method = "POST",
            path = accountSettingsPath(accountId),
            body = buildAccountSettingsJson(patch),
            timeoutMs = readTimeoutMs,
        )
        return parseAccountSnapshot(response)
    }

    /**
     * 账号级模型映射：`POST /api/provider/accountmapping {id, account, mapping}`
     * （`gateway.go:869` → `provider_api.go:698`）。
     *
     * **服务端是整表替换，不是字段更新**：`SetModelMapping(body.Mapping)`
     * （`internal/provider/env.go:585-602`）先清洗再直接 `e.modelMapping = clean`，
     * 传空对象就是清除。其余账号字段不动（`handleProviderAccountMapping` 只碰这一个 map，
     * 然后 `saveAccountsNow()` 持久化整份账号文件）。
     *
     * 所以调用方必须提交**完整的目标映射**（先读 `row.modelMapping`、改完再整体发），
     * 不能只发改动的那几条。
     *
     * 回包是 `respondProviders`（服务商 + 账号的整份视图），这里不解析：保存成功后
     * 由 ViewModel 走一次 `refresh()` 拿最新状态，避免对那个形状做第二份假设。
     */
    fun saveAccountMapping(
        status: GatewayStatus,
        providerId: String,
        accountId: String,
        mapping: Map<String, String>,
    ) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        if (providerId.isBlank()) {
            throw ManagementApiException("invalid_provider", "渠道 id 为空，无法提交模型映射")
        }
        request(
            status = status,
            method = "POST",
            path = "/api/provider/accountmapping",
            body = buildAccountMappingJson(providerId, accountId, mapping),
            timeoutMs = readTimeoutMs,
        )
    }

    /** 标签：`POST /api/accounts/{id}/tags`（`accounts_api.go:19-44`）。**空列表 = 清空标签**。 */
    fun setAccountTags(status: GatewayStatus, accountId: String, tags: List<String>): List<String> {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        val response = request(
            status = status,
            method = "POST",
            path = accountTagsPath(accountId),
            body = buildAccountTagsJson(tags),
            timeoutMs = readTimeoutMs,
        )
        return parseTagsAck(response)
    }

    /** 备注：`POST /api/accounts/{id}/note`（`accounts_api.go:46-69`）。**空字符串 = 清空备注**。 */    fun setAccountNote(status: GatewayStatus, accountId: String, note: String): String {        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        val response = request(
            status = status,
            method = "POST",
            path = accountNotePath(accountId),
            body = buildAccountNoteJson(note),
            timeoutMs = readTimeoutMs,
        )
        return parseNoteAck(response)
    }

    /**
     * 删除账号：`DELETE /api/accounts/{id}`（`gateway.go:873`；桌面 `channels.js:315`）。
     *
     * 只移除本机记录，渠道本身不动（桌面确认框原话）。服务端成功回 200，失败回 `{error}`。
     */
    fun deleteAccount(status: GatewayStatus, accountId: String) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        request(
            status = status,
            method = "DELETE",
            path = accountPath(accountId),
            body = null,
            timeoutMs = readTimeoutMs,
        )
    }

    /**
     * 官方渠道绑定信息：`GET /api/accounts/{id}/binding`（桌面 `channels.js:594`）。
     *
     * 回 `{account_id, provider_id, binding:{official,account_id,email,plan,expires_at},
     * official, paused, pause_reason, paused_at}`（`binding_api.go:45-53`）。
     * 渠道不支持时服务端回 **409 `{error}`** —— 这不是错误，页面显示"该渠道不提供绑定信息"。
     */
    fun fetchBinding(status: GatewayStatus, accountId: String): AccountBinding {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        val response = try {
            request(
                status = status,
                method = "GET",
                path = accountBindingPath(accountId),
                body = null,
                timeoutMs = readTimeoutMs,
            )
        } catch (e: ManagementApiException) {
            // 409 = 这个渠道没有"官方绑定"能力（`binding_api.go:41`）：当成"不适用"，不弹红字。
            if (e.code == "http_409") {
                return AccountBinding(supported = false, reason = e.message)
            }
            throw e
        }
        return parseAccountBinding(response)
    }

    /**
     * 立即唤醒：`POST /api/wake/run {"account_id":…}`（桌面 `channels.js:427`）。
     *
     * 语义（桌面 tooltip 原话）：「发一个最小请求，提前触发额度重置」。
     * 仅 `capabilities.chat` 为真的渠道显示该动作，调用方负责判定。
     */    fun wakeAccount(status: GatewayStatus, accountId: String) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        validateAccountId(accountId)?.let { code -> throw ManagementApiException(code, "账号 id 不合法") }
        request(
            status = status,
            method = "POST",
            path = wakeRunPath(),
            body = buildWakeRunJson(accountId),
            // 唤醒要真发一次上游请求，给足超时（与额度读取同档）。
            timeoutMs = readTimeoutMs,
        )
    }

    /**
     * 渠道额外模型：`POST /api/providers/{id}/models/extra`（`gateway.go:1139`）。
     *
     * 请求体 `{model, action:"add"|"remove"}`；成功回**完整渠道列表**（调用方整页重拉），
     * 失败把服务端 `{error}` 原文抛出（例如对内置模型发 remove 会被拒）。
     */
    fun setExtraModel(
        status: GatewayStatus,
        providerId: String,
        model: String,
        action: ExtraModelAction,
    ) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        if (providerId.isBlank()) {
            throw ManagementApiException("invalid_provider", "缺少渠道 id")
        }
        if (action == ExtraModelAction.Add) {
            validateExtraModelName(model)?.let { problem ->
                throw ManagementApiException("invalid_model", problem)
            }
        }
        request(
            status = status,
            method = "POST",
            path = providerExtraModelsPath(providerId),
            body = buildExtraModelJson(model, action),
            timeoutMs = readTimeoutMs,
        )
    }

    /**
     * 渠道顺序：`POST /api/providers/arrange {"order":[id,…]}`（`gateway.go:882`）。
     *
     * 服务端只保留 `provider.Get(id)` 认识的 id（未知 id 静默丢弃），保存后回完整渠道列表；
     * 手机端不解析回包，交给调用方整页重拉。
     */
    fun arrangeProviders(status: GatewayStatus, order: List<String>) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        request(
            status = status,
            method = "POST",
            path = providersArrangePath(),
            body = buildProvidersArrangeJson(order),
            timeoutMs = readTimeoutMs,
        )
    }

    /**
     * 渠道自定义余额：`POST /api/provider/balance`（`gateway.go:884`，`provider_balance.go:139`）。
     *
     * [clear] = true 时发送 `{id, clearBalance:true}`，服务端清除该渠道的自定义余额配置；
     * 否则按草稿写入 URL / path / method / headers / token（令牌留空 = 不改动）。
     * 失败把服务端 `{error}` 原文抛出。
     */
    fun saveProviderBalance(
        status: GatewayStatus,
        draft: ProviderBalanceDraft,
        clear: Boolean = false,
    ) {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        if (!clear) {
            validateProviderBalanceDraft(draft)?.let { problem ->
                throw ManagementApiException("invalid_balance", problem)
            }
        }
        request(
            status = status,
            method = "POST",
            path = providerBalancePath(),
            body = buildProviderBalanceJson(draft, clear),
            timeoutMs = readTimeoutMs,
        )
    }

    /** 预设列表：`GET /api/presets`（`request_presets.go:340-343`）。 */
    fun fetchPresets(status: GatewayStatus): PresetsView {        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        val response = request(status, "GET", presetsPath(), body = null, timeoutMs = readTimeoutMs)
        return parsePresets(response)
    }

    /**
     * 把预设挂到**渠道**上：`POST /api/presets/attach`（`request_presets.go:575-652`）。
     * `ids` 传空数组 = 解绑该渠道的全部预设（handler 里 `delete(ProviderPresets, key)`）。
     */
    fun attachProviderPresets(
        status: GatewayStatus,
        providerId: String,
        ids: List<String>,
    ): PresetsView {
        val guard = guardOf(status)
        if (guard != null) {
            throw guard
        }
        if (providerId.isBlank()) {
            throw ManagementApiException("provider_id_empty", "渠道 id 不能为空")
        }
        val response = request(
            status = status,
            method = "POST",
            path = presetsAttachPath(),
            body = buildPresetAttachJson(providerId, ids),
            timeoutMs = readTimeoutMs,
        )
        return parsePresets(response)
    }

    /** 网关没跑 / 没有会话凭据时一次请求都不发。 */
    private fun guardOf(status: GatewayStatus): ManagementApiException? = when {        !status.running || status.addr.isBlank() -> ManagementApiException("gateway_not_running", "网关未启动")
        status.adminSession.isBlank() -> ManagementApiException("gateway_not_running", "网关未启动")
        else -> null
    }

    private fun request(
        status: GatewayStatus,
        method: String,
        path: String,
        body: String?,
        timeoutMs: Int,
    ): String {
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = timeoutMs
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
        }
        try {
            if (body != null) {
                connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }
            val code = connection.responseCode
            if (code != HttpURLConnection.HTTP_OK) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { readLimited(it) }
                        ?.let { JSONObject(it.trim()).optString("error").trim() }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(200))
            }
            val response = connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            return response
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 4 * 1024 * 1024): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return out.toString()
            }
            out.append(buffer, 0, read)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "管理接口响应过大")
            }
        }
    }
}

/** 账号 id 直接进路径段，必须先编码（id 里可能有 `/` 或空格以外的怪字符）。 */
internal fun accountSettingsPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/settings"

/** 删除账号路径（`DELETE /api/accounts/{id}`，`gateway.go:873`）。 */
internal fun accountPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}"

/** 官方绑定信息路径（`GET /api/accounts/{id}/binding`，桌面 `channels.js:594`）。 */
internal fun accountBindingPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/binding"

/**
 * 账号的「渠道绑定」结果。
 *
 * `supported == false` 表示**这个渠道没有官方绑定能力**（服务端 409，`binding_api.go:41`），
 * 不是错误；页面要显示"该渠道不提供绑定信息"，不要弹红字。
 * `bound` 为 false 而 supported 为 true 时显示「未绑定」。
 */
data class AccountBinding(
    val supported: Boolean = true,
    val official: Boolean = false,
    val boundAccountId: String = "",
    val email: String = "",
    val plan: String = "",
    /** unix 秒；0 = 没上报。 */
    val expiresAt: Long = 0L,
    val paused: Boolean = false,
    val pauseReason: String = "",
    /** unix 秒；0 = 没上报。 */
    val pausedAt: Long = 0L,
    val reason: String = "",
) {
    /** 有绑定的官方账号/邮箱才算"已绑定"。 */
    val bound: Boolean
        get() = boundAccountId.isNotBlank() || email.isNotBlank()
}

internal fun parseAccountBinding(body: String): AccountBinding {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "绑定信息返回格式不正确")
    }
    val binding = root.optJSONObject("binding")
    return AccountBinding(
        supported = true,
        official = root.optBoolean("official", false) ||
            (binding?.optBoolean("official", false) ?: false),
        boundAccountId = binding?.optString("account_id")?.trim().orEmpty(),
        email = binding?.optString("email")?.trim().orEmpty(),
        plan = binding?.optString("plan")?.trim().orEmpty(),
        expiresAt = binding?.optLong("expires_at", 0L)?.coerceAtLeast(0L) ?: 0L,
        paused = root.optBoolean("paused", false),
        pauseReason = root.optString("pause_reason").trim(),
        pausedAt = root.optLong("paused_at", 0L).coerceAtLeast(0L),
    )
}

/** 立即唤醒路径（`POST /api/wake/run`，桌面 `channels.js:427`）。 */
internal fun wakeRunPath(): String = "/api/wake/run"

/** 渠道排序路径（`POST /api/providers/arrange`，`gateway.go:882`）。 */
internal fun providersArrangePath(): String = "/api/providers/arrange"

/** 渠道排序请求体：`{"order":["id1","id2",…]}`。 */
internal fun buildProvidersArrangeJson(order: List<String>): String =
    JSONObject().put("order", JSONArray(order)).toString()

/* ==========================================================================
 * 渠道级自定义余额（`POST /api/provider/balance`，`provider_balance.go:139`）
 *
 * 服务端字段：`id` / `balanceURL` / `balancePath` / `balanceMethod` / `headers{}` /
 * `balanceToken` / `clearBalanceToken` / `account` / `clearBalance`。
 * 语义是**写入或清除这个渠道的余额端点配置**（不是"查一次余额"）。
 * ========================================================================== */

/** 表单草稿（headers 用文本承载，解析见 [parseHeaderLines]）。
 *  `public`：`saveProviderBalance` 是公开客户端方法，暴露 internal 类型会触发
 *  Kotlin 的「public function exposes its internal parameter type」编译错误。 */
data class ProviderBalanceDraft(
    val providerId: String,
    val url: String = "",
    val path: String = "",
    val method: String = "GET",
    /** 每行 `Key: Value`；`#` 开头与空行忽略。 */
    val headersText: String = "",
    /** 新令牌；留空时按"已保存，不改动"处理。 */
    val token: String = "",
)

/** 头文本解析结果：失败带**行号**，用户能直接定位。 */
internal data class HeaderParseResult(
    val headers: Map<String, String> = emptyMap(),
    val errorLine: Int = 0,
    val error: String? = null,
) {
    val ok: Boolean get() = error == null
}

/**
 * 解析 `Key: Value` 多行文本。
 *
 * 只按**每行第一个冒号**分界（值里可以再含冒号，例如 `Authorization: Bearer a:b`）；
 * 空行与 `#` 注释忽略；缺冒号 / 键空 / 值空都返回行号与原因，不静默丢弃。
 */
internal fun parseHeaderLines(text: String): HeaderParseResult {
    val out = LinkedHashMap<String, String>()
    text.split('\n').forEachIndexed { index, rawLine ->
        val line = rawLine.trim()
        if (line.isEmpty() || line.startsWith("#")) {
            return@forEachIndexed
        }
        val colon = line.indexOf(':')
        if (colon <= 0) {
            return HeaderParseResult(
                errorLine = index + 1,
                error = "第 ${index + 1} 行缺少冒号，应写成 `Key: Value`",
            )
        }
        val key = line.substring(0, colon).trim()
        val value = line.substring(colon + 1).trim()
        if (key.isEmpty()) {
            return HeaderParseResult(errorLine = index + 1, error = "第 ${index + 1} 行的 Key 为空")
        }
        if (value.isEmpty()) {
            return HeaderParseResult(errorLine = index + 1, error = "第 ${index + 1} 行的 Value 为空")
        }
        out[key] = value
    }
    return HeaderParseResult(headers = out)
}

/** 表单校验：URL 必填且 http(s)；方法只 GET/POST；headers 必须能解析。 */
internal fun validateProviderBalanceDraft(draft: ProviderBalanceDraft): String? {
    if (draft.providerId.isBlank()) {
        return "缺少渠道 id"
    }
    val url = draft.url.trim()
    if (url.isEmpty()) {
        return "请填写余额接口 URL"
    }
    if (!url.startsWith("http://") && !url.startsWith("https://")) {
        return "余额接口 URL 必须以 http:// 或 https:// 开头"
    }
    val headers = parseHeaderLines(draft.headersText)
    if (!headers.ok) {
        return headers.error
    }
    if (draft.method.trim().uppercase() !in setOf("GET", "POST")) {
        return "请求方法只支持 GET / POST"
    }
    return null
}

/**
 * 组装请求体。
 *
 * - [clear] = true：只发 `{id, clearBalance:true}`（服务端按"清除配置"处理）；
 * - 令牌三态：填了 → 发 `balanceToken`；留空 → **不发**（等于"不改动"，
 *   与工具服务那套"已保存（不改动）"哨兵语义一致）；
 * - `headers` 解析为空时不发该字段（避免把已有配置清空）。
 */
internal fun buildProviderBalanceJson(draft: ProviderBalanceDraft, clear: Boolean = false): String {
    val body = JSONObject().put("id", draft.providerId.trim())
    if (clear) {
        return body.put("clearBalance", true).toString()
    }
    body.put("balanceURL", draft.url.trim())
    val path = draft.path.trim()
    if (path.isNotEmpty()) {
        body.put("balancePath", path)
    }
    body.put("balanceMethod", draft.method.trim().uppercase())
    val headers = parseHeaderLines(draft.headersText).headers
    if (headers.isNotEmpty()) {
        val json = JSONObject()
        headers.forEach { (k, v) -> json.put(k, v) }
        body.put("headers", json)
    }
    val token = draft.token.trim()
    if (token.isNotEmpty()) {
        body.put("balanceToken", token)
    }
    return body.toString()
}

/** 余额配置端点路径（`gateway.go:884`）。 */
internal fun providerBalancePath(): String = "/api/provider/balance"

/* ==========================================================================
 * 渠道额外模型（`POST /api/providers/{id}/models/extra`，`gateway.go:1139`）
 *
 * 契约（`provider_extra_models.go:111`）：路径 `{id}` 是渠道 id，请求体
 * `{model, action:"add"|"remove"}`（缺省按 add）；未知 action 回 400 + `{error}`。
 * 桌面入口（`channels.js:1018-1034`）：「添加缺失模型」输入框 + 「添加」，
 * 每个模型行的「删除」走 `action:"remove"`；说明「上游 /models 漏报或厂商不实现 /models 时用」。
 * ========================================================================== */

/** 额外模型动作（服务端只认 add / remove，缺省 = add）。 */
enum class ExtraModelAction(val wire: String) {
    Add("add"),
    Remove("remove"),
}

/** 额外模型路径：渠道 id 要编码，避免 `/` 把路径拆坏。 */
internal fun providerExtraModelsPath(providerId: String): String =
    "/api/providers/${encodeAccountSegment(providerId)}/models/extra"

/** 额外模型请求体：`{model, action}`。 */
internal fun buildExtraModelJson(model: String, action: ExtraModelAction): String =
    JSONObject()
        .put("model", model.trim())
        .put("action", action.wire)
        .toString()

/** 校验：模型名不能为空、不能含空白（上游模型名不允许空格）。 */
internal fun validateExtraModelName(model: String): String? {
    val name = model.trim()
    if (name.isEmpty()) {
        return "请输入模型名"
    }
    if (name.any { it.isWhitespace() }) {
        return "模型名里不能有空格"
    }
    return null
}

/** 额外模型弹窗的说明（桌面 `channels.js:1034` 原话）。 */
internal fun extraModelNote(): String =
    "上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models"

/**
 * 「删除」按钮是否该出现在这一行。
 *
 * 只有**额外模型**（手动追加的）才能移除：服务端 `removeProviderExtraModel` 只认额外模型，
 * 对渠道登记的内置模型发 remove 会 400，所以内置模型行不渲染删除按钮。
 */
internal fun extraModelRowShowsRemove(isExtraModel: Boolean): Boolean = isExtraModel

/** 上移 / 下移方向。**不暴露裸 delta**：方向搞反过，签名上直接说清楚。 */
internal enum class MoveDirection(val indexDelta: Int) {
    /** 「↑」按钮：在数组里索引 -1（更靠前、更优先）。 */
    Up(-1),

    /** 「↓」按钮：在数组里索引 +1（更靠后）。 */
    Down(+1),
}

/**
 * 把 [id] 在 [order] 里按方向移动一格（UI 语义：↑ = 上移一格、↓ = 下移一格）。
 *
 * 越界钳制（首行上移 / 末行下移原样返回，不抛异常）；未知 id 同样原样返回。
 */
internal fun moveProvider(order: List<String>, id: String, direction: MoveDirection): List<String> =
    moveProviderOrder(order, id, direction.indexDelta)

/**
 * 裸 delta 版本（保留给既有调用点）：**delta > 0 是下移**（索引 +1），delta < 0 是上移。
 *
 * 新代码请用 [moveProvider] + [MoveDirection]，别再传数字。
 */
internal fun moveProviderOrder(order: List<String>, id: String, delta: Int): List<String> {
    val from = order.indexOf(id)
    if (from < 0 || delta == 0) {
        return order
    }
    val to = (from + delta).coerceIn(0, order.lastIndex)
    if (to == from) {
        return order
    }
    val next = order.toMutableList()
    next.removeAt(from)
    next.add(to, id)
    return next
}

/** 立即唤醒请求体：`{"account_id":"…"}`。 */
internal fun buildWakeRunJson(accountId: String): String =
    JSONObject().put("account_id", accountId).toString()

/** 批量端点路径（`gateway.go:1073` `POST /api/accounts/batch`）。 */
internal fun accountsBatchPath(): String = "/api/accounts/batch"

/** 标签 / 备注 / 预设端点（`gateway.go:1072-1074`、`gateway.go:808-811`）。 */
internal fun accountTagsPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/tags"

internal fun accountNotePath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/note"

internal fun presetsPath(): String = "/api/presets"

internal fun presetsAttachPath(): String = "/api/presets/attach"

/**
 * 账号设置请求体：**只放改动过的字段**（缺省 = 不修改，见 `accounts_api.go:82-95`）。
 * `clear_allowed_models` 落成 `"allowed_models": []`——必须真的发空数组才算清空。
 */
internal fun buildAccountSettingsJson(patch: AccountSettingsPatch): String {
    val json = JSONObject()
    patch.disabled?.let { json.put("disabled", it) }
    patch.priority?.let { json.put("priority", it) }
    patch.weight?.let { json.put("weight", it) }
    patch.concurrency?.let { json.put("concurrency", it) }
    if (patch.clearAllowedModels) {
        json.put("allowed_models", JSONArray())
    }
    return json.toString()
}

/** 标签请求体：`{"tags":[…]}`；空数组会真的发出去（清空语义）。 */
internal fun buildAccountTagsJson(tags: List<String>): String =
    JSONObject().put("tags", JSONArray(cleanTagList(tags))).toString()

/** 备注请求体：`{"note":"…"}`；空字符串会真的发出去（清空语义）。 */
internal fun buildAccountNoteJson(note: String): String =
    JSONObject().put("note", note).toString()

/**
 * 模型映射请求体：`{"id":渠道, "account":账号, "mapping":{模型:上游模型}}`
 * （`provider_api.go:699-703`）。
 *
 * **mapping 是整表替换**（`env.go:585-602`），所以这里不合并、不剔除"已存在"的键——
 * 传什么就是最终状态；空 map 序列化成 `{}`，等于清除映射。
 * 键值两端都 trim，空键/空值丢掉（服务端也会丢，这里先丢让 UI 的计数准确）。
 */
internal fun buildAccountMappingJson(
    providerId: String,
    accountId: String,
    mapping: Map<String, String>,
): String {
    val clean = JSONObject()
    mapping.entries
        .map { it.key.trim() to it.value.trim() }
        .filter { (k, v) -> k.isNotEmpty() && v.isNotEmpty() }
        .sortedBy { (k, _) -> k }
        .forEach { (k, v) -> clean.put(k, v) }
    return JSONObject()
        .put("id", providerId.trim())
        .put("account", accountId.trim())
        .put("mapping", clean)
        .toString()
}

/**
 * 把多行文本解析成映射：一行 `模型=上游模型`。
 *
 * 规则（与对话框里的提示一致）：`#` 开头算注释；空行跳过；只按**第一个** `=` 切
 * （上游模型名里可能有 `=` 以外的符号，但不该有等号；真遇到了保留在值里更诚实）；
 * 键或值为空的行丢掉；同键后者覆盖前者；`k == v` 也丢掉——服务端会把它当"没映射"
 * （`env.go:590`），留着会让"已配置 N 条"与实际生效数不一致。
 */
internal fun parseAccountMappingLines(text: String): Map<String, String> {
    val out = LinkedHashMap<String, String>()
    text.lineSequence().forEach { raw ->
        val line = raw.trim()
        if (line.isEmpty() || line.startsWith("#")) {
            return@forEach
        }
        val index = line.indexOf('=')
        if (index <= 0) {
            return@forEach
        }
        val key = line.substring(0, index).trim()
        val value = line.substring(index + 1).trim()
        if (key.isEmpty() || value.isEmpty() || key == value) {
            return@forEach
        }
        out.remove(key)
        out[key] = value
    }
    return out
}

/** 映射 → 多行文本（对话框打开时的初始值）；键排序保证每次打开顺序一致。 */
internal fun formatAccountMappingLines(mapping: Map<String, String>): String =
    mapping.entries
        .filter { (k, v) -> k.isNotBlank() && v.isNotBlank() }
        .sortedBy { (k, _) -> k }
        .joinToString("\n") { (k, v) -> "$k=$v" }

/**
 * 解析账号回包里的 `model_mapping`。
 *
 * 服务端 `omitempty`：没有映射时字段整个不出现 → 空 map（不是 null，UI 不用分两种空）。
 * 值不是字符串的键直接跳过（不猜它在说什么）。
 */
internal fun parseModelMapping(json: JSONObject?): Map<String, String> {
    if (json == null) {
        return emptyMap()
    }
    val out = LinkedHashMap<String, String>()
    json.keys().forEach { key ->
        val value = json.optString(key).trim()
        val name = key.trim()
        if (name.isNotEmpty() && value.isNotEmpty()) {
            out[name] = value
        }
    }
    return out
}

/** 预设挂载请求体：`{"scope":"provider","key":<pid>,"ids":[…]}`（`request_presets.go:575-579`）。 */
internal fun buildPresetAttachJson(providerId: String, ids: List<String>): String {
    val json = JSONObject()
    json.put("scope", "provider")
    json.put("key", providerId)
    json.put("ids", JSONArray(ids.map { it.trim() }.filter { it.isNotEmpty() }.distinct()))
    return json.toString()
}

/**
 * 把用户输入切成标签：逗号（中英文）/空白分隔、去重、最多 8 个。
 * 与桌面 `tagDialog`（`channels.js:460`）的口径一致。
 */
internal fun parseTagInput(input: String): List<String> = input
    .split(Regex("[,，\\s]+"))
    .map { it.trim() }
    .filter { it.isNotEmpty() }
    .distinct()
    .take(MAX_ACCOUNT_TAGS)

/** 服务端标签列表也按同一口径清洗（去空、去重、上限 8）。 */
internal fun cleanTagList(tags: List<String>): List<String> = tags
    .map { it.trim() }
    .filter { it.isNotEmpty() }
    .distinct()
    .take(MAX_ACCOUNT_TAGS)

/** 账号标签上限（桌面 `tagDialog` 的 `slice(0, 8)`）。 */
internal const val MAX_ACCOUNT_TAGS = 8

/** `{"account_id":…,"tags":[…]}` → 标签列表。 */
internal fun parseTagsAck(body: String): List<String> {
    val root = objectOrThrow(body, "标签")
    val array = root.optJSONArray("tags")
        ?: throw ManagementApiException("invalid_response", "标签回包缺少 tags 字段")
    return (0 until array.length()).mapNotNull { index -> array.optString(index).trim().ifBlank { null } }
}

/** `{"account_id":…,"note":"…"}` → 备注（服务端回显的值，可能是空串）。 */
internal fun parseNoteAck(body: String): String {
    val root = objectOrThrow(body, "备注")
    if (!root.has("note")) {
        throw ManagementApiException("invalid_response", "备注回包缺少 note 字段")
    }
    return root.optString("note")
}

/** `presetsView` → 预设列表 + 每个渠道挂载的预设 id。 */
internal fun parsePresets(body: String): PresetsView {
    val root = objectOrThrow(body, "预设")
    val array = root.optJSONArray("presets")
        ?: throw ManagementApiException("invalid_response", "预设回包缺少 presets 字段")
    val presets = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        RequestPreset(
            id = id,
            name = item.optString("name").trim().ifBlank { id },
            description = item.optString("description").trim().take(160),
            enabled = item.optBoolean("enabled", true),
            hasSystemPrompt = item.optString("system_prompt").isNotBlank(),
            bodyCount = item.optJSONObject("body")?.length() ?: 0,
            headerCount = item.optJSONObject("headers")?.length() ?: 0,
        )
    }
    val providerPresetsJson = root.optJSONObject("provider_presets")
    val providerPresets = buildMap {
        providerPresetsJson?.keys()?.forEach { key ->
            val ids = providerPresetsJson.optJSONArray(key) ?: return@forEach
            put(key, (0 until ids.length()).mapNotNull { ids.optString(it).trim().ifBlank { null } })
        }
    }
    return PresetsView(presets = presets, providerPresets = providerPresets)
}

/** 单账号读额度路径（`gateway.go:1116` `POST /api/accounts/{id}/refresh`）。 */
internal fun accountRefreshPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/refresh"

/**
 * 渠道根本没有额度接口时的统一文案。
 * 必须与"没读过额度"用不同句子说，否则用户分不清"上游不提供"和"还没拉过"。
 */
internal const val QUOTA_UNSUPPORTED_COPY = "该渠道不提供额度接口"

/** 服务端只认 delete / refresh / ready；本 App 只用 refresh（另外两个会删账号/重置状态，不在移动端做）。 */
internal fun buildAccountBatchJson(action: String, ids: List<String>): String {
    val json = JSONObject()
    json.put("action", action)
    json.put("ids", JSONArray(ids))
    return json.toString()
}

internal const val BATCH_ACTION_REFRESH = "refresh"

/** 批量删除（`handleAccountBatch` 认 delete/refresh/ready，`accounts_api.go`）。 */
internal const val BATCH_ACTION_DELETE = "delete"

/** 空列表或含非法 id 时在发请求前就拒掉，避免打一轮空批量。 */
internal fun validateBatchIds(ids: List<String>): String? {
    if (ids.isEmpty()) {
        return "batch_ids_empty"
    }
    for (id in ids) {
        validateAccountId(id)?.let { return it }
    }
    return null
}

internal fun accountCheckinPath(accountId: String): String =
    "/api/accounts/${encodeAccountSegment(accountId)}/checkin"

internal fun encodeAccountSegment(accountId: String): String =
    URLEncoder.encode(accountId.trim(), "UTF-8").replace("+", "%20")

/** 只把 `disabled` 放进 body：这个端点只改启用位，不带别的字段。 */
internal fun buildAccountDisabledJson(disabled: Boolean): String =
    JSONObject().put("disabled", disabled).toString()

/** 空 id / 带路径分隔符的 id 在发请求前就拒掉。 */
internal fun validateAccountId(accountId: String): String? {
    val trimmed = accountId.trim()
    return when {
        trimmed.isEmpty() -> "account_id_empty"
        trimmed.any { it.isWhitespace() || it in "/?#&=%" } -> "account_id_invalid"
        else -> null
    }
}

private fun objectOrThrow(body: String, label: String): JSONObject = try {
    JSONObject(body.trim())
} catch (_: JSONException) {
    throw ManagementApiException("invalid_response", "$label 返回的数据格式不正确")
}

/** `GET /api/accounts` → `{"accounts":[…]}`；空数组是合法结果，缺字段不是。 */
internal fun parseAccounts(body: String): List<AccountQuotaRow> {
    val root = objectOrThrow(body, "账号列表")
    val array = root.optJSONArray("accounts")
        ?: throw ManagementApiException("invalid_response", "账号列表缺少 accounts 字段")
    if (array.length() == 0) {
        return emptyList()
    }
    val rows = (0 until array.length()).mapNotNull { index ->
        array.optJSONObject(index)?.let(::parseAccountObject)
    }
    // 有元素却一条都解析不出来 = 格式错了，不能静默当成"还没有账号"。
    if (rows.isEmpty()) {
        throw ManagementApiException("invalid_response", "账号列表返回的数据格式不正确")
    }
    return rows
}

/**
 * `POST /api/accounts/batch` → `{"results":[{id,ok,error?}],"ok":N,"total":M}`。
 *
 * 自校验：results 的条数必须等于回包里的 `total`、其中 ok 的条数必须等于回包里的 `ok`。
 * 对不上说明这不是完整回包，宁可报 `invalid_response` 也不拿它去凑「成功 N」。
 */
internal fun parseAccountBatch(body: String): AccountBatchOutcome {
    val root = objectOrThrow(body, "批量操作")
    val array = root.optJSONArray("results")
        ?: throw ManagementApiException("invalid_response", "批量操作缺少 results 字段")
    val results = (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        AccountBatchEntry(
            id = id,
            ok = item.optBoolean("ok", false),
            error = item.optString("error").trim().take(200),
        )
    }
    if (results.size != array.length()) {
        throw ManagementApiException("invalid_response", "批量结果里有无法解析的条目")
    }
    if (root.has("total") && root.optInt("total", -1) != results.size) {
        throw ManagementApiException("invalid_response", "批量结果的条数与 total 不一致")
    }
    if (root.has("ok") && root.optInt("ok", -1) != results.count { it.ok }) {
        throw ManagementApiException("invalid_response", "批量结果的成功数与 ok 不一致")
    }
    return AccountBatchOutcome(results = results)
}

/** `POST /api/accounts/{id}/settings` → `{"account":<snapshot>}`。 */
internal fun parseAccountSnapshot(body: String): AccountQuotaRow {    val root = objectOrThrow(body, "账号设置")
    val account = root.optJSONObject("account")
        ?: throw ManagementApiException("invalid_response", "账号设置没有返回账号快照")
    return parseAccountObject(account)
        ?: throw ManagementApiException("invalid_response", "账号快照缺少 account_id")
}

/** `POST /api/accounts/{id}/checkin` → `{"checkin":{…},"outcome":…}`；没有 checkin 时回 null。 */
internal fun parseCheckinAck(body: String): AccountCheckin? {
    val root = objectOrThrow(body, "签到结果")
    return root.optJSONObject("checkin")?.let(::parseCheckin)
}

/**
 * `POST /api/accounts/{id}/refresh` → `{"refreshed","refresh_supported","quota","quota_supported",
 * "quota_read","quota_error"?,"warning"?}`（`auth_api.go:339-352`）。
 *
 * `quota_supported` 是这个端点唯一必带的字段（同一份 map 里恒定写出），缺了就是坏回包。
 */
internal fun parseQuotaRead(accountId: String, body: String): AccountQuotaRead {
    val root = objectOrThrow(body, "额度读取")
    if (!root.has("quota_supported")) {
        throw ManagementApiException("invalid_response", "额度读取没有返回 quota_supported")
    }
    return AccountQuotaRead(
        accountId = accountId,
        quotaSupported = root.optBoolean("quota_supported", false),
        quotaRead = root.optBoolean("quota_read", false),
        quotaError = root.optString("quota_error").trim().take(400),
        warning = root.optString("warning").trim().take(200),
        snapshot = parseQuotaSnapshot(root.optJSONObject("quota")),
    )
}

/** 把一次读取的结果合并回卡片行（就地更新，不整页重建）。 */
internal fun AccountQuotaRow.withQuotaRead(read: AccountQuotaRead): AccountQuotaRow {
    val snapshot = read.snapshot
    return copy(
        quotaSupported = read.quotaSupported,
        // 读取成功时服务端不会带 quota_error（空串）→ 这里正好把上一次的错误清掉。
        quotaError = read.quotaError,
        quotaWindows = snapshot?.windows ?: quotaWindows,
        quotaTotal = snapshot?.total ?: quotaTotal,
        quotaUsed = snapshot?.used ?: quotaUsed,
        quotaUnit = snapshot?.unit ?: quotaUnit,
        quotaMessage = snapshot?.message ?: quotaMessage,
        quotaReadAt = snapshot?.readAt ?: quotaReadAt,
    )
}

/**
 * 读取结果 → 卡片上要显示的失败（null = 成功且没有需要报告的问题）。
 *
 * 三种情况互斥且文案不同：渠道没额度接口 / 上游读失败（原文）/ 凭证续期失败（原文）。
 */
internal fun quotaReadFailure(read: AccountQuotaRead): Pair<String, String>? = when {
    !read.quotaSupported -> "quota_unsupported" to QUOTA_UNSUPPORTED_COPY
    read.quotaError.isNotBlank() -> "quota_read_failed" to read.quotaError
    read.warning.isNotBlank() -> "credential_refresh_warning" to read.warning
    else -> null
}

internal fun parseCheckin(item: JSONObject): AccountCheckin = AccountCheckin(
    day = item.optString("day").trim(),
    outcome = item.optString("outcome").trim(),
    credit = item.optDouble("credit", 0.0).coerceAtLeast(0.0),
    streak = item.optInt("streak", 0).coerceAtLeast(0),
    message = item.optString("msg").trim().take(200),
    again = item.optBoolean("again", false),
)

private fun parseAccountObject(item: JSONObject): AccountQuotaRow? {
    val accountId = item.optString("account_id").trim()
    if (accountId.isEmpty()) {
        return null
    }
    val quota = item.optJSONObject("quota")
    val snapshot = parseQuotaSnapshot(quota)
    return AccountQuotaRow(
        accountId = accountId,
        providerId = item.optString("provider_id").trim(),
        label = item.optString("label").trim().ifBlank { accountId },
        disabled = item.optBoolean("disabled", false),
        resetAt = item.optLong("reset_at", 0L).coerceAtLeast(0L),
        lastUsed = item.optLong("last_used", 0L).coerceAtLeast(0L),
        // 老版本没有 quota_supported 字段时，按"有 quota 对象"推断，避免误报「不提供额度接口」。
        quotaSupported = if (item.has("quota_supported")) {
            item.optBoolean("quota_supported", false)
        } else {
            quota != null
        },
        quotaError = item.optString("quota_error").trim().take(400),
        checkinSupported = item.optBoolean("checkin_supported", false),
        checkin = item.optJSONObject("checkin")?.let(::parseCheckin),
        quotaWindows = snapshot?.windows.orEmpty(),
        quotaTotal = snapshot?.total ?: 0,
        quotaUsed = snapshot?.used ?: 0,
        quotaUnit = snapshot?.unit ?: "",
        quotaMessage = snapshot?.message ?: "",
        quotaReadAt = snapshot?.readAt ?: 0L,
        priority = item.optInt("priority", 0).coerceAtLeast(0),
        weight = item.optInt("weight", 0).coerceAtLeast(0),
        // concurrency 是 *int：缺省/为 null 表示跟随全局，不能和 0 混为一谈。
        concurrency = if (item.has("concurrency") && !item.isNull("concurrency")) {
            item.optInt("concurrency", 0).coerceAtLeast(0)
        } else {
            null
        },
        allowedModels = item.optJSONArray("allowed_models")?.let { array ->
            (0 until array.length()).mapNotNull { array.optString(it).trim().ifBlank { null } }
        }.orEmpty(),
        modelMapping = parseModelMapping(item.optJSONObject("model_mapping")),
        tags = item.optJSONArray("tags")?.let { array ->
            (0 until array.length()).mapNotNull { array.optString(it).trim().ifBlank { null } }
        }.orEmpty(),
        note = item.optString("note").trim().take(500),
    )
}

/** `quota` 对象（`QuotaStatus`，`contract.go:228-252`）→ 卡片要用的精简快照。 */
private fun parseQuotaSnapshot(quota: JSONObject?): AccountQuotaSnapshot? {
    if (quota == null) {
        return null
    }
    val windows = quota.optJSONArray("windows")?.let { array ->
        (0 until array.length()).mapNotNull { index -> array.optJSONObject(index)?.let(::parseQuotaWindow) }
    }.orEmpty()
    return AccountQuotaSnapshot(
        windows = windows,
        total = quota.optInt("total", 0).coerceAtLeast(0),
        used = quota.optInt("used", 0).coerceAtLeast(0),
        unit = quota.optString("unit").trim(),
        message = quota.optString("message").trim().take(200),
        readAt = quota.optLong("read_at", 0L).coerceAtLeast(0L),
    )
}

private fun parseQuotaWindow(item: JSONObject): AccountQuotaWindow = AccountQuotaWindow(
    name = item.optString("name").trim(),
    // 契约写明是 0-100 的百分比；上游万一给越界值就夹回区间，不显示 >100% / 负剩余。
    usedPercent = item.optDouble("used", 0.0).coerceIn(0.0, 100.0),
    resetsAt = item.optLong("resets_at", 0L).coerceAtLeast(0L),
    unit = item.optString("unit").trim(),
    amount = item.optLong("amount", 0L).coerceAtLeast(0L),
    limit = item.optLong("limit", 0L).coerceAtLeast(0L),
)
