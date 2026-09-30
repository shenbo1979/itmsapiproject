// 由 tools/port_switch_batch2a.py 从 switchapi/handler_port_detail.go 整体搬运（端口详情与操作）
package switchmod

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 端口详情模块：聚合端口基础信息、实时统计、趋势和操作入口。
const (
	switchSingleInterfaceReadTimeout = 1200 * time.Millisecond
	switchPortReadCommandDelay       = 250 * time.Millisecond
	maxSwitchPortDescriptionRunes    = 30
)

func (m *Module) handleSwitchPortDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}

	nAct, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct: " + err.Error()})
		return
	}

	saveData := strings.TrimSpace(r.FormValue("saveData"))
	if saveData == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "传参不能为空"})
		return
	}

	// var encryptedReq SwitchRequest
	// if err := json.Unmarshal([]byte(saveData), &encryptedReq); err == nil {
	// 	log.Printf("handleSwitchPortDetail encrypted credentials: username=%q password=%q", encryptedReq.Username, encryptedReq.Password)
	// }

	var req SwitchRequest
	if err := m.decodeEncryptedSwitchRequest(saveData, &req); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid saveData: " + err.Error()})
		return
	}
	req.Interface = normalizeSwitchPortName(req.Interface)
	if req.Interface == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "szInterface is required"})
		return
	}
	if strings.TrimSpace(req.Hostip) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "szHostip is required"})
		return
	}

	if err := normalizeSwitchPortDetailRequest(&req); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
		return
	}

	switch nAct {
	case 0:
		detail, err := m.buildSwitchPortDetail(req)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		detail.TerminalMAC = make([]TerminalMACInfo, 0)
		if terminalMAC, macErr := m.collectSwitchPortTerminalMAC(req); macErr == nil {
			if terminalMAC != nil {
				detail.TerminalMAC = terminalMAC
			}
		} else {
			detail.TerminalMACError = macErr.Error()
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: detail})
	case 1, 2, 3:
		var actionReq SwitchPortDetailRequest
		if err := json.Unmarshal([]byte(saveData), &actionReq); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid saveData: " + err.Error()})
			return
		}
		if err := validateSwitchPortActionRequest(nAct, actionReq); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		if err := m.handleSwitchPortAction(req, nAct, actionReq); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

