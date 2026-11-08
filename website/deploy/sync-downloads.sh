#!/usr/bin/env bash
# 把 Aether Cockpit 最新 release 的构建产物镜像到本机，供官网直接下载。
#
# 为什么镜像而不是反代 GitHub：
#   国内用户访问 GitHub Releases 常超时，反代只是把超时从浏览器挪到服务器，
#   用户仍然要看运气。镜像到本地磁盘后，下载链路只剩「服务器 → 用户」，
#   和访问官网本身同一条线路。
#
# 用法：
#   bash /opt/aether-site/sync-downloads.sh            # 拉最新
#   bash /opt/aether-site/sync-downloads.sh v0.4.1     # 指定 tag
#
# 幂等：文件大小与 GitHub 一致就跳过，不重复下载。

set -euo pipefail

REPO=${REPO:-lyydfys/aether-cockpit}
DEST=/opt/aether-site/downloads
TAG=${1:-latest}
API="https://api.github.com/repos/${REPO}/releases/${TAG}"

mkdir -p "$DEST"

echo "== 查询 release: $API"
JSON=$(curl -fsSL --max-time 30 "$API")

# 取 tag_name 和每个附件的 name/size/browser_download_url
echo "$JSON" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("TAG\t"+d.get("tag_name",""))
for a in d.get("assets",[]):
    print("ASSET\t%s\t%d\t%s" % (a["name"], a["size"], a["browser_download_url"]))
' > /tmp/release.tsv || { echo "!! release 解析失败"; exit 1; }

TAGNAME=$(awk -F'\t' '$1=="TAG"{print $2}' /tmp/release.tsv)
echo "== 版本: $TAGNAME"

changed=0
while IFS=$'\t' read -r kind name size url; do
  [ "$kind" = "ASSET" ] || continue

  # 只镜像安装包与校验文件，源码包不需要
  case "$name" in
    *.apk|*.zip|SHA256SUMS.txt) ;;
    *) echo "   跳过 $name"; continue ;;
  esac

  target="$DEST/$name"
  if [ -f "$target" ] && [ "$(stat -c%s "$target")" = "$size" ]; then
    echo "   已存在且大小一致，跳过 $name"
    continue
  fi

  echo "   下载 $name ($(( size / 1048576 )) MB)"
  curl -fsSL --max-time 600 --retry 3 --retry-delay 2 \
       -o "$target.part" "$url"
  mv "$target.part" "$target"
  changed=$((changed + 1))
done < /tmp/release.tsv

# 清理旧版本文件（只保留当前 release 的产物，避免磁盘堆积）
echo "== 清理非当前版本"
for f in "$DEST"/*.apk "$DEST"/*.zip; do
  [ -e "$f" ] || continue
  base=$(basename "$f")
  if ! grep -q "ASSET	$base	" /tmp/release.tsv; then
    echo "   删除旧版本 $base"
    rm -f "$f"
    changed=$((changed + 1))
  fi
done

# 写一个清单，供官网显示版本号与校验值
python3 - "$DEST" "$TAGNAME" /tmp/release.tsv <<'PY'
import hashlib, json, os, sys

dest, tag, tsv = sys.argv[1], sys.argv[2], sys.argv[3]
items = []
if os.path.exists(tsv):
    for line in open(tsv, encoding="utf-8"):
        parts = line.rstrip("\n").split("\t")
        if parts[0] != "ASSET":
            continue
        name, size = parts[1], int(parts[2])
        path = os.path.join(dest, name)
        if not os.path.exists(path):
            continue
        h = hashlib.sha256()
        with open(path, "rb") as fh:
            for chunk in iter(lambda: fh.read(1 << 20), b""):
                h.update(chunk)
        items.append(
            {
                "name": name,
                "size": os.path.getsize(path),
                "sha256": h.hexdigest(),
                "url": "/download/" + name,
            }
        )

manifest = {"version": tag, "assets": items}
out = os.path.join(dest, "manifest.json")
json.dump(manifest, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print("== 清单已写入", out)
for it in items:
    print("   %-50s %6.1f MB  %s" % (it["name"], it["size"] / 1048576, it["sha256"][:16] + "…"))
PY

echo "== 完成，本次变动 $changed 项"
du -sh "$DEST"
