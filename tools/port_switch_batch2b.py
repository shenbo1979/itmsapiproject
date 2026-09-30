#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""第二批迁移（B 组）：从 switchapi/handler.go 提取交换机相关顶层声明（不含已重写的 6 个接口），
并把 models.go 中这批代码需要的类型补进 types_extra.go。

说明：Go 顶层声明都从第 0 列开始，因此按"行首为 func/type/var/const"切块即可。
"""
from __future__ import annotations

import re
import sys

SWITCHAPI = "/switchapi"
TARGET = "/itmsapiproject/internal/modules/switch"

EXCLUDE = {
    "handleSwitchVenue", "handleSwitchManufacturer", "handleSwitchPurpose",
    "handleSwitchProtocol", "handleSwitchModel", "handleSwitchInfo",
    "writeJSON", "sanitizeClientMessage", "serverPathPattern",
}

CALL_MAP = {
    "ConnectWithLoginInfo(": "m.ConnectWithLoginInfo(",
    "ExecuteCmdsWithLoginInfo(": "m.ExecuteCmdsWithLoginInfo(",
    "ExecuteCmds(": "m.ExecuteCmds(",
    "GetAllDeviceInformation()": "m.store.GetAllDeviceInformation()",
    "GetDeviceInformationByID(": "m.store.GetDeviceInformationByID(",
    "UpdateDeviceAutoEnableByManageIP(": "m.store.UpdateDeviceAutoEnableByManageIP(",
    "UpdateDeviceAutoEnableByID(": "m.store.UpdateDeviceAutoEnableByID(",
    "GetAllModels()": "m.store.GetAllModels()",
    "GetAllVenues()": "m.store.GetAllVenues()",
    "GetAllManufacturers()": "m.store.GetAllManufacturers()",
    "GetAllPurposes()": "m.store.GetAllPurposes()",
    "GetAllProtocols()": "m.store.GetAllProtocols()",
    "requireSwitchRegistration()": "m.requireRegistration()",
    "recordSwitchLoginConnection(": "m.recordLoginConnection(",
    "switchDeviceFileDir": "m.deviceFileDir",
    "sanitizeClientMessage(": "core.SanitizeClientMessage(",
}

TOP_LEVEL = re.compile(r"^(func|type|var|const)\b")
TYPE_PATTERN = re.compile(r"^(Switch|Terminal|Routing|Transceiver|Vlan|VLAN|Interface|Client|Interactive|LocalDevice)")

ALREADY_DEFINED = {
    "SwitchDeviceInformation", "SwitchVenue", "SwitchManufacturer", "SwitchPurpose", "SwitchProtocol",
    "SwitchModel", "Client", "InteractiveOptions", "SwitchInterfaceInfo", "TransceiverInfo",
    "SwitchVlanMapping", "RoutingTable", "SwitchDeviceDetail", "SwitchFanInfo", "SwitchTemperatureSensor",
    "SwitchRequest", "LocalDeviceInformation", "InterfaceVlanif", "JSONRPCRequest", "JSONRPCResponse",
    "CmdParams", "RPCError", "SwitchLoginRecord", "SwitchLoginInfo",
}


def split_blocks(text: str) -> list[tuple[list[str], list[str]]]:
    """按顶层声明切块，声明上方的连续注释归属该块。"""
    lines = text.splitlines()
    starts = [index for index, line in enumerate(lines) if TOP_LEVEL.match(line)]
    blocks = []
    for position, start in enumerate(starts):
        end = starts[position + 1] if position + 1 < len(starts) else len(lines)
        comments = []
        cursor = start - 1
        while cursor >= 0 and lines[cursor].startswith("//"):
            comments.insert(0, lines[cursor])
            cursor -= 1
        blocks.append((comments, lines[start:end]))
    return blocks


def decl_name(block_lines: list[str]) -> str:
    first = block_lines[0]
    match = re.match(r"func (?:\([^)]*\) )?([A-Za-z0-9_]+)", first) or re.match(r"type ([A-Za-z0-9_]+)", first)
    return match.group(1) if match else ""


def main() -> int:
    apply = "--write" in sys.argv
    header = ("// 由 tools/port_switch_batch2b.py 从 switchapi/handler.go 提取（交换机链路 handler 与辅助函数；\n"
              "// 已重写的 6 个基础资料/台账接口未包含）。")
    imports = ('import (\n\t"encoding/json"\n\t"fmt"\n\t"log"\n\t"net"\n\t"net/http"\n\t"net/url"\n'
               '\t"os"\n\t"path/filepath"\n\t"sort"\n\t"strconv"\n\t"strings"\n\t"time"\n\n'
               '\t"itmsapiproject/internal/core"\n)\n')

    blocks = split_blocks(open("%s/handler.go" % SWITCHAPI, encoding="utf-8").read())
    kept, skipped = [], []
    for comments, block in blocks:
        name = decl_name(block)
        if not name or name in EXCLUDE or name == "main":
            skipped.append(name or block[0][:30])
            continue
        kept.append((comments, block))
    body = "\n\n".join("\n".join(comments + block) for comments, block in kept)
    for old, new in CALL_MAP.items():
        body = body.replace(old, new)
    content = "%s\npackage switchmod\n\n%s\n%s\n" % (header, imports, body.rstrip())
    if apply:
        with open("%s/handlers.go" % TARGET, "w", encoding="utf-8") as fh:
            fh.write(content)
    print("%s handlers.go（%d 个声明；跳过 %d 个：%s）" %
          ("写入" if apply else "将写入", len(kept), len(skipped), ", ".join(sorted(set(skipped))[:10])))

    with open("%s/types_extra.go" % TARGET, encoding="utf-8") as fh:
        existing = fh.read()
    added = []
    for comments, block in split_blocks(open("%s/models.go" % SWITCHAPI, encoding="utf-8").read()):
        name = decl_name(block)
        if not name or not TYPE_PATTERN.match(name) or name in ALREADY_DEFINED:
            continue
        if ("type %s " % name) in existing:
            continue
        added.append("\n".join(comments + block))
    if apply and added:
        with open("%s/types_extra.go" % TARGET, "a", encoding="utf-8") as fh:
            fh.write("\n" + "\n\n".join(added) + "\n")
    print("%s types_extra.go 追加 %d 个类型" % ("写入" if apply else "将写入", len(added)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
