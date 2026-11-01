# 前端架构（ui2）

> 现状：**自有实现，无构建步骤**。Go 用 `//go:embed ui2` 把 `internal/web/ui2` 嵌进二进制，浏览器端是原生 ES module。
> 本文描述 2026-10-30 之后的真实结构。旧的 magpie 改造版（`internal/web/dist`）与 Vue 版已在 2026-10-31 删除，仓库只保留 ui2。

## 目录

```
internal/web/ui2/
├── index.html              入口：1 个合并 CSS（/css/app.css）、1 个 module、单一 #status 播报点
├── css/
│   ├── tokens.css          唯一色值 / 字号 / 圆角 / 阴影 / 动效来源（含 Inter @font-face）
│   ├── base.css            重置、排版、焦点环、滚动条、自绘复选框
│   ├── components.css      外壳、按钮、输入、卡片、浮层、命令面板、响应式
│   └── views.css           各页栅格、热力图、趋势图、设置页、HUD
├── js/
│   ├── app.js              注册路由（4 个同步 + 13 个按需 import）、启动外壳
│   ├── router.js           hash 路由；支持视图对象或加载函数，带竞态令牌
│   ├── shell.js            左轨、页头、工具条（只建一次）、命令面板、快捷键
│   ├── cmdk.js             ⌘K 面板：listbox 语义 + 焦点陷阱
│   ├── trend.js            趋势图：堆叠柱/面积切换、底部图例单选、双轴、十字线与键盘游标
│   ├── components.js       chip/btn/card/meter/toast/menu/drawer/modal/trapFocus
│   ├── dom.js              h() 构造器与 svg()
│   ├── icons.js            39 枚自绘图标（20 视框 / 1.5 描边 / non-scaling-stroke）
│   ├── brandicons.js       41 个渠道品牌 SVG 的映射
│   ├── api.js              同源 fetch，并发合并 + 1s 缓存
│   ├── store.js / format.js / win.js
│   └── views/              17 个页面
└── assets/
    ├── icons/              渠道品牌 SVG（41，已 svgo 降精度 120KB→33KB）
    └── fonts/              inter-latin.woff2（可变字重 400–700，OFL）
                            noto-sans-sc-{400,500,600}.woff2（中文子集，合计 1.6MB，OFL）
                            由 scripts/build-font.py 生成，改了中文文案要重跑

frontend/                   6KB 占位（skip-build.js + 空 package.json），只为 wails 前置命令存在
```

## 设计令牌

`css/tokens.css` 是**唯一色值来源**，其它 CSS 只允许 `var(--…)`（热力图色阶是唯一例外）。

- 字号：12 / 14 / 16 / 18 / 20 / 24 / 32 / 40。11px 只留给装饰（kbd、图例、日历月份），正文 14，
  卡片标题 18，页面标题 24，KPI 主数据 40。
- 圆角：8（小徽章/图标钮）/ 10（控件）/ 12（内嵌块）/ 16（卡片）/ 24（浮层）；胶囊只给 chip 与分段条。
- 海拔：卡片用 1px 描边，浮层才用 `--shadow-2`。
- 灰阶 5 档：`--fg` / `--fg-2` / `--muted` / `--faint` / `--disabled`；`--disabled` 禁止用于正文。
- 主题：`data-theme="auto|light|dark"`，写在 `<html>` 上；`auto` 走 `prefers-color-scheme`。
- 动效：120 / 180 / 240ms + 三条曲线；`prefers-reduced-motion` 下全部归零。

改色只改 tokens.css；改完跑一遍对比度（正文 ≥ 4.5:1）。

## 开发

ui2 是 `//go:embed` 的，**改文件不会自动生效**。两种方式：

```bash
# A. 带 API 代理的静态服务（推荐，改完刷新即可）
bin/cockpit.exe -addr 127.0.0.1:7999     # 终端 1：起网关
node scripts/ui-dev.mjs                  # 终端 2：http://127.0.0.1:7998

# B. 重编译（验证最终产物）
go build -o bin/cockpit.exe ./cmd/cockpit
```

