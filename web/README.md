# WEB 界面（工程控制台）与后端的契约

本工程前端**整体采用老 `/switchweb` 的样式与 JS**（`styles.css` / `app.js` / `api.js` / `main.js` /
`server.cjs` 原样复制），只做三处 JS 定制：品牌文案、左侧栏模块菜单、采集配置进右侧面板（TAB 文案为「设置」）；
并把界面所有 `border-radius` 统一为 `5px`（73 处）。
基线与定制细节见 `docs/08-web-visual-baseline.md`；老壳自身仍运行在 8091，可直接对照验收。

## 0. 信息架构

```
┌─────────────────────────┬──────────────────────────────────────────────┐
│ 工程控制台               │  右侧内容区（.content）                        │
│ Engineering Console      │                                              │
│                         │  ── 交换机管理（默认）──                       │
│ 功能模块（.module-nav）   │   设备视图 #overview-cards，三个平级根 TAB：    │
│  · OLT/ONU 管理          │     面板 │ VLAN │ 设置                          │
│  · TR069 管理            │       · 面板：设备概况 + 接口状态（.device-monitor）│
│  · 交换机管理 ← 默认激活  │       · VLAN：端口 VLAN 查询 / 下发             │
│                         │       · 设置：交换机接口采集 + 在线嗅探          │
│ 注册状态（顶部状态条）     │         （settings-form / settings-toggle）    │
│ 左侧栏台账（随模块切换）    │                                              │
│  · 交换机：设备台账面板    │  ── OLT/ONU 管理 ──                          │
│  · OLT：OLT 设备台账面板   │   OLT 设备详情（选中设备）：信息 + 连接结果     │
│  · TR069：都不显示        │   （版本 / 机框 / 运行时长 / 板卡数 / ONT 统计  │
│ 提示（.notices）          │    + 板卡表：槽位 / 板卡 / 状态 / 温度 / 端口数）│
│                         │                                              │
│                         │  ── 其他模块 ──                              │
│                         │   模块占位页（.panel + .empty-state）          │
│                         │   按 /api/capabilities 标注 已安装 / 未安装    │
└─────────────────────────┴──────────────────────────────────────────────┘
```

规则（`web/src/app.js`）：
1. 左侧栏一级导航固定三项：**OLT/ONU 管理、TR069 管理、交换机管理**（`MODULE_VIEWS`），
   与后端 `/api/capabilities` 的 `modules[].id`（olt / tr069 / switch）对齐；
2. 点击菜单同时切换**左栏台账面板**与**右侧内容区**：
   交换机 → 左栏「设备台账」+ 右侧设备视图（根 TAB：**面板 / VLAN / 设置**，三者平级）；
   OLT/ONU 管理 → 左栏 **OLT 设备台账**（与交换机台账同款：数量 + 刷新/新增/连接/修改/删除 + 设备列表）
   + 右侧 **OLT 设备详情**（`web/src/modules/olt.js`：连接结果、板卡表）；
   TR069 管理 → 两个台账面板都隐藏 + 右侧模块占位页（未安装模块按 `-4` 语义提示，而不是报成参数错误）；
3. **切换模块即清理上一个模块的操作痕迹**（`clearModuleTransients()`）：清空 `#notices` 提示条，
   并关闭设备台账表单 / 二次确认框 / 端口弹窗 / VLAN 弹窗 / SSH 终端等临时弹窗；
4. **刷新页面与所选菜单同步**：页面加载只初始化当前选中的模块（`initModuleFromHash(true)` → `activateModule({initial:true})`）——
   交换机管理才加载采集配置并刷新工作台（提示「交换机工作台数据已刷新。」）；OLT / TR069 只渲染自己，
   不会跑交换机刷新、也不会出现交换机提示（采集配置在切到「设置」TAB 时按需加载）；
5. 支持 `#module=<switch|olt|tr069>` 直达与前进/后退；
6. 注册 / 授权仍属平台层：`#registration-status` 状态条在左侧栏，点击弹出注册弹窗（老壳同款）。

## 1. 能力接口（用于模块安装状态）

```
GET|POST /api/capabilities
```
```json
{
  "product": "txconsole",
  "version": "0.1.0-m0",
  "apiVersion": 2,
  "modules": [
    {"id": "olt","version":"0.1.0-m0","nav":{"title":"OLT/ONU 管理","order":10},
     "menus":[{"id":"olt.ont","title":"ONT 管理","path":"/olt/ont","order":20}]},
    {"id": "tr069","version":"0.1.0-m0","nav":{"title":"TR069 管理","order":20}, "menus":[]},
    {"id": "switch","version":"0.1.0-m1","nav":{"title":"交换机管理","order":30},
     "menus":[{"id":"switch.ledger","title":"设备台账","path":"/switch/devices","order":10}]}
  ]
}
```

