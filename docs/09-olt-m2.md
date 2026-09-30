# 09 M2：OLT/ONU 管理（现状、契约、数据与分批计划）

> 施工时间：2026-09-22 起；继续遵守施工边界：**不修改、不操作任何原工程与原系统**——
> `/switchProject`、`/fusiongateway`、`/itms-py`、`/home/olt/**`、`/sxzdb/db/itms.db` 只作只读参考。

## 0. 施工政策（2026-09-22 起，来自业务方明确要求）

> **新接口只沿用老系统的「接口名（路径）与业务流程」；该改的就改、可优化的就优化、有 BUG 的必须修复。**

由此产生的口径变化（覆盖本文件与 docs/01 早前"逐字保持契约"的表述）：

| 项 | 旧口径（M1 沿用） | 新口径（本政策） |
|---|---|---|
| 接口名/路径 | 逐字不变 | **不变**（前端与第三方仍按老路径调用） |
| 业务流程 | 逐字不变 | **不变**（查询/新增/修改/删除的动作语义、与设备交互的步骤保持一致） |
| 字段名 | 逐字不变 | 前端需要的字段名保持不变（如 `nID`/`szOltName`）；**纯内部或明显不合理的字段可优化** |
| 响应细节 | 逐字不变（含怪算法） | **允许修正缺陷**：分页口径、时间格式、空参数提示等按合理语义实现，并在 docs 记录偏差 |
| 已知 BUG | 照搬 | **必须修**（如 SQL 拼接注入面、分页算法、静默失败） |
| 连接方式 | 仅 Telnet | **Telnet 与 SSH 都要支持**（老 OLT 只有 Telnet；新实现按 `Type`/端口自动选择） |

已按新政策修正/待修正的清单，见 §5.2。


| 维度 | 事实 |
|---|---|
| 接口面 | 老单体 `/itms/*` 中归 olt **18 条**（原 17 条 + `handleVLANFunctionModule`，见 §3） |
| 实现位置 | `/switchProject/switchmain/olthandler.go`（5232 行 / 121 个函数），全部接口为 Go 提供 |
| 参数/vs switch | **OLT 用 `Act`（不是 switch 的 `nAct`）**；另有 `saveData` / `connData` / `trace` / `reqData` / `RegProFileId` |
| Act 两种来源 | ① form 字段 `Act`（多数接口）② `saveData` 里的 JSON 字段 `X.Act`（`handleDBAProfile` / `handleLineProfile` / `handleSrvProfile` 等） |
| 设备通道 | **Telnet**（`connectOltTelnet`，账号取 `oltuser`，`Type/Port`，默认 23）；OLT 172.20.247.88:23、10.38.123.58:12112 实测可连 |
| 数据面①（结构化） | 老库 `/sxzdb/db/itms.db`（344KB，经 `sxzdbmid:8306` 中间件）的 5 张表：`oltuser` / `oltregprofile` / `oltboard` / `oltontlist` / `tbOltOperationLog` |
| 数据面②（文件缓存） | `/home/olt/<OLTIP>/{currentconfiguration.json,savedconfiguration.txt,slot_ont_file/*.txt,ontautomaticcfg.json}` → **2026-09-22 起已入库表**（`tbOltDeviceSnapshot`/`tbOltDeviceBoard`/`tbOltDevicePonPort`/`tbOltDeviceGePort`/`tbOltOntCache`/`tbOltConfigCache`/`tbOltAutoRegConfig`），老目录只作**只读兜底**；映射与口径见 docs/02 §4.2 |
| 数据面③（实时） | 板卡/ONT 状态由 Telnet 现场取；`oltboard`/`oltontlist` 只是缓存 |
| 新工程现状 | `internal/modules/olt` 已完成 **第一批迁移**：`/itms/handleOltLogList/` 为真实实现（模块库 `olt.db`）；其余 17 条仍返回 `-1 过渡期` |

**与 Python 无关**：`/itms-py/oltApi.py`（179KB）在 `/itms-py`、`/fusiongateway` 内**零引用**，
OLT 接口不经 Python（nginx `/itms/` → `127.0.0.1:8090`）。该文件是否废弃需业务口头确认（docs/03 待确认 #1/#6 一并确认）。

## 2. 契约基线（工具生成，勿手改）

```bash
make olt-contract         # 生成 docs/generated/olt-contract.md + tests/fixtures/olt-contract.json
make olt-contract-check   # 校验：routes.json / module.go / manifest 三方一致 + 生成物最新（CI 用）
```

- 输入（全部只读）：`tests/golden/routes.json`、老单体 `olthandler.go`/`main.go`、老壳
  `chunk-itmsApi.js`、`internal/modules/olt/module.go`、`deploy/modules/olt.manifest`；
- 「Act 分支」只统计与 Act 相关的 `switch` 一级 `case`（兼容 `case act == 0 || act == 9:` 与
  `default:` 内的嵌套 `switch handleact`），并标注 Act 来源（`form:Act` / `saveData(JSON 字段 Act)`）；
- 「老壳包装函数」来自压缩产物：老壳用 `post(var,null,{params:{…r}})` 透传，**逐字段参数在压缩产物中不可见**，
  需由真机只读录制补齐（见 §5）。

## 3. 已解决的清单问题（2026-09-22）

`handleVLANFunctionModule`、`handleTestDiag` 原在 `routes.json` 中为 `unclassified`（老壳在调却无归属 →
兼容层会 404/`-4`）。依据 handler 定义位置确认归属并落地：

| 路由 | 归属 | 依据 | 落地改动 |
|---|---|---|---|
| `/itms/handleVLANFunctionModule/` | olt | 定义在 `olthandler.go` | `tools/api_inventory.py` 加显式归属；重新生成清单；`olt/module.go` +1；`olt.manifest` 前缀规范化 |
| `/itms/handleTestDiag/` | tr069 | 定义在 `tr069handler.go` | 同上；`tr069/module.go` +1；`tr069.manifest` +1 |

**工具回归修复（2026-09-22）**：`internal/modules/olt/module.go` 的 `LegacyRoutes()` 改用指针接收者
（`func (m *Module)`）后，`tools/olt_contract_scan.py` 的旧正则（只认 `func (Module)`）静默解析出 0 条，
`make olt-contract-check` 于是报「仅清单有 18 条；仅 module.go 有 []」。已修：
正则同时兼容两种接收者，且**匹配不到但源码里存在 `LegacyRoutes` 时直接报错并指出要改哪里**（不再静默失效）；
随后重新生成 `docs/generated/olt-contract.md` 与 `tests/fixtures/olt-contract.json`，`make olt-contract-check` 通过。

## 4. 数据迁移与「丝滑过渡」

工具：`tools/migrate_olt_data.py`（老库只读；详见 docs/02 §4/§6）。

```bash
make migrate-olt-data      # dry-run：预览差异（新增/变更/一致/清理），不写盘
make migrate-olt-apply     # 幂等落库到 <DataDir>/olt.db（默认 /home/tx/olt.db）
make migrate-olt-check     # 两通道对账（8306 中间件 vs 直读 itms.db：行数 + 主键）
make migrate-olt-replace   # 切流当天取精确副本（清空目标表后重写）
```

### 4.1 文件类缓存 → 表（2026-09-22 已落地）

老系统写在 `/home/olt/<OLTIP>/` 下的采集缓存与配置**全部可以入库表**（结构化 JSON / 整篇文本），
已按实情建表并完成导入，映射与口径见 **docs/02 §4.2**。落地要点：

| 老文件 | 表 | 读取口径 |
|---|---|---|
| `currentconfiguration.json` | `tbOltDeviceSnapshot` + `tbOltDeviceBoard` + `tbOltDevicePonPort` + `tbOltDeviceGePort` | 设备快照/板卡/端口（后续 ONT 页面与总览页直接读表） |
| `slot_ont_file/<槽位>.txt` | `tbOltOntCache` | `handleONTListAndStatus` Act=0：**表优先、老文件兜底** |
| `savedconfiguration.txt` / `olt_cur_cfg.txt` | `tbOltConfigCache` | 文本配置（Kind 区分） |
| `ontautomaticcfg.json` | `tbOltAutoRegConfig` | 自动注册配置（口令加密入库） |

- 模块启动时若缓存表为空 → 自动导入一次（`importFileCacheIfEmpty`），日志给出各表条数；
- **幂等**（按主键覆盖）、**老目录只读**（导入前后文件 md5 不变）、每表带 `SourceFile/SourceMTime/UpdatedAt` 可追溯；
- 本机真机缓存实测：快照 1、板卡 5、PON 口 16、GE 口 4、ONT 2、配置文本 1（自动注册 0：老目录无该文件）。

已落地口径：
1. **一模块一库**：OLT 独立 `olt.db`，绝不与 `switch_rpc_api.db` 共库；表名按规范改 `tbOlt*`，**列名原样**；
2. **老程序零改动**：老库只读（`mode=ro` / 只发 SELECT），老 itmsmain、itmssocketmain、Python、老控制台照旧；
3. **口令两代可混存**：老库明文；新库口令按 `/home/switchfile/aeskey.key` 加密、账号列可读；
   **模块取口令必须「密文优先、明文兜底」**（对应工具里的 `plain_value`）；
4. **可反复重跑**：同样数据重跑写入 0 行（凭据按解密后语义比较，避免随机 IV 造成假更新）；
5. **过渡期增量同步**：老控制台期间新增/修改的数据，重跑一次即带入新库（表小、秒级）；
6. **可回滚**：写前自动备份 `<DataDir>/backup/olt.db-<时间戳>`，写后逐列核对 + `integrity_check`。

## 5. 分批计划

> 原则沿用 switch 的经验（docs/06/07）：先「只读接口 + 真机回归」闭环，再搬写操作与后台任务。

### 第一批（只读 + 纯库表；已落地 2026-09-22）
**已迁入（2026-09-22 上午）**：`/itms/handleOltLogList/`（操作日志查询/删除，纯模块库、不连设备），
文件：`internal/modules/olt/{module,store,models,response,handler_log}.go` + `handler_log_test.go`。

**已迁入（2026-09-22 下午，含设备连接层）**：

