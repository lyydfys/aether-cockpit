#!/usr/bin/env bash
# 生成 NOTICE：列出直接/间接依赖模块及版本（发布包随附）。
set -euo pipefail
cd "$(dirname "$0")/.."
{
  echo "Aether Cockpit"
  echo "=================="
  echo
  echo "本产物包含以下 Go 模块（模块保留各自许可证，全文见其源码仓库）："
  echo
  go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all | grep -v '^$' | sort | sed 's/^/  /'
} > NOTICE
echo "wrote NOTICE ($(grep -c '^  ' NOTICE) modules)"
