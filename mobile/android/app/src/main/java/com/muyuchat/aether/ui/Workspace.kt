package com.muyuchat.aether.ui

/** Five primary mobile entries from the approved design. */
enum class MobileTab(
    val label: String,
    val subtitle: String,
    val stage: String? = null,
) {
    OVERVIEW("概览", "状态、地址与启停"),
    // 桌面端把这一页叫「账号与额度」（账号、模型与余量一眼看到底）。
    // 手机上底栏只有两个字的位置，所以 Tab 用「额度」，页内标题仍写全称。
    CHANNELS("额度", "账号、模型与余量", "Stage 4"),
    USAGE("用量", "本机用量与成本", "Stage 5"),
    REQUESTS("请求", "请求账本与诊断", "Stage 5"),
    MORE("更多", "全部工作区与设置入口"),
}

/**
 * 「更多」页每一行的真实去处。
 *
 * 上一版把去向藏在 `title == "路由"` 这类字符串比较里，于是只有 3 个真跳，
 * 用量/请求/渠道明明已经能用却点了弹「后续阶段」。改成显式的 sealed 类型后，
 * 「这一行到底去哪」是数据的一部分，可以被单测锁住，也不会再靠标题字符串猜。
 *
 * **条目数是 13**（见 [workspaceEntries] 的说明）：桌面 16 项导航里，
 * 概览 / 账号与额度 / 用量 / 请求日志 已经在手机底栏的 4 个 Tab 上，不再重复列出；
 * 剩下 12 项 + 「客户端」（桌面是 `gateway.js:113` 的一张卡）＝ 13。
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

    /**
     * 打开 Agent 接入页（真实页面，只读：本机 agent 的模型清单与 CLI 巡检）。
     *
     * 与 [Clients] 是两件事：桌面端「Agent 接入」（`misc.js:10`）看的是各 agent 的
     * **模型**，「客户端接入片段」（`gateway.js:113`）看的是注入状态。手机端曾经把
     * 「Agent」错指到客户端页，这个成员就是为修掉那次错接而加的（DESKTOP_PARITY_GAPS §2-12）。
     */
    data object Agents : WorkspaceAction

    /** 打开唤醒队列页（真实页面：只读状态 + 手动触发，不承诺定时）。 */
    data object Wake : WorkspaceAction

    /** 打开资源库页（真实页面：查看与删除，发现/下发仅桌面端）。 */
    data object Library : WorkspaceAction

    /** 打开「接入渠道」页（真实页面：桌面 `views/onboard.js` 的落点，含 Qoder 登录与账号包）。 */
    data object Onboard : WorkspaceAction

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
 * 设置分组的**路由目的地**（数据驱动分发，不再靠标题/序号字符串比较）。
 *
 * - `Partition`：01–08，内容就在设置页里的某个分区（`SettingsPage(initialGroup=...)`）；
 * - `Backup`：09 同步与备份 → 独立的 `BackupPage`；
 * - `About`：10 关于 → `SettingsAboutSection`。
 */
sealed interface SettingsRoute {
    data class Partition(val id: String) : SettingsRoute
    data object Backup : SettingsRoute
    data object About : SettingsRoute
}

/**
 * One of the 9 Windows settings groups.
 *
 * [available] 表示该组的内容**今天真的能在设置页里找到**。
 * [route] 是点击后的目的地；09/10 有了 route 就是真能到的独立目的地。
 */
data class SettingsGroup(
    val index: String,
    val title: String,
    val subtitle: String,
    val available: Boolean = false,
    val route: SettingsRoute,
)

/**
 * 「更多」页的条目 = 桌面 16 项导航里**底栏放不下**的那些。
 *
 * 桌面导航（`internal/web/ui2/js/shell.js:17-36`）共三组 12 项：工作（概览 / 接入渠道 /
 * 账号与额度 / 用量 / 唤醒任务 / 应用启动）、资产（会话 / 库）、系统（请求日志 /
 * 网关与探针 / 2FA 管理 / 设置）；`shell.js:38-43` 另有「更多」4 项（Agent 接入 / 路由 /
 * 脱敏 / 插件）。
 *
 * 手机底栏只有 5 格：概览 / 额度(=账号与额度) / 用量 / 请求(=请求日志) / 更多。
 * 所以那 4 项**不能**再出现在本列表里——同一目的地出现两次，用户会以为是两个页面。
 * 剩下的 12 项（接入渠道 / 唤醒任务 / 应用启动 / 会话 / 库 / 网关与探针 / 2FA 管理 / 设置 /
 * Agent 接入 / 路由 / 脱敏 / 插件）全部在这里，再加 1 项「客户端」：
 * 桌面把它做成网关页的一张卡（`views/gateway.js:113`），手机端有独立页，需要入口。
 *
 * 标题与桌面逐字一致，方便逐条对照；副标题写手机端能做什么。
 */
