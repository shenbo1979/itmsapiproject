# 02 数据库规范与老数据迁移（M0 交付物②）

事实基线由工具生成：`docs/generated/tr069-file-inventory.md`、
`tests/fixtures/tr069-file-inventory.json`（`make inventory` 可重复生成）。

## 1. 现状（只读实测）

| 数据源 | 内容 | 规模 | 迁移难度 |
|---|---|---|---|
| `/home/switch_rpc_api.db` | **10 张表**：`tbDeviceInformation`、`tbManufacturer`、`tbModel`、`tbProtocol`、`tbPurpose`、`tbVenue`、`tbValidation`、`tbSwitchCollectionSnapshot`、`tbSwitchCollectionInterfaceStat` | 200MB（大头是两张采集表） | 同库内改写，**数据量小**（业务表千级行） |
| `/home/tr069file/Parameters/*.json` | 每台 CPE 一个 JSON：`Cookie/SerialNumber/Parameters{2266 项}` | 单台 308KB | 拆分入库（参数明细） |
| `/home/tr069file/templatefile/templatefile.json` | 模板名（`LAN-100`、`PPPOE上网`、`会客室-INTERNET`…）→ 参数默认值 | 3KB | 直接入库 |
| `/home/tr069file/tr69serverinfo/tr69serverinfo.json` | `szHost/szPort/nPeriodicInformInterval/szUsername…` | 158B | 单例表 |
| `/home/tr069file/tr069specialfield/*.json` | `ServiceList/IPMode/LanList/Wifi2List…` | 482B/台 | 直接入库 |
| `/home/tr069file/DeviceSummary/*.json` | `Cookie/Manufacturer/OUI/ProductClass…` | 503B/台 | 直接入库 |
| `/home/tr069file/tr069dofile/*.json`、根目录 `*.json` | 下发/临时结果、快照 | 40B/台 | 归档表（可只留最近 N 条） |
| `/opt/itmsapikey/*`、`/txapikeygen/*`、`/dev/shm/verify_info.json` | 授权与云窗口 | 小 | **保持文件不动**，只在表内登记引用 |

> 结论：**迁移不是“大库搬迁”，而是“小表规整 + 文件入库”**。真正的风险在**锁与并发**，不在数据量。

## 2. 新库表规范（沿用 `switch_rpc_api.db` 风格并补齐）

- 命名：`tb<Module><Entity>`（`tbSwitchDevice`、`tbOltProfile`、`tbCpeDevice`、`tbCpeParameter`…）
- 字段：`nId INTEGER PRIMARY KEY AUTOINCREMENT`、文本 `sz*`、整数 `n*`、时间 `dt*`
  （统一 `YYYY-MM-DD HH:MM:SS`）、`szRemarks`、`dtCreatedAt`、`dtUpdatedAt`
- 必备表：每库一张 `tbSchemaVersion(module TEXT PRIMARY KEY, nVersion INT, dtAppliedAt TEXT)`
- 索引/唯一键显式声明（迁移幂等依赖唯一键）
- 一模块一库：`/home/tx/switch.db`、`/home/tx/olt.db`、`/home/tx/tr069.db`
  （若必须同库，则强制 WAL + `_busy_timeout=3000` + 单写者，并在部署文档中写明）

## 3. 文件 → 表 映射（草案，随扫描结果更新）

| 老数据 | 新表 | 关键列 | 备注 |
|---|---|---|---|
| `Parameters/<SN>.json` | `tbCpeDevice` + `tbCpeParameter` | `tbCpeDevice: szSerialNumber(唯一), szCookie, dtUpdatedAt, szRawJson`<br>`tbCpeParameter: nCpeId, szPath, szValue, nValueType` | 2266 项/台 → 必须拆行；`szRawJson` 保留原始快照便于回滚 |
| `templatefile/templatefile.json` | `tbTr069Template` | `szName(唯一), szJson, dtUpdatedAt` | 模板名即 key |
| `cpegroup/*` | `tbCpeGroup` | `szGroupName(唯一), szJson` | 目录当前为空，规则按老代码补 |
| `tr69serverinfo/tr69serverinfo.json` | `tbTr069Server` | `szHost, nPort, nPeriodicInformInterval, szUsername, szPassword` | 单例（nId=1） |
| `tr069specialfield/<SN>.json` | `tbCpeSpecialField` | `szSerialNumber(唯一), szServiceList, nIPMode, szLanList, szWifi2List, szJson` | 字段随业务扩展 |
| `DeviceSummary/<SN>.json` | `tbCpeSummary` | `szSerialNumber(唯一), szManufacturer, szOUI, szProductClass, szJson` | 汇总快照 |
| `tr069dofile/*.json`、根 `*.json` | `tbCpeFileArchive` | `szPath, szSerialNumber, nSize, szHash, szContent, dtCreatedAt` | 归档；可按保留策略清理 |
| 授权文件 | 不入库 | — | `tbApiKeyRef` 仅登记路径+hash |

