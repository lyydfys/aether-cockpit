# 命令行与 MCP 桥

Aether Cockpit 装好之后有三种用法：桌面界面、无头 CLI、以及给 agent 用的 MCP 工具。

---

## 一、无头 CLI（`aether-cockpit-cli`）

服务器 / 容器 / WSL 里没有桌面环境，CLI 是唯一入口。

```bash
# 启动网关（默认行为，与以前完全一致）
aether-cockpit-cli --addr 127.0.0.1:7860 --key <网关密钥>

# 也可以显式写 serve
aether-cockpit-cli serve --addr 127.0.0.1:7860
```

子命令**操作的是正在运行的实例**（对回环地址免鉴权），因此不会出现"CLI 看到的和界面不一致"：

| 命令 | 作用 |
| --- | --- |
| `status` | 运行地址、已服务请求数、接入 Agent 数 |
| `providers` | 渠道清单与启停状态 |
| `usage [--days N] [--csv]` | 用量汇总；`--csv` 输出完整 43 列台账 |
| `quota` | 订阅窗口额度（原始 JSON，便于脚本处理） |
| `alerts` | 当前额度提醒（即将重置 / 偏低 / 已用尽） |
| `sessions [--agent ID]` | 列出本机会话 |
| `backup export --out FILE` | 导出配置备份 |
| `backup import --in FILE` | 导入配置备份 |

公共参数：

```text
--addr   网关地址，默认 http://127.0.0.1:7860（也可用 AETHER_ADDR）
--key    网关 API Key（也可用 AETHER_API_KEY）；本机访问可省略
```

示例：每天导出一次用量到文件，交给外部记账。

```bash
aether-cockpit-cli usage --days 1 --csv >> /var/log/aether-usage.csv
```

---

## 二、MCP 桥（`aether-mcp`）

网关有 `/v1/chat/completions`、`/v1/models`、`/v1/images/*`、`/v1/videos/*` 这些 HTTP 端点，
但 agent 不会自己拼 HTTP 请求——它们只会调工具。`aether-mcp` 就是这层包装：一个 stdio
MCP server，由**客户端**拉起，把网关能力暴露成四个工具：

| 工具 | 说明 |
| --- | --- |
| `aether_chat` | 用网关里的任意模型对话（自动选号、计费、脱敏）。参数：`prompt` 或 `messages`（二选一）、`system`、`model`、`max_tokens`、`temperature`。返回助手文本 + token 用量 |
| `aether_models` | 列出当前可用模型与归属渠道（走 `/v1/models`） |
| `aether_image` | 文生图。参数：`prompt`（必填）、`model`、`size`、`n` |
| `aether_video` | 文生视频（异步任务）。参数：`prompt`（必填）、`model` |

> `aether_chat` 的返回值是**纯文本 + 用量摘要**，不是原始 JSON：工具结果是给模型读的，
> 塞整段 JSON 只会白烧上下文。需要原始结构时直接调 HTTP 端点。

### 接到 Claude Code

在 `~/.claude.json`（或项目级 `.mcp.json`）里加：

```json
{
  "mcpServers": {
    "aether": {
      "command": "C:/path/to/aether-mcp.exe",
      "env": { "AETHER_ADDR": "http://127.0.0.1:7860" }
    }
  }
}
```

### 接到 Codex CLI

`~/.codex/config.toml`：

```toml
[mcp_servers.aether]
command = "C:/path/to/aether-mcp.exe"
env = { AETHER_ADDR = "http://127.0.0.1:7860" }
```

环境变量：

```text
AETHER_ADDR     网关地址，默认 http://127.0.0.1:7860
AETHER_API_KEY  网关密钥（本机回环可省略）
```

### 为什么是 stdio

MCP 里 stdio 是兼容性最好的传输。这个进程是**客户端**拉起的，不是网关拉起的——
网关本体仍然是单二进制、零外挂进程。

---

## 三、云同步的合并语义

配置同步（WebDAV / S3 / GitHub）不再是"整包覆盖"，而是三路合并：

- 本机留一份**底稿**（上次成功同步的内容）；
- 推送时取远端当前内容，做 `底稿 / 本机 / 远端` 三方比较；
- 只有一边改过 → 取改过的那一边；两边都改过 → **保留本机值**，远端值写进
  `data/sync/conflicts-<时间>.json` 留档；
- 上传走条件写（WebDAV 的 `If-Match` / GitHub 的 sha）——版本不符就重新取远端、
  重新合并、再写一次。

这意味着两台机器同时改设置不会再"后推的覆盖先推的"。冲突档案在 `data/sync/` 下，
可以直接打开看另一台机器当时想写什么。

S3 对象存储不支持条件 PUT，这条路径会标记 `Blind`（下次同步重新合并），
但仍会先合并再写，不会整包覆盖。
