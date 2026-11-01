# aether 实现级深度分析

> 调研日期：2026-10-27（子智能体逐文件读源码产出，非 README 转述）
> 对象：github.com/aether/aether（Go，22.5k stars，MIT，监听 127.0.0.1:3425）
> 仓库结构：main.go + 单一 internal/（72 子包）+ plugins/（15 个 Go 内建插件包）+ native/（pf-serve 推理边车）+ docs/subsystems/（319KB gateway-routing.md 设计文档，与代码同仓演进）

## 1. 网关核心：请求链路与协议互转

完整链路（docs/subsystems/gateway-routing.md + internal/gateway/chat.go 交叉证实）：
`handle(proto)` → `readBoundedRequestBody`（gzip/zstd 解码，512MB 上限）→ `serveAgent` 中间件链 → 秘密遮蔽 → 模型解析（GroupFor → provider.Resolve）→ 意图规则/分类（失败回退上一意图）→ planGroup/plan 生成候选 → weighRouted 构建 Seat → 每个 try 过并发闸（lanes.take，满则 429）与 RPM 闸（尊重 Retry-After）→ attempt（每账号代理）→ usage 台账与 trace。

**协议互转是统一 IR（hub-and-spoke），不是成对转换器**——与本项（internal/core/ir + 4 种极简事件 + transducer）同一流派。aether 的 IR 较胖：system/developer 归并进 `r.System`、tool 消息转成带 ToolResult Part 的 user 消息、`reasoning_content`/`reasoning`/`reasoning_details` 三种拼法统一进 Thinking Part，都在入站做足；出站 `buildChat` 内嵌 per-host 策略层（DeepSeek 推理重放、MiniMax `<think>` 重内联、Gemini extra_content 签名等）。

流式细节（chat.go）：
- 工具调用乱序增量索引片段用 `heldCall` 暂存、`giveHeld` 按索引整段吐出
- `<think>` 标签提取用 `mdCode.find` 做 Markdown 围栏感知，避免把代码块里的伪标签当真
- 消息修复：`pairToolMessages`（按调用顺序重排 tool 结果、孤儿结果转 user、未应答调用合成错误结果）
- cache-token 归一集中在一处（OpenAI/DeepSeek/Moonshot/Anthropic/OpenRouter 拼法各异）

## 2. 路由组：策略 = 纯排列函数 + fail-open

`internal/routes/strategies.go`：核心抽象 `Strategy{Order func(pool string, seats []Seat) []int}`——返回下标排列；`Seat` 携带 Used/Soon/Pace/Weight/Tokens/Free/TTFT/TPS 等。**策略只排序，resting/affinity/failover 全在网关侧**。`routes.Apply` recover 策略 panic 并校验结果是真排列，失败退回原始顺序（fail-open）。

- smart（默认）：按用量 share 分三档；fine 档"有 Learns 的排前，按续期时间升序（小时粒度截断避免频繁重排）"，并列保持输入序以保厂商 prompt cache 命中
- order：identity；rotate：per-pool 计数器；usage：tier 感知 + 用量半衰期 1h；pace：按"距续期每小时剩余额度"降序，分带保证比较器一致性；fast：TTFT/TPS

**意图路由是小模型分类**（internal/gateway/classify.go）：并发两路调分类模型（意图编号 + effort 等级），自带防抖：SHA-256 缓存键（TTL 10 分钟）、失败 30 秒冷却、inflight 去重共享 channel、防注入剥离 + 头 3000/尾 1000 rune 截断、短追问继承上一轮意图。

**会话亲和按"缓存价值"量化**（affinity.go）：决策链 `no-cache`（cache read+write < cacheWorth=1024）→ `cold`（>5 分钟未读）→ `cache` 保持；写入算已读；失败即拆粘；Strict 模式宁可拒绝也不换号。

## 3. 订阅共享：两条路线

- **Claude 订阅 = 驱动真实 claude 二进制**（claude_subscription.go + internal/claudebridge）：不是一请求一进程，而是**运行复用**——turnKey 匹配喂空闲运行（stdin）、其次 --resume、最后新起并把历史包进 `<conversation_history>`；工具经 aether 自身 MCP server 回环回调；配额窗口从流内 `rate_limit_event` 读出；进程房限总量，满则 503 让路由换别家。**窗口预热**（claude_warmup.go）：重置时刻通过真实 CLI 发小请求把窗口起点提前固定。
- **Codex/ChatGPT = OAuth token 直连**：读 Codex home 凭据 + 到期管理 + 自动重置。
- **Web 会话级反代：没有**——全部走 API/OAuth token 或驱动官方 CLI。

## 4. 隐私遮蔽（三层）

- 规则引擎（internal/redact/redact.go，47KB）：`rule{regexp, markers 快速预筛, 校验器}`，含 Luhn、身份证、邮箱 TLD、代码引用误报排除
- **稳定占位符**：`tag(kind,v)` = HMAC-SHA256 取 base32 前 8 位 → `{{API_KEY_k3v9x2mq}}`，确定性可复现；原文存全局 map（上限 20 万条）
- **容错还原**：正则容忍模型改坏占位符（大小写/丢花括号/夹空格归一化）；JSON-in-JSON 先重新转义再插回；流式 `partialTail` 扣住半个占位符
- 本地模型检测：独立边车进程 pf-serve（GGUF，二进制长度前缀 stdio 协议，单 turn channel 串行，失败分级退避，闲置自杀）
- **fail-closed**：遮蔽开着但 masker 不可用 → 请求一律不发（503）

