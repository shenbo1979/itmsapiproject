package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 本文件搬运 handleLineProfile（/switchProject/switchmain/olthandler.go 2378 行起「ONT线路模版」），
// 即「DBA/线路模板」栏目中线路模板（ont-lineprofile）部分的读、写骨架，与 handler_profile.go 的
// handleDBAProfile 同批（第二批）迁入。
//
// 契约：POST + `connData`（连接参数）+ `saveData`（JSON：LineProfile）：
//   - Act=0 读已保存配置 → 解析出全部 ONT 线路模板（`[global-config]` 段的 `ont-lineprofile` 及其
//     tcont / gem add / gem mapping 子树，`GemMapping[].GemInfo[]` 层级与服务模板页子表格一致）；
//   - Act=1 新增 / Act=2 删除：连接设备下发 `ont-lineprofile …`（新增时随之一并发 `tr069-management`、
//     `tcont`、`gem add`、`gem mapping`、`commit`、`quit`；删除用 `undo ont-lineprofile`，需 `Id`+`PonType`），成功后**同一会话**
//     采集并持久化已保存配置、写一条「ONT线路模板」操作日志。
//
// 与老实现的差异（记入 docs/09-olt-m2.md）：
//   - 设备写路径用 runCommand（RunCommandUntilSignal 等到 `(config)` 提示符）而非老实现单轮
//     readOLTResponseUntil，与 handleDBAProfile 同一口径，避免交互参数提示把命令读断；
//   - 写分支补上操作日志（老实现不写）。
const (
	// lineProfileLogMenu 是写操作日志使用的菜单名。
	lineProfileLogMenu = "ONT线路模板"
)

// validateLineProfileWrite 校验 Act=1/2 的必填参数；合法返回空串。
//
// 老实现范围校验：Id 10-4092、Name 1-33（仅新增；删除只需 Id）。沿用同一闭区间口径，
// 按业务方需求把 Id 范围改为 1-8192。Act=1 新增时 DbaProfileId 必选（非正数拒绝）。
// 删除（Act=2）命令 `undo ont-lineprofile <type> profile-id <id>` 同样需要 PonType，
// 故 gpon/epon 校验对新增与删除都生效，避免缺 PonType 时拼出畸形命令却“假成功”。
func validateLineProfileWrite(lp LineProfile) string {
	if lp.Id < 1 || lp.Id > 8192 {
		return "请输入正确的模板ID(1-8192)"
	}
	if lp.Act == 1 && (len(strings.TrimSpace(lp.Name)) < 1 || len(strings.TrimSpace(lp.Name)) > 33) {
		return "请输入正确的模板名(1-33)"
	}
	if (lp.Act == 1 || lp.Act == 2) && !strings.EqualFold(lp.PonType, "gpon") && !strings.EqualFold(lp.PonType, "epon") {
		return "请输入正确的PON类型(gpon,epon)"
	}
	if lp.Act == 1 && lp.DbaProfileId <= 0 {
		return "请选择绑定的DBA模板"
	}
	// Vlan 非空：新增线路模板的 TCONT/GEM/VLAN 全部由 Vlan 驱动（非空才建 tcont 1、gem add、
	// gem mapping）。空 Vlan 会让 applyLineProfileMapping 跳过整段下发，只剩 ont-lineprofile+commit，
	// 表现为「什么都不执行直接忽略」。这里在写入前就拦下，避免空 VLAN 假成功。
	if lp.Act == 1 && len(strings.TrimSpace(lp.Vlan)) == 0 {
		return "请输入VLAN列表(1-4094)"
	}
	return ""
}

// runLineProfileWrite 执行一条线路模板写命令并做失败提取（删除分支与模板创建分支共用）。
func (m *Module) runLineProfileWrite(client *oltDevice, cmd, prefix string) string {
	domsg, err := client.runCommand(cmd)
	if err != nil {
		return prefix + oltOperationError(err).Error()
	}
	if failure, ok := extractOLTFailure(domsg); ok {
		return prefix + strings.TrimSpace(failure)
	}
	return ""
}

// applyLineProfileCommand 在已连接的配置会话上执行新增（Act=1）或删除（Act=2）的整段命令序列。
// 返回非空字符串表示失败消息；空串表示成功。
func (m *Module) applyLineProfileCommand(client *oltDevice, lp LineProfile) string {
	if lp.Act == 2 {
		incmd := fmt.Sprintf("undo ont-lineprofile %s profile-id %d", strings.TrimSpace(lp.PonType), lp.Id)
		return m.runLineProfileWrite(client, incmd, "删除ONT线路模板失败;")
	}
	// Act=1：新建模板。
	incmd := fmt.Sprintf("ont-lineprofile %s profile-id %d profile-name %s",
		strings.TrimSpace(lp.PonType), lp.Id, strings.TrimSpace(lp.Name))
	if rv := m.runLineProfileWrite(client, incmd, "新增ONT线路模板失败;"); rv != "" {
		return rv
	}
	return m.applyLineProfileMapping(client, lp)
}

