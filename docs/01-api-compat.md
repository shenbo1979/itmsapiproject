# 01 API 兼容设计（M0 交付物①）

事实清单由工具生成，**请以生成结果为准**：
`docs/generated/api-inventory.md`（表格明细）、`tests/golden/routes.json`（机读）、
`tests/golden/itms_routes.txt`（路径清单）。生成命令：`make inventory`。

## 1. 结论速览（2026-09-20 扫描）

| 接口面 | 数量 | 提供方 | 说明 |
|---|---|---|---|
| `/itms/*`（老 Go 单体） | 52 | `itmsmain` 8090 | 归模块：switch 17、olt 17、tr069(Go) 16、待定 2 |
| `/client/*` | 31 | `/itms-py` Python | CPE/TR069 业务（参数、WAN、WiFi、SIP、QoS、工单、模板…） |
| 老 WEB 壳实际调用 | `/itms` 48 + `/client` 31 | 同上 | = **最小兼容集**（必须 1:1 保持可用） |
| 新 `switchapi` 已实现 | 18 | Go 8092 | switch 模块已拆分完成 |

## 2. 兼容总原则（“丝滑”的含义）

1. **路径不改**：52 条 `/itms/handleXxx/` 与 31 条 `/client/fnXxx/` 原样保留；新接口另开命名空间
   `/api/v2/<module>/...`，老路径永久保留（可标 deprecated，不删）。
2. **传参不改**：老前端用 axios `post(url, null, {params})`，即 **query 参数**；老代码另有 form 提交。
   → 新实现统一用 `r.FormValue`，同时兼容 query 与 form 两种传法。
3. **返回体不改**：`{code, message, data}`；`code` 语义沿用 `0 成功 / -1 参数或业务错误 /
   -2 授权过期 / -3 未注册`；新增 `-4 = 模块未安装`（见第 4 节）。
4. **字段与类型不改**：历史字段名大小写混排（如 `Tr069profile`、`szRPCxxx`），数字字段以**字符串**返回；
   允许新增字段，**禁止重命名或改类型**（老前端按字面量取值、直接字符串比较）。
5. **大小写与尾部斜杠不改**：扫描发现老前端调用 `/itms/HandleUploadFile/`（大写 H），
   而 Go 路由是 `/itms/handleUploadFile/` → **新实现必须同时接受两种写法**（Go 的 `ServeMux` 区分大小写），
   否则该功能在老界面上是隐藏的 404。

## 3. 最小兼容集（P0）与兼容等级

| 等级 | 范围 | 要求 |
|---|---|---|
| P0 | 老 WEB 壳实际调用的 48 + 31 条 | 路径/参数/返回体逐字段一致；黄金用例必须全绿 |
| P1 | 老单体有路由、老壳未调用（6 条：`handleItmsUpgraded`、`handleItmsVersion`、`handleRpcApiConfig`、`handleUploadFile`、`handleVlanClassifier`、`validateThirdToken`） | 保证可调用且不报错；`validateThirdToken` 属登录链路，需按 P0 对待 |
| P2 | 老壳存在但后端不存在（如大小写变体） | 新实现兼容或明确返回 `-4/-1`，不得 404 裸奔 |

> `handleUploadFile` 同时出现在 P1（未调用）与 P2（大小写不一致）中：说明老前端定义了大写路径但可能未使用，
> 需要旁证（问前端作者或抓线上日志）后再定级。

## 4. 未启用模块的行为（分包后的关键约定）

只装 switch 的客户访问 OLT 接口时，必须得到**可读错误**而不是 404：

```json
{"code":-4,"message":"当前设备未安装该功能模块（olt）","data":{"module":"olt","installed":["switch"]}}
```

前端壳据此隐藏菜单并在直接访问 URL 时提示，而不是白屏或死转圈。

## 5. 兼容层落地方式（M1 实现要点）

```
internal/compat/
  registry.go     老路径 -> 模块处理器 的显式映射表（与 tests/golden/routes.json 同源）
  fallback.go     未命中时：已装模块 -> 404 语义化提示；未装模块 -> -4
  legacy_form.go  query/form 双兼容解析 + 老式 JSON 包装（code/message/data）
```

- 兼容层是**显式白名单**（由 `routes.json` 生成/校验），避免“隐式通配”导致新老语义混淆；
- 每个模块的 `Register()` 只注册自己的新命名空间；老路径由兼容层统一转发，卸载模块时自动转为 `-4`；
- 每次改兼容层必须跑 `make golden`（见 `docs/04-golden-tests.md`）。

## 6. 未决问题（需业务确认）

1. `/client/*`（31 条，Python）是随 `tr069` 模块一起迁，还是保持独立服务由网关分流？
   → 影响打包与安装脚本（`deploy/modules/tr069.manifest` 已按“可能包含 Python 层”预留）。
2. 老壳 `chunk-*Api.js` 之外，是否存在**第三方/脚本**直接调用 `/itms/*`？需抓线上访问日志确认 P1 等级。
3. ~~`handleTestDiag`、`handleVLANFunctionModule` 的模块归属待定（扫描器已列为“待人工确认”）~~ →
   **已定（2026-09-22）**：`handleVLANFunctionModule` → **olt**、`handleTestDiag` → **tr069**
   （依据 handler 定义所在的处理器文件）；已在 `tools/api_inventory.py` 加显式归属规则并重新生成清单，
   模块 `LegacyRoutes()` 与 `*.manifest` 同步；契约明细见 `docs/generated/olt-contract.md`
   （`make olt-contract-check` 校验三方一致）。

## 7. M1 进展：平台层注册/授权 + switch 全部功能已迁入（2026-09-21）

| 接口 | 归属 | 状态 |
|---|---|---|
| `/itms/handleRegistrationInfo/`（nAct=0/1） | platform | ✅ 真实实现（与老工程逐字段一致），见 `internal/registration/` |
| `/itms/validateThirdToken/` | platform | ✅ 按线上现状兼容（可信内网、不做令牌强校验） |
| `/api/v2/platform/registration/`（新命名空间） | platform | ✅ 与老路径同一处理器，供新前端壳使用 |
| switch 16 条（台账/基础资料/设备详情/端口/VLAN/终端 MAC/交互终端/采集/嗅探/RPC 配置） | switch | ✅ **全部真实实现**（见 docs/06、docs/07） |
| `/api/v2/switch/ledger`、`/api/v2/switch/ping`（新命名空间） | switch | ✅ 供新前端壳使用 |
| olt 17 条、tr069 16 条 | olt / tr069 | ⏳ 未注册 `-3`；已注册 `-1 迁移中`（M2 迁入） |

新增语义（前端必须区分处理，见 `web/README.md` 第 2、3 节）：
- 注册无效（未注册 `-3` / 已过期 `-2`）→ 前端跳注册页，且响应 `data` 直接是注册信息（可回显草稿记录）；
- 模块未安装 `-4` → 前端隐藏菜单，不当作参数错误。
