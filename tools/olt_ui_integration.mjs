// OLT 第一批（只读 6 条接口）× 前端壳 的结合验证（tools/olt_ui_integration.mjs）。
//
// 为什么是这个形态：新控制台的 OLT 业务页属第三批（M3）交付，当前 OLT 菜单渲染的是**占位页**；
// 因此本脚本验证"第一批后端能力与前端壳能不能真的接上"，分两层：
//
//   A. 界面层：用最小 DOM 桩真实启动 web/src/app.js 的 createApp()，打真实前端壳（默认 18094），
//      断言模块菜单/占位页/capabilities 的 OLT TAB 清单都按预期渲染（前端据此建页）；
//   B. 接口层：穿过前端壳的反代（relative /itms/*），按"未来 OLT 页面会怎么调"跑一遍第一批 6 条接口，
//      断言老前端消费所需的字段（顶层 OntOnlines/OntTotal/SlotidRefreshTime、count/nPage/nLimit、
//      `"sueccess"` 消息、trace=1 的 data.result+timings 等）。
//
// 工具自带假 OLT（tools/fake_olt_telnet.py），全程只读命令，不触碰真机与老工程。
//
// 用法：
//   make olt-ui                                    # 需先 make run 与 make web-run（或对已注册实例）
//   TXAPI_BASE=http://127.0.0.1:18092 node tools/olt_ui_integration.mjs   # 也可直接打骨架
import { spawn } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import net from "node:net";
import { fileURLToPath, pathToFileURL } from "node:url";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const BASE = process.env.TXAPI_BASE || "http://127.0.0.1:18094";
const FAKE_OLT_PORT = Number(process.env.FAKE_OLT_PORT || 18999);
// 用 127.0.0.2 而不是 127.0.0.1，避免与真机/其它联调共用缓存目录（Linux 整个 127.0.0.0/8 都是本机）。
const FAKE_OLT_IP = process.env.OLT_UI_FAKE_IP || "127.0.0.2";

let failures = 0;
function check(name, ok, extra = "") {
  console.log(`  ${ok ? "✓" : "✗"} ${name}${extra ? `（${extra}）` : ""}`);
  if (!ok) failures += 1;
}
function section(title) {
  console.log(`\n== ${title} ==`);
}

// toInt 与 app.js 的同名工具一致（界面脚本里统一用它兜底）。
function toInt(value, fallback = 0) {
  const parsed = Number.parseInt(String(value ?? ""), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

// fetch 包装：页面用相对路径（/itms/...）调用，浏览器里由前端壳同源处理；
// 这里把相对路径补成 BASE 指向的壳，并打印业务返回码，便于看"界面点了什么、后端回了什么"。
const realFetch = globalThis.fetch;
// 写路径（handleLineProfile Act=1/2）请求体快照：断言前端真把 PonType 带上，杜绝“删模板缺 PonType 却假成功”。
const handledLineProfileRequests = [];
// 写路径（handleSrvProfile Act=1/2）请求体快照：同一目的（业务模板删除同样需要 PonType）。
const handledSrvProfileRequests = [];
globalThis.fetch = async (input, init) => {
  const raw = String(input);
  const url = raw.startsWith("http") ? raw : BASE + raw;
  const response = await realFetch(url, init);
  const clone = response.clone();
  let code = null;
  let message = "";
  try {
    const parsed = await clone.json();
    code = typeof parsed.code === "number" ? parsed.code : null;
    message = typeof parsed.message === "string" ? parsed.message : "";
  } catch {
    code = null;
  }
  const detail = code === null ? "" : ` => code=${code}${code !== 0 && message ? `（${message}）` : ""}`;
  console.log(`  → ${(init && init.method) || "GET"} ${url.replace(BASE, "") || "/"}${detail}`);
  // 记录 handleLineProfile 的 Act=1/2 请求体，供写路径断言，杜绝“表单缺 PonType 却显示成功”。
  if (url.endsWith("/itms/handleLineProfile/")) {
    handledLineProfileRequests.push(String(init?.body || ""));
  }
  // 记录 handleSrvProfile 的 Act=1/2 请求体，供写路径断言（业务模板删除同样需要 PonType）。
  if (url.endsWith("/itms/handleSrvProfile/")) {
    handledSrvProfileRequests.push(String(init?.body || ""));
  }
  return response;
};

// ── 假 OLT：工具自带，跑完即停（端口已有服务时复用；刚被上一个联调释放时重试，避免竞态）──
async function startFakeOlt() {
  let lastError = null;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      return await spawnFakeOlt();
    } catch (error) {
      lastError = error;
      if (await probePort(FAKE_OLT_PORT)) {
        console.log(`  提示：${FAKE_OLT_PORT} 端口已有服务，复用它（本次不新建假 OLT）`);
        return null;
      }
      await new Promise((done) => setTimeout(done, 1200));
    }
  }
  throw lastError || new Error("假 OLT 启动失败");
}

function probePort(port) {
  return new Promise((resolvePromise) => {
    const socket = net.connect({ host: "127.0.0.1", port });
    const done = (result) => {
      socket.destroy();
      resolvePromise(result);
    };
    socket.once("connect", () => done(true));
    socket.once("error", () => done(false));
    setTimeout(() => done(false), 800);
  });
}

function spawnFakeOlt() {
  return new Promise((resolvePromise, reject) => {
    const child = spawn("python3", [join(REPO_ROOT, "tools", "fake_olt_telnet.py"), String(FAKE_OLT_PORT), "0.0.0.0"], {
      stdio: ["ignore", "pipe", "pipe"]
    });
    let settled = false;
    const timer = setTimeout(() => {
      if (!settled) {
        settled = true;
        reject(new Error("假 OLT 启动超时"));
      }
    }, 5000);
    child.stdout.on("data", (chunk) => {
      if (!settled && String(chunk).includes("listening")) {
        settled = true;
        clearTimeout(timer);
        resolvePromise(child);
      }
    });
    child.on("error", (error) => {
      if (!settled) {
        settled = true;
        clearTimeout(timer);
        reject(error);
      }
    });
  });
}

// ── 后端调用（穿前端壳的反代；form 与老前端一致）──
async function callAPI(path, form = {}) {
  const response = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form)
  });
  const text = await response.text();
  let payload = null;
  try {
    payload = JSON.parse(text);
  } catch {
    payload = null;
  }
  console.log(`  → POST ${path} => code=${payload ? payload.code : "?"} http=${response.status}`);
  return { status: response.status, payload, text };
}

// 注册守卫会把未注册实例的业务接口拦成 -3：给出明确指引而不是一堆莫名断言失败。
function guardRegistration(stepName, payload) {
  if (payload && payload.code === -3) {
    check(`${stepName}：服务未注册（code=-3）`, false, "先按 web/README §4 完成注册，或对已注册实例运行");
    return true;
  }
  return false;
}

// ── 最小 DOM 桩：同一 id/选择器复用同一节点，便于断言渲染结果与派发事件 ──
function makeNode(label) {
  const node = {
    label,
    innerHTML: "",
    textContent: "",
    className: "",
    hidden: false,
    disabled: false,
    checked: false,
    value: "",
    elements: [],
    dataset: {},
    style: {},
    isConnected: true,
    _listeners: [],
    classList: {
      toggle() {},
      add() {},
      remove() {},
      contains() {
        return false;
      }
    },
    setAttribute() {},
    getAttribute() {
      return null;
    },
    removeAttribute() {},
    addEventListener(type, handler) {
      node._listeners.push({ type, handler });
    },
    removeEventListener() {},
    async dispatch(type, event) {
      for (const item of node._listeners) {
        if (item.type === type) {
          await item.handler(event || { preventDefault() {}, target: node });
        }
      }
    },
    focus() {},
    select() {},
    scrollIntoView() {},
    closest() {
      return null;
    },
    matches() {
      return false;
    },
    remove() {},
    replaceChildren(...children) {
      node.innerHTML = "";
      node.textContent = children.map((child) => (typeof child === "string" ? child : child?.textContent ?? "")).join("");
    },
    appendChild(child) {
      node.textContent += child?.textContent ?? "";
      return child;
    },
    _children: new Map(),
    querySelector(selector) {
      if (!node._children.has(selector)) {
        node._children.set(selector, makeNode(`${label} ${selector}`));
      }
      return node._children.get(selector);
    },
    querySelectorAll() {
      return [];
    }
  };
  return node;
}

