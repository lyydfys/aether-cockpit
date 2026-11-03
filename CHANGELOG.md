# 更新日志

## 0.1.9（2026-11-04）

### 云同步：三路合并（数据丢失级修复）

- 配置同步从"整包覆盖"改为**三路合并**：本机留底稿，推送时比较「底稿 / 本机 / 远端」，只有一边改过就取那一边；两边都改过则**保留本机值**，远端值写进 `data/sync/conflicts-<时间>.json` 留档。此前两台机器同时改设置，后推的会把先推的整份盖掉——配置静默消失。
- 上传走**条件写**：WebDAV 用 `If-Match`、GitHub 用 sha；版本不符就重新取远端、重新合并、再写一次（只重试一次，连续冲突说明对端在高频写入）。S3 对象存储没有条件 PUT，会标记 `Blind` 下次重新合并，但仍先合并再写。
- 新增**跨进程锁**（`sync-state.json.lock`，带 PID 与 TTL）：GUI 与 CLI 同时同步不再互相覆盖；进程崩溃后锁自动过期。
- 新增 part 级摘要（settings/specs/accounts_meta/library/icons）：内容没变时跳过上传。
- 合并结果会落回本机——只影响"本机没动过"的字段，因此不会覆盖用户刚改的设置。

### 命令行

- `aether-cockpit-cli` 不再只能"起一个网关"：新增子命令 `status` / `providers` / `usage [--csv]` / `quota` / `alerts` / `sessions` / `backup export|import`。它们打本机 `/api/*`（回环免鉴权），行为与桌面界面完全一致，也能对**已在运行**的实例操作。`serve` 与无参数调用保持原有行为。

### MCP 桥

- 新增 `aether-mcp`：stdio MCP server，把网关的媒体能力包装成 `aether_image` / `aether_video` 两个工具，agent（Claude Code / Codex / 任意 MCP 客户端）可以把网关当工具用。此前只有 HTTP 端点，agent 调不到。
- 该进程由**客户端**拉起，网关本体仍是单二进制、零外挂进程。
- 打包与发布流程一并带上 `aether-mcp.exe`（校验和自动包含）。

### 文档

- 新增 `docs/CLI.md`：CLI 子命令、MCP 接入示例（Claude Code / Codex）、云同步合并语义。

## 0.1.8（2026-11-04）

### 会话删除与回收站

- 会话删除不再返回 501：删除 = 把会话文件移进回收站并记 manifest，随时可以恢复；「彻底清除」才真删文件。原位置已有同名文件时拒绝恢复（不覆盖用户数据）。WSL 里的会话仍不支持删除（跨发行版搬运不可靠），页面按 `deletable` 隐藏按钮。
- 会话页新增「回收站」入口与「删除（可恢复）」按钮。

### 预算（多窗口并行）

- 网关密钥的限额从"单窗口"扩展为**多个并行窗口**：`{"windows":[{"period":"hours","hours":1,"tokens":1000},{"period":"day","tokens":50000}]}`，任意一个窗口触顶就拒。老的单窗口配置（`period`+`tokens`/`cost`）行为完全不变。
- 窗口类型支持 `hours` / `days`（滚动）与 `day` / `week` / `month`（日历，周从周一开始），可与 RPM 叠加。

### OTLP

- span 现在带 token 与费用：`gen_ai.usage.input_tokens / output_tokens / cache_read_tokens / cache_write_tokens / reasoning_tokens / total_tokens`、`cost_usd`、`pricing_model`、`gen_ai.request.model`，以及自动续接标记。此前这些字段在日志里齐备却一个都没写进 span，接 Langfuse/Tempo 基本点不亮。
- 新增 OTLP **metrics** 导出：traces 发成功后同时向 `/v1/metrics` 推送两个计数器（`gen_ai.client.token.usage` 按类型分点、`aether.request.cost`），按 model+provider 聚合；指标失败只记日志，不影响 traces。

### 用量导出

- 请求台账 CSV 从 14 列扩到 **43 列**：补齐渠道 ID / 账号 ID / 调用方 key / 会话 / 请求 ID / 来源 / 目的 / reasoning / total_tokens / 定价模型 / err_type / 重试 / 转交 / 流式 / 上下文项数 / 裁剪项数 / 预估 token / 上游截断 / 路由 ID。列定义改成"表头 + 取值函数"成对声明，加列不会再错位。
- 用量汇总新增**渠道维度**（`/api/usage` 的 `providers`）：账单能回答"钱主要花在哪个渠道"。

