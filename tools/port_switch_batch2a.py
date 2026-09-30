#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""第二批迁移（A 组）：端口详情、SSH 交互终端、采集数据层。

搬运规则（与 tools/port_switch_batch2.py 一致）：
  * package main → package switchmod；
  * 老包级全局/自由函数 → 模块方法（m.）或 Store 方法（s.）：
      GetDB()                      → m.store.db / s.db
      writeJSON / APIResponse      → 模块内的兼容适配（legacy_response.go）
      ConnectWithLoginInfo(...)    → m.ConnectWithLoginInfo(...)
      ExecuteCmdsWithLoginInfo(...)→ m.ExecuteCmdsWithLoginInfo(...)
      ExecuteCmds(...)             → m.ExecuteCmds(...)
      GetAllDeviceInformation()    → m.store.GetAllDeviceInformation()
      GetDeviceInformationByID(id) → m.store.GetDeviceInformationByID(id)
      ...
"""
from __future__ import annotations

import re
import sys

SWITCHAPI = "/switchapi"
TARGET = "/itmsapiproject/internal/modules/switch"

# 老自由函数 → 新调用写法（模块方法/Store 方法）
CALL_MAP = {
    "ConnectWithLoginInfo(": "m.ConnectWithLoginInfo(",
    "ExecuteCmdsWithLoginInfo(": "m.ExecuteCmdsWithLoginInfo(",
    "ExecuteCmds(": "m.ExecuteCmds(",
    "GetAllDeviceInformation()": "m.store.GetAllDeviceInformation()",
    "UpdateDeviceAutoEnableByManageIP(": "m.store.UpdateDeviceAutoEnableByManageIP(",
    "UpdateDeviceAutoEnableByID(": "m.store.UpdateDeviceAutoEnableByID(",
    "GetAllModels()": "m.store.GetAllModels()",
    "GetAllVenues()": "m.store.GetAllVenues()",
    "GetAllManufacturers()": "m.store.GetAllManufacturers()",
    "GetAllPurposes()": "m.store.GetAllPurposes()",
    "GetAllProtocols()": "m.store.GetAllProtocols()",
    "requireSwitchRegistration()": "m.requireRegistration()",
}

# 采集数据层里成为 Store 方法的函数（其余保持自由函数）
COLLECTION_METHODS = [
    "createSwitchCollectionTables", "ensureSwitchCollectionTables", "migrateSwitchCollectionFilesToDB",
    "migrateSingleSwitchCollectionHostToDB", "hasSwitchCollectionFileStorage",
    "cleanupMigratedSwitchCollectionFiles", "countSwitchCollectionSnapshotsInDB",
    "saveSwitchCollectionSnapshotToDB", "pruneSwitchCollectionSnapshotsInDB",
    "loadAllSwitchCollectionSnapshotsForHostFromDB", "loadSwitchCollectionSnapshotsInRangeFromDB",
    "loadSwitchCollectionSnapshotsFromDB", "loadSwitchCollectionInterfaceStatsFromDB",
    "loadLatestSwitchCollectionSnapshotTimeFromDB",
]


def read(path: str) -> str:
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def import_block(text: str) -> str:
    match = re.search(r"^import \(\n(.*?)^\)\n", text, re.MULTILINE | re.DOTALL)
    if not match:
        return ""
    lines = [line for line in match.group(1).splitlines() if line.strip()]
    return "import (\n" + "\n".join(lines) + "\n)\n"


def body_without_package_and_imports(text: str) -> str:
    text = re.sub(r"^package .*\n", "", text, count=1, flags=re.MULTILINE)
    return re.sub(r"^import \(\n.*?^\)\n", "", text, count=1, flags=re.MULTILINE | re.DOTALL).lstrip("\n")


def apply_calls(text: str) -> str:
    for old, new in CALL_MAP.items():
        text = text.replace(old, new)
    return text


def port(src_name: str, dst_name: str, header: str, apply: bool, transform=None) -> None:
    text = read("%s/%s" % (SWITCHAPI, src_name))
    imports = import_block(text)
    body = apply_calls(body_without_package_and_imports(text))
    if transform:
        body, imports = transform(body, imports)
    content = "// %s\npackage switchmod\n\n%s\n%s" % (header, imports, body.rstrip() + "\n")
    if apply:
        with open("%s/%s" % (TARGET, dst_name), "w", encoding="utf-8") as fh:
            fh.write(content)
    print("%s %s（%d 行）" % ("写入" if apply else "将写入", dst_name, content.count("\n")))


def collection_transform(body: str, imports: str):
    # 采集数据层：DB 访问改为 Store 方法；调用点加 s. 前缀（跳过函数定义行）
    out_lines = []
    for line in body.splitlines():
        if line.startswith("func "):
            for name in COLLECTION_METHODS:
                line = line.replace("func %s(" % name, "func (s *Store) %s(" % name)
            out_lines.append(line)
            continue
        for key in ("createSwitchCollectionTables", "ensureSwitchCollectionTables",
                    "migrateSwitchCollectionFilesToDB", "migrateSingleSwitchCollectionHostToDB",
                    "hasSwitchCollectionFileStorage", "cleanupMigratedSwitchCollectionFiles",
                    "countSwitchCollectionSnapshotsInDB", "saveSwitchCollectionSnapshotToDB",
                    "pruneSwitchCollectionSnapshotsInDB", "loadAllSwitchCollectionSnapshotsForHostFromDB",
                    "loadSwitchCollectionSnapshotsInRangeFromDB", "loadSwitchCollectionSnapshotsFromDB",
                    "loadSwitchCollectionInterfaceStatsFromDB", "loadLatestSwitchCollectionSnapshotTimeFromDB"):
            line = re.sub(r"(?<![.\w])%s\(" % re.escape(key), "s.%s(" % key, line)
        out_lines.append(line)
    body = "\n".join(out_lines).replace("GetDB()", "s.db")
    return body, imports


def main() -> int:
    apply = "--write" in sys.argv
    port("handler_port_detail.go", "port_detail.go",
         "由 tools/port_switch_batch2a.py 从 switchapi/handler_port_detail.go 整体搬运（端口详情与操作）", apply)
    port("handler_switch_terminal.go", "terminal.go",
         "由 tools/port_switch_batch2a.py 从 switchapi/handler_switch_terminal.go 整体搬运（SSH 交互终端）", apply)
    port("switch_collection_db.go", "collection_db.go",
         "由 tools/port_switch_batch2a.py 从 switchapi/switch_collection_db.go 整体搬运（采集快照数据层，改为 Store 方法）",
         apply, transform=collection_transform)
    port("switch_sniffer.go", "sniffer.go",
         "由 tools/port_switch_batch2a.py 从 switchapi/switch_sniffer.go 整体搬运（采集/嗅探配置与后台任务）",
         apply, transform=sniffer_transform)
    return 0


def sniffer_transform(body: str, imports: str):
    # 采集/嗅探任务：DB 直连改为 Store 方法；采集数据层函数加 m.store. 前缀
    body = body.replace("GetDB()", "m.store.db")
    for key in ("saveSwitchCollectionSnapshotToDB", "loadSwitchCollectionSnapshotsFromShard",
                "loadSwitchCollectionHostFile", "loadSwitchCollectionSnapshotsFromDB",
                "loadSwitchCollectionSnapshotsInRangeFromDB", "loadLatestSwitchCollectionSnapshotTimeFromDB",
                "loadAllSwitchCollectionSnapshotsForHostFromDB", "loadSwitchCollectionInterfaceStatsFromDB",
                "pruneSwitchCollectionSnapshotsInDB", "ensureSwitchCollectionTables"):
        body = re.sub(r"(?<![.\w])%s\(" % re.escape(key), "m.store.%s(" % key, body)
    body = re.sub(r"(?<![.\w])loadSwitchCollectionSettings\(" , "m.loadSwitchCollectionSettings(", body)
    body = re.sub(r"(?<![.\w])saveSwitchCollectionSettings\(", "m.saveSwitchCollectionSettings(", body)
    return body, imports


if __name__ == "__main__":
    raise SystemExit(main())