## 5. 配置原子改写（含金量最高的一块）

`internal/edit` 包 13 源文件 + 32 测试文件，对 JSONC/TOML/YAML/env 各自手写**格式保留编辑器**，不重新序列化。TOML 路线 = "parser 只定位、字符串只动刀"：
1. go-toml Parser{KeepComments:true} 把偏移映射成行 span（含值与行尾注释之间的空白）
2. 只重建目标那一行 `prefix + literal + suffix`，行级拼接——未触碰的字节一个不变
3. 写盘前双重验证（语法解析 + Unmarshal 抓重复键），通过才原子写
4. I/O 层：temp+rename、symlink 解析到最终目标（最多 255 跳）、硬链接检测转 in-place 写、BOM 保留、陈旧 temp 清扫、多文件快照回滚

对比本项 `internal/core/clients`：同属"最小手术"路线，但 aether 有 parser 定位 + 写前验证，我们是行级正则。**值得抄：写前语法验证一步**（成本低、防呆收益大）。

## 6. 插件系统（README 的"Bun"说法需修正）

实际是三套运行时四个扩展点：
1. **Go 内建插件**：Register(Manifest) + Reconcile 依赖序应用 + "插件间只许通过服务通信"
2. **JS 网关中间件 = 纯 Go 引擎 moejs，进程内**（不是 Bun 不是 V8）：hooks onRequest/onProvider/onEvent/onResponse；`sync.Pool` 每请求取运行时 + `time.AfterFunc` → `rt.Interrupt` 限时（onRequest 250ms）；实测每事件 1-1.6µs；热重载靠 mtime stamp；fail-open
3. **Bun 只做 provider 插件宿主**：npm 包跑在 Bun 子进程（stdio、20s 加载超时、stderr 排水防管道堵死）
4. **New API 兼容层**：社区预置中间件包（param-override/model-map/system-prompt/word-guard），逐个行为对齐 New API 渠道设置

## 7. 故障转移与用量/OTLP

- resting 熔断：失败入内存 sink，重启解除；429 且有余量的键"沉底不出局"；可重试的只有"绘制类失败"（5xx/配额尽/不供该模型）
- 闸门：每账号并发 lane（满则 429）+ RPM；HTTP/2 静默断连视同 502
- OTLP：**手写 OTLP-over-HTTP**（不用官方 SDK），队列 128 条/128MiB 预算满则丢，批 32 条或 5 秒，配置变更的批次直接作废

## 8. 与本项架构的客观对比

**结论：宏观上同一流派，aether 是已跑通生产级验证的样本。** 分歧三点：
1. **IR 胖瘦**：aether 胖 IR（入站归并做足，跨协议转换天然完备）vs 本项极简事件（渠道更薄，但"tool 调用配对修复"这类上游序约束要 transducer 承担）。建议：保持极简事件，把 aether 的三类"解码端消歧"（heldCall 乱序整段化、围栏感知、partialTail 扣尾）抄进渠道适配层。
2. **厂商策略位置**：aether 把 host 差异烘在转换函数里（它最大的债务，anthropic.go 66KB/chat.go 55KB 巨石）；本项 transducer 天然是策略挂载点，做成"每渠道策略表 + 通用 transducer"会比它更可扩展——这是本项可以明确做得更好的地方。
3. **周边机制**：策略纯函数化 + fail-open、亲和按缓存价值量化（Why 枚举可观测）、池化 JS 引擎 + Interrupt 限时、手写 OTLP——都是本项多账号化路上迟早需要的可行做法。

## 9. 它做得一般的点

1. 协议文件巨石化（66KB/55KB，host 策略与协议形状不分层）
2. 包级全局可变状态偏多（redact values 全局 map、全局 sticks）——与多租户方向有张力
3. PII 边车串行瓶颈
4. JS 插件 DX 限制（无 node_modules、import 仅相对路径）
5. Claude 订阅桥链条长（真实 claude 二进制 + --dangerously-skip-permissions + 回环 MCP 回调），对 Claude Code 版本敏感，本质上脆弱——**本项 claude-sub 走 OAuth 直连 api.anthropic.com 的路线更轻**

## 10. 对本项的可执行启示（按优先级）

1. clients 包写配置前加"语法预验证"（TOML/JSON parse 一遍再写）——低成本防呆
2. 账号亲和从"会话粘住"升级为"缓存价值量化"决策（多账号 prompt cache 收益大）
3. 路由策略抽成纯排列函数 + fail-open recover（为 pace/fast 等未来策略铺路）
4. XML 工具补偿器补三类消歧：乱序 index 整段化、Markdown 围栏感知、流式扣尾（本项已有扣尾，补前两个）