### 插件钩子

- 新增进程内请求管线钩子（**不启动任何外部进程**，与"单二进制"定位一致）：`onRequest` / `onProvider` / `onAccount` / `onEvent` / `onResponse` / `onUsage` 六个扩展点，Go 接口级注册，按注册顺序执行。
- 钩子全程 fail-open：单个钩子 panic 或超时（200ms）只跳过它自己，绝不拖垮请求。
- 内置 5 个可直接配置的钩子：`drop_reasoning`（丢弃推理增量）、`clamp_output`（封顶 max_tokens）、`block_models`（按前缀拦截模型，返回 4xx）、`guard_provider`（跳过指定渠道）、`guard_account`（跳过指定账号）。
- 设置页「请求钩子」可开关/填写；`GET /api/pipeline-hooks` 返回当前生效的钩子清单，便于确认配置真的装上了。

### 用量与限额

- **修复用量聚合漏加 cache write**：同一（日期 / 渠道 / 模型 / 账号 / 客户端 / key）从第二次请求起，`cache_creation` token 全部丢失——只有当天第一次落格时才带上。Anthropic 系 cache write 单价是 input 的 1.25 倍，等于用量页的 token 与费用长期偏低。
- **限额判定不再逐次翻账本**：原实现每个带 key 的请求都持锁把 30 天账本从磁盘读进来再排序，而账本单日有条数上限（5000），高流量日会丢最旧的记录 → "已用"偏小、硬上限被穿透。改为内存增量计数器（重启时用账本种一次，不会清零）。
- **新增在途预留**：原来只判"已用 ≥ 上限"，100 个并发请求会同时通过、一起烧掉上限的 100 倍。现在按"请求体 / 4 + 平均输出"预留额度，结束时用实际用量结算；并发突发不再穿透硬上限。
- **被拒的请求现在记账**：超限返回 429 时同时写账本与计数器，页面能回答"今天被限流拒了多少次"。
- **窗口语义补齐**：新增 `day` / `week` / `month` 日历窗口（到点整段归零，周从周一开始），与既有 `days` / `hours` 滚动窗口并存。
- **新增额度巡检（5 分钟一轮）**：窗口「快到期但还剩一大半」时提醒——这条最省钱，花钱买的额度不至于白扔；另有「额度偏低」「已用尽」两类告警。同类告警 6 小时内不重复，去重状态落盘，重启后不会重刷一遍。首页顶部显示，事件总线也会推一条。
- 新增 `GET /api/alerts`：当前仍成立的额度提醒 + 各密钥被拒次数。

### 会话

- **「在终端重开」真正可用**：新增 `POST /api/sessions/terminal`，在新终端窗口里于会话原工作目录执行 resume 命令。此前该端点是空 stub，页面只能复制命令去粘贴。
- resume 命令覆盖扫描器支持的三个客户端（`claude --resume` / `codex resume` / `gemini --resume`），此前只给 Claude Code 拼。
- 会话 ID 会拼进 shell 命令行，因此限制为安全字符集（字母数字与 `-_.`），其它一律拒绝并返回 400。

### 资料库

- **移除只删自己写过的条目**：新增下发记账（`applied`：kind + name + agent + 内容摘要）。此前 MCP 按名字 `delete()`、子代理无条件 `os.Remove()`，用户自己在配置里手写的同名条目会在"移除/断开同步"时被一起删掉。现在查不到下发记录就跳过，并在结果里明确写"这条不是 Aether Cockpit 写入的，已保留"。

### 脱敏

