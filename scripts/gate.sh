#!/usr/bin/env bash
# 一条命令跑完本项目门禁。用法：
#   ./scripts/gate.sh              # 磁盘 + vet + 重编 + 串行全量 + mock smoke + 产物哈希
#   ./scripts/gate.sh --no-build   # 不重编（只验源码；产物哈希可能与源码不一致）
#   ./scripts/gate.sh --quick      # 跳过 mock smoke（仍需先起网关时用）
#   SKIP_TESTS=1 ./scripts/gate.sh # 只跑磁盘/vet/重编/smoke/产物
#
# 为什么是这样（都是踩过的坑）：
#  - 先看磁盘：C 盘写满时 link.exe 会报 "not enough space"，
#    看起来像测试挂了；Go 构建缓存在 %LOCALAPPDATA%\go-build。
#  - 串行 -p 1：并行跑偶发的 ".test build failed" 是磁盘/资源问题，
#    不是代码缺陷；门禁以串行为准。
#  - 默认重编：否则"门禁全绿"只证明源码，bin/ 里的产物可能落后于它。
#    重编注入真实 BuildTime（/version 的 build_time_source 会是 ldflags）。
#  - 计数用 "^ok" 与 "no test files"，不要用 === RUN 数子测试。
set -u
cd "$(dirname "$0")/.." || exit 2

quick=0
nobuild=0
for a in "$@"; do
  case "$a" in
    --quick) quick=1 ;;
    --no-build) nobuild=1 ;;
  esac
done
fail=0

echo "== 1. 磁盘 =="
df -h /c | tail -1
avail_kb=$(df -P /c | awk 'NR==2 {print $4}')
# 1.3 GB 时实测会在链接峰值 ENOSPC（§二百八十一）；2.8 GB 实测能跑完。
# 所以：<2 GB 直接失败；2–3 GB 只警告，让人知道余量不宽。
if [ -z "${avail_kb:-}" ] || [ "$avail_kb" -lt 2097152 ]; then
  # C 盘吃紧时不要把门禁判死：把 Go 构建缓存挪到 D 盘即可（不删任何人的文件）。
  d_kb=$(df -P /d 2>/dev/null | awk 'NR==2 {print $4}')
  if [ -n "${d_kb:-}" ] && [ "${d_kb:-0}" -gt 3145728 ]; then
    export GOCACHE="D:/gocache"
    echo "~~ C 盘可用不足 2 GB（${avail_kb} KB）：GOCACHE 改用 D:/gocache（D 盘可用 $((d_kb/1024/1024)) GB）"
  else
    echo "!! C 盘可用不足 2 GB，且 D 盘也放不下构建缓存：先清理 %TEMP% 或 go clean -cache"
    fail=1
  fi
elif [ "$avail_kb" -lt 3145728 ]; then
  echo "~~ C 盘可用不足 3 GB：能跑，但峰值余量不宽（实测 2.8 GB 可通过）"
fi

echo "== 1b. 禁止外部项目名硬锁 =="
bash scripts/check-no-external-project-names.sh || { echo "!! 外部项目名硬锁失败"; fail=1; }

echo "== 2. go vet =="
go vet ./... || { echo "!! vet 失败"; fail=1; }

if [ "$nobuild" -eq 0 ]; then
  echo "== 3a. Windows 图标资源 =="
  if go run ./cmd/winres-icon >/dev/null 2>&1; then
    echo "  syso ok"
  else
    echo "!! 图标资源生成失败"
    fail=1
  fi
  echo "== 3. 重编桌面端（注入版本与 BuildTime） =="
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  ver=$(tr -d '\r\n' < VERSION 2>/dev/null || true)
  [ -z "$ver" ] && ver=dev
  if go build -tags desktop,production \
    -ldflags "-H windowsgui -s -w -X aether-cockpit/internal/core/gateway.BuildVersion=$ver -X aether-cockpit/internal/core/gateway.BuildTime=$ts" \
    -o build/bin/AetherCockpit.exe ./cmd/desktop; then
    cp build/bin/AetherCockpit.exe bin/
    # 桌面端必须是 GUI 子系统（2）；console 子系统会在启动/子进程时弹控制台窗口。
    if ! python - build/bin/AetherCockpit.exe <<'PY'
