package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 本文件搬运 handleVLANFunctionModule（/switchProject/switchmain/olthandler.go"OLT VLAN功能模块"）。
//
// 契约：POST + `connData`（连接参数）+ `saveData`（JSON：HandleVLAN）；
//   - Act=0 读已保存配置 → 解析出四类 VLAN 信息，用**顶层字段** `vlanifdata / vlandata /
//     tranvlandata / routestaticdata` 返回（老壳 `getOltVlan` 直接按 res.* 取值）；
//   - Act=1 新增 / Act=2 删除：按 Type 下发对应命令序列（0 VLAN/二层互通、1 上行VLAN透传、
//     2 三层VLAN接口），成功后在同一会话采集并持久化已保存配置、写一条操作日志。
//
// 与老实现的差异（记入 docs/09 §5.2）：写分支补上操作日志（老实现不写）——
// 命令序列与失败文案逐字保持。

const (
	// vlanOperationTimeout 是单条命令的一轮读取超时（老实现 1s）。
	vlanOperationTimeout = 1 * time.Second
	// vlanLogMenu 是写操作日志使用的菜单名。
	vlanLogMenu = "VLAN功能"
)

// handleVLANFunctionModule 处理OLT VLAN 功能模块（获取/新增/删除）。
func (m *Module) handleVLANFunctionModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	cp, err := decodeConnectParameters(r.FormValue("connData"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	var hv HandleVLAN
	if err := json.Unmarshal([]byte(r.FormValue("saveData")), &hv); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch a := hv.Act; {
	case a == 0:
		// 读已保存配置：表优先、文件兜底（与 handleCurrentConfiguration 同一读路径）。
		analysestr := ""
		if content, ok, err := m.store.LoadConfigText(cp.IP, ConfigKindSaved); err == nil && ok {
			analysestr = content
		} else {
			analysestr = m.loadSavedConfiguration(cp.IP)
		}
		if strings.TrimSpace(analysestr) == "" {
			refreshed, refreshErr := m.refreshSavedConfiguration(cp)
			if refreshErr != nil {
				respondConnectionFailed(w, refreshErr)
				return
			}
			analysestr = refreshed
		}
		vlaniflist, normalvlanlist, tranvlanlist, routestaticlist := parseOLTVLANConfiguration(analysestr)
		writeJSON(w, http.StatusOK, APIResponse{
			Code:            0,
			Message:         "success",
			VlanifData:      vlaniflist,
			VlanData:        normalvlanlist,
			TranVlanData:    tranvlanlist,
			RouteStaticData: routestaticlist,
		})
	case a == 1 || a == 2:
		// 写操作先校验必填（VlanID / 三段 Fsp / 三层接口新增时的 IP+Mask），缺参直接拒绝。
		// 删除（Act=2）三层 VLAN 接口只用到 VlanID（undo interface vlanif），故 IP/Mask
		// 仅在「新增」时必填——修复：此前删除三层接口只带 VlanID 却仍要求 IP+Mask，导致
		// 前端删除被误拒 "IP and Mask are required"。
		if problem := validateVLANWrite(hv); problem != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: problem})
			return
		}
		client, err := m.connectDevice(cp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		if message := m.applyVLANCommand(client, hv); message != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
			return
		}
		// 写命令完成后，直接在「同一会话」上采集并持久化已保存配置（与 handleOltTerminalUser 同一提速路径）。
		if _, refreshErr := m.persistSavedConfiguration(client, cp.IP); refreshErr != nil {
			m.logf("VLAN 操作后刷新配置失败(%s/%d): %v", cp.IP, hv.Act, refreshErr)
		}
		// 写操作日志（老实现没有，按本模块同口径补齐）。
		m.writeVLANLog(cp.IP, hv, r.RemoteAddr)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
	}
}

// validateVLANWrite 校验 Act=1/2 的必填参数；合法返回空串。
func validateVLANWrite(hv HandleVLAN) string {
	switch hv.Type {
	case 0: // VLAN / 二层互通：只需 VlanID
		if strings.TrimSpace(hv.VlanID) == "" {
			return "invalid request: VlanID is required"
		}
	case 1: // 上行 VLAN 透传：VlanID + Fsp（三段 机框/槽位/端口）
		if strings.TrimSpace(hv.VlanID) == "" {
			return "invalid request: VlanID is required"
		}
		if len(strings.Split(strings.TrimSpace(hv.Fsp), "/")) != 3 {
			return "invalid request: Fsp must be frame/slot/port"
		}
	case 2: // 三层 VLAN 接口：新增需 VlanID + IP + Mask；删除只需 VlanID（undo interface vlanif）
		if strings.TrimSpace(hv.VlanID) == "" {
			return "invalid request: VlanID is required"
		}
		// 删除（Act=2）只发 undo interface vlanif<id>，无需 IP/Mask；仅新增（Act=1）要带地址。
		if hv.Act != 2 && (strings.TrimSpace(hv.IP) == "" || strings.TrimSpace(hv.Mask) == "") {
			return "invalid request: IP and Mask are required"
		}
	}
	return ""
}

