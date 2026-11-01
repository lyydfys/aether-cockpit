#!/usr/bin/env bash
# 桌面端一键构建：wails build 产出 build/bin，再同步到 bin/（用户实际启动的位置），并做标记校验。
# 用法：npm run build:desktop
set -euo pipefail
cd "$(dirname "$0")/.."

MARKER=${1:-usedPct}

echo "· wails build ..."
wails build -skipbindings -o AetherCockpit-next.exe >/dev/null

SRC="build/bin/AetherCockpit-next.exe"
DST="bin/AetherCockpit.exe"
[ -f "$SRC" ] || { echo "✗ 构建产物缺失：$SRC"; exit 1; }

# 覆盖运行中的 exe 会失败：先改名旧的（Windows 允许重命名运行中的文件），再放入新的
if tasklist 2>/dev/null | grep -qi "AetherCockpit.exe"; then
  echo "· 桌面端正在运行，先改名旧文件（关闭窗口后生效）"
  rm -f "${DST%.exe}.stale.exe"
  mv -f "$DST" "${DST%.exe}.stale.exe" 2>/dev/null || true
else
  rm -f "$DST"
fi
cp -f "$SRC" "$DST"
rm -f "$SRC"

n=$(grep -a -c "$MARKER" "$DST" || true)
if [ "$n" -ge 1 ]; then
  echo "✓ $DST 已更新（标记 $MARKER ×$n）"
else
  echo "✗ $DST 里找不到标记 $MARKER，可能构建用错了资源目录"
  exit 1
fi
echo "· 重启桌面端即可看到新界面"