import struct, sys
raw = open(sys.argv[1], 'rb').read()
pe = struct.unpack_from('<I', raw, 0x3c)[0]
opt = pe + 24
magic = struct.unpack_from('<H', raw, opt)[0]
subsystem = struct.unpack_from('<H', raw, opt + 68)[0]
if subsystem != 2:
    print('subsystem=%d（期望 2 GUI）' % subsystem)
    raise SystemExit(1)
PY
    then
      echo "!! 桌面端不是 GUI 子系统：会自带控制台窗口"
      fail=1
    fi
    # 记下本轮构建产物的哈希：并行工作线可能在门禁途中重写 build/bin，
    # 收尾时必须证明 bin/ 仍是本轮产物（2026-10-31 实测出现过两个不同哈希）。
    build_hash=$(sha256sum build/bin/AetherCockpit.exe | awk '{print $1}')
    echo "built $ver at $ts ($build_hash)"
  else
    echo "!! 构建失败"
    fail=1
  fi
else
  echo "== 3. 重编：--no-build，跳过（产物可能与源码不一致） =="
fi

if [ "${SKIP_TESTS:-0}" != "1" ]; then
  echo "== 4. 串行全量测试 =="
  out=$(mktemp)
  go test ./... -count=1 -shuffle=on -p 1 >"$out" 2>&1
  code=$?
  grep -E '^(FAIL|---)' "$out" | head -20
  echo "packages ok: $(grep -c '^ok' "$out")  no test files: $(grep -c 'no test files' "$out")"
  if [ "$code" -ne 0 ]; then
    echo "!! 测试失败，完整输出保留在 $out"
    fail=1
  else
    rm -f "$out"
  fi
else
  echo "== 4. 串行全量测试：SKIP_TESTS=1，跳过 =="
fi

if [ "${SKIP_TESTS:-0}" != "1" ]; then
  echo "== 4b. 前端单测 =="
  uiout=$(mktemp)
  if node --test scripts/ui-unit.mjs >"$uiout" 2>&1; then
    grep -E '^ℹ (tests|pass|fail)' "$uiout" | sed 's/^/  /'
    rm -f "$uiout"
  else
    tail -25 "$uiout"
    echo "!! 前端单测失败，完整输出保留在 $uiout"
    fail=1
  fi
fi

if [ "${SKIP_TESTS:-0}" != "1" ]; then
  echo "== 4c. 前端类型检查 =="
  tsout=$(mktemp)
  if npx --no-install tsc -p tsconfig.json >"$tsout" 2>&1; then
    echo "  tsc ok"
    rm -f "$tsout"
  else
    tail -30 "$tsout"
    echo "!! 类型检查失败，完整输出保留在 $tsout"
    fail=1
  fi
fi

if [ "$quick" -eq 0 ]; then
  echo "== 5. mock smoke（本地假上游，不消耗账号额度） =="
  smoke=$(mktemp)
  # 端口默认随机空闲：固定 7999 会被并行工作线或遗留进程占住，
  # smoke 会打到别人的网关上（2026-10-31 实测一次 11 项假失败）。
  SMOKE_PORT="${PORT:-$(python -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()' 2>/dev/null || echo 17999)}"
  PORT="$SMOKE_PORT" ./scripts/real-smoke.sh mock-native/echo-pro >"$smoke" 2>&1
  code=$?
  tail -4 "$smoke"
  if [ "$code" -ne 0 ]; then
    echo "!! smoke 失败，完整输出保留在 $smoke"
    fail=1
  else
    rm -f "$smoke"
  fi
else
  echo "== 5. mock smoke：--quick，跳过 =="
fi

echo "== 6. 产物 =="
sha256sum bin/AetherCockpit.exe build/bin/AetherCockpit.exe 2>/dev/null || echo "(产物不存在)"
current_bin=$(sha256sum bin/AetherCockpit.exe 2>/dev/null | awk '{print $1}')
if [ -n "${build_hash:-}" ] && [ "$current_bin" != "$build_hash" ]; then
  echo "!! bin/ 已不是本轮构建产物（构建 $build_hash，当前 ${current_bin:-缺失}）：并行工作线在门禁期间改写了产物，本门禁无效"
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "== 门禁通过 =="
else
  echo "== 门禁未通过：看上面以 !! 开头的行 =="
fi
exit "$fail"