const nodes = new Map();
function nodeFor(key) {
  if (!nodes.has(key)) {
    nodes.set(key, makeNode(key));
  }
  return nodes.get(key);
}

globalThis.document = {
  body: makeNode("body"),
  documentElement: makeNode("html"),
  getElementById: (id) => nodeFor(id),
  querySelector: (selector) => nodeFor(`sel:${selector}`),
  querySelectorAll: () => [],
  addEventListener: () => {},
  removeEventListener: () => {},
  createElement: (tag) => makeNode(`<${tag}>`),
  createTextNode: (text) => ({ textContent: String(text) })
};

const preferenceStore = new Map();
const windowListeners = [];
globalThis.window = {
  location: { hash: "", origin: BASE, href: `${BASE}/` },
  localStorage: {
    getItem: (key) => (preferenceStore.has(key) ? preferenceStore.get(key) : null),
    setItem: (key, value) => preferenceStore.set(key, String(value)),
    removeItem: (key) => preferenceStore.delete(key)
  },
  addEventListener(type, handler) {
    windowListeners.push({ type, handler });
  },
  removeEventListener() {},
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} })
};
globalThis.requestAnimationFrame = (callback) => setTimeout(callback, 0);
globalThis.getComputedStyle = () => ({ getPropertyValue: () => "" });

// fireHashChange 触发 app.js 注册的 hashchange 监听（#module=<id> 直达路径）。
function fireHashChange() {
  for (const item of windowListeners) {
    if (item.type === "hashchange") item.handler();
  }
}

const wait = (ms) => new Promise((done) => setTimeout(done, ms));

// waitFor 轮询等待条件成立（默认 8s）：板卡多/假 OLT 慢时，写死等待时间会把"连接中"误判成"没渲染"。
async function waitFor(predicate, timeout = 8000, step = 200) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (predicate()) return true;
    await wait(step);
  }
  return Boolean(predicate());
}

// ── A. 界面层：真实启动 createApp，打真实前端壳 ──
async function checkInterface() {
  section("A. 前端壳 × OLT 模块（DOM 桩真实启动 createApp）");
  const { createApp } = await import(pathToFileURL(join(REPO_ROOT, "web", "src", "app.js")).href);
  createApp(nodeFor("#app"));
  await wait(3000);

  const navHTML = nodeFor("module-nav").innerHTML;
  check("左侧栏模块菜单含 OLT/ONU 管理、TR069 管理、交换机管理",
    ["OLT/ONU 管理", "TR069 管理", "交换机管理"].every((title) => navHTML.includes(title)));
  const oltAnchor = /data-module="olt" class="([^"]*)"/.exec(navHTML);
  check("OLT 菜单未被标为未安装（capabilities 已含 olt）",
    Boolean(oltAnchor) && !oltAnchor[1].includes("is-missing"),
    oltAnchor ? `class="${oltAnchor[1]}"` : "未找到 OLT 菜单项");

  // 点击 OLT 菜单 → 左栏切到「OLT 设备台账」、右栏显示「OLT 设备详情」
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: { closest: (selector) => (selector === "a[data-module]" ? { dataset: { module: "olt" } } : null) }
  });
  await wait(800);
  const switchLedger = nodeFor("switch-ledger-sidebar");
  const oltLedger = nodeFor("olt-ledger-sidebar");
  const appHTML = String(nodeFor("#app").innerHTML || "");
  // 工具条按钮**必须带 data-olt-action**：真实页面里点击靠它做事件委托，
  // 曾经因为只写了 id 漏了这个属性，导致"连接/新增/修改/删除点了没反应"（真实 DOM 校验可复现）。
  check("OLT 工具条按钮都带 data-olt-action（连接在新增前、无刷新按钮）",
    ['id="olt-connect"', 'id="olt-add"', 'id="olt-edit"', 'id="olt-delete"'].every((id) => appHTML.includes(id)) &&
    !appHTML.includes('id="olt-refresh"') &&
    /id="olt-connect"[^>]*data-olt-action="connect"/.test(appHTML) &&
    /id="olt-add"[^>]*data-olt-action="add"/.test(appHTML) &&
    /id="olt-edit"[^>]*data-olt-action="edit"/.test(appHTML) &&
    /id="olt-delete"[^>]*data-olt-action="delete"/.test(appHTML) &&
    appHTML.indexOf('id="olt-connect"') < appHTML.indexOf('id="olt-add"'));
  check("OLT 菜单生效：左栏显示 OLT 设备台账、隐藏交换机设备台账",
    oltLedger.hidden === false && switchLedger.hidden === true);
  check("布局含 OLT 台账面板（标题 + 工具条 + 列表容器）",
    appHTML.includes('id="olt-ledger-sidebar"') && appHTML.includes("OLT 设备台账") &&
    appHTML.includes('id="olt-connect"') && appHTML.includes('id="olt-add"') &&
    appHTML.includes('id="olt-edit"') && appHTML.includes('id="olt-delete"') &&
    appHTML.includes('id="olt-device-list"'));
  // 右侧面板（业务方规格 2026-09-22）：8 个栏目 TAB、默认停在「OLT状态」；
  // 面板内不显示设备详情行（`OLT 设备详情 · <IP>`、`登录账号：…`）与连接/修改/删除按钮，
  // 设备身份走 data-olt-device-id（不产生可见文案），操作统一由左栏工具条承担。
  const oltListForDetail = await callAPI("/itms/handleConnectOltUser/", { Act: "0" });
  const firstLedgerDevice = Array.isArray(oltListForDetail.payload?.data) ? oltListForDetail.payload.data[0] : null;
  const detailHTML = String(nodeFor("olt-detail").innerHTML || "");
  const wantTabIds = ["status", "config", "vlan", "dba", "srv", "ont", "ontreg", "log"];
  check("右栏渲染 OLT 设备详情（默认选中台账第一条 + 8 个栏目 TAB）",
    wantTabIds.every((id) => detailHTML.includes(`data-olt-action="select-detail-tab" data-tab="${id}"`)) &&
    /data-tab="status"[^>]*aria-selected="true"/.test(detailHTML) &&
    (!firstLedgerDevice || detailHTML.includes(`data-olt-device-id="${toInt(firstLedgerDevice.Id, 0)}"`)),
    firstLedgerDevice ? `第一条=${firstLedgerDevice.IP}:${firstLedgerDevice.Port}` : "台账为空");
  check("右栏不显示设备详情行与连接/修改/删除按钮",
    !detailHTML.includes("OLT 设备详情 ·") && !detailHTML.includes("登录账号：") &&
    !/data-olt-action="(connect|edit|delete)"/.test(detailHTML));

  // 需求 1：切换模块会清掉上一个模块留下的提示（这里先用 OLT 的「刷新」产生一条提示）
  await clickOlt(oltLedger, "refresh");
  await wait(800);
  const hadOltNotice = String(nodeFor("notices").innerHTML || "").includes("OLT 设备列表已刷新");
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: { closest: (selector) => (selector === "a[data-module]" ? { dataset: { module: "tr069" } } : null) }
  });
  await wait(300);
  check("切换模块清掉上一个模块的操作提示",
    hadOltNotice && String(nodeFor("notices").innerHTML || "").trim() === "");
  check("TR069（未迁入业务页）仍走占位页且两个台账面板都隐藏",
    nodeFor("module-view").innerHTML.includes("TR069 管理") &&
    nodeFor("switch-ledger-sidebar").hidden === true && nodeFor("olt-ledger-sidebar").hidden === true);

  // 哈希直达回 OLT
  globalThis.window.location.hash = "#module=olt";
  fireHashChange();
  await wait(400);
  check("回到 OLT：台账面板恢复显示", nodeFor("olt-ledger-sidebar").hidden === false);

  // capabilities 里的 OLT TAB 清单（第三批建 OLT 页面时的依据）
  const capabilities = await fetch(`${BASE}/api/capabilities`).then((response) => response.json());
  const olt = (capabilities.modules || []).find((item) => item.id === "olt");
  const menuTitles = olt ? (olt.menus || []).map((menu) => menu.title) : [];
  check("capabilities 提供 OLT 三个 TAB（OLT 总览 / ONT 管理 / 线路·业务模板）",
    menuTitles.length === 3 && menuTitles.includes("OLT 总览") && menuTitles.includes("ONT 管理"),
    menuTitles.join(" | "));
}