fun workspaceEntries(): List<WorkspaceEntry> = listOf(
    // 工作（shell.js:18-25）
    WorkspaceEntry("接入渠道", "多平台登录：本机导入、浏览器授权、设备码、手动凭据", WorkspaceAction.Onboard),
    // 签到调度器已随移动运行时接线（Go 侧），但手机端还没有独立的签到页面，
    // 所以这里不再写"签到仅 Windows"——那会变成一句假话。
    WorkspaceEntry("唤醒任务", "唤醒队列（签到调度已接线）", WorkspaceAction.Wake),
    WorkspaceEntry("应用启动", "启动 PC 客户端；仅 Windows", WorkspaceAction.DesktopOnly),
    // 资产（shell.js:26-29）。会话页在手机端恒为空（`sessions.Scan()` 只读本机 agent 会话文件，
    // Android 上没有），已整页删除，不再列入工作区。
    WorkspaceEntry("库（指令/技能/MCP）", "指令、技能、MCP 与子代理（查看/删除）", WorkspaceAction.Library),
    // 系统（shell.js:30-35）；请求日志在底栏「请求」Tab，不重复列
    WorkspaceEntry("网关与探针", "健康、版本与指标", WorkspaceAction.Probe),
    WorkspaceEntry("2FA 管理", "TOTP 验证码", WorkspaceAction.Totp),
    WorkspaceEntry("设置", "网络与共享、隐私、同步与备份", WorkspaceAction.Settings),
    // 更多（shell.js:38-43）+ 客户端（桌面是 gateway.js:113 的卡）
    WorkspaceEntry("Agent 接入", "本机 agent 的模型与 CLI 巡检（只读）", WorkspaceAction.Agents),
    WorkspaceEntry("客户端", "客户端接入片段（只读）", WorkspaceAction.Clients),
    WorkspaceEntry("路由", "模型映射与回退", WorkspaceAction.Routing),
    WorkspaceEntry("脱敏", "隐私与请求改写", WorkspaceAction.Settings),
    WorkspaceEntry("插件", "请求改写规则与内置插件（只读）", WorkspaceAction.Plugins),
)

/**
 * 设置页的分组 = 桌面 `settings.js:44-47` 的 10 个 tab，**标题与顺序逐字照抄**：
 * 常规 / 网络与共享 / 工具服务 / 预设 / 定价 / 模型目录 / 隐私 / 可观测 / 同步与备份 / 关于。
 *
 * [available] 表示该组内容**今天真的能在手机的设置页里找到**：10 组全部已接内容
 * （01–08 → 设置页对应分区；09 同步与备份 → BackupPage；10 关于 → SettingsAboutSection）。
 * [route] 给出分发目的地，GatewayHome 不再按标题判断。
 */
fun settingsGroups(): List<SettingsGroup> = listOf(
    SettingsGroup("01", "常规", "外观、语言、通知、更新与启动策略", available = true, route = SettingsRoute.Partition("01")),
    // 出网身份在 02 组里（WorkBuddy 渠道的 UA/头族档位）；写进副标题让设置页搜索能命中。
    SettingsGroup("02", "网络与共享", "监听、局域网、代理、访问控制与出网身份", available = true, route = SettingsRoute.Partition("02")),
    SettingsGroup("03", "工具服务", "搜索、图像、音频、视频与实时语音", available = true, route = SettingsRoute.Partition("03")),
    SettingsGroup("04", "预设", "客户端、模型与请求参数模板", available = true, route = SettingsRoute.Partition("04")),
    SettingsGroup("05", "定价", "模型单价、自定义倍率与成本估算", available = true, route = SettingsRoute.Partition("05")),
    SettingsGroup("06", "模型目录", "模型重定向与目录刷新", available = true, route = SettingsRoute.Partition("06")),
    SettingsGroup("07", "隐私", "脱敏开关、动作、自定义规则与个人信息", available = true, route = SettingsRoute.Partition("07")),
    SettingsGroup("08", "可观测", "OTLP、指标、链路与运行诊断", available = true, route = SettingsRoute.Partition("08")),
    SettingsGroup("09", "同步与备份", "本机加密备份；云同步需主动配置", available = true, route = SettingsRoute.Backup),
    SettingsGroup("10", "关于", "版本、更新通道、许可与诊断包", available = true, route = SettingsRoute.About),
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
