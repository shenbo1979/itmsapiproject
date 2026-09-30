# 00 现状与目标（只读调查结论）

> 调查时间：2026-09-20；所有结论来自**只读**巡检（进程、端口、nginx、源码、数据目录），未改动任何原系统。

## 1. 现状：OLT+TR069 与 Switch 各自由哪些部件构成

| 部件 | 形态 | 端口/入口 | 源码位置 | 负责的功能 |
|---|---|---|---|---|
| `itmsmain` | Go 单片 | 8090 | `/switchProject/switchmain`（单一 module，含 OLT/TR069 + Switch） | `/itms/handleXxx/` 共 52 条（**OLT 17 + TR069(Go) 16 + Switch 17 + 待定 2**） |
| `/itms-py` | Python | 经 fusiongateway(4438) 或 WSGI | `/itms-py/*.py`（vTr69Api 5103 行、oltApi 179KB、vTr69SocketMain 93KB…） | `/client/fnXxx/` 共 31 条（CPE/TR069 业务：参数、WAN、WiFi、SIP、QoS、工单、模板…） |
| `itmssocketmain` | Go | 8012 | `/itmsProject`（`cmd/server` + `api/handlers/util.go`，已有 Go 重写雏形） | TR069/CWMP 协议侧（CPE 主动上报/下发） |
| `switchapi` | Go 模块 | 8092 | `/switchapi` | `/itms/handleSwitch*` 等 18 条（switch 模块，已从单体拆出） |
| `switchweb` | Node | 8091 / 54433 | `/switchweb` | 新 WEB 壳（静态页 + `/itms/*` 反向代理 + HTTPS） |
| 老 WEB 壳 | Vue 打包 | nginx 4433/9901 | `/fusiongateway/web` | 老 ITMS 控制台：`chunk-itmsApi.js`（`/itms` + `/client`）、`chunk-switchApi.js` |

nginx 分流（`/etc/nginx/conf.d/default.conf`）：`location /itms/` → `127.0.0.1:8090`；`location /` → `127.0.0.1:4438`（`fusiongateway_g`，`/client/*` 走这条）。

## 2. 数据与状态分布（决定迁移与分包的关键事实）

| 数据 | 位置 | 规模/结构 | 归属 |
|---|---|---|---|
| 设备台账/场景/型号/协议/用途、注册信息、采集快照 | `/home/switch_rpc_api.db`（SQLite，200MB） | **仅 10 张表**：`tbDeviceInformation`、`tbManufacturer`、`tbModel`、`tbProtocol`、`tbPurpose`、`tbVenue`、`tbValidation`、`tbSwitchCollectionSnapshot`、`tbSwitchCollectionInterfaceStat` | 全部是 **Switch** 相关（OLT/TR069 的表不在这个库里） |
| TR069/CPE 参数与模板 | `/home/tr069file/**`（JSON 文件） | 8 个条目共 312KB；单台 CPE 参数 **2266 项**（308KB）；模板名形如 `LAN-100`、`PPPOE上网`、`会客室-INTERNET` | TR069 |
| TR069 服务器配置 | `/home/tr069file/tr69serverinfo/tr69serverinfo.json` | `szHost/szPort/nPeriodicInformInterval/szUsername…` | TR069 |
| 授权/注册 | `/opt/itmsapikey/{itmsapikey.key,registration.state}`、`/txapikeygen/*`（Node 写成）、`/dev/shm/verify_info.json` | 云窗口字段：`AuthExpire/RegistTime/ServiceExpire/State/TR069/IPTV/IPPBX/VNAAS/ThirdInterface…` | 平台级 |
| 采集/嗅探配置 | MySQL/文件（旧实现）与 SQLite（新 switchapi 已入库） | — | Switch |

**重要发现**：`/home/switch_rpc_api.db` 被 `switchapi` 与 `itmsmain` **两个进程同时读写**，
2026-09-20 上午因此发生写锁争用 + 事务泄漏，导致整站接口假死（前端一直显示“正在读取注册状态”）。
→ 统一工程必须落实「一模块一库 / 一进程一库」，或强制 WAL + `_busy_timeout` + 单写者。

## 3. 目标：三种交付形态（同一源码）

```
itmsapiproject（同一 commit）
   ├─ 构建产物 A：switch-only 包   →  只装 switch 模块（服务/DB/前端菜单）
   ├─ 构建产物 B：olt-only 包      →  只装 olt+tr069 模块（内含 Go 层 + Python 层 + socket 层）
   └─ 构建产物 C：all 包           →  两者并集；install.sh --modules 选择启用
```

三种形态的差异应**只体现在文件集合与模块开关**（manifest 驱动），而不是代码分叉。

## 4. 模块边界（M0 结论，供 M1/M2 使用）

| 模块 id | 内容 | 依赖 | 运行形态建议 |
|---|---|---|---|
| `switch` | `/itms/handleSwitch*`、`getTerminalMAC`、`handleDeviceInformation`、`handleRpcApiConfig`、`handleVlan*` 等 17 条 + 采集/嗅探后台任务 | 无 | 独立服务（8092），独立 SQLite |
| `olt` | `handleOltConnect/ONTAutoFind/ONTListAndStatus/LineProfile/SrvProfile/DBAProfile/BoardAndSlotInfo/LinkAggregationInfo/CurrentConfiguration/VLANFunctionModule` 等 17 条 | 无 | 独立服务（沿用 8090 或新端口） |
| `tr069` | Go 侧 16 条（`handleTr069*`、`handleDeviceInfo/Status/Delete`、`handleCpeGroupInfo`、`handleVersionInfo`…）+ Python 侧 31 条 `/client/fn*` + socket 层 | `olt`（同一套 OLT/ONT 上下文） | Go 层 + Python 层协同；建议网关按路径分流 |
| `platform`（core） | 注册/授权（`handleRegistrationInfo`、`validateThirdToken`）、RPC-API 配置、菜单/能力查询 | — | 常驻，随任一包安装 |
