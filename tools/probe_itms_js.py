#!/usr/bin/env python3
"""只读探针：了解老 ITMS 前端打包文件里 /itms/ 调用的写法。"""
import re

SRC = "/fusiongateway/web/assets/js/chunk-itmsApi.js"
OUT = "/tmp/itms_js_probe.txt"

text = open(SRC, encoding="utf-8", errors="replace").read()
positions = [m.start() for m in re.finditer(r"/itms/", text)]

with open(OUT, "w", encoding="utf-8") as fh:
    fh.write(f"file={SRC}\nlen={len(text)}\nhits={len(positions)}\n\n")
    for i, pos in enumerate(positions[:8]):
        window = text[max(0, pos - 260):pos + 320].replace("\n", " ")
        fh.write(f"--- #{i} @{pos} ---\n{window}\n\n")
    # 看看有没有 sourcemap 线索 / 请求封装名
    for pattern in (r"sourceMappingURL=[^\s*]+", r"\.post\([^)]{0,40}", r"axios[^,;]{0,40}"):
        found = re.findall(pattern, text)[:5]
        fh.write(f"pattern {pattern} -> {found}\n")
print(f"written {OUT}")
