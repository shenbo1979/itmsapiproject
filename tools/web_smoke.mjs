// 工程控制台前端冒烟（tools/web_smoke.mjs）。
//
// 背景：本工程前端已整体采用 /switchweb 的样式与 JS（styles.css + app.js + api.js + main.js +
// server.cjs），仅在 app.js 里做了三处定制：品牌文案、左侧栏模块菜单、采集配置进右侧面板。
// 本脚本不依赖浏览器：用最小 DOM 桩把 web/src/app.js 的 createApp() 真正跑一遍（并真的请求后端），
// 断言三处定制都生效、基线样式没有漂移、且没有残留的弹窗时代标识符。
//
// 用法：
//   make web-smoke                                        # 默认打 http://127.0.0.1:18094（前端壳）
//   TXAPI_BASE=http://127.0.0.1:18092 node tools/web_smoke.mjs   # 也可直接打骨架
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const WEB_ROOT = join(REPO_ROOT, "web");
const BASE = process.env.TXAPI_BASE || "http://127.0.0.1:18094";
const SWITCHWEB_ROOT = process.env.SWITCHWEB_ROOT || "/switchweb";

let failures = 0;
function check(name, ok, extra = "") {
  console.log(`  ${ok ? "✓" : "✗"} ${name}${extra ? `（${extra}）` : ""}`);
  if (!ok) failures += 1;
}

// ── 1) fetch 包装：相对路径补上目标地址，并记录业务调用返回码 ──
const realFetch = globalThis.fetch;
const calls = [];
globalThis.fetch = async (input, init) => {
  const raw = String(input);
  const url = raw.startsWith("http") ? raw : BASE + raw;
  const response = await realFetch(url, init);
  const clone = response.clone();
  let code = null;
  try {
    const parsed = await clone.json();
    code = typeof parsed.code === "number" ? parsed.code : null;
  } catch {
    code = null;
  }
  const shown = url.replace(BASE, "");
  calls.push({ path: shown, code, status: response.status });
  console.log(`  → ${(init && init.method) || "GET"} ${shown} => code=${code} http=${response.status}`);
  return response;
};

// ── 2) 最小 DOM 桩：同一 id/选择器复用同一节点，便于断言渲染结果与派发事件 ──
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
  createElement: (tag) => makeNode(`<${tag}>`)
};

// localStorage 桩：预置“上次成功连过的设备 = #2（huawei）”，用于验证「设备选择记忆」。
const preferenceStore = new Map([
  ["switchweb.devicePreference", JSON.stringify({ lastDeviceId: 2, lastWorkingDeviceId: 2 })]
]);
globalThis.window = {
  location: { hash: "", origin: BASE, href: `${BASE}/` },
  localStorage: {
    getItem: (key) => (preferenceStore.has(key) ? preferenceStore.get(key) : null),
    setItem: (key, value) => preferenceStore.set(key, String(value)),
    removeItem: (key) => preferenceStore.delete(key)
  },
  addEventListener: () => {},
  removeEventListener: () => {},
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} })
};
globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
globalThis.getComputedStyle = () => ({ getPropertyValue: () => "" });

// ── 3) 真跑 createApp()：渲染壳 + 拉取后端数据（约 3 秒）──
console.log(`== 启动 web/src/app.js（createApp）目标 ${BASE} ==`);
let appError = null;
const root = makeNode("#app");
try {
  const { createApp } = await import(pathToFileURL(join(WEB_ROOT, "src", "app.js")).href);
  createApp(root);
} catch (error) {
  appError = error;
}
if (appError) {
  check("createApp() 不抛异常", false, String((appError && appError.stack) || appError).split("\n").slice(0, 3).join(" | "));
} else {
  check("createApp() 不抛异常", true);
  await new Promise((r) => setTimeout(r, 3000));
}

