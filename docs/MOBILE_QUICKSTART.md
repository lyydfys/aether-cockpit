# Aether Cockpit Mobile · 上手（Android）

> **一句话定位**：这是一台**独立**的手机本地网关。它跑在手机自己的沙箱里，只监听 `127.0.0.1`，
> 与 Windows 端的账号、会话、账本**没有任何同步通路**——你在手机上看到的数据是这台手机自己的。

本文只讲"怎么把它装起来、跑通第一条请求"。能力边界与逐条对标见
`docs/MOBILE_APP_ARCHITECTURE.md` 与 `docs/internal/MOBILE_PARITY_MATRIX.md`。

---

## 0. 需要什么

| 项 | 版本 / 说明 |
|---|---|
| JDK | **21** |
| Android SDK | `compileSdk 36`、`minSdk 26` |
| NDK | 与 `mobile/android/app/build.gradle.kts` 里 `ndkVersion` 一致（本仓库当前用 `29.*`） |
| 设备 | arm64 真机（**x86_64 模拟器当前不可用**，见文末） |
| 工具 | `adb`（来自 Android SDK platform-tools） |

下面的命令都用变量，**把你的路径填进去**，不要把别人机器上的绝对路径抄进来：

```bash
# 按需改成你自己的路径（这三个是变量，不是某个人的机器路径）
export JAVA_HOME="/path/to/jdk-21"
export ANDROID_HOME="/path/to/android-sdk"
export NDK_VERSION="29.0.13113456"      # 与 build.gradle.kts 的 ndkVersion 保持一致
export PATH="$JAVA_HOME/bin:$ANDROID_HOME/platform-tools:$PATH"

cd /path/to/Aether\ Cockpit
```

> Windows（PowerShell）等价写法：
> ```powershell
> $env:JAVA_HOME="C:\path\to\jdk-21"
> $env:ANDROID_HOME="C:\path\to\android-sdk"
> $env:PATH="$env:JAVA_HOME\bin;$env:ANDROID_HOME\platform-tools;$env:PATH"
> ```

---

## 1. 构建

### 1.1 先编原生桥（改了 Go 就必须重编）

```bash
bash scripts/build-android-mobilebridge.sh arm64-v8a
```

产物落在 `dist/android/arm64-v8a/libaether_mobilebridge.so`。
**只要动过 `internal/**` 或 `cmd/**`，就必须重跑这一步**，否则 APK 里还是旧桥接库。

### 1.2 再打 APK

```bash
cd mobile/android
./gradlew --no-daemon :app:testDebugUnitTest :app:assembleDebug
```

产物：`mobile/android/app/build/outputs/apk/debug/app-debug.apk`。
版本号来自 `mobile/android/version.properties`（**唯一来源**，不要写进 build.gradle）。

---

## 2. 装到手机

```bash
adb devices                       # 确认设备状态是 device，不是 unauthorized
adb install -r mobile/android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.muyuchat.aether/.MainActivity
```

首屏是「概览」。点蓝卡上的 **⚡ 启动网关**：

- 底部出现 **常驻通知**（前台服务，Android 要求可见通知，属于合规设计）；
- 卡片显示监听地址，形如 `http://127.0.0.1:<端口>/v1`——**端口是系统分配的，每次启动可能不同**；
- 状态栏「运行中」，未启动时写「已停止」。

> 网关只绑回环。这不是配置没做完，是**运行时的硬约束**：
> 非回环地址在启动时会被拒绝（`listen_host_must_be_loopback`）。

---

## 3. 跑通第一条请求

`/v1` 需要**调用方 key**（与"管理会话凭据"是两把不同的东西）。

### 3.1 建一把 key

App 内：**更多 → 设置 → 拉到「调用方密钥」→ 新建密钥 → 创建**。

创建后明文**只显示这一次**（不写日志、不进剪贴板、不入普通设置文件），
本机只保存加密密文。请当场抄到你的密码管理器里，然后点「已抄写，关闭」。

### 3.2 从电脑打通（推荐）

```bash
# ① 把手机的网关端口映射到本机（<PORT> 换成 App 里显示的端口）
adb forward tcp:<PORT> tcp:<PORT>

# ② 用占位符代表你的 key —— 不要把它写进 shell 历史
read -rs AETHER_KEY          # 粘贴后回车，屏幕不回显
export AETHER_KEY

# ③ 列模型
curl -s http://127.0.0.1:<PORT>/v1/models \
  -H "Authorization: Bearer $AETHER_KEY" | head -c 400

# ④ 发一条最小对话
curl -s http://127.0.0.1:<PORT>/v1/chat/completions \
  -H "Authorization: Bearer $AETHER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"<上面列出的某个 model id>","messages":[{"role":"user","content":"回复 PONG"}],"max_tokens":16}'

# ⑤ 完事后撤掉端口映射
adb forward --remove tcp:<PORT>
```

**成功标志**：③ 返回模型列表；④ 返回 `choices[0].message.content`；
并且 App 的「用量」页今日请求数 +1，「概览」出现
「最近一次：刚刚 · <模型> · 成功」。

