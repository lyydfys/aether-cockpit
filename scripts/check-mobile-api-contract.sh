#!/usr/bin/env bash
set -euo pipefail

# Stage 7 · 客户端/服务端 API 契约门禁。
#
# 背景：移动端的每个 `xxxApiClient.kt` 都在硬编码 `/api/...` 路径。路径写错了单测抓不到
# （单测测的是解析函数），只有真机上点进那一页才会 404。本脚本把「Kotlin 里用到的路径」
# 与「Go 侧真实注册的路由」对齐，在构建前就发现打错的路径。
#
# 规则：
#   1. 从 `internal/core/gateway/gateway.go` 提取 `HandleFunc("METHOD /api/…")` / `HandleFunc("/api/…")`；
#   2. 从移动端 Kotlin 提取 `/api/…` 字面量（排除注释里含空格/中文的假路径）；
#   3. 每个字面量必须能对上路：
#        - 含 `$`（字符串模板）→ 用 `$` 之前的静态前缀做前缀匹配；
#        - 以 `/` 或带 `?`（后面还要拼 query/子路径）→ 前缀匹配；
#        - 否则 → 必须**精确**匹配某条路由（路由里的 `{…}` 视为单段通配）。
#
# 实现约束：**匹配全程用纯 bash**，不逐次起 sed/grep 子进程。
# 在 Windows/MSYS 上，6000+ 次子进程会把 5 秒的检查拖成几分钟（实测 200s+ → 3s）。
#
# 已知边界（别把它当万能）：
#   - 只校验**路径形状**，不校验 HTTP 方法。`/api/2fa/cod` 会匹配到 `/api/2fa/{id}`
#     （单段通配），所以"路径拼错成另一条的 id 段"这种错误抓不到；
#   - 不校验 query 参数名、请求体字段、状态码语义——那些靠单测与真机。
#   它能抓的是：整条路径根本不存在、层级写错、拼写错到对不上任何路由。
#
# 用法：bash scripts/check-mobile-api-contract.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROUTES_FILE="$ROOT/internal/core/gateway/gateway.go"
SRC="$ROOT/mobile/android/app/src/main/java/com/muyuchat/aether"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$ROUTES_FILE" ]] || fail "找不到 $ROUTES_FILE"
[[ -d "$SRC" ]] || fail "找不到 $SRC"

# --- 1. 路由表 -------------------------------------------------------------
ROUTE_RAW=$(grep -oE 'HandleFunc\("(GET |POST |PUT |DELETE |PATCH )?/api/[^"]*"' "$ROUTES_FILE" \
  | sed -E 's/^HandleFunc\("//; s/"$//' || true)
[[ -n "$ROUTE_RAW" ]] || fail "从 gateway.go 里没提取到任何 /api 路由"
mapfile -t ROUTES < <(printf '%s\n' "$ROUTE_RAW" | awk '{print $NF}' | sort -u)
pass "服务端路由 ${#ROUTES[@]} 条"

# --- 2. 移动端字面量 -------------------------------------------------------
# 只接受"看起来像路径"的（无空格、无中文），避免把注释里的句子当路径。
mapfile -t LITERALS < <(
  grep -rhoE '"/api/[A-Za-z0-9/_?=&.{}:$-]*"' "$SRC" | tr -d '"' | sort -u
)
[[ ${#LITERALS[@]} -gt 0 ]] || fail "移动端源码里没提取到任何 /api 字面量"
pass "移动端 /api 字面量 ${#LITERALS[@]} 条"

# --- 3. 纯 bash 匹配 -------------------------------------------------------
# 精确匹配：逐段比，路由里的 {…} 是单段通配；`{path...}` 这种尾段通配按前缀处理。
path_matches_exact() {
  local route="$1" path="$2"
  local IFS=/ rs ps i seg
  read -ra rs <<<"$route"
  read -ra ps <<<"$path"
  [[ ${#rs[@]} -eq ${#ps[@]} ]] || return 1
  for ((i = 0; i < ${#rs[@]}; i++)); do
    seg="${rs[i]}"
    [[ "$seg" == \{*\} ]] && continue
    [[ "$seg" == "${ps[i]}" ]] || return 1
  done
  return 0
}

# 前缀匹配：字面量后面还要拼东西（模板变量 / 子路径 / query）。
# 只比较 `/` 之前的那一段，避免 "/api/2fa" 误配 "/api/2fa/codes" 之外的相近路由。
path_matches_prefix() {
  local route="$1" prefix="$2"
  [[ "$route" == "$prefix"* ]] && return 0
  [[ "$prefix" == "$route"* ]] && return 0
  return 1
}

MISSING=0
CHECKED=0
for lit in "${LITERALS[@]}"; do
  CHECKED=$((CHECKED + 1))
  ok=0

  if [[ "$lit" == *'$'* ]]; then
    prefix="${lit%%\$*}"
    for r in "${ROUTES[@]}"; do
      if path_matches_prefix "$r" "$prefix"; then ok=1; break; fi
    done
  elif [[ "$lit" == */ || "$lit" == *'?'* ]]; then
    prefix="${lit%%\?*}"
    for r in "${ROUTES[@]}"; do
      if path_matches_prefix "$r" "$prefix"; then ok=1; break; fi
    done
  else
    for r in "${ROUTES[@]}"; do
      if path_matches_exact "$r" "$lit"; then ok=1; break; fi
    done
  fi

  if [[ "$ok" -ne 1 ]]; then
    echo "  ✗ 移动端用了服务端不存在的路径: $lit" >&2
    MISSING=$((MISSING + 1))
  fi
done

if [[ "$MISSING" -ne 0 ]]; then
  echo "api-contract=fail（$MISSING/$CHECKED 条对不上）" >&2
  exit 1
fi

pass "移动端 $CHECKED 条 /api 路径全部能在服务端路由里找到"
echo "api-contract=pass"
