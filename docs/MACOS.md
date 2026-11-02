# macOS 桌面构建

> 状态：预览。仓库已有 macOS 平台适配与 unsigned universal `.app` 构建流程；正式分发仍需 Apple Developer ID 签名与公证。

## 当前已接入

- `cmd/cockpit` 可交叉编译为 `darwin/arm64`（Windows 上已验证）。
- Wails 桌面壳复用现有 `internal/web/ui2`，不是另一套前端。
- 数据目录默认落到 `~/Library/Application Support/AetherCockpit`；可用 `AETHER_DATA_DIR` 覆盖。
- 登录 shell PATH 会合并进 GUI 进程，便于找到 `claude`、`codex` 等 CLI。
- 单实例锁使用 `flock`；关闭窗口只隐藏，⌘Q / 托盘菜单退出。
- 托盘使用 macOS template PNG；应用菜单包含标准 App/Edit 菜单。
- 登录自启写 `~/Library/LaunchAgents/com.aether.cockpit.plist`。
- `/Applications`、`~/Applications` 中的 `Claude.app` / `Codex.app` 可一键打开。
- Claude Code / Codex CLI 一键启动会打开 Terminal.app 新窗口，并从临时脚本回收真实 PID；桌面端多开按独立进程组启动。
- 更新检查按平台选资产，macOS 不会下载 Windows zip；暂无 macOS Release 资产时只打开下载页。

## 本机构建（需 macOS + Xcode Command Line Tools）

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
wails build -platform darwin/universal -skipbindings -s -clean \
  -tags desktop,production
open build/bin/AetherCockpit.app
```

未签名产物首次运行可右键“打开”，或执行：

```bash
xattr -dr com.apple.quarantine build/bin/AetherCockpit.app
```

## CI

`.github/workflows/desktop-macos.yml` 在 `macos-latest` 上构建 unsigned universal `.app`，产出 zip 与 SHA256 artifact。该 workflow 只做 `workflow_dispatch`，不自动发布 Release。

## 尚未完成

- Apple Developer ID 签名、notarization、Sparkle/自更新。
- 部分渠道的本地凭据导入/注入需要 macOS 版本；网关反代本身可用。
- Terminal 启动/多开只做了 darwin 编译与单测编译，尚未在 Mac 实机验收。
- macOS 实机 UI 验收（无 Mac 设备时由 CI 构建 + 远程 Mac 验收补足）。
