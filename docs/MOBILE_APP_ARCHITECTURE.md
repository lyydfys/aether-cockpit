# Aether Cockpit Mobile App 架构方案

版本：1.0 · 日期：2026-11-02 · 状态：实施前架构基线

本文定义把现有 Windows 网关做成手机独立网关 App 的可执行路线。目标读者可能只拿到一张实现任务卡，因此每阶段都包含目标、文件写集、接口、验证命令和停止条件。没有通过阶段验收，不进入下一阶段。

当前网页设计稿 [mobile-local-gateway-concept.html](design/mobile-local-gateway-concept.html) 只作业务内容和信息架构参考；生产导航、主题、组件和动效以 `E:\MAC` 当前版 Compose 设计系统为准。

## 1. 产品与仓库边界

`E:\MAC` 是唯一 MCA 设计与工程标准来源。`E:\MAC项目` 当前不存在；`E:\MCA-FRONTEND-EXPORT-20261016` 是旧导出，不得作为实现基线。MCA 规范和源码只读用于对齐，除非用户另外授权，不在 MCA 仓库实施本项目功能。

首发是独立 Android 应用：手机持有自己的渠道凭据、账号池、路由、用量、日志和设置；网关只绑定本机回环地址。请求模型时手机仍需联网到用户配置的上游服务商。手机数据与 Windows 数据不自动合并，也不操作 Windows 上的进程、WSL 或客户端文件。

| 决策 | 方案 | 原因 / 边界 |
| --- | --- | --- |
| UI 技术 | Kotlin + Jetpack Compose + Material 3 | 与 `E:\MAC` 当前 Android 技术栈一致；不用 HTML 原型、Wails 或另一套跨平台皮肤作为生产 UI |
| MCA 视觉 | 依赖版本化的 `com.muyuchat.mca:designsystem` AAR | 直接复用 `McaTheme`、主题 token、按钮/控件、图标和动效；不复制旧导出包，也不另造颜色/圆角系统 |
| Aether 后端 | 现有 Go provider、协议转换、路由、账号池和 HTTP API | 保留业务实现；移动端只新增生命周期/JNI/平台适配层 |
| Go 到 Android | `c-shared` 共享库 + 极窄 JNI 包装 | 只导出启动、状态、刷新落盘、停止；HTTP 仍承载稳定的版本化 JSON API；不把整个 Gateway 暴露成 gomobile 对象图 |
| 管理 UI 到 Go | Compose 通过本机 HTTP 调管理 API | 可复用已实现的管理端点；移动模式必须为管理请求增加独立会话凭据，不能依赖 loopback 地址代表 App 身份 |
| 数据位置 | Android 私有 `noBackupFilesDir` / `filesDir` | 所有账号、设置、SQLite、用量、日志和缓存留在应用沙箱；默认不进入 Android 云备份 |
| iOS | Android 稳定后单独评估 SwiftUI + Go 静态库 | MCA 当前只有 Android Compose 视觉实现；iOS 长期后台网关不能承诺，首期目标不包含 iOS 常驻服务 |

MCA 当前没有发布版设计系统 AAR。阶段 0 必须先为 `E:\MAC\core\designsystem` 建立只发布设计模块的版本化 Maven artifact，或者得到 MCA 维护者提供的等价只读 Maven 坐标。Aether 不得硬编码 `E:\MAC`、使用本机绝对路径 composite build 作为 CI 依赖，或自行维护第二套 token。若 artifact 无法取得，暂停 UI 阶段并交付阻断说明，不得用临时复制伪装成已完成对齐。

### ADR-001：平台语言与共享边界

选“每个平台使用官方原生 UI + Go 共享网关核心 + 版本化 JSON/HTTP 契约”。这是当前约束下最合适的现代架构：MCA 设计系统由 Jetpack Compose 实际实现；Android 前台 Service、Keystore、SAF 需要一等平台 API；Go 已有完整 Provider/IR/Gateway 代码，不应为移动版重写核心；iOS 进入范围时 SwiftUI/Keychain 直接映射同一产品语义。共享业务规则集中在 Go；客户端只承担展示、操作编排、系统权限和生命周期。

| 候选 | 结论 | 原因 |
| --- | --- | --- |
| Kotlin/Compose（Android）+ SwiftUI（iOS）+ Go core | 采用 | Android 与 MCA 设计系统原生对齐；iOS 使用系统导航/安全存储；两端复用 Go 核心，不复制协议/路由逻辑 |
| Flutter / React Native | 不采用 | 需要再建立第三方主题组件层；Android foreground service、SAF、Keychain、Go FFI 仍需原生桥；与 MCA Compose 复用弱 |
| Kotlin Multiplatform Compose UI | 不作为首发 | MCA 现有视觉组件只有 Compose/Android 运行合同；共享 Go 核心后再复制业务逻辑到 Kotlin 没收益；iOS 仍要适配后台和 Keychain |
| Wails / WebView 直包 HTML | 禁止作为生产 App | 现有 Wails 是桌面壳，网页稿只表达 IA；WebView 不能替代 Android/iOS service 生命周期及 MCA 原生交互 |

“顶级”由稳定契约、单一资源 owner、平台正确的安全/生命周期和可重复验收实现，不以增加框架或层数实现。Android 客户端用 Kotlin；iOS 客户端用 Swift；协议/调度/渠道实现只用 Go。禁止在 Kotlin/Swift 再实现一份账号池或模型路由规则。

## 2. 目标架构

```text
Android Compose App
  MCA Theme / Components / Motion
  Screens -> Route ViewModels -> typed repositories / use cases
                     |                         |
                     | management JSON API     | JNI: start/status/flush/stop
                     v                         v
             127.0.0.1:<ephemeral port>   Android Foreground Service
                     |                         |
                     +---- Go HTTP Server <----+
                              |
                  Aether Runtime Lifecycle Owner
                    | Gateway + Provider Registry
                    | Account Pool + Model Router
                    | Usage / Ledger / SQLite / Scheduler
                    | Platform Ports (data dir, key provider, local auth)
                              |
                    user-selected upstream providers
```

`MainActivity` 只负责 Compose 装配和系统 Activity Result（文件选择、浏览器回调）。`AetherGatewayService` 是 Android 网关运行状态的唯一 owner。Go `MobileRuntime` 是 Gateway、HTTP server、scheduler、autosave 和后台循环的唯一 owner。页面、ViewModel、Receiver 和 JNI callback 都不得各自启动第二个 Gateway。