// applyVLANCommand 在已连接的配置会话上执行新增（Act=1）或删除（Act=2）命令序列（老实现逐字）。
// 返回非空字符串表示失败消息；空串表示成功。
func (m *Module) applyVLANCommand(client *oltDevice, hv HandleVLAN) string {
	switch hv.Act {
	case 1: // 新增
		switch hv.Type {
		case 0: // VLAN / 二层互通
			var docmd string
			switch hv.VlanType {
			case 0:
				docmd = fmt.Sprintf("vlan %s smart", strings.TrimSpace(hv.VlanID))
			case 1:
				docmd = fmt.Sprintf("vlan attrib %s q-in-q", strings.TrimSpace(hv.VlanID))
			case 2:
				docmd = fmt.Sprintf("vlan attrib %s stacking", strings.TrimSpace(hv.VlanID))
			}
			if docmd == "" {
				return "Operation failed"
			}
			domsg, _ := client.runTerminalCommand(docmd, vlanOperationTimeout)
			if strings.Contains(domsg, "(y/n)") {
				if err := client.session.Send("y"); err != nil {
					return oltOperationError(err).Error()
				}
			} else if failure, ok := extractOLTFailure(domsg); ok {
				return strings.TrimSpace(failure)
			}
		case 1: // 上行 VLAN 透传
			undovlans := strings.TrimSpace(hv.VlanID)
			fsplist := strings.Split(strings.TrimSpace(hv.Fsp), "/")
			if len(fsplist) != 3 {
				return "Operation failed"
			}
			docmd := fmt.Sprintf("port vlan %s %s/%s %s", undovlans,
				strings.TrimSpace(fsplist[0]), strings.TrimSpace(fsplist[1]), strings.TrimSpace(fsplist[2]))
			domsg, _ := client.runTerminalCommand(docmd, vlanOperationTimeout)
			if strings.Contains(domsg, "(y/n)") {
				if err := client.session.Send("y"); err != nil {
					return oltOperationError(err).Error()
				}
			} else if failure, ok := extractOLTFailure(domsg); ok {
				return strings.TrimSpace(failure)
			}
		case 2: // 三层 VLAN 接口
			docmd := fmt.Sprintf("interface vlanif%s", strings.TrimSpace(hv.VlanID))
			if err := client.session.Send(docmd); err != nil {
				return oltOperationError(err).Error()
			}
			domsg, _ := client.readOnce(vlanOperationTimeout)
			if strings.Contains(strings.ToLower(domsg), "error") {
				return "Operation failed;VLAN ID does not exist or input format is incorrect"
			}
			if err := client.session.Send(fmt.Sprintf("ip address %s %s", strings.TrimSpace(hv.IP), strings.TrimSpace(hv.Mask))); err != nil {
				return oltOperationError(err).Error()
			}
			if err := client.session.Send(""); err != nil {
				return oltOperationError(err).Error()
			}
			if _, geterr := client.readOnce(vlanOperationTimeout); geterr != nil {
				return "Operation failed"
			}
			if err := client.session.Send("quit"); err != nil {
				return oltOperationError(err).Error()
			}
		}
	case 2: // 删除
		var docmd string
		switch hv.Type {
		case 0:
			docmd = fmt.Sprintf("undo vlan %s", strings.TrimSpace(hv.VlanID))
		case 1:
			fsp := strings.Split(strings.TrimSpace(hv.Fsp), "/")
			if len(fsp) == 3 {
				docmd = fmt.Sprintf("undo port vlan %s %s/%s %s", strings.TrimSpace(hv.VlanID), fsp[0], fsp[1], fsp[2])
			}
		case 2:
			docmd = fmt.Sprintf("undo interface vlanif%s", strings.TrimSpace(hv.VlanID))
		}
		if docmd == "" {
			return "Operation failed"
		}
		if err := client.session.Send(docmd); err != nil {
			return oltOperationError(err).Error()
		}
		domsg, _ := client.readOnce(vlanOperationTimeout)
		if strings.Contains(domsg, ":") {
			if strings.Contains(domsg, "(y/n)") {
				if err := client.session.Send("y"); err != nil {
					return oltOperationError(err).Error()
				}
			} else if err := client.session.Send(""); err != nil {
				return oltOperationError(err).Error()
			}
		} else if strings.Contains(domsg, "success") || strings.Contains(domsg, "#") {
			// 成功
		} else {
			return "Operation failed"
		}
	default:
		return "invalid Action"
	}
	return ""
}

// vlanActionLabel 把 Act 转成操作日志的 szAct 文案。
func vlanActionLabel(act int) string {
	switch act {
	case 1:
		return "新增"
	case 2:
		return "删除"
	default:
		return fmt.Sprintf("Act=%d", act)
	}
}

// writeVLANLog 写一条 VLAN 功能操作日志（老实现未写，本模块补齐）。
func (m *Module) writeVLANLog(ip string, hv HandleVLAN, remoteAddr string) {
	text := fmt.Sprintf("Type=%d VlanID=%s VlanType=%d Fsp=%s IP=%s Mask=%s",
		hv.Type, strings.TrimSpace(hv.VlanID), hv.VlanType, strings.TrimSpace(hv.Fsp),
		strings.TrimSpace(hv.IP), strings.TrimSpace(hv.Mask))
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        vlanLogMenu,
		Act:         vlanActionLabel(hv.Act),
		OperationIP: remoteAddr,
		Text:        text,
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, vlanActionLabel(hv.Act), err)
	}
}
