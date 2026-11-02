# 更新日志

## 未发布

### macOS 桌面（预览）

- 新增 macOS 平台启动层：数据目录落到 `~/Library/Application Support/AetherCockpit`、登录 shell PATH 合并、单实例锁、菜单栏托盘与标准 App/Edit 菜单、LaunchAgent 登录自启。
- Claude Code / Codex CLI 一键启动改为 Terminal.app 新窗口，并通过临时脚本回收真实 PID；桌面端多开按独立进程组启动。
- 更新检查按平台选择资产：macOS 只接受 `macos/darwin/osx/universal` 包，不再把 Windows zip 当成 mac 更新；没有对应包时退回下载页。
- 复用现有 ui2 前端与 Wails 壳；新增 `desktop-macos.yml` 在 macOS runner 构建 unsigned universal `.app`，正式分发仍需签名与公证。

### 应用启动

- 识别 Microsoft Store / MSIX 版 Claude 桌面端：读取包清单里的 AUMID 并经 `shell:AppsFolder` 激活，不再把已安装的 Claude 误报为“不可用”。

### 安全与正确性

- 修复：只开启脱敏规则、未开启 privacy_mask 时，Responses 流式直通会绕过脱敏；Fast-Path 现在同时检查 privacy 与 redact 引擎。
- 云同步备份强制口令（至少 8 位），不再允许空口令明文上传；PBKDF2 迭代从 120k 提升到 600k，设置页新增口令输入与加密状态提示。
- 路由组不再把 `weight/free/levels/match/rules/fast/off/affinity` 静默当作 priority：保存时移除未实现字段，`/api/groups` 与保存响应返回 `routingEffective` 和 `warnings`；旧配置会明确提示“尚未实现，已忽略”。
- 账号卡区分“额度读取失败”和“渠道不支持额度”：不支持的渠道显示“该渠道不支持额度窗口”，不再留空白。
- 配置漂移支持一键写回：新增 `POST /api/drift/apply`，Agent 页出现漂移提示与“重新写入”按钮。
- 上下文超限自动续接扩展到 IR 路径：`/v1/messages`、Chat 与走 IR 的 Responses 也会裁剪较早消息后重试一次，并回填 `trimmed_items`。

### 测试

- 新增回归测试：Fast-Path 脱敏绕过、路由组未实现字段、账号额度能力标记、IR 上下文续接、备份口令强制、WebDAV 自动推送密封。

## 0.1.7（2026-11-02）

### Gemini CLI OAuth

- 恢复 Gemini CLI 官方公开 OAuth 客户端参数（与 aether 一致）作为刷新默认；账号凭据/环境变量可覆盖。
- 修复安全版移除内置参数后 Gemini CLI token 刷新失败的问题。

## 0.1.6（2026-11-02）

### Antigravity 额度

- 恢复官方公开 OAuth 客户端参数（与 local-vault / aether 一致）作为 Antigravity 刷新默认值；账号凭据/环境变量可覆盖，客户端参数轮换时从本机 `language_server` 运行时发现。
- 修复安全版移除内置参数后 Antigravity token 刷新失败、额度窗口读不到的问题；账号卡恢复 Gemini / Claude & GPT 的 5h/7d 窗口。

## 0.1.5（2026-11-02）

### 额度窗口

- Qoder CN / Intl 接入 `/api/v1/me/usage`，账号卡显示 Pro / Pro Trial 与加量包额度窗口，并给出计划到期时间。
- 账号卡「可用 AI 积分」优先显示剩余/余额，不再把总量当作可用量。

## 0.1.4（2026-11-01）

### 请求日志

- 列表分页：每页 20 条，支持首页 / 上一页 / 下一页 / 末页与页码；搜索、成功/失败筛选和范围切换自动回到第一页。

## 0.1.3（2026-11-01）

### 请求日志

- 列表新增「输出速率」（tok/s）；Token 列改为输入/输出，修正此前误显示总量/输出的问题。
- 重做日志列表：固定表头与对齐网格；中窄屏折叠为带标签的卡片行；KPI 5 列自适应；长协议名缩写显示。

## 0.1.2（2026-11-01）

### 品牌图标

- 品牌图标配色更新：四个星点改为青色，与蓝底、白色星芒和三节点路由徽章统一；网页品牌标与 favicon 同步，窗口/exe 与托盘图标保持一致。

## 0.1.1（2026-11-01）

### 浏览器授权与日志

