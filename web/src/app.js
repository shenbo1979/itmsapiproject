import { api } from "./api.js";
import { createOltPage } from "./modules/olt.js";

// 模块业务页（第 4 处定制，M3 起逐步落地）：键 = /api/capabilities 的 module id，
// 值 = 页面工厂（由 createApp 注入 escapeHTML / toInt / notify / confirmAction 等公共工具）。
// 页面文件放在 web/src/modules/<id>.js，app.js 只负责渲染进 #module-view，避免把业务代码继续堆进本文件。
const MODULE_PAGES = { olt: createOltPage };
const modulePageInstances = {};

const state = {
  devices: [],
  deviceInformationCount: 0,
  editingDeviceId: null,
  expandedDeviceId: null,
  selectedDeviceIds: new Set(),
  connectingDeviceId: null,
  viewingDeviceId: null,
  deviceConnectionDetails: {},
  deviceActiveTabs: {},
  overviewActiveTab: "panel",
  overviewVlanPorts: {},
  deviceVlanLists: {},
  registration: { status: "loading", registered: false, record: null, error: "" },
  registrationDialog: null,
  // 一级导航 = 功能模块：activeModule 决定右侧主内容区展示哪个模块；capabilities 用于标注安装状态。
  activeModule: "switch",
  capabilities: { loaded: false, installed: [], error: "" },
  // 采集配置已从弹窗迁移到右侧面板，状态对象随之改名（原 settingsDialog）。
  collectionPanel: null,
  vlanDialog: null,
  portDialog: null,
  confirmDialog: null,
  sshTerminal: null,
  notices: [],
  loading: false
};

// 一级导航（功能模块）：与后端 /api/capabilities 的 modules[].id 对齐。
const MODULE_VIEWS = [
  {
    id: "olt",
    title: "OLT/ONU 管理",
    hint: "OLT 板卡 / 槽位、PON 口与 ONT 注册状态管理。"
  },
  {
    id: "tr069",
    title: "TR069 管理",
    hint: "光猫（CPE）模板、任务与在线状态管理。"
  },
  {
    id: "switch",
    title: "交换机管理",
    hint: "交换机设备台账、设备面板、VLAN / 端口 / 终端，以及设置。"
  }
];