// collectSwitchPortTerminalMAC reuses the terminal-MAC query implementation
// used by getTerminalMAC, but returns the parsed result for port-detail APIs.
func (m *Module) collectSwitchPortTerminalMAC(req SwitchRequest) ([]TerminalMACInfo, error) {
	morePatterns := []string{"---- More ----", "--More--", "More"}
	switch GetSwitchMaker(req.Maker) {
	case "huawei", "h3c", "ruijie":
		switch req.Type {
		case 2:
			client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
			if err := client.SwitchConnect(req.Username, req.Password); err != nil {
				return nil, err
			}
			defer client.Conn.Close()
			_, result, errMsg, ok := queryHuaweiTerminalMACByTelnet(client, req, morePatterns)
			if !ok {
				return nil, fmt.Errorf("%s", firstNonEmpty(errMsg, "获取终端MAC失败"))
			}
			return result, nil
		case 3:
			client, err := m.connectSwitchSSHClient(req)
			if err != nil {
				return nil, err
			}
			defer client.Close()
			_, result, errMsg, ok := queryHuaweiTerminalMACBySSH(client, req)
			if !ok {
				return nil, fmt.Errorf("%s", firstNonEmpty(errMsg, "获取终端MAC失败"))
			}
			return result, nil
		}
	case "centec":
		switch req.Type {
		case 1:
			_, result, errMsg, ok := m.queryCentecTerminalMACByRPC(req)
			if !ok {
				return nil, fmt.Errorf("%s", firstNonEmpty(errMsg, "获取终端MAC失败"))
			}
			return result, nil
		case 2:
			client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
			if err := client.SwitchConnect(req.Username, req.Password); err != nil {
				return nil, err
			}
			defer client.Conn.Close()
			_, result, errMsg, ok := queryCentecTerminalMACByTelnet(client, req, morePatterns)
			if !ok {
				return nil, fmt.Errorf("%s", firstNonEmpty(errMsg, "获取终端MAC失败"))
			}
			return result, nil
		case 3:
			client, err := m.connectSwitchSSHClient(req)
			if err != nil {
				return nil, err
			}
			defer client.Close()
			_, result, errMsg, ok := queryCentecTerminalMACBySSH(client, req)
			if !ok {
				return nil, fmt.Errorf("%s", firstNonEmpty(errMsg, "获取终端MAC失败"))
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("当前不支持获取终端MAC")
}

func normalizeSwitchPortDetailRequest(req *SwitchRequest) error {
	if req == nil {
		return fmt.Errorf("invalid request")
	}
	if strings.TrimSpace(req.Hostip) == "" {
		return fmt.Errorf("szHostip is required")
	}
	if strings.TrimSpace(req.Maker) == "" || GetSwitchMaker(req.Maker) == "" {
		return fmt.Errorf("szMaker is required")
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		return fmt.Errorf("saveData 中缺少登录账号或密码")
	}
	if req.Type == 0 {
		req.Type = 3
	}
	switch req.Type {
	case 1:
		if req.Port == 0 {
			req.Port = 80
		}
		if req.Format == "" {
			req.Format = "text"
		}
	case 2:
		if req.Port == 0 {
			req.Port = 23
		}
	case 3:
		if req.Port == 0 {
			req.Port = 22
		}
	default:
		return fmt.Errorf("不支持的连接方式: %d", req.Type)
	}
	return nil
}

func validateSwitchPortActionRequest(act int, actionReq SwitchPortDetailRequest) error {
	switch act {
	case 1:
		if actionReq.PortEnabled == nil {
			return fmt.Errorf("portEnabled 为必填字段")
		}
	case 2:
		if strings.TrimSpace(actionReq.PortDescription) == "" {
			return fmt.Errorf("portDescription 为必填字段")
		}
		return validateSwitchPortDescription(actionReq.PortDescription)
	}
	return nil
}

func validateSwitchPortDescription(description string) error {
	if utf8.RuneCountInString(strings.TrimSpace(description)) > maxSwitchPortDescriptionRunes {
		return fmt.Errorf("端口描述不能超过 %d 个字符", maxSwitchPortDescriptionRunes)
	}
	return nil
}

// 端口详情构建模块：优先走快路径，失败后再回退到设备级采集和端口级补采。
func (m *Module) buildSwitchPortDetail(req SwitchRequest) (SwitchPortDetail, error) {
	if detail, ok, err := m.buildSwitchPortDetailFast(req); ok && err == nil {
		return detail, nil
	}

	device, err := m.queryDeviceInformation(req)
	if err != nil {
		return SwitchPortDetail{}, fmt.Errorf("实时采集端口信息失败: %v", err)
	}
	info, ok := findSwitchInterfaceInfo(device.Interfaces, req.Interface)
	if !ok {
		return SwitchPortDetail{}, fmt.Errorf("未找到端口信息")
	}

	stats, speed, singleOutput, err := m.collectSwitchPortRealtimeDataWithOutput(req, info)
	if err != nil {
		return SwitchPortDetail{}, fmt.Errorf("实时采集端口统计失败: %v", err)
	}
	switch GetSwitchMaker(req.Maker) {
	case "h3c":
		mergeH3CInterfaceDetailInfo(&info, req.Interface, singleOutput)
	case "ruijie":
		mergeRuijieInterfaceDetailInfo(&info, singleOutput)
	}
	trend, err := m.loadSwitchPortTrafficTrend(req.Hostip, req.Interface)
	if err != nil {
		return SwitchPortDetail{}, fmt.Errorf("读取流量采集数据失败: %v", err)
	}

	vlanList, err := m.buildSwitchPortVLANList(req, info)
	if err != nil {
		return SwitchPortDetail{}, err
	}

	return SwitchPortDetail{
		Hostip: req.Hostip,
		Port:   info.Port,
		BasicInfo: SwitchPortDetailBasicInfo{
			PortName:      info.Port,
			PortStatus:    formatSwitchPortStatus(info.Status),
			PortSpeed:     speed,
			PortBandWidth: "",
		},
		PacketStats:        stats,
		OpticalInfo:        buildSwitchPortOpticalInfo(info),
		TrafficTrend:       trend,
		VLANList:           vlanList,
		CurrentDescription: firstNonEmpty(info.Description, info.VlanMapping.PortDescription),
		LinkType:           info.VlanMapping.LinkType,
		PVID:               info.VlanMapping.PVID,
		TrunkVLANList:      info.VlanMapping.TrunkVLANList,
	}, nil
}

// 端口实时补采模块：尽量复用单端口输出，同时提取速率和报文统计。
func (m *Module) collectSwitchPortRealtimeData(req SwitchRequest, info SwitchInterfaceInfo) (SwitchPortPacketStats, string, error) {
	stats, speed, _, err := m.collectSwitchPortRealtimeDataWithOutput(req, info)
	return stats, speed, err
}

func (m *Module) collectSwitchPortRealtimeDataWithOutput(req SwitchRequest, info SwitchInterfaceInfo) (SwitchPortPacketStats, string, string, error) {
	speed := formatSwitchPortSpeed(info.Speed)
	singleOutput := ""

	if output, err := m.collectSwitchSingleInterfaceOutput(req); err == nil {
		singleOutput = output
		if speed == "" {
			if parsedSpeed := parseSwitchPortSpeedFromInterfaceOutput(output); parsedSpeed != "" {
				speed = formatSwitchPortSpeed(parsedSpeed)
			}
		}
		if hasSwitchPortPacketStatsOutput(output) {
			if stats, parseErr := parseSwitchPortPacketStatsFromOutput(output, req.Interface); parseErr == nil {
				return stats, speed, singleOutput, nil
			}
		}
	}

	output, err := m.collectSwitchInterfaceOutput(req)
	if err != nil {
		return SwitchPortPacketStats{}, "", singleOutput, err
	}
	stats, ok := findSwitchPortPacketStatsFromCollectionOutput(output, req.Interface)
	if !ok {
		return SwitchPortPacketStats{}, "", singleOutput, fmt.Errorf("未找到端口实时统计信息")
	}
	if speed == "" {
		speed = m.collectRealtimeSwitchPortSpeed(req, info)
	}
	return stats, speed, singleOutput, nil
}

func mergeH3CInterfaceDetailInfo(info *SwitchInterfaceInfo, requestedPort, output string) {
	if strings.TrimSpace(output) == "" {
		return
	}
	parsed, ok := findSwitchInterfaceInfoFromMap(ParseH3CInterfaceDetailInfo(output), requestedPort)
	if !ok {
		return
	}
	if parsed.Description != "" {
		info.Description = parsed.Description
	}
	if parsed.Status != "" {
		info.Status = parsed.Status
	}
	if parsed.VlanMapping.Port != "" {
		info.VlanMapping = parsed.VlanMapping
	}
}

func mergeRuijieInterfaceDetailInfo(info *SwitchInterfaceInfo, output string) {
	if info == nil {
		return
	}
	description := parseSwitchInterfaceDescription(output)
	if description == "" {
		return
	}
	info.Description = description
	info.VlanMapping.PortDescription = description
}

func parseSwitchInterfaceDescription(output string) string {
	output = html.UnescapeString(output)
	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		line := strings.TrimSpace(raw)
		if len(line) < len("Description:") || !strings.EqualFold(line[:len("Description:")], "Description:") {
			continue
		}
		return strings.TrimSpace(line[len("Description:"):])
	}
	return ""
}

func findSwitchInterfaceInfoFromMap(infos map[string]SwitchInterfaceInfo, port string) (SwitchInterfaceInfo, bool) {
	want := normalizeH3CPortName(port)
	for key, info := range infos {
		if normalizeH3CPortName(key) == want {
			return info, true
		}
	}
	return SwitchInterfaceInfo{}, false
}

// 华为单端口快路径：直接查询目标端口，并结合缓存回填光模块和 VLAN 信息。
func (m *Module) buildSwitchPortDetailFast(req SwitchRequest) (SwitchPortDetail, bool, error) {
	if GetSwitchMaker(req.Maker) != "huawei" || req.Type != 3 {
		return SwitchPortDetail{}, false, nil
	}

	var cachedInfo SwitchInterfaceInfo
	cachedFound := false
	if cached, err := m.loadDeviceInformation(req.Hostip); err == nil {
		if item, ok := findSwitchInterfaceInfo(cached.Interfaces, req.Interface); ok {
			cachedInfo = item
			cachedFound = true
		}
	}

	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return SwitchPortDetail{}, false, err
	}
	defer client.Close()

	commands := []string{
		fmt.Sprintf("display interface %s", req.Interface),
	}
	if !hasSwitchInterfaceOpticalInfo(cachedInfo) {
		commands = append(commands, fmt.Sprintf("display interface %s transceiver verbose", req.Interface))
	}

	output, err := client.RunInteractiveWithOptions(commands, InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(GetSwitchMaker(req.Maker)),
		PromptSuffixes: []string{">", "#", "]"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   switchPortReadCommandDelay,
		ReadTimeout:    switchSingleInterfaceReadTimeout,
		AutoExit:       true,
	})
	if err != nil {
		return SwitchPortDetail{}, false, err
	}

	info, err := m.parseHuaweiSinglePortDetail(output, req.Interface)
	if err != nil {
		return SwitchPortDetail{}, false, err
	}
	if cachedFound {
		info = mergeCachedSwitchInterfaceInfo(info, cachedInfo)
	}
	stats, err := parseSwitchPortPacketStatsFromOutput(output, req.Interface)
	if err != nil {
		return SwitchPortDetail{}, false, err
	}

	trend, err := m.loadSwitchPortTrafficTrend(req.Hostip, req.Interface)
	if err != nil {
		return SwitchPortDetail{}, true, fmt.Errorf("读取流量采集数据失败: %v", err)
	}

	vlanList, err := m.buildSwitchPortVLANList(req, info)
	if err != nil {
		return SwitchPortDetail{}, true, err
	}

	detail := SwitchPortDetail{
		Hostip: req.Hostip,
		Port:   info.Port,
		BasicInfo: SwitchPortDetailBasicInfo{
			PortName:      info.Port,
			PortStatus:    formatSwitchPortStatus(info.Status),
			PortSpeed:     formatSwitchPortSpeed(info.Speed),
			PortBandWidth: "",
		},
		PacketStats:        stats,
		OpticalInfo:        buildSwitchPortOpticalInfo(info),
		TrafficTrend:       trend,
		VLANList:           vlanList,
		CurrentDescription: firstNonEmpty(info.Description, info.VlanMapping.PortDescription),
		LinkType:           info.VlanMapping.LinkType,
		PVID:               info.VlanMapping.PVID,
		TrunkVLANList:      info.VlanMapping.TrunkVLANList,
	}
	return detail, true, nil
}

func mergeCachedSwitchInterfaceInfo(info, cached SwitchInterfaceInfo) SwitchInterfaceInfo {
	if info.VlanMapping.Port == "" {
		info.VlanMapping = cached.VlanMapping
	}
	if strings.TrimSpace(info.Description) == "" {
		info.Description = cached.Description
	}
	if strings.TrimSpace(info.ModuleType) == "" {
		info.ModuleType = cached.ModuleType
	}
	if strings.TrimSpace(info.Wavelength) == "" {
		info.Wavelength = cached.Wavelength
	}
	if info.Temperature == 0 {
		info.Temperature = cached.Temperature
	}
	if info.Voltage == 0 {
		info.Voltage = cached.Voltage
	}
	if info.Current == 0 {
		info.Current = cached.Current
	}
	if info.TxPower == 0 {
		info.TxPower = cached.TxPower
	}
	if info.RxPower == 0 {
		info.RxPower = cached.RxPower
	}
	if strings.TrimSpace(info.TransceiverDiagnostic) == "" || strings.EqualFold(info.TransceiverDiagnostic, "unknown") {
		info.TransceiverDiagnostic = cached.TransceiverDiagnostic
	}
	return info
}

func buildSwitchPortOpticalInfo(info SwitchInterfaceInfo) SwitchPortOpticalInfo {
	return SwitchPortOpticalInfo{
		TransceiverDiagnostic: info.TransceiverDiagnostic,
		ModuleType:            info.ModuleType,
		Wavelength:            info.Wavelength,
		Temperature:           info.Temperature,
		Voltage:               info.Voltage,
		Current:               info.Current,
		RxPower:               info.RxPower,
		TxPower:               info.TxPower,
	}
}

func hasSwitchInterfaceOpticalInfo(info SwitchInterfaceInfo) bool {
	return strings.TrimSpace(info.ModuleType) != "" ||
		strings.TrimSpace(info.Wavelength) != "" ||
		info.Temperature != 0 ||
		info.Voltage != 0 ||
		info.Current != 0 ||
		info.TxPower != 0 ||
		info.RxPower != 0 ||
		(strings.TrimSpace(info.TransceiverDiagnostic) != "" && !strings.EqualFold(info.TransceiverDiagnostic, "unknown"))
}

func (m *Module) parseHuaweiSinglePortDetail(output, port string) (SwitchInterfaceInfo, error) {
	port = normalizeSwitchPortName(port)
	info := SwitchInterfaceInfo{Port: port}

	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, strings.ToLower(port)+" current state"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				state := strings.TrimSpace(line[idx+1:])
				if end := strings.Index(state, "("); end >= 0 {
					state = strings.TrimSpace(state[:end])
				}
				info.Status = strings.ToLower(state)
			}
		case strings.HasPrefix(lower, "description:"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				info.Description = strings.TrimSpace(line[idx+1:])
			}
		}
	}

	if speed := parseSwitchPortSpeedFromInterfaceOutput(output); speed != "" {
		info.Speed = speed
	}
	if transceiver, ok := ParseHuaweiTransceiverInfo(output)[port]; ok {
		info.TransceiverDiagnostic = transceiver.TransceiverDiagnostic
		info.ModuleType = transceiver.ModuleType
		info.Wavelength = transceiver.Wavelength
		info.Temperature = transceiver.Temperature
		info.Voltage = transceiver.Voltage
		info.Current = transceiver.Current
		info.TxPower = transceiver.TxPower
		info.RxPower = transceiver.RxPower
	}
	if vlan, ok := findSwitchPortVlan(ParseHuaweiPortVlan(output), port); ok {
		info.VlanMapping = vlan
	}
	if info.Status == "" {
		return SwitchInterfaceInfo{}, fmt.Errorf("未找到端口信息")
	}
	return info, nil
}

