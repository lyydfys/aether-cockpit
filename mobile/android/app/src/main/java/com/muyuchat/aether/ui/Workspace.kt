package com.muyuchat.aether.ui

/** Five primary mobile entries from the approved design. */
enum class MobileTab(
    val label: String,
    val subtitle: String,
    val stage: String? = null,
) {
    OVERVIEW("概览", "状态、地址与启停"),
    CHANNELS("渠道", "账号、渠道与额度", "Stage 4"),
    USAGE("用量", "本机用量与成本", "Stage 5"),
    REQUESTS("请求", "请求账本与诊断", "Stage 5"),
    MORE("更多", "全部工作区与设置入口"),
}

/**
 * 「更多」页每一行的真实去处。
 *
 * 上一版把去向藏在 `title == "路由"` 这类字符串比较里，于是 17 个工作区里只有 3 个真跳，
 * 用量/请求/渠道明明已经能用却点了弹「后续阶段」。改成显式的 sealed 类型后，
 * 「这一行到底去哪」是数据的一部分，可以被单测锁住，也不会再靠标题字符串猜。
 */
sealed interface WorkspaceAction {
    /** 切到某个真实存在的底部 Tab。 */
    data class Tab(val tab: MobileTab) : WorkspaceAction

    /** 打开路由页（真实页面，非 Tab）。 */
    data object Routing : WorkspaceAction

    /** 打开设置页（真实页面，非 Tab）。 */
    data object Settings : WorkspaceAction

    /** 打开网关探针页（真实页面，非 Tab）。 */
    data object Probe : WorkspaceAction

    /** 打开 2FA（TOTP）页（真实页面，非 Tab）。 */
    data object Totp : WorkspaceAction

    /** 打开插件页（真实页面，只读：规则与内置插件说明）。 */
    data object Plugins : WorkspaceAction

    /** 打开客户端页（真实页面，只读：客户端清单与检测状态）。 */
    data object Clients : WorkspaceAction

    /** 打开会话页（真实页面，只读；手机端通常为空，见页面说明）。 */
    data object Sessions : WorkspaceAction

    /** 打开唤醒队列页（真实页面：只读状态 + 手动触发，不承诺定时）。 */
    data object Wake : WorkspaceAction

    /** 打开资源库页（真实页面：查看与删除，发现/下发仅桌面端）。 */
    data object Library : WorkspaceAction

    /** 尚未实现：显示明确阶段占位，不伪装成可用。 */
    data class Pending(val stage: String) : WorkspaceAction

    /** 手机端没有同义能力：显式标注"仅 Windows"，不提供可执行按钮。 */
    data object DesktopOnly : WorkspaceAction
}

/** One of the 17 Windows workspaces exposed on mobile. */
data class WorkspaceEntry(
    val title: String,
    val subtitle: String,
    val action: WorkspaceAction,
) {
    val desktopOnly: Boolean get() = action is WorkspaceAction.DesktopOnly

    /** 未实现时展示的阶段标签；已接线或仅桌面端时为 null。 */
    val pendingStage: String? get() = (action as? WorkspaceAction.Pending)?.stage
}

/**
 * One of the 9 Windows settings groups.
 *
 * [available] 表示该组的内容**今天真的能在设置页里找到**。只有 02（网络与共享：端口/局域网/
 * 代理/CORS/IP 白名单）与 06（隐私与脱敏：脱敏开关、动作、自定义规则、个人信息）为真；
 * 其余仍在后续阶段，点了要如实说，不能因为"设置页存在"就宣称 9 组都能用。
 */
data class SettingsGroup(
    val index: String,
    val title: String,
    val subtitle: String,
    val available: Boolean = false,
)

fun workspaceEntries(): List<WorkspaceEntry> = listOf(
    WorkspaceEntry("概览", "本机网关状态与入口", WorkspaceAction.Tab(MobileTab.OVERVIEW)),
    WorkspaceEntry("渠道接入", "登录、新增账号、导入；PC 扫描仅 Windows", WorkspaceAction.Tab(MobileTab.CHANNELS)),
    WorkspaceEntry("账号额度", "额度、优先级与并发", WorkspaceAction.Tab(MobileTab.CHANNELS)),
    WorkspaceEntry("用量", "成本、Token 与趋势", WorkspaceAction.Tab(MobileTab.USAGE)),
    // 签到调度器已随移动运行时接线（Go 侧），但手机端还没有独立的签到页面，
    // 所以这里不再写"签到仅 Windows"——那会变成一句假话。
    WorkspaceEntry("任务", "唤醒队列（签到调度已接线）", WorkspaceAction.Wake),
    WorkspaceEntry("应用", "启动 PC 客户端；仅 Windows", WorkspaceAction.DesktopOnly),
    WorkspaceEntry("会话", "本机 Agent 会话（只读）", WorkspaceAction.Sessions),
    WorkspaceEntry("资源库", "指令、技能、MCP、子代理（查看/删除）", WorkspaceAction.Library),
    WorkspaceEntry("请求", "请求日志与诊断", WorkspaceAction.Tab(MobileTab.REQUESTS)),
    WorkspaceEntry("网关探针", "健康、版本与指标", WorkspaceAction.Probe),
    WorkspaceEntry("2FA", "TOTP 验证码", WorkspaceAction.Totp),
    WorkspaceEntry("设置", "常规、网络、隐私与备份", WorkspaceAction.Settings),
    WorkspaceEntry("Agent", "客户端接入片段（只读）", WorkspaceAction.Clients),
    WorkspaceEntry("路由", "模型映射与回退", WorkspaceAction.Routing),
    WorkspaceEntry("脱敏", "隐私与请求改写", WorkspaceAction.Settings),
    WorkspaceEntry("插件", "请求改写规则与内置插件（只读）", WorkspaceAction.Plugins),
    WorkspaceEntry("额度快捷查看", "渠道余量速览", WorkspaceAction.Tab(MobileTab.CHANNELS)),
)

fun settingsGroups(): List<SettingsGroup> = listOf(
    SettingsGroup("01", "常规", "外观、语言、通知、更新与启动策略"),
    SettingsGroup("02", "网络与共享", "监听端口、局域网、代理、CORS 与访问控制", available = true),
    SettingsGroup("03", "工具服务", "搜索、图像、音频、视频与实时语音"),
    SettingsGroup("04", "预设", "客户端、模型与请求参数模板"),
    SettingsGroup("05", "定价", "模型单价、自定义倍率与成本估算"),
    SettingsGroup("06", "隐私", "脱敏规则、请求正文与本机日志留存", available = true),
    SettingsGroup("07", "可观测", "OTLP、指标、链路与运行诊断"),
    SettingsGroup("08", "同步与备份", "本机加密备份；外部同步需主动配置"),
    SettingsGroup("09", "关于与更新", "版本、更新通道、许可与诊断包"),
)

fun filterWorkspaces(query: String): List<WorkspaceEntry> = workspaceEntries().filter { entry ->
    matches(query, entry.title, entry.subtitle)
}

fun filterSettings(query: String): List<SettingsGroup> = settingsGroups().filter { group ->
    matches(query, group.title, group.subtitle)
}

private fun matches(query: String, vararg fields: String): Boolean {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return true
    }
    return fields.any { it.contains(needle, ignoreCase = true) }
}