function notify(kind, text) {
  const id = `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  state.notices.unshift({ id, kind, text });
  state.notices = state.notices.slice(0, 4);
  renderNotices();
  setTimeout(() => {
    state.notices = state.notices.filter((item) => item.id !== id);
    renderNotices();
  }, 5000);
}

function setLoading(loading) {
  state.loading = loading;
  document.body.classList.toggle("is-busy", loading);
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

let confirmDialogResolve = null;
let confirmDialogReturnFocus = null;

function renderConfirmDialog() {
  const root = document.getElementById("confirm-dialog-root");
  const dialog = state.confirmDialog;
  if (!root) return;
  if (!dialog) {
    root.innerHTML = "";
    return;
  }

  root.innerHTML = `
    <div class="confirm-dialog-backdrop" data-confirm-backdrop>
      <section class="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-description">
        <div class="confirm-dialog__mark" aria-hidden="true">!</div>
        <div class="confirm-dialog__content">
          <h3 id="confirm-dialog-title">${escapeHTML(dialog.title)}</h3>
          <p id="confirm-dialog-description">${escapeHTML(dialog.message)}</p>
          ${dialog.target ? `<div class="confirm-dialog__target"><span>${escapeHTML(dialog.targetLabel)}</span>${dialog.editable ? `<input class="confirm-dialog__target-input" value="${escapeHTML(String(dialog.target))}" data-confirm-target-input>` : `<strong>${escapeHTML(dialog.target)}</strong>`}</div>` : ""}
        </div>
        <div class="confirm-dialog__actions">
          <button type="button" class="button" data-action="cancel-confirm">取消</button>
          <button type="button" class="button ${dialog.danger ? "button--danger" : "button--primary"}" data-action="accept-confirm">${escapeHTML(dialog.confirmLabel)}</button>
        </div>
      </section>
    </div>
  `;
}

function closeConfirmDialog(confirmed) {
  const resolve = confirmDialogResolve;
  const returnFocus = confirmDialogReturnFocus;
  const dialog = state.confirmDialog;
  const editable = Boolean(dialog?.editable);
  // 可编辑确认框（如删除前修改 VLAN ID）：先取输入框当前值，再关闭弹窗。
  const inputValue = editable ? (document.querySelector('[data-confirm-target-input]')?.value.trim() ?? "") : "";
  confirmDialogResolve = null;
  confirmDialogReturnFocus = null;
  state.confirmDialog = null;
  renderConfirmDialog();
  // 可编辑确认框确认时回传 { confirmed, value }，取消回传 { confirmed:false }；不可编辑保持布尔值。
  if (editable) {
    resolve?.({ confirmed, value: inputValue });
  } else {
    resolve?.(confirmed);
  }
  requestAnimationFrame(() => returnFocus?.isConnected && returnFocus.focus());
}

function confirmAction({ title, message, target = "", targetLabel = "操作对象", confirmLabel = "确认", danger = false, editable = false }) {
  if (confirmDialogResolve) closeConfirmDialog(false);
  confirmDialogReturnFocus = document.activeElement;
  state.confirmDialog = { title, message, target, targetLabel, confirmLabel, danger, editable };
  renderConfirmDialog();

  return new Promise((resolve) => {
    confirmDialogResolve = resolve;
    requestAnimationFrame(() => {
      const focusTarget = document.querySelector('[data-action="cancel-confirm"]');
      (editable ? document.querySelector(".confirm-dialog__target input") : focusTarget)?.focus();
    });
  });
}

function collectFormData(form) {
  const data = Object.fromEntries(new FormData(form).entries());
  Object.keys(data).forEach((key) => {
    if (typeof data[key] === "string") {
      data[key] = data[key].trim();
    }
  });
  return data;
}

function toInt(value, fallback = 0) {
  const parsed = Number.parseInt(String(value ?? "").trim(), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function formatRpcTypeLabel(value) {
  switch (Number(value)) {
    case 1:
      return "RPC-API";
    case 2:
      return "TELNET";
    case 3:
    default:
      return "SSH";
  }
}

function inferDeviceMaker(device) {
  const candidates = [device.szRemarks, device.szName];
  const matched = candidates.find((value) => /huawei|华为|centec|盛科|h3c|华三|cisco|思科|ruijie|锐捷/i.test(String(value || "")));
  return matched ? String(matched).trim() : String(device.szName || "").trim();
}

function normalizeDeviceDetail(detail, device, source = "实时信息") {
  const rawInterfaces = detail?.Interfaces || detail?.interfaces || [];
  const interfaces = Array.isArray(rawInterfaces)
    ? rawInterfaces.map((item) => ({
        id: item?.ID ?? item?.Id ?? item?.id ?? null,
        port: item?.Port || item?.port || "",
        status: item?.Status || item?.status || "未知",
        speed: item?.Speed || item?.speed || "--",
        duplex: item?.Duplex || item?.duplex || "",
        type: item?.Type || item?.type || "",
        description: item?.Description || item?.description || "",
        moduleType: item?.ModuleType || item?.moduleType || "",
        temperature: item?.Temperature ?? item?.temperature,
        voltage: item?.Voltage ?? item?.voltage,
        current: item?.Current ?? item?.current,
        txPower: item?.TxPower ?? item?.txPower,
        rxPower: item?.RxPower ?? item?.rxPower,
        linkType: item?.VlanMapping?.LinkType ?? item?.VlanMapping?.linkType ?? item?.vlanMapping?.LinkType ?? item?.vlanMapping?.linkType ?? "",
        trunkVlanList: item?.VlanMapping?.TrunkVLANList ?? item?.VlanMapping?.trunkVlanList ?? item?.vlanMapping?.TrunkVLANList ?? item?.vlanMapping?.trunkVlanList ?? "",
        pvid: item?.VlanMapping?.PVID ?? item?.VlanMapping?.pvid ?? item?.vlanMapping?.PVID ?? item?.vlanMapping?.pvid ?? ""
      }))
    : [];
  const rawTemperatureSensors = detail?.TemperatureSensors || detail?.temperatureSensors || [];
  const temperatureSensors = Array.isArray(rawTemperatureSensors)
    ? rawTemperatureSensors.map((item) => ({
        index: item?.Index ?? item?.index ?? "--",
        position: item?.Position ?? item?.position ?? "",
        temperature: item?.Temperature ?? item?.temperature,
        status: item?.Status ?? item?.status ?? ""
      }))
    : [];
  const rawFans = detail?.Fans || detail?.fans || [];
  const fans = Array.isArray(rawFans)
    ? rawFans.map((item) => ({
        id: item?.ID ?? item?.id ?? "",
        index: item?.Index ?? item?.index ?? "",
        type: item?.Type ?? item?.type ?? "",
        status: item?.Status ?? item?.status ?? "",
        speedRatio: item?.SpeedRatio ?? item?.speedRatio,
        mode: item?.Mode ?? item?.mode ?? "",
        hardwareVersion: item?.HardwareVersion ?? item?.hardwareVersion ?? "",
        serialNumber: item?.SerialNumber ?? item?.serialNumber ?? ""
      }))
    : [];

  return {
    deviceName: detail?.DeviceName || detail?.deviceName || device.szName || "--",
    hostip: detail?.Hostip || detail?.hostip || detail?.szHostip || device.szManageIP || "--",
    softwareVersion: detail?.SoftwareVersion || detail?.softwareVersion || "--",
    uptime: detail?.Uptime || detail?.uptime || "--",
    online: detail?.Online ?? detail?.online ?? 1,
    cpuUsage: detail?.CPUUsage ?? detail?.cpuUsage ?? null,
    cpuInterrupt: detail?.CPUInterrupt ?? detail?.cpuInterrupt ?? null,
    memoryAllocated: detail?.MemoryAllocated ?? detail?.memoryAllocated ?? null,
    deviceTemperature: detail?.DeviceTemperature ?? detail?.deviceTemperature ?? null,
    temperatureSensors,
    fanSpeedRatio: detail?.FanSpeedRatio ?? detail?.fanSpeedRatio ?? detail?.fanRatio ?? detail?.fanSpeed ?? null,
    fans,
    interfaces,
    updatedAt: source === "实时信息" ? new Date() : null,
    source
  };
}

function resolveInterfaceStatusTone(status) {
  const value = String(status || "").toLowerCase();
  if (value === "up") {
    return "up";
  }
  if (/admin|shutdown|disable|close/.test(value)) {
    return "closed";
  }
  if (value === "down") {
    return "down";
  }
  return "unknown";
}

function getInterfaceID(item) {
  const value = item?.id;
  const id = Number(value);
  return Number.isFinite(id) ? id : null;
}

function resolveInterfaceMedium(item, device) {
  const type = String(item?.type || "");
  if (/centec|盛科/i.test(inferDeviceMaker(device))) {
    if (/aggregation/i.test(type)) return "aggregation";
    return /_t/i.test(type) ? "electrical" : "optical";
  }
  return /copper|electrical|rj45|base-t/i.test(type) ? "electrical" : "optical";
}

function renderPortMark() {
  return `
    <span class="port-mark" aria-hidden="true">
      <svg viewBox="0 0 64 64">
        <rect x="12" y="16" width="40" height="32" rx="8"></rect>
        <path d="M20 26h24v6H20zm2 9h4v5h-4zm6 0h4v5h-4zm6 0h4v5h-4zm6 0h4v5h-4z"></path>
      </svg>
    </span>
  `;
}

function formatTooltipValue(value, suffix = "") {
  if (value === undefined || value === null || value === "") {
    return "--";
  }
  return `${value}${suffix}`;
}

function hasOpticalValue(value) {
  if (value === undefined || value === null || value === "") return false;
  if (typeof value === "number") return Number.isFinite(value) && value !== 0;
  const text = String(value).trim();
  if (!text || /^(?:--?|unknown|n\/a|null)$/i.test(text)) return false;
  const numeric = Number(text);
  return !Number.isFinite(numeric) || numeric !== 0;
}

function renderPortOpticalFacts(optical) {
  const facts = [
    ["模块类型", optical.moduleType, ""],
    ["波长", optical.wavelength, ""],
    ["模块温度", optical.temperature, " C"],
    ["电压", optical.voltage, " V"],
    ["电流", optical.current, " mA"],
    ["发光功率", optical.txPower, " dBm"],
    ["收光功率", optical.rxPower, " dBm"]
  ];
  return facts
    .filter(([, value]) => hasOpticalValue(value))
    .map(([label, value, suffix]) => `<p>${label}：${escapeHTML(formatTooltipValue(value, suffix))}</p>`)
    .join("");
}

function renderPortTooltip(item) {
  return `
    <span class="port-tooltip" role="tooltip">
      <span><strong>接口 ID</strong>${escapeHTML(formatTooltipValue(item.id))}</span>
      <span><strong>模块类型</strong>${escapeHTML(formatTooltipValue(item.type))}</span>
      <span><strong>模块温度</strong>${escapeHTML(formatTooltipValue(item.temperature, " C"))}</span>
      <span><strong>电压</strong>${escapeHTML(formatTooltipValue(item.voltage, " V"))}</span>
      <span><strong>电流</strong>${escapeHTML(formatTooltipValue(item.current, " mA"))}</span>
      <span><strong>发光功率</strong>${escapeHTML(formatTooltipValue(item.txPower, " dBm"))}</span>
      <span><strong>收光功率</strong>${escapeHTML(formatTooltipValue(item.rxPower, " dBm"))}</span>
      <span><strong>PVID</strong>${escapeHTML(formatTooltipValue(item.pvid))}</span>
    </span>
  `;
}

function summarizeInterfaces(rows) {
  return rows.reduce(
    (summary, item) => {
      const tone = resolveInterfaceStatusTone(item.status);
      summary.total += 1;
      summary[tone] += 1;
      const temperature = Number(item.temperature);
      if (Number.isFinite(temperature) && temperature > 0) {
        summary.temperatures.push(temperature);
      }
      return summary;
    },
    { total: 0, up: 0, down: 0, closed: 0, unknown: 0, temperatures: [] }
  );
}

function renderDeviceTemperatureChart(sensors, fallbackTemperature) {
  const chartSensors = (Array.isArray(sensors) ? sensors : []).map((item) => ({ ...item, value: Number(item.temperature) })).filter((item) => Number.isFinite(item.value) && item.value >= 0);
  if (!chartSensors.length && Number.isFinite(fallbackTemperature) && fallbackTemperature >= 0) {
    chartSensors.push({ index: "设备", position: "", value: fallbackTemperature });
  }
  if (!chartSensors.length) return '<div class="device-temperature-chart__empty">暂无温度传感器数据</div>';
  const maxTemperature = Math.max(60, Math.ceil(Math.max(...chartSensors.map((item) => item.value)) / 10) * 10);
  const positionLabels = { AROUND_CHIP: "芯片周边", AROUND_FAN: "风扇周边", SWITCH_CHIP: "芯片内部" };
  const chart = { width: 300, height: 150, left: 32, top: 12, right: 8, bottom: 34 };
  const plotWidth = chart.width - chart.left - chart.right;
  const plotHeight = chart.height - chart.top - chart.bottom;
  const slotWidth = plotWidth / chartSensors.length;
  const barWidth = Math.max(8, Math.min(24, slotWidth * 0.48));
  const baseY = chart.top + plotHeight;
  const bars = chartSensors.map((item, index) => {
    const value = item.value.toFixed(1);
    const position = positionLabels[String(item.position || "").toUpperCase()] || item.position || "传感器";
    const label = `${position}_${item.index}`;
    const height = Math.max(2, Math.min(plotHeight, item.value / maxTemperature * plotHeight));
    const x = chart.left + index * slotWidth + (slotWidth - barWidth) / 2;
    const y = baseY - height;
    const centerX = chart.left + index * slotWidth + slotWidth / 2;
    return `<g class="device-temperature-chart__series"><title>${escapeHTML(label)}：${value} C</title><rect class="device-temperature-chart__column" x="${x.toFixed(2)}" y="${y.toFixed(2)}" width="${barWidth.toFixed(2)}" height="${height.toFixed(2)}"></rect><text class="device-temperature-chart__value" x="${centerX.toFixed(2)}" y="${Math.max(chart.top + 8, y - 3).toFixed(2)}">${value}</text><text class="device-temperature-chart__label" x="${centerX.toFixed(2)}" y="${chart.height - 13}">${escapeHTML(label)}</text></g>`;
  }).join("");
  const midTemperature = Math.round(maxTemperature / 2);
  return `<svg class="device-temperature-chart" viewBox="0 0 ${chart.width} ${chart.height}" role="img" aria-label="设备温度传感器柱状图，最高刻度 ${maxTemperature} C"><rect fill="#ffffff" class="highcharts-background" x="0" y="0" width="300" height="150" rx="0" ry="0"></rect><line class="device-temperature-chart__axis-line" x1="${chart.left}" y1="${chart.top}" x2="${chart.left}" y2="${baseY}"></line><line class="device-temperature-chart__axis-line" x1="${chart.left}" y1="${baseY}" x2="${chart.width - chart.right}" y2="${baseY}"></line><line class="device-temperature-chart__grid" x1="${chart.left}" y1="${chart.top + plotHeight / 2}" x2="${chart.width - chart.right}" y2="${chart.top + plotHeight / 2}"></line><text class="device-temperature-chart__tick" x="3" y="${chart.top + 4}">${maxTemperature} C</text><text class="device-temperature-chart__tick" x="8" y="${chart.top + plotHeight / 2 + 4}">${midTemperature} C</text><text class="device-temperature-chart__tick" x="17" y="${baseY + 4}">0 C</text>${bars}</svg>`;
}

function renderDeviceFanSpeedChart(fans, fallbackSpeed) {
  const normalizedFans = Array.isArray(fans) ? fans : [];
  const chartFans = normalizedFans.map((item) => ({ ...item, value: Number(item.speedRatio) })).filter((item) => Number.isFinite(item.value) && item.value > 0);
  if (!chartFans.length) {
    const statusFans = normalizedFans.filter((item) => String(item.status || "").trim());
    if (statusFans.length) {
      const rows = statusFans.map((item, index) => {
        const status = String(item.status || "").trim();
        const healthy = /^(ok|normal)$/i.test(status);
        const statusLabel = healthy ? "正常" : status;
        const identity = item.index || item.id || index + 1;
        const metadata = [item.type, item.hardwareVersion ? `HW ${item.hardwareVersion}` : "", item.serialNumber].filter(Boolean).join(" · ");
        return `<div class="device-fan-status__row" role="listitem"><span><strong>风扇 ${escapeHTML(identity)}</strong>${metadata ? `<small>${escapeHTML(metadata)}</small>` : ""}</span><b class="device-fan-status__badge ${healthy ? "is-ok" : "is-fault"}">${escapeHTML(statusLabel)}</b></div>`;
      }).join("");
      return `<div class="device-fan-status" role="list" aria-label="设备风扇状态">${rows}</div>`;
    }
  }
  if (!chartFans.length && Number.isFinite(fallbackSpeed) && fallbackSpeed > 0) {
    chartFans.push({ id: "", index: "设备", status: "", value: fallbackSpeed });
  }
  if (!chartFans.length) return '<div class="device-temperature-chart__empty">暂无风扇数据</div>';
  const chart = { width: 300, height: 150, left: 32, top: 12, right: 8, bottom: 34 };
  const plotWidth = chart.width - chart.left - chart.right;
  const plotHeight = chart.height - chart.top - chart.bottom;
  const slotWidth = plotWidth / chartFans.length;
  const barWidth = Math.max(8, Math.min(24, slotWidth * 0.48));
  const baseY = chart.top + plotHeight;
  const bars = chartFans.map((item, index) => {
    const value = Math.min(100, item.value).toFixed(0);
    const label = `风扇_${item.index || item.id || index + 1}`;
    const height = Math.max(2, Math.min(plotHeight, item.value / 100 * plotHeight));
    const x = chart.left + index * slotWidth + (slotWidth - barWidth) / 2;
    const y = baseY - height;
    const centerX = chart.left + index * slotWidth + slotWidth / 2;
    return `<g class="device-temperature-chart__series"><title>${escapeHTML(label)}：${value}%</title><rect class="device-fan-chart__column" x="${x.toFixed(2)}" y="${y.toFixed(2)}" width="${barWidth.toFixed(2)}" height="${height.toFixed(2)}"></rect><text class="device-temperature-chart__value device-fan-chart__value" x="${centerX.toFixed(2)}" y="${Math.max(chart.top + 8, y - 3).toFixed(2)}">${value}%</text><text class="device-temperature-chart__label" x="${centerX.toFixed(2)}" y="${chart.height - 13}">${escapeHTML(label)}</text></g>`;
  }).join("");
  return `<svg class="device-temperature-chart device-fan-chart" viewBox="0 0 ${chart.width} ${chart.height}" role="img" aria-label="设备风扇速比柱状图，最高刻度 100%"><rect fill="#ffffff" class="highcharts-background" x="0" y="0" width="300" height="150" rx="0" ry="0"></rect><line class="device-temperature-chart__axis-line" x1="${chart.left}" y1="${chart.top}" x2="${chart.left}" y2="${baseY}"></line><line class="device-temperature-chart__axis-line" x1="${chart.left}" y1="${baseY}" x2="${chart.width - chart.right}" y2="${baseY}"></line><line class="device-temperature-chart__grid" x1="${chart.left}" y1="${chart.top + plotHeight / 2}" x2="${chart.width - chart.right}" y2="${chart.top + plotHeight / 2}"></line><text class="device-temperature-chart__tick" x="3" y="${chart.top + 4}">100%</text><text class="device-temperature-chart__tick" x="8" y="${chart.top + plotHeight / 2 + 4}">50%</text><text class="device-temperature-chart__tick" x="17" y="${baseY + 4}">0%</text>${bars}</svg>`;
}

function renderCPUUtilizationChart(value) {
  const usage = Number(value);
  if (!Number.isFinite(usage) || usage < 0) return '<div class="device-cpu-chart__empty">暂无 CPU 数据</div>';
  const percent = Math.min(100, usage);
  const center = 55;
  const radius = 43;
  let sector = "";
  if (percent >= 100) {
    sector = `<circle class="device-cpu-chart__used" cx="${center}" cy="${center}" r="${radius}"></circle>`;
  } else if (percent > 0) {
    const endAngle = (-90 + percent * 3.6) * Math.PI / 180;
    const endX = center + radius * Math.cos(endAngle);
    const endY = center + radius * Math.sin(endAngle);
    const largeArc = percent > 50 ? 1 : 0;
    sector = `<path class="device-cpu-chart__used" d="M ${center} ${center} L ${center} ${center - radius} A ${radius} ${radius} 0 ${largeArc} 1 ${endX.toFixed(2)} ${endY.toFixed(2)} Z"></path>`;
  }
  return `<svg class="device-cpu-chart" viewBox="0 0 110 110" role="img" aria-label="CPU 利用率 ${percent.toFixed(1)}%"><circle class="device-cpu-chart__remaining" cx="${center}" cy="${center}" r="${radius}"></circle>${sector}<circle class="device-cpu-chart__center" cx="${center}" cy="${center}" r="26"></circle><text x="${center}" y="${center + 4}">${percent.toFixed(1)}%</text></svg>`;
}

function renderDeviceVlanTable(deviceId) {
  const vlanState = state.deviceVlanLists[deviceId];
  if (!vlanState) return '<div class="device-loading"><strong>正在准备 VLAN 数据...</strong></div>';
  if (vlanState?.status === "loading") return '<div class="device-loading"><strong>正在读取 VLAN 信息...</strong></div>';
  if (vlanState?.status === "error") return `<div class="device-vlan-toolbar"><div><strong>设备 VLAN 列表</strong></div><div class="device-vlan-toolbar__actions"><button type="button" class="button" data-action="refresh-device-vlans" data-id="${escapeHTML(deviceId)}">重新加载</button><button type="button" class="button button--primary" data-action="create-device-vlan" data-id="${escapeHTML(deviceId)}">新增 VLAN</button></div></div><div class="port-dialog-error" title="${escapeHTML(vlanState.error)}">${escapeHTML(describeDeviceError(vlanState.error, state.devices.find((item) => toInt(item.nId, 0) === deviceId)).text)}</div>`;
  if (vlanState?.status === "ready") {
    const vlans = Array.isArray(vlanState.data) ? vlanState.data : [];
    const active = vlans.filter((item) => /active|enable|static|vxlan|启用/i.test(item.status || item.Status || "")).length;
    return `<div class="device-vlan-toolbar"><div><strong>设备 VLAN 列表</strong><span class="device-vlan-count">共 ${vlans.length} 项 · ${active} 项启用</span></div><div class="device-vlan-toolbar__actions"><button type="button" class="button" data-action="refresh-device-vlans" data-id="${escapeHTML(deviceId)}">刷新</button><button type="button" class="button button--primary" data-action="create-device-vlan" data-id="${escapeHTML(deviceId)}">新增 VLAN</button></div></div>${vlans.length ? `<div class="device-vlan-table-wrap"><table class="data-table device-vlan-table"><thead><tr><th>VLAN ID</th><th>名称</th><th>状态</th><th>成员端口</th><th class="device-vlan-actions-head">操作</th></tr></thead><tbody>${vlans.map((item) => { const id = item.vlanId || item.VlanId || ""; const status = item.status || item.Status || "--"; const isRange = String(id).includes("-"); return `<tr><td><strong class="vlan-id">${escapeHTML(id || "--")}</strong></td><td>${escapeHTML(item.name || item.Name || "--")}</td><td><span class="vlan-status ${/active|enable|static|vxlan|启用/i.test(status) ? "is-active" : ""}">${escapeHTML(status)}</span></td><td class="vlan-members">${escapeHTML(item.memberPort || item.MemberPort || "--")}</td><td class="device-vlan-actions">${isRange ? "" : `<button type="button" class="button button--table" data-action="rename-device-vlan" data-id="${escapeHTML(deviceId)}" data-vlan="${escapeHTML(id)}" data-name="${escapeHTML(item.name || item.Name || "")}">改名</button>`}<button type="button" class="button button--table button--danger" data-action="delete-device-vlan" data-id="${escapeHTML(deviceId)}" data-vlan="${escapeHTML(id)}">删除</button></td></tr>`; }).join("")}</tbody></table></div>` : '<div class="empty-state">设备上暂无 VLAN，点击右上角新增。</div>'}`;
  }
  return '<div class="empty-state">暂无设备 VLAN 数据。</div>';
}

function formatTimestamp(value) {
  return value instanceof Date
    ? value.toLocaleString("zh-CN", { hour12: false })
    : "--";
}

function formatPercent(value) {
  return Number.isFinite(value) ? `${Math.round(value)}%` : "--";
}

function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) {
    return "--";
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function renderDeviceConnectionRow(deviceId) {
  if (state.deviceConnectionDetails[deviceId]) {
    return "";
  }
  if (state.expandedDeviceId !== deviceId) {
    return "";
  }

  if (state.connectingDeviceId === deviceId || state.viewingDeviceId === deviceId) {
    const readingLocal = state.viewingDeviceId === deviceId;
    return `
      <tr class="device-subrow">
        <td colspan="6">
          <div class="device-monitor device-monitor--loading">
            <div class="device-loading">
              <span class="device-loading__spinner" aria-hidden="true"></span>
              <strong>${readingLocal ? "正在读取本地保存的设备信息..." : "正在连接设备并读取面板数据..."}</strong>
              <p>${readingLocal ? "不会连接交换机，将展示最近一次保存的面板和接口状态。" : "设备信息和接口状态返回后会自动展开。"}</p>
            </div>
          </div>
        </td>
      </tr>
    `;
  }

  return "";
}

function getOverviewVlanPortState(deviceId, interfaces = []) {
  const ports = interfaces.map((item) => String(item?.port || "").trim()).filter(Boolean);
  let portState = state.overviewVlanPorts[deviceId];
  if (!portState || (portState.port && !ports.includes(portState.port))) {
    portState = { port: ports[0] || "", status: "idle", data: null, error: "", pending: false, request: null };
    state.overviewVlanPorts[deviceId] = portState;
  } else if (!portState.port && ports.length) {
    portState.port = ports[0];
    portState.status = "idle";
  }
  return portState;
}

function renderOverviewVlanContent(device, detail, deviceId) {
  if (!device) return '<div class="empty-state overview-tab-placeholder">请选择需要管理的设备。</div>';
  const interfaces = Array.isArray(detail?.interfaces) ? detail.interfaces : [];
  const portState = getOverviewVlanPortState(deviceId, interfaces);
  const mapping = normalizePortVlanMapping(portState.data);
  const loading = portState.status === "loading";
  const pending = Boolean(portState.pending);
  const disabled = !portState.port || loading || pending || portState.status !== "ready";
  const maker = inferDeviceMaker(device);
  const isPVIDOnly = /h3c|华三|ruijie|锐捷/i.test(maker);
  const linkType = String(mapping.linkType || "access").toLowerCase();
  const pvid = mapping.pvid || 1;

  return `
    <div class="device-monitor">
      <section class="overview-vlan-section" aria-label="设备 VLAN">
        ${renderDeviceVlanTable(deviceId)}
      </section>
      <section class="overview-vlan-section" aria-labelledby="overview-port-vlan-title" data-overview-port-vlan-form>
        <div class="overview-port-vlan__header">
          <div><h4 id="overview-port-vlan-title">接口 VLAN</h4></div>
          <div class="overview-port-vlan__selector"><label><span>接口</span><select name="overviewVlanPort" data-action="select-overview-vlan-port" ${interfaces.length ? "" : "disabled"}>${interfaces.length ? interfaces.map((item) => `<option value="${escapeHTML(item.port)}" ${item.port === portState.port ? "selected" : ""}>${escapeHTML(item.port)}</option>`).join("") : '<option value="">暂无接口</option>'}</select></label><button type="button" class="button" data-action="refresh-overview-port-vlan" ${!portState.port || loading ? "disabled" : ""}>${loading ? "读取中..." : "刷新"}</button></div>
        </div>
        ${portState.error ? `<div class="port-dialog-error" title="${escapeHTML(portState.error)}">${escapeHTML(describeDeviceError(portState.error, device).text)}</div>` : ""}
        <div class="overview-port-vlan__fields">
          <label><span>链路类型</span><select name="linkType" ${disabled || isPVIDOnly ? "disabled" : ""}><option value="access" ${linkType === "access" ? "selected" : ""}>Access</option><option value="trunk" ${linkType === "trunk" ? "selected" : ""}>Trunk</option><option value="hybrid" ${linkType === "hybrid" ? "selected" : ""}>Hybrid</option></select></label>
          <label><span>PVID</span><input name="pvid" type="number" min="1" max="4094" value="${escapeHTML(pvid)}" ${disabled ? "disabled" : ""}></label>
          <label class="overview-port-vlan__allowed"><span>允许 VLAN</span><input name="trunkVlanList" value="${escapeHTML(mapping.trunkVlanList || "")}" placeholder="例如 100,200" ${disabled || isPVIDOnly ? "disabled" : ""}></label>
        </div>
        <div class="overview-port-vlan__actions">
          <button type="button" class="button button--primary" data-action="save-overview-port-vlan" ${disabled ? "disabled" : ""}>${pending ? "应用中..." : "应用配置"}</button>
          <button type="button" class="button button--danger" data-action="delete-overview-port-vlan" ${disabled ? "disabled" : ""}>清除配置</button>
        </div>
      </section>
    </div>
  `;
}

function renderOverview() {
  const overview = document.getElementById("overview-cards");
  if (!overview) return;
  const device = state.devices.find((item) => toInt(item.nId, 0) === state.expandedDeviceId);
  const detail = device ? state.deviceConnectionDetails[state.expandedDeviceId] : null;
  const panels = detail && device ? [(() => {
    const id = state.expandedDeviceId;
    const name = detail.deviceName || device?.szName || `设备 ${id}`;
    const cpu = Number(detail.cpuUsage);
    const temperature = Number(detail.deviceTemperature);
    const fan = detail.fanSpeedRatio === null || detail.fanSpeedRatio === undefined || detail.fanSpeedRatio === ""
      ? Number.NaN
      : Number(String(detail.fanSpeedRatio).replace("%", "").trim());
    const temperatureChart = renderDeviceTemperatureChart(detail.temperatureSensors, temperature);
    const fanChart = renderDeviceFanSpeedChart(detail.fans, fan);
    const cpuChart = renderCPUUtilizationChart(cpu);
    const summary = summarizeInterfaces(detail.interfaces || []);
    const renderPanelPort = (item) => {
      const tone = resolveInterfaceStatusTone(item.status);
      const port = escapeHTML(item.port || "--");
      const interfaceID = getInterfaceID(item);
      const medium = resolveInterfaceMedium(item, device);
      const deviceName = escapeHTML(detail.deviceName || device?.szName || device?.szManageIP || "--");
      const interfaceType = escapeHTML(item.type || "");
      return `<button type="button" class="device-panel-port device-panel-port--${escapeHTML(tone)}" data-action="inspect-overview-port" data-port="${port}" data-device-name="${deviceName}" data-interface-type="${interfaceType}" title="查看 ${port} 接口信息${interfaceID !== null ? `（ID ${interfaceID}）` : ""}" aria-label="查看接口 ${port}${interfaceID !== null ? `，ID ${interfaceID}` : ""} 的信息"><span class="device-panel-port__number">${port}</span><span class="device-panel-port__socket device-panel-port__socket--${medium}" aria-hidden="true"><i></i><i></i><i></i><i></i></span><span class="device-panel-port__speed">${escapeHTML(item.speed || "--")}</span>${renderPortTooltip(item)}</button>`;
    };
    const sortedInterfaces = (detail.interfaces || []).map((item, index) => ({ item, index, id: getInterfaceID(item) })).sort((left, right) => {
      if (left.id !== null && right.id !== null) return left.id - right.id || left.index - right.index;
      if (left.id !== null) return -1;
      if (right.id !== null) return 1;
      return left.index - right.index;
    }).map(({ item }) => item);
    const interfaces = Array.from({ length: Math.ceil(sortedInterfaces.length / 2) }, (_, index) => `<div class="device-panel-port-pair">${renderPanelPort(sortedInterfaces[index * 2])}${sortedInterfaces[index * 2 + 1] ? renderPanelPort(sortedInterfaces[index * 2 + 1]) : '<span class="device-panel-port--empty" aria-hidden="true"></span>'}</div>`).join("");
    const portLegend = `<div class="device-panel-legend" aria-label="接口状态图例"><span><i class="device-panel-legend__medium device-panel-legend__medium--electrical"></i>电口</span><span><i class="device-panel-legend__medium device-panel-legend__medium--optical"></i>光口</span><span><i class="device-panel-legend__medium device-panel-legend__medium--aggregation"></i>聚合</span><span><i class="device-panel-legend__state device-panel-legend__state--up"></i>激活 ${summary.up}</span><span><i class="device-panel-legend__state device-panel-legend__state--down"></i>未激活 ${summary.down + summary.unknown}</span><span><i class="device-panel-legend__state device-panel-legend__state--closed"></i>关闭 ${summary.closed}</span></div>`;
    const interfaceContent = `<section class="device-interface-status" aria-labelledby="device-interface-status-title"><div class="device-interface-status__header"><strong id="device-interface-status-title">接口状态</strong><span>${summary.total} 个接口</span></div><div class="device-panel-ports" aria-label="设备接口状态">${interfaces || '<div class="empty-state">暂无接口状态数据。</div>'}</div>${portLegend}</section>`;
    // 「数据来源」在本地缓存分支渲染的是中文标签：中文（雅黑 / Noto）在 13px 下比同号的英文、数字值明显偏大，
    // 这里单独给一个类，由 styles.css 按本工程「中文标签 12px、数值 13px」的口径渲染，四项事实视觉一致。
    const sourceValue = detail.source === "本地保存信息"
      ? '<span class="device-facts__source-label">本地文件缓存</span>'
      : escapeHTML(formatTimestamp(detail.updatedAt));
    const activeTab = "interfaces";
    const tabContent = interfaceContent;
    return `<div class="device-monitor"><div class="device-monitor__hero"><div><strong>${escapeHTML(name)}</strong><p>${escapeHTML(detail.hostip || device?.szManageIP || "--")} · ${escapeHTML(formatRpcTypeLabel(device?.nRpcType))}</p></div></div><div class="device-facts"><span><small>软件版本</small>${escapeHTML(detail.softwareVersion)}</span><span><small>运行时长</small>${escapeHTML(detail.uptime)}</span><span><small>内存占用</small>${escapeHTML(formatBytes(detail.memoryAllocated))}</span><span><small>数据来源</small>${sourceValue}</span></div><div class="device-health-grid"><article class="device-health-card device-health-card--cpu"><span>CPU 利用率</span>${cpuChart}</article><article class="device-health-card device-health-card--temperature"><span>设备温度</span>${temperatureChart}</article><article class="device-health-card device-health-card--fan"><span>风扇速比</span>${fanChart}</article></div><div class="port-dialog-tabs device-monitor-tabs" role="tablist" aria-label="设备面板信息"><button type="button" role="tab" data-action="select-overview-tab" data-tab="interfaces" aria-selected="${activeTab === "interfaces"}" class="${activeTab === "interfaces" ? "is-active" : ""}">接口状态</button><button type="button" role="tab" data-action="select-overview-tab" data-tab="vlan" aria-selected="${activeTab === "vlan" ? "is-active" : ""}">VLAN 信息</button></div>${tabContent}</div>`;
  })()] : [];
  // 根 TAB：面板 / VLAN / 设置（三者平级，设置原为弹窗、后为独立面板，现并入 TAB）
  const activeTab = state.overviewActiveTab === "vlan"
    ? "vlan"
    : state.overviewActiveTab === "collection"
      ? "collection"
      : "panel";
  const panelContent = panels.length ? panels.join("").replace("<span>风扇速比</span>", "<span>风扇状态</span>") : '<div class="empty-state">展开设备后显示设备面板信息。</div>';
  const tabContent = activeTab === "vlan"
    ? renderOverviewVlanContent(device, detail, state.expandedDeviceId)
    : activeTab === "collection"
      ? renderCollectionPanelContent()
      : panelContent;
  overview.innerHTML = `
    <div class="overview-tabs" role="tablist" aria-label="设备信息视图">
      <button type="button" role="tab" data-action="select-overview-root-tab" data-tab="panel" aria-selected="${activeTab === "panel"}" class="${activeTab === "panel" ? "is-active" : ""}">面板</button>
      <button type="button" role="tab" data-action="select-overview-root-tab" data-tab="vlan" aria-selected="${activeTab === "vlan"}" class="${activeTab === "vlan" ? "is-active" : ""}">VLAN</button>
      <button type="button" role="tab" data-action="select-overview-root-tab" data-tab="collection" aria-selected="${activeTab === "collection"}" class="${activeTab === "collection" ? "is-active" : ""}">设置</button>
    </div>
    <section class="overview-tab-content" role="tabpanel">${tabContent}</section>
  `;
  const monitor = overview.querySelector(".device-monitor");
  if (monitor) {
    monitor.querySelector(".device-monitor-tabs")?.remove();
  }
  if (activeTab === "vlan") {
    queueMicrotask(() => void ensureOverviewVlanData());
  }
  if (activeTab === "collection" && !state.collectionPanel) {
    queueMicrotask(() => void loadCollectionPanel());
  }
}

function renderDeviceList() {
  const root = document.getElementById("device-list");
  if (!root) return;
  const hasDevices = state.devices.length > 0;
  const hasExpandedDevice = state.devices.some((item) => toInt(item.nId, 0) === state.expandedDeviceId);
  const deviceIds = state.devices.map((item) => toInt(item.nId, 0)).filter(Boolean);
  const selectedCount = deviceIds.filter((id) => state.selectedDeviceIds.has(id)).length;
  const allSelected = hasDevices && selectedCount === deviceIds.length;
  const count = document.getElementById("device-list-count");
  if (count) count.textContent = `${state.devices.length} 台`;
  const selectAll = document.getElementById("select-all-devices");
  if (selectAll) {
    selectAll.checked = allSelected;
    selectAll.indeterminate = selectedCount > 0 && !allSelected;
    selectAll.disabled = !hasDevices;
    selectAll.setAttribute("aria-label", allSelected ? "取消全选设备" : "全选设备");
  }
  ["import-devices", "export-devices"].forEach((id) => {
    const button = document.getElementById(id);
    if (button) button.disabled = !hasDevices;
  });
  const editButton = document.getElementById("edit-selected");
  if (editButton) editButton.disabled = !hasExpandedDevice;
  const deleteButton = document.getElementById("delete-selected");
  if (deleteButton) {
    deleteButton.disabled = selectedCount === 0;
    deleteButton.textContent = selectedCount ? `删除 (${selectedCount})` : "删除";
  }
  root.innerHTML = hasDevices
    ? state.devices.map((item) => {
        const id = toInt(item.nId, 0);
        const online = toInt(item.nAutoEnable, 0) !== 0;
        const name = item.szName || item.szManageIP || "未命名设备";
        return `<div class="device-list-row${state.expandedDeviceId === id ? " is-active" : ""}">
          <input class="device-list-row__checkbox" type="checkbox" data-action="select-device-for-delete" data-id="${escapeHTML(id)}" aria-label="选择设备 ${escapeHTML(name)}"${state.selectedDeviceIds.has(id) ? " checked" : ""}>
          <button type="button" class="device-list-item" data-action="select-device" data-id="${escapeHTML(id)}">
            <i class="device-list-item__status${online ? " is-online" : ""}"></i>
            <span><strong>${escapeHTML(item.szName || "未命名设备")}</strong><small>${escapeHTML(item.szManageIP || "未配置管理 IP")}:${escapeHTML(item.nRpcPort || "--")}</small><small>备注：${escapeHTML(item.szRemarks || "--")}</small></span>
            <em>${escapeHTML(formatRpcTypeLabel(item.nRpcType))}</em>
          </button>
        </div>`;
      }).join("")
    : '<div class="device-list-empty">暂无设备记录</div>';
}

function exportDevices() {
  const rows = [["设备名称", "管理IP", "连接类型", "端口", "备注"], ...state.devices.map((item) => [item.szName || "", item.szManageIP || "", formatRpcTypeLabel(item.nRpcType), item.nRpcPort || "", item.szRemarks || ""])];
  const csv = rows.map((row) => row.map((cell) => `"${String(cell).replaceAll('"', '""')}"`).join(",")).join("\n");
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob(["\ufeff" + csv], { type: "text/csv;charset=utf-8" }));
  link.download = `switch-devices-${new Date().toISOString().slice(0, 10)}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
  notify("success", "设备台账已导出。");
}

function renderNotices() {
  const root = document.getElementById("notices");
  if (!root) {
    return;
  }
  const noticeTitles = {
    success: "完成",
    error: "异常",
    info: "处理中"
  };
  root.innerHTML = state.notices
    .map(
      (notice) => `
        <div class="notice notice--${escapeHTML(notice.kind)}">
          <span>${escapeHTML(noticeTitles[notice.kind] || "提示")}</span>
          <p>${escapeHTML(notice.text)}</p>
        </div>
      `
    )
    .join("");
}

function renderDevices() {
  const container = document.getElementById("device-table");
  if (!container) {
    return;
  }
  container.innerHTML = `
    <table class="data-table">
      <thead>
        <tr>
          <th>名称</th>
          <th>管理 IP</th>
          <th>连接类型</th>
          <th>备注</th>
          <th>端口</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>${
        state.devices.length
          ? state.devices
              .map(
                (item) => `
                  ${(() => {
                    const deviceID = toInt(item.nId, 0);
                    const isConnecting = state.connectingDeviceId === deviceID;
                    const isViewing = state.viewingDeviceId === deviceID;
                    const isBusy = isConnecting || isViewing;
                    const supportsSSH = toInt(item.nRpcType, 3) === 3;
                    return `
                  <tr class="${toInt(item.nAutoEnable, 0) === 0 ? "device-row--offline" : "device-row--online"}">
                    <td>${escapeHTML(item.szName)}</td>
                    <td>${escapeHTML(item.szManageIP)}<span class="device-online-status ${toInt(item.nAutoEnable, 0) === 0 ? "device-online-status--offline" : ""}">${toInt(item.nAutoEnable, 0) === 0 ? "离线" : "在线"}</span></td>
                    <td>${escapeHTML(formatRpcTypeLabel(item.nRpcType))}</td>
                    <td>${escapeHTML(item.szRemarks || "-")}</td>
                    <td>${escapeHTML(item.nRpcPort || "-")}</td>
                    <td>
                      <div class="table-actions">
                        <button class="button button--table${isConnecting ? " button--busy" : ""}" data-action="connect-device" data-id="${escapeHTML(item.nId)}"${isBusy ? " disabled" : ""}>${isConnecting ? "连接中..." : "连接"}</button>
                        <button class="button button--table${isViewing ? " button--busy" : ""}" data-action="view-device" data-id="${escapeHTML(item.nId)}"${isBusy ? " disabled" : ""}>${isViewing ? "读取中..." : "查看"}</button>
                        ${supportsSSH ? `<button class="button button--table" data-action="open-ssh-terminal" data-id="${escapeHTML(item.nId)}">SSH 终端</button>` : ""}
                        <button class="button button--table" data-action="edit-device" data-id="${escapeHTML(item.nId)}">修改</button>
                        <button class="button button--table button--danger" data-action="delete-device" data-id="${escapeHTML(item.nId)}">删除</button>
                      </div>
                    </td>
                  </tr>
                  ${renderDeviceConnectionRow(deviceID)}
                    `;
                  })()}
                `
              )
              .join("")
          : `<tr><td colspan="6"><div class="empty-state">暂无设备记录</div></td></tr>`
      }</tbody>
    </table>
  `;
}

function renderDeviceForm() {
  const form = document.getElementById("device-form");
  if (!form) {
    return;
  }
  const submitButton = form.querySelector('button[type="submit"]');
  if (submitButton) {
    submitButton.textContent = state.editingDeviceId ? "保存修改" : "保存";
  }
}

function fillDeviceForm(device) {
  const form = document.getElementById("device-form");
  if (!form || !device) {
    return;
  }
  form.elements.szName.value = device.szName || "";
  form.elements.szManageIP.value = device.szManageIP || "";
  form.elements.nRpcType.value = String(device.nRpcType || 3);
  form.elements.nRpcPort.value = String(device.nRpcPort || 22);
  form.elements.szRpcUsername.value = device.szRpcUsername || "";
  form.elements.szRpcPassword.value = "";
  form.elements.szRemarks.value = device.szRemarks || "";
  renderDeviceForm();
}

function resetDeviceForm() {
  const form = document.getElementById("device-form");
  if (!form) {
    return;
  }
  form.reset();
  form.elements.nRpcType.value = "3";
  form.elements.nRpcPort.value = "22";
  state.editingDeviceId = null;
  renderDeviceForm();
}

function setDeviceFormModal(open) {
  const modal = document.querySelector("[data-device-form-backdrop]");
  if (!modal) return;
  modal.hidden = !open;
  if (open) {
    document.querySelector('#device-form input[name="szName"]')?.focus();
  }
}

// 模块导航的 hashchange 只绑定一次。
let moduleHashListenerBound = false;
let registrationDialogReturnFocus = null;
let registrationStatusTimer = null;

const REGISTRATION_STATUS_REFRESH_INTERVAL = 60 * 60 * 1000;

function normalizeRegistrationInfo(value = {}) {
  const record = value.record && typeof value.record === "object" ? value.record : null;
  return {
    registered: Boolean(value.registered && record),
    expired: Boolean(value.expired),
    expiresAt: String(value.dtExpiresAt || ""),
    remainingDays: Math.max(0, toInt(value.nRemainingDays, 0)),
    record: record ? {
      nId: toInt(record.nId, 0),
      szCompanyName: String(record.szCompanyName || ""),
      szMachineId: String(record.szMachineId || ""),
      szSerialNumber: String(record.szSerialNumber || ""),
      szAesKey: String(record.szAesKey || ""),
      szRandomCode: String(record.szRandomCode || ""),
      dtRegistrationDate: String(record.dtRegistrationDate || ""),
      nValidDuration: toInt(record.nValidDuration, 30) || 30,
      szRemarks: String(record.szRemarks || "")
    } : null
  };
}

function currentDateTimeInputValue() {
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 16);
}

function registrationDateInputValue(value) {
  const normalized = String(value || "").trim().replace(" ", "T");
  return normalized ? normalized.slice(0, 16) : currentDateTimeInputValue();
}

function generateRegistrationAESKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
}

function renderRegistrationStatus() {
  const root = document.getElementById("registration-status");
  if (!root) return;

  const registration = state.registration;
  const loading = registration.status === "loading";
  root.tabIndex = loading ? -1 : 0;
  root.setAttribute("role", "button");
  root.setAttribute("aria-disabled", String(loading));
  root.setAttribute("aria-label", loading ? "正在读取注册状态" : "打开注册信息");

  if (loading) {
    root.innerHTML = '<div class="registration-validity is-loading" role="status"><strong>正在读取注册状态...</strong></div>';
  } else if (registration.status === "error") {
    root.innerHTML = `<div class="registration-warning" role="alert"><strong>注册状态读取失败</strong><span>${escapeHTML(registration.error)}</span></div>`;
  } else if (registration.status === "ready" && registration.expired) {
    root.innerHTML = `<div class="registration-warning" role="alert"><strong>注册信息已过期</strong><span>到期时间：${escapeHTML(registration.expiresAt || "--")}，请及时更新注册信息。</span></div>`;
  } else if (registration.status === "ready" && !registration.registered) {
    root.innerHTML = '<div class="registration-warning" role="alert"><strong>系统未注册</strong><span>请打开注册信息并完成登记。</span></div>';
  } else if (registration.status === "ready") {
    root.innerHTML = `<div class="registration-validity" role="status"><strong>注册有效，距离到期 ${escapeHTML(registration.remainingDays)} 天</strong><span>到期时间：${escapeHTML(registration.expiresAt || "--")}</span></div>`;
  } else {
    root.innerHTML = "";
  }
}

async function loadRegistrationStatus() {
  state.registration = { ...state.registration, status: "loading", error: "" };
  renderRegistrationStatus();
  try {
    const payload = await api.loadRegistrationInfo();
    const info = normalizeRegistrationInfo(payload.data);
    state.registration = { status: "ready", ...info, error: "" };
  } catch (error) {
    state.registration = {
      ...state.registration,
      status: "error",
      error: error.message || "注册状态读取失败。"
    };
  }
  renderRegistrationStatus();
}

function renderRegistrationDialog() {
  const root = document.getElementById("registration-dialog-root");
  const dialog = state.registrationDialog;
  if (!root) return;
  if (!dialog) {
    root.innerHTML = "";
    return;
  }

  const record = dialog.record;
  // 已有注册记录时的保存动作：
  //   - 序列号记录：按云平台授权窗口刷新，无需填写注册码；
  //   - 注册码记录：填新注册码则“原剩余+新有效时长”累加有效时长，留空则只保存公司名称/备注。
  const serialRecord = state.registration.registered && !String(record.szSerialNumber || "").trim().startsWith("TXR1.");
  const registeredHint = !state.registration.registered
    ? ""
    : serialRecord
      ? "当前为序列号注册，无需填写注册码：保存将按云平台授权窗口刷新有效期，公司名称/备注可直接修改。"
      : "填写新的本机注册码后保存，将按「原剩余时长+注册码有效时长」累加有效期；不填写注册码则只保存公司名称/备注，不改变有效期。";
  const serialLabel = !state.registration.registered
    ? "注册码(请输入序列号或注册码)"
    : serialRecord
      ? "注册码(序列号注册无需填写)"
      : "注册码(请输入新的注册码)";
  const submitLabel = dialog.pending
    ? "保存中..."
    : (state.registration.registered ? "保存" : "保存注册信息");
  root.innerHTML = `
    <div class="registration-dialog-backdrop" data-registration-backdrop>
      <section class="registration-dialog" role="dialog" aria-modal="true" aria-labelledby="registration-dialog-title">
        <header class="registration-dialog__header">
          <div><h3 id="registration-dialog-title">注册信息</h3><span class="settings-status${state.registration.registered && !state.registration.expired ? " is-enabled" : ""}">${state.registration.expired ? "已过期" : state.registration.registered ? "已注册" : "待注册"}</span></div>
          <button type="button" class="port-dialog__close" data-action="close-registration" aria-label="关闭注册信息" title="关闭"${dialog.pending ? " disabled" : ""}>&times;</button>
        </header>
        <form id="registration-form" class="registration-form">
          ${dialog.error ? `<div class="settings-dialog__error" role="alert">${escapeHTML(dialog.error)}</div>` : ""}
          ${state.registration.registered ? `<div class="registration-expiration${state.registration.expired ? " is-expired" : ""}"><span>到期时间</span><strong>${escapeHTML(state.registration.expiresAt || "--")}</strong><small>${state.registration.expired ? "注册已过期" : `剩余 ${escapeHTML(state.registration.remainingDays)} 天`}</small></div>` : ""}
          <fieldset${dialog.pending ? " disabled" : ""}>
            <div class="registration-form__fields">
              <label class="settings-field"><span>公司名称</span><input name="szCompanyName" maxlength="256" value="${escapeHTML(record.szCompanyName)}" required></label>
              <div class="settings-field registration-aes-field"><label for="registration-aes-key">AES 密钥</label><span class="registration-input-action"><input id="registration-aes-key" name="szAesKey" maxlength="256" autocomplete="off" spellcheck="false" value="${escapeHTML(record.szAesKey)}" required><button type="button" class="button button--small" data-action="generate-registration-key">随机生成</button></span></div>
              <label class="settings-field settings-field--wide"><span>${serialLabel}</span><textarea id="registration-serial-number" name="szSerialNumber" rows="3" maxlength="256" autocomplete="off" spellcheck="false" placeholder="${serialRecord ? "序列号注册无需填写注册码" : state.registration.registered ? "请输入主注册服务签发的注册码" : ""}">${state.registration.registered ? "" : escapeHTML(record.szSerialNumber)}</textarea></label>
              <label class="settings-field settings-field--wide"><span>备注</span><input name="szRemarks" maxlength="256" value="${escapeHTML(record.szRemarks)}"></label>
              <label class="settings-field"><span>机器码</span><input name="szMachineId" maxlength="256" value="${escapeHTML(record.szMachineId)}" readonly required></label>
              <label class="settings-field"><span>随机码</span><input name="szRandomCode" minlength="18" maxlength="18" value="${escapeHTML(record.szRandomCode)}" readonly required></label>
              <label class="settings-field"><span>注册日期</span><input name="dtRegistrationDate" type="datetime-local" value="${escapeHTML(registrationDateInputValue(record.dtRegistrationDate))}" readonly required></label>
              <label class="settings-field"><span>有效时长（天）</span><input name="nValidDuration" type="number" min="1" step="1" value="${escapeHTML(record.nValidDuration)}" readonly required></label>
            </div>
            ${registeredHint ? `<div class="registration-form__hint">${escapeHTML(registeredHint)}</div>` : ""}
            <footer class="registration-form__actions">
              <button type="button" class="button" data-action="close-registration">取消</button>
              <button type="submit" class="button button--primary"${dialog.pending ? " disabled" : ""}>${submitLabel}</button>
            </footer>
          </fieldset>
        </form>
      </section>
    </div>`;
}

function closeRegistrationDialog() {
  if (state.registrationDialog?.pending) return;
  const returnFocus = registrationDialogReturnFocus;
  registrationDialogReturnFocus = null;
  state.registrationDialog = null;
  renderRegistrationDialog();
  requestAnimationFrame(() => returnFocus?.isConnected && returnFocus.focus());
}

async function openRegistrationDialog() {
  if (state.registration.status === "loading") return;
  registrationDialogReturnFocus = document.activeElement;
  await loadRegistrationStatus();
  const record = state.registration.record || {
    nId: 0,
    szCompanyName: "",
    szMachineId: "",
    szSerialNumber: "",
    szAesKey: "",
    szRandomCode: "",
    dtRegistrationDate: currentDateTimeInputValue(),
    nValidDuration: 30,
    szRemarks: ""
  };
  state.registrationDialog = {
    pending: false,
    error: state.registration.status === "error" ? state.registration.error : "",
    record: { ...record }
  };
  renderRegistrationDialog();
  requestAnimationFrame(() => document.querySelector('#registration-form input[name="szCompanyName"]')?.focus());
}

async function submitRegistrationForm(form) {
  const dialog = state.registrationDialog;
  if (!dialog || dialog.pending) return;
  const values = collectFormData(form);
  const record = {
    nId: toInt(dialog.record.nId, 0),
    szCompanyName: values.szCompanyName,
    szMachineId: values.szMachineId,
    szSerialNumber: values.szSerialNumber,
    szAesKey: values.szAesKey,
    szRandomCode: values.szRandomCode,
    dtRegistrationDate: values.dtRegistrationDate,
    nValidDuration: toInt(values.nValidDuration, 0),
    szRemarks: values.szRemarks,
    nBrowserTime: Date.now()
  };
  dialog.record = record;
  dialog.pending = true;
  dialog.error = "";
  renderRegistrationDialog();
  try {
    await api.saveRegistrationInfo(record);
    if (state.registrationDialog !== dialog) return;
    window.location.reload();
  } catch (error) {
    if (state.registrationDialog === dialog) {
      dialog.pending = false;
      dialog.error = error.message || "注册信息保存失败。";
      renderRegistrationDialog();
      notify("error", dialog.error);
    }
  }
}

function getConfigPayload(payload) {
  const value = Array.isArray(payload?.data) ? payload.data[0] : payload?.data;
  return value && typeof value === "object" ? value : {};
}

function normalizeSnifferConfig(value = {}) {
  return {
    nEnable: toInt(value.nEnable, 0) === 0 ? 0 : 1,
    nType: toInt(value.nType, 2) === 1 ? 1 : 2,
    szDailyTime: String(value.szDailyTime || "").trim(),
    nIntervalSecond: toInt(value.nIntervalSecond, 300) || 300
  };
}

function normalizeCollectionSettings(value = {}) {
  return {
    nEnable: toInt(value.nEnable, 0) === 0 ? 0 : 1,
    nIntervalSecond: toInt(value.nIntervalSecond, 300) || 300,
    nRetentionPeriod: toInt(value.nRetentionPeriod, 7) || 7
  };
}

// 设置（原采集配置）：右侧内容区根 TAB 之一（与“面板 / VLAN”平级）。
// renderCollectionPanelContent 返回该 TAB 的内容 HTML（纯函数，由 renderOverview 调用）。
function renderCollectionPanelContent() {
  const dialog = state.collectionPanel;
  const pending = Boolean(dialog?.saving);
  let body = `<div class="settings-dialog__loading device-loading"><span class="device-loading__spinner" aria-hidden="true"></span><strong>正在读取设置...</strong></div>`;
  if (dialog && dialog.status === "error") {
    body = `<div class="settings-dialog__load-error" role="alert"><strong>配置读取失败</strong><span>${escapeHTML(dialog.error || "请求失败")}</span><button type="button" class="button" data-action="retry-settings">重新加载</button></div>`;
  } else if (dialog && dialog.status === "ready") {
    const sniffer = dialog.sniffer;
    const collection = dialog.collection;
    const snifferSaving = dialog.saving === "sniffer";
    const collectionSaving = dialog.saving === "collection";
    body = `
      ${dialog.error ? `<div class="settings-dialog__error" role="alert">${escapeHTML(dialog.error)}</div>` : ""}
      <form id="sniffer-config-form" class="settings-form">
        <fieldset${pending ? " disabled" : ""}>
          <header class="settings-form__header">
            <div><h4>交换机在线嗅探</h4><span class="settings-status${sniffer.nEnable ? " is-enabled" : ""}">${sniffer.nEnable ? "已启用" : "已停用"}</span></div>
            <label class="settings-toggle"><input type="checkbox" name="nEnable" value="1"${sniffer.nEnable ? " checked" : ""}><span>启用</span></label>
          </header>
          <div class="settings-form__fields">
            <div class="settings-field settings-field--wide"><span>执行方式</span><div class="settings-segmented" role="radiogroup" aria-label="嗅探执行方式"><label><input type="radio" name="nType" value="2"${sniffer.nType === 2 ? " checked" : ""}><span>周期执行</span></label><label><input type="radio" name="nType" value="1"${sniffer.nType === 1 ? " checked" : ""}><span>定时执行</span></label></div></div>
            <label class="settings-field" data-sniffer-interval${sniffer.nType === 1 ? " hidden" : ""}><span>嗅探间隔（秒）</span><input type="number" name="nIntervalSecond" min="30" max="86400" step="1" value="${escapeHTML(sniffer.nIntervalSecond)}"${sniffer.nType === 1 ? " disabled" : ""} required></label>
            <label class="settings-field" data-sniffer-daily${sniffer.nType !== 1 ? " hidden" : ""}><span>每日执行时间</span><input type="time" name="szDailyTime" value="${escapeHTML(sniffer.szDailyTime)}"${sniffer.nType !== 1 ? " disabled" : ""} required></label>
          </div>
          <footer class="settings-form__actions"><button type="submit" class="button button--primary">${snifferSaving ? "保存中..." : "保存嗅探配置"}</button></footer>
        </fieldset>
      </form>
      <form id="collection-config-form" class="settings-form">
        <fieldset${pending ? " disabled" : ""}>
          <header class="settings-form__header">
            <div><h4>交换机接口采集</h4><span class="settings-status${collection.nEnable ? " is-enabled" : ""}">${collection.nEnable ? "已启用" : "已停用"}</span></div>
            <label class="settings-toggle"><input type="checkbox" name="nEnable" value="1"${collection.nEnable ? " checked" : ""}><span>启用</span></label>
          </header>
          <div class="settings-form__fields">
            <label class="settings-field"><span>采集间隔（秒）</span><input type="number" name="nIntervalSecond" min="1" max="86400" step="1" value="${escapeHTML(collection.nIntervalSecond)}" required></label>
            <label class="settings-field"><span>数据保留（天）</span><input type="number" name="nRetentionPeriod" min="1" max="3650" step="1" value="${escapeHTML(collection.nRetentionPeriod)}" required></label>
          </div>
          <footer class="settings-form__actions"><button type="submit" class="button button--primary">${collectionSaving ? "保存中..." : "保存采集配置"}</button></footer>
        </fieldset>
      </form>`;
  }

  return `<div class="device-monitor" role="region" aria-label="设置">
      <div class="collection-panel__toolbar">
        <span>配置保存后由后台任务按新参数执行（与“面板 / VLAN”并列的全局配置，不随设备切换）。</span>
        <button type="button" class="button button--small" data-action="retry-settings"${pending ? " disabled" : ""}>重新读取</button>
      </div>
      ${body}
    </div>`;
}

// renderCollectionPanel 刷新“设置” TAB 的内容：只有当前选中的是该 TAB 时才重绘。
function renderCollectionPanel() {
  if (state.overviewActiveTab === "collection") {
    renderOverview();
  }
}

// loadCollectionPanel 读取采集 / 在线嗅探配置并渲染到右侧面板（保存成功后同样回到这里重渲染）。
async function loadCollectionPanel() {
  const dialog = { status: "loading", error: "", saving: "", sniffer: null, collection: null };
  state.collectionPanel = dialog;
  renderCollectionPanel();
  try {
    const [snifferPayload, collectionPayload] = await Promise.all([
      api.loadSnifferConfig(),
      api.loadCollectionSettings()
    ]);
    if (state.collectionPanel !== dialog) return;
    dialog.sniffer = normalizeSnifferConfig(getConfigPayload(snifferPayload));
    dialog.collection = normalizeCollectionSettings(getConfigPayload(collectionPayload));
    dialog.status = "ready";
    renderCollectionPanel();
  } catch (error) {
    if (state.collectionPanel !== dialog) return;
    dialog.status = "error";
    dialog.error = error.message || "设置读取失败。";
    renderCollectionPanel();
  }
}

async function submitSettingsForm(form, kind) {
  const dialog = state.collectionPanel;
  if (!dialog || dialog.status !== "ready" || dialog.saving) return;
  const data = collectFormData(form);
  let payload;
  if (kind === "sniffer") {
    payload = {
      nEnable: data.nEnable ? 1 : 0,
      nType: toInt(data.nType, dialog.sniffer.nType),
      szDailyTime: data.szDailyTime ?? dialog.sniffer.szDailyTime,
      nIntervalSecond: toInt(data.nIntervalSecond, dialog.sniffer.nIntervalSecond)
    };
  } else {
    payload = {
      nEnable: data.nEnable ? 1 : 0,
      nIntervalSecond: toInt(data.nIntervalSecond, dialog.collection.nIntervalSecond),
      nRetentionPeriod: toInt(data.nRetentionPeriod, dialog.collection.nRetentionPeriod)
    };
  }

  dialog.saving = kind;
  dialog.error = "";
  renderCollectionPanel();
  try {
    const response = kind === "sniffer"
      ? await api.saveSnifferConfig(payload)
      : await api.saveCollectionSettings(payload);
    if (state.collectionPanel !== dialog) return;
    const saved = { ...payload, ...getConfigPayload(response) };
    if (kind === "sniffer") {
      dialog.sniffer = normalizeSnifferConfig(saved);
      notify("success", "在线嗅探配置已保存并生效。");
    } else {
      dialog.collection = normalizeCollectionSettings(saved);
      notify("success", "接口采集配置已保存并生效。");
    }
  } catch (error) {
    if (state.collectionPanel === dialog) {
      dialog.error = error.message || "设置保存失败。";
      notify("error", dialog.error);
    }
  } finally {
    if (state.collectionPanel === dialog) {
      dialog.saving = "";
      renderCollectionPanel();
    }
  }
}

// ── 设备选择记忆（localStorage）：避免“一进 VLAN 就撞上不可达设备” ──
// 优先级：上次**成功实时连过**的设备 > 上次选中的设备 > 台账第一台。
const DEVICE_PREFERENCE_KEY = "switchweb.devicePreference";

function readDevicePreference() {
  try {
    const raw = window.localStorage?.getItem(DEVICE_PREFERENCE_KEY);
    const parsed = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writeDevicePreference(patch) {
  try {
    const next = { ...readDevicePreference(), ...patch };
    window.localStorage?.setItem(DEVICE_PREFERENCE_KEY, JSON.stringify(next));
  } catch {
    // 隐私模式 / 无 localStorage 时忽略，功能不受影响
  }
}

function rememberExpandedDevice(deviceId) {
  const id = toInt(deviceId, 0);
  if (id) writeDevicePreference({ lastDeviceId: id });
}

// 实时查询成功（设备 VLAN / 端口 VLAN / 实时采集）时记录，下次默认优先选它。
function rememberWorkingDevice(deviceId) {
  const id = toInt(deviceId, 0);
  if (id) writeDevicePreference({ lastDeviceId: id, lastWorkingDeviceId: id });
}

function preferredDeviceId() {
  const preference = readDevicePreference();
  return (
    [preference.lastWorkingDeviceId, preference.lastDeviceId]
      .map((value) => toInt(value, 0))
      .filter(Boolean)
      .find((id) => state.devices.some((item) => toInt(item.nId, 0) === id)) || 0
  );
}

// describeDeviceError 把设备侧/网络的原始错误翻译成可执行的排查提示；
// 参数类错误（如 port is required）原样返回，不做猜测。原始文本仍保留在 title 中便于定位。
function describeDeviceError(message, device) {
  const raw = String(message || "").trim();
  const where = device
    ? `${device.szName || "--"}（${device.szManageIP || "--"}:${device.nRpcPort || "--"}，${formatRpcTypeLabel(device.nRpcType)}）`
    : "设备";
  if (/context deadline exceeded|i\/o timeout|request timeout|timeout of|超时/i.test(raw)) {
    return {
      text: `${where} 实时查询超时：设备不可达或管理服务无响应（30 秒超时）。请确认设备在线、管理口可达；RPC-API 设备还需确认设备侧 RPC 服务已启动。`,
      raw
    };
  }
  if (/connection refused|拒绝连接/i.test(raw)) {
    return { text: `${where} 目标端口拒绝连接：设备侧管理服务未启动或被防火墙拦截。`, raw };
  }
  if (/no route to host|network is unreachable|no route/i.test(raw)) {
    return { text: `${where} 网络不可达：请检查管理网路由 / 网段是否可达。`, raw };
  }
  if (/auth|authentication|unauthorized|login failed|password|密码|认证/i.test(raw)) {
    return { text: `${where} 登录被拒绝：账号或密码不正确（可用“修改设备”重新保存密码）。`, raw };
  }
  if (/rpc error: read response|rpc error: send request/i.test(raw)) {
    return { text: `${where} RPC 服务无响应：TCP 端口可连但设备没有返回数据。`, raw };
  }
  return { text: raw, raw };
}

async function loadDevices() {
  const devicesPayload = await api.listDevices();
  state.devices = Array.isArray(devicesPayload.data) ? devicesPayload.data : [];
  const availableDeviceIds = new Set(state.devices.map((item) => toInt(item.nId, 0)).filter(Boolean));
  state.selectedDeviceIds = new Set([...state.selectedDeviceIds].filter((id) => availableDeviceIds.has(id)));
  if (!state.devices.length) {
    state.expandedDeviceId = null;
  } else if (!state.devices.some((item) => toInt(item.nId, 0) === state.expandedDeviceId)) {
    // 优先沿用“上次成功实时连过”的设备，其次上次选中，最后才回落到台账第一台。
    state.expandedDeviceId = preferredDeviceId() || toInt(state.devices[0].nId, 0);
  }
  renderDeviceList();
  renderDevices();
}

async function loadLocalDeviceInformation(hostip) {
  const payload = await api.loadDeviceInformation(hostip);
  const record = payload.data;
  let detail = record?.Data ?? record?.data;
  if (typeof detail === "string") {
    try {
      detail = JSON.parse(detail);
    } catch {
      detail = null;
    }
  }
  if (!detail || typeof detail !== "object") {
    throw new Error("本地设备信息格式无效。");
  }
  return detail;
}

async function refreshAll() {
  setLoading(true);
  try {
    await loadDevices();
    const selected = state.devices.find((item) => toInt(item.nId, 0) === state.expandedDeviceId);
    if (selected && !state.deviceConnectionDetails[state.expandedDeviceId]) {
      try {
        const detail = await loadLocalDeviceInformation(String(selected.szManageIP || "").trim());
        state.deviceConnectionDetails[state.expandedDeviceId] = normalizeDeviceDetail(detail, selected, "本地保存信息");
      } catch {
        // A device may not have cached information yet.
      }
    }
    renderOverview();
    notify("success", "交换机工作台数据已刷新。");
  } catch (error) {
    notify("error", error.message);
  } finally {
    setLoading(false);
  }
}

async function buildDeviceConnectionRequest(device, extra = {}) {
  const hostip = String(device.szManageIP || "").trim();
  const username = String(device.szRpcUsername || "").trim();
  const password = String(device.szRpcPassword || "").trim();
  const maker = inferDeviceMaker(device);

  if (!hostip) {
    throw new Error("该设备缺少管理 IP。");
  }
  if (!username || !password) {
    throw new Error("该设备缺少连接用户名或密码。");
  }
  if (!maker) {
    throw new Error("该设备缺少名称，无法生成连接参数。");
  }

  const encrypted = await api.encryptSwitchCredentials({
    szUsername: username,
    szPassword: password
  });

  return {
    nDeviceId: toInt(device.nId),
    nType: toInt(device.nRpcType, 3),
    szHostip: hostip,
    nPort: toInt(device.nRpcPort, toInt(device.nRpcType, 3) === 2 ? 23 : 22),
    szUsername: encrypted.data?.szUsername || "",
    szPassword: encrypted.data?.szPassword || "",
    szMaker: maker,
    ...extra
  };
}

async function connectDevice(device) {
  return api.connectDevice(await buildDeviceConnectionRequest(device));
}

async function connectSelectedDevice() {
  const device = state.devices.find((item) => toInt(item.nId, 0) === state.expandedDeviceId);
  if (!device) {
    notify("error", "请先选择设备。");
    return;
  }
  const deviceID = toInt(device.nId, 0);
  state.connectingDeviceId = deviceID;
  state.viewingDeviceId = null;
  renderDeviceList();
  renderDevices();
  setLoading(true);
  try {
    const payload = await connectDevice(device);
    state.deviceConnectionDetails[deviceID] = normalizeDeviceDetail(payload.data || null, device, "实时信息");
    rememberWorkingDevice(deviceID);
    await loadDevices();
    renderOverview();
    notify("success", `设备「${device.szName || device.szManageIP}」连接成功。`);
  } catch (error) {
    notify("error", describeDeviceError(error?.message, device).text);
  } finally {
    state.connectingDeviceId = null;
    renderDeviceList();
    renderDevices();
    setLoading(false);
  }
}

async function loadDeviceVlanList(deviceId, device) {
  const maker = inferDeviceMaker(device).toLowerCase();
  if (!/centec|盛科|huawei|华为|h3c|华三|ruijie|锐捷/i.test(maker)) return;
  state.deviceVlanLists[deviceId] = { status: "loading", data: [] };
  renderDevices();
  renderOverview();
  try {
    const payload = await api.listDeviceVlans(await buildDeviceConnectionRequest(device));
    state.deviceVlanLists[deviceId] = { status: "ready", data: Array.isArray(payload.data) ? payload.data : [] };
    rememberWorkingDevice(deviceId);
  } catch (error) {
    state.deviceVlanLists[deviceId] = { status: "error", error: error.message || "VLAN 信息读取失败。" };
  }
  renderDevices();
  renderOverview();
}

async function refreshDeviceVlanList(button) {
  const deviceId = toInt(button?.dataset.id, 0);
  const device = state.devices.find((item) => toInt(item.nId, 0) === deviceId);
  if (!device) {
    notify("error", "未找到需要刷新 VLAN 的设备。");
    return;
  }
  await loadDeviceVlanList(deviceId, device);
}

async function mutateDeviceVlan(action, deviceId, device, vlan, name = "") {
  const request = await buildDeviceConnectionRequest(device, { szVlan: vlan, szVlanName: name });
  if (action === "create") await api.createDeviceVlans(request);
  if (action === "delete") await api.deleteDeviceVlans(request);
  if (action === "rename") await api.renameDeviceVlan(request);
  await loadDeviceVlanList(deviceId, device);
  const successMessages = {
    create: "VLAN 已新增并刷新列表。",
    rename: "VLAN 名称已修改并刷新列表。",
    delete: "VLAN 已删除并刷新列表。"
  };
  notify("success", successMessages[action] || "设备 VLAN 配置已更新。");
}

function renderVlanDialog() {
  const root = document.getElementById("vlan-dialog-root");
  const dialog = state.vlanDialog;
  if (!root) return;
  if (!dialog) { root.innerHTML = ""; return; }
  const remove = dialog.mode === "delete";
  root.innerHTML = `<div class="port-dialog-backdrop" data-vlan-backdrop><section class="port-dialog" role="dialog" aria-modal="true" aria-labelledby="vlan-dialog-title"><header class="port-dialog__header"><div><h3 id="vlan-dialog-title">${remove ? "删除 VLAN" : dialog.mode === "rename" ? "修改 VLAN 名称" : "新增 VLAN"}</h3></div><button type="button" class="port-dialog__close" data-action="close-vlan-dialog" aria-label="关闭">&times;</button></header><form id="vlan-dialog-form" class="port-dialog__body vlan-dialog__body" novalidate><div class="vlan-dialog__notice">${remove ? "删除后设备将立即移除该 VLAN 配置，请确认操作。" : dialog.mode === "rename" ? "仅修改 VLAN 名称，不会影响成员端口。" : "可填写单个 VLAN 或多个 VLAN，例如 13,14,17-20。"}</div>${dialog.error ? `<div class="vlan-dialog__error" role="alert">${escapeHTML(dialog.error)}</div>` : ""}<label><span>VLAN ID</span><input name="vlan" type="text" value="${escapeHTML(dialog.vlan || "")}" ${dialog.mode === "rename" || remove ? "readonly" : "required"}></label>${dialog.mode === "rename" ? `<label><span>VLAN 名称</span><input name="name" maxlength="64" value="${escapeHTML(dialog.name || "")}" placeholder="例如：办公网" required></label>` : remove ? `<div class="vlan-dialog__target">待删除 VLAN：<strong>${escapeHTML(dialog.vlan)}</strong></div>` : ""}<div class="vlan-dialog__actions"><button type="button" class="button" data-action="close-vlan-dialog">取消</button><button type="submit" class="button ${remove ? "button--danger" : "button--primary"}">${remove ? "确认删除" : "保存"}</button></div></form></section></div>`;
  if (dialog.pending) {
    const progressLabels = {
      create: ["正在新增 VLAN...", "正在下发新增配置并刷新 VLAN 列表，请稍候。"],
      rename: ["正在修改 VLAN 名称...", "正在保存新名称并刷新 VLAN 列表，请稍候。"],
      delete: ["正在删除 VLAN...", "正在下发删除配置并刷新 VLAN 列表，请稍候。"]
    };
    const [title, message] = progressLabels[dialog.mode] || ["正在更新 VLAN...", "正在下发配置并刷新 VLAN 列表，请稍候。"];
    const form = root.querySelector("#vlan-dialog-form");
    form?.setAttribute("aria-busy", "true");
    form?.querySelector(".vlan-dialog__notice")?.insertAdjacentHTML("afterend", `<div class="vlan-dialog__progress" role="status" aria-live="polite"><span class="device-loading__spinner" aria-hidden="true"></span><span><strong>${title}</strong><small>${message}</small></span></div>`);
    root.querySelectorAll("input, button").forEach((control) => { control.disabled = true; });
    const submitButton = form?.querySelector('button[type="submit"]');
    if (submitButton) submitButton.textContent = title.replace("...", "");
  }
}

function openVlanDialog(mode, deviceId, device, vlan = "", name = "") {
  state.vlanDialog = { mode, deviceId, deviceName: device.szName || device.szManageIP || "设备", vlan, name, pending: false };
  renderVlanDialog();
  document.querySelector("#vlan-dialog-form input:not([readonly])")?.focus();
}

function validateVlanValue(value, allowRange) {
  const text = String(value || "").trim();
  const entries = text.split(",").map((item) => item.trim()).filter(Boolean);
  if (!entries.length || entries.join(",") !== text.replaceAll(" ", "")) return false;
  return entries.every((entry) => {
    const parts = entry.split("-").map((item) => item.trim());
    if (parts.length > (allowRange ? 2 : 1) || !parts.every((item) => /^\d+$/.test(item))) return false;
    const start = Number(parts[0]);
    const end = parts.length === 2 ? Number(parts[1]) : start;
    return start >= 1 && start <= 4094 && end >= start && end <= 4094;
  });
}

function createPortDialog(deviceId, device, port, context = {}) {
  return {
    deviceId,
    deviceName: context.deviceName || device.szName || device.szManageIP || "--",
    interfaceType: context.interfaceType || "--",
    hostip: device.szManageIP || "--",
    port,
    activeTab: "detail",
    trafficOpen: false,
    trafficRange: "oneHour",
    trafficRefreshing: false,
    trafficRefreshError: "",
    trafficUpdatedAt: null,
    opening: true,
    preparing: true,
    portActionPending: false,
    request: null,
    tabs: {
      detail: { status: "idle", data: null, error: "" },
      vlan: { status: "idle", data: null, error: "" },
      mac: { status: "idle", data: null, error: "" }
    }
  };
}

function isCurrentPortDialog(dialog) {
  return state.portDialog === dialog;
}

function normalizePortVlanMapping(data) {
  const mapping = data && typeof data === "object" ? data : {};
  return {
    port: mapping.port ?? mapping.Port ?? "",
    aggregationPort: mapping.aggregationPort ?? mapping.AggregationPort ?? "",
    linkType: mapping.linkType ?? mapping.LinkType ?? "",
    pvid: mapping.pvid ?? mapping.PVID ?? "",
    trunkVlanList: mapping.trunkVlanList ?? mapping.TrunkVLANList ?? "",
    portDescription: mapping.portDescription ?? mapping.PortDescription ?? ""
  };
}

async function loadOverviewPortVlan(deviceId, device, port) {
  const detail = state.deviceConnectionDetails[deviceId];
  const portState = getOverviewVlanPortState(deviceId, detail?.interfaces || []);
  if (!device || !port || portState.status === "loading") return;

  portState.port = port;
  portState.status = "loading";
  portState.error = "";
  renderOverview();
  try {
    const request = await buildDeviceConnectionRequest(device, { szInterface: port });
    const payload = await api.loadPortVlan(request);
    if (state.expandedDeviceId !== deviceId || portState.port !== port) return;
    portState.request = request;
    portState.data = normalizePortVlanMapping(payload.data);
    portState.status = "ready";
    rememberWorkingDevice(deviceId);
  } catch (error) {
    if (state.expandedDeviceId !== deviceId || portState.port !== port) return;
    portState.status = "error";
    portState.error = error.message || "接口 VLAN 信息读取失败。";
  }
  renderOverview();
}

async function ensureOverviewVlanData() {
  if (state.overviewActiveTab !== "vlan") return;
  const deviceId = state.expandedDeviceId;
  const device = state.devices.find((item) => toInt(item.nId, 0) === deviceId);
  if (!device) return;
  const detail = state.deviceConnectionDetails[deviceId];
  const portState = getOverviewVlanPortState(deviceId, detail?.interfaces || []);
  const tasks = [];
  if (!state.deviceVlanLists[deviceId]) tasks.push(loadDeviceVlanList(deviceId, device));
  if (portState.port && portState.status === "idle") tasks.push(loadOverviewPortVlan(deviceId, device, portState.port));
  await Promise.all(tasks);
}

async function updateOverviewPortVlan(action) {
  const deviceId = state.expandedDeviceId;
  const device = state.devices.find((item) => toInt(item.nId, 0) === deviceId);
  const detail = state.deviceConnectionDetails[deviceId];
  const portState = getOverviewVlanPortState(deviceId, detail?.interfaces || []);
  const form = document.querySelector("[data-overview-port-vlan-form]");
  if (!device || !portState.port || !form || portState.pending) return;

  if (action === "delete") {
    const confirmed = await confirmAction({
      title: "确认清除 VLAN 配置",
      message: "清除后将立即更新设备接口配置，请确认该接口无需保留当前 VLAN 设置。",
      target: portState.port,
      targetLabel: "接口",
      confirmLabel: "确认清除",
      danger: true
    });
    if (!confirmed) return;
  }

  const mapping = normalizePortVlanMapping(portState.data);
  const isPVIDOnly = /h3c|华三|ruijie|锐捷/i.test(inferDeviceMaker(device));
  const request = {
    ...(portState.request || await buildDeviceConnectionRequest(device, { szInterface: portState.port })),
    szInterface: portState.port,
    port: portState.port,
    aggregationPort: mapping.aggregationPort || "",
    linkType: isPVIDOnly ? "" : (form.querySelector('[name="linkType"]')?.value || "access"),
    pvid: toInt(form.querySelector('[name="pvid"]')?.value, 1),
    trunkVlanList: isPVIDOnly ? "" : (form.querySelector('[name="trunkVlanList"]')?.value || "")
  };

  portState.pending = true;
  portState.error = "";
  renderOverview();
  setLoading(true);
  try {
    await api.savePortVlan(request, action === "delete" ? 3 : 2);
    portState.status = "idle";
    await Promise.all([
      loadOverviewPortVlan(deviceId, device, portState.port),
      loadDeviceVlanList(deviceId, device)
    ]);
    notify("success", action === "delete" ? "接口 VLAN 配置已清除。" : "接口 VLAN 配置已应用。");
  } catch (error) {
    portState.status = "error";
    portState.error = error.message || "接口 VLAN 操作失败。";
    notify("error", describeDeviceError(portState.error, device).text);
  } finally {
    portState.pending = false;
    renderOverview();
    setLoading(false);
  }
}

async function loadPortDialogTab(tabName) {
  const dialog = state.portDialog;
  if (!dialog || !dialog.request || !dialog.tabs[tabName]) {
    return;
  }
  const tab = dialog.tabs[tabName];
  if (tab.status === "loading" || tab.status === "ready") {
    return;
  }

  tab.status = "loading";
  tab.error = "";
  renderPortDialog();
  try {
    const loaders = {
      detail: api.loadPortDetail,
      vlan: api.loadPortVlan,
      mac: api.lookupTerminalMac
    };
    const payload = await loaders[tabName](dialog.request);
    if (!isCurrentPortDialog(dialog)) {
      return;
    }
    tab.status = "ready";
    tab.data = tabName === "vlan" ? normalizePortVlanMapping(payload.data) : payload.data;
    if (tabName === "detail") {
      dialog.trafficUpdatedAt = new Date();
      dialog.trafficRefreshError = "";
    }
    if (tabName === "detail" && payload.data && Object.hasOwn(payload.data, "terminalMAC")) {
      const macError = String(payload.data.terminalMACError || "").trim();
      dialog.tabs.mac.status = macError ? "error" : "ready";
      dialog.tabs.mac.data = Array.isArray(payload.data.terminalMAC) ? payload.data.terminalMAC : [];
      dialog.tabs.mac.error = macError;
    }
  } catch (error) {
    if (!isCurrentPortDialog(dialog)) {
      return;
    }
    tab.status = "error";
    tab.error = error.message || "端口信息读取失败。";
  }
  renderPortDialog();
}

async function openPortDialog(deviceId, device, port, context = {}) {
  const dialog = createPortDialog(deviceId, device, port, context);
  state.portDialog = dialog;
  renderPortDialog();
  dialog.opening = false;
  try {
    dialog.request = await buildDeviceConnectionRequest(device, { szInterface: port });
    if (!isCurrentPortDialog(dialog)) {
      return;
    }
    dialog.preparing = false;
    renderPortDialog();
    await loadPortDialogTab(dialog.activeTab);
  } catch (error) {
    if (!isCurrentPortDialog(dialog)) {
      return;
    }
    dialog.preparing = false;
    dialog.tabs[dialog.activeTab].status = "error";
    dialog.tabs[dialog.activeTab].error = error.message || "无法构建端口查询参数。";
    renderPortDialog();
  }
}

function formatTrafficRate(value) {
  let rate = Number(value);
  if (!Number.isFinite(rate) || rate < 0) {
    return "--";
  }
  const units = ["bps", "Kbps", "Mbps", "Gbps", "Tbps"];
  let unit = 0;
  while (rate >= 1000 && unit < units.length - 1) {
    rate /= 1000;
    unit += 1;
  }
  return `${rate >= 100 || unit === 0 ? Math.round(rate) : rate.toFixed(1)} ${units[unit]}`;
}

function formatTrafficBytes(value) {
  let bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return "--";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let unit = 0;
  while (bytes >= 1024 && unit < units.length - 1) {
    bytes /= 1024;
    unit += 1;
  }
  return `${bytes >= 100 || unit === 0 ? Math.round(bytes) : bytes.toFixed(1)} ${units[unit]}`;
}

function formatTrafficCounter(value) {
  const counter = Number(value);
  return Number.isFinite(counter) && counter >= 0 ? Math.round(counter).toLocaleString("zh-CN") : "--";
}

function formatTrafficTrendTime(value) {
  const text = String(value || "").trim();
  return text.length >= 16 ? text.slice(5, 16) : text || "--";
}

function parseTrafficTrendTimestamp(value) {
  const text = String(value || "").trim();
  if (!text) return null;
  const timestamp = Date.parse(text.includes("T") ? text : text.replace(" ", "T"));
  return Number.isFinite(timestamp) ? timestamp : null;
}

function normalizeTrafficTrendRecords(points) {
  const records = Array.isArray(points) ? points.filter((point) => point && typeof point === "object") : [];
  return records
    .map((point, index) => ({ point, index, timestamp: parseTrafficTrendTimestamp(point.collectedAt) }))
    .sort((left, right) => {
      if (left.timestamp === null || right.timestamp === null) return left.index - right.index;
      return left.timestamp - right.timestamp || left.index - right.index;
    })
    .map(({ point }) => point);
}

function trafficTrendPointX(points, index, left, right) {
  if (points.length === 1) return (left + right) / 2;
  const firstTime = parseTrafficTrendTimestamp(points[0].collectedAt);
  const lastTime = parseTrafficTrendTimestamp(points[points.length - 1].collectedAt);
  const pointTime = parseTrafficTrendTimestamp(points[index].collectedAt);
  if (firstTime !== null && lastTime !== null && pointTime !== null && lastTime > firstTime) {
    return left + ((right - left) * (pointTime - firstTime)) / (lastTime - firstTime);
  }
  return left + ((right - left) * index) / (points.length - 1);
}

function buildTrafficTrendPolyline(points, key, maxBytes) {
  const left = 38;
  const right = 614;
  const top = 14;
  const bottom = 144;
  const height = bottom - top;
  return points
    .map((point, index) => {
      const bytes = Math.max(0, Number(point[key]) || 0);
      const x = trafficTrendPointX(points, index, left, right);
      const y = bottom - (bytes / maxBytes) * height;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

function renderTrafficTrendPoints(points, maxBytes) {
  const top = 14;
  const bottom = 144;
  const height = bottom - top;
  const radius = points.length > 120 ? 1.75 : points.length > 48 ? 2.25 : 3.25;
  return points.map((point, index) => {
    const x = trafficTrendPointX(points, index, 38, 614).toFixed(1);
    const inBytes = Math.max(0, Number(point.inBytes) || 0);
    const outBytes = Math.max(0, Number(point.outBytes) || 0);
    const inY = (bottom - (inBytes / maxBytes) * height).toFixed(1);
    const outY = (bottom - (outBytes / maxBytes) * height).toFixed(1);
    const pointDetail = `${point.collectedAt || "--"}\n入方向：${formatTrafficBytes(inBytes)}\n出方向：${formatTrafficBytes(outBytes)}`;
    return `<g class="traffic-chart__sample"><title>${escapeHTML(pointDetail)}</title><circle class="traffic-chart__dot traffic-chart__dot--in" cx="${x}" cy="${inY}" r="${radius}"></circle><circle class="traffic-chart__dot traffic-chart__dot--out" cx="${x}" cy="${outY}" r="${radius}"></circle></g>`;
  }).join("");
}

function renderTrafficTrendChart(label, points) {
  const records = normalizeTrafficTrendRecords(points);
  if (!records.length) {
    return `
      <article class="traffic-chart">
        <div class="traffic-chart__header"><h5>${escapeHTML(label)}</h5></div>
        <div class="traffic-chart__empty">该时间段暂无采集记录。</div>
      </article>
    `;
  }
  const maxBytes = Math.max(
    1,
    ...records.flatMap((point) => [Math.max(0, Number(point.inBytes) || 0), Math.max(0, Number(point.outBytes) || 0)])
  );
  const inLine = buildTrafficTrendPolyline(records, "inBytes", maxBytes);
  const outLine = buildTrafficTrendPolyline(records, "outBytes", maxBytes);
  const samplePoints = renderTrafficTrendPoints(records, maxBytes);
  const timeLabelIndexes = [...new Set([0, Math.floor((records.length - 1) / 2), records.length - 1])];
  return `
    <article class="traffic-chart">
      <div class="traffic-chart__header">
        <h5>${escapeHTML(label)}</h5>
        <span>${records.length} 个采集点</span>
      </div>
      <div class="traffic-chart__legend"><i class="traffic-chart__in"></i>入方向流量 <i class="traffic-chart__out"></i>出方向流量</div>
      <svg class="traffic-chart__svg" viewBox="0 0 640 180" role="img" aria-label="${escapeHTML(label)}端口累计流量图">
        <path class="traffic-chart__grid" d="M38 14H614M38 57H614M38 100H614M38 144H614M38 14V144"></path>
        <text class="traffic-chart__axis" x="2" y="19">${escapeHTML(formatTrafficBytes(maxBytes))}</text>
        <text class="traffic-chart__axis" x="20" y="148">0</text>
        <polyline class="traffic-chart__line traffic-chart__line--in" points="${inLine}"></polyline>
        <polyline class="traffic-chart__line traffic-chart__line--out" points="${outLine}"></polyline>
        ${samplePoints}
      </svg>
      <div class="traffic-chart__range">${timeLabelIndexes.map((index) => `<span>${escapeHTML(formatTrafficTrendTime(records[index].collectedAt))}</span>`).join("")}</div>
    </article>
  `;
}

function renderPortTrafficCharts(trafficTrend, range = "oneHour") {
  const trend = trafficTrend && typeof trafficTrend === "object" ? trafficTrend : {};
  const ranges = {
    oneHour: ["一小时", "最近一小时"],
    oneDay: ["一天", "最近一天"],
    sevenDays: ["七天", "最近七天"]
  };
  const selectedRange = Object.hasOwn(ranges, range) ? range : "oneHour";
  return `
    <section class="port-traffic-section" aria-labelledby="port-traffic-trend-title">
      <div class="port-traffic-section__header">
        <div><span>历史采集</span><h4 id="port-traffic-trend-title">流量趋势</h4></div>
        <div class="port-traffic-range" role="tablist" aria-label="流量趋势时间范围">
          ${Object.entries(ranges).map(([id, labels]) => `<button type="button" role="tab" data-action="select-traffic-range" data-range="${id}" aria-selected="${selectedRange === id}" class="${selectedRange === id ? "is-active" : ""}">${labels[0]}</button>`).join("")}
        </div>
      </div>
      <div class="traffic-chart-list">${renderTrafficTrendChart(ranges[selectedRange][1], trend[selectedRange])}</div>
    </section>
  `;
}

function renderPortTrafficStats(stats) {
  const packetStats = stats && typeof stats === "object" ? stats : {};
  return `
    <section class="port-traffic-section" aria-labelledby="port-traffic-realtime-title">
      <div class="port-traffic-section__header">
        <div><span class="port-traffic-live"><i aria-hidden="true"></i>实时数据</span><h4 id="port-traffic-realtime-title">实时报文统计</h4></div>
      </div>
      <div class="port-traffic-stats">
        <span class="port-traffic-stat port-traffic-stat--in"><small>入方向速率</small><strong>${escapeHTML(formatTrafficRate(packetStats.inRate))}</strong></span>
        <span class="port-traffic-stat port-traffic-stat--out"><small>出方向速率</small><strong>${escapeHTML(formatTrafficRate(packetStats.outRate))}</strong></span>
        <span class="port-traffic-stat"><small>入方向累计</small><strong>${escapeHTML(formatTrafficBytes(packetStats.inBytes))}</strong></span>
        <span class="port-traffic-stat"><small>出方向累计</small><strong>${escapeHTML(formatTrafficBytes(packetStats.outBytes))}</strong></span>
        <span class="port-traffic-stat"><small>广播报文（入 / 出）</small><strong>${escapeHTML(formatTrafficCounter(packetStats.inBroadcast))} / ${escapeHTML(formatTrafficCounter(packetStats.outBroadcast))}</strong></span>
        <span class="port-traffic-stat"><small>错误报文（入 / 出）</small><strong>${escapeHTML(formatTrafficCounter(packetStats.inError))} / ${escapeHTML(formatTrafficCounter(packetStats.outError))}</strong></span>
        <span class="port-traffic-stat"><small>丢弃报文（入 / 出）</small><strong>${escapeHTML(formatTrafficCounter(packetStats.inDiscard))} / ${escapeHTML(formatTrafficCounter(packetStats.outDiscard))}</strong></span>
        <span class="port-traffic-stat"><small>入 CRC / 入暂停 / 出暂停</small><strong>${escapeHTML(formatTrafficCounter(packetStats.inCRC))} / ${escapeHTML(formatTrafficCounter(packetStats.inPause))} / ${escapeHTML(formatTrafficCounter(packetStats.outPause))}</strong></span>
      </div>
    </section>
  `;
}

function renderPortTrafficDialog() {
  const root = document.getElementById("port-traffic-dialog-root");
  const dialog = state.portDialog;
  if (!root) return;
  if (!dialog?.trafficOpen) {
    root.innerHTML = "";
    return;
  }

  const detailTab = dialog.tabs.detail;
  let content = `<div class="device-loading"><span class="device-loading__spinner" aria-hidden="true"></span><strong>正在采集端口流量...</strong></div>`;
  if (detailTab.status === "error") {
    content = `<div class="port-dialog-error">${escapeHTML(detailTab.error)}</div>`;
  } else if (detailTab.status === "ready") {
    const detail = detailTab.data && typeof detailTab.data === "object" ? detailTab.data : {};
    content = `${dialog.trafficRefreshError ? `<div class="port-dialog-error">${escapeHTML(dialog.trafficRefreshError)}</div>` : ""}${renderPortTrafficStats(detail.packetStats)}${renderPortTrafficCharts(detail.trafficTrend, dialog.trafficRange)}`;
  }

  root.innerHTML = `
    <div class="port-traffic-dialog-backdrop" data-traffic-dialog-backdrop>
      <section class="port-traffic-dialog" role="dialog" aria-modal="true" aria-labelledby="port-traffic-dialog-title">
        <header class="port-traffic-dialog__header">
          <div><h3 id="port-traffic-dialog-title">端口流量监测 ${escapeHTML(dialog.port)}</h3></div>
          <button type="button" class="port-dialog__close" data-action="close-port-traffic" aria-label="关闭流量趋势图" title="关闭">&times;</button>
        </header>
        <div class="port-traffic-dialog__toolbar">
          <span>更新于 ${escapeHTML(formatTimestamp(dialog.trafficUpdatedAt))}</span>
          <button type="button" class="button button--small" data-action="refresh-port-traffic" ${dialog.trafficRefreshing || detailTab.status === "loading" ? "disabled" : ""}>${dialog.trafficRefreshing ? "采集中..." : "刷新数据"}</button>
        </div>
        <div class="port-traffic-dialog__body">${content}</div>
      </section>
    </div>
  `;
}

function closePortTrafficDialog() {
  if (!state.portDialog) return;
  state.portDialog.trafficOpen = false;
  renderPortTrafficDialog();
  requestAnimationFrame(() => document.querySelector('[data-action="open-port-traffic"]')?.focus());
}

async function refreshPortTraffic() {
  const dialog = state.portDialog;
  if (!dialog?.request || dialog.trafficRefreshing) return;
  dialog.trafficRefreshing = true;
  dialog.trafficRefreshError = "";
  renderPortTrafficDialog();
  try {
    const payload = await api.loadPortDetail(dialog.request);
    if (!isCurrentPortDialog(dialog)) return;
    dialog.tabs.detail.status = "ready";
    dialog.tabs.detail.data = payload.data;
    dialog.trafficUpdatedAt = new Date();
    renderPortDialog();
  } catch (error) {
    if (isCurrentPortDialog(dialog)) {
      dialog.trafficRefreshError = error.message || "端口流量刷新失败。";
    }
  } finally {
    if (isCurrentPortDialog(dialog)) {
      dialog.trafficRefreshing = false;
      renderPortTrafficDialog();
    }
  }
}

function renderPortDialogBasicInfo(data, dialog) {
  const detail = data && typeof data === "object" ? data : {};
  const basic = detail.basicInfo || {};
  const stats = detail.packetStats || {};
  const optical = detail.opticalInfo || {};
  const terminalMacEntries = Array.isArray(detail.terminalMAC) ? detail.terminalMAC : [];
  const terminalMacText = terminalMacEntries
    .map((item) => String(item?.mac || "").trim())
    .filter(Boolean)
    .join(", ");
  const terminalMacValue = terminalMacText || (detail.terminalMACError ? `获取失败：${detail.terminalMACError}` : "未查询到");
  const opticalFacts = renderPortOpticalFacts(optical);
  return `
    <section class="port-dialog-section port-power-control" aria-label="基本信息">
      <div class="port-dialog-section__header">
        <div>
          <h4>基本信息</h4>
          <div class="port-basic-info__facts">
            <p>端口名称：${escapeHTML(formatTooltipValue(dialog?.request?.szInterface || dialog?.port))}</p>
            <p>端口速率：${escapeHTML(formatTooltipValue(basic.portSpeed))}</p>
            <p>包错误(入/出)：${escapeHTML(formatTooltipValue(stats.inError))}/${escapeHTML(formatTooltipValue(stats.outError))}</p>
            <p>端口类型：${escapeHTML(formatTooltipValue(dialog?.interfaceType))}</p>
            <p class="port-basic-info__terminal-mac">终端 MAC：${escapeHTML(terminalMacValue)}</p>
            ${opticalFacts}
          </div>
        </div>
      </div>
    </section>
  `;
}

function renderPortDescriptionPanel(data) {
  const detail = data && typeof data === "object" ? data : {};
  const description = detail.currentDescription || "";
  return `
    <div class="port-dialog__basic-info">
      <section class="port-dialog-section port-power-control" aria-label="端口描述">
        <div class="port-dialog-section__header">
          <div>
            <h4>端口描述</h4>
            <div class="port-basic-info__facts">
              <input name="portDescription" maxlength="30" value="${escapeHTML(description)}">
            </div>
            <div class="port-description-form__actions">
              <button type="button" class="button button--primary" data-action="save-port-description">保存描述</button>
            </div>
          </div>
        </div>
      </section>
    </div>
  `;
}

function renderPortVlanPanel(data, dialog) {
  const detail = data && typeof data === "object" ? data : {};
  const vlan = dialog?.tabs?.vlan?.data || detail;
  const linkType = String(vlan.linkType || vlan.LinkType || "access").toLowerCase();
  const pvid = vlan.pvid ?? vlan.PVID ?? 1;
  const trunkVlanList = vlan.trunkVlanList ?? vlan.TrunkVLANList ?? "";
  const maker = String(dialog?.request?.szMaker || "");
  const isPVIDOnly = /h3c|华三|ruijie|锐捷/i.test(maker);
  const pending = dialog?.tabs?.vlan?.status === "loading";
  const error = dialog?.tabs?.vlan?.error || "";
  return `
    <div class="port-dialog__basic-info">
      <section class="port-dialog-section port-power-control" aria-label="VLAN 配置" data-port-vlan-form>
        <div class="port-dialog-section__header">
          <div>
            <h4>VLAN 配置</h4>
            ${error ? `<div class="port-dialog-error" role="alert">${escapeHTML(error)}</div>` : ""}
            <div class="port-basic-info__facts">
              <div class="port-vlan-fields">
                <p class="port-vlan-field"><span>链路类型：</span><select name="linkType" ${isPVIDOnly || pending ? "disabled" : ""}><option value="access" ${linkType === "access" ? "selected" : ""}>Access</option><option value="trunk" ${linkType === "trunk" ? "selected" : ""}>Trunk</option><option value="hybrid" ${linkType === "hybrid" ? "selected" : ""}>Hybrid</option></select></p>
                <p class="port-vlan-field"><span>PVID：</span><input name="pvid" type="number" min="1" max="4094" value="${escapeHTML(pvid)}" ${pending ? "disabled" : ""}></p>
                <p class="port-vlan-field port-vlan-field--allowed"><span>允许 VLAN：</span><input name="trunkVlanList" value="${escapeHTML(trunkVlanList)}" placeholder="例如 100,200" ${isPVIDOnly || pending ? "disabled" : ""}></p>
              </div>
            </div>
            <div class="port-vlan-form__actions">
              <button type="button" class="button button--primary" data-action="save-port-vlan" ${pending ? "disabled" : ""}>${pending ? "应用中..." : "应用配置"}</button>
              <button type="button" class="button button--danger" data-action="delete-port-vlan" ${pending ? "disabled" : ""}>清除配置</button>
            </div>
          </div>
        </div>
      </section>
    </div>
  `;
}

function renderPortPowerPanel(data) {
  const detail = data && typeof data === "object" ? data : {};
  const basic = detail.basicInfo || {};
  const actionPending = Boolean(state.portDialog?.portActionPending);
  const statusTone = resolveInterfaceStatusTone(basic.portStatus);
  return `
    <div class="port-dialog__basic-info">
      <section class="port-dialog-section port-power-control" aria-label="端口启停操作">
        <div class="port-dialog-section__header">
          <div>
            <h4>端口启停</h4>
            <p>当前状态：<strong class="port-power-control__status port-power-control__status--${statusTone}">${escapeHTML(formatTooltipValue(basic.portStatus))}</strong></p>
          </div>
          <div class="port-power-control__actions">
            <button type="button" class="button button--primary" data-action="enable-port" ${actionPending ? "disabled" : ""}>${actionPending ? "操作中..." : "开启端口"}</button>
            <button type="button" class="button button--danger" data-action="disable-port" ${actionPending ? "disabled" : ""}>关闭端口</button>
          </div>
        </div>
      </section>
    </div>
  `;
}

async function updatePortDialogEnabled(portEnabled) {
  const dialog = state.portDialog;
  if (!dialog || !dialog.request || dialog.portActionPending) return;

  const actionLabel = portEnabled ? "开启" : "关闭";
  const confirmed = await confirmAction({
    title: `确认${actionLabel}端口`,
    message: `此操作将立即${actionLabel}设备端口，请确认后继续。`,
    target: dialog.port,
    targetLabel: "端口",
    confirmLabel: `确认${actionLabel}`,
    danger: !portEnabled
  });
  if (!confirmed) return;

  dialog.portActionPending = true;
  renderPortDialog();
  setLoading(true);
  try {
    await api.setPortEnabled(dialog.request, portEnabled ? 1 : 0);
    if (!isCurrentPortDialog(dialog)) return;
    dialog.tabs.detail.status = "idle";
    await loadPortDialogTab("detail");
    notify("success", `端口「${dialog.port}」已${actionLabel}。`);
  } catch (error) {
    if (isCurrentPortDialog(dialog)) {
      notify("error", error.message || `端口${actionLabel}失败。`);
    }
  } finally {
    if (isCurrentPortDialog(dialog)) {
      dialog.portActionPending = false;
      renderPortDialog();
    }
    setLoading(false);
  }
}

async function updatePortDialogDescription() {
  const dialog = state.portDialog;
  const input = document.querySelector('input[name="portDescription"]');
  if (!dialog || !input || !dialog.request) return;

  const portDescription = String(input.value || "").trim();
  const tab = dialog.tabs.detail;
  tab.status = "loading";
  tab.error = "";
  renderPortDialog();
  setLoading(true);
  try {
    await api.updatePortDescription(dialog.request, portDescription || "");
    if (!isCurrentPortDialog(dialog)) return;
    tab.status = "idle";
    await loadPortDialogTab("detail");
    notify("success", "端口描述已保存。");
  } catch (error) {
    if (isCurrentPortDialog(dialog)) {
      tab.status = "error";
      tab.error = error.message || "端口描述保存失败。";
      renderPortDialog();
    }
    notify("error", error.message || "端口描述保存失败。");
  } finally {
    setLoading(false);
  }
}

async function updatePortDialogVlan(action) {
  const dialog = state.portDialog;
  const form = document.querySelector("[data-port-vlan-form]");
  if (!dialog || !form || !dialog.request) return;
  const isPVIDOnly = /h3c|华三|ruijie|锐捷/i.test(String(dialog.request.szMaker || ""));
  if (action === "delete") {
    const confirmed = await confirmAction({
      title: "确认清除 VLAN 配置",
      message: "清除后将立即更新设备接口配置，请确认该接口无需保留当前 VLAN 设置。",
      target: dialog.port,
      targetLabel: "接口",
      confirmLabel: "确认清除",
      danger: true
    });
    if (!confirmed) return;
  }

  const values = {
    linkType: form.querySelector('[name="linkType"]')?.value || "",
    pvid: form.querySelector('[name="pvid"]')?.value || "",
    trunkVlanList: form.querySelector('[name="trunkVlanList"]')?.value || ""
  };
  const tab = dialog.tabs.vlan;
  const request = {
    ...dialog.request,
    szInterface: dialog.port,
    port: dialog.port,
    aggregationPort: tab.data?.aggregationPort || "",
    linkType: isPVIDOnly ? "" : (values.linkType || "access"),
    pvid: toInt(values.pvid, 1),
    trunkVlanList: isPVIDOnly ? "" : (values.trunkVlanList || "")
  };
  tab.status = "loading";
  tab.error = "";
  renderPortDialog();
  setLoading(true);
  try {
    await api.savePortVlan(request, action === "delete" ? 3 : 2);
    if (!isCurrentPortDialog(dialog)) return;
    tab.status = "idle";
    await loadPortDialogTab("vlan");
    notify("success", action === "delete" ? "接口 VLAN 配置已清除。" : "接口 VLAN 配置已应用。");
  } catch (error) {
    if (isCurrentPortDialog(dialog)) {
      tab.status = "error";
      tab.error = error.message || "接口 VLAN 操作失败。";
      renderPortDialog();
    }
    notify("error", error.message || "接口 VLAN 操作失败。");
  } finally {
    setLoading(false);
  }
}

function renderPortDialog() {
  const root = document.getElementById("port-dialog-root");
  if (!root) {
    return;
  }
  const dialog = state.portDialog;
  if (!dialog) {
    root.innerHTML = "";
    renderPortTrafficDialog();
    return;
  }
  const detailTab = dialog.tabs.detail;
  let content = "";
  if (detailTab.status === "ready") {
    content = `<div class="port-dialog__basic-info">${renderPortDialogBasicInfo(detailTab.data, dialog)}</div>${renderPortDescriptionPanel(detailTab.data)}${renderPortVlanPanel(detailTab.data, dialog)}${renderPortPowerPanel(detailTab.data)}`;
  } else if (detailTab.status === "error") {
    content = `<div class="port-detail-dialog__message"><div class="port-dialog-error" role="alert">${escapeHTML(detailTab.error || "端口信息读取失败。")}</div></div>`;
  } else {
    content = `<div class="port-detail-dialog__loading device-loading"><span class="device-loading__spinner" aria-hidden="true"></span><strong>${dialog.preparing ? "正在准备端口信息..." : "正在读取端口信息..."}</strong></div>`;
  }
  root.innerHTML = `
    <div class="port-dialog-backdrop${dialog.opening ? " port-dialog-backdrop--opening" : ""}" data-dialog-backdrop>
      <section class="port-dialog port-detail-dialog" role="dialog" aria-modal="true" aria-labelledby="port-dialog-title" aria-busy="${dialog.preparing || detailTab.status === "idle" || detailTab.status === "loading"}">
        <header class="port-dialog__header">
          <div class="port-dialog__title-row"><h3 id="port-dialog-title">端口信息 ${escapeHTML(dialog.port)}</h3><button type="button" class="button button--small port-traffic-trigger" data-action="open-port-traffic" aria-haspopup="dialog" ${detailTab.status === "ready" ? "" : "disabled"}>趋势图</button></div>
          <button type="button" class="port-dialog__close" data-action="close-port-dialog" aria-label="关闭端口信息" title="关闭">&times;</button>
        </header>
        <div class="port-detail-dialog__content">${content}</div>
      </section>
    </div>
  `;
  renderPortTrafficDialog();
}

function appendTerminalOutput(terminal, output) {
  if (!terminal || output === undefined || output === null || output === "") {
    return;
  }
  terminal.output = `${terminal.output || ""}${String(output)}`.slice(-100000);
}

function renderSshTerminal() {
  const root = document.getElementById("ssh-terminal-root");
  if (!root) return;
  const terminal = state.sshTerminal;
  if (!terminal) {
    root.innerHTML = "";
    return;
  }
  const connecting = terminal.status === "connecting";
  const running = terminal.status === "running";
  root.innerHTML = `
    <div class="ssh-terminal-backdrop" data-terminal-backdrop>
      <section class="ssh-terminal" role="dialog" aria-modal="true" aria-labelledby="ssh-terminal-title">
        <header class="ssh-terminal__header">
          <div><span>SSH 终端</span><h3 id="ssh-terminal-title">${escapeHTML(terminal.deviceName)}</h3><p>${escapeHTML(terminal.hostip)}</p></div>
          <button type="button" class="port-dialog__close" data-action="close-ssh-terminal" aria-label="关闭 SSH 终端" title="关闭">&times;</button>
        </header>
        <pre class="ssh-terminal__output" aria-live="polite">${escapeHTML(terminal.output || (connecting ? "正在建立 SSH 连接..." : ""))}</pre>
        <div class="ssh-terminal__command">
          <textarea data-terminal-command rows="2" placeholder="输入交换机命令，按发送执行" ${connecting || running ? "disabled" : ""}></textarea>
          <div class="ssh-terminal__actions">
            <button type="button" class="button" data-action="clear-ssh-terminal">清屏</button>
            <button type="button" class="button button--primary" data-action="run-ssh-command" ${connecting || running ? "disabled" : ""}>${running ? "执行中..." : "发送"}</button>
          </div>
        </div>
      </section>
    </div>`;
  const output = root.querySelector(".ssh-terminal__output");
  if (output) output.scrollTop = output.scrollHeight;
}

async function closeSshTerminal() {
  const terminal = state.sshTerminal;
  state.sshTerminal = null;
  renderSshTerminal();
  if (terminal?.sessionId) {
    try {
      await api.closeSwitchTerminal(terminal.sessionId);
    } catch {
      // The server also expires abandoned sessions.
    }
  }
}

async function openSshTerminal(deviceID, device) {
  if (toInt(device.nRpcType, 3) !== 3) {
    notify("error", "仅 SSH 类型设备支持交互终端。");
    return;
  }
  const terminal = {
    deviceID,
    deviceName: device.szName || device.szManageIP || "SSH 终端",
    hostip: device.szManageIP || "--",
    status: "connecting",
    sessionId: "",
    output: ""
  };
  state.sshTerminal = terminal;
  renderSshTerminal();
  try {
    const request = await buildDeviceConnectionRequest(device);
    const payload = await api.openSwitchTerminal(request);
    if (state.sshTerminal !== terminal) {
      if (payload.data?.sessionId) await api.closeSwitchTerminal(payload.data.sessionId);
      return;
    }
    terminal.sessionId = payload.data?.sessionId || "";
    terminal.status = "idle";
    appendTerminalOutput(terminal, payload.data?.output);
    renderSshTerminal();
  } catch (error) {
    if (state.sshTerminal === terminal) {
      terminal.status = "idle";
      appendTerminalOutput(terminal, `\n连接失败: ${error.message}\n`);
      renderSshTerminal();
    }
  }
}

async function runSshTerminalCommand() {
  const terminal = state.sshTerminal;
  const input = document.querySelector("[data-terminal-command]");
  const command = String(input?.value || "").trim();
  if (!terminal?.sessionId || !command || terminal.status === "running") return;
  terminal.status = "running";
  appendTerminalOutput(terminal, `${terminal.output && !terminal.output.endsWith("\n") ? "\n" : ""}> ${command}\n`);
  renderSshTerminal();
  try {
    const payload = await api.runSwitchTerminalCommand(terminal.sessionId, command);
    if (state.sshTerminal !== terminal) return;
    appendTerminalOutput(terminal, payload.data?.output);
  } catch (error) {
    if (state.sshTerminal === terminal) appendTerminalOutput(terminal, `命令失败: ${error.message}\n`);
  } finally {
    if (state.sshTerminal === terminal) {
      terminal.status = "idle";
      renderSshTerminal();
    }
  }
}

function registerForms() {
	document.getElementById("device-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const data = collectFormData(form);
    const isEditing = state.editingDeviceId !== null;
    setLoading(true);
    try {
      const payload = {
        nId: state.editingDeviceId ?? 0,
        szName: data.szName,
        szDevMac: "",
        nModelId: 0,
        nPurposeId: 0,
        szManageIP: data.szManageIP,
        szGateway: "",
        szSubnetMask: "",
        szServerIP: "",
        nAutoEnable: 0,
        szDevPassword: "",
        szRemarks: data.szRemarks,
        szRpcHostip: "",
        nRpcPort: toInt(data.nRpcPort),
        szRpcUsername: data.szRpcUsername,
        szRpcPassword: data.szRpcPassword || (isEditing ? state.devices.find((item) => toInt(item.nId, 0) === state.editingDeviceId)?.szRpcPassword || "" : ""),
        nRpcType: toInt(data.nRpcType, 3)
      };
      if (isEditing) {
        await api.updateDevice(payload);
      } else {
        await api.createDevice(payload);
      }
      resetDeviceForm();
      setDeviceFormModal(false);
      await loadDevices();
      renderOverview();
      notify("success", isEditing ? "交换机台账已更新。" : "交换机台账已保存。");
    } catch (error) {
      notify("error", error.message);
    } finally {
      setLoading(false);
    }
  });

  document.getElementById("device-table")?.addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const action = button.dataset.action;
    const deviceID = toInt(button.dataset.id, 0);
    if (action === "refresh-device-vlans") {
      await refreshDeviceVlanList(button);
      return;
    }
    if (action === "select-device-tab") {
      const row = button.closest("tr.device-subrow");
      const expandedID = state.expandedDeviceId;
      if (!row || expandedID === null || !state.deviceConnectionDetails[expandedID]) {
        return;
      }
      state.deviceActiveTabs[expandedID] = button.dataset.tab === "vlan" ? "vlan" : "interfaces";
      renderDevices();
      if (state.deviceActiveTabs[expandedID] === "vlan") {
        const expandedDevice = state.devices.find((item) => toInt(item.nId, 0) === expandedID);
        if (expandedDevice && /centec|盛科|huawei|华为|h3c|华三|ruijie|锐捷/i.test(inferDeviceMaker(expandedDevice)) && !state.deviceVlanLists[expandedID]) {
          await loadDeviceVlanList(expandedID, expandedDevice);
        }
      }
      return;
    }
    const device = state.devices.find((item) => toInt(item.nId, 0) === deviceID);
    if (!device) {
      notify("error", "未找到对应设备记录。");
      return;
    }

    if (["create-device-vlan", "delete-device-vlan", "rename-device-vlan"].includes(action)) {
      if (!/centec|盛科|huawei|华为|h3c|华三|ruijie|锐捷/i.test(inferDeviceMaker(device))) {
        notify("error", "当前厂商暂不支持设备级 VLAN 列表管理。");
        return;
      }
      openVlanDialog(action === "create-device-vlan" ? "create" : action === "delete-device-vlan" ? "delete" : "rename", deviceID, device, button.dataset.vlan || "", button.dataset.name || "");
      return;
    }

    if (action === "inspect-port") {
      const port = String(button.dataset.port || "").trim();
      if (!port) {
        notify("error", "未找到对应端口信息。");
        return;
      }
      await openPortDialog(deviceID, device, port);
      return;
    }

    if (action === "open-ssh-terminal") {
      await openSshTerminal(deviceID, device);
      return;
    }

    if (action === "edit-device") {
      state.editingDeviceId = deviceID;
      fillDeviceForm(device);
      setDeviceFormModal(true);
      notify("success", `已载入设备「${device.szName || device.szManageIP}」用于修改。`);
      return;
    }

    if (action === "view-device") {
      state.viewingDeviceId = deviceID;
      state.expandedDeviceId = deviceID;
      renderDevices();
      setLoading(true);
      try {
        const detail = await loadLocalDeviceInformation(String(device.szManageIP || "").trim());
        state.deviceConnectionDetails[deviceID] = normalizeDeviceDetail(detail, device, "本地保存信息");
        state.viewingDeviceId = null;
        renderDevices();
        renderOverview();
        notify("success", `已展示设备「${device.szName || device.szManageIP}」的本地保存信息。`);
      } catch (error) {
        state.viewingDeviceId = null;
        state.expandedDeviceId = null;
        renderDevices();
        notify("error", error.message);
      } finally {
        setLoading(false);
      }
      return;
    }

    if (action === "connect-device") {
      state.connectingDeviceId = deviceID;
      state.viewingDeviceId = null;
      state.expandedDeviceId = deviceID;
      renderDevices();
      notify("info", `正在连接设备「${device.szName || device.szManageIP}」，请稍候。`);
      setLoading(true);
      try {
        const payload = await connectDevice(device);
        state.deviceConnectionDetails[deviceID] = normalizeDeviceDetail(payload.data || null, device, "实时信息");
        state.connectingDeviceId = null;
        renderDevices();
        await loadDevices();
        renderOverview();
        notify("success", `设备「${device.szName || device.szManageIP}」连接成功，已展开接口信息。`);
      } catch (error) {
        state.connectingDeviceId = null;
        state.expandedDeviceId = null;
        renderDevices();
        try {
          await loadDevices();
          renderOverview();
        } catch (refreshError) {
          notify("error", `设备状态刷新失败: ${refreshError.message}`);
        }
        notify("error", error.message);
      } finally {
        setLoading(false);
      }
      return;
    }

    if (action === "delete-device") {
      const confirmed = await confirmAction({
        title: "确认删除设备",
        message: "删除后该设备将从设备台账中移除，请确认后继续。",
        target: device.szName || device.szManageIP,
        targetLabel: "设备",
        confirmLabel: "确认删除",
        danger: true
      });
      if (!confirmed) return;
      setLoading(true);
      try {
        await api.deleteDevices([deviceID]);
        if (state.editingDeviceId === deviceID) {
          resetDeviceForm();
        }
        await loadDevices();
        renderOverview();
        notify("success", "设备已删除。");
      } catch (error) {
        notify("error", error.message);
      } finally {
        setLoading(false);
      }
    }
  });

  document.getElementById("vlan-dialog-root").addEventListener("click", (event) => {
    if (event.target.closest('[data-action="close-vlan-dialog"]')) {
      if (state.vlanDialog?.pending) return;
      state.vlanDialog = null;
      renderVlanDialog();
    }
  });
  async function submitVlanDialog(form) {
    const dialog = state.vlanDialog;
    if (!dialog || dialog.pending) return;
    const values = collectFormData(form);
    const vlan = String(values.vlan || "").trim();
    const allowRange = dialog.mode === "create" || dialog.mode === "delete";
    if (!validateVlanValue(vlan, allowRange)) {
      dialog.error = allowRange
        ? "VLAN ID 格式无效。请填写 1-4094 的单个 ID，或以逗号和范围组合，例如 13,14,17-20。"
        : "VLAN ID 必须是 1-4094 的单个数字。";
      renderVlanDialog();
      return;
    }
    if (dialog.mode === "rename" && !String(values.name || "").trim()) {
      dialog.error = "请输入 VLAN 名称。";
      renderVlanDialog();
      return;
    }
    dialog.vlan = vlan;
    dialog.name = String(values.name || "").trim();
    dialog.error = "";
    dialog.pending = true;
    renderVlanDialog();
    setLoading(true);
    try {
      await mutateDeviceVlan(dialog.mode, dialog.deviceId, state.devices.find((item) => toInt(item.nId, 0) === dialog.deviceId), vlan, dialog.name);
      state.vlanDialog = null;
      renderVlanDialog();
    } catch (error) {
      if (state.vlanDialog === dialog) {
        dialog.error = error.message || "VLAN 操作失败。";
      }
    } finally {
      if (state.vlanDialog === dialog) {
        dialog.pending = false;
        renderVlanDialog();
      }
      setLoading(false);
    }
  }

  document.getElementById("vlan-dialog-root").addEventListener("submit", async (event) => {
    const form = event.target.closest("#vlan-dialog-form");
    if (!form) return;
    event.preventDefault();
    await submitVlanDialog(form);
  });

  document.getElementById("confirm-dialog-root").addEventListener("click", (event) => {
    if (event.target.closest('[data-action="cancel-confirm"]')) {
      closeConfirmDialog(false);
      return;
    }
    if (event.target.closest('[data-action="accept-confirm"]')) {
      closeConfirmDialog(true);
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && state.confirmDialog) {
      event.preventDefault();
      closeConfirmDialog(false);
      return;
    }
    // 可编辑确认框（删除前修改 VLAN ID）：在输入框里按回车直接确认删除。
    if (event.key === "Enter" && state.confirmDialog?.editable && event.target?.matches(".confirm-dialog__target input")) {
      event.preventDefault();
      closeConfirmDialog(true);
      return;
    }
    if (event.key === "Escape" && state.registrationDialog && !state.registrationDialog.pending) {
      event.preventDefault();
      closeRegistrationDialog();
      return;
    }
    if (event.key === "Escape" && state.portDialog?.trafficOpen) {
      event.preventDefault();
      closePortTrafficDialog();
    }
  });

  document.getElementById("port-dialog-root").addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button || !state.portDialog) {
      return;
    }
    if (button.dataset.action === "close-port-dialog") {
      state.portDialog = null;
      renderPortDialog();
      return;
    }
    if (button.dataset.action === "open-port-traffic") {
      state.portDialog.trafficOpen = true;
      renderPortTrafficDialog();
      requestAnimationFrame(() => document.querySelector('[data-action="close-port-traffic"]')?.focus());
      if (!state.portDialog.preparing && state.portDialog.tabs.detail.status === "idle") {
        await loadPortDialogTab("detail");
      }
      return;
    }
    if (button.dataset.action === "save-port-vlan") {
      await updatePortDialogVlan("save");
      return;
    }
    if (button.dataset.action === "save-port-description") {
      await updatePortDialogDescription();
      return;
    }
    if (button.dataset.action === "enable-port") {
      await updatePortDialogEnabled(true);
      return;
    }
    if (button.dataset.action === "disable-port") {
      await updatePortDialogEnabled(false);
      return;
    }
    if (button.dataset.action === "delete-port-vlan") {
      await updatePortDialogVlan("delete");
    }
  });

  document.getElementById("port-traffic-dialog-root").addEventListener("click", async (event) => {
    const dialog = state.portDialog;
    if (!dialog) return;
    if (event.target.closest('[data-action="close-port-traffic"]')) {
      closePortTrafficDialog();
      return;
    }
    const button = event.target.closest("button[data-action]");
    if (!button) return;
    if (button.dataset.action === "select-traffic-range") {
      const range = button.dataset.range;
      if (!["oneHour", "oneDay", "sevenDays"].includes(range)) return;
      dialog.trafficRange = range;
      renderPortTrafficDialog();
      return;
    }
    if (button.dataset.action === "refresh-port-traffic") {
      await refreshPortTraffic();
    }
  });

  document.getElementById("ssh-terminal-root").addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) return;
    if (button.dataset.action === "close-ssh-terminal") {
      await closeSshTerminal();
      return;
    }
    if (button.dataset.action === "clear-ssh-terminal") {
      if (state.sshTerminal) state.sshTerminal.output = "";
      renderSshTerminal();
      return;
    }
    if (button.dataset.action === "run-ssh-command") {
      await runSshTerminalCommand();
    }
  });

  document.getElementById("ssh-terminal-root").addEventListener("keydown", async (event) => {
    if (event.target.matches("[data-terminal-command]") && event.ctrlKey && event.key === "Enter") {
      event.preventDefault();
      await runSshTerminalCommand();
    }
  });

  document.getElementById("overview-cards")?.addEventListener("click", async (event) => {
    const rootTabButton = event.target.closest('[data-action="select-overview-root-tab"]');
    if (rootTabButton) {
      // 根 TAB 三项平级：面板 / VLAN / 设置
      const tab = rootTabButton.dataset.tab;
      state.overviewActiveTab = tab === "vlan" ? "vlan" : tab === "collection" ? "collection" : "panel";
      renderOverview();
      if (state.overviewActiveTab === "vlan") await ensureOverviewVlanData();
      if (state.overviewActiveTab === "collection" && !state.collectionPanel) await loadCollectionPanel();
      return;
    }
    const button = event.target.closest('[data-action="inspect-overview-port"]');
    const device = state.devices.find((item) => toInt(item.nId, 0) === state.expandedDeviceId);
    const port = String(button?.dataset.port || "").trim();
    if (button && device && port) {
      await openPortDialog(state.expandedDeviceId, device, port, {
        deviceName: button.dataset.deviceName,
        interfaceType: button.dataset.interfaceType
      });
      return;
    }
    const refreshVlanButton = event.target.closest('[data-action="refresh-device-vlans"]');
    if (refreshVlanButton) {
      await refreshDeviceVlanList(refreshVlanButton);
      return;
    }
    const portVlanButton = event.target.closest('[data-action="save-overview-port-vlan"], [data-action="delete-overview-port-vlan"]');
    if (portVlanButton) {
      await updateOverviewPortVlan(portVlanButton.dataset.action === "delete-overview-port-vlan" ? "delete" : "save");
      return;
    }
    const refreshPortVlanButton = event.target.closest('[data-action="refresh-overview-port-vlan"]');
    if (refreshPortVlanButton && device) {
      const detail = state.deviceConnectionDetails[state.expandedDeviceId];
      const portState = getOverviewVlanPortState(state.expandedDeviceId, detail?.interfaces || []);
      portState.status = "idle";
      await loadOverviewPortVlan(state.expandedDeviceId, device, portState.port);
      return;
    }
    const vlanButton = event.target.closest('[data-action="create-device-vlan"], [data-action="delete-device-vlan"], [data-action="rename-device-vlan"]');
    if (!vlanButton || !device) return;
    const action = vlanButton.dataset.action;
    openVlanDialog(action === "create-device-vlan" ? "create" : action === "delete-device-vlan" ? "delete" : "rename", state.expandedDeviceId, device, vlanButton.dataset.vlan || "", vlanButton.dataset.name || "");
  });

  document.getElementById("overview-cards")?.addEventListener("change", async (event) => {
    const select = event.target.closest('[data-action="select-overview-vlan-port"]');
    if (!select) return;
    const deviceId = state.expandedDeviceId;
    const device = state.devices.find((item) => toInt(item.nId, 0) === deviceId);
    const detail = state.deviceConnectionDetails[deviceId];
    const portState = getOverviewVlanPortState(deviceId, detail?.interfaces || []);
    portState.port = String(select.value || "").trim();
    portState.status = "idle";
    portState.data = null;
    portState.request = null;
    portState.error = "";
    renderOverview();
    if (device && portState.port) await loadOverviewPortVlan(deviceId, device, portState.port);
  });

  document.getElementById("add-device")?.addEventListener("click", () => {
    resetDeviceForm();
    setDeviceFormModal(true);
  });

  document.getElementById("registration-status")?.addEventListener("click", openRegistrationDialog);
  document.getElementById("registration-status")?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    void openRegistrationDialog();
  });

  document.getElementById("registration-dialog-root")?.addEventListener("click", (event) => {
    if (event.target.closest('[data-action="generate-registration-key"]')) {
      const input = document.querySelector('#registration-form input[name="szAesKey"]');
      if (input) {
        input.value = generateRegistrationAESKey();
        input.focus();
        input.select();
      }
      return;
    }
    if (event.target.closest('[data-action="close-registration"]')) {
      closeRegistrationDialog();
      return;
    }
    // 点击窗体外（遮罩）不关闭：与其他弹窗保持一致，只通过关闭按钮、Esc 或提交后退出。
  });

  document.getElementById("registration-dialog-root")?.addEventListener("submit", async (event) => {
    const form = event.target.closest("#registration-form");
    if (!form) return;
    event.preventDefault();
    await submitRegistrationForm(form);
  });

  // 一级导航：左侧栏的三个模块菜单（OLT/ONU 管理、TR069 管理、交换机管理）。
  document.getElementById("module-nav")?.addEventListener("click", (event) => {
    const link = event.target.closest("a[data-module]");
    if (!link) return;
    event.preventDefault();
    activateModule(link.dataset.module);
  });

  // 设置 TAB（原采集配置，与“面板 / VLAN”平级，内容渲染在 #overview-cards 里）：
  // 重新读取 + 嗅探执行方式联动 + 两个表单提交，统一在 overview 容器上做事件委托。
  document.getElementById("overview-cards")?.addEventListener("click", (event) => {
    if (event.target.closest('[data-action="retry-settings"]')) {
      void loadCollectionPanel();
    }
  });

  document.getElementById("overview-cards")?.addEventListener("change", (event) => {
    if (!event.target.matches('#sniffer-config-form input[name="nType"]')) return;
    const form = event.target.form;
    const daily = event.target.value === "1";
    const dailyField = form?.querySelector("[data-sniffer-daily]");
    const intervalField = form?.querySelector("[data-sniffer-interval]");
    if (dailyField) dailyField.hidden = !daily;
    if (intervalField) intervalField.hidden = daily;
    const dailyInput = dailyField?.querySelector("input");
    const intervalInput = intervalField?.querySelector("input");
    if (dailyInput) dailyInput.disabled = !daily;
    if (intervalInput) intervalInput.disabled = daily;
  });

  document.getElementById("overview-cards")?.addEventListener("submit", async (event) => {
    const form = event.target.closest("#sniffer-config-form, #collection-config-form");
    if (!form) return;
    event.preventDefault();
    await submitSettingsForm(form, form.id === "sniffer-config-form" ? "sniffer" : "collection");
  });

  document.querySelector("[data-device-form-backdrop]")?.addEventListener("click", (event) => {
    if (event.target.matches("[data-action=close-device-form]")) {
      setDeviceFormModal(false);
    }
  });

  document.getElementById("device-list")?.addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-action=select-device]");
    if (!button) return;
    const deviceID = toInt(button.dataset.id, 0);
    const device = state.devices.find((item) => toInt(item.nId, 0) === deviceID);
    if (!device) return;
    state.expandedDeviceId = deviceID;
    rememberExpandedDevice(deviceID);
    renderDeviceList();
    if (!state.deviceConnectionDetails[deviceID]) {
      state.viewingDeviceId = deviceID;
      renderDevices();
      try {
        const detail = await loadLocalDeviceInformation(String(device.szManageIP || "").trim());
        state.deviceConnectionDetails[deviceID] = normalizeDeviceDetail(detail, device, "本地保存信息");
      } catch (error) {
        notify("error", error.message);
      } finally {
        state.viewingDeviceId = null;
      }
    }
    renderDevices();
    renderOverview();
    await ensureOverviewVlanData();
  });

  document.getElementById("device-list")?.addEventListener("change", (event) => {
    const checkbox = event.target.closest('input[data-action="select-device-for-delete"]');
    if (!checkbox) return;
    const deviceID = toInt(checkbox.dataset.id, 0);
    if (!deviceID) return;
    if (checkbox.checked) {
      state.selectedDeviceIds.add(deviceID);
    } else {
      state.selectedDeviceIds.delete(deviceID);
    }
    renderDeviceList();
  });

  document.getElementById("select-all-devices")?.addEventListener("change", (event) => {
    state.selectedDeviceIds = event.target.checked
      ? new Set(state.devices.map((item) => toInt(item.nId, 0)).filter(Boolean))
      : new Set();
    renderDeviceList();
  });

  document.getElementById("export-devices")?.addEventListener("click", exportDevices);
  document.getElementById("import-devices")?.addEventListener("click", () => document.getElementById("device-import-file")?.click());
  document.getElementById("device-import-file")?.addEventListener("change", (event) => {
    const file = event.target.files?.[0];
    if (file) notify("info", `已选择导入文件「${file.name}」，批量导入接口待确认。`);
  });
  document.getElementById("edit-selected")?.addEventListener("click", () => {
    const device = state.devices.find((item) => toInt(item.nId, 0) === state.expandedDeviceId);
    if (!device) return;
    state.editingDeviceId = toInt(device.nId, 0);
    fillDeviceForm(device);
    setDeviceFormModal(true);
  });
  document.getElementById("delete-selected")?.addEventListener("click", async () => {
    const devices = state.devices.filter((item) => state.selectedDeviceIds.has(toInt(item.nId, 0)));
    if (!devices.length) return;
    const deviceIDs = devices.map((item) => toInt(item.nId, 0));
    const names = devices.map((item) => item.szName || item.szManageIP || `设备 ${item.nId}`);
    const target = names.length > 3 ? `${names.slice(0, 3).join("、")} 等 ${names.length} 台` : names.join("、");
    const confirmed = await confirmAction({
      title: `确认删除 ${devices.length} 台设备`,
      message: "删除后所选设备将从设备台账中移除，请确认后继续。",
      target,
      targetLabel: "所选设备",
      confirmLabel: `删除 ${devices.length} 台`,
      danger: true
    });
    if (!confirmed) return;
    const deleteButton = document.getElementById("delete-selected");
    if (deleteButton) {
      deleteButton.disabled = true;
      deleteButton.textContent = "删除中...";
    }
    notify("info", `正在删除 ${devices.length} 台设备，请稍候。`);
    setLoading(true);
    try {
      await api.deleteDevices(deviceIDs);
      deviceIDs.forEach((deviceID) => {
        state.selectedDeviceIds.delete(deviceID);
        delete state.deviceConnectionDetails[deviceID];
        delete state.deviceActiveTabs[deviceID];
        delete state.deviceVlanLists[deviceID];
        delete state.overviewVlanPorts[deviceID];
      });
      if (deviceIDs.includes(state.expandedDeviceId)) state.expandedDeviceId = null;
      if (deviceIDs.includes(state.editingDeviceId)) {
        resetDeviceForm();
        setDeviceFormModal(false);
      }
      await loadDevices();
      renderOverview();
      notify("success", `已删除 ${devices.length} 台设备。`);
    } catch (error) {
      notify("error", error.message);
    } finally {
      setLoading(false);
      renderDeviceList();
    }
  });

  document.getElementById("refresh-all").addEventListener("click", connectSelectedDevice);
}

// ── 一级导航（功能模块） ──
//
// 左侧栏的三个菜单：OLT/ONU 管理、TR069 管理、交换机管理。切换只影响右侧主内容区：
//   switch → 设备面板（overview-cards：面板 / VLAN / 设置 三个平级 TAB）
//   其他   → 模块占位页（M2 与后端处理器一起迁入），并按 /api/capabilities 标注安装状态。

// renderModuleNav 渲染左侧栏模块菜单。
function renderModuleNav() {
  const nav = document.getElementById("module-nav");
  if (!nav) return;
  nav.innerHTML = MODULE_VIEWS.map((view) => {
    const installed = isModuleInstalled(view.id);
    const active = view.id === state.activeModule;
    const title = installed ? view.hint : "当前设备未安装该功能模块，业务接口将返回 code=-4。";
    return `<a href="#module=${view.id}" data-module="${view.id}" class="${active ? "is-active" : ""}${installed ? "" : " is-missing"}" title="${escapeHTML(title)}">${escapeHTML(view.title)}</a>`;
  }).join("");
}

// isModuleInstalled 能力清单未读到（或清单为空）时按“已安装”处理，避免误报“未安装”。
function isModuleInstalled(moduleId) {
  if (!state.capabilities.loaded || !state.capabilities.installed.length) return true;
  return state.capabilities.installed.includes(moduleId);
}

// loadCapabilities 读取模块安装状态，仅用于导航与占位页的标注，不影响交换机功能。
async function loadCapabilities() {
  try {
    const response = await fetch("/api/capabilities", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const payload = await response.json();
    const modules = Array.isArray(payload.modules) ? payload.modules : [];
    state.capabilities.installed = modules.map((item) => String(item.id));
    state.capabilities.loaded = true;
    state.capabilities.error = "";
  } catch (error) {
    state.capabilities.loaded = true;
    state.capabilities.error = error.message || "能力清单读取失败";
  }
  renderModuleNav();
  if (state.activeModule !== "switch") renderModuleView(state.activeModule);
}

// activateModule 切换一级导航。
// clearModuleTransients 切换一级导航时清掉"上一个模块留下的操作痕迹"：
// 提示条 + 各类弹窗与临时态（设备台账表单、确认框、端口、VLAN、SSH 终端），
// 避免出现"在 OLT 页面看到交换机操作提示/弹窗"这类串味。
function clearModuleTransients() {
  if (state.notices.length) {
    state.notices = [];
    renderNotices();
  }
  if (state.confirmDialog) {
    closeConfirmDialog(false);
  }
  state.editingDeviceId = null;
  state.viewingDeviceId = null;
  setDeviceFormModal(false);
  if (state.vlanDialog) {
    state.vlanDialog = null;
    renderVlanDialog();
  }
  if (state.portDialog) {
    state.portDialog = null;
    renderPortDialog();
  }
  if (state.sshTerminal) {
    state.sshTerminal = null;
    renderSshTerminal();
  }
}

function activateModule(moduleId, options = {}) {
  const target = MODULE_VIEWS.some((view) => view.id === moduleId) ? moduleId : "switch";
  if (state.activeModule !== target) clearModuleTransients();
  state.activeModule = target;
  renderModuleNav();
  const switchView = document.getElementById("switch-view");
  const moduleView = document.getElementById("module-view");
  if (switchView) switchView.hidden = target !== "switch";
  if (moduleView) moduleView.hidden = target === "switch";
  // 左侧栏台账面板跟着当前模块走：交换机 → 交换机设备台账；OLT/ONU 管理 → OLT 设备台账；TR069 → 都不显示。
  const switchLedger = document.getElementById("switch-ledger-sidebar");
  const oltLedger = document.getElementById("olt-ledger-sidebar");
  if (switchLedger) switchLedger.hidden = target !== "switch";
  if (oltLedger) oltLedger.hidden = target !== "olt";
  if (target !== "switch") renderModuleView(target);
  if (!options.fromHash) {
    const hash = `#module=${target}`;
    if (window.location.hash !== hash) window.location.hash = hash;
  }
  // 回到交换机管理且尚无设备数据时补一次加载（例如首次直达 #module=switch）；
  // options.initial=true 表示"页面加载（F5）后的首次初始化"——此时不做缓存判断，总是刷新一次工作台，
  // 保证"刷新页面 = 初始化当前选中的模块"这一语义（模块间来回切换仍走上面的缓存判断，不重复请求）。
  if (target === "switch" && (options.initial || (!state.devices.length && !state.loading))) {
    void refreshAll();
  }
}

