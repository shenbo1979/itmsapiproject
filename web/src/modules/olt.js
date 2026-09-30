// OLT 业务页（web/src/modules/olt.js）。
//
// 信息架构与本工程交换机管理保持一致（见 web/README §0、docs/08 §2.1）：
//
//   左侧栏（app.js 的 #olt-ledger-sidebar）  「OLT 设备台账」：紧凑设备列表 + 工具条
//                                            [刷新][新增][连接][修改][删除]（选中行后才可用）
//   右侧内容区（#module-view）               「OLT 设备详情」：选中设备的信息 + 连接结果
//                                            （版本/机框/运行时长/板卡数/ONT 统计 + 板卡表）
//
// 显示与按钮沿用交换机「设备台账」同一套 class（device-sidebar-panel / device-list-row /
// device-list-item / device-vlan-toolbar / data-table / table-actions / detail-grid / detail-tile /
// device-form-modal…），不新增样式，也不改 web/src/api.js（基线要求与老壳逐字节一致）。
//
// 用到的老接口（路径与参数名与老壳一致）：
//   POST /itms/handleConnectOltUser/  Act=0 查询 / Act=1 新增 / Act=2 修改 / Act=3 删除（saveData 为 JSON）
//   POST /itms/handleOltConnect/      Act=1 连接并读取版本/温度/板卡（connData 为 JSON；refresh=1 强制刷新）
//
// 挂载：app.js 的 renderModuleView() 在 moduleId === "olt" 时调用本页（见 app.js 的 MODULE_PAGES）。
import { postForm } from "../api.js";

// 连接类型（业务方规格 2026-09-22）：0 = TELNET（默认）/ 1 = SSH。
// 老口径 2 = TELNET / 3 = SSH 仍能被后端识别（历史数据兼容），但表单只提供 0/1。
const TYPE_OPTIONS = [
  { value: 0, label: "TELNET" },
  { value: 1, label: "SSH" }
];

// DBA 模板类型（type1..type5）—— 文案对齐老控制台 chunk-dba_lineTem.js：
//   type1-固定带宽 / type2-保证带宽 / type3-保证带宽+最大带宽 / type4-最大带宽 / type5-固定带宽+保证带宽+最大带宽。
// typeFields 描述每种类型需要填写的带宽字段（新增表单据此显示/隐藏输入行，也与后端 validateDBAWrite 同规则）。
const DBA_TYPE_OPTIONS = [
  { value: "type4", label: "type4-最大带宽", fields: ["Max"] },
  { value: "type1", label: "type1-固定带宽", fields: ["Fix"] },
  { value: "type2", label: "type2-保证带宽", fields: ["Assure"] },
  { value: "type3", label: "type3-保证带宽+最大带宽", fields: ["Assure", "Max"] },
  { value: "type5", label: "type5-固定带宽+保证带宽+最大带宽", fields: ["Fix", "Assure", "Max"] }
];

// OLT 写操作（DBA / VLAN / 终端用户增删改）的前端兜底超时（毫秒）。
// 这些请求后端已加绝对值超时（oltConfigCollectTimeout=30s）且 applyDBACommand 改为等设备回
// 提示符；前端再加一层超时，避免在某些极端情况（后端卡在设备读取循环、HTTP 写响应迟迟不回）
// 下界面无限停留在“正在下发…”。未收到响应即 aborted，postForm 抛“请求超时，请稍后重试”。
const OLT_WRITE_TIMEOUT_MS = 20000;