// runLineProfileSubcmd 执行一条线路模板子命令（tr069-management / tcont / gem add / gem mapping），
// 并把首个可见失败回显收进 firstFail（诊断）而非静默吞掉。
//
// 与 applyDBACommand 同一口径：统一走 runCommand（RunCommandUntilSignal，等设备回到 `#` 提示符），
// 不再用 runTerminalCommand 的「单轮 Read」——后者只读一轮且不确认是否回到提示符，若设备因交互参数
// 停留在参数提示上，命令会被当作已完成的输出返回、实际未生效（真机「Vlan 里没建出 T-CONT/GEM」的隐患）。
// 失败与否仅记录日志、不中断后续子命令（保留真机转录的容错循环），但把首条 Failure/Error 带出，
// 让 handleLineProfile 在 commit 后如实返回诊断，前端据此定位而非「假成功」。
func (m *Module) runLineProfileSubcmd(client *oltDevice, cmd, label string, firstFail *string) {
	if firstFail == nil {
		return
	}
	domsg, err := client.runCommand(cmd)
	if err != nil {
		m.logf("[%s] %s 执行失败:%v", label, cmd, err)
		if *firstFail == "" {
			*firstFail = label + " 下发失败:" + oltOperationError(err).Error()
		}
		return
	}
	lower := strings.ToLower(domsg)
	if failure, ok := extractOLTFailure(domsg); ok {
		m.logf("[%s] %s 回显失败:%s", label, cmd, failure)
		if *firstFail == "" {
			*firstFail = label + ":" + strings.TrimSpace(failure)
		}
	} else if strings.Contains(lower, "error") || strings.Contains(lower, "command not") {
		msg := strings.TrimSpace(domsg)
		m.logf("[%s] %s 疑似失败:%s", label, cmd, msg)
		if *firstFail == "" {
			*firstFail = label + ":" + msg
		}
	}
}

// applyLineProfileMapping 在已创建的线路模板下发 TR069 / TCONT / GEM 映射，随后 commit 并退出模板视图。
// 编号规则与华为真机转录（pro12）及老实现 python 逐字一致，修正此前不兼容的 1-based/独立 T-CONT：
//   - 单一 T-CONT=1：`Vlan` 只要非空，就恒发 `tcont 1 dba-profile-id <dba>`（空值整体跳过，非空必建）；
//   - 按 `Vlan` 的 `/` 分段，段内 `,` 拆 VLAN（0-based，空段/空 VLAN 过滤不发畸形命令）：
//     不含 `/` 时只有 1 个非空段 → `gem add 0 eth tcont 1`（GEM 编号恒为 0）；
//     段内无 `,`（单 VLAN）→ `gem mapping <i> 0 vlan <vlan>`；Vlan 不含 `/` 时即 `gem mapping 0 0 vlan <vlan>`；
//   - `commit` 提交通用后追加 `quit` 退出线路模板子视图，避免残留未提交的配置视图。
//
// 与老实现一致：单个 tr069/tcont/gem/mapping 失败不中断循环（继续发后续命令），仅对 commit 做失败处理；
// 但与老实现不同，不再静默吞没——首条 Failure/Error 会被收进 firstFail，commit 后若存在则作为诊断消息
// 返回（applyLineProfileMapping 已于末尾按此实现），避免「ont-lineprofile 建出来但 Vlan 没 T-CONT/GEM」假成功。
func (m *Module) applyLineProfileMapping(client *oltDevice, lp LineProfile) string {
	tr069 := "disable"
	if lp.OpenTr069 == 1 {
		tr069 = "enable"
	}
	var firstFail string
	m.runLineProfileSubcmd(client, "tr069-management "+tr069, "TR069", &firstFail)
	vlan := strings.TrimSpace(lp.Vlan)
	if len(vlan) > 0 {
		// 单一 T-CONT=1：全部 GEM 共用 tcont 1（与老实现 python 一致）。
		// 当前模板视图内 tcont 未必已建，故先显式创建 tcont 1，再挂 GEM。
		m.runLineProfileSubcmd(client, fmt.Sprintf("tcont 1 dba-profile-id %d", lp.DbaProfileId), "TCONT", &firstFail)
		// gem 编号取「非空段」的计数索引（0-based）：Vlan 不含 `/` 时仅 1 个非空段，
		// gem 编号恒为 0（gem add 0 eth tcont 1）；含 `/` 时按非空段依次 0、1、…。
		gemIndex := 0
		for _, val := range strings.Split(vlan, "/") {
			val = strings.TrimSpace(val)
			if val == "" {
				continue // 空段（如尾随 /）不发 gem add，避免空段 gem 命令
			}
			m.runLineProfileSubcmd(client, fmt.Sprintf("gem add %d eth tcont 1", gemIndex), "GEM", &firstFail)
			// 段内从 0 起的 gem mapping（同一 GEM 内 VLAN 序号 j=0..）：`gem mapping <gemIndex> <j> vlan <v>`。
			vlanindex := 0
			for _, vlanobj := range strings.Split(val, ",") {
				vlanobj = strings.TrimSpace(vlanobj)
				if vlanobj == "" {
					continue // 空 VLAN 不发 mapping，避免 `vlan ` 空参数命令
				}
				m.runLineProfileSubcmd(client, fmt.Sprintf("gem mapping %d %d vlan %s", gemIndex, vlanindex, vlanobj), "gem映射", &firstFail)
				vlanindex++
			}
			gemIndex++
		}
	}
	if _, err := client.runCommand("commit"); err != nil {
		return "提交ONT线路模板失败;" + oltOperationError(err).Error()
	}
	// commit 提交后退出线路模板子视图（华为标准流程收尾）。
	if _, err := client.runTerminalCommand("quit", 1*time.Second); err != nil {
		m.logf("退出ONT线路模板视图失败:%v", err)
	}
	// 子命令曾有失败：如实返回诊断，让调用方/前端看到为何 Vlan 没注册出 T-CONT/GEM，而非「假成功」。
	if firstFail != "" {
		return "ONT线路模板T-CONT/GEM配置未全部生效;" + firstFail
	}
	return ""
}

