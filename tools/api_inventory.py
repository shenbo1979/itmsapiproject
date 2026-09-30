#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""API 兼容清单生成器（M0 交付物①，只读原工程）。

输入（全部只读）：
  * 老 Go 单体路由表：/switchProject/switchmain/main.go
  * 新 switch 模块路由：/switchapi/main.go
  * 新 WEB 壳调用：/switchweb/src/api.js
  * 老 ITMS WEB 壳调用：/fusiongateway/web/assets/js/*.js

输出（全部写入本工程）：
  docs/generated/api-inventory.md      人读清单（分组表格 + 风险提示）
  tests/golden/routes.json             机读清单（黄金用例骨架输入）
  tests/golden/itms_routes.txt         纯路径清单（便于 diff/回归）

用法：
  python3 tools/api_inventory.py            # 生成到本工程
  python3 tools/api_inventory.py --check    # 只比对不写文件（CI 用）
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import re
import sys

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

OLD_MONOLITH_MAIN = "/switchProject/switchmain/main.go"
NEW_SWITCH_MAIN = "/switchapi/main.go"
NEW_WEB_API = "/switchweb/src/api.js"
OLD_WEB_JS_GLOB = "/fusiongateway/web/assets/js/*.js"

OUT_MD = os.path.join(PROJECT_ROOT, "docs", "generated", "api-inventory.md")
OUT_JSON = os.path.join(PROJECT_ROOT, "tests", "golden", "routes.json")
OUT_TXT = os.path.join(PROJECT_ROOT, "tests", "golden", "itms_routes.txt")

HANDLEFUNC_RE = re.compile(
    r'HandleFunc\(\s*"(?P<path>/[^"]*)"\s*,\s*(?P<handler>\w+)\s*\)\s*(?://\s*(?P<comment>.*))?'
)
PATH_LITERAL_RE = re.compile(r'"(?P<path>/(?:itms|client)/[A-Za-z0-9_]+/)"')
POSTFORM_RE = re.compile(r'postForm\(\s*"(?P<path>/[^"]+)"')

EXPLICIT_PREFIX_RE = re.compile(r"\((?P<tag>Switch|OLT|TR069|CPE|ITMS)\)", re.I)
PREFIX_MODULE = {"switch": "switch", "olt": "olt", "tr069": "tr069", "cpe": "tr069", "itms": "tr069"}

# 人工确认的显式归属（优先级高于关键词/注释启发式）。依据：老单体里该 handler 定义所在的处理器文件。
#   handleVLANFunctionModule → /switchProject/switchmain/olthandler.go（OLT 文件），老壳 chunk-itmsApi.js 在调
#   handleTestDiag           → /switchProject/switchmain/tr069handler.go（TR069 文件），老壳 chunk-itmsApi.js 在调
#   handleTr069Profile       → /switchProject/switchmain/olthandler.go（OLT 文件，2960 行起「TR069管理模板」），
#                             操作 OLT 设备（`ont tr069-server-profile` 命令，用 OLT 连接参数 connData +
#                             解析 OLT 的 [global-config]），与 ONT 业务模板同属 OLT 详情页「业务/TR069模板」TAB。
#                             虽路径带 Tr069 关键词，但物理定义与运行时上下文都是 OLT，按 handler 文件归 olt。
# 这三条原先落在 unclassified 或关键词误归：兼容层无归属，老壳调用会变成 404/-4（见 docs/generated/olt-contract.md 第 2 节）。
EXPLICIT_MODULE = {
    "/itms/handleVLANFunctionModule/": "olt",
    "/itms/handleTestDiag/": "tr069",
    "/itms/handleTr069Profile/": "olt",
}
SWITCH_KEYWORDS = ("/switch", "Switch", "交换机")
OLT_KEYWORDS = ("Olt", "OLT", "Ont", "ONT", "Onu", "ONU", "板卡", "插槽", "线路模版",
                "业务模板", "服务模板", "DBA", "汇聚")
TR069_KEYWORDS = ("Tr069", "TR069", "Cpe", "CPE", "光猫", "固件", "版本", "参数", "分组",
                  "策略", "日志", "工单", "房间", "文件导入", "用户操作", "设备状态", "Itms")


def classify(path: str, comment: str = "", owners=(), in_new_switch: bool = False):
    """返回 (模块, 判定依据)。依据优先级：switchapi 已存在 > 注释前缀 > 老壳 chunk > 关键词。"""
    if in_new_switch:
        return "switch", "switchapi"
    if path in EXPLICIT_MODULE:
        return EXPLICIT_MODULE[path], "explicit(handler 文件)"
    match = EXPLICIT_PREFIX_RE.search(comment or "")
    if match:
        return PREFIX_MODULE[match.group("tag").lower()], "comment-prefix"
    if any("chunk-switchApi" in owner for owner in owners):
        return "switch", "chunk-switchApi"
    haystack = "%s %s" % (path, comment)
    for keyword in SWITCH_KEYWORDS:
        if keyword in haystack:
            return "switch", "keyword:%s" % keyword
    for keyword in OLT_KEYWORDS:
        if keyword in haystack:
            return "olt", "keyword:%s" % keyword
    for keyword in TR069_KEYWORDS:
        if keyword in haystack:
            return "tr069", "keyword:%s" % keyword
    return "unclassified", "unclassified"



def read_text(path: str) -> str:
    with open(path, encoding="utf-8", errors="replace") as fh:
        return fh.read()


def parse_go_routes(path: str) -> list[dict]:
    if not os.path.exists(path):
        return []
    routes = []
    for match in HANDLEFUNC_RE.finditer(read_text(path)):
        routes.append({
            "path": match.group("path"),
            "handler": match.group("handler"),
            "comment": (match.group("comment") or "").strip(),
            "source": path,
        })
    return routes


def parse_web_paths() -> dict:
    """返回 {路径: [引用它的前端文件, ...]}，覆盖老壳与新壳。"""
    usage = {}

    def record(found: str, owner: str) -> None:
        usage.setdefault(found, [])
        if owner not in usage[found]:
            usage[found].append(owner)

    if os.path.exists(NEW_WEB_API):
        for match in POSTFORM_RE.finditer(read_text(NEW_WEB_API)):
            record(match.group("path"), os.path.relpath(NEW_WEB_API, "/"))

    for js_path in sorted(glob.glob(OLD_WEB_JS_GLOB)):
        text = read_text(js_path)
        for match in PATH_LITERAL_RE.finditer(text):
            record(match.group("path"), os.path.relpath(js_path, "/"))
    return usage


def build_report() -> dict:
    old_routes = parse_go_routes(OLD_MONOLITH_MAIN)
    new_routes = parse_go_routes(NEW_SWITCH_MAIN)
    usage = parse_web_paths()

    old_paths = {r["path"] for r in old_routes}
    new_paths = {r["path"] for r in new_routes}
    lowered = {p.lower(): p for p in usage}

    records = []
    for route in old_routes:
        path = route["path"]
        owners = usage.get(path, [])
        module, basis = classify(path, route["comment"], owners, path in new_paths)
        notes = []
        if not owners and path.lower() in lowered:
            notes.append("老前端调用的大小写为 " + lowered[path.lower()])
        if basis == "unclassified":
            notes.append("模块归属待人工确认")
        records.append({
            **route,
            "module": module,
            "module_basis": basis,
            "in_old_web": bool(owners),
            "old_web_owners": owners,
            "in_new_switch": path in new_paths,
            "notes": notes,
        })

    for path, owners in sorted(usage.items()):
        if not path.startswith("/client/"):
            continue
        records.append({
            "path": path,
            "handler": "",
            "comment": "(TR069/CPE 侧接口，由 Python /itms-py 提供)",
            "module": "tr069",
            "module_basis": "python:/itms-py/vTr69Api.py",
            "source": "python:/itms-py/vTr69Api.py",
            "in_old_web": True,
            "old_web_owners": owners,
            "in_new_switch": False,
            "notes": ["非 Go 路由：需确认迁入统一工程或保持独立服务"],
        })

    unused_old_routes = sorted(p for p in old_paths if p not in usage)
    new_web_only = sorted(p for p in new_paths if p not in usage and "switch" in p.lower())

    return {
        "summary": {
            "old_monolith_routes": len(old_routes),
            "new_switch_routes": len(new_routes),
            "old_web_itms_paths": len([p for p in usage if p.startswith("/itms/")]),
            "old_web_client_paths": len([p for p in usage if p.startswith("/client/")]),
            "old_routes_not_used_by_old_web": len(unused_old_routes),
        },
        "by_module": {
            module: sorted({r["path"] for r in records if r["module"] == module})
            for module in ("switch", "olt", "tr069", "core")
        },
        "old_routes_not_used_by_old_web": unused_old_routes,
        "new_switch_routes_not_in_old_web": new_web_only,
        "usage": usage,
        "records": sorted(records, key=lambda item: item["path"]),
    }


def render_markdown(report: dict) -> str:
    summary = report["summary"]
    lines = [
        "# 老/新接口面清单（自动生成，勿手改）",
        "",
        "生成工具：`tools/api_inventory.py`；输入：老 Go 单体 `main.go`、`/switchapi/main.go`、"
        "`/switchweb/src/api.js`、`/fusiongateway/web/assets/js/*.js`（全部只读）。",
        "",
        "## 1. 概览",
        "",
        "| 指标 | 数量 |",
        "|---|---|",
        f"| 老 Go 单体 `/itms/*` 路由 | {summary['old_monolith_routes']} |",
        f"| 新 `switchapi` `/itms/*` 路由 | {summary['new_switch_routes']} |",
        f"| 老 WEB 壳调用的 `/itms/*` 路径 | {summary['old_web_itms_paths']} |",
        f"| 老 WEB 壳调用的 `/client/*` 路径（Python 侧） | {summary['old_web_client_paths']} |",
        f"| 老 Go 路由中未被老壳调用的 | {summary['old_routes_not_used_by_old_web']} |",
        "",
        "## 2. 按模块归类",
        "",
        "| 模块 | 路径数 | 路径 |",
        "|---|---|---|",
    ]
    for module in ("switch", "olt", "tr069", "core"):
        paths = report["by_module"][module]
        cell = "<br>".join("`%s`" % p for p in paths) or "—"
        lines.append(f"| {module} | {len(paths)} | {cell} |")

    lines += ["", "## 3. `/itms/*` 逐条明细（老 Go 单体视角）", "",
              "| 路径 | handler | 模块 | 判定依据 | 老壳调用 | 新 switchapi | 备注 |",
              "|---|---|---|---|---|---|---|"]
    for record in report["records"]:
        if not record["path"].startswith("/itms/"):
            continue
        owners = "、".join(record["old_web_owners"]) if record["in_old_web"] else "—"
        lines.append("| `%s` | `%s` | %s | %s | %s | %s | %s |" % (
            record["path"], record["handler"] or "—", record["module"],
            record.get("module_basis", ""),
            "✅ " + owners if record["in_old_web"] else "—",
            "✅" if record["in_new_switch"] else "❌",
            "；".join(record["notes"]),
        ))

    lines += ["", "## 4. `/client/*` 明细（TR069/CPE 侧，Python 提供）", "",
              "| 路径 | 模块 | 老壳调用 | 备注 |", "|---|---|---|---|"]
    for record in report["records"]:
        if not record["path"].startswith("/client/"):
            continue
        lines.append("| `%s` | %s | ✅ | %s |" % (
            record["path"], record["module"], "；".join(record["notes"])))

    lines += ["", "## 5. 模块归属判定统计与待确认", ""]
    basis_counts = {}
    for record in report["records"]:
        basis = record.get("module_basis", "")
        key = basis.split(":")[0]
        basis_counts[key] = basis_counts.get(key, 0) + 1
    lines.append("| 判定依据 | 条数 |")
    lines.append("|---|---|")
    for key in sorted(basis_counts):
        lines.append("| %s | %d |" % (key, basis_counts[key]))
    lines.append("")
    unclassified = [r for r in report["records"] if r["module"] == "unclassified"]
    if unclassified:
        lines.append("**待人工确认（无法从现有证据判定模块归属）**：")
        lines.append("")
        lines.append("| 路径 | handler | 老壳调用 | 注释 |")
        lines.append("|---|---|---|---|")
        for record in unclassified:
            lines.append("| `%s` | `%s` | %s | %s |" % (
                record["path"], record["handler"] or "—",
                "、".join(record["old_web_owners"]) or "—", record.get("comment", "")))
        lines.append("")

    for path in report["old_routes_not_used_by_old_web"]:
        lines.append("- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`%s`" % path)
    for record in report["records"]:
        if record["notes"] and record["path"].startswith("/itms/"):
            lines.append("- `%s`：%s" % (record["path"], "；".join(record["notes"])))
    lines += [
        "- 老前端用 axios `post(url, null, {params})` 传参（**query 参数**），"
        "新实现必须继续用 `r.FormValue` 同时兼容 query 与 form。",
        "- 返回体统一 `{code,message,data}`；历史数字字段以字符串返回，禁止顺手改成 int。",
        "- `/client/*` 目前不在 Go 后端内（由 `/itms-py` Python 实现），"
        "分包/迁移前必须决定：随 `tr069` 模块一起迁，还是保持独立服务由网关分流。",
    ]
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description="生成 API 兼容清单")
    parser.add_argument("--check", action="store_true", help="只校验生成结果是否与现有文件一致")
    args = parser.parse_args()

    report = build_report()
    markdown = render_markdown(report)
    paths = "\n".join(sorted({r["path"] for r in report["records"]})) + "\n"
    payload = json.dumps(report, ensure_ascii=False, indent=2) + "\n"

    if args.check:
        ok = True
        for path, expected in ((OUT_MD, markdown), (OUT_TXT, paths), (OUT_JSON, payload)):
            if not os.path.exists(path) or read_text(path) != expected:
                print("清单过期：%s（请运行 python3 tools/api_inventory.py）" % path, file=sys.stderr)
                ok = False
        return 0 if ok else 1

    os.makedirs(os.path.dirname(OUT_MD), exist_ok=True)
    os.makedirs(os.path.dirname(OUT_JSON), exist_ok=True)
    for path, content in ((OUT_MD, markdown), (OUT_TXT, paths), (OUT_JSON, payload)):
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(content)
        print("已生成 %s" % os.path.relpath(path, "/"))

    summary = report["summary"]
    print("老单体路由 %d，新 switchapi %d，老壳 /itms %d，老壳 /client %d" % (
        summary["old_monolith_routes"], summary["new_switch_routes"],
        summary["old_web_itms_paths"], summary["old_web_client_paths"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