Android Service 状态机固定为：

```text
Stopped -> Starting -> Running -> Stopping -> Stopped
                    \-> Failed -> Stopped
```

所有转移通过 `GatewayServiceController` 串行化。`Start` 幂等；`Stop` 可重复；失败状态带稳定错误码和可读说明。Activity 销毁、旋转或 Compose 重组不停止 Service。用户从通知停止、系统杀进程、应用升级、存储不足、端口无法绑定都必须进入可观测状态。

## 3. MCA 设计语言合同

实现前确认 AAR 导出的公开 API 和版本，当前源码参照如下：

- 主题与系统栏：`E:\MAC\core\designsystem\src\main\java\com\muyuchat\core\designsystem\McaTheme.kt`
- 尺寸、形状、描边和色调：同目录 `McaTokens.kt`
- 按压和内容动效：`McaMotion.kt`、`McaPageMotion.kt`、`SmoothPage.kt`
- 图标和图标底板：`McaIcons.kt`、`McaIconPlate.kt`
- 交互组件：`McaButton.kt`、`McaButtons.kt`、`McaControls.kt`、`McaDialog.kt`、`McaFeedback.kt`
- 规范与截图：`E:\MAC\docs\architecture\MCA_PROJECT_HARD_LOCK.md` 第 9 节；`E:\MAC\docs\assets\screenshots\01-home.png`、`02-workspace-nav.png`、`05-settings.png`

实施要求：

1. 页面根部只使用 `McaTheme`；色彩只取 `MaterialTheme.colorScheme` 或 MCA 导出的 token。禁止在 feature 中硬编码品牌色、另设 `AetherTheme` 或局部覆盖全局主题。
2. 当前基准使用单一 Google 蓝强调色。Light：primary `#1A73E8`、secondary `#185ABC`、background `#F8F9FA`、surface `#FFFFFF`、surfaceVariant `#F1F3F4`、onSurface `#202124`、onSurfaceVariant `#5F6368`。Dark：primary `#8AB4F8`、background `#121212`、surface `#202124`、surfaceVariant `#303134`、onSurface `#E8EAED`、onSurfaceVariant `#9AA0A6`。这些值作为 AAR parity test 的核对值，不作为复制 token 的理由。
3. 几何跟随 `McaShape`：badge 10dp、card action 12dp、card 20dp、sheet 28dp、胶囊只用于真实胶囊控件；MCA hairline 0.5dp。图标使用 MCA 图标族及其 16/18/20/24dp 尺寸，平台品牌图标才使用渠道资产。
4. 不另造字号级数。Typography 使用 MCA/Material 主题和现有 MCA 页面实际尺寸；首屏、列表、对话框必须对照现有截图，不以本方案里的网页原型字号当规格。
5. 点击范围至少 48dp，即使图形 plate 只有 28/32/40dp。支持 TalkBack 标签、焦点顺序、系统字体缩放、返回手势、IME、状态栏/导航栏 inset 和横屏/窄宽度重排。
6. 动效使用 MCA motion API；减少动态效果时尊重 Android Animator duration scale 与系统无障碍设置。禁止装饰性无限动画、渐变/模糊换肤、每行增加卡片。
7. 导航按 MCA 当前 AppTab 工作区切换、抽屉/菜单入口和页面 overlay 状态保存模式实现。上一版 HTML 的固定五栏只表达功能分类，不能直接照抄为 Compose 导航。
8. 每个视觉阶段对照 MCA 浅色/深色基准屏幕，并运行下文指定的截图、字体缩放、无障碍与动效验收。设计稿只作页面清单和内容顺序参考。

## 4. Aether Android 源码结构

在本仓库新增以下目录。阶段可以逐步创建文件；不得一次生成空模块或无调用者的抽象层。

```text
mobile/android/
├── settings.gradle.kts
├── build.gradle.kts
├── gradle/libs.versions.toml
├── app/                                  # 唯一 Android application/composition root
│   └── src/main/java/com/muyuchat/aether/
│       ├── MainActivity.kt               # 只装配主题、导航、系统 picker
│       ├── AetherApplication.kt           # 构造 app scoped dependencies
│       ├── navigation/AetherNavigation.kt
│       ├── runtime/GatewayServiceController.kt
│       ├── runtime/AetherGatewayService.kt
│       └── di/AetherAppContainer.kt
├── feature/gateway/                      # 概览、启停、入口地址、平台状态
├── feature/channels/                     # Manifest 目录、账号、OAuth/设备码
├── feature/usage/                        # 用量/账本/请求日志
├── feature/routing/                      # 路由组、模型映射
├── feature/settings/                     # 网络、隐私、备份、安全
├── core/aether-contract/                  # Kotlin typed DTO 与 repository port
├── core/aether-data/                      # OkHttp API client、JSON、SSE、缓存
├── core/android-platform/                 # Keystore、SAF、通知、前台 service adapter
└── native/
    ├── go/                                # Go c-shared bridge 与 build config
    └── jni/                               # 仅 string/int/status 的 JNI C/C++ wrapper
```

Gradle 坐标 pin MCA design system 版本。Compose Material、Kotlin、AGP 版本优先和该 artifact 发布版本的兼容矩阵一致，不照抄整个 MCA `libs.versions.toml`。`app` 可依赖所有 feature；feature 只依赖 `core:aether-contract`、`core:aether-data` 中其所需的公共契约和 `:core:designsystem` artifact，禁止 feature 反向依赖 app 或其它 feature。

## 5. Go 核心与移动端运行时

### 5.1 当前可复用部分

Go `Gateway` 本身实现 `http.Handler`，可继续承载 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、Gemini、账号/渠道、路由、用量、请求日志、2FA、设置和备份等 API。复用边界是 Gateway/Provider/IR/Pool/Router/State 业务包；不复用 Wails 窗口、托盘、Windows 客户端文件扫描和 `cmd/desktop` 生命周期。

`cmd/cockpit` 的 headless 启动路径是现有服务装配参考，但手机不得直接调用该命令或复制整段 main。先新增可测试的移动运行时 owner，再让 headless/desktop 以后选择是否复用它；本项目移动切片不做无关全平台重构。

### 5.2 `MobileRuntime` 契约

新增 `internal/mobile/runtime.go`（具体拆分由阶段任务决定）管理单次启动的完整依赖。首版公共 Go 语义：