| 接口 | 已迁入分支 | 主要文件 |
|---|---|---|
| `/itms/handleConnectOltUser/` | `Act=0` 查询 `tbOltUser`（`ORDER BY Automatic DESC`） | `handler_device.go`、`store.go` |
| `/itms/handleOltConnect/` | `Act=0` 读当前配置缓存（**连带返回该设备在台账的连接/在线状态 `Automatic`**）；`Act=1` 采集（版本/温度/板卡）+ 标记 `Automatic` | `handler_device.go`、`collect.go` |
| `/itms/handleBoardAndSlotInfo/` | 全量与单槽只读采集（`display version` / `temperature 0` / `board 0[/x]`） | `handler_board_config.go`、`parse_board*.go` |
| `/itms/handleCurrentConfiguration/` | `Act=0` 读已保存配置；`Act=1` `display current-configuration` 采集 | `handler_board_config.go`、`collect.go` |
| `/itms/handleONTListAndStatus/` | `Act=0` 读槽位 ONT 缓存（**表优先、老文件兜底**：`tbOltOntCache` → `slot_ont_file/*.txt`） | `handler_ont.go`、`store_cache.go` |
| 设备连接层（新包） | Telnet + SSH 双通道：登录、提示符/分页判定、命令执行、会话串行 | `internal/oltconn/{session,telnet,ssh,conn}.go` + `conn_test.go` |
| 登录提示判定（共用新包） | 用户名/口令提示的**空白归一化**判定，OLT 与交换机两个连接器共用同一套规则 | `internal/devcli/prompt.go` + `prompt_test.go` |

契约细节（已写成回归断言，后续批次同样适用）：

| # | 细节 | 说明 |
|---|---|---|
| 1 | **顶层统计字段** | `handleBoardAndSlotInfo` 的 `OntOnlines`/`OntTotal`/`SlotidRefreshTime`、`handleONTListAndStatus` 的 `count`/`nPage`/`nLimit` 都在**响应顶层**（老前端按 `res.count` 取值），不能只用 `core.APIResponse` |
| 2 | **ONT 列表的 `nLimit` 是请求值** | 不像 `handleOltLogList` 会 `nLimit=Page*Limit` 后按 count 收敛；这里的 `nLimit` 原样回传，`count=0` 时因 `omitempty` 字段缺省 |
| 3 | **`Message` 的 `"sueccess"` 笔误** | `handleOltConnect` Act=0 的老笔误逐字保留（不改老前端判断） |
| 3.1 | **Act=0 连带设备连接/在线状态** | `handleOltConnect` Act=0 只读缓存（不连设备，`tbOltDeviceSnapshot` 表优先、老文件兜底），并随响应 `data` 返回该设备在台账 `tbOltUser.Automatic` 的连接状态（`Automatic=1` 视为已连接/在线，否则缺省/0）。新增只增字段 `OltCurrentConfig.Automatic`（旧前端忽略，不影响既有字段） |
| 4 | **老解析口径原样保留** | `PonType` 取"最后见到的端口类型"（混合板卡落到 GE）；GE 行按 `fields[3]/[5]/[6]/[9]` 取 NativeVLAN/Speed/Duplex/Link；离线板卡不下钻；`FrameDesc` 只认 5/13/21/23 槽位 |
| 5 | **单槽刷新是合并语义** | `handleBoardAndSlotInfo` 单槽采集后按板卡 Id **就地合并**回缓存（不是替换整个列表），并回写 `SlotidRefreshTime` |
| 6 | **文件缓存落点** | 新目录 `<DataDir>/olt/<OLTIP>/`（可写；env `TXAPI_OLT_FILE_DIR` 可覆盖），老目录 `/home/olt`（env `TXAPI_OLT_LEGACY_FILE_DIR`）**只读兜底**；槽位 ONT 缓存按"槽位号"逐一回落，切换当天老控制台写的缓存仍可读 |
| 7 | **凭据读取** | 库中口令"密文优先、明文兜底"解密；`connData` 缺账号/口令时按 IP 从 `tbOltUser` 回落补全（老实现会拿空口令直接登录，必然失败） |
| 8 | **只读命令集** | 本批只发 `display version` / `display temperature 0[/x]` / `display board 0[/x]` / `display current-configuration` / `screen-length`（单测断言：全程无写命令） |

#### 5.2 与老实现的偏差、已修 BUG

| # | 类别 | 说明 |
|---|---|---|
| 1 | **修 BUG** | 老实现 `parseOLTBoardSlotDetail`/`collectOltCurrentConfig` 在端口解析**之前**调用 `mergeCachedOltBoardDetail`，于是缓存里的 PON 口被整段拷入、随后端口循环再 append 一遍 → 同一板卡的 PON 口列两遍（有缓存时 8 个口会显示成 16 个）。新实现改为"先解析本次回显、再补缓存缺失字段"，语义不变且不再重复（`parse_board_detail.go` 注释 + 单测） |
| 2 | **修 BUG** | `handleConnectOltUser` Act=0 老实现忽略 `QueryRows` 错误（静默返回空列表，前端表现为"没有账号"）；新实现返回 `-1 / Error querying database` 并写服务端日志 |
| 3 | 优化 | 连接参数缺失时从 `tbOltUser` 回落补全（省一次必然失败的登录往返） |
| 4 | 优化 | 命令读取统一走 `internal/oltconn` 的"静默窗口 + 自动翻页"，不再依赖老实现逐轮 200ms 读窗口与 `(config)` 标记兜底；`display current-configuration` 仍按老口径等独立行 `return` |
| 5 | **待迁入（第二批）** | 写分支：`handleConnectOltUser` `Act=1/2/3`、`handleONTListAndStatus` `Act=9`（采集 + 明细补全 + 后台刷新）与 `Act=1..6`（重启/激活/禁用/注册）。这些分支当前返回 `-1`「第二批待迁入」提示——**不做假成功、不静默失败** |
| 6 | 待核对 | GE 行的列序假设来自老实现，需真机录制核对（P0-2） |
| 7 | 待确认 | `BoardTypeMap` 缺 `GPHF`/`GPLF`/`XGHD` 等在网板型 → 这些板卡不会下钻端口（老实现原样行为）。补板型会改变展示行为，需业务确认后按"可优化的就优化"处理 |
| 8 | **修 BUG** | `handleConnectOltUser` Act=1/2/3 的 SQL 全部是字符串拼接（`fmt.Sprintf` 把 saveData 直接拼进语句）→ 改为绑定参数，消除注入面；新增/修改校验 `Username`/`IP` 必填、修改/删除校验 `Id` 有效（老实现会写出空账号，或对无效 Id 静默返回成功） |
| 9 | 优化 | OLT 口令入库前用平台层 AES 密钥加密（账号列保持可读；已是密文则不重复加密），与 `tools/migrate_olt_data.py` 的落库口径一致；查询时按"密文优先、明文兜底"解密后返回（老口径明文回前端） |
| 10 | 优化 | `handleConnectOltUser` Act=0 补上 `Automatic`/`ScanInterval`/`TemplateId` 三列（老实现只查 7 列，前端永远看到 0，无法显示"当前连接的是哪台"）；老库里这三列可能是 NULL，查询用 `COALESCE(...,0)` 兼容两代数据 |
| 11 | 优化 | 新增/修改/删除成功后写 OLT 操作日志（等价老实现 `MakeOltLogSave(IP,"OLT连接用户",新增/修改/删除,RemoteAddr,Username,"")`，`RemoteAddr` 仍按老库形态原样写入）；写失败只记服务端日志，不阻断主流程 |
| 12 | **口径变更（业务方规格 2026-09-22）** | OLT 的**连接类型 `Type`：0 = TELNET（默认）/ 1 = SSH**（老口径 1 = RPC-API / 2 = Telnet / 3 = SSH 中，OLT 本就不使用 RPC-API）。为不破坏老壳与迁移数据，通道层**同时接受 2 = TELNET / 3 = SSH**；`connData` 未带 `Type` 时置 `TypeAuto(-1)`，按端口推断（22 → SSH，其余 → TELNET），库中凭据回落时优先采用该设备库里的 `Type` |
| 13 | **按规格校验字段（前后端同规则）** | IP：合法 IPv4 或合法域名；`Type`：0/1；`Username`：大小写字母 + 数字 + 下划线，≤ 32；`Password`：大小写字母 + 数字 + 特殊字符组合，≤ 32；`Port`：1-65535（空值回落 23）。前端在提交前校验并提示（不发起请求），后端独立再校验（老实现完全不校验，脏数据直接入库）。**编辑时口令未改动则跳过强度校验**（只校验长度与字符集），避免老库存量口令无法保存 |
| 14 | 前端列表口径 | 「OLT 设备台账」列表只显示 **IP / 端口 / 备注 / 连接类型**（登录账号移到行 `title` 提示中）；类型标签按 0/2 → TELNET、1/3 → SSH 显示（兼容存量数据） |
| 15 | 列表列结构与批量删除（业务方规格 2026-09-22） | 行结构与交换机台账**完全对齐**：勾选列（`device-list-row__checkbox`）+ 状态点 + 三行信息 + 右侧类型标签；面板头部加「全选」勾选框与计数，工具条按勾选集合显示 **「删除 (n)」**（未勾选时禁用、删除后复位），二次确认含选中台数——即与交换机台账同一套批处理交互。后端 `handleConnectOltUser` Act=3 相应支持**单条与批量**：`{"Id":n}`（老契约）、`{"Ids":"n,m"}`（沿用本模块 Ids 逗号串口径）、`[n,m]`（JSON 数组），不存在的 Id 跳过、每个真正删除的设备写一条操作日志 |
| 16 | **修 BUG：刷新页面与所选菜单不同步** | `createApp()` 末尾原先**无条件**调用 `refreshAll()`，导致刷新时即使 hash 停在 `#module=olt`，也会跑交换机工作台刷新并弹出「交换机工作台数据已刷新。」（还白连一次交换机数据；采集配置同样无条件读取）。现改为：**页面加载只初始化"当前选中的模块"** —— 交换机管理才加载采集配置 + 刷新工作台；OLT/TR069 只渲染自己（OLT 页按需拉列表；采集配置在切到「设置」TAB 时按需加载）。同时把"首次进入"语义显式化（`initModuleFromHash(true)` → `activateModule({initial:true})`）：刷新页面时**总是**初始化所选模块，而模块间来回切换仍走"有数据不重复请求"的判断 |
| 17 | **修 BUG：工具条点了没反应 + 列表口径调整（业务方反馈 2026-09-22）** | 两个只在**真实浏览器**里才暴露的缺陷（最小 DOM 桩掩盖了它们）：① 左栏工具条按钮只写了 `id`、**漏了 `data-olt-action`**，而点击靠 `closest("[data-olt-action]")` 委托 → 「连接/新增/修改/删除」全无反应；② 点击委托里对所有命中元素都 `preventDefault()`，浏览器因此**取消了复选框的默认勾选** → 「全选 / 行勾选」看着像没反应。修复：按钮补齐 `data-olt-action`；委托遇到 `INPUT`（勾选框）直接交给 `change` 事件、不做 `preventDefault`。同时按业务方要求调整左栏：**去掉「刷新」按钮**、**「连接」放到「新增」之前**（工具条为 [连接][新增][修改][删除]），列表信息区改为 **`<IP>:<Port>` + 备注**（不再单独占一行显示端口）。**新增真实 DOM 校验**（`tools/olt_ui_dom_check.mjs` + `make olt-dom`，基于 jsdom，真点击/真冒泡/真解析 DOM）以覆盖这类桩测不到的缺陷，并在桩工具里补「工具条按钮必须带 data-olt-action」的静态断言 |
| 34 | **CSS 字体颜色统一：#000000（中性正文/次要文字；保留强调/状态色）（2026-09-28）** | ① 需求：把 styles.css 里**中性正文/次要文字**的字体颜色统一改为 **#000000**，**保留**强调/状态色（`--teal` 链接、`--rose` 错误/告警、`--amber` 警示、成功绿 `#16854a`、在线红 `#ff0000`、端口上下行状态点、深底浅字等）。② **改动**（`web/src/styles.css`）：`:root` 中 `--ink` 由 `#17222f` → `#000000`、`--muted` 由 `#5e6672` → `#000000`（各 1 处，全部 `var(--ink)`/`var(--muted)` 引用自动生效）；未定义变量 `var(--text)`（确认框输入框）直接改为 `#000000`；纯色中性值 `#606266`×3（detail-line span / olt-frame-desc / olt-port-meta）、`#1f2329`（detail-value）、`#909399`（olt-board-image--missing）、`#303133`（olt-board-port）均改为 `#000000`；`var(--ink, #303133)` / `var(--muted, #909399)` 的 fallback 保持不动（变量已定义，fallback 不生效）。**不动**：`#5e6672` 端口 down 状态点、`#17222f` 深色背景、`#7f3240` 错误文案、rgba 半透明状态徽章、深底浅字（tooltip `#fff8ef`、终端 `#dbe9e2` 等）。③ **验证**：`grep` 确认 `#606266`/`#1f2329`/`var(--text)` 清零、`#909399`/`#303133` 仅剩 `var()` fallback；18094 静态服务与本地 `diff` 完全一致（`--ink`/`--muted` 均 `#000000`）。 |

