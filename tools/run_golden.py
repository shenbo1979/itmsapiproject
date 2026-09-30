#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""老接口黄金用例回放器（M0 交付物④）。

用途：
  1) 从兼容清单（tests/golden/routes.json）生成用例骨架；
  2) 对目标服务回放用例，校验 HTTP 状态 / `code` / 必需字段 / 与录制结果的一致性。

安全默认：
  * 默认目标 http://127.0.0.1:18092（本工程骨架服务，不动线上）；
  * 指向非本机地址必须显式加 `--allow-remote`，避免误操作原系统。

用法：
  python3 tools/run_golden.py --from-inventory          # 生成/刷新用例骨架
  python3 tools/run_golden.py --status                  # 只看骨架进度
  python3 tools/run_golden.py --base-url http://127.0.0.1:18092 --module switch
  python3 tools/run_golden.py --record tests/golden/records   # 录制基线（慎用，需授权）
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROUTES_JSON = os.path.join(PROJECT_ROOT, "tests", "golden", "routes.json")
CASES_DIR = os.path.join(PROJECT_ROOT, "tests", "golden", "cases")
SKELETON = os.path.join(CASES_DIR, "_skeleton.json")
DEFAULT_BASE_URL = "http://127.0.0.1:18092"

# 各接口的默认探测参数（老前端常见形态：nAct 驱动动作）
DEFAULT_QUERY = {"nAct": "0"}


def load_skeleton() -> dict:
    if not os.path.exists(SKELETON):
        return {"generated": True, "cases": []}
    with open(SKELETON, encoding="utf-8") as fh:
        return json.load(fh)


def write_skeleton(payload: dict) -> None:
    os.makedirs(CASES_DIR, exist_ok=True)
    with open(SKELETON, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2)
        fh.write("\n")


def build_skeleton_from_inventory() -> dict:
    if not os.path.exists(ROUTES_JSON):
        raise SystemExit("缺少 tests/golden/routes.json，请先运行 python3 tools/api_inventory.py")
    with open(ROUTES_JSON, encoding="utf-8") as fh:
        report = json.load(fh)

    previous = {case["path"]: case for case in load_skeleton().get("cases", [])}
    cases = []
    for record in report["records"]:
        path = record["path"]
        old = previous.get(path, {})
        cases.append({
            "path": path,
            "module": record.get("module", "unclassified"),
            "provider": "python" if path.startswith("/client/") else "go",
            "method": "POST",
            "query": old.get("query", dict(DEFAULT_QUERY)),
            "expect": old.get("expect", {
                "http": 200,
                "required": ["code", "message"],
                "note": "待补：录制基线后填写 code 与 data 字段要求",
            }),
            "status": old.get("status", "todo"),
            "used_by": record.get("old_web_owners", []),
        })
    return {"generated": True, "source": "tests/golden/routes.json", "cases": cases}