func parseSwitchPortPacketStatsFromOutput(output, port string) (SwitchPortPacketStats, error) {
	for _, item := range ParseSwitchCollectionInterfaceStats(output) {
		if !switchPortNamesEqual(item.Interface, port) {
			continue
		}
		return SwitchPortPacketStats{
			InBroadcast:  item.InBroadcast,
			OutBroadcast: item.OutBroadcast,
			InCRC:        item.InCRC,
			InError:      item.InError,
			OutError:     item.OutError,
			InDiscard:    item.InDiscard,
			OutDiscard:   item.OutDiscard,
			InPause:      item.InPause,
			OutPause:     item.OutPause,
			InBytes:      item.InBytes,
			OutBytes:     item.OutBytes,
			InRate:       item.InRate,
			OutRate:      item.OutRate,
		}, nil
	}
	return SwitchPortPacketStats{}, fmt.Errorf("未找到端口实时统计信息")
}

func findSwitchInterfaceInfo(interfaces []SwitchInterfaceInfo, port string) (SwitchInterfaceInfo, bool) {
	port = normalizeSwitchPortName(port)
	for _, item := range interfaces {
		if switchPortNamesEqual(item.Port, port) {
			return item, true
		}
	}
	return SwitchInterfaceInfo{}, false
}

func switchPortNamesEqual(left, right string) bool {
	left = normalizeSwitchPortName(left)
	right = normalizeSwitchPortName(right)
	if left == right {
		return true
	}
	leftH3C := normalizeH3CPortName(left)
	rightH3C := normalizeH3CPortName(right)
	return leftH3C != "" && leftH3C == rightH3C
}

