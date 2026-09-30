# 06 M1：switch 模块迁入（第一批：设备台账与基础资料）

> 施工时间：2026-09-21；继续遵守 M0 施工边界：**不修改、不操作任何原工程与原系统**，
> `/switchapi`（8092）与 `/home/switch_rpc_api.db` 只作只读参考与契约来源。

## 1. 本次迁入范围（6/17 条老接口）

| 老接口 | 数据表 | 状态 |
|---|---|---|
| `/itms/handleSwitchVenue/` | `tbVenue` | ✅ 真实实现（nAct 0/1/2/3） |
| `/itms/handleSwitchManufacturer/` | `tbManufacturer` | ✅ |
| `/itms/handleSwitchPurpose/` | `tbPurpose` | ✅ |
| `/itms/handleSwitchProtocol/` | `tbProtocol` | ✅ |
| `/itms/handleSwitchModel/` | `tbModel` | ✅ |
| `/itms/handleSwitchInfo/` | `tbDeviceInformation` | ✅（含 RPC 账号密码加解密） |

仍未迁入（第二批，**统一返回 `-1 迁移中`**，老接口与老界面在过渡期继续可用）：

| 老接口 | 为什么留到第二批 |
|---|---|
| `/itms/handleDeviceInformation/` | nAct=1 需要实时采集（SSH/Telnet/RPC 客户端 + 命令解析） |
| `/itms/handleSwitchPortDetail/` | 端口详情依赖实时采集结果 |
| `/itms/handleSwitchVlan/`、`/itms/handleVlanInformation/`、`/itms/handleVlanClassifier/` | 依赖 SSH/Telnet 下发与解析 |
| `/itms/getTerminalMAC/`、`/itms/handleSwitchTerminal/` | 依赖 SSH 会话与终端 MAC 表解析 |
| `/itms/handleSwitchCollectionSettings/`、`/itms/handleSwitchSnifferConfig/` | 采集/嗅探后台任务（含 `tbSwitchCollection*` 两张表与调度） |
| `/itms/handleRpcApiConfig/` | RPC-API 配置读写 |

## 2. 新代码结构

```
internal/modules/switch/
├── module.go        模块契约：菜单、老接口清单、已迁移处理器表、Migrate（开库建表）、RotateCredentials
├── models.go        6 个结构体，字段名与老 models.go 逐字一致（json tag 不可改）
├── store.go         数据访问：建表 + 基础资料/型号/设备台账 CRUD（SQL 与老实现逐字一致）
├── credentials.go   设备凭据 AES-GCM 加解密 + 登录记录文件读写
├── rotate.go        AES 密钥轮换：设备台账（事务内）+ 登录记录文件（可回滚）
├── handler.go       场景/制造商/用途/协议四个 nAct 接口（共用泛型处理器）
├── handler_ledger.go 型号 + 设备台账接口
└── *_test.go        14 个单测（CRUD、加密存储、老库密文互通、轮换幂等、参数校验）
```

外部依赖注入（不再有包级全局）：

| 注入项 | 来源 | 用途 |
|---|---|---|
| `core.Context.CredentialKeyPath` | 平台层注册服务（`aeskey.key`） | 设备账号密码加解密 |
| `core.Context.DataDir` | 启动环境变量 `TXAPI_DATA_DIR` | 模块库默认位置 |
| `TXAPI_SWITCH_DB` | 环境变量（可选） | 覆盖模块库路径 |
| `TXAPI_SWITCH_LOGIN_FILE` | 环境变量（可选） | 覆盖登录记录文件路径 |
| `core.Context.Registration` | 平台层注册服务 | 内部调用（后续设备操作/后台任务）前校验注册状态 |

## 3. 关键设计决定

1. **一模块一库**：模块独占 `<TXAPI_DATA_DIR>/switch.db`（默认 `/home/tx/switch.db`，与
   `deploy/modules/switch.manifest` 一致），通过 `core.OpenSQLite` 打开（WAL + `busy_timeout=5000`
   + `_txlock=immediate` + 单连接），不再与任何进程共写 `switch_rpc_api.db`。
2. **老接口契约不变**：路径、`nAct` 语义、`saveData` JSON、字段名（`szName` / `szRpcUsername` …）、
   返回体 `{code,message,data}`、错误码（`0/-1`，非 POST 返回 405）逐项对齐；错误消息文案也保持
   （`failed to create venue: …`），便于黄金用例逐条接管。
3. **凭据格式可移植**：设备账号密码沿用老实现线格式 `base64(IV||密文||tag)`（AES-256-GCM），
   实现在 `core.EncryptSecret/DecryptSecret`，因此**老库里的密文在新模块可直接解密**
   （单测 `TestReadsCiphertextWrittenInLegacyFormat` 锁定）。
   同时保留老行为：更新时如果提交的已是合法密文则原样保留，不会二次加密。
4. **密钥轮换（`core.CredentialRotator`）**：注册信息里的 AES 密钥变化时，平台层回调本模块，
   把设备台账与登录记录文件里的凭据从旧密钥改为新密钥。
   - 顺序：库（同一事务）→ 登录记录文件；任一步失败即回滚，平台层据此放弃本次注册保存。
   - 与老实现的差异：老实现在“无法用旧密钥解密”时直接报错，导致整个注册保存失败且重试无法推进；
     本实现把「既不是旧密钥密文」的值原样保留，**轮换可重入**（单测覆盖）。