```go
type RuntimeConfig struct {
    DataDir string
    ListenHost string // 固定 127.0.0.1
    Port int          // 0 表示系统分配临时端口
    AdminSessionKey string
    CallerKeyRequired bool
    Platform string // android | ios；拒绝未知值
}

type RuntimeStatus struct { State string; Addr string; StartedAt int64; ErrorCode string }
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, error)
func (r *Runtime) Status() RuntimeStatus
func (r *Runtime) Flush(ctx context.Context) error
func (r *Runtime) Stop(ctx context.Context) error
```

这是设计签名，不可一次粘贴为未测试的空 facade。`Start` 必须：创建/校验 DataDir → 设置所有持久化路径 → 构造允许的 Provider Registry → 初始化 Gateway → 恢复账号与状态 → 创建 `127.0.0.1:0` listener → 得到实际 port → 启动受 ctx 管理的 scheduler/autosave/token refresh/monitor → 返回 ready 状态和管理凭据状态。遇错按逆序关闭已创建资源并返回稳定错误。

`Stop` 必须先阻止新管理写入和新网关请求，再取消调度器/监控/refresh/auto-import，graceful shutdown HTTP，flush 账号/状态/用量/账本，关闭 SQLite/文件句柄，最后清除内存管理 token。重复调用安全。后台切换调用 `Flush`/平台策略，不用 Activity `onDestroy` 伪装 Service 停止。

现有 `gateway.New` 在构造期间即启动 WSL 同步/扫描、会话预热、keep-awake、monitor、wake 和 auto-import 等逻辑，且没有统一 `Close`。移动开关在代码中默认为关闭不够；必须把循环变为 context 生命周期，或通过显式 `PlatformCapabilities` 禁用并由 `Runtime.Stop` 等待退出。停止 HTTP server 但留下 goroutine 不算关停成功。

### 5.3 移动 JNI ABI

JNI 只暴露稳定窄接口，不暴露 `Gateway`、`http.Handler`、Go map/interface、`time.Duration` 或任意 Go 指针：

```text
nativeStart(configJson: String): String       # JSON {state, baseUrl, adminSession, errorCode}
nativeGetStatus(): String                     # JSON RuntimeStatus
nativeFlush(timeoutMillis: Long): String      # JSON {ok, errorCode}
nativeStop(timeoutMillis: Long): String       # JSON {ok, errorCode}
nativeFreeString(pointer: Long)               # 仅当 ABI 用 C 字符串返回时需要
```

实现使用 Go `-buildmode=c-shared` 输出每个 ABI 的 Go library，NDK 编译极薄 JNI adapter 放入 `app/src/main/jniLibs/<abi>/`。所有 `JNIEnv`/`jstring` 转换留在 C/Kotlin 边界；C 字符串由同一 library 的 free 函数释放，禁止跨语言保留 Go 内存指针。桥接错误转成 `{code,message}`，不把 panic 或敏感上游响应直接带到 UI。

阶段 0 必须在完整 UI 开始前验证 Windows 开发机与 CI Linux/macOS Android toolchain 能生成并装载 arm64-v8a 产物，实际调用 start/status/stop 并在第二次 start 后确认没有旧监听。不要以 `go tool dist list` 中列出 Android 作为编译通过证据。

## 6. 数据目录、密钥与 API 安全

### 6.1 私有路径合同

移动运行时禁止使用相对 cwd 的 `data/`、`SetCurrentDirectory` 或进程级 `AETHER_DATA_DIR` 覆盖。现有 `apppaths.Data()` 是环境/相对路径型全局默认，并在 Gateway、clients、logging 等包直接调用。新增不可变 `apppaths.Paths` 值对象；移动端从 `AetherApplication` 取得 Android `Context` path 后注入 `Gateway.Config.Paths` 和依赖该值的 `clients`/`logging`/scheduler。桌面/headless 保留旧默认解析作为兼容 fallback；禁止把移动路径写入 package global。

`AetherDataPaths` 至少包含以下命名路径，先用实际 Go store 初始化点对照，不要只创建目录：

```text
<noBackupFilesDir>/aether/                 # accounts, settings, caller keys, scheduler
<noBackupFilesDir>/aether/state.db         # SQLite state/session snapshots
<noBackupFilesDir>/aether/usage/           # usage, ledger, quota history
<filesDir>/aether/cache/                   # models/spec/icons; cache may be reclaimed
<cacheDir>/aether/tmp/                     # temporary export/import staging
```

路径写集包括 `Gateway.Config` 的状态/设置/账号/用量/library/caller-key/auto-import/spec/cache 路径、scheduler checkin state、logging 和 clients manager。必须覆盖 Gateway 内部直接拼 `library.json`、`caller-keys.json`、`autoimport.json`、`checkin.json` 的路径。临时文件权限、原子 rename、磁盘满时错误反馈必须有单测。隐私偏好为不备份账号和运行数据；用户主动导出走系统 Sharesheet。

### 6.2 Keystore / AES-GCM

当前 `simplecrypt` 由 hostname、UserConfigDir、UserHomeDir 派生 key，移动端不适用且不是安全密钥存储。新增窄 `CredentialCipher` 接口：`Encrypt(plaintext, accountID, providerID)` / `Decrypt(ciphertext, accountID, providerID)`；Go 实现 AES-256-GCM 和版本化封套。Android Keystore 保存不可导出的 wrapping key；native host 生成随机 256-bit data key，用 Keystore AES-GCM 包装后只把包装密文写入 `noBackupFilesDir`，Go runtime 启动时由 native 解包并通过 JNI 把 data key 短暂交给 Go cipher。包装 key 永远不交给 Go；data key 不落盘/日志，Stop 时尽力清除 Go/native 缓冲。iOS 后续用 Keychain `ThisDeviceOnly` 保存/包装 data key。Go 不再通过 hostname 或 home 生成新数据密钥。

账号文件 v2 至少带 `format_version`、`key_id`、`nonce`、`ciphertext`、`provider_id`、`account_id`；AAD 绑定账号和渠道，防止密文跨账号替换。现存 v1 文件仅在同一安装、原 key source 仍可解密时只读迁移；迁移失败保留旧文件并阻止覆盖，UI 显示可恢复错误。Windows 的 `accounts.json` 使用 Windows hostname/config/home key 加密，不能在 Android 伪装兼容解密；从 Windows 迁移账号只走口令加密账号包，导入前显示渠道/账号/覆盖预览。Keystore key 丢失不得猜 key 或写空账号；要求用户恢复口令加密备份或重新登录。迁移测试用固定 fixture，覆盖 v1/v2、损坏、错误 key、进程中断和文件权限。

