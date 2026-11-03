#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK="${MCA_LOCK_FILE:-$ROOT/mobile/android/vendor/mca-designsystem.lock}"
CI_MODE=false
LOCK_ONLY=false
DEVICE_SERIAL=""
CI_EMULATOR=false
EMULATOR_API="${AETHER_EMULATOR_API:-35}"

usage() {
  echo "usage: $0 [--ci] [--lock-only] [--device <serial>] [--ci-emulator]" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --ci)
      CI_MODE=true
      shift
      ;;
    --ci-emulator)
      # Boots an x86_64 AVD, installs the app, and runs the instrumented
      # loadLibrary test. Needs KVM on Linux; GitHub's ubuntu runner has it.
      CI_MODE=true
      CI_EMULATOR=true
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
# Stage 0 acceptance includes "Go runtime starts and stops 3 times, releasing the
# port each time". That test lives in internal/mobile (TestThreeStartHealthStopCycles),
# so it has to run here -- not only in the Stage 1 gate -- or a bare Stage 0 run
# never checks its own acceptance item. -run pins the intent so a rename or a
# silent skip shows up as "no tests to run" instead of a green pass.
CGO_ENABLED=0 go test -count=1 ./internal/mobilebridge ./cmd/mobilebridge
CGO_ENABLED=0 go test -count=1 -v -run 'TestThreeStartHealthStopCycles' ./internal/mobile \
  | tee "$ROOT/.stage0-runtime-cycles.log"
grep -q '^--- PASS: TestThreeStartHealthStopCycles' "$ROOT/.stage0-runtime-cycles.log" \
  || fail "internal/mobile TestThreeStartHealthStopCycles did not run or did not pass"
rm -f "$ROOT/.stage0-runtime-cycles.log"
CGO_ENABLED=0 go vet ./internal/mobilebridge ./cmd/mobilebridge ./internal/mobile
pass "Go ABI tests + runtime start/stop cycles (3x)"

SDK_ROOT="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
[ -n "$SDK_ROOT" ] || fail "ANDROID_HOME/ANDROID_SDK_ROOT is not set"

# The CI emulator is x86_64, so it needs an x86_64 bridge. Build both ABIs when
# the emulator gate is requested; device runs keep the arm64-only default.
if $CI_EMULATOR; then
  ANDROID_HOME="$SDK_ROOT" bash "$ROOT/scripts/build-android-mobilebridge.sh" arm64-v8a
  ANDROID_HOME="$SDK_ROOT" bash "$ROOT/scripts/build-android-mobilebridge.sh" x86_64
  pass "arm64-v8a + x86_64 bridge + probe"
else
  ANDROID_HOME="$SDK_ROOT" bash "$ROOT/scripts/build-android-mobilebridge.sh" arm64-v8a
  pass "arm64-v8a bridge + probe"
fi

cd "$ROOT/mobile/android"
chmod +x ./gradlew
# Keep `clean` in its own invocation. When clean and compilation share one
# Gradle build graph, Gradle may execute them in parallel; on Windows this
# intermittently deletes R.jar while Kotlin is reading it. A separate clean
# also makes the "clean build" claim in the gate unambiguous.
./gradlew --no-daemon --no-build-cache clean
./gradlew --no-daemon --no-build-cache --rerun-tasks :app:testDebugUnitTest :app:assembleDebug
TEST_XML="$ROOT/mobile/android/app/build/test-results/testDebugUnitTest/TEST-com.muyuchat.aether.DesignSystemPinTest.xml"
[ -f "$TEST_XML" ] || fail "missing Gradle test result: $TEST_XML"
echo "test_result=$(grep -h '<testsuite' "$TEST_XML" | head -1)"
pass "clean Gradle build + pin test"

# The pin test above only compares the version string and loads a class, which
# proves *an* artifact resolved. Stage 0 also requires that the CI runner resolves
# the SAME AAR, so hash the file Gradle actually put on the classpath and compare
# it with the lock.
./gradlew --no-daemon --no-build-cache :app:verifyPinnedDesignSystem
pass "resolved AAR bytes match vendor/mca-designsystem.lock"

