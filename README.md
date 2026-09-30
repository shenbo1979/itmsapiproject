# itmsapiproject —— 统一工程（OLT+TR069 与 Switch 可分可合）

本工程用于把现有 **OLT+TR069（ITMS）** 与 **Switch** 两套功能模块收敛成一个工程设计，
目标是同一份源码、同一套构建，产出三种交付形态：

| 交付形态 | 安装方式 | 目标客户 |
|---|---|---|
| 仅 Switch | 装 switch 单模块包 | 只做交换机运维 |
| 仅 OLT+TR069 | 装 olt 单模块包 | 只做 PON/光猫管理 |
| 全功能 | 装 all 包（或 `--modules switch,olt,tr069`） | 两套都要 |

> **施工边界（重要）**：本工程在收到明确指令前，**不修改、不操作任何原工程与原系统**——
> `/switchapi`、`/switchweb`、`/switchProject`、`/itmsProject`、`/itms-py`、`/fusiongateway`、
> `/home/**` 数据、systemd 单元等一律**只读引用**，仅作业务流程参考。

## 目录

```
cmd/txapi/            唯一入口：按 MODULES 注册模块 + 平台层（注册/授权）装配
internal/core/        公共基建：Module 接口、注册表、一模块一库的 SQLite 打开方式、凭据轮换扩展点
internal/compat/      老接口兼容层：/itms/handleXxx 原路径转发 + 未装模块错误码
internal/registration/ 平台层注册/授权业务流（M1 注入：接口、守卫、注册码、签名授权状态）
internal/modules/     模块实现（M1: switch；M2: olt、tr069）
web/                  工程控制台界面（老 switchweb 的 styles.css/app.js/api.js/server.cjs 原样采用 + 三处定制，M1 已落地）
deploy/modules/       各模块的打包清单（manifest）：文件/单元/env/端口/数据目录
docs/                 设计文档与自动生成的清单
tools/                可重复运行的分析工具（只读原工程）
tests/golden/         老接口黄金用例（兼容性回归）
tests/fixtures/       迁移/扫描基线数据
```

## M0（本次）交付物

| # | 交付物 | 位置 | 状态 |
|---|---|---|---|
| 1 | API 兼容清单（含最小兼容集、归模块、风险点） | `docs/01-api-compat.md` + `docs/generated/api-inventory.md` | ✅ 已生成（工具可重复运行） |
| 2 | 「文件 → 新库表」迁移事实基线 | `docs/02-db-migration.md` + `docs/generated/tr069-file-inventory.md` | ✅ 已生成 |
| 3 | 数据权威源调查与待确认清单 | `docs/03-data-authority.md` | ✅ 已完成调查 |
| 4 | 黄金用例骨架与采集/回放方法 | `docs/04-golden-tests.md` + `tests/golden/` + `tools/run_golden.py` | ✅ 骨架可运行 |
| 5 | 工程骨架（可编译的模块注册 + capabilities） | `cmd/`、`internal/`、`go.mod` | ✅ `go build ./...` 通过 |

## M1（进行中）交付物