前端处理规则：
1. 读取失败或清单为空时按「已安装」处理，只在菜单与占位页上标注状态，不影响交换机功能；
2. 未安装模块在菜单上弱化显示并附「未安装」标记，点击进入占位页说明 `-4` 语义；
3. 交换机页面的数据来自 `/itms/*`（见第 3 节），不依赖 capabilities。

## 2. 未安装模块的响应约定

后端对未安装模块的老接口返回 `code=-4`（HTTP 200）：

```json
{"code": -4, "message": "当前设备未安装该功能模块（switch）",
 "data": {"module": "switch", "installed": ["olt", "tr069"]}}
```

前端遇到 `-4` 时：菜单弱化 + 占位页说明；不允许把 `-4` 当成“参数错误”提示。

## 3. 接口约定（老壳 `api.js` 原样使用）

- 统一 `POST` + form（`nAct` 驱动，`saveData` 为 JSON 字符串），返回体固定 `{code,message,data}`：
  0 成功、-1 参数/业务错误、-2 授权过期、-3 未注册、-4 模块未安装；
- 交换机业务：`/itms/handleSwitchInfo/`、`/itms/handleDeviceInformation/`、`/itms/handleSwitchPortDetail/`、
  `/itms/handleSwitchVlan/`、`/itms/handleSwitchTerminal/`、`/itms/getTerminalMAC/`、
  `/itms/handleSwitchCollectionSettings/`、`/itms/handleSwitchSnifferConfig/` 等（全部由统一工程骨架实现）；
- 平台层：`/itms/handleRegistrationInfo/`（注册信息）；
- 老壳自有端点：`/switchweb/api/encrypt-switch-credentials`（设备凭据传输前加密，由 `web/server.cjs` 本地实现）；
- 过渡期接口返回 `code=-1` + `message="接口尚未迁移到统一工程（过渡期）"`，前端按错误提示展示；
- `/client/*`（Python TR069）由 `web/server.cjs` 反代到骨架（骨架再转发给 `/itms-py`）。

## 4. 运行方式与文件结构

```bash
# 1) 起后端骨架（隔离注册目录，不动设备真实数据）
make run                      # = TXAPI_DATA_DIR=/tmp/txapi-dev/data TXAPI_REGISTRATION_DIR=/tmp/txapi-dev/reg dist/txapi（:18092）

# 2) 起界面（静态资源 + /itms、/api、/client 反代；凭据密钥用 $DEV_DIR/reg/aeskey.key）
make web-run                  # 默认 :18094；PORT=18095 make web-run 可换端口；TLS_CERT/TLS_KEY 启用 HTTPS
                              # 演示目录：DEV_DIR=/tmp/txapi-demo make web-run
```

`web/server.cjs` 与老壳的差异：默认端口 18094、默认真实后端 18092、反代前缀增加 `/api` `/client` `/healthz`、
凭据密钥文件可用 `SWITCH_LOGIN_KEY_FILE` 覆盖（默认仍是设备真实路径，生产无需设置）、
静态资源响应带 `Cache-Control: no-store`（改完 css/js 刷新即生效，避免浏览器拿旧文件）。

```
web/
├── index.html            界面入口（老壳同款 head，标题为 工程控制台）
├── server.cjs            静态服务 + /switchweb/api/encrypt-switch-credentials + 反代（默认 18094 → 18092）
├── package.json          type=module，无第三方依赖（与 /switchweb 一致，浏览器原生 ESM）
└── src/
    ├── main.js           引导（原样）
    ├── api.js            老接口封装（原样）
    ├── app.js            老壳应用（三处定制 + 模块业务页挂载点 MODULE_PAGES）
    ├── modules/olt.js    OLT 业务页：设备台账（连接/新增/修改/删除）+ 右侧 8 栏目 TAB；
    │                     「OLT状态」含 概览（含耗时 X.X 秒）、机框示意图（板卡图 + 端口叠加）、
    │                     板卡明细表与 单槽「刷新」/「刷新所有板卡状态」
    ├── assets/olt/       板卡示意图素材（olt_<型号>.png，19 种，41~47 × 719 竖条）
    ├── styles.css       老壳样式（基线逐字节一致；末尾追加定制样式，含 OLT 机框示意图/端口叠加）
    └── favicon*         站点图标（老壳同款）
```