// renderModuleView 渲染非交换机模块的页面：已落地的业务页（MODULE_PAGES）走页面表，其余为占位页。
function renderModuleView(moduleId) {
  const root = document.getElementById("module-view");
  if (!root) return;
  const pageFactory = MODULE_PAGES[moduleId];
  if (pageFactory) {
    if (!modulePageInstances[moduleId]) {
      modulePageInstances[moduleId] = pageFactory({ escapeHTML, toInt, notify, confirmAction });
    }
    modulePageInstances[moduleId].render(root);
    return;
  }
  const view = MODULE_VIEWS.find((item) => item.id === moduleId) || MODULE_VIEWS[0];
  const installed = isModuleInstalled(moduleId);
  const reading = !state.capabilities.loaded;
  const detail = reading
    ? "正在读取模块安装状态（/api/capabilities）..."
    : installed
      ? `该模块已随本包安装：业务页与后端处理器在 M2 一起迁入统一工程，当前可用 /api/v2/${view.id}/ping 自证在线。`
      : "当前设备未安装该功能模块，调用其业务接口会返回 code=-4（前端据此提示，而不是报成参数错误）。";
  root.innerHTML = `
    <section class="panel module-placeholder">
      <div class="panel__header">
        <h3>${escapeHTML(view.title)}</h3>
        <span class="settings-status${installed && !reading ? " is-enabled" : ""}">${reading ? "读取中" : installed ? "已安装" : "未安装"}</span>
      </div>
      <div class="empty-state">${escapeHTML(view.hint)}<br>${escapeHTML(detail)}</div>
    </section>`;
}