| # | 交付物 | 位置 | 状态 |
|---|---|---|---|
| 1 | WEB 骨架：capabilities 驱动菜单 + hash 路由 + 注册页 + 模块占位页 + 静态服务/反代 | `web/` | ✅ 已落地（`make web-run`） |
| 2 | 注册 / 授权业务流注入平台层（接口、守卫、注册码、签名授权状态） | `internal/registration/`、`cmd/txapi/main.go` | ✅ 已完成（18 个单测 + 自检 10/10） |
| 3 | 模块凭据轮换扩展点（替代老实现跨模块直改）+ 一模块一库打开方式 | `internal/core/credential.go`、`internal/core/db.go` | ✅ 已完成 |
| 4 | 平台层打包清单 | `deploy/modules/platform.manifest` | ✅ 已生成 |
| 5 | switch 模块第一批迁入（设备台账 + 基础资料 6 条接口、模块库、凭据轮换、前端页面） | `internal/modules/switch/`、`web/src/app.js`（界面复用 switchweb） | ✅ 已完成 |
| 6 | switch 模块第二批迁入（SSH/RPC 客户端 + 端口/VLAN/终端/采集 10 条接口 + 后台任务 + 前端页面） | `internal/modules/switch/`、`web/src/app.js`（界面复用 switchweb） | ✅ 已完成（36 用例 + 冒烟 17/17） |
| 7 | WEB 基线：整体采用老 `switchweb` 的样式与 JS（+ 三处 JS 定制：品牌文案 / 左侧栏模块菜单 / 采集配置并入右侧 TAB，TAB 文案为「设置」；+ 圆角统一 5px） | `web/src/styles.css`、`web/src/app.js`、`web/server.cjs` | ✅ 已完成（`make web-smoke`、`make web-cred`） |
| 8 | 交换机**数据**迁移：老库 8 张表 + `devicefile` 采集缓存 + 采集/嗅探配置 → 统一工程（老库只读、幂等、按主键核对） | `tools/migrate_switch_data.py` | ✅ 已跑通（`make migrate-switch-data`，见 docs/02 §4.1、docs/07 §6.2） |

设计、搬运方式与迁移前置条件见 `docs/05`（平台层）、`docs/06`（switch 第一批）、`docs/07`（switch 第二批）、`docs/08`（WEB 视觉基线）。

## M2（进行中）交付物

| # | 交付物 | 位置 | 状态 |
|---|---|---|---|
| 1 | OLT 接口契约基线（18 条路由的 Act 分支/连设备/含写，工具生成） | `docs/generated/olt-contract.md`、`tests/fixtures/olt-contract.json` | ✅ 已生成（`make olt-contract`） |
| 2 | OLT 数据迁移（老 `itms.db` 5 张表 → 模块库 `olt.db`；只读老库、幂等、可回滚） | `tools/migrate_olt_data.py` | ✅ 已跑通（`make migrate-olt-*`） |
| 3 | OLT 设备连接层（Telnet + SSH 双通道：登录/提示符/分页/命令执行） | `internal/oltconn/` | ✅ 已落地（含单测） |
| 4 | OLT 第一批接口迁入（操作日志 + 账号查询 + 板卡/当前配置/ONT 缓存等只读 6 条） | `internal/modules/olt/` | ✅ 已落地（见 `docs/09` §5；`go test ./...` + 本地假 OLT E2E） |
| 5 | OLT 设备（连接用户）的**连接 / 新增 / 修改 / 删除（含批量删除）**：`handleConnectOltUser` 四个 Act（Act=3 兼容单条 `{"Id"}` / 批量 `{"Ids":"n,m"}` / 数组）+ `handleOltConnect` 连接采集；**前端左栏「OLT 设备台账」**（列结构与交换机台账一致：勾选列 + 全选 + 「删除 (n)」批处理；工具条 [连接][新增][修改][删除]；信息区显示 `<IP>:<Port>` + 备注，0 TELNET、1 SSH）**+ 右栏「OLT 设备详情」**；新增/修改按业务方规格校验（IPv4 或域名、账号字母数字下划线 ≤32、口令字母+数字或特殊字符 ≤32、端口 1-65535 默认 23） | `internal/modules/olt/`、`web/src/modules/olt.js`、`web/src/app.js` | ✅ 已落地（`make olt-ui` 54 项 + `make olt-dom` 15 项 + `go test ./...`） |
| 6 | OLT 第二批剩余（ONT 采集/写操作、模板类写操作、自动注册后台任务）与 ONT / 线路·业务模板两个 TAB 页 | `internal/modules/olt/`、`web/src/modules/` | ⬜ 待开始（相关分支当前返回「第二批待迁入」） |
| 7 | **OLT 文件类缓存 → 表**：`/home/olt/<IP>/` 下的 `currentconfiguration.json`、`slot_ont_file/*.txt`、`savedconfiguration.txt`、`olt_cur_cfg.txt`、`ontautomaticcfg.json` 全部入库（7 张表），启动自动导入历史文件、接口按"表优先、老文件兜底"读 | `internal/modules/olt/{models_cache,store_cache,filecache}.go` | ✅ 已落地（映射与口径见 docs/02 §4.2、docs/09 §4.1） |


