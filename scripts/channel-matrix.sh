#!/usr/bin/env bash
# 逐渠道实测：对每个已配置账号各发一个**极小**真实请求（max_tokens=16），
# 打印 渠道/账号/模型/HTTP/耗时/原因。配额恢复后可重复跑。
#
#   ./scripts/channel-matrix.sh                # 默认 http://127.0.0.1:7860
#   ADDR=http://127.0.0.1:7988 ./scripts/channel-matrix.sh
#   INCLUDE_MOCK=1 ./scripts/channel-matrix.sh # 连 mock-* 一起测
#
# 注意：这会对真实账号消耗极少量额度；mock/假上游不花真实额度。
set -u
ADDR="${ADDR:-http://127.0.0.1:7860}"
INCLUDE_MOCK="${INCLUDE_MOCK:-0}" python - "$ADDR" <<'PY'
import json, os, sys, time, urllib.request, urllib.error

addr = sys.argv[1].rstrip('/')
include_mock = os.environ.get('INCLUDE_MOCK') == '1'


def get(path):
    with urllib.request.urlopen(addr + path, timeout=30) as r:
        return json.load(r)


def pick_model(models, provider):
    for mid in models:
        if mid.startswith(provider + '/'):
            return mid
    return None


try:
    accounts = get('/api/accounts')['accounts']
    models = [m['id'] for m in get('/v1/models')['data']]
except Exception as e:
    print('无法读取网关：', e)
    sys.exit(2)

rows, passed = [], 0
for a in accounts:
    prov, label, state = a['provider_id'], a.get('label', ''), a['state']
    if prov.startswith('mock') and not include_mock:
        continue
    model = pick_model(models, prov)
    if not model:
        rows.append((prov, label, '-', '-', '-', '该渠道没有模型'))
        continue
    body = json.dumps({'model': model, 'max_tokens': 16,
                       'messages': [{'role': 'user', 'content': 'ping'}]}).encode()
    req = urllib.request.Request(addr + '/v1/chat/completions', data=body,
                                 headers={'Content-Type': 'application/json'})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            code = r.status
            raw = r.read()
    except urllib.error.HTTPError as e:
        code, raw = e.code, e.read()
    except Exception as e:
        code, raw = 0, str(e).encode()
    ms = int((time.time() - t0) * 1000)
    why = ''
    try:
        j = json.loads(raw)
        if j.get('error'):
            why = str(j['error'].get('message', j['error']))[:110].replace('\n', ' ')
        else:
            why = 'ok'
    except Exception:
        why = raw[:110].decode('utf-8', 'replace').replace('\n', ' ')
    ok = code == 200
    passed += 1 if ok else 0
    rows.append((prov, label, model, str(code), f'{ms}ms', why))

width = max((len(r[0]) for r in rows), default=4)
print(f"{'渠道':<{width}}  账号            模型                               HTTP  耗时    结果")
for r in rows:
    print(f'{r[0]:<{width}}  {r[1][:14]:<14}  {r[2][:32]:<32}  {r[3]:<4}  {r[4]:<6}  {r[5]}')
print(f'\n合计 {len(rows)} 个账号，200 通过 {passed} 个；其余为账号侧限额/冷却或网关未配置。')
PY