func (m *Module) collectRealtimeSwitchPortSpeed(req SwitchRequest, info SwitchInterfaceInfo) string {
	if speed := formatSwitchPortSpeed(info.Speed); speed != "" {
		return speed
	}
	output, err := m.collectSwitchSingleInterfaceOutput(req)
	if err != nil {
		return formatSwitchPortSpeed(info.Speed)
	}
	if speed := parseSwitchPortSpeedFromInterfaceOutput(output); speed != "" {
		return formatSwitchPortSpeed(speed)
	}
	return formatSwitchPortSpeed(info.Speed)
}

func formatSwitchPortStatus(raw string) string {
	return strings.TrimSpace(raw)
}

// 单端口采集模块：屏蔽不同连接方式下的单端口命令执行细节。
func (m *Module) collectSwitchSingleInterfaceOutput(req SwitchRequest) (string, error) {
	maker := GetSwitchMaker(req.Maker)
	command := switchSingleInterfaceCommandForMaker(maker, req.Interface)
	if command == "" {
		return "", fmt.Errorf("single interface collection is not supported")
	}
	if req.Format == "" {
		req.Format = "text"
	}

	switch req.Type {
	case 1:
		return m.collectSwitchSingleInterfaceOutputByRPC(req, command)
	case 2:
		return collectSwitchSingleInterfaceOutputByTelnet(req, command)
	case 3:
		return m.collectSwitchSingleInterfaceOutputBySSH(req, command)
	default:
		return "", fmt.Errorf("未识别的连接方式")
	}
}

func switchSingleInterfaceCommandForMaker(maker, port string) string {
	port = normalizeSwitchPortName(port)
	switch maker {
	case "huawei", "h3c":
		return fmt.Sprintf("display interface %s", port)
	case "centec", "ruijie":
		return fmt.Sprintf("show interface %s", port)
	default:
		return ""
	}
}

func (m *Module) collectSwitchSingleInterfaceOutputByRPC(req SwitchRequest, command string) (string, error) {
	resp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{command}, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%s", resp.Error.Message)
	}
	return extractRPCTextOutput(resp.Result)
}

func collectSwitchSingleInterfaceOutputByTelnet(req SwitchRequest, command string) (string, error) {
	client := &TelnetClient{
		Host:    req.Hostip,
		Port:    req.Port,
		Maker:   req.Maker,
		Timeout: 30 * time.Second,
	}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		return "", err
	}
	defer client.Conn.Close()
	return runTelnetCommandWithPaging(client, command, []string{"---- More ----", "--More--", "More"}, 3*time.Second)
}

func (m *Module) collectSwitchSingleInterfaceOutputBySSH(req SwitchRequest, command string) (string, error) {
	kexitems := []string{
		"mlkem768x25519-sha256",
		"curve25519-sha256",
		"curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256",
		"ecdh-sha2-nistp384",
		"ecdh-sha2-nistp521",
		"diffie-hellman-group-exchange-sha256",
		"diffie-hellman-group14-sha256",
		"diffie-hellman-group14-sha1",
	}
	client, err := m.ConnectWithLoginInfo(req.Hostip, req.Port, req.Username, req.Password, kexitems, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return "", err
	}
	defer client.Close()

	return client.RunInteractiveWithOptions([]string{command}, InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(GetSwitchMaker(req.Maker)),
		PromptSuffixes: []string{">", "#", "]"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		ExitCommand:    switchInteractiveExitCommandForMaker(req.Maker),
		CommandDelay:   switchPortReadCommandDelay,
		ReadTimeout:    switchSingleInterfaceReadTimeout,
		AutoExit:       true,
	})
}

func parseSwitchPortSpeedFromInterfaceOutput(output string) string {
	normalized := normalizeCLIOutput(output)
	lines := strings.Split(normalized, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if line == "" {
			continue
		}

		switch {
		case strings.Contains(lower, "speed :"):
			if idx := strings.Index(line, ":"); idx >= 0 {
				return cleanSwitchPortSpeedValue(line[idx+1:])
			}
		case strings.Contains(lower, "speed:"):
			if idx := strings.Index(lower, "speed:"); idx >= 0 {
				return cleanSwitchPortSpeedValue(line[idx+len("speed:"):])
			}
		case strings.Contains(lower, "port mode,") && strings.Contains(lower, "speed"):
			if idx := strings.Index(lower, "speed"); idx >= 0 {
				return cleanSwitchPortSpeedValue(line[idx+len("speed"):])
			}
		case strings.HasPrefix(lower, "speed"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return cleanSwitchPortSpeedValue(fields[len(fields)-1])
			}
		}
	}
	return ""
}

func cleanSwitchPortSpeedValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, ","); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, ":"))
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "="))
	return strings.TrimSpace(raw)
}

func formatSwitchPortSpeed(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	if speed, err := strconv.Atoi(value); err == nil {
		if speed >= 1000 && speed%1000 == 0 {
			return fmt.Sprintf("%dG", speed/1000)
		}
		return fmt.Sprintf("%dM", speed)
	}

	upper := strings.ToUpper(value)
	switch {
	case strings.HasSuffix(upper, "GBPS"):
		return strings.TrimSpace(value[:len(value)-4]) + "G"
	case strings.HasSuffix(upper, "MBPS"):
		return strings.TrimSpace(value[:len(value)-4]) + "M"
	default:
		return value
	}
}