| 33 | **新增 VLAN / 二层互通：VLAN 类型与 VLAN ID 互换位置；删除时 VLAN 可编辑（2026-09-28）** | ① 需求：a) **新增 VLAN / 二层互通**（Type=0）表单里「VLAN 类型」与「VLAN ID」**互换位置**（VLAN 类型在上、VLAN ID 在下，与上行透传表单 F/S/P 在前同口径）；b) **新增 VLAN / 二层互通**的**删除二次确认**框里「VLAN」也像上行透传一样改为**可编辑输入框**，允许确认前修改再删除。② **改动**（`web/src/modules/olt.js`）：a) `olt-vlan-form` 内 `olt-vlan-vlantype-row`（VLAN 类型）上移到 VLAN ID 之前（显隐逻辑不变，仍 `meta.type === 0` 时显示）；b) `removeVLAN` 的 `editable` 条件从 `kind === \"tran\"` 扩展为 `kind === \"tran\" || kind === \"vlan\"`，`proceed`/`effectiveVlanid` 取值同步用 `editableKinds`（可编辑 kind 走 `{ confirmed, value }`，三层接口 `vlanif` 仍只读）。③ **验证**：`node --check web/src/modules/olt.js` 通过；18094 静态服务即时生效（拉取确认表单顺序与 editable 条件均含新内容）。 |

