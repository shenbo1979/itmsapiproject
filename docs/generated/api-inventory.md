# 老/新接口面清单（自动生成，勿手改）

生成工具：`tools/api_inventory.py`；输入：老 Go 单体 `main.go`、`/switchapi/main.go`、`/switchweb/src/api.js`、`/fusiongateway/web/assets/js/*.js`（全部只读）。

## 1. 概览

| 指标 | 数量 |
|---|---|
| 老 Go 单体 `/itms/*` 路由 | 52 |
| 新 `switchapi` `/itms/*` 路由 | 18 |
| 老 WEB 壳调用的 `/itms/*` 路径 | 16 |
| 老 WEB 壳调用的 `/client/*` 路径（Python 侧） | 32 |
| 老 Go 路由中未被老壳调用的 | 37 |

## 2. 按模块归类

| 模块 | 路径数 | 路径 |
|---|---|---|
| switch | 17 | `/itms/getTerminalMAC/`<br>`/itms/handleDeviceInformation/`<br>`/itms/handleRpcApiConfig/`<br>`/itms/handleSwitchCollectionSettings/`<br>`/itms/handleSwitchInfo/`<br>`/itms/handleSwitchManufacturer/`<br>`/itms/handleSwitchModel/`<br>`/itms/handleSwitchPortDetail/`<br>`/itms/handleSwitchProtocol/`<br>`/itms/handleSwitchPurpose/`<br>`/itms/handleSwitchSnifferConfig/`<br>`/itms/handleSwitchTerminal/`<br>`/itms/handleSwitchVenue/`<br>`/itms/handleSwitchVlan/`<br>`/itms/handleVlanClassifier/`<br>`/itms/handleVlanInformation/`<br>`/itms/validateThirdToken/` |
| olt | 19 | `/itms/handleBoardAndSlotInfo/`<br>`/itms/handleConnectOltUser/`<br>`/itms/handleCurrentConfiguration/`<br>`/itms/handleDBAProfile/`<br>`/itms/handleLineProfile/`<br>`/itms/handleLinkAggregationInfo/`<br>`/itms/handleONTAutoFind/`<br>`/itms/handleONTAutoMatic/`<br>`/itms/handleONTListAndStatus/`<br>`/itms/handleONTRegisterProfile/`<br>`/itms/handleONTRegistration/`<br>`/itms/handleOltConfigAutoSave/`<br>`/itms/handleOltConnect/`<br>`/itms/handleOltLogList/`<br>`/itms/handleOltSavedConfig/`<br>`/itms/handleOltTerminalUser/`<br>`/itms/handleSrvProfile/`<br>`/itms/handleTr069Profile/`<br>`/itms/handleVLANFunctionModule/` |
| tr069 | 48 | `/client/apiPolicySave/`<br>`/client/fnDeleteWANPath/`<br>`/client/fnDeployWAN/`<br>`/client/fnDeviceDelete/`<br>`/client/fnDeviceReboot/`<br>`/client/fnDoOrderInfo/`<br>`/client/fnDoQosInfo/`<br>`/client/fnDoSIPInfo/`<br>`/client/fnDoTelnetInfo/`<br>`/client/fnDoWANDelete/`<br>`/client/fnDownModeFile/`<br>`/client/fnEditRoom/`<br>`/client/fnExportTemplate/`<br>`/client/fnFactoryReset/`<br>`/client/fnGetAdminInfo/`<br>`/client/fnGetAllParams/`<br>`/client/fnGetBusinessModeList/`<br>`/client/fnGetSIPInfo/`<br>`/client/fnGetWanConnect/`<br>`/client/fnGetWifiInfo/`<br>`/client/fnImportTemplate/`<br>`/client/fnInfosSave/`<br>`/client/fnLogDelete/`<br>`/client/fnModeCfgSave/`<br>`/client/fnRefreshDeviceInfo/`<br>`/client/fnSendWanPing/`<br>`/client/fnServerSave/`<br>`/client/fnSetAdminInfo/`<br>`/client/fnTestDiag/`<br>`/client/fnUpCpeGroup/`<br>`/client/fnWorkOrderDelete/`<br>`/client/fnWorkOrderSave/`<br>`/itms/handleCpeGroupInfo/`<br>`/itms/handleDeviceDelete/`<br>`/itms/handleDeviceInfo/`<br>`/itms/handleDeviceStatus/`<br>`/itms/handleEditRoom/`<br>`/itms/handleItmsUpgraded/`<br>`/itms/handleItmsVersion/`<br>`/itms/handleServerConfig/`<br>`/itms/handleTestDiag/`<br>`/itms/handleTr069LogList/`<br>`/itms/handleTr069Poliey/`<br>`/itms/handleTr069TemplateList/`<br>`/itms/handleUploadFile/`<br>`/itms/handleUserAction/`<br>`/itms/handleVersionInfo/`<br>`/itms/handleWorkOrderList/` |
| core | 0 | — |

