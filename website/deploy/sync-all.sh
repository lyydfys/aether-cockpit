#!/usr/bin/env bash
# 发版后一条命令同步官网：安装包 + 更新日志。
#
#   bash /opt/aether-site/sync-all.sh          # 同步最新 release
#   bash /opt/aether-site/sync-all.sh v0.4.1   # 指定 tag
#
# 两步都幂等：文件大小没变就跳过，不会重复下载。
# 任何一步失败都不会继续，避免出现"安装包更新了但日志没更新"的半状态。

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
DEST="$HERE/downloads"
TAG="${1:-latest}"

log() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
die() { printf '\n\033[31m!! %s\033[0m\n' "$*" >&2; exit 1; }

# ---------- 前置检查 ----------
log "前置检查"
command -v curl >/dev/null || die "缺少 curl"
command -v python3 >/dev/null || die "缺少 python3"
[ -f "$HERE/sync-downloads.sh" ] || die "缺少 sync-downloads.sh"
[ -f "$HERE/sync-changelog.py" ] || die "缺少 sync-changelog.py"

# GitHub API 有匿名限流（60 次/小时）。先探一下，避免跑到一半才失败。
if ! curl -fsS --max-time 20 -o /dev/null \
     "https://api.github.com/repos/lyydfys/aether-cockpit"; then
  die "GitHub API 不可达或已限流，稍后重试"
fi
echo "   GitHub API 可达"

# 记录同步前的包名集合，用于最后汇报"到底变了什么"。
# 不能用 manifest.json 的 mtime —— 它每次都会被重写，"没变"也会报成"已更新"。
before_list=$(mktemp)
ls -1 "$DEST"/*.apk "$DEST"/*.zip 2>/dev/null | xargs -r -n1 basename | sort > "$before_list" || true

# ---------- 第一步：安装包 ----------
log "1/2 同步安装包"
bash "$HERE/sync-downloads.sh" "$TAG"

# ---------- 第二步：更新日志 ----------
log "2/2 同步更新日志"
OUT_DIR="$DEST" python3 "$HERE/sync-changelog.py"
[ -f "$DEST/releases.json" ] || die "releases.json 未生成"

# ---------- 汇报 ----------
log "结果"
after_list=$(mktemp)
ls -1 "$DEST"/*.apk "$DEST"/*.zip 2>/dev/null | xargs -r -n1 basename | sort > "$after_list" || true
after_pkgs=$(wc -l < "$after_list")

if diff -q "$before_list" "$after_list" >/dev/null 2>&1; then
  echo "   安装包：无变化（当前 $after_pkgs 个）"
else
  echo "   安装包：已更新（当前 $after_pkgs 个）"
  echo "     新增："
  comm -13 "$before_list" "$after_list" | sed 's/^/       + /'
  echo "     移除："
  comm -23 "$before_list" "$after_list" | sed 's/^/       - /'
fi
rm -f "$before_list" "$after_list"

python3 - "$DEST" <<'PY'
import json, os, sys
d = sys.argv[1]
try:
    rel = json.load(open(os.path.join(d, "releases.json"), encoding="utf-8"))
    man = json.load(open(os.path.join(d, "manifest.json"), encoding="utf-8"))
except Exception as e:
    print("   读取清单失败:", e)
    sys.exit(0)
rs = rel.get("releases", [])
print("   更新日志：%d 个版本，最新 %s" % (len(rs), rs[0]["version"] if rs else "?"))
print("   当前版本：%s，%d 个安装包" % (man.get("version", "?"), len(man.get("assets", []))))
PY

echo "   磁盘占用：$(du -sh "$DEST" | cut -f1)"

# ---------- 让线上生效 ----------
# 容器把 downloads 目录挂成只读卷，文件换掉即生效，不需要重建镜像。
# index.html 是 no-cache，清单也是 no-cache，所以访问即可看到新内容。
log "完成"
echo "   容器未重启：downloads 是只读挂载，文件替换后立即生效"
echo "   验证： curl -s https://aether.mac-api.xyz/download/manifest.json"