| 18 | **修 BUG：真机 Telnet 登录卡在用户名提示（2026-09-22 实机 `172.20.247.88:23` → `Connection failed: … 登录后未取得命令提示符（已读 20 字节）`）** | 真机（MA5600/MA5800 系列）Telnet 欢迎信息实测为 `\xff\xfb\x01\xff\xfb\x03\r\n>>User name:`（`IAC WILL ECHO` + `IAC WILL SGA` + CRLF + `>>User name:`，**共 20 字节**），用户名提示是 **`>>User name:`（`User` 与 `name` 之间有空格）**。`Login` 原先只匹配 `username`/`login:`，于是**根本不发送用户名**，一直空等到 `4 × DefaultLoginTimeout`（12s）才失败——这既解释了「20 字节」也解释了登录必然卡死。修复：新增 `LooksLikeUsernamePrompt`/`LooksLikePasswordPrompt`，先做**空白归一化**（去空格/制表/换行）再匹配，顺带兼容 `Login name:`、`User  name :`、`>>User password:` 等排版；同时把本地假 OLT（`tools/fake_olt_telnet.py`）改成**与真机逐字节一致**（原来发 `Username:`，正是它掩盖了这个缺陷）。回归：`conn_test.go` 新增 `TestLoginHuaweiUserNamedPrompt`（修复前 12.4s 失败、修复后 1.6s 通过）+ `TestHelpers` 补提示符判定用例；实机 `Act=1&refresh=1` 采集成功（`ConnectTime` 实时刷新、`MA5603T` 板卡/温度入库） |
| 19 | **口径统一：登录提示判定抽成共用包（2026-09-22，业务方要求"对齐交换机模块与老工程"）** | 新增 `internal/devcli`（`LooksLikeUsernamePrompt`/`LooksLikePasswordPrompt`）：**空白归一化**（`User name:` → `username:`）+ **按回显结尾判定**（避免把 `Last login: …` 这类登录横幅当成提示）+ **口令提示要求带冒号**（避免把 `The initial password poses security risks` 当成口令提示而提前发口令）。OLT 侧 `internal/oltconn` 改用该包（`Login` 不再各写一份）；交换机侧 `internal/modules/switch/telnet.go` 同步对齐，**并修掉"提示里带 `>` 就误判成已登录"的老分支**（`>>User name:` 会命中该误判，表现为登录"成功"却把 `""`/`screen-length` 发到登录提示上）；老工程 `/switchapi/rpc_client.go` 用同名规则（`switch_login_prompt.go`）对齐。回归：`devcli/prompt_test.go`、`internal/modules/switch/telnet_login_test.go`、`/switchapi` 的 `TestTelnetSwitchConnectHandlesSpacedUserNamedPrompt`（三处都用真机 20 字节欢迎信息逐字节复现；临时回退老口径时用例会失败，已实测） |
| 20 | **修 BUG：选中「OLT/ONU 管理」后默认不选中台账第一条记录（2026-09-22 业务方反馈）** | `web/src/modules/olt.js` 的 `refresh()` 原先只在"选中项已不存在"时把 `selectedId` 清 0 → 首次进入时列表**没有任何一行是选中态**，右栏一直显示"暂未选择 OLT 设备"（工具条「连接/修改」也不可用）。现改为与交换机台账同口径（`app.js loadDevices`）：空列表 → 无选中；选中项被删除 / 首次进入 → **自动选中台账列表第一条记录**，右栏同步渲染该设备详情。回归：`tools/olt_ui_dom_check.mjs` 增两条真实 DOM 断言（进入页面即"第一条为选中态且右栏渲染其详情"、"台账非空时恰有一行选中"），旧逻辑下实测两条断言均失败 |
| 21 | **前端信息架构：右侧「OLT 设备详情」改为 8 个栏目 TAB（业务方规格 2026-09-22）** | `web/src/modules/olt.js`：右侧面板改为 **OLT状态 / 配置信息 / VLAN / DBA/线路模板 / 业务/TR069模板 / ONT配置/状态 / ONT注册 / 日志** 八个栏目（`role="tablist"` + `role="tabpanel"`，复用交换机工作台的 `.overview-tabs` / `.overview-tab-content` 样式；点击走页面既有的事件委托 `data-olt-action="select-detail-tab"` + `data-tab`，栏目**跨设备保留**）。**当前只有「OLT状态」有数据**：把「连接」采集到的版本 / 机框 / 运行时长 / 板卡数 / ONT 统计与板卡表渲染在该栏目（未连接时提示去左栏工具条点「连接」；点「连接」时**自动切回「OLT状态」**，保证看得到"连接中 → 连接结果"）；其余 7 个栏目为「待迁入（第二批）」占位，占位文案**写明对应老接口**（`handleCurrentConfiguration` / `handleVLANFunctionModule` / `handleDBAProfile`+`handleLineProfile` / `handleSrvProfile` / `handleONTListAndStatus` / `handleONTRegisterProfile`+`handleONTAutoFind` / `handleOltLogList`），不假成功、不展示假数据。同时**右侧面板不再显示**设备详情行（`OLT 设备详情 · <IP>`、`登录账号：… · TELNET · 端口 23 · 已连接 · 备注：…`）、`设备信息/已连接` 工具条与 **连接/修改/删除** 按钮：设备身份改为 `data-olt-device-id`（不产生可见文案，供断言/调试用），设备操作统一由左侧「OLT 设备台账」工具条承担。回归：`make olt-dom` 新增/改写 4 条真实 DOM 断言（8 个栏目且默认 OLT状态、点击「配置信息」TAB 激活并显示待迁入占位、面板内不出现设备行与连接/修改/删除按钮、连接结果落在 OLT状态栏目），`make olt-ui` 桩断言同步更新，`make web-smoke` 校验复用 class 均有样式定义 |
| 22 | **OLT状态 栏目补齐展示信息 + 耗时 + 板卡示意图（业务方 2026-09-22：对照原前端补全）** | 对照老控制台 `chunk-oltonu.js`（OLT 页头部字段）与 `chunk-oltStatus.js`（OLT状态 组件）后补齐四件事：<br>① **耗时**：连接与单槽刷新都按"发起 → 返回"的墙钟时间显示「耗时 X.X 秒」（连接成功标题、概览 tile、机框示意图工具条三处）；<br>② **字段对齐老控制台**：版本 / 补丁、机框（+机框类型）、OLT运行时间、状态刷新时间、在线 ONT / 板卡、机框说明（`FrameDesc`），并补上本工程可得的 **连接时间** 与 **板卡"已插 N 块（机框 M 槽）"**（`FrameTotal`/`FrameCount` 的真实语义是板卡行数/有效行数，见 `parse_board_detail.go`，不臆造"主控数"）；<br>③ **板卡示意图**：按槽位横排单板竖条图（老控制台素材 41~47 × 719），图上按老控制台算法叠加端口 —— PON 口为 `P<i>` + 在线/离线状态点 + `<在线>/<总数> ONT`；上行 GE 口为 `P<i> / GE` + 链路状态点 + 速率（`auto`→自适应、`auto_10/100/1000`→10/100/1000 Mbps）+ 双工（含 `auto_full/auto_half`）+ `VLAN<NativeVLAN>`；端口纵坐标沿用老控制台**按板卡分组的偏移**（`olt.js` 的 `BOARD_PORT_GROUPS`：H805GPBD 系 `i<4?i*45+195:i*45+280`、SCUN 系 `i*53+375`、GIC 系 `i*55+315` …），状态点配色沿用老控制台（在线 `#5da500` / 离线 `#ff0000`）；未知型号显示"暂无该板卡的示意图"占位（不发 404）；<br>④ **单槽刷新 / 全部刷新**：示意图每块板卡一个「刷新」、工具条一个「刷新所有板卡状态」，走 `/itms/handleBoardAndSlotInfo/`（`saveData={SlotId}`；`SlotId` 为空=全部，`refresh=1` 强制读设备而不是回缓存），结果按槽位合并回当前板卡列表并更新 状态刷新时间 / ONT 统计 / 本次刷新耗时；板卡明细表补 **描述**、**运行状态**（老口径 `Normal`→`[OK]`、`Active_normal`→激活、`Standby_normal`→备用、`Standby_failed`→失效）与单槽刷新按钮。<br>**素材来源与许可**：19 张板卡图从老控制台 `/fusiongateway/web/assets/png/chunk-olt_<型号>.png` 复制为本工程 `web/src/assets/olt/olt_<型号>.png`（仅重命名，逐字节未改）。<br>**回归**：`make olt-dom` 增 6 条真实 DOM 断言（耗时正则、按型号渲染板卡图、图片 HTTP 200 + `image/png`、单槽刷新后 PON 叠加 `P0/P1` + `5/8 ONT` + 状态点、刷新耗时、GE 叠加 `P0 / GE` + `1000 Mbps` + 全双工 + `VLAN1`），`make olt-ui` 同步 5 条（含 API 层板卡数 4 块口径）；假 OLT 的板卡名改为**有素材的真实型号**（`H801SCUN`/`H805GPBD`/`H801GICF`，并新增一块上行板槽位 8）——否则联调只能覆盖"暂无示意图"占位分支；两个工具把写死的 `wait(3000)` 改为**条件轮询 `waitFor()`**（板卡变多后连接 ~3.0s，写死等待会把"连接中"误判成"没渲染"），并把批量删除断言改为按**本轮创建的设备 id** 判定以消除跨轮残留导致的偶发失败。<br>**接口板自然尺寸与文字缩放（业务方 2026-09-23 追加）**：上行/下行接口板（`H801X2CS`/`H801GICF`/`H801GICG`/`H901PILA`）素材本身是矮图（403/359/231px 高，老控制台素材逐字节相同），早期实现把它们强行按 50×403 显示、端口说明文字仍用 719px 基准偏移（如 GIC 系 `i*55+315`、X2CS 系 `i*110+315`），导致文字整体落到图片下方/图外。现在改为**接口板按各自自然尺寸显示**（CSS `olt-board-image--interface` 为 `width/height:auto`，不再强制 50×403），说明文字 `top` 由 `olt.js` 按 `round(老偏移 × 自然高 / 719)` **等比缩放**（如 GICF `top:P0=177/P1=207` 对应 403 高图、X2CS 高 403、GICG 高 359、PILA 高 231），`left` 按 `板宽+4px` 内联给出（class 附加 `olt-board-port--interface`），随尺寸自适应且文字落在端口附近。回归用槽位 8 `H801GICF` 的 GE 叠加（`P0/P1 / GE` + `olt-board-port--interface` + `top:177/207px`）验证。 |
| 23 | **「配置信息」栏目接入真实数据（此前为待迁入占位；后端 `handleCurrentConfiguration` 已于第一批迁入）** | 后端 `/itms/handleCurrentConfiguration/`（`internal/modules/olt/handler_board_config.go`）早已迁移完毕并回归（`Act=0` 读已保存配置——表 `tbOltConfigCache` 优先、老文件兜底；`Act=1` 连设备 `display current-configuration` 采集落盘），但前端 8 栏目里「配置信息」一直是「待迁入（第二批）」占位。本次在 `web/src/modules/olt.js` 把它接入真实数据：<br>① **新增两个端点** `readConfig`（`Act=0`，读缓存）/ `refreshConfig`（`Act=1`，强制采集），`data` 为配置全文字符串；<br>② **切到「配置信息」栏目自动读一次已保存配置**（`select-detail-tab` 分支，`configsReadId` 防重复），命中即显示、未命中回落为引导态——与「OLT状态」的"读库回填"一致，出现即信息；<br>③ **「刷新配置」按钮**（`refresh-config`，`Act=1`）：采集时自动把用户切到本栏目并转圈（`configsLoadingId`），返回后以 `<pre class="olt-config-text">` 逐字展示配置全文并记「上次采集耗时」；连接失败保留旧文本并标记 `error`，不清空已展示配置；<br>④ `configs` 独立于 `connections`（配置只读、不与「连接采集」互锁），一并保留 elapsed 口径；`DETAIL_TABS` 的 `config` 条目去掉 `pending`。<br>**样式**：新增 `.olt-config-text`（`white-space: pre-wrap` + `overflow-wrap:anywhere` + 等宽字体 + 深色终端底 `#10181f`，与 `.ssh-terminal__output` 同口径），并复用 `.device-vlan-toolbar` / `.button--busy` / `.device-loading`。<br>**回归**：`make olt-dom` 改写「配置信息」断言（占位→引导+刷新按钮），新增连设备后点「刷新配置」→ `waitFor` 到 `hostname OLT-E2E` / `ont add` / `return` 的配置全文渲染与耗时；`make olt-ui` 同步 2 条（切栏目激活非占位、刷新配置渲染全文）；`make web-smoke` 校验新增 `.olt-config-text` 有样式定义 |
| 24 | **「配置信息」栏目新增「OLT 登录用户」管理（终端用户读/增/改/删；此前 `handleOltTerminalUser` 为待迁入占位）** | 后端 `/itms/handleOltTerminalUser/`（`internal/modules/olt/handler_terminal_user.go`）全部四个分支迁入并注册进 `LegacyHandlers()`（路由早已在 `LegacyRoutes()` 里），编入「配置信息」栏目的**终端用户**小节：<br>① **Act=0 读用户名**：读已保存配置文本（表 `tbOltConfigCache` 优先、老文件兜底，与 `handleCurrentConfiguration` 同一读路径），空则连设备 `display current-configuration` 刷新，再 `parseOLTTerminalUsers` 解析出 `terminal user name <user>` 的用户名列表（跳过 `history_password`/`buildrun_new_password`/`terminal user password` 行、去重、去引号）；`Message` 沿用老实现笔误 `"sueccess"`<br>② **Act=1 新增**：`terminal user name` 交互（用户名 + 两次口令），回显含 `user profile name` 时发 `root` 取档位（含 `3/2` 判定，否则 `1`）再 `4`/空行/`n` 收尾；否则按老文案报「账号重复或密码格式不正确(1:长度6~15;2…不能包含问号;3…)"<br>③ **Act=2 修改口令**：`terminal user password`（用户（两次新口令），确认 `(y/n)` 回显时回 `n`<br>④ **Act=3 删除**：`undo terminal user name` + 用户名，确认 `(y/n)` 时回 `y`+`n`；否则报「账号不存在或权限不足;」<br>**与老实现的差异**：老实现在写分支**不写操作日志**，本模块按其 #11 同一口径补齐——增/改/删成功后各写一条 `tbOltOperationLog`（`szMenu=OLT登录用户`，`szAct=新增/修改/删除`，`szText=用户名`，`RemoteAddr` 原样写入）并在解锁会话后 `refreshSavedConfiguration` 刷新配置缓存；失败文案逐字保持<br>**前端**（`web/src/modules/olt.js`）：`config` 栏目下新增「终端用户」卡片 —— 端点 `readTerminalUsers`（Act=0）/`addTerminalUser`（Act=1）/`changeTerminalPassword`（Act=2）/`removeTerminalUser`（Act=3）；列表只读展示用户名；新增走同款 `device-form-modal`（用户名 + 口令两次）；每行「修改口令」「删除」，删除先 `confirmAction` 二次确认；变更成功后重拉列表（`refreshTerminalUsers`）<br>**回归**：新增 `handler_terminal_user_test.go`（`TestParseOLTTerminalUsers` 解析、`TestHandleOltTerminalUserRead`/`Add`/`ChangePassword`/`Delete`/`RequiresUserName`）；假 OLT 增加 `terminal user name admin/olt_operator` 配置行与新增/改/删的交互序列回显；`make olt-dom`、`make olt-ui` 各增「终端用户」断言；`make web-smoke` 校验新增样式 |

