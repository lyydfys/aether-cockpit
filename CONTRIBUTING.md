# 参与贡献

感谢你愿意帮助改进 Aether Cockpit。欢迎提交问题报告、文档修正、测试和代码改进。开始前请先阅读本页与 [安全政策](SECURITY.md)。

## 提交问题前

- 先查看 [现有 Issues](https://github.com/lyydfys/aether-cockpit/issues)，避免重复报告。
- 使用 [快速开始](docs/QUICKSTART.md) 和 [项目文档索引](docs/README.md) 确认行为与预期。
- 不要在 Issue、Pull Request、截图或日志中放入真实 API Key、OAuth/会话令牌、账号数据、私钥或未脱敏的请求内容。安全漏洞请按 [SECURITY.md](SECURITY.md) 私下报告。

## 开发环境

项目当前主要面向 Windows 10/11 x64。构建桌面端需要 Go 工具链（仓库 `go.mod` 指定 Go 1.27.3 toolchain）；前端检查需要 Node.js 与 npm。Go 会按 `go.mod` 自动选择指定工具链，首次运行可能需要下载。

```powershell
go version
node --version
npm --version
```

执行前端检查前安装锁定的开发依赖：

```powershell
npm ci
```

## 本地验证

改动 Go 代码后至少运行：

```powershell
go test ./...
go vet ./...
```

改动管理界面后再运行：

```powershell
npm run test:ui
npm run typecheck
npm run ui:lint
```

桌面端也可在 Windows 上直接构建验证：

```powershell
go build -tags desktop,production -ldflags "-H windowsgui" -o AetherCockpit.exe ./cmd/desktop
go build -o cockpit.exe ./cmd/cockpit
```

仓库提供更完整的维护者门禁 `scripts/gate.sh`。它使用 Bash，并包含 Windows PE 资源生成、桌面端重编、串行 Go 测试、前端检查和本地 mock 冒烟；脚本假设 Windows/Git Bash 环境以及 C:、D: 盘路径，不是通用的 Linux/macOS 命令。它会在忽略的 `bin/` 和 `build/bin/` 下生成构建产物。

```bash
./scripts/gate.sh
```

不要为了通过测试连接自己的真实 AI 账号或复制账号库。`scripts/real-smoke.sh` 默认会使用本机账号并消耗上游额度；自动化验证应使用 mock 模式：

```bash
./scripts/real-smoke.sh mock-native/echo-pro
```

## Pull Request

1. 从最新的 `master` 建立短期分支；一个 PR 聚焦一个可描述、可验证的改动。
2. 代码遵循相邻模块的现有结构。涉及协议、账号存储、认证、导入导出或网络监听的改动应补充针对性测试。
3. 在 PR 描述中写清问题、用户可见变化、验证命令及其结果；界面改动附脱敏截图，行为变化同步更新用户文档或 CHANGELOG。
4. 确认提交中没有账号数据、凭证、构建产物、个人路径、参考项目源码或无关文件。
5. 提交 PR 后配合 CI 和评审反馈；未通过的检查应说明原因及复现方式。

提交标题建议使用简洁的 Conventional Commit 风格，例如 `fix(gateway): handle upstream timeout` 或 `docs: clarify Windows setup`。无需为格式本身拆分提交。

## 发布

维护者发布新版本时，使用 `scripts/bump.sh <x.y.z>` 同步根目录 `VERSION` 与 `wails.json` 产品版本，更新 `CHANGELOG.md`，并确认 CI 全绿。将 `v<x.y.z>` 标签推送到 GitHub 后，Release 工作流会重新验证、构建 Windows x64 桌面端与 CLI、生成 SHA256 校验和并创建 Release。不要把本机 `dist/` 或二进制构建产物提交到仓库。

## 许可证

提交代码即表示你有权提交该贡献，并同意该贡献按仓库的 [Apache License 2.0](LICENSE) 发布；如有不同的授权条件，请在提交前明确说明。