`scripts/ui-dev.mjs` 会把 `/api`、`/healthz`、`/readyz`、`/version` 代理到 7999，其余从磁盘读；它也会像 Go 端一样把四份 CSS 拼成 `/css/app.css`。

## 构建

```bash
go build -o bin/cockpit.exe ./cmd/cockpit      # 嵌入 ui2
go test ./internal/web/                        # 静态资源与安全头
```

`wails.json` 的 `assetdir` 指向 `internal/web/ui2`；`frontend/` 只剩 `skip-build.js` 与一个空 package.json，
用于满足 wails 的 install/build 前置命令（已用 `wails build` 实测）。

## 验证

```bash
npm run ui:lint     # 色值只允许出现在 tokens.css
npm run ui:audit    # 16 条路由 × 3 宽度 + 暗色：对比度 / 溢出 / 命名 / live region / 焦点
npm run ui:axe      # axe-core：22 个页面状态的 WCAG 2.1 AA
npm run ui:visual   # 像素回归（ui:visual:update 重建基线）
npm run ui-check    # 冒烟：外壳/概览/账号/用量/网关/设置/会话/库
npm run ui:profile  # 瓶颈画像：Layout/Style 耗时、CSS 命中率、LCP 元素
npm run ui:layout   # 排版体检：横向溢出 / 文本重叠 / 非省略式裁切
npm run build:desktop   # 桌面端：wails build + 同步到 bin/ + 标记校验
npm run ui:all      # 三条串跑
node --test scripts/ui-unit.mjs                   # 纯函数单测
```

组件与令牌的说明书在 `docs/design/components.html`——它直接 link 本目录的四份 CSS，改产品即改文档。

冒烟脚本会点真实接口，部分用例依赖后端能力（2FA、各提供方 OAuth），接口缺失时会失败而不是误报通过。

## 国际化

- 语言包：`js/locales/en.js`（中文原文 → 英文）；`js/i18n.js` 负责查找、规则与切换。
- 翻译时机：`dom.js` 的 `h()/append()` 构建时同步翻译（含 title/aria-label/placeholder/alt），首帧即目标语言；运行时改文案用 `setText()`。
- 新增文案：直接在 `en.js` 加一条「中文原文: English」即可，不需要改视图；计数类文案加一条 `RULES` 正则。
- 切换：页面头「中 / EN」，持久化后重载一次（语言是低频操作，重载保证导航等启动期文案一致）。
- 现状：全部页面界面文案已英文化，仅账号名/路径/错误码等数据保留原文。

## 约定

1. **动作层级**：一行最多一个实心主按钮，其余为描边或纯文字；低频动作进「更多」菜单。
2. **密集列表用 `.row2`**：标题一行、元信息一行、右侧固定列；不要在一行里塞五段。
2. **筛选只更新状态**：工具条只构建一次，`setFilter()` 不重建 DOM——否则输入框会丢焦点。
3. **视图入场只在路由切换时播放**（`.view--enter`），筛选不重播。
4. **表单控件必须有名字**：`aria-label` 或 `<label>`；自绘控件保留原生 `input`。
5. **标题结构**：每页一个 `h1`；卡片标题 `role="heading" aria-level="2"`；卡片内实体名 `aria-level="3"`。
6. **数字用等宽数字**：`font-variant-numeric: tabular-nums`（`.num`）。
7. **文案说后果**：删除/轮换类确认要写清影响范围，不要只写「确定吗」。

## 快照

概览页会把上次成功加载的数据存进 `localStorage`（`aether.overview.snapshot.v1`，24h TTL），
二次进入先渲染快照再静默刷新；页面右上角的状态行会写明 `快照 HH:MM · 正在刷新…` 或 `更新于 HH:MM`。
刷新失败时保留快照并提示，不会白屏。

## 已知缺口

- 图表只有原生 `title`，没有跟随鼠标的十字线与 tooltip；热力图不能点某天看明细。
- 像素回归只覆盖 3 张确定性画面（组件文档浅/深、命令面板），数据页仍是人工看截图。
- 组件文档（`components.html`）是手工维护的展示页，新增组件时要同步补一节。
