#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ABI="${1:-arm64-v8a}"
NDK_VERSION="${ANDROID_NDK_VERSION:-29.0.13113456}"
API="${ANDROID_API:-26}"

case "$ABI" in
  arm64-v8a)
    GOARCH=arm64
    TRIPLE=aarch64-linux-android
    ;;
  *)
    echo "unsupported ABI: $ABI (only arm64-v8a is verified)" >&2
    exit 2
    ;;
esac

NDK="${ANDROID_NDK_HOME:-}"
if [ -z "$NDK" ]; then
  SDK_ROOT="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
  if [ -z "$SDK_ROOT" ]; then
    echo "set ANDROID_HOME/ANDROID_SDK_ROOT or ANDROID_NDK_HOME" >&2
    exit 2
  fi
  NDK="$SDK_ROOT/ndk/$NDK_VERSION"
fi

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    HOST_TAG=windows-x86_64
    EXE=.exe
    CC_SUFFIX=.cmd
    ;;
  Linux)
    HOST_TAG=linux-x86_64
    EXE=
    CC_SUFFIX=
    ;;
  Darwin)
    HOST_TAG=darwin-x86_64
    EXE=
    CC_SUFFIX=
    ;;
  *)
    echo "unsupported host: $(uname -s)" >&2
    exit 2
    ;;
esac

BIN="$NDK/toolchains/llvm/prebuilt/$HOST_TAG/bin"
CC="$BIN/${TRIPLE}${API}-clang${CC_SUFFIX}"
NM="$BIN/llvm-nm${EXE}"
READELF="$BIN/llvm-readelf${EXE}"

for tool in "$CC" "$NM" "$READELF"; do
  if [ ! -f "$tool" ]; then
    echo "missing tool: $tool" >&2
    exit 3
  fi
done

OUT="$ROOT/dist/android/$ABI"
mkdir -p "$OUT"
SO="$OUT/libaether_mobilebridge.so"
HDR="$OUT/libaether_mobilebridge.h"

echo "android_abi=$ABI"
echo "goarch=$GOARCH"
echo "cc=$CC"
echo "go=$(go version)"

cd "$ROOT"
GOOS=android GOARCH="$GOARCH" CGO_ENABLED=1 CC="$CC" \
  go build -trimpath -buildmode=c-shared -o "$SO" ./cmd/mobilebridge

for symbol in nativeBridgeVersion nativeStart nativeGetStatus nativeStop; do
  if ! "$NM" -D --defined-only "$SO" | grep -q " $symbol$"; then
    echo "missing exported symbol: $symbol" >&2
    "$NM" -D --defined-only "$SO" | head -20 >&2
    exit 4
  fi
done

if ! "$READELF" -h "$SO" | grep -q "AArch64"; then
  echo "unexpected ELF machine:" >&2
  "$READELF" -h "$SO" | grep -E "Class|Machine" >&2
  exit 5
fi

for symbol in nativeBridgeVersion nativeStart nativeGetStatus nativeStop; do
  if ! grep -q "$symbol" "$HDR"; then
    echo "header $HDR does not declare $symbol" >&2
    exit 6
  fi
done

PROBE="$OUT/mobilebridge-probe"
"$CC" -std=c11 -O2 -fPIE -pie -o "$PROBE" "$ROOT/cmd/mobilebridge/probe/main.c" -ldl
if ! "$READELF" -h "$PROBE" | grep -q "AArch64"; then
  echo "probe is not an AArch64 binary:" >&2
  "$READELF" -h "$PROBE" | grep -E "Class|Machine" >&2
  exit 7
fi

echo "artifact=$SO"
echo "header=$HDR"
echo "size=$(wc -c < "$SO" | tr -d ' ')"
echo "sha256=$(sha256sum "$SO" | cut -d' ' -f1)"
echo "probe=$PROBE"
echo "probe_size=$(wc -c < "$PROBE" | tr -d ' ')"
echo "probe_sha256=$(sha256sum "$PROBE" | cut -d' ' -f1)"
go version -m "$SO" | head -8
