package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// 本文件搬运 handleConnectOltUser 与 handleOltConnect（只读分支）。
//
// 施工口径见 docs/09 §0：接口路径、Act 语义、字段名与业务流程不变；
// 未迁入的写分支（handleConnectOltUser Act=1/2/3）明确返回"第二批待迁入"提示，
// 不做假成功，也不静默失败（老实现 Act=0 会吞掉库错误，这里改为返回 -1 并写明原因）。

// writeSecondBatchPending 对尚未迁入的分支给出明确提示（避免被误判成参数错误）。
func writeSecondBatchPending(w http.ResponseWriter, path, detail string) {
	message := "该分支尚未迁移到统一工程（第二批待迁入）：" + path
	if strings.TrimSpace(detail) != "" {
		message += "（" + detail + "）"
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
}

// hasUsableOltCurrentConfig 判断缓存里是否有可用板卡数据（老实现同名函数）。
func hasUsableOltCurrentConfig(cfg OltCurrentConfig) bool {
	return len(cfg.OltBoardInfo) > 0
}

// isOltConnectionError 判断错误是否属于"连不上 / 登录不上 / 设备操作失败"。
// 老实现按消息特征分流（"读取欢迎信息错误" / "读取响应错误" / "操作失败"）；
// 新实现的 oltconn 用中文消息描述连接与登录失败，这里把两类特征都覆盖。
func isOltConnectionError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, hint := range []string{"连接", "登录", "欢迎信息", "读取响应", "操作失败"} {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

// respondConnectionFailed 等价老实现 writeOltConnectionFailed（文案 "Connection failed: " 逐字保持）。
func respondConnectionFailed(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Connection failed: " + err.Error()})
}

// handleConnectOltUser 搬运自老实现（/switchProject/switchmain/olthandler.go 1228 行起）。
//
// 契约：POST + `Act` + `saveData`（JSON：Id/Username/Password/Port/Type/Comment/IP）；
//   - Act=0 查询 tbOltUser 全表（`ORDER BY Automatic DESC`），字段名不变；
//   - Act=1 新增 / Act=2 修改（按 Id）/ Act=3 删除（按 Id），成功后写一条 OLT 操作日志
//     （老实现 `MakeOltLogSave(IP, "OLT连接用户", 新增|修改|删除, RemoteAddr, Username, "")`）。
//
// 与老实现的差异（详见 docs/09 §5.2）：
//   - SQL 改为绑定参数（老实现把 saveData 字段直接拼进 SQL，属注入面）；
//   - 口令入库前用平台层 AES 密钥加密（账号列保持可读），已是密文则不重复加密；
//   - 新增/修改校验 Username 与 IP 必填、修改/删除校验 Id 有效（老实现会写入空账号或静默不生效）；
//   - 库错误不再被吞（老实现只是打日志或返回通用文案，这里同样写服务端日志并返回可读错误）。
//
// 口令沿用老口径明文回给前端（老前端要把 connData 原样回传），库里存的可能是密文，
// 因此查询时就地解密（密文优先、明文兜底）。
func (m *Module) handleConnectOltUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	handleact, acterr := strconv.Atoi(r.FormValue("Act"))
	if acterr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + acterr.Error()})
		return
	}
	switch handleact {
	case 0:
		users, err := m.store.QueryOltUsers()
		if err != nil {
			// 老实现在这里忽略了 QueryRows 的错误（静默失败）；按新政策修正为显式失败。
			m.logf("查询 OLT 连接用户失败: %v", err)
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Error querying database"})
			return
		}
		for index := range users {
			users[index].Username = m.decryptOltSecret(users[index].Username)
			users[index].Password = m.decryptOltSecret(users[index].Password)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: users})
	case 1, 2, 3:
		m.writeOltUser(w, r, handleact)
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request"})
	}
}