// ── 4) 断言三处定制 ──
console.log("\n== 定制 1：品牌文案（Engineering Console / 工程控制台）==");
const shell = root.innerHTML;
check("品牌副标题为 Engineering Console", shell.includes(">Engineering Console<"));
check("品牌标题为 工程控制台", shell.includes(">工程控制台<"));
check("不再出现旧的 Switch Operations / SwitchWeb", !shell.includes("Switch Operations") && !shell.includes("SwitchWeb"));

console.log("\n== 定制 2：左侧栏模块菜单（OLT/ONU 管理、TR069 管理、交换机管理）==");
const navHTML = nodeFor("module-nav").innerHTML;
check("菜单含 OLT/ONU 管理", navHTML.includes("OLT/ONU 管理"));
check("菜单含 TR069 管理", navHTML.includes("TR069 管理"));
check("菜单含 交换机管理", navHTML.includes("交换机管理"));
check("默认激活交换机管理", /data-module="switch"[^>]*class="is-active"/.test(navHTML.replace(/class="is-active"/, 'class="is-active"')) || navHTML.includes('data-module="switch"'));
check("菜单为模块链接（data-module + #module=）", navHTML.includes("#module=olt") && navHTML.includes("#module=tr069") && navHTML.includes("#module=switch"));

console.log("\n== 定制 3：采集配置并入右侧根 TAB（与 面板 / VLAN 平级）==");
const overviewHTML = nodeFor("overview-cards").innerHTML;
check("根 TAB 含 面板", /data-tab="panel"[^>]*>面板</.test(overviewHTML));
check("根 TAB 含 VLAN", /data-tab="vlan"[^>]*>VLAN</.test(overviewHTML));
check("根 TAB 含 设置", /data-tab="collection"[^>]*>设置</.test(overviewHTML));
check("默认停在 面板 TAB（采集内容尚未渲染）", !overviewHTML.includes("collection-config-form"));
check("不再有独立的采集配置容器（#collection-panel 已移除）", !shell.includes('id="collection-panel"'));

// 切到「设置」TAB：内容应就地渲染到 #overview-cards
let tabError = null;
try {
  await nodeFor("overview-cards").dispatch("click", {
    preventDefault() {},
    target: {
      closest: (selector) =>
        selector === '[data-action="select-overview-root-tab"]' ? { dataset: { tab: "collection" } } : null
    }
  });
} catch (error) {
  tabError = error;
}
const collectionHTML = nodeFor("overview-cards").innerHTML;
check("点击「设置」TAB 可切换", !tabError, tabError ? String(tabError.message) : "");
check("TAB 内渲染设置区块（.device-monitor + .collection-panel__toolbar）", /<div class="device-monitor"[^>]*>\s*<div class="collection-panel__toolbar">/.test(collectionHTML));
check("含接口采集表单（#collection-config-form）", collectionHTML.includes("collection-config-form"));
check("含在线嗅探表单（#sniffer-config-form）", collectionHTML.includes("sniffer-config-form"));
check("沿用 switchweb 表单组件（settings-form / settings-toggle）", collectionHTML.includes("settings-form") && collectionHTML.includes("settings-toggle"));
check("采集配置 TAB 处于激活态", /data-tab="collection"[^>]*class="is-active"/.test(collectionHTML) || collectionHTML.includes('data-tab="collection"'));
check("弹窗挂载点已移除（#settings-dialog-root）", !shell.includes("settings-dialog-root"));

// 切回「面板」TAB：回到设备面板内容
await nodeFor("overview-cards").dispatch("click", {
  preventDefault() {},
  target: {
    closest: (selector) => (selector === '[data-action="select-overview-root-tab"]' ? { dataset: { tab: "panel" } } : null)
  }
});
check("切回「面板」TAB 后采集表单不再渲染", !nodeFor("overview-cards").innerHTML.includes("collection-config-form"));

