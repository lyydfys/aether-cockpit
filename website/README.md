# Aether Cockpit 官方网站

独立设计的单页官网，静态资源、零构建，可直接丢到任意静态托管。

图标与色系**取自项目自身的权威定义**，不是另行设计的：
几何来自 [`internal/web/ui2/assets/aether-cockpit.svg`](../internal/web/ui2/assets/aether-cockpit.svg)，
配色来自 [`internal/web/ui2/css/tokens.css`](../internal/web/ui2/css/tokens.css)。

设计与取舍见 [DESIGN.md](./DESIGN.md)。

```
website/
├─ index.html                 单页结构（开屏 + 13 个分区）
├─ DESIGN.md                  设计说明：图标来源、色板依据、开屏叙事
├─ assets/
│  ├─ css/site.css            设计系统与全部样式
│  ├─ js/ignition.js          开屏动效（图标按自身构成长出来 + 飞入导航）
│  ├─ js/site.js              页面交互（标签页 / 路由演示 / 揭示 / 复制）
│  ├─ img/appicon.png         应用图标
│  ├─ img/ignition-storyboard.png  开屏分镜图（由脚本生成）
│  ├─ img/shots/*.png         工作台截图 + 4 张手机截图（取自 docs/images/screenshots）
│  └─ fonts/*.woff2           Inter + Noto Sans SC（含 OFL 许可文本）
└─ scripts/
   ├─ serve.py                本地预览（ThreadingHTTPServer）
   ├─ check-brand-static.py   静态校验：图标几何与权威 SVG 逐条比对
   ├─ check-timeline.mjs      开屏五层的出现顺序与收尾完整性
   ├─ check-theme.mjs         黑白双主题 + 首屏双端 + 平台开关
   ├─ check-glass.mjs         柔光玻璃主题（渐变底/blur/高光/光点）
   ├─ check-responsive.mjs    三主题 × 四尺寸：标题横排与横向溢出
   ├─ check-default.mjs       默认主题、存档覆盖、系统深色不干扰
   ├─ check-mobile.mjs        六台真机视口：溢出/触控尺寸/字号
   ├─ check-ignition-text.mjs 开屏文字对比度（WCAG AA）
   ├─ check-assets.mjs        图片/资源无损坏、无运行期错误
   ├─ check-subpath.mjs        子路径部署（GitHub Pages 前缀）验证
   ├─ check-legibility.py     小尺寸下青点/路由标是否仍可辨
   ├─ make_storyboard.py      生成开屏分镜图
   ├─ test-interactions.mjs   交互回归（标签页/路由/复制/FAQ/跳过/降级）
   └─ test-file-protocol.mjs  file:// 直开冒烟检查
```

## 本地预览

```powershell
cd website
python scripts/serve.py 8125
# 打开 http://127.0.0.1:8125/index.html
```

`file://` 直接双击打开同样可用（字体与截图已验）。

## 图标

**全站统一使用完整图标**（浅色底板 + 气泡 + 星芒 + 4 青点 + 路由标），不使用简化版：

| 层 | 元素 | 值 |
|---|---|---|
| 0 | 浅色底板，108×108 `rx=20` | `#F8F9FA` |
| 1 | 蓝色对话气泡，尾巴朝左下 | `#1A73E8` |
| 2 | 白色四角星芒 | `#FFFFFF` |
| 3 | 四个青点 | `#CCFFFF`×2、`#80FFFF`×2 |
| 4 | 右下路由徽标 | 白盘 + `#13A99B` 描边 + `#1A73E8` 连线 + 三节点 |

底板是青点与路由标可辨的**前提**，不是装饰：青点在 108 坐标系里半径只有 3.5/2.5/2.0，
缩到 36px 约 1.2–2.3px，深底上会糊掉。实测 36px 下青点仍有 15/10 px、路由盘 6 px，
28px 是下限。数字见 `check-legibility.py`。

## 首屏与双主题

首屏是「桌面一台 + 手机四张」的展台，不是一句"双平台支持"：

