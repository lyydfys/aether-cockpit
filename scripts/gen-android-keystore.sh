#!/usr/bin/env bash
set -euo pipefail

# 本地一次性生成 Android release keystore，并把 base64 打到终端，供粘贴到
# GitHub Secrets 的 AETHER_RELEASE_KEYSTORE_BASE64。
#
# 安全边界：
#   - 不写入仓库（默认路径在 $HOME，且 .gitignore 已忽略 *.jks / *.keystore / signing.properties）；
#   - 不打印密码，只在 keytool 参数里短暂使用；
#   - 只应在本地终端运行，不要把输出贴进 CI 日志或 issue。
#
# 用法：bash scripts/gen-android-keystore.sh [输出路径]
# 依赖：JAVA_HOME/bin/keytool 或 PATH 里的 keytool。

if [[ -n "${CI:-}" ]]; then
  echo "拒绝在 CI 中生成/打印 keystore；请在本机终端运行。" >&2
  exit 1
fi

OUT="${1:-$HOME/aether-release.jks}"
ALIAS="${AETHER_RELEASE_KEY_ALIAS:-aether-release}"

if [[ -e "$OUT" ]]; then
  echo "拒绝覆盖已存在的文件：$OUT" >&2
  exit 1
fi

if [[ -n "${JAVA_HOME:-}" && -x "$JAVA_HOME/bin/keytool" ]]; then
  KEYTOOL="$JAVA_HOME/bin/keytool"
elif command -v keytool >/dev/null 2>&1; then
  KEYTOOL="$(command -v keytool)"
else
  echo "找不到 keytool；请设置 JAVA_HOME 或把 JDK 的 bin 加进 PATH" >&2
  exit 1
fi

read -rsp "Keystore password: " STORE_PASS
echo
read -rsp "Key password (直接回车 = 与 keystore 相同): " KEY_PASS
echo
if [[ -z "$STORE_PASS" ]]; then
  echo "密码不能为空" >&2
  exit 1
fi
if [[ -z "$KEY_PASS" ]]; then
  KEY_PASS="$STORE_PASS"
fi

umask 077
mkdir -p "$(dirname "$OUT")"
"$KEYTOOL" -genkeypair \
  -keystore "$OUT" \
  -storetype PKCS12 \
  -alias "$ALIAS" \
  -keyalg RSA \
  -keysize 4096 \
  -validity 10000 \
  -storepass "$STORE_PASS" \
  -keypass "$KEY_PASS" \
  -dname "CN=Aether Cockpit, OU=Mobile, O=Aether Cockpit, C=CN"

echo
echo "keystore: $OUT"
echo "alias: $ALIAS"
echo
echo "把下面这一行完整复制到 GitHub Secret AETHER_RELEASE_KEYSTORE_BASE64（只输出一次）："
base64 "$OUT" | tr -d '\n'
echo
echo
echo "还需要在 GitHub 配置："
echo "  AETHER_RELEASE_STORE_PASSWORD  (刚才的 keystore password)"
echo "  AETHER_RELEASE_KEY_ALIAS       ($ALIAS)"
echo "  AETHER_RELEASE_KEY_PASSWORD    (刚才的 key password)"