### 3.3 只在手机里打通（不想用电脑）

同一把 key 也可以直接给手机上的任意 HTTP 客户端用（例如 Termux 的 `curl`），
目标地址就是 App 显示的 `http://127.0.0.1:<PORT>/v1`。

---

## 4. 排障：三个最常见的错误码

| 错误码 | 意思 | 怎么办 |
|---|---|---|
| `listen_failed` | 端口被占用，网关没起来 | 换端口（设置 → 首选端口，**保存后下次启动生效**），或重启网关 |
| `caller_auth_required` | `/v1` 没收到有效 key | 请求头带 `Authorization: Bearer <key>`；确认这把 key **没被删/没被停用** |
| `upstream_failed` | 网关连不上游 | 看响应里的 `message`：常见是**该渠道需要访问外网 OAuth**（例如 Google 登录），手机网络不通就到不了上游 |

另外两个值得知道的：

- **`invalid_response`**：管理接口返回的不是预期 JSON（多半是版本不匹配）。
- **`no_provider_available`**：模型名没被任何渠道登记，响应里会列出可用模型，照着改 `model` 即可。

---

## 5. 明确做不到的事（不是没做，是做不了）

手机是独立沙箱：只能看到自己的数据，不能驱动 PC 的进程、窗口或文件。因此下面这些**只在桌面端提供**：

WSL 管理、PC 进程控制与启动器、桌面窗口操作、会话删除与终端、桌面 Agent 目录扫描、
Windows 客户端自动导入、`reveal`（打开所在位置）、准点后台调度、写桌面客户端配置、自定义图标写入。

App 内「更多 → 应用」有一整页逐条解释了**为什么**做不到。

另有一项**按设计不做**：请求正文查看——账本记录里根本没有正文字段，正文从不落盘。
要"看正文"必须先改隐私模型把正文存下来，那是产品决策，不是补一个页面。

---

## 6. 已知限制

- **x86_64 模拟器不可用**：Go runtime 在 `untrusted_app` 域触发 `SYS_LSTAT` 被 seccomp 杀死；
  arm64 真机不受影响。详见 `docs/internal/STAGE0_X86_64_BLOCKER.md`。
- **前台服务不等于"永不被杀"**：系统仍可能在省电策略下停止服务，App 不承诺常驻。
- **端口每次启动可能不同**：系统分配，脚本里请从 App 或 `/api/settings` 读取，不要写死。
- **`antigravity/*` 这类需要外网 OAuth 的渠道**：手机网络到不了对应 OAuth 端点时会 502，
  这是网络可达性问题，不是网关 bug。

---

## 7. 一次完整验证（可选）

想确认整条链路（含账本与界面）都是真的：

```bash
# 账本行数（前后各看一次，成功/失败请求都会 +1）
adb shell run-as com.muyuchat.aether sh -c 'wc -l no_backup/aether/usage/ledger/*.jsonl'
```

- 发请求前记一次、发请求后再记一次 → **行数应当增加**；
- 同时看 App「用量」页今日请求数 +1；
- 若只有账本变、界面没变，说明界面还没刷新（下拉或切页重进即可）。

**远程验证记录**（含非流式、SSE 流式、tool_calls、失败态）见
`docs/internal/E2E_REVERSE_PROXY.md`。

### 「读取额度」看不懂时怎么查

额度卡片上的「最近读数」有三种说法，含义各不相同：

| 屏幕显示 | 含义 |
|---|---|
| `最近读数 从未读过` | 这个账号**一次都没成功读过**额度（没点过、或上次读取失败了） |
| `最近读数 无（这次读取没返回额度）` | **点了、请求也成功了，但服务端这次没拿到额度**——常见原因是该渠道续期凭证需要访问外网 OAuth（例如 Google），手机网络不通时就会这样 |
| `最近读数 刚刚` / `N 分钟前` | 真的读到了，时间就是上次成功读取的时间 |

如果额度区还出现 `读取失败：<原文>`，那是**请求本身失败**，原文来自服务端，照着它排查（401/429 类通常是上游凭证问题）。

自查命令（日志只记录脱敏信息，不含账号 id 全量、URL 或凭据）：

```bash
adb logcat -c
# 然后在 App 里点一次「读取额度」
adb logcat -d -s AetherQuotaRead
```

三种结果分别对应：

| 日志 | 结论 |
|---|---|
| 只有 `start …` | 请求发出去了但没有任何回包/异常 → 真卡住，查网关是否在跑 |
| `start …` + `ok … readAt=无` | 回包正常但这次没读到额度 → **上游问题，不是 App 问题** |
| `start …` + `fail … code=… type=…` | 客户端侧失败，`code` 是 `network_error` / `status_5xx`，`type` 是异常类型名 |

## 8. 自定义 spec 渠道（自己写 JSON 加一个渠道）

手机网关和桌面一样会在启动时加载 **spec 渠道参数**：把 `*.json` 放进 spec 目录，就能在不重新编译 App 的前提下多出一个可路由的渠道（`base_url` / 端点路径 / 模型映射 / 请求头模板都可热改）。

