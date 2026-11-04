#!/usr/bin/env bash
set -euo pipefail

# Stage 7 · 发布门禁第一步：release 构建能不能产出一份"口径正确"的 APK。
#
# 这里**不**校验签名（仓库里没有 keystore，本卡也不允许自造一个）——它只回答四个问题：
#   1. release 变体能构建出 APK 吗？
#   2. APK 的 applicationId / versionCode / versionName / minSdk / targetSdk 与源码一致吗？
#   3. release APK 真的不可调试吗（没有 android:debuggable="true"）？
#   4. 版本号是不是只有一处来源（version.properties），而不是 build.gradle.kts 里另写死一份？
#
# 用法：bash scripts/check-mobile-release.sh [--ci]
#   --ci 只做 shell 侧静态检查，不跑 Gradle（托管 runner 上由 CI 自己跑 assembleRelease）。

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$ROOT/mobile/android"
VERSION_FILE="$APP_DIR/version.properties"
BUILD_FILE="$APP_DIR/app/build.gradle.kts"
CATALOG="$APP_DIR/gradle/libs.versions.toml"

CI_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --ci) CI_ONLY=1 ;;
    -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# --- 0. 版本号单一来源 -------------------------------------------------------
[[ -f "$VERSION_FILE" ]] || fail "missing $VERSION_FILE —— 版本号必须只有一个来源"
# 解析 version.properties，不依赖 java/gradle。
read_prop() {
  local key="$1"
  local line
  line=$(grep -E "^[[:space:]]*${key}[[:space:]]*=" "$VERSION_FILE" | tail -n 1 || true)
  [[ -n "$line" ]] || return 1
  printf '%s' "${line#*=}" | tr -d '[:space:]'
}
WANT_CODE=$(read_prop versionCode) || fail "version.properties 缺少 versionCode"
WANT_NAME=$(read_prop versionName) || fail "version.properties 缺少 versionName"
[[ "$WANT_CODE" =~ ^[0-9]+$ ]] || fail "versionCode 必须是整数，实际 '$WANT_CODE'"
[[ -n "$WANT_NAME" ]] || fail "versionName 不能为空"
pass "version.properties: versionCode=$WANT_CODE versionName=$WANT_NAME"

# build.gradle.kts 里不允许再出现写死的数字/字符串版本（必须从文件读）。
if grep -Eq 'versionCode[[:space:]]*=[[:space:]]*[0-9]+' "$BUILD_FILE"; then
  fail "build.gradle.kts 里仍有写死的 versionCode —— 版本号必须只有一个来源"
fi
if grep -Eq 'versionName[[:space:]]*=[[:space:]]*"' "$BUILD_FILE"; then
  fail "build.gradle.kts 里仍有写死的 versionName —— 版本号必须只有一个来源"
fi
grep -q 'version.properties' "$BUILD_FILE" \
  || fail "build.gradle.kts 没有读取 version.properties（单一来源没有真正接上）"
pass "build.gradle.kts 只从 version.properties 读取版本号"

# minSdk/targetSdk 的唯一来源是版本目录；它们会用来核对 APK。
cat_sdk() {
  local key="$1"
  grep -E "^[[:space:]]*${key}[[:space:]]*=" "$CATALOG" | tail -n 1 | sed -E 's/.*=[[:space:]]*"?([0-9]+)"?.*/\1/'
}
WANT_MIN=$(cat_sdk minSdk)
WANT_TARGET=$(cat_sdk targetSdk)
[[ "$WANT_MIN" =~ ^[0-9]+$ ]] || fail "无法从 $CATALOG 读到 minSdk"
[[ "$WANT_TARGET" =~ ^[0-9]+$ ]] || fail "无法从 $CATALOG 读到 targetSdk"
pass "catalog: minSdk=$WANT_MIN targetSdk=$WANT_TARGET"

if [[ "$CI_ONLY" == "1" ]]; then
  pass "release-gate(source)=pass (--ci: 未构建 APK)"
  exit 0
fi

# --- 1. 环境 ---------------------------------------------------------------
[[ -n "${JAVA_HOME:-}" && -d "${JAVA_HOME}" ]] || fail "JAVA_HOME 未设置或不存在"
"$JAVA_HOME/bin/java" -version 2>&1 | head -n 1 | grep -q '"21' || fail "需要 JDK 21（实际: $("$JAVA_HOME/bin/java" -version 2>&1 | head -n 1)）"
[[ -n "${ANDROID_HOME:-}" && -d "${ANDROID_HOME}" ]] || fail "ANDROID_HOME 未设置或不存在"
# aapt2 在 Linux/macOS 上叫 aapt2，在 Windows 上叫 aapt2.exe —— 两种都要能找到。
AAPT2=$(find "$ANDROID_HOME/build-tools" -mindepth 2 -maxdepth 2 \( -name aapt2 -o -name aapt2.exe \) -type f 2>/dev/null | sort -V | tail -n 1)
[[ -n "$AAPT2" ]] || fail "找不到 aapt2（$ANDROID_HOME/build-tools/*/aapt2[.exe]）"
pass "env: JDK21 + aapt2=$(basename "$(dirname "$AAPT2")")"