// 实时统计模块：优先解析单端口输出，必要时回退到全量接口采集输出。
func (m *Module) collectRealtimeSwitchPortPacketStats(req SwitchRequest) (SwitchPortPacketStats, error) {
	if output, err := m.collectSwitchSingleInterfaceOutput(req); err == nil {
		if hasSwitchPortPacketStatsOutput(output) {
			if stats, parseErr := parseSwitchPortPacketStatsFromOutput(output, req.Interface); parseErr == nil {
				return stats, nil
			}
		}
	}

	output, err := m.collectSwitchInterfaceOutput(req)
	if err != nil {
		return SwitchPortPacketStats{}, err
	}
	if stats, ok := findSwitchPortPacketStatsFromCollectionOutput(output, req.Interface); ok {
		return stats, nil
	}
	return SwitchPortPacketStats{}, fmt.Errorf("未找到端口实时统计信息")
}

func findSwitchPortPacketStatsFromCollectionOutput(output, port string) (SwitchPortPacketStats, bool) {
	statsList := ParseSwitchCollectionInterfaceStats(output)
	for _, item := range statsList {
		if !switchPortNamesEqual(item.Interface, port) {
			continue
		}
		return SwitchPortPacketStats{
			InBroadcast:  item.InBroadcast,
			OutBroadcast: item.OutBroadcast,
			InCRC:        item.InCRC,
			InError:      item.InError,
			OutError:     item.OutError,
			InDiscard:    item.InDiscard,
			OutDiscard:   item.OutDiscard,
			InPause:      item.InPause,
			OutPause:     item.OutPause,
			InBytes:      item.InBytes,
			OutBytes:     item.OutBytes,
			InRate:       item.InRate,
			OutRate:      item.OutRate,
		}, true
	}
	return SwitchPortPacketStats{}, false
}

func hasSwitchPortPacketStatsOutput(output string) bool {
	hasInput := false
	hasOutput := false
	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		if isSwitchCollectionInputLine(raw) {
			hasInput = true
		}
		if isSwitchCollectionOutputLine(raw) {
			hasOutput = true
		}
	}
	return hasInput && hasOutput
}

// 趋势模块：从采集库读取指定端口的最近一小时、一天和七天流量趋势。
func (m *Module) loadSwitchPortTrafficTrend(hostip, port string) (SwitchPortTrafficTrend, error) {
	trend := SwitchPortTrafficTrend{
		OneHour:   []SwitchPortTrafficPoint{},
		OneDay:    []SwitchPortTrafficPoint{},
		SevenDays: []SwitchPortTrafficPoint{},
	}

	m.collectionFileMu.Lock()
	defer m.collectionFileMu.Unlock()

	retentionPeriod := m.loadCurrentSwitchCollectionRetentionPeriod(0)
	now := time.Now()
	if err := m.pruneSwitchCollectionHostStorage(hostip, retentionPeriod, now); err != nil {
		return trend, err
	}

	latest, existed, err := m.store.loadLatestSwitchCollectionSnapshotTimeFromDB(hostip)
	if err != nil {
		return trend, err
	}
	if !existed {
		return trend, nil
	}

	targetPort := normalizeSwitchPortName(port)
	dbItems, err := m.store.loadSwitchCollectionInterfaceStatsFromDB(hostip, targetPort, latest.Add(-7*24*time.Hour), latest)
	if err != nil {
		return trend, err
	}
	type trafficRecord struct {
		collectedAt time.Time
		item        SwitchPortTrafficPoint
	}
	records := make([]trafficRecord, 0)
	for _, item := range dbItems {
		collectedAt, err := parseSwitchCollectionTimestamp(item.CollectedAt)
		if err != nil {
			continue
		}
		point := SwitchPortTrafficPoint{
			CollectedAt:  item.CollectedAt,
			InBroadcast:  item.InBroadcast,
			OutBroadcast: item.OutBroadcast,
			InCRC:        item.InCRC,
			InError:      item.InError,
			OutError:     item.OutError,
			InDiscard:    item.InDiscard,
			OutDiscard:   item.OutDiscard,
			InPause:      item.InPause,
			OutPause:     item.OutPause,
			InBytes:      item.InBytes,
			OutBytes:     item.OutBytes,
			InRate:       item.InRate,
			OutRate:      item.OutRate,
		}
		records = append(records, trafficRecord{collectedAt: collectedAt, item: point})
		if item.InRate > trend.InRateMax {
			trend.InRateMax = item.InRate
		}
		if item.OutRate > trend.OutRateMax {
			trend.OutRateMax = item.OutRate
		}
	}

	oneHourStart := latest.Add(-time.Hour)
	oneDayStart := latest.Add(-24 * time.Hour)
	sevenDaysStart := latest.Add(-7 * 24 * time.Hour)
	for _, record := range records {
		point := record.item
		point.CollectedAt = record.collectedAt.Format("2006-01-02 15:04:05")
		if !record.collectedAt.Before(oneHourStart) {
			trend.OneHour = append(trend.OneHour, point)
		}
		if !record.collectedAt.Before(oneDayStart) {
			trend.OneDay = append(trend.OneDay, point)
		}
		if !record.collectedAt.Before(sevenDaysStart) {
			trend.SevenDays = append(trend.SevenDays, point)
		}
	}

	sortTrafficPoints(trend.OneHour)
	sortTrafficPoints(trend.OneDay)
	sortTrafficPoints(trend.SevenDays)
	return trend, nil
}

func sortTrafficPoints(points []SwitchPortTrafficPoint) {
	sort.Slice(points, func(i, j int) bool {
		return points[i].CollectedAt < points[j].CollectedAt
	})
}

func (m *Module) buildSwitchPortVLANList(req SwitchRequest, info SwitchInterfaceInfo) ([]SwitchPortVLANItem, error) {
	if GetSwitchMaker(req.Maker) != "centec" || req.Type != 1 {
		return buildSwitchPortVLANListFromInterfaceInfo(info), nil
	}

	if req.Format == "" {
		req.Format = "text"
	}
	cmds := []string{"show ip interface brief"}
	resp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return nil, fmt.Errorf("rpc error: %v", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	infos := []map[string]interface{}{}
	if err := json.Unmarshal(resp.Result, &infos); err != nil {
		return nil, fmt.Errorf("rpc error: %v", err)
	}

	vlanifMap := make(map[string]InterfaceVlanif)
	for _, item := range infos {
		sd, _ := item["sourceDetails"].(string)
		for _, line := range strings.Split(sd, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(line), "vlan") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			name := strings.TrimSpace(fields[0])
			detailResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{fmt.Sprintf("show interface %s", name)}, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil || detailResp.Error != nil {
				continue
			}
			detailInfos := []map[string]interface{}{}
			if err := json.Unmarshal(detailResp.Result, &detailInfos); err != nil {
				continue
			}
			for _, detailItem := range detailInfos {
				section, _ := detailItem["sourceDetails"].(string)
				if parsed := ParseInterfaceVlanif(section); parsed != nil {
					vlanifMap[strings.TrimPrefix(strings.ToLower(parsed.Name), "vlan")] = *parsed
				}
			}
		}
	}

	items := buildSwitchPortVLANListFromInterfaceInfo(info)
	for i := range items {
		if vlanif, ok := vlanifMap[strings.ToLower(items[i].VLAN)]; ok {
			items[i].VPCName = firstNonEmpty(vlanif.Name, "vlan"+items[i].VLAN)
			items[i].Gateway = vlanif.IPAddress
			items[i].SubnetMask = vlanif.SubnetMask
		}
	}
	return items, nil
}