**代码依据**（想复核就看这三处）：`internal/mobile/runtime.go:216`（`SpecDir: paths.SpecDir`）→ `internal/core/gateway/gateway.go:582-595`（`spec.Default().LoadDir(cfg.SpecDir)` + `provider.ReconcileSpecChannels()`）；目录不存在是**安静 no-op**（`internal/core/spec/spec.go:245-253`），不会报错、不会拖慢启动。

### 8.1 文件放哪

真机上 spec 目录是 **App 沙箱内的 `files/aether/cache/spec/`**：

```
/data/data/com.muyuchat.aether/files/aether/cache/spec/
```

（对应 `GatewayServiceController.kt:45` 传给 Go 的 `cacheDir = <filesDir>/aether/cache`，`apppaths` 再拼 `spec`。App 私有目录，未 root 的手机只能用 `adb` 或下文第 3 条的方式写入。）

```bash
# 建目录 + 看现有文件（debug 包可直接 run-as，不需要 root）
adb shell run-as com.muyuchat.aether mkdir -p files/aether/cache/spec
adb shell run-as com.muyuchat.aether ls -l files/aether/cache/spec

# 推一个 spec 进去
adb push my-channel.json /data/local/tmp/my-channel.json
adb shell run-as com.muyuchat.aether cp /data/local/tmp/my-channel.json files/aether/cache/spec/
```

另外两条不需要 adb 的路子：

1. **从备份包恢复**：备份包里的 `specs` 会被写回同一个目录并自动重载（`internal/core/gateway/backup_api.go:358-374`），入口在「设置 → 备份」；
2. 远程 spec（仅桌面/网关侧配置项 `spec_remote_url`，手机上默认未配置）。

### 8.2 文件格式（最小可用示例）

文件名随便取，**以 JSON 里的 `provider_id` 为准**（`internal/core/spec/spec.go:233-279`）。一条 spec 就是一个对象；也可以写成 `{"version":1,"specs":[...]}` 的聚合格式。

```json
{
  "provider_id": "my-relay",
  "spec_version": "1",
  "base_url": "https://relay.example.com",
  "models": {
    "my-relay/gpt-4o-mini": "gpt-4o-mini"
  },
  "endpoints": {
    "chat": "/v1/chat/completions"
  },
  "extra": {
    "reverse_profile": {
      "kind": "reverse",
      "display_name": "我的中转",
      "family": "openai",
      "description": "自建中转，OpenAI 兼容"
    }
  }
}
```

- `provider_id` 就是渠道 id（会出现在「接入渠道」列表里），**不能与内置渠道同名**（`internal/providers/reverse/spec_register.go:163-166` 会拒绝覆盖内置渠道）；
- `extra.reverse_profile.kind` **必须显式写 `"reverse"`**（`internal/providers/reverse/spec_register.go:115-117`），否则这条 spec 只改参数、不注册成渠道；
- `models` 的键是「对外名」（可带 `<渠道>/` 前缀），值是上游真实模型名——写了映射，这些模型才会出现在 `/v1/models`；
- spec 只声明**渠道与路由**；账号与凭据仍然在 App 的「接入渠道 → 我的中转 → 接入」里录入。

### 8.3 什么时候生效

| 做法 | 生效时机 |
|---|---|
| `POST /api/specs/reload`（带 admin session） | **立即**：重扫目录 + 重建路由表（`internal/core/gateway/diag.go:23-38`、路由 `gateway.go:1100`） |
| 重启 App（或让网关重启一次） | 下次启动加载（`gateway.go:582`） |
| 从备份包恢复 | 自动 reload（`backup_api.go:374`） |

### 8.4 排查

```bash
# 1) 网关到底认到几条 spec（含热更新状态）
curl -s -H "Authorization: Bearer <admin-session>" http://127.0.0.1:<PORT>/api/specs

# 2) 触发一次重载，看返回的 error（400/500 时原文就是原因）
curl -s -X POST -H "Authorization: Bearer <admin-session>" http://127.0.0.1:<PORT>/api/specs/reload

# 3) 网关日志（Go 侧）：加载失败的 WARN 在这里
adb shell run-as com.muyuchat.aether cat no_backup/aether/logs/aether.log | tail -n 50
```

常见原因，按日志顺序查：

| 现象 | 原因 |
|---|---|
| 日志有 `[WARN] spec 目录加载失败: …` | 目录里某个 `*.json` 读失败/解析失败，路径与错误原文都会打出来 |
| `/api/specs` 的 `status.count` 不涨 | 文件没放对目录（对照 8.1 的绝对路径），或文件名后缀不是 `.json` |
| count 涨了但渠道列表没有 | `extra.reverse_profile.kind` 不是 `"reverse"`，或 `provider_id` 与内置渠道同名（`internal/providers/reverse/spec_register.go:115-117` / `:163-166` 会拒绝注册） |
| 渠道有了但请求 404/502 | `base_url` / `endpoints.chat` 写错；反代路径要写到上游真正接受的那个路径 |