| 25 | **「VLAN」栏目接入 `handleVLANFunctionModule`（读 + 写增删；此前待迁入占位）** | 后端 `/itms/handleVLANFunctionModule/`（`internal/modules/olt/handler_vlan.go` + `parse_vlan.go`）注册进 `LegacyHandlers()`（路由早已在 `LegacyRoutes()`）：<br>① **Act=0 读**：读已保存配置（表 `tbOltConfigCache` 优先、老文件兜底），空则连设备刷新；`parseOLTVLANConfiguration` 解析出四类信息，用**顶层字段** `vlanifdata`（三层接口）/`vlandata`（smart/q-in-q/stacking + 组播）/`tranvlandata`（上行透传）/`routestaticdata`（静态路由）返回——老壳 `getOltVlan` 直接按 `res.*` 取值；`Message="success"`<br>② **Act=1 新增 / Act=2 删除**：按 `Type`（0 VLAN/二层互通、1 上行VLAN透传含 `(y/n)`、2 三层VLAN接口 `interface vlanif` 序列）逐字下发；写操作先校验 `VlanID` 必填、上行透传的 `Fsp` 三段、三层接口的 `IP+Mask`，缺参在**连设备之前**直接拒绝（老实现缺参 `Operation failed`）<br>③ 写成功后在同一会话 `persistSavedConfiguration`，并补写操作日志（老实现**不写**，`szMenu=VLAN功能`，`szAct=新增/删除`，`szText=Type/VlanID/Fsp/IP/Mask`，`RemoteAddr` 原样写入）<br>**前端**（`web/src/modules/olt.js`）：`vlan` 栏目去掉 `pending`，三张卡片「三层 VLAN 接口 / 上行 VLAN 透传 / VLAN 列表」+ 静态路由只读条；端点 `readVLAN`（Act=0）/`addVLAN`（Act=1）/`removeVLAN`（Act=2）；切「VLAN」栏目自动读一次；新增走 `device-form-modal`（按类型显示 VlanID / Fsp / 类型 / IP+掩码），删除先 `confirmAction` 二次确认，变更后重拉列表<br>**回归**：新增 `handler_vlan_test.go`（`TestParseOLTVLANConfiguration` 解析、`TestSubStringBetween`、`TestHandleVLANFunctionRead`/`ReadViaRefresh`/`AddType0`/`DeleteType1`/`RequiresVlanID`/`BadFsp`）；假 OLT 配置加 `vlanif`/`vlan`/`port vlan`/`ip route-static` 行；`make olt-dom`、`make olt-ui` 各增「VLAN」断言；`make olt-contract-check` 保持 18 条不变 |
| 26 | **修 BUG：MA5603T（Huawei MA5600 系）版本采集（`display version 0/<主控槽>` + `Software Version:` 解析）；假 OLT 增 config 模式** | ① **`parseOLTVersionMessage` 兼容两种版本格式**：`MA5603T` 的 `display version 0/<主控槽>` 回显为大小写混合的 `Software Version: MA5600V800R011C00`（区别于 MA5800 的 `VERSION : MA5800…` 全大写）；下行并按"仅取含 `software` 的行"过滤，避免误捕同屏的 `Base BIOS Version: 216` 等硬件版本<br>② **`findOLTControlBoardSlot` + 采集顺序（collect.go）**：MA5603T 的**主控板不在槽 0**（如槽 6），且裸 `display version` 在该型号上会**弹交互参数提示**（`{ <cr>|backplane|frameid/slotid }:`，纯交互不回版本）。故 collectCurrentConfig 改为**先解析 `display board 0` 的主控槽 → 发 `display version 0/<主控槽>`**；仅当该路径仍取不到版本时才回退裸 `display version`。版本采集同时**后置于板卡/温度采集**，防止未闭合的交互提示污染后续只读命令<br>③ **回归**：`fake_olt_test.go` 假 OLT 新增 `model`（默认 MA5800 行为不变；`fakeModelMA5603`）；`startFakeOLT5603` 的主控槽 6、裸 `display version` 弹提示、`display version 0/6` 回显 `Software Version: MA5600V800R011C00`；新增端到端用例 `TestOltConnectCollectsMA5603T`（断言版本取自 `display version 0/6`、**未发裸 `display version` 兜底**、主控槽 6 不下钻端口、在线业务板照常下钻）<br>④ **前端/工具**：`tools/fake_olt_telnet.py` 补齐 `enable→scroll→config` 的**配置模式提升回显**（`(config)#`；此前缺此分支导致 `make olt-ui`/`olt-dom` 连假 OLT 时报"登录后未能进入配置模式"、VLAN/当前配置等接口全挂——已修）；并新增 `FAKE_OLT_MODEL=ma5603` 环境开关，可单独运行 MA5603T 风格假 OLT（默认仍 MA5800，不改既有断言）<br>**验证**：`go test ./internal/modules/olt/` 全绿（新增端到端 + 既有回归）；`make olt-ui` 由 15 项失败降至仅剩 1 项既有 VLAN 渲染断言（与本案无关，见 #25）。 |
| 28 | **修 BUG：获取机框、OLT 运行时间失败——移除 `display version 0/<主控槽>`，统一用裸 `display version`（2026-09-28 业务方反馈）** | ① **症状**：「OLT状态」页（`handleOltConnect` Act=1 采集后）版本能取到、但**机框（`Frame`/`Product`）与 OLT 运行时间（`OLTRunTime`）取不到/为空**。② **根因**：上一版为了绕开 MA5603T 裸 `display version` 弹交互参数提示，先解析 `display board 0` 的主控槽发 `display version 0/<主控槽>`。但该命令只回显**主控板软件版本**（`Software Version:` 等单板字段），**不含**机框 `PRODUCT` 与 `Uptime`——机框/运行时间只出现在**系统级完整版本文本**（裸 `display version`）里，于是这两个字段恒为空。③ **修复**：collectCurrentConfig 版本采集段**删除 `display version 0/<主控槽>` 分支**，统一只发**裸 `display version`**。裸命令在 MA5603T 上会先弹 `{ <cr>|backplane|frameid/slotid }:` 交互提示，`oltconn.RunCommandUntilSignal` 命中 `{ <cr>|... }:` 参数提示会自动补一个回车（`pushContinuation`），提示闭合后设备回显完整版本文本并回到 `#` 提示符——命令结束判定依然成立，无需再规避裸命令。版本采集仍后置于板卡/温度之后，未闭合提示不再污染后续只读命令。④ **回归**：`fake_olt_test.go`/`tools/fake_olt_telnet.py` 的 MA5603T 风格改为「裸 `display version` 首弹提示 → 补回车回显完整版本（`Software Version:` + `PRODUCT` + `Uptime`）」；`TestOltConnectCollectsMA5603T` 断言只发裸 `display version`、**不再发 `display version 0/6`**，且 `Product=MA5603T`、`OLTRunTime=42天7小时30分9秒`；`findOLTControlBoardSlot` 及其单测移除；`go test ./internal/modules/olt/` 全绿、`go vet`/`gofmt` 通过。 |
| 35 | **修 BUG：DBA 模板写操作范围校验恒不生效（老实现 `&&`/`||` 逻辑缺陷）；并补写操作日志** | `handleDBAProfile`（DBA 模板，第二批迁入，`internal/modules/olt/handler_profile.go` + `parse_profile.go`）：<br>① **修 BUG**：老实现校验用 `Id<10 && Id>512`、`fix<128 && fix>10000000`（同组 `Id<10 && Id>10000000`、`fix<128 && fix>10000000`、`assure<128 && assure>10000000`、`max<128 && max>10000000`）—— 同一变量不可能同时小于下界**又**大于上界，故这些范围校验**恒永不触发**（等于没校验）。新实现改为正确闭区间（`Id<10 || Id>512`、带宽 `v<128 || v>10000000`），并在非法时**连设备之前**返回对应中文提示（老实现缺参 `Operation failed`）<br>② **Act=0 读**：读已保存配置（表 `tbOltConfigCache` 优先、老文件兜底），空则连设备刷新；`parseOLTDBAProfiles` 解析 `[global-config]` 段的 `dba-profile add` 行（`profile-id` 越界 10-512 跳过；`Type` 取字段中 `type*`；`Fix/Assure/Max` 按关键字取值）<br>③ **Act=1 新增 / Act=2 删除**：按类型（type1 fix / type2 assure / type3 assure+max / type4 max / type5 fix+assure+max）逐字下发 `dba-profile add/delete`；删除只校验 Id；成功后同一会话 `persistSavedConfiguration` + 补写操作日志（`szMenu=DBA模板`，`szAct=新增/删除`，`szText=Id/Name/Type/Fix/Assure/Max`）<br>**回归**：新增 `parse_profile_test.go`（5 种 type 解析、越界/不相关行跳过、`oltCommandArgument` 含引号）与 `handler_profile_test.go`（Act=0 读库、Act=1/2 经假 OLT成功下发并写日志、Id/Name/带宽非法拒绝且不连设备、未知 Act 返回 invalid Action）；`go test ./internal/modules/olt/` 全绿、`make olt-contract-check` 保持 18 条不变。`handleLineProfile`（线路模板）**留待后续**，不在本次范围 |
| 36 | **「DBA/线路模板」栏目接入 DBA 模板前端（读 + 写增删；此前待迁入占位）** | 前端 `web/src/modules/olt.js`（后端 `/itms/handleDBAProfile/` Act=0/1/2 已在 #35 迁入并注册进 `LegacyHandlers()`）：<br>① **「DBA/线路模板」栏目**去掉 `pending` 占位，承载 DBA 模板内容：切到该栏目自动读一次（Act=0 读已保存配置解析，不连设备），渲染一张 `data-table olt-vlan-table` 表格，列对齐老控制台 `chunk-dba_lineTem.js`——**模板ID / 模板名 / DBA类型 / 固定带宽(kbps) / 保证带宽(kbps) / 最大带宽(kbps) / 操作**；`Type` 字段按老控制台文案映射（`type1-固定带宽`/`type2-保证带宽`/`type3-保证带宽+最大带宽`/`type4-最大带宽`/`type5-固定带宽+保证带宽+最大带宽`），未用的带宽字段显示 0；「线路模板」（`ont-lineprofile`，老接口 `handleLineProfile`）仍待迁入，卡片底部保留占位说明（不假成功）<br>② **新增 DBA**：`device-form-modal` 弹窗（模板ID 10-512 / 模板名 1-33 / DBA类型 select / 按类型显示对应带宽输入行，`change` 委托在 `#module-view` 上联动显隐），提交走 `addDBA`（Act=1）；**删除 DBA**：每行「删除」→ `confirmAction` 二次确认 → 操作反馈弹窗（复用 `.olt-terminal-op*` 样式）→ 成功后重读列表；前端校验与后端 `validateDBAWrite` 同规则（Id 10-512、Name 1-33、带宽 128-10000000）<br>③ **回归**：`web/src/modules/olt.js` 新增 `dba`/`dbaLoadingId`/`dbaForm`/`dbaOpPending` 状态、`readDBA`/`addDBA`/`removeDBA` 端点、`loadDBA`/`refreshDBA`/`openDBAForm`/`submitDBAForm`/`removeDBA`；`tools/fake_olt_telnet.py` 与 `internal/modules/olt/fake_olt_test.go` 的假 OLT 增加 `[global-config]` 段的 5 种 type `dba-profile add` 行与 `dba-profile add/delete` 写命令回显（Python 假 OLT 的 `DBA_PROFILES` 随写命令实时变化，使 Act=1/2 后的重读能反映增删）；`tools/olt_ui_integration.mjs`（DOM 桩补 `replaceChildren`/`createTextNode`/`submitOltDBAForm`）与 `tools/olt_ui_dom_check.mjs`（真实 jsdom 点击 + change 联动）各增 DBA 断言；`make olt-ui` 与 `make olt-dom` 全绿（0 失败）、`go test ./...` 全绿、`make olt-contract-check` 保持 18 条不变 |
| 37 | **迁入 ONT 线路模板读写（`handleLineProfile`，DBA/线路模板栏目，此前待迁入）** | 后端 `internal/modules/olt/handler_line_profile.go` + `parse_line_profile.go` + `models.go`（4 个结构体，与老 `models.go` 562-589 逐字一致），并把 `/itms/handleLineProfile/` 注册进 `LegacyHandlers()`（路由与清单早已在 `LegacyRoutes()`/manifest，契约仍 18 条不变）：<br>① **Act=0 读**：读已保存配置（表 `tbOltConfigCache` 优先、老文件兜底，与 `handleDBAProfile` 同一读路径），空则连设备刷新；`parseOLTLineProfiles` 忠实移植老 `olthandler.go` 2191-2260：只认 `[global-config]` 段、`ont-lineprofile <type> profile-id <id> profile-name <name>`（`profile-id` 越界 10-4092 或非数字跳过），随后收集 `tcont <id> dba-profile-id <dba>`、`gem add <id> eth tcont <t>`、`gem mapping <prot> <to> vlan <vlan>` 直到 `commit`，产出 `OntLineProfile{Id/Name/Type + GemMapping[]}`：`Id` 为 string、`GemToId/GemVlan` 逗号拼接、gem 索引=VLAN 段索引、mapping 索引=逗号子索引（全部保留老怪癖）<br>② **Act=1 新增**：校验 `Id 1-8192`、`Name 1-33`、`PonType gpon/epon`（删除只需 Id+`PonType`）；连设备下发 `ont-lineprofile <type> profile-id <id> profile-name <name>` → `tr069-management enable|disable`（依 `OpenTr069`）→ 按 `Vlan`（`/` 分段、段内 `,` 分 VLAN）**每段一个 T-CONT/GEM Port**：段 i（0-based）`tcont <i+1> dba-profile-id <dba>` + `gem add <i+1> eth tcont <i+1>`，段内第 k 个 VLAN `gem mapping <i+1> <k+1> vlan <vlan>`（`to` 在同一个 GEM 内唯一递增）→ `commit` → `quit`。**与华为标准流程对齐**：每个 / 段各自独立的 T-CONT 与 GEM Port（编号 1-based），不再沿用老实现「tcont 恒为 1、gem/mapping 从 0 起」的不兼容写法——后者在华为真机会导致命令部分/全部失败<br>③ **Act=2 删除**：下发 `undo ont-lineprofile <type> profile-id <id>`（只校验 Id+`PonType`）<br>④ **设备通道**：写命令走 `runCommand`（`RunCommandUntilSignal` 等到 `(config)` 提示符，与 DBA 口径一致），只在模板创建与删除做 `extractOLTFailure` 失败返回，单个 tcont/gem/tr069 保留老实现的忽略继续；最后同一会话 `persistSavedConfiguration`（P1 提速）+ 补写操作日志（老实现没有）：`szMenu=ONT线路模板`、`szAct=新增/删除`、`szText=ProfileId/Name/DbaProfileId/PonType/OpenTr069/Vlan`<br>**回归**：新增 `parse_line_profile_test.go` 与 `handler_line_profile_test.go`（Act=0 读库、Act=1/2 经假 OLT 成功下发并写日志、属性校验拒绝且不连设备、未知 Act）；假 OLT 增加线路模板写回显。`go test ./internal/modules/olt/` 全绿、`go vet`、`go build ./...` 通过、`make olt-contract-check` 保持 18 条不变 |
| 38 | **「DBA/线路模板」栏目接入 ONT 线路模板前端（读 + 写增删；此前后端已迁入、前端待接入）** | 前端 `web/src/modules/olt.js` + `web/src/styles.css`（后端 `/itms/handleLineProfile/` Act=0/1/2 已在 #37 迁入并注册进 `LegacyHandlers()`）：<br>① **「DBA/线路模板」栏目**去掉线路模板的「待迁入」占位条，在同栏目 DBA 卡片下方新增一张 **ONT 线路模板**卡片：切到该栏目自动读一次（Act=0 读已保存配置解析，不连设备），渲染 `data-table olt-vlan-table` 表格，头部列为 **模板ID / 模板名 / PON类型 / 操作**；每条模板下按 `GemMapping[]` 渲染 **T-CONT / DBA 子树行**（`.olt-line-tcont-row`）与 **GEM 端口 / 映射ID / VLAN 子行**（`.olt-line-gem-row`，`.olt-line-indent` 层级缩进），`Type`（gpon/epon）复用 `.olt-dba-type` 浅色小标签<br>② **新增线路模板**：`device-form-modal` 弹窗 `olt-line-dialog`（模板ID 10-4092 / 模板名 1-33 / PON类型 select gpon|epon / DBA模板ID / TR069 enable|disable / VLAN textarea——同一 GEM 内多 VLAN 逗号分隔、不同 GEM 用换行或 `/` 分隔，提交时换行统一成 `/`），走 `addLineProfile`（Act=1）；**删除**：每行「删除」→ `confirmAction` 二次确认 → 操作反馈弹窗（复用 `.olt-terminal-op*`）→ 成功重读；前端校验与后端 `validateLineProfileWrite` 同规则（Id 10-4092、Name 1-33、PonType gpon/epon、DbaProfileId 必填、Vlan 各段 VLAN 1-4094）<br>③ **回归**：`web/src/modules/olt.js` 新增 `line`/`lineLoadingId`/`lineForm`/`lineOpPending` 状态、`readLineProfile`/`addLineProfile`/`removeLineProfile` 端点、`loadLine`/`refreshLine`/`openLineForm`/`validateLineForm`/`submitLineForm`/`removeLine`；`tools/fake_olt_telnet.py` 的假 OLT `[global-config]` 段增两条 `ont-lineprofile gpon profile-id 10/20`（含 tcont + gem mapping）且写命令回显 `ont-lineprofile success`；`tools/olt_ui_integration.mjs`（DOM 桩增 `submitOltLineForm`，新增/删除线路模板写路径断言）与 `tools/olt_ui_dom_check.mjs`（真实 jsdom 点击 + 渲染断言）各增线路模板用例；`node --check` 全部通过
| 39 | **修 BUG：ONT 线路模板新增的 TCONT/GEM/VLAN 配置命令与华为真机不兼容（2026-09-29）** | 后端 `internal/modules/olt/handler_line_profile.go`（`applyLineProfileMapping`）：<br>①**症状**：「DBA/线路模板」新增 ONT 线路模板时命令部分/全部失败——老实现 `tcont 1 dba-profile-id <dba>` 恒用 T-CONT 1 复用、`gem add <i> eth tcont 1` 的 gem 索引发自 0、`gem mapping <i> <j> vlan <v>` 的子索引也发 0 起，且未在提交后退出线路模板子视图，与华为真实设备（MA5600/5800）的 `ont-lineprofile` 视图语义不符，导致配置不完整或下发失败<br>② **修复**：对齐华为标准流程——按 `Vlan` 的 `/` 分段，**每段独立一个 T-CONT 与 GEM Port（1-based）**：段 i（0-based）发 `tcont <i+1> dba-profile-id <dba>` + `gem add <i+1> eth tcont <i+1>`；段内第 k 个 VLAN 映射到该 GEM Port：`gem mapping <i+1> <k+1> vlan <v>`（`to` 在同一个 GEM 内唯一递增，单 VLAN 段恰为 `gem mapping <i+1> 1 vlan <v>`）；`commit` 之后追加 `quit` 退出线路模板子视图收尾。<br>③**回归**：`handler_line_profile_test.go` 的 `TestHandleLineProfileAdd` 断言更新为 `tcont 1/2 dba-profile-id 10`、`gem add 1 eth tcont 1`/`gem add 2 eth tcont 2`、`gem mapping 1 1 vlan 100`/`gem mapping 1 2 vlan 200`/`gem mapping 2 1 vlan 300` 及 `commit`+`quit` 均存在；`internal/modules/olt/fake_olt_test.go` 与 `tools/fake_olt_telnet.py` 的写回显分支补 `quit` 关键词。解析（`parse_line_profile.go`）、前端 `olt.js`（`/` 分段、`,` 分 VLAN 语义不变）不改。`gofmt`/`go vet`/`go build ./...`/`go test ./internal/modules/olt/`、`node --check` 通过。 |
| 40 | **迁入 ONT 业务模板 + TR069 管理模板读写（`handleSrvProfile`/`handleTr069Profile`，业务/TR069模板栏目）** | 后端 `internal/modules/olt/handler_srv_profile.go` + `handler_tr069_profile.go` + `parse_srv_profile.go` + `models.go`（5 个新结构体），并把两个接口注册进 `LegacyHandlers()`；**归属调整**：`handleTr069Profile` 从 tr069 划归 olt（依据老单体定义位置 `olthandler.go` 2960 行起 + 运行时上下文都在 OLT 设备侧），同步更新 `tools/api_inventory.py` 的 `EXPLICIT_MODULE`、`tests/golden/routes.json`（重跑 `api_inventory.py`）、`internal/modules/tr069/module.go` 的 `LegacyRoutes()`、两个 `deploy/modules/*.manifest` 的 `legacy_routes`，并重跑 `make olt-contract`（19 条，`olt-contract-check` 通过）：<br>① **Act=0 读**：读已保存配置（表 `tbOltConfigCache` 优先、老文件兜底），空则连设备刷新；`parseOLTSrvProfiles` 解析 `[global-config]` 段的 `ont-srvprofile <type> profile-id <id> profile-name <name>` + `ont-port pots <N> eth <M>` + 逐条 `port vlan eth <n> translation <vlan>` 直到 `commit`，产出 `OntSrvProfileInfo{Id/Name/Type/Pots/Eths + SrvVlan[]}`；`parseOLTTR069Profiles` 解析 `ont tr069-server-profile add profile-id ... url ... user ...` 行，只回 `Id/Name/AcsUrl/AcsUserName`，**不回密码**（与老契约一致）<br>② **Act=1 新增 / Act=2 删除**（业务模板）：校验 `Id 1-8192`、`Name 1-33`、`PonType gpon/epon`（删除也需要）、`Pots 0-32`/`Eths 0-24`（clamp）、业务 VLAN 1-4094 且最多 8 条；连设备下发 `ont-srvprofile <pon> profile-id <id> profile-name <name>` → `ont-port pots <N> eth <M>`（adaptive 组合）→ 逐条 `port vlan eth <n> <vlan>`（eth 1-based）→ `commit`；删除用 `undo ont-srvprofile <pon> profile-id <id>`<br>③ **Act=1 新增 / Act=2 删除**（TR069 模板）：校验 `Id 1-32`、`Name 1-32`、`AcsUrl 1-63`、`AcsUserName 1-50`、`AcsPassword 1-25`（密码仅新增）；命令 `ont tr069-server-profile add profile-id <id> profile-name <name> url <url> user <user> <pass> auth-realm auth` / `ont tr069-server-profile delete profile-id <id>`<br>④ **修正项**：老校验 `Id<1 && Id>8192`/`Id<1 && Id>32` 等恒假 → 正确闭区间；老壳表单「网口数量→Pots、电话口数量→Eths」错位 → 按语义修正（Eths=网口、Pots=电话口，解析/写命令/前端一致）；写命令统一 `runCommand` 等 `(config)#` 提示符；补写操作日志（`szMenu=ONT业务模板`/`TR069管理模板`，TR069 日志**不含密码**）<br>**回归**：新增 `parse_srv_profile_test.go`（业务/TR069 解析、VLAN 拆分、ID 闭区间）与 `handler_srv_profile_test.go`/`handler_tr069_profile_test.go`（Act=0 读库、Act=1/2 经假 OLT 下发并写日志、adaptive 拼装、校验拒绝不连设备、未知 Act）；假 OLT（`fake_olt_test.go` + `fake_olt_telnet.py`）增加 srv/tr069 写回显与 `[global-config]` 模板行。`go test ./...`、`go vet`、`make olt-contract-check`、`make olt-ui`/`olt-dom` 的 srv/TR069 断言全绿 |
| 42 | **「业务/TR069模板」新增业务模板弹窗对齐老控制台：字段单列显示 + 业务VLAN 帮助文案换用老壳原文（2026-09-30，方案 A：仅前端）** | ① **需求（业务方反馈）**：OLT 详情 →「业务/TR069模板」TAB → 新增业务模板弹窗：a) **字段改为一列显示**（当前走 `.device-form-dialog__body` 默认两列 grid）；b) **业务VLAN列表**帮助说明换用老控制台文案（`/fusiongateway/web/assets/js/chunk-ont_tr069Tem.js` 原文：「仅EPON或SFU的GPON需要添加，每个ETH一行，多个VLAN逗号分隔。例如 ETH1 透传VLAN 2,9；ETH2 透传4015；IPHOST 透传11，则规则如下：2,9 / 4015 / 11 iphost」）。② **改动**（仅前端）：`web/src/styles.css` 把 `#olt-srv-form` 加入单列布局清单（`grid-template-columns: 1fr`，与 VLAN/终端用户/DBA/线路模板弹窗同口径，复用 76px 默认标签列 + 输入框右对齐，标签全部为短文案无需 150px 放宽）；`web/src/modules/olt.js` 的 `olt-srv-vlan` textarea placeholder 改为「仅EPON或SFU的GPON需要添加，每个ETH一行，多个VLAN逗号分隔。例如：2,9 / 4015 / 11 iphost」，`.olt-line-vlan-hint` 改为老壳逐字文案 + 多行 `<code>` 示例（`2,9`（ETH1 透传）、`4015`（ETH2 透传）、`11 iphost`（IPHOST 透传），与线路模板弹窗同款样式）。③ **边界（方案 A 已确认）**：后端/写命令/解析/fake OLT/Go 测试**全部不动**；仍按现有平铺逻辑拆 VLAN（`split(/[\n,]/)` 后 `join(",")`），`11 iphost` 这类示例仅是展示用——若照抄输入，现有前端校验/后端解析会因非纯数字 VLAN 拒绝（暂不支持下发 IPHOST，后续按方案 B 扩展）。④ **验证**：`node --check web/src/modules/olt.js` 通过；`make olt-dom`/`make olt-ui` 既有断言只校验字段存在与默认值、不断言帮助文案，全部保持绿；18094 静态服务即时生效。 |