// writeOltUser 处理 handleConnectOltUser 的写分支（Act=1 新增 / 2 修改 / 3 删除）。
func (m *Module) writeOltUser(w http.ResponseWriter, r *http.Request, act int) {
	saveData := r.FormValue("saveData")
	if act == 3 {
		m.deleteOltUsers(w, r, saveData)
		return
	}
	var user OltUser
	if err := json.Unmarshal([]byte(saveData), &user); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	user.Username = strings.TrimSpace(user.Username)
	user.Password = strings.TrimSpace(user.Password)
	user.IP = strings.TrimSpace(user.IP)
	user.Comment = strings.TrimSpace(user.Comment)
	port, portErr := parseOltUserPort(user.Port)

	var (
		action               string
		err                  error
		skipPasswordStrength bool
	)
	switch act {
	case 1:
		action = "新增"
	default: // act == 2
		action = "修改"
		if user.Id <= 0 {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: OLT id is required"})
			return
		}
		// 编辑场景：口令未改动时只校验长度与字符集（老库存量口令可能不满足新的强度要求，
		// 否则会出现"存量设备改不了备注"的死结）。
		if existing, findErr := m.store.FindOltUserByID(user.Id); findErr == nil {
			if m.decryptOltSecret(existing.Password) == user.Password {
				skipPasswordStrength = true
			}
		}
	}

	// 新增/修改：按业务方规格做字段校验（IP/域名、连接类型、账号、口令、端口）。
	if portErr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + portErr.Error()})
		return
	}
	if err := validateOltUserWrite(user, port, skipPasswordStrength); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	if act == 1 {
		err = m.store.InsertOltUser(user, m.encryptOltSecret(user.Password), port)
	} else {
		err = m.store.UpdateOltUser(user, m.encryptOltSecret(user.Password), port)
	}
	if err != nil {
		m.logf("OLT 连接用户%s失败(IP=%s, Username=%s): %v", action, user.IP, user.Username, err)
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Error updating database"})
		return
	}
	m.writeOltUserLog(user.IP, action, r.RemoteAddr, user.Username)
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

// parseOltUserPort 解析表单里的端口文本：空值按 23（业务方规格的默认端口），非法文本返回错误
// （范围 1-65535 由 validateOltUserWrite 统一校验）。
func parseOltUserPort(portText string) (int, error) {
	portText = strings.TrimSpace(portText)
	if portText == "" {
		return 23, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0, fmt.Errorf("端口必须是数字")
	}
	return port, nil
}