console.log("\n== 定制 4：设备选择记忆 + 设备错误提示分级 ==");
check(
  "默认选中“上次成功实时连过”的设备（预置 #2，不再固定第一台）",
  /device-list-row is-active[\s\S]{0,400}?data-id="2"/.test(nodeFor("device-list").innerHTML)
);
await nodeFor("device-list").dispatch("click", {
  preventDefault() {},
  target: { closest: (selector) => (selector === "button[data-action=select-device]" ? { dataset: { id: "4" } } : null) }
});
await new Promise((resolve) => setTimeout(resolve, 1500));
let stored = {};
try {
  stored = JSON.parse(preferenceStore.get("switchweb.devicePreference") || "{}");
} catch {
  stored = {};
}
check("点击设备后写回选择记忆（lastDeviceId=4）", stored.lastDeviceId === 4, JSON.stringify(stored));
check(
  "切换后 #4 行变为激活",
  /device-list-row is-active[\s\S]{0,400}?data-id="4"/.test(nodeFor("device-list").innerHTML)
);

// 模块切换：模拟点击 OLT/ONU 管理，右侧应切到模块占位视图
let switchError = null;
try {
  const link = { dataset: { module: "olt" } };
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: {
      closest: (selector) => (selector === "a[data-module]" ? link : null)
    }
  });
} catch (error) {
  switchError = error;
}
check("点击 OLT/ONU 管理可切换视图", !switchError, switchError ? String(switchError.message) : "");
check("OLT 视图显示后隐藏交换机视图", nodeFor("switch-view").hidden === true);
check(
  "OLT 菜单生效：左栏切到 OLT 设备台账、右栏显示 OLT 设备详情",
  nodeFor("olt-ledger-sidebar").hidden === false &&
    nodeFor("switch-ledger-sidebar").hidden === true &&
    String(nodeFor("olt-device-list").innerHTML || "").length > 0 &&
    String(nodeFor("olt-detail").innerHTML || "").includes("OLT 设备详情")
);
try {
  const link = { dataset: { module: "tr069" } };
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: { closest: (selector) => (selector === "a[data-module]" ? link : null) }
  });
} catch (error) {
  switchError = error;
}
check(
  "未迁入业务页的模块仍是占位页（TR069），且两个台账面板都隐藏",
  nodeFor("module-view").innerHTML.includes("TR069 管理") &&
    nodeFor("module-view").innerHTML.includes("panel") &&
    nodeFor("switch-ledger-sidebar").hidden === true &&
    nodeFor("olt-ledger-sidebar").hidden === true
);
try {
  const link = { dataset: { module: "switch" } };
  await nodeFor("module-nav").dispatch("click", {
    preventDefault() {},
    target: { closest: (selector) => (selector === "a[data-module]" ? link : null) }
  });
} catch (error) {
  switchError = error;
}
check("切回交换机管理可恢复设备面板视图", !nodeFor("switch-view").hidden && nodeFor("module-view").hidden === true);

// ── 5) 后端联通性（真实调用）──
console.log("\n== 后端联通性（经前端壳代理）==");
// 允许的业务返回码（与 docs/01-api-compat.md 一致）：0 成功、-1 参数/业务错误、-2 过期、-3 未注册、-4 未安装。
// 另：/api/capabilities 是能力清单，本身不带 code 字段。
const ALLOWED_CODES = [0, -1, -2, -3, -4];
const business = calls.filter((item) => item.path.startsWith("/itms/") || item.path.startsWith("/api/"));
check("有业务请求发出", business.length > 0, `共 ${business.length} 条`);
const bad = business.filter((item) => item.status >= 500 || (item.code !== null && !ALLOWED_CODES.includes(item.code)));
check("业务请求无异常返回码", bad.length === 0, bad.map((item) => `${item.path}=${item.code}/http${item.status}`).join(", "));
for (const path of ["/itms/handleSwitchInfo/", "/itms/handleSwitchCollectionSettings/", "/itms/handleSwitchSnifferConfig/"]) {
  const hit = calls.find((item) => item.path === path);
  check(`已调用 ${path}`, Boolean(hit), hit ? `code=${hit.code}` : "未调用");
}
const caps = calls.find((item) => item.path === "/api/capabilities");
check("已调用 /api/capabilities（能力清单）", Boolean(caps && caps.status === 200), caps ? `http=${caps.status}` : "未调用");

