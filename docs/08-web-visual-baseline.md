# 08 · WEB 基线：整体采用老 switchweb 的样式与 JS

> 结论（本条为硬约束）：统一工程的部署界面**整体采用 `/switchweb` 的样式与 JS**——
> `styles.css` / `app.js` / `api.js` / `main.js` / `server.cjs` 一律以老壳为基线原样复制，
> 只做三处 JS 定制（品牌文案、左侧栏模块菜单、采集配置进右侧面板，TAB 文案为「设置」）**＋ 一处全局样式统一（圆角全部 5px）**
> **＋ 一处模块业务页挂载点（M3 起：`MODULE_PAGES`，页面代码放 `web/src/modules/`，不再往 app.js 堆业务代码）**。
> 用户可直接对照 `/switchweb`（老壳 :8091）验收外观。

## 1. 落地方式

```
web/index.html          ← 复制 /switchweb/index.html（仅改 <title>/<meta description>）
web/src/styles.css      ← 复制 /switchweb/src/styles.css（基线逐字节一致，末尾追加 176 行定制样式）
web/src/app.js          ← 复制 /switchweb/src/app.js（三处定制 + 模块业务页挂载点，见第 2 节）
web/src/modules/olt.js  ← OLT 业务页（设备台账：连接 / 新增 / 修改 / 删除），复用 styles.css 既有 class，不新增样式
web/src/api.js          ← 复制 /switchweb/src/api.js（原样，未改）
web/src/main.js         ← 复制 /switchweb/src/main.js（原样，未改）
web/src/favicon*        ← 复制老壳站点图标
web/server.cjs          ← 复制 /switchweb/server.cjs（仅改端口/后端地址/代理前缀，见第 3 节）
```

`api.js` 打的就是统一工程骨架已经实现的 `/itms/*` 老接口（`/itms/handleSwitchInfo/`、
`/itms/handleDeviceInformation/`、`/itms/handleSwitchVlan/`、`/itms/handleSwitchTerminal/` 等），
所以「采用老壳 JS」不需要改任何接口路径；唯一的老壳自有端点是
`/switchweb/api/encrypt-switch-credentials`（设备凭据传输前加密），由 `web/server.cjs` 本地实现（原样保留）。

## 2. 三处定制（`web/src/app.js`）

