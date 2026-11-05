#!/usr/bin/env bash
# scripts/check-no-external-project-names.sh
#
# Hard-lock gate enforcing zero external / competitor / reference project names
# anywhere in repository filenames, comments, documentation, or source code.
#
# Usage:
#   bash scripts/check-no-external-project-names.sh
#   bash scripts/check-no-external-project-names.sh --self-test

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Build forbidden tokens from split segments so this script itself contains zero literal matches.
T_DSH1="dsh""-go"
T_DSH2="dsh""go"
T_DSH_AUTH="ccch1m""neyyy"
T_MAG="mag""pie"
T_MAG_AUTH="yet""one"
T_SUB="sub2""api"
T_SUB_AUTH="wei-""shaw"
T_CT1="cockpit""-tools"
T_CT2="cockpit""_tools"
T_CT3="cockpit""tools"
T_CT4="Cockpit"" Tools"
T_CC1="cc-""switch"
T_CC2="cc_""switch"
T_CC3="cc""switch"
T_CC4="CC ""Switch"
T_CP1="cli""proxy""api"
T_CP2="cli""proxy"
T_NA="new-""api"
T_OA="one-""api"
T_VEL="velo""era"
T_AGY="opencode-""agy-""auth"
T_CLW="cle""wd"
T_LITE="lite""llm"
T_PROMPT="prompt""foo"
T_AGW="agent""gateway"
T_HUB="one-""hub"
T_SUP="super-""api"
T_CN="chat""ni""o"
T_CHERRY="cherry"" studio"
T_NEXT="next""chat"
T_LOBE="lobe-""chat"
T_OPENWUI="open-""webui"
T_LIBRE="libre""chat"

PATTERN="(${T_DSH1}|${T_DSH2}|${T_DSH_AUTH}|${T_MAG}|${T_MAG_AUTH}|${T_SUB}|${T_SUB_AUTH}|${T_CT1}|${T_CT2}|${T_CT3}|${T_CT4}|${T_CC1}|${T_CC2}|${T_CC3}|${T_CC4}|${T_CP1}|${T_CP2}|${T_NA}|${T_OA}|${T_VEL}|${T_AGY}|${T_LITE}|${T_PROMPT}|${T_AGW}|${T_HUB}|${T_SUP}|${T_CN}|${T_CHERRY}|${T_NEXT}|${T_LOBE}|${T_OPENWUI}|${T_LIBRE}|\\b${T_CLW}\\b)"

run_scan() {
  local target_dir="$1"
  local failed=0
  local in_repo=0
  if git -C "$target_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    in_repo=1
  fi

  # 1. Check file & directory paths
  #
  # 在 git 仓库里只查**受版本控制**的路径：硬锁要守的是"仓库里有什么"，而不是
  # 开发者本机的临时目录（.smoke-data/、build-verify/、抓包脚本…）。那些地方
  # 出现参考项目的名字是正常的，算作违规只会让锁被无视。
  local path_hits
  if [[ "$in_repo" == "1" ]]; then
    path_hits="$(git -C "$target_dir" ls-files | grep -E -i "$PATTERN" || true)"
  else
    path_hits="$(find "$target_dir" \
      -path "$target_dir/.git" -prune -o \
      -path "*/node_modules" -prune -o \
      -path "*/.gradle" -prune -o \
      -path "*/build" -prune -o \
      -path "$target_dir/bin" -prune -o \
      -print | grep -E -i "$PATTERN" || true)"
  fi

  if [[ -n "$path_hits" ]]; then
    echo "FAIL [no-external-project-names]: forbidden project name found in file/directory path:" >&2
    echo "$path_hits" >&2
    failed=1
  fi

  # 2. Check file contents across all text files
  #
  # 署名文件豁免：NOTICE / LICENSE / README / CHANGELOG 必须能承载上游项目的版权与
  # 许可声明（MIT、LGPL-3.0 都要求保留归属）。硬锁要的是**产品代码、脚本、配置**
  # 零外部项目名，而不是抹掉法律要求的致谢——所以这些文件只做路径检查、不做内容检查。
  # 反过来：任何 .go/.kt/.js/.css/.yml… 里出现禁词仍然会失败，不受影响。
  local content_hits
  if [[ "$in_repo" == "1" ]]; then
    # git grep 只搜受版本控制的文件，正好符合上面的口径。
    content_hits="$(git -C "$target_dir" grep -I -n -E -i "$PATTERN" -- . || true)"
  else
    content_hits="$(grep -R -I -n -E -i \
      --exclude-dir=.git \
      --exclude-dir=node_modules \
      --exclude-dir=.gradle \
      --exclude-dir=build \
      --exclude-dir=bin \
      "$PATTERN" "$target_dir" || true)"
  fi

  local filtered_hits="" line
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    case "$line" in
      */NOTICE:* | */NOTICE.md:* | */LICENSE:* | */LICENSE.md:* | \
        */CHANGELOG.md:* | */README.md:* | */README.en.md:*) continue ;;
      NOTICE:* | NOTICE.md:* | LICENSE:* | LICENSE.md:* | \
        CHANGELOG.md:* | README.md:* | README.en.md:*) continue ;;
    esac
    filtered_hits+="$line"$'\n'
  done <<< "$content_hits"

  if [[ -n "${filtered_hits//[$'\n']/}" ]]; then
    echo "FAIL [no-external-project-names]: forbidden project name found in file contents:" >&2
    printf '%s' "$filtered_hits" >&2
    failed=1
  fi

  return "$failed"
}

if [[ "${1:-}" == "--self-test" ]]; then
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT

  # (a) 产品代码里出现禁词必须被拒
  echo "// reference to ${T_DSH1}" > "$TMP_DIR/sample.kt"
  if run_scan "$TMP_DIR" >/dev/null 2>&1; then
    echo "FAIL: --self-test expected scanner to reject forbidden project token" >&2
    exit 1
  fi

  # (b) 署名文件里出现同样的禁词必须被放行（否则无法保留 MIT / LGPL 要求的致谢）
  rm -f "$TMP_DIR/sample.kt"
  echo "reference to ${T_DSH1}" > "$TMP_DIR/NOTICE"
  if ! run_scan "$TMP_DIR" >/dev/null 2>&1; then
    echo "FAIL: --self-test expected attribution files (NOTICE) to be exempt" >&2
    exit 1
  fi

  # (c) 豁免只针对署名文件，README 旁边的普通源码不受豁免
  echo "// ${T_DSH1}" > "$TMP_DIR/sample.go"
  if run_scan "$TMP_DIR" >/dev/null 2>&1; then
    echo "FAIL: --self-test expected non-attribution source next to NOTICE to be rejected" >&2
    exit 1
  fi

  echo "[self-test] PASS: rejects source hits, exempts attribution files"
  exit 0
fi

if ! run_scan "$ROOT"; then
  echo "no-external-project-names=fail" >&2
  exit 1
fi

echo "no-external-project-names=pass"
