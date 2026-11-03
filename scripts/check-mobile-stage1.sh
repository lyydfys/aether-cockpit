#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TEST_PACKAGES=(
  ./internal/mobile
  ./internal/mobilebridge
  ./cmd/mobilebridge
  ./internal/core/launcher
  ./internal/core/apppaths
  ./internal/core/logging
  ./internal/providers/workbuddy
)

# gateway 已随 mobile runtime 链入 c-shared 产物；host 单测由 Windows CI
# 的 go test ./... 覆盖，这里只验证 Android 目标的 Linux/Windows 编译面。
BUILD_PACKAGES=("${TEST_PACKAGES[@]}" ./internal/core/gateway)

echo "stage1=test"
CGO_ENABLED=0 go test -count=1 "${TEST_PACKAGES[@]}"

echo "stage1=vet"
CGO_ENABLED=0 go vet "${BUILD_PACKAGES[@]}"

echo "stage1=build-linux-amd64"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build "${BUILD_PACKAGES[@]}"

echo "stage1=build-windows-amd64"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build "${BUILD_PACKAGES[@]}"

echo "stage1=pass"