## 4. 迁移流水线（幂等 / 可回滚 / 不停机）

```
migrate backup    →  VACUUM INTO '/home/backup/<db>-<ts>.db'（秒级，不阻塞读）
migrate dry-run   →  只读解析 + 打印将写入的行数与抽样（默认输出到 docs/generated/）
migrate apply     →  建表（IF NOT EXISTS）→ 批量 upsert（唯一键 + ON CONFLICT DO UPDATE）
                     → 每 500 行提交 → 写 tbSchemaVersion
migrate verify    →  行数 / 抽样字段 / 校验和 与老数据对比，输出差异报告
migrate rollback  →  从备份还原，或按 tbSchemaVersion 回退
```

> 实施进度（2026-09-21）：**switch 部分已实现并跑通**，见
> `tools/migrate_switch_data.py`（`make migrate-switch-data`）。TR069/CPE 部分仍待做。
>
> 实施进度（2026-09-22）：**OLT 部分已实现并跑通**，见 `tools/migrate_olt_data.py`：
> `make migrate-olt-data`（预览差异）/ `migrate-olt-apply`（幂等落库）/ `migrate-olt-replace`（取精确副本）/
> `migrate-olt-check`（双通道对账）。OLT 独立成库 `<DataDir>/olt.db`，老库 `itms.db` 全程只读。

### 4.1 已实现的 switch 迁移工具

```bash
make migrate-switch-data        # = 迁到 $DEV_DIR/data，--replace（老库成为唯一来源）
make migrate-switch-check       # 凭据可解密性体检（哪些设备需要重存密码）
python3 tools/migrate_switch_data.py --dry-run                 # 只看会做什么
python3 tools/migrate_switch_data.py --data-dir /tmp/x/data    # 指定目标数据目录
python3 tools/migrate_switch_data.py --include-registration    # 连 tbValidation（注册记录）一起迁
python3 tools/migrate_switch_data.py --include-login           # 连 loginfile.json(+.key) 一起迁
python3 tools/migrate_switch_data.py --skip-files              # 只迁数据库表
python3 tools/migrate_switch_data.py --check-credentials       # 逐台报告凭据能否被目标实例解密
python3 tools/migrate_switch_data.py --device-credential 名称:账号:密码   # 用目标实例密钥重写该设备凭据（可重复）
```

老数据分三处，工具一次处理（老库/老目录**只读**）：

| 老数据 | 用途 | 目标（默认，可用 `TXAPI_SWITCH_*` 覆盖） |
|---|---|---|
| `/home/switch_rpc_api.db` 的 8 张表：`tbVenue/Manufacturer/Purpose/Protocol/Model/DeviceInformation/SwitchCollectionSnapshot/SwitchCollectionInterfaceStat` | 台账、基础资料、采集历史 | `<DataDir>/switch.db`（同表结构，`INSERT OR REPLACE`） |
| `/home/switchfile/devicefile/*.json` | 设备面板「读取缓存」（`/itms/handleDeviceInformation/` nAct=0） | `<DataDir>/devicefile/`（`TXAPI_SWITCH_DEVICE_DIR`） |
| `/home/switchfile/collectionsettings.json`、`sniffinginfocfg.json` | 周期采集 / 在线嗅探配置 | `<DataDir>/`（`TXAPI_SWITCH_SNIFFER_DIR`） |
| （可选）`loginfile.json` + `.key` | 设备登录记录 | `<DataDir>/`（`TXAPI_SWITCH_LOGIN_FILE`，需 `--include-login`） |
| `aeskey.key`、`*.key` | 设备凭据密钥 | **不迁移**（注册平台负责，路径不变）；因此凭据密文无需重新加密 |