### 6.3 Loopback 不是进程认证

目前管理 API 可能把 127.0.0.1 视为可信来源；Android 上同设备其它 App 也可连接 loopback。移动运行时必须启用 `AdminAuthMode=mobile-session`：管理 API 要求每次携带 native 私有内存 session key；key 每次 Service 启动随机生成，不写设置文件、不放 URL/query、不进日志。模型 `/v1` 使用单独的 caller/API key，不与管理凭据共用。移动模式 LAN 永久关闭首版；任何以后开放 LAN 的实现需独立安全设计和强制确认。

要求：API key/token 在 HTTP 日志、crash report、clipboard 和无障碍描述中全部脱敏；管理请求限制 Host/Origin/Content-Type、body 大小和 JSON 深度；敏感动作（显示明文、轮换、导出、清除数据）再次验证设备凭据或生物识别。`/healthz` 可匿名但不得返回路径、密钥、账号名或上游错误原文。

## 7. Android 生命周期和能力映射

Android API 26+ 是首发支持基线。用户显式启动网关时，`AetherGatewayService` 请求前台运行并显示 ongoing notification（状态、监听地址、停止操作）。按目标 SDK 正确声明 Android 14+ foreground service type 和权限；不得借用 media playback/location 等不相关类别。Doze、OEM 电池策略、用户强停和通知权限都可能中断服务，界面必须报告真实状态，不能承诺永不停止。仅在实测证明需要时才申请短时 partial WakeLock，并有时限、释放测试和设置开关。

| Windows 能力 | Android 行为 | UI 和数据要求 |
| --- | --- | --- |
| Gateway + 4 协议端点 | 本机 Go server，127.0.0.1 随机空闲端口 | 显示实际 Base URL、可复制 API 信息、运行/停止原因 |
| Provider、OAuth、设备码、手动凭据 | 保留内置登录类型；OAuth 用系统浏览器与 deep link/PKCE；设备码轮询可取消 | 账号写入 Keystore 加密存储；Manifest capability 驱动显示 |
| Windows 本机账号扫描 / DPAPI / 客户端导入 | 禁止后台跨 App 扫描；文件通过 Storage Access Framework 由用户选择并预览 | 不暴露“自动扫描 Windows”；逐渠道说明 Android 不支持或手动导入 |
| Channels / quota / aliases / account priority | 可复用业务 API；刷新额度是实际上游请求 | 标清刷新操作会访问供应商；分页和 stale/loading/error 状态 |
| Usage / ledger / request logs | 仅本机请求数据 | 支持删除/留存；请求正文默认不保存；错误原文需手动展开 |
| Model groups / routing / failover | 复用 Go 路由，屏幕适配手机编辑顺序和数值 | 保存前预览变化；失败回退保持原语义 |
| Wake / check-in scheduler | Android 前台期间准确执行；后台周期任务使用 WorkManager 尽力而为 | 明确系统最短周期与省电影响，不称精确 cron |
| sessions / library / Agent clients | 只显示 Aether 自己有权读取或用户显式选择的文件 | 其他 App sandbox 会话为空时给原因和导入方式 |
| WSL / Windows process / tray / isolated clients | 无手机同义能力 | 不显示可执行按钮；标注“仅 Windows”；不能伪装成成功 |
| Keep awake / quota floating window | Foreground notification + 可选 widget | widget 刷新由 Android 系统节流，非实时保证 |
| 2FA / redaction / tools / backup | 复用 Go 功能并通过 native 私密存储 | 导出前预览敏感内容和加密选项 |

## 8. 页面结构与状态

页面以 MCA `AppTab`/工作区 overlay 交互为样板，底部导航不是强制默认。建议主入口为 MCA 风格的应用菜单/导航抽屉，加 4 个高频工作区快捷入口；平板/横屏可显示侧栏。菜单和返回栈要用 `rememberSaveableStateHolder` 等价状态保存，不把页面切换实现成全屏白闪重建。

| 工作区 | Android MVP 内容 | 高风险状态 |
| --- | --- | --- |
| Overview | Gateway 状态、地址、今日请求/错误、低额度、最近请求 | 未启动、启动中、恢复中、失败、上游离线 |
| Gateway | 协议、调用方 key、探针、客户端接入片段 | 端口变化、caller key 新建/轮换/撤销 |
| Channels | 渠道目录、账号、登录、额度、模型开关、权重/并发 | OAuth 取消、设备码过期、凭据无效、额度刷新失败 |
| Usage | 日/周/月、渠道/模型/调用方、成本估算 | 空数据、旧数据、货币/价格缺失 |
| Requests | 筛选、日志详情、错误解释、请求/响应元数据 | 敏感字段折叠、长 JSON、断流请求 |
| Routing | 模型映射、候选顺序、失败回退策略 | 保存冲突、未知渠道/模型、路由无候选 |
| Automation | 签到、唤醒队列、计划、运行结果 | Android 后台调度延迟、重试、取消 |
| Library / Sessions | 设备上可访问的提示词、Skills、MCP、会话 | 授权文件失效、来源 App 不可访问 |
| Security / Settings | Key、2FA、脱敏、代理、隐私、备份 | 生物识别取消、Key 丢失、导入冲突、清除确认 |

每页按 `Route -> ViewModel -> typed StateFlow<UiState> + sealed UiAction -> Repository/UseCase -> Go API` 实现。Screen 只渲染状态与发 action；Composable 不调用 JNI、数据库、Go 或 Android Service。每个 ViewModel 页面状态可恢复；全局 Service 状态由唯一 `GatewayServiceController` 提供只读状态流。

### 8.1 Windows 版工作区到移动工作区逐项映射

来源是 `internal/web/ui2/js/shell.js` 的 12 个主导航页、4 个 More 页面，以及额度 HUD。这里映射的是业务能力，不代表 Windows 主机功能自动可移植。

