const JSON_HEADERS = {
  Accept: "application/json"
};

// 轻量查询类请求（如注册状态）的超时时间：后端异常（数据库被锁死等）时快速失败，
// 避免界面一直停留在“正在读取...”。耗时操作（连设备、读端口等）不设超时。
const READ_TIMEOUT_MS = 15000;

function toFormBody(fields) {
  const params = new URLSearchParams();
  Object.entries(fields).forEach(([key, value]) => {
    if (value === undefined || value === null) {
      return;
    }
    params.set(key, typeof value === "string" ? value : String(value));
  });
  return params;
}

async function parseJSON(response) {
  const text = await response.text();
  if (!text) {
    return { code: -1, message: "empty response" };
  }
  try {
    return JSON.parse(text);
  } catch (error) {
    return { code: -1, message: `invalid json: ${error.message}`, raw: text };
  }
}

export async function postForm(endpoint, fields, options = {}) {
	const requestFields = { ...fields };
  const timeout = Number(options.timeout) > 0 ? Number(options.timeout) : 0;
  const controller = timeout > 0 && typeof AbortController === "function" ? new AbortController() : null;
  const timer = controller ? setTimeout(() => controller.abort(), timeout) : null;
  let response;
  try {
    response = await fetch(endpoint, {
      method: "POST",
      headers: {
        ...JSON_HEADERS,
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8"
      },
			body: toFormBody(requestFields),
      signal: controller ? controller.signal : undefined
    });
  } catch (error) {
    if (error && error.name === "AbortError") {
      throw new Error("请求超时，请稍后重试");
    }
    throw error;
  } finally {
    if (timer) {
      clearTimeout(timer);
    }
  }
  const payload = await parseJSON(response);
  if (!response.ok) {
    throw new Error(payload.message || `HTTP ${response.status}`);
  }
  if (payload.code !== 0) {
    throw new Error(payload.message || "request failed");
  }
  return payload;
}

export const api = {
  listVenues: () => postForm("/itms/handleSwitchVenue/", { nAct: 0 }),
  createVenue: (item) => postForm("/itms/handleSwitchVenue/", { nAct: 1, saveData: JSON.stringify(item) }),

  listManufacturers: () => postForm("/itms/handleSwitchManufacturer/", { nAct: 0 }),
  createManufacturer: (item) =>
    postForm("/itms/handleSwitchManufacturer/", { nAct: 1, saveData: JSON.stringify(item) }),

  listPurposes: () => postForm("/itms/handleSwitchPurpose/", { nAct: 0 }),
  createPurpose: (item) => postForm("/itms/handleSwitchPurpose/", { nAct: 1, saveData: JSON.stringify(item) }),

  listProtocols: () => postForm("/itms/handleSwitchProtocol/", { nAct: 0 }),
  createProtocol: (item) => postForm("/itms/handleSwitchProtocol/", { nAct: 1, saveData: JSON.stringify(item) }),

  listModels: () => postForm("/itms/handleSwitchModel/", { nAct: 0 }),
  createModel: (item) => postForm("/itms/handleSwitchModel/", { nAct: 1, saveData: JSON.stringify(item) }),

  listDevices: () => postForm("/itms/handleSwitchInfo/", { nAct: 0 }),
  loadDeviceInformation: (szLinkIp) => postForm("/itms/handleDeviceInformation/", { nAct: 0, szLinkIp }),
  encryptSwitchCredentials: (credentials) =>
    postForm("/switchweb/api/encrypt-switch-credentials", credentials),
  connectDevice: (connData) =>
    postForm("/itms/handleDeviceInformation/", { nAct: 1, connData: JSON.stringify(connData) }),
  createDevice: (item) => postForm("/itms/handleSwitchInfo/", { nAct: 1, saveData: JSON.stringify(item) }),
  updateDevice: (item) => postForm("/itms/handleSwitchInfo/", { nAct: 2, saveData: JSON.stringify(item) }),
  deleteDevices: (ids) => postForm("/itms/handleSwitchInfo/", { nAct: 3, saveData: JSON.stringify(ids) }),

  loadSnifferConfig: () => postForm("/itms/handleSwitchSnifferConfig/", { nAct: 0 }),
  saveSnifferConfig: (item) =>
    postForm("/itms/handleSwitchSnifferConfig/", { nAct: 1, saveData: JSON.stringify(item) }),

  loadCollectionSettings: () => postForm("/itms/handleSwitchCollectionSettings/", { nAct: 0 }),
  saveCollectionSettings: (item) =>
    postForm("/itms/handleSwitchCollectionSettings/", { nAct: 1, saveData: JSON.stringify(item) }),

  loadRegistrationInfo: () =>
    postForm("/itms/handleRegistrationInfo/", { nAct: 0 }, { timeout: READ_TIMEOUT_MS }),
  saveRegistrationInfo: (item) =>
    postForm("/itms/handleRegistrationInfo/", { nAct: 1, saveData: JSON.stringify(item) }),

  loadPortDetail: (req) =>
    postForm("/itms/handleSwitchPortDetail/", { nAct: 0, saveData: JSON.stringify(req) }),
  setPortEnabled: (req, portEnabled) =>
    postForm("/itms/handleSwitchPortDetail/", {
      nAct: 1,
      saveData: JSON.stringify({ ...req, portEnabled })
    }),
  updatePortDescription: (req, portDescription) =>
    postForm("/itms/handleSwitchPortDetail/", {
      nAct: 2,
      saveData: JSON.stringify({ ...req, portDescription })
    }),

  loadPortVlan: (req) => postForm("/itms/handleSwitchVlan/", { nAct: 0, saveData: JSON.stringify(req) }),
  savePortVlan: (req, nAct) => postForm("/itms/handleSwitchVlan/", { nAct, saveData: JSON.stringify(req) }),
  listDeviceVlans: (req) => postForm("/itms/handleSwitchVlan/", { nAct: 1, saveData: JSON.stringify(req) }),
  createDeviceVlans: (req) => postForm("/itms/handleSwitchVlan/", { nAct: 4, saveData: JSON.stringify(req) }),
  deleteDeviceVlans: (req) => postForm("/itms/handleSwitchVlan/", { nAct: 5, saveData: JSON.stringify(req) }),
  renameDeviceVlan: (req) => postForm("/itms/handleSwitchVlan/", { nAct: 6, saveData: JSON.stringify(req) }),

  openSwitchTerminal: (req) => postForm("/itms/handleSwitchTerminal/", { nAct: 1, saveData: JSON.stringify(req) }),
  runSwitchTerminalCommand: (sessionId, command) => postForm("/itms/handleSwitchTerminal/", { nAct: 2, saveData: JSON.stringify({ sessionId, command }) }),
  closeSwitchTerminal: (sessionId) => postForm("/itms/handleSwitchTerminal/", { nAct: 3, saveData: JSON.stringify({ sessionId }) }),

  lookupTerminalMac: (req) =>
    postForm("/itms/getTerminalMAC/", { nAct: 0, saveData: JSON.stringify(req) }),

  loadOltSummary: (connData, refresh = false) =>
    postForm("/itms/handleOltConnect/", {
      Act: 1,
      refresh: refresh ? 1 : 0,
      connData: JSON.stringify(connData)
    }),
  loadOltBoards: (connData, slotId = "", refresh = false) =>
    postForm("/itms/handleBoardAndSlotInfo/", {
      refresh: refresh ? 1 : 0,
      connData: JSON.stringify(connData),
      saveData: JSON.stringify({ SlotId: slotId })
    })
};