// ── 6) 静态检查：基线一致性与定制点 ──
console.log("\n== 静态检查 ==");
const appSource = readFileSync(join(WEB_ROOT, "src", "app.js"), "utf8");
check(
  "app.js 含 OLT 侧栏台账面板与工具条（左栏台账 + 切换清理；工具条按钮带 data-olt-action）",
  appSource.includes('id="switch-ledger-sidebar"') &&
    appSource.includes('id="olt-ledger-sidebar"') &&
    appSource.includes('id="olt-device-list"') &&
    appSource.includes('id="olt-add"') &&
    appSource.includes('id="olt-connect"') &&
    appSource.includes('id="olt-edit"') &&
    appSource.includes('id="olt-delete"') &&
    appSource.includes('data-olt-action="add"') &&
    appSource.includes('data-olt-action="connect"') &&
    appSource.includes("clearModuleTransients")
);
check(
  "app.js 已无弹窗时代标识符",
  !/settings-dialog-root|openSettingsDialog|closeSettingsDialog|open-collection-config|settings-dialog-backdrop/.test(appSource)
);
check("app.js 品牌文案已替换", appSource.includes("Engineering Console") && appSource.includes("<h1>工程控制台</h1>"));
check(
  "app.js 含定制锚点（模块菜单 / 交换机视图 / 采集配置 TAB / 模块业务页挂载点）",
  appSource.includes('id="module-nav"') &&
    appSource.includes('id="switch-view"') &&
    appSource.includes('data-tab="collection"') &&
    appSource.includes("renderCollectionPanelContent") &&
    appSource.includes("MODULE_PAGES") &&
    appSource.includes("./modules/olt.js") &&
    !appSource.includes('id="collection-panel"')
);
check(
  "app.js 含设备选择记忆与错误提示分级实现",
  appSource.includes("DEVICE_PREFERENCE_KEY") &&
    appSource.includes("rememberWorkingDevice") &&
    appSource.includes("describeDeviceError")
);
check(
  "VLAN 错误提示经 describeDeviceError 分级（不再直接抛裸错误）",
  /describeDeviceError\((vlanState|portState)\.error/.test(appSource)
);

check(
  "三个根 TAB 内容壳统一为 .device-monitor（面板 / VLAN / 设置）",
  /function renderOverviewVlanContent[\s\S]{0,1500}?<div class="device-monitor">/.test(appSource) &&
    /<div class="device-monitor"[^>]*>\s*<div class="collection-panel__toolbar">/.test(appSource) &&
    !appSource.includes('class="overview-vlan-view"') &&
    !appSource.includes('class="collection-panel"')
);
check(
  "设置 TAB 内容壳保留无障碍名称（role=region + aria-label=设置）",
  /<div class="device-monitor" role="region" aria-label="设置">/.test(appSource)
);

const stylesPath = join(WEB_ROOT, "src", "styles.css");
const baselinePath = join(SWITCHWEB_ROOT, "src", "styles.css");
const ourStyles = readFileSync(stylesPath, "utf8");

check(
  "面板「数据来源」中文值字号与其余三项一致（.device-facts__source-label = 12px）",
  /class="device-facts__source-label">本地文件缓存</.test(appSource) &&
    /\.device-facts__source-label\s*\{[^}]*font-size:\s*12px/.test(ourStyles)
);

const ourOverviewPanelRule = [...ourStyles.matchAll(/\.overview-panel\s*\{([^}]*)\}/g)]
  .map((match) => match[1])
  .find((block) => /align-content:\s*start/.test(block)) || "";
check(
  ".overview-panel 的 grid 行不再被容器拉伸（align-content: start，基线默认 stretch）",
  /align-content:\s*start/.test(ourOverviewPanelRule)
);

