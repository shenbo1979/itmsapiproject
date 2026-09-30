// 由 tools/port_switch_batch2.py 从 switchapi/rpc_client.go 的 230-294（RPC-API JSON-RPC 通道） 段搬运
// （package 改名、注册校验走 m.requireRegistration）。
package switchmod

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// rpcHTTPClient 只读（无状态），保留为包级以避免每请求新建连接池。
var rpcHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// ExecuteCmds 向交换机发送 JSON-RPC 请求执行 CLI 命令
func (m *Module) ExecuteCmds(host string, port int64, cmds []string, format string) (*JSONRPCResponse, error) {
	return m.executeCmds(host, port, cmds, format, "", "")
}

func (m *Module) executeCmds(host string, port int64, cmds []string, format, username, password string) (*JSONRPCResponse, error) {
	// 交换机接口的注册信息验证：未注册/已过期时不允许操作交换机。
	if err := m.requireRegistration(); err != nil {
		return nil, err
	}
	if format == "" {
		format = "text"
	}
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "executeCmds",
		ID:      uuid.New().String(),
		Params: []CmdParams{
			{
				Format:  format,
				Version: 1,
				Cmds:    cmds,
			},
		},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := fmt.Sprintf("http://%s:%d/command-api", host, port)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// 关键：要求设备在响应后关闭连接。
	// 交换机侧 RPC 服务（如盛科 CentecRpcApi，Python BaseHTTP）在返回 401 时既不带
	// Content-Length 也不关闭连接，Go 的 http 客户端按“读到 EOF”处理响应体，会一直等到
	// 30s 超时（表现为"context deadline exceeded"，把凭据错误伪装成设备不可达）。
	// 老工程同样是 30s 且同样不设该头，只是因为线上用的是可解密的正确口令（返回 200 带分帧）
	// 所以从未暴露；迁移后一旦口令不可用就会退化成超时。带上 Connection: close 后，
	// 设备立即关闭连接：实测 401 在 5ms 内返回，错误信息清晰可见。请求本身无状态，关闭复用无副作用。
	req.Close = true
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := rpcHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 先看状态码：非 2xx 时最多读 512 字节作为诊断信息，避免在未分帧响应上长时间阻塞。
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &rpcResp, nil
}
func ParseInterfaceInfo(name, text string, tranInfo []TransceiverInfo) ([]SwitchInterfaceInfo, error) {
	lines := strings.Split(text, "\n")
	if len(name) == 0 {
		name = "centec"
	}
	//headerDone := false
	result := make([]SwitchInterfaceInfo, 0)
	if name == "centec" {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// 跳过空行
			if len(strings.TrimSpace(line)) == 0 {
				continue
			}
			if isCentecInterfaceStatusLine(line) {
				fields := strings.Fields(line)
				if len(fields) >= 6 {
					ethname := strings.TrimSpace(fields[0])
					duplexIndex := findCentecDuplexIndex(fields)
					// Some Centec versions report multi-word states such as "admin down".
					// Join their fragments so Status has no embedded whitespace.
					ethstatus := strings.Join(fields[1:duplexIndex], "")
					duplex := strings.TrimSpace(fields[duplexIndex])
					speed := strings.TrimSpace(fields[duplexIndex+1])
					mode := strings.TrimSpace(fields[duplexIndex+2])
					ethtype := strings.TrimSpace(fields[duplexIndex+3])
					description := ""
					var ethTemperature float64 = 0.0
					var ethVoltage float64 = 0.0
					var ethCurrent float64 = 0.0
					var ethTxPower float64 = 0.0
					var ethRxPower float64 = 0.0
					for _, tran := range tranInfo {
						if tran.Port == ethname {
							ethTemperature = tran.Temperature
							ethVoltage = tran.Voltage
							ethCurrent = tran.Current
							ethTxPower = tran.TxPower
							ethRxPower = tran.RxPower
							break
						}
					}
					if len(fields) > duplexIndex+4 {
						description = strings.Join(fields[duplexIndex+4:], " ")
					}
					result = append(result, SwitchInterfaceInfo{
						Port:        ethname,
						Status:      ethstatus,
						Duplex:      duplex,
						Speed:       speed,
						Mode:        mode,
						Type:        ethtype,
						Description: description,
						Temperature: ethTemperature,
						Voltage:     ethVoltage,
						Current:     ethCurrent,
						TxPower:     ethTxPower,
						RxPower:     ethRxPower,
					})
				}
			}
		}
	}
	return result, nil
}