// ── B. 接口层：穿过前端壳调用第一批 6 条接口 ──
async function checkEndpoints() {
  section(`B. 第一批接口（经前端壳 ${BASE} 反代 → 后端）`);
  const connData = JSON.stringify({
    Type: 2,
    IP: FAKE_OLT_IP,
    Port: String(FAKE_OLT_PORT),
    Username: "oltuser",
    Password: "secret"
  });

  // 1) 账号列表（页面下拉框数据源）
  const users = await callAPI("/itms/handleConnectOltUser/", { Act: "0" });
  if (!guardRegistration("handleConnectOltUser Act=0", users.payload)) {
    const list = Array.isArray(users.payload?.data) ? users.payload.data : null;
    const first = list && list.length ? list[0] : null;
    check("handleConnectOltUser Act=0：data 为数组，元素含 Id/Username/IP/Port/Type",
      Boolean(list) && (!first || ["Id", "Username", "IP", "Port", "Type"].every((key) => key in first)),
      list ? `${list.length} 条` : "data 不是数组");
  }

  // 2) 采集（页面"初始化/刷新"）
  const collect = await callAPI("/itms/handleOltConnect/", { Act: "1", connData });
  if (!guardRegistration("handleOltConnect Act=1", collect.payload)) {
    const data = collect.payload?.data || {};
    check("handleOltConnect Act=1：返回板卡数组 + 版本 + ONT 统计",
      // 假 OLT（tools/fake_olt_telnet.py）的 display board 0 共 4 块：0 主控 / 1、2 PON / 8 上行
      Array.isArray(data.OltBoardInfo) && data.OltBoardInfo.length === 4 &&
      Boolean(data.Version) && typeof data.OntTotal === "number" && Boolean(data.SlotidRefreshTime),
      `boards=${data.OltBoardInfo?.length} version=${data.Version} ont=${data.OntOnlines}/${data.OntTotal}`);
  }

  // 3) 读缓存（页面首屏走这条；老消息体 "sueccess" 逐字保持）
  const cached = await callAPI("/itms/handleOltConnect/", { Act: "0", connData });
  check("handleOltConnect Act=0：message=\"sueccess\" 且能读回刚采集的缓存",
    cached.payload?.message === "sueccess" && cached.payload?.data?.Version === collect.payload?.data?.Version,
    `message=${cached.payload?.message}`);

  // 4) 板卡全量（顶层统计字段是老前端的取值口径）
  const boards = await callAPI("/itms/handleBoardAndSlotInfo/", {
    connData,
    saveData: JSON.stringify({ SlotId: "" }),
    refresh: "1"
  });
  if (!guardRegistration("handleBoardAndSlotInfo 全量", boards.payload)) {
    check("handleBoardAndSlotInfo 全量：data 4 块 + 顶层 OntOnlines/OntTotal/SlotidRefreshTime",
      Array.isArray(boards.payload?.data) && boards.payload.data.length === 4 &&
      typeof boards.payload.OntOnlines === "number" && typeof boards.payload.OntTotal === "number" &&
      Boolean(boards.payload.SlotidRefreshTime));
  }

  // 5) 板卡单槽（缓存命中路径；前端点某块板卡）
  const single = await callAPI("/itms/handleBoardAndSlotInfo/", { connData, saveData: JSON.stringify({ SlotId: "1" }) });
  const singleBoard = Array.isArray(single.payload?.data) ? single.payload.data[0] : null;
  check("handleBoardAndSlotInfo 单槽：只返回该槽位且带 PON 口明细",
    Boolean(singleBoard) && singleBoard.Id === "1" && Array.isArray(singleBoard.PonInfos) && singleBoard.PonInfos.length > 0,
    singleBoard ? `Id=${singleBoard.Id} pon=${singleBoard.PonInfos?.length}` : "无数据");

  // 6) 当前配置（文本直出）
  const configRefresh = await callAPI("/itms/handleCurrentConfiguration/", { Act: "1", connData });
  const configText = typeof configRefresh.payload?.data === "string" ? configRefresh.payload.data : "";
  check("handleCurrentConfiguration Act=1：data 为配置文本且含设备主机名",
    configText.includes("hostname OLT-E2E"), `${configText.length} 字符`);
  const configCached = await callAPI("/itms/handleCurrentConfiguration/", { Act: "0", connData });
  check("handleCurrentConfiguration Act=0：命中缓存（与 Act=1 一致）", configCached.payload?.data === configText);

  // 6.1) 终端登录用户（Act=0 读配置文本解析出用户名数组）
  const terminalUsers = await callAPI("/itms/handleOltTerminalUser/", { Act: "0", connData });
  const tuArr = Array.isArray(terminalUsers.payload?.data) ? terminalUsers.payload.data : [];
  check("handleOltTerminalUser Act=0：返回用户名数组（含 admin / olt_operator）",
    tuArr.includes("admin") && tuArr.includes("olt_operator"),
    `users=[${tuArr.join(",")}]`);

  // 7) ONT 列表（分页字段在顶层）
  // 老口径：nLimit 是请求值原样回传；count=0 时 count/nPage 因 APIResponse 的 omitempty 一起缺省
  // （老实现同样如此，前端按 res.count || 0 处理）。非空数据要等 Act=9（第二批）迁入后才有。
  const ontList = await callAPI("/itms/handleONTListAndStatus/", {
    Act: "0",
    connData,
    reqData: JSON.stringify({ Page: 1, Limit: 50 })
  });
  const ontCount = typeof ontList.payload?.count === "number" ? ontList.payload.count : 0;
  check("handleONTListAndStatus Act=0：顶层 nLimit + data 为数组；有数据时 nPage 齐备",
    Boolean(ontList.payload) && "nLimit" in ontList.payload && Array.isArray(ontList.payload.data) &&
    (ontCount === 0 || typeof ontList.payload.nPage === "number"),
    `nPage=${ontList.payload?.nPage ?? "(缺省)"} nLimit=${ontList.payload?.nLimit} count=${ontList.payload?.count ?? "(缺省)"}`);

  const ontLive = await callAPI("/itms/handleONTListAndStatus/", {
    Act: "9",
    connData,
    reqData: JSON.stringify({ Page: 1, Limit: 50 })
  });
  check("handleONTListAndStatus Act=9：明确提示属第二批（-1，不是参数错误）",
    ontLive.payload?.code === -1 && String(ontLive.payload?.message || "").includes("第二批"));

  // 8) 操作日志（分页字段在顶层）
  const logs = await callAPI("/itms/handleOltLogList/", { Act: "0", reqData: JSON.stringify({ Page: 1, Limit: 5 }) });
  if (!guardRegistration("handleOltLogList Act=0", logs.payload)) {
    check("handleOltLogList Act=0：顶层 count/nPage/nLimit 齐备",
      typeof logs.payload?.count === "number" && typeof logs.payload?.nPage === "number" &&
      typeof logs.payload?.nLimit === "number",
      `count=${logs.payload?.count} nPage=${logs.payload?.nPage} nLimit=${logs.payload?.nLimit}`);
  }

  // 9) trace=1（前端调试面板：data.result + data.timings）
  const traced = await callAPI("/itms/handleBoardAndSlotInfo/", {
    connData,
    saveData: JSON.stringify({ SlotId: "" }),
    refresh: "1",
    trace: "1"
  });
  const traceData = traced.payload?.data || {};
  check("trace=1：data 包装为 {result, timings}，timings 含阶段耗时",
    Boolean(traceData.result) && Boolean(traceData.timings) && Object.keys(traceData.timings).length > 1,
    traceData.timings ? Object.keys(traceData.timings).join(",") : "");
}