func buildSwitchPortVLANListFromInterfaceInfo(info SwitchInterfaceInfo) []SwitchPortVLANItem {
	tokens := expandSwitchVLANTokens(info.VlanMapping)
	items := make([]SwitchPortVLANItem, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, SwitchPortVLANItem{
			VLAN:    token,
			VPCName: info.VlanMapping.PortDescription,
		})
	}
	return items
}

func expandSwitchVLANTokens(mapping SwitchVlanMapping) []string {
	seen := map[string]struct{}{}
	ordered := make([]string, 0, 4)
	add := func(val string) {
		val = strings.TrimSpace(val)
		if val == "" || val == "-" || val == "--" {
			return
		}
		val = strings.TrimPrefix(strings.ToLower(val), "vlan")
		if _, ok := seen[val]; ok {
			return
		}
		seen[val] = struct{}{}
		ordered = append(ordered, val)
	}
	if mapping.PVID > 0 {
		add(strconv.FormatInt(mapping.PVID, 10))
	}
	cleaned := strings.NewReplacer(",", " ", ";", " ", "to", "-", "\t", " ").Replace(mapping.TrunkVLANList)
	for _, token := range strings.Fields(cleaned) {
		add(token)
	}
	return ordered
}

// 端口操作模块：按厂商和连接方式分发启停口、改描述和清计数器动作。
func (m *Module) handleSwitchPortAction(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	switch GetSwitchMaker(req.Maker) {
	case "huawei":
		return m.executeHuaweiPortAction(req, act, actionReq)
	case "h3c":
		return m.executeH3CPortAction(req, act, actionReq)
	case "centec":
		return m.executeCentecPortAction(req, act, actionReq)
	case "ruijie":
		return m.executeRuijiePortAction(req, act, actionReq)
	default:
		return fmt.Errorf("当前仅支持华为、华三、盛科或锐捷交换机")
	}
}

func (m *Module) executeHuaweiPortAction(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	switch req.Type {
	case 2:
		return m.executeHuaweiPortActionByTelnet(req, act, actionReq)
	case 3:
		return m.executeHuaweiPortActionBySSH(req, act, actionReq)
	default:
		return fmt.Errorf("当前仅支持Telnet或SSH方式")
	}
}

func (m *Module) executeH3CPortAction(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	switch req.Type {
	case 2:
		return m.executeH3CPortActionByTelnet(req, act, actionReq)
	case 3:
		return m.executeH3CPortActionBySSH(req, act, actionReq)
	default:
		return fmt.Errorf("当前仅支持Telnet或SSH方式")
	}
}

func (m *Module) executeCentecPortAction(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	switch req.Type {
	case 1:
		return m.executeCentecPortActionByRPC(req, act, actionReq)
	case 2:
		return m.executeCentecPortActionByTelnet(req, act, actionReq)
	case 3:
		return m.executeCentecPortActionBySSH(req, act, actionReq)
	default:
		return fmt.Errorf("当前仅支持RPC、Telnet或SSH方式")
	}
}

func (m *Module) executeRuijiePortAction(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	switch req.Type {
	case 2:
		return m.executeRuijiePortActionByTelnet(req, act, actionReq)
	case 3:
		return m.executeRuijiePortActionBySSH(req, act, actionReq)
	default:
		return fmt.Errorf("当前仅支持Telnet或SSH方式")
	}
}

