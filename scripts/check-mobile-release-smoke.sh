#!/usr/bin/env bash
set -euo pipefail

# Stage 7 · 发布门禁第三步：**release 变体可装机冒烟**。
#
# 为什么需要这一步：前两步只证明了"能构建、装了什么"，但 release 变体从来没被装上过——
# 未签名 APK 根本无法安装。本脚本用 `-PaetherSmokeSigning=true` 让构建把 **debug keystore**
# 挂到 release 变体上（默认不开，见 app/build.gradle.kts），从而验证：
#   1. release 变体本身能被安装（不是 debug 变体冒充）；
#   2. 冷启动不崩；
#   3. 装上去的确实是 version.properties 里那一版。
#
# ⚠ 这是**冒烟用签名**，不是发布签名：产物不可发布、不可分发。
#
# 用法：bash scripts/check-mobile-release-smoke.sh --device <serial>

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$ROOT/mobile/android"
VERSION_FILE="$APP_DIR/version.properties"
PKG="com.muyuchat.aether"

SERIAL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --device) SERIAL="${2:-}"; [[ -n "$SERIAL" ]] || { echo "missing value for --device" >&2; exit 2; }; shift 2 ;;
    -h|--help) sed -n '2,18p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; echo "usage: bash scripts/check-mobile-release-smoke.sh --device <serial>" >&2; exit 2 ;;
  esac
done
if [[ -z "$SERIAL" ]]; then
  echo "usage: bash scripts/check-mobile-release-smoke.sh --device <serial>" >&2
  echo "拒绝猜设备：release 冒烟会装包并清数据，必须显式指定 serial。" >&2
  exit 2
fi

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

ADB="${ADB:-adb}"
command -v "$ADB" >/dev/null 2>&1 || fail "找不到 adb（可用 ADB=/path/to/adb 指定）"

cleanup_device() {
  "$ADB" -s "$SERIAL" shell pm clear "$PKG" >/dev/null 2>&1 || true
  "$ADB" -s "$SERIAL" logcat -c >/dev/null 2>&1 || true
}
trap cleanup_device EXIT

# --- 0. 前置 ---------------------------------------------------------------
[[ -n "${JAVA_HOME:-}" && -d "${JAVA_HOME}" ]] || fail "JAVA_HOME 未设置或不存在"
"$JAVA_HOME/bin/java" -version 2>&1 | head -n 1 | grep -q '"21' || fail "需要 JDK 21"
[[ -n "${ANDROID_HOME:-}" && -d "${ANDROID_HOME}" ]] || fail "ANDROID_HOME 未设置或不存在"
[[ "$("$ADB" -s "$SERIAL" get-state 2>/dev/null || true)" == "device" ]] || fail "设备 $SERIAL 不可用（adb get-state != device）"
pass "env: JDK21 + 设备 $SERIAL"

[[ -f "$VERSION_FILE" ]] || fail "缺少 $VERSION_FILE"
WANT_NAME=$(grep -E '^[[:space:]]*versionName[[:space:]]*=' "$VERSION_FILE" | tail -n 1 | sed -E 's/.*=[[:space:]]*//' | tr -d '[:space:]')
WANT_CODE=$(grep -E '^[[:space:]]*versionCode[[:space:]]*=' "$VERSION_FILE" | tail -n 1 | sed -E 's/.*=[[:space:]]*//' | tr -d '[:space:]')
[[ -n "$WANT_NAME" && -n "$WANT_CODE" ]] || fail "version.properties 解析失败"
pass "期望版本：$WANT_NAME ($WANT_CODE)"

echo "SMOKE SIGNING: 使用 debug keystore 给 release 变体签名，产物仅供冒烟，不可发布"

# --- 1. 构建（冒烟签名） ---------------------------------------------------
( cd "$APP_DIR" && ./gradlew --no-daemon --no-build-cache :app:assembleRelease -PaetherSmokeSigning=true \
    >/tmp/aether-release-smoke-build.log 2>&1 ) \
  || { tail -n 40 /tmp/aether-release-smoke-build.log >&2; fail ":app:assembleRelease -PaetherSmokeSigning=true 失败"; }
pass "release 构建成功（切了冒烟签名）"

APK=$(find "$APP_DIR/app/build/outputs/apk/release" -name '*.apk' -type f | sort | tail -n 1)
[[ -n "$APK" ]] || fail "release 目录下没有 APK"
# 未签名产物一定叫 *-unsigned.apk；出现它说明开关没生效。
if [[ "$(basename "$APK")" == *unsigned* ]]; then
  fail "产物仍是未签名 APK（$(basename "$APK")）—— aetherSmokeSigning 开关没有生效"
fi
pass "apk=$(basename "$APK") ($(wc -c < "$APK" | tr -d ' ') bytes)"

