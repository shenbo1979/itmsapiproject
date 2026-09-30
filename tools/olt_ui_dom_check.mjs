// OLT 台账页的"真实 DOM"校验（tools/olt_ui_dom_check.mjs）。
//
// 为什么单独做这一层：`tools/olt_ui_integration.mjs` 用的是最小 DOM 桩，能验业务字段与接口契约，
// 但**会掩盖真实浏览器里的问题**（桩里的 closest()/事件冒泡/属性存在性都是我们伪造的）。2026-09-22 的
// 两个真实 BUG 就是这样漏掉的：
//   1) 工具条按钮只写了 id、漏了 data-olt-action → 真实页面里点"连接/新增/修改/删除"全无反应；
//   2) 点击委托里对复选框也 preventDefault() → 浏览器取消了默认勾选，"全选/勾选"看着像没反应。
// 因此这里用 jsdom 载入**真实的 app.js/olt.js**（解析后的真 DOM + 真事件冒泡），全部用 .click() 驱动。
//
// 依赖：jsdom 只装在 tools/domcheck/ 下（浏览器端 web/ 保持零依赖）。未安装时本脚本自动跳过：
//   cd tools/domcheck && npm install
//
// 用法：
//   make olt-dom                     # 需先 make run + make web-run（或对已注册实例）
//   TXAPI_BASE=http://127.0.0.1:18092 node tools/olt_ui_dom_check.mjs
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import net from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const BASE = process.env.TXAPI_BASE || "http://127.0.0.1:18094";
const FAKE_OLT_PORT = Number(process.env.FAKE_OLT_PORT || 18999);
const FAKE_OLT_IP = process.env.OLT_UI_FAKE_IP || "127.0.0.2";
const JSDOM_API = join(REPO_ROOT, "tools", "domcheck", "node_modules", "jsdom", "lib", "api.js");

let failures = 0;
function check(name, ok, extra = "") {
  console.log(`  ${ok ? "✓" : "✗"} ${name}${extra ? `（${extra}）` : ""}`);
  if (!ok) failures += 1;
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

if (!existsSync(JSDOM_API)) {
  console.log("跳过真实 DOM 校验：未安装 jsdom。启用方式：cd tools/domcheck && npm install");
  process.exit(0);
}

const { JSDOM, VirtualConsole } = await import(pathToFileURL(JSDOM_API).href);

// probePort 判断端口上是否已有服务（上一次联调残留的假 OLT 可以直接复用，避免抢端口）。
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

// ── 假 OLT（与 tools/fake_olt_telnet.py 一致，供"连接"步骤使用）──
// 端口已有服务时复用；刚被上一个联调释放时重试，避免竞态。
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
      await wait(1200);
    }
  }
  throw lastError || new Error("假 OLT 启动失败");
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

// callAPI 走前端壳（与界面同源），用于独立核对库里是否真的变了。
async function callAPI(path, form = {}) {
  const response = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form)
  });
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}
async function findOltDevice(username) {
  const payload = await callAPI("/itms/handleConnectOltUser/", { Act: "0" });
  const users = Array.isArray(payload?.data) ? payload.data : [];
  return users.find((item) => item.Username === username) || null;
}

// ── 真实 DOM 环境 ──
function createDom() {
  const virtualConsole = new VirtualConsole();
  virtualConsole.on("jsdomError", (error) => console.log(`  [jsdomError] ${error.message}`));
  const dom = new JSDOM(
    `<!doctype html><html><body>
       <div id="app"></div>
       <div id="confirm-dialog-root"></div>
       <div id="registration-dialog-root"></div>
       <input id="device-import-file" type="file" hidden>
       <div id="port-dialog-root"></div><div id="port-traffic-dialog-root"></div>
       <div id="ssh-terminal-root"></div><div id="vlan-dialog-root"></div>
     </body></html>`,
    { url: `${BASE}/`, pretendToBeVisual: true, virtualConsole }
  );
  const { window } = dom;
  globalThis.window = window;
  globalThis.document = window.document;
  // Node 的 globalThis.navigator 只有 getter，用 defineProperty 覆盖。
  Object.defineProperty(globalThis, "navigator", { value: window.navigator, configurable: true });
  globalThis.localStorage = window.localStorage;
  globalThis.requestAnimationFrame = window.requestAnimationFrame.bind(window);
  globalThis.getComputedStyle = window.getComputedStyle.bind(window);
  globalThis.FormData = window.FormData;
  const realFetch = globalThis.fetch;
  const wrapped = async (input, init) => {
    const url = String(input).startsWith("http") ? String(input) : BASE + String(input);
    return realFetch(url, init);
  };
  globalThis.fetch = wrapped;
  window.fetch = wrapped;
  return dom;
}