func (m *Module) executeHuaweiPortActionBySSH(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return err
	}
	defer client.Close()

	softwareVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip)
	if err != nil {
		return err
	}
	cmds, err := buildHuaweiPortActionCommandsForVersion(req.Interface, act, actionReq, softwareVersion)
	if err != nil {
		return err
	}
	output, err := runHuaweiConfigCommandsBySSH(client, cmds)
	if err != nil {
		return fmt.Errorf("执行命令失败: %v", err)
	}
	if hasHuaweiCommandError(output) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeHuaweiPortActionByTelnet(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		return fmt.Errorf("Connection failed: %v", err)
	}
	defer client.Conn.Close()

	morePatterns := []string{"---- More ----", "--More--", "More"}
	softwareVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip)
	if err != nil {
		return err
	}
	cmds, err := buildHuaweiPortActionCommandsForVersion(req.Interface, act, actionReq, softwareVersion)
	if err != nil {
		return err
	}
	output, err := runHuaweiConfigCommandsByTelnet(client, cmds, 1200*time.Millisecond, morePatterns)
	if err != nil {
		return err
	}
	if hasHuaweiCommandError(output) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeH3CPortActionBySSH(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return err
	}
	defer client.Close()

	softwareVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip)
	if err != nil {
		return err
	}
	cmds, err := buildH3CPortActionCommandsForVersion(req.Interface, act, actionReq, softwareVersion)
	if err != nil {
		return err
	}
	// SSH can use the non-interactive variant; TELNET handles the save prompts below.
	if act == 2 && len(cmds) > 0 {
		cmds[len(cmds)-1] = "save force"
	}
	options := InteractiveOptions{MorePatterns: []string{"---- More ----", "--More--", "More"}, PromptSuffixes: []string{"#", ">", "]"}, AutoConfirm: act == 2, AutoExit: false}
	output, err := client.RunInteractiveWithOptions(cmds, options)
	if err != nil {
		return fmt.Errorf("执行命令失败: %v", err)
	}
	if hasHuaweiCommandError(output) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeH3CPortActionByTelnet(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		return fmt.Errorf("Connection failed: %v", err)
	}
	defer client.Conn.Close()

	var output strings.Builder
	morePatterns := []string{"---- More ----", "--More--", "More"}
	softwareVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip)
	if err != nil {
		return err
	}
	cmds, err := buildH3CPortActionCommandsForVersion(req.Interface, act, actionReq, softwareVersion)
	if err != nil {
		return err
	}
	for index, cmd := range cmds {
		if err := client.SendCommand(cmd); err != nil {
			return fmt.Errorf("发送命令失败: %v", err)
		}
		cur, err := client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
		if err != nil {
			return fmt.Errorf("执行命令失败: %v", err)
		}
		output.WriteString(cur)
		if act == 2 && index == len(cmds)-1 { // save: confirm, keep filename, confirm overwrite
			for step := 0; step < 3; step++ {
				upper := strings.ToUpper(strings.ReplaceAll(cur, " ", ""))
				response := ""
				switch {
				case strings.Contains(upper, "[Y/N]"):
					response = "y"
				case strings.Contains(upper, "FILENAME") || strings.Contains(upper, "PRESSENTER") || strings.Contains(upper, "ENTER"):
					response = ""
				default:
					step = 3
				}
				if step >= 3 {
					break
				}
				if err := client.SendCommand(response); err != nil {
					return fmt.Errorf("保存确认失败: %v", err)
				}
				cur, err = client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
				if err != nil {
					return fmt.Errorf("读取保存确认失败: %v", err)
				}
				output.WriteString(cur)
			}
		}
	}
	if hasHuaweiCommandError(output.String()) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeCentecPortActionByRPC(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	if req.Format == "" {
		req.Format = "text"
	}
	if err := m.executeCentecRPCCommands(req, buildCentecPortActionCommands(req.Interface, act, actionReq)); err != nil {
		return err
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeCentecPortActionBySSH(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return err
	}
	defer client.Close()

	options := InteractiveOptions{
		PromptSuffixes: []string{"#", ">"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   1200,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       true,
	}
	output, err := client.RunInteractiveWithOptions(buildCentecPortActionCommands(req.Interface, act, actionReq), options)
	if err != nil {
		return fmt.Errorf("执行命令失败: %v", err)
	}
	if hasCentecCommandError(output) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeCentecPortActionByTelnet(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		return fmt.Errorf("Connection failed: %v", err)
	}
	defer client.Conn.Close()

	var output strings.Builder
	morePatterns := []string{"---- More ----", "--More--", "More"}
	for _, cmd := range buildCentecPortActionCommands(req.Interface, act, actionReq) {
		if err := client.SendCommand(cmd); err != nil {
			return fmt.Errorf("发送命令失败: %v", err)
		}
		cur, err := client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
		if err != nil {
			return fmt.Errorf("执行命令失败: %v", err)
		}
		output.WriteString(cur)
	}
	if hasCentecCommandError(output.String()) {
		return fmt.Errorf("端口操作失败")
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeRuijiePortActionBySSH(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client, err := m.connectSwitchSSHClient(req)
	if err != nil {
		return err
	}
	defer client.Close()

	output, err := client.RunInteractiveWithOptions(buildRuijiePortActionCommands(req.Interface, act, actionReq), InteractiveOptions{
		PromptSuffixes: []string{"#", ">"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   1200 * time.Millisecond,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       false,
	})
	if err != nil {
		return fmt.Errorf("执行命令失败: %v", err)
	}
	if pattern := matchSwitchCollectionCommandError(output); pattern != "" {
		return fmt.Errorf("端口操作失败: %s", pattern)
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) executeRuijiePortActionByTelnet(req SwitchRequest, act int, actionReq SwitchPortDetailRequest) error {
	client := &TelnetClient{Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		return fmt.Errorf("Connection failed: %v", err)
	}
	defer client.Conn.Close()

	var output strings.Builder
	morePatterns := []string{"---- More ----", "--More--", "More"}
	for _, command := range buildRuijiePortActionCommands(req.Interface, act, actionReq) {
		if err := client.SendCommand(command); err != nil {
			return fmt.Errorf("发送命令失败: %v", err)
		}
		current, err := client.ReadPagedResponse(1500*time.Millisecond, morePatterns)
		if err != nil {
			return fmt.Errorf("执行命令失败: %v", err)
		}
		output.WriteString(current)
	}
	if pattern := matchSwitchCollectionCommandError(output.String()); pattern != "" {
		return fmt.Errorf("端口操作失败: %s", pattern)
	}
	return m.updateCachedSwitchPortState(req.Hostip, req.Interface, act, actionReq)
}

func (m *Module) connectSwitchSSHClient(req SwitchRequest) (*Client, error) {
	kexitems := []string{
		"mlkem768x25519-sha256",
		"curve25519-sha256",
		"curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256",
		"ecdh-sha2-nistp384",
		"ecdh-sha2-nistp521",
		"diffie-hellman-group-exchange-sha256",
		"diffie-hellman-group14-sha256",
		"diffie-hellman-group14-sha1",
	}
	client, err := m.ConnectWithLoginInfo(req.Hostip, req.Port, req.Username, req.Password, kexitems, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return nil, fmt.Errorf("执行命令失败: %v,请重新连接", err)
	}
	return client, nil
}

func buildHuaweiPortActionCommands(port string, act int, actionReq SwitchPortDetailRequest) []string {
	port = normalizeSwitchPortName(port)
	cmds := []string{}
	switch act {
	case 1:
		cmds = append(cmds, "system-view", fmt.Sprintf("interface %s", port))
		if actionReq.PortEnabled != nil && !*actionReq.PortEnabled {
			cmds = append(cmds, "shutdown")
		} else {
			cmds = append(cmds, "undo shutdown")
		}
		cmds = append(cmds, buildHuaweiConfigFinalizeCommands()...)
	case 2:
		cmds = append(cmds, "system-view", fmt.Sprintf("interface %s", port))
		if strings.TrimSpace(actionReq.PortDescription) == "" {
			cmds = append(cmds, "undo description")
		} else {
			cmds = append(cmds, fmt.Sprintf("description %s", strings.TrimSpace(actionReq.PortDescription)))
		}
		cmds = append(cmds, buildHuaweiConfigFinalizeCommands()...)
	case 3:
		cmds = append(cmds, fmt.Sprintf("reset counters interface %s", port))
	}
	return cmds
}

func buildHuaweiPortActionCommandsForVersion(port string, act int, actionReq SwitchPortDetailRequest, softwareVersion string) ([]string, error) {
	port = normalizeSwitchPortName(port)
	softwareVersion = strings.TrimSpace(softwareVersion)
	switch act {
	case 3:
		return buildHuaweiPortClearCounterCommandsByVersion(port, softwareVersion)
	default:
		return buildHuaweiPortActionCommands(port, act, actionReq), nil
	}
}

func buildH3CPortActionCommands(port string, act int, actionReq SwitchPortDetailRequest) []string {
	port = normalizeSwitchPortName(port)
	cmds := []string{}
	switch act {
	case 1:
		cmds = append(cmds, "system-view", fmt.Sprintf("interface %s", port))
		if actionReq.PortEnabled != nil && !*actionReq.PortEnabled {
			cmds = append(cmds, "shutdown")
		} else {
			cmds = append(cmds, "undo shutdown")
		}
		cmds = append(cmds, "quit", "quit", "save force")
	case 2:
		cmds = append(cmds, "system-view", fmt.Sprintf("interface %s", port))
		if strings.TrimSpace(actionReq.PortDescription) == "" {
			cmds = append(cmds, "undo description")
		} else {
			cmds = append(cmds, fmt.Sprintf("description %s", strings.TrimSpace(actionReq.PortDescription)))
		}
		cmds = append(cmds, "quit", "quit", "save")
	case 3:
		cmds = append(cmds, fmt.Sprintf("reset counters interface %s", port))
	}
	return cmds
}

func buildH3CPortActionCommandsForVersion(port string, act int, actionReq SwitchPortDetailRequest, softwareVersion string) ([]string, error) {
	port = normalizeSwitchPortName(port)
	softwareVersion = strings.TrimSpace(softwareVersion)
	switch act {
	case 3:
		return buildH3CPortClearCounterCommandsByVersion(port, softwareVersion)
	default:
		return buildH3CPortActionCommands(port, act, actionReq), nil
	}
}

func buildCentecPortActionCommands(port string, act int, actionReq SwitchPortDetailRequest) []string {
	port = normalizeSwitchPortName(port)
	cmds := []string{}
	switch act {
	case 1:
		cmds = append(cmds, "configure terminal", fmt.Sprintf("interface %s", port))
		if actionReq.PortEnabled != nil && !*actionReq.PortEnabled {
			cmds = append(cmds, "shutdown")
		} else {
			cmds = append(cmds, "no shutdown")
		}
		cmds = append(cmds, "end", "write memory")
	case 2:
		cmds = append(cmds, "configure terminal", fmt.Sprintf("interface %s", port))
		if strings.TrimSpace(actionReq.PortDescription) == "" {
			cmds = append(cmds, "no description")
		} else {
			cmds = append(cmds, fmt.Sprintf("description %s", strings.TrimSpace(actionReq.PortDescription)))
		}
		cmds = append(cmds, "end", "write memory")
	case 3:
		cmds = append(cmds, "configure terminal", fmt.Sprintf("interface %s", port), fmt.Sprintf("clear counters interface %s", port), "end")
	}
	return cmds
}

func buildRuijiePortActionCommands(port string, act int, actionReq SwitchPortDetailRequest) []string {
	port = normalizeSwitchPortName(port)
	var cmds []string
	switch act {
	case 1:
		cmds = append(cmds, "configure terminal", fmt.Sprintf("interface %s", port))
		if actionReq.PortEnabled != nil && !*actionReq.PortEnabled {
			cmds = append(cmds, "shutdown")
		} else {
			cmds = append(cmds, "no shutdown")
		}
		cmds = append(cmds, "exit", "exit", "write")
	case 2:
		cmds = append(cmds, "configure terminal", fmt.Sprintf("interface %s", port))
		if strings.TrimSpace(actionReq.PortDescription) == "" {
			cmds = append(cmds, "no description")
		} else {
			cmds = append(cmds, fmt.Sprintf("description %s", strings.TrimSpace(actionReq.PortDescription)))
		}
		cmds = append(cmds, "exit", "exit", "write")
	case 3:
		cmds = append(cmds, fmt.Sprintf("clear counters interface %s", port))
	}
	return cmds
}

func buildHuaweiPortClearCounterCommandsByVersion(port, softwareVersion string) ([]string, error) {
	port = normalizeSwitchPortName(port)
	softwareVersion = strings.TrimSpace(softwareVersion)
	branch := huaweiSoftwareVersionBranch(softwareVersion)
	switch {
	case branch == "SPC800":
		return []string{
			"system-view",
			fmt.Sprintf("interface %s", port),
			fmt.Sprintf("reset counters interface %s", port),
			"quit",
			"quit",
		}, nil
	case branch == "SPC500":
		return []string{
			fmt.Sprintf("reset counters interface %s", port),
		}, nil
	default:
		return nil, fmt.Errorf("华为交换机软件版本未识别，无法执行清除计数器: %s", softwareVersion)
	}
}

func buildH3CPortClearCounterCommandsByVersion(port, softwareVersion string) ([]string, error) {
	port = normalizeSwitchPortName(port)
	softwareVersion = strings.TrimSpace(softwareVersion)
	switch {
	case strings.Contains(softwareVersion, "7.1.070, Release 2609"):
		return []string{
			"system-view",
			fmt.Sprintf("interface %s", port),
			fmt.Sprintf("reset counters interface %s", port),
			"quit",
			"quit",
		}, nil
	case strings.Contains(strings.ToLower(softwareVersion), "comware"):
		return []string{
			fmt.Sprintf("reset counters interface %s", port),
		}, nil
	default:
		return nil, fmt.Errorf("H3C交换机软件版本未识别，无法执行清除计数器: %s", softwareVersion)
	}
}

func (m *Module) loadSwitchPortSoftwareVersion(hostip string) (string, error) {
	detail, err := m.loadDeviceInformation(hostip)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(detail.SoftwareVersion)
	if version == "" {
		return "", fmt.Errorf("未获取到设备软件版本")
	}
	return version, nil
}

func huaweiSoftwareVersionBranch(softwareVersion string) string {
	softwareVersion = strings.TrimSpace(softwareVersion)
	if len(softwareVersion) <= 6 {
		return softwareVersion
	}
	return softwareVersion[len(softwareVersion)-6:]
}

func (m *Module) updateCachedSwitchPortState(hostip, port string, act int, actionReq SwitchPortDetailRequest) error {
	detail, err := m.loadDeviceInformation(hostip)
	if err != nil {
		return err
	}
	updated := false
	for i := range detail.Interfaces {
		if !switchPortNamesEqual(detail.Interfaces[i].Port, port) {
			continue
		}
		switch act {
		case 1:
			if actionReq.PortEnabled != nil {
				if *actionReq.PortEnabled {
					detail.Interfaces[i].Status = "up"
				} else {
					detail.Interfaces[i].Status = "down"
				}
			}
		case 2:
			detail.Interfaces[i].Description = strings.TrimSpace(actionReq.PortDescription)
		}
		updated = true
		break
	}
	if !updated {
		return nil
	}
	return m.saveDeviceInformation(hostip, detail)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