| Windows 工作区 | 移动工作区/入口 | 首期规则 |
| --- | --- | --- |
| 概览 `overview` | Gateway Overview | 本机运行状态、请求摘要、低额度、最近错误 |
| 接入渠道 `onboard` | Channels → Add | Manifest 目录、浏览器授权、设备码、手动 Key、SAF 显式导入 |
| 账号与额度 `channels` | Channels → Account detail | 启停、额度、模型映射、标签、优先级、并发和渠道参数 |
| 用量 `usage` | Usage | 成本/Token/渠道/模型/调用方趋势，只有本机产生的数据 |
| 唤醒任务 `scheduler` | Automation | 签到、账号队列和状态；后台准确度服从 Android WorkManager/前台限制 |
| 应用启动 `launcher` | Client setup | 分享接入片段/打开 Android URL scheme；不能发现或启动 PC 进程 |
| 会话 `sessions` | Sessions | Aether 自有会话或用户显式导入；不扫描其它 App 私有目录 |
| 库 `library` | Library | 本机指令、Skills、MCP、子代理、Hooks；只写 SAF 授权位置 |
| 请求日志 `logs` | Requests | 搜索、筛选、诊断和详情；正文默认不记录 |
| 网关与探针 `gateway` | Gateway & diagnostics | 协议 Base URL、caller key、health/ready/version/metrics |
| 2FA `twofa` | Security → TOTP | Keystore 加密 TOTP seed，重验后显码/导出 |
| 设置 `settings` | Settings pages | 常规、网络、工具、预设、定价、隐私、可观测、备份、关于 |
| Agent 接入 `agents` | Client setup / snippets | 移动端兼容配置片段；Windows CLI 发现和版本扫描标为 desktop-only |
| 路由 `routing` | Routing | 虚拟模型、渠道链、策略、失败回退与队列 |
| 脱敏 `masking` | Privacy | 全局/按渠道规则、预览、日志过滤 |
| 插件 `plugins` | Extensions | 只展示 Go/mobile build 实际注册的插件和能力，不展示桌面 DLL/进程插件 |
| 额度悬浮 HUD `hud` | Notification / widget | Android notification + 可选 widget；刷新周期不承诺实时 |

共享设置分组固定对应 MCA/Windows 基线中的 General、Network、Tools、Presets、Pricing、Privacy、Observability、Sync/Backup、About；每组单独页面或 MCA 风格 overlay，不用 9 个平铺主 tab。每个 Windows 页面开工前都要有一行“移动动作 / Android 等价 / desktop-only / 不支持原因”的验收状态。

## 9. JSON API 与 Provider Registry

1. Android App 与 Gateway 的管理 API 保持同一 loopback listener，沿用已存在的 JSON 资源，但为移动专用认证与每项权限建立明确模式。MVP 对前端暴露一份精简 `AetherApiClient`，禁止 ViewModel 手写 URL/JSON。
2. `core:aether-contract` 中定义 request/response DTO、稳定 `error_code`、分页游标、`updated_at` 和 `schema_version`。第一阶段先从 `/api/health`、`/api/settings`、`/api/accounts`、`/api/providers`、`/api/usage`、`/api/logs` 的真实 handler 和测试 fixture 建 contract，不凭设计稿编造字段。
3. Provider 注册通过 `cmd/mobilebridge` 的明确 blank imports 完成；首阶段注册可跨平台编译的 Provider。编译 matrix 决定是否每渠道全部注册；渠道能力/登录方式/是否支持 quota/model-list 以 `ProviderManifest` 返回，缺能力时隐藏动作并显示原因。
4. JSON/SSE 使用 OkHttp；取消协程必须关闭 HTTP Call/Response body，断开流不留下 goroutine。EventSource 不能自定义 Header 时，写独立 OkHttp SSE collector，禁止把管理 key 放 query。
5. 本机管理 UI 所有写操作要发送 `Idempotency-Key`（新加账号、导入、删除、清除、密钥轮换、定时任务），避免系统重试重复执行。服务端返回真实执行结果，不返回假成功。

## 10. 文件写集与分阶段执行

弱模型每次只实现一阶段中的一个小任务卡。每张卡须以“目标 / 允许修改文件 / 不修改文件 / 实现步骤 / 验收命令 / 失败如何停下”开头；禁止一次实现整张阶段表。

### Stage 0：构建与依赖闸门

**目标：**拿到可复现的 MCA design system artifact，并验证 Go Android shared library 真正可编译和装载。

**允许写入：**`E:\MAC\core\designsystem\build.gradle.kts`、MCA Maven publish/workflow 所需的最少文件（需先按 MCA 规则单独 review）；Aether 侧 `mobile/android/build.gradle.kts`、`mobile/android/gradle/libs.versions.toml`、`mobile/android/native/`、`internal/mobilebridge/`、`cmd/mobilebridge/`、测试与构建脚本。

**小任务：**(0a) 发布 `com.muyuchat.mca:designsystem` 版本 artifact，仅包含当前 `:core:designsystem`，包含传递依赖并签出源码 revision；(0b) 建空 Compose launcher，只应用该 AAR 并渲染 `McaTheme`；(0c) 建 Go mobile bridge 空壳；(0d) Android arm64-v8a 上 `Start -> health -> Stop -> Start`。

**验收：**本地 clean Gradle build、CI clean runner 解析同一 AAR、Android emulator/device load native library、Go runtime 循环启动停止 3 次、端口每次正确释放。若 Go 编译阻塞，先只修 Go 构建错误并加 regression test，禁止开始完整 UI。