// ── C. OLT 设备台账（左栏）× 设备详情（右栏）：界面操作（新增 / 选择 / 连接 / 修改 / 删除）──
//
// 与 A 段同一套 DOM 桩：真点按钮、真提交表单、真走接口，再用 Act=0 独立核对库里确实变了。
async function checkOltDevicePage() {
  section("C. OLT 设备台账（左栏）× 设备详情（右栏）：新增 → 选择 → 连接 → 修改 → 删除");
  const root = nodeFor("module-view");
  const sidebar = nodeFor("olt-ledger-sidebar");

  // 左侧栏点进 OLT 模块
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: { closest: (selector) => (selector === "a[data-module]" ? { dataset: { module: "olt" } } : null) }
  });
  await wait(1500);
  check("左栏 OLT 台账面板显示且列表容器已渲染（含空态或设备行）",
    sidebar.hidden === false &&
    String(nodeFor("olt-device-list").innerHTML || "").length > 0);

  const username = `olt_ui_${String(Date.now()).slice(-6)}`; // 账号规则：字母/数字/下划线
  let created = null;
  // createdIds 记录本次联调创建的设备，finally 里兜底清理（删除失败时不残留）。
  const createdIds = [];
  try {
    // 1) 新增：左栏「新增」→ 同款表单弹窗 → 填表 → 保存
    await clickOlt(sidebar, "add");
    check("点击左栏「新增」弹出同款表单弹窗", nodeFor("olt-device-dialog").hidden === false);
    setOltField("olt-field-ip", FAKE_OLT_IP);
    setOltField("olt-field-type", "0"); // 0 = TELNET（新口径默认）
    setOltField("olt-field-username", username);
    setOltField("olt-field-password", "UiPass-123");
    setOltField("olt-field-port", String(FAKE_OLT_PORT));
    setOltField("olt-field-comment", "界面联调");
    await submitOltForm(root);
    await wait(1500);
    const listAfterCreate = String(nodeFor("olt-device-list").innerHTML || "");
    check("保存后弹窗关闭，左栏列表按「IP:Port / 备注 / 类型」显示新设备",
      nodeFor("olt-device-dialog").hidden === true &&
      listAfterCreate.includes(`${FAKE_OLT_IP}:${FAKE_OLT_PORT}`) &&
      listAfterCreate.includes("备注：界面联调") && listAfterCreate.includes("TELNET") &&
      !listAfterCreate.includes("端口："));
    check("列表不再显示登录账号（只在 title 提示里）",
      listAfterCreate.includes(`title="登录账号：${username}"`) && !listAfterCreate.includes(`<strong>${username}</strong>`));
    created = await findOltDevice(username);
    check("新增已落库（用 Act=0 独立核对）", Boolean(created), created ? `Id=${created.Id} Type=${created.Type}` : "未找到");

    if (!created) return;
    const deviceId = toInt(created.Id, 0);
    createdIds.push(deviceId);

    // 2) 字段校验（业务方规格）：非法 IP / 账号 / 口令 / 端口都必须被拦下且不落库
    const invalidCases = [
      { name: "非法 IP", field: ["olt-field-ip", "192.0.2.999"], want: "IPv4" },
      { name: "账号含连字符", field: ["olt-field-username", "olt-ui"], want: "登录账号" },
      { name: "口令强度不足", field: ["olt-field-password", "abcdefgh"], want: "密码" },
      { name: "端口越界", field: ["olt-field-port", "70000"], want: "端口" }
    ];
    for (const item of invalidCases) {
      await clickOlt(sidebar, "add");
      setOltField("olt-field-ip", FAKE_OLT_IP);
      setOltField("olt-field-type", "0");
      setOltField("olt-field-username", `${username}x`);
      setOltField("olt-field-password", "UiPass-123");
      setOltField("olt-field-port", String(FAKE_OLT_PORT));
      setOltField(item.field[0], item.field[1]);
      await submitOltForm(root);
      await wait(400);
      const notice = String(nodeFor("notices").innerHTML || "");
      check(`校验拦截：${item.name}`, notice.includes(item.want) && notice.includes("异常"));
      const stray = await findOltDevice(`${username}x`);
      check(`校验拦截后未落库：${item.name}`, !stray);
    }
    await clickOlt(root, "close-form");

    // 3) 选择：点左栏列表行 → 右侧面板出现该设备的栏目（默认 OLT状态）
    await clickOlt(sidebar, "select", deviceId);
    await wait(200);
    check("点击左栏设备行后，右侧面板渲染该设备的栏目（默认 OLT状态）",
      nodeFor("olt-detail").innerHTML.includes(`data-olt-device-id="${deviceId}"`) &&
      /data-tab="status"[^>]*aria-selected="true"/.test(nodeFor("olt-detail").innerHTML));
    // 3.1) 栏目切换：点击「配置信息」→ 该 TAB 激活并显示引导 + 「刷新配置」（不假成功）
    check("右侧面板含「配置信息」栏目按钮",
      nodeFor("olt-detail").innerHTML.includes('data-olt-action="select-detail-tab" data-tab="config"'));
    await clickOltDetailTab("config");
    await wait(250);
    check("切换「配置信息」栏目后该 TAB 激活并显示引导与「刷新配置」按钮（不再显示待迁入占位）",
      /data-tab="config"[^>]*aria-selected="true"/.test(nodeFor("olt-detail").innerHTML) &&
      nodeFor("olt-detail").innerHTML.includes("刷新配置") &&
      !nodeFor("olt-detail").innerHTML.includes("待迁入（第二批）"));
    // 未连接/未采集过时点击「刷新配置」：连设备采集当前配置全文
    await clickOlt(sidebar, "refresh-config", deviceId);
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("olt-config-text") &&
      nodeFor("olt-detail").innerHTML.includes("hostname OLT-E2E"), 10000, 250);
    const configHTML = nodeFor("olt-detail").innerHTML;
    check("「刷新配置」采集到当前配置全文（hostname / ont add / return）",
      configHTML.includes("hostname OLT-E2E") &&
      configHTML.includes("ont add 0 0 sn-auth") &&
      configHTML.includes("return"));
    // 3.1.1) 终端登录用户：切到「配置信息」会自动读取用户名列表（Act=0）
    await waitFor(() => {
      const html = nodeFor("olt-detail").innerHTML;
      return html.includes("OLT 登录用户") && html.includes("olt-user-row");
    }, 8000, 250);
    const usersHTML = nodeFor("olt-detail").innerHTML;
    check("「配置信息」栏目渲染「OLT 登录用户」列表（admin / olt_operator）+ 新增按钮",
      usersHTML.includes("olt-user-name") &&
      usersHTML.includes("admin") &&
      usersHTML.includes("olt_operator") &&
      usersHTML.includes('data-olt-action="add-terminal-user"'));
    // 3.1.2) VLAN 栏目：切换「VLAN」自动读取并渲染四张 data-table 表格（不再显示待迁入占位）
    await clickOltDetailTab("vlan");
    await waitFor(() => {
      const html = nodeFor("olt-detail").innerHTML;
      return html.includes("三层 VLAN 接口") || html.includes("VLAN功能");
    }, 8000, 250);
    const vlanHTML = nodeFor("olt-detail").innerHTML;
    check("「VLAN」栏目激活并渲染四张表格（三层接口 / 上行透传 / VLAN 列表 / 静态路由），不再显示待迁入",
      /data-tab="vlan"[^>]*aria-selected="true"/.test(vlanHTML) &&
      vlanHTML.includes("三层 VLAN 接口") && vlanHTML.includes("上行 VLAN 透传") &&
      vlanHTML.includes("VLAN 列表") && vlanHTML.includes("静态路由") &&
      (vlanHTML.match(/olt-vlan-table/g) || []).length >= 4 && !vlanHTML.includes("待迁入（第二批）"));
    check("「VLAN」栏目解析出假 OLT 的三层接口与上行透传（以 data-table 渲染）",
      vlanHTML.includes("data-table") && vlanHTML.includes("192.168.50.1") &&
      vlanHTML.includes("0/3/0") && vlanHTML.includes("100"));
    check("VLAN 列表按类型各占一行，仅「单层智能VLAN(Smart VLAN)」有删除（组播等无删除，且删除按钮只此一处）",
      vlanHTML.includes("单层智能VLAN(Smart VLAN)") &&
      vlanHTML.includes("39,46,100,139,200,300,405,2000,4015,4034") &&
      (vlanHTML.match(/data-olt-action="remove-vlan"[^>]*data-vlan-kind="vlan"/g) || []).length === 1 &&
      vlanHTML.includes("组播业务 VLAN"));
    check("静态路由多条时一行一条（目的网络/掩码 → 下一跳）",
      vlanHTML.includes("0.0.0.0 / 0.0.0.0") && vlanHTML.includes("172.20.247.254") &&
      vlanHTML.includes("6.6.6.6 / 255.255.255.255") && vlanHTML.includes("192.168.46.1"));
    // 3.1.3) DBA/线路模板 栏目：切换「DBA」自动读取 DBA 模板列表（Act=0 读已保存配置解析）
    await clickOltDetailTab("dba");
    await waitFor(() => {
      const html = nodeFor("olt-detail").innerHTML;
      return html.includes("DBA 模板") && (html.includes("dba-profile") || html.includes("DBA Main"));
    }, 8000, 250);
    const dbaHTML = nodeFor("olt-detail").innerHTML;
    check("「DBA/线路模板」栏目激活并渲染 DBA 模板表格（5 条 + 添加按钮），不再显示待迁入占位",
      /data-tab="dba"[^>]*aria-selected="true"/.test(dbaHTML) &&
      dbaHTML.includes("DBA 模板") && dbaHTML.includes("模板ID") && dbaHTML.includes("DBA类型") &&
      dbaHTML.includes("固定带宽(kbps)") && dbaHTML.includes("保证带宽(kbps)") && dbaHTML.includes("最大带宽(kbps)") &&
      dbaHTML.includes("DBA Main") && dbaHTML.includes("Full") &&
      dbaHTML.includes('data-olt-action="add-dba"') &&
      !dbaHTML.includes("待迁入（第二批）"));
    check("DBA 类型以老控制台文案展示（type3-保证带宽+最大带宽 / type1-固定带宽）",
      dbaHTML.includes("type3-保证带宽+最大带宽") && dbaHTML.includes("type1-固定带宽"));
    // 3.1.3a) 新增 DBA：点「添加DBA模板」→ 弹窗 → 填表 → 提交（写路径，连假 OLT 下发命令）
    await clickOlt(sidebar, "add-dba", deviceId);
    await wait(150);
    const dbaDialog = nodeFor("olt-dba-dialog");
    check("点「添加DBA模板」弹出 DBA 弹窗（默认 type1 只显示固定带宽行）",
      dbaDialog.hidden === false &&
      nodeFor("olt-dba-fix-row").hidden === false &&
      nodeFor("olt-dba-assure-row").hidden === true &&
      nodeFor("olt-dba-max-row").hidden === true);
    setOltField("olt-dba-id", "100");
    setOltField("olt-dba-name", "让利宽带");
    setOltField("olt-dba-type", "type3");
    setOltField("olt-dba-assure", "20032");
    setOltField("olt-dba-max", "1000000");
    await submitOltDBAForm(root); // 走 #module-view 上的 submit 委托（target=olt-dba-form）
    await waitFor(() => nodeFor("olt-dba-dialog").hidden === true, 8000, 250);
    check("提交新增 DBA 后弹窗关闭（写操作成功路径）", nodeFor("olt-dba-dialog").hidden === true);
    // refreshDBA 异步重读列表（连设备采集后的最新配置），等渲染出新增的「让利宽带」。
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("让利宽带"), 8000, 250);
    check("新增 DBA 后列表重新读取并包含新模板（让利宽带）",
      nodeFor("olt-detail").innerHTML.includes("让利宽带"));
    // 3.1.3b) 删除 DBA：二次确认 → 操作反馈弹窗 → 成功 → 重读列表
    await clickOlt(sidebar, "remove-dba", deviceId, { dbaId: "100", dbaName: "让利宽带" });
    await wait(250);
    const dbaConfirmRoot = nodeFor("confirm-dialog-root");
    check("删除 DBA 弹出二次确认（含模板ID 100）",
      String(dbaConfirmRoot.innerHTML).includes("DBA 模板") && String(dbaConfirmRoot.innerHTML).includes("100"));
    await dbaConfirmRoot.dispatch("click", {
      preventDefault() {},
      target: { closest: (selector) => (selector === '[data-action="accept-confirm"]' ? {} : null) }
    });
    await waitFor(() => nodeFor("olt-dba-op-dialog").hidden === true, 8000, 250);
    check("删除 DBA 后操作反馈弹窗关闭并重读列表（写回路径）",
      nodeFor("olt-dba-op-dialog").hidden === true);
    // 3.1.3c) 线路模板：同栏目下方应有「ONT 线路模板」卡片（读已保存配置解析，两模板两 GEM 映射）。
    // 进入 dba 栏目即联动 loadLine，等渲染出"ONT 线路模板"卡片与模板行。
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("ONT 线路模板")
      && nodeFor("olt-detail").innerHTML.includes("ClientA"), 8000, 250);
    const lineHTML = nodeFor("olt-detail").innerHTML;
    check("「DBA/线路模板」栏目渲染 ONT 线路模板卡片（标题 + 两条模板 + 添加按钮 + PON 类型标签）",
      lineHTML.includes("ONT 线路模板") &&
      lineHTML.includes("ClientA") && lineHTML.includes("ClientB") &&
      lineHTML.includes("模板ID") && lineHTML.includes("PON类型") && lineHTML.includes("映射关系") &&
      lineHTML.includes("gpon") &&
      lineHTML.includes('data-olt-action="add-line"') &&
      lineHTML.includes('data-olt-action="remove-line"'));
    check("线路模板「映射关系」列内嵌子表（T-CONT / DBA模版 / GEM Port / GEM 映射ID / VLAN 表头 + 数据行）",
      lineHTML.includes('class="olt-line-map-table"') &&
      lineHTML.includes(">T-CONT</th>") && lineHTML.includes(">DBA模版</th>") &&
      lineHTML.includes(">GEM Port</th>") && lineHTML.includes(">GEM 映射ID</th>") &&
      lineHTML.includes(">VLAN</th>") && lineHTML.includes("GEM") && lineHTML.includes("100"));
    // 3.1.3d) 新增线路模板：点「添加ONT线路模板」→ 弹窗 → 填表 → 提交（写路径，连假 OLT 下发命令）。
    await clickOlt(sidebar, "add-line", deviceId);
    await wait(150);
    const lineDialog = nodeFor("olt-line-dialog");
    check("点「添加ONT线路模板」弹出线路模板弹窗（默认 gpon / TR069 默认勾选）",
      lineDialog.hidden === false &&
      nodeFor("olt-line-pontype").value === "gpon" &&
      nodeFor("olt-line-tr069").checked === true);
    setOltField("olt-line-id", "100");
    setOltField("olt-line-name", "让利线路");
    setOltField("olt-line-pontype", "gpon");
    setOltField("olt-line-dba", "50");
    nodeFor("olt-line-tr069").checked = true;
    setOltField("olt-line-vlan", "100,200\n300");
    await submitOltLineForm(root); // 走 #module-view 上的 submit 委托（target=olt-line-form）
    await waitFor(() => nodeFor("olt-line-dialog").hidden === true, 8000, 250);
    check("提交新增线路模板后弹窗关闭（写操作成功路径）", nodeFor("olt-line-dialog").hidden === true);
    // 3.1.3e) 删除线路模板：二次确认 → 操作反馈弹窗 → 成功 → 关闭。
    await clickOlt(sidebar, "remove-line", deviceId, {
      lineId: "100", lineName: "让利线路", lineType: "gpon"
    });
    await wait(250);
    // 删除请求体必须带上 PonType（后端 Act=2 缺 PonType 会拒绝，否则 `undo ont-lineprofile  profile-id` 假成功）。
    const lineDeleteReq = handledLineProfileRequests.find(
      (s) => s.includes('"Act":2')
    ) || handledLineProfileRequests[handledLineProfileRequests.length - 1] || "";
    check("删除线路模板请求体携带 PonType=gpon（后端 Act=2 缺 PonType 拒绝）",
      lineDeleteReq.includes('"PonType"') && lineDeleteReq.includes("gpon"),
      lineDeleteReq.slice(0, 200));
    const lineConfirmRoot = nodeFor("confirm-dialog-root");
    check("删除线路模板弹出二次确认（含模板ID 100）",
      String(lineConfirmRoot.innerHTML).includes("ONT 线路模板") && String(lineConfirmRoot.innerHTML).includes("100"));
    await lineConfirmRoot.dispatch("click", {
      preventDefault() {},
      target: { closest: (selector) => (selector === '[data-action="accept-confirm"]' ? {} : null) }
    });
    await waitFor(() => nodeFor("olt-line-op-dialog").hidden === true, 8000, 250);
    check("删除线路模板后操作反馈弹窗关闭（写回路径）", nodeFor("olt-line-op-dialog").hidden === true);
    // 3.1.3f) 业务/TR069模板 栏目：切「srv」自动读业务模板 + TR069 管理模板（Act=0 读已保存配置解析）
    await clickOltDetailTab("srv");
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("ONT 业务模板")
      && nodeFor("olt-detail").innerHTML.includes("SrvA"), 8000, 250);
    const srvHTML = nodeFor("olt-detail").innerHTML;
    check("「业务/TR069模板」栏目激活并渲染 ONT 业务模板表格（两条模板 + 电话口/网口/业务VLAN 子表 + 添加/删除按钮），不再显示待迁入占位",
      /data-tab="srv"[^>]*aria-selected="true"/.test(srvHTML) &&
      srvHTML.includes("ONT 业务模板") && srvHTML.includes("SrvA") && srvHTML.includes("SrvB") &&
      srvHTML.includes("模板ID") && srvHTML.includes("电话口") && srvHTML.includes("网口") &&
      srvHTML.includes("业务VLAN") &&
      srvHTML.includes('data-olt-action="add-srv"') &&
      srvHTML.includes('data-olt-action="remove-srv"') &&
      !srvHTML.includes("待迁入"));
    check("业务模板「业务VLAN」列内嵌子表（网口 / 业务VLAN 表头 + 映射值）",
      srvHTML.includes('class="olt-line-map-table"') && srvHTML.includes(">网口</th>") &&
      srvHTML.includes(">业务VLAN</th>") && srvHTML.includes("100"));
    // 新增业务模板：点「添加ONT业务模板」→ 弹窗 → 填表 → 提交（写路径，连假 OLT 下发命令）。
    await clickOlt(sidebar, "add-srv", deviceId);
    await wait(150);
    const srvDialog = nodeFor("olt-srv-dialog");
    check("点「添加ONT业务模板」弹出业务模板弹窗（默认 gpon / 电话口数量 / 网口数量 / 业务VLAN 字段）",
      srvDialog.hidden === false &&
      nodeFor("olt-srv-pontype").value === "gpon" &&
      nodeFor("olt-srv-pots") && nodeFor("olt-srv-eths") && nodeFor("olt-srv-vlan"));
    setOltField("olt-srv-id", "100");
    setOltField("olt-srv-name", "让利业务");
    setOltField("olt-srv-pontype", "gpon");
    setOltField("olt-srv-pots", "1");
    setOltField("olt-srv-eths", "2");
    setOltField("olt-srv-vlan", "500\n501");
    await submitOltSrvForm(root); // 走 #module-view 上的 submit 委托（target=olt-srv-form）
    await waitFor(() => nodeFor("olt-srv-dialog").hidden === true, 8000, 250);
    check("提交新增业务模板后弹窗关闭（写操作成功路径）", nodeFor("olt-srv-dialog").hidden === true);
    // refreshSrv 异步重读列表（连设备采集后的最新配置），等渲染出新增的「让利业务」。
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("让利业务"), 8000, 250);
    check("新增业务模板后列表重新读取并包含新模板（让利业务）",
      nodeFor("olt-detail").innerHTML.includes("让利业务"));
    // 删除业务模板：二次确认 → 操作反馈弹窗 → 成功 → 关闭。
    await clickOlt(sidebar, "remove-srv", deviceId, {
      srvId: "100", srvName: "让利业务", srvType: "gpon"
    });
    await wait(250);
    const srvConfirmRoot = nodeFor("confirm-dialog-root");
    check("删除业务模板弹出二次确认（含模板ID 100）",
      String(srvConfirmRoot.innerHTML).includes("ONT 业务模板") && String(srvConfirmRoot.innerHTML).includes("100"));
    await srvConfirmRoot.dispatch("click", {
      preventDefault() {},
      target: { closest: (selector) => (selector === '[data-action="accept-confirm"]' ? {} : null) }
    });
    await waitFor(() => nodeFor("olt-srv-op-dialog").hidden === true, 8000, 250);
    check("删除业务模板后操作反馈弹窗关闭（写回路径）", nodeFor("olt-srv-op-dialog").hidden === true);
    // 删除请求体必须带上 PonType（后端 Act=2 缺 PonType 会拒绝，否则 `undo ont-srvprofile  profile-id` 假成功）。
    // 请求体是表单（URL 编码），先 decode 再找 Act=2 的删除请求并校验带 PonType。
    const srvDeleteReq = handledSrvProfileRequests.map((s) => decodeURIComponent(s))
      .find((s) => s.includes('"Act":2')) || "";
    check("删除业务模板请求体携带 PonType=gpon（后端 Act=2 缺 PonType 拒绝）",
      srvDeleteReq.includes('"PonType"') && srvDeleteReq.includes("gpon"),
      `records=[${handledSrvProfileRequests.map((s) => decodeURIComponent(s)).join(" || ")}]`);
    // TR069 管理模板卡片（同栏目下方，联动 loadTr069 读已保存配置解析；读取不回密码）。
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("TR069 管理模板")
      && nodeFor("olt-detail").innerHTML.includes("ACS-A"), 8000, 250);
    const tr069HTML = nodeFor("olt-detail").innerHTML;
    check("「业务/TR069模板」栏目渲染 TR069 管理模板卡片（两条模板 + ACS地址/ACS账号 + 添加/删除按钮，不回密码）",
      tr069HTML.includes("TR069 管理模板") && tr069HTML.includes("ACS-A") && tr069HTML.includes("ACS-B") &&
      tr069HTML.includes("ACS地址") && tr069HTML.includes("ACS账号") &&
      tr069HTML.includes("http://acs.example.com") && tr069HTML.includes("acsuser") &&
      tr069HTML.includes('data-olt-action="add-tr069"') &&
      tr069HTML.includes('data-olt-action="remove-tr069"') &&
      !tr069HTML.includes("AcsPassword"));
    // 新增 TR069：点「添加TR069管理模板」→ 弹窗 → 填表 → 提交（写路径，连假 OLT 下发命令）。
    await clickOlt(sidebar, "add-tr069", deviceId);
    await wait(150);
    const tr069Dialog = nodeFor("olt-tr069-dialog");
    check("点「添加TR069管理模板」弹出 TR069 弹窗（模板ID/模板名/ACS地址/ACS账号/ACS密码）",
      tr069Dialog.hidden === false &&
      nodeFor("olt-tr069-name") && nodeFor("olt-tr069-url") &&
      nodeFor("olt-tr069-user") && nodeFor("olt-tr069-pass"));
    setOltField("olt-tr069-id", "3");
    setOltField("olt-tr069-name", "ACS-C");
    setOltField("olt-tr069-url", "http://c.example.com");
    setOltField("olt-tr069-user", "cuser");
    setOltField("olt-tr069-pass", "secret");
    await submitOltTr069Form(root); // 走 #module-view 上的 submit 委托（target=olt-tr069-form）
    await waitFor(() => nodeFor("olt-tr069-dialog").hidden === true, 8000, 250);
    check("提交新增 TR069 模板后弹窗关闭（写操作成功路径）", nodeFor("olt-tr069-dialog").hidden === true);
    // refreshTr069 异步重读列表（连设备采集后的最新配置），等渲染出新增的「ACS-C」。
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("ACS-C"), 8000, 250);
    check("新增 TR069 模板后列表重新读取并包含新模板（ACS-C）",
      nodeFor("olt-detail").innerHTML.includes("ACS-C"));
    await clickOltDetailTab("status");

    // 4) 连接：左栏工具条「连接」（作用于选中设备）→ 右栏显示统计与板卡表
    await clickOlt(sidebar, "connect");
    await waitFor(() => {
      const html = nodeFor("olt-detail").innerHTML;
      return html.includes("连接成功") || html.includes("连接失败");
    });
    const connectedHTML = nodeFor("olt-detail").innerHTML;
    // 面板结构 dump（排查渲染/时序时用）：DEBUG_OLT_UI=1 make olt-ui
    if (process.env.DEBUG_OLT_UI === "1") {
      console.log("---- [debug] #olt-detail after connect ----\n" + connectedHTML.slice(0, 1200) + "\n----");
      console.log("---- [debug] notices ----\n" + nodeFor("notices").innerHTML + "\n----");
    }
    check("连接成功（TELNET，Type=0）：右栏显示版本 / 补丁 / 板卡数",
      connectedHTML.includes("连接成功") && connectedHTML.includes("MA5800V100R019C10") &&
      connectedHTML.includes("版本 / 补丁") && connectedHTML.includes("4 块"),
      connectedHTML.includes("连接失败") ? "出现连接失败面板" : "");
    check("连接结果带「耗时 X.X 秒」（业务方 2026-09-22 要求）",
      /耗时\s*(<b class="olt-elapsed">\d+\.\d<\/b>|\d+\.\d)\s*秒/.test(connectedHTML),
      (connectedHTML.match(/耗时[^秒]*秒/) || [""])[0]);
    check("连接结果带板卡表（槽位 / 板卡 / 描述 / 运行状态 / 温度 / 示意图 / 操作；不显示 PON 口）",
      connectedHTML.includes("H805GPBD") &&
      connectedHTML.includes("描述") && connectedHTML.includes("运行状态") &&
      !connectedHTML.includes("PON 口 (在线/总数)"));
    check("机框示意图渲染板卡素材图（/src/assets/olt/olt_<型号>.png）",
      connectedHTML.includes('class="olt-board-image" src="/src/assets/olt/olt_H805GPBD.png"') &&
      connectedHTML.includes("/src/assets/olt/olt_H801SCUN.png") &&
      connectedHTML.includes("刷新所有板卡状态"));
    // 4.1) 单板「刷新」：单槽刷新 → 端口叠加出现（P0/P1 + 在线/总数），并回报刷新耗时
    await clickOlt(sidebar, "refresh-slot", deviceId, { slot: "1" });
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes("上次刷新耗时"));
    const refreshedHTML = nodeFor("olt-detail").innerHTML;
    check("单板「刷新」后出现端口叠加（P0/P1 + ONT 在线/总数）与刷新耗时",
      refreshedHTML.includes('class="olt-port-name">P0</span>') &&
      refreshedHTML.includes('class="olt-port-dot') &&
      refreshedHTML.includes("上次刷新耗时") && /耗时\s*\d+\.\d\s*秒/.test(refreshedHTML));
    // 4.2) 上行板（槽位 8）单槽刷新 → GE 端口叠加（P0 / GE + 速率 + 双工 + VLAN）
    await clickOlt(sidebar, "refresh-slot", deviceId, { slot: "8" });
    await waitFor(() => nodeFor("olt-detail").innerHTML.includes('class="olt-port-name">P0 / GE'));
    const geHTML = nodeFor("olt-detail").innerHTML;
    check("GE 端口叠加：P0 / GE + 速率 + 双工 + VLAN",
      geHTML.includes('class="olt-port-name">P0 / GE</span>') &&
      geHTML.includes("1000 Mbps") && geHTML.includes("全双工") && geHTML.includes("VLAN1"));
    const connected = await findOltDevice(username);
    check("连接后设备标记为已连接（Automatic=1，左栏状态点为在线）",
      Number(connected?.Automatic || 0) === 1 && nodeFor("olt-device-list").innerHTML.includes("is-online"));

    // 5) 修改：左栏工具条「修改」→ 弹窗回填 → 改备注与连接类型 → 保存
    await clickOlt(sidebar, "edit");
    check("点击左栏「修改」回填原值（IP / 账号 / 类型）",
      nodeFor("olt-field-ip").value === FAKE_OLT_IP &&
      nodeFor("olt-field-username").value === username &&
      nodeFor("olt-field-type").value === "0");
    setOltField("olt-field-type", "1"); // 改成 SSH，验证 0/1 口径与新类型标签
    setOltField("olt-field-comment", "改过的备注");
    await submitOltForm(root);
    await wait(1500);
    const updated = await findOltDevice(username);
    check("修改已生效（备注 + 连接类型 = 1/SSH，保留已连接标记）",
      updated?.Comment === "改过的备注" && Number(updated?.Type) === 1 && Number(updated?.Automatic || 0) === 1,
      `Comment=${updated?.Comment} Type=${updated?.Type}`);
    check("列表按新类型显示 SSH", nodeFor("olt-device-list").innerHTML.includes("SSH"));

    // 6) 列表列结构与交换机台账一致：每行含勾选列（可批量删除）
    const listHTML = String(nodeFor("olt-device-list").innerHTML || "");
    check("列表行含勾选列（与交换机台账列结构一致）",
      listHTML.includes('class="device-list-row__checkbox"') && listHTML.includes('data-olt-action="select-for-delete"'));
    check("面板头部含「全选」与计数（同交换机台账）",
      String(nodeFor("#app").innerHTML || "").includes('id="olt-select-all"') &&
      String(nodeFor("#app").innerHTML || "").includes('id="olt-device-count"'));

    // 7) 批量删除：再建一台 → 勾选 2 台 → 工具条「删除 (2)」→ 二次确认 → 两台都消失
    const secondName = `${username}b`;
    await clickOlt(sidebar, "add");
    setOltField("olt-field-ip", "olt-b.example.com");
    setOltField("olt-field-type", "1");
    setOltField("olt-field-username", secondName);
    setOltField("olt-field-password", "UiPass-456");
    setOltField("olt-field-port", "22");
    setOltField("olt-field-comment", "批量删除联调");
    await submitOltForm(root);
    await wait(1500);
    const second = await findOltDevice(secondName);
    check("第二台设备已新增（批量删除用）", Boolean(second), second ? `Id=${second.Id}` : "未找到");
    if (!second) return;
    createdIds.push(toInt(second.Id, 0));

    await tickOltDevice(sidebar, deviceId, true);
    await tickOltDevice(sidebar, toInt(second.Id, 0), true);
    check("勾选 2 台后工具条显示「删除 (2)」且可用",
      String(nodeFor("olt-delete").textContent) === "删除 (2)" && nodeFor("olt-delete").disabled === false);
    check("勾选部分设备时「全选」呈半选态（与交换机台账一致）",
      nodeFor("olt-select-all").indeterminate === true && nodeFor("olt-select-all").checked === false);

    await clickOlt(sidebar, "delete");
    await wait(300);
    const confirmRoot = nodeFor("confirm-dialog-root");
    check("批量删除弹出二次确认（含选中台数）",
      String(confirmRoot.innerHTML).includes("确认删除选中的 OLT 设备") &&
      String(confirmRoot.innerHTML).includes("2 台"));
    await confirmRoot.dispatch("click", {
      preventDefault() {},
      target: { closest: (selector) => (selector === '[data-action="accept-confirm"]' ? {} : null) }
    });
    await wait(1500);
    const stillFirst = await findOltDevice(username);
    const stillSecond = await findOltDevice(secondName);
    // 删除后被删的两台必须消失；右栏按新口径落到"剩余台账的第一条"，没有剩余设备时才是空态。
    const oltListAfterDelete = await callAPI("/itms/handleConnectOltUser/", { Act: "0" });
    const remainingFirst = Array.isArray(oltListAfterDelete.payload?.data) ? oltListAfterDelete.payload.data[0] : null;
    const detailAfterDelete = String(nodeFor("olt-detail").innerHTML || "");
    check("批量删除后两台设备都已从列表与库中移除",
      !stillFirst && !stillSecond &&
      !nodeFor("olt-device-list").innerHTML.includes(FAKE_OLT_IP) &&
      !nodeFor("olt-device-list").innerHTML.includes("olt-b.example.com") &&
      (remainingFirst
        ? detailAfterDelete.includes(`data-olt-device-id="${toInt(remainingFirst.Id, 0)}"`)
        : detailAfterDelete.includes("暂未选择 OLT 设备")));
    check("删除后勾选与按钮状态复位（「删除」且禁用）",
      String(nodeFor("olt-delete").textContent) === "删除" && nodeFor("olt-delete").disabled === true);
    createdIds.length = 0;
  } finally {
    // 兜底清理：避免联调设备残留在模块库里（删除失败只提示）
    if (createdIds.length) {
      try {
        await callAPI("/itms/handleConnectOltUser/", {
          Act: "3",
          saveData: JSON.stringify({ Ids: createdIds.join(",") })
        });
      } catch {
        /* 清理失败不影响结论 */
      }
    }
  }
}

