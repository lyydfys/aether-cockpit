# 线路自动切换（Open Design / Cloudflare 边缘优选）

社区有两个针对 Open Design 线路的开源项目：

- [open-design-ip-optimizer](https://github.com/ft5191306/open-design-ip-optimizer)：
  PowerShell/Python 脚本，扫描 Cloudflare IP 段，用 `/cdn-cgi/trace` 找香港机房，
  再做完整 HTTPS 实测，把最优 IP 写进系统 hosts；另有一个常驻模式（3 秒采样当前
  线路、5 分钟一轮候选、快 150ms 就切）。
- [DSH-CFHKoptimizer](https://github.com/ft5191306/DSH-CFHKoptimizer)：同一套测量
  策略的 DSH 插件版，常驻测速并自动改写 hosts 的优选段。

两者都验证了同一件事：**把域名钉到更优的 Cloudflare 边缘 IP，能把这条链路从
"十次里四次超时"拉到稳定**。它们的代价是改写系统 hosts——需要管理员权限、影响
全系统、浏览器开 DoH 还会绕过。

本项目采用同样的测量模型，但**只作用于网关内的某个渠道**：不碰 hosts、不要管理员，
切换结果写进 `settings.json` 的 `edge_pins`，随时可清除。

## 与两个项目的对应关系

| 它们做的事 | 我们的实现 |
|---|---|
| 扫 CF 段找候选 | `defaultEdgeCandidates`：`162.159/16`、`104.26/16` 按 /24 步长 4 抽样 + 社区实测的 SJC 段 `104.17.128-159`，默认 96 个候选 |
| TCP 粗筛 | `probeEdgePath`：真实 SNI 握手 + 证书校验，任何一步失败即淘汰 |
| `/cdn-cgi/trace` 确认机房 | 支持，作为「纯边缘」口径 |
| 完整 HTTPS 实测（含回源） | **关键差异**：允许按渠道配置真实路径（如 `/v1/models`），排序用的是「边缘 + 回源」总耗时。社区实测 amr-link 的源站在美西，只看边缘会把 HKG 选成最优，真实请求反而比 SJC 慢 35%~42% |
| 淘汰「证书正常但拒绝服务」的 IP | `edgeBlockedResponse`：命中 `error code: 10xx` 或 CF 403 错误页即判不可用。这类 IP 响应往往比健康 IP 还快，只看耗时会把它排第一 |
| 阈值 150ms 才切 | `decideEdgeSwitch`：快出阈值才切；当前线路全挂（0 个成功样本）立即切；样本不足 2 个时只要更快就切 |
| 常驻采样 | `StartEdgeAuto`：默认 30 秒检查一次，每个域名按自己的 `interval_sec`（默认 300 秒）重扫 |
| 写 hosts | **不写**：结果写 `edge_pins`，由网关的拨号器在内部生效 |

## 判定规则（对齐社区经验）

1. **先淘汰再排序**：可用候选之间才比速度；`degraded`（有失败样本）一票否决。
2. **迟滞**：新线路要比当前快出阈值（默认 150ms）才切，避免两条相近线路来回跳。
3. **当前线路全挂立即切**，不等阈值。
4. **全失败时不改动现有固定表**：宁可用旧线路，也不要清空后随机漂。

## 使用

渠道详情 → 网络 → **自动切换**：

- 路径：留空 = `/cdn-cgi/trace`（纯边缘）；回源在远端的站点填真实 API 路径，
  例如 Open Design 填 `/v1/models`；
- 重扫间隔：默认 300 秒；阈值：默认 150ms；
- 开启后首轮在后台执行，页面会显示当前线路、实测中位数、可用候选数、切换次数与原因。

命令行/脚本可用：

```
GET  /api/network/edge-auto
POST /api/network/edge-auto {"host":"amr-link.open-design.ai","enabled":true,"path":"/v1/models"}
POST /api/network/edge-pin  {"host":"amr-link.open-design.ai","ips":[]}   # 一键还原
```

## 实测（本机，2026-11-07）

```
扫描 96 个候选 → 可用 38 个
首轮钉住 104.26.0.1 / 104.26.4.1，实测中位 240ms，切换 1 次
第二轮：当前 241ms、最优 242ms → "优势不足阈值，保持当前线路"
settings.json: edge_auto + edge_pins 均已落盘
```

## 局限

- **只在直连该域名时有效**：如果渠道配置了代理，网关拨号的对端是代理，IP 固定表
  不会生效；此时瓶颈在代理线路，应换代理或直连。
- **IP 会变**：运营商路由或机场线路变化后，自动切换会在下一轮重扫时修正，属于正常。
- **ping 不通是正常的**：这些边缘 IP 大多不回 ICMP，测的是 TCP/TLS/HTTP。
- **不能保证不封号**：线路优化只影响延迟与成功率，与上游风控是两件事。