def http_post(base_url: str, path: str, query: dict, timeout: float):
    url = base_url.rstrip("/") + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    request = urllib.request.Request(url, data=b"", method="POST")
    request.add_header("Accept", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read().decode("utf-8", errors="replace")
            return response.status, body, ""
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", errors="replace"), ""
    except Exception as exc:  # noqa: BLE001
        return 0, "", str(exc)



def get_by_path(payload, dotted: str):
    node = payload
    for part in dotted.split("."):
        if isinstance(node, dict) and part in node:
            node = node[part]
        else:
            return None, False
    return node, True


def evaluate(case: dict, status: int, body: str):
    expect = case.get("expect", {})
    problems = []
    expected_http = expect.get("http", 200)
    if expected_http and status != expected_http:
        problems.append("HTTP %s != %s" % (status, expected_http))
    payload = None
    if body:
        try:
            payload = json.loads(body)
        except json.JSONDecodeError as exc:
            problems.append("响应不是 JSON：%s" % exc)
    if payload is None and expect.get("code") is not None:
        problems.append("缺少响应体")
    if isinstance(payload, dict):
        raw_code = payload.get("code")
        try:
            code_value = int(raw_code)
        except (TypeError, ValueError):
            code_value = None
        if expect.get("code") is not None and code_value != expect["code"]:
            problems.append("code %r != %r" % (raw_code, expect["code"]))
        for dotted in expect.get("required", []):
            _value, found = get_by_path(payload, dotted)
            if not found:
                problems.append("缺少字段 %s" % dotted)
    return (not problems), problems


def load_cases_file(path: str) -> list:
    """支持 --cases：文件可为 {cases:[...]} 或直接是数组。"""
    with open(path, encoding="utf-8") as fh:
        payload = json.load(fh)
    if isinstance(payload, dict):
        return payload.get("cases", [])
    return payload


def run_cases(args) -> int:
    if args.cases:
        cases = load_cases_file(args.cases)
    else:
        cases = load_skeleton().get("cases", [])
    if args.module:
        wanted = {m.strip() for m in args.module.split(",") if m.strip()}
        cases = [c for c in cases if c.get("module") in wanted]
    if args.provider:
        cases = [c for c in cases if c.get("provider") == args.provider]
    if args.limit:
        cases = cases[: args.limit]

    if not cases:
        print("没有可执行的用例：请先运行 --from-inventory，并用 --module/--provider 过滤")
        return 0

    if args.record:
        os.makedirs(args.record, exist_ok=True)
    passed, failed, skipped = 0, 0, 0
    report = []
    for case in cases:
        if case.get("status") == "skip" and not args.force:
            skipped += 1
            continue
        status, body, error = http_post(args.base_url, case["path"], case.get("query", {}),
                                        args.timeout)
        if error:
            ok, problems = False, ["请求失败：%s" % error]
        else:
            ok, problems = evaluate(case, status, body)
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
        report.append({"path": case["path"], "module": case.get("module"),
                       "status": status, "ok": ok, "problems": problems})
        if args.record and not error:
            name = case["path"].strip("/").replace("/", "_") or "root"
            with open(os.path.join(args.record, name + ".json"), "w", encoding="utf-8") as fh:
                json.dump({"path": case["path"], "query": case.get("query", {}),
                           "http": status, "body": body}, fh, ensure_ascii=False, indent=2)
        print("%-4s %-52s %s" % ("PASS" if ok else "FAIL", case["path"],
                                 "；".join(problems)[:80]))

    print("\n结果：通过 %d，失败 %d，跳过 %d（目标 %s）" % (passed, failed, skipped, args.base_url))
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"base_url": args.base_url, "passed": passed, "failed": failed,
                       "skipped": skipped, "results": report}, fh, ensure_ascii=False, indent=2)
    return 1 if failed and args.strict else 0


def main() -> int:
    parser = argparse.ArgumentParser(description="老接口黄金用例回放器")
    parser.add_argument("--from-inventory", action="store_true", help="从兼容清单生成用例骨架")
    parser.add_argument("--status", action="store_true", help="只打印骨架统计")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="目标服务地址")
    parser.add_argument("--allow-remote", action="store_true", help="允许访问非本机地址（默认禁止）")
    parser.add_argument("--module", default="", help="按模块过滤，如 switch,olt,tr069")
    parser.add_argument("--provider", default="", choices=["", "go", "python"], help="按提供方过滤")
    parser.add_argument("--limit", type=int, default=0, help="只跑前 N 条")
    parser.add_argument("--timeout", type=float, default=8.0, help="单请求超时（秒）")
    parser.add_argument("--record", default="", help="把响应录制到该目录")
    parser.add_argument("--json", default="", help="把汇总写入 JSON 文件")
    parser.add_argument("--cases", default="", help="指定用例文件（默认用 tests/golden/cases/_skeleton.json）")
    parser.add_argument("--force", action="store_true", help="忽略 status=skip")
    parser.add_argument("--strict", action="store_true", help="有失败则返回非 0")
    args = parser.parse_args()

    if args.from_inventory:
        payload = build_skeleton_from_inventory()
        write_skeleton(payload)
        print("已生成 %s（%d 条用例）" % (os.path.relpath(SKELETON, "/"), len(payload["cases"])))
        return 0

    if args.status:
        cases = load_skeleton().get("cases", [])
        stats = {}
        for case in cases:
            key = (case.get("module", "?"), case.get("status", "?"))
            stats[key] = stats.get(key, 0) + 1
        print("用例总数 %d" % len(cases))
        for (module, status), count in sorted(stats.items()):
            print("  %-14s %-6s %d" % (module, status, count))
        return 0

    host = urllib.parse.urlparse(args.base_url).hostname or ""
    if host not in ("127.0.0.1", "localhost", "::1") and not args.allow_remote:
        print("目标 %s 不是本机地址：为避免影响原系统，请显式加 --allow-remote" % args.base_url,
              file=sys.stderr)
        return 2
    return run_cases(args)


if __name__ == "__main__":
    raise SystemExit(main())