**板卡示意图素材来源**：`src/assets/olt/olt_<型号>.png` 由老控制台 `/fusiongateway/web/assets/png/chunk-olt_<型号>.png`
**复制并重命名**而来（逐字节未改），用于 OLT状态 的机框示意图；未收录的型号显示"暂无该板卡的示意图"占位，
不产生 404。新增型号时把同名 PNG 放进该目录并在 `web/src/modules/olt.js` 的 `BOARD_IMAGE_NAMES`
与 `BOARD_PORT_GROUPS`（端口纵坐标分组，沿用老控制台偏移）中登记即可。

### 定制摘要

| # | 需求 | 落地位置 |
|---|---|---|
| 1 | `Switch Operations` → **Engineering Console**，`SwitchWeb` → **工程控制台** | `app.js` 的 `layout()` / `legacyLayout()`、`index.html` 的 `<title>` |
| 2 | 左侧栏加入 OLT/ONU 管理、TR069 管理、交换机管理菜单 | `app.js`：`MODULE_VIEWS` + `renderModuleNav()` + `activateModule()` + `#module-nav` |
| 3 | 采集配置并入右侧根 TAB（与「面板 / VLAN」平级；TAB 文案为 **设置**） | `app.js`：`renderOverview()` 三项根 TAB（`data-tab="panel\|vlan\|collection"`）+ `renderCollectionPanelContent()` / `loadCollectionPanel()` / `renderCollectionPanel()`；内容壳与面板 / VLAN 统一为 `<div class="device-monitor" role="region" aria-label="设置">`（内层仅 `.collection-panel__toolbar` 保留）；事件在 `#overview-cards` 委托（不再有 `#settings-dialog-root` 与独立 `#collection-panel`） |
| 4 | 所有 `border-radius` 统一为 `5px` | `styles.css`：73 处声明全部改写（胶囊/正圆类元素随之变为 5px 圆角矩形） |
| 5 | 设备选择记忆 + 设备错误提示分级 | `app.js`：`DEVICE_PREFERENCE_KEY` / `preferredDeviceId()` / `rememberWorkingDevice()` / `describeDeviceError()`（见 `docs/08` 定制 4） |
| 6 | 模块业务页挂载点（M3 起） | `app.js`：`MODULE_PAGES` + `renderModuleView()` 优先渲染业务页；左栏台账面板（`#switch-ledger-sidebar` / `#olt-ledger-sidebar`）随模块切换，并用 `clearModuleTransients()` 清理上一个模块的提示与弹窗；页面文件 `web/src/modules/olt.js`：**列结构与交换机台账对齐**（勾选列 + 状态点 + 信息区 + 类型标签，外加「全选」与 **「删除 (n)」批量删除**），工具条为 **[连接][新增][修改][删除]**（无刷新按钮），信息区显示 **`<IP>:<Port>` + 备注**（0 TELNET、1 SSH）；新增/修改弹窗按规格校验（IPv4 或域名、账号字母数字下划线 ≤32、口令字母+数字或特殊字符 ≤32、端口 1-65535 默认 23、备注可选）；复用既有 class，`api.js` / `main.js` 仍与老壳逐字节一致（见 `docs/08` §2.1、`docs/09` §5.2） |

### 前端回归

```bash
make web-smoke    # 无浏览器：最小 DOM 桩真实启动 createApp() + 打真实后端 + 校验三处定制与基线未漂移
make web-cred     # 凭据链路端到端：界面加密端点 → 台账保存 → 回读解密 → 清理自检设备
make olt-ui       # OLT 模块 × 前端壳：菜单/面板互斥/刷新同步 + 穿壳调接口 + 左栏台账操作（自带假 OLT）
make olt-dom      # OLT 台账页「真实 DOM」校验：jsdom 解析后真点击真冒泡（jsdom 见 tools/domcheck，未装则跳过）
```

> `make olt-dom` 依赖 jsdom，只装在 `tools/domcheck/`（浏览器端 `web/` 保持零依赖）；
> 需要时执行 `cd tools/domcheck && npm install`。它专门覆盖最小 DOM 桩测不到的问题
> （例如"工具条按钮漏了 `data-olt-action` → 真实页面点击没反应"、"对复选框 preventDefault → 勾选失效"）。

## 5. M1 剩余 / M3

- `tr069` 业务页（M2 迁入后端处理器后替换占位页）；OLT 的 ONT 管理 / 线路·业务模板两个 TAB 页（待后端 Act=9 等分支迁入）；
- 三形态打包机（switch / olt / all）与安装脚本（`deploy/README.md` 第 2、3 节）。
