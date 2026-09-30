# tests/golden —— 老接口黄金用例

| 文件 | 说明 |
|---|---|
| `routes.json` | 兼容清单（`make inventory` 生成，含模块归属与判定依据） |
| `itms_routes.txt` | 路径清单，便于 diff 与评审 |
| `cases/_skeleton.json` | 用例骨架（`make golden-init` 生成；`status` 默认 `todo`） |
| `cases/selfcheck.json` | 自检用例（校验骨架服务：健康、能力、命名空间、大小写容错、`-4`、Python 侧提示） |
| `records/` | 录制基线输出目录（默认被 `.gitignore` 忽略） |

## 常用命令

```bash
make golden-init                                    # 刷新骨架
python3 tools/run_golden.py --status                # 进度统计
make selfcheck                                      # 起本地骨架 + 自检（默认 6/6 PASS）
python3 tools/run_golden.py --module switch --limit 5
python3 tools/run_golden.py --cases tests/golden/cases/selfcheck.json --strict
```

## 约定

1. **默认只打本机 18092**（本工程骨架）。指向线上须显式 `--allow-remote` 并已获授权。
2. 用例用 **query 传参**（老前端 axios `params` 习惯）；实现必须同时兼容 query 与 form。
3. 断言只校验 `code` 与字段存在性，不校验易变值（时间、累计量、随机码）。
4. 兼容等级与风险点见 `docs/01-api-compat.md`；用例格式见 `docs/04-golden-tests.md`。
