#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK="${MCA_LOCK_FILE:-$ROOT/mobile/android/vendor/mca-designsystem.lock}"
CI_MODE=false
LOCK_ONLY=false
DEVICE_SERIAL=""

usage() {
  echo "usage: $0 [--ci] [--lock-only] [--device <serial>]" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --ci)
      CI_MODE=true
      shift
      ;;
    --lock-only)
      LOCK_ONLY=true
      shift
      ;;
    --device)
      [ $# -ge 2 ] || usage
      DEVICE_SERIAL="$2"
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

if $CI_MODE && [ -n "$DEVICE_SERIAL" ]; then
  echo "--device is not supported with --ci" >&2
  exit 2
fi

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  echo "PASS: $*"
}

[ -f "$LOCK" ] || fail "missing lock file: $LOCK"

COORDINATE="$(grep -E '^coordinate=' "$LOCK" | cut -d= -f2)"
VERSION="${COORDINATE##*:}"
[ -n "$VERSION" ] || fail "lock has no usable coordinate"
ARTIFACT_DIR="${MCA_ARTIFACT_DIR:-$ROOT/mobile/android/vendor/mca-maven/com/muyuchat/mca/designsystem/$VERSION}"

check_hash() {
  local key="$1"
  local file="$2"
  local want got
  want="$(grep -E "^${key}=" "$LOCK" | cut -d= -f2)"
  [ -n "$want" ] || fail "lock has no $key"
  [ -f "$ARTIFACT_DIR/$file" ] || fail "missing artifact: $ARTIFACT_DIR/$file"
  got="$(sha256sum "$ARTIFACT_DIR/$file" | cut -d' ' -f1)"
  [ "$got" = "$want" ] || fail "$file sha256 mismatch: want $want got $got"
}

check_hash aarSha256 "designsystem-$VERSION.aar"
check_hash pomSha256 "designsystem-$VERSION.pom"
check_hash moduleSha256 "designsystem-$VERSION.module"
check_hash sourcesSha256 "designsystem-$VERSION-sources.jar"
pass "lock hashes (4/4) for $COORDINATE"

if $LOCK_ONLY; then
  exit 0
fi

[ -n "${JAVA_HOME:-}" ] || fail "JAVA_HOME is not set (JDK 21 required)"
JAVA_VERSION="$("$JAVA_HOME/bin/java" -version 2>&1 | head -1)"
echo "java=$JAVA_VERSION"
case "$JAVA_VERSION" in
  *'"21'*) ;;
  *) fail "JDK 21 required, got: $JAVA_VERSION" ;;
esac

cd "$ROOT"
CGO_ENABLED=0 go test -count=1 ./internal/mobilebridge ./cmd/mobilebridge
CGO_ENABLED=0 go vet ./internal/mobilebridge ./cmd/mobilebridge
pass "Go ABI/runtime tests"

SDK_ROOT="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
[ -n "$SDK_ROOT" ] || fail "ANDROID_HOME/ANDROID_SDK_ROOT is not set"
ANDROID_HOME="$SDK_ROOT" bash "$ROOT/scripts/build-android-mobilebridge.sh" arm64-v8a
pass "arm64-v8a bridge + probe"

cd "$ROOT/mobile/android"
chmod +x ./gradlew
./gradlew --no-daemon --no-build-cache --rerun-tasks clean :app:testDebugUnitTest :app:assembleDebug
TEST_XML="$ROOT/mobile/android/app/build/test-results/testDebugUnitTest/TEST-com.muyuchat.aether.DesignSystemPinTest.xml"
[ -f "$TEST_XML" ] || fail "missing Gradle test result: $TEST_XML"
echo "test_result=$(grep -h '<testsuite' "$TEST_XML" | head -1)"
pass "clean Gradle build + pin test"

if [ -n "$DEVICE_SERIAL" ]; then
  ADB="${ADB:-adb}"
  command -v "$ADB" >/dev/null 2>&1 || fail "adb not found; set ADB to the adb executable"
  cd "$ROOT"
  DEVICE_DIR="/data/local/tmp/aether-mobilebridge"
  adb_device() {
    MSYS_NO_PATHCONV=1 "$ADB" -s "$DEVICE_SERIAL" "$@"
  }
  adb_device shell mkdir -p "$DEVICE_DIR"
  adb_device push "dist/android/arm64-v8a/libaether_mobilebridge.so" "$DEVICE_DIR/" >/dev/null
  adb_device push "dist/android/arm64-v8a/mobilebridge-probe" "$DEVICE_DIR/" >/dev/null
  adb_device shell chmod 755 "$DEVICE_DIR/mobilebridge-probe"
  PROBE_OUTPUT="$(adb_device shell \
    "$DEVICE_DIR/mobilebridge-probe" "$DEVICE_DIR/libaether_mobilebridge.so" --cycles 3)"
  printf '%s\n' "$PROBE_OUTPUT"
  echo "$PROBE_OUTPUT" | grep -q 'probe=ok cycles=3' || fail "device probe did not report 3 cycles"
  [ "$(echo "$PROBE_OUTPUT" | grep -c 'port-released=true')" -eq 3 ] || fail "device probe did not release all 3 ports"
  pass "device probe on $DEVICE_SERIAL (3 cycles)"
fi

echo "stage0=pass mode=$([ "$CI_MODE" = true ] && echo ci || echo local)"
