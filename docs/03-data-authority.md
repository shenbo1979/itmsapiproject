# 03 数据权威源与运行形态调查（M0 交付物③）

目标：为“分包 / 迁移 / 前端单独展现”提供事实依据。以下均为**只读**调查结论。

## 1. 部件与数据流向

```
                 ┌─────────────────────────── nginx (4433 / 9901) ───────────────────────────┐
   浏览器 ──►  /fusiongateway/web（老 Vue 壳：chunk-itmsApi.js / chunk-switchApi.js）        │
                 │  location /itms/  → 127.0.0.1:8090   (itmsmain, Go 单片)                  │
                 │  location /       → 127.0.0.1:4438   (fusiongateway_g) ──► /client/fn* (Python /itms-py)
                 └───────────────────────────────────────────────────────────────────────────┘
   新壳 /switchweb (8091/54433) ──► /itms/* ──► switchapi (8092)   ← 目前只含 switch 18 条
   CPE/光猫 ──► itmssocketmain (8012) ──► TR069 协议处理（Go，/itmsProject 的 Go 重写雏形）
```

## 2. 各数据的权威源判定

| 数据 | 权威源 | 依据 |
|---|---|---|
| 交换机台账/场景/型号/用途、注册信息、采集快照 | `/home/switch_rpc_api.db`（switchapi + itmsmain 共写） | 两进程均持有该库文件句柄；新 `switchapi` 代码路径默认 `SWITCHAPI_DB_PATH=/home/switch_rpc_api.db` |
| CPE/TR069 参数、模板、分组、特殊字段、汇总 | `/home/tr069file/**` 文件（Go 与 Python 双方读写） | 老 Go 代码常量集中在 `rpc_client.go`（`ParametersPath`/`CpeGroupPath`/`TemplateDir`…）；Python `vTr69Api.py` 同名函数族 |
| TR069 下发/临时结果 | `/home/tr069file/tr069dofile/*.json` | 同上 |
| 授权/注册 | `/opt/itmsapikey/*`、`/txapikeygen/*`（Node 服务）、`/dev/shm/verify_info.json`（云窗口） | 文件存在 + 云窗口字段含 `TR069/IPTV/IPPBX/VNAAS/ThirdInterface` |
| 实时设备状态（ONT/OLT） | 设备侧（RPC 实时获取），本地仅缓存 | `handleOltConnect` 等接口属于“连设备后取数据”的形态；本地无对应大表 |

## 3. 关键结论

1. **OLT+TR069 模块的“数据”目前主要不是表，而是文件 + 实时 RPC**；因此迁移是“文件入库”，
   不存在大规模停机搬迁，风险集中在**锁与单写者**而非数据量。
2. **云授权窗口已按业务模块给开关**（`TR069/IPTV/IPPBX/VNAAS`），说明“按模块授权”在商务与技术上都可行，
   统一工程应把模块 id 与授权位对齐（见 `docs/01-api-compat.md` 第 4 节的 `-4 模块未安装` 约定）。
3. `/home/switch_rpc_api.db` 被 **两个进程共写**，是当前最大的稳定性隐患（已发生过整站假死）。
   统一工程必须收敛为「一模块一库」或「单写者 + WAL + busy_timeout」。
4. `/txapikeygen` 是一个 **Node 服务**（`server.js` + `package.json` + `.registration-secret`），
   与 switch 侧 `/txapikeygen/.registration-secret`、Go 侧 `REGISTRATION_SECRET` 之间存在共享密钥关系；
   模块化交付时**注册/授权仍应属于平台层**，不应被拆进某个业务模块包。

## 4. 待业务确认清单（阻塞 M1/M2 的问题）

| # | 问题 | 影响 |
|---|---|---|
| 1 | `/client/*`（Python 31 条）随 `tr069` 模块迁入，还是保持独立服务由网关分流？ | 打包清单、安装脚本、Go/Python 边界 |
| 2 | `/home/tr069file/**` 由 Go 与 Python 双方写？写入时机与频率？ | 只读窗口安排、迁移一致性 |
| 3 | 老壳（`/fusiongateway/web`）之外是否有第三方脚本直接调 `/itms/*`？ | P1 等级与兼容承诺范围 |
| 4 | 注册/授权是否按“整机授权”还是“按模块授权”售卖？ | 安装包与 license 校验点设计 |
| 5 | OLT/TR069 是否需要独立数据库（推荐）还是与 switch 共库？ | 迁移与并发方案 |
| 6 | `itmssocketmain`（8012）是否也并入统一工程（`/itmsProject` 已有 Go 雏形）？ | 打包形态（是否含 socket 层） |