| 29 | **修 BUG：删除三层 VLAN 接口误报“invalid request: IP and Mask are required”（2026-09-28 业务方反馈）** | ① **症状**：「VLAN」栏目删除**三层 VLAN 接口**（Type=2）失败，返回 `invalid request: IP and Mask are required`，无法下发 `undo interface vlanif<id>`。② **根因**：`handler_vlan.go` 的 `validateVLANWrite` 对写操作（新增/删除共走 `case a==1||a==2`）一视同仁地做必填校验，而 Type=2 分支**无条件要求 IP+Mask**。但删除命令 `undo interface vlanif<VlanID>` 只需 `VlanID`，前端删除时只带 `VlanID`、不带 `IP/Mask` → 被校验拦下，连设备都未连就报“IP and Mask are required”。③ **修复**：Type=2 分支改为——`VlanID` 恒必填；`IP+Mask` 仅在**新增（Act=1）**时必填，**删除（Act=2）**放行。删除命令序列（`undo interface vlanif`）本就只用 `VlanID`，无需地址。④ **回归**：新增 `TestHandleVLANDeleteType2`（Act=2 Type=2 只带 VlanID → 应发送 `undo interface vlanif200` 成功并写「删除」日志）；既有 `TestHandleVLANAddType0`/`RequiresVlanID` 仍通过；`gofmt`/`go vet` 通过。 |
| 32 | **上行透传删除确认：VLAN 与新增表单一样可编辑（2026-09-28）** | ① 需求：删除**上行 VLAN 透传**的二次确认框里，「VLAN」从只读展示改为**可编辑输入框**（与新增表单的 VLAN ID 输入一致），允许确认前修改再删除。② **改动**：a) `web/src/app.js` 的 `confirmAction` 增加可选 `editable` 参数——可编辑时确认框目标渲染成 `<input class="confirm-dialog__target-input">` 而非 `<strong>`；`closeConfirmDialog` 先读取输入值再关框，可编辑时 resolve `{ confirmed, value }`、不可编辑保持布尔（全部既有调用方不感知变化）；`confirm-dialog` 根点击区补回车确认（在输入框内按 Enter 直接确认删除）。b) `web/src/styles.css` 增加 `.confirm-dialog__target-input` 样式（与表单输入框同外观、右对齐）。c) `web/src/modules/olt.js` 的 `removeVLAN`：`kind === "tran"` 时 `confirmAction` 传 `editable: true`，确认后用返回值里的 VLAN ID 删除，弹窗文案同步换成实际删除的 VLAN ID。③ **验证**：`node --check web/src/app.js`、`node --check web/src/modules/olt.js` 通过；18094 静态服务即时生效（拉取确认 app.js/olt.js 均含新逻辑）。 |
| 31 | **上行透传新增表单调整：F/S/P 字段改名、与 VLAN ID 互换位置、修标签遮挡（2026-09-28）** | ① 需求：新增**上行 VLAN 透传**表单里「机框/槽位/端口」字段：a) 文案从「机框/槽位/端口（Fsp）」改为「机框/槽位/端口(F/S/P)」（半角括号）；b) 字段从「VLAN ID 之后」移到「VLAN ID 之前」；c) 修复输入框遮挡说明文字问题；d) 为美观「VLAN ID：」与「机框/槽位/端口(F/S/P)：」右对齐。② **根因（遮挡）**：`.device-form-dialog__body label` 默认 `grid-template-columns: 76px minmax(0,1fr)` + `white-space: nowrap`，F/S/P 标签约 150px 宽超出 76px 标签列，文字溢出到输入框区域被盖住。③ **修复**：`olt.js` 表单内 `olt-vlan-fsp-row` 上移到 VLAN ID 之前并改文案为 `机框/槽位/端口(F/S/P)：`；`styles.css` 将 `#olt-vlan-form label` 统一为 `grid-template-columns: 150px minmax(0,1fr)`（标签列足够容纳 F/S/P 文案），配合默认 `span { text-align: right }`，两行标签右边缘对齐、输入框同一起点；`#olt-vlan-fsp-row span` 保留 `white-space: normal` 兜底换行。④ **验证**：`node --check olt.js` 通过；18094 静态服务即时生效（拉取确认 HTML/CSS 均含新内容）。 |
| 30 | **前端一致性：三层 VLAN 接口删除改为与「配置信息」栏目终端用户删除同一「操作反馈弹窗」模式（2026-09-28）** | ① 在 `web/src/modules/olt.js` 中，删除**三层 VLAN 接口**（以及上行透传/VLAN 列表）此前二次确认后走右上角 toast 提示，而「配置信息」栏目终端用户删除用的是**操作反馈弹窗**（确认后直接在弹窗内显示「正在删除…→ 已删除成功 → 短暂停留 → 关闭并刷新」，失败也留在弹窗内）。② **修复**：为 VLAN 删除新增同款反馈弹窗 `olt-vlan-op-dialog`（复用 `.olt-terminal-op*` 样式类，不新增 CSS）+ `setVLANOpStatus`/`setVLANOpDialogVisible` 辅助函数 + `close-vlan-op` 事件；`removeVLAN` 确认后改挂弹窗（进行中 → 成功停留 300ms → 关闭刷新；失败留在弹窗），不再弹 toast。③ **验证**：`node --check web/src/modules/olt.js` 语法通过；前端为静态 ESM（`server.cjs` 直接供源），无需重新打包，刷新 `txconsole-web`（18094）即生效。 |