- 浏览器授权统一交给系统默认浏览器打开；OAuth PKCE、设备码、MCP 登录、账号重绑与网页登录不再单开 WebView 窗口。
- Antigravity 与 Gemini CLI 的 OAuth 客户端参数改为从账号凭据或环境变量读取，仓库不再内置公开的 client_id / client_secret。
- 请求日志改为持久化账本（按天 JSONL，重启不丢），补错误解释、耗时/Token/计费详情与今日小时趋势。
- 显式 `渠道/模型` 前缀优先于裸名别名，修复非聊天渠道通用模型名（如 `cursor/auto`）被错误路由到其他渠道的问题。

### 品牌图标

- 保留 MCA 对话泡与星标主视觉，加入三节点路由徽章，并统一网页品牌标、favicon、桌面与托盘图标。

### 渠道模型目录与账号管理

- 部分渠道可在启动时读取上游实时模型目录，并在本机缓存 24 小时；模型详情和 `/v1/models` 可展示目录提供的上下文、能力、价格与推理档位信息。
- 按规范名称归并跨渠道模型路由；推理档位会收窄到目标模型支持的选项，并优先选择有可用账号的渠道。
- 账号导出提供脱敏元数据清单和口令加密账号包两种选择；账号包用于迁移时恢复凭证。
- 调整部分渠道错误归类和额度冷却处理；OpenCode Zen 与 OpenCode Go 使用各自渠道配置。

### 前端视觉与设计系统收口（不改色系）

- **灰阶重新分配**：图表网格线、拓扑连线从 `--line-2`/半透明 `--line-strong` 改用实色 `--line-strong`（1.17:1 → 1.58:1）；placeholder 由 `--disabled`（2.64:1）改为 `--muted`（6.34:1）；卡片 hover 补位移与描边加深。
- **排版尺度**：字号改为 12/14/16/18/20/24/32/40，修掉 `--fs-3xl(24) < --fs-2xl(32)` 的命名反转；11px 降为装饰专用（`--fs-2xs`）；补 18px 卡片标题档、40px KPI 档。
- **中文排印**：随包内置 Noto Sans SC 子集（400/500/600 三个字重共 1.6MB，见 `scripts/build-font.py`），汉字不再由系统字体决定；`:lang(zh)` 归零负字距、权重收敛到 400/500/600、关闭 `font-synthesis`；`.woff2` 的 MIME 在 Go 侧写死，避免无注册表的机器上因 `nosniff` 被拒而静默回落。
- **节律与边界**：区块间距 20→24；工具条 padding 6→8；去掉「更新于」的负 margin；1024 下工具条改为换行而非隐藏滚动条裁切；账号不足 3 个时补虚线占位卡。
- **图标精度**：描边统一 1.5 + `non-scaling-stroke`，渲染尺寸收敛到 16；补 `menu` 图标；未定义图标名改为告警而非静默降级；单色品牌标在暗色下反相。
- **信息去噪**：删掉恒为空的「可用 AI 积分 —」行；「加备注」改为 hover/聚焦显示；日期补「入库」语义、额度补「重置」后缀；健康卡改用渠道显示名、去掉逐行重复的「刚测」。
- **动效**：弹窗 / 命令面板 / 菜单补进入动效；抽屉动作条沉底，单字段动作改用 420px 紧凑抽屉。
- **图表**：趋势图重做为 `js/trend.js` —— 对齐 relay-panel 看板形态的单调三次平滑曲线 + 渐变面积 + 十字线与带色点、多指标读数的悬浮卡；轴刻度按「峰值三等分取整」得到 0/50/100/150；按容器实测宽度重算 viewBox（刻度恒 11px，不再被缩放放大）；支持指针悬停与 ←/→/Home/End 键盘游标；柱状模式两侧让半个柱宽避免压住 "0" 刻度；全 0 时只留基线并提示「该区间没有用量」。
- **热力图**：365 个格子不再只有原生 `title`，悬停时改写右侧汇总（日期 + 当日调用 / Token / 成本），读数位置固定、不遮挡网格。
- **趋势图对齐 relay-panel 消耗分布页（像素级）**：
  - 卡头：图标徽标（24px，accent-soft）+ 标题 + `总计: X`；右上角两组分段控件「总计|按模型」「柱状图|面积图」（28px 高，含图标，激活项白底投影）；
  - 纵轴 6 条线（0 + 5 档），步长候选扩到 1/2/2.5/3/5/10 —— 数据 137 才不会被顶成 250 的上限；网格线用 `--line-2` 实线、基线用 `--line-strong`（参考图同款分层）；
  - 横轴标签 `MM-DD`；
  - 柱状为堆叠柱、直角；面积为不堆叠面积（填充 0.08 / 线宽 2 / 单调曲线 / 点隐藏）；
  - 图例在图下方居中，10px 方形色块，单选隔离（点一次只看该序列）；
  - 分类色板 `--chart-1..10` 换成 VChart 默认数据 scheme（首色 `#1664FF`，与参考图取色一致）；
  - 后端 `usage/requests` 补 `chartBy.model`（按调用量前 5 + 其他，逐日对齐，带单元测试）；「总计」页签保留双轴（调用左轴 / 成本右轴）。