// initModuleFromHash 支持 #module=<id> 直达（默认交换机管理）；initial=true 表示页面加载后的首次初始化。
function initModuleFromHash(initial = false) {
  const match = /#module=([a-z0-9_]+)/i.exec(String(window.location.hash || ""));
  activateModule(match ? match[1] : "switch", { fromHash: true, initial });
}

function legacyLayout() {
  return `
    <div class="shell">
      <aside class="sidebar">
        <div class="brand">
          <span class="brand__eyebrow">Engineering Console</span>
          <h1>工程控制台</h1>
          <p>以设备面板为核心的交换机状态总览。</p>
        </div>
        <nav class="nav">
          <a href="#overview-cards">总览</a>
          <a href="#devices">设备台账</a>
        </nav>
        <button id="refresh-all" class="button button--primary button--full">刷新所有数据</button>
        <div id="notices" class="notices"></div>
      </aside>
      <main class="content">
        <section class="panel">
          <div class="panel__header">
            <h3>设备面板</h3>
          </div>
          <div class="metric-grid"></div>
        </section>

        <section id="devices" class="panel">
          <div class="panel__header">
            <h3>设备台账</h3>
            <p>录入管理地址、RPC/SSH/Telnet 参数与自动采集状态，形成统一设备档案。</p>
          </div>
          <form id="device-form" class="form-grid form-grid--wide">
            <input name="szName" placeholder="设备名称" required>
            <input name="szManageIP" placeholder="管理 IP" required>
            <select name="nRpcType">
              <option value="3">SSH</option>
              <option value="2">TELNET</option>
              <option value="1">RPC-API</option>
            </select>
            <input name="nRpcPort" placeholder="Port" value="22">
            <input name="szRpcUsername" placeholder="Username">
            <input name="szRpcPassword" placeholder="Password">
            <input name="szRemarks" placeholder="备注">
            <button class="button button--primary">保存设备</button>
          </form>
          <div id="device-table"></div>
        </section>

      </main>
    </div>
    <div id="port-dialog-root"></div>
    <div id="port-traffic-dialog-root"></div>
    <div id="ssh-terminal-root"></div>
    <div id="vlan-dialog-root"></div>
    <div id="confirm-dialog-root"></div>
  `;
}

