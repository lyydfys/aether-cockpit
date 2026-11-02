#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PACKAGES=(
  ./internal/mobile
  ./internal/mobilebridge
  ./cmd/mobilebridge
  ./internal/core/launcher
  ./internal/core/apppaths
  ./internal/providers/workbuddy
)

echo "stage1=test"
CGO_ENABLED=0 go test -count=1 "${PACKAGES[@]}"

echo "stage1=vet"
CGO_ENABLED=0 go vet "${PACKAGES[@]}"

echo "stage1=build-linux-amd64"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build "${PACKAGES[@]}"

echo "stage1=build-windows-amd64"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build "${PACKAGES[@]}"

echo "stage1=pass"
