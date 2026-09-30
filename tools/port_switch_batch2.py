#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""第二批迁移的机械搬运脚本（一次性工具，保留以便审计"哪些代码从哪来"）。

从 /switchapi（只读）按行区间/符号名提取代码到 internal/modules/switch/，并做最小改名：
  - package main → package switchmod
  - 老包级函数 → 模块/Store 方法（便于不再使用包级全局）
  - 老包级全局（DB、路径、登录锁）→ 模块注入（此处仅登记，实际改动由后续编辑完成）

用法：python3 tools/port_switch_batch2.py            # 打印将生成的文件与行数
      python3 tools/port_switch_batch2.py --write    # 实际写入
"""
from __future__ import annotations

import argparse
import os
import re
import sys

SWITCHAPI = "/switchapi"
TARGET = "/itmsapiproject/internal/modules/switch"
HEADER = "// 由 tools/port_switch_batch2.py 从 {source} 的 {range} 段搬运（package 改名、包级全局改为注入）。\n"


def slice_lines(path: str, start: int, end: int | None) -> list[str]:
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()
    return lines[start - 1: (end if end else len(lines))]


def extract_type_blocks(path: str, names: list[str]) -> tuple[str, list[str]]:
    """从 Go 源码里按名字提取 `type X ...`（含 struct 块）定义。"""
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    out, found = [], []
    for name in names:
        pattern = re.compile(r"^type %s\b.*?(?=\n(?:type |func |const |var |// )|\Z)" % re.escape(name),
                             re.MULTILINE | re.DOTALL)
        match = pattern.search(text)
        if not match:
            continue
        block = match.group(0).rstrip()
        # 带上紧邻其上的注释行
        start = match.start()
        prefix = text[:start].splitlines()
        comments = []
        for line in reversed(prefix):
            if line.startswith("//"):
                comments.insert(0, line)
            else:
                break
        out.append("\n".join(comments + block.splitlines()) if comments else block)
        found.append(name)
    return "\n\n".join(out) + "\n", found


def write(path: str, content: str, apply: bool) -> None:
    if apply:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(content)
    print("%s %d 行 → %s" % ("写入" if apply else "将写入", content.count("\n"), path))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true", help="实际写入（默认只预览）")
    args = parser.parse_args()

    # 1) SSH/Telnet 客户端 + 厂商解析（switchdb.go 末尾整段）
    ssh = slice_lines(os.path.join(SWITCHAPI, "switchdb.go"), 811, None)
    ssh_text = "\n".join(ssh)
    edits = [
        ("func Connect(", "func (m *Module) Connect("),
        ("func ConnectWithLoginInfo(", "func (m *Module) ConnectWithLoginInfo("),
        ("func ConnectWithOptions(", "func (m *Module) ConnectWithOptions("),
        ("requireSwitchRegistration()", "m.requireRegistration()"),
        ("recordSwitchLoginConnection(host, port, 3, loginInfo.Username, loginInfo.Password, loginInfo.Maker)",
         "m.recordLoginConnection(host, port, 3, loginInfo.Username, loginInfo.Password, loginInfo.Maker)"),
    ]
    for old, new in edits:
        ssh_text = ssh_text.replace(old, new)
    write(os.path.join(TARGET, "sshclient.go"),
          HEADER.format(source="switchapi/switchdb.go", range="811-末尾（SSH 客户端 + 厂商解析）") + ssh_text + "\n",
          args.write)

    # 2) RPC-API(HTTP JSON-RPC) 通道（rpc_client.go 的 httpClient + ExecuteCmds + executeCmds）
    rpc = slice_lines(os.path.join(SWITCHAPI, "rpc_client.go"), 230, 601)
    rpc_text = "\n".join(rpc)
    rpc_text = rpc_text.replace("var httpClient = &http.Client{", "// rpcHTTPClient 只读（无状态），保留为包级以避免每请求新建连接池。\nvar rpcHTTPClient = &http.Client{")
    rpc_text = rpc_text.replace("httpClient.Do(", "rpcHTTPClient.Do(")
    rpc_text = rpc_text.replace("func ExecuteCmds(", "func (m *Module) ExecuteCmds(")
    rpc_text = rpc_text.replace("func executeCmds(", "func (m *Module) executeCmds(")
    rpc_text = rpc_text.replace("return executeCmds(host, port, cmds, format, \"\", \"\")",
                                "return m.executeCmds(host, port, cmds, format, \"\", \"\")")
    rpc_text = rpc_text.replace("requireSwitchRegistration()", "m.requireRegistration()")
    write(os.path.join(TARGET, "rpcapi.go"),
          HEADER.format(source="switchapi/rpc_client.go", range="230-294（RPC-API JSON-RPC 通道）") + rpc_text + "\n",
          args.write)

    # 3) 老 models.go 里被搬运代码引用的类型定义
    names = ["JSONRPCRequest", "CmdParams", "JSONRPCResponse", "RPCError", "Client", "InteractiveOptions",
             "SwitchInterfaceInfo", "TransceiverInfo", "SwitchVlanMapping", "RoutingTable", "SwitchLoginInfo",
             "SwitchDeviceDetail", "SwitchFanInfo", "SwitchTemperatureSensor", "SwitchRequest",
             "LocalDeviceInformation", "InterfaceVlanif"]
    types_text, found = extract_type_blocks(os.path.join(SWITCHAPI, "models.go"), names)
    missing = [n for n in names if n not in found]
    if missing:
        print("未找到的类型：%s" % missing, file=sys.stderr)
    write(os.path.join(TARGET, "types_extra.go"),
          HEADER.format(source="switchapi/models.go", range="按类型名提取") + types_text,
          args.write)

    # 4) 登录后的 JSON-RPC 执行封装（switch_login_file.go 的 ExecuteCmdsWithLoginInfo）
    login = slice_lines(os.path.join(SWITCHAPI, "switch_login_file.go"), 77, 84)
    login_text = "\n".join(login)
    login_text = login_text.replace("func ExecuteCmdsWithLoginInfo(", "func (m *Module) ExecuteCmdsWithLoginInfo(")
    login_text = login_text.replace("rpcResp, err := executeCmds(", "rpcResp, err := m.executeCmds(")
    login_text = login_text.replace("recordSwitchLoginConnection(", "m.recordLoginConnection(")
    write(os.path.join(TARGET, "login_exec.go"),
          HEADER.format(source="switchapi/switch_login_file.go", range="77-84（ExecuteCmdsWithLoginInfo）") + login_text + "\n",
          args.write)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
