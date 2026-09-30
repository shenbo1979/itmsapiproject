#!/usr/bin/env python3
"""OLT 接口契约基线（M2 前置，全部只读老工程）。

为什么需要它：OLT 17 条的**参数名与 switch 不同**——switch 用 `nAct`，OLT 用 **`Act`**；
另有 `saveData` / `connData` / `trace` / `reqData` / `RegProFileId` 等，且逐条不同。
这些细节只存在于老代码里，靠记忆或推断必然出错，因此用工具固化并生成可评审的契约表。

输入（全部只读）：
  tests/golden/routes.json                        路径 ↔ handler（权威清单，由 api_inventory.py 生成）
  /switchProject/switchmain/olthandler.go         参数、Act 分支、是否连设备 / 是否写数据
  /switchProject/switchmain/main.go               路由注册（校验 handler 确实被注册）
  /fusiongateway/web/assets/js/chunk-itmsApi.js   老壳调用面（实际传了哪些参数）
  internal/modules/olt/module.go                  LegacyRoutes（模块声明，需与 routes.json 一致）
  deploy/modules/olt.manifest                     legacy_routes（打包清单，语义为「前缀」）

输出：
  docs/generated/olt-contract.md                  人读契约表（逐条：参数 / Act 分支 / 连设备 / 含写）
  tests/fixtures/olt-contract.json                机读契约（供黄金用例骨架与回归使用）

用法：
  python3 tools/olt_contract_scan.py            # 生成契约表
  python3 tools/olt_contract_scan.py --check    # 只比对（CI）：三方声明一致性 + 契约表是否最新
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

ROUTES_JSON = os.path.join(PROJECT_ROOT, "tests", "golden", "routes.json")
OLT_MODULE_GO = os.path.join(PROJECT_ROOT, "internal", "modules", "olt", "module.go")
OLT_MANIFEST = os.path.join(PROJECT_ROOT, "deploy", "modules", "olt.manifest")
OUT_MD = os.path.join(PROJECT_ROOT, "docs", "generated", "olt-contract.md")
OUT_JSON = os.path.join(PROJECT_ROOT, "tests", "fixtures", "olt-contract.json")

OLD_OLT_HANDLER = "/switchProject/switchmain/olthandler.go"
OLD_TR069_HANDLER = "/switchProject/switchmain/tr069handler.go"
OLD_MONOLITH_MAIN = "/switchProject/switchmain/main.go"
OLD_WEB_JS = "/fusiongateway/web/assets/js/chunk-itmsApi.js"

FUNC_RE = re.compile(r"^func\s+(?P<name>\w+)\s*\(", re.M)
FORM_VALUE_RE = re.compile(r'r\.FormValue\("(?P<name>[^"]+)"\)')
CASE_RE = re.compile(r"case\s+(?P<num>\d+)\s*:")
ACT_VAR_RE = re.compile(r'(?P<var>\w+)\s*,\s*\w+\s*:=\s*strconv\.Atoi\(\s*r\.FormValue\("Act"\)')
DEVICE_HINTS = ("connectOltTelnet", "newOltTelnetClient", "writeOltConnectionFailed", "executeCmds")
WRITE_HINTS = ("os.WriteFile", "os.MkdirAll", "os.Remove", "os.Rename", "ioutil.WriteFile")
SQL_WRITE_RE = re.compile(r"(?i)\b(INSERT\s+INTO|UPDATE\s+\w+\s+SET|DELETE\s+FROM)\b")
PARAM_KEYS = ("Act", "nAct", "saveData", "connData", "trace", "reqData", "RegProFileId", "refresh")


def read_text(path):
    if not os.path.exists(path):
        return ""
    with open(path, "r", encoding="utf-8", errors="ignore") as fh:
        return fh.read()


def function_bodies(path):
    """把源码切成 {函数名: 函数体}（以顶层 func 为界，够用且不依赖 AST）。"""
    text = read_text(path)
    matches = list(FUNC_RE.finditer(text))
    bodies = {}
    for index, match in enumerate(matches):
        start = match.start()
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        bodies[match.group("name")] = text[start:end]
    return bodies


def scan_handler(bodies, handler):
    """扫描单个 handler 的参数名、Act 来源与分支、是否连设备 / 含写。"""
    body = bodies.get(handler, "")
    params = []
    for match in FORM_VALUE_RE.finditer(body):
        if match.group("name") not in params:
            params.append(match.group("name"))
    switches = act_switches(body)
    act_cases = sorted({number for _header, _labels, numbers in switches for number in numbers})
    if 'r.FormValue("Act")' in body:
        act_source = "form:Act"
    elif ".Act" in body:
        act_source = "saveData(JSON 字段 Act)"
    else:
        act_source = "—"
    return {
        "params": params,
        "act_source": act_source,
        "act_cases": act_cases,
        "act_switches": [{"header": header, "cases": numbers} for header, _labels, numbers in switches],
        "connects_device": any(hint in body for hint in DEVICE_HINTS),
        "writes": [hint for hint in WRITE_HINTS if hint in body] + (["SQL 写"] if SQL_WRITE_RE.search(body) else []),
        "lines": body.count("\n"),
    }


def old_web_callers(js_text, path):
    """老壳调用证据（包装函数名）。

    压缩后的老壳把路径与调用分离：`at="/itms/handleOltConnect/"` → `oltConInfoStatic(r){return …n.post(at,null,{params:{…r}})}`。
    字段名在调用方（Vue 组件）里构造、以 `{...r}` 透传，**压缩产物中取不到逐字段参数**，
    因此这里只固化「哪个包装函数在调」，逐字段参数由 P0-2 真机录制补齐。
    """
    callers = []
    for var in re.findall(r'([A-Za-z_$][\w$]*)="' + re.escape(path) + r'"', js_text):
        pattern = re.compile(
            r"([A-Za-z_$][\w$]*)\s*\(r\)\s*\{\s*return[^}]{0,140}post\(\s*" + re.escape(var) + r"\s*,"
        )
        for match in pattern.finditer(js_text):
            if match.group(1) not in callers:
                callers.append(match.group(1))
    return callers


def act_switches(body):
    """提取与 Act 相关的 switch 块：返回 [(header, case 标签原文, 数字集合)]。

    老代码有两种 Act 来源，且分支写法不统一（天真实现必漏）：
      1) form 字段：`handleact, acterr := strconv.Atoi(r.FormValue("Act"))`
         → `switch handleact { case 0: …; default: switch handleact { case 1: … } }`（嵌套里仍是 Act）
         → `switch act := handleact; { case act == 0 || act == 9: … }`
      2) `saveData` 的 JSON 字段：`switch a := lp.Act; { case a == 0: … }`（如 handleLineProfile）
    做法：先找出「由 Act 派生的变量名」，再对所有 header 含这些变量 / `handleact` / `.Act` 的 switch 块
    取一级 case 标签，并从标签里抽数字（兼容 `case act == 0 || act == 9:`）。
    """
    names = set()
    for match in re.finditer(r'(\w+)\s*,\s*\w+\s*:=\s*strconv\.Atoi\(\s*r\.FormValue\("Act"\)', body):
        names.add(match.group(1))
    for match in re.finditer(r"switch\s+(\w+)\s*:=\s*(\w+|\w*\.Act)\s*;", body):
        if match.group(2) in names or match.group(2).endswith(".Act"):
            names.add(match.group(1))

    blocks = []
    for match in re.finditer(r"switch\b(?P<header>[^{]*)\{", body):
        header = match.group("header").strip()
        relevant = "handleact" in header or ".Act" in header or any(
            re.search(r"\b" + re.escape(name) + r"\b", header) for name in names
        )
        if not relevant:
            continue
        index, depth, labels = match.end(), 1, []
        while index < len(body) and depth > 0:
            char = body[index]
            if char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    break
            elif depth == 1:
                label = re.match(r"case\s+(?P<label>[^:]+):", body[index:])
                if label:
                    labels.append(label.group("label").strip())
                    index += label.end()
                    continue
            index += 1
        numbers = sorted({int(n) for text in labels for n in re.findall(r"\b\d+\b", text)})
        blocks.append((header, labels, numbers))
    return blocks


def parse_module_go_routes(text):
    """解析 internal/modules/olt/module.go 的 LegacyRoutes() 内的路径字面量。

    兼容两种接收者写法：`func (Module) ...`（tr069 用）与 `func (m *Module) ...`（olt 用指针接收者）。

    回归记录（2026-09-22）：olt 的 module.go 改用指针接收者后，旧正则 `func\\s*\\(Module\\)` 匹配不到，
    静默返回 0 条，于是 `make olt-contract-check` 报出「仅清单有 18 条；仅 module.go 有 []」这种难以定位的提示。
    现在改为：匹配不到但源码里确实出现 LegacyRoutes 时**直接报错并指出要改哪里**，避免再次静默失效。
    """
    block = re.search(
        r"func\s*\(\s*(?:\w+\s+)?\*?\s*Module\s*\)\s*LegacyRoutes\(\)\s*\[\]string\s*\{\s*return\s*\[\]string\{(?P<body>.*?)\}",
        text,
        re.S,
    )
    if not block:
        if "LegacyRoutes" in text:
            raise SystemExit(
                "✗ 无法解析 module.go 的 LegacyRoutes()（签名可能变了）："
                "请同步更新 tools/olt_contract_scan.py 的 parse_module_go_routes() 正则"
            )
        return []
    return re.findall(r'"(?P<path>/itms/[^"]+)"', block.group("body"))


def parse_manifest_routes(text):
    for line in text.splitlines():
        if line.startswith("module.legacy_routes="):
            raw = line.split("=", 1)[1].strip()
            return [item.strip() for item in raw.split(",") if item.strip()]
    return []


def manifest_covers(prefixes, path):
    """清单里写的是「前缀」（见 deploy/README.md），且历史写法可能省略 /itms/ 前缀。"""
    for prefix in prefixes:
        candidate = prefix if prefix.startswith("/itms/") else "/itms/" + prefix.lstrip("/")
        if path.startswith(candidate):
            return True
    return False


def build_contract():
    routes = json.load(open(ROUTES_JSON, encoding="utf-8"))
    olt_records = [r for r in routes["records"] if r.get("module") == "olt"]

    olt_bodies = function_bodies(OLD_OLT_HANDLER)
    tr069_bodies = function_bodies(OLD_TR069_HANDLER)
    main_text = read_text(OLD_MONOLITH_MAIN)
    js_text = read_text(OLD_WEB_JS)

    entries = []
    for record in sorted(olt_records, key=lambda r: r["path"]):
        handler = record["handler"]
        info = scan_handler(olt_bodies, handler)
        entries.append({
            "path": record["path"],
            "handler": handler,
            "comment": record.get("comment", ""),
            "registered_in_old_main": handler in main_text,
            "params": info["params"],
            "act_source": info["act_source"],
            "act_cases": info["act_cases"],
            "act_switches": info["act_switches"],
            "connects_device": info["connects_device"],
            "writes": info["writes"],
            "handler_lines": info["lines"],
            "old_web_params": old_web_callers(js_text, record["path"]),
        })

    # 仍未归类、且老壳在调的路由（兼容层无归属 → 404/-4）
    unclassified = []
    for record in routes["records"]:
        if record.get("module") != "unclassified":
            continue
        handler = record["handler"]
        evidence = []
        if handler in olt_bodies:
            evidence.append(f"{OLD_OLT_HANDLER}（OLT 处理器文件）")
        if handler in tr069_bodies:
            evidence.append(f"{OLD_TR069_HANDLER}（TR069 处理器文件）")
        unclassified.append({
            "path": record["path"],
            "handler": handler,
            "comment": record.get("comment", ""),
            "module_in_inventory": record.get("module"),
            "evidence": evidence,
            "old_web_owners": record.get("old_web_owners", []),
        })

    module_go = parse_module_go_routes(read_text(OLT_MODULE_GO))
    manifest = parse_manifest_routes(read_text(OLT_MANIFEST))
    declared = [entry["path"] for entry in entries]
    return {
        "generated_from": {
            "routes_json": ROUTES_JSON,
            "old_olt_handler": OLD_OLT_HANDLER,
            "old_tr069_handler": OLD_TR069_HANDLER,
            "old_web_js": OLD_WEB_JS,
            "module_go": OLT_MODULE_GO,
            "manifest": OLT_MANIFEST,
        },
        "summary": {
            "olt_routes_in_inventory": len(declared),
            "declared_in_module_go": len(module_go),
            "declared_in_manifest_prefixes": len(manifest),
            "unclassified_candidates": len(unclassified),
        },
        "entries": entries,
        "unclassified": unclassified,
        "declared": {
            "routes_json": declared,
            "module_go": module_go,
            "manifest_prefixes": manifest,
        },
    }


def render_markdown(contract):
    summary = contract["summary"]
    lines = [
        "# OLT 接口契约基线（自动生成，勿手改）",
        "",
        "生成工具：`tools/olt_contract_scan.py`（输入全部为只读：`tests/golden/routes.json`、"
        "老单体 `olthandler.go`/`main.go`、老壳 `chunk-itmsApi.js`、`internal/modules/olt/module.go`、"
        "`deploy/modules/olt.manifest`）。",
        "",
        "| 指标 | 数量 |",
        "|---|---|",
        f"| 老单体 `/itms/*` 中归于 olt 的路由 | {summary['olt_routes_in_inventory']} |",
        f"| `internal/modules/olt/module.go` 声明 | {summary['declared_in_module_go']} |",
        f"| `deploy/modules/olt.manifest` 前缀条数 | {summary['declared_in_manifest_prefixes']} |",
        f"| 未归类候选（需确认归属） | {summary['unclassified_candidates']} |",
        "",
        "> ⚠️ **OLT 用 `Act`，不是 switch 的 `nAct`**；逐条参数也不同"
        "（`saveData` / `connData` / `trace` / `reqData` / `RegProFileId`）。"
        "新实现必须继续用 `r.FormValue`（同时兼容 query 与 form，见 docs/01 §2），"
        "返回体仍是 `{code,message,data}`。",
        "",
        "> 「Act 分支」只统计 `switch <Act 变量>` 一级的 `case`（嵌套 switch 已排除）；"
        "「老壳包装函数」来自压缩产物——老壳用 `post(var,null,{params:{...r}})` 透传，"
        "**逐字段参数在压缩产物中不可见**，需由 P0-2 真机只读录制补齐（见 docs/09）。",
        "",
        "## 1. 逐条契约",
        "",
        "| 路径 | handler | 注释 | 参数（老代码 `r.FormValue`） | Act 分支 | 连设备 | 含写 | 老壳包装函数 |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for entry in contract["entries"]:
        fmt = lambda items: "、".join(f"`{item}`" for item in items) if items else "—"
        cases = ",".join(str(c) for c in entry["act_cases"])
        act_cell = f"{cases}（{entry['act_source']}）" if cases else entry["act_source"]
        lines.append(
            f"| `{entry['path']}` | `{entry['handler']}` | {entry['comment'] or '—'} | "
            f"{fmt(entry['params'])} | {act_cell} | "
            f"{'是' if entry['connects_device'] else '—'} | {fmt(entry['writes'])} | "
            f"{fmt(entry['old_web_params'])} |"
        )

    lines += ["", "## 2. 未归类候选（老壳在调，必须确认归属才能避免 404/-4）", ""]
    if contract["unclassified"]:
        lines += [
            "| 路径 | handler | 注释 | 清单中的归属 | 源码证据 | 老壳调用 |",
            "|---|---|---|---|---|---|",
        ]
        for item in contract["unclassified"]:
            lines.append(
                f"| `{item['path']}` | `{item['handler']}` | {item['comment'] or '—'} | "
                f"{item['module_in_inventory'] or '未归类'} | {'；'.join(item['evidence']) or '—'} | "
                f"{'、'.join(item['old_web_owners']) or '—'} |"
            )
        lines += [
            "",
            f"> **已解决（2026-09-22）**：`handleVLANFunctionModule` → **olt**（定义在 OLT 处理器文件）；"
            f"`handleTestDiag` → **tr069**（定义在 TR069 处理器文件）。"
            f"已在 `tools/api_inventory.py` 加显式归属规则（{len(contract['unclassified'])} 条待办如上表）并重新生成清单，"
            "同时同步了 `internal/modules/*/module.go` 的 `LegacyRoutes()` 与 `deploy/modules/*.manifest`。",
        ]
    else:
        lines.append("（无）")

    declared = contract["declared"]
    lines += [
        "",
        "## 3. 声明一致性（routes.json / module.go / manifest）",
        "",
        f"- `routes.json` 的 olt 集合：{len(declared['routes_json'])} 条",
        f"- `module.go` 的 `LegacyRoutes()`：{len(declared['module_go'])} 条",
        f"- `manifest` 的 `legacy_routes`（前缀语义）：{len(declared['manifest_prefixes'])} 条",
        "- 一致性由 `make olt-contract-check` 校验（不一致即失败）",
    ]
    return "\n".join(lines) + "\n"


def check_consistency(contract):
    declared = contract["declared"]
    failures = []
    routes_set, module_set = set(declared["routes_json"]), set(declared["module_go"])
    if routes_set != module_set:
        failures.append(
            "module.go 与 routes.json 不一致：仅清单有 "
            f"{sorted(routes_set - module_set)}；仅 module.go 有 {sorted(module_set - routes_set)}"
        )
    uncovered = [path for path in declared["routes_json"]
                 if not manifest_covers(declared["manifest_prefixes"], path)]
    if uncovered:
        failures.append(f"manifest 前缀未覆盖这些路由（老壳调用会走 -4/404）：{uncovered}")
    return failures


def main():
    parser = argparse.ArgumentParser(description="OLT 接口契约基线（只读老工程；--check 只比对不写文件）")
    parser.add_argument("--check", action="store_true", help="只比对（CI 用）：三方声明一致性 + 生成物是否最新")
    args = parser.parse_args()

    contract = build_contract()
    markdown = render_markdown(contract)
    payload = json.dumps(contract, ensure_ascii=False, indent=1) + "\n"
    failures = check_consistency(contract)

    if args.check:
        for failure in failures:
            print(f"✗ {failure}")
        for path, expected in ((OUT_MD, markdown), (OUT_JSON, payload)):
            if read_text(path) != expected:
                print(f"✗ 生成物不是最新（请运行 make olt-contract）：{os.path.relpath(path, PROJECT_ROOT)}")
                failures.append(path)
        print("✓ 契约声明一致且生成物最新" if not failures else f"共 {len(failures)} 项不一致")
        return 1 if failures else 0

    os.makedirs(os.path.dirname(OUT_MD), exist_ok=True)
    with open(OUT_MD, "w", encoding="utf-8") as fh:
        fh.write(markdown)
    with open(OUT_JSON, "w", encoding="utf-8") as fh:
        fh.write(payload)
    print(f"已生成：{os.path.relpath(OUT_MD, PROJECT_ROOT)}、{os.path.relpath(OUT_JSON, PROJECT_ROOT)}")
    summary = contract["summary"]
    print(f"  olt 路由 {summary['olt_routes_in_inventory']} 条（module.go 声明 {summary['declared_in_module_go']}、"
          f"manifest 前缀 {summary['declared_in_manifest_prefixes']}、"
          f"未归类候选 {summary['unclassified_candidates']}）")
    for failure in failures:
        print(f"  ✗ {failure}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())

