#!/usr/bin/env bash
set -euo pipefail

# Stage 7 · 发布门禁第二步：**产物内容**断言（ABI / 桥接库 / 调试符号 / 体积）。
#
# 与 check-mobile-release.sh 的分工：
#   - 那个脚本回答"这份 APK 的身份对不对"（包名/版本/SDK/不可调试）；
#   - 这个脚本回答"这份 APK 里装了什么"（哪个 ABI、桥接库在不在、有没有调试符号、多大）。
#
# 用法：
#   bash scripts/check-mobile-apk-contents.sh            # 构建 release 再检查
#   bash scripts/check-mobile-apk-contents.sh --no-build # 只检查已有 APK
#   bash scripts/check-mobile-apk-contents.sh --apk <path>
#
# 期望 ABI 默认 arm64-v8a；用 `-PaetherAbis=arm64-v8a,x86_64` 构建时通过
# `EXPECTED_ABIS=arm64-v8a,x86_64` 显式声明，否则脚本会如实判定为异常。

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$ROOT/mobile/android"
DEFAULT_APK="$APP_DIR/app/build/outputs/apk/release/app-release-unsigned.apk"

APK=""
DO_BUILD=1
EXPECTED_ABIS="${EXPECTED_ABIS:-arm64-v8a}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apk) APK="${2:-}"; [[ -n "$APK" ]] || { echo "usage: --apk <path>" >&2; exit 2; }; DO_BUILD=0; shift 2 ;;
    --no-build) DO_BUILD=0; shift ;;
    -h|--help) sed -n '2,18p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
note() { echo "NOTE: $*"; }

command -v unzip >/dev/null 2>&1 || fail "需要 unzip"

if [[ "$DO_BUILD" == "1" ]]; then
  [[ -n "${JAVA_HOME:-}" && -d "${JAVA_HOME}" ]] || fail "JAVA_HOME 未设置或不存在"
  "$JAVA_HOME/bin/java" -version 2>&1 | head -n 1 | grep -q '"21' || fail "需要 JDK 21"
  [[ -n "${ANDROID_HOME:-}" && -d "${ANDROID_HOME}" ]] || fail "ANDROID_HOME 未设置或不存在"
  ( cd "$APP_DIR" && ./gradlew --no-daemon --no-build-cache :app:assembleRelease >/tmp/aether-apk-contents-build.log 2>&1 ) \
    || { tail -n 40 /tmp/aether-apk-contents-build.log >&2; fail ":app:assembleRelease 失败"; }
  pass "release 构建成功"
fi

[[ -n "$APK" ]] || APK="$DEFAULT_APK"
[[ -f "$APK" ]] || fail "找不到 APK: $APK"
SIZE=$(wc -c < "$APK" | tr -d ' ')
pass "apk=$(basename "$APK") ($SIZE bytes)"

LISTING=$(unzip -l "$APK")
# 只取 `lib/<abi>/xxx.so` 形态的条目；目录条目（以 / 结尾）单独忽略。
SO_ENTRIES=$(printf '%s\n' "$LISTING" | awk '{print $4}' | grep -E '^lib/[^/]+/[^/]+\.so$' || true)
[[ -n "$SO_ENTRIES" ]] || fail "APK 里没有任何 lib/<abi>/*.so —— 桥接库没被打包"

# --- 1. ABI 集合 -----------------------------------------------------------
GOT_ABIS=$(printf '%s\n' "$SO_ENTRIES" | awk -F/ '{print $2}' | sort -u | tr '\n' ',' | sed 's/,$//')
[[ -n "$GOT_ABIS" ]] || fail "无法从 lib/ 推断 ABI"
pass "abi=$GOT_ABIS"

IFS=',' read -r -a EXPECTED <<< "$EXPECTED_ABIS"
UNEXPECTED=$(comm -23 <(printf '%s\n' "$GOT_ABIS" | tr ',' '\n' | sort -u) <(printf '%s\n' "${EXPECTED[@]}" | sort -u) | tr '\n' ' ' | sed 's/ *$//')
if [[ -n "$UNEXPECTED" ]]; then
  printf '%s\n' "$SO_ENTRIES" | sed 's/^/    /' >&2
  fail "出现预期之外的 ABI: $UNEXPECTED（期望只有 $EXPECTED_ABIS；若确有需要请显式设置 EXPECTED_ABIS）"
fi
MISSING_ABI=$(comm -13 <(printf '%s\n' "$GOT_ABIS" | tr ',' '\n' | sort -u) <(printf '%s\n' "${EXPECTED[@]}" | sort -u) | tr '\n' ' ' | sed 's/ *$//')
[[ -z "$MISSING_ABI" ]] || fail "缺少预期 ABI: $MISSING_ABI"
pass "ABI 集合与期望一致（$EXPECTED_ABIS）"

# --- 2. 桥接库与 JNI 必须都在 ---------------------------------------------
for abi in "${EXPECTED[@]}"; do
  for lib in libaether_mobilebridge.so libaether_jni.so; do
    printf '%s\n' "$SO_ENTRIES" | grep -qx "lib/$abi/$lib" \
      || fail "缺少 lib/$abi/$lib —— Go runtime 或 JNI 层没被打进 APK"
  done
done
pass "桥接库齐全（每个 ABI 都有 libaether_mobilebridge.so + libaether_jni.so）"

# --- 3. 调试残留 -----------------------------------------------------------
DEBUG_ENTRIES=$(printf '%s\n' "$LISTING" | awk '{print $4}' | grep -Ei '(\.debug$|\.dSYM|/debug/|lib-debug)' || true)
if [[ -n "$DEBUG_ENTRIES" ]]; then
  printf '%s\n' "$DEBUG_ENTRIES" | sed 's/^/    /' >&2
  fail "APK 里含调试符号/调试目录（release 不应带）"
fi
pass "没有调试符号残留"

# --- 4. 体积与构成（先观察，不设硬阈值） -----------------------------------
SO_BYTES=$(printf '%s\n' "$SO_ENTRIES" | while read -r e; do
  unzip -l "$APK" "$e" 2>/dev/null | awk 'NR==4{print $1}'
done | awk '{s+=$1} END{print s+0}')
pass "native 体积：${SO_BYTES} bytes（占 APK 的 $(( SO_BYTES * 100 / SIZE ))%）"
pass "参与统计的 .so：$(printf '%s\n' "$SO_ENTRIES" | wc -l | tr -d ' ') 个"
note "APK 总体积 ${SIZE} bytes；本轮只观察、不设硬阈值（避免为了数字去删功能）"

# 体积基线：只记录与比较，不作为失败条件。
BASELINE_FILE="$ROOT/mobile/android/apk-size-baseline.txt"
if [[ -f "$BASELINE_FILE" ]]; then
  PREV=$(tr -dc '0-9' < "$BASELINE_FILE")
  if [[ -n "$PREV" ]]; then
    DELTA=$(( SIZE - PREV ))
    if (( DELTA > 0 )); then
      note "比上次记录大 ${DELTA} bytes（基线 ${PREV}）"
    else
      note "比上次记录小 $(( -DELTA )) bytes（基线 ${PREV}）"
    fi
  fi
else
  note "尚无体积基线（首次运行）"
fi

echo "apk-contents=pass"