# --- 2. 构建 release -------------------------------------------------------
cd "$APP_DIR"
./gradlew --no-daemon --no-build-cache :app:assembleRelease >/tmp/aether-release-build.log 2>&1 \
  || { tail -n 40 /tmp/aether-release-build.log >&2; fail ":app:assembleRelease 失败（日志 /tmp/aether-release-build.log）"; }
pass ":app:assembleRelease 构建成功"

APK=$(find "$APP_DIR/app/build/outputs/apk/release" -name '*.apk' -type f | sort | tail -n 1)
[[ -n "$APK" ]] || fail "release 目录下没有 APK"
pass "apk=$(basename "$APK") ($(wc -c < "$APK" | tr -d ' ') bytes)"

# 明确记录签名形态：本卡不引入 keystore，所以这里只可能是未签名产物。
if ! "$AAPT2" dump badging "$APK" >/dev/null 2>&1; then
  fail "aapt2 无法解析该 APK"
fi
if [[ "$(basename "$APK")" == *unsigned* ]]; then
  echo "NOTE: 该 APK 未签名（$(basename "$APK")）—— 发布前需要由发布流程签名，本门禁不校验签名"
fi

BADGING=$("$AAPT2" dump badging "$APK")

# --- 3. 断言 APK 元数据 ----------------------------------------------------
# badging 的第一行形如：
#   package: name='com.x' versionCode='1' versionName='0.1.0' ... compileSdkVersionCodename='16'
# 注意行尾还有一个 `...Codename='16'`：不能用 `.*name='([^']*)'` 这种贪婪前缀，
# 否则会把 compileSdkVersionCodename 的值当成 applicationId。必须从行首按顺序锚定。
GOT_ID=$(printf '%s\n' "$BADGING" | sed -nE "s/^package: name='([^']*)'.*/\1/p" | head -n 1)
GOT_CODE=$(printf '%s\n' "$BADGING" | sed -nE "s/^package: name='[^']*' versionCode='([^']*)'.*/\1/p" | head -n 1)
GOT_NAME=$(printf '%s\n' "$BADGING" | sed -nE "s/^package: name='[^']*' versionCode='[^']*' versionName='([^']*)'.*/\1/p" | head -n 1)

EXPECT_ID="com.muyuchat.aether"
[[ "$GOT_ID" == "$EXPECT_ID" ]] || fail "applicationId=$GOT_ID，期望 $EXPECT_ID"
pass "applicationId=$GOT_ID"

[[ "$GOT_CODE" == "$WANT_CODE" ]] || fail "APK versionCode=$GOT_CODE，version.properties=$WANT_CODE"
pass "versionCode=$GOT_CODE（与 version.properties 一致）"

[[ "$GOT_NAME" == "$WANT_NAME" ]] || fail "APK versionName=$GOT_NAME，version.properties=$WANT_NAME"
pass "versionName=$GOT_NAME（与 version.properties 一致）"

# aapt2 用的是 `minSdkVersion:'26'`；aapt1 的老格式是 `sdkVersion:'26'`。
# 两种都接受，避免换 build-tools 版本时门禁无声失效。
GOT_MIN=$(printf '%s\n' "$BADGING" | sed -nE "s/^(min)?[sS]dkVersion:'([0-9]+)'.*/\2/p" | head -n 1)
GOT_TARGET=$(printf '%s\n' "$BADGING" | sed -nE "s/^targetSdkVersion:'([0-9]+)'.*/\1/p" | head -n 1)
[[ "$GOT_MIN" == "$WANT_MIN" ]] || fail "APK minSdk=$GOT_MIN，期望 $WANT_MIN"
pass "minSdk=$GOT_MIN"
[[ "$GOT_TARGET" == "$WANT_TARGET" ]] || fail "APK targetSdk=$GOT_TARGET，期望 $WANT_TARGET"
pass "targetSdk=$GOT_TARGET"

# --- 4. 断言不可调试 -------------------------------------------------------
# 用 xmltree 直接看二进制 manifest：debuggable 为 false 时该属性根本不出现。
MANIFEST_XML=$("$AAPT2" dump xmltree --file AndroidManifest.xml "$APK" 2>/dev/null || true)
[[ -n "$MANIFEST_XML" ]] || fail "无法 dump AndroidManifest.xml"
if printf '%s' "$MANIFEST_XML" | grep -Eq 'android:debuggable\(0x[0-9a-f]+\)=\(type 0x12\)0xffffffff'; then
  fail "release APK 含 android:debuggable=true"
fi
pass "release APK 不可调试（无 android:debuggable=true）"

echo "release-gate=pass"