// lineProfileActionLabel 把 Act 转成操作日志的 szAct 文案。
func lineProfileActionLabel(act int) string {
	switch act {
	case 1:
		return "新增"
	case 2:
		return "删除"
	default:
		return fmt.Sprintf("Act=%d", act)
	}
}

// writeLineProfileLog 写一条 ONT 线路模板操作日志（老实现未写，本模块补齐）。
func (m *Module) writeLineProfileLog(ip string, lp LineProfile, remoteAddr string) {
	text := fmt.Sprintf("Id=%d Name=%s DbaProfileId=%d PonType=%s OpenTr069=%d Vlan=%s",
		lp.Id, strings.TrimSpace(lp.Name), lp.DbaProfileId, strings.TrimSpace(lp.PonType), lp.OpenTr069, lp.Vlan)
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        lineProfileLogMenu,
		Act:         lineProfileActionLabel(lp.Act),
		OperationIP: remoteAddr,
		Text:        text,
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, lineProfileActionLabel(lp.Act), err)
	}
}

// handleLineProfile 处理 ONT 线路模板读、写请求（老实现 handleLineProfile；Act 在 saveData JSON 的 Act 字段）。
func (m *Module) handleLineProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	tcp, err := decodeConnectParameters(r.FormValue("connData"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	var lp LineProfile
	if err := json.Unmarshal([]byte(r.FormValue("saveData")), &lp); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch a := lp.Act; {
	case a == 0:
		// 读已保存配置：表优先、文件兜底（与 handleDBAProfile 同一读路径）。
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
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseOLTLineProfiles(analysestr)})
	case a == 1 || a == 2:
		if problem := validateLineProfileWrite(lp); problem != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: problem})
			return
		}
		client, err := m.connectDevice(tcp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		if message := m.applyLineProfileCommand(client, lp); message != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
			return
		}
		// 写命令完成后，直接在「同一会话」上采集并持久化已保存配置（与 DBA/VLAN/终端用户同一提速路径）。
		// 响应返回时已落库；前端关窗后延迟 ~1s 的单次 Act=0 立即读到本次新增/删除结果，无需手动刷新。
		if _, refreshErr := m.persistSavedConfiguration(client, tcp.IP); refreshErr != nil {
			m.logf("ONT 线路模板操作后刷新配置失败(%s/%d): %v", tcp.IP, lp.Act, refreshErr)
		}
		// 写操作日志（老实现没有，按本模块同口径补齐）。
		m.writeLineProfileLog(tcp.IP, lp, r.RemoteAddr)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
	}
}
