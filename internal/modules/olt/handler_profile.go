package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 本文件搬运 handleDBAProfile（/switchProject/switchmain/olthandler.go 2264 行起"OLT DBA模版"）。
//
// 契约：POST + `connData`（连接参数）+ `saveData`（JSON：DBAProfile）：
//   - Act=0 读已保存配置 → 解析出全部 DBA 模板（`[global-config]` 段的 `dba-profile add` 行）；
//   - Act=1 新增 / Act=2 删除：连接设备下发 `dba-profile add/delete` 命令，成功后**同一会话**
//     采集并持久化已保存配置、写一条「DBA模板」操作日志。
//
// 与老实现的差异（记入 docs/09 §5.2）：
//   - 校验老实现用了错误的 `&&`/`||` 组合（`id<10 && id>512`、`fix<128 && fix>10000000` 恒为假），
//     导致范围校验永远不触发；本实现按文档口径修正为正确闭区间（`id<10 || id>512` 等）。
//   - 写分支补上操作日志（老实现不写）。

const (
	// dbaLogMenu 是写操作日志使用的菜单名。
	dbaLogMenu = "DBA模板"
)

// validateDBAWrite 校验 Act=1/2 的必填参数；合法返回空串。
//
// 老实现的范围校验写法存在缺陷（如 `Id<10 && Id>512` 恒假），这里按文档语义修正为
// 正确闭区间（Id 10-512、Name 1-33、带宽 128-10000000；type1 fix / type2 assure /
// type3 assure+max / type4 max / type5 fix+assure+max）。
func validateDBAWrite(dp DBAProfile) string {
	if dp.Id < 10 || dp.Id > 512 {
		return "请输入正确的模板ID(10-512)"
	}
	if dp.Act == 1 && (len(strings.TrimSpace(dp.Name)) < 1 || len(strings.TrimSpace(dp.Name)) > 33) {
		return "请输入正确的模板名(1-33)"
	}
	// 删除（Act=2）只需 Id；以下带宽校验仅适用于新增。
	if dp.Act != 1 {
		return ""
	}
	inRange := func(v int) bool { return v >= 128 && v <= 10000000 }
	switch dp.Type {
	case "type1", "type5":
		if !inRange(dp.Fix) {
			return "请输入正确的固定带宽(数值类型,取值范围:128-10000000)"
		}
	}
	switch dp.Type {
	case "type2", "type3", "type5":
		if !inRange(dp.Assure) {
			return "请输入正确的保证带宽(数值类型,取值范围:128-10000000)"
		}
	}
	switch dp.Type {
	case "type3", "type4", "type5":
		if !inRange(dp.Max) {
			return "请输入正确的最大带宽(数值类型,取值范围:128-10000000)"
		}
	}
	switch dp.Type {
	case "type1", "type2", "type3", "type4", "type5":
	default:
		return "invalid request: unsupported DBA type " + dp.Type
	}
	return ""
}

