# 05 M1：WEB 骨架 + 注册业务流注入（交付与边界说明）

> 施工时间：2026-09-21；仍遵守 M0 的施工边界：**不修改、不操作任何原工程与原系统**。
> 老系统（`/switchapi` 8092、`/switchweb` 8091、itmsmain 8090 等）仅作只读参考与契约来源。

## 1. 本次交付物

| # | 交付物 | 位置 | 验证方式 |
|---|---|---|---|
| 1 | WEB 骨架（capabilities 驱动菜单 + 路由壳 + 注册页 + 模块占位页） | `web/`（`index.html`、`server.cjs`、`src/**`） | `make web-run`；浏览器访问 `http://<ip>:18094` |
| 2 | 注册 / 授权业务流注入平台层 | `internal/registration/**`、`cmd/txapi/main.go` | `go test ./internal/registration/`（18 个用例）、`make selfcheck`（10/10 PASS） |
| 3 | 模块凭据轮换扩展点（替代老实现的跨模块直改） | `internal/core/credential.go` | 注册测试 `TestProfileOnlyUpdateKeepsExpiryAndRotatesAESKey` |
| 4 | 一模块一库的统一打开方式（WAL + busy_timeout + 单连接） | `internal/core/db.go` | 平台库 `/home/tx/platform.db` 以 WAL 模式创建 |
| 5 | 平台层打包清单 | `deploy/modules/platform.manifest` | 与三形态 bundle 约定一致（`switch,olt,all`） |

## 2. 注册业务流与老实现的对应关系（逐字段兼容）

| 老实现（`/switchapi`） | 新实现（本工程） | 说明 |
|---|---|---|
| `validation_handler.go`（handleRegistrationInfo / loadRegistrationInfo / saveRegistrationInfo） | `internal/registration/{handler,service,resolve}.go` | 四个分支（首次注册 / 仅改资料 / 注册码续期 / 序列号续期）与判断条件完全一致 |
| `validation_table.go`（tbValidation DDL 与列迁移） | `internal/registration/store.go` | 表名、列名、约束不变，迁移可直接整表搬运 |
| `registration_apikey.go`（TXR1 注册码、密钥来源与派生） | `internal/registration/apikey.go` | 与主注册服务 `/txapikeygen` 双向互通（黄金向量用例锁定） |
| `registration_license.go`（签名授权状态文件） | `internal/registration/license.go` | 到期判定权威来源不变；时间锚点防回拨 |
| `registration_guard.go`（未注册拦截） | `internal/registration/guard.go` + `main.go` 入口挂载 | 只拦 `/itms/*`，注册接口始终放行，返回 `-3/-2` |
| AES 密钥轮换时 **直接 UPDATE `tbDeviceInformation` + 改写 `/home/switchfile/loginfile.json`** | `core.CredentialRotator` 回调（模块自己换） | 见第 4 节边界说明 |

对外契约（未改动）：
```
POST /itms/handleRegistrationInfo/   nAct=0 读取、nAct=1 保存（saveData=JSON）
POST /itms/validateThirdToken/       登录令牌校验
POST /api/v2/platform/registration/  新命名空间（新前端壳使用，同一处理器）
响应：{code,message,data}；0 成功 / -1 参数或业务错误 / -2 过期 / -3 未注册 / -4 模块未安装
```

## 3. 路径与隔离约定

| 环境变量 | 默认值 | 用途 |
|---|---|---|
| `TXAPI_REGISTRATION_DIR` | 空 | **仅本地自检/测试**：把状态文件、注册码、AES 密钥、verify_info 收敛到该目录，绝不触碰真实注册数据 |
| `TXAPI_REGISTRATION_STATE` | `/opt/itmsapikey/registration.state` | 签名授权状态文件 |
| `TXAPI_REGISTRATION_KEY` | `/opt/itmsapikey/itmsapikey.key` | 本机注册码 |
| `TXAPI_REGISTRATION_SECRET_FILE` | `/txapikeygen/.registration-secret` | 主注册服务共享密钥 |
| `TXAPI_REGISTRATION_AESKEY` | `/home/switchfile/aeskey.key` | 注册信息里的 AES 密钥 |
| `TXAPI_VERIFY_INFO` | `/dev/shm/verify_info.json` | 云平台授权窗口 |
| `REGISTRATION_SECRET` / `REGISTRATION_LEGACY_SECRETS` | 空 / 空 | 与老实现同名，保证两端密钥一致 |

`make run` / `make selfcheck` 都带 `TXAPI_DATA_DIR` + `TXAPI_REGISTRATION_DIR` 指向 `/tmp/txapi-dev`，
因此本地调试既不会读到设备真实注册状态，也不会覆盖 `/opt/itmsapikey/**`。