## 0.1.0（2026-10-30）

首个产品化版本。定位：**本机 AI 算力聚合网关**——把多个订阅账号 / API Key / 渠道收敛成一个 OpenAI 兼容入口，桌面端与无头服务共用同一套 Go 网关。

### 渠道与反代

- OpenAI Chat / Responses / Anthropic / Gemini 四种入口，外加 Responses WebSocket 入口。
- Codex 官方订阅上游 WebSocket 直通；通用逆向渠道可通过 spec 的 `endpoints.responses_ws` 启用上游 WS。
- 订阅渠道：Claude、Codex、Qoder（CN/Intl）、WorkBuddy（CN/Intl）、Trae（含 SOLO）、Kiro、ZCode（CN/Intl）、CodeBuddy（CN/Intl，OpenAI 兼容 SSE + 令牌刷新）、Devin CLI（Connect-RPC）、Antigravity、MiniMax。
- 通用渠道：OpenAI 兼容（含 Azure）、Bedrock、Vertex、Gemini 兼容、OpenRouter/DeepSeek/Kimi/GLM/本地推理等。
- 账号池：粘性会话、并发/RPM、429 窗口冷却、故障转移、官方优先、routing groups。

### 媒体与工具

- 图像：生成、编辑、异步任务、批量（≤16）。
- 视频：Grok Imagine 与 Sora 形状。
- 音频：转写 / 翻译 / 合成，`/transcribe`。
- 实时语音：`/v1/realtime` WebSocket 桥。
- 网络搜索：Tavily / Serper / SearXNG / Brave / Exa / Firecrawl / DeepSeek / You.com，支持多 Key 轮换与设置页测试；网关自带 MCP 服务器（`/mcp`）暴露 web_search / web_fetch / generate_image。

### 账号与运维

- 唤醒任务：账号级定时最小请求，提前触发额度重置周期；支持一键唤醒。
- 官方授权管理：官方通道暂停 / 恢复 / 检查，鉴权被拒自动暂停。
- 用量：配额窗口、成本、热力图、路由轨迹；签到调度。
- 客户端注入（17 个 Agent）与「应用多开」（按账号隔离实例）。
- 资源库：指令 / MCP / 技能 / 子代理 / Hooks，项目级与全局级，MCP Registry 与 skills.sh 市场。
- 云同步：WebDAV / S3 / GitHub；备份导出/导入。
- 2FA、隐私脱敏、PII 边车、OTLP 导出。

### 产品化

- 版本单一来源 `VERSION`；构建注入版本与时间，Windows 文件属性带版本资源；`/version` 与「设置 → 关于」一致。
- 应用内检查并**下载更新包**（`/api/update/download`）：写入下载目录、校验清单里的 SHA256，校验失败删除文件；不自动替换正在运行的 exe。
- `scripts/release.sh`：门禁 + 桌面端/无头 CLI + zip + SHA256SUMS + 发布说明。
- 文件日志：`data/logs/aether.log`，5 MB 滚动、保留一份历史。
- 首启「三步接入」引导；概览首屏不再被慢接口拖住。
- 桌面端「设置 → 常规 → 开机自启」开关（写入 HKCU Run，升级/移动目录后自动修正指向）；无头模式如实显示不可用。
- 用户级安装/卸载脚本（`installer/`，无需管理员）：安装到 %LOCALAPPDATA%/Programs/AetherCockpit，创建开始菜单/桌面快捷方式，可选登录自启，写入「应用和功能」条目；卸载保留数据可加 `-KeepData`。
- 正式产物不含 mock 渠道（仅 `-tags dev_mock` 的开发构建注册）；冒烟测试使用随机端口与现场种子账号。

### 已知限制

- Cursor / Zed / Windsurf 使用私有协议，暂不能作为上游渠道反代。
- Agent 版本巡检只提示升级命令，不代跑安装。
- 发布形态为便携压缩包 + PowerShell 用户级安装脚本（无 MSI/NSIS 安装包）。