### 第二批（写操作 + 前端页面；部分已落地 2026-09-22）

**已迁入**：

| 接口 / 页面 | 已迁入分支 | 主要文件 |
|---|---|---|
| `/itms/handleConnectOltUser/` | **全部四个 Act**：0 查询 / 1 新增 / 2 修改 / 3 删除 | `handler_device.go`、`store.go` |
| `/itms/handleOltConnect/` | Act=1 兼作「连接」：登录 → 采集版本/温度/板卡 → 标记 `Automatic` | `handler_device.go`、`collect.go` |
| 前端 OLT 界面（第 4 处定制：业务页挂载点） | **左栏「OLT 设备台账」**（与交换机台账同款：数量 + 刷新/新增/连接/修改/删除 + 紧凑设备列表，行内显示在线状态点、账号、`IP:端口`、备注、连接类型）+ **右栏「OLT 设备详情」**（设备信息 + 连接结果：版本/机框/运行时长/板卡数/ONT 统计 + 板卡表）+ 同款表单弹窗与二次确认 | `web/src/modules/olt.js`、`web/src/app.js`（`MODULE_PAGES`、左栏面板与 `clearModuleTransients()`） |
| `/itms/handleOltTerminalUser/` | **全部四个 Act**：0 读登录用户名 / 1 新增 / 2 修改口令 / 3 删除（「配置信息」栏目终端用户管理） | `handler_terminal_user.go` |
| 前端「配置信息」栏目 | **终端用户**小节：只读用户名列表 + 新增 / 修改口令 / 删除（二次确认），复用 `device-form-modal` | `web/src/modules/olt.js` |
| `/itms/handleVLANFunctionModule/` | **全部四个 Act**：0 读 VLAN 四类信息 / 1 新增 / 2 删除（「VLAN」栏目） | `handler_vlan.go`、`parse_vlan.go`、`models.go` |
| `/itms/handleDBAProfile/` | **全部分支**：0 读已保存配置的 DBA 模板 / 1 新增 / 2 删除（「DBA/线路模板」栏目的 DBA 部分） | `handler_profile.go`、`parse_profile.go`、`models.go` |
| `/itms/handleLineProfile/` | **全部分支**：0 读已保存配置的 ONT 线路模板 / 1 新增 / 2 删除（「DBA/线路模板」栏目的线路模板部分） | `handler_line_profile.go`、`parse_line_profile.go`、`models.go` |
| 前端「DBA/线路模板」栏目 | **DBA 模板表格**（模板ID/模板名/DBA类型/固定/保证/最大带宽 + 删除）+ 新增弹窗（按类型显示带宽字段）+ 操作反馈弹窗；**ONT 线路模板表格**（模板ID/模板名/PON类型 + T-CONT/GEM 映射层级子行 + 删除）+ 新增弹窗（模板ID/模板名/PON类型/DBA模板ID/TR069/VLAN 映射）+ 操作反馈弹窗 | `web/src/modules/olt.js`、`web/src/styles.css` |
| 前端「VLAN」栏目 | **三层 VLAN 接口 / 上行 VLAN 透传 / VLAN 列表**三张卡片 + 静态路由只读条；新增 / 删除（二次确认） | `web/src/modules/olt.js` |

