package olt

import (
	"sort"
	"strings"
)

// SubStringBetween 返回 start 与 end 之间的子串（endIndex 传 0 表示第一个匹配，负数表示倒数第几个）。
// 老实现（rpc_client.go 同名函数）语义逐字搬运。
func SubStringBetween(strValue, start, end string, endIndex int) string {
	if start == "" {
		return ""
	}
	n := strings.Index(strValue, start)
	if n == -1 {
		return ""
	}
	n += len(start)
	strValue = strValue[n:]
	if end == "" {
		return strValue
	}
	if endIndex == 0 {
		m := strings.Index(strValue, end)
		if m == -1 {
			return ""
		}
		return strValue[:m]
	}
	n = strings.LastIndex(strValue, end)
	if n == -1 {
		return ""
	}
	return strValue[:n]
}

// extractOLTFailure 从命令回显里提取 \"Failure: ...\" 后的失败信息（老实现同名函数）。
func extractOLTFailure(output string) (string, bool) {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "failure") {
		return "", false
	}
	failure := strings.TrimSpace(SubStringBetween(output, "Failure:", "\r\n", 0))
	if failure == "" {
		failure = strings.TrimSpace(output)
	}
	return failure, true
}

// oltConfigurationLines 把配置文本规整成行（老实现同名函数，逐字搬运）。
func oltConfigurationLines(configuration string) []string {
	configuration = strings.ReplaceAll(configuration, "\r\n", "\n")
	configuration = strings.ReplaceAll(configuration, "\r", "\n")
	return strings.Split(configuration, "\n")
}

// oltConfigurationSection 截取配置里 `[<name>]` 起到独立 `#` 结束的区段（老实现同名函数）。
func oltConfigurationSection(lines []string, name string) []string {
	header := "[" + strings.ToLower(strings.TrimSpace(name)) + "]"
	inside := false
	section := make([]string, 0)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if strings.EqualFold(line, header) {
			inside = true
			continue
		}
		if inside && line == "#" {
			break
		}
		if inside {
			section = append(section, rawLine)
		}
	}
	return section
}

