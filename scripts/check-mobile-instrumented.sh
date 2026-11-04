#!/usr/bin/env bash
set -euo pipefail

# Replayable gate for EVERY Android instrumented test class.
#
# `scripts/check-mobile-stage0.sh --device` only calls one method through bare
# `am instrument`; its output never becomes a Gradle report and the other two
# classes were never executed by any script or CI job. This gate runs
# `:app:connectedDebugAndroidTest` against the requested serial and grades the
# JUnit XML Gradle writes. "No visible failure" is not enough: an empty report,
# a missing class, or a non-zero skipped count all fail.
#
# Usage: bash scripts/check-mobile-instrumented.sh --device <serial>
# Requires: JDK 21, ANDROID_HOME/ANDROID_SDK_ROOT, adb, and an arm64-v8a device
# in the `device` state. Build the bridge first if dist/android/arm64-v8a is
# empty: bash scripts/build-android-mobilebridge.sh arm64-v8a
# (the CI device job does this in its preceding Stage 0 step).

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANDROID_PROJECT="$ROOT/mobile/android"
RESULTS_DIR="$ANDROID_PROJECT/app/build/outputs/androidTest-results/connected"
APP_ID="com.muyuchat.aether"
EXPECTED_CLASSES=(
  "com.muyuchat.aether.runtime.NativeBridgeInstrumentedTest"
  "com.muyuchat.aether.service.GatewayServiceInstrumentedTest"
  "com.muyuchat.aether.service.RequestLedgerInstrumentedTest"
)

DEVICE_SERIAL=""

usage() {
  echo "usage: $0 --device <serial>" >&2
  exit 2
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  echo "PASS: $*"
}

while [ $# -gt 0 ]; do
  case "$1" in
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

[ -n "$DEVICE_SERIAL" ] || usage

# --- host preconditions -----------------------------------------------------
[ -n "${JAVA_HOME:-}" ] || fail "JAVA_HOME is not set (JDK 21 required)"
JAVA_VERSION="$("$JAVA_HOME/bin/java" -version 2>&1 | head -1)"
echo "java=$JAVA_VERSION"
case "$JAVA_VERSION" in
  *'"21'*) ;;
  *) fail "JDK 21 required, got: $JAVA_VERSION" ;;
esac

SDK_ROOT="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
[ -n "$SDK_ROOT" ] || fail "ANDROID_HOME/ANDROID_SDK_ROOT is not set"
[ -d "$SDK_ROOT" ] || fail "ANDROID_HOME/ANDROID_SDK_ROOT is not a directory: $SDK_ROOT"
export ANDROID_HOME="$SDK_ROOT"
export ANDROID_SDK_ROOT="$SDK_ROOT"

ADB="${ADB:-adb}"
command -v "$ADB" >/dev/null 2>&1 || fail "adb not found; set ADB to the adb executable"

adb_device() {
  MSYS_NO_PATHCONV=1 "$ADB" -s "$DEVICE_SERIAL" "$@"
}

DEVICE_STATE="$(adb_device get-state 2>/dev/null | tr -d '\r' || true)"
[ "$DEVICE_STATE" = "device" ] \
  || fail "device $DEVICE_SERIAL is not ready (adb get-state='${DEVICE_STATE:-<none>}')"

DEVICE_ABI="$(adb_device shell getprop ro.product.cpu.abi 2>/dev/null | tr -d '\r' || true)"
[ "$DEVICE_ABI" = "arm64-v8a" ] \
  || fail "device $DEVICE_SERIAL reports ABI '${DEVICE_ABI:-unknown}'; this gate needs arm64-v8a " \
    "(x86_64 app load is blocked by Android seccomp, see docs/internal/STAGE0_X86_64_BLOCKER.md)"
pass "device $DEVICE_SERIAL online (arm64-v8a)"

for lib in libaether_mobilebridge.so libaether_jni.so; do
  [ -f "$ROOT/dist/android/arm64-v8a/$lib" ] \
    || fail "missing dist/android/arm64-v8a/$lib; run: bash scripts/build-android-mobilebridge.sh arm64-v8a"
done
pass "arm64-v8a bridge libraries present"

# --- device state -----------------------------------------------------------
# Start from a clean app state so account keys, usage rows and a leftover
# gateway from an earlier run cannot make this run's assertions pass for the
# wrong reason.
adb_device shell am force-stop "$APP_ID" >/dev/null 2>&1 || true
adb_device shell pm clear "$APP_ID" >/dev/null 2>&1 || true
MSYS_NO_PATHCONV=1 "$ADB" logcat -c >/dev/null 2>&1 || true

cleanup_device() {
  # Best effort: the gate result is the test report, not the cleanup. Keep the
  # device free of app data and log noise for the next replay.
  adb_device shell pm clear "$APP_ID" >/dev/null 2>&1 || true
  MSYS_NO_PATHCONV=1 "$ADB" logcat -c >/dev/null 2>&1 || true
}
trap cleanup_device EXIT

# --- run the whole instrumentation suite ------------------------------------
cd "$ANDROID_PROJECT"
chmod +x ./gradlew
# AGP's ConnectedDeviceProvider honours ANDROID_SERIAL, so a machine with more
# than one attached target still runs the suite on exactly --device's serial.
export ANDROID_SERIAL="$DEVICE_SERIAL"

# A stale report must never be graded as this run's result.
rm -rf "$RESULTS_DIR"

set +e
./gradlew --no-daemon --no-build-cache :app:connectedDebugAndroidTest
GRADLE_RC=$?
set -e
echo "gradle_exit=$GRADLE_RC"

