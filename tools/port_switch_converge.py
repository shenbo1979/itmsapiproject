#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""统一收敛脚本：循环修复搬运后的编译错误，直到 BUILD_OK。

处理的错误形态：
  1) undefined: NAME                      → 若 NAME 是模块/Store 方法，给调用点补 receiver 前缀；
                                            若 NAME 是标准库包，补 import；否则报告（需要人工搬代码）。
  2) NAME undefined (type *Module/Store ...) → 给 NAME 的定义补 receiver。
  3) undefined: m / s                     → 给所在顶层函数补 receiver。
"""
from __future__ import annotations

import glob
import os
import re
import subprocess

MOD = "/itmsapiproject/internal/modules/switch"
STDLIB = {"bufio", "bytes", "encoding/json", "errors", "fmt", "io", "log", "math", "net", "net/http",
          "net/url", "os", "path/filepath", "regexp", "sort", "strconv", "strings", "sync", "time",
          "unicode/utf8", "context", "database/sql"}
TOP_LEVEL = re.compile(r"^(func|type|var|const)\b")


def read(path: str) -> str:
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def write(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


def build() -> str:
    result = subprocess.run(["go", "build", "-gcflags=-e", "./..."], cwd="/itmsapiproject",
                            capture_output=True, text=True,
                            env={**os.environ, "GOFLAGS": "-mod=mod", "GOPROXY": "off", "GOSUMDB": "off"})
    return (result.stdout + result.stderr).strip()


def methods(pattern: str) -> set[str]:
    found: set[str] = set()
    for path in glob.glob(MOD + "/*.go"):
        found |= set(re.findall(pattern, read(path), re.MULTILINE))
    return found


def add_receiver(path: str, func_name: str, receiver: str) -> bool:
    lines = read(path).splitlines()
    for index, line in enumerate(lines):
        if re.match(r"^func %s\(" % re.escape(func_name), line):
            lines[index] = line.replace("func ", "func (%s %s) " % (receiver, "*Module" if receiver == "m" else "*Store"), 1)
            write(path, "\n".join(lines) + "\n")
            return True
    return False


def add_receiver_for_line(path: str, line_no: int, receiver: str) -> bool:
    lines = read(path).splitlines()
    starts = [index for index, line in enumerate(lines) if TOP_LEVEL.match(line)]
    owner = None
    for start in starts:
        if start <= line_no - 1:
            owner = start
        else:
            break
    if owner is None or not lines[owner].startswith("func ") or re.match(r"func \([^)]*\) ", lines[owner]):
        return False
    lines[owner] = lines[owner].replace("func ", "func (%s %s) " % (receiver, "*Module" if receiver == "m" else "*Store"), 1)
    write(path, "\n".join(lines) + "\n")
    return True


def add_import(path: str, name: str) -> bool:
    text = read(path)
    if ('"%s"' % name) in text:
        return False
    match = re.search(r"import \(\n", text)
    if match:
        text = text[:match.end()] + '\t"%s"\n' % name + text[match.end():]
    else:
        text = text.replace("package switchmod\n", 'package switchmod\n\nimport (\n\t"%s"\n)\n' % name, 1)
    write(path, text)
    return True


UNDEFINED = re.compile(r"^(internal/modules/switch/([a-z_]+)\.go):(\d+):(\d+): undefined: ([A-Za-z0-9_]+)$")
NO_METHOD = re.compile(r"^(internal/modules/switch/[a-z_]+\.go):(\d+):(\d+): ([A-Za-z0-9_]+) undefined \(type \*(Module|Store) has no field or method ([A-Za-z0-9_]+)\)$")

remaining_decl = None
for round_index in range(25):
    output = build()
    if "internal/modules/switch" not in output:
        print("第 %d 轮：BUILD_OK" % (round_index + 1))
        break
    module_methods = methods(r"^func \(m \*Module\) ([A-Za-z0-9_]+)")
    store_methods = methods(r"^func \(s \*Store\) ([A-Za-z0-9_]+)")
    fixed = 0
    unresolved = []
    for raw in output.splitlines():
        line = raw.strip()
        match = NO_METHOD.match(line)
        if match:
            name, receiver = match.group(6), ("m" if match.group(5) == "Module" else "s")
            for path in glob.glob(MOD + "/*.go"):
                if add_receiver(path, name, receiver):
                    fixed += 1
                    break
            continue
        match = UNDEFINED.match(line)
        if not match:
            if "undefined:" in line or "redeclared" in line:
                unresolved.append(line)
            continue
        rel, file_name, line_no, name = match.group(1), match.group(2), int(match.group(3)), match.group(5)
        path = "/itmsapiproject/" + rel
        if name in ("m", "s"):
            if add_receiver_for_line(path, line_no, name):
                fixed += 1
            continue
        if name in STDLIB:
            if add_import(path, name):
                fixed += 1
            continue
        if name in module_methods:
            lines = read(path).splitlines()
            lines[line_no - 1] = lines[line_no - 1].replace(name + "(", "m." + name + "(", 1)
            write(path, "\n".join(lines) + "\n")
            fixed += 1
            continue
        if name in store_methods:
            prefix = "s." if file_name == "collection_db" else "m.store."
            lines = read(path).splitlines()
            lines[line_no - 1] = lines[line_no - 1].replace(name + "(", prefix + name + "(", 1)
            write(path, "\n".join(lines) + "\n")
            fixed += 1
            continue
        unresolved.append(line)
    print("第 %d 轮：修正 %d 处；未解决 %d" % (round_index + 1, fixed, len(unresolved)))
    if fixed == 0:
        remaining_decl = unresolved
        break

if remaining_decl:
    print("\n需要人工搬运的符号：")
    for item in remaining_decl[:40]:
        print(" ", item)
print(build()[:1500] or "BUILD_OK")
