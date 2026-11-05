#!/usr/bin/env bash
# 升级版本号：同步 VERSION 与 Wails 包元数据，随后请更新 CHANGELOG 并跑 release。
set -euo pipefail
cd "$(dirname "$0")/.."
VER="${1:-}"
if ! [[ "$VER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "用法: scripts/bump.sh <x.y.z>" >&2
  exit 2
fi
node - "$VER" <<'NODE'
const fs = require("node:fs");
const version = process.argv[2];
const path = "wails.json";
const metadata = JSON.parse(fs.readFileSync(path, "utf8"));
metadata.info.productVersion = version;
fs.writeFileSync(path, `${JSON.stringify(metadata, null, 2)}\n`);
fs.writeFileSync("VERSION", `${version}\n`);

// 移动端：versionName 跟随同一个版本号，versionCode 单调递增。
// 之前这里漏了移动端，导致 tag 校验（桌面 VERSION）与 APK 版本号（version.properties）漂移，
// v0.2.0 第一次发版就是因此失败的。
const propsPath = "mobile/android/version.properties";
const props = fs.readFileSync(propsPath, "utf8");
const codeMatch = props.match(/^versionCode=(\d+)$/m);
const nextCode = codeMatch ? Number(codeMatch[1]) + 1 : 1;
const updated = props
  .replace(/^versionCode=\d+$/m, `versionCode=${nextCode}`)
  .replace(/^versionName=.*$/m, `versionName=${version}`);
fs.writeFileSync(propsPath, updated);
console.log(`mobile version.properties -> versionName=${version} versionCode=${nextCode}`);
NODE
echo "VERSION -> $VER"
echo "wails.json productVersion -> $VER"
echo "mobile/android/version.properties versionName -> $VER（versionCode 自动 +1）"
echo "下一步：在 CHANGELOG.md 增加本次版本条目，然后创建 v$VER 标签以触发 GitHub Release。"