// parseOLTVLANConfiguration 从已保存配置解析出四类 VLAN 信息（老实现同名函数逐字搬运）：
//   - vlaniflist：三层 VLAN 接口（`vlanif` + `ip address`）；每条 {VLANID, IPAddress, SubnetMask}；
//   - normalvlanlist：VLAN/二层互通（smart / q-in-q / stacking 三档 + 组播业务 VLAN）；
//   - tranvlanlist：上行 VLAN 透传（`port vlan`）；每条 {szPortInfo, szVlan, szDesc}；
//   - routestaticlist：静态路由（`ip route-static`）；每条 {szNetwork, szGateway}。
func parseOLTVLANConfiguration(configuration string) ([]map[string]any, []map[string]any, []map[string]any, []map[string]any) {
	lines := oltConfigurationLines(configuration)
	vlaniflist := make([]map[string]any, 0)
	normalvlanlist := make([]map[string]any, 0)
	tranvlanlist := make([]map[string]any, 0)
	routestaticlist := make([]map[string]any, 0)

	// 老的已保存配置用 LF 换行，且不一定保留 [prevlanif]，所以从配置块里识别每个 VLAN 接口。
	currentVLANID := ""
	seenVLANIF := make(map[string]struct{})
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "#" || (strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")) {
			currentVLANID = ""
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "interface") {
			name := strings.ToLower(fields[1])
			switch {
			case strings.HasPrefix(name, "vlanif") && strings.TrimPrefix(name, "vlanif") != "":
				currentVLANID = strings.TrimPrefix(name, "vlanif")
			case name == "vlanif" && len(fields) >= 3:
				currentVLANID = fields[2]
			default:
				currentVLANID = ""
			}
			continue
		}
		if currentVLANID != "" && len(fields) >= 4 && strings.EqualFold(fields[0], "ip") && strings.EqualFold(fields[1], "address") {
			key := currentVLANID + "\x00" + fields[2] + "\x00" + fields[3]
			if _, exists := seenVLANIF[key]; !exists {
				seenVLANIF[key] = struct{}{}
				vlaniflist = append(vlaniflist, map[string]any{"VLANID": currentVLANID, "IPAddress": fields[2], "SubnetMask": fields[3]})
			}
		}
	}

	modeVLANs := map[string][]string{"smart": {}, "q-in-q": {}, "stacking": {}}
	modeSeen := make(map[string]map[string]struct{})
	for mode := range modeVLANs {
		modeSeen[mode] = make(map[string]struct{})
	}
	transparency := make(map[string][]string)
	transparencySeen := make(map[string]map[string]struct{})
	for _, rawLine := range oltConfigurationSection(lines, "vlan-config") {
		fields := strings.Fields(strings.TrimSpace(rawLine))
		if len(fields) < 3 {
			continue
		}
		if strings.EqualFold(fields[0], "vlan") {
			mode := strings.ToLower(fields[len(fields)-1])
			if _, supported := modeVLANs[mode]; supported {
				vlanFields := fields[1 : len(fields)-1]
				if len(vlanFields) > 0 && strings.EqualFold(vlanFields[0], "attrib") {
					vlanFields = vlanFields[1:]
				}
				vlan := strings.Join(vlanFields, " ")
				if vlan != "" {
					if _, exists := modeSeen[mode][vlan]; !exists {
						modeSeen[mode][vlan] = struct{}{}
						modeVLANs[mode] = append(modeVLANs[mode], vlan)
					}
				}
			}
			continue
		}
		if !strings.EqualFold(fields[0], "port") || !strings.EqualFold(fields[1], "vlan") || len(fields) < 5 {
			continue
		}
		vlan := strings.Join(fields[2:len(fields)-2], " ")
		port := fields[len(fields)-2] + "/" + fields[len(fields)-1]
		if vlan == "" {
			continue
		}
		if transparencySeen[port] == nil {
			transparencySeen[port] = make(map[string]struct{})
		}
		if _, exists := transparencySeen[port][vlan]; !exists {
			transparencySeen[port][vlan] = struct{}{}
			transparency[port] = append(transparency[port], vlan)
		}
	}

	for _, item := range []struct{ mode, label string }{
		{"smart", "单层智能VLAN(Smart VLAN)"},
		{"q-in-q", "基本/普通 QinQ"},
		{"stacking", "灵活 QinQ"},
	} {
		if vlans := modeVLANs[item.mode]; len(vlans) > 0 {
			normalvlanlist = append(normalvlanlist, map[string]any{"szName": item.label, "szVlan": strings.Join(vlans, ",")})
		}
	}
	for _, rawLine := range oltConfigurationSection(lines, "btv-config") {
		fields := strings.Fields(strings.TrimSpace(rawLine))
		if len(fields) >= 2 && strings.EqualFold(fields[0], "multicast-vlan") {
			normalvlanlist = append(normalvlanlist, map[string]any{"szName": "组播业务 VLAN", "szVlan": strings.Join(fields[1:], " ")})
		}
	}

	ports := make([]string, 0, len(transparency))
	for port := range transparency {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		tranvlanlist = append(tranvlanlist, map[string]any{"szPortInfo": port, "szVlan": strings.Join(transparency[port], ","), "szDesc": ""})
	}

	seenRoutes := make(map[string]struct{})
	for _, rawLine := range lines {
		fields := strings.Fields(strings.TrimSpace(rawLine))
		if len(fields) < 5 || !strings.EqualFold(fields[0], "ip") || !strings.EqualFold(fields[1], "route-static") {
			continue
		}
		key := strings.Join(fields[2:5], "\x00")
		if _, exists := seenRoutes[key]; exists {
			continue
		}
		seenRoutes[key] = struct{}{}
		routestaticlist = append(routestaticlist, map[string]any{"szNetwork": fields[2] + " / " + fields[3], "szGateway": fields[4]})
	}

	return vlaniflist, normalvlanlist, tranvlanlist, routestaticlist
}
