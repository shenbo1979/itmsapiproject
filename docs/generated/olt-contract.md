# OLT 接口契约基线（自动生成，勿手改）

生成工具：`tools/olt_contract_scan.py`（输入全部为只读：`tests/golden/routes.json`、老单体 `olthandler.go`/`main.go`、老壳 `chunk-itmsApi.js`、`internal/modules/olt/module.go`、`deploy/modules/olt.manifest`）。

| 指标 | 数量 |
|---|---|
| 老单体 `/itms/*` 中归于 olt 的路由 | 19 |
| `internal/modules/olt/module.go` 声明 | 19 |
| `deploy/modules/olt.manifest` 前缀条数 | 11 |
| 未归类候选（需确认归属） | 0 |

> ⚠️ **OLT 用 `Act`，不是 switch 的 `nAct`**；逐条参数也不同（`saveData` / `connData` / `trace` / `reqData` / `RegProFileId`）。新实现必须继续用 `r.FormValue`（同时兼容 query 与 form，见 docs/01 §2），返回体仍是 `{code,message,data}`。

> 「Act 分支」只统计 `switch <Act 变量>` 一级的 `case`（嵌套 switch 已排除）；「老壳包装函数」来自压缩产物——老壳用 `post(var,null,{params:{...r}})` 透传，**逐字段参数在压缩产物中不可见**，需由 P0-2 真机只读录制补齐（见 docs/09）。

## 1. 逐条契约

| 路径 | handler | 注释 | 参数（老代码 `r.FormValue`） | Act 分支 | 连设备 | 含写 | 老壳包装函数 |
|---|---|---|---|---|---|---|---|
| `/itms/handleBoardAndSlotInfo/` | `handleBoardAndSlotInfo` | 初始板卡及插槽相关信息 | `trace`、`connData`、`saveData`、`refresh` | — | 是 | — | — |
| `/itms/handleConnectOltUser/` | `handleConnectOltUser` | 监听OLT连接用户信息 | `Act`、`saveData` | 0,1,2,3（form:Act） | — | `SQL 写` | — |
| `/itms/handleCurrentConfiguration/` | `handleCurrentConfiguration` | OLT当前配置信息 | `Act`、`connData` | 0,1（form:Act） | 是 | — | — |
| `/itms/handleDBAProfile/` | `handleDBAProfile` | DBA模版 | `connData`、`saveData` | 0,1,2（saveData(JSON 字段 Act)） | 是 | — | — |
| `/itms/handleLineProfile/` | `handleLineProfile` | ONT线路模版 | `connData`、`saveData` | 0,1,2（saveData(JSON 字段 Act)） | 是 | — | — |
| `/itms/handleLinkAggregationInfo/` | `handleLinkAggregationInfo` | 端口汇聚功能 | `Act`、`connData`、`saveData` | 0,1,2（form:Act） | 是 | — | — |
| `/itms/handleONTAutoFind/` | `handleONTAutoFind` | 查找发现新的ONT设备 | `connData` | — | 是 | — | — |
| `/itms/handleONTAutoMatic/` | `handleONTAutoMatic` | ONT自动注册配置 | `Act`、`saveData` | 0,1（form:Act） | — | `os.WriteFile`、`os.MkdirAll` | — |
| `/itms/handleONTListAndStatus/` | `handleONTListAndStatus` | ONT配置/状态 | `trace`、`Act`、`connData`、`reqData`、`saveData` | 0,1,2,3,4,5,6,9（form:Act） | 是 | `os.MkdirAll` | — |
| `/itms/handleONTRegisterProfile/` | `handleONTRegisterProfile` | ONT注册模板 | `Act`、`saveData` | 0,1,2,3（form:Act） | — | `SQL 写` | — |
| `/itms/handleONTRegistration/` | `handleONTRegistration` | ONT一键注册 | `trace`、`RegProFileId`、`connData`、`saveData` | saveData(JSON 字段 Act) | 是 | — | — |
| `/itms/handleOltConfigAutoSave/` | `handleOltConfigAutoSave` | (OLT)OLT配置自动保存 | `Act`、`connData`、`saveData` | 0,1（form:Act） | 是 | — | — |
| `/itms/handleOltConnect/` | `handleOltConnect` | 初始化OLT相关信息 | `trace`、`Act`、`connData`、`refresh` | 0,1（form:Act） | 是 | `os.MkdirAll`、`SQL 写` | — |
| `/itms/handleOltLogList/` | `handleOltLogList` | 渲染OLT日志列表 | `Act`、`reqData` | 0,1,2（form:Act） | — | `SQL 写` | — |
| `/itms/handleOltSavedConfig/` | `handleOltSavedConfig` | (OLT)OLT保存配置 | `connData` | — | 是 | — | — |
| `/itms/handleOltTerminalUser/` | `handleOltTerminalUser` | 处理OLT登录用户 | `Act`、`connData`、`saveData` | 0,1,2,3（form:Act） | 是 | — | — |
| `/itms/handleSrvProfile/` | `handleSrvProfile` | ONT业务模板(服务模板) | `connData`、`saveData` | 0,1,2（saveData(JSON 字段 Act)） | 是 | — | — |
| `/itms/handleTr069Profile/` | `handleTr069Profile` | TR069管理模板 | `connData`、`saveData` | 0,1,2（saveData(JSON 字段 Act)） | 是 | — | — |
| `/itms/handleVLANFunctionModule/` | `handleVLANFunctionModule` | VLAN功能模块 | `connData`、`saveData` | 0,1,2,3（saveData(JSON 字段 Act)） | 是 | — | — |

## 2. 未归类候选（老壳在调，必须确认归属才能避免 404/-4）

（无）

## 3. 声明一致性（routes.json / module.go / manifest）

- `routes.json` 的 olt 集合：19 条
- `module.go` 的 `LegacyRoutes()`：19 条
- `manifest` 的 `legacy_routes`（前缀语义）：11 条
- 一致性由 `make olt-contract-check` 校验（不一致即失败）