// tickOltDevice 勾选/取消勾选某台设备（页面对勾选列做 change 委托）。
async function tickOltDevice(sidebar, id, checked) {
  await sidebar.dispatch("change", {
    target: {
      checked,
      closest: (selector) =>
        selector === "[data-olt-action]" ? { dataset: { oltAction: "select-for-delete", id: String(id) } } : null
    }
  });
}

// clickOlt 以页面自己的事件委托方式触发 data-olt-action 按钮（extra 用于补 data-slot 之类的 dataset）。
async function clickOlt(root, action, id = 0, extra = {}) {
  await root.dispatch("click", {
    preventDefault() {},
    target: {
      closest: (selector) =>
        selector === "[data-olt-action]" ? { dataset: { oltAction: action, id: String(id), ...extra } } : null
    }
  });
}

// clickOltDetailTab 以同一套事件委托点击右侧面板的栏目 TAB（data-olt-action="select-detail-tab" + data-tab）。
// 注意：页面把点击委托绑在 #module-view 上（DOM 桩不做事件冒泡），因此必须在该节点派发。
async function clickOltDetailTab(tab) {
  await nodeFor("module-view").dispatch("click", {
    preventDefault() {},
    target: {
      closest: (selector) =>
        selector === "[data-olt-action]"
          ? { dataset: { oltAction: "select-detail-tab", tab } }
          : null
    }
  });
}

