#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""TR069/CPE 文件型数据扫描器（M0 交付物②，只读原系统）。

扫描对象（全部只读，不写入、不修改）：
  /home/tr069file/**           老 ITMS/TR069 的 JSON 文件型状态
  /opt/itmsapikey/*            ITMS 授权文件（只记录元数据与 JSON 键名）
  /txapikeygen/*               注册码生成目录（只记录文件清单与大小）
  /dev/shm/verify_info.json    云平台授权窗口（只记录键名，不记录值）

输出（写入本工程）：
  docs/generated/tr069-file-inventory.md   人读清单 + 文件→表映射建议
  tests/fixtures/tr069-file-inventory.json 机读清单（迁移脚本的输入基线）

用法：
  python3 tools/tr069_file_scan.py
"""
from __future__ import annotations

import json
import os

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_MD = os.path.join(PROJECT_ROOT, "docs", "generated", "tr069-file-inventory.md")
OUT_JSON = os.path.join(PROJECT_ROOT, "tests", "fixtures", "tr069-file-inventory.json")

TR069_DIR = "/home/tr069file"
EXTRA_PATHS = ["/opt/itmsapikey", "/txapikeygen", "/dev/shm/verify_info.json"]

# 目录/文件名 -> 目标表与说明（迁移设计草案，人工维护；扫描器只做匹配与提示）
TABLE_MAP = [
    ("Parameters/*.json", "tbCpeDevice + tbCpeParameter",
     "每个 CPE 一个 JSON：Cookie/SerialNumber/Parameters{}；建议原始 JSON 留档一列 + 参数明细行"),
    ("templatefile/templatefile.json", "tbTr069Template",
     "模板名 -> 参数默认值集合"),
    ("cpegroup", "tbCpeGroup", "CPE 分组"),
    ("tr069specialfield", "tbCpeSpecialField", "CPE 特殊字段"),
    ("tr69serverinfo", "tbTr069Server", "TR069 服务器信息（单例）"),
    ("tr069dofile", "tbTr069TaskFile", "下发/临时文件（可只保留最近 N 条）"),
    ("DeviceSummary", "tbCpeSummary", "设备汇总快照"),
    ("*.json（根目录）", "tbCpeSnapshotRaw", "按序列号命名的快照文件，建议入库并去重"),
]

SAMPLE_LIMIT = 8


def describe_json(path: str, relpath: str) -> dict:
    """读取 JSON 文件的结构摘要（不保留业务值，只留键名与类型统计）。"""
    info = {"kind": "json"}
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            payload = json.load(fh)
    except Exception as exc:  # noqa: BLE001 - 扫描器需容错
        info["kind"] = "json-unparsable"
        info["error"] = str(exc)
        return info

    if isinstance(payload, dict):
        keys = list(payload.keys())
        info["top_keys"] = keys[:SAMPLE_LIMIT]
        info["top_key_count"] = len(keys)
        parameters = payload.get("Parameters")
        if isinstance(parameters, dict):
            info["parameter_count"] = len(parameters)
            info["parameter_samples"] = sorted(parameters.keys())[:3]
            types: dict[str, int] = {}
            for value in parameters.values():
                types[type(value).__name__] = types.get(type(value).__name__, 0) + 1
            info["parameter_value_types"] = types
    elif isinstance(payload, list):
        info["top_keys"] = ["<list>"]
        info["list_len"] = len(payload)
        if payload and isinstance(payload[0], dict):
            info["item_keys"] = sorted(payload[0].keys())[:SAMPLE_LIMIT]
    return info


def scan_tree(root: str) -> list[dict]:
    entries = []
    if not os.path.isdir(root):
        return entries
    for dirpath, _dirnames, filenames in os.walk(root):
        rel_dir = os.path.relpath(dirpath, root)
        if not filenames:
            entries.append({"relpath": rel_dir + "/", "kind": "dir", "size": 0})
        for name in sorted(filenames):
            full = os.path.join(dirpath, name)
            rel = os.path.normpath(os.path.join(rel_dir, name)) if rel_dir != "." else name
            stat = os.stat(full)
            entry = {"relpath": rel, "size": stat.st_size, "mtime": int(stat.st_mtime)}
            if name.endswith(".json"):
                entry.update(describe_json(full, rel))
            else:
                entry["kind"] = "file"
                entry["preview_len"] = min(stat.st_size, 200)
            entries.append(entry)
    return entries


def scan_extra(path: str) -> dict:
    """额外状态文件：只记录存在性/大小/键名，绝不落库业务值。"""
    if not os.path.exists(path):
        return {"path": path, "exists": False}
    if os.path.isdir(path):
        files = sorted(os.listdir(path))
        return {"path": path, "exists": True, "kind": "dir", "files": [
            {"name": name, "size": os.path.getsize(os.path.join(path, name))} for name in files]}
    stat = os.stat(path)
    result = {"path": path, "exists": True, "kind": "file", "size": stat.st_size}
    if path.endswith(".json"):
        try:
            with open(path, encoding="utf-8", errors="replace") as fh:
                payload = json.load(fh)
            if isinstance(payload, dict):
                result["top_keys"] = sorted(payload.keys())
        except Exception as exc:  # noqa: BLE001
            result["error"] = str(exc)
    return result


def match_table(relpath: str) -> tuple[str, str]:
    normalized = relpath.replace(os.sep, "/")
    if normalized.startswith("Parameters/"):
        return TABLE_MAP[0][1], TABLE_MAP[0][2]
    for pattern, table, note in TABLE_MAP[1:]:
        stem = pattern.split("/")[-1].replace("*", "")
        if stem and stem in normalized:
            return table, note
    if normalized.endswith(".json"):
        return TABLE_MAP[-1][1], TABLE_MAP[-1][2]
    return "tbCpeFileArchive", "其余文件先归档入表（保留原始内容 hash + 路径）"



def render_markdown(report: dict) -> str:
    entries = report["tr069_entries"]
    lines = [
        "# TR069/CPE 文件型数据清单（自动生成，勿手改）",
        "",
        "生成工具：`tools/tr069_file_scan.py`；**只读扫描**，不修改任何原系统文件。",
        "本清单是「文件 → 新库表」迁移设计的事实依据（映射表由 `docs/02-db-migration.md` 定义）。",
        "",
        "## 1. 概览",
        "",
        "| 指标 | 值 |",
        "|---|---|",
        "| 扫描根目录 | `%s` |" % TR069_DIR,
        "| 条目数（文件+目录） | %d |" % len(entries),
        "| 总字节数 | %d |" % report["total_bytes"],
        "| 其中 JSON 文件 | %d |" % len([e for e in entries if e.get("kind") == "json"]),
        "",
        "## 2. 文件清单与映射建议",
        "",
        "| 相对路径 | 大小(B) | 类型 | 关键结构 | 建议目标表 | 说明 |",
        "|---|---|---|---|---|---|",
    ]
    for entry in entries:
        structure = []
        if entry.get("kind") == "json":
            if entry.get("top_keys"):
                structure.append("顶层键 %s%s" % (
                    ", ".join(str(k) for k in entry["top_keys"][:4]),
                    "…" if entry.get("top_key_count", 0) > 4 else ""))
            if entry.get("parameter_count") is not None:
                structure.append("Parameters=%d 项" % entry["parameter_count"])
            if entry.get("parameter_value_types"):
                structure.append("值类型 %s" % json.dumps(
                    entry["parameter_value_types"], ensure_ascii=False))
        elif entry.get("kind") == "dir":
            structure.append("目录")
        table, note = match_table(entry["relpath"])
        lines.append("| `%s` | %d | %s | %s | %s | %s |" % (
            entry["relpath"], entry.get("size", 0), entry.get("kind", ""),
            "；".join(structure), table, note))

    lines += ["", "## 3. 授权/注册状态文件（只记录元数据，不落业务值）", "",
              "| 路径 | 存在 | 类型 | 大小 | 文件/键名 |", "|---|---|---|---|---|"]
    for extra in report["extra_paths"]:
        names = extra.get("top_keys") or [f["name"] for f in extra.get("files", [])]
        lines.append("| `%s` | %s | %s | %s | %s |" % (
            extra["path"], "✅" if extra.get("exists") else "❌",
            extra.get("kind", "—"), extra.get("size", "—"),
            ", ".join(names) or "—"))

    lines += ["", "## 4. 迁移注意", "",
              "- 迁移脚本必须**幂等**（唯一键 + `INSERT ... ON CONFLICT DO UPDATE`），可重复执行。",
              "- 单条 JSON 可能达数百 KB、参数上千项：入库时按 `tbCpeParameter(cpeId, szPath, szValue)` 拆分，"
              "并保留原始 JSON 归档列，便于回滚与回溯。",
              "- 迁移期间**单写者**（停写老进程或设只读窗口），连接串带 `_busy_timeout`，每 500 行提交一次，"
              "避免长事务占锁（历史上出现过共享 SQLite 被写事务卡死、前端整页转圈的事故）。",
              "- 迁移完成后老文件保留只读副本，新代码不再读写 `/home/tr069file`。"]
    return "\n".join(lines) + "\n"


def main() -> int:
    entries = scan_tree(TR069_DIR)
    extras = [scan_extra(path) for path in EXTRA_PATHS]
    report = {
        "root": TR069_DIR,
        "tr069_entries": entries,
        "total_bytes": sum(e.get("size", 0) for e in entries),
        "extra_paths": extras,
    }

    os.makedirs(os.path.dirname(OUT_MD), exist_ok=True)
    os.makedirs(os.path.dirname(OUT_JSON), exist_ok=True)
    with open(OUT_MD, "w", encoding="utf-8") as fh:
        fh.write(render_markdown(report))
    with open(OUT_JSON, "w", encoding="utf-8") as fh:
        fh.write(json.dumps(report, ensure_ascii=False, indent=2) + "\n")

    print("已生成 %s" % os.path.relpath(OUT_MD, "/"))
    print("已生成 %s" % os.path.relpath(OUT_JSON, "/"))
    print("条目 %d，总大小 %d 字节" % (len(entries), report["total_bytes"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
