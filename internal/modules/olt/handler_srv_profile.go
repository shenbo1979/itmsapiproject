package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 本文件搬运 handleSrvProfile（/switchProject/switchmain/olthandler.go 2659 行起「ONT业务模板」，
// 第三批迁入），即 OLT 详情页「业务/TR069模板」TAB 的 ONT 业务模板（ont-srvprofile）部分，
// 与 handler_tr069_profile.go 的 handleTr069Profile 同批。
//
// 契约：POST + `connData`（连接参数）+ `saveData`（JSON：OntSrvProfile）：
//   - Act=0 读已保存配置 → 解析出全部 ONT 业务模板（`[global-config]` 段的 `ont-srvprofile`
//     及其 `ont-port` / `port vlan eth … translation …` 子树，SrvVlan[] 为「网口 → 业务VLAN」映射）；
//   - Act=1 新增 / Act=2 删除：连接设备下发 `ont-srvprofile …`（新增时随之一并发 `ont-port`
//     pots/eth 数量、逐条 `port vlan eth`、`commit`；删除用 `undo ont-srvprofile`，需 Id+PonType），
//     成功后**同一会话**采集并持久化已保存配置、写一条「ONT业务模板」操作日志。
//
// 与老实现的差异（记入 docs/09-olt-m2.md）：
//   - 老校验 `Id<1 && Id>8192` / `Pots/Eths` 组合恒假 → 修正为正确闭区间（Id 1-8192）；
//   - 老壳表单「网口数量」错存到 Pots、「电话口数量」错存到 Eths → 本实现按语义修正
//     （Eths=网口、Pots=电话口），解析端与写命令端一致；
//   - 设备写路径用 runCommand（RunCommandUntilSignal 等到 `(config)` 提示符）而非老实现单轮
//     ReadResponse，与 handleLineProfile 同一口径，避免交互参数提示把命令读断；
//   - 写分支补上操作日志（老实现不写）。

const (
	// srvProfileLogMenu 是写操作日志使用的菜单名。
	srvProfileLogMenu = "ONT业务模板"
)

// validateSrvProfileWrite 校验 Act=1/2 的必填参数；合法返回空串。
//
// 老实现的范围校验写法存在缺陷（`Id<1 && Id>8192`、`Pots<0 && Pots>32` 恒假），这里按文档语义
// 修正为正确闭区间：Id 1-8192、Name 1-33（仅新增）、PonType gpon/epon（新增与删除都需要，
// 删除命令 `undo ont-srvprofile <pon> profile-id <id>` 同样拼 PonType）。Pots/Eths 为 0-32/0-24
// 的字符串数量，非数字按 0 收敛（老实现 clamp），Vlan 每个业务 VLAN 须为 1-4094（仅新增）。
func validateSrvProfileWrite(sp OntSrvProfile) string {
	if sp.Id < 1 || sp.Id > 8192 {
		return "请输入正确的模板ID(1-8192)"
	}
	if sp.Act == 1 && (len(strings.TrimSpace(sp.Name)) < 1 || len(strings.TrimSpace(sp.Name)) > 33) {
		return "请输入正确的模板名(1-33)"
	}
	if (sp.Act == 1 || sp.Act == 2) && !strings.EqualFold(sp.PonType, "gpon") && !strings.EqualFold(sp.PonType, "epon") {
		return "请输入正确的PON类型(gpon,epon)"
	}
	if sp.Act == 1 {
		// 网口/电话口数量解析并收敛到合法区间（非数字按 0）。
		_ = srvPortCount(sp.Pots, 32)
		_ = srvPortCount(sp.Eths, 24)
		// 业务 VLAN：必须非空、每条 1-4094、最多 8 条（老实现 `index > 7 break` 的硬件上限）。
		vlans := srvVlanList(sp.Vlan)
		if len(vlans) == 0 {
			return "请输入业务VLAN列表(1-4094)"
		}
		if len(vlans) > 8 {
			return "业务VLAN数量最多8个"
		}
		for _, vlan := range vlans {
			if !srvVlanIsValid(vlan) {
				return "请输入正确的业务VLAN(1-4094)"
			}
		}
	}
	return ""
}

// srvPortCount 把电话口/网口数量字符串解析为闭区间内的整数（非数字/空按 0，越界收敛）。
func srvPortCount(value string, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return clampSrvPortCount(n, max)
}

// runSrvProfileWrite 执行一条业务模板写命令并做失败提取（新增/删除共用）。
func (m *Module) runSrvProfileWrite(client *oltDevice, cmd, prefix string) string {
	domsg, err := client.runCommand(cmd)
	if err != nil {
		return prefix + oltOperationError(err).Error()
	}
	if failure, ok := extractOLTFailure(domsg); ok {
		return prefix + strings.TrimSpace(failure)
	}
	return ""
}

// applySrvProfileCommand 在已连接的配置会话上执行新增（Act=1）或删除（Act=2）的整段命令序列。
// 返回非空字符串表示失败消息；空串表示成功。
func (m *Module) applySrvProfileCommand(client *oltDevice, sp OntSrvProfile) string {
	if sp.Act == 2 {
		incmd := fmt.Sprintf("undo ont-srvprofile %s profile-id %d", strings.TrimSpace(sp.PonType), sp.Id)
		return m.runSrvProfileWrite(client, incmd, "删除ONT业务模板失败;")
	}
	// Act=1：新建模板（老实现命令逐字）。
	incmd := fmt.Sprintf("ont-srvprofile %s profile-id %d profile-name %s",
		strings.TrimSpace(sp.PonType), sp.Id, strings.TrimSpace(sp.Name))
	if rv := m.runSrvProfileWrite(client, incmd, "新增ONT业务模板失败;"); rv != "" {
		return rv
	}
	return m.applySrvProfilePorts(client, sp)
}