| # | 需求 | 实现 |
|---|---|---|
| 1 | 品牌文案 | `layout()` 与 `legacyLayout()`：`brand__eyebrow` = **Engineering Console**（<工程控制台> 的英译），`h1` = **工程控制台**；`index.html` 的 `<title>` 同步为 工程控制台 |
| 2 | 左侧栏加入 OLT/ONU 管理、TR069 管理、交换机管理 | `layout()` 的 `.sidebar` 中 brand 之后新增 `<nav class="nav module-nav" id="module-nav">`；由 `renderModuleNav()` 按 `MODULE_VIEWS` 渲染，`activateModule()` 负责切换 |
| 3 | 采集配置并入右侧根 TAB（与「面板 / VLAN」平级）；TAB 文案为 **设置** | 删除侧栏「采集配置」按钮、`#settings-dialog-root` 与独立容器 `#collection-panel`；`renderOverview()` 的根 TAB 增为三项：`data-tab="panel" / "vlan" / "collection"`（第三项按钮文案「设置」），第三项内容由新的 `renderCollectionPanelContent()` 生成（表单组件 `settings-form` / `settings-toggle` / `settings-segmented` 与提交逻辑沿用），内容壳与另两个 TAB 统一为 `<div class="device-monitor" role="region" aria-label="设置">`（内层仅 `.collection-panel__toolbar` 保留），事件在 `#overview-cards` 上委托，`renderCollectionPanel()` 只在选中该 TAB 时重绘 |
| 4 | 设备选择记忆 + 设备错误提示分级（VLAN/采集"取不到数据"的体验修复） | ① `DEVICE_PREFERENCE_KEY`（localStorage）记 `lastWorkingDeviceId` / `lastDeviceId`，`loadDevices()` 依次回落「上次实时连过 → 上次选中 → 台账第一台」，避免一进 VLAN 就停在不可达设备上；`rememberWorkingDevice()` 在设备 VLAN / 端口 VLAN / 实时采集成功后写入。② `describeDeviceError(message, device)` 把超时 / 拒连 / 路由 / 认证 / RPC 无响应翻译成可执行提示（原始文本保留在 `title`），参数类错误原样放过；VLAN 设备列表、接口 VLAN、连接与 VLAN 应用错误均改走它 |
| 5 | 三个根 TAB 尺寸/内容对齐 + 提示/告警字号统一 | `styles.css` 追加块：**面板 grid 行不再被拉伸**——`.overview-panel { align-content: start }`（基线 `.overview-panel { display: grid; gap: 0; … }` 没写 `align-content`，默认 `stretch`；面板被 `min-height: 100vh` 的 shell 拉到整屏高，两行会被剩余高度拉伸，内容短的 TAB 会把上面的 TAB 条行一起“撑高”）；**TAB 条尺寸固定**——`.overview-tabs { width: 100%; height/min-height/max-height: 47.5px; box-sizing: border-box; overflow: hidden; align-items: flex-end }`（宽度即内容区宽，例如 1422px；高恒为 47.5px，条内任何节点都撑不动它，三个 TAB 永远同一尺寸）；**根 TAB 按钮高度硬固定 34px**：`.overview-tabs button, #overview-cards .overview-tabs button { height/min-height/max-height: 34px; box-sizing: border-box; padding: 0 12px; display: inline-flex; align-items: center; justify-content: center; align-self: flex-end; flex: none; white-space: nowrap; overflow: hidden; line-height: 1.4 }` —— `height/min/max` 三重写死 + 选择器带 `#overview-cards` 提权（特异性 1,2,2，本文件没有任何 `!important`），任何后续类规则、窄屏媒体查询都改不动它；`align-self` 防容器 `align-items: stretch` 拉伸，`nowrap` + `overflow: hidden` 让标签折行 / 溢出都不影响按钮盒子（基线原先只有 `min-height: 34px`，交叉轴为 auto，会被同行最高项“带高”）；**同口径**也用于对话框 / 面板内 TAB 条 `.port-dialog-tabs button`（`height/min/max: 40px`，但刻意不写左右 padding 与 `flex`，避免覆盖窄屏媒体查询的 `flex: 1` 与 `padding-left/right: 6px`）；三个 TAB 的内容壳统一为 `.device-monitor`——`.overview-tab-content > .device-monitor { margin-top: 8px }`、`> .device-monitor > .overview-vlan-section:first-child { padding-top: 0 }`（去掉 VLAN 区块自带的上内边距）、`> .device-monitor > .settings-form + .settings-form { margin-top: 8px }`（补回原 `grid gap` 的间距，分隔线仍由基线 `.settings-form + .settings-form` 提供），使「面板 / VLAN / 设置」首块内容顶距统一为 30px；提示与告警文字（`.port-dialog-error`、`.settings-dialog__error`、`.settings-dialog__load-error`、`.registration-form__hint`、`.notice p`、`.empty-state`、`.collection-panel__toolbar`、`.device-vlan-count`、`.vlan-members`）统一为正文 **14px** |
| 6 | OLT 台账默认选中第一条（业务方反馈 2026-09-22） | `web/src/modules/olt.js` 的 `refresh()` 与交换机台账同口径（`app.js loadDevices` 的"回落到台账第一台"）：空列表 → 无选中；选中项被删除 / 首次进入 → **自动选中台账列表第一条记录**并渲染右侧面板。此前只在"选中项已不存在"时清 0，导致选中「OLT/ONU 管理」进来右侧面板一直停在"暂未选择 OLT 设备"（工具条「连接/修改」也不可用）。真实 DOM 断言见 `tools/olt_ui_dom_check.mjs`（`make olt-dom`：进入页面即"第一条为选中态且右侧面板渲染其栏目"、"台账非空时恰有一行选中"），桩工具 `tools/olt_ui_integration.mjs` 的旧口径断言（"未选设备时为空态提示"）同步更新；详见 `docs/09-olt-m2.md` §5.2 #20 |
| 7 | 右侧「OLT 设备详情」栏目化（业务方规格 2026-09-22） | `web/src/modules/olt.js`：面板内 **8 个栏目 TAB**（OLT状态 / 配置信息 / VLAN / DBA/线路模板 / 业务/TR069模板 / ONT配置/状态 / ONT注册 / 日志），复用交换机工作台的 `.overview-tabs` / `.overview-tab-content` 样式；「OLT状态」承载「连接」采集的版本/补丁、机框（+说明）、OLT运行时间、连接时间、状态刷新时间、在线 ONT/板卡、板卡"已插 N 块（机框 M 槽）"与 **耗时 X.X 秒**（点「连接」自动切回该栏目），并渲染 **板卡示意图**（老控制台素材 `web/src/assets/olt/olt_<型号>.png` + 按老算法叠加 PON/GE 端口与状态点）与 **单槽刷新 / 刷新所有板卡状态**（`/itms/handleBoardAndSlotInfo/`）；其余 7 个栏目为待迁入占位（写明对应老接口，不假成功）；**面板内不再显示**设备详情行（`OLT 设备详情 · <IP>`、`登录账号：…`）、`设备信息/已连接` 工具条与 **连接/修改/删除** 按钮（操作走左栏「OLT 设备台账」工具条），设备身份改由 `data-olt-device-id` 承载。真实 DOM 断言见 `tools/olt_ui_dom_check.mjs`；详见 `docs/09-olt-m2.md` §5.2 #21（栏目化）与 #22（信息补齐 + 耗时 + 板卡图） |

