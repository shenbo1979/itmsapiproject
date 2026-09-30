package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 本文件搬运 handleOltTerminalUser（/switchProject/switchmain/olthandler.go 1284 行起"处理OLT登录用户"）。
//
// 契约：POST + `Act` + `connData`（连接参数）+ `saveData`（JSON：TerminalUserName / TerminalPassword）；
//   - Act=0 读已保存配置 → 解析出 `terminal user name <user>` 的登录用户名列表（返回 []string）；
//   - Act=1 新增终端用户（terminal user name 交互序列）；
//   - Act=2 修改终端用户口令（terminal user password 交互序列）；
//   - Act=3 删除终端用户（undo terminal user name 交互序列，需 (y/n) 确认）。
//
// 与老实现逐字保持：Act=0 的 Message 为 "sueccess"（老实现笔误）、读配置走"表优先、文件兜底"、
// 失败提示文本原样；并补齐老实现没有的**操作日志**（增/改/删成功各写一条，与 handleConnectOltUser #11 同一口径）。
type TerminalUser struct {
	TerminalUserName string `json:"TerminalUserName"`
	// TerminalPassword 老实现用同一个字段承载"新增/修改时的口令"；新增/修改都是连输两次（确认）。
	TerminalPassword string `json:"TerminalPassword"`
}

// readOnce 读一轮回显（等价老实现 TelnetClient.ReadResponse：单轮读取）。
func (d *oltDevice) readOnce(timeout time.Duration) (string, error) {
	if d == nil || d.session == nil {
		return "", fmt.Errorf("OLT connection is not initialized")
	}
	return d.session.Read(timeout)
}

// runTerminalCommand 发送一条命令并读一轮回显（等价老实现 runOLTCommandOnce）。
func (d *oltDevice) runTerminalCommand(command string, timeout time.Duration) (string, error) {
	if d == nil || d.session == nil {
		return "", fmt.Errorf("OLT connection is not initialized")
	}
	if err := d.session.Send(command); err != nil {
		return "", err
	}
	return d.session.Read(timeout)
}

// parseOLTTerminalUsers 从已保存配置解析出 OLT 登录用户名（老实现同名函数，逐行一致）。
//
// 只认形如 `terminal user name <user>` 的行，并跳过 password/history_password 等口令行；
// 结果去重、去除首尾空白与双引号，保持出现顺序。
func parseOLTTerminalUsers(configuration string) []string {
	users := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(configuration, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[0], "terminal") ||
			!strings.EqualFold(fields[1], "user") || !strings.EqualFold(fields[2], "name") {
			continue
		}
		if strings.EqualFold(fields[3], "history_password") {
			continue
		}
		usernameIndex := 3
		if strings.EqualFold(fields[3], "buildrun_new_password") {
			usernameIndex = 4
		}
		if len(fields) <= usernameIndex {
			continue
		}
		username := strings.Trim(strings.TrimSpace(fields[usernameIndex]), `"`)
		if username == "" {
			continue
		}
		if _, exists := seen[username]; exists {
			continue
		}
		seen[username] = struct{}{}
		users = append(users, username)
	}
	return users
}

// handleOltTerminalUser 处理OLT登录用户（OLT「配置信息」栏目的终端用户管理）。
func (m *Module) handleOltTerminalUser(w http.ResponseWriter, r *http.Request) {
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
	cp, err := decodeConnectParameters(connData)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch act := handleact; {
	case act == 0:
		// 读配置用户名列表：表优先、文件兜底（与 handleCurrentConfiguration 同一读路径）。
		var configuration string
		if content, ok, err := m.store.LoadConfigText(cp.IP, ConfigKindSaved); err == nil && ok {
			configuration = content
		} else {
			configuration = m.loadSavedConfiguration(cp.IP)
		}
		if strings.TrimSpace(configuration) == "" {
			refreshed, refreshErr := m.refreshSavedConfiguration(cp)
			if refreshErr != nil {
				respondConnectionFailed(w, refreshErr)
				return
			}
			configuration = refreshed
		}
		userdata := parseOLTTerminalUsers(configuration)
		// 与老实现一致：Message 是 "sueccess"（笔误逐字保持）。
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "sueccess", Data: userdata})
	case act == 1 || act == 2 || act == 3: // 新增、修改、删除（均需与设备交互，写操作）
		saveData := r.FormValue("saveData")
		var tu TerminalUser
		if err := json.Unmarshal([]byte(saveData), &tu); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		if strings.TrimSpace(tu.TerminalUserName) == "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: TerminalUserName is required"})
			return
		}
		client, err := m.connectDevice(cp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		switch act {
		case 1: // 新增用户
			if err := m.addOLTTerminalUser(client, tu); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
				return
			}
		case 2: // 修改密码
			if err := m.changeOLTTerminalPassword(client, tu); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
				return
			}
		case 3: // 删除用户
			if err := m.deleteOLTTerminalUser(client, tu); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
				return
			}
		}
		// P1 提速：写命令完成后，直接在「同一会话」上采集并持久化已保存配置
		// （refreshSavedConfiguration 会另外开一条新会话重连登录，这一步正是此前
		// 端到端延迟的大头）。此处仍持有会话写锁：写会话采集不受同 key 并发写影响，
		// 采集完毕后会话由 defer client.Close() 统一释放。
		refreshed := ""
		if cfg, refreshErr := m.persistSavedConfiguration(client, cp.IP); refreshErr != nil {
			m.logf("终端用户操作后刷新配置失败(%s/%s): %v", cp.IP, tu.TerminalUserName, refreshErr)
		} else {
			refreshed = cfg
		}
		// 写操作日志（老实现没有，按本模块 handleConnectOltUser #11 同一口径补齐）。
		action := map[int]string{1: "新增", 2: "修改", 3: "删除"}[act]
		m.writeOltTerminalUserLog(cp.IP, action, r.RemoteAddr, tu.TerminalUserName)
		// P3 提速：Data 直接回传最新用户名列表，前端无需再发一次 Act=0 二次读取。
		// 刷新失败时 Data 为空（前端会兜底走一次刷新，见 olt.js）。
		var userdata []string
		if strings.TrimSpace(refreshed) != "" {
			userdata = parseOLTTerminalUsers(refreshed)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "sueccess", Data: userdata})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}