// applySrvProfilePorts 下发电话口/网口数量与逐条业务 VLAN 映射（在已创建的模板视图内），随后 commit。
//
// 与老实现一致的数量拼装规则（修正老壳表单错位后，语义为 Pots=电话口、Eths=网口）：
//   - 两者都非 0：`ont-port pots <N> eth <M>`；
//   - 仅电话口：`ont-port pots <N> eth adaptive`；仅网口：`ont-port pots adaptive eth <M>`；
//   - 都 0：`ont-port pots adaptive eth adaptive`。
//
// 业务 VLAN 逐条 `port vlan eth <n> <vlan>`（eth 从 1 递增，老实现 `index+1`；每条一个网口透传一个
// VLAN）。老实现只处理 `/` 第一段且最多 8 条；这里把逗号/换行/斜杠全部拆成独立 VLAN（srvVlanList），
// 每 VLAN 一条命令，仍保留 8 条上限。子命令失败不中断（与线路模板同口径，失败回显由调用方带出）。
func (m *Module) applySrvProfilePorts(client *oltDevice, sp OntSrvProfile) string {
	nPots := srvPortCount(sp.Pots, 32)
	nEths := srvPortCount(sp.Eths, 24)
	var portCmd string
	switch {
	case nPots > 0 && nEths > 0:
		portCmd = fmt.Sprintf("ont-port pots %d eth %d", nPots, nEths)
	case nPots > 0:
		portCmd = fmt.Sprintf("ont-port pots %d eth adaptive", nPots)
	case nEths > 0:
		portCmd = fmt.Sprintf("ont-port pots adaptive eth %d", nEths)
	default:
		portCmd = "ont-port pots adaptive eth adaptive"
	}
	if rv := m.runSrvProfileWrite(client, portCmd, "配置电话口和网口数量失败;"); rv != "" {
		return rv
	}
	var firstFail string
	for index, vlan := range srvVlanList(sp.Vlan) {
		vlanCmd := fmt.Sprintf("port vlan eth %d %s", index+1, vlan)
		m.runSrvProfileSubcmd(client, vlanCmd, fmt.Sprintf("业务VLAN[%d]", index+1), &firstFail)
	}
	if _, err := client.runCommand("commit"); err != nil {
		return "提交ONT业务模板失败;" + oltOperationError(err).Error()
	}
	if firstFail != "" {
		return "ONT业务模板VLAN配置未全部生效;" + firstFail
	}
	return ""
}

// runSrvProfileSubcmd 执行一条业务模板子命令（port vlan eth），并把首个可见失败回显收进 firstFail。
// 与 runLineProfileSubcmd 同一口径：统一走 runCommand（等 `(config)` 提示符），失败不中断后续命令。
func (m *Module) runSrvProfileSubcmd(client *oltDevice, cmd, label string, firstFail *string) {
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

// handleSrvProfile 处理 ONT 业务模板读、写请求（老实现 handleSrvProfile；Act 在 saveData JSON 的 Act 字段）。
func (m *Module) handleSrvProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	tcp, err := decodeConnectParameters(r.FormValue("connData"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	var sp OntSrvProfile
	if err := json.Unmarshal([]byte(r.FormValue("saveData")), &sp); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}

	switch a := sp.Act; {
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
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseOLTSrvProfiles(analysestr)})
	case a == 1 || a == 2:
		if problem := validateSrvProfileWrite(sp); problem != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: problem})
			return
		}
		client, err := m.connectDevice(tcp)
		if err != nil {
			respondConnectionFailed(w, err)
			return
		}
		defer client.Close()
		if message := m.applySrvProfileCommand(client, sp); message != "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
			return
		}
		// 写命令完成后，直接在「同一会话」上采集并持久化已保存配置（与 DBA/VLAN/终端用户同一提速路径）。
		if _, refreshErr := m.persistSavedConfiguration(client, tcp.IP); refreshErr != nil {
			m.logf("ONT 业务模板操作后刷新配置失败(%s/%d): %v", tcp.IP, sp.Act, refreshErr)
		}
		// 写操作日志（老实现没有，按本模块同口径补齐）。
		m.writeSrvProfileLog(tcp.IP, sp, r.RemoteAddr)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
	}
}

// srvProfileActionLabel 把 Act 转成操作日志的 szAct 文案。
func srvProfileActionLabel(act int) string {
	switch act {
	case 1:
		return "新增"
	case 2:
		return "删除"
	default:
		return fmt.Sprintf("Act=%d", act)
	}
}

// writeSrvProfileLog 写一条 ONT 业务模板操作日志（老实现未写，本模块补齐）。
func (m *Module) writeSrvProfileLog(ip string, sp OntSrvProfile, remoteAddr string) {
	text := fmt.Sprintf("Id=%d Name=%s PonType=%s Pots=%s Eths=%s Vlan=%s",
		sp.Id, strings.TrimSpace(sp.Name), strings.TrimSpace(sp.PonType),
		sp.Pots, sp.Eths, sp.Vlan)
	item := OltOperationLog{
		OltName:     strings.TrimSpace(ip),
		Menu:        srvProfileLogMenu,
		Act:         srvProfileActionLabel(sp.Act),
		OperationIP: remoteAddr,
		Text:        text,
		CreatTime:   time.Now().Format(oltTimeLayout),
	}
	if err := m.store.InsertOltLog(item); err != nil {
		m.logf("写 OLT 操作日志失败(%s/%s): %v", ip, srvProfileActionLabel(sp.Act), err)
	}
}