func findCentecDuplexIndex(fields []string) int {
	for index := 2; index+3 < len(fields); index++ {
		switch strings.ToLower(strings.TrimSpace(fields[index])) {
		case "full", "half", "auto", "unknown":
			return index
		}
	}
	return 2
}

// isCentecInterfaceStatusLine identifies data rows from "show interface
// status". Centec uses different names for physical, aggregate, VLAN and
// management interfaces across software versions, so filtering by a fixed
// set of prefixes loses valid interfaces.
func isCentecInterfaceStatusLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return false
	}
	port := normalizeSwitchPortName(fields[0])
	if port == "" || strings.EqualFold(port, "port") || strings.EqualFold(port, "interface") {
		return false
	}
	for _, char := range port {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("-_/.:", char) {
			continue
		}
		return false
	}
	return true
}

// 分析CENTEC(盛科)交换机的光模块信息
func ParseTransceiverInfo(data string) []TransceiverInfo {
	var results []TransceiverInfo
	// 按端口分割（每个 "Port eth-0-xx" 作为新记录的开始）
	portSections := regexp.MustCompile(`(?m)^Port (eth-\d+-\d+)`).FindAllStringIndex(data, -1)
	if len(portSections) == 0 {
		return results
	}
	for i, sectionStart := range portSections {
		var sectionEnd int
		if i+1 < len(portSections) {
			sectionEnd = portSections[i+1][0]
		} else {
			sectionEnd = len(data)
		}
		section := data[sectionStart[0]:sectionEnd]
		info := TransceiverInfo{}
		PortName := extractPortName(section)
		info.Port = PortName
		if strings.Contains(section, "Temperature") {
			info.Temperature = extractCentecMetricValue(section, "Temperature", PortName)
		}
		if strings.Contains(section, "Voltage") {
			info.Voltage = extractCentecMetricValue(section, "Voltage", PortName)
		}
		if strings.Contains(section, "Current") {
			info.Current = extractCentecMetricValue(section, "Current", PortName)
		}

		if strings.Contains(section, "Transmit Power") {
			info.TxPower = extractCentecMetricValue(section, "Transmit Power", PortName)
		}
		if strings.Contains(section, "Receive Power") {
			info.RxPower = extractCentecMetricValue(section, "Receive Power", PortName)
		}
		results = append(results, info)
	}
	return results
}

