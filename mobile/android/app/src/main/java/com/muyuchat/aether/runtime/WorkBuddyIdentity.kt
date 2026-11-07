package com.muyuchat.aether.runtime

/**
 * WorkBuddy 渠道的「出网身份」档位：一个渠道同一时刻只用一档。
 *
 * **wire 值必须与 Go 侧 `internal/providers/workbuddy/identity.go` 的
 * `resolveIdentityPreset` 完全一致**：`cli` / `desktop` / `quiet` / `browser`
 * （服务端对空值与非法值都回退 `cli`）。
 *
 * 这里**只保留档位与文案**：UA / 版本 / 请求间隔 / Stainless 一律从
 * `GET /api/providers/workbuddy/identity` 读（见 [WorkBuddyIdentityState]），
 * 客户端不再镜像 Go 的常量——否则 Go 升版本后页面会一直显示过期 UA。
 */
enum class WorkBuddyIdentity(
    val wireValue: String,
    val title: String,
    val detail: String,
) {
    CLI(
        wireValue = "cli",
        title = "官方 CLI（默认）",
        detail = "双段 UA + Stainless 头族，与 PC 客户端内置 CLI 一致",
    ),
    DESKTOP(
        wireValue = "desktop",
        title = "桌面客户端",
        detail = "单段 UA，不发 Stainless 头",
    ),
    QUIET(
        wireValue = "quiet",
        title = "安静",
        detail = "同桌面，但请求间隔 900ms",
    ),
    BROWSER(
        wireValue = "browser",
        title = "浏览器",
        detail = "使用 Chrome 125 UA，不发 Stainless 头",
    ),
    ;

    companion object {
        /** 默认档位：Go 侧空值 / 非法值都回退 cli。 */
        val Default: WorkBuddyIdentity = CLI

        /**
         * 解析 `workbuddy_identity` 的 wire 值。
         *
         * 空串 / 未知值一律按 cli（与服务端回退口径一致）；只读兼容参考实现的
         * 别名 `official`（= cli），界面保存时仍写 `cli`。
         */
        fun fromWire(raw: String?): WorkBuddyIdentity = fromWireOrNull(raw) ?: Default

        /** 严格解析：只认四个 wire 值（含 `official` 别名）；认不出返回 null。 */
        internal fun fromWireOrNull(raw: String?): WorkBuddyIdentity? {
            val value = raw?.trim()?.lowercase().orEmpty()
            if (value == "official") {
                return CLI
            }
            return WorkBuddyIdentity.entries.firstOrNull { it.wireValue == value }
        }
    }
}

/**
 * `GET /api/providers/workbuddy/identity` 的只读快照：**运行中进程真正在用的身份**。
 *
 * 与 `workbuddy_identity`（settings.json 里的保存值）是两回事：改档位只落盘，
 * 重启网关后才换。两边不同时用 [workBuddyIdentityMismatchNote] 明确说出来。
 */
data class WorkBuddyIdentityState(
    /** 生效档位；服务端给了客户端不认识的字符串时为 null（原文见 [effectiveRaw]）。 */
    val effective: WorkBuddyIdentity?,
    /** 服务端下发的原始档位字符串（用于「保存值 ≠ 生效值」的比较，不做本地归一）。 */
    val effectiveRaw: String,
    val userAgent: String,
    val product: String,
    val ideVersion: String,
    val cliVersion: String,
    val stainless: Boolean,
    val stainlessVersion: String,
    val paceMs: Int,
    val machineHeaders: Boolean,
    /** 服务端声明的可选档位；空 = 服务端没下发（UI 仍按本地四档渲染，不猜更多）。 */
    val available: List<WorkBuddyIdentity> = emptyList(),
    /** 服务端从 settings.json 读到的保存值（可选字段）。 */
    val saved: WorkBuddyIdentity? = null,
)

/** 生效档位的人类可读名；服务端给了不认识的字符串时原样显示，不冒充四档之一。 */
internal fun workBuddyIdentityLiveName(state: WorkBuddyIdentityState): String =
    state.effective?.title ?: state.effectiveRaw.ifBlank { "未知" }

/** 「是否发 Stainless 头」的人话说明（版本号取接口返回值，不写死）。 */
internal fun workBuddyStainlessLabel(state: WorkBuddyIdentityState): String = when {
    !state.stainless -> "不发"
    state.stainlessVersion.isBlank() -> "发送（X-Stainless-* 头族）"
    else -> "发送（X-Stainless-Lang js · 包版本 ${state.stainlessVersion}）"
}

/**
 * 「保存值 ≠ 运行中身份」的提示；两者一致时返回 null。
 *
 * 文案直接点名两个档位，比一句通用的「重启网关后生效」更能解释"为什么现在没变"。
 */
internal fun workBuddyIdentityMismatchNote(
    saved: WorkBuddyIdentity,
    state: WorkBuddyIdentityState,
): String? {
    if (state.effectiveRaw.trim().equals(saved.wireValue, ignoreCase = true)) {
        return null
    }
    return "已保存 ${saved.title}，当前运行仍是 ${workBuddyIdentityLiveName(state)}，重启网关后生效"
}

/** 「运行中身份」整块的可复制文本：档位 + UA + 两个固定头 + 间隔 + Stainless + 设备标识。 */
internal fun workBuddyIdentityCopyText(state: WorkBuddyIdentityState): String = buildString {
    appendLine("WorkBuddy 出网身份（运行中）：${workBuddyIdentityLiveName(state)}")
    appendLine("User-Agent: ${state.userAgent}")
    appendLine("X-Product: ${state.product}")
    appendLine("X-IDE-Version: ${state.ideVersion}")
    if (state.cliVersion.isNotBlank()) {
        appendLine("CLI 版本: ${state.cliVersion}")
    }
    appendLine("请求间隔: ${if (state.paceMs > 0) "${state.paceMs}ms" else "未上报"}")
    appendLine("Stainless: ${workBuddyStainlessLabel(state)}")
    append("X-Machine-ID / X-Session-ID: ${if (state.machineHeaders) "发送" else "不发"}")
}