// addOLTTerminalUser 在设备上新增终端用户（老实现 Act=1 的逐字序列）。
func (m *Module) addOLTTerminalUser(client *oltDevice, tu TerminalUser) error {
	for _, cmd := range []string{"terminal user name", tu.TerminalUserName, tu.TerminalPassword, tu.TerminalPassword} {
		if err := client.session.Send(cmd); err != nil {
			return oltOperationError(err)
		}
	}
	readmsg, readerr := client.readOnce(1 * time.Second)
	if readerr != nil {
		return oltOperationError(readerr)
	}
	if strings.Contains(strings.ToLower(readmsg), "user profile name") {
		profmsg, runErr := client.runTerminalCommand("root", 1*time.Second)
		if runErr != nil {
			return oltOperationError(runErr)
		}
		szDoid := "1"
		if strings.Contains(strings.ToLower(profmsg), "3") {
			szDoid = "3"
		} else if strings.Contains(strings.ToLower(profmsg), "2") {
			szDoid = "2"
		}
		for _, cmd := range []string{szDoid, "4", "", "n"} {
			if err := client.session.Send(cmd); err != nil {
				return oltOperationError(err)
			}
		}
		return nil
	}
	return fmt.Errorf("账号重复或密码格式不正确(1:长度6~15;2:密码要有一定的复杂度,密码中至少包含一个数字和一个字母,密码字符串不能包含问号;3:字母区分大小写,密码不能和用户名相同,也不能是用户名字符串的倒序);")
}

// changeOLTTerminalPassword 修改终端用户口令（搬运老实现 Act=2 的逐字序列）。
func (m *Module) changeOLTTerminalPassword(client *oltDevice, tu TerminalUser) error {
	for _, cmd := range []string{"terminal user password", tu.TerminalUserName, tu.TerminalPassword, tu.TerminalPassword} {
		if err := client.session.Send(cmd); err != nil {
			return oltOperationError(err)
		}
	}
	readmsg, readerr := client.readOnce(1 * time.Second)
	if readerr != nil {
		return oltOperationError(readerr)
	}
	if !strings.Contains(strings.ToLower(readmsg), "(y/n)") {
		return fmt.Errorf("账号重复或密码格式不正确(1:长度6~15;2:密码要有一定的复杂度,密码中至少包含一个数字和一个字母,密码字符串不能包含问号;3:字母区分大小写,密码不能和用户相同,也不能是用户名字符串的倒序);")
	}
	if err := client.session.Send("n"); err != nil {
		return oltOperationError(err)
	}
	return nil
}

// deleteOLTTerminalUser 删除终端用户（对应老实现 Act=3 的逐字序列）。
func (m *Module) deleteOLTTerminalUser(client *oltDevice, tu TerminalUser) error {
	for _, cmd := range []string{"undo terminal user name", tu.TerminalUserName} {
		if err := client.session.Send(cmd); err != nil {
			return oltOperationError(err)
		}
	}
	readmsg, readerr := client.readOnce(1 * time.Second)
	if readerr != nil {
		return oltOperationError(readerr)
	}
	if !strings.Contains(strings.ToLower(readmsg), "(y/n)") {
		return fmt.Errorf("账号不存在或权限不足;")
	}
	for _, cmd := range []string{"y", "n"} {
		if err := client.session.Send(cmd); err != nil {
			return oltOperationError(err)
		}
	}
	return nil
}

// oltOperationError 给设备交互失败的错误补一层"操作失败"前缀（与老实现 writeJSON 的 Message 文案对齐）。
func oltOperationError(err error) error {
	return fmt.Errorf("Operation failed: %s", err.Error())
}

// writeOltTerminalUserLog 写一条"OLT登录用户"操作日志（老实现未写，本模块按 handleConnectOltUser #11 补齐）。
func (m *Module) writeOltTerminalUserLog(ip, action, remoteAddr, username string) {
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        "OLT登录用户",
		Act:         action,
		OperationIP: remoteAddr,
		Text:        strings.TrimSpace(username),
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, action, err)
	}
}