- **拦截动作现在真的拦截**：处置方式设为「拦截」时，请求在发往上游之前就被拒（403，错误码 `privacy_blocked`），不再"设了拦截却明文发出"。同时该分支也把原值换成占位符——纵深防御，即使将来某条路径漏判也不会泄露原文。
- **脱敏覆盖面补齐**：默认路径（脱敏规则引擎）现在也覆盖工具调用参数、工具执行结果、附件文件名；此前只有旧的隐私引擎覆盖，而 Agent 场景里最高频的泄露恰好是工具参数。
- 工具参数是 JSON 文本：脱敏改为逐字符串叶子替换再重新编码，回程还原按 JSON 转义，原值里含引号/反斜杠也不会拼出非法 JSON。
- 占位符加盐：由 24 位无盐哈希改为 64 位、带 HMAC 的占位符。旧实现下四千多个候选值就有约一半概率撞符，且拿到占位符可离线反推原值。盐**落盘**（`data/redact-salt`，0600）并跨重启复用——否则把带占位符的历史贴回来、断线重连、备份恢复都换不回原值。
- 新增规则类别：URL 内嵌密码（`PASSWORD`）、`Authorization` / `x-api-key` / `Cookie` 头、环境变量与 JSON 字段里的口令、中文写法（密码：xxx），以及火山方舟 / OpenRouter / Stripe / HuggingFace / Groq / xAI / Replicate 的 Key 前缀。
- 新增语义类实体（靠上下文锚点定位，不裸匹配）：`PERSON`（姓名：/我叫…）、`ADDRESS`（地址：/中文结构化地址）、`ACCOUNT`（账号：/用户名：）、`HOSTNAME`（hostname: / *.internal 等内网域名）、`USERNAME`。裸文本里猜人名会在没有本地模型时大量误伤，所以宁缺勿滥。
- 值映射表上限提到 20 万条并改为 **LRU** 淘汰：此前是 FIFO，灌入大量一次性值（日志、堆栈）时会把仍活跃的老密钥挤掉，回程就换不回原文了。
- 修复旧隐私引擎（`privacy_mask`）的工具参数不转义：值里含引号/反斜杠时会拼出非法 JSON，客户端直接解析失败。现在工具参数走 JSON 转义路径。
- 修复脱敏流尾没有按 JSON 转义（只在占位符被切断时触发，量小但性质相同）。
- 脱敏页从"两个开关 + 一段读错字段的列表"改为完整页面：总开关、按类型处置（脱敏/只记录/拦截）、细项开关、自定义规则增删、白名单编辑、试一试预览、记录过滤与详情（含实际发出去的文本）。顺带修掉两个前端 bug：设置页读 `redact_personal`（服务端发的是 `redactPersonal`）导致开关永远显示关闭；记录列表读 `r.kind/r.sample/r.at`（接口返回 `{key,t,agent,provider,values}`）导致类型恒为"字段"、时间恒为空。

### 会话续接

- 修复 `store:false` 导致 WebSocket 会话静默丢历史：`store` 的语义是"**上游**要不要保留这条 response"，不是"网关不能记上下文"。现在网关照常记账，只是标记为内部快照，`GET /v1/responses/{id}` 不会把它暴露出去。
- 新增 `previous_response_not_found`：引用的上一轮在网关里找不到时明确报 400，而不是静默跳过回填。Codex 一类客户端收到后会重开连接并重发整段会话，可以自愈。
- WebSocket 支持 `response.interrupt` / `response.cancel`：取消当前轮、连接继续可用；同一连接内自持最近 2 轮上下文，不再每次都依赖 store。

### 路由与调度

- 新增嵌套路由组：组员可以写 `group/<id>`，递归展开最多 8 层、带环检测；内层组没写 routing 时继承外层。
- 新增组规则：条件支持 `intent` / `effort` / `time` / `tokens` / `images` / `agents` / `folders` / `exceptFolders`，动作是 `use`（成员前移）与 `useEffort`（强制推理档）；`pause` 可临时停用一条规则。时段规则支持跨午夜、IANA 时区与星期过滤。
  - 没有条件的规则一律不命中：否则一条写漏条件的规则会把整组顺序钉死。
  - 文件夹条件用"路径前缀 + 分隔符"比较，`/work/ap` 不会误命中 `/work/app`。
- 路由页可以编辑路由组了：成员（含 `group/<id>` 嵌套）、策略、规则都能在页面上改；此前只有只读列表，改配置只能手写 JSON 打接口。
- 新增 `usage` 路由策略，与 `pace` 分开：`usage` 按剩余额度最多排序（先花大号、保住小号做兜底），`pace` 按"剩余 / 距到期时间"排序（先花临期额度）。此前组级的 `usage` 被直接折成 `pace`，选了 `usage` 拿到的是另一套语义。
- 新增窗口级额度闸（`window_caps`）：账号在某个限流窗口用到设定百分比后自动排到队尾（不排除——全池触顶时仍能发请求）。
- 冷却细分到模型：某个模型被限流只让那个模型休息，不再把整个账号按住。账号级失败改为指数退避（1min → 10min 上限，带抖动），并随账号落盘、重启后恢复。
- 新增自适应推理档：客户端没指定 `reasoning_effort` 时，按请求难度给 low/medium/high（短问题 low 省 token，改代码 / 长上下文 / 多工具 high），再按模型支持的档位收窄。组规则强制指定的档位优先于自适应。