**待迁入（第二批剩余）**：`handleONTListAndStatus` Act=9（采集 + 明细补全 + 后台刷新）与 Act=1..6（重启/激活/禁用/注册）、
`handleLinkAggregationInfo`、`handleSrvProfile`、
`handleOltSavedConfig`/`handleOltConfigAutoSave`、`handleONTRegisterProfile`/`handleONTRegistration`/`handleONTAutoMatic`/`handleONTAutoFind`。


**验证方式（已执行）**：
`go test ./...` 全绿（olt 模块 8.9s：解析断言 + 假 OLT 采集 + 缓存往返 + 接口契约）；
另用本地假 OLT（`tools/fake_olt_telnet.py`）对运行中的服务（18092）做了只读 E2E：
`handleConnectOltUser`(Act=0)、`handleOltConnect`(Act=1→0)、`handleBoardAndSlotInfo`(全量/单槽)、
`handleCurrentConfiguration`(Act=1)、`handleONTListAndStatus`(Act=0/9) 全部符合预期。

**与前端壳的结合验证（新增，可复跑）**：

```bash
make run            # 后端骨架（:18092，用隔离目录；或对已注册实例）
make web-run        # 前端壳（:18094）
make olt-ui         # = node tools/olt_ui_integration.mjs（DOM 桩；自带假 OLT，穿前端壳反代跑接口）
make olt-dom        # = node tools/olt_ui_dom_check.mjs（真实 DOM：jsdom 解析后真点击真冒泡）
                    #   jsdom 未安装时自动跳过：cd tools/domcheck && npm install
```

`tools/olt_ui_integration.mjs` 分四层断言（61 项）：

1. **界面层**（最小 DOM 桩真实启动 `web/src/app.js` 的 `createApp()`，打真实前端壳）：
   左侧栏三个模块菜单渲染、OLT 未被标「未安装」（capabilities 已含 olt）、
   **左栏台账面板随模块切换**（OLT → OLT 台账面板显示 / 交换机台账隐藏；TR069 → 两个都隐藏）、
   **右侧面板栏目结构**（8 个栏目 TAB、默认停在「OLT状态」、面板内不显示设备详情行与连接/修改/删除按钮）、
   **切换模块清理上一个模块的提示**（`#notices` 清空）、
   布局含 OLT 台账面板与工具条（刷新/新增/连接/修改/删除 + 列表容器）、
   capabilities 给出 OLT 三个 TAB（OLT 总览 / ONT 管理 / 线路/业务模板，后续建页依据）；
2. **接口层**（穿过前端壳的反代 `relative /itms/*`，按"OLT 页面会怎么调"跑）：
   账号列表字段、`handleOltConnect` Act=1 采集（板卡数组/版本/ONT 统计）与 Act=0 缓存读回（`"sueccess"`）、
   `handleBoardAndSlotInfo` 全量顶层统计与单槽 PON 明细、`handleCurrentConfiguration` 采集与缓存命中、
   `handleONTListAndStatus` Act=0 分页字段（空缓存时 `count`/`nPage` 缺省属老口径）与 Act=9 第二批提示、
   `handleOltLogList` 顶层分页、`trace=1` 的 `{result,timings}`；
3. **界面操作层**（真点左栏/右栏按钮、真提交表单，再用 `Act=0` 独立核对库里确实变了）：
   左栏「新增」→ 弹窗落库并出现在列表（按 IP/端口/备注/类型 显示）、
   **字段校验拦截**（非法 IP / 账号含非法字符 / 弱口令 / 端口越界都提示且不落库）、
   点列表行 → 右侧面板渲染该设备的栏目（默认「OLT状态」，切「配置信息」显示待迁入占位）、
   左栏「连接」→ **OLT状态**栏目显示版本/板卡数与板卡表、并标记「已连接」、
   左栏「修改」→ 弹窗回填（含类型 0→1 改为 SSH）并保存生效、
   **列表列结构**（勾选列 + 全选 + 计数）与**批量删除**（勾选 2 台 → 「删除 (2)」→ 二次确认 →
   两台都从列表与库中移除，勾选与按钮状态复位）；
   工具带兜底清理，联调设备不会残留在模块库。
4. **刷新与所选菜单同步层**（再次调用 `createApp()` 等价 F5 重新初始化）：
   hash 停在 `#module=olt` 时只初始化 OLT（左栏 OLT 台账、OLT 列表按需加载、**不出现交换机提示**）；
   hash 停在 `#module=switch` 时左栏/右栏切回交换机并正常刷新工作台（行为不变）。

**真实 DOM 校验（`make olt-dom`，15 项）**：用 jsdom 载入真实 `app.js`/`olt.js`，全部通过 `.click()`
驱动（真事件冒泡）：工具条按钮带 `data-olt-action`、点「新增」真的弹窗、点列表行选中并可用「连接」、
点「连接」出结果与板卡表、点「修改」回填并保存生效、点「全选」/逐行勾选真的改变勾选集合与
「删除 (n)」文案、点「删除」弹二次确认并批量删除。该层专门覆盖 DOM 桩无法暴露的真实浏览器问题。

**文件→表 校验（`go test ./internal/modules/olt/`）**：`TestSaveAndLoadDeviceSnapshot`（快照+板卡+端口拆表往返、
整份覆盖不残留）、`TestSaveAndLoadOntCache`（ONT 缓存覆盖式写入、槽位列表排序、老 JSON 的 `IP`→`OntIP`）、
`TestSaveAndLoadConfigTextAndAutoReg`（文本配置两种 Kind、自动注册口令加密）、
`TestImportFileCacheFromLegacyFiles`（按老目录形态造文件 → 导入 → 表里可读 → **重复导入幂等** → **老文件 md5 不变**）；
另有 `TestHandleONTListAndStatusPrefersTableCache` / `...FallsBackToFileCache` 覆盖表优先与文件兜底。

> 注意：OLT 的 ONT 管理 / 线路·业务模板两个 TAB 页仍待第三批（后端 `Act=9` 等分支迁入后一起做）；
> 本次验证覆盖的是「OLT 设备台账」页（连接 / 新增 / 修改 / 删除）。
> 脚本会在后端模块文件目录生成假设备缓存（`<DataDir>/olt/<假设备IP>`），验证完可删除。


**下一步（P0 剩余）**：
1. **真机只读录制黄金用例**：对 `handleOltConnect`(Act=1)、`handleBoardAndSlotInfo`、`handleCurrentConfiguration`(Act=1)、
   `handleONTListAndStatus`(Act=9) 各录一份基线响应 → `tests/fixtures/olt/`；
   录制方式：通过老系统（8090）只读调用，或新模块实现后直连 OLT 比对；**不改任何数据**（真机：`172.20.247.88:23`、`10.38.123.58:12112`）；
2. **老代码搬运依赖分析**：为 `olthandler.go` 的 121 个函数产出「函数 → 依赖（DB/文件/Telnet/task）」表，
   明确包级全局（`ontAutoRegistrationManager`、各缓存 mu）→ `Module` 字段的改写清单（第二批前置）。

**安全提示（老口径沿用，需业务确认）**：`handleConnectOltUser` Act=0 会把 `tbOltUser` 的口令**明文**回给前端
（老前端需要它回传 `connData`）。新实现保持该契约，仅在库里存密文时解密后返回；如需改为"不返回口令"，属接口行为变更，需前端同步改造与业务确认。

### 第二批（写操作 + 后台任务）
`handleOltConfigAutoSave`、`handleOltSavedConfig`、`handleLinkAggregationInfo`、`handleVLANFunctionModule`、
`handleLineProfile`/`handleSrvProfile`/`handleDBAProfile`（写模板）、`handleONTRegisterProfile`、
`handleONTRegistration`、`handleONTAutoMatic`、`handleONTAutoFind`、
`handleONTAutoMatic` 的自动注册后台任务。

### 第三批（交付）
`web/src/modules/olt.js` + `olt.css`（三 TAB：OLT 总览 / ONT 管理 / 线路·业务模板，按 docs/08 视觉基线）、
manifest 修订与 M3 打包、老壳(4433) vs 新壳(54433) 逐条对账。

## 6. 待确认（阻塞项）

| # | 问题 | 影响 |
|---|---|---|
| 1 | `oltApi.py` 是否确认废弃（本工程只读调查结论：零引用） | 决定 OLT 是否涉及 Python 层 |
| 2 | `oltuser.Password` 在新库加密后，是否需要「老程序也能读新库」的场景 | 若需要则改 `--no-encrypt-credentials` 或提供兼容视图 |
| 3 | 文件类数据 `/home/olt/**` 是纳入模块库还是保留文件形态 → **已按"纳入模块库"落地（2026-09-22）**：建 7 张缓存表并导入历史文件；老目录保留为**只读兜底**（不删不改），若业务方希望彻底停写文件，需确认后把写入路径也切到表 | 决定新实现是否继续写文件（当前：新代码写表，老程序仍可写老目录、新实现能读到） |
| 4 | OLT 前端三页的验收口径（与老壳哪些页面 1:1） | 决定前端工作量与排期 |