5. **NULL 列兜底**：老实现把可能为 `NULL` 的列直接 Scan 到 `string`，遇到 NULL 行会报错；
   本实现在 SELECT 中统一 `COALESCE(...,'')` / `COALESCE(...,0)`，字段名与类型不变。
   （现网 `tbDeviceInformation` 有 4 行真实数据，升级时不能因为 NULL 打不开台账。）

## 4. 升级前置条件（M2 迁移工具必须做）

1. **表数据搬迁**：把老库 `/home/switch_rpc_api.db` 的 5 张基础资料表与设备台账表搬入模块库
   （表名为 `tbVenue`/`tbManufacturer`/`tbPurpose`/`tbProtocol`/`tbModel`/`tbDeviceInformation`）。
   老库建表语句与新库 DDL 的差异只有 `szRpcUsername/szRpcPassword` 的长度声明（64 → 512，SQLite 不强制），
   列名与顺序完全一致，可直接 `INSERT INTO ... SELECT ...`。
2. **凭据不需要重新加密**：迁移时保持密文原样即可——新模块用同一把 `aeskey.key` 解密。
3. **挂载切换**：把老 `switchapi`（8092）停掉后由本模块接管同名接口；
   在此之前两套服务不要同时写同一个库（本模块默认库路径已与老库分离，天然避免共写）。
4. `deviceFileDir`（`/home/switchfile/devicefile/*.json`）与 `tbSwitchCollection*` 属于第二批，
   其迁移与采集任务一并设计（见 docs/03 待确认清单 #2）。

## 5. 前端（`web/src/app.js`，采用老 switchweb 的界面）

> 说明（M1 后续调整）：本工程界面已**整体采用老 `/switchweb` 的样式与 JS**
> （`styles.css` / `app.js` / `api.js` / `main.js` / `server.cjs` 原样复制 + 三处定制：
> 品牌文案、左侧栏模块菜单、采集配置进右侧面板，该 TAB 文案为「设置」），本节描述的交换机功能（设备台账、基础资料）
> 由老壳界面直接调用同一批 `/itms/*` 接口实现，接口契约与下表完全一致。详见 `docs/08`。

原自研页面的能力与接口对应关系（现由 switchweb 界面等价提供）：

| 菜单 | 页面 | 状态 |
|---|---|---|
| 设备台账 `/switch/devices` | 列表 + 新增/编辑/删除（含连接方式、账号密码、在线标记） | ✅ 真实接口 |
| 基础资料 `/switch/masterdata` | 场景/制造商/用途/协议（增删改名）+ 型号（关联四项基础资料） | ✅ 真实接口 |
| 设备面板 `/switch/panel` | 占位（说明第二批范围） | ⏳ |
| 采集配置 `/switch/collection` | 占位（说明第二批范围） | ⏳ |

页面调用与老壳一致：`POST /itms/handleSwitchXxx/` + form（`nAct`、`saveData`），
因此同一套后端也能服务老界面（灰期并行）。

**凭据传输方式的决定**：老壳先把账号密码交给 `switchweb` 的
`/switchweb/api/encrypt-switch-credentials` 加密后再转发给 `switchapi`；本工程前端壳**省掉这一跳**，
由浏览器直接把表单发给 txapi（同机 `127.0.0.1` 反代，生产用 HTTPS 对外），后端落库前一律加密。
这样少一个组件、少一处密钥副本；若后续要求"传输前即密文"，在 `web/server.cjs` 补一个等价端点即可
（`internal/modules/switch` 已兼容接收密文并原样保留，不会二次加密）。

## 6. 验证记录（2026-09-21）

| 验证 | 命令 | 结果 |
|---|---|---|
| 单元测试 | `go test ./...` | core 1 + registration 18 + switch 8 全绿 |
| 未注册自检 | `make selfcheck` | 12/12 PASS（独立端口 18093，可与演示并存） |
| switch 冒烟 | `make selfcheck-switch` | 11/11 PASS（6 条真实接口 + 5 条未迁入接口仍返回 `-1 迁移中`） |
| 信息架构 | 三种模块组合 | 仅 switch → 1 项（前端隐藏导航）；olt+tr069 → 2 项；三者 → 3 项（OLT/ONU 10 / TR069 20 / 交换机 30） |
| 静态检查 | `go vet ./...`、`gofmt -l .`、`node --check` | 干净 |

## 7. 下一步（M1 第二批）

1. 迁入 RPC/SSH 客户端（`/switchapi` 的 `rpc_client.go`、`switchdb.go` 的会话与解析部分）
   → 设备详情、端口详情、VLAN、终端 MAC、SSH 终端。
2. 迁入采集与嗅探（`switch_collection_db.go`、`switch_sniffer.go`）+ `tbSwitchCollection*` 两张表与后台任务。
3. 录制第二批接口的黄金基线（需授权），把 `_skeleton.json` 中对应条目刷成 `active`。