### 渠道与扩展

- spec 现在可以**声明一个新渠道**：在 `extra.reverse_profile` 里写 `{"kind":"reverse", ...}`，即可把一条 OpenAI 兼容端点注册成可路由渠道，热更新生效、删除即下线，无需改代码重新发版。
  - 只允许 reverse 形态（base_url / 鉴权头 / 字段名 / 响应路径）；不允许声明 shell 或任意 HTTP 钩子，保持"单二进制、无外挂进程"的定位。
  - 内置渠道受保护：spec 不能覆盖同名内置渠道。

### Agent 接线

- 配置保真：Claude Code / opencode / mimocode / Qwen / Droid / Cline（三份文件）/ CodeBuddy / WorkBuddy / Copilot JetBrains / Zed 改为「只替换改动过的键」，保留注释、键序与用户其它配置；带注释的 JSONC 不再被拒写。splice 不可用时自动回退到整文件重写。写盘路径还支持**删除键**（例如 Cline 的旧 `modelInfo` 会被真正移除，而不是留在原地）。
- 写回时保留 UTF-8 BOM（原来读时剥掉、写时不还，用户用记事本再打开会看到乱码）。
- **断开改为键级还原**：接入时记录"我方改过哪些路径、原来是什么"，断开只还原这些路径。用户接入后手改的主题、自己加的 provider 都会保留——此前整份备份写回会把这些一起抹掉。文件若从未被动过仍走整份备份，保证逐字节回到原样。
- Claude Code 的 `opus` / `sonnet` / `haiku` 三档可以分别选模型了（此前被压成同一个）；Codex 可以选推理档位（写 `model_reasoning_effort`）。页面上暂存的附加字段现在会随「应用」真正落盘——此前 `stage` 是只写不读的死代码，选了也不生效。
- Agent 列表默认只显示"本机装了"或"已接入"的，其余收起来并可一键展开；此前十几条全列，真正在用的那几个淹在一堆"未检测到"里。
- 漂移（drift）分了 5 类：文件被删 / 被新建 / 网关地址被改回官方 / 只有模型被改 / 其它键被改。页面可以按类型给不同修复动作，而不是一律报"未接入"。
- 「保持当前设置」真的生效了：记住当时那份差异的摘要，之后不再重复提示；配置再次变化时才重新报漂移。
- 切换端口后自动把已接入的 Agent 配置改写到新地址，不再出现"改完端口全部连不上"。
- Agent 页新增「方案」：把当前各 Agent 的模型存成命名快照，一键切换（后端 `/api/profiles` 一直存在，此前前端零调用）。

### Fast-Path

- 修复 Fast-Path 绕过全部改写规则：启用任意改写规则（参数覆盖 / 模型映射 / 关键词拦截 / 系统提示）或渠道 spec 声明了系统提示/参数缺省时，该请求不再走直通，改走完整管线。
- 中间件清单变更现在会作废管道缓存（此前要重启进程才生效）。

### 媒体接口

- 图像 / 视频 / 音频 / 实时接口可以按模型名路由到已配置渠道，吃该渠道的账号池与失败转移；未配置渠道时仍走设置页的单一 base_url，行为不变。

### 备份

- 备份格式升级为 `AETHER2`：头部携带 KDF 迭代次数并纳入完整性校验，读取时校验区间。旧 `AETHER1` 仍可读取。
- 读取侧 fail-closed：不是备份格式的内容（包括明文 JSON）一律拒绝，不再原样放行。
- 保存云同步配置时拒绝把备份口令设成 WebDAV 密码 / S3 Secret / GitHub Token。

### 模型目录