# The probe below is a standalone C program that dlopens the .so itself. It
# does NOT exercise the app's real entry path: System.loadLibrary("aether_jni")
# -> JNI_OnLoad -> dlopen("libaether_mobilebridge.so"). Stage 0 acceptance says
# "Android emulator/device load native library", so after the probe we also
# install the APK and run the instrumented test through that real path.
run_probe_and_app_load_check() {
  local serial="$1"
  local abi="$2"
  local adb_bin="$3"
  local adb_device
  adb_device() {
    MSYS_NO_PATHCONV=1 "$adb_bin" -s "$serial" "$@"
  }

  cd "$ROOT"
  DEVICE_DIR="/data/local/tmp/aether-mobilebridge"
  adb_device shell mkdir -p "$DEVICE_DIR"
  adb_device push "dist/android/$abi/libaether_mobilebridge.so" "$DEVICE_DIR/" >/dev/null
  adb_device push "dist/android/$abi/mobilebridge-probe" "$DEVICE_DIR/" >/dev/null
  adb_device shell chmod 755 "$DEVICE_DIR/mobilebridge-probe"
  PROBE_OUTPUT="$(adb_device shell \
    "$DEVICE_DIR/mobilebridge-probe" "$DEVICE_DIR/libaether_mobilebridge.so" --cycles 3)"
  printf '%s\n' "$PROBE_OUTPUT"
  echo "$PROBE_OUTPUT" | grep -q 'probe=ok cycles=3' || fail "device probe did not report 3 cycles on $serial"
  [ "$(echo "$PROBE_OUTPUT" | grep -c 'port-released=true')" -eq 3 ] \
    || fail "device probe did not release all 3 ports on $serial"
  pass "device probe on $serial (3 cycles, $abi)"

  cd "$ROOT/mobile/android"
  ./gradlew --no-daemon --no-build-cache :app:assembleDebug :app:assembleDebugAndroidTest \
    -PaetherAbis="$abi"
  adb_device install -r "app/build/outputs/apk/debug/app-debug.apk" >/dev/null
  adb_device install -r "app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk" >/dev/null
  INSTRUMENT_OUTPUT="$(adb_device shell am instrument -w \
    -e class 'com.muyuchat.aether.runtime.NativeBridgeInstrumentedTest#startsServesBothCredentialScopesAndStops' \
    com.muyuchat.aether.test/androidx.test.runner.AndroidJUnitRunner)"
  printf '%s\n' "$INSTRUMENT_OUTPUT"
  if ! echo "$INSTRUMENT_OUTPUT" | grep -q 'OK (1 test)'; then
    # x86_64 + app process is a known Go-runtime/seccomp incompatibility:
    # syscall 6 (SYS_LSTAT) is blocked for untrusted_app, and arm64 has no
    # lstat at all so it never hits this. Surface the real cause instead of a
    # bare "Process crashed". See docs/internal/STAGE0_X86_64_BLOCKER.md.
    if [ "$abi" = "x86_64" ] && adb_device logcat -d -b crash 2>/dev/null | grep -q 'SYS_SECCOMP'; then
      adb_device logcat -d -b crash 2>/dev/null | grep -E 'SIGSYS|SYS_SECCOMP|syscall 6' | head -3 >&2 || true
      fail "x86_64 app load is blocked by Android seccomp (SYS_LSTAT=6 in Go runtime); " \
        "this is a known upstream issue, see docs/internal/STAGE0_X86_64_BLOCKER.md"
    fi
    fail "app loadLibrary path failed on $serial (see instrumented output above)"
  fi
  pass "app System.loadLibrary -> JNI -> runtime start/stop on $serial ($abi)"
  adb_device shell pm clear com.muyuchat.aether >/dev/null
  cd "$ROOT"
}

if [ -n "$DEVICE_SERIAL" ]; then
  ADB="${ADB:-adb}"
  command -v "$ADB" >/dev/null 2>&1 || fail "adb not found; set ADB to the adb executable"
  run_probe_and_app_load_check "$DEVICE_SERIAL" arm64-v8a "$ADB"
fi

