#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把搬运过来的顶层函数按需改成 *Module 方法：
凡是用到 m./m.store./m.deviceFileDir 的函数都需要 receiver（顶层声明都从第 0 列开始）。
"""
from __future__ import annotations

import re
import sys

TARGET = "/itmsapiproject/internal/modules/switch"
FILES = ["handlers.go", "port_detail.go", "terminal.go", "collection_db.go", "sshclient.go", "rpcapi.go"]
TOP_LEVEL = re.compile(r"^(func|type|var|const)\b")


def convert(path: str, apply: bool) -> None:
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()

    starts = [index for index, line in enumerate(lines) if TOP_LEVEL.match(line)]
    changed = 0
    for position in range(len(starts) - 1, -1, -1):
        start = starts[position]
        end = starts[position + 1] if position + 1 < len(starts) else len(lines)
        if not lines[start].startswith("func "):
            continue
        signature = lines[start]
        if re.match(r"func \([^)]*\) ", signature):
            continue  # 已经是方法
        body = "\n".join(lines[start:end])
        if re.search(r"\bm\.", body):
            lines[start] = signature.replace("func ", "func (m *Module) ", 1)
            changed += 1

    if apply and changed:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    print("%s：%d 个函数加上 *Module receiver%s" % (path.split("/")[-1], changed, "（已写入）" if apply else ""))


def main() -> int:
    apply = "--write" in sys.argv
    for name in FILES:
        convert("%s/%s" % (TARGET, name), apply)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