> **验收覆盖边界（2026-11-03 实测记录，勿误解为全部由 CI 自动保证）**
>
> | 验收项 | 由谁保证 | 状态 |
> |---|---|---|
> | 本地 clean Gradle build | `scripts/check-mobile-stage0.sh`（`--rerun-tasks clean`） | 已覆盖 |
> | CI clean runner 解析同一 AAR | `scripts/check-mobile-stage0.sh` 里的 `verifyPinnedDesignSystem`：让 Gradle resolver 交出实际 AAR 文件并比对 `vendor/mca-designsystem.lock` 的 `aarSha256` | 已覆盖（含负向验证） |
> | Go runtime 循环启动停止 3 次 + 端口每次释放 | 独立 C 探针（`cmd/mobilebridge/probe`，stop 后真的重新 `bind()` 同端口） | 已覆盖（设备/模拟器上跑） |
> | **Android emulator/device load native library** | `check-mobile-stage0.sh --device <serial>` 装 APK 并跑 `NativeBridgeInstrumentedTest`（走 `System.loadLibrary` → JNI → `dlopen`） | **托管 CI 不覆盖**：`stage0` job 只跑 `--ci`。真机覆盖靠手动触发的 `device` job（需 `vars.AETHER_ARM64_DEVICE=true` + 自托管 arm64 runner）或本地执行 |
>
> **整类仪器测试 gate（2026-11-03 增补）**：上表最后一行只覆盖 `NativeBridgeInstrumentedTest` 的一个方法，且裸 `am instrument` 的 `OK (1 test)` 不产生 Gradle 报告。现在 `device` job 在 Stage 0 之后追加 `bash scripts/check-mobile-instrumented.sh --device <serial>`：用 `./gradlew :app:connectedDebugAndroidTest` 跑全部 `androidTest` 类，再直接判 Gradle 生成的 JUnit XML——`tests > 0`、`failures/errors/skipped` 均为 0、且三个测试类都必须出现 `<testcase>`。空报告或某个类静默不跑都判失败；确需跳过的类必须写进脚本的显式 skip 清单并打印原因。该 gate 已于 2026-11-04 在 arm64 真机（`98a37aa7`）实跑通过一次：`tests=3 failures=0 errors=0 skipped=0`，三个类的 `<testcase>` 都在同一份 `TEST-…-_app-.xml` 里（`NativeBridgeInstrumentedTest#startsServesBothCredentialScopesAndStops`、`GatewayServiceInstrumentedTest#serviceShowsNotificationServesBothScopesAndStops`、`RequestLedgerInstrumentedTest#fakeAccountRejectionAppearsInUsageAndRequestLedger`）。注意 AGP 在 `connectedDebugAndroidTest` 结束后会**卸载**两个 APK，因此跑完设备上不留包——这是正常行为，不是失败信号。
>
> **已知阻塞**：x86_64 模拟器路线当前不可用——Go runtime 在 `untrusted_app` 域触发 `SYS_LSTAT=6`，被 Android seccomp 杀死；arm64 无该 syscall 故不受影响。证据与可选解法见 `docs/internal/STAGE0_X86_64_BLOCKER.md`。在 Go 侧解决前，**不要**把 `--ci-emulator` 接进 CI。

#### 额度页与额度读取（2026-11-04 补记）

额度页（原「渠道」页）已按桌面端 `channels.js` 重做，当前具备：

- **账号卡片优先**：名称/渠道、账号启停开关、额度窗口条、最近使用、签到行、三个动作；
- **额度读取三态**：`从未读过` / `无（这次读取没返回额度）` / `最近读数 刚刚·N 分钟前`；
  判据只认回包 `read_at` 有无，**不用时间戳推断**（常量见 `ui/ChannelsPage.kt`）；
- **失败可见**：额度区出现 `读取失败：<原文>`，且只认 `kind == QUOTA`，不与启停/签到失败混用一条通道；
- **工具条与批量**：筛选（全部/仅启用/仅停用/有额度窗口）+ 搜索叠加；长按进选择态，
  批量刷新额度走真批量端点 `/api/accounts/batch`，批量启停与签到因服务端无批量端点而**串行执行并如实汇总**；
- **超时纪律**：客户端单账号读取超时 90s，必须晚于服务端最坏路径 65s（续期 45s + 读额度 20s），
  否则回包永远到不了、失败原因也拿不到。

排障方法（三种日志形态各自结论）见 `docs/MOBILE_QUICKSTART.md` 的「读取额度看不懂时怎么查」。


#### 当前覆盖度（2026-11-04 实测；17/17 条工作区已接到真实页面）

`Workspace.kt` 锁定的 17 条工作区入口**全部有真实落地**，没有"点了什么都不发生"的第四种状态。逐条如下（"边界"列写的是**手机上确实做不到**的部分，不是待办）：

| # | 工作区 | 落地页面 | 边界 |
|---|---|---|---|
| 1 | 概览 | `OverviewPage`（状态/URL/今日统计卡） | 统计来自 `/api/usage` 与 `/api/providers`，两侧独立失败 |
| 2 | 渠道接入 | `ChannelsPage`（Qoder 登录、账号包导入/导出、逐渠道探测） | PC 端扫描/文件导入仅 Windows |
| 3 | 账号额度 | `ChannelsPage` 行内额度 | 余额令牌本机绑定，需在手机重新录入 |
| 4 | 用量 | `UsagePage`（总览/热力图/配额历史/价格表） | 价格表只读；编辑在桌面端 |
| 5 | 成本 | `UsagePage` 同一数据源 | 同上 |
| 6 | 请求 | `RequestsPage` + CSV 导出（FileProvider 分享） | 正文查看不做：`/api/usage/requests/content` 是恒返回 `{found:false}` 的桩 |
| 7 | 任务 | `WakePage`（唤醒队列 新建/删除/手动触发 + 签到调度与汇总） | 准点执行仅桌面端；手机端尽力而为 |
| 8 | 会话 | `SessionsPage`（只读） | 会话删除/终端仅 Windows |
| 9 | 资源库 | `LibraryPage`（查看/新增/删除） | MCP config 编辑仅桌面端（掩码回写会写坏真实值）；条目"编辑"无按 id 更新端点 |
| 10 | 网关探针 | `GatewayProbePage`（`/healthz`、`/version`、`/metrics`、`/api/runtime`） | 无 |
| 11 | 2FA | `TotpPage`（Keystore 信封保存 seed） | 无 |
| 12 | 设置 | `SettingsPage`（首选端口/脱敏/调用方密钥/备份导入导出/局域网只读） | 局域网开关仅桌面端生效（运行时只绑 127.0.0.1） |
| 13 | Agent | `ClientsPage`（客户端清单与检测状态） | 注入/还原要写桌面配置文件，仅 Windows |
| 14 | 路由 | `RoutingPage`（组/成员/改名/保存） | 决策器与权重可视化未做 |
| 15 | 脱敏 | `SettingsPage` 脱敏小节 | 规则编辑在桌面端 |
| 16 | 插件 | `PluginsPage`（改写规则与内置插件，只读） | 规则增删改仅桌面端；图标无列表接口 |
| 17 | 额度快捷查看 | `ChannelsPage` | 同上（余额令牌需重录） |
| — | 应用（第 18 项以外） | `WorkspaceAction.DesktopOnly`，UI 标注「仅 Windows」 | 启动 PC 客户端，仅 Windows |

**全量门禁实测（2026-11-04，本机 arm64 真机 `f9d98aa3`）**