if $CI_EMULATOR; then
  ADB="${ADB:-adb}"
  command -v "$ADB" >/dev/null 2>&1 || fail "adb not found; set ADB to the adb executable"
  AVD_NAME="${AETHER_AVD_NAME:-aether-stage0-api$EMULATOR_API}"
  IMAGE="system-images;android-$EMULATOR_API;google_apis;x86_64"
  # Windows SDK ships sdkmanager.bat / avdmanager.bat, Linux ships the plain
  # names. Look for either so the gate runs on both.
  find_tool() {
    local name="$1"
    local hit
    hit="$(command -v "$name" 2>/dev/null || true)"
    if [ -z "$hit" ]; then
      hit="$(find "$SDK_ROOT/cmdline-tools" -maxdepth 3 -type f \
        \( -name "$name" -o -name "$name.bat" \) 2>/dev/null | head -1)"
    fi
    printf '%s' "$hit"
  }
  SDKMANAGER="$(find_tool sdkmanager)"
  [ -n "$SDKMANAGER" ] || fail "sdkmanager not found under $SDK_ROOT"

  echo "no" | "$SDKMANAGER" --sdk_root="$SDK_ROOT" --licenses >/dev/null 2>&1 || true
  "$SDKMANAGER" --sdk_root="$SDK_ROOT" "emulator" "$IMAGE" >/dev/null

  AVDMANAGER="$(find_tool avdmanager)"
  [ -n "$AVDMANAGER" ] || fail "avdmanager not found under $SDK_ROOT"
  # The AVD's userdata partition needs several GB. On a full C: drive the
  # emulator dies with "Not enough space to create userdata partition", so let
  # callers point ANDROID_AVD_HOME at a roomier disk.
  AVD_HOME="${ANDROID_AVD_HOME:-${HOME}/.android/avd}"
  if [ ! -d "${AVD_HOME}/${AVD_NAME}.avd" ]; then
    ANDROID_AVD_HOME="$AVD_HOME" bash -c \
      "echo no | \"$AVDMANAGER\" create avd -n \"$AVD_NAME\" -k \"$IMAGE\" --force" >/dev/null
  fi
  [ -d "${AVD_HOME}/${AVD_NAME}.avd" ] || fail "AVD ${AVD_NAME} not found under $AVD_HOME"
  export ANDROID_AVD_HOME="$AVD_HOME"

  EMULATOR="$SDK_ROOT/emulator/emulator"
  [ -x "$EMULATOR" ] || EMULATOR="$EMULATOR.exe"
  [ -x "$EMULATOR" ] || fail "emulator binary not found under $SDK_ROOT/emulator"
  # -no-window keeps the runner headless; -gpu swiftshader_indirect works without
  # a real GPU; -no-snapshot keeps runs reproducible.
  "$EMULATOR" -avd "$AVD_NAME" -no-window -no-audio -no-boot-anim \
    -gpu swiftshader_indirect -no-snapshot >/tmp/aether-emulator.log 2>&1 &
  EMULATOR_PID=$!
  trap 'kill "$EMULATOR_PID" 2>/dev/null || true' EXIT

  MSYS_NO_PATHCONV=1 "$ADB" start-server >/dev/null 2>&1 || true
  # Do not use bare `adb wait-for-device` / `adb shell`: a developer machine may
  # also have physical devices attached, which makes adb refuse with
  # "more than one device/emulator". Resolve the emulator serial first, then
  # address it explicitly everywhere.
  EMULATOR_SERIAL=""
  for _ in $(seq 1 180); do
    EMULATOR_SERIAL="$(MSYS_NO_PATHCONV=1 "$ADB" devices | awk '/^emulator-[0-9]+[[:space:]]+device$/ {print $1; exit}')"
    [ -n "$EMULATOR_SERIAL" ] && break
    sleep 2
  done
  [ -n "$EMULATOR_SERIAL" ] || {
    tail -50 /tmp/aether-emulator.log >&2
    fail "emulator did not register an adb serial within 360s"
  }

  BOOTED=""
  for _ in $(seq 1 180); do
    if [ "$(MSYS_NO_PATHCONV=1 "$ADB" -s "$EMULATOR_SERIAL" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ]; then
      BOOTED=1
      break
    fi
    sleep 2
  done
  [ -n "$BOOTED" ] || { tail -50 /tmp/aether-emulator.log >&2; fail "emulator did not boot within 360s"; }
  pass "emulator $EMULATOR_SERIAL booted (api $EMULATOR_API, x86_64)"

  run_probe_and_app_load_check "$EMULATOR_SERIAL" x86_64 "$ADB"

  MSYS_NO_PATHCONV=1 "$ADB" -s "$EMULATOR_SERIAL" emu kill >/dev/null 2>&1 || true
  trap - EXIT
  kill "$EMULATOR_PID" 2>/dev/null || true
fi

echo "stage0=pass mode=$([ "$CI_MODE" = true ] && echo ci || echo local)"
