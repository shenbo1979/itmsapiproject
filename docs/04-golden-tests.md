# 04 黄金用例（兼容性回归）设计（M0 交付物④）

目标：把“老接口契约”变成可执行的回归测试，任何改造（模块搬迁、分包、前端切换）都必须先过它。

## 1. 目录与流程

```
tests/golden/routes.json              兼容清单（tools/api_inventory.py 生成）
tests/golden/itms_routes.txt          路径清单（便于 diff）
tests/golden/cases/_skeleton.json     用例骨架（83 条：switch 17 / olt 17 / tr069 47 / 待定 2）
tests/golden/cases/selfcheck.json     自检用例（校验骨架服务自身，6 条，已全绿）
tests/golden/records/                 录制基线（--record 输出，默认不入库）
tools/run_golden.py                   回放器
```

```bash
make golden-init                 # 生成/刷新骨架（从兼容清单）
python3 tools/run_golden.py --status                     # 看进度（todo/active/skip 统计）
make selfcheck                   # 起本地骨架 + 跑自检用例（当前 6/6 PASS）
python3 tools/run_golden.py --cases tests/golden/cases/selfcheck.json --strict
python3 tools/run_golden.py --module switch --limit 5     # 按模块/条数抽样
```

## 2. 用例格式

```json
{
  "path": "/itms/handleSwitchInfo/",
  "module": "switch",
  "provider": "go",
  "method": "POST",
  "query": {"nAct": "0"},
  "expect": {"http": 200, "code": 0, "required": ["message", "data.list"]},
  "status": "todo"
}
```

- `query`：**按老前端习惯用 query 传参**（axios `params`），实现侧必须兼容 query/form；
- `expect.required`：点号路径，校验字段存在性（不校验值，避免环境差异导致抖动）；
- `status`：`todo`（待补断言）/`active`（参与回归）/`skip`（临时跳过，`--force` 可强制跑）。

## 3. 安全边界（重要）

回放器默认目标 `http://127.0.0.1:18092`（本工程骨架服务）。
**指向线上（8092/8090 或任意非本机地址）必须显式加 `--allow-remote`，并需获得授权**——
按现行约定，未经指令不得对原系统发起任何请求。

```bash
# 需要针对线上做兼容性验收时（须先获授权）：
python3 tools/run_golden.py --base-url http://127.0.0.1:8092 --allow-remote --module switch
```

## 4. 基线录制与比对（M1/M2 使用）

1. 在**老服务**上录制基线：`--record tests/golden/records/<模块或阶段>`；
2. 人工裁剪基线中的环境相关字段（时间戳、累计流量、随机码等）后入库；
3. 新服务实现完成后，把 `expect` 补成 `code` + 关键字段，并逐步把 `status` 置为 `active`；
4. CI 对 **switch-only / olt-only / all** 三种装配各跑一遍（避免“单模块包能跑、all 包报错”）。

## 5. CI 建议

```yaml
# 伪代码
- run: make check-inventory          # 清单是否过期
- run: go build ./... && go test ./...
- run: make golden-init
- run: python3 tools/run_golden.py --cases tests/golden/cases/selfcheck.json --strict
- run: TXAPI_MODULES=switch make selfcheck   # 分包组合各跑一遍
```

## 6. 当前状态

### 2026-09-21（M1：WEB 骨架 + 注册链路 + switch 全部功能）

| 指标 | 值 |
|---|---|
| 自检用例 | **12，全绿**（注册链路 3 条、业务接口 `-3` 守卫 3 条、新命名空间台账 1 条） |
| switch 冒烟用例 | **17，全绿**（`make selfcheck-switch`：先注册，再断言 16 条已迁入接口的真实结果） |
| 注册链路单测 | 18 个（`go test ./internal/registration/`） |
| switch 模块单测 | 36 个（28 条厂商解析用例 + 8 条业务用例；1 个继承自老工程的解析缺口标记为 skip） |
| 自检环境 | 强制隔离：`TXAPI_REGISTRATION_DIR=/tmp/... + TXAPI_DATA_DIR=/tmp/...`；自检用独立端口 18093，可与演示并存 |
| 老接口返回码 | 未注册 `-3`；已注册：switch 16 条返回真实结果，olt/tr069 33 条返回 `-1 迁移中` |

### 2026-09-20（M0）

| 指标 | 值 |
|---|---|
| 骨架用例 | 83（switch 17 / olt 17 / tr069 47 / 待定 2） |
| 自检用例 | 6，全绿（`code`/`required` 断言 + 大小写容错 + `-4` 语义） |
| 已录制基线 | 无（M1 起在获授权后录制） |