const ourTabBarRule = [...ourStyles.matchAll(/\.overview-tabs\s*\{([^}]*)\}/g)]
  .map((match) => match[1])
  .find((block) => /(?<!min-)height:\s*47\.5px/.test(block)) || "";
check(
  "根 TAB 条尺寸固定（width: 100% + height/min/max: 47.5px + overflow: hidden，内部节点撑不动）",
  /(?<!min-)height:\s*47\.5px/.test(ourTabBarRule) &&
    /min-height:\s*47\.5px/.test(ourTabBarRule) &&
    /max-height:\s*47\.5px/.test(ourTabBarRule) &&
    /width:\s*100%/.test(ourTabBarRule) &&
    /overflow:\s*hidden/.test(ourTabBarRule) &&
    /align-items:\s*flex-end/.test(ourTabBarRule)
);

const ourRootTabRule = [...ourStyles.matchAll(/\.overview-tabs button\s*\{([^}]*)\}/g)]
  .map((match) => match[1])
  .find((block) => /(?<!min-)height:\s*34px/.test(block)) || "";
check(
  "根 TAB 按钮高度硬固定 34px（height/min/max 三重 + border-box + align-self + nowrap/overflow，内容与同行元素都改不动）",
  /\.overview-tabs\s*\{[^}]*align-items:\s*flex-end/.test(ourStyles) &&
    /(?<!min-)height:\s*34px/.test(ourRootTabRule) &&
    /min-height:\s*34px/.test(ourRootTabRule) &&
    /max-height:\s*34px/.test(ourRootTabRule) &&
    /box-sizing:\s*border-box/.test(ourRootTabRule) &&
    /align-self:\s*flex-end/.test(ourRootTabRule) &&
    /overflow:\s*hidden/.test(ourRootTabRule) &&
    /white-space:\s*nowrap/.test(ourRootTabRule)
);

const ourPortTabRule = [...ourStyles.matchAll(/\.port-dialog-tabs button\s*\{([^}]*)\}/g)]
  .map((match) => match[1])
  .find((block) => /(?<!min-)height:\s*40px/.test(block)) || "";
check(
  "对话框 / 面板内 TAB 条按钮同口径硬固定 40px（height/min/max 三重，且不覆盖窄屏左右 padding 与 flex）",
  /\.port-dialog-tabs\s*\{[^}]*align-items:\s*flex-end/.test(ourStyles) &&
    /(?<!min-)height:\s*40px/.test(ourPortTabRule) &&
    /min-height:\s*40px/.test(ourPortTabRule) &&
    /max-height:\s*40px/.test(ourPortTabRule) &&
    /align-self:\s*flex-end/.test(ourPortTabRule) &&
    /padding-top:\s*0/.test(ourPortTabRule) &&
    /padding-bottom:\s*0/.test(ourPortTabRule) &&
    !/padding-(left|right)/.test(ourPortTabRule) &&
    !/flex:/.test(ourPortTabRule)
);

check(
  "VLAN 首块内容顶距与面板 / 设置一致（去掉区块自带的 18px 上内边距）",
  /\.overview-tab-content\s*>\s*\.device-monitor\s*>\s*\.overview-vlan-section:first-child\s*\{[^}]*padding-top:\s*0/.test(ourStyles)
);

// 圆角统一：本工程硬性要求所有 border-radius 都是 5px。
{
  const values = [...ourStyles.matchAll(/border-radius\s*:\s*([^;}\n]+)/g)].map((m) => m[1].trim());
  const unusual = [...new Set(values)].filter((value) => value !== "5px");
  check(`styles.css 的 border-radius 全部为 5px（共 ${values.length} 处）`, unusual.length === 0, unusual.join(", "));
}