# --- 2. 签名存在性 ---------------------------------------------------------
# 注意：AGP 默认只做 APK Signature Scheme **v2**（不写 v1 的 META-INF/CERT.RSA），
# 所以不能用"META-INF 里有没有 CERT"来判断签名——那是 v1 时代的写法。
# 权威判断是 apksigner；没有它时退回到"文件尾部有没有 v2 签名块"。
# 优先用 java -jar lib/apksigner.jar：Windows 的 apksigner.bat 会另起 cmd，
# 输出不经过 bash 的管道/重定向（会直接打到控制台），导致断言永远抓不到内容。
APKSIGNER_JAR=$(find "$ANDROID_HOME/build-tools" -mindepth 3 -maxdepth 3 -name apksigner.jar -type f 2>/dev/null | sort -V | tail -n 1)
VERIFY_OUT=""
if [[ -n "$APKSIGNER_JAR" ]]; then
  VERIFY_OUT=$("$JAVA_HOME/bin/java" -jar "$APKSIGNER_JAR" verify --verbose "$APK" 2>&1 || true)
elif [[ -n "${APKSIGNER:-}" && "$APKSIGNER" != *.bat ]]; then
  VERIFY_OUT=$("$APKSIGNER" verify --verbose "$APK" 2>&1 || true)
fi

if [[ -n "$VERIFY_OUT" ]]; then
  # 实际输出形如：`Verified using v2 scheme (APK Signature Scheme v2): true`，
  # 注意 `v2)` 后面才是冒号 —— 少写这个右括号会让门禁永远失败。
  printf '%s\n' "$VERIFY_OUT" | grep -qE 'APK Signature Scheme v2\): true' \
    || { printf '%s\n' "$VERIFY_OUT" >&2; fail "签名不是 v2 方案（或 APK 未签名）"; }
  pass "apksigner verify 通过（v2 方案）"
elif grep -qa "APK Sig Block 42" "$APK"; then
  pass "发现 v2 签名块（无可用 apksigner，退化为魔数检查）"
else
  fail "既没有 apksigner 可验证，文件尾部也没有 v2 签名块"
fi

# --- 3. 安装 ---------------------------------------------------------------
"$ADB" -s "$SERIAL" uninstall "$PKG" >/dev/null 2>&1 || true
"$ADB" -s "$SERIAL" install -r "$APK" >/tmp/aether-smoke-install.log 2>&1 \
  || { tail -n 20 /tmp/aether-smoke-install.log >&2; fail "adb install 失败"; }
grep -q "Success" /tmp/aether-smoke-install.log || fail "adb install 没有返回 Success"
pass "已安装到 $SERIAL"

# 装上去的必须是这一版（而不是机器上旧包残留）。
INSTALLED=$("$ADB" -s "$SERIAL" shell dumpsys package "$PKG" | tr -d '\r')
GOT_NAME=$(printf '%s\n' "$INSTALLED" | sed -nE 's/.*versionName=([^ ]+).*/\1/p' | head -n 1)
GOT_CODE=$(printf '%s\n' "$INSTALLED" | sed -nE 's/.*versionCode=([0-9]+).*/\1/p' | head -n 1)
[[ "$GOT_NAME" == "$WANT_NAME" ]] || fail "设备上 versionName=$GOT_NAME，期望 $WANT_NAME"
[[ "$GOT_CODE" == "$WANT_CODE" ]] || fail "设备上 versionCode=$GOT_CODE，期望 $WANT_CODE"
pass "设备上版本一致：$GOT_NAME ($GOT_CODE)"

# --- 4. 冷启动不崩 ---------------------------------------------------------
"$ADB" -s "$SERIAL" shell am force-stop "$PKG"
"$ADB" -s "$SERIAL" logcat -c
"$ADB" -s "$SERIAL" shell am start -W -n "$PKG/.MainActivity" >/tmp/aether-smoke-start.log 2>&1 \
  || { cat /tmp/aether-smoke-start.log >&2; fail "冷启动失败"; }
grep -q "Status: ok" /tmp/aether-smoke-start.log || { cat /tmp/aether-smoke-start.log >&2; fail "am start 没有返回 Status: ok"; }
pass "冷启动成功（$(grep -E 'TotalTime' /tmp/aether-smoke-start.log | tail -n 1 | tr -d '\r')）"

sleep 3
FATAL=$("$ADB" -s "$SERIAL" logcat -d -b crash 2>/dev/null | tr -d '\r' | grep -E "FATAL EXCEPTION|$PKG" | head -n 5 || true)
if [[ -n "$FATAL" ]]; then
  printf '%s\n' "$FATAL" >&2
  fail "启动后 crash buffer 里有异常"
fi
pass "crash buffer 干净"

RESUMED=$("$ADB" -s "$SERIAL" shell dumpsys activity activities 2>/dev/null | tr -d '\r' | grep -E "mResumedActivity|topResumedActivity" | head -n 3 || true)
printf '%s' "$RESUMED" | grep -q "$PKG" || fail "前台 Activity 不是 $PKG（实际: ${RESUMED:-空}）"
pass "前台 Activity 是 $PKG"

# 说明：这里**不**自动启动网关。启动网关是用户显式动作（点「启动网关」），
# 由 UI 自动化或人工完成；脚本只验证"release 变体装得上、打得开、不崩"。
echo "NOTE: 未自动启动网关（那是用户显式动作）。网关起停的验收见 check-mobile-stage0.sh --device 与 instrumented gate。"

echo "release-smoke=pass"