| 门禁 | 命令 | 实测结果 |
|---|---|---|
| Kotlin 单测 | `./gradlew :app:testDebugUnitTest` | **51 suites / 259 tests / 0 failure** |
| Debug 构建 | `./gradlew :app:assembleDebug` | BUILD SUCCESSFUL |
| 设计系统契约 | `scripts/check-mobile-theme-contract.sh` | `theme-contract=pass`（lock 4 个 sha256；无自建主题/硬编码色值字号） |
| API 契约 | `scripts/check-mobile-api-contract.sh` | `api-contract=pass`（移动端 37 条 `/api` 字面量全部能在服务端路由里找到） |
| 发布门禁（源码） | `scripts/check-mobile-release.sh --ci` | `release-gate(source)=pass`（version.properties 单一来源；minSdk=26 targetSdk=36） |
| 产物内容 | `scripts/check-mobile-apk-contents.sh --no-build` | `apk-contents=pass`（36,722,638 B；单一 ABI arm64-v8a；桥接库齐全；无调试符号） |
| 仪器测试 | `scripts/check-mobile-instrumented.sh --device <serial>` | 2026-11-04 真机实跑 `tests=3 failures=0 errors=0 skipped=0`（见上文） |

**明确不做（⛔ 仅 Windows / 物理不可能，继续保持「仅 Windows」标注）**：WSL 管理、PC 进程控制与启动器、桌面窗口操作、会话删除与终端、桌面 Agent 目录扫描（`/api/library/found`、market）、`WindowsAutoImport`、`reveal`（打开文件所在位置）、准点后台调度、写桌面客户端配置文件、供应商自定义图标的写入。

**按设计不做（改手机页面也解决不了，别当待办）**：请求正文查看——`ledgerRecord`（`internal/core/gateway/ledger.go`）**没有任何正文字段**，请求正文从不落盘；`/api/usage/requests/content` 因此只能返回 `{found:false}`。要"查看正文"必须先改隐私模型把正文存下来，那是产品决策。

其余对标缺口分两类，也都不是手机端单侧能补的：**需要 Go 侧补语义**（MCP config 的"保持不变"哨兵、资源库按 id 更新端点）；**属桌面壳能力**（自动更新下载、DAV 外部同步目标、MCP OAuth 浏览器回调）。


### Stage 1：Go 可移动编译与纯服务生命周期

**写集：**`internal/core/gateway` 的平台能力/生命周期文件；`internal/core/launcher` 与 `internal/providers/workbuddy` 的 Android/非 Windows build-tag 文件；`internal/mobile/runtime.go`；`cmd/mobilebridge/*`；对应 Go unit tests。

**步骤：**修复当前已知编译缺口：非 Windows 的 `processAlive`/process API stub；WorkBuddy mobile 本机 crypto/import 明确返回 `capability_unsupported`，保留 API/手动登录。新增 `RuntimeConfig`、`Start/Status/Flush/Stop`；关闭 WSL、Windows auto-import、keep-awake、CLI process discovery；给 `Gateway.New` 启动的 autoimport/monitor/wake 等循环加 `context.Context` 和统一 waitgroup。DataDir 所有者从配置传入，不设置全局 cwd。

首轮失败已定位，不要改成“恒返回成功”来绕过：`internal/core/launcher/launcher.go` 引用了仅在 `launch_windows.go` 定义的 `processAlive`；Android/iOS 需要有意义的非 Windows stub 或将进程能力移入 Windows build-tag 文件。`internal/providers/workbuddy/localimport.go` 调用 `loadWBKey`/`decryptWBField`，实现只在 Windows `crypto.go`；移动侧必须让该导入能力返回明确不支持，不能复制 DPAPI 解密或模拟读到 PC 登录凭据。

**验收：**Go 代码单测在 host 上运行；Android native library 通过 `scripts/build-android-mobilebridge.ps1` 或 CI 对应 `.sh` 调用 NDK clang + `GOOS=android GOARCH=arm64 CGO_ENABLED=1 go build -buildmode=c-shared ./cmd/mobilebridge` 真正产出共享库。脚本必须检查 NDK 路径/clang 存在、输出 ABI 和 Go 版本，不能只设置 `GOOS` 就报告通过。`GOOS=ios GOARCH=arm64` 构建验证放在 macOS CI，不在 Windows 上声称通过；runtime test 使用临时目录和随机端口，启动失败/stop/restart/timeout 均无 goroutine leak。Windows `go test ./internal/core/...` 与既有启动路径不能回归。

### Stage 2：JNI、Android Service 与端口安全

**写集：**`mobile/android/native/**`、`mobile/android/app/src/main/AndroidManifest.xml`、`app/.../runtime/**`、`internal/core/gateway` 管理认证与相关安全测试。

**步骤：**用 JNI 字符串 JSON 调 `nativeStart`；服务收到结果后展示实际随机端口；notification stop action 和应用内停止都调同一个 controller；通知状态与 GatewayStatus 同源；在 mobile auth mode 下拒绝没有 ephemeral admin session 的 `/api/*`；对 `/v1*` 强制 caller key（或独立显式 local-consumer key），不沿用 loopback 无鉴权。

**验收：**仪器测试核对按钮/通知 start-stop 同步、旋转后 service 仍运行、stop 两次成功、无 admin key 得 401、key 轮换立即使旧 key 失效、服务监听 `127.0.0.1` 而不是 `0.0.0.0`、外部 LAN 无连接。运行 Android 13/14+ 权限与 foreground service 启动检查。

### Stage 3：MCA UI shell 与设计 parity

**写集：**`mobile/android/app/**`、`mobile/android/feature/gateway/**`、`mobile/android/core/aether-contract/**`；不修改 Go。

**步骤：**接入 pinned MCA AAR → `McaTheme` → system bars/insets → launcher icon → MCA 风格 workspace navigation → Gateway read-only Overview → loading/empty/error/running states。视图先消费 mock repository 和 fake status flow，再接真实 Gateway API。逐页截图与 MCA `01-home.png`、`02-workspace-nav.png`、`05-settings.png` 对照；只在 MCA 现有图形语言内使用本项目衍生图标。

**验收：**`./gradlew :app:assembleDebug :feature:gateway:test`；浅/深色、360dp 宽、大字体 1.3x、TalkBack label、触控 48dp、back/insets；`McaThemeColorContract` 的外部 parity test 对 pinned 版本通过。若屏幕看起来像旧 HTML 原型而非 MCA Compose 工作区，退回调整，不加第二套颜色/形状 token。

### Stage 4：账号、渠道和本机加密

**写集：**`mobile/android/core/android-platform/**`、`core/aether-contract`、`core/aether-data`、`feature/channels/**`；Go cipher/path files 与 Go migration tests 单独一张卡。

