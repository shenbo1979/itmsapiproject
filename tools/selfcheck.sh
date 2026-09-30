#!/usr/bin/env bash
# 骨架自检脚本：在独立端口起一个临时实例，只清理自己启动的进程。
#
# 为什么不用 pkill：make run / 演示实例也占用默认端口，早期版本的 selfcheck 会
# 把别人的实例一并杀掉（并可能让请求落到"别人的进程"上产生假结果）。本脚本：
#   1. 先检查端口是否被占用，被占用直接报错退出（不抢、不杀）；
#   2. 用自己的 PID 记录，结束时只 kill 这一个进程；
#   3. 运行数据全部落在 mktemp 临时目录，自检结束即清理。
#
# 用法：
#   tools/selfcheck.sh            # 未注册自检（-3 守卫 / -4 语义 / 注册接口）
#   tools/selfcheck.sh switch     # 先注册，再跑 switch 模块冒烟（真实接口 + 未迁入接口仍为过渡）
# 环境变量：
#   SELFCHECK_PORT  自检端口（默认 18093；与 make run 的 18092 错开）
#   TXAPI_MODULES   启用模块（默认全部）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${SELFCHECK_PORT:-18093}"
MODULES="${TXAPI_MODULES:-switch,olt,tr069}"
MODE="${1:-guard}"
BIN="$ROOT/dist/txapi"
DIR="$(mktemp -d /tmp/txapi-selfcheck.XXXXXX)"
PID=""

cleanup() {
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null || true
  fi
  rm -rf "$DIR"
}
trap cleanup EXIT

if [ ! -x "$BIN" ]; then
  echo "缺少 $BIN，请先运行：make build" >&2
  exit 1
fi

if command -v ss >/dev/null 2>&1 && ss -lntp 2>/dev/null | grep -q ":${PORT} "; then
  echo "端口 ${PORT} 已被占用（不会影响正在运行的服务）：" >&2
  ss -lntp | grep ":${PORT} " >&2
  echo "请换一个端口重试：SELFCHECK_PORT=18096 make selfcheck" >&2
  exit 2
fi

mkdir -p "$DIR/data" "$DIR/reg"
TXAPI_PORT="$PORT" TXAPI_MODULES="$MODULES" \
TXAPI_DATA_DIR="$DIR/data" TXAPI_REGISTRATION_DIR="$DIR/reg" \
  nohup "$BIN" > "$DIR/txapi.log" 2>&1 &
PID=$!

ready=""
for _ in $(seq 1 20); do
  if curl -sf "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    ready="yes"
    break
  fi
  sleep 0.5
done
if [ -z "$ready" ]; then
  echo "骨架服务未就绪（端口 ${PORT}），日志尾部：" >&2
  tail -20 "$DIR/txapi.log" >&2
  exit 1
fi

if [ "$MODE" = "switch" ]; then
  key="$(python3 -c 'print("a" * 32)')"
  now="$(date +%s%3N)"
  body="$(printf '{"szCompanyName":"自检","szAesKey":"%s","nBrowserTime":%s}' "$key" "$now")"
  code="$(curl -s -X POST "http://127.0.0.1:${PORT}/api/v2/platform/registration/" \
    --data-urlencode 'nAct=1' --data-urlencode "saveData=${body}" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin).get("code"))')"
  if [ "$code" != "0" ]; then
    echo "自检注册失败（code=${code}），日志尾部：" >&2
    tail -20 "$DIR/txapi.log" >&2
    exit 1
  fi
  CASES="$ROOT/tests/golden/cases/switch-smoke.json"
  echo "已注册自检环境（端口 ${PORT}，隔离目录 ${DIR}），开始回放 switch 冒烟用例..."
else
  CASES="$ROOT/tests/golden/cases/selfcheck.json"
  echo "未注册自检环境（端口 ${PORT}，隔离目录 ${DIR}），开始回放自检用例..."
fi

python3 "$ROOT/tools/run_golden.py" --cases "$CASES" --strict --base-url "http://127.0.0.1:${PORT}"