if (existsSync(baselinePath)) {
  const baseline = readFileSync(baselinePath, "utf8");
  const baseLines = baseline.split("\n");
  const ourLines = ourStyles.split("\n");
  // 逐行比对老壳基线：只有 border-radius 取值允许不同（其余必须一致），追加块属本工程自有。
  const drifted = [];
  baseLines.forEach((line, index) => {
    if (line.includes("border-radius")) {
      if (!String(ourLines[index] || "").includes("border-radius")) {
        drifted.push(index + 1);
      }
      return;
    }
    if (line !== String(ourLines[index] ?? "")) {
      drifted.push(index + 1);
    }
  });
  check(
    "styles.css 与 /switchweb 基线逐行一致（仅圆角取值统一，另有自有追加块）",
    drifted.length === 0,
    drifted.length ? `漂移行 ${drifted.slice(0, 8).join(", ")}` : `追加 ${ourLines.length - baseLines.length} 行`
  );
  check(
    "styles.css 保留三 TAB 对齐与字号统一定制（标记 + 关键规则）",
    ourStyles.includes("三个根 TAB（面板 / VLAN / 设置）视觉对齐") &&
      ourStyles.includes("提示 / 告警字号统一到正文") &&
      /\.overview-tab-content\s*>\s*\.device-monitor\s*\{[^}]*margin-top:\s*8px/.test(ourStyles) &&
      /\.overview-tabs button\s*\{[^}]*min-height:\s*34px/.test(ourStyles)
  );
} else {
  console.log(`  - 跳过基线比对：未找到 ${baselinePath}`);
}
for (const name of ["app.js", "api.js", "main.js"]) {
  const oursPath = join(WEB_ROOT, "src", name);
  const basePath = join(SWITCHWEB_ROOT, "src", name);
  if (!existsSync(basePath)) {
    continue;
  }
  const same = readFileSync(oursPath, "utf8") === readFileSync(basePath, "utf8");
  if (name === "app.js") {
    check("app.js 已按定制集合修改（三处定制 + 模块业务页挂载点；与老壳不同属预期）", !same);
  } else {
    check(`${name} 与 /switchweb 原样一致`, same);
  }
}

// 类名覆盖：JS/HTML 里写到的 class 都要有样式定义。
// 口径：只检查“本工程新增”的类——老壳自带的类（在 /switchweb/src/app.js 里同样出现）即使无样式也属
// 继承现状，不作为本工程的回归项。
{
  const css = readFileSync(stylesPath, "utf8");
  const defined = new Set();
  for (const match of css.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) {
    defined.add(match[1]);
  }
  const baselineClasses = new Set();
  const baselineAppPath = join(SWITCHWEB_ROOT, "src", "app.js");
  if (existsSync(baselineAppPath)) {
    const baselineText = readFileSync(baselineAppPath, "utf8");
    for (const match of baselineText.matchAll(/class="([^"$]*)"/g)) {
      for (const token of match[1].split(/\s+/)) {
        if (token) {
          baselineClasses.add(token);
        }
      }
    }
  }
  const sources = [join(WEB_ROOT, "index.html"), join(WEB_ROOT, "src", "app.js"), join(WEB_ROOT, "src", "modules", "olt.js")];
  const missing = new Map();
  for (const file of sources) {
    const text = readFileSync(file, "utf8");
    for (const match of text.matchAll(/class="([^"$]*)"/g)) {
      for (const token of match[1].split(/\s+/)) {
        if (!token || defined.has(token) || baselineClasses.has(token)) {
          continue;
        }
        if (!missing.has(token)) {
          missing.set(token, new Set());
        }
        missing.get(token).add(file.replace(REPO_ROOT + "/", ""));
      }
    }
  }
  if (missing.size === 0) {
    check("本工程新增的 class 均有样式定义", true);
  } else {
    check("本工程新增的 class 均有样式定义", false, [...missing.keys()].map((item) => `.${item}`).join(", "));
  }
}

console.log(`\n结果：${failures === 0 ? "全部通过" : `${failures} 项失败`}`);
process.exit(failures === 0 ? 0 : 1);
