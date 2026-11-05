#!/usr/bin/env bash
set -euo pipefail

# Stage 7 · 设计系统契约门禁：把「MCA 的主题/导航/组件/图标不得另起一套」变成可执行断言。
#
# 这条规则写在做题须知里很久了，但它一直是「靠自觉」。人一多、卡一多，
# 迟早有人为了快一点在本仓再写一个 ColorScheme 或 Typography。这个脚本就是那道闸。
#
# 检查项：
#   1. 不得自建主题：lightColorScheme/darkColorScheme/Typography/materialTheme{
#   2. 不得硬编码颜色字面量：Color(0x…)
#   3. 不得硬编码字号：fontSize = N.sp / TextStyle(fontSize = …)
#      （`letterSpacing = N.sp` 属于例外：它不是字阶，见下方 ALLOW 规则）
#   4. 不得直接用 Material 图标集：Icons.Default/Filled/Outlined/Rounded
#   5. 必须依赖固定的 MCA Design System AAR
#
# 用法：bash scripts/check-mobile-theme-contract.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/mobile/android/app/src/main/java/com/muyuchat/aether"
BUILD="$ROOT/mobile/android/app/build.gradle.kts"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -d "$SRC" ]] || fail "找不到源码目录: $SRC"
[[ -f "$BUILD" ]] || fail "找不到 $BUILD"

# 收集要扫描的 kt 文件（不含 androidTest：测试可以有自己的断言代码）。
mapfile -t FILES < <(find "$SRC" -name '*.kt' -type f | sort)
[[ ${#FILES[@]} -gt 0 ]] || fail "$SRC 下没有 .kt 文件"

violations() {
  # $1 = 描述；其余 = grep -E 模式。命中则打印文件:行号:内容 并返回 1。
  local label="$1"; shift
  local hits
  hits=$(grep -nHE "$@" "${FILES[@]}" 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    echo "  ✗ $label" >&2
    printf '%s\n' "$hits" | sed 's/^/    /' >&2
    return 1
  fi
  return 0
}

FAILED=0
echo "扫描 ${#FILES[@]} 个 Kotlin 文件…"

# 1. 自建主题
violations "出现自建主题（应用 McaTheme，不得自造 ColorScheme/Typography）" \
  'lightColorScheme\(|darkColorScheme\(|Typography\(|[^a-zA-Z]materialTheme[[:space:]]*\{' || FAILED=1

# 2. 硬编码颜色字面量
violations "出现硬编码颜色字面量（应用 MaterialTheme.colorScheme / Mca*）" \
  'Color\(0[xX][0-9a-fA-F]+\)' || FAILED=1

# 3. 硬编码字号（letterSpacing 例外：它是字距而不是字阶，且只在验证码展示用到）
FONT_HITS=$(grep -nHE 'fontSize[[:space:]]*=[[:space:]]*[0-9]+(\.[0-9]+)?\.sp' "${FILES[@]}" 2>/dev/null || true)
if [[ -n "$FONT_HITS" ]]; then
  echo "  ✗ 出现硬编码正文字号（应用 MaterialTheme.typography）" >&2
  printf '%s\n' "$FONT_HITS" | sed 's/^/    /' >&2
  FAILED=1
fi
LETTER_HITS=$(grep -nHE 'letterSpacing[[:space:]]*=[[:space:]]*[0-9]+(\.[0-9]+)?\.sp' "${FILES[@]}" 2>/dev/null || true)
if [[ -n "$LETTER_HITS" ]]; then
  # 允许，但必须打印出来：让评审看到"确实只有字距用了字面值"。
  echo "  · letterSpacing 字面值（允许，字距不属于字阶）:"
  printf '%s\n' "$LETTER_HITS" | sed 's/^/    /'
fi

# 4. Material 图标集
violations "直接使用 Material 图标集（图标应由 McaIcons / 既有 drawable 提供）" \
  'Icons\.(Default|Filled|Outlined|Rounded)\.' || FAILED=1

# 5. 设计系统依赖
grep -q 'implementation(libs.mca.designsystem)' "$BUILD" \
  || fail "app/build.gradle.kts 没有依赖固定的 MCA Design System AAR（libs.mca.designsystem）"
pass "依赖固定的 MCA Design System AAR"

# 6. 版本固定：lock 文件必须存在且声明 4 个 sha256（与 Stage 0 门禁互补）。
#    真实键名是 `<artifact>Sha256=`（aar/pom/module/sources），不是 `.sha256=`。
LOCK="$ROOT/mobile/android/vendor/mca-designsystem.lock"
[[ -f "$LOCK" ]] || fail "缺少 vendor/mca-designsystem.lock"
SHA_COUNT=$(grep -cE '^[a-zA-Z]+Sha256=[0-9a-f]{64}$' "$LOCK" || true)
[[ "$SHA_COUNT" -ge 4 ]] || fail "lock 文件里的 sha256 条目只有 $SHA_COUNT 个（期望 ≥4）"
pass "lock 文件存在且含 $SHA_COUNT 个 sha256"

# 7. 全仓禁止出现外部项目名称硬锁
if bash "$ROOT/scripts/check-no-external-project-names.sh" >/dev/null 2>&1; then
  pass "全仓代码与文件零外部项目名称引用"
else
  bash "$ROOT/scripts/check-no-external-project-names.sh" >&2 || true
  FAILED=1
fi

if [[ "$FAILED" -ne 0 ]]; then
  echo "theme-contract=fail" >&2
  exit 1
fi

pass "没有自建主题 / 硬编码颜色 / 硬编码字号 / Material 图标集"
echo "theme-contract=pass"
