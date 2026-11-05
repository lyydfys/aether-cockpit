# Aether Cockpit

**English:** [README in English](README.en.md)

把分散在 CLI、IDE 和 API 配置里的 AI 编程渠道，收拢到一个本机入口。Aether Cockpit 将你有权使用的订阅账号与模型 API 接入统一网关，让 Claude Code、Codex CLI、Gemini CLI、OpenCode 等客户端使用熟悉的接口调用不同渠道；账号池、模型路由、故障转移、额度和用量也能在同一处查看。

典型请求路径是「客户端 → 本机网关 → 账号与模型路由 → 你配置的上游服务」。Aether Cockpit 负责本地接入、协议适配与运维视图，不托管上游账号，也不替代你选择的 AI 客户端。凭证、请求记录和运行数据默认保存在本机；网关默认只监听回环地址，需要时可在概览页开启局域网共享（管理接口仍要求 App 内会话凭据）。Android 端提供同一网关的本地运行版本，渠道接入、额度和运维视图都在手机上进行。

和直接用各家客户端相比，多出来的是**同一张网关上的一层调度**：一个客户端配置可以指向多个渠道，账号可以放进池子里按策略轮转，额度与用量集中在一处看，出问题时有请求日志和探测结果可查。这些都在本机完成，不经过项目中转。