模块切换规则（`activateModule`）：
- 交换机管理 → 右侧显示设备视图（根 TAB：**面板 / VLAN / 设置**，三者平级），并在缺数据时补一次 `refreshAll()`，
  左栏显示交换机「设备台账」面板（全选 / 数量 / 连接 / 新增 / 导入 / 导出 / 修改 / 删除 + 设备列表）；
- OLT/ONU 管理 → 右侧显示 **OLT 设备详情**（`web/src/modules/olt.js`，M3 起按 `MODULE_PAGES` 挂载），
  左栏切换为 **OLT 设备台账**面板（数量 / 刷新 / 新增 / 连接 / 修改 / 删除 + OLT 设备列表；
  列表行显示在线状态点、账号、`IP:端口`、备注与连接类型，与交换机台账同款）；
- TR069 管理 → 右侧显示该模块占位页（`.panel` + `.panel__header` + `.empty-state`），
  左栏两个台账面板都隐藏，并依据 `/api/capabilities` 标注「已安装 / 未安装」（未安装即 `code=-4` 语义）；
- **切换模块即清理上一个模块的操作痕迹**（`clearModuleTransients()`）：清空提示条 `#notices`，
  并关闭设备台账表单、二次确认框、端口弹窗、VLAN 弹窗、SSH 终端等临时弹窗/态；
- 支持 `#module=<switch|olt|tr069>` 直达与浏览器前进/后退（`hashchange`）；
- **刷新页面（F5）与所选菜单同步**（2026-09-22 修）：`createApp()` 只初始化当前选中的模块
  （`initModuleFromHash(true)` → `activateModule({initial:true})`）——交换机管理才加载采集配置并刷新工作台，
  OLT / TR069 只渲染自己；此前的 BUG 是无条件 `refreshAll()`，导致刷新时停在 OLT 也会弹「交换机工作台数据已刷新。」。

### 2.1 模块业务页挂载点（第 4 处定制，2026-09-22）

`app.js` 顶部：

```js
import { createOltPage } from "./modules/olt.js";
const MODULE_PAGES = { olt: createOltPage };   // 键 = /api/capabilities 的 module id
const modulePageInstances = {};                 // 页面实例（只创建一次）
```

`renderModuleView(moduleId)` 先查 `MODULE_PAGES`：命中就调用页面工厂（注入 `escapeHTML / toInt / notify / confirmAction`）
并渲染到 `#module-view`，未命中才回退占位页。页面自己负责：
数据加载与重绘、事件委托（左栏面板与 `#module-view` 各挂一次，切换模块/外壳重绘都不丢）、
外壳缺失时重建。业务代码因此不再往 `app.js` 里堆，`api.js` / `main.js` 依旧与老壳逐字节一致。