// applyDBACommand 在已连接的配置会话上执行新增（Act=1）或删除（Act=2）命令。
// 返回非空字符串表示失败消息；空串表示成功。
//
// 用 runCommand（连接层 RunCommandUntilSignal）而不是单轮 runTerminalCommand：
// runTerminalCommand 只在 1s 内读一轮回显、不确认设备是否回到 `(config)#` 提示符；
// 若设备因 profile-name 等交互参数而停留在参数提示上，回显会被当作「已完成的命令输出」
// 返回，随后同一会话的 `display current-configuration` 会被打字到参数提示、既不发命令也
// 永不出现 `return`，进而让 collectSavedConfiguration 无限空转（本缺陷的真机根因）。
// 改为等设备回到 `#` 提示符，读回内容必然完整，写命令才算真正完成。
func (m *Module) applyDBACommand(client *oltDevice, dp DBAProfile) string {
	incmd := ""
	switch dp.Act {
	case 1:
		switch dp.Type {
		case "type1":
			incmd = fmt.Sprintf("dba-profile add profile-id %d profile-name %s type1 fix %d", dp.Id, strings.TrimSpace(dp.Name), dp.Fix)
		case "type2":
			incmd = fmt.Sprintf("dba-profile add profile-id %d profile-name %s type2 assure %d", dp.Id, strings.TrimSpace(dp.Name), dp.Assure)
		case "type3":
			incmd = fmt.Sprintf("dba-profile add profile-id %d profile-name %s type3 assure %d max %d", dp.Id, strings.TrimSpace(dp.Name), dp.Assure, dp.Max)
		case "type4":
			incmd = fmt.Sprintf("dba-profile add profile-id %d profile-name %s type4 max %d", dp.Id, strings.TrimSpace(dp.Name), dp.Max)
		case "type5":
			incmd = fmt.Sprintf("dba-profile add profile-id %d profile-name %s type5 fix %d assure %d max %d", dp.Id, strings.TrimSpace(dp.Name), dp.Fix, dp.Assure, dp.Max)
		}
	case 2:
		incmd = fmt.Sprintf("dba-profile delete profile-id %d", dp.Id)
	}
	if strings.TrimSpace(incmd) == "" {
		return "invalid request"
	}
	domsg, err := client.runCommand(incmd)
	if err != nil {
		return oltOperationError(err).Error()
	}
	if failure, ok := extractOLTFailure(domsg); ok {
		return strings.TrimSpace(failure)
	}
	return ""
}

// dbaActionLabel 把 Act 转成操作日志的 szAct 文案。
func dbaActionLabel(act int) string {
	switch act {
	case 1:
		return "新增"
	case 2:
		return "删除"
	default:
		return fmt.Sprintf("Act=%d", act)
	}
}

// writeDBALog 写一条 DBA 模板操作日志（老实现未写，本模块补齐）。
func (m *Module) writeDBALog(ip string, dp DBAProfile, remoteAddr string) {
	text := fmt.Sprintf("Id=%d Name=%s Type=%s Fix=%d Assure=%d Max=%d",
		dp.Id, strings.TrimSpace(dp.Name), dp.Type, dp.Fix, dp.Assure, dp.Max)
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        dbaLogMenu,
		Act:         dbaActionLabel(dp.Act),
		OperationIP: remoteAddr,
		Text:        text,
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, dbaActionLabel(dp.Act), err)
	}
}
func (m *Module) handleDBAProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	tcp, err := decodeConnectParameters(r.FormValue("connData"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	var dp DBAProfile
	if err := json.Unmarshal([]byte(r.FormValue("saveData")), &dp); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch a := dp.Act; {
	case a == 0:
		// 读已保存配置：表优先、文件兜底（与 handleVLANFunctionModule 同一读路径）。
		analysestr := ""
		if content, ok, err := m.store.LoadConfigText(tcp.IP, ConfigKindSaved); err == nil && ok {
			analysestr = content
		} else {
			analysestr = m.loadSavedConfiguration(tcp.IP)
		}
		if strings.TrimSpace(analysestr) == "" {
			refreshed, refreshErr := m.refreshSavedConfiguration(tcp)
			if refreshErr != nil {
				respondConnectionFailed(w, refreshErr)
				return
			}
			analysestr = refreshed
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseOLTDBAProfiles(analysestr)})
	case a == 1 || a == 2:
		if problem := validateDBAWrite(dp); problem != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: problem})
			return
		}
		client, err := m.connectDevice(tcp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		if message := m.applyDBACommand(client, dp); message != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
			return
		}
		// 写命令完成后，直接在「同一会话」上采集并持久化已保存配置（与 handleOltTerminalUser 同一提速路径）。
		if _, refreshErr := m.persistSavedConfiguration(client, tcp.IP); refreshErr != nil {
			m.logf("DBA 操作后刷新配置失败(%s/%d): %v", tcp.IP, dp.Act, refreshErr)
		}
		// 写操作日志（老实现没有，按本模块同口径补齐）。
		m.writeDBALog(tcp.IP, dp, r.RemoteAddr)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
	}
}