func extractCentecMetricValue(section, metricName, portName string) float64 {
	matches := regexp.MustCompile(regexp.QuoteMeta(metricName)+`\s`).FindAllStringIndex(section, -1)
	if len(matches) == 0 {
		return 0
	}
	subsection := section[matches[0][0]:]
	for _, line := range strings.Split(subsection, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, portName) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, portName))
		if match := regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`).FindString(rest); match != "" {
			if val, err := strconv.ParseFloat(match, 64); err == nil {
				return val
			}
		}
	}
	return 0
}

func ParseCentecPortVlan(data string) map[string]SwitchVlanMapping {
	result := make(map[string]SwitchVlanMapping)
	lines := strings.Split(data, "\n")
	inSection := false
	currentVLAN := ""
	portPattern := regexp.MustCompile(`eth-\d+-\d+`)

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "<") || strings.HasPrefix(line, "#") {
			break
		}
		if strings.Contains(lower, "show vlan brief") {
			continue
		}
		if strings.Contains(lower, "vlan") && strings.Contains(lower, "ports") {
			inSection = true
			continue
		}
		if !inSection || strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		if matched, _ := regexp.MatchString(`^\d+$`, fields[0]); matched {
			currentVLAN = fields[0]
			mergeCentecVlanPorts(result, currentVLAN, strings.Join(portPattern.FindAllString(line, -1), " "))
			continue
		}

		if currentVLAN != "" {
			mergeCentecVlanPorts(result, currentVLAN, strings.Join(portPattern.FindAllString(line, -1), " "))
		}
	}

	return result
}

func ParseCentecSwitchportVlan(data string) map[string]SwitchVlanMapping {
	result := make(map[string]SwitchVlanMapping)
	blocks := regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(data), -1)
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) == 0 {
			continue
		}

		var port, mode, configuredVLANs string
		var pvid int64 = 1
		for _, raw := range lines {
			line := strings.TrimSpace(raw)
			if line == "" || !strings.Contains(line, ":") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			switch {
			case key == "interface name" || key == "name":
				port = value
			case key == "switchport mode":
				mode = strings.ToLower(value)
			case key == "default vlan":
				if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
					pvid = parsed
				}
			case key == "configured vlans":
				configuredVLANs = value
			}
		}
		if port == "" {
			continue
		}
		entry := SwitchVlanMapping{
			Port:          strings.TrimSpace(port),
			LinkType:      mode,
			PVID:          pvid,
			TrunkVLANList: configuredVLANs,
		}
		if entry.LinkType == "" {
			if strings.Contains(configuredVLANs, ",") || strings.Contains(strings.ToLower(configuredVLANs), "to") || strings.Contains(configuredVLANs, "-") {
				entry.LinkType = "trunk"
			} else {
				entry.LinkType = "access"
			}
		}
		result[entry.Port] = entry
	}
	return result
}

func mergeCentecVlanPorts(vlanMap map[string]SwitchVlanMapping, vlanID, portText string) {
	vlanID = strings.TrimSpace(vlanID)
	if vlanID == "" {
		return
	}
	replacer := strings.NewReplacer(",", " ", ";", " ", "\t", " ")
	for _, token := range strings.Fields(replacer.Replace(portText)) {
		port := strings.TrimSpace(strings.TrimRight(token, ","))
		if !strings.HasPrefix(port, "eth-") {
			continue
		}
		entry := vlanMap[port]
		entry.Port = port
		if entry.PVID == 0 {
			if pvid, err := strconv.ParseInt(vlanID, 10, 64); err == nil {
				entry.PVID = pvid
			}
		}
		entry.TrunkVLANList = appendUniqueVlanToken(entry.TrunkVLANList, vlanID)
		if strings.Contains(entry.TrunkVLANList, ",") {
			entry.LinkType = "trunk"
		} else if entry.TrunkVLANList != "" {
			entry.LinkType = "access"
		}
		vlanMap[port] = entry
	}
}

func appendUniqueVlanToken(existing, vlan string) string {
	vlan = strings.TrimSpace(vlan)
	if vlan == "" {
		return existing
	}
	if existing == "" {
		return vlan
	}
	for _, item := range strings.Split(existing, ",") {
		if strings.TrimSpace(item) == vlan {
			return existing
		}
	}
	return existing + "," + vlan
}

// 分析ruijie(锐捷)交换机的光模块信息

func ParseRuijieTransceiverInfo(data string) []TransceiverInfo {
	var results []TransceiverInfo
	scanner := bufio.NewScanner(strings.NewReader(data))
	var current TransceiverInfo
	var inInterface bool
	var hasTempData bool

	ifaceHeaderRegex := regexp.MustCompile(`^=+Interface\s+(.+?)=+$`)
	absentRegex := regexp.MustCompile(`^Interface\s+(\S+)\s+:\s+the transceiver is absent!`)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "User's password is too weak") {
			continue
		}

		// 跳过 absent
		if absentRegex.MatchString(trimmed) {
			if inInterface {
				inInterface = false
				current = TransceiverInfo{}
			}
			continue
		}
		// 新接口开始
		if matches := ifaceHeaderRegex.FindStringSubmatch(trimmed); matches != nil {
			if inInterface && hasTempData {
				results = append(results, current)
			}
			current = TransceiverInfo{Port: normalizeSwitchPortName(matches[1]), TransceiverDiagnostic: "unsupported"}
			inInterface = true
			hasTempData = false
			continue
		}

		if !inInterface {
			continue
		}

		if strings.Contains(trimmed, "doesn't support DDM") || strings.Contains(trimmed, "does not support DDM") {
			current.TransceiverDiagnostic = "unsupported"
			if inInterface {
				results = append(results, current)
				inInterface = false
				current = TransceiverInfo{}
			}
			continue
		}

		// 解析温度行
		if strings.Contains(trimmed, "Temp(Celsius)") {
			// 下一行才是数据
			scanner.Scan()
			dataLine := strings.TrimSpace(scanner.Text())
			if fields := strings.Fields(dataLine); len(fields) >= 5 {
				var wd, dy, dl, tx, rx float64
				for tranindex, val := range fields {
					if strings.Contains(val, "(") {
						parts := strings.SplitN(val, "(", 2)
						if len(parts) == 2 {
							valStr := parts[0]
							val, err := strconv.ParseFloat(valStr, 64)
							if err == nil {
								switch tranindex {
								case 0:
									wd = val
								case 1:
									dy = val
								case 2:
									dl = val
								case 3:
									tx = val
								case 4:
									rx = val
								}
							}
						}
					}
				}
				current = TransceiverInfo{Temperature: wd, Voltage: dy, Current: dl, RxPower: rx, TxPower: tx, Port: current.Port, TransceiverDiagnostic: "available"}
				hasTempData = true
			}
			continue
		}
	}
	if inInterface && hasTempData {
		results = append(results, current)
	}
	return results
}

func ParseRuijieVersionInfo(data string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(data, "\n")
	versionPattern := regexp.MustCompile(`(?i)(software version|version)\s*:\s*(.+)$`)
	modelPattern := regexp.MustCompile(`(?:Device|Model)\s*(?:name|Number)?\s*[: ]\s*(\S.+)$`)
	uptimePattern := regexp.MustCompile(`(?i)uptime\s+is\s+(.+)$`)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case result["softwareVersion"] == "" && (strings.Contains(lower, "software version") || strings.Contains(lower, "rgos")):
			if matches := versionPattern.FindStringSubmatch(line); len(matches) == 2 {
				result["softwareVersion"] = strings.TrimSpace(matches[1])
			} else if idx := strings.Index(line, ":"); idx >= 0 {
				result["softwareVersion"] = strings.TrimSpace(line[idx+1:])
			} else {
				result["softwareVersion"] = line
			}
		case result["deviceName"] == "" && strings.Contains(lower, "uptime is"):
			parts := strings.SplitN(line, " uptime is ", 2)
			if len(parts) == 2 {
				result["deviceName"] = strings.TrimSpace(parts[0])
				result["uptime"] = strings.TrimSpace(parts[1])
			} else if matches := uptimePattern.FindStringSubmatch(line); len(matches) == 2 {
				result["uptime"] = strings.TrimSpace(matches[1])
			}
		case result["deviceModel"] == "" && (strings.Contains(lower, "device name") || strings.Contains(lower, "model")):
			if matches := modelPattern.FindStringSubmatch(line); len(matches) == 2 {
				result["deviceModel"] = strings.TrimSpace(matches[1])
			}
		}
	}
	if result["deviceModel"] == "" && result["deviceName"] != "" {
		result["deviceModel"] = strings.TrimSpace(strings.TrimPrefix(result["deviceName"], "Ruijie"))
	}
	if result["deviceName"] == "" && result["deviceModel"] != "" {
		result["deviceName"] = "Ruijie " + result["deviceModel"]
	}
	return result
}

func ParseRuijieInterfaceStatus(data string, transceiverMap map[string]TransceiverInfo) []SwitchInterfaceInfo {
	var results []SwitchInterfaceInfo
	lines := strings.Split(data, "\n")
	started := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "show interface status"):
			continue
		case strings.HasPrefix(lower, "interface") && strings.Contains(lower, "status") && strings.Contains(lower, "duplex"):
			started = true
			continue
		case strings.HasPrefix(line, "Switch#") || strings.HasPrefix(line, "Ruijie#") || strings.HasPrefix(line, "<"):
			if started {
				break
			}
			continue
		}
		if !started || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		typeIndex := len(fields) - 1
		speedIndex := len(fields) - 2
		duplexIndex := len(fields) - 3
		vlanIndex := len(fields) - 4
		statusIndex := len(fields) - 5
		if statusIndex <= 0 {
			continue
		}
		port := normalizeSwitchPortName(strings.Join(fields[:statusIndex], " "))
		if !strings.Contains(port, "/") && !strings.Contains(strings.ToLower(port), "aggregateport") {
			continue
		}
		info := SwitchInterfaceInfo{
			Port:   port,
			Status: strings.ToLower(fields[statusIndex]),
			Duplex: fields[duplexIndex],
			Speed:  fields[speedIndex],
			Mode:   fields[vlanIndex],
			Type:   fields[typeIndex],
		}
		if tran, ok := transceiverMap[port]; ok {
			info.TransceiverDiagnostic = tran.TransceiverDiagnostic
			info.Temperature = tran.Temperature
			info.Voltage = tran.Voltage
			info.Current = tran.Current
			info.TxPower = tran.TxPower
			info.RxPower = tran.RxPower
		} else {
			info.TransceiverDiagnostic = "unknown"
		}
		results = append(results, info)
	}
	return results
}

func ParseRuijieSwitchportVlan(data string) map[string]SwitchVlanMapping {
	result := make(map[string]SwitchVlanMapping)
	lines := strings.Split(data, "\n")
	started := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "show interface switchport"):
			continue
		case strings.HasPrefix(lower, "interface") && strings.Contains(lower, "switchport mode"):
			started = true
			continue
		case strings.HasPrefix(line, "Switch#") || strings.HasPrefix(line, "Ruijie#") || strings.HasPrefix(line, "<"):
			if started {
				break
			}
			continue
		}
		if !started || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		enabledIndex := -1
		for i, field := range fields {
			if strings.EqualFold(field, "enabled") || strings.EqualFold(field, "disabled") {
				enabledIndex = i
				break
			}
		}
		if enabledIndex <= 0 || len(fields) < enabledIndex+6 {
			continue
		}
		port := normalizeSwitchPortName(strings.Join(fields[:enabledIndex], " "))
		mode := strings.ToLower(fields[enabledIndex+1])
		pvid, _ := strconv.ParseInt(fields[enabledIndex+2], 10, 64)
		vlanList := fields[enabledIndex+5]
		if strings.EqualFold(vlanList, "ALL") {
			vlanList = strconv.FormatInt(pvid, 10)
		}
		result[port] = SwitchVlanMapping{
			Port:          port,
			LinkType:      mode,
			PVID:          pvid,
			TrunkVLANList: vlanList,
		}
	}
	return result
}

func buildRuijieInterfaceInfo(statusOutput, transceiverOutput, switchportOutput string) ([]SwitchInterfaceInfo, error) {
	transceiverSlice := ParseRuijieTransceiverInfo(transceiverOutput)
	transceiverMap := make(map[string]TransceiverInfo, len(transceiverSlice))
	for _, item := range transceiverSlice {
		port := normalizeSwitchPortName(item.Port)
		item.Port = port
		if item.TransceiverDiagnostic == "" {
			item.TransceiverDiagnostic = "available"
		}
		transceiverMap[port] = item
	}
	interfaces := ParseRuijieInterfaceStatus(statusOutput, transceiverMap)
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("解析锐捷接口信息失败")
	}
	vlanMap := ParseRuijieSwitchportVlan(switchportOutput)
	for i := range interfaces {
		if vlanInfo, ok := vlanMap[interfaces[i].Port]; ok {
			interfaces[i].VlanMapping = vlanInfo
		}
	}
	return interfaces, nil
}

func extractPortName(section string) string {
	re := regexp.MustCompile(`Port\s+(eth-\d+-\d+)`)
	matches := re.FindStringSubmatch(section)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func extractFloat(text, pattern string) float64 {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		val, err := strconv.ParseFloat(strings.TrimSpace(matches[1]), 64)
		if err == nil {
			return val
		}
	}
	return 0.0
}

func extractRxPower(section string) float64 {
	// 匹配 Receive Power 行，可能带有 -- 等标记
	re := regexp.MustCompile(`Receive Power\s*\(dBm\)\s*-+\s*\n\s*[^\n]+\s+([\d.-]+)\s*(?:--|\+\+|-\+)?`)
	matches := re.FindStringSubmatch(section)
	if len(matches) >= 2 {
		val, err := strconv.ParseFloat(strings.TrimSpace(matches[1]), 64)
		if err == nil {
			return val
		}
	}
	return 0.0
}

// ParseInterfaceVlanif 解析VLAN接口信息
func ParseInterfaceVlanif(text string) *InterfaceVlanif {
	lines := strings.Split(text, "\n")
	info := &InterfaceVlanif{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 解析接口名称
		if strings.HasPrefix(line, "Interface vlan") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				info.Name = parts[1]
			}
		}
	}
	info.IPAddress, info.SubnetMask, info.SubnetMaskBits, _ = ExtractIPAndMask(text)
	return info
}

// ExtractIPAndMask 从文本中提取 IP 地址和子网掩码
func ExtractIPAndMask(text string) (ip string, mask string, maskBits int, err error) {
	re := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)/(\d+)`)
	matches := re.FindStringSubmatch(text)

	if len(matches) < 3 {
		return "", "", 0, fmt.Errorf("未找到有效的 IP/掩码格式")
	}

	ip = matches[1]
	maskBits, _ = strconv.Atoi(matches[2])
	mask = cidrToDottedMask(maskBits)

	return ip, mask, maskBits, nil
}

// cidrToDottedMask 将 CIDR 位数转换为点分十进制掩码
func cidrToDottedMask(bits int) string {
	mask := ^uint32(0) << (32 - uint32(bits))
	return fmt.Sprintf("%d.%d.%d.%d",
		byte(mask>>24),
		byte(mask>>16),
		byte(mask>>8),
		byte(mask))
}