// setOltField 给 OLT 表单字段赋值（页面用 document.getElementById 读写，桩里同一实例）。
function setOltField(id, value) {
  nodeFor(id).value = value;
}

// submitOltForm 触发表单提交：在 #module-view 上派发（DOM 桩不做事件冒泡，这里手动以 target=form
// 触发页面在根节点上的 submit 委托，等价于真实浏览器的表单提交冒泡）。
async function submitOltForm(root) {
  const form = root.querySelector("#olt-device-form");
  form.id = "olt-device-form"; // 桩节点没有真实 id 属性（真实 DOM 由 HTML 提供），这里补上以对齐委托判断
  await root.dispatch("submit", { preventDefault() {}, target: form });
}

// submitOltDBAForm 提交「添加DBA模板」表单（与 submitOltForm 同一套委托路径，target 换成 olt-dba-form）。
async function submitOltDBAForm(root) {
  const form = root.querySelector("#olt-dba-form");
  form.id = "olt-dba-form";
  await root.dispatch("submit", { preventDefault() {}, target: form });
}

// submitOltLineForm 提交「添加ONT线路模板」表单（与 submitOltDBAForm 同一套委托路径，target 换成 olt-line-form）。
async function submitOltLineForm(root) {
  const form = root.querySelector("#olt-line-form");
  form.id = "olt-line-form";
  await root.dispatch("submit", { preventDefault() {}, target: form });
}

