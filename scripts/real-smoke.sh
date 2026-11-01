#!/usr/bin/env bash
# 用真实账号的隔离副本跑完整验收（不写 data/，跑完删除副本）。
#
#   ./scripts/real-smoke.sh                        # 默认 qoder-cn/DeepSeek-V4-Flash
#   ./scripts/real-smoke.sh mock-native/echo-pro   # 只想验证脚本本身
#   PORT=7999 ./scripts/real-smoke.sh
#
# 说明：每次真实 smoke 会发 10+ 个最小请求，消耗该渠道少量额度；
# 副本里是加密凭证，跑完（含失败）trap 会删掉整个 build/probe。

set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${PORT:-7988}"
MODEL="${1:-qoder-cn/DeepSeek-V4-Flash}"
DIR="build/probe"

# 桌面端实际从 bin/ 启动，账号库在 bin/data；源码直跑时才用 data/。
DATA_DIR="${AETHER_SMOKE_DATA_DIR:-}"
if [ -z "$DATA_DIR" ]; then
  if [ -f bin/data/accounts.json ]; then DATA_DIR="bin/data"; else DATA_DIR="data"; fi
fi

# mock 运行只验证脚本本身：不读用户账号库，构建带 dev_mock 的探针二进制，
# 起来后用 API 现场种 mock 账号（产品产物默认不含假上游）。
MOCK_RUN=0
case "$MODEL" in mock*) MOCK_RUN=1 ;; esac

if [ "$MOCK_RUN" = "0" ] && [ ! -f "$DATA_DIR/accounts.json" ]; then
  echo "没有 $DATA_DIR/accounts.json：先在本机添加账号。" >&2
  exit 1
fi

rm -rf "$DIR"
mkdir -p "$DIR"
if [ "$MOCK_RUN" = "0" ]; then
  cp "$DATA_DIR/accounts.json" "$DIR/accounts.json"
fi
[ -f "$DATA_DIR/checkin.json" ] && cp "$DATA_DIR/checkin.json" "$DIR/checkin.json" || true
cat > "$DIR/cockpit.json" <<JSON
{"addr":"127.0.0.1:$PORT","snapshot_path":"$DIR/state.json","sqlite_path":"$DIR/state.db",
 "account_store_path":"$DIR/accounts.json","settings_path":"$DIR/settings.json",
 "usage_path":"$DIR/usage.json","spec_dir":"configs/specs",
 "quota_refresh_minutes":-1,"token_refresh_minutes":-1}
JSON

if [ "$MOCK_RUN" = "1" ]; then
  go build -tags dev_mock -o "$DIR/cockpit.exe" ./cmd/cockpit
else
  go build -o "$DIR/cockpit.exe" ./cmd/cockpit
fi
"$DIR/cockpit.exe" -config "$DIR/cockpit.json" -checkin-state "$DIR/checkin.json" > "$DIR/gw.log" 2>&1 &
PID=$!
cleanup() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  # Windows 上句柄释放略滞后：重试几次再删（副本里是加密凭证，必须删干净）。
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    rm -rf "$DIR" 2>/dev/null && return 0
    sleep 0.3
  done
  echo "警告：$DIR 未能删除，请手动清理（内含加密凭证副本）" >&2
}
trap cleanup EXIT

for _ in $(seq 1 40); do
  # 探针进程先死了（最常见：端口被别的进程占住，bind 失败）：
  # 立刻停下并打日志，否则健康检查会打到别人的网关上，跑出一串假失败。
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "探针网关启动失败（端口 $PORT 可能被占用）。日志：" >&2
    tail -20 "$DIR/gw.log" >&2
    exit 1
  fi
  if curl -sf -o /dev/null "http://127.0.0.1:$PORT/api/health"; then break; fi
  sleep 0.5
done
if ! kill -0 "$PID" 2>/dev/null; then
  echo "探针网关中途退出。日志：" >&2
  tail -20 "$DIR/gw.log" >&2
  exit 1
fi
if ! curl -sf -o /dev/null "http://127.0.0.1:$PORT/api/health"; then
  echo "网关未就绪，日志：" >&2
  tail -20 "$DIR/gw.log" >&2
  exit 1
fi

if [ "$MOCK_RUN" = "1" ]; then
  for pid in mock-native mock-weak mock-responses; do
    curl -sS -X POST "http://127.0.0.1:$PORT/api/accounts"       -H 'Content-Type: application/json'       -d "{\"provider_id\":\"$pid\",\"label\":\"smoke-$pid\",\"credentials\":{\"token\":\"smoke\"}}" >/dev/null || true
  done
fi

if ! go run ./cmd/smoke -addr "http://127.0.0.1:$PORT" -model "$MODEL"; then
  # 失败时先留现场：探针网关若中途退出，答案只在 gw.log 里，而 trap 会删掉
  # 整个目录（2026-10-28 就因为现场被删，一次 smoke 中途掉线无法定性）。
  echo "---- 验收未通过：探针网关日志尾部 ----" >&2
  tail -40 "$DIR/gw.log" >&2 || true
  exit 1
fi