## 本地演示（看页面效果）

```bash
# 1) 起后端骨架（隔离目录，不动设备真实数据）
make run DEV_DIR=/tmp/txapi-demo

# 2) 起前端壳（另开一个终端）
make web-run                      # http://<本机IP>:18094

# 3) 灌入演示数据（可选；会自动完成 30 天注册并写入示例基础资料与 3 台设备）
make demo-seed
```

浏览器打开 `http://<本机IP>:18094`：顶部是注册状态条，**左侧一级导航 = 功能模块，模块内功能 = TAB**
（信息架构见 `web/README.md` 第 0 节）：

| 安装形态 | 左侧导航 | 说明 |
|---|---|---|
| 仅 switch | （不显示） | 全屏展示交换机页面，模块内功能以 TAB 呈现 |
| olt + tr069 | OLT/ONU 管理、TR069 管理 | 两个一级导航项 |
| 三者都装 | OLT/ONU 管理、TR069 管理、交换机管理 | 顺序取 `nav.order`（10/20/30） |

未注册时业务页会被拦到注册页（`code=-3` 语义）；注册后进入各模块 TAB 可增删改查。
`make demo-status` 可查看演示数据概览。

> 自检不会影响正在运行的演示：`make selfcheck` / `make selfcheck-switch` 使用独立端口
> （`SELFCHECK_PORT`，默认 18093）与临时目录，且只清理自己启动的进程。

## 路线图

- **M0**：事实与契约（已完成）
- **M1**：`internal/core` + 模块注册 + compat 层 + **平台层注册/授权注入** + **WEB 骨架**（已完成）；
  剩余：把 switch 模块 17 条处理器整体搬入 + 前端业务页 → 可运行的 switch-only 包
- **M2**：OLT/TR069 迁入 + 文件→表迁移工具（backup/dry-run/apply/verify/rollback）+ 老表只读视图
- **M3**：三形态打包机（switch / olt / all）+ CI 三组合冒烟；`olt` / `tr069` 业务页替换占位页
- **M4**：`--add-module` 在线补装、模块级升级/回滚、老 ITMS 前端下线

## 快速开始

```bash
# 重新生成清单（只读原工程）
make inventory          # = python3 tools/api_inventory.py && python3 tools/tr069_file_scan.py

# 校验清单是否为最新（CI 用）
make check-inventory

# 构建骨架
make build              # go build -buildvcs=false ./...

# 单元测试（注册/授权链路回归）
make test

# 本地起骨架服务（默认 :18092，不动线上 8092；注册文件走 /tmp/txapi-dev 隔离目录）
make run
curl -s http://127.0.0.1:18092/api/capabilities | python3 -m json.tool

# 起前端壳（默认 :18094，代理到 18092）
make web-run

# 前端渲染冒烟（无浏览器：最小 DOM 桩真实启动 createApp + 三处定制 + 基线未漂移）
make web-smoke

# OLT 第一批 × 前端壳 结合验证（DOM 桩启动界面 + 穿前端壳反代跑 6 条只读接口，自带假 OLT）
make olt-ui

# OLT 台账页真实 DOM 校验（jsdom：真点击真冒泡；jsdom 见 tools/domcheck，未装则自动跳过）
make olt-dom

# 界面与骨架的凭据链路端到端（加密端点 → 台账保存 → 回读解密）
make web-cred

# 交换机功能（含注册）迁入审计：老壳调用面逐 nAct 对账 + 注册链路 E2E
make switch-audit

# 交换机数据迁移：老库 8 张表 + devicefile 采集缓存 + 采集/嗅探配置 → 模块库/模块数据目录
make migrate-switch-data          # = python3 tools/migrate_switch_data.py --data-dir $DEV_DIR/data --replace --yes

# 起骨架 + 跑自检用例（12 条：注册链路 + -3 守卫 + -4 语义）
make selfcheck

# 起骨架 → 注册 → 跑 switch 模块冒烟（6 条真实接口 + 未迁入接口仍为过渡）
make selfcheck-switch
```