**步骤：**SAF 导入先预览再提交；授权走系统浏览器 PKCE/deep link 或 provider device code；账号展示由 Manifest/capabilities 决定。实现 Android Keystore key provider；Go credential envelope v2 使用 AES-GCM/AAD/version，v1 只读迁移。所有写操作返回真实失败原因。

**验收：**假 OAuth/device-code server、取消和过期测试；v1->v2 迁移 fixture；损坏/错误 key 保留原文件；Go SQLite state 和账号 JSON 关闭后重新打开并校验恢复；导出备份口令错失败；删除和轮换需要确认并完成后端回读。

### Stage 5：用量、请求、路由、设置

**写集：**`feature/usage/**`、`feature/requests/**`、`feature/routing/**`、`feature/settings/**`、对应 DTO/repository 与 Go API tests。

**步骤：**按 MCA 组件组合列表、筛选、详情页和 modal bottom sheet；不一页实现所有设置。依次落地 usage read-only、log filter/detail、route edit/preview/save、settings read/write、privacy/redaction。路由写入前保留旧值快照和服务端校验错误。

**验收：**API contract fixtures、页级状态单测、真实 local Gateway integration、无数据/慢网/断开/超长文本/键盘/返回测试；隐私默认不保存请求正文；token、headers 和 OAuth code 在日志截图全脱敏。

### Stage 6：自动化、库、会话、2FA、备份

**写集：**分别只触碰 `feature/automation/**`、`feature/library/**`、`feature/sessions/**`、`feature/security/**` 中一个 feature 及其 contract/repository。

**规则：**WorkManager 是尽力执行，不伪称精确 cron；Library/Sessions 只显示 Aether 私有数据或用户经 SAF 显式授予的文件，不扫描其它 App 沙箱；MCP server secrets 走 credential cipher；TOTP seed 不出现在列表 DTO；导出备份支持口令加密和 Android Sharesheet。

**验收：**work cancellation/retry、撤销 SAF grant、MCP secret masking、TOTP 向量/时钟漂移、备份校验/中断恢复/覆盖预览、删除前设备验证。不能访问 Windows session 时显示空态，不显示“同步成功”。

### Stage 7：渠道矩阵与 Release Gate

**写集：**`cmd/mobilebridge/providers_android.go`、`mobile/android` CI/build/release 脚本、测试和能力矩阵文档。

每增加一组 Provider blank import，先对 Android ABI 编译、manifest auth flow、mock upstream 登录/聊天/配额/刷新/取消测试；失败则该 provider 在 mobile registry 中按 capability 标成 unsupported，不影响其它渠道或 Windows build。真实上游端到端测试只能在有独立凭据且用户明确授权后运行。

**Release Gate：**`go test ./internal/core/...`；Android bridge cross-build；`./gradlew clean :app:testDebugUnitTest :app:lintDebug :app:assembleDebug`；仪器/UI 自动化跑至少 API 26 emulator 与 API 35 emulator，再用一台兼容 ARM64 真机测 service 生命周期；浅/深色 + 字体缩放 + rotation + process death + no network + storage full；用户备份升级恢复；MCA screenshots 视觉对比；Go/Compose logs 中无密钥。

### Stage 8：iOS（另立里程碑）

iOS 不复用 Compose UI。保持 `core/aether-contract` JSON/API 和 Go runtime 业务相同，新增 SwiftUI navigation/Keychain/file importer/notifications host，Go 在 macOS CI 产出 arm64 simulator/device 静态库。SwiftUI 的色彩/间距/形状/动效必须消费与同一 MCA 版本绑定的中性 `mca-design-tokens.json` 和截图基准；该 manifest 尚未提供前不开始 iOS UI，不允许目测猜值。App active 时启动 loopback Gateway，切 background 时 flush 并按策略停止；返回 foreground 后恢复。不要使用无关音频或 location background mode 维持 server。iOS 上游 API、URI callback 和 keychain accessibility 必须单独验收后才可宣称支持。

## 11. 每张弱模型任务卡模板

复制下面模板创建一张卡，只填一个可在一到两次 review 内完成的小功能：

```text
目标：实现 <唯一行为>。
基线：先读取 docs/MOBILE_APP_ARCHITECTURE.md 的 Stage <N>、E:\MAC 对应 MCA 设计系统版本及本卡列出的源码。
允许修改：<精确文件路径列表>。
禁止修改：Windows 主流程、MCA 其它功能、协议字段、未列入本卡的 Go/Compose 文件。
实现顺序：1) 写/补失败测试；2) 最小实现；3) 更新 fixture/文档；4) 执行列出的命令。
接口：沿用 <明确 DTO/方法>；不要新增第二套 Repository、Theme、Gateway 或状态 owner。
验收：<精确命令> 全成功；手动截图/状态 <清单>；git diff 只含允许写集。
停止条件：依赖不存在、字段与真实 handler 不符、跨平台编译失败或需要扩大安全边界时，保留现状，提交错误和证据，不猜字段、不返回假成功。
交付：变更文件、测试输出、截图路径、未验证平台和下一张单一任务卡。
```

AI 每次最多做一个 feature 页面或一个 backend 生命周期切片；不允许“一次把 Android App 全做完”。每次先发一段包含写集的短计划，然后实现、验证、检查 diff，再交接。禁止为通过测试放宽原 Windows 行为、删除既有功能、改写 MCA hard-lock、增加无意义 manager/facade 层。

## 12. 明确不做与成功定义

首版不做：Windows 数据远程控制/配对、LAN 开放、后台精确 cron 承诺、跨 App 自动登录态扫描、WSL、Windows 托盘/客户端进程控制、把所有 17 个页面塞进 Overview、WebView 包装旧 HTML、无实际后端的假开关。

阶段性成功不是“导航里有 17 个名字”，而是：

1. Gateway 在 Android app sandbox 内真实启动、鉴权、接收本机客户端请求并路由到用户上游。
2. 停止/重启/崩溃恢复/数据升级有唯一 owner 和可验证终态；凭据由 Android Keystore 派生加密保护。
3. 所有宣称支持的账户、渠道和管理动作都有真实 API、错误态、权限说明及测试；不支持的 Windows 主机动作明确隐藏/解释。
4. Compose UI 使用 pinned MCA design system，视觉截图、触控、字体缩放、导航和动效合同通过 MCA 质量 gate。
5. 文档和发行说明将“移动端业务功能覆盖”与“Windows 主机系统能力”分开声明。只有相同测试矩阵逐项通过后才可宣称功能对标；不能用原型图或菜单数量声称 100%。
