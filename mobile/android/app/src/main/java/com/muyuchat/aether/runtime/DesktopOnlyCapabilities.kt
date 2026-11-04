package com.muyuchat.aether.runtime

/**
 * 一条"手机上做不到"的能力说明。
 *
 * `reason` 必须回答**为什么**（权限 / 物理限制 / 安全边界），不是重复标题——
 * 用户看到"仅 Windows"时最想知道的就是这个。
 */
data class UnsupportedCapability(
    val title: String,
    val reason: String,
    val isByDesign: Boolean = false,
)

/**
 * 11 项仅 Windows 能力。
 *
 * 依据：`internal/mobile/runtime.go` 只绑 127.0.0.1；平台门禁 `platform.WSL` /
 * `platform.WindowsAutoImport` / `platform.DesktopApp`；架构文档第 7 条
 * 「不得扫描其它 App 私有目录、WSL、PC 进程」。
 */
internal fun desktopOnlyCapabilities(): List<UnsupportedCapability> = listOf(
    UnsupportedCapability(
        title = "WSL 发行版管理",
        reason = "要列/启停 WSL 发行版必须调用 Windows 的 wsl.exe；Android 沙箱里没有该进程，也无权跨设备控制 PC。",
    ),
    UnsupportedCapability(
        title = "PC 进程控制与启动器",
        reason = "启动/结束 PC 上的客户端需要访问 Windows 进程表；手机既看不到也操作不了另一台机器的进程。",
    ),
    UnsupportedCapability(
        title = "桌面窗口操作",
        reason = "窗口枚举/激活/截图依赖 Win32 窗口句柄，Android 没有对应能力。",
    ),
    UnsupportedCapability(
        title = "会话删除与终端",
        reason = "会话文件与终端都住在桌面 Agent 的家目录；手机端到不了那个路径，也没有可拉起的本地终端。",
    ),
    UnsupportedCapability(
        title = "桌面 Agent 目录扫描（发现 / market）",
        reason = "「发现」以桌面用户目录为根扫描，且架构禁止手机扫描其它 App 私有目录——这条是安全边界，不是缺功能。",
    ),
    UnsupportedCapability(
        title = "Windows 客户端自动导入",
        reason = "网关侧用 platform.WindowsAutoImport 门禁直接关掉；要读的是 Windows 已装客户端的登录态。",
    ),
    UnsupportedCapability(
        title = "打开文件所在位置（reveal）",
        reason = "reveal 调的是桌面文件管理器；Android 没有「打开所在文件夹」这种系统能力。",
    ),
    UnsupportedCapability(
        title = "准点后台调度",
        reason = "手机端定时器跑在网关进程里，系统省电策略会冻结或回收进程；要准点只能在桌面端执行。",
    ),
    UnsupportedCapability(
        title = "把配置写入桌面客户端",
        reason = "注入/还原要改桌面客户端（Claude Code、Codex 等）的家目录文件；手机端没有那些路径。",
    ),
    UnsupportedCapability(
        title = "供应商自定义图标写入",
        reason = "图标没有列表接口，写入还依赖桌面文件布局；手机端只能读内置图标。",
    ),
    UnsupportedCapability(
        title = "自动更新下载与安装",
        reason = "桌面壳自行下载安装包；手机端要走应用商店或未知来源安装，属于产品决策，不在本版承诺内。",
    ),
)

/** 按设计不做：不是"仅 Windows"，而是**整套产品口径**都不保存正文。 */
internal fun byDesignNotSupported(): UnsupportedCapability = UnsupportedCapability(
    title = "请求正文查看",
    reason = "账本记录（ledgerRecord）根本没有正文字段——请求正文从不落盘。所以 /api/usage/requests/content 只能返回未找到；" +
        "要「查看正文」必须先改隐私模型把正文存下来，那是产品决策，不是补一个手机页面能解决的。",
    isByDesign = true,
)