// 字段校验规格（与后端 internal/modules/olt/validate.go 同一套，见 docs/09 §5.2）：
//   IP       合法的 IPv4 地址或合法域名
//   Username 大小写字母 + 数字 + 下划线，长度 ≤ 32
//   Password 大小写字母 + 数字 + 特殊字符组合，长度 ≤ 32
//   Port     1 - 65535（默认 23）
const USERNAME_PATTERN = /^[A-Za-z0-9_]{1,32}$/;
const PASSWORD_PATTERN = /^[A-Za-z0-9!@#$%^&*()_+\-=[\]{};:'",./<>?\\|`~]{1,32}$/;
const DOMAIN_PATTERN = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i;

// isIPv4 判断点分十进制 IPv4（4 段、每段 0-255、不允许前导零）。
function isIPv4(value) {
  const parts = String(value || "").split(".");
  if (parts.length !== 4) return false;
  return parts.every((part) => {
    if (!/^\d{1,3}$/.test(part)) return false;
    if (part.length > 1 && part.startsWith("0")) return false;
    const number = Number(part);
    return number >= 0 && number <= 255;
  });
}

// validateOltForm 返回第一条校验错误（无错误返回空串）；与后端规则一致。
// skipPasswordStrength=true（编辑且口令未改动）时只校验口令长度与字符集。
function validateOltForm(form, { skipPasswordStrength = false } = {}) {
  const ip = String(form.IP || "").trim();
  if (!ip) return "请输入 IP 或域名。";
  if (ip.length > 253) return "IP 或域名过长（≤ 253）。";
  if (!isIPv4(ip) && !DOMAIN_PATTERN.test(ip)) return "请输入合法的 IPv4 地址或域名。";

  if (!["0", "1"].includes(String(form.Type))) return "连接类型只能是 0（TELNET）或 1（SSH）。";

  if (!USERNAME_PATTERN.test(form.Username || "")) {
    return "登录账号只能包含大小写字母、数字与下划线，长度 1-32。";
  }

  const password = form.Password || "";
  if (!password) return "请输入登录密码。";
  if (!PASSWORD_PATTERN.test(password)) {
    return "登录密码只能包含大小写字母、数字与特殊字符，长度 1-32。";
  }
  if (!skipPasswordStrength && !(/[A-Za-z]/.test(password) && /[0-9!@#$%^&*()_+\-=[\]{};:'",./<>?\\|`~]/.test(password))) {
    return "登录密码需同时包含字母与（数字或特殊字符）。";
  }

  const port = Number(String(form.Port ?? "").trim() === "" ? 23 : form.Port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) return "端口范围 1-65535。";

  return "";
}

// createOltPage 由 app.js 注入公共工具（escapeHTML / toInt / notify / confirmAction），
// 页面自身只负责 OLT 的数据与渲染，避免复制 app.js 的实现。
//
// 右侧「OLT 设备详情」栏目（业务方规格 2026-09-22）：面板内以 TAB 组织，共 8 个栏目：
//   OLT状态 / 配置信息 / VLAN / DBA/线路模板 / 业务/TR069模板 / ONT配置/状态 / ONT注册 / 日志。
//   「OLT状态」显示「连接」采集到的 OLT 信息（版本/机框/运行时长/板卡/ONT 统计 + 板卡表）；
//   「配置信息」显示当前配置 + 终端登录用户；「VLAN」显示四类 VLAN 信息 + 增删；
//   「DBA/线路模板」显示 DBA 模板（handleDBAProfile Act=0/1/2）与 ONT 线路模板
//   （handleLineProfile Act=0/1/2，T-CONT/GEM 层级）；
//   其余栏目先做信息架构占位，占位文案写明对应的老接口，第二批按栏目迁入（不做假成功）。
//   右侧面板不再显示设备详情行（`OLT 设备详情 · <IP>`、`登录账号：…`）与连接/修改/删除按钮——
//   这些操作统一由左侧「OLT 设备台账」工具条承担。
export function createOltPage({ escapeHTML, toInt, notify, confirmAction }) {
  const state = {
    devices: [],
    loaded: false,
    loading: false,
    selectedId: 0,
    // selectedIds 是"勾选待删除"的集合（对应交换机台账的 checked 集合，用于批量删除）。
    selectedIds: new Set(),
    connectingId: 0,
    editingId: 0,
    // activeTab 是右侧面板当前栏目（跨设备保留：切设备不跳回第一个 TAB）。
    activeTab: "status",
    connections: {},
    // configs 是「配置信息」栏目按设备 id 缓存的已采集配置文本（键 → { text, refreshElapsedMilliseconds }）。
    // 与 connections 分开存：配置只读 `display current-configuration`，与"连接采集"互不依赖。
    configs: {},
    // configsLoadingId 是正在强制刷新配置的设备 id（Act=1，刷新按钮转圈/禁用）。
    configsLoadingId: 0,
    // configsReadId 是正在读已保存配置的设备 id（Act=0，切到「配置信息」栏目时触发一次）。
    configsReadId: 0,
    // terminalUsers 按设备 id 缓存的登录用户名列表（键 → []string，来自配置解析）。
    terminalUsers: {},
    // terminalUsersLoadingId 是正在读终端用户列表的设备 id。
    terminalUsersLoadingId: 0,
    // 终端用户表单弹窗上下文：{ devices, username } —— null 表示关闭。
    // mode === "add" 新增（用户名 + 两次口令）；"change" 修改口令（用户名只读 + 新口令两次）。
    terminalForm: null,
    // 终端用户删除是否进行中（防重复触发删除）。
    terminalOpPending: false,
    // vlan 按设备 id 缓存的 VLAN 功能模块数据（Act=0 返回的四类顶层数组：
    // { vlanif: vlanifdata[]（三层 VLAN 接口）, vlan: vlandata[], tran: tranvlandata[], routes: routestaticdata[] }）。
    vlan: {},
    // vlanLoadingId 是正在读 VLAN 四类信息的设备 id（Act=0，切到「VLAN」栏目时触发）。
    vlanLoadingId: 0,
    // vlanForm 是 VLAN 弹窗上下文：{ mode, deviceId } —— null 表示关闭。
    vlanForm: null,
    // vlanOpPending 是否进行中（防重复提交）。
    vlanOpPending: false,
    // dba 按设备 id 缓存的 DBA 模板数据（Act=0 返回的 DBAProfileInfo[]：
    // { Id, Name, Type, Fix, Assure, Max }，键 → { read, profiles }）。
    dba: {},
    // dbaLoadingId 是正在读 DBA 模板列表的设备 id（Act=0，切到「DBA/线路模板」栏目时触发）。
    dbaLoadingId: 0,
    // dbaForm 是 DBA 新增弹窗上下文：{ deviceId, type } —— null 表示关闭。
    dbaForm: null,
    // dbaOpPending 是否进行中（防重复提交）。
    dbaOpPending: false,
    // line 按设备 id 缓存的 ONT 线路模板数据（Act=0 返回的 OntLineProfile[]：
    // { Id, Name, Type, GemMapping[] }，键 → { read, profiles }）。
    // line 层级渲染自后端的 T-CONT / DBA模版 / GEM Port / GEM 映射ID / VLAN 子表。
    line: {},
    // lineLoadingId 是正在读线路模板列表的设备 id（Act=0，切到「DBA/线路模板」栏目时触发）。
    lineLoadingId: 0,
    // lineForm 是线路模板新增弹窗上下文：{ deviceId } —— null 表示关闭。
    lineForm: null,
    // lineOpPending 是否进行中（防重复提交）。
    lineOpPending: false,
    // srv 按设备 id 缓存的 ONT 业务模板数据（Act=0 返回的 OntSrvProfileInfo[]：
    // { Id, Name, Type, Pots, Eths, SrvVlan[] }，键 → { read, profiles }）。
    srv: {},
    // srvLoadingId 是正在读业务模板列表的设备 id（Act=0，切到「业务/TR069模板」栏目时触发）。
    srvLoadingId: 0,
    // srvForm 是业务模板新增弹窗上下文：{ deviceId } —— null 表示关闭。
    srvForm: null,
    // srvOpPending 是否进行中（防重复提交）。
    srvOpPending: false,
    // tr069 按设备 id 缓存的 TR069 管理模板数据（Act=0 返回的 Tr069ProfileInfo[]：
    // { Id, Name, AcsUrl, AcsUserName }，键 → { read, profiles }）。
    tr069: {},
    // tr069LoadingId 是正在读 TR069 模板列表的设备 id（Act=0，切到「业务/TR069模板」栏目时触发）。
    tr069LoadingId: 0,
    // tr069Form 是 TR069 模板新增弹窗上下文：{ deviceId } —— null 表示关闭。
    tr069Form: null,
    // tr069OpPending 是否进行中（防重复提交）。
    tr069OpPending: false
  };

  // DETAIL_TABS 是右侧面板的栏目定义；pending 为待迁入栏目对应的老接口（写进占位文案，便于逐条对账）。
  const DETAIL_TABS = [
    { id: "status", label: "OLT状态" },
    { id: "config", label: "配置信息" },
    { id: "vlan", label: "VLAN" },
    // DBA 模板与 ONT 线路模板均已迁入（/itms/handleDBAProfile/、/itms/handleLineProfile/ 的 Act=0/1/2），
    // 在 dbaTabBody 里各渲染一张卡片（DBA 表格在上、线路模板表格在下）。
    { id: "dba", label: "DBA/线路模板" },
    // 业务模板与 TR069 管理模板均已迁入（/itms/handleSrvProfile/、/itms/handleTr069Profile/ 的 Act=0/1/2），
    // 在 srvTabBody 里各渲染一张卡片（业务模板表格在上、TR069 模板表格在下）。
    { id: "srv", label: "业务/TR069模板" },
    { id: "ont", label: "ONT配置/状态", pending: "/itms/handleONTListAndStatus/（ONT 列表与状态）" },
    { id: "ontreg", label: "ONT注册", pending: "/itms/handleONTRegisterProfile/（注册模板）、/itms/handleONTAutoFind/（自动发现）" },
    { id: "log", label: "日志", pending: "/itms/handleOltLogList/（OLT 操作日志）" }
  ];

  function detailTab(tabId) {
    return DETAIL_TABS.find((tab) => tab.id === tabId) || DETAIL_TABS[0];
  }

  // ── 单板示意图（图与端口叠加算法对齐老控制台 chunk-oltStatus.js）──
  //
  // ① 图片：老控制台素材（41~47 × 719 的竖条），已随本工程静态资源放入
  //    web/src/assets/olt/olt_<板卡名>.png（19 种，见 docs/09 §5.2 #22 的来源说明）。
  const BOARD_IMAGE_DIR = "/src/assets/olt";
  const BOARD_IMAGE_NAMES = new Set([
    "H801GICF", "H801GICG", "H801SCUK", "H801SCUN", "H801X2CS", "H802EPBD", "H802GPBD",
    "H802SCUN", "H803GPFD", "H805GPBD", "H805GPFD", "H806GPBD", "H806GPBH", "H806GPFD",
    "H807GPBD", "H808EPSD", "H901MPLA", "H901PILA", "H902CGHF"
  ]);
  // ② 上行/下行接口板：素材本身是矮图（403/359/231px 高），业务方要求按各自自然尺寸显示，
  //    不随 719px 的高板拉长（见 docs/09 §5.2 #22 与素材 olt_H801X2CS / H801GICF / H801GICG / H901PILA）。
  //    BOARD_SMALL_IMAGE_NAMES：识别这些矮板，图片按自然尺寸渲染；
  //    BOARD_SMALL_IMAGE_SIZES：各板自然宽高（素材实测），供端口说明文字 overlay 坐标缩放使用。
  const BOARD_SMALL_IMAGE_NAMES = new Set(["H801X2CS", "H801GICF", "H801GICG", "H901PILA"]);
  const BOARD_SMALL_IMAGE_SIZES = {
    H801X2CS: { width: 50, height: 403 },
    H801GICF: { width: 50, height: 403 },
    H801GICG: { width: 47, height: 359 },
    H901PILA: { width: 60, height: 231 }
  };
  // 端口 offset 的参考坐标系高度：老控制台按 719px 高的竖条图设计（见上 BOARD_PORT_GROUPS 注释）。
  const BOARD_BASE_HEIGHT = 719;
  // 接口板（矮图）说明文字在上一步缩放（× 自然高/719）后，仅对第一个端口（P0）额外纵向上移量
  //（业务方 2026-09-23：为了让 P0 与 P1 两块说明文字拉开、避免部分重叠，只上移 P0，P1 保持原位）。
  const BOARD_SMALL_FIRST_PORT_TOP_ADJUST = 30;
  // ③ 端口纵坐标：沿用老控制台的"按板卡分组的纵向偏移"（单位 px，基于 719px 高的板卡图）。
  const BOARD_PORT_GROUPS = [
    { names: ["H802EPBD", "H805GPBD", "H806GPBD", "H807GPBD", "H806GPBH", "H808EPSD", "H802GPBD"], offset: (i) => (i < 4 ? i * 45 + 195 : i * 45 + 280) },
    { names: ["H801SCUN", "H802SCUN", "H801SCUK"], offset: (i) => i * 53 + 375 },
    { names: ["H801GICF", "H801GICG", "H901PILA"], offset: (i) => i * 55 + 315 },
    { names: ["H801X2CS"], offset: (i) => i * 110 + 315 },
    { names: ["H805GPFD", "H803GPFD", "H806GPFD"], offset: (i) => i * 37 + 105 },
    { names: ["H901MPLA"], offset: (i) => i * 55 + 320 },
    { names: ["H902CGHF"], offset: (i) => i * 37 + 75 }
  ];

  function boardHasImage(name) {
    return BOARD_IMAGE_NAMES.has(String(name || "").toUpperCase());
  }

  function boardImagePath(name) {
    return `${BOARD_IMAGE_DIR}/olt_${String(name || "").toUpperCase()}.png`;
  }

  function boardPortOffset(index, boardName) {
    const name = String(boardName || "").toUpperCase();
    const group = BOARD_PORT_GROUPS.find((item) => item.names.includes(name));
    let top = group ? group.offset(index) : index * 45 + 180;
    // 接口板（矮图）不再拉高到 719px：图片按自然高度显示后，说明文字纵坐标必须随尺寸
    // 等比缩放（× 自然高/719），否则 719 基准坐标会整体落到图片底部/图外（本次修复问题）。
    const small = BOARD_SMALL_IMAGE_SIZES[name];
    if (small) {
      // 缩放至自然高度坐标系；再仅对首个端口上移一段，拉开 P0 与相邻端口文字避免重叠。
      top = Math.round((top * small.height) / BOARD_BASE_HEIGHT);
      if (index === 0) {
        top -= BOARD_SMALL_FIRST_PORT_TOP_ADJUST;
      }
    }
    return top;
  }

  // boardStatusLabel 与老控制台的运行状态口径一致（Normal → [OK]，主备 → 激活/备用/失效）。
  function boardStatusLabel(status) {
    switch (String(status || "")) {
      case "Normal":
        return "[OK]";
      case "Active_normal":
        return "激活";
      case "Standby_normal":
        return "备用";
      case "Standby_failed":
        return "失效";
      default:
        return String(status || "--");
    }
  }

  // speedLabel / duplexLabel 沿用老控制台文案（auto → 自适应，auto_1000 → 1000 Mbps 等）。
  function speedLabel(speed) {
    switch (String(speed || "")) {
      case "auto":
      case "auto_auto":
        return "自适应";
      case "auto_10":
        return "10 Mbps";
      case "auto_100":
        return "100 Mbps";
      case "auto_1000":
        return "1000 Mbps";
      default:
        return String(speed || "");
    }
  }

  function duplexLabel(duplex) {
    switch (String(duplex || "")) {
      case "half":
      case "auto_half":
        return "半双工";
      case "full":
      case "auto_full":
        return "全双工";
      case "auto":
        return "自适应";
      default:
        return String(duplex || "");
    }
  }

  // formatSeconds 把毫秒耗时格式化为秒（1 位小数），用于「耗时 X.X 秒」。
  // delay 返回一个若干毫秒后 resolve 的 Promise（用于展示成功态后再关闭弹窗）。
  function delay(milliseconds) {
    return new Promise((resolve) => setTimeout(resolve, milliseconds));
  }

  function formatSeconds(milliseconds) {
    const value = Number(milliseconds);
    if (!Number.isFinite(value) || value < 0) return "--";
    return (value / 1000).toFixed(1);
  }

  // connData 组装（连接 / 单槽刷新共用；字段名与老壳一致）。
  function connectParams(item) {
    return JSON.stringify({
      Type: toInt(item.Type, 0), // 新口径：0 TELNET（默认）/ 1 SSH
      Username: item.Username || "",
      Password: item.Password || "",
      Port: String(item.Port || 23),
      IP: item.IP || ""
    });
  }

  const endpoints = {
    list: () => postForm("/itms/handleConnectOltUser/", { Act: 0 }),
    create: (item) => postForm("/itms/handleConnectOltUser/", { Act: 1, saveData: JSON.stringify(item) }),
    update: (item) => postForm("/itms/handleConnectOltUser/", { Act: 2, saveData: JSON.stringify(item) }),
    removeMany: (ids) =>
      // 批量删除：与 switch 台账同一套思路（一次提交选中集合）；后端 Act=3 兼容 Ids 逗号串与 JSON 数组。
      postForm("/itms/handleConnectOltUser/", { Act: 3, saveData: JSON.stringify({ Ids: ids.join(",") }) }),
    // readCached：读该 OLT 上次采集结果（Act=0，表优先、老文件兜底），不连设备。
    // 用于「页面加载 / 选中设备时直接从库表回填右侧」，让 OLT 信息出现即显示、刷新即保留。
    readCached: (item) =>
      postForm("/itms/handleOltConnect/", {
        Act: 0,
        connData: connectParams(item)
      }),
    // connect：工具条「连接」专用 —— 真正连设备重新采集（Act=1 强制刷新），并写库。
    // 说明：不要给本请求加 trace=1 —— trace 会把 data 包成 {result, timings}，
    // 而本页直接读 data 顶层（boards/version/...），加 trace 会破坏渲染。
    // 需要命令级分段耗时定位时，改用 curl 带 trace=1 直测（见 handler/日志口径）。
    connect: (item) =>
      postForm("/itms/handleOltConnect/", {
        Act: 1,
        refresh: "1",
        connData: connectParams(item)
      }),
    // refreshBoards 单槽/全部槽位刷新（老壳 getOltRefresh → /itms/handleBoardAndSlotInfo/）：
    // SlotId 为空 = 刷新所有板卡状态；refresh=1 表示用户点的是"刷新"，必须读设备而不是回缓存。
    refreshBoards: (item, slotId) =>
      postForm("/itms/handleBoardAndSlotInfo/", {
        refresh: "1",
        connData: connectParams(item),
        saveData: JSON.stringify({ SlotId: slotId || "" })
      }),
    // readConfig：读「配置信息」栏目的已保存配置（Act=0，表优先、老文件兜底），不连设备。
    // 用于选中设备/切到「配置信息」栏目时把上次采集的 `display current-configuration` 显示出来。
    readConfig: (item) =>
      postForm("/itms/handleCurrentConfiguration/", {
        Act: 0,
        connData: connectParams(item)
      }),
    // refreshConfig：强制连设备重新采集 `display current-configuration`（Act=1）写入缓存。
    refreshConfig: (item) =>
      postForm("/itms/handleCurrentConfiguration/", {
        Act: 1,
        connData: connectParams(item)
      }),
    // readTerminalUsers：读该 OLT 的终端登录用户名列表（Act=0，读配置文本解析）。
    readTerminalUsers: (item) =>
      postForm("/itms/handleOltTerminalUser/", {
        Act: 0,
        connData: connectParams(item)
      }),
    // addTerminalUser / changeTerminalPassword / removeTerminalUser：终端用户的写操作
    //（老接口 Act=1 / 2 / 3，saveData 为 {TerminalUserName, TerminalPassword}）。
    // 写操作涉及真机交互序列 + 同会话采集，设 20s 前端超时兜底。
    addTerminalUser: (item, tu) =>
      postForm("/itms/handleOltTerminalUser/", {
        Act: 1,
        connData: connectParams(item),
        saveData: JSON.stringify(tu)
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    changeTerminalPassword: (item, tu) =>
      postForm("/itms/handleOltTerminalUser/", {
        Act: 2,
        connData: connectParams(item),
        saveData: JSON.stringify(tu)
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeTerminalUser: (item, username) =>
      postForm("/itms/handleOltTerminalUser/", {
        Act: 3,
        connData: connectParams(item),
        saveData: JSON.stringify({ TerminalUserName: username })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    // readVLAN：读该 OLT 的 VLAN 功能模块四类信息（Act=0，读已保存配置解析，不连设备）。
    // 响应四类数组在各顶层字段：vlanifdata / vlandata / tranvlandata / routestaticdata。
    readVLAN: (item) =>
      postForm("/itms/handleVLANFunctionModule/", {
        Act: 0,
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 0 })
      }),
    // addVLAN / removeVLAN：VLAN 写操作（老接口 Act=1 新增 / Act=2 删除；saveData 为 HandleVLAN）。
    // 写操作涉及真机下发命令 + 同会话采集，设 20s 前端超时兜底。
    addVLAN: (item, data) =>
      postForm("/itms/handleVLANFunctionModule/", {
        Act: 1,
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 1, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeVLAN: (item, data) =>
      postForm("/itms/handleVLANFunctionModule/", {
        Act: 2,
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 2, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    // readDBA：读该 OLT 的 DBA 模板（Act=0，读已保存配置解析，不连设备）。
    // 响应顶层 data 为 DBAProfileInfo[]（Id/Name/Type/Fix/Assure/Max）。
    readDBA: (item) =>
      postForm("/itms/handleDBAProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 0 })
      }),
    // addDBA / removeDBA：DBA 模板写操作（老接口 Act=1 新增 / Act=2 删除；saveData 为 DBAProfile）。
    // 写操作涉及真机下发命令 + 同会话采集，设 20s 前端超时兜底，避免后端卡死时界面无限等待。
    addDBA: (item, data) =>
      postForm("/itms/handleDBAProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 1, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeDBA: (item, data) =>
      postForm("/itms/handleDBAProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 2, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    // readLineProfile：读该 OLT 的 ONT 线路模板（Act=0，读已保存配置解析，不连设备）。
    // 响应顶层 data 为 OntLineProfile[]（Id/Name/Type/GemMapping[]，含 T-CONT 子树）。
    readLineProfile: (item) =>
      postForm("/itms/handleLineProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 0 })
      }),
    // addLineProfile / removeLineProfile：线路模板写操作（老接口 Act=1 新增 / Act=2 删除；saveData 为 LineProfile）。
    // 写操作涉及真机下发命令 + 同会话采集，设 20s 前端超时兜底，避免后端卡死时界面无限等待。
    addLineProfile: (item, data) =>
      postForm("/itms/handleLineProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 1, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeLineProfile: (item, data) =>
      postForm("/itms/handleLineProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 2, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    // readSrvProfile：读该 OLT 的 ONT 业务模板（Act=0，读已保存配置解析，不连设备）。
    // 响应顶层 data 为 OntSrvProfileInfo[]（Id/Name/Type/Pots/Eths/SrvVlan[]）。
    readSrvProfile: (item) =>
      postForm("/itms/handleSrvProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 0 })
      }),
    // addSrvProfile / removeSrvProfile：业务模板写操作（老接口 Act=1 新增 / Act=2 删除；saveData 为 OntSrvProfile）。
    // 写操作涉及真机下发命令 + 同会话采集，设 20s 前端超时兜底。
    addSrvProfile: (item, data) =>
      postForm("/itms/handleSrvProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 1, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeSrvProfile: (item, data) =>
      postForm("/itms/handleSrvProfile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 2, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    // readTr069Profile：读该 OLT 的 TR069 管理模板（Act=0，读已保存配置解析，不连设备）。
    // 响应顶层 data 为 Tr069ProfileInfo[]（Id/Name/AcsUrl/AcsUserName，不回密码）。
    readTr069Profile: (item) =>
      postForm("/itms/handleTr069Profile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 0 })
      }),
    // addTr069Profile / removeTr069Profile：TR069 模板写操作（老接口 Act=1 新增 / Act=2 删除；saveData 为 Tr069Profile）。
    // 写操作涉及真机下发命令 + 同会话采集，设 20s 前端超时兜底。
    addTr069Profile: (item, data) =>
      postForm("/itms/handleTr069Profile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 1, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS }),
    removeTr069Profile: (item, data) =>
      postForm("/itms/handleTr069Profile/", {
        connData: connectParams(item),
        saveData: JSON.stringify({ Act: 2, ...data })
      }, { timeout: OLT_WRITE_TIMEOUT_MS })
  };

  function typeLabel(value) {
    // 显示口径：0/2 → TELNET，1/3 → SSH（0/1 是新口径，2/3 是老数据）。
    switch (toInt(value, 0)) {
      case 0:
      case 2:
        return "TELNET";
      case 1:
      case 3:
        return "SSH";
      default:
        return `类型 ${value}`;
    }
  }

  function deviceById(id) {
    return state.devices.find((item) => toInt(item.Id, 0) === id) || null;
  }

  function selectedDevice() {
    return deviceById(state.selectedId);
  }

  // ── 左侧栏：OLT 设备台账（与交换机设备台账同款紧凑列表）──

  function sidebarListHTML() {
    if (!state.devices.length) {
      return '<div class="device-list-empty">暂无 OLT 设备记录</div>';
    }
    return state.devices
      .map((item) => {
        const id = toInt(item.Id, 0);
        const connected = toInt(item.Automatic, 0) !== 0;
        // 列表列与交换机台账一致：勾选列 + 状态点 + 信息区 + 右侧类型标签。
        // 信息区按业务方要求显示 `<IP>:<Port>` 与备注（不再单独占一行显示端口）。
        return `<div class="device-list-row${state.selectedId === id ? " is-active" : ""}">
          <input class="device-list-row__checkbox" type="checkbox" data-olt-action="select-for-delete" data-id="${id}" aria-label="选择设备 ${escapeHTML(item.IP || "")}"${state.selectedIds.has(id) ? " checked" : ""}>
          <button type="button" class="device-list-item" data-olt-action="select" data-id="${id}" aria-pressed="${state.selectedId === id}" title="登录账号：${escapeHTML(item.Username || "--")}">
            <i class="device-list-item__status${connected ? " is-online" : ""}"></i>
            <span><strong>${escapeHTML(item.IP || "未配置 IP/域名")}:${escapeHTML(String(item.Port || "--"))}</strong><small>备注：${escapeHTML(item.Comment || "--")}</small></span>
            <em>${escapeHTML(typeLabel(item.Type))}</em>
          </button>
        </div>`;
      })
      .join("");
  }

  // renderSidebar 只重画左侧栏列表与工具条状态（工具条按钮在 app.js 的 layout() 里，ids 固定）。
  function renderSidebar() {
    const list = document.getElementById("olt-device-list");
    if (list) list.innerHTML = sidebarListHTML();
    const count = document.getElementById("olt-device-count");
    if (count) {
      const connected = state.devices.filter((item) => toInt(item.Automatic, 0) !== 0).length;
      count.textContent = `共 ${state.devices.length} 台 · 已连接 ${connected} 台`;
    }
    // 全选 / 删除 (n)：与交换机台账同一套口径（勾选集合驱动批量删除）。
    const checkedCount = state.devices.filter((item) => state.selectedIds.has(toInt(item.Id, 0))).length;
    const allChecked = state.devices.length > 0 && checkedCount === state.devices.length;
    const selectAll = document.getElementById("olt-select-all");
    if (selectAll) {
      selectAll.checked = allChecked;
      selectAll.indeterminate = checkedCount > 0 && !allChecked;
      selectAll.disabled = state.devices.length === 0;
      selectAll.setAttribute("aria-label", allChecked ? "取消全选 OLT 设备" : "全选 OLT 设备");
    }
    const hasSelection = Boolean(selectedDevice());
    const connectButton = document.getElementById("olt-connect");
    if (connectButton) connectButton.disabled = !hasSelection;
    const editButton = document.getElementById("olt-edit");
    if (editButton) editButton.disabled = !hasSelection;
    const deleteButton = document.getElementById("olt-delete");
    if (deleteButton) {
      deleteButton.disabled = checkedCount === 0;
      deleteButton.textContent = checkedCount ? `删除 (${checkedCount})` : "删除";
    }
  }

  // ── 右侧内容区：#module-view（OLT 设备详情 + 新增/修改弹窗）──

  function shell() {
    return `
      <div id="olt-detail"></div>
      <div class="device-form-modal" hidden id="olt-device-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-device-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-device-dialog-title">新增 OLT 设备</h3></header>
          <form id="olt-device-form" class="device-form-dialog__body">
            <label><span>IP 或域名：</span><input id="olt-field-ip" placeholder="例如 192.168.1.10 或 olt.example.com" required></label>
            <label><span>连接类型：</span><select id="olt-field-type">${TYPE_OPTIONS.map((item) => `<option value="${item.value}">${item.label}</option>`).join("")}</select></label>
            <label><span>登录账号：</span><input id="olt-field-username" maxlength="32" placeholder="大小写字母/数字/下划线，≤32" required></label>
            <label><span>登录密码：</span><span class="olt-pass-wrap"><input id="olt-field-password" type="password" maxlength="32" placeholder="字母+数字或特殊字符，≤32"><button type="button" class="olt-pass-toggle" data-olt-action="toggle-password" data-target="olt-field-password" aria-pressed="false" title="显示登录密码">👁</button></span></label>
            <label><span>端口：</span><input id="olt-field-port" value="23" inputmode="numeric" placeholder="1-65535（默认 23）"></label>
            <label><span>备注：</span><input id="olt-field-comment" placeholder="备注（可选）"></label>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-form">取消</button>
              <button type="submit" class="button button--primary">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- 终端用户新增/修改口令弹窗（仅当 state.terminalForm 非空时使用；复用 device-form-modal 外观）。 -->
      <div class="device-form-modal" hidden id="olt-terminal-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-terminal-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-terminal-dialog-title">新增终端用户</h3></header>
          <form id="olt-terminal-form" class="device-form-dialog__body">
            <label><span>终端用户名：</span><input id="olt-terminal-username" maxlength="32" placeholder="例如 root 或 inspector"></label>
            <label><span>口令：</span><span class="olt-pass-wrap"><input id="olt-terminal-password" type="password" maxlength="15" placeholder="6-15 位，含字母与数字，不含 ?"><button type="button" class="olt-pass-toggle" data-olt-action="toggle-terminal-password" data-target="olt-terminal-password" aria-pressed="false" title="显示口令">👁</button></span></label>
            <label><span>确认口令：</span><span class="olt-pass-wrap"><input id="olt-terminal-password2" type="password" maxlength="15" placeholder="再次输入口令"><button type="button" class="olt-pass-toggle" data-olt-action="toggle-terminal-password" data-target="olt-terminal-password2" aria-pressed="false" title="显示口令">👁</button></span></label>
            <div id="olt-terminal-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-terminal-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-terminal-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- 终端用户操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，全部在弹窗内提示，不用右上角 toast）。 -->
      <div class="device-form-modal" hidden id="olt-terminal-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-terminal-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-terminal-op-title">终端用户操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-terminal-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-terminal-op">关闭</button>
          </footer>
        </section>
      </div>
      <!-- VLAN 操作弹窗（新增三层接口 / 新增上行透传 / 新增 VLAN；复用 device-form-modal 外观）。 -->
      <div class="device-form-modal" hidden id="olt-vlan-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-vlan-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-vlan-dialog-title">新增 VLAN</h3></header>
          <form id="olt-vlan-form" class="device-form-dialog__body">
            <label id="olt-vlan-fsp-row" hidden><span>机框/槽位/端口(F/S/P)：</span><input id="olt-vlan-fsp" maxlength="32" placeholder="例如 0/3/0"></label>
            <label id="olt-vlan-vlantype-row" hidden><span>VLAN 类型：</span><select id="olt-vlan-vlantype"><option value="0">单层智能VLAN(Smart VLAN)</option><option value="1">基本/普通 QinQ</option><option value="2">灵活 QinQ</option></select></label>
            <label><span>VLAN ID：</span><input id="olt-vlan-vlanid" maxlength="64" placeholder="例如 100 或 100,200 或 100-200"></label>
            <label id="olt-vlan-ip-row" hidden><span>IP地址：</span><input id="olt-vlan-ip" maxlength="32" placeholder="例如 192.168.50.1"></label>
            <label id="olt-vlan-mask-row" hidden><span>子网掩码：</span><input id="olt-vlan-mask" maxlength="32" placeholder="例如 255.255.255.0"></label>
            <div id="olt-vlan-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-vlan-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-vlan-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- VLAN 操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，与「配置信息」栏目终端用户删除同一模式）。 -->
      <div class="device-form-modal" hidden id="olt-vlan-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-vlan-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-vlan-op-title">VLAN 操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-vlan-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-vlan-op">关闭</button>
          </footer>
        </section>
      </div>
      <!-- DBA 模板新增弹窗（模板ID/模板名/DBA类型 + 按类型显示的带宽字段；校验口径与后端 validateDBAWrite 一致）。 -->
      <div class="device-form-modal" hidden id="olt-dba-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-dba-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-dba-dialog-title">添加DBA模板</h3></header>
          <form id="olt-dba-form" class="device-form-dialog__body">
            <label><span>模板ID：</span><input id="olt-dba-id" inputmode="numeric" maxlength="3" placeholder="10-512，不能重复"></label>
            <label><span>模板名：</span><input id="olt-dba-name" maxlength="33" placeholder="1-33 个字符"></label>
            <label><span>DBA类型：</span><select id="olt-dba-type">${DBA_TYPE_OPTIONS.map((item, i) => `<option value="${item.value}"${i === 0 ? " selected" : ""}>${item.label}</option>`).join("")}</select></label>
            <label id="olt-dba-fix-row" hidden><span>固定带宽(kbps)：</span><input id="olt-dba-fix" inputmode="numeric" maxlength="8" placeholder="128-10000000"></label>
            <label id="olt-dba-assure-row" hidden><span>保证带宽(kbps)：</span><input id="olt-dba-assure" inputmode="numeric" maxlength="8" placeholder="128-10000000"></label>
            <label id="olt-dba-max-row" hidden><span>最大带宽(kbps)：</span><input id="olt-dba-max" inputmode="numeric" maxlength="8" placeholder="128-10000000"></label>
            <div id="olt-dba-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-dba-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-dba-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- DBA 操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，与 VLAN 删除同一模式）。 -->
      <div class="device-form-modal" hidden id="olt-dba-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-dba-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-dba-op-title">DBA 模板操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-dba-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-dba-op">关闭</button>
          </footer>
        </section>
      </div>
      <!-- 线路模板新增弹窗（模板ID 1-8192 不可重复 / 模板名 1-33 / PON类型 / 绑定DBA模板必选 / TR069 复选框 / VLAN 列表）。
           校验口径与后端 validateLineProfileWrite 一致。 -->
      <div class="device-form-modal" hidden id="olt-line-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-line-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-line-dialog-title">添加ONT线路模板</h3></header>
          <form id="olt-line-form" class="device-form-dialog__body">
            <label><span>模板ID：</span><input id="olt-line-id" inputmode="numeric" maxlength="4" placeholder="1-8192，不能重复"></label>
            <label><span>模板名：</span><input id="olt-line-name" maxlength="33" placeholder="1-33 个字符"></label>
            <label><span>PON类型：</span><select id="olt-line-pontype">
              <option value="gpon" selected>gpon</option>
              <option value="epon">epon</option>
            </select></label>
            <label><span>绑定DBA模板：</span><select id="olt-line-dba" aria-label="绑定DBA模板"></select></label>
            <div class="olt-line-field-hint" id="olt-line-dba-hint" hidden>该 OLT 暂无已读取的 DBA 模板，请先在「DBA模板」栏目读取。</div>
            <label><span>TR069：</span><span class="olt-line-check-wrap"><input type="checkbox" id="olt-line-tr069" checked><span class="olt-line-check-label">启用 TR069 管理</span></span></label>
            <div class="olt-line-field-hint">通过OLT下发TR069 WAN 业务及ACS参数时，需勾选。</div>
            <label class="olt-line-vlan-row"><span>VLAN列表：</span><textarea id="olt-line-vlan" rows="4" placeholder="多个VLAN用逗号分隔，每一种类型的业务占一行。例如：46,4015"></textarea></label>
            <div class="olt-line-vlan-hint">多个VLAN逗号分隔，每一种类型的业务占一行。示例：<br>
              <code>46,4015</code>（tr069）<br>
              <code>100</code>（上网）<br>
              <code>101</code>（语音）<br>
              <code>102</code>（IPTV）</div>
            <div id="olt-line-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-line-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-line-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- 线路模板操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，与 DBA 删除同一模式）。 -->
      <div class="device-form-modal" hidden id="olt-line-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-line-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-line-op-title">ONT线路模板操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-line-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-line-op">关闭</button>
          </footer>
        </section>
      </div>
      <!-- 业务模板新增弹窗（模板ID 1-8192 不可重复 / 模板名 1-33 / PON类型 / 电话口数量 0-32 / 网口数量 0-24 / 业务VLAN列表）。
           字段语义按修正口径：Pots=电话口、Eths=网口（老壳表单曾把两者错位）；校验口径与后端 validateSrvProfileWrite 一致。 -->
      <div class="device-form-modal" hidden id="olt-srv-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-srv-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-srv-dialog-title">添加ONT业务模板</h3></header>
          <form id="olt-srv-form" class="device-form-dialog__body">
            <label><span>模板ID：</span><input id="olt-srv-id" inputmode="numeric" maxlength="4" placeholder="1-8192，不能重复"></label>
            <label><span>模板名：</span><input id="olt-srv-name" maxlength="33" placeholder="1-33 个字符"></label>
            <label><span>PON类型：</span><select id="olt-srv-pontype">
              <option value="gpon" selected>gpon</option>
              <option value="epon">epon</option>
            </select></label>
            <label><span>电话口数量：</span><input id="olt-srv-pots" inputmode="numeric" maxlength="2" placeholder="0-32，默认 0"></label>
            <label><span>网口数量：</span><input id="olt-srv-eths" inputmode="numeric" maxlength="2" placeholder="0-24，默认 0"></label>
            <label class="olt-line-vlan-row"><span>业务VLAN列表：</span><textarea id="olt-srv-vlan" rows="5" placeholder="仅EPON或SFU的GPON需要添加，每个ETH一行，多个VLAN逗号分隔。例如：2,9&#10;4015&#10;11 iphost"></textarea></label>
            <div class="olt-line-vlan-hint">仅EPON或SFU的GPON需要添加，每个ETH一行，多个VLAN逗号分隔。<br>
              <code>2,9</code>（ETH1 透传 VLAN 2,9）<br>
              <code>4015</code>（ETH2 透传 4015）<br>
              <code>11 iphost</code>（IPHOST 透传 11）</div>
            <div id="olt-srv-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-srv-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-srv-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- 业务模板操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，与 DBA 删除同一模式）。 -->
      <div class="device-form-modal" hidden id="olt-srv-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-srv-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-srv-op-title">ONT业务模板操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-srv-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-srv-op">关闭</button>
          </footer>
        </section>
      </div>
      <!-- TR069 管理模板新增弹窗（模板ID 1-32 不可重复 / 模板名 1-32 / ACS地址 1-63 / ACS账号 1-50 / ACS密码 1-25）。
           读取时后端不回密码（与老契约一致）；校验口径与后端 validateTr069ProfileWrite 一致。 -->
      <div class="device-form-modal" hidden id="olt-tr069-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-tr069-dialog-title">
          <header class="device-form-dialog__header"><h3 id="olt-tr069-dialog-title">添加TR069管理模板</h3></header>
          <form id="olt-tr069-form" class="device-form-dialog__body">
            <label><span>模板ID：</span><input id="olt-tr069-id" inputmode="numeric" maxlength="2" placeholder="1-32，不能重复"></label>
            <label><span>模板名：</span><input id="olt-tr069-name" maxlength="32" placeholder="1-32 个字符"></label>
            <label><span>ACS地址：</span><input id="olt-tr069-url" maxlength="63" placeholder="1-63 个字符，如 http://acs.example.com"></label>
            <label><span>ACS账号：</span><input id="olt-tr069-user" maxlength="50" placeholder="1-50 个字符"></label>
            <label><span>ACS密码：</span><input id="olt-tr069-pass" type="password" maxlength="25" placeholder="1-25 个字符"></label>
            <div id="olt-tr069-form-status" class="olt-terminal-status" hidden role="status" aria-live="polite"></div>
            <footer class="device-form-dialog__actions">
              <button type="button" class="button" data-olt-action="close-tr069-form">取消</button>
              <button type="submit" class="button button--primary" id="olt-tr069-save">保存</button>
            </footer>
          </form>
        </section>
      </div>
      <!-- TR069 管理模板操作反馈弹窗（删除等耗时操作：确认后在此显示"进行中/成功/失败"，与 DBA 删除同一模式）。 -->
      <div class="device-form-modal" hidden id="olt-tr069-op-dialog">
        <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="olt-tr069-op-title">
          <header class="device-form-dialog__header"><h3 id="olt-tr069-op-title">TR069管理模板操作</h3></header>
          <div class="device-form-dialog__body olt-terminal-op-body">
            <div id="olt-tr069-op-status" class="olt-terminal-op" role="status" aria-live="polite"></div>
          </div>
          <footer class="device-form-dialog__actions">
            <button type="button" class="button" data-olt-action="close-tr069-op">关闭</button>
          </footer>
        </section>
      </div>`;
  }

  // portOverlayHTML 渲染单板图的端口叠加（PON 口与 GE/上行口口径对齐老控制台）：
  //   PON：`P<序号>` + 在线/离线状态点（行 1），换行 `<在线> ONT / <总数> 在线`（行 2）
  //   GE ：`P<序号> / <类型>` + 链路状态点（行 1），换行 `速率 · 双工`（行 2），再换行 `VLAN<NativeVLAN>`（行 3）
  function portOverlayHTML(board) {
    const cells = [];
    const ponPorts = Array.isArray(board.PonInfos) ? board.PonInfos : [];
    const gePorts = Array.isArray(board.GeInfos) ? board.GeInfos : [];
    // 接口板（矮图）：overlay 文字放在图片右侧（left = 板宽 + 4px，与高板 left:54px 口径一致），
    // 并加 --interface 标识；top 由 boardPortOffset 按自然高度/719 缩放。
    const boardName = String(board.Name || "").toUpperCase();
    const smallSize = BOARD_SMALL_IMAGE_SIZES[boardName];
    const portClass = smallSize ? "olt-board-port olt-board-port--interface" : "olt-board-port";
    const portLeft = smallSize ? `${smallSize.width + 4}px` : null;
    ponPorts.forEach((port) => {
      const index = toInt(port.Id, 0);
      const offline = String(port.PortStatus || "").toLowerCase() === "offline";
      const ponOnline = toInt(port.OntOnline, 0);
      const ponTotal = toInt(port.OntTotal, 0);
      cells.push(`<div class="${portClass}" style="top:${boardPortOffset(index, board.Name)}px${portLeft ? `; left:${portLeft}` : ""}">
        <span class="olt-port-line"><span class="olt-port-name">P${index}</span><i class="olt-port-dot${offline ? "" : " is-online"}" title="${escapeHTML(port.PortStatus || "未知")}"></i></span>
        <span class="olt-port-meta">${escapeHTML(String(ponOnline))} ONT / ${escapeHTML(String(ponTotal))} 在线</span>
      </div>`);
    });
    gePorts.forEach((port) => {
      const index = toInt(port.Id, 0);
      const offline = String(port.Link || "").toLowerCase() === "offline";
      const type = String(port.Type || "").toUpperCase();
      const linkMeta = [speedLabel(port.Speed), duplexLabel(port.Duplex)]
        .filter((text) => String(text || "").trim() !== "")
        .join(" · ");
      const vlan = port.NativeVLAN ? `VLAN${port.NativeVLAN}` : "";
      cells.push(`<div class="${portClass}" style="top:${boardPortOffset(index, board.Name)}px${portLeft ? `; left:${portLeft}` : ""}">
        <span class="olt-port-line"><span class="olt-port-name">P${index}${type ? ` / ${escapeHTML(type)}` : ""}</span><i class="olt-port-dot${offline ? "" : " is-online"}" title="${escapeHTML(port.Link || "未知")}"></i></span>
        ${linkMeta ? `<span class="olt-port-meta">${escapeHTML(linkMeta)}</span>` : ""}
        ${vlan ? `<span class="olt-port-meta">${escapeHTML(vlan)}</span>` : ""}
      </div>`);
    });
    return cells.join("");
  }

  // boardCellFigureHTML 渲染单板示意图（图 + 端口叠加 + 单槽「刷新」），
  //  置于转置表格的「示意图」行单元格内（与老控制台 oltStatus 的板位口径一致）。
  function boardCellFigureHTML(board, boardId, refreshing) {
    const name = String(board.Name || "");
    const isSmallBoard = BOARD_SMALL_IMAGE_NAMES.has(name.toUpperCase());
    const figure = boardHasImage(name)
      ? `<img class="olt-board-image${isSmallBoard ? " olt-board-image--interface" : ""}" src="${escapeHTML(boardImagePath(name))}" alt="${escapeHTML(name)} 单板示意图">`
      : `<div class="olt-board-image olt-board-image--missing"><span>${escapeHTML(name || "未知板卡")}</span><small>暂无该板卡的示意图</small></div>`;
    return `
      <div class="olt-cell-figure">
        <div class="olt-board-cell">
          ${figure}
          ${portOverlayHTML(board)}
        </div>
        <button type="button" class="button button--table" data-olt-action="refresh-slot" data-id="${boardId}" data-slot="${escapeHTML(String(board.Id ?? ""))}"${refreshing ? " disabled" : ""}>${refreshing ? "刷新中..." : "刷新"}</button>
      </div>`;
  }

  // formatBoardDesc 规整板卡描述文案：主控板不显示端口数量（业务方口径），
  //   如 `0口主控板(SCU)` → `主控板(SCU)`、`2口主控板(MPLA)` → `主控板(MPLA)`。
  //   非「N口主控板/主控盘」形态的描述原样保留（如 `8口GPON业务板` 不动）。
  function formatBoardDesc(desc) {
    const text = String(desc || "").trim();
    return text.replace(/^\d+口(?=主控(?:板|盘))/, "");
  }

  // boardsHTML 合并「机框示意图 + 板卡信息」为一张转置表格（对照老控制台 oltStatus 的板位展示）：
  //   行 = 插槽 / 型号 / 运行状态 / 描述 / 温度 / 示意图（业务方口径：板卡区不显示 PON 口在线/总数）
  //   列 = 每一块板卡（表头为槽位号，按槽位顺序左→右横向滚动；示意图行内嵌单板图 + 端口叠加 + 刷新）。
  function boardsHTML(result, id) {
    if (!Array.isArray(result.boards) || !result.boards.length) {
      return "";
    }
    const refreshingSlot = String(result.refreshingSlot || "");
    const boards = result.boards
      .map((board) => {
        const refreshing = refreshingSlot === String(board.Id ?? "");
        return {
          slot: escapeHTML(String(board.Id ?? "--")),
          model: escapeHTML(board.Name || "--"),
          status: escapeHTML(boardStatusLabel(board.Status)),
          desc: escapeHTML(formatBoardDesc(board.Desc) || "--"),
          temp: escapeHTML(board.Temperature || "--"),
          figure: boardCellFigureHTML(board, id, refreshing)
        };
      })
      // 按槽位号升序排（数字比较，避免 "10" < "3"）。
      .sort((a, b) => Number(a.slot) - Number(b.slot));
    const cell = (key) => boards.map((b) => `<td>${b[key]}</td>`).join("");
    const descCells = boards.map((b) => `<td class="olt-board-desc">${b.desc}</td>`).join("");
    const figureCells = boards.map((b) => `<td class="olt-figure-cell">${b.figure}</td>`).join("");
    return `
      <div class="device-monitor">
        <div class="device-vlan-toolbar">
          <div><span class="olt-board-title">机框板卡（示意图 + 状态）</span><span class="device-vlan-count">${escapeHTML(String(boards.length))} 块板卡 · 状态刷新时间 ${escapeHTML(result.slotidRefreshTime || "--")}${result.refreshElapsedMilliseconds ? ` · 上次刷新耗时 ${escapeHTML(formatSeconds(result.refreshElapsedMilliseconds))} 秒` : ""}</span></div>
          <div class="device-vlan-toolbar__actions">
            <button type="button" class="button${result.refreshingAll ? " button--busy" : ""}" data-olt-action="refresh-all" data-id="${id}"${result.refreshingAll ? " disabled" : ""}>${result.refreshingAll ? "刷新中..." : "刷新所有板卡状态"}</button>
          </div>
        </div>
        <div class="device-vlan-table-wrap">
          <table class="data-table olt-board-table">
            <tbody>
              <tr><th>插槽</th>${boards.map((b) => `<td class="olt-cell-slot">${b.slot}</td>`).join("")}</tr>
              <tr><th>型号</th>${cell("model")}</tr>
              <tr><th>运行状态</th>${cell("status")}</tr>
              <tr><th>描述</th>${descCells}</tr>
              <tr><th>温度</th>${cell("temp")}</tr>
              <tr><th>示意图</th>${figureCells}</tr>
            </tbody>
          </table>
        </div>
      </div>`;
  }

  function resultHTML(id, connecting) {
    if (connecting) {
      return `
        <div class="device-monitor device-monitor--loading">
          <div class="device-loading">
            <span class="device-loading__spinner" aria-hidden="true"></span>
            <span class="device-loading__text">正在连接 OLT 并读取版本与板卡信息...</span>
            <p>只发送 display 类只读命令，不会修改设备配置。</p>
          </div>
        </div>`;
    }
    const result = state.connections[id];
    if (!result) {
      return '<div class="empty-state">点击左侧「OLT 设备台账」工具条的「连接」，读取该 OLT 的版本、温度与板卡信息（仅只读命令）。</div>';
    }
    if (result.error) {
      return `
        <div class="device-monitor" role="alert">
          <div class="device-monitor__header"><span class="olt-connect-head">连接失败</span></div>
          <p>${escapeHTML(result.error)}</p>
        </div>`;
    }
    // OLT 信息逐行文本展示（业务方要求：不要卡片，用「标签：值」换行）。
    // 顺序：机框 → 版本/补丁 → OLT运行时间 → 状态刷新时间 → 在线 ONT/板卡 → 板卡。
    // 连接时间 / 耗时已显示在面板头部，不在此重复。
    const oltLines = [
      ["机框", `${result.frame || "--"}${result.frameType ? `（${result.frameType}）` : ""}`],
      ["版本 / 补丁", `${result.version || "--"}${result.patch ? ` / ${result.patch}` : ""}`],
      ["OLT运行时间", result.uptime || "--"],
      ["状态刷新时间", result.slotidRefreshTime || "--"],
      ["在线 ONT / 板卡", `${result.ontOnline ?? 0} / ${result.ontTotal ?? 0}`],
      // FrameTotal 是机框板卡行数（= 机框槽位数）、FrameCount 是其中有效板卡数（见 parse_device_detail.go），
      // 老控制台用它们生成 FrameDesc 文案，这里如实展示"已插 N 块（机框 M 槽）"。
      ["板卡", `${(result.boards || []).length} 块${result.frameTotal ? `（机框 ${result.frameTotal} 槽）` : ""}`]
    ];
    return `
      <div class="device-monitor">
        <div class="device-monitor__header">
          <span class="olt-connect-head">连接成功（${escapeHTML(result.time)}）</span>
          <span>耗时 <b class="olt-elapsed">${escapeHTML(formatSeconds(result.elapsedMilliseconds))}</b> 秒 · ${escapeHTML(result.ip)}:${escapeHTML(result.port)}</span>
        </div>
        <div class="olt-detail-lines">
          ${oltLines.map(([label, value]) => `<div class="olt-detail-line"><span>${escapeHTML(label)}：</span><span class="olt-detail-value">${escapeHTML(value)}</span></div>`).join("")}
        </div>
        ${result.frameDesc ? `<p class="olt-frame-desc">机框说明：${escapeHTML(result.frameDesc)}</p>` : ""}
      </div>
      ${boardsHTML(result, id)}`;
  }

  // configTabBody 渲染「配置信息」栏目（Act=0 读到已保存配置即显示；未保存时给引导 + 刷新按钮）。
  // 配置文本与状态/板卡互不依赖，独立于 connections 缓存（见 state.configs）。
  // 布局：先「OLT 登录用户」，再「当前配置信息」（后者整体包一层 device-monitor）。
  function configTabBody(id, device) {
    const entry = state.configs[id];
    const loading = state.configsLoadingId === id;
    const reading = state.configsReadId === id;
    // 「刷新配置」= Act=1 强制连设备重新采集 `display current-configuration`。
    // 标题后不再展示 IP，仅保留刷新按钮，并整体套一层 device-monitor。
    const header = `
      <div class="device-monitor">
        <div class="device-vlan-toolbar">
          <div><span class="olt-board-title">当前配置信息</span>
            <span class="device-vlan-count">${entry?.refreshElapsedMilliseconds ? `上次采集耗时 ${escapeHTML(formatSeconds(entry.refreshElapsedMilliseconds))} 秒` : ""}</span>
          </div>
          <div class="device-vlan-toolbar__actions">
            <button type="button" class="button${loading ? " button--busy" : ""}" data-olt-action="refresh-config" data-id="${id}"${loading ? " disabled" : ""}>${loading ? "采集中..." : "刷新配置"}</button>
          </div>
        </div>`;
    // 配置全文块（采集中/读中/已采集/错误/空 五种状态）。外层由 device-monitor 统一包裹，不再各自嵌套。
    let configBody;
    if (loading) {
      configBody = `<div class="device-loading">
        <span class="device-loading__spinner" aria-hidden="true"></span>
        <span class="device-loading__text">正在采集 OLT 当前配置（display current-configuration，只读命令）...</span>
      </div>`;
    } else if (reading) {
      configBody = `<div class="device-loading">
        <span class="device-loading__spinner" aria-hidden="true"></span>
        <span class="device-loading__text">正在读取已保存的配置信息...</span>
      </div>`;
    } else if (entry && entry.text) {
      configBody = `<pre class="olt-config-text">${escapeHTML(entry.text)}</pre>`;
    } else if (entry && entry.error) {
      configBody = `<div role="alert"><p>${escapeHTML(entry.error)}</p></div>`;
    } else {
      configBody = `<div class="empty-state">尚未读取该 OLT 的配置信息。点右上角「刷新配置」连设备采集 <code>display current-configuration</code>（仅只读命令），或先在左侧工具条「连接」后再次进入本栏目自动读到上次采集结果。</div>`;
    }
    // 「OLT 登录用户」在前，「当前配置信息」在后，两段位置互换。
    return `${terminalUsersHTML(id, device)}
      ${header}
      ${configBody}
      </div>`;
  }

  // terminalUsersHTML 渲染「配置信息」栏目下的「OLT 登录用户」小节：
  // 只读用户名列表 + 新增 / 修改口令 / 删除（二次确认）。读失败给错误占位，不假成功。
  function terminalUsersHTML(id, device) {
    const users = Array.isArray(state.terminalUsers[id]) ? state.terminalUsers[id] : null;
    const loading = state.terminalUsersLoadingId === id;
    let listBody;
    if (loading) {
      listBody = `<div class="device-loading">
        <span class="device-loading__spinner" aria-hidden="true"></span>
        <span class="device-loading__text">正在读取该 OLT 的终端登录用户...</span>
      </div>`;
    } else if (users === null) {
      listBody = `<div class="empty-state">尚未读取终端登录用户。进入本栏目时自动读取该 OLT 配置中的 <code>terminal user name</code> 账号。</div>`;
    } else if (users.length === 0) {
      listBody = `<div class="empty-state">该 OLT 未配置终端登录用户（未发现 <code>terminal user name</code> 行）。</div>`;
    } else {
      listBody = `<ul class="olt-user-list">
        ${users.map((username) => `
          <li class="olt-user-row">
            <span class="olt-user-name" title="${escapeHTML(username)}">${escapeHTML(username)}</span>
            <span class="olt-user-actions">
              <button type="button" class="button button--small" data-olt-action="change-terminal-password" data-id="${id}" data-username="${escapeHTML(username)}">修改口令</button>
              <button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-terminal-user" data-id="${id}" data-username="${escapeHTML(username)}">删除</button>
            </span>
          </li>`).join("")}
      </ul>`;
    }
    return `
      <div class="olt-subsection">
        <div class="device-vlan-toolbar">
          <div><span class="olt-board-title">OLT 登录用户</span>
            <span class="device-vlan-count">${users ? `${users.length} 个` : "—"}</span>
            <button type="button" class="button" data-olt-action="add-terminal-user" data-id="${id}">新增</button>
          </div>
          <div class="device-vlan-toolbar__actions"></div>
        </div>
        ${listBody}
      </div>`;
  }

  // pendingTabHTML 是尚未迁入栏目的占位内容（写明对应老接口，便于第二批逐条对账）。
  function pendingTabHTML(tab) {
    return `
      <div class="empty-state">「${escapeHTML(tab.label)}」栏目待迁入（第二批）：${escapeHTML(tab.pending || "")}。<br>
      迁入前不展示数据，也不做假成功；当前先提供栏目位置。</div>`;
  }

  // vlanTabBody 渲染「VLAN」栏目：三层 VLAN 接口 / 上行透传 / VLAN 列表 三张卡片。
  // 数据来自 handleVLANFunctionModule Act=0 顶层四类数组（vlanifdata/vlandata/tranvlandata/routestaticdata）。
  function vlanTabBody(id, device) {
    const entry = state.vlan[id];
    const loading = state.vlanLoadingId === id;
    if (loading) {
      return `
        <div class="device-monitor"><div class="device-loading">
          <span class="device-loading__spinner" aria-hidden="true"></span>
          <span class="device-loading__text">正在读取该 OLT 的 VLAN 功能（三层接口 / 上行透传 / VLAN 列表）...</span>
        </div></div>`;
    }
    if (!entry || (!entry.read && !entry.error)) {
      return `
        <div class="device-monitor"><div class="empty-state">尚未读取该 OLT 的 VLAN 信息。进入本栏目自动读取（Act=0 读已保存配置解析），未缓存时连设备刷新。</div></div>`;
    }
    if (entry.error) {
      return `<div class="device-monitor" role="alert"><p>${escapeHTML(entry.error)}</p></div>`;
    }
    const vlanifList = Array.isArray(entry.vlanif) ? entry.vlanif : [];
    const vlanList = Array.isArray(entry.vlan) ? entry.vlan : [];
    const tranList = Array.isArray(entry.tran) ? entry.tran : [];
    const routes = Array.isArray(entry.routes) ? entry.routes : [];

    // card 渲染一张子区块卡片：标题、数量、正文（表格/空态）与可选工具条动作。
    const card = (title, { count, html, actions } = {}) => `
      <div class="olt-subsection">
        <div class="device-vlan-toolbar"><div><span class="olt-board-title">${escapeHTML(title)}</span>${count ? `<span class="device-vlan-count">${escapeHTML(count)}</span>` : ""}</div><div class="device-vlan-toolbar__actions">${actions || ""}</div></div>
        ${html}
      </div>`;

    // vlanTable 生成一张纵向 data-table（表头 + 数据行），用于 VLAN 四类信息。
    // columns: [{ label }]; rows: 已组装好的 <tr> 内 <td> 片段数组。
    const vlanTable = (columns, rows) => `
      <div class="device-vlan-table-wrap">
        <table class="data-table olt-vlan-table">
          <thead><tr>${columns.map((c) => `<th>${escapeHTML(c.label)}</th>`).join("")}</tr></thead>
          <tbody>${rows.join("") || `<tr><td class="olt-vlan-empty" colspan="${columns.length}">暂无数据</td></tr>`}</tbody>
        </table>
      </div>`;

    // ① 三层 VLAN 接口：VLAN ID / IP地址 / 子网掩码 / 操作
    // 每个数据片段用 <tr>...</tr> 包裹，保证 vlanTable 的 rows.join 一行一条（否则平铺 <td> 会被浏览器并到同一行）。
    const vlanifRows = vlanifList.map((item) => `
      <tr>
      <td class="olt-vlan-mono">${escapeHTML(String(item.VLANID ?? ""))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.IPAddress ?? ""))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.SubnetMask ?? ""))}</td>
      <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-vlan" data-id="${id}" data-vlan-kind="vlanif" data-vlanid="${escapeHTML(String(item.VLANID ?? ""))}">删除</button></td>
      </tr>`);
    const vlanifCard = {
      count: `${vlanifList.length} 个三层接口`,
      html: vlanifList.length
        ? vlanTable(
            [{ label: "VLAN ID" }, { label: "IP地址" }, { label: "子网掩码" }, { label: "操作" }],
            vlanifRows
          )
        : `<div class="empty-state">未发现三层 VLAN 接口（<code>interface vlanif</code>）。</div>`,
      actions: `<button type="button" class="button button--small" data-olt-action="add-vlan" data-id="${id}" data-vlan-kind="3if">新增三层接口</button>`
    };

    // ② 上行 VLAN 透传：端口 / 透传 VLAN / 操作
    const tranRows = tranList.map((item) => `
      <tr>
      <td class="olt-vlan-mono">${escapeHTML(String(item.szPortInfo ?? ""))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.szVlan ?? ""))}</td>
      <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-vlan" data-id="${id}" data-vlan-kind="tran" data-vlanid="${escapeHTML(String(item.szVlan ?? ""))}" data-fsp="${escapeHTML(String(item.szPortInfo ?? ""))}">删除</button></td>
      </tr>`);
    const tranCard = {
      count: `${tranList.length} 条`,
      html: tranList.length
        ? vlanTable(
            [{ label: "端口" }, { label: "透传 VLAN" }, { label: "操作" }],
            tranRows
          )
        : `<div class="empty-state">未发现上行 VLAN 透传（<code>port vlan</code>）。</div>`,
      actions: `<button type="button" class="button button--small" data-olt-action="add-vlan" data-id="${id}" data-vlan-kind="tran">新增上行透传</button>`
    };

    // ③ VLAN / 二层互通（smart / q-in-q / stacking + 组播业务）。
    // 后端已按类型分组、每类合并成一个逗号串（各 szName 各自成一行）。
    // 删除操作仅对「单层智能VLAN(Smart VLAN)」开放（真机 undo vlan 只对 smart 直接生效），
    // 其他类型（q-in-q / stacking / 组播）不渲染删除按钮。
    const vlanRemovable = (name) => String(name ?? "") === "单层智能VLAN(Smart VLAN)";
    const vlanRows = vlanList.map((item) => `
      <tr>
      <td>${escapeHTML(String(item.szName ?? ""))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.szVlan ?? ""))}</td>
      <td class="olt-vlan-actions">${vlanRemovable(item.szName) ? `<button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-vlan" data-id="${id}" data-vlan-kind="vlan" data-vlanid="${escapeHTML(String(item.szVlan ?? ""))}">删除</button>` : ""}</td>
      </tr>`);
    const vlanCard = {
      count: `${vlanList.length} 类`,
      html: vlanList.length
        ? vlanTable(
            [{ label: "类型" }, { label: "VLAN" }, { label: "操作" }],
            vlanRows
          )
        : `<div class="empty-state">未发现 VLAN（<code>vlan ... smart / q-in-q / stacking</code>）。</div>`,
      actions: `<button type="button" class="button button--small" data-olt-action="add-vlan" data-id="${id}" data-vlan-kind="vlan">新增 VLAN</button>`
    };

    // ④ 静态路由（只读展示，本栏目不支持增删）。多值一行一条，必须用 <tr> 包裹每行。
    const routeRows = routes.map((r) => `
      <tr>
      <td class="olt-vlan-mono">${escapeHTML(String(r.szNetwork ?? ""))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(r.szGateway ?? ""))}</td>
      </tr>`);
    const routeCard = {
      count: `${routes.length} 条`,
      html: routes.length
        ? vlanTable([{ label: "目的网络" }, { label: "下一跳" }], routeRows)
        : `<div class="empty-state">未发现静态路由（<code>ip route-static</code>）。</div>`
    };

    return `
      ${card("三层 VLAN 接口", vlanifCard)}
      ${card("上行 VLAN 透传", tranCard)}
      ${card("VLAN 列表", vlanCard)}
      ${card("静态路由", routeCard)}`;
  }

  // dbaTabBody 渲染「DBA/线路模板」栏目的 DBA 部分：一张 DBA 模板表格 + 「添加DBA模板」按钮。
  // 数据来自 handleDBAProfile Act=0 顶层 data 数组（DBAProfileInfo[]：Id/Name/Type/Fix/Assure/Max）。
  // 线路模板（handleLineProfile）仍待迁入，在 DBA 卡片底部保留一条占位说明（不假成功）。
  function dbaTabBody(id, device) {
    const entry = state.dba[id];
    const loading = state.dbaLoadingId === id;
    if (loading) {
      return `
        <div class="device-monitor"><div class="device-loading">
          <span class="device-loading__spinner" aria-hidden="true"></span>
          <span class="device-loading__text">正在读取该 OLT 的 DBA 模板（动态带宽分配）...</span>
        </div></div>`;
    }
    if (!entry || (!entry.read && !entry.error)) {
      return `
        <div class="device-monitor"><div class="empty-state">尚未读取该 OLT 的 DBA 模板。进入本栏目自动读取（Act=0 读已保存配置解析），未缓存时连设备刷新。</div></div>`;
    }
    if (entry.error) {
      return `<div class="device-monitor" role="alert"><p>${escapeHTML(entry.error)}</p></div>`;
    }
    const profiles = Array.isArray(entry.profiles) ? entry.profiles : [];
    // dbaTypeLabel 把后端 Type 字段（type1..type5）映射为老控制台文案。
    const dbaTypeLabel = (type) => {
      const option = DBA_TYPE_OPTIONS.find((item) => item.value === String(type || ""));
      return option ? option.label : escapeHTML(String(type || ""));
    };
    // 带宽展示：某类型不用的字段显示 0（后端 parse 缺省为 0），表格直接展示数值。
    const profileRows = profiles.map((item) => `
      <tr>
      <td class="olt-vlan-mono">${escapeHTML(String(item.Id ?? ""))}</td>
      <td>${escapeHTML(String(item.Name ?? ""))}</td>
      <td><span class="olt-dba-type">${dbaTypeLabel(item.Type)}</span></td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.Fix ?? 0))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.Assure ?? 0))}</td>
      <td class="olt-vlan-mono">${escapeHTML(String(item.Max ?? 0))}</td>
      <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-dba" data-id="${id}" data-dba-id="${escapeHTML(String(item.Id ?? ""))}" data-dba-name="${escapeHTML(String(item.Name ?? ""))}">删除</button></td>
      </tr>`);
    return `
      <div class="olt-subsection">
        <div class="device-vlan-toolbar">
          <div><span class="olt-board-title">DBA 模板</span>
            <span class="device-vlan-count">${profiles.length} 个模板 · 用于控制每个 ONU 上行时的带宽（动态带宽分配）</span>
          </div>
          <div class="device-vlan-toolbar__actions">
            <button type="button" class="button button--small" data-olt-action="add-dba" data-id="${id}">添加DBA模板</button>
          </div>
        </div>
        ${profiles.length
          ? `<div class="device-vlan-table-wrap">
              <table class="data-table olt-vlan-table">
                <thead><tr><th>模板ID</th><th>模板名</th><th>DBA类型</th><th>固定带宽(kbps)</th><th>保证带宽(kbps)</th><th>最大带宽(kbps)</th><th>操作</th></tr></thead>
                <tbody>${profileRows.join("")}</tbody>
              </table>
            </div>`
          : `<div class="empty-state">未发现 DBA 模板（<code>[global-config]</code> 段无 <code>dba-profile add</code> 行）。</div>`}
      </div>`;
  }

  // lineTabBody 渲染「DBA/线路模板」栏目的线路模板（ONT line profile）部分：
  // 一张按模板分组的表格 + 「添加ONT线路模板」按钮。数据来自 handleLineProfile Act=0 顶层 data 数组
  // （OntLineProfile[]：Id/Name/Type + GemMapping[]，GemMapping 内含 T-CONT/DBA 绑定与 GEM Port/VLAN）。
  // Type 为 gpon/epon，展示为浅色小标签；GEM/T-CONT 层级为表格内的子区块（对齐老控制台 chunk-dba_lineTem.js）。
  function lineTabBody(id, device) {
    const entry = state.line[id];
    const loading = state.lineLoadingId === id;
    if (loading) {
      return `
        <div class="device-monitor"><div class="device-loading">
          <span class="device-loading__spinner" aria-hidden="true"></span>
          <span class="device-loading__text">正在读取该 OLT 的 ONT 线路模板...</span>
        </div></div>`;
    }
    if (!entry || (!entry.read && !entry.error)) {
      return `
        <div class="device-monitor"><div class="empty-state">尚未读取该 OLT 的 ONT 线路模板。进入本栏目自动读取（Act=0 读已保存配置解析），未缓存时连设备刷新。</div></div>`;
    }
    if (entry.error) {
      return `<div class="device-monitor" role="alert"><p>${escapeHTML(entry.error)}</p></div>`;
    }
    const profiles = Array.isArray(entry.profiles) ? entry.profiles : [];
    // lineProfileRows 把每条模板渲染成「一行」，其中「映射关系」列内嵌一张子表格（对齐老控制台
    // chunk-dba_lineTem.js 的层级）：外层行 = 模板ID + 模板名 + PON类型 + [映射关系子表] + 操作；
    // 子表把一条模板的 GemMapping（T-CONT / DBA模版 绑定 + 其下挂 GEM 端口与映射）摊平成数据行，
    // 列对齐上层子层表头：T-CONT / DBA模版 / GEM Port / GEM 映射ID / VLAN。
    const profileRows = profiles.map((profile) => {
      const profileId = escapeHTML(String(profile.Id ?? ""));
      const name = escapeHTML(String(profile.Name ?? ""));
      const type = escapeHTML(String(profile.Type ?? ""));
      const mappings = Array.isArray(profile.GemMapping) ? profile.GemMapping : [];
      // lineMapRows：每个 GemMapping 下的每个 GemInfo 各一行（重复其所属 T-CONT/DBA）；
      // 某个 T-CONT 名下没有 GEM 时也保留一行占位（T-CONT/DBA 两列有值，其余留空）。
      const lineMapRows = mappings.flatMap((mapping) => {
        const tcontId = escapeHTML(String(mapping.TcontId ?? ""));
        const dba = escapeHTML(String(mapping.DbaProfileId ?? ""));
        const gems = Array.isArray(mapping.GemInfo) ? mapping.GemInfo : [];
        if (!gems.length) {
          return `
            <tr>
              <td class="olt-vlan-mono">${tcontId}</td>
              <td class="olt-vlan-mono">${dba}</td>
              <td class="olt-vlan-mono"></td>
              <td class="olt-vlan-mono"></td>
              <td class="olt-vlan-mono"></td>
            </tr>`;
        }
        return gems.map((gem) => `
          <tr>
            <td class="olt-vlan-mono">${tcontId}</td>
            <td class="olt-vlan-mono">${dba}</td>
            <td class="olt-vlan-mono">${escapeHTML(String(gem.GemPort ?? ""))}</td>
            <td class="olt-vlan-mono">${escapeHTML(String(gem.GemToId ?? ""))}</td>
            <td class="olt-vlan-mono">${escapeHTML(String(gem.GemVlan ?? ""))}</td>
          </tr>`);
      }).join("");
      // 子表始终渲染「表头 + 数据行」；没有任何映射时只保留表头。
      const mapCell = mappings.length
        ? `
          <table class="olt-line-map-table">
            <thead><tr>
              <th>T-CONT</th><th>DBA模版</th><th>GEM Port</th><th>GEM 映射ID</th><th>VLAN</th>
            </tr></thead>
            <tbody>${lineMapRows}</tbody>
          </table>`
        : `<div class="olt-line-map-empty">无 T-CONT / GEM 映射</div>`;
      return `
        <tr class="olt-line-profile-row">
          <td class="olt-vlan-mono">${profileId}</td>
          <td>${name}</td>
          <td><span class="olt-dba-type">${type}</span></td>
          <td class="olt-line-map-cell">${mapCell}</td>
          <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-line" data-id="${id}" data-line-id="${profileId}" data-line-name="${name}" data-line-type="${type}">删除</button></td>
        </tr>`;
    });
    return `
      <div class="olt-subsection">
        <div class="device-vlan-toolbar">
          <div><span class="olt-board-title">ONT 线路模板</span>
            <span class="device-vlan-count">${profiles.length} 个模板 · 定义 ONT 的 T-CONT / GEM 端口与映射（VLAN 通道）</span>
          </div>
          <div class="device-vlan-toolbar__actions">
            <button type="button" class="button button--small" data-olt-action="add-line" data-id="${id}">添加ONT线路模板</button>
          </div>
        </div>
        ${profiles.length
          ? `<div class="device-vlan-table-wrap">
              <table class="data-table olt-vlan-table">
                <thead><tr><th>模板ID</th><th>模板名</th><th>PON类型</th><th>映射关系</th><th>操作</th></tr></thead>
                <tbody>${profileRows.join("")}</tbody>
              </table>
            </div>`
          : `<div class="empty-state">未发现 ONT 线路模板（<code>[global-config]</code> 段无 <code>ont-lineprofile</code> 行）。</div>`}
      </div>`;
  }

  // srvTabBody 渲染「业务/TR069模板」栏目的两部分：
  //  1. ONT 业务模板（ont-srvprofile）：一张按模板分组的表格 + 「添加ONT业务模板」按钮，
  //     数据来自 handleSrvProfile Act=0 顶层 data 数组（OntSrvProfileInfo[]：
  //     Id/Name/Type/Pots/Eths + SrvVlan[]，SrvVlan 为「网口 → 业务VLAN」映射）；
  //  2. TR069 管理模板（ont tr069-server-profile）：一张表格 + 「添加TR069管理模板」按钮，
  //     数据来自 handleTr069Profile Act=0 顶层 data 数组（Tr069ProfileInfo[]：
  //     Id/Name/AcsUrl/AcsUserName，不回密码）。
  // 字段语义按修正口径：Pots=电话口数量、Eths=网口数量（老壳表单曾错位，这里展示对）。
  function srvTabBody(id, device) {
    const srvEntry = state.srv[id];
    const srvLoading = state.srvLoadingId === id;
    const tr069Entry = state.tr069[id];
    const tr069Loading = state.tr069LoadingId === id;
    const renderSrv = () => {
      if (srvLoading) {
        return `
          <div class="device-monitor"><div class="device-loading">
            <span class="device-loading__spinner" aria-hidden="true"></span>
            <span class="device-loading__text">正在读取该 OLT 的 ONT 业务模板...</span>
          </div></div>`;
      }
      if (!srvEntry || (!srvEntry.read && !srvEntry.error)) {
        return `
          <div class="device-monitor"><div class="empty-state">尚未读取该 OLT 的 ONT 业务模板。进入本栏目自动读取（Act=0 读已保存配置解析），未缓存时连设备刷新。</div></div>`;
      }
      if (srvEntry.error) {
        return `<div class="device-monitor" role="alert"><p>${escapeHTML(srvEntry.error)}</p></div>`;
      }
      const profiles = Array.isArray(srvEntry.profiles) ? srvEntry.profiles : [];
      const profileRows = profiles.map((profile) => {
        const profileId = escapeHTML(String(profile.Id ?? ""));
        const name = escapeHTML(String(profile.Name ?? ""));
        const type = escapeHTML(String(profile.Type ?? ""));
        // 电话口/网口为空或为 "adaptive"（老壳表单把空口数量提交为 adaptive）时，列表显示「自适应」。
        const rawPots = String(profile.Pots ?? "");
        const rawEths = String(profile.Eths ?? "");
        const pots = (rawPots === "" || rawPots.toLowerCase() === "adaptive") ? "自适应" : escapeHTML(rawPots);
        const eths = (rawEths === "" || rawEths.toLowerCase() === "adaptive") ? "自适应" : escapeHTML(rawEths);
        const vlanRows = (Array.isArray(profile.SrvVlan) ? profile.SrvVlan : []).map((sv) => `
          <tr>
            <td class="olt-vlan-mono">${escapeHTML(String(sv.Eth ?? ""))}</td>
            <td class="olt-vlan-mono">${escapeHTML(String(sv.Vlan ?? ""))}</td>
          </tr>`).join("");
        // 业务VLAN 为空时列表直接留空（不显示占位文案）。
        const vlanCell = (Array.isArray(profile.SrvVlan) ? profile.SrvVlan : []).length
          ? `
            <table class="olt-line-map-table">
              <thead><tr><th>网口</th><th>业务VLAN</th></tr></thead>
              <tbody>${vlanRows}</tbody>
            </table>`
          : ``;
        return `
          <tr class="olt-line-profile-row">
            <td class="olt-vlan-mono">${profileId}</td>
            <td>${name}</td>
            <td><span class="olt-dba-type">${type}</span></td>
            <td class="olt-vlan-mono">${pots}</td>
            <td class="olt-vlan-mono">${eths}</td>
            <td class="olt-line-map-cell">${vlanCell}</td>
            <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-srv" data-id="${id}" data-srv-id="${profileId}" data-srv-name="${name}" data-srv-type="${type}">删除</button></td>
          </tr>`;
      });
      return `
        <div class="olt-subsection">
          <div class="device-vlan-toolbar">
            <div><span class="olt-board-title">ONT 业务模板</span>
              <span class="device-vlan-count">${profiles.length} 个模板 · 定义 ONT 的电话口/网口数量与业务VLAN 映射</span>
            </div>
            <div class="device-vlan-toolbar__actions">
              <button type="button" class="button button--small" data-olt-action="add-srv" data-id="${id}">添加ONT业务模板</button>
            </div>
          </div>
          ${profiles.length
            ? `<div class="device-vlan-table-wrap">
                <table class="data-table olt-vlan-table">
                  <thead><tr><th>模板ID</th><th>模板名</th><th>PON类型</th><th>电话口</th><th>网口</th><th>业务VLAN</th><th>操作</th></tr></thead>
                  <tbody>${profileRows.join("")}</tbody>
                </table>
              </div>`
            : `<div class="empty-state">未发现 ONT 业务模板（<code>[global-config]</code> 段无 <code>ont-srvprofile</code> 行）。</div>`}
        </div>`;
    };
    const renderTr069 = () => {
      if (tr069Loading) {
        return `
          <div class="device-monitor"><div class="device-loading">
            <span class="device-loading__spinner" aria-hidden="true"></span>
            <span class="device-loading__text">正在读取该 OLT 的 TR069 管理模板...</span>
          </div></div>`;
      }
      if (!tr069Entry || (!tr069Entry.read && !tr069Entry.error)) {
        return `
          <div class="device-monitor"><div class="empty-state">尚未读取该 OLT 的 TR069 管理模板。进入本栏目自动读取（Act=0 读已保存配置解析），未缓存时连设备刷新。</div></div>`;
      }
      if (tr069Entry.error) {
        return `<div class="device-monitor" role="alert"><p>${escapeHTML(tr069Entry.error)}</p></div>`;
      }
      const profiles = Array.isArray(tr069Entry.profiles) ? tr069Entry.profiles : [];
      const profileRows = profiles.map((item) => `
        <tr>
          <td class="olt-vlan-mono">${escapeHTML(String(item.Id ?? ""))}</td>
          <td>${escapeHTML(String(item.Name ?? ""))}</td>
          <td class="olt-vlan-mono">${escapeHTML(String(item.AcsUrl ?? ""))}</td>
          <td>${escapeHTML(String(item.AcsUserName ?? ""))}</td>
          <td class="olt-vlan-actions"><button type="button" class="button button--small button--danger-ghost" data-olt-action="remove-tr069" data-id="${id}" data-tr069-id="${escapeHTML(String(item.Id ?? ""))}" data-tr069-name="${escapeHTML(String(item.Name ?? ""))}">删除</button></td>
        </tr>`);
      return `
        <div class="olt-subsection">
          <div class="device-vlan-toolbar">
            <div><span class="olt-board-title">TR069 管理模板</span>
              <span class="device-vlan-count">${profiles.length} 个模板 · OLT 下发给 ONT 的 TR069/ACS 连接参数</span>
            </div>
            <div class="device-vlan-toolbar__actions">
              <button type="button" class="button button--small" data-olt-action="add-tr069" data-id="${id}">添加TR069管理模板</button>
            </div>
          </div>
          ${profiles.length
            ? `<div class="device-vlan-table-wrap">
                <table class="data-table olt-vlan-table">
                  <thead><tr><th>模板ID</th><th>模板名</th><th>ACS地址</th><th>ACS账号</th><th>操作</th></tr></thead>
                  <tbody>${profileRows.join("")}</tbody>
                </table>
              </div>`
            : `<div class="empty-state">未发现 TR069 管理模板（<code>[global-config]</code> 段无 <code>ont tr069-server-profile</code> 行）。</div>`}
        </div>`;
    };
    return `${renderSrv()}${renderTr069()}`;
  }

  function detailTabBody(id, connecting) {
    const tab = detailTab(state.activeTab);
    if (tab.id === "status") return resultHTML(id, connecting);
    if (tab.id === "config") return configTabBody(id, selectedDevice());
    if (tab.id === "vlan") return vlanTabBody(id, selectedDevice());
    if (tab.id === "dba") return `${dbaTabBody(id, selectedDevice())}${lineTabBody(id, selectedDevice())}`;
    if (tab.id === "srv") return srvTabBody(id, selectedDevice());
    return pendingTabHTML(tab);
  }

  function detailHTML() {
    const device = selectedDevice();
    if (!device) {
      return `
        <section class="panel">
          <div class="panel__header">
            <h3>OLT 设备详情</h3>
            <p>在左侧「OLT 设备台账」里选择一台设备，即可连接并查看版本、板卡与 ONT 统计。</p>
          </div>
          <div class="empty-state">暂未选择 OLT 设备。</div>
        </section>`;
    }
    const id = toInt(device.Id, 0);
    const connecting = state.connectingId === id;
    const active = detailTab(state.activeTab);
    // 面板内不显示设备详情行与连接/修改/删除按钮（业务方规格 2026-09-22）：
    // 设备身份只用于「渲染哪台设备」，故放在 data-olt-device-id 上（不产生可见文案），
    // 设备操作统一走左侧「OLT 设备台账」工具条。
    return `
      <section class="panel" data-olt-device-id="${id}">
        <div class="overview-tabs" role="tablist" aria-label="OLT 设备栏目">
          ${DETAIL_TABS.map((tab) => `<button type="button" role="tab" data-olt-action="select-detail-tab" data-tab="${tab.id}" aria-selected="${active.id === tab.id}" class="${active.id === tab.id ? "is-active" : ""}">${escapeHTML(tab.label)}</button>`).join("")}
        </div>
        <section class="overview-tab-content" role="tabpanel" aria-label="${escapeHTML(active.label)}">${detailTabBody(id, connecting)}</section>
      </section>`;
  }

  function renderDetail() {
    const container = document.getElementById("olt-detail");
    if (container) container.innerHTML = detailHTML();
  }

  // ── 数据 ──

  // refresh 读取 OLT 设备列表（Act=0）；失败只提示不改数据。
  async function refresh(root) {
    state.loading = true;
    try {
      const payload = await endpoints.list();
      state.devices = Array.isArray(payload.data) ? payload.data : [];
      state.loaded = true;
      // 默认选中：与交换机台账同口径（app.js loadDevices 的"回落到台账第一台"）。
      // 修复 BUG：此前只在校验"选中项是否仍存在"（选中项不存在时才清 0），导致选中
      // 「OLT/ONU 管理」进来时列表没有任何一行是选中态、右栏一直显示"暂未选择 OLT 设备"。
      // 现在：空列表 → 无选中；选中项已被删除/首次进入 → 自动选中台账列表的第一条记录。
      if (!state.devices.length) {
        state.selectedId = 0;
      } else if (!deviceById(state.selectedId)) {
        state.selectedId = toInt(state.devices[0].Id, 0);
      }
      // 勾选集合按最新列表收敛（设备可能被其它入口删掉）。
      const known = new Set(state.devices.map((item) => toInt(item.Id, 0)));
      state.selectedIds = new Set([...state.selectedIds].filter((id) => known.has(id)));
      renderSidebar();
      // 读台账后对该设备触发一次「读库回填」：直接从 tbOltDeviceSnapshot（表优先、老文件兜底）
      // 把上次采集结果显示在右侧，跟交换机"出现即有信息"一致，无需先点「连接」。
      const first = selectedDevice();
      if (first) {
        await loadCachedDetail(root, first);
      } else {
        renderDetail();
      }
    } catch (error) {
      notify("error", `读取 OLT 设备列表失败：${error.message}`);
    } finally {
      state.loading = false;
    }
  }

  // ── 新增 / 修改弹窗（与交换机设备台账同一套表单弹窗 class）──

  function setDialogVisible(visible) {
    const dialog = document.getElementById("olt-device-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  function setField(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function fillForm(device) {
    setField("olt-field-ip", device?.IP || "");
    setField("olt-field-type", toInt(device?.Type, 0) === 1 || toInt(device?.Type, 0) === 3 ? "1" : "0");
    setField("olt-field-username", device?.Username || "");
    setField("olt-field-password", device?.Password || "");
    setField("olt-field-port", device?.Port === undefined || device?.Port === "" ? "23" : String(device.Port));
    setField("olt-field-comment", device?.Comment || "");
    const title = document.getElementById("olt-device-dialog-title");
    if (title) title.textContent = device ? "修改 OLT 设备" : "新增 OLT 设备";
  }

  function fieldValue(id) {
    const node = document.getElementById(id);
    return node && typeof node.value === "string" ? node.value.trim() : "";
  }

  function readForm() {
    return {
      Username: fieldValue("olt-field-username"),
      Password: fieldValue("olt-field-password"),
      IP: fieldValue("olt-field-ip"),
      Type: toInt(fieldValue("olt-field-type"), 0),
      Port: fieldValue("olt-field-port") || "23",
      Comment: fieldValue("olt-field-comment")
    };
  }

  // ── 操作（与交换机设备台账同一套交互：弹窗保存 / 二次确认删除 / 连接中态）──

  async function submitForm(root) {
    const form = readForm();
    const editing = state.editingId;
    // 编辑时若口令与库中原值一致（用户没改口令），跳过强度校验：
    // 老库存量口令可能不满足新规格，否则会出现"存量设备改不了备注"的死结（后端同一口径）。
    const original = editing ? deviceById(editing) : null;
    const skipPasswordStrength = Boolean(original) && String(original.Password || "") === form.Password;
    const problem = validateOltForm(form, { skipPasswordStrength });
    if (problem) {
      notify("error", problem);
      return;
    }
    try {
      if (editing) {
        await endpoints.update({ Id: editing, ...form });
        notify("success", `OLT 设备「${form.Username}」已修改。`);
      } else {
        await endpoints.create(form);
        notify("success", `OLT 设备「${form.Username}」已新增。`);
      }
      state.editingId = 0;
      setDialogVisible(false);
      await refresh(root);
    } catch (error) {
      notify("error", `${editing ? "修改" : "新增"} OLT 设备失败：${error.message}`);
    }
  }

  // removeDevices 删除一批 OLT 设备（单条删除即 ids 只有一个元素）：
  // 二次确认 → Act=3 批量提交 → 清掉勾选与详情缓存 → 刷新列表。
  async function removeDevices(root, ids) {
    const targets = ids.map((id) => deviceById(id)).filter(Boolean);
    const single = targets.length === 1 ? targets[0] : null;
    const confirmed = await confirmAction({
      title: single ? "确认删除 OLT 设备" : "确认删除选中的 OLT 设备",
      message: single
        ? "删除后该 OLT 设备将从台账中移除（不会修改设备上的既有配置）。"
        : `将删除选中的 ${targets.length} 台 OLT 设备（不会修改设备上的既有配置）。`,
      target: single ? single.IP || single.Username || "--" : `共 ${targets.length} 台`,
      targetLabel: "OLT 设备",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    try {
      await endpoints.removeMany(ids);
      ids.forEach((id) => {
        delete state.connections[id];
        state.selectedIds.delete(id);
      });
      if (ids.includes(state.selectedId)) state.selectedId = 0;
      notify("success", single
        ? `OLT 设备「${single.IP || single.Username}」已删除。`
        : `已删除 ${targets.length} 台 OLT 设备。`);
      await refresh(root);
    } catch (error) {
      notify("error", `删除 OLT 设备失败：${error.message}`);
    }
  }

  // fillConnection 把后端返回的 OLT 快照（data 为 OltCurrentConfig 的 JSON 字段）映射成
  // state.connections[id]，供「连接采集」(Act=1) 与「读库回填」(Act=0) 共用。
  // 两处后端 Data 字段同构（tbOltDeviceSnapshot 的 JSON 标签与原 currentconfiguration.json 逐字一致）。
  function fillConnection(id, device, data) {
    return {
      time: data.ConnectTime || data.SlotidRefreshTime || new Date().toLocaleString(),
      connectTime: data.ConnectTime || "",
      slotidRefreshTime: data.SlotidRefreshTime || "",
      version: data.Version || "",
      patch: data.Patch || "",
      product: data.Product || "",
      frame: data.Frame || "",
      frameType: data.FrameType || "",
      frameDesc: data.FrameDesc || "",
      frameCount: toInt(data.FrameCount, 0),
      frameTotal: toInt(data.FrameTotal, 0),
      uptime: data.OLTRunTime || "",
      ip: device.IP || "",
      port: String(device.Port || 23),
      boards: Array.isArray(data.OltBoardInfo) ? data.OltBoardInfo : [],
      ontOnline: toInt(data.OntOnlines, 0),
      ontTotal: toInt(data.OntTotal, 0),
      ontOnlineCount: toInt(data.OntOnlineCount, 0),
      elapsedMilliseconds: 0
    };
  }

  // loadCachedDetail 从库表（Act=0，表优先、老文件兜底）读该设备的上次采集结果回填右侧，
  // 让页面加载 / 选中设备时右侧直接显示版本/温度/板卡，无需先点「连接」。
  // 库/文件都没有结果时清理该设备的内存态，右侧回落为引导文案（仍可用工具条「连接」手动采集）。
  async function loadCachedDetail(root, device) {
    const id = toInt(device.Id, 0);
    try {
      const payload = await endpoints.readCached(device);
      const data = payload.data || {};
      const usableBoards = Array.isArray(data.OltBoardInfo) && data.OltBoardInfo.length;
      const usableScalar = data.Version || data.Frame || data.OLTRunTime;
      if (usableBoards || usableScalar) {
        // 读库回填时保留已存在的实测耗时：真实「连接」(connectDevice) 刚算出的
        // elapsedMilliseconds 不能被后续 refresh→loadCachedDetail 的回填归零冲掉，
        // 否则头部「耗时」会从真实值跌回 0.0。读缓存本身无实时耗时，保持默认 0 即可。
        const keptElapsed = state.connections[id]?.elapsedMilliseconds;
        state.connections[id] = fillConnection(id, device, data);
        if (Number.isFinite(Number(keptElapsed)) && Number(keptElapsed) > 0) {
          state.connections[id].elapsedMilliseconds = keptElapsed;
        }
      } else {
        delete state.connections[id];
      }
    } catch (error) {
      // 读库失败（网络/后端问题）不清空已有内存态，仅静默保留；下次「连接」可再采集。
      if (state.connections[id] && !state.connections[id].error) {
        state.connections[id] = { error: error.message };
      }
    }
    if (root) renderDetail();
  }

  // connectDevice 连接 OLT 并读取版本/温度/板卡（只读命令）；结果渲染在右侧「OLT状态」栏目。
  async function connectDevice(root, device) {
    const id = toInt(device.Id, 0);
    state.connectingId = id;
    // 连接过程与结果都落在「OLT状态」栏目：若用户停在其它栏目，自动切回来，否则看不到"连接中/连接结果"。
    state.activeTab = "status";
    delete state.connections[id];
    renderSidebar();
    renderDetail();
    notify("info", `正在连接 OLT「${device.Username || device.IP}」，请稍候。`);
    const startedAt = Date.now();
    try {
      const payload = await endpoints.connect(device);
      const data = payload.data || {};
      const elapsed = Date.now() - startedAt;
      state.connections[id] = fillConnection(id, device, data);
      state.connections[id].elapsedMilliseconds = elapsed;
      const boardCount = state.connections[id].boards.length;
      notify("success", `OLT「${device.Username || device.IP}」连接成功：${data.Version || "版本未知"}，板卡 ${boardCount} 块，耗时 ${formatSeconds(elapsed)} 秒。`);
      await refresh(root);
      // 连接成功后后台重采「已保存配置」（后端 handleOltConnect Act=1 提交响应后触发
      // refreshSavedConfigurationInBackground），使 DBA/线路模板等「读已保存配置」栏目反映
      // 真机最新配置。这里清掉本页已读缓存并只发一次 Act=0（refreshDBA + refreshLine）重读；
      // 若后台采集尚未写完缓存，结果可能是旧快照——按需求接受（可稍后手动刷新/切换栏目重读）。
      // 全在后台进行（void），不阻塞「连接」返回与成功提示。
      void refreshDBAAndLineAfterConnect(root, device);
    } catch (error) {
      state.connections[id] = { error: error.message };
      notify("error", `OLT「${device.Username || device.IP}」连接失败：${error.message}`);
    } finally {
      state.connectingId = 0;
      renderSidebar();
      renderDetail();
    }
  }

  function openCreateForm() {
    state.editingId = 0;
    fillForm(null);
    setDialogVisible(true);
  }

  function openEditForm(device) {
    state.editingId = toInt(device.Id, 0);
    fillForm(device);
    setDialogVisible(true);
  }

  // onAction 处理左栏工具条/列表与右侧详情区共用的动作（工具条按钮不带 data-id → 用选中设备）。
  // 说明：工具条按业务方要求只保留 [连接][新增][修改][删除]；refresh 分支保留给内部/后续入口使用。
  async function onAction(root, action, id, data = {}) {
    // data 是触发元素的 dataset（栏目 TAB 用 data.tab，单板刷新用 data.slot）。
    const tab = data.tab || "";
    const slot = data.slot === undefined || data.slot === null ? "" : String(data.slot);
    if (action === "refresh") {
      await refresh(root);
      notify("success", "OLT 设备列表已刷新。");
      return;
    }
    if (action === "add") {
      openCreateForm();
      return;
    }
    if (action === "select") {
      state.selectedId = id;
      renderSidebar();
      renderDetail();
      // 选中设备后立即读库回填右侧（无需先点「连接」就能看到该设备上次采集结果）。
      const device = selectedDevice();
      if (device) void loadCachedDetail(root, device);
      return;
    }
    if (action === "select-detail-tab") {
      // 右侧面板栏目切换：只换栏目内容，不改变选中设备（栏目跨设备保留）。
      state.activeTab = tab || "status";
      renderDetail();
      // 切到「配置信息」：立即读已保存配置回填（Act=0，表优先、老文件兜底），省一次手动「刷新配置」。
      if (state.activeTab === "config" && state.configsReadId === 0 && state.configsLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadConfig(root, device);
      }
      // 切到「配置信息」栏目同时拉取终端登录用户列表（Act=0 读配置解析）。
      if (state.activeTab === "config" && state.terminalUsersLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadTerminalUsers(root, device);
      }
      // 切到「VLAN」栏目：立即读一次 VLAN 四类信息回填（Act=0，表优先、文件兜底，不连设备）。
      if (state.activeTab === "vlan" && state.vlanLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadVLAN(root, device);
      }
      // 切到「DBA/线路模板」栏目：立即读一次 DBA 模板回填（Act=0，读已保存配置解析，不连设备）。
      if (state.activeTab === "dba" && state.dbaLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadDBA(root, device);
      }
      // 切到「DBA/线路模板」栏目且 DBA 读完（同栏目）时，联动读一次线路模板回填。
      // 让两张卡片几乎同时出现，用户不必二次切换触发；线宽仅在首次进入栏目时读（lineLoadingId 判重）。
      if (state.activeTab === "dba" && state.lineLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadLine(root, device);
      }
      // 切到「业务/TR069模板」栏目：立即读一次 ONT 业务模板回填（Act=0，读已保存配置解析，不连设备）。
      if (state.activeTab === "srv" && state.srvLoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadSrv(root, device);
      }
      // 切到「业务/TR069模板」栏目同时联动读一次 TR069 管理模板回填（同栏目两张卡片）。
      if (state.activeTab === "srv" && state.tr069LoadingId === 0) {
        const device = selectedDevice();
        if (device) void loadTr069(root, device);
      }
      return;
    }
    if (action === "close-form") {
      state.editingId = 0;
      setDialogVisible(false);
      return;
    }
    const device = id ? deviceById(id) : selectedDevice();
    if (!device) {
      notify("info", "请先在左侧「OLT 设备台账」里选择一台设备。");
      return;
    }
    if (action === "connect") {
      await connectDevice(root, device);
      return;
    }
    if (action === "refresh-slot") {
      // 单板「刷新」（带 data-slot）；不带 slot 时等价于「刷新所有板卡状态」。
      await refreshBoards(root, device, slot);
      return;
    }
    if (action === "refresh-all") {
      await refreshBoards(root, device, "");
      return;
    }
    if (action === "refresh-config") {
      await refreshConfig(root, device);
      return;
    }
    if (action === "close-terminal-form") {
      state.terminalForm = null;
      setTerminalDialogVisible(false);
      return;
    }
    if (action === "close-terminal-op") {
      setTerminalOpDialogVisible(false);
      return;
    }
    if (action === "add-terminal-user") {
      openTerminalAddForm(device);
      return;
    }
    if (action === "change-terminal-password") {
      openTerminalChangeForm(device, data.username);
      return;
    }
    if (action === "remove-terminal-user") {
      await removeTerminalUser(root, device, data.username);
      return;
    }
    if (action === "add-vlan") {
      openVLANForm(device, data.vlanKind);
      return;
    }
    if (action === "remove-vlan") {
      await removeVLAN(root, device, data);
      return;
    }
    if (action === "close-vlan-form") {
      setVLANDialogVisible(false);
      return;
    }
    if (action === "close-vlan-op") {
      setVLANOpDialogVisible(false);
      return;
    }
    if (action === "add-dba") {
      openDBAForm(device);
      return;
    }
    if (action === "remove-dba") {
      await removeDBA(root, device, data);
      return;
    }
    if (action === "close-dba-form") {
      state.dbaForm = null;
      setDBADialogVisible(false);
      return;
    }
    if (action === "close-dba-op") {
      setDBAOpDialogVisible(false);
      return;
    }
    if (action === "add-line") {
      openLineForm(device);
      return;
    }
    if (action === "remove-line") {
      await removeLine(root, device, data);
      return;
    }
    if (action === "close-line-form") {
      state.lineForm = null;
      setLineDialogVisible(false);
      return;
    }
    if (action === "close-line-op") {
      setLineOpDialogVisible(false);
      return;
    }
    if (action === "add-srv") {
      openSrvForm(device);
      return;
    }
    if (action === "remove-srv") {
      await removeSrv(root, device, data);
      return;
    }
    if (action === "close-srv-form") {
      state.srvForm = null;
      setSrvDialogVisible(false);
      return;
    }
    if (action === "close-srv-op") {
      setSrvOpDialogVisible(false);
      return;
    }
    if (action === "add-tr069") {
      openTr069Form(device);
      return;
    }
    if (action === "remove-tr069") {
      await removeTr069(root, device, data);
      return;
    }
    if (action === "close-tr069-form") {
      state.tr069Form = null;
      setTr069DialogVisible(false);
      return;
    }
    if (action === "close-tr069-op") {
      setTr069OpDialogVisible(false);
      return;
    }
    if (action === "edit") {
      openEditForm(device);
      return;
    }
    if (action === "delete") {
      // 详情区的「删除」带 data-id（单条）；左侧工具条的「删除 (n)」不带 id（批处理勾选集合）。
      if (id) {
        await removeDevices(root, [id]);
        return;
      }
      const ids = state.devices
        .map((item) => toInt(item.Id, 0))
        .filter((value) => value > 0 && state.selectedIds.has(value));
      if (!ids.length) {
        notify("info", "请先勾选要删除的 OLT 设备（或用面板上的「全选」）。");
        return;
      }
      await removeDevices(root, ids);
      return;
    }
  }

  // refreshBoards 单槽/全部槽位刷新（老壳「刷新所有板卡状态」与单板「刷新」→ /itms/handleBoardAndSlotInfo/）：
  // 结果按槽位合并回当前连接的板卡列表，并更新状态刷新时间 / ONT 统计 / 本次刷新耗时。
  async function refreshBoards(root, device, slotId) {
    const id = toInt(device.Id, 0);
    const result = state.connections[id];
    if (!result || result.error) {
      notify("info", "请先点左侧工具条的「连接」读取该 OLT 信息，再刷新板卡状态。");
      return;
    }
    const slot = String(slotId || "").trim();
    if (slot) {
      result.refreshingSlot = slot;
    } else {
      result.refreshingAll = true;
    }
    renderDetail();
    notify("info", slot ? `正在读取槽位 ${slot} 的板卡状态（只读命令）...` : "正在刷新所有板卡状态（只读命令）...");
    const startedAt = Date.now();
    try {
      const payload = await endpoints.refreshBoards(device, slot);
      const boards = Array.isArray(payload.data) ? payload.data : [];
      const elapsed = Date.now() - startedAt;
      boards.forEach((board) => {
        const index = (result.boards || []).findIndex((item) => String(item.Id ?? "") === String(board.Id ?? ""));
        if (index >= 0) {
          result.boards[index] = board;
        } else {
          result.boards.push(board);
        }
      });
      // 顶层统计字段带 omitempty：字段缺省（0）时保留原值，避免把非 0 统计覆盖成 0。
      if (payload.SlotidRefreshTime) result.slotidRefreshTime = payload.SlotidRefreshTime;
      if (payload.OntOnlines !== undefined) result.ontOnline = toInt(payload.OntOnlines, 0);
      if (payload.OntTotal !== undefined) result.ontTotal = toInt(payload.OntTotal, 0);
      result.refreshElapsedMilliseconds = elapsed;
      notify("success", `${slot ? `槽位 ${slot}` : "所有板卡"}状态已刷新（${boards.length} 块，耗时 ${formatSeconds(elapsed)} 秒）。`);
    } catch (error) {
      notify("error", `${slot ? `槽位 ${slot}` : "所有板卡"}状态刷新失败：${error.message}`);
    } finally {
      result.refreshingSlot = "";
      result.refreshingAll = false;
      renderDetail();
    }
  }

// loadConfig 读「配置信息」栏目的已保存配置（Act=0，表优先、老文件兜底），不连设备。
  // 切到「配置信息」栏目时自动触发一次；读到内容即显示，读不到清空为引导态。
  async function loadConfig(root, device) {
    const id = toInt(device.Id, 0);
    if (state.configsLoadingId === id) return;
    state.configsReadId = id;
    renderDetail();
    try {
      const payload = await endpoints.readConfig(device);
      const text = typeof payload.data === "string" ? payload.data : "";
      if (text.trim()) {
        // 保留已实测的刷新耗时：手动「刷新配置」刚写入的 refreshElapsedMilliseconds 不能被回填冲掉。
        const keptElapsed = state.configs[id]?.refreshElapsedMilliseconds;
        state.configs[id] = { text };
        if (Number.isFinite(Number(keptElapsed)) && Number(keptElapsed) > 0) {
          state.configs[id].refreshElapsedMilliseconds = keptElapsed;
        }
      } else if (state.configs[id]?.text) {
        // 已保存配置为空时不覆盖已有展示，仅保留现状（与 status 栏目的读库回填口径一致）。
      } else {
        state.configs[id] = { text: "" };
      }
    } catch (error) {
      // 读库失败不清空已有缓存，仅保留；下次「刷新配置」可再采集。
      if (!state.configs[id]?.text) state.configs[id] = { error: error.message };
    } finally {
      state.configsReadId = 0;
      if (root) renderDetail();
    }
  }

  // refreshConfig 强制连设备重新采集 `display current-configuration`（Act=1）写入缓存，
  // 结果渲染在「配置信息」栏目，并把用户带到该栏目（否则看不到采集中/结果）。
  async function refreshConfig(root, device) {
    const id = toInt(device.Id, 0);
    state.configsLoadingId = id;
    state.activeTab = "config";
    renderDetail();
    notify("info", `正在采集 OLT「${device.Username || device.IP}」当前配置（只读命令）...`);
    const startedAt = Date.now();
    try {
      const payload = await endpoints.refreshConfig(device);
      const text = typeof payload.data === "string" ? payload.data : "";
      state.configs[id] = {
        text,
        refreshElapsedMilliseconds: Date.now() - startedAt
      };
      notify("success", `OLT「${device.Username || device.IP}」当前配置已刷新（${text.trim() ? text.trim().split("\n").length : 0} 行，耗时 ${formatSeconds(Date.now() - startedAt)} 秒）。`);
    } catch (error) {
      // 连接失败时保留旧的已采集文本，并标记 error 附在本次失败信息上（不清空已展示配置）。
      if (state.configs[id]?.text) {
        state.configs[id].error = error.message;
        notify("error", `OLT「${device.Username || device.IP}」配置刷新失败：${error.message}（已保留上次采集结果）。`);
      } else {
        state.configs[id] = { error: error.message };
        notify("error", `OLT「${device.Username || device.IP}」配置刷新失败：${error.message}`);
      }
    } finally {
      state.configsLoadingId = 0;
      renderSidebar();
      renderDetail();
    }
  }
// ── 终端用户弹窗（新增/修改口令）与操作反馈弹窗（删除等耗时操作）共用状态区。──
  // 弹窗内提示，不弹右上角 toast。

  // setTerminalFormStatus 在「新增/修改口令」弹窗内显示进行中/成功/失败文案。
  //   kind: "info" 进行中（带加载动画） | "success" 成功 | "error" 失败；message 为空时隐藏并清空。
  function setTerminalFormStatus(kind, message) {
    const status = document.getElementById("olt-terminal-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
    status.dataset.kind = kind || "info";
  }

  // setTerminalOpStatus 在「操作反馈弹窗」（删除）里显示进行中/成功/失败文案。
  function setTerminalOpStatus(kind, message) {
    const status = document.getElementById("olt-terminal-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  // setTerminalDialogVisible 显示/隐藏终端用户弹窗。
  function setTerminalDialogVisible(visible) {
    const dialog = document.getElementById("olt-terminal-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setTerminalFormStatus("", "");
  }

  // setTerminalOpDialogVisible 显示/隐藏「操作反馈弹窗」（删除）。
  function setTerminalOpDialogVisible(visible) {
    const dialog = document.getElementById("olt-terminal-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setTerminalOpStatus("", "");
  }

  // setTerminalSubmitting 锁定/解锁终端用户表单：提交期间禁用字段与按钮，并给出"等待中"提示。
  function setTerminalSubmitting(submitting, text) {
    const form = document.getElementById("olt-terminal-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-terminal-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-terminal-username", "olt-terminal-password", "olt-terminal-password2"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    // 提交期间禁用「取消」按钮，避免请求半途关闭弹窗导致状态错乱。
    const cancelBtn = form && form.querySelector('[data-olt-action="close-terminal-form"]');
    if (cancelBtn) cancelBtn.disabled = submitting;
    // 「修改口令」时用户名本来就是只读（disabled），这里无论如何保持提交期间不可改。
    if (state.terminalForm?.mode === "change") {
      const userField = document.getElementById("olt-terminal-username");
      if (userField) userField.disabled = true;
    }
    if (text) setTerminalFormStatus("info", text);
  }

  // openTerminalAddForm 弹出「新增终端用户」表单（按 OLT 终端用户口令规则校验）。
  function openTerminalAddForm(device) {
    state.terminalForm = { mode: "add", deviceId: toInt(device.Id, 0), username: "" };
    const title = document.getElementById("olt-terminal-dialog-title");
    if (title) title.textContent = "新增终端用户";
    const userField = document.getElementById("olt-terminal-username");
    if (userField) {
      userField.disabled = false;
      userField.value = "";
    }
    setField("olt-terminal-password", "");
    setField("olt-terminal-password2", "");
    setTerminalSubmitting(false);
    setTerminalDialogVisible(true);
  }

  // openTerminalChangeForm 弹出「修改口令」（用户名只读）。
  function openTerminalChangeForm(device, username) {
    state.terminalForm = { mode: "change", deviceId: toInt(device.Id, 0), username };
    const title = document.getElementById("olt-terminal-dialog-title");
    if (title) title.textContent = `修改终端用户「${username}」口令`;
    const usernameField = document.getElementById("olt-terminal-username");
    setField("olt-terminal-username", username);
    if (usernameField) usernameField.disabled = true;
    setField("olt-terminal-password", "");
    setField("olt-terminal-password2", "");
    setTerminalSubmitting(false);
    setTerminalDialogVisible(true);
  }

  // validateTerminalUserForm 校验终端用户口令（老 OLT 规则：长度 6-15、含字母与数字、不含 ?）。
  function validateTerminalUserForm({ username, password, password2 }) {
    if (!username) return "请输入终端用户名。";
    if (String(username).length > 32) return "终端用户名过长（≤32）。";
    if (!/^[A-Za-z0-9_.-]+$/.test(username)) return "终端用户名只能包含字母、数字、下划线、点与连字符。";
    if (!/^.{6,15}$/.test(password || "")) return "口令长度需为 6-15 位。";
    if (!/[A-Za-z]/.test(password) || !/[0-9]/.test(password)) return "口令需同时包含字母与数字。";
    if (String(password || "").indexOf("?") !== -1) return "口令不能包含问号（?）。";
    if (password !== password2) return "两次输入的口令不一致。";
    return "";
  }

  // submitTerminalForm 提交终端用户的新增 / 修改口令。全部在弹窗内提示：
  // 提交期间锁定表单并显示"等待中"，成功/失败都渲染在 #olt-terminal-form-status，不弹 toast。
  async function submitTerminalForm(root) {
    const ctx = state.terminalForm;
    if (!ctx || ctx.pending) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const username = fieldValue("olt-terminal-username") || ctx.username;
    const password = fieldValue("olt-terminal-password");
    const problem = validateTerminalUserForm({ username, password, password2: fieldValue("olt-terminal-password2") });
    if (problem) {
      setTerminalFormStatus("error", problem);
      return;
    }
    ctx.pending = true;
    state.terminalForm = ctx;
    const operationLabel = ctx.mode === "change" ? "修改口令" : "新增终端用户";
    setTerminalFormStatus("", "");
    setTerminalSubmitting(true, `正在${operationLabel}（${username}），请稍候...`);
    try {
      const payload = ctx.mode === "change"
        ? await endpoints.changeTerminalPassword(device, { TerminalUserName: username, TerminalPassword: password })
        : await endpoints.addTerminalUser(device, { TerminalUserName: username, TerminalPassword: password });
      setTerminalSubmitting(false);
      setTerminalFormStatus("success", `${ctx.mode === "change" ? "口令已修改" : "已新增"}成功。`);
      state.terminalForm = null;
      // 短暂停留展示成功态后关闭弹窗并刷新列表（P4 提速：600ms→300ms，可感等待减半）。
      await delay(300);
      setTerminalDialogVisible(false);
      await refreshTerminalUsers(root, device, payload?.data);
    } catch (error) {
      ctx.pending = false;
      state.terminalForm = ctx;
      setTerminalSubmitting(false);
      // 失败时弹窗保留，把报错信息显示在弹窗内，方便用户直接看到并改正。
      setTerminalFormStatus("error", `${operationLabel}失败：${error.message}`);
    }
  }

  // removeTerminalUser 删除终端用户（二次确认 → Act=3 → 重拉列表）。
  // 确认后转入「操作反馈弹窗」：删除进行中显示等待，成功/失败信息也都在弹窗内展示，不弹 toast。
  async function removeTerminalUser(root, device, username) {
    if (state.terminalOpPending) return;
    const confirmed = await confirmAction({
      title: "确认删除终端用户",
      message: `将从 OLT「${device.Username || device.IP}」上删除终端登录用户「${username}」。`,
      target: username,
      targetLabel: "终端用户",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    state.terminalOpPending = true;
    setTerminalOpDialogVisible(true);
    setTerminalOpStatus("info", `正在删除终端用户「${username}」，请稍候...`);
    try {
      const payload = await endpoints.removeTerminalUser(device, username);
      setTerminalOpStatus("success", `终端用户「${username}」已删除成功。`);
      // 短暂停留展示成功态后关闭弹窗并刷新列表（P4 提速：600ms→300ms，可感等待减半）。
      await delay(300);
      setTerminalOpDialogVisible(false);
      await refreshTerminalUsers(root, device, payload?.data);
    } catch (error) {
      setTerminalOpStatus("error", `删除终端用户「${username}」失败：${error.message}`);
    } finally {
      state.terminalOpPending = false;
    }
  }

  // loadTerminalUsers 读该 OLT 终端用户名列表（Act=0，读配置解析），进入「配置信息」栏目时触发。
  async function loadTerminalUsers(root, device) {
    const id = toInt(device.Id, 0);
    if (state.terminalUsersLoadingId === id) return;
    state.terminalUsersLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readTerminalUsers(device);
      const users = Array.isArray(payload.data) ? payload.data.map(String) : [];
      state.terminalUsers[id] = users;
    } catch (error) {
      // 读失败保留已有列表，未读过则标记 null（显示引导/错误）。
      if (!Array.isArray(state.terminalUsers[id])) state.terminalUsers[id] = null;
      notify("error", `读取终端登录用户失败：${error.message}`);
    } finally {
      state.terminalUsersLoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshTerminalUsers 写操作成功后更新列表并渲染。
  // P3 提速：若写接口已在 Data 里回传最新列表（data !== undefined），直接用，
  // 不再发一次 Act=0 二次读取；否则（未带回 / 带回空）仍走一次只读 Act=0 兜底。
  async function refreshTerminalUsers(root, device, data) {
    const id = toInt(device.Id, 0);
    state.terminalUsersLoadingId = id;
    renderDetail();
    try {
      if (Array.isArray(data)) {
        state.terminalUsers[id] = data.map(String);
      } else {
        const payload = await endpoints.readTerminalUsers(device);
        state.terminalUsers[id] = Array.isArray(payload.data) ? payload.data.map(String) : [];
      }
    } catch (error) {
      notify("error", `刷新终端登录用户失败：${error.message}`);
    } finally {
      state.terminalUsersLoadingId = 0;
      if (root) renderDetail();
    }
  }

// ── VLAN 功能模块 ──

  // loadVLAN 读该 OLT 的 VLAN 四类信息（Act=0，读已保存配置解析，不连设备），进入「VLAN」栏目时触发。
  async function loadVLAN(root, device) {
    const id = toInt(device.Id, 0);
    if (state.vlanLoadingId === id) return;
    state.vlanLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readVLAN(device);
      state.vlan[id] = {
        read: true,
        vlanif: Array.isArray(payload.vlanifdata) ? payload.vlanifdata : [],
        vlan: Array.isArray(payload.vlandata) ? payload.vlandata : [],
        tran: Array.isArray(payload.tranvlandata) ? payload.tranvlandata : [],
        routes: Array.isArray(payload.routestaticdata) ? payload.routestaticdata : []
      };
    } catch (error) {
      if (!state.vlan[id]?.read) state.vlan[id] = { error: error.message };
      notify("error", `读取 VLAN 功能失败：${error.message}`);
    } finally {
      state.vlanLoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshVLAN 写操作成功后重新读取并渲染（连设备采集后的最新配置）。
  async function refreshVLAN(root, device) {
    const id = toInt(device.Id, 0);
    state.vlanLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readVLAN(device);
      state.vlan[id] = {
        read: true,
        vlanif: Array.isArray(payload.vlanifdata) ? payload.vlanifdata : [],
        vlan: Array.isArray(payload.vlandata) ? payload.vlandata : [],
        tran: Array.isArray(payload.tranvlandata) ? payload.tranvlandata : [],
        routes: Array.isArray(payload.routestaticdata) ? payload.routestaticdata : []
      };
    } catch (error) {
      notify("error", `刷新 VLAN 功能失败：${error.message}`);
    } finally {
      state.vlanLoadingId = 0;
      if (root) renderDetail();
    }
  }

  function setVLANDialogVisible(visible) {
    const dialog = document.getElementById("olt-vlan-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  // setVLANOpStatus 在「操作反馈弹窗」（删除）里显示进行中/成功/失败文案（与终端用户删除同一模式）。
  function setVLANOpStatus(kind, message) {
    const status = document.getElementById("olt-vlan-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  // setVLANOpDialogVisible 显示/隐藏「操作反馈弹窗」（删除）。
  function setVLANOpDialogVisible(visible) {
    const dialog = document.getElementById("olt-vlan-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setVLANOpStatus("", "");
  }

  function setVLANField(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function setVLANRowVisible(rowId, visible) {
    const row = document.getElementById(rowId);
    if (row) row.hidden = !visible;
  }

  function setVLANFormStatus(kind, message) {
    const status = document.getElementById("olt-vlan-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
  }

  // vlanKindMeta 由 data-vlan-kind（3if / tran / vlan）映射弹窗标题与后端 Type。
  const vlanKindMeta = {
    "3if": { title: "新增三层 VLAN 接口", type: 2 },
    tran: { title: "新增上行 VLAN 透传", type: 1 },
    vlan: { title: "新增 VLAN / 二层互通", type: 0 }
  };

  // openVLANForm 弹出对应类型的 VLAN 弹窗。
  function openVLANForm(device, kind) {
    const meta = vlanKindMeta[String(kind || "vlan")] || vlanKindMeta.vlan;
    state.vlanForm = { mode: "add", kind: meta.type, deviceId: toInt(device.Id, 0) };
    const title = document.getElementById("olt-vlan-dialog-title");
    if (title) title.textContent = meta.title;
    setVLANField("olt-vlan-vlanid", "");
    setVLANField("olt-vlan-fsp", "");
    setVLANField("olt-vlan-ip", "");
    setVLANField("olt-vlan-mask", "");
    const vlanType = document.getElementById("olt-vlan-vlantype");
    if (vlanType) vlanType.value = "0";
    setVLANRowVisible("olt-vlan-fsp-row", meta.type === 1);
    setVLANRowVisible("olt-vlan-vlantype-row", meta.type === 0);
    setVLANRowVisible("olt-vlan-ip-row", meta.type === 2);
    setVLANRowVisible("olt-vlan-mask-row", meta.type === 2);
    setVLANFormStatus("", "");
    setVLANDialogVisible(true);
  }

  // validateVLANForm 校验 VLAN 新增表单（与后端 validateVLANWrite 同规则）。
  function validateVLANForm(kind, form) {
    if (!String(form.VlanID || "").trim()) return "请输入 VLAN ID。";
    if (kind === 1 && String(form.Fsp || "").split("/").length !== 3) {
      return "上行透传需填写 机框/槽位/端口（三段）。";
    }
    if (kind === 2) {
      if (!form.IP) return "请输入 IP地址。";
      if (!form.Mask) return "请输入子网掩码。";
    }
    return "";
  }
// setVLANSubmitting 锁定/解锁 VLAN 表单（提交期间禁用字段与按钮）。
  function setVLANSubmitting(submitting, text) {
    const form = document.getElementById("olt-vlan-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-vlan-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-vlan-vlanid", "olt-vlan-fsp", "olt-vlan-vlantype", "olt-vlan-ip", "olt-vlan-mask"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    const closeBtn = document.getElementById("olt-vlan-dialog")?.querySelector('[data-olt-action="close-vlan-form"]');
    if (closeBtn) closeBtn.disabled = submitting;
    if (text) setVLANFormStatus("info", text);
  }

  // submitVLANForm 提交 VLAN 新增（Act=1）。全部在弹窗内提示。
  async function submitVLANForm(root) {
    const ctx = state.vlanForm;
    if (!ctx) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const vlanid = fieldValue("olt-vlan-vlanid");
    const kind = ctx.kind;
    const form = {
      VlanID: vlanid,
      VlanType: kind === 0 ? toInt(document.getElementById("olt-vlan-vlantype")?.value, 0) : 0,
      Fsp: fieldValue("olt-vlan-fsp"),
      IP: fieldValue("olt-vlan-ip"),
      Mask: fieldValue("olt-vlan-mask")
    };
    const problem = validateVLANForm(kind, form);
    if (problem) {
      setVLANFormStatus("error", problem);
      return;
    }
    setVLANFormStatus("", "");
    setVLANSubmitting(true, "正在下发 VLAN 命令到 OLT，请稍候...");
    try {
      await endpoints.addVLAN(device, { Type: kind, ...form });
      setVLANSubmitting(false);
      setVLANFormStatus("success", "VLAN 已新增。");
      state.vlanForm = null;
      await delay(300);
      setVLANDialogVisible(false);
      await refreshVLAN(root, device);
    } catch (error) {
      setVLANSubmitting(false);
      setVLANFormStatus("error", `新增失败：${error.message}`);
    }
  }

  // removeVLAN 删除 VLAN（二次确认 → Act=2 → 重读 VLAN 列表）。
  async function removeVLAN(root, device, data) {
    if (state.vlanOpPending) return;
    const kind = String(data.vlanKind || "");
    const vlanid = String(data.vlanid || "");
    const fsp = String(data.fsp || "");
    let label = `VLAN「${vlanid}」`;
    let targetLabel = "VLAN";
    if (kind === "tran") {
      label = `上行透传 ${label}（端口 ${fsp}）`;
      targetLabel = "上行透传";
    } else if (kind === "vlanif") {
      label = `三层 VLAN 接口 ${label}`;
      targetLabel = "三层 VLAN 接口";
    }
    const confirmed = await confirmAction({
      title: "确认删除",
      message: `将从 OLT「${device.Username || device.IP}」上删除${label}。`,
      target: vlanid,
      targetLabel,
      confirmLabel: "确认删除",
      danger: true,
      // 上行透传 / 二层互通（VLAN）删除：VLAN 与新增表单一样可编辑，允许在确认前修改后删除。
      editable: kind === "tran" || kind === "vlan"
    });
    // 可编辑确认框返回 { confirmed, value }，不可编辑返回布尔值；两态都兼容。
    const editableKinds = kind === "tran" || kind === "vlan";
    const proceed = editableKinds ? confirmed?.confirmed : confirmed;
    if (!proceed) return;
    const effectiveVlanid = editableKinds ? String(confirmed?.value ?? vlanid) : vlanid;
    state.vlanOpPending = true;
    const type = kind === "vlanif" ? 2 : kind === "tran" ? 1 : 0;
    // 弹窗文案使用实际删除的 VLAN ID（若用户在确认框里改过则用新值）。
    if (effectiveVlanid !== vlanid) {
      label = label.replace(`「${vlanid}」`, `「${effectiveVlanid}」`);
    }
    // 与「配置信息」栏目终端用户删除同一模式：确认后转入「操作反馈弹窗」，
    // 删除进行中显示等待，成功/失败信息也都在弹窗内展示，不弹 toast。
    setVLANOpDialogVisible(true);
    setVLANOpStatus("info", `正在删除${label}，请稍候...`);
    try {
      await endpoints.removeVLAN(device, { Type: type, VlanID: effectiveVlanid, Fsp: fsp });
      setVLANOpStatus("success", `${label}已删除成功。`);
      // 短暂停留展示成功态后关闭弹窗并刷新列表（与终端用户删除一致：可感等待减半）。
      await delay(300);
      setVLANOpDialogVisible(false);
      await refreshVLAN(root, device);
    } catch (error) {
      setVLANOpStatus("error", `删除${label}失败：${error.message}`);
    } finally {
      state.vlanOpPending = false;
    }
  }

  // ── DBA 模板（/itms/handleDBAProfile/ Act=0/1/2，第二批迁入）──

  // loadDBA 读该 OLT 的 DBA 模板列表（Act=0，读已保存配置解析，不连设备），进入「DBA/线路模板」栏目时触发。
  async function loadDBA(root, device) {
    const id = toInt(device.Id, 0);
    if (state.dbaLoadingId === id) return;
    state.dbaLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readDBA(device);
      state.dba[id] = {
        read: true,
        profiles: Array.isArray(payload.data) ? payload.data : []
      };
    } catch (error) {
      if (!state.dba[id]?.read) state.dba[id] = { error: error.message };
      notify("error", `读取 DBA 模板失败：${error.message}`);
    } finally {
      state.dbaLoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshDBA 写操作成功后重新读取并渲染（连设备采集后的最新配置）。
  async function refreshDBA(root, device) {
    const id = toInt(device.Id, 0);
    state.dbaLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readDBA(device);
      state.dba[id] = {
        read: true,
        profiles: Array.isArray(payload.data) ? payload.data : []
      };
    } catch (error) {
      notify("error", `刷新 DBA 模板失败：${error.message}`);
    } finally {
      state.dbaLoadingId = 0;
      if (root) renderDetail();
    }
  }

  function setDBADialogVisible(visible) {
    const dialog = document.getElementById("olt-dba-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  function setDBAOpStatus(kind, message) {
    const status = document.getElementById("olt-dba-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  function setDBAOpDialogVisible(visible) {
    const dialog = document.getElementById("olt-dba-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setDBAOpStatus("", "");
  }

  function setDBAField(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function setDBARowVisible(rowId, visible) {
    const row = document.getElementById(rowId);
    if (row) row.hidden = !visible;
  }

  function setDBAFormStatus(kind, message) {
    const status = document.getElementById("olt-dba-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
  }

  // openDBAForm 弹出「添加DBA模板」弹窗（默认 type4，只显示最大带宽行）。
  function openDBAForm(device) {
    state.dbaForm = { deviceId: toInt(device.Id, 0) };
    setDBAField("olt-dba-id", "");
    setDBAField("olt-dba-name", "");
    const typeSelect = document.getElementById("olt-dba-type");
    if (typeSelect) typeSelect.value = "type4";
    setDBARowVisible("olt-dba-fix-row", false);
    setDBARowVisible("olt-dba-assure-row", false);
    setDBARowVisible("olt-dba-max-row", true);
    setDBAField("olt-dba-fix", "");
    setDBAField("olt-dba-assure", "");
    setDBAField("olt-dba-max", "");
    setDBAFormStatus("", "");
    setDBADialogVisible(true);
  }

  // validateDBAForm 校验 DBA 新增表单（与后端 validateDBAWrite 同规则）。
  function validateDBAForm(type, form) {
    const id = Number(String(form.Id || "").trim());
    if (!String(form.Id || "").trim() || !Number.isInteger(id) || id < 10 || id > 512) {
      return "请输入正确的模板ID(10-512)。";
    }
    const name = String(form.Name || "").trim();
    if (!name || name.length > 33) {
      return "请输入正确的模板名(1-33)。";
    }
    const option = DBA_TYPE_OPTIONS.find((item) => item.value === type);
    if (!option) return "不支持的 DBA 类型。";
    const inRange = (value) => Number.isInteger(value) && value >= 128 && value <= 10000000;
    if (option.fields.includes("Fix") && !inRange(Number(String(form.Fix ?? "").trim()))) {
      return "请输入正确的固定带宽(数值类型,取值范围:128-10000000)。";
    }
    if (option.fields.includes("Assure") && !inRange(Number(String(form.Assure ?? "").trim()))) {
      return "请输入正确的保证带宽(数值类型,取值范围:128-10000000)。";
    }
    if (option.fields.includes("Max") && !inRange(Number(String(form.Max ?? "").trim()))) {
      return "请输入正确的最大带宽(数值类型,取值范围:128-10000000)。";
    }
    return "";
  }

  // setDBASubmitting 锁定/解锁 DBA 表单（提交期间禁用字段与按钮）。
  function setDBASubmitting(submitting, text) {
    const form = document.getElementById("olt-dba-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-dba-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-dba-id", "olt-dba-name", "olt-dba-type", "olt-dba-fix", "olt-dba-assure", "olt-dba-max"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    const closeBtn = document.getElementById("olt-dba-dialog")?.querySelector('[data-olt-action="close-dba-form"]');
    if (closeBtn) closeBtn.disabled = submitting;
    if (text) setDBAFormStatus("info", text);
  }

  // submitDBAForm 提交 DBA 新增（Act=1）。全部在弹窗内提示。
  async function submitDBAForm(root) {
    const ctx = state.dbaForm;
    if (!ctx) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const type = String(document.getElementById("olt-dba-type")?.value || "type4");
    const form = {
      Id: Number(String(document.getElementById("olt-dba-id")?.value || "").trim()),
      Name: String(document.getElementById("olt-dba-name")?.value || "").trim(),
      Type: type,
      Fix: Number(String(document.getElementById("olt-dba-fix")?.value || "").trim()),
      Assure: Number(String(document.getElementById("olt-dba-assure")?.value || "").trim()),
      Max: Number(String(document.getElementById("olt-dba-max")?.value || "").trim())
    };
    const problem = validateDBAForm(type, form);
    if (problem) {
      setDBAFormStatus("error", problem);
      return;
    }
    setDBAFormStatus("", "");
    setDBASubmitting(true, "正在下发 DBA 模板命令到 OLT，请稍候...");
    try {
      await endpoints.addDBA(device, form);
      setDBASubmitting(false);
      setDBAFormStatus("success", "DBA 模板已新增。");
      state.dbaForm = null;
      await delay(300);
      setDBADialogVisible(false);
      await refreshDBA(root, device);
    } catch (error) {
      setDBASubmitting(false);
      setDBAFormStatus("error", `新增失败：${error.message}`);
    }
  }

  // removeDBA 删除 DBA 模板（二次确认 → Act=2 → 重读 DBA 列表）。
  async function removeDBA(root, device, data) {
    if (state.dbaOpPending) return;
    const id = String(data.dbaId || "");
    const name = String(data.dbaName || "");
    const label = `DBA 模板「${id}${name ? ` ${name}` : ""}」`;
    const confirmed = await confirmAction({
      title: "确认删除",
      message: `将从 OLT「${device.Username || device.IP}」上删除${label}。`,
      target: id,
      targetLabel: "DBA 模板ID",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    state.dbaOpPending = true;
    // 与「配置信息」栏目终端用户删除同一模式：确认后转入「操作反馈弹窗」，
    // 删除进行中显示等待，成功/失败信息也都在弹窗内展示，不弹 toast。
    setDBAOpDialogVisible(true);
    setDBAOpStatus("info", `正在删除${label}，请稍候...`);
    try {
      await endpoints.removeDBA(device, { Id: Number(id) });
      setDBAOpStatus("success", `${label}已删除成功。`);
      // 短暂停留展示成功态后关闭弹窗并刷新列表（与终端用户删除一致：可感等待减半）。
      await delay(300);
      setDBAOpDialogVisible(false);
      await refreshDBA(root, device);
    } catch (error) {
      setDBAOpStatus("error", `删除${label}失败：${error.message}`);
    } finally {
      state.dbaOpPending = false;
    }
  }

  // ── ONT 线路模板（/itms/handleLineProfile/ Act=0/1/2，第二批迁入）──

  // loadLine 读该 OLT 的 ONT 线路模板列表（Act=0，读已保存配置解析，不连设备），进入「DBA/线路模板」栏目时触发。
  async function loadLine(root, device) {
    const id = toInt(device.Id, 0);
    if (state.lineLoadingId === id) return;
    state.lineLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readLineProfile(device);
      // Act=0 顶层 data 为 OntLineProfile[]（含 GemMapping 子树）；个别实现可能返回顶层数组，都兼容。
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.line[id] = { read: true, profiles: data };
    } catch (error) {
      if (!state.line[id]?.read) state.line[id] = { error: error.message };
      notify("error", `读取 ONT 线路模板失败：${error.message}`);
    } finally {
      state.lineLoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshLine 写操作成功后重新读取并渲染（连设备采集后的最新配置）。
  // silent=true 时走「静默重绘」：不置 lineLoadingId（表格不变 loading、不闪 spinner），
  // 数据回填后直接 renderDetail 一次把列表原位更新——用于写后轮询，避免
  // 「表格→loading→表格」的整块闪烁。
  async function refreshLine(root, device, silent) {
    const id = toInt(device.Id, 0);
    if (!silent) {
      state.lineLoadingId = id;
      renderDetail();
    }
    try {
      const payload = await endpoints.readLineProfile(device);
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.line[id] = { read: true, profiles: data };
      if (silent) {
        // 静默：数据已有，仅原位更新表格，不经过 loading 态。
        if (root && state.activeTab === "dba") renderDetail();
      }
    } catch (error) {
      if (!silent || !state.line[id]?.read) {
        notify("error", `刷新 ONT 线路模板失败：${error.message}`);
      }
    } finally {
      if (!silent) {
        state.lineLoadingId = 0;
        if (root) renderDetail();
      }
    }
  }

  // refreshDBAAndLineAfterConnect 「连接」成功后重采 DBA / 线路 / 业务 / TR069 模板列表。
  // 说明：连接成功后仅发一次 Act=0（refreshDBA + refreshLine + refreshSrv + refreshTr069）重读模板列表，
  // 不再有界轮询重读；后端 handleOltConnect Act=1 会后台采集「已保存配置」，若此刻缓存尚未写完，
  // 结果可能仍是旧快照——按需求接受这一取舍（与模板写操作一致，列表可稍后手动刷新/切换栏目重读）。
  async function refreshDBAAndLineAfterConnect(root, device) {
    const id = toInt(device.Id, 0);
    // 清掉本页各模板的已读缓存，强制 Act=0 从库表/文件重新读取（单次）。
    delete state.dba[id];
    delete state.line[id];
    delete state.srv[id];
    delete state.tr069[id];
    await Promise.all([
      refreshDBA(root, device),
      refreshLine(root, device),
      refreshSrv(root, device),
      refreshTr069(root, device)
    ]);
    if (state.activeTab === "dba" || state.activeTab === "srv") renderDetail();
  }

  function setLineDialogVisible(visible) {
    const dialog = document.getElementById("olt-line-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  function setLineOpDialogVisible(visible) {
    const dialog = document.getElementById("olt-line-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setLineOpStatus("", "");
  }

  function setLineOpStatus(kind, message) {
    const status = document.getElementById("olt-line-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  function setLineField(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function setLineFormStatus(kind, message) {
    const status = document.getElementById("olt-line-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
  }

  // openLineForm 弹出「添加ONT线路模板」弹窗（默认 gpon / PON 类型 gpon、TR069 默认勾选、DBA 默认空必选）。
  // 绑定 DBA 模板为下拉框，选项来自 state.dba[deviceId].profiles（「DBA/线路模板」栏目读取的 DBA 模板列表）。
  function fillLineDBASelect(deviceId) {
    const select = document.getElementById("olt-line-dba");
    const hint = document.getElementById("olt-line-dba-hint");
    const dba = state.dba[toInt(deviceId, 0)];
    const profiles = Array.isArray(dba?.profiles) ? dba.profiles : [];
    if (select) {
      select.replaceChildren();
      const placeholder = document.createElement("option");
      placeholder.value = "";
      placeholder.textContent = "请选择绑定的 DBA 模板";
      select.appendChild(placeholder);
      for (const p of profiles) {
        const opt = document.createElement("option");
        opt.value = String(p.Id);
        opt.textContent = p.Name ? `${p.Id} - ${p.Name}` : String(p.Id);
        select.appendChild(opt);
      }
    }
    if (hint) hint.hidden = profiles.length > 0;
  }

  function openLineForm(device) {
    state.lineForm = { deviceId: toInt(device.Id, 0) };
    setLineField("olt-line-id", "");
    setLineField("olt-line-name", "");
    setLineField("olt-line-dba", "");
    setLineField("olt-line-vlan", "");
    const pontype = document.getElementById("olt-line-pontype");
    if (pontype) pontype.value = "gpon";
    const tr069 = document.getElementById("olt-line-tr069");
    if (tr069) tr069.checked = true;
    fillLineDBASelect(device.Id);
    setLineFormStatus("", "");
    setLineDialogVisible(true);
  }

  // validateLineForm 校验线路模板新增表单（与后端 validateLineProfileWrite 同规则：Id 1-8192、
  // Name 1-33、PonType gpon/epon、DbaProfileId 必选、Vlan 按 gpon 校验 GEM/VLAN 划分）。
  function validateLineForm(form) {
    const id = Number(String(form.Id || "").trim());
    if (!String(form.Id || "").trim() || !Number.isInteger(id) || id < 1 || id > 8192) {
      return "请输入正确的模板ID(1-8192)。";
    }
    // 模板ID 不能与已有线路模板重复（遍历 state.line[deviceId].profiles 判断 Id 是否已存在）。
    const ctx = state.lineForm;
    const existing = state.line[ctx?.deviceId || 0]?.profiles || [];
    if (Array.isArray(existing) && existing.some((p) => String(p.Id) === String(id))) {
      return `模板ID ${id} 已存在，不能重复。`;
    }
    const name = String(form.Name || "").trim();
    if (!name || name.length > 33) {
      return "请输入正确的模板名(1-33)。";
    }
    const ponType = String(form.PonType || "").trim().toLowerCase();
    if (ponType !== "gpon" && ponType !== "epon") {
      return "请选择 PON 类型(gpon/epon)。";
    }
    const dbaRaw = String(form.DbaProfileId ?? "").trim();
    const dbaId = Number(dbaRaw);
    if (!dbaRaw || !Number.isInteger(dbaId) || dbaId <= 0) {
      return "请选择绑定的 DBA 模板。";
    }
    const vlan = String(form.Vlan || "").trim();
    if (!vlan) {
      return "请输入 VLAN 列表（多个VLAN逗号分隔，每一种类型的业务占一行）。";
    }
    // 校验每个业务行的 VLAN 都是合法数字（1-4094），与后端解析口径一致。
    const gemSegs = vlan.split("/");
    for (const seg of gemSegs) {
      const segTrim = seg.trim();
      if (!segTrim) continue;
      const ok = String(segTrim).split(",").map((v) => v.trim())
        .every((v) => /^\d+$/.test(v) && Number(v) >= 1 && Number(v) <= 4094);
      if (!ok) {
        return "VLAN 必须是 1-4094 的数字，多个VLAN用逗号分隔、每一种类型的业务占一行。";
      }
    }
    return "";
  }

  // setLineSubmitting 锁定/解锁线路模板表单（提交期间禁用字段与按钮）。
  function setLineSubmitting(submitting, text) {
    const form = document.getElementById("olt-line-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-line-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-line-id", "olt-line-name", "olt-line-pontype", "olt-line-dba", "olt-line-tr069", "olt-line-vlan"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    const closeBtn = document.getElementById("olt-line-dialog")?.querySelector('[data-olt-action="close-line-form"]');
    if (closeBtn) closeBtn.disabled = submitting;
    if (text) setLineFormStatus("info", text);
  }

  // submitLineForm 提交线路模板新增（Act=1）。全部在弹窗内提示。
  async function submitLineForm(root) {
    const ctx = state.lineForm;
    if (!ctx) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const form = {
      Id: Number(String(document.getElementById("olt-line-id")?.value || "").trim()),
      Name: String(document.getElementById("olt-line-name")?.value || "").trim(),
      PonType: String(document.getElementById("olt-line-pontype")?.value || "gpon").trim(),
      DbaProfileId: Number(String(document.getElementById("olt-line-dba")?.value || "").trim()),
      OpenTr069: document.getElementById("olt-line-tr069")?.checked ? 1 : 0,
      // Vlan 把换行统一成 / 分隔（不同 GEM 用 /，同一 GEM 内多 VLAN 用逗号）——与后端 parse/split 口径一致。
      Vlan: String(document.getElementById("olt-line-vlan")?.value || "")
        .replace(/\r\n/g, "\n")
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean)
        .join("/")
    };
    const problem = validateLineForm(form);
    if (problem) {
      setLineFormStatus("error", problem);
      return;
    }
    setLineFormStatus("", "");
    setLineSubmitting(true, "正在下发线路模板命令到 OLT，请稍候...");
    try {
      await endpoints.addLineProfile(device, form);
      setLineSubmitting(false);
      setLineFormStatus("success", "ONT 线路模板已新增。");
      state.lineForm = null;
      // 新增成功：先关窗，再只发一次 Act=0（readLineProfile）刷新列表。后端已在写命令同一会话
      // 同步采集落库，响应返回时即最新，故立即刷新即可；不再轮询、不再人为延迟。
      setLineDialogVisible(false);
      await refreshLine(root, device, true);
    } catch (error) {
      setLineSubmitting(false);
      setLineFormStatus("error", `新增失败：${error.message}`);
      // 失败：不关闭弹窗（保留错误提示）。关窗后的列表刷新只发一次、不再轮询；
      // 但「连不上设备」时后端什么都没写，再发 Act=0 也没意义，故跳过（业务失败仍发一次对齐列表）。
      const connectionFailed = /Connection failed/i.test(String(error.message || ""));
      if (!connectionFailed) {
        try {
          await refreshLine(root, device, true);
        } catch {
          // 刷新本身失败不覆盖主错误信息，保持弹窗内的新增失败提示。
        }
      }
    }
  }

  // removeLine 删除 ONT 线路模板（二次确认 → Act=2 → 重读列表）。
  async function removeLine(root, device, data) {
    if (state.lineOpPending) return;
    const id = String(data.lineId || "");
    const name = String(data.lineName || "");
    const label = `ONT 线路模板「${id}${name ? ` ${name}` : ""}」`;
    const confirmed = await confirmAction({
      title: "确认删除",
      message: `将从 OLT「${device.Username || device.IP}」上删除${label}。`,
      target: id,
      targetLabel: "线路模板ID",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    state.lineOpPending = true;
    // 与 DBA 删除同一模式：确认后转入「操作反馈弹窗」，不再弹 toast。
    setLineOpDialogVisible(true);
    setLineOpStatus("info", `正在删除${label}，请稍候...`);
    try {
      await endpoints.removeLineProfile(device, {
        Id: Number(id),
        PonType: String(data.lineType || "").trim()
      });
      setLineOpStatus("info", `${label}已删除，正在刷新列表...`);
      // 删除成功：先关窗，再只发一次 Act=0 刷新列表。后端已在写命令同一会话同步采集落库，
      // 响应返回时即最新，故立即刷新即可；不再轮询、不再人为延迟。
      setLineOpDialogVisible(false);
      await refreshLine(root, device, true);
    } catch (error) {
      setLineOpStatus("error", `删除${label}失败：${error.message}`);
    } finally {
      state.lineOpPending = false;
    }
  }

  // loadSrv 读该 OLT 的 ONT 业务模板列表（Act=0，读已保存配置解析，不连设备），进入「业务/TR069模板」栏目时触发。
  async function loadSrv(root, device) {
    const id = toInt(device.Id, 0);
    if (state.srvLoadingId === id) return;
    state.srvLoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readSrvProfile(device);
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.srv[id] = { read: true, profiles: data };
    } catch (error) {
      if (!state.srv[id]?.read) state.srv[id] = { error: error.message };
      notify("error", `读取 ONT 业务模板失败：${error.message}`);
    } finally {
      state.srvLoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshSrv 写操作成功后重新读取并渲染（连设备采集后的最新配置）。
  // silent=true 时走「静默重绘」：不置 srvLoadingId（表格不变 loading、不闪 spinner），
  // 数据回填后直接 renderDetail 一次把列表原位更新——用于写后轮询。
  async function refreshSrv(root, device, silent) {
    const id = toInt(device.Id, 0);
    if (!silent) {
      state.srvLoadingId = id;
      renderDetail();
    }
    try {
      const payload = await endpoints.readSrvProfile(device);
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.srv[id] = { read: true, profiles: data };
      if (silent && root && state.activeTab === "srv") renderDetail();
    } catch (error) {
      if (!silent || !state.srv[id]?.read) {
        notify("error", `刷新 ONT 业务模板失败：${error.message}`);
      }
    } finally {
      if (!silent) {
        state.srvLoadingId = 0;
        if (root) renderDetail();
      }
    }
  }

  // loadTr069 读该 OLT 的 TR069 管理模板列表（Act=0，读已保存配置解析，不连设备），进入「业务/TR069模板」栏目时触发。
  async function loadTr069(root, device) {
    const id = toInt(device.Id, 0);
    if (state.tr069LoadingId === id) return;
    state.tr069LoadingId = id;
    renderDetail();
    try {
      const payload = await endpoints.readTr069Profile(device);
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.tr069[id] = { read: true, profiles: data };
    } catch (error) {
      if (!state.tr069[id]?.read) state.tr069[id] = { error: error.message };
      notify("error", `读取 TR069 管理模板失败：${error.message}`);
    } finally {
      state.tr069LoadingId = 0;
      if (root) renderDetail();
    }
  }

  // refreshTr069 写操作成功后重新读取并渲染（连设备采集后的最新配置）。
  async function refreshTr069(root, device, silent) {
    const id = toInt(device.Id, 0);
    if (!silent) {
      state.tr069LoadingId = id;
      renderDetail();
    }
    try {
      const payload = await endpoints.readTr069Profile(device);
      const data = Array.isArray(payload.data) ? payload.data : Array.isArray(payload) ? payload : [];
      state.tr069[id] = { read: true, profiles: data };
      if (silent && root && state.activeTab === "srv") renderDetail();
    } catch (error) {
      if (!silent || !state.tr069[id]?.read) {
        notify("error", `刷新 TR069 管理模板失败：${error.message}`);
      }
    } finally {
      if (!silent) {
        state.tr069LoadingId = 0;
        if (root) renderDetail();
      }
    }
  }

  function setSrvDialogVisible(visible) {
    const dialog = document.getElementById("olt-srv-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  function setSrvOpDialogVisible(visible) {
    const dialog = document.getElementById("olt-srv-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setSrvOpStatus("", "");
  }

  function setSrvOpStatus(kind, message) {
    const status = document.getElementById("olt-srv-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  function setSrvField(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function setSrvFormStatus(kind, message) {
    const status = document.getElementById("olt-srv-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
  }

  function setTr069DialogVisible(visible) {
    const dialog = document.getElementById("olt-tr069-dialog");
    if (dialog) dialog.hidden = !visible;
  }

  function setTr069OpDialogVisible(visible) {
    const dialog = document.getElementById("olt-tr069-op-dialog");
    if (dialog) dialog.hidden = !visible;
    if (visible) setTr069OpStatus("", "");
  }

  function setTr069OpStatus(kind, message) {
    const status = document.getElementById("olt-tr069-op-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-op";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-op olt-terminal-op--" + (kind || "error");
    status.replaceChildren(document.createTextNode(message));
  }

  function setTr069Field(id, value) {
    const node = document.getElementById(id);
    if (node) node.value = value;
  }

  function setTr069FormStatus(kind, message) {
    const status = document.getElementById("olt-tr069-form-status");
    if (!status) return;
    if (!message) {
      status.hidden = true;
      status.className = "olt-terminal-status";
      status.replaceChildren();
      return;
    }
    status.hidden = false;
    status.className = "olt-terminal-status olt-terminal-status--" + (kind || "info");
    status.replaceChildren(document.createTextNode(message));
  }

  // openSrvForm 弹出「添加ONT业务模板」弹窗（默认 gpon / 电话口与网口默认空=0 / 业务VLAN 空）。
  function openSrvForm(device) {
    state.srvForm = { deviceId: toInt(device.Id, 0) };
    setSrvField("olt-srv-id", "");
    setSrvField("olt-srv-name", "");
    setSrvField("olt-srv-pots", "");
    setSrvField("olt-srv-eths", "");
    setSrvField("olt-srv-vlan", "");
    const pontype = document.getElementById("olt-srv-pontype");
    if (pontype) pontype.value = "gpon";
    setSrvFormStatus("", "");
    setSrvDialogVisible(true);
  }

  // validateSrvForm 校验业务模板新增表单（与后端 validateSrvProfileWrite 同规则：
  // Id 1-8192、Name 1-33、PonType gpon/epon、Pots 0-32、Eths 0-24、业务VLAN 1-4094 且最多 8 个）。
  function validateSrvForm(form) {
    const id = Number(String(form.Id ?? ""));
    if (!Number.isInteger(id) || id < 1 || id > 8192) {
      return "请输入正确的模板ID(1-8192)。";
    }
    const name = String(form.Name || "").trim();
    if (!name || name.length < 1 || name.length > 33) {
      return "请输入正确的模板名(1-33)。";
    }
    const ponType = String(form.PonType || "").trim().toLowerCase();
    if (ponType !== "gpon" && ponType !== "epon") {
      return "请选择 PON 类型(gpon/epon)。";
    }
    const pots = Number(String(form.Pots ?? "").trim());
    if (form.Pots !== undefined && form.Pots !== "" && (!Number.isInteger(pots) || pots < 0 || pots > 32)) {
      return "请输入正确的电话口数量(0-32)。";
    }
    const eths = Number(String(form.Eths ?? "").trim());
    if (form.Eths !== undefined && form.Eths !== "" && (!Number.isInteger(eths) || eths < 0 || eths > 24)) {
      return "请输入正确的网口数量(0-24)。";
    }
    // 业务 VLAN：逗号/换行分隔，每个 1-4094，最多 8 个（与后端 srvVlanList 口径一致）。
    const vlan = String(form.Vlan || "").replace(/\r\n/g, "\n").split(/[\n,]/).map((v) => v.trim()).filter(Boolean);
    if (vlan.length === 0) {
      return "请输入业务VLAN列表(1-4094)。";
    }
    if (vlan.length > 8) {
      return "业务VLAN数量最多8个。";
    }
    if (!vlan.every((v) => /^\d+$/.test(v) && Number(v) >= 1 && Number(v) <= 4094)) {
      return "请输入正确的业务VLAN(1-4094)。";
    }
    return "";
  }

  // setSrvSubmitting 锁定/解锁业务模板表单（提交期间禁用字段与按钮）。
  function setSrvSubmitting(submitting, text) {
    const form = document.getElementById("olt-srv-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-srv-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-srv-id", "olt-srv-name", "olt-srv-pontype", "olt-srv-pots", "olt-srv-eths", "olt-srv-vlan"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    const closeBtn = document.getElementById("olt-srv-dialog")?.querySelector('[data-olt-action="close-srv-form"]');
    if (closeBtn) closeBtn.disabled = submitting;
    if (text) setSrvFormStatus("info", text);
  }

  // submitSrvForm 提交业务模板新增（Act=1）。全部在弹窗内提示。
  async function submitSrvForm(root) {
    const ctx = state.srvForm;
    if (!ctx) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const form = {
      Id: Number(String(document.getElementById("olt-srv-id")?.value || "").trim()),
      Name: String(document.getElementById("olt-srv-name")?.value || "").trim(),
      PonType: String(document.getElementById("olt-srv-pontype")?.value || "gpon").trim(),
      // 字段语义按修正口径：Pots=电话口数量、Eths=网口数量（老壳表单曾把两者错位，这里发对）。
      Pots: String(document.getElementById("olt-srv-pots")?.value || "").trim(),
      Eths: String(document.getElementById("olt-srv-eths")?.value || "").trim(),
      // 业务VLAN 统一成逗号分隔（多个VLAN逗号分隔或每行一个，每个VLAN对应一个网口）。
      Vlan: String(document.getElementById("olt-srv-vlan")?.value || "")
        .replace(/\r\n/g, "\n")
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean)
        .join(",")
    };
    const problem = validateSrvForm(form);
    if (problem) {
      setSrvFormStatus("error", problem);
      return;
    }
    setSrvFormStatus("", "");
    setSrvSubmitting(true, "正在下发业务模板命令到 OLT，请稍候...");
    try {
      await endpoints.addSrvProfile(device, form);
      setSrvSubmitting(false);
      setSrvFormStatus("success", "ONT 业务模板已新增。");
      state.srvForm = null;
      // 新增成功：先关窗，再只发一次 Act=0（readSrvProfile）刷新列表。后端已在写命令同一会话
      // 同步采集落库，响应返回时即最新，故立即刷新即可。
      setSrvDialogVisible(false);
      await refreshSrv(root, device, true);
    } catch (error) {
      setSrvSubmitting(false);
      setSrvFormStatus("error", `新增失败：${error.message}`);
      const connectionFailed = /Connection failed/i.test(String(error.message || ""));
      if (!connectionFailed) {
        try {
          await refreshSrv(root, device, true);
        } catch {
          // 刷新本身失败不覆盖主错误信息，保持弹窗内的新增失败提示。
        }
      }
    }
  }

  // removeSrv 删除 ONT 业务模板（二次确认 → Act=2 → 重读列表）。
  async function removeSrv(root, device, data) {
    if (state.srvOpPending) return;
    const id = String(data.srvId || "");
    const name = String(data.srvName || "");
    const label = `ONT 业务模板「${id}${name ? ` ${name}` : ""}」`;
    const confirmed = await confirmAction({
      title: "确认删除",
      message: `将从 OLT「${device.Username || device.IP}」上删除${label}。`,
      target: id,
      targetLabel: "业务模板ID",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    state.srvOpPending = true;
    setSrvOpDialogVisible(true);
    setSrvOpStatus("info", `正在删除${label}，请稍候...`);
    try {
      await endpoints.removeSrvProfile(device, {
        Id: Number(id),
        PonType: String(data.srvType || "").trim()
      });
      setSrvOpStatus("info", `${label}已删除，正在刷新列表...`);
      setSrvOpDialogVisible(false);
      await refreshSrv(root, device, true);
    } catch (error) {
      setSrvOpStatus("error", `删除${label}失败：${error.message}`);
    } finally {
      state.srvOpPending = false;
    }
  }

  // openTr069Form 弹出「添加TR069管理模板」弹窗（模板ID 1-32 不可重复；密码不回显，新增时填写）。
  function openTr069Form(device) {
    state.tr069Form = { deviceId: toInt(device.Id, 0) };
    setTr069Field("olt-tr069-id", "");
    setTr069Field("olt-tr069-name", "");
    setTr069Field("olt-tr069-url", "");
    setTr069Field("olt-tr069-user", "");
    setTr069Field("olt-tr069-pass", "");
    setTr069FormStatus("", "");
    setTr069DialogVisible(true);
  }

  // validateTr069Form 校验 TR069 管理模板新增表单（与后端 validateTr069ProfileWrite 同规则：
  // Id 1-32、Name 1-32、AcsUrl 1-63、AcsUserName 1-50、AcsPassword 1-25）。
  function validateTr069Form(form) {
    const id = Number(String(form.Id ?? ""));
    if (!Number.isInteger(id) || id < 1 || id > 32) {
      return "请输入正确的模板ID(1-32)。";
    }
    const name = String(form.Name || "").trim();
    if (!name || name.length < 1 || name.length > 32) {
      return "请输入正确的模板名(1-32)。";
    }
    const url = String(form.AcsUrl || "").trim();
    if (!url || url.length < 1 || url.length > 63) {
      return "请输入正确的ACS地址(1-63)。";
    }
    const user = String(form.AcsUserName || "").trim();
    if (!user || user.length < 1 || user.length > 50) {
      return "请输入正确的ACS账号(1-50)。";
    }
    const pass = String(form.AcsPassword || "");
    if (!pass || pass.length < 1 || pass.length > 25) {
      return "请输入正确的ACS密码(1-25)。";
    }
    return "";
  }

  // setTr069Submitting 锁定/解锁 TR069 模板表单（提交期间禁用字段与按钮）。
  function setTr069Submitting(submitting, text) {
    const form = document.getElementById("olt-tr069-form");
    if (form) form.setAttribute("aria-busy", submitting ? "true" : "false");
    const save = document.getElementById("olt-tr069-save");
    if (save) {
      save.disabled = submitting;
      save.classList.toggle("button--busy", submitting);
      save.textContent = submitting ? "提交中..." : "保存";
    }
    ["olt-tr069-id", "olt-tr069-name", "olt-tr069-url", "olt-tr069-user", "olt-tr069-pass"].forEach((id) => {
      const node = document.getElementById(id);
      if (node) node.disabled = submitting;
    });
    const closeBtn = document.getElementById("olt-tr069-dialog")?.querySelector('[data-olt-action="close-tr069-form"]');
    if (closeBtn) closeBtn.disabled = submitting;
    if (text) setTr069FormStatus("info", text);
  }

  // submitTr069Form 提交 TR069 管理模板新增（Act=1）。全部在弹窗内提示。
  async function submitTr069Form(root) {
    const ctx = state.tr069Form;
    if (!ctx) return;
    const device = ctx.deviceId ? deviceById(ctx.deviceId) : selectedDevice();
    if (!device) return;
    const form = {
      Id: Number(String(document.getElementById("olt-tr069-id")?.value || "").trim()),
      Name: String(document.getElementById("olt-tr069-name")?.value || "").trim(),
      AcsUrl: String(document.getElementById("olt-tr069-url")?.value || "").trim(),
      AcsUserName: String(document.getElementById("olt-tr069-user")?.value || "").trim(),
      AcsPassword: String(document.getElementById("olt-tr069-pass")?.value || "")
    };
    const problem = validateTr069Form(form);
    if (problem) {
      setTr069FormStatus("error", problem);
      return;
    }
    setTr069FormStatus("", "");
    setTr069Submitting(true, "正在下发 TR069 管理模板命令到 OLT，请稍候...");
    try {
      await endpoints.addTr069Profile(device, form);
      setTr069Submitting(false);
      setTr069FormStatus("success", "TR069 管理模板已新增。");
      state.tr069Form = null;
      // 新增成功：先关窗，再只发一次 Act=0（readTr069Profile）刷新列表。后端已在写命令同一会话
      // 同步采集落库，响应返回时即最新，故立即刷新即可。
      setTr069DialogVisible(false);
      await refreshTr069(root, device, true);
    } catch (error) {
      setTr069Submitting(false);
      setTr069FormStatus("error", `新增失败：${error.message}`);
      const connectionFailed = /Connection failed/i.test(String(error.message || ""));
      if (!connectionFailed) {
        try {
          await refreshTr069(root, device, true);
        } catch {
          // 刷新本身失败不覆盖主错误信息，保持弹窗内的新增失败提示。
        }
      }
    }
  }

  // removeTr069 删除 TR069 管理模板（二次确认 → Act=2 → 重读列表）。
  async function removeTr069(root, device, data) {
    if (state.tr069OpPending) return;
    const id = String(data.tr069Id || "");
    const name = String(data.tr069Name || "");
    const label = `TR069 管理模板「${id}${name ? ` ${name}` : ""}」`;
    const confirmed = await confirmAction({
      title: "确认删除",
      message: `将从 OLT「${device.Username || device.IP}」上删除${label}。`,
      target: id,
      targetLabel: "模板ID",
      confirmLabel: "确认删除",
      danger: true
    });
    if (!confirmed) return;
    state.tr069OpPending = true;
    setTr069OpDialogVisible(true);
    setTr069OpStatus("info", `正在删除${label}，请稍候...`);
    try {
      await endpoints.removeTr069Profile(device, {
        Id: Number(id)
      });
      setTr069OpStatus("info", `${label}已删除，正在刷新列表...`);
      setTr069OpDialogVisible(false);
      await refreshTr069(root, device, true);
    } catch (error) {
      setTr069OpStatus("error", `删除${label}失败：${error.message}`);
    } finally {
      state.tr069OpPending = false;
    }
  }

  function bind(root) {
    const handler = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== "function") return;
      const trigger = target.closest("[data-olt-action]");
      if (!trigger) return;
      // 勾选框交给 change 事件处理：这里**不能** preventDefault，
      // 否则浏览器会取消复选框的默认勾选（表现为"点了全选/勾选框没反应"）。
      const tag = String(trigger.tagName || target.tagName || "").toUpperCase();
      const action = trigger.dataset.oltAction || "";
      if (tag === "INPUT") return;
      if (action === "toggle-password" || action === "toggle-terminal-password") {
        // 登录密码 / 终端用户口令 明暗文切换：切换 input type 并同步按钮状态。
        const input = document.getElementById(trigger.dataset.target || "");
        if (input) {
          const show = input.type === "password";
          input.type = show ? "text" : "password";
          trigger.setAttribute("aria-pressed", show ? "true" : "false");
          trigger.title = show ? "隐藏口令" : "显示口令";
          trigger.textContent = show ? "🙈" : "👁";
        }
        if (typeof event.preventDefault === "function") event.preventDefault();
        return;
      }
      if (typeof event.preventDefault === "function") event.preventDefault();
      void onAction(root, trigger.dataset.oltAction, toInt(trigger.dataset.id, 0), trigger.dataset);
    };
    // 左栏台账面板与右侧详情区都做点击委托（面板在 app.js 的 layout() 里，生命周期长于本页）。
    const sidebar = document.getElementById("olt-ledger-sidebar");
    if (sidebar) sidebar.addEventListener("click", handler);
    root.addEventListener("click", handler);
    // 勾选列与「全选」用 change 事件（与交换机台账一致：勾选集合驱动批量删除）。
    const changeHandler = (event) => {
      const box = event.target;
      if (!box || typeof box.closest !== "function") return;
      // DBA 新增弹窗的「DBA类型」切换：按类型显示/隐藏对应的带宽输入行（type1→Fix，type2→Assure，
      // type3→Assure+Max，type4→Max，type5→Fix+Assure+Max，与后端 validateDBAWrite 同规则）。
      // 走 root 上的 change 委托（与勾选列同一监听），不依赖 bind 时元素是否已渲染。
      if (box.id === "olt-dba-type") {
        const type = String(box.value || "type4");
        const option = DBA_TYPE_OPTIONS.find((item) => item.value === type) || DBA_TYPE_OPTIONS[0];
        setDBARowVisible("olt-dba-fix-row", option.fields.includes("Fix"));
        setDBARowVisible("olt-dba-assure-row", option.fields.includes("Assure"));
        setDBARowVisible("olt-dba-max-row", option.fields.includes("Max"));
        return;
      }
      const trigger = box.closest("[data-olt-action]");
      if (!trigger) return;
      const action = trigger.dataset.oltAction;
      if (action === "select-for-delete") {
        const id = toInt(trigger.dataset.id, 0);
        if (box.checked) {
          state.selectedIds.add(id);
        } else {
          state.selectedIds.delete(id);
        }
        renderSidebar();
        return;
      }
      if (action === "select-all") {
        state.selectedIds = box.checked
          ? new Set(state.devices.map((item) => toInt(item.Id, 0)).filter((id) => id > 0))
          : new Set();
        renderSidebar();
      }
    };
    if (sidebar) sidebar.addEventListener("change", changeHandler);
    root.addEventListener("change", changeHandler);
    // 表单提交：委托在 #module-view 上（外壳重绘后监听不丢）。
    root.addEventListener("submit", (event) => {
      const form = event.target;
      if (!form) return;
      if (!form.id) return;
      if (typeof event.preventDefault === "function") event.preventDefault();
      if (form.id === "olt-device-form") {
        void submitForm(root);
      } else if (form.id === "olt-terminal-form") {
        void submitTerminalForm(root);
      } else if (form.id === "olt-vlan-form") {
        void submitVLANForm(root);
      } else if (form.id === "olt-dba-form") {
        void submitDBAForm(root);
      } else if (form.id === "olt-line-form") {
        void submitLineForm(root);
      } else if (form.id === "olt-srv-form") {
        void submitSrvForm(root);
      } else if (form.id === "olt-tr069-form") {
        void submitTr069Form(root);
      }
    });
  }

  // render 幂等：右栏外壳缺失时重建（首次进入、或从其它模块切回来后 #module-view 被占位页覆盖过），
  // 左栏列表与右栏详情每次重画，监听只绑一次；数据按需首次加载。
  function render(root) {
    if (!root) return;
    if (!root.__oltPageBound) {
      bind(root);
      root.__oltPageBound = true;
    }
    if (!String(root.innerHTML || "").includes('id="olt-detail"')) {
      root.innerHTML = shell();
    }
    renderSidebar();
    renderDetail();
    if (!state.loaded && !state.loading) void refresh(root);
  }

  return { render };
}