function layout() {
  return `
    <div class="shell">
      <aside class="sidebar">
        <div class="brand">
          <div class="brand__copy">
            <span class="brand__eyebrow">Engineering Console</span>
            <h1>工程控制台</h1>
          </div>
        </div>
        <nav class="nav module-nav" id="module-nav" aria-label="功能模块"></nav>
        <div class="device-sidebar-stack">
          <div id="registration-status"></div>
          <section class="device-sidebar-panel" aria-label="设备台账列表" id="switch-ledger-sidebar">
            <header class="device-sidebar-panel__header">
              <strong>设备台账</strong>
              <div class="device-sidebar-panel__selection">
                <label><input id="select-all-devices" type="checkbox" disabled><span>全选</span></label>
                <span id="device-list-count">0 台</span>
              </div>
            </header>
            <div class="device-sidebar-panel__actions device-sidebar-panel__actions--toolbar">
              <button id="refresh-all" class="button button--small" type="button">连接</button>
              <button id="add-device" class="button button--small button--primary" type="button">新增</button>
              <button id="import-devices" class="button button--small" type="button" disabled>导入</button>
              <button id="export-devices" class="button button--small" type="button" disabled>导出</button>
              <button id="edit-selected" class="button button--small" type="button" disabled>修改</button>
              <button id="delete-selected" class="button button--small button--danger" type="button" disabled>删除</button>
            </div>
            <div id="device-list"></div>
          </section>
          <section class="device-sidebar-panel" aria-label="OLT 设备台账" id="olt-ledger-sidebar" hidden>
            <header class="device-sidebar-panel__header">
              <strong>OLT 设备台账</strong>
              <div class="device-sidebar-panel__selection">
                <label><input id="olt-select-all" type="checkbox" data-olt-action="select-all" disabled><span>全选</span></label>
                <span id="olt-device-count">0 台</span>
              </div>
            </header>
            <div class="device-sidebar-panel__actions device-sidebar-panel__actions--toolbar">
              <button id="olt-connect" class="button button--small" type="button" data-olt-action="connect">连接</button>
              <button id="olt-add" class="button button--small button--primary" type="button" data-olt-action="add">新增</button>
              <button id="olt-edit" class="button button--small" type="button" data-olt-action="edit" disabled>修改</button>
              <button id="olt-delete" class="button button--small button--danger" type="button" data-olt-action="delete" disabled>删除</button>
            </div>
            <div id="olt-device-list"></div>
          </section>
        </div>
        <div id="notices" class="notices"></div>
      </aside>
      <main class="content">
        <div id="module-view" class="module-view" hidden></div>
        <div id="switch-view" class="module-view">
          <div id="overview-cards" class="overview-panel"></div>
        </div>
      </main>
    </div>
    <div class="device-form-modal" hidden data-device-form-backdrop>
      <section class="device-form-dialog" role="dialog" aria-modal="true" aria-labelledby="device-form-title">
        <header class="device-form-dialog__header"><h3 id="device-form-title">设备台账</h3></header>
        <form id="device-form" class="device-form-dialog__body">
          <label><span>设备名称：</span><input name="szName" required></label>
          <label><span>管理 IP：</span><input name="szManageIP" required></label>
          <label><span>连接类型：</span><select name="nRpcType"><option value="3">SSH</option><option value="2">TELNET</option><option value="1">RPC-API</option></select></label>
          <label><span>端口：</span><input name="nRpcPort" value="22"></label>
          <label><span>用户名：</span><input name="szRpcUsername"></label>
          <label><span>密码：</span><input name="szRpcPassword" type="password"></label>
          <label><span>备注：</span><input name="szRemarks"></label>
          <footer class="device-form-dialog__actions"><button type="button" class="button" data-action="close-device-form">取消</button><button type="submit" class="button button--primary">保存</button></footer>
        </form>
      </section>
    </div>
    <div id="registration-dialog-root"></div>
    <input id="device-import-file" type="file" accept=".csv,text/csv" hidden>
    <div id="port-dialog-root"></div>
    <div id="port-traffic-dialog-root"></div>
    <div id="ssh-terminal-root"></div>
    <div id="vlan-dialog-root"></div>
    <div id="confirm-dialog-root"></div>
  `;
}