**约定（踩坑记录 2026-09-22）**：模块页放在**左栏**的按钮/勾选框必须带 `data-olt-action`（与右侧 `#module-view` 一致），
因为页面统一用 `closest("[data-olt-action]")` 做事件委托——只写 `id` 会导致真实浏览器里"点了没反应"
（最小 DOM 桩测不出，已由 `make olt-dom` 的真实 DOM 校验覆盖）；另外**勾选框不要在点击委托里 `preventDefault`**，
否则浏览器会取消默认勾选（勾选/全选失效），勾选框交给 `change` 事件处理。

新增样式 176 行（`styles.css` 末尾，带醒目注释块）：`.module-nav`（激活态/未安装态）、
`.module-view`（右侧视图容器）、`.collection-panel__toolbar`（设置 TAB 工具栏）；
基线部分除第 3 节的圆角统一外一字未改，
因此「逐行比对 `/switchweb/src/styles.css`，差异只允许出现在 border-radius 所在行」永远成立，
老壳换皮肤时仍可整体重同步。

## 3. 圆角统一：所有 `border-radius` = `5px`

需求：界面所有圆角统一为 `5px`。做法：把 `web/src/styles.css` 中**全部 73 处** `border-radius`
声明（含单行内多条声明的写法）统一改写为 `border-radius: 5px`，基线其余内容逐行未动：

```bash
cd web && python3 - <<'PY'
import io, re
p = 'src/styles.css'
s = io.open(p, encoding='utf-8').read()
io.open(p, 'w', encoding='utf-8').write(re.sub(r'border-radius\s*:\s*[^;\n}]+', 'border-radius: 5px', s))
PY
```

| 原取值 | 处数 | 典型元素 | 统一后的视觉变化 |
|---|---|---|---|
| `5px` | 19 | 面板、表格、字段 | 不变 |
| `8px` / `6px` / `3px` / `2px` / `1px` / `12px` / `14px` / `16px` / `18px` / `22px` | 33 | 弹窗、指标卡、提示块 | 圆角收敛为 5px |
| `999px` / `99px` | 8 | `.nav a`、`.settings-status`、`.settings-toggle input`、`.device-loading__spinner`、`.vlan-status`、进度条 | 胶囊 / 圆点 → 5px 圆角矩形（加载圈变为圆角方，动画不受影响） |
| `50%` | 8 | 开关滑块 `::before`、状态点、`.confirm-dialog__mark`、图例点 | 正圆 → 5px 圆角小方块 |
| `var(--radius)` / `var(--radius-sm)` / `calc(var(--radius) - 6px)` / `inherit` / `0` | 5 | 面板、弹窗字段、侧栏按钮 | 显式写死为 5px，避免继承/计算带来的不一致 |

回归口径（`tools/web_smoke.mjs`）：① `styles.css` 所有 `border-radius` 取值集合必须为 `{5px}`；
② 与老壳基线逐行比对，只有含 `border-radius` 的行允许不同，其余行零差异。

## 4. 部署服务（`web/server.cjs`）

| 项 | 老壳 | 统一工程 |
|---|---|---|
| 默认端口 | 8091 | **18094**（`PORT` 可覆盖） |
| 默认真实后端 | `http://127.0.0.1:8092` | **`http://127.0.0.1:18092`**（`BACKEND_TARGET` / `TXAPI_TARGET` 覆盖） |
| 反代前缀 | `/itms/*` | `/itms/*`、`/api/*`（capabilities 与 `/api/v2/*`）、`/client/*`、`/healthz` |
| 自有端点 | `/switchweb/api/encrypt-switch-credentials` | 原样保留（设备凭据传输前 AES-GCM 加密） |
| 凭据密钥来源 | `/home/switchfile/aeskey.key`（缺失则生成） | 同名默认值，但可用 **`SWITCH_LOGIN_KEY_FILE`** 覆盖；`make web-run` 默认指向 `$(DEV_DIR)/reg/aeskey.key` |
| TLS | `TLS_CERT` + `TLS_KEY` | 原样保留 |
| 静态资源缓存 | 无缓存头（浏览器可能继续用旧的 css/js） | 静态响应带 **`Cache-Control: no-store`**（文件本就每次请求实时读盘，改完刷新即生效） |