模式与核对：
- 默认 **合并**（按主键 upsert，目标库原有行保留）；`--replace` 先清目标表，得到与老库一致的精确副本；
- 迁移后按主键做行核对（老库的键必须全部在新库），`PRAGMA integrity_check` 一并检查；
- `--dry-run` 不写任何库/文件，只报告行数与列交集。

**凭据可解密性**（切流前必看）：老库里既可能有**明文**行，也可能有**密文**行（`base64(IV(12)||密文||tag(16))`）。
密文用设备密钥加密，因此**生产环境**（统一工程与老实现同用 `/home/switchfile/aeskey.key`）搬完即可解密；
但若目标实例用了别的密钥（本地隔离目录、换机等），密文就解不开。`--check-credentials` 会逐台报告：

```
✗ #1   centec     172.20.251.29    账号=不可解密（异密钥密文）  密码=不可解密（异密钥密文）
✓ #2   huawei     172.20.247.181   账号=明文           密码=明文
```

解不开的行可用 `--device-credential 名称:账号:密码` 重写（工具按 `core.EncryptSecret` 的同一线格式加密，
回读校验后写库）；本次用临时设备做过往返验证：工具写入 → `/itms/handleSwitchInfo/` 回读为该明文 ✓。

实施注意（实测）：
1. **老系统仍在运行**：`tbSwitchCollectionSnapshot/InterfaceStat` 两次读数不同（老进程按保留期在清理），
   且 `devicefile/*.json` 今天仍在更新 → 切流当天需**再跑一次**（建议 `--replace`）以获得一致快照；
2. 真正切流仍建议先停老进程（单写者），避免迁移窗口内的增量丢失；
3. 设备凭据：老库中既有**明文**行（老版本写入）也有 **AES-GCM 密文**行；密文由设备密钥
   （生产 `/home/switchfile/aeskey.key`，与统一工程同一把）加密，因此搬完即可解密；
   本地演示用隔离密钥时密文解不开（界面显示原密文），属预期；
4. 已迁真实数据后不要再跑 `make demo-seed`（它会再追加 3 台示例设备，虽然按名字幂等但会混入演示数据）。

硬性要求：
1. **单写者**：迁移期间停写老进程（或设维护窗口）；不要与 `itmsmain` 并发写同一库。
2. **不用大事务**：分批提交；连接串带 `_busy_timeout=3000`；必要时先切 WAL。
3. **老表只读兼容**：为老表建 `CREATE VIEW v_<oldName> AS SELECT ...`，老接口可继续读，保留 1~2 个版本再删。
4. **可重复执行**：`apply` 与 `verify` 必须可反复跑且结果一致（CI 用临时库跑两遍做幂等断言）。
5. **授权文件零改动**：`/opt/itmsapikey`、`/txapikeygen`、`/dev/shm/verify_info.json` 只读引用。

### 4.2 OLT 文件类缓存 → 表（2026-09-22 已落地）

老系统把 OLT 的采集结果与配置写在 `/home/olt/<OLTIP>/` 下的文件里。**结论：这些数据全部可以入库表**
（都是结构化 JSON / 整篇文本，字段固定、以设备为主键），已按实情建表并完成历史数据导入：

| 老文件 | 落地表 | 说明 |
|---|---|---|
| `currentconfiguration.json` | `tbOltDeviceSnapshot`（17 个标量列）+ `tbOltDeviceBoard` + `tbOltDevicePonPort` + `tbOltDeviceGePort` | 嵌套数组拆子表；主键 `(HostIP)` / `(HostIP,BoardId)` / `(HostIP,FSP)`；整份覆盖（先清后写，避免残留已拔出的板卡/端口） |
| `slot_ont_file/<槽位>.txt`（每行一个 ONT JSON） | `tbOltOntCache` | 17 个 JSON 字段一列一字段（老 JSON 的 `IP` → `OntIP` 列，避免与设备 IP 混淆）；主键 `(HostIP,FSP,OntId)`；`INSERT OR REPLACE`（同一 ONT 换槽位时不致整次导入失败） |
| `savedconfiguration.txt`、`olt_cur_cfg.txt` | `tbOltConfigCache` | `Kind` 区分（`savedconfiguration` / `currentconfig`），`Content` 存整篇文本 + `ByteSize`；主键 `(HostIP,Kind)` |
| `ontautomaticcfg.json` | `tbOltAutoRegConfig` | 列名与老 JSON 字段一致（`Type/Port/Username/Password/Registration/Relocation/TemplateId/ScanInterval`）；**口令列加密**（与 `oltuser` 同口径：密文优先、明文兜底）；主键 `(HostIP)` |