export function createApp(root) {
  root.innerHTML = layout();
  renderNotices();
  renderRegistrationStatus();
  renderDeviceForm();
  registerForms();
  // 一级导航（三个模块菜单）→ 右侧视图；采集配置直接渲染到右侧面板（不再是弹窗）。
  renderModuleNav();
  // 页面加载的首次初始化：由 hash 决定当前模块，并对该模块做一次初始化（initial: true）。
  initModuleFromHash(true);
  if (!moduleHashListenerBound) {
    moduleHashListenerBound = true;
    window.addEventListener("hashchange", () => initModuleFromHash());
  }
  void loadCapabilities();
  void loadRegistrationStatus();
  if (registrationStatusTimer) clearInterval(registrationStatusTimer);
  registrationStatusTimer = setInterval(() => void loadRegistrationStatus(), REGISTRATION_STATUS_REFRESH_INTERVAL);
  // 页面加载只初始化"当前选中的模块"（hash 决定）：
  //   - 交换机管理 → 采集配置 + 工作台刷新（提示「交换机工作台数据已刷新。」；首次加载由 activateModule() 负责，这里不重复调用）
  //   - OLT/ONU 管理、TR069 管理 → 只渲染该模块（OLT 页自己按需拉列表），不跑交换机刷新与采集配置读取，
  //     否则会出现"焦点停在 OLT 菜单、提示却是『交换机工作台数据已刷新』"的串味，也白白请求一次交换机数据。
  // 采集配置在切到交换机管理的「设置」TAB 时会按需加载（见 renderOverview 与其点击处理）。
  if (state.activeModule === "switch") {
    void loadCollectionPanel();
  }
}