- 新增模型重定向（对齐 One API / New API）：`model_redirects` 把虚拟模型名映射到「渠道 → 上游模型名」，客户端只写虚拟名，路由按渠道顺序生成候选链；`/v1/models`、Gemini 清单、Agent 与调用密钥选择器都会列出虚拟名。设置页新增「模型目录」编辑器。右边的目标可以写公开模型名、显示名或上游 key，路由会折成该渠道真正发送的上游名（例如 Qoder `DeepSeek-Flash` → `dfmodel`）。
- 新增「添加缺失模型」：渠道详情 → 模型页可手动补一个上游 `/models` 漏报、或厂商不实现 `/models` 的模型；写入 `provider_extra_models`，在 merge / fallback / allowlist 三种模式下都会参与路由与 `/v1/models`，并自动加入已配置的渠道模型选择。
- 新增「刷新全部模型目录」：设置页与全局工具栏可一次刷新所有支持上游目录的渠道（allowlist 与无账号渠道自动跳过）。
- 修正 DeepSeek V4 Flash 与 V4.1 Flash 的身份：两者是两个模型，不再把 Qoder 的 V4 Flash 静态项/旧名映射到 V4.1 的 `dfmodel`；models.dev canonical 别名与家族兜底都增加版本代际守卫，带版本号的请求不会在家族兜底时接到完全不标版本的泛槽位，避免回退时静默换模型。
- 修复「模型映射」规则只改 `req.Model`、渠道仍按旧 `UpstreamModel` 发送的问题；现在规则会同步改写上游模型名。
- 新增渠道级模型目录三态配置（对齐 GoModel 的 `fallback / allowlist / merge`）：
  - `merge`（默认）：上游实时目录 + 静态清单，live 优先、静态补缺，保持既有行为；
  - `fallback`：上游实时目录优先，拉取失败或为空时才使用静态清单；
  - `allowlist`：只用静态清单，完全跳过上游 `/models`，自动刷新与渠道页的「从上游刷新」一并关闭。
- 渠道编辑器「模型」页新增目录模式下拉，并显示当前来源（实时 / 静态 / 合并）；切换后保存立即重建路由表。
- 路由表、`/v1/models`、渠道页、调用密钥模型选择器、Agent 模型选择器统一使用同一份有效目录，避免各处看到的模型清单不一致。
- 修复渠道详情保存会把模型选择收缩到实时目录的问题：只有真正改动模型勾选/倍率时才提交 `models`，避免一次无关保存覆盖静态补缺的模型选择。
- 合并实时与静态目录时按归一化 ID 去重，`DeepSeek-V4-Pro` 与 `deepseek-v4-pro` 不再重复出现。

### 渠道可用性

- 渠道健康巡检：某个渠道的账号全部额度耗尽或登录失效时，自动把它下线、不再参与模型解析；账号恢复额度或重新登录后，下一轮巡检自动上线——不用手动开关。巡检每分钟一轮，纯内存判定，只在状态变化时落盘。
  - 边界：`cooling_down` / `refreshing` 属于临时状态，不算死；一个账号都没有的渠道不处理（那是"还没配置"，关掉会让人找不到入口去加账号）；手动停用优先级更高，自动状态不会覆盖也不会替它恢复。
  - 手动开启一个仍然全灭的渠道时，自动状态会被清掉，但下一轮巡检会重新置上——开关文案会提示原因，避免"点了又关回去"的困惑。
- 渠道列表新增 `auto_off` / `auto_off_reason` / `auto_off_until` 字段，「供应商与开关」里对应渠道显示「额度用尽 · 已自动关闭 · MM-DD HH:MM 恢复」。

### 外链与桌面集成

- 站外链接（GitHub 仓库 / Releases / Issues / 赞助 / 作者主页）统一改为经 `/api/open` 交给系统默认浏览器；此前走 `window.open`，在桌面端会开出一个没有标题栏、也没有 Wails 运行时的裸窗口，关掉之前回不到主界面。
- 网关页的 `/healthz`、`/readyz`、`/metrics` 改为抽屉内联展示：带状态码、`/readyz` 未就绪时直接给出原因，支持复制与重读，不再为看一眼探针多弹一个窗口。
- 会话「下载 Markdown」改用 anchor 触发（服务端已带 `Content-Disposition`），避免先弹一个空白 WebView 窗口再开始下载。
- 三份重复的 `openAuthPage` 合并为 `js/links.js` 的 `openInBrowser`，授权页与普通外链共用同一出口，浏览器模式与桌面模式行为一致。
- 修正托盘菜单文案与动作不符：原「打开日志目录」实际用浏览器打开了管理界面；现拆成「打开管理界面」与「打开日志目录」两项，后者真正调起系统文件管理器。
- 托盘读取网关地址改走加锁的 `currentAddr()`，修复端口热切换时对 `addr` 的数据竞争。