// submitOltSrvForm 提交「添加ONT业务模板」表单（同一套委托路径，target 换成 olt-srv-form）。
async function submitOltSrvForm(root) {
  const form = root.querySelector("#olt-srv-form");
  form.id = "olt-srv-form";
  await root.dispatch("submit", { preventDefault() {}, target: form });
}

// submitOltTr069Form 提交「添加TR069管理模板」表单（同一套委托路径，target 换成 olt-tr069-form）。
async function submitOltTr069Form(root) {
  const form = root.querySelector("#olt-tr069-form");
  form.id = "olt-tr069-form";
  await root.dispatch("submit", { preventDefault() {}, target: form });
}

// findOltDevice 通过 Act=0 独立核对某账号是否在库里（黑盒校验界面操作真的落库）。
async function findOltDevice(username) {
  const listed = await callAPI("/itms/handleConnectOltUser/", { Act: "0" });
  const users = Array.isArray(listed.payload?.data) ? listed.payload.data : [];
  return users.find((item) => item.Username === username) || null;
}

// ── D. 刷新页面与所选菜单同步 ──
//
// 回归场景（业务方反馈）：刷新页面时左侧焦点停在所选菜单（hash 决定），
// 但提示却是"完成 交换机工作台数据已刷新。" —— 因为 createApp() 末尾无条件跑了交换机工作台刷新。
// 现在改为"只初始化当前选中的模块"，这里用再次调用 createApp（等价于 F5 重新初始化）来断言。
async function checkRefreshSyncsWithActiveModule() {
  section("D. 刷新页面与所选菜单同步（hash = #module=olt / #module=switch）");
  const { createApp } = await import(pathToFileURL(join(REPO_ROOT, "web", "src", "app.js")).href);

  // 场景 1：刷新时 hash 停在 OLT → 只初始化 OLT，不出现交换机提示
  globalThis.window.location.hash = "#module=olt";
  createApp(nodeFor("#app"));
  await wait(2500);
  check("刷新后左栏停在 OLT 台账（与 hash 同步）",
    nodeFor("olt-ledger-sidebar").hidden === false && nodeFor("switch-ledger-sidebar").hidden === true);
  const oltNotices = String(nodeFor("notices").innerHTML || "");
  check("刷新后不出现「交换机工作台数据已刷新」提示", !oltNotices.includes("交换机工作台数据已刷新"), oltNotices.slice(0, 120));
  check("刷新后 OLT 页按需加载自己的列表", String(nodeFor("olt-device-list").innerHTML || "").length > 0);

  // 场景 2：刷新时 hash 停在交换机 → 正常刷新工作台并给出提示（行为不变）
  globalThis.window.location.hash = "#module=switch";
  createApp(nodeFor("#app"));
  await wait(3000);
  check("刷新到交换机管理：左栏显示交换机台账、右栏显示设备视图",
    nodeFor("switch-ledger-sidebar").hidden === false && nodeFor("olt-ledger-sidebar").hidden === true &&
    nodeFor("switch-view").hidden === false);
  check("刷新到交换机管理：工作台刷新提示出现（交换机路径行为不变）",
    String(nodeFor("notices").innerHTML || "").includes("交换机工作台数据已刷新"));
}

// ── 主流程 ──
let fakeOlt = null;
try {
  console.log(`目标前端壳/骨架：${BASE}；假 OLT：${FAKE_OLT_IP}:${FAKE_OLT_PORT}`);
  fakeOlt = await startFakeOlt();
  await checkInterface();
  await checkEndpoints();
  await checkOltDevicePage();
  await checkRefreshSyncsWithActiveModule();
} catch (error) {
  check("工具执行异常", false, error.message);
} finally {
  if (fakeOlt) {
    fakeOlt.kill("SIGTERM");
    console.log(`\n提示：本次联调会在后端模块文件目录下生成假设备缓存（<DataDir>/olt/${FAKE_OLT_IP}），可自行清理。`);
  }
}

console.log(`\n结果：失败 ${failures} 项`);
process.exit(failures === 0 ? 0 : 1);
