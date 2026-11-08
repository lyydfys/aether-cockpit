#!/usr/bin/env bash
# 打包服务器 docker build 所需文件
set -euo pipefail
cd "/mnt/d/Aether Cockpit/website"

OUT=dist/aether-site-docker.tar.gz
rm -f "$OUT"

tar -czf "$OUT" \
    index.html \
    404.html \
    changelog.html \
    assets \
    deploy/Dockerfile \
    deploy/nginx-container.conf \
    deploy/security-headers.conf \
    deploy/sync-changelog.py \
    deploy/compose.mac-api.yml

echo "打包完成: $(du -h "$OUT" | cut -f1)"
tar -tzf "$OUT" | grep -v '^assets/' | sed 's/^/  /'
echo "  assets/ ($(tar -tzf "$OUT" | grep -c '^assets/') 个文件)"