### macOS 桌面（预览）

- 新增 macOS 平台启动层：数据目录落到 `~/Library/Application Support/AetherCockpit`、登录 shell PATH 合并、单实例锁、菜单栏托盘与标准 App/Edit 菜单、LaunchAgent 登录自启。
- Claude Code / Codex CLI 一键启动改为 Terminal.app 新窗口，并通过临时脚本回收真实 PID；桌面端多开按独立进程组启动。
- 更新检查按平台选择资产：macOS 只接受 `macos/darwin/osx/universal` 包，不再把 Windows zip 当成 mac 更新；没有对应包时退回下载页。
- 复用现有 ui2 前端与 Wails 壳；新增 `desktop-macos.yml` 在 macOS runner 构建 unsigned universal `.app`，正式分发仍需签名与公证。

### 应用启动

- 识别 Microsoft Store / MSIX 版 Claude 桌面端：读取包清单里的 AUMID 并经 `shell:AppsFolder` 激活，不再把已安装的 Claude 误报为“不可用”。

### 安全与正确性

- 修复「模型明明可用却路由不到」：上游 `/models` 目录经常漏项，此前 live 目录会**整体替换**渠道的静态清单——只要上游少报一个模型，清单里写好的那个就从路由表里消失，客户端发裸名再也匹配不上。现在改为 live 优先、静态补缺（`mergeCatalogModels`）。
  - 实测案例：`workbuddy-cn` 的静态清单里有 `deepseek-v4.1-flash`，上游 `/models` 没返回它，但直连请求 200——被 live 覆盖后这个模型凭空消失。
  - 同时给 `trae-cn` / `trae-solo-cn` 补上 `deepseek-v4.1-flash`（上游接受，清单里一直没有）。
- 修掉「客户端卡在正在重试」：`attachResetHint` 原本把账号的额度重置时间原样塞进 `Retry-After`，实测出现过 **85538 秒（≈23.8 小时）**——带自动重试的 agent 会把它当硬等待，界面上就是"正在重试(1/10) · 重试间隔 85538 秒"一直挂着不动。现在退避上限 120 秒，到点自动重试（期间用户往往已经充值、加号或启用了别的渠道）。
- WorkBuddy 通道校验（11128「Illegal API invocation from an unapproved channel」）自动重放：上游的提示是"请求被拦截，请重新发送"，但它按 `X-Conversation-ID` / `X-Conversation-Request-ID` 记住被拦的那一次，而这两个 id 是"会话 + 最后一条用户文本"的哈希——用户点重试时文本没变，id 没变，于是每次都被同样拦下。现在遇到 11128 会用一组全新 id 重放一次。
- 补裸协议路径别名（`/chat/completions`、`/responses`、`/messages`、`/models`）：各家客户端对 Base URL 的拼法不一致，Qoder 这类客户端会把 `/chat/completions` 直接拼在用户填的地址后面，填 `http://host:7860`（漏 `/v1`）时整条接入流程卡在 404，校验不通过就不保存配置，表现为"接入无法使用"。
- 修复 Qoder 账号被误判下线：加量包（`add_on_quota`）是独立钱包，此前只按主额度算 `Remaining`，"主额度 2000/2000 用尽但加量包还剩 100"的账号会被标成 `depleted` 直接不参与路由。现在 `Remaining` 计入加量包。
- 额度恢复后自动解除 `depleted`：此前只有"耗尽 → 标记"的单向逻辑，充值、买加量包或新计费窗口开始后，账号仍会卡在耗尽状态直到旧的 `reset_at` 到期。
- 选号失败的报错不再笼统：按状态拆成「N 个额度用尽 / N 个登录失效 / N 个冷却中 / N 个不服务该模型」并给出最早恢复时间。此前客户端（Qoder IDE、DSH 等）只能把 `no_account_available` 显示成"系统发生异常"，看不出是额度问题还是登录失效。
- 修复用量双计：WorkBuddy / Qoder / Trae / 逆向 spec 渠道的 `prompt_tokens` 本已包含 `cached_tokens`，解析侧却直接当 `InputTokens`、再额外记一份 `CachedTokens`，导致缓存命中量被算两遍——总量虚增近一倍，命中率在数学上被压在 50% 以下（`cached/(prompt+cached)`）。新增 `ir.SplitPromptCache` 统一拆分，上述渠道全部改用它并重算 `TotalTokens`。
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