你可以从 [GitHub Releases](https://github.com/lyydfys/aether-cockpit/releases/latest) 下载，两个平台的产物同名同版本：

| 平台 | 文件 | 说明 |
|---|---|---|
| Windows x64 | `AetherCockpit-<版本>-windows-amd64.zip` | 解压后运行 `AetherCockpit.exe` |
| Android arm64 | `AetherCockpit-<版本>-android-arm64-v8a.apk` | 正式签名包，安装时需允许「未知来源」 |

也可以先看[快速上手](docs/QUICKSTART.md)与[能力说明](docs/FEATURES.md)。

[![CI](https://github.com/lyydfys/aether-cockpit/actions/workflows/ci.yml/badge.svg)](https://github.com/lyydfys/aether-cockpit/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/lyydfys/aether-cockpit)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows%20x64%20%7C%20Android%20arm64-2675bf)](#系统要求)

## 目录

- [一个入口，管理你的 AI 渠道](#一个入口管理你的-ai-渠道)
- [界面预览](#界面预览)
- [渠道覆盖](#渠道覆盖)
- [开始使用](#开始使用)
- [能力概览](#能力概览)
- [系统要求](#系统要求)
- [从源码构建](#从源码构建)
- [安全与数据](#安全与数据)
- [常见问题](#常见问题)
- [文档](#文档)
- [参与项目](#参与项目)
- [赞助支持](#赞助支持)
- [设计参考与致谢](#设计参考与致谢)
- [许可证](#许可证)
- [发布签名](#发布签名)

## 一个入口，管理你的 AI 渠道

- **客户端各用所长：** 让支持的 AI 编程客户端共用本机网关；按客户端能力选择 OpenAI Chat、Responses、Anthropic Messages 或 Gemini API。
- **多个账号协同：** 将不同渠道账号放进账号池，通过路由组、会话粘性、轮转或额度感知策略分配请求，并在可用时故障转移。
- **运行状态集中查看：** 在一处检查模型列表、渠道额度、Token 与请求用量、成本、探测结果和请求日志。
- **把周边工作流也收拢起来：** 管理客户端连接配置、提示词、Skills、MCP 服务、子代理和独立客户端会话。

## 界面预览

以下截图取自隔离演示配置，账号、额度与用量均为合成数据。

**概览**：六个示例渠道汇入统一 API 端点，账号、额度、调用和探测状态集中在一页。

![Aether Cockpit 概览：六个示例渠道汇入统一 API 端点](docs/images/screenshots/overview-zh.png)

**设置**：本机网关的监听、局域网共享、访问控制与脱敏选项都在这一页，页面只展示本机选项，不含个人凭据。

![本机设置与自动探测选项](docs/images/screenshots/settings-zh.png)

**用量**：请求数、Token 与成本按时间和渠道聚合，数据来自本机记录。

![用量趋势与渠道分布](docs/images/screenshots/usage-zh.png)

## 渠道覆盖

渠道目录包含订阅账号集成，也包含 API 平台与兼容端点。下面是项目支持类型的代表性示例；登录方式、额度信息和模型能力因渠道及账号权限而异，完整清单以应用当前版本显示为准。

| 类型 | 渠道示例 |
| --- | --- |
| 国内订阅与编程服务 | Qoder、Trae、WorkBuddy、CodeBuddy、ZCode、MiniMax |
| 海外订阅与客户端账号 | Claude、Codex、GitHub Copilot、Cursor、Antigravity、Kiro、Devin、OpenCode |
| API 与云平台 | OpenAI 兼容端点、Azure OpenAI、Gemini、Vertex AI、Amazon Bedrock |

![渠道目录中的订阅渠道与 API 平台](docs/images/screenshots/channels-zh.png)

*渠道目录展示可接入类型，不表示所有账号均已登录，也不保证每个渠道都支持相同的模型与功能。*

## 开始使用

### Windows 桌面版

1. 下载 `AetherCockpit-<版本>-windows-amd64.zip` 并解压到任意目录（不要直接在压缩包里运行）。
2. 双击 `AetherCockpit.exe`。桌面版依赖 Microsoft Edge WebView2 Runtime，Windows 10/11 通常已自带。
3. 首次启动默认监听 `127.0.0.1:7860`，浏览器打开 <http://127.0.0.1:7860/> 即可进入界面。
4. 无头场景可以使用压缩包里的 `aether-cockpit-cli.exe`，不需要图形界面。

**数据目录**：首次启动后在用户目录下创建（Windows 为 `%LOCALAPPDATA%\AetherCockpit`），其中有 `settings.json`、`accounts.json`、`state.db`、`logs/aether.log` 与 `usage/`。备份或迁移时整目录一起带走；这份目录含账号信息，不要提交到版本库或发到聊天里。

**默认端口**：`7860`。被占用时可在设置页改端口，或用启动参数指定（见「常见问题」）。

### Android

1. 下载 `AetherCockpit-<版本>-android-arm64-v8a.apk`。
2. 系统会提示「未知来源」安装，需在系统中允许该来源后再继续；这是侧载 APK 的正常提示。
3. 仅支持 **arm64-v8a** 设备，最低 **Android 8.0（API 26）**。
4. 首次打开后在应用内启动本机网关，随后「接入渠道」与「概览」即可使用；手机端不承担桌面端独有的能力（界面上会标「仅 Windows」）。

### 接入一个模型

1. 在「接入渠道」添加一个你有权使用的订阅账号或 API 服务。
2. 在「网关与探针」运行探测，并复制目标客户端对应的接入配置。
3. 使用 `渠道 ID/模型名` 作为模型名，例如 `openai-compat/gpt-4.1`；可用名称以界面或 `/v1/models` 返回结果为准。

也可以先用兼容的客户端或命令行检查当前可路由模型。开启网关密钥后，将 `YOUR_GATEWAY_KEY` 替换为本机配置的密钥；示例中的模型名请替换为 `/v1/models` 返回的实际名称。

```powershell
curl.exe http://127.0.0.1:7860/v1/models `
  -H "Authorization: Bearer YOUR_GATEWAY_KEY"

curl.exe http://127.0.0.1:7860/v1/chat/completions `
  -H "Authorization: Bearer YOUR_GATEWAY_KEY" `
  -H "Content-Type: application/json" `
  -d '{"model":"<provider-id>/<model-id>","messages":[{"role":"user","content":"你好"}]}'
```

客户端配置可能包含网关密钥。请从界面复制并保存在本机，不要把真实密钥粘贴到公开 Issue、截图或日志中。

## 能力概览

| 场景 | 能力 |
| --- | --- |
| 统一接入 | OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini API；支持相应流式请求与工具调用 |
| 账号与渠道 | 订阅渠道、本机授权导入、通用 OpenAI 兼容服务、账号池、标签、批量管理、自动刷新与健康探测 |
| 模型路由 | 粘性会话、优先级/轮转/额度感知/意图路由、虚拟模型组、故障转移与模型白名单 |
| 客户端工作流 | 为多个 AI 客户端写入或恢复网关配置；管理提示词、Skills、MCP、子代理及独立客户端实例 |
| 用量与运维 | 额度和成本视图、请求日志、定时任务、备份导入导出、WebDAV 同步、健康与版本端点 |
| 本机隐私 | 默认回环监听；账号凭证留在本机数据目录；支持请求隐私遮蔽和敏感信息脱敏导出 |

按渠道和功能域整理的说明见[能力与兼容性](docs/FEATURES.md)。各服务商接口、额度、授权方式和使用条款会变化，具体兼容性以当前版本及各渠道说明为准。

**能力边界**：以下能力只在 Windows 桌面版提供，Android 界面上会以「仅 Windows」标注并说明原因——WSL 发行版探测、Windows 客户端进程接管、本机客户端扫描与 PC 隔离实例。更新检查只做「检查 + 提示 + 打开 Release 页」，不会自动下载或静默安装。

## 系统要求

| 目标 | 要求 |
| --- | --- |
| 桌面版（预编译） | Windows 10/11 x64；Microsoft Edge WebView2 Runtime |
| 无头版（预编译） | 当前 Release 提供 Windows x64；其它平台请从源码构建并自行验证 |
| Android（预编译） | arm64-v8a 设备；Android 8.0+（API 26）；允许「未知来源」安装 |
| 从源码构建 | Go 1.26+、Node.js 20+（前端检查）、JDK 17+（本次构建使用 JDK 21）、Android SDK 36 + NDK 29.0.13113456（仅 Android） |

## 从源码构建

前置：Go 1.26 或更新；Node.js 20 或更新（只用于前端检查）；JDK 17 或更新（Android 构建本次使用 JDK 21）；Android SDK 36 与 NDK 29.0.13113456（只构建 Android 时需要，路径写在 `mobile/android/local.properties` 或 `ANDROID_HOME`）。

```powershell
# 1) 桌面（Windows）
go run ./cmd/winres-icon
go build -tags desktop,production -ldflags "-H windowsgui" -o AetherCockpit.exe ./cmd/desktop

# 2) Android（arm64-v8a）
bash scripts/build-android-mobilebridge.sh arm64-v8a   # 编译 Go 侧 .so
cd mobile/android
gradlew.bat :app:assembleDebug                          # 或 :app:assembleRelease（需签名）

# 3) 测试
go test ./...
cd mobile/android
gradlew.bat :app:testDebugUnitTest
```

无头/命令行版本：`go build -o cockpit.exe ./cmd/cockpit`。前端检查需要 `npm ci && npm run test:ui && npm run typecheck && npm run ui:lint`。

`scripts/gate.sh` 是维护者的**一键门禁**：先看磁盘余量（C 盘写满时链接器会报 “not enough space”，看起来像测试挂了），再跑 `go vet`，重编产物（否则「门禁全绿」只证明源码对、`bin/` 里的产物可能是旧的），然后串行跑全量测试（并行偶发的 “.test build failed” 是资源问题），最后跑 mock smoke 并核对产物哈希。`--quick` 跳过 mock smoke，`--no-build` 只验源码，`SKIP_TESTS=1` 只跑磁盘/vet/重编/smoke/产物校验。

维护者完整门禁和开发环境说明见[贡献指南](CONTRIBUTING.md)。真实渠道冒烟测试可能调用上游并消耗账号额度；日常开发应使用文档中说明的 mock 流程。

## 安全与数据

Aether Cockpit 是本机网关，不是托管代理服务。

- **凭证存哪：** 账号凭证、Cookie、OAuth 令牌与代理口令都写入本机数据目录（`accounts.json`、`state.db` 与平台密钥库），由项目自己的加密层保护；不写入仓库、不上传任何服务器。
- **是否上传：** 除你自己配置的上游渠道外，项目不向第三方回传账号、提示词或用量；更新检查只请求 GitHub Releases 的公开接口，可用开关关闭。
- **监听范围：** 默认只绑定回环地址 `127.0.0.1`。开启局域网共享后，网关才对同网段开放；管理接口仍然要求 App 内的会话凭据，不是打开页面就能改配置。
- **日志脱敏：** 请求日志默认记录元数据（时间、模型、状态、耗时、Token），内置脱敏引擎支持按前缀/正则把凭证与个人信息替换为占位符，可导出脱敏后的记录；完整请求体不落盘。
- **数据目录：** 含本机账号和运行数据，不应直接分享或提交。`docs/evidence/` 下的本机验收截图同样不入库。

开放到局域网或其他网络前，先设置网关密钥并检查允许访问的来源。使用第三方订阅或服务时，请遵守对应服务商的条款；项目不提供账号、授权，也不对上游稳定性和额度作保证。数据流说明见 [PRIVACY.md](PRIVACY.md)；漏洞报告方式见 [SECURITY.md](SECURITY.md)。

## 常见问题

**端口 7860 被占用怎么办？**
在设置页把监听端口改成其它值（例如 7861）后重启网关；或启动时用参数指定。判断占用者可以用 `netstat -ano | findstr :7860`（Windows）或 `lsof -i :7860`（macOS/Linux）。改端口后，客户端的接入地址也要一起改。

**开了局域网共享，另一台设备还是连不上？**
按顺序查三件事：① 两台设备是否在同一网段（跨 VLAN、访客网络、部分路由器的「客户端隔离」都会挡）；② 主机防火墙是否放行该端口（Windows 首次监听时会弹窗，选「专用网络」）；③ 管理接口需要 App 内会话凭据，另一台设备的浏览器直接打开管理面本来就会被拒，能连的是网关的 API 端点，并且要带上你配置的网关密钥。

**更新检查报 5xx / 超时，是应用坏了吗？**
不是。更新检查请求的是 GitHub 公开接口，在部分网络环境下会返回 5xx 或超时；界面会显示「检查失败」，其余功能不受影响。可以稍后重试，或直接用 Releases 页面手动下载。

**APK 装不上，提示「未知来源」或「不安全」？**
Android 侧载 APK 需要先允许该来源（系统设置 → 应用 → 特殊权限 → 安装未知应用 → 选中文件管理器或浏览器）。另外确认设备是 **arm64-v8a** 且系统 ≥ Android 8.0；x86 模拟器与 32 位设备不在支持范围内。

**更新后需要重新登录吗？**
账号凭证保存在本机数据目录并加密，正常覆盖安装/升级不需要重新登录。只有卸载重装、更换设备，或手动清理数据目录时才需要重新授权。

**为什么手机上没有桌面端的某些能力？**
WSL 探测、Windows 客户端进程接管、本机客户端扫描、PC 隔离实例这几项依赖 Windows 本机环境，Android 上无法等价实现，因此界面上直接标「仅 Windows」并说明原因，而不是放一个点了没反应的入口。

## 文档

- [文档索引](docs/README.md)：用户指南、开发文档与架构说明
- [隐私说明](PRIVACY.md)：本机数据、上游请求、日志与可选导出
- [快速上手](docs/QUICKSTART.md)：首次启动、安全设置、客户端接入与常见问题
- [能力与兼容性](docs/FEATURES.md)：协议、渠道类别、运维能力与限制
- [新增渠道](docs/ADD_PROVIDER.md)：Provider 与 spec 扩展指南
- [架构说明](docs/ARCHITECTURE.md)：核心模块和请求处理流程
- [更新日志](CHANGELOG.md)

## 参与项目

欢迎通过 Issue 报告可复现的问题、提出改进建议，或提交 Pull Request。提交前请阅读[贡献指南](CONTRIBUTING.md)和[行为准则](CODE_OF_CONDUCT.md)。支持渠道见 [SUPPORT.md](SUPPORT.md)。

提 PR 前请在本地跑完这几道门禁，覆盖桌面与 Android 两条线：

```bash
go test ./...
cd mobile/android && gradlew.bat :app:testDebugUnitTest   # 移动端单测
bash scripts/check-mobile-theme-contract.sh               # 主题契约：禁止硬编码颜色/字号/自建主题
bash scripts/check-no-external-project-names.sh           # 仓库内零外部项目名
```

改动涉及原生库或发布流程时再加跑 `scripts/gate.sh`。CI 会重复执行同样的检查，本地先跑能省一轮往返。

## 赞助支持

如果这个项目帮到了你，欢迎点一个 Star、分享项目，或通过收款码支持持续开发与维护。赞助完全自愿，不影响任何功能。

<table>
  <tr>
    <td align="center"><img src="docs/images/alipay.jpg" alt="支付宝收款码" width="200" /><br/>支付宝</td>
    <td align="center"><img src="docs/images/wechat.jpg" alt="微信支付收款码" width="200" /><br/>微信支付</td>
  </tr>
</table>

其他支持方式见[赞助支持](docs/DONATE.md)。

## 设计参考与致谢

渠道目录、路由策略与用量视图的设计参考了 [magpie](https://github.com/yetone/magpie)（MIT）与 [sub2api](https://github.com/Wei-Shaw/sub2api)（LGPL-3.0）。感谢两个项目的作者与贡献者。这些项目的代码未被复制或链接进本仓库，署名与许可信息见 [NOTICE](NOTICE)。

## 许可证

本项目使用 [Apache License 2.0](LICENSE)。第三方组件的归属信息见 [NOTICE](NOTICE)。

## 发布签名

Android 正式包使用独立 release keystore 签名，构建时从 mobile/android/signing.properties（不入库）或 AETHER_RELEASE_* 环境变量读取。CI 通过以下仓库 Secrets 提供：

AETHER_RELEASE_KEYSTORE_BASE64、AETHER_RELEASE_STORE_PASSWORD、AETHER_RELEASE_KEY_ALIAS、AETHER_RELEASE_KEY_PASSWORD

本地可用 scripts/gen-android-keystore.sh 生成密钥并打印 base64。

> **务必在仓库外备份生成的 keystore**。密钥一旦丢失，后续版本无法覆盖安装，用户必须先卸载（会丢数据）。仅存放在 CI Secrets 中不够——Secrets 删除后无法恢复。