## 3. `/itms/*` 逐条明细（老 Go 单体视角）

| 路径 | handler | 模块 | 判定依据 | 老壳调用 | 新 switchapi | 备注 |
|---|---|---|---|---|---|---|
| `/itms/getTerminalMAC/` | `getTerminalMAC` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleBoardAndSlotInfo/` | `handleBoardAndSlotInfo` | olt | keyword:板卡 | ✅ switchweb/src/api.js | ❌ |  |
| `/itms/handleConnectOltUser/` | `handleConnectOltUser` | olt | keyword:Olt | — | ❌ |  |
| `/itms/handleCpeGroupInfo/` | `handleCpeGroupInfo` | tr069 | keyword:Cpe | — | ❌ |  |
| `/itms/handleCurrentConfiguration/` | `handleCurrentConfiguration` | olt | keyword:OLT | — | ❌ |  |
| `/itms/handleDBAProfile/` | `handleDBAProfile` | olt | keyword:DBA | — | ❌ |  |
| `/itms/handleDeviceDelete/` | `handleDeviceDelete` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleDeviceInfo/` | `handleDeviceInfo` | tr069 | keyword:CPE | — | ❌ |  |
| `/itms/handleDeviceInformation/` | `handleDeviceInformation` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleDeviceStatus/` | `handleDeviceStatus` | tr069 | keyword:设备状态 | — | ❌ |  |
| `/itms/handleEditRoom/` | `handleEditRoom` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleItmsUpgraded/` | `handleItmsUpgraded` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleItmsVersion/` | `handleItmsVersion` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleLineProfile/` | `handleLineProfile` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleLinkAggregationInfo/` | `handleLinkAggregationInfo` | olt | keyword:汇聚 | — | ❌ |  |
| `/itms/handleONTAutoFind/` | `handleONTAutoFind` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleONTAutoMatic/` | `handleONTAutoMatic` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleONTListAndStatus/` | `handleONTListAndStatus` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleONTRegisterProfile/` | `handleONTRegisterProfile` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleONTRegistration/` | `handleONTRegistration` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleOltConfigAutoSave/` | `handleOltConfigAutoSave` | olt | comment-prefix | — | ❌ |  |
| `/itms/handleOltConnect/` | `handleOltConnect` | olt | keyword:Olt | ✅ switchweb/src/api.js | ❌ |  |
| `/itms/handleOltLogList/` | `handleOltLogList` | olt | keyword:Olt | — | ❌ |  |
| `/itms/handleOltSavedConfig/` | `handleOltSavedConfig` | olt | comment-prefix | — | ❌ |  |
| `/itms/handleOltTerminalUser/` | `handleOltTerminalUser` | olt | keyword:Olt | — | ❌ |  |
| `/itms/handleRpcApiConfig/` | `handleRpcApiConfig` | switch | switchapi | — | ✅ |  |
| `/itms/handleServerConfig/` | `handleServerConfig` | tr069 | keyword:参数 | — | ❌ |  |
| `/itms/handleSrvProfile/` | `handleSrvProfile` | olt | keyword:ONT | — | ❌ |  |
| `/itms/handleSwitchCollectionSettings/` | `handleSwitchCollectionSettings` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchInfo/` | `handleSwitchInfo` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchManufacturer/` | `handleSwitchManufacturer` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchModel/` | `handleSwitchModel` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchPortDetail/` | `handleSwitchPortDetail` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchProtocol/` | `handleSwitchProtocol` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchPurpose/` | `handleSwitchPurpose` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchSnifferConfig/` | `handleSwitchSnifferConfig` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchTerminal/` | `handleSwitchTerminal` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchVenue/` | `handleSwitchVenue` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleSwitchVlan/` | `handleSwitchVlan` | switch | switchapi | ✅ switchweb/src/api.js | ✅ |  |
| `/itms/handleTestDiag/` | `handleTestDiag` | tr069 | explicit(handler 文件) | — | ❌ |  |
| `/itms/handleTr069LogList/` | `handleTr069LogList` | tr069 | keyword:Tr069 | — | ❌ |  |
| `/itms/handleTr069Poliey/` | `handleTr069Poliey` | tr069 | keyword:Tr069 | — | ❌ |  |
| `/itms/handleTr069Profile/` | `handleTr069Profile` | olt | explicit(handler 文件) | — | ❌ |  |
| `/itms/handleTr069TemplateList/` | `handleTr069TemplateList` | tr069 | keyword:Tr069 | — | ❌ |  |
| `/itms/handleUploadFile/` | `handleUploadFile` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleUserAction/` | `handleUserAction` | tr069 | keyword:用户操作 | — | ❌ |  |
| `/itms/handleVLANFunctionModule/` | `handleVLANFunctionModule` | olt | explicit(handler 文件) | — | ❌ |  |
| `/itms/handleVersionInfo/` | `handleVersionInfo` | tr069 | comment-prefix | — | ❌ |  |
| `/itms/handleVlanClassifier/` | `handleVlanClassifier` | switch | switchapi | — | ✅ |  |
| `/itms/handleVlanInformation/` | `handleVlanInformation` | switch | switchapi | — | ✅ |  |
| `/itms/handleWorkOrderList/` | `handleWorkOrderList` | tr069 | keyword:工单 | — | ❌ |  |
| `/itms/validateThirdToken/` | `handleValidateThirdToken` | switch | switchapi | — | ✅ |  |

## 4. `/client/*` 明细（TR069/CPE 侧，Python 提供）

| 路径 | 模块 | 老壳调用 | 备注 |
|---|---|---|---|
| `/client/apiPolicySave/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDeleteWANPath/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDeployWAN/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDeviceDelete/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDeviceReboot/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDoOrderInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDoQosInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDoSIPInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDoTelnetInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDoWANDelete/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnDownModeFile/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnEditRoom/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnExportTemplate/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnFactoryReset/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetAdminInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetAllParams/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetBusinessModeList/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetSIPInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetWanConnect/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnGetWifiInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnImportTemplate/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnInfosSave/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnLogDelete/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnModeCfgSave/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnRefreshDeviceInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnSendWanPing/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnServerSave/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnSetAdminInfo/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnTestDiag/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnUpCpeGroup/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnWorkOrderDelete/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |
| `/client/fnWorkOrderSave/` | tr069 | ✅ | 非 Go 路由：需确认迁入统一工程或保持独立服务 |

## 5. 模块归属判定统计与待确认

| 判定依据 | 条数 |
|---|---|
| comment-prefix | 8 |
| explicit(handler 文件) | 3 |
| keyword | 24 |
| python | 32 |
| switchapi | 17 |

- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleConnectOltUser/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleCpeGroupInfo/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleCurrentConfiguration/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleDBAProfile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleDeviceDelete/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleDeviceInfo/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleDeviceStatus/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleEditRoom/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleItmsUpgraded/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleItmsVersion/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleLineProfile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleLinkAggregationInfo/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleONTAutoFind/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleONTAutoMatic/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleONTListAndStatus/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleONTRegisterProfile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleONTRegistration/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleOltConfigAutoSave/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleOltLogList/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleOltSavedConfig/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleOltTerminalUser/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleRpcApiConfig/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleServerConfig/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleSrvProfile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleTestDiag/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleTr069LogList/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleTr069Poliey/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleTr069Profile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleTr069TemplateList/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleUploadFile/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleUserAction/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleVLANFunctionModule/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleVersionInfo/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleVlanClassifier/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleVlanInformation/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/handleWorkOrderList/`
- 老 Go 单体中未被老壳调用的路由（兼容等级可降低，先确认是否有第三方调用）：`/itms/validateThirdToken/`
- 老前端用 axios `post(url, null, {params})` 传参（**query 参数**），新实现必须继续用 `r.FormValue` 同时兼容 query 与 form。
- 返回体统一 `{code,message,data}`；历史数字字段以字符串返回，禁止顺手改成 int。
- `/client/*` 目前不在 Go 后端内（由 `/itms-py` Python 实现），分包/迁移前必须决定：随 `tr069` 模块一起迁，还是保持独立服务由网关分流。
