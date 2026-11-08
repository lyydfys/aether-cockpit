#!/usr/bin/env bash
# 把 aether.mac-api.xyz 站点块接进现有 Caddyfile，并安全重载 Caddy。
#
# 特性：
#   - 幂等：已存在就直接退出，不会重复追加
#   - 改前备份：备份到 Caddyfile.bak.<时间戳>
#   - 改后校验：caddy validate 失败则自动回滚
#   - 只 reload，不 restart（不中断现有 mac-api.xyz 连接）
#
# 用法：bash /opt/aether-site/caddy-apply.sh

set -euo pipefail

CADDYFILE=/opt/mac-api/Caddyfile
SNIPPET=/opt/aether-site/caddy-aether.snippet
DOMAIN=aether.mac-api.xyz

[ -f "$CADDYFILE" ] || { echo "!! 找不到 $CADDYFILE"; exit 1; }
[ -f "$SNIPPET" ]   || { echo "!! 找不到 $SNIPPET"; exit 1; }

if grep -q "$DOMAIN" "$CADDYFILE"; then
  echo "== $DOMAIN 已存在于 Caddyfile，跳过修改"
else
  TS=$(date +%Y%m%d%H%M%S)
  cp -a "$CADDYFILE" "$CADDYFILE.bak.$TS"
  echo "== 已备份 -> $CADDYFILE.bak.$TS"

  {
    echo ""
    cat "$SNIPPET"
  } >> "$CADDYFILE"
  echo "== 已追加站点块"

  # 校验；失败自动回滚
  if ! docker exec caddy caddy validate --config /etc/caddy/Caddyfile >/tmp/caddy-validate.log 2>&1; then
    echo "!! 配置校验失败，回滚"
    tail -20 /tmp/caddy-validate.log
    cp -a "$CADDYFILE.bak.$TS" "$CADDYFILE"
    echo "== 已回滚"
    exit 1
  fi
  echo "== 校验通过"
fi

# 重载（不中断现有连接）
docker exec caddy caddy reload --config /etc/caddy/Caddyfile
echo "== Caddy 已重载"

sleep 2
echo
echo "== 当前 Caddyfile"
cat "$CADDYFILE"