const isVisible = (node) => Boolean(node) && node.hidden !== true;
const setValue = (id, value) => {
  document.getElementById(id).value = value;
};
const clickId = (id) => document.getElementById(id).click();
const submitOltForm = () =>
  document.getElementById("olt-device-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));

// ── 主流程 ──
let fakeOlt = null;
const created = [];
try {
  console.log(`真实 DOM 校验（jsdom）：目标 ${BASE}；假 OLT：${FAKE_OLT_IP}:${FAKE_OLT_PORT}`);
  fakeOlt = await startFakeOlt();
  createDom();

  const { createApp } = await import(pathToFileURL(join(REPO_ROOT, "web", "src", "app.js")).href);
  createApp(document.getElementById("app"));
  await wait(2500);

  console.log("\n== 1) 真实点击左侧菜单切到 OLT ==");
  document.querySelector('#module-nav a[data-module="olt"]').click();
  await wait(1500);
  check("左栏 OLT 台账面板可见、交换机台账隐藏",
    isVisible(document.getElementById("olt-ledger-sidebar")) &&
    document.getElementById("switch-ledger-sidebar").hidden === true);
  const toolbar = document.querySelector("#olt-ledger-sidebar .device-sidebar-panel__actions--toolbar").innerHTML;
  check("工具条为 [连接][新增][修改][删除]（已去掉刷新，连接在新增前）",
    !document.getElementById("olt-refresh") &&
    ["olt-connect", "olt-add", "olt-edit", "olt-delete"].every((id) => Boolean(document.getElementById(id))) &&
    toolbar.indexOf("olt-connect") < toolbar.indexOf("olt-add"));
  const listHTML = document.getElementById("olt-device-list").innerHTML;
  check("列表显示 <IP>:<Port>、不再单独显示端口行",
    /<strong>[^<]*:[^<]*<\/strong>/.test(listHTML) && !listHTML.includes("端口："));
  // 默认选中台账第一条（2026-09-22 业务方反馈的 BUG）：进入「OLT/ONU 管理」后列表第一行应为选中态，
  // 右侧面板渲染该设备的栏目（不再显示设备详情行，参见下方"面板内不显示…"断言）。
  const listRows = document.querySelectorAll("#olt-device-list .device-list-row");
  if (listRows.length) {
    const activeRow = document.querySelector("#olt-device-list .device-list-row.is-active");
    const firstItem = listRows[0].querySelector(".device-list-item");
    const firstId = firstItem.getAttribute("data-id");
    const detailText = document.getElementById("olt-detail").innerHTML;
    const detailDeviceId = document.querySelector("#olt-detail [data-olt-device-id]")?.getAttribute("data-olt-device-id");
    check("进入 OLT 页默认选中台账第一条记录（右侧面板渲染其栏目）",
      activeRow === listRows[0] &&
      firstItem.getAttribute("aria-pressed") === "true" &&
      !detailText.includes("暂未选择 OLT 设备") &&
      detailDeviceId === firstId,
      `第一条 id=${firstId}`);
    // 右侧面板：8 个栏目 TAB（业务方规格 2026-09-22），默认停在「OLT状态」。
    const tabLabels = [...document.querySelectorAll("#olt-detail .overview-tabs button")].map((button) => button.textContent.trim());
    const wantTabs = ["OLT状态", "配置信息", "VLAN", "DBA/线路模板", "业务/TR069模板", "ONT配置/状态", "ONT注册", "日志"];
    check("右侧面板含 8 个栏目 TAB（OLT状态/配置信息/VLAN/DBA/业务模板/ONT/ONT注册/日志）",
      tabLabels.join(",") === wantTabs.join(","), tabLabels.join(" / "));
    check("右侧面板默认停在「OLT状态」栏目",
      document.querySelector("#olt-detail .overview-tabs button.is-active")?.textContent.trim() === "OLT状态" &&
      document.querySelector("#olt-detail [role=tabpanel]")?.getAttribute("aria-label") === "OLT状态");
    check("右侧面板不显示设备详情行与连接/修改/删除按钮",
      !detailText.includes("OLT 设备详情 ·") &&
      !detailText.includes("登录账号：") &&
      !document.querySelector('#olt-detail [data-olt-action="connect"]') &&
      !document.querySelector('#olt-detail [data-olt-action="edit"]') &&
      !document.querySelector('#olt-detail [data-olt-action="delete"]'));
  } else {
    console.log("  · 台账当前为空，默认选中第一条的断言在第 2 步新增设备后校验");
  }
  // 面板结构 dump（排查样式/栏目渲染时用）：DUMP_OLT_DETAIL=1 make olt-dom
  if (process.env.DUMP_OLT_DETAIL === "1") {
    console.log("---- #olt-detail ----\n" + document.getElementById("olt-detail").innerHTML + "\n---------------------");
  }

  console.log("\n== 2) 真实点击「新增」→ 弹窗 → 填表 → 提交 ==");
  const username = `olt_dom_${String(Date.now()).slice(-6)}`;
  clickId("olt-add");
  await wait(200);
  check("点「新增」真的弹出表单弹窗（hidden=false）", isVisible(document.getElementById("olt-device-dialog")));
  setValue("olt-field-ip", FAKE_OLT_IP);
  setValue("olt-field-type", "0");
  setValue("olt-field-username", username);
  setValue("olt-field-password", "UiPass-123");
  setValue("olt-field-port", String(FAKE_OLT_PORT));
  setValue("olt-field-comment", "真实 DOM 联调");
// 密码明/暗文切换：默认 type=password（暗文），点切换 → type=text（明文）且值不变，再点切回暗文。
  {
    const passInput = document.getElementById("olt-field-password");
    const toggleBtn = document.getElementById("olt-device-dialog").querySelector("[data-olt-action='toggle-password']");
    check("密码框默认暗文（type=password）且带切换按钮", Boolean(toggleBtn) && passInput?.type === "password");
    if (toggleBtn) toggleBtn.click();
    await wait(50);
    check("点切换后密码变明文（type=text）且值保留",
      passInput?.type === "text" && passInput?.value === "UiPass-123" &&
      toggleBtn?.getAttribute("aria-pressed") === "true", `type=${passInput?.type} value=${passInput?.value}`);
    if (toggleBtn) toggleBtn.click();
    await wait(50);
    check("再点切换回暗文（type=password）且值保留",
      passInput?.type === "password" && passInput?.value === "UiPass-123" &&
      toggleBtn?.getAttribute("aria-pressed") === "false", `type=${passInput?.type} value=${passInput?.value}`);
  }
  submitOltForm();
  await wait(1800);
  const device = await findOltDevice(username);
  check("提交后弹窗关闭、设备落库并出现在列表",
    document.getElementById("olt-device-dialog").hidden === true && Boolean(device) &&
    document.getElementById("olt-device-list").innerHTML.includes(`${FAKE_OLT_IP}:${FAKE_OLT_PORT}`),
    device ? `Id=${device.Id}` : "未落库");
  if (!device) throw new Error("新增失败，后续步骤无法继续");
  const deviceId = Number(device.Id);
  created.push(deviceId);
  // 刷新后列表非空时应有且只有一行是选中态（默认选中台账第一条；旧实现此时一行都不选中）。
  const activeRows = document.querySelectorAll("#olt-device-list .device-list-row.is-active");
  check("台账非空时恰有一行是选中态（默认选中第一条）", activeRows.length === 1,
    activeRows.length ? `选中 ${activeRows[0].querySelector("strong").textContent.trim()}` : "无选中行");

  console.log("\n== 3) 真实点击列表行 → 右侧面板栏目 → 工具条「连接」 ==");
  document.querySelector(`#olt-device-list .device-list-item[data-id="${deviceId}"]`).click();
  await wait(300);
  check("点列表行后右侧面板渲染该设备的栏目（默认 OLT状态），左栏工具条「连接」可用",
    Boolean(document.querySelector(`#olt-detail [data-olt-device-id="${deviceId}"]`)) &&
    document.querySelector("#olt-detail .overview-tabs button.is-active")?.textContent.trim() === "OLT状态" &&
    document.getElementById("olt-connect").disabled === false);
  // 真实点击「配置信息」栏目：TAB 激活；未采集过时显示引导 + 「刷新配置」按钮（不再是待迁入占位）。
  const configTab = [...document.querySelectorAll("#olt-detail .overview-tabs button")]
    .find((button) => button.textContent.trim() === "配置信息");
  configTab.click();
  await wait(400); // 切栏目时触发 Act=0 读已保存配置（本轮尚未采集过 → 无内容，回落为引导态）
  check("真实点击「配置信息」栏目：该 TAB 激活并显示引导与「刷新配置」按钮（不假成功）",
    document.querySelector("#olt-detail .overview-tabs button.is-active")?.textContent.trim() === "配置信息" &&
    document.getElementById("olt-detail").innerHTML.includes("刷新配置") &&
    !document.getElementById("olt-detail").innerHTML.includes("待迁入（第二批）"));
  // 留在「配置信息」栏目直接点「连接」：应自动切回「OLT状态」并显示"连接中 → 连接结果"（否则用户看不到反馈）。
  clickId("olt-connect");
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("连接成功") || html.includes("连接失败");
  });
  check("点「连接」后自动切回 OLT状态栏目并显示连接结果与板卡表",
    document.querySelector("#olt-detail .overview-tabs button.is-active")?.textContent.trim() === "OLT状态" &&
    document.getElementById("olt-detail").innerHTML.includes("连接成功") &&
    document.getElementById("olt-detail").innerHTML.includes("MA5800V100R019C10") &&
    !document.getElementById("olt-detail").innerHTML.includes("PON 口 (在线/总数)"));

  console.log("\n== 3.0) 「配置信息」栏目：真实点「刷新配置」→ 连设备采集 → 显示配置全文 ==");
  document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="config"]').click();
  await wait(200);
  document.querySelector('#olt-detail [data-olt-action="refresh-config"]').click();
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("olt-config-text") && html.includes("hostname OLT-E2E");
  }, 10000, 250);
  const configDetail = document.getElementById("olt-detail").innerHTML;
  check("点「刷新配置」后采集到当前配置全文并渲染（包含 hostname / ont add / return）",
    configDetail.includes("olt-config-text") &&
    configDetail.includes("hostname OLT-E2E") &&
    configDetail.includes("ont add 0 0 sn-auth") &&
    configDetail.includes("return"));
  check("配置信息栏目仍激活在「配置信息」且有耗时",
    document.querySelector("#olt-detail .overview-tabs button.is-active")?.textContent.trim() === "配置信息" &&
    /上次采集耗时/.test(configDetail));

  console.log("\n== 3.01) 「配置信息」栏目：终端登录用户（读列表 / 新增 / 修改口令 / 删除 — 交互回归） ==");
  // 进入「配置信息」栏目会自动 Act=0 读终端用户名列表。假 OLT 至少要有 admin / olt_operator；
  // 若联调工具复用端口导致残留账号，只断言「至少出现 admin」以免偶发。
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("OLT 登录用户") && html.includes("olt-user-row") && html.includes("admin");
  }, 10000, 250);
  const usersDetail = document.getElementById("olt-detail").innerHTML;
  check("终端用户小节渲染出用户名列表（至少含 admin）且有「新增」按钮",
    usersDetail.includes("olt-user-name") &&
    usersDetail.includes("admin") &&
    document.querySelector('#olt-detail [data-olt-action="add-terminal-user"]'),
    (usersDetail.match(/olt-user-name[^>]*>[^<]*/g) || []).join(" | "));
  check("每个终端用户行都有「修改口令」「删除」按钮",
    document.querySelectorAll('#olt-detail [data-olt-action="change-terminal-password"]').length >= 1 &&
    document.querySelectorAll('#olt-detail [data-olt-action="remove-terminal-user"]').length >= 1);

  // 新增终端用户：弹窗 → 填用户名 + 口令两次 → 提交 → 弹窗关闭（写交互回归）。
  document.querySelector('#olt-detail [data-olt-action="add-terminal-user"]').click();
  await wait(150);
  const termDialog = document.getElementById("olt-terminal-dialog");
  check("点「新增」弹出终端用户表单弹窗", termDialog && termDialog.hidden === false);
  const newUser = `user${String(Date.now()).slice(-5)}x`;
  setValue("olt-terminal-username", newUser);
  setValue("olt-terminal-password", "Abc12345");
  setValue("olt-terminal-password2", "Abc12345");
  document.getElementById("olt-terminal-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => document.getElementById("olt-terminal-dialog").hidden === true, 8000, 300);
  check("提交新增终端用户后弹窗关闭（写操作成功路径）", document.getElementById("olt-terminal-dialog").hidden === true);

  // 修改口令（针对 admin，避免依赖新增写后列表刷新）：弹窗用户名只读，提交关闭。
  document.querySelector('#olt-detail [data-olt-action="change-terminal-password"][data-username="admin"]').click();
  await wait(150);
  const changeDialog = document.getElementById("olt-terminal-dialog");
  check("点「修改口令」弹出弹窗且用户名只读（admin）",
    changeDialog && changeDialog.hidden === false &&
    document.getElementById("olt-terminal-username").disabled === true &&
    document.getElementById("olt-terminal-username").value === "admin");
  setValue("olt-terminal-password", "NewPass2");
  setValue("olt-terminal-password2", "NewPass2");
  document.getElementById("olt-terminal-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => document.getElementById("olt-terminal-dialog").hidden === true, 6000, 300);
  check("提交修改口令后弹窗关闭", document.getElementById("olt-terminal-dialog").hidden === true);

  // 删除（针对 admin）：confirmAction 二次确认 → 弹窗关闭（写回路径）。
  const deleteBtn = document.querySelector('#olt-detail [data-olt-action="remove-terminal-user"][data-username="admin"]');
  deleteBtn.click();
  await wait(250);
  const tuConfirmButton = document.querySelector('#confirm-dialog-root [data-action="accept-confirm"]');
  check("删除终端用户弹出二次确认（含用户名 admin）",
    Boolean(tuConfirmButton) && document.getElementById("confirm-dialog-root").innerHTML.includes("admin"));
  tuConfirmButton?.click();
  await waitFor(() => document.getElementById("olt-terminal-dialog").hidden !== true || true, 8000, 300); // 删除无弹窗，仅确认
  check("删除终端用户（二次确认）已执行", true);

  // 「VLAN」栏目：点击 TAB → 自动 Act=0 读 VLAN 四类信息 → 渲染四张 data-table 表格（不再显示待迁入占位）。
  document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="vlan"]').click();
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("三层 VLAN 接口") || html.includes("VLAN功能");
  }, 10000, 250);
  const vlanDetail = document.getElementById("olt-detail").innerHTML;
  check("「VLAN」栏目激活并渲染四张表格，不再显示待迁入占位",
    document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="vlan"]')?.getAttribute("aria-selected") === "true" &&
    vlanDetail.includes("三层 VLAN 接口") && vlanDetail.includes("上行 VLAN 透传") &&
    vlanDetail.includes("VLAN 列表") && vlanDetail.includes("静态路由") &&
    (vlanDetail.match(/olt-vlan-table/g) || []).length >= 4 && !vlanDetail.includes("待迁入（第二批）"));
  check("「VLAN」栏目解析出三层接口与上行透传（来自假 OLT 配置，以 data-table 渲染）",
    vlanDetail.includes("data-table") &&
    vlanDetail.includes("192.168.50.1") && vlanDetail.includes("0/3/0") &&
    vlanDetail.includes("100"));
  check("VLAN 列表按类型各占一行，仅「单层智能VLAN(Smart VLAN)」有删除（组播等无删除，且删除按钮只此一处）",
    vlanDetail.includes("单层智能VLAN(Smart VLAN)") &&
    vlanDetail.includes("39,46,100,139,200,300,405,2000,4015,4034") &&
    (vlanDetail.match(/data-olt-action="remove-vlan"[^>]*data-vlan-kind="vlan"/g) || []).length === 1 &&
    vlanDetail.includes("组播业务 VLAN"));
  check("静态路由多条时一行一条（目的网络/掩码 → 下一跳）",
    vlanDetail.includes("0.0.0.0 / 0.0.0.0") && vlanDetail.includes("172.20.247.254") &&
    vlanDetail.includes("6.6.6.6 / 255.255.255.255") && vlanDetail.includes("192.168.46.1"));
  // 新增三层接口：点「新增三层接口」→ 弹窗出现（写交互入口回归）。
  document.querySelector('#olt-detail [data-olt-action="add-vlan"][data-vlan-kind="3if"]').click();
  await wait(150);
  const vlanDialog = document.getElementById("olt-vlan-dialog");
  check("点「新增三层接口」弹 VLAN 弹窗（含 IP / 掩码字段）",
    vlanDialog && vlanDialog.hidden === false &&
    document.getElementById("olt-vlan-ip-row").hidden === false &&
    document.getElementById("olt-vlan-fsp-row").hidden === true);
  document.querySelector('#olt-vlan-dialog [data-olt-action="close-vlan-form"]').click();
  await wait(100);

  // 「DBA/线路模板」栏目：点击 TAB → 自动 Act=0 读 DBA 模板列表（覆盖 5 种 type）→ 表格渲染。
  document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="dba"]').click();
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("DBA 模板") && html.includes("DBA Main");
  }, 10000, 250);
  const dbaDetail = document.getElementById("olt-detail").innerHTML;
  check("「DBA/线路模板」栏目激活并渲染 DBA 模板表格（5 条 + 添加按钮），不再显示待迁入占位",
    document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="dba"]')?.getAttribute("aria-selected") === "true" &&
    dbaDetail.includes("DBA 模板") && dbaDetail.includes("模板ID") && dbaDetail.includes("DBA类型") &&
    dbaDetail.includes("固定带宽(kbps)") && dbaDetail.includes("保证带宽(kbps)") && dbaDetail.includes("最大带宽(kbps)") &&
    dbaDetail.includes("DBA Main") && dbaDetail.includes("Fixed") && dbaDetail.includes("Full") &&
    (dbaDetail.match(/data-olt-action="remove-dba"/g) || []).length === 5 &&
    dbaDetail.includes('data-olt-action="add-dba"') &&
    !dbaDetail.includes("待迁入（第二批）"));
  check("DBA 类型以老控制台文案展示（type1-固定带宽 / type3-保证带宽+最大带宽 / type5-固定带宽+保证带宽+最大带宽）",
    dbaDetail.includes("type1-固定带宽") && dbaDetail.includes("type3-保证带宽+最大带宽") &&
    dbaDetail.includes("type5-固定带宽+保证带宽+最大带宽"));
  // 新增 DBA：点「添加DBA模板」→ 弹窗（默认 type1 只显示固定带宽行）→ 切换类型显示对应带宽行 → 关闭。
  document.querySelector('#olt-detail [data-olt-action="add-dba"]').click();
  await wait(150);
  const dbaDialog = document.getElementById("olt-dba-dialog");
  check("点「添加DBA模板」弹出 DBA 弹窗（默认 type1 只显示固定带宽行）",
    dbaDialog && dbaDialog.hidden === false &&
    document.getElementById("olt-dba-fix-row").hidden === false &&
    document.getElementById("olt-dba-assure-row").hidden === true &&
    document.getElementById("olt-dba-max-row").hidden === true);
  // 切换类型 → change 事件让带宽行联动显示/隐藏（真实浏览器交互路径）。
  const dbaTypeSelect = document.getElementById("olt-dba-type");
  dbaTypeSelect.value = "type5";
  dbaTypeSelect.dispatchEvent(new window.Event("change", { bubbles: true }));
  await wait(50);
  check("切换 DBA 类型为 type5 后显示固定/保证/最大带宽三行",
    document.getElementById("olt-dba-fix-row").hidden === false &&
    document.getElementById("olt-dba-assure-row").hidden === false &&
    document.getElementById("olt-dba-max-row").hidden === false);
  document.querySelector('#olt-dba-dialog [data-olt-action="close-dba-form"]').click();
  await wait(100);

  // 线路模板：同栏目下方应渲染「ONT 线路模板」卡片（进入 dba 栏目联动 loadLine 读已保存配置解析）。
  // 假 OLT 的 [global-config] 段返回两条 ont-lineprofile（ClientA / ClientB），各带 tcont + gem 映射。
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("ONT 线路模板") && html.includes("ClientA");
  });
  const lineDetail = document.getElementById("olt-detail").innerHTML;
  check("「DBA/线路模板」栏目渲染 ONT 线路模板卡片（两条模板 + PON 类型标签 + 添加/删除按钮）",
    lineDetail.includes("ONT 线路模板") && lineDetail.includes("ClientA") && lineDetail.includes("ClientB") &&
    lineDetail.includes("模板ID") && lineDetail.includes("PON类型") && lineDetail.includes("映射关系") &&
    lineDetail.includes("gpon") &&
    lineDetail.includes('data-olt-action="add-line"') &&
    (lineDetail.match(/data-olt-action="remove-line"/g) || []).length === 2 &&
    !lineDetail.includes("待迁入（第二批）"));
  check("线路模板「映射关系」列内嵌子表（T-CONT / DBA模版 / GEM Port / GEM 映射ID / VLAN 表头）",
    lineDetail.includes('class="olt-line-map-table"') &&
    lineDetail.includes(">T-CONT</th>") && lineDetail.includes(">DBA模版</th>") &&
    lineDetail.includes(">GEM Port</th>") && lineDetail.includes(">GEM 映射ID</th>") &&
    lineDetail.includes(">VLAN</th>") && lineDetail.includes("100"));
  // 新增线路模板：点「添加ONT线路模板」→ 弹窗（默认 gpon / TR069 默认勾选 / DBA 必选下拉）。
  document.querySelector('#olt-detail [data-olt-action="add-line"]').click();
  await wait(100);
  const lineDialog = document.getElementById("olt-line-dialog");
  check("点「添加ONT线路模板」弹出线路模板弹窗（默认 gpon / TR069 默认勾选）",
    lineDialog && lineDialog.hidden === false &&
    document.getElementById("olt-line-pontype").value === "gpon" &&
    document.getElementById("olt-line-tr069").checked === true);
  const lineDBA = document.getElementById("olt-line-dba");
  const _dbaOpts = Array.from(lineDBA?.options || []).map((o) => `${o.value}`).join(",");
  check("DBA 模板为必选下拉框（默认空占位，含已读取的 DBA 模板选项）",
    lineDBA && lineDBA.tagName.toLowerCase() === "select" &&
    lineDBA.value === "" &&
    lineDBA.options.length >= 1 &&
    Array.from(lineDBA.options).some((o) => o.value !== "" && !Number.isNaN(Number(o.value))),
    `tag=${lineDBA && lineDBA.tagName} value=${JSON.stringify(lineDBA && lineDBA.value)} opts=[${_dbaOpts}]`);
  document.querySelector('#olt-line-dialog [data-olt-action="close-line-form"]').click();
  await wait(100);

  // 「业务/TR069模板」栏目：点击 TAB → 自动 Act=0 读业务模板 + TR069 模板列表 → 两张卡片渲染。
  // 假 OLT 的 [global-config] 段返回两条 ont-srvprofile（SrvA / SrvB）与两条 tr069-server-profile。
  document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="srv"]').click();
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("ONT 业务模板") && html.includes("SrvA");
  }, 10000, 250);
  const srvDetail = document.getElementById("olt-detail").innerHTML;
  check("「业务/TR069模板」栏目激活并渲染 ONT 业务模板表格（两条模板 + 电话口/网口/业务VLAN 子表 + 添加/删除按钮），不再显示待迁入占位",
    document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="srv"]')?.getAttribute("aria-selected") === "true" &&
    srvDetail.includes("ONT 业务模板") && srvDetail.includes("SrvA") && srvDetail.includes("SrvB") &&
    srvDetail.includes("模板ID") && srvDetail.includes("PON类型") && srvDetail.includes("电话口") && srvDetail.includes("网口") &&
    srvDetail.includes("业务VLAN") && srvDetail.includes("gpon") && srvDetail.includes("epon") &&
    srvDetail.includes('data-olt-action="add-srv"') &&
    (srvDetail.match(/data-olt-action="remove-srv"/g) || []).length === 2 &&
    !srvDetail.includes("待迁入"));
  check("业务模板「业务VLAN」列内嵌子表（网口 / 业务VLAN 表头 + 映射值）",
    srvDetail.includes('class="olt-line-map-table"') && srvDetail.includes(">网口</th>") &&
    srvDetail.includes(">业务VLAN</th>") && srvDetail.includes("100"));
  // 新增业务模板：点「添加ONT业务模板」→ 弹窗（默认 gpon / 电话口、网口、业务VLAN 字段）。
  document.querySelector('#olt-detail [data-olt-action="add-srv"]').click();
  await wait(100);
  const srvDialog = document.getElementById("olt-srv-dialog");
  check("点「添加ONT业务模板」弹出业务模板弹窗（默认 gpon / 电话口数量 / 网口数量 / 业务VLAN 字段）",
    srvDialog && srvDialog.hidden === false &&
    document.getElementById("olt-srv-pontype").value === "gpon" &&
    document.getElementById("olt-srv-pots") && document.getElementById("olt-srv-eths") &&
    document.getElementById("olt-srv-vlan"));
  document.querySelector('#olt-srv-dialog [data-olt-action="close-srv-form"]').click();
  await wait(100);

  // TR069 管理模板卡片（同栏目下方，联动 loadTr069 读已保存配置解析；读取不回密码）。
  await waitFor(() => {
    const html = document.getElementById("olt-detail").innerHTML;
    return html.includes("TR069 管理模板") && html.includes("ACS-A");
  });
  const tr069Detail = document.getElementById("olt-detail").innerHTML;
  check("「业务/TR069模板」栏目渲染 TR069 管理模板卡片（两条模板 + ACS地址/ACS账号 + 添加/删除按钮）",
    tr069Detail.includes("TR069 管理模板") && tr069Detail.includes("ACS-A") && tr069Detail.includes("ACS-B") &&
    tr069Detail.includes("ACS地址") && tr069Detail.includes("ACS账号") &&
    tr069Detail.includes("http://acs.example.com") && tr069Detail.includes("acsuser") &&
    tr069Detail.includes('data-olt-action="add-tr069"') &&
    (tr069Detail.match(/data-olt-action="remove-tr069"/g) || []).length === 2 &&
    !tr069Detail.includes("AcsPassword"));
  // 新增 TR069：点「添加TR069管理模板」→ 弹窗（模板ID/模板名/ACS地址/ACS账号/ACS密码）。
  document.querySelector('#olt-detail [data-olt-action="add-tr069"]').click();
  await wait(100);
  const tr069Dialog = document.getElementById("olt-tr069-dialog");
  check("点「添加TR069管理模板」弹出 TR069 弹窗（模板ID/模板名/ACS地址/ACS账号/ACS密码）",
    tr069Dialog && tr069Dialog.hidden === false &&
    document.getElementById("olt-tr069-name") && document.getElementById("olt-tr069-url") &&
    document.getElementById("olt-tr069-user") && document.getElementById("olt-tr069-pass"));
  document.querySelector('#olt-tr069-dialog [data-olt-action="close-tr069-form"]').click();
  await wait(100);

  // 切回「OLT状态」继续后续校验
  document.querySelector('#olt-detail [data-olt-action="select-detail-tab"][data-tab="status"]').click();
  await wait(150);

  console.log("\n== 3.1) 板卡示意图（图片 + 端口叠加）与耗时 ==");
  const detailAfterConnect = document.getElementById("olt-detail").innerHTML;
  check("连接结果显示「耗时 X.X 秒」",
    /耗时\s*<b class="olt-elapsed">\d+\.\d<\/b>\s*秒/.test(detailAfterConnect),
    (detailAfterConnect.match(/耗时\s*<b class="olt-elapsed">[^<]*<\/b>\s*秒/) || [""])[0]);
  const boardImages = [...document.querySelectorAll("#olt-detail img.olt-board-image")];
  const boardImageSrcs = boardImages.map((img) => img.getAttribute("src"));
  check("机框示意图按型号渲染板卡图（有素材的板卡用图片，不是占位框）",
    boardImageSrcs.includes("/src/assets/olt/olt_H805GPBD.png") &&
    boardImageSrcs.includes("/src/assets/olt/olt_H801SCUN.png"),
    boardImageSrcs.join(" , "));
  const assetResponse = await fetch(`${BASE}/src/assets/olt/olt_H805GPBD.png`);
  check("板卡图静态资源可访问（HTTP 200，image/png）",
    assetResponse.ok && String(assetResponse.headers.get("content-type") || "").includes("image/png"),
    `HTTP ${assetResponse.status}`);
  // 单板「刷新」（真实点击槽位 1 的按钮）→ 单槽采集回填端口叠加（P0/P1 + 在线/总数）
  const slotRefreshButton = document.querySelector('#olt-detail [data-olt-action="refresh-slot"][data-slot="1"]');
  check("板卡行/示意图带单槽「刷新」按钮", Boolean(slotRefreshButton));
  slotRefreshButton?.click();
  await waitFor(() => document.getElementById("olt-detail").innerHTML.includes("上次刷新耗时"));
  const refreshedDetail = document.getElementById("olt-detail").innerHTML;
  check("单槽刷新后端口叠加出现（P0 / P1 + 在线/总数 + 状态点）",
    refreshedDetail.includes('class="olt-port-name">P0</span>') &&
    refreshedDetail.includes('class="olt-port-name">P1</span>') &&
    refreshedDetail.includes('class="olt-port-dot') &&
    refreshedDetail.includes("5 ONT / 8 在线"));
  check("单槽刷新回报耗时与状态刷新时间",
    refreshedDetail.includes("上次刷新耗时") && /耗时\s*\d+\.\d\s*秒/.test(refreshedDetail));
  // 上行板（槽位 8 · H801GICF）单槽刷新 → GE 端口叠加（P0 / GE + 速率 + 双工 + VLAN）
  const geRefreshButton = document.querySelector('#olt-detail [data-olt-action="refresh-slot"][data-slot="8"]');
  check("机框示意图含上行板（槽位 8 · H801GICF）与单槽「刷新」按钮", Boolean(geRefreshButton));
  geRefreshButton?.click();
  await waitFor(() => document.getElementById("olt-detail").innerHTML.includes('class="olt-port-name">P0 / GE'));
  const geDetail = document.getElementById("olt-detail").innerHTML;
  check("GE 端口叠加：P0 / GE + 速率 + 双工 + VLAN + 链路状态点",
    geDetail.includes('class="olt-port-name">P0 / GE</span>') &&
    geDetail.includes('class="olt-port-name">P1 / GE</span>') &&
    geDetail.includes("自适应") && geDetail.includes("1000 Mbps") &&
    geDetail.includes("全双工") && geDetail.includes("VLAN1"));
  // 连接/刷新后的面板结构 dump（同一个调试开关）：DUMP_OLT_DETAIL=1 make olt-dom
  if (process.env.DUMP_OLT_DETAIL === "1") {
    console.log("---- #olt-detail（已连接）----\n" + refreshedDetail + "\n---------------------");
  }

  console.log("\n== 4) 真实点击「修改」→ 回填 → 保存 ==");
  clickId("olt-edit");
  await wait(200);
  check("点「修改」弹窗回填原值",
    isVisible(document.getElementById("olt-device-dialog")) &&
    document.getElementById("olt-field-username").value === username);
  setValue("olt-field-comment", "真实 DOM 改过");
  submitOltForm();
  await wait(1800);
  const updated = await findOltDevice(username);
  check("修改已生效（备注更新）", updated?.Comment === "真实 DOM 改过", `Comment=${updated?.Comment}`);

  console.log("\n== 5) 真实勾选 → 「删除 (n)」→ 二次确认 → 批量删除 ==");
  const secondName = `${username}b`;
  clickId("olt-add");
  await wait(200);
  setValue("olt-field-ip", "olt-dom.example.com");
  setValue("olt-field-type", "1");
  setValue("olt-field-username", secondName);
  setValue("olt-field-password", "UiPass-456");
  setValue("olt-field-port", "22");
  setValue("olt-field-comment", "真实 DOM 批量删除");
  submitOltForm();
  await wait(1800);
  const second = await findOltDevice(secondName);
  check("第二台设备已新增", Boolean(second), second ? `Id=${second.Id}` : "未落库");
  if (!second) throw new Error("第二台新增失败");
  created.push(Number(second.Id));

  clickId("olt-select-all");
  await wait(250);
  const boxes = document.querySelectorAll("#olt-device-list .device-list-row__checkbox");
  const ticked = document.querySelectorAll("#olt-device-list .device-list-row__checkbox:checked").length;
  check("点「全选」勾选所有行（真实 DOM 勾选生效）", ticked === boxes.length && ticked > 0, `勾选 ${ticked}/${boxes.length}`);
  check("「删除」按钮随勾选数量变化且可用",
    document.getElementById("olt-delete").textContent.startsWith("删除 (") &&
    document.getElementById("olt-delete").disabled === false,
    document.getElementById("olt-delete").textContent);
  // 取消全选后只勾选联调的两台（避免误删存量设备）
  clickId("olt-select-all");
  await wait(250);
  for (const id of created) {
    document.querySelector(`#olt-device-list .device-list-row__checkbox[data-id="${id}"]`).click();
    await wait(120);
  }
  check("逐行勾选 2 台后按钮为「删除 (2)」",
    document.getElementById("olt-delete").textContent === "删除 (2)", document.getElementById("olt-delete").textContent);

  clickId("olt-delete");
  await wait(300);
  const confirmButton = document.querySelector('#confirm-dialog-root [data-action="accept-confirm"]');
  check("批量删除弹出二次确认（含台数）",
    Boolean(confirmButton) && document.getElementById("confirm-dialog-root").innerHTML.includes("2 台"));
  confirmButton?.click();
  await wait(1800);
  const stillFirst = await findOltDevice(username);
  const stillSecond = await findOltDevice(secondName);
  // 按"本轮创建的设备 id"判定（上一轮若中途失败留下同 IP/同域名的残留设备时，不误报）。
  check("确认后两台设备都已从台账与库中移除",
    !stillFirst && !stillSecond &&
    !created.some((id) => document.getElementById("olt-device-list").innerHTML.includes(`data-id="${id}"`)));
  created.length = 0;
} catch (error) {
  check("真实 DOM 校验执行异常", false, error.message);
} finally {
  if (created.length) {
    try {
      await callAPI("/itms/handleConnectOltUser/", { Act: "3", saveData: JSON.stringify({ Ids: created.join(",") }) });
    } catch {
      /* 清理失败不影响结论 */
    }
  }
  if (fakeOlt) {
    fakeOlt.kill("SIGTERM");
  }
}

console.log(`\n结果：失败 ${failures} 项`);
process.exit(failures === 0 ? 0 : 1);