// deleteOltUsers 处理 Act=3：**单条与批量删除**（前端台账的「删除 (n)」批处理走这里）。
//
// saveData 兼容三种形态：
//
//	{"Id":1}           单条删除（老契约）
//	{"Ids":"1,2,3"}    批量删除（沿用本模块 handleOltLogList 的 Ids 逗号串口径）
//	[1,2,3]            JSON 数组（与 switch 台账 api.deleteDevices(ids) 同风格）
//
// 不存在的 Id 会被跳过（老实现对该情况也返回 success），每个真正删除的设备写一条操作日志。
func (m *Module) deleteOltUsers(w http.ResponseWriter, r *http.Request, saveData string) {
	ids := parseOltUserDeleteIds(saveData)
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: OLT id is required"})
		return
	}
	targets := make([]OltUser, 0, len(ids))
	for _, id := range ids {
		if user, err := m.store.FindOltUserByID(id); err == nil {
			targets = append(targets, user)
		}
	}
	if err := m.store.DeleteOltUsers(ids); err != nil {
		m.logf("OLT 连接用户删除失败(ids=%v): %v", ids, err)
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Error updating database"})
		return
	}
	for _, user := range targets {
		m.writeOltUserLog(user.IP, "删除", r.RemoteAddr, user.Username)
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

// parseOltUserDeleteIds 解析删除目标 Id（兼容 {"Id":n} / {"Ids":"n,m"} / [n,m] 三种形态，自动去重）。
func parseOltUserDeleteIds(saveData string) []int {
	saveData = strings.TrimSpace(saveData)
	if saveData == "" {
		return nil
	}
	var array []int
	if err := json.Unmarshal([]byte(saveData), &array); err == nil {
		return normalizeOltUserIds(array)
	}
	var payload struct {
		Id  int    `json:"Id"`
		Ids string `json:"Ids"`
	}
	if err := json.Unmarshal([]byte(saveData), &payload); err != nil {
		return nil
	}
	ids := make([]int, 0, 4)
	if payload.Id > 0 {
		ids = append(ids, payload.Id)
	}
	for _, part := range strings.Split(payload.Ids, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return normalizeOltUserIds(ids)
}

// normalizeOltUserIds 过滤非法 Id 并去重（保持调用顺序，便于日志按界面顺序写入）。
func normalizeOltUserIds(ids []int) []int {
	seen := make(map[int]bool, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

// writeOltUserLog 写一条 OLT 操作日志（等价老实现 MakeOltLogSave）。
//
// 老实现在这里把 `r.RemoteAddr`（形如 127.0.0.1:54321）直接当操作 IP 存下，列宽 128 足够；
// 为保持老库的既有形态，这里同样原样写入，不做拆分。
// 日志写失败只记服务端日志，不影响主流程（与老实现一致）。
func (m *Module) writeOltUserLog(ip, action, remoteAddr, username string) {
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        "OLT连接用户",
		Act:         action,
		OperationIP: remoteAddr,
		Text:        strings.TrimSpace(username),
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, action, err)
	}
}

// handleOltConnect 搬运自老实现（olthandler.go 1400 行起"初始OLT相关信息"）。
//
// Act=0：返回本模块缓存的 OLT 当前配置（老实现读 currentconfiguration.json）。
//
//	老实现此时 Message 是 "sueccess"（笔误），为不改变老前端的判断逻辑逐字保留。
//
// Act=1：标记 Automatic 登录用户 → 采集（display version/temperature/board）→ 写缓存；
//
//	命中缓存且未带 refresh=1 时立即返回缓存并后台刷新（老实现的快路径）。
func (m *Module) handleOltConnect(w http.ResponseWriter, r *http.Request) {
	trace := newOltTrace(r.FormValue("trace") == "1")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	handleact, acterr := strconv.Atoi(r.FormValue("Act"))
	if acterr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + acterr.Error()})
		return
	}
	connData := r.FormValue("connData")
	var cp ConnectParameters
	cp, err := decodeConnectParameters(connData)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	// 老实现会确保 OltFilePath 存在；新实现确保本模块自己的文件目录存在（老目录保持只读）。
	if err := os.MkdirAll(m.fileDir(), 0755); err != nil {
		m.logf("创建 OLT 文件目录失败: %v", err)
	}
	respond := func(code int, message string, data any) {
		// 慢请求诊断：无关 trace=1，整段采集超 3 秒即打印各阶段耗时（connect_telnet / display_version /
		// display_board_slot ...），用于真机定位「连接/刷新耗时偏长」到底卡在哪条命令。
		if elapsed := trace.totalElapsed(); elapsed >= 3*time.Second {
			m.logf("handleOltConnect 慢请求 ip=%s act=%d trace=%t 耗时=%s 阶段=%s",
				cp.IP, handleact, trace.enabled, elapsed, trace.summary())
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: code, Message: message, Data: writeTraceData(data, trace)})
	}
	switch handleact {
	case 0:
		// 读缓存：**表优先、老文件兜底**（2026-09-22 起采集结果入库 tbOltDeviceSnapshot 系列）。
		// 连带返回该设备在台账的连接/在线状态（tbOltUser.Automatic），供前端只看缓存时也能展示。
		var cfg OltCurrentConfig
		if cached, ok, err := m.store.LoadDeviceSnapshot(cp.IP); err == nil && ok {
			cfg = cached
		} else {
			cfg, _ = m.loadCurrentConfig(cp.IP)
		}
		m.fillDeviceOnlineStatus(cp.IP, &cfg)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "sueccess", Data: cfg})
		return
	case 1:
		if err := m.store.SetOltUserAutomatic(cp.Username); err != nil {
			m.logf("更新登录OLT信息失败: %v", err)
		}
		forceRefresh := r.FormValue("refresh") == "1"
		var cachedCfg *OltCurrentConfig
		if cached, ok := m.loadCurrentConfig(cp.IP); ok && hasUsableOltCurrentConfig(cached) {
			cachedCfg = &cached
		}
		if cachedCfg != nil && !forceRefresh {
			m.refreshCurrentConfigInBackground(cp)
			respond(0, "success", *cachedCfg)
			return
		}
		oltconfig, err := m.collectCurrentConfig(cp, trace, cachedCfg, true)
		if err != nil {
			if isOltConnectionError(err) {
				respondConnectionFailed(w, err)
				return
			}
			respond(-1, err.Error(), "")
			return
		}
		respond(0, "success", oltconfig)
		// 连接成功后：后台采集并持久化「已保存配置」（Kind=savedconfiguration），使
		// DBA/线路模板等「读已保存配置」的栏目（Act=0 表优先、文件兜底）在重连后立即反映
		// 真机最新配置——此前 Act=1 只采版本/温度/板卡，重连不会刷新模板缓存（2026-09-29 修）。
		// 全量 display current-configuration 采集不走本次响应，复用与 handle line/DBA 写分支
		// 同一后台会话（refreshSavedConfigurationInBackground），失败仅记日志、读取侧自然兜底。
		m.refreshSavedConfigurationInBackground(cp)
		return
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}

// fillDeviceOnlineStatus 把该设备在台账的连接/在线状态（tbOltUser.Automatic）填进 cfg.Automatic。
//
// 语义对齐 handleConnectOltUser Act=0 的列表口径：Automatic=1 表示该条连接用户正被标记为
// "当前连接/自动登录"（Act=1 连设备时 SetOltUserAutomatic 置 1）。同一 IP 下任一条被标记即视为已连接。
// 库错误只记日志，不影响读缓存主流程（与老实现吞错的宽容口径一致，缓存仍照常返回）。
func (m *Module) fillDeviceOnlineStatus(ip string, cfg *OltCurrentConfig) {
	if cfg == nil {
		return
	}
	cfg.Automatic = 0
	found, err := m.store.FindOltUserByIP(ip, "")
	if err != nil {
		m.logf("读取 OLT 设备连接状态失败(%s): %v", ip, err)
		return
	}
	if found.Automatic == 1 {
		cfg.Automatic = 1
	}
}