## 4. 关键设计决定（需要业务/后续里程碑确认）

1. **注册属于平台层，不拆进任何模块包**（依据 `docs/03` 结论 4）：三形态都带 `platform.manifest`；
   模块未安装不影响注册，注册无效时所有模块的业务接口统一 `-3`。
2. **AES 密钥轮换改为跨模块回调**：老实现让注册逻辑直接改交换机表与登录文件，违反“一模块一库”。
   新实现先让各模块用「旧密钥 → 新密钥」完成自身凭据重加密（`core.CredentialRotator`），
   再写密钥文件、授权状态并提交；钩子失败则整体放弃本次保存（不产生“密钥换了、凭据还是旧密钥”的不可解密状态）。
   - 当前 `switch` 模块尚未实现该接口（业务处理器与模块库在 M1 后续步骤迁入），
     保存时会在日志中列出“尚未实现凭据轮换”的模块，**升级为 switch-only 包前必须完成**。
3. **写入顺序（跨库无单事务）**：模块轮换 → AES 密钥文件 → 授权状态文件 → 提交 tbValidation；
   提交失败时回滚授权状态与密钥文件，并记录日志。
4. **fail-closed**：授权状态文件被篡改（签名不符）时按未注册处理，且拒绝新的注册/续期。
5. `validateThirdToken` 沿用线上现状（可信内网、不做令牌强校验），仅做方法与返回码兼容。

## 5. 数据迁移前置条件（M1 剩余步骤 / M2 必须做）

`tbValidation` 现在位于**平台库** `/home/tx/platform.db`，而老实现它在 `/home/switch_rpc_api.db`。
升级到新版本时必须迁移这一张表（1 行数据，幂等即可）：

```sql
-- 迁移思路（由 M2 的 migrate 工具实现，含 backup/dry-run/apply/verify/rollback）
ATTACH '/home/switch_rpc_api.db' AS legacy;
INSERT OR IGNORE INTO tbValidation (CompanyName, MachineId, SerialNumber, AesKey, RandomCode,
        RegistrationDate, ValidDuration, RemainingTime, Remarks)
SELECT CompanyName, MachineId, SerialNumber, AesKey, RandomCode,
       RegistrationDate, ValidDuration, COALESCE(RemainingTime, 0), COALESCE(Remarks, '')
FROM legacy.tbValidation;
```
注意：`/opt/itmsapikey/registration.state`（签名授权状态）与 `itmsapikey.key` 属于平台文件、
路径不变，**升级后无需重建**；缺少授权状态文件时新的服务会按 `tbValidation` 记录自动补建。

## 6. 验证记录（2026-09-21）

| 验证 | 命令 | 结果 |
|---|---|---|
| 单元测试 | `go test ./...` | 注册链路 18 个用例全绿（含黄金注册码向量、续期累加、防篡改、防时间回拨、守卫拦截） |
| 静态检查 | `go vet ./...`、`gofmt -l .` | 干净 |
| 骨架自检 | `make selfcheck` | 12/12 PASS（含注册接口、`-3` 守卫、`-4` 语义、大小写容错、Python 侧提示） |
| 前端语法 | `node --check`（ESM 校验） | `server.cjs` + ES 模块全部通过 |
| 端到端冒烟 | `make web-run` + 浏览器/curl | 未注册时业务接口 `-3`；完成注册后放行 |
| **注册链路 E2E（含界面同款字段）** | `make switch-audit`（B 部分） | 8/8 PASS：未注册 `-3` 守卫 → 用老壳注册弹窗同款字段集提交注册（`szAesKey` 32 位十六进制 + `nBrowserTime` 毫秒）→ 同一实例业务接口 **-3 → 0** → 注册回读 `registered=true / expired=false` → **`aeskey.key` 落盘且内容等于提交的密钥**（交换机凭据加密链路依赖它）→ `/itms/validateThirdToken/` 可用 |

> 注册与交换机凭据的联动全链路另由 `make web-cred` 验证：界面
> `/switchweb/api/encrypt-switch-credentials` 加密 → `/itms/handleSwitchInfo/` 落库 →
> 后端用同一把 `aeskey.key` 解密回读明文 → 删除自检设备。

## 7. 下一步（M1 剩余）

1. switch 模块处理器迁入（17 条 `/itms/handleSwitch*`），并实现 `CredentialRotator`（设备账号密码 + 登录文件重加密）。
2. 把 `tests/golden/cases/_skeleton.json` 中 switch 的 17 条用例从 `todo` 刷成 `active`（需授权后录制线上基线）。
3. 前端接入 switch 业务页（`web/src/modules/switch.js` 替换占位脚手架）。