导入方式与口径（实现见 `internal/modules/olt/{store_cache,filecache,models_cache}.go`）：

- **自动导入**：模块启动时若缓存表为空 → 扫描"新目录 + 老目录"的设备子目录导入一次（`importFileCacheIfEmpty`），
  日志打印 `设备快照/板卡/PON 口/GE 口/ONT/配置文本/自动注册` 的条数；
- **幂等**：按主键覆盖，重复执行结果一致（单测 `TestImportFileCacheFromLegacyFiles` 连续导入两遍断言无重复行）；
- **只读边界**：老目录只发 `ReadDir/ReadFile`，导入前后文件 md5 不变（单测断言）；
- **可追溯**：每张表都带 `SourceFile` / `SourceMTime` / `UpdatedAt`，能回溯到来源文件与采集时间；
- **读取口径**：接口按"**表优先、老文件兜底**"读（如 `handleONTListAndStatus` Act=0），
  过渡期老控制台写出的文件缓存仍能被读到；
- 实测（本机真机缓存 `172.20.247.88`）：快照 1、板卡 5、PON 口 16、GE 口 4、ONT 2、配置文本 1，老文件 md5 全部不变。

> 与老库迁移表的关系：老库 `oltboard`/`oltontlist`（迁移后为 `tbOltBoard`/`tbOltOnt`）是另一套较薄的缓存表，
> 与本文件对应、字段更全的 `tbOltDevice*` 系列**并存**，互不覆盖。

## 5. 迁移实施顺序建议
1. switch 表（同库改名/加列，最小风险，先做以验证流水线）；✅ **已实现并跑通**（`make migrate-switch-data`）
2. TR069 模板/服务器/分组/汇总（小文件，一次迁移）；
3. CPE 参数明细（最大项，按 CPE 分批，可后台续跑）；
4. 归档类（`tr069dofile`、根 `*.json`）最后处理，允许丢弃历史瞬时文件。

## 6. 待确认

1. 老 Python（`/itms-py`）与 Go 层是否都写 `/home/tr069file`？谁在什么时机写？（决定只读窗口如何安排）
2. 采集表（`tbSwitchCollectionSnapshot/InterfaceStat`，200MB）是否需要历史保留策略与分区/归档？
3. 是否为 OLT/TR069 单独建库（推荐），还是与 switch 共库共用锁策略（不推荐）。
   → **已定（2026-09-22）：OLT 独立建库 `<DataDir>/olt.db`（默认 `/home/tx/olt.db`），绝不与
     `switch_rpc_api.db` 共库**。依据：① 老 OLT 数据本就不在 switch 库，而在 `/sxzdb/db/itms.db`
     （344KB，经 `sxzdbmid:8306` 中间件暴露给老单体）；② `switch_rpc_api.db` 现被两个老进程共写，
     曾导致整站假死（§1），再加写者等于复现故障；③ M3 的 olt-only 包需要能独立装卸。
     落地工具：`tools/migrate_olt_data.py`（`make migrate-olt-data`、`make migrate-olt-check`），
     **已实现（2026-09-22）**：dry-run 预览差异 → `--apply` 幂等合并 → 切流当天 `--replace --yes` 取精确副本；
     写前自动备份到 `<DataDir>/backup/`，写后逐列核对 + `PRAGMA integrity_check`。
     口令口径：**老库保持明文**（老程序零改动），新库口令按实例密钥加密、账号列保持可读；
     OLT 模块取口令一律「密文优先、明文兜底」（同 `plain_value` 口径），因此新老数据可混存、可回滚。
     过渡期同步：同一命令可反复执行（同数据重跑**写入 0 行**）；老控制台期间新增/修改的数据，
     重跑一次即增量带入新库（表小、秒级）。