凭据密钥必须与骨架 `ctx.CredentialKeyPath`（即注册目录下的 `aeskey.key`）是**同一把**，
否则「界面加密 → 后端解密」会失败。`make web-run` 因此默认带上隔离路径，
既保证链路一致，也保证本地运行不读写设备真实密钥文件（本工程硬性约束）。

## 5. 回归方式

```bash
make run && make web-run     # 后端 18092 + 界面 18094
make web-smoke               # 无浏览器冒烟（见下）
make web-cred                # 凭据链路端到端（加密端点 → 台账保存 → 回读解密 → 清理）
```

`tools/web_smoke.mjs` 用最小 DOM 桩**真实启动 `web/src/app.js` 的 `createApp()`**（并真的请求后端），断言：
1. `createApp()` 不抛异常，品牌文案已是 Engineering Console / 工程控制台，旧文案不再出现；
2. 左侧栏三个模块菜单齐全、默认激活交换机管理、点击可切换视图（含 OLT 占位页）；
3. 采集配置并入右侧根 TAB：`#overview-cards` 内三项平级（面板 / VLAN / 设置），
   点击「设置」就地渲染接口采集 + 在线嗅探两个 `settings-form`，无独立容器与弹窗挂载点；
4. 后端联通性：台账 / 采集配置 / 嗅探配置等接口真实调用且返回码合法；
5. 基线未漂移：`styles.css` 以老壳文件为前缀，`api.js` / `main.js` 与老壳逐字节一致；
6. 本工程新增的 class 都有样式定义（老壳自带的无样式类不计入，避免把继承现状当回归）；
7. 设备选择记忆与错误提示分级：预置 `switchweb.devicePreference`（localStorage 桩）→ 断言默认选中「上次实时连过」的设备、
   点击设备后写回记忆、激活态随之切换，并静态校验 `describeDeviceError` 已用于 VLAN 错误展示；
8. 三 TAB 对齐与字号统一：静态校验 `styles.css` 仍带这两段定制规则标记（`三个根 TAB` 视觉对齐、`提示 / 告警字号统一`），
   并校验三个 TAB 内容壳统一为 `.device-monitor`（含 VLAN 首块顶距规则、设置 TAB 的 `role="region" aria-label="设置"`）、
   根 TAB 条尺寸固定（`width: 100%` + `height/min/max: 47.5px` + `overflow: hidden`，条内节点撑不动）、
   面板 grid 行不被拉伸（`.overview-panel { align-content: start }`，基线默认 `stretch`）、
   根 TAB 按钮高度硬固定（`height/min-height/max-height: 34px` + `border-box` + `align-self: flex-end` + `overflow: hidden`）、
   对话框 / 面板内 TAB 条按钮同口径硬固定（`height/min/max: 40px`，且不覆盖窄屏的左右 padding 与 `flex`）、
   面板「数据来源」中文值字号统一（`.device-facts__source-label` = 12px，与同为 13px 的英文/数字值视觉一致）。

`tools/web_cred_e2e.py` 校验最容易踩坑的一环——设备凭据链路：
界面 `/switchweb/api/encrypt-switch-credentials` 加密 → `/itms/handleSwitchInfo/` 落库 →
后端用同一把 `aeskey.key` 解密回读为明文 → 删除自检设备（可重复运行）。

## 6. 已知差异（有意为之）

- 圆角统一：见第 3 节——所有 `border-radius` 均为 `5px`，因此胶囊、正圆类元素（开关滑块、状态点、
  加载圈、确认弹窗标记）呈 5px 圆角矩形，与老壳观感不同属本次需求。
- 老壳是「sidebar = 品牌 + 注册状态 + 设备台账 + 采集配置按钮，主区 = 设备面板」；
  统一工程在此之上多了「一级导航 = 功能模块」，并把采集配置从弹窗搬到主区面板（TAB 文案为「设置」）。
- 老壳 `app.js` 里少量类名本身没有样式（如 `.button--small`、`.device-fan-chart`、`.highcharts-background`），
  属继承现状，本次不修（改它们会偏离「原样采用」的目标）。
