#!/usr/bin/env bash
# 把 aether.mac-api.xyz 的站点块替换为最新版（支持重复执行）
set -euo pipefail

CADDYFILE=/opt/mac-api/Caddyfile
SNIPPET=/opt/aether-site/caddy-aether.snippet
DOMAIN=aether.mac-api.xyz
TS=$(date +%Y%m%d%H%M%S)

cp -a "$CADDYFILE" "$CADDYFILE.bak.$TS"
echo "== 备份 -> $CADDYFILE.bak.$TS"

# 删掉已有的 aether 站点块（从域名行到匹配的收尾大括号）
python3 - "$CADDYFILE" "$DOMAIN" <<'PY'
import re, sys
path, domain = sys.argv[1], sys.argv[2]
src = open(path, encoding="utf-8").read()

# 匹配 "<domain> {" 到该块对应的收尾 "}"（按大括号计数）
pat = re.compile(r"^" + re.escape(domain) + r"\s*\{", re.M)
m = pat.search(src)
if m:
    i = m.start()
    depth = 0
    j = m.end() - 1
    while j < len(src):
        if src[j] == "{":
            depth += 1
        elif src[j] == "}":
            depth -= 1
            if depth == 0:
                j += 1
                break
        j += 1
    # 连带后面的空行一起去掉
    while j < len(src) and src[j] == "\n":
        j += 1
    src = src[:i] + src[j:]
    open(path, "w", encoding="utf-8").write(src)
    print("   已移除旧块")
else:
    print("   无旧块")
PY

# 追加最新片段
{
  echo ""
  cat "$SNIPPET"
} >> "$CADDYFILE"
echo "== 已写入最新站点块"

if ! docker exec caddy caddy validate --config /etc/caddy/Caddyfile >/tmp/cv.log 2>&1; then
  echo "!! 校验失败，回滚"
  tail -20 /tmp/cv.log
  cp -a "$CADDYFILE.bak.$TS" "$CADDYFILE"
  exit 1
fi
echo "== 校验通过"

docker exec caddy caddy reload --config /etc/caddy/Caddyfile 2>&1 | grep -v 'not formatted' || true
echo "== 已重载"
