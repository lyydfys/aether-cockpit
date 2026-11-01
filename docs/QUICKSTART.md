# Aether Cockpit 快速开始（Windows）

## 1. 运行

桌面版：双击 `AetherCockpit.exe`。首次启动会在可执行文件所在目录创建 `data/`（配置、账号、日志与用量），默认只监听 `127.0.0.1:7860`。

无头版（服务器/脚本）：

```bash
aether-cockpit-cli.exe -addr 127.0.0.1:7860
aether-cockpit-cli.exe -config cockpit.json      # 配置文件方式
```

## 1.1 安装（可选）

不想每次手动解压：在发布包根目录运行

```powershell
powershell -ExecutionPolicy Bypass -File installer/install.ps1              # 安装到 %LOCALAPPDATA%/Programs/AetherCockpit
powershell -ExecutionPolicy Bypass -File installer/install.ps1 -AutoStart   # 同时设置登录自启
```

卸载：设置 → 应用 → Aether Cockpit，或运行安装目录下的 `uninstall.ps1`（加 `-KeepData` 保留 data/）。

> 桌面端可以直接在「设置 → 常规 → 开机自启」打开登录自启；无头模式请用系统服务或计划任务。

## 2. 安全三件事

1. **设置密钥**：「设置 → 常规 → 网关密钥」。本机回环可免鉴权；开放到局域网必须设置。
2. **监听范围**：默认仅本机。需要局域网访问时在「设置 → 网络与共享」开启，并确认已设置密钥与允许的来源 IP。
3. **数据目录**：`data/` 内有加密凭据。备份走「设置 → 同步与备份」；不要把 `data/` 直接发给别人。

## 3. 接入第一个渠道

「接入渠道」页：

- 订阅类（Claude/Codex/Qoder/WorkBuddy/Trae/Kiro/ZCode/CodeBuddy/Devin…）：按渠道提示登录或粘贴令牌。
- 通用 API：添加「OpenAI 兼容」账号，填 Base URL 与 API Key。

添加后在「渠道与额度」页能看到账号状态与剩余额度；「网关与探针」页有四种协议的接入片段（OpenAI Chat / Responses / Anthropic / Gemini）。

## 4. 客户端接入

把客户端指向 `http://127.0.0.1:7860/v1`（或页面上的实际地址），模型名写 `<渠道>/<模型>`，例如 `qoder-cn/glm-5.2`；不知道模型名时先在「接入渠道」页看渠道清单，或在 `/v1/models` 查询。

## 5. 高级选项（可选）

- **数据目录**：默认 `./data`；设置环境变量 `AETHER_DATA_DIR=D:\AetherData` 可整体搬迁（桌面端与无头 CLI 都生效）。
- **更新检查**：「设置 → 关于 → 重新检查」会读取环境变量 `AETHER_UPDATE_URL` 指向的 JSON 清单（`{"version","url","notes","published_at","sha256","size"}`）；未配置时如实提示"未配置更新源"。清单带 `sha256` 时可用「下载更新」把更新包下载到 `AETHER_DOWNLOAD_DIR`（默认用户 Downloads）并校验；只下载，不自动替换运行中的程序。
- **日志**：`data/logs/aether.log`，单文件 5 MB 滚动，保留一份历史；panic 会连同堆栈写入。
- **Google OAuth 刷新**：Antigravity 默认使用官方客户端公开发布的 OAuth 参数（与 cockpit-tools / magpie 一致，非用户账号凭证）；账号凭据或 `AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID` / `AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET` 可覆盖，客户端参数轮换时会从本机官方 `language_server` 运行时发现。Gemini CLI 仍需配置 `AETHER_GEMINI_CLI_OAUTH_CLIENT_ID` / `AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET`（或账号凭据里的 `oauth_client_id` / `oauth_client_secret`）。Antigravity 的 OAuth 登录按钮只会在显式配置客户端凭据时显示；已导入且未过期的访问令牌仍可使用。
- **构建工具链**：`go.mod` 固定 `toolchain go1.27.3`（修复了 go1.27.0 的 11 个标准库漏洞）；首次构建会自动拉取该工具链。

## 6. 常见问题

- **端口被占用**：`-addr 127.0.0.1:7870` 换端口，或先关闭占用进程。
- **局域网打不开**：确认监听范围已是局域网、密钥已设置、Windows 防火墙放行该端口。
- **额度显示为空**：部分渠道没有公开额度接口，或令牌已过期；到账号卡点「刷新额度」，仍为空属该渠道限制。
- **想重置本机数据**：退出应用后删除 `data/`（会丢账号与用量历史）。

## 7. 目录说明

| 路径 | 用途 |
| --- | --- |
| `data/accounts.json` | 账号与加密凭据 |
| `data/settings.json` | 设置 |
| `data/wake.json` | 唤醒任务 |
| `data/logs/aether.log` | 滚动日志 |
| `data/ledger/` | 用量账本 |
| `data/wake.json` | 唤醒任务 |
| `docs/QUICKSTART.md` | 本文 |
| `VERSION` | 当前版本号 |

## 8. 升级

下载新版本压缩包，退出旧版后覆盖可执行文件即可；`data/` 保持不动。版本号与构建时间可在「设置 → 关于」或 `/version` 查看。
