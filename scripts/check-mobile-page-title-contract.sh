#!/usr/bin/env bash
set -uo pipefail

# 页面标题契约门禁：每个 *Page.kt 的页面级标题必须走统一组件 DeepPageTitle，
# 且不得再用 typography.headlineMedium（28sp 大标题会把首屏吃掉）。
#
# 为什么是脚本而不是单测：
#   单测里做「跨文件源码字符串匹配」既会误伤（注释里提到关键词就红），
#   也会被「文件正被写一半」误伤，而且一旦红了会阻塞所有人的构建。
#   源码合同属于 lint 层：随时可跑、失败只影响 CI、不抢 Gradle 锁。
#
# 规则（先剥注释，再看真实代码）：
#   1. 每个 *Page.kt 必须出现 `DeepPageTitle(`；
#   2. 剥注释后不得出现 `typography.headlineMedium`。
#
# 用法：bash scripts/check-mobile-page-title-contract.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI_DIR="$ROOT/mobile/android/app/src/main/java/com/muyuchat/aether/ui"

if [[ ! -d "$UI_DIR" ]]; then
  echo "page-title-contract=fail"
  echo "  找不到 UI 源码目录：$UI_DIR" >&2
  exit 1
fi

# 剥掉 // 行注释与 /* ... */ 块注释（含跨行），并丢掉空行。
# Kotlin 文件用这个足够；字符串里出现 // 的极端情况不影响本契约的两条断言。
strip_comments() {
  awk '
    {
      line = $0
      while (1) {
        if (inblock) {
          i = index(line, "*/")
          if (i == 0) { line = ""; break }
          line = substr(line, i + 2); inblock = 0
        } else {
          i = index(line, "/*")
          if (i == 0) break
          pre = substr(line, 1, i - 1)
          rest = substr(line, i + 2)
          j = index(rest, "*/")
          if (j == 0) { line = pre; inblock = 1; break }
          line = pre substr(rest, j + 2)
        }
      }
      k = index(line, "//")
      if (k > 0) line = substr(line, 1, k - 1)
      print line
    }
    END { if (inblock) exit 0 }
  ' "$1" | sed '/^[[:space:]]*$/d'
}

shopt -s nullglob
files=()
for f in "$UI_DIR"/*Page.kt; do
  files+=("$f")
done
shopt -u nullglob

if [[ ${#files[@]} -eq 0 ]]; then
  echo "page-title-contract=fail"
  echo "  $UI_DIR 下没有 *Page.kt" >&2
  exit 1
fi

failures=0
declare -a problems=()

for f in "${files[@]}"; do
  name="$(basename "$f")"
  code="$(strip_comments "$f")"

  if ! grep -q 'DeepPageTitle(' <<<"$code"; then
    problems+=("$name: 页面级标题没有走 DeepPageTitle（禁止各页自己写 Text(style = headlineXxx)）")
    failures=$((failures + 1))
  fi

  if grep -q 'typography\.headlineMedium' <<<"$code"; then
    problems+=("$name: 仍在使用 typography.headlineMedium（改用 DeepPageTitle）")
    failures=$((failures + 1))
  fi
done

# ---------------------------------------------------------------------------
# --self-test：证明"注释里提到关键词"不会误伤，"代码里真用了"会被抓住。
# 用法：bash scripts/check-mobile-page-title-contract.sh --self-test
# ---------------------------------------------------------------------------
if [[ "${1:-}" == "--self-test" ]]; then
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  cat > "$tmpdir/CommentOnlyPage.kt" <<'KOTLIN'
package selftest
// 这里提到 typography.headlineMedium 只是文档说明，不是用法
/* 块注释也可以提到 typography.headlineMedium
   而且能跨行 */
@Composable
internal fun CommentOnlyPage() {
    DeepPageTitle(title = "demo")
}
KOTLIN

  cat > "$tmpdir/RealViolationPage.kt" <<'KOTLIN'
package selftest
@Composable
internal fun RealViolationPage() {
    Text(text = "x", style = MaterialTheme.typography.headlineMedium)
}
KOTLIN

  check_one() {
    local file="$1" want_deep="$2" want_headline="$3" label="$4"
    local code has_deep has_headline verdict="pass"
    code="$(strip_comments "$file")"
    grep -q 'DeepPageTitle(' <<<"$code" && has_deep=1 || has_deep=0
    grep -q 'typography\.headlineMedium' <<<"$code" && has_headline=1 || has_headline=0
    [[ "$has_deep" == "$want_deep" ]] || verdict="fail"
    [[ "$has_headline" == "$want_headline" ]] || verdict="fail"
    echo "  self-test[$label]: deep=$has_deep headline=$has_headline -> $verdict"
    [[ "$verdict" == "pass" ]]
  }

  echo "page-title-contract=self-test"
  ok=0
  # 注释里提到关键词 + 代码里用 DeepPageTitle → 必须判定为「合规」
  check_one "$tmpdir/CommentOnlyPage.kt" 1 0 "comment-only" || ok=1
  # 代码里真用 headlineMedium → 必须被抓住
  check_one "$tmpdir/RealViolationPage.kt" 0 1 "real-violation" || ok=1

  if [[ $ok -eq 0 ]]; then
    echo "page-title-contract=self-test-pass"
    exit 0
  fi
  echo "page-title-contract=self-test-fail" >&2
  exit 1
fi

if [[ $failures -gt 0 ]]; then
  echo "page-title-contract=fail"
  for p in "${problems[@]}"; do
    echo "  ✗ $p" >&2
  done
  echo "  共 ${#files[@]} 个页面文件，${failures} 处违约" >&2
  exit 1
fi

echo "page-title-contract=pass"
echo "  已检查 ${#files[@]} 个页面文件：全部走 DeepPageTitle，且没有 headlineMedium"
