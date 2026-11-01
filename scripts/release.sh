#!/usr/bin/env bash
# 构建可分发产物：桌面端 + 无头 CLI，打 zip 并生成校验和。
#
#   ./scripts/release.sh              # 先跑完整门禁，再打包
#   ./scripts/release.sh --skip-gate  # 跳过门禁（仅本地试打包用）
#
# 版本号来自仓库根 VERSION；构建时间与版本注入 /version 与 Windows 文件属性。
set -euo pipefail
cd "$(dirname "$0")/.."

SKIP_GATE=0
for arg in "$@"; do
  case "$arg" in
    --skip-gate) SKIP_GATE=1 ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

VER=$(tr -d '\r\n' < VERSION 2>/dev/null || true)
if [ -z "$VER" ]; then
  echo "VERSION 文件缺失或为空" >&2
  exit 1
fi
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)

export GOCACHE="${GOCACHE:-D:/gocache}"
export TMP="${TMP:-D:/tmp}" TEMP="${TEMP:-D:/tmp}" TMPDIR="${TMPDIR:-D:/tmp}"

if [ "$SKIP_GATE" = "0" ]; then
  echo "== 发布前门禁 =="
  ./scripts/gate.sh
fi

NAME="aether-cockpit-$VER-windows-amd64"
DIST="dist"
DIR="$DIST/$NAME"
rm -rf "$DIR"
mkdir -p "$DIR/docs"

LDF="-s -w -X aether-cockpit/internal/core/gateway.BuildVersion=$VER -X aether-cockpit/internal/core/gateway.BuildTime=$TS"
LDF_GUI="-H windowsgui $LDF"

echo "== 构建桌面端 =="
go build -tags desktop,production -ldflags "$LDF_GUI" -o "$DIR/AetherCockpit.exe" ./cmd/desktop
echo "== 构建无头 CLI =="
go build -ldflags "$LDF" -o "$DIR/aether-cockpit-cli.exe" ./cmd/cockpit

cp README.md README.en.md VERSION CHANGELOG.md NOTICE LICENSE PRIVACY.md SECURITY.md SUPPORT.md CONTRIBUTING.md CODE_OF_CONDUCT.md "$DIR/"
cp docs/README.md docs/QUICKSTART.md docs/FEATURES.md docs/ADD_PROVIDER.md docs/ARCHITECTURE.md docs/FRONTEND.md docs/DONATE.md "$DIR/docs/"
mkdir -p "$DIR/docs/images"
cp docs/images/alipay.jpg docs/images/wechat.jpg "$DIR/docs/images/"
mkdir -p "$DIR/docs/images/screenshots"
cp docs/images/screenshots/*.png "$DIR/docs/images/screenshots/"
mkdir -p "$DIR/licenses"
cp internal/web/ui2/assets/fonts/OFL*.txt "$DIR/licenses/"
mkdir -p "$DIR/installer"
cp packaging/install.ps1 packaging/uninstall.ps1 "$DIR/installer/"

( cd "$DIR" && sha256sum *.exe > SHA256SUMS.txt )

python - "$DIR" "$DIST/$NAME.zip" <<'PY'
import os
import sys
import zipfile

src, out = sys.argv[1], sys.argv[2]
root_parent = os.path.dirname(src)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(src):
        for name in sorted(files):
            p = os.path.join(root, name)
            z.write(p, os.path.relpath(p, root_parent))
print("zip:", out)
PY

( cd "$DIST" && sha256sum "$NAME.zip" > SHA256SUMS.txt )

cat > "$DIST/RELEASE-$VER.md" <<EOF
# Aether Cockpit $VER

- 版本：$VER
- 构建时间：$TS
- 平台：windows/amd64
- 桌面端：AetherCockpit.exe（Wails 壳，含托盘/额度悬浮窗）
- 无头网关：aether-cockpit-cli.exe（-addr / -config，适合服务器或脚本）
- 校验和：见同目录 SHA256SUMS.txt

## 安装（可选）

不想每次手动解压的话，用用户级安装脚本（无需管理员）：

    powershell -ExecutionPolicy Bypass -File installer/install.ps1              # 安装到 %LOCALAPPDATA%/Programs/AetherCockpit
    powershell -ExecutionPolicy Bypass -File installer/install.ps1 -AutoStart   # 同时设置登录自启

卸载：设置 → 应用 → Aether Cockpit，或运行安装目录下的 uninstall.ps1（-KeepData 保留 data/）。
也可以直接双击 AetherCockpit.exe 便携使用。

## 首次运行

1. 双击 AetherCockpit.exe；默认仅监听 127.0.0.1:7860。
2. 「设置 → 常规」设置网关密钥（局域网访问必填）。
3. 「接入渠道」添加账号；「网关与探针」复制接入片段。
4. 详细步骤见包内 docs/QUICKSTART.md。

## 数据

默认在当前工作目录的 data/（便携）；删除该目录即重置本机数据。
EOF

echo
echo "== 产物 =="
ls -lh "$DIST/$NAME.zip" "$DIST/RELEASE-$VER.md" "$DIST/SHA256SUMS.txt"
cat "$DIST/SHA256SUMS.txt"
