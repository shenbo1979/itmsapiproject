package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 本文件搬运 handleTr069Profile（/switchProject/switchmain/olthandler.go 2960 行起「TR069管理模板」，
// 第三批迁入），即 OLT 详情页「业务/TR069模板」TAB 的 TR069 管理模板（ont tr069-server-profile）部分。
//
// 归属说明：路径虽带 TR069 关键词，但老单体定义位置（olthandler.go）与运行时上下文都在 OLT 设备侧
// （`ont tr069-server-profile` 命令、解析 OLT 的 [global-config]、connData 为 OLT 连接参数），
// 故随 handleSrvProfile 一并归 olt 模块（见 docs/generated/olt-contract.md 第 2 节）。
//
// 契约：POST + `connData`（连接参数）+ `saveData`（JSON：Tr069Profile）：
//   - Act=0 读已保存配置 → 解析出全部 TR069 管理模板（`[global-config]` 段的
//     `ont tr069-server-profile add` 行，只回 Id/Name/AcsUrl/AcsUserName，**不回密码**）；
//   - Act=1 新增 / Act=2 删除：连接设备下发 `ont tr069-server-profile add/delete` 命令，成功后
//     **同一会话**采集并持久化已保存配置、写一条「TR069管理模板」操作日志。
//
// 与老实现的差异（记入 docs/09-olt-m2.md）：
//   - 老校验 `Id<1 && Id>32`、`len(Name)<1 && len(Name)>32` 等恒假 → 修正为正确闭区间
//     （Id 1-32、Name 1-32、AcsUrl 1-63、AcsUserName 1-50、AcsPassword 1-25）；
//   - 老实现 AcsUserName 校验重复执行（`if len(AcsUserName)<1 && len(AcsUserName)>50` 写了两遍），
//     且 AcsPassword 漏校验 → 这里一次校验并补上密码；
//   - 设备写路径用 runCommand（RunCommandUntilSignal 等到 `(config)` 提示符）而非老实现单轮
//     ReadResponse，与 handleSrvProfile 同一口径；
//   - 写分支补上操作日志（老实现不写）。

const (
	// tr069ProfileLogMenu 是写操作日志使用的菜单名。
	tr069ProfileLogMenu = "TR069管理模板"
)

// validateTr069ProfileWrite 校验 Act=1/2 的必填参数；合法返回空串。
//
// 老实现的范围校验写法存在缺陷（`Id<1 && Id>32` 等恒假，任何 Id 都放行），这里按文档语义
// 修正为正确闭区间：Id 1-32、Name 1-32、AcsUrl 1-63、AcsUserName 1-50、AcsPassword 1-25
// （密码仅新增需要；删除只需 Id）。
func validateTr069ProfileWrite(tp Tr069Profile) string {
	if tp.Id < 1 || tp.Id > 32 {
		return "请输入正确的模板ID(数值类型,取值范围:1-32)"
	}
	if tp.Act == 1 {
		if len(strings.TrimSpace(tp.Name)) < 1 || len(strings.TrimSpace(tp.Name)) > 32 {
			return "请输入正确的模板名(字符串长度:1-32)"
		}
		if len(strings.TrimSpace(tp.AcsUrl)) < 1 || len(strings.TrimSpace(tp.AcsUrl)) > 63 {
			return "请输入正确的URL(字符串长度:1-63)"
		}
		if len(strings.TrimSpace(tp.AcsUserName)) < 1 || len(strings.TrimSpace(tp.AcsUserName)) > 50 {
			return "请输入正确的用户名(字符串长度:1-50)"
		}
		if len(tp.AcsPassword) < 1 || len(tp.AcsPassword) > 25 {
			return "请输入正确的密码(字符串长度:1-25)"
		}
	}
	return ""
}

// applyTr069ProfileCommand 在已连接的配置会话上执行新增（Act=1）或删除（Act=2）命令。
// 返回非空字符串表示失败消息；空串表示成功。
func (m *Module) applyTr069ProfileCommand(client *oltDevice, tp Tr069Profile) string {
	var incmd, prefix string
	switch tp.Act {
	case 1:
		// 老实现命令逐字：`ont tr069-server-profile add profile-id <id> profile-name <name>
		// url <url> user <user> <pass> auth-realm auth`（密码明文拼进命令，与老实现一致）。
		incmd = fmt.Sprintf("ont tr069-server-profile add profile-id %d profile-name %s url %s user %s %s auth-realm auth",
			tp.Id, strings.TrimSpace(tp.Name), strings.TrimSpace(tp.AcsUrl),
			strings.TrimSpace(tp.AcsUserName), tp.AcsPassword)
		prefix = "新增TR069管理模板失败;"
	case 2:
		incmd = fmt.Sprintf("ont tr069-server-profile delete profile-id %d", tp.Id)
		prefix = "删除TR069管理模板失败;"
	}
	domsg, err := client.runCommand(incmd)
	if err != nil {
		return prefix + oltOperationError(err).Error()
	}
	if failure, ok := extractOLTFailure(domsg); ok {
		return prefix + strings.TrimSpace(failure)
	}
	return ""
}

// tr069ProfileActionLabel 把 Act 转成操作日志的 szAct 文案。
func tr069ProfileActionLabel(act int) string {
	switch act {
	case 1:
		return "新增"
	case 2:
		return "删除"
	default:
		return fmt.Sprintf("Act=%d", act)
	}
}

// writeTr069ProfileLog 写一条 TR069 管理模板操作日志（老实现未写，本模块补齐）。
// 密码明文**不**写入日志文本（只记 Id/Name/AcsUrl/AcsUserName）。
func (m *Module) writeTr069ProfileLog(ip string, tp Tr069Profile, remoteAddr string) {
	text := fmt.Sprintf("Id=%d Name=%s AcsUrl=%s AcsUserName=%s",
		tp.Id, strings.TrimSpace(tp.Name), strings.TrimSpace(tp.AcsUrl), strings.TrimSpace(tp.AcsUserName))
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        tr069ProfileLogMenu,
		Act:         tr069ProfileActionLabel(tp.Act),
		OperationIP: remoteAddr,
		Text:        text,
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, tr069ProfileActionLabel(tp.Act), err)
	}
}

// handleTr069Profile 处理 TR069 管理模板读、写请求（老实现 handleTr069Profile；
// Act 在 saveData JSON 的 Act 字段）。
func (m *Module) handleTr069Profile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	tcp, err := decodeConnectParameters(r.FormValue("connData"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	var tp Tr069Profile
	if err := json.Unmarshal([]byte(r.FormValue("saveData")), &tp); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch a := tp.Act; {
	case a == 0:
		// 读已保存配置：表优先、文件兜底（与 handleSrvProfile 同一读路径）。
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
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseOLTTR069Profiles(analysestr)})
	case a == 1 || a == 2:
		if problem := validateTr069ProfileWrite(tp); problem != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: problem})
			return
		}
		client, err := m.connectDevice(tcp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		if message := m.applyTr069ProfileCommand(client, tp); message != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
			return
		}
		// 写命令完成后，直接在「同一会话」上采集并持久化已保存配置（与 handleSrvProfile 同一提速路径）。
		if _, refreshErr := m.persistSavedConfiguration(client, tcp.IP); refreshErr != nil {
			m.logf("TR069 管理模板操作后刷新配置失败(%s/%d): %v", tcp.IP, tp.Act, refreshErr)
		}
		// 写操作日志（老实现没有，按本模块同口径补齐）。
		m.writeTr069ProfileLog(tcp.IP, tp, r.RemoteAddr)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
	}
}