mapfile -t XML_FILES < <(find "$RESULTS_DIR" -type f -name 'TEST-*.xml' 2>/dev/null | sort)

sum_attr() {
  local attr="$1"
  [ "${#XML_FILES[@]}" -gt 0 ] || { printf '0'; return 0; }
  { grep -hoE "[[:space:]]${attr}=\"[0-9]+\"" "${XML_FILES[@]}" || true; } \
    | tr -d '\r' \
    | sed -E 's/.*="([0-9]+)"$/\1/' \
    | awk '{ total += $1 } END { printf "%d", total + 0 }'
}

class_ran() {
  local fqcn="$1"
  [ "${#XML_FILES[@]}" -gt 0 ] || return 1
  grep -hFq "classname=\"$fqcn\"" "${XML_FILES[@]}"
}

# Returns "Class#method" for the first <failure>/<error> in the report.
first_failed_case() {
  local file fail_line start_line blob name cls
  for file in "${XML_FILES[@]}"; do
    fail_line="$(grep -nE '<(failure|error)([[:space:]>]|$)' "$file" | head -1 | cut -d: -f1 || true)"
    [ -n "$fail_line" ] || continue
    start_line="$(grep -n '<testcase' "$file" \
      | awk -F: -v limit="$fail_line" '$1 + 0 <= limit + 0 { n = $1 } END { print n + 0 }')"
    [ "${start_line:-0}" -gt 0 ] || start_line=1
    blob="$(sed -n "${start_line},${fail_line}p" "$file")"
    name="$(printf '%s\n' "$blob" | grep -oE 'name="[^"]*"' | head -1 \
      | sed -E 's/^name="(.*)"$/\1/' || true)"
    cls="$(printf '%s\n' "$blob" | grep -oE 'classname="[^"]*"' | head -1 \
      | sed -E 's/^classname="(.*)"$/\1/' || true)"
    printf '%s#%s\n' "${cls:-?}" "${name:-?}"
    return 0
  done
  return 1
}

report_failure() {
  local reason="$1" tests failures errors skipped first
  echo "FAIL: $reason" >&2
  echo "results_dir=$RESULTS_DIR" >&2
  if [ "${#XML_FILES[@]}" -eq 0 ]; then
    echo "xml_files=0 (no TEST-*.xml was produced)" >&2
    echo "xml_counts: tests=0 failures=0 errors=0 skipped=0" >&2
    exit 1
  fi
  printf 'xml=%s\n' "${XML_FILES[@]}" >&2
  tests="$(sum_attr tests)"
  failures="$(sum_attr failures)"
  errors="$(sum_attr errors)"
  skipped="$(sum_attr skipped)"
  echo "xml_counts: tests=$tests failures=$failures errors=$errors skipped=$skipped" >&2
  if [ "$((failures + errors))" -gt 0 ]; then
    first="$(first_failed_case)" || first=""
    echo "first_failure=${first:-<could not parse a <testcase> around <failure>/<error>}" >&2
  fi
  exit 1
}

[ "$GRADLE_RC" -eq 0 ] || report_failure "connectedDebugAndroidTest exited $GRADLE_RC"
[ "${#XML_FILES[@]}" -gt 0 ] \
  || report_failure "connectedDebugAndroidTest exited 0 but produced no TEST-*.xml"

TESTS="$(sum_attr tests)"
FAILURES="$(sum_attr failures)"
ERRORS="$(sum_attr errors)"
SKIPPED="$(sum_attr skipped)"
echo "xml_counts: tests=$TESTS failures=$FAILURES errors=$ERRORS skipped=$SKIPPED"

# Core of this card: an empty report ("no failures" because nothing ran) must
# fail, not pass.
[ "$TESTS" -gt 0 ] || report_failure "report contains 0 tests; an empty report is not a pass"
[ "$FAILURES" -eq 0 ] || report_failure "report contains $FAILURES failure(s)"
[ "$ERRORS" -eq 0 ] || report_failure "report contains $ERRORS error(s)"
[ "$SKIPPED" -eq 0 ] \
  || report_failure "report contains $SKIPPED skipped test(s); every test must actually run"
pass "XML counts (tests=$TESTS failures=0 errors=0 skipped=0)"

# Explicit, reason-bearing skip list. It is empty on purpose: all three classes
# must really run on a device. If a class genuinely cannot run on some device,
# add a case branch here *with the reason*; the gate then prints
# "SKIP: <class> (<reason>)" instead of silently ignoring it. Never add a class
# here just to make this gate green.
skip_reason() {
  case "$1" in
    # "com.muyuchat.aether.runtime.NativeBridgeInstrumentedTest")
    #   echo "requires ...; tracked in ..."
    #   ;;
    *) return 1 ;;
  esac
}

MISSING_CLASSES=0
for class in "${EXPECTED_CLASSES[@]}"; do
  if class_ran "$class"; then
    pass "ran $class"
    continue
  fi
  if reason="$(skip_reason "$class")"; then
    echo "SKIP: $class ($reason)"
    continue
  fi
  echo "MISSING: $class produced no <testcase> in the report" >&2
  MISSING_CLASSES=$((MISSING_CLASSES + 1))
done
[ "$MISSING_CLASSES" -eq 0 ] \
  || report_failure "$MISSING_CLASSES expected instrumentation class(es) did not run"

pass "all instrumented classes on $DEVICE_SERIAL"
echo "instrumented=pass"