- 桌面版占右栏主位，用真实概览截图装进笔记本外框；
- 手机版四张（概览 / 设置 / 额度 / 更多）横跨整行并排，每张约 **281px 宽**
  （最初那版只有 168px）；
- 上方有 **平台开关**：切到「手机版」时网格重排，四张手机升为主角（281×554），
  桌面缩成淡出小窗——构图真的变了，不只是变透明。

配色提供**黑 / 白两套**，都对齐 App 的 `tokens.css`：

| | 黑 | 白 | 柔光玻璃（默认） |
|---|---|---|---|
| 背景 | `#0f1115` 实色 | `#f7f8fa` 实色 | `#eef2fa` + 三段淡彩渐变 |
| 卡片 | `#1b1f26` | `#ffffff` | 白色半透明 + `backdrop-filter` |
| 正文 | `#e8eaee` | `#17181b` | `#17181b` |
| 强调 | `#4a9bf5` | `#1a73e8` | `#1a73e8` |
| 边缘 | 1px 描边 | 1px 描边 | 上缘实白 + 下缘冷灰收边 |
| 反转区 | 米白 | 近黑 | 深色玻璃 |
| 交互 | — | — | 光点跟随 / 按下发亮 |

柔光玻璃（浅色）参考 Xiaomi HyperOS 4 与 iOS Liquid Glass 的公开描述，
实现要点见 [DESIGN.md](./DESIGN.md#55-柔光玻璃第三套主题)。

- **默认是柔光玻璃**；切换按钮在导航右侧（玻璃 / 白 / 黑），选择写入 `localStorage`；
- 不跟随系统深浅偏好：访客系统是深色时也不会被换成纯黑；
- 首帧不闪：`<head>` 内联脚本在 `body` 渲染前定主题；
- 图标浅色底板**不随主题变**（`#f8f9fa` 是品牌固定值，否则青点失去对比度）。

## 资源与性能

按国内常见移动网络（1.6 Mbps / 150 ms RTT）实测：

| | 优化前 | 优化后 |
|---|---|---|
| 传输总量 | 3130 KB | **643 KB** |
| load | 15.9 s | **3.7 s** |
| 图片 | 1395 KB | **286 KB** |
| 字体 | 1592 KB | **215 KB** |

两处优化：

1. **截图转 WebP（q85）** —— 2.05 MB → 0.51 MB。
   截图无透明通道，用 RGB 即可；肉眼看不出差别。
2. **中文字体子集化** —— 从页面实际用到的 627 个字符重新打包，
   三档字重各 525–535 KB → 71–72 KB。
   新增字符时必须重新跑子集，否则会掉字（见下）。

```bash
# 重新生成字体子集（改了文案之后必须跑）
python -m fontTools.subset assets/fonts/noto-sans-sc-400.woff2 \
  --text-file=_charset.txt --flavor=woff2 \
  --output-file=assets/fonts/noto-sans-sc-400.subset.woff2
```

> 注意：`assets/fonts/*.woff2`（完整字体）保留在仓库里作为子集源，
> 部署时属于冗余，可只上传 `*.subset.woff2`。

## 移动浏览器适配

断点：**1024 / 900 / 760 / 620 / 420 / 360**。

| 断点 | 变化 |
|---|---|
| 900 | 导航链接收进抽屉，出现汉堡；触控目标提到 ≥44px |
| 760 | 导航高度 58px；首屏改单列；网格单列化 |
| 620 | 主题开关收成单按钮轮换（三格占 120px 会挤掉下载按钮） |
| 420 | 手机截图两列；影墙单列 |
| 360 | 品牌名隐藏，导航四件套在 320px 内放得下 |

### 小屏导航

900px 以下原来的做法是 `display:none` 直接隐藏六个导航链接——
等于小屏上这些入口根本不存在。现在改为：

- 汉堡按钮（44×44，展开时变 X）
- 抽屉卡片挂在导航下方，抽屉底几乎不透明（`--drawer-bg`），
  避免下面的大标题透上来与菜单文字打架
- 遮罩从导航条下方开始，**不盖住关闭按钮**
- 点链接 / 点遮罩 / 按 Esc / 放大到桌面宽度，都会自动收起

### 已验证的真机视口

`check-mobile.mjs` 用 Playwright 的设备描述符跑六台：

iPhone SE(320) · iPhone 13(390) · iPhone 15 Pro Max(430) ·
Pixel 7(412) · Galaxy S9+(320) · iPad Mini(768)

断言三件事：无横向溢出、触控目标 ≥36px、无运行期错误。

实测结果：六台全部通过，小目标数为 0。

> 只有 11–11.5px 的小字（手机截图里的内嵌文字、平台副标）低于 12px，
> 这些是截图内容或次要标注，不承担主要阅读，保留原样。

## 开屏动效（Aether Ignition）

叙事是「图标按它自己的构成顺序长出来，再飞进导航栏」，约 3 秒进正题：

| 时间 | 画面 | 对应层 |
|---|---|---|
| 0 – 260ms | 粒子沿图标外缘汇入 | — |
| 260ms | 浅色底板铺开，图标落在屏幕上 | 0 |
| 500ms | 蓝色气泡弹性成形 | 1 |
| 860ms | 白色星芒旋入落定 | 2 |
| 1160ms | 四个青点依次点亮（带放大回弹） | 3 |
| 1440ms | 白盘 → 连线 → 三节点 | 4 |
| 1900ms | 字标逐字上浮 | — |
| 2480ms | 图标收缩飞入导航槽位 | — |
| 3020ms | 遮罩揭开 | — |

- **字标跟随主题**：用 `--fg` 而不是固定的白色——
  白色在浅色/玻璃主题下会白字压浅底（实测对比度仅 1.06:1）；
- **副标可读性**：13.5–15px、`--fg-2`、字距 0.14em，
  对比度从 3.1 提到 9.8（WCAG AA 要求小字 ≥4.5）；
- **可跳过**：点遮罩空白、按 `Esc`、点 `Skip / 跳过`。
- **降级**：`prefers-reduced-motion` 时间轴压到 30%。
- **无 JS**：`<noscript>` 直接隐藏开屏。

## 验收

```powershell
cd website
python scripts/check-brand-static.py     # 图标/配色是否仍与项目一致
python scripts/check-legibility.py       # 36/28px 下细节是否还看得见
python scripts/serve.py 8125
node scripts/check-timeline.mjs http://127.0.0.1:8125/index.html   # 五层顺序
node scripts/check-theme.mjs    http://127.0.0.1:8125/index.html   # 双主题 + 双端
node scripts/check-glass.mjs    http://127.0.0.1:8125/index.html   # 柔光玻璃主题
node scripts/check-responsive.mjs http://127.0.0.1:8125/index.html # 三主题 × 四尺寸
node scripts/check-default.mjs  http://127.0.0.1:8125/index.html # 默认主题验证
node scripts/check-mobile.mjs   http://127.0.0.1:8125/index.html # 真机视口验证
node scripts/check-ignition-text.mjs http://127.0.0.1:8125/index.html # 开屏文字对比度
node scripts/check-assets.mjs   http://127.0.0.1:8125/index.html # 资源完整性
node scripts/check-subpath.mjs                                   # 子路径部署
node scripts/test-interactions.mjs http://127.0.0.1:8125/index.html
python scripts/make_storyboard.py        # 开屏改动后重生成分镜图
```

`check-brand-static.py` 不依赖浏览器，比对 path 坐标、青点位置、徽标半径与色值，
是防止图标再次画错的回归闸口。

> 截图脚本若遇到 `page.goto timeout`，多半是残留 Chrome 进程或预览服务卡住：
> 关掉残留浏览器、换一个端口重启 `serve.py` 即可。

## 改内容时注意

- 对外文案以 `README.md` / `docs/` 为准；改站内数字（协议数、渠道数、端口、文件命名）时同步核对。
- 截图取自隔离演示配置，页面已标注「演示数据」，替换截图时保留这句。
- **改图标前先看 `aether-cockpit.svg`**，不要凭印象重画；本站曾因臆造图形返工一次。
- 新增分区请复用 `.section` + `.eyebrow` + `.h2` + `.lede`，并加 `data-reveal`。
