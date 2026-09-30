# TR069/CPE 文件型数据清单（自动生成，勿手改）

生成工具：`tools/tr069_file_scan.py`；**只读扫描**，不修改任何原系统文件。
本清单是「文件 → 新库表」迁移设计的事实依据（映射表由 `docs/02-db-migration.md` 定义）。

## 1. 概览

| 指标 | 值 |
|---|---|
| 扫描根目录 | `/home/tr069file` |
| 条目数（文件+目录） | 8 |
| 总字节数 | 312316 |
| 其中 JSON 文件 | 7 |

## 2. 文件清单与映射建议

| 相对路径 | 大小(B) | 类型 | 关键结构 | 建议目标表 | 说明 |
|---|---|---|---|---|---|
| `ZNXT64BD7A98.json` | 44 | json | 顶层键 code, data, message | tbCpeSnapshotRaw | 按序列号命名的快照文件，建议入库并去重 |
| `DeviceSummary/ZNXT64BD7A98.json` | 503 | json | 顶层键 Cookie, Manufacturer, OUI, ProductClass… | tbCpeSummary | 设备汇总快照 |
| `cpegroup/` | 0 | dir | 目录 | tbCpeGroup | CPE 分组 |
| `Parameters/ZNXT64BD7A98.json` | 308025 | json | 顶层键 Cookie, SerialNumber, Parameters；Parameters=2266 项；值类型 {"str": 2266} | tbCpeDevice + tbCpeParameter | 每个 CPE 一个 JSON：Cookie/SerialNumber/Parameters{}；建议原始 JSON 留档一列 + 参数明细行 |
| `tr069dofile/ZNXT64BD7A98.json` | 40 | json | 顶层键 code, data, message | tbTr069TaskFile | 下发/临时文件（可只保留最近 N 条） |
| `templatefile/templatefile.json` | 3064 | json | 顶层键 LAN-100, PPPOE上网, 会客室-INTERNET, 会客室-IPPBX… | tbTr069Template | 模板名 -> 参数默认值集合 |
| `tr69serverinfo/tr69serverinfo.json` | 158 | json | 顶层键 szHost, szPort, nPeriodicInformInterval, szUsername… | tbTr069Server | TR069 服务器信息（单例） |
| `tr069specialfield/ZNXT64BD7A98.json` | 482 | json | 顶层键 ServiceList, IPMode, LanList, Wifi2List… | tbCpeSpecialField | CPE 特殊字段 |

## 3. 授权/注册状态文件（只记录元数据，不落业务值）

| 路径 | 存在 | 类型 | 大小 | 文件/键名 |
|---|---|---|---|---|
| `/opt/itmsapikey` | ✅ | dir | — | itmsapikey.key, registration.state |
| `/txapikeygen` | ✅ | dir | — | .gitignore, .registration-secret, .registration-secret.state, README.md, package.json, server.js, server.test.js |
| `/dev/shm/verify_info.json` | ✅ | file | 678 | AuthExpire, CloudVhost, CloudVkey, Desc, HostID, IPPBX, IPTV, IdKey, IsOffLine, ModelName, Name, RegistTime, ServiceExpire, State, TR069, ThirdInterface, VNAAS |

## 4. 迁移注意

- 迁移脚本必须**幂等**（唯一键 + `INSERT ... ON CONFLICT DO UPDATE`），可重复执行。
- 单条 JSON 可能达数百 KB、参数上千项：入库时按 `tbCpeParameter(cpeId, szPath, szValue)` 拆分，并保留原始 JSON 归档列，便于回滚与回溯。
- 迁移期间**单写者**（停写老进程或设只读窗口），连接串带 `_busy_timeout`，每 500 行提交一次，避免长事务占锁（历史上出现过共享 SQLite 被写事务卡死、前端整页转圈的事故）。
- 迁移完成后老文件保留只读副本，新代码不再读写 `/home/tr069file`。
