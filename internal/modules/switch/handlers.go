// 由 tools/port_switch_batch2b.py 从 switchapi/handler.go 提取（交换机链路 handler 与辅助函数；
// 已重写的 6 个基础资料/台账接口未包含）。
package switchmod

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// decodeFormJSON decodes a JSON object carried in a form field.
// Switch endpoints use this for nested request payloads such as terminal
// commands and scheduled task configuration.
func (m *Module) decodeFormJSON(r *http.Request, field string, target interface{}) error {
	if r == nil {
		return fmt.Errorf("request is nil")
	}
	raw := strings.TrimSpace(r.FormValue(field))
	if raw == "" {
		return fmt.Errorf("%s is required", field)
	}
	if target == nil {
		return fmt.Errorf("decode target is nil")
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid %s: %w", field, err)
	}
	return nil
}

func roundToTwoDecimalPlaces(value float64) float64 {
	return math.Round(value*100) / 100
}

// RPC-API 配置模块：负责查询和维护交换机 RPC-API 服务状态。
// RPC-API接口设置

// RPC-API 配置模块：负责查询和维护交换机 RPC-API 服务状态。
// RPC-API接口设置
func (m *Module) handleRpcApiConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	handleact, acterr := strconv.Atoi(r.FormValue("Act"))
	if acterr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + acterr.Error()})
		return
	}
	saveData := r.FormValue("saveData")
	var rac RpcApiConfig
	if racerr := json.Unmarshal([]byte(saveData), &rac); racerr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + racerr.Error()})
		return
	}
	switch act := handleact; {
	case act == 0:
		if rac.Hostip == "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "host and cmds are required"})
			return
		}
		if rac.Apiport == 0 {
			rac.Apiport = 80
		}
		if rac.Apiport < 1 || rac.Apiport > 65535 {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid api port"})
			return
		}
		cmds := []string{
			"show services rpc-api",
		}
		rpcResp, err := m.ExecuteCmdsWithLoginInfo(rac.Hostip, rac.Apiport, cmds, "text", SwitchLoginInfo{
			Username: rac.Username,
			Password: rac.Password,
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
			return
		}
		if rpcResp.Error != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
			return
		}
		infos := []map[string]interface{}{}
		if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
			writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
			return
		}
		for _, info := range infos {
			sourceDetails, ok := info["sourceDetails"].(string)
			if !ok {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: invalid sourceDetails"})
				return
			}
			sdlist := strings.Split(sourceDetails, "\n")
			for _, line := range sdlist {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "Server State") && strings.Contains(line, ":") {
					rac.Enable = strings.TrimSpace(strings.Split(line, ":")[1])
				}

			}
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: rac})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}

// 设备信息模块：负责设备缓存读取、实时采集和凭据解密入口。
// TODO 交换机接口设计开始
// handleDeviceInformation 处理 POST /api/handleDeviceInformation/ 请求
// nAct: 0 获取本地设备信息；1 获取设备的实时信息

// 设备信息模块：负责设备缓存读取、实时采集和凭据解密入口。
// TODO 交换机接口设计开始
// handleDeviceInformation 处理 POST /api/handleDeviceInformation/ 请求
// nAct: 0 获取本地设备信息；1 获取设备的实时信息
func (m *Module) handleDeviceInformation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	nAct, _ := strconv.Atoi(r.FormValue("nAct"))
	switch nAct {
	case 0:
		linkIP := strings.TrimSpace(r.FormValue("szLinkIp"))
		if linkIP != "" {
			device, err := m.readLocalDeviceInformationByHostip(linkIP)
			if err != nil {
				message := "读取本地设备信息失败: " + err.Error()
				if os.IsNotExist(err) {
					message = "本地尚未保存该设备信息，请先连接设备进行采集"
				}
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
				return
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: device})
			return
		}

		// 未指定 szLinkIp 时保持原有格式，读取全部本地缓存文件。
		devices, err := m.readLocalDeviceInformation()
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "读取本地设备信息失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: devices})
		return
	case 1:
		// 实时获取设备信息，并写入本地缓存目录
		connData := r.FormValue("connData")
		var req SwitchRequest
		if err := m.decodeEncryptedSwitchRequest(connData, &req); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		if req.Hostip == "" {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "host and cmds are required"})
			return
		}
		detail, err := m.queryDeviceInformation(req)
		if err != nil {
			// 连接/采集失败时仅记录日志，不更新设备台账状态。
			log.Printf("设备[%s]连接/采集失败: %v", req.Hostip, err)
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error(), Data: SwitchDeviceDetail{Hostip: req.Hostip, Online: 0, Interfaces: []SwitchInterfaceInfo{}}})
			return
		}
		detail.Hostip = req.Hostip
		detail.Online = 1
		assignSwitchInterfaceIDs(detail.Interfaces)
		detail = normalizeSwitchDeviceDetail(detail)
		m.recordLoginConnection(req.Hostip, req.Port, req.Type, req.Username, req.Password, req.Maker)
		cacheErr := m.saveDeviceInformation(req.Hostip, detail)
		statusErr := m.markConnectedDeviceOnline(req)
		if cacheErr != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "保存设备信息失败: " + cacheErr.Error()})
			return
		}
		if statusErr != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "设备连接成功，但更新在线状态失败: " + statusErr.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: detail})
		return
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
		return
	}
}

func (m *Module) markConnectedDeviceOnline(req SwitchRequest) error {
	if req.DeviceID > 0 {
		_, err := m.store.UpdateDeviceAutoEnableByID(req.DeviceID, 1)
		return err
	}
	_, err := m.store.UpdateDeviceAutoEnableByManageIP(req.Hostip, 1)
	return err
}

func (m *Module) decryptSwitchRequestCredentials(req *SwitchRequest) error {
	if req == nil {
		return nil
	}
	if strings.TrimSpace(req.Username) != "" {
		username, err := m.decryptSwitchRequestSecret(req.Username)
		if err != nil {
			return err
		}
		req.Username = username
	}
	if strings.TrimSpace(req.Password) != "" {
		password, err := m.decryptSwitchRequestSecret(req.Password)
		if err != nil {
			return err
		}
		req.Password = password
	}
	return nil
}

func (m *Module) decodeEncryptedSwitchRequest(raw string, req *SwitchRequest) error {
	if err := json.Unmarshal([]byte(raw), req); err != nil {
		return err
	}
	return m.decryptSwitchRequestCredentials(req)
}

func (m *Module) decryptSwitchRequestSecret(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "%") {
		if decoded, err := url.QueryUnescape(value); err == nil && strings.TrimSpace(decoded) != "" {
			value = decoded
		}
	}

	raw, err := m.decodeSwitchCiphertext(value)
	if err != nil {
		return value, nil
	}
	if len(raw) < 28 {
		return value, nil
	}
	return m.decryptSwitchLoginSecret(value)
}

// readLocalDeviceInformation 读取 deviceFileDir 目录下所有 .json 文件并返回

// readLocalDeviceInformation 读取 deviceFileDir 目录下所有 .json 文件并返回
func (m *Module) readLocalDeviceInformation() ([]LocalDeviceInformation, error) {
	devices := make([]LocalDeviceInformation, 0)
	entries, err := os.ReadDir(m.deviceFileDir)
	if err != nil {
		if os.IsNotExist(err) {
			return devices, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(m.deviceFileDir, entry.Name()))
		if err != nil {
			continue
		}
		if normalized, ok := normalizeLocalDeviceInformationContent(content); ok {
			content = normalized
		}
		devices = append(devices, LocalDeviceInformation{
			Hostip: strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Data:   json.RawMessage(content),
		})
	}
	return devices, nil
}

func (m *Module) readLocalDeviceInformationByHostip(hostip string) (LocalDeviceInformation, error) {
	hostip = strings.TrimSpace(hostip)
	if net.ParseIP(hostip) == nil {
		return LocalDeviceInformation{}, fmt.Errorf("invalid szLinkIp")
	}
	content, err := os.ReadFile(filepath.Join(m.deviceFileDir, hostip+".json"))
	if err != nil {
		return LocalDeviceInformation{}, err
	}
	if detail, ok := normalizeLocalDeviceInformationContent(content); ok {
		content = detail
	}
	return LocalDeviceInformation{Hostip: hostip, Data: json.RawMessage(content)}, nil
}

func normalizeLocalDeviceInformationContent(content []byte) ([]byte, bool) {
	var detail SwitchDeviceDetail
	if err := json.Unmarshal(content, &detail); err != nil {
		return nil, false
	}
	detail = normalizeSwitchDeviceDetail(detail)
	normalized, err := json.Marshal(detail)
	if err != nil {
		return nil, false
	}
	return normalized, true
}

// saveDeviceInformation 将实时获取的设备信息以 <szHostip>.json 写入本地目录

// saveDeviceInformation 将实时获取的设备信息以 <szHostip>.json 写入本地目录
func (m *Module) saveDeviceInformation(hostip string, detail SwitchDeviceDetail) error {
	if err := os.MkdirAll(m.deviceFileDir, 0755); err != nil {
		return err
	}
	assignSwitchInterfaceIDs(detail.Interfaces)
	detail = normalizeSwitchDeviceDetail(detail)
	data, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.deviceFileDir, hostip+".json"), data, 0644)
}

func assignSwitchInterfaceIDs(interfaces []SwitchInterfaceInfo) {
	for index := range interfaces {
		if interfaces[index].ID == 0 {
			interfaces[index].ID = index + 1
		}
	}
}

func normalizeSwitchDeviceDetail(detail SwitchDeviceDetail) SwitchDeviceDetail {
	if detail.Interfaces == nil {
		detail.Interfaces = []SwitchInterfaceInfo{}
		return detail
	}

	interfaces := append([]SwitchInterfaceInfo(nil), detail.Interfaces...)
	sortSwitchDeviceInterfaces(interfaces)
	detail.Interfaces = interfaces
	return detail
}

func sortSwitchDeviceInterfaces(interfaces []SwitchInterfaceInfo) {
	sort.Slice(interfaces, func(i, j int) bool {
		return compareSwitchCollectionInterfaceName(interfaces[i].Port, interfaces[j].Port) < 0
	})
}

// 实时采集模块：按连接方式和厂商分发命令，并组装统一的设备详情结构。
// queryDeviceInformation 根据连接方式实时获取交换机设备接口信息及设备级信息(运行时长等)

// 实时采集模块：按连接方式和厂商分发命令，并组装统一的设备详情结构。
// queryDeviceInformation 根据连接方式实时获取交换机设备接口信息及设备级信息(运行时长等)
func (m *Module) queryDeviceInformation(req SwitchRequest) (SwitchDeviceDetail, error) {
	detail := SwitchDeviceDetail{Hostip: req.Hostip, Interfaces: []SwitchInterfaceInfo{}}
	nType := req.Type
	szMaker := req.Maker
	morePatterns := []string{"---- More ----", "--More--", "More"}
	switch nType {
	case 2:
		if req.Port == 0 {
			req.Port = 23
		}
	case 3:
		if req.Port == 0 {
			req.Port = 22
		}
	default:
		if req.Port == 0 {
			req.Port = 80
		}
		if req.Format == "" {
			req.Format = "text"
		}
	}

	switch nType {
	case 1: //RPC-API方式连接交换机，获取设备接口信息暂时只有盛科支持此连接方式
		csname := GetSwitchMaker(szMaker)
		switch csname {
		case "centec":
			info, versionOutput, err := m.queryCentecInterfaceInfoByRPC(req)
			if err != nil {
				return detail, err
			}
			detail.Interfaces = info
			fillCentecDeviceDetail(&detail, versionOutput)
			return detail, nil
		default:
			return detail, fmt.Errorf("不支持的交换机类型")
		}
	case 2: //TELNET连接交换机，获取设备信息
		// 建立TELNET连接
		client := &TelnetClient{
			owner:   m,
			Host:    req.Hostip,
			Port:    req.Port,
			Maker:   szMaker,
			Timeout: 30 * time.Second,
		}
		// 连接
		if err := client.SwitchConnect(req.Username, req.Password); err != nil {
			return detail, fmt.Errorf("Connection failed: %v", err)
		}
		defer client.Conn.Close()
		csname := GetSwitchMaker(szMaker)
		switch csname {
		case "centec":
			info, versionOutput, err := queryCentecInterfaceInfoByTelnet(client, morePatterns)
			if err != nil {
				return detail, err
			}
			detail.Interfaces = info
			fillCentecDeviceDetail(&detail, versionOutput)
			return detail, nil
		case "ruijie":
			info, versionOutput, err := queryRuijieInterfaceInfoByTelnet(client, morePatterns)
			if err != nil {
				return detail, err
			}
			detail.Interfaces = info
			fillRuijieDeviceDetail(&detail, versionOutput)
			return detail, nil
		case "h3c":
			// 处理华三交换机的特定逻辑
			client.SendCommand("")
			_, _ = client.ReadResponse(500 * time.Millisecond)
			client.SendCommand("display version")
			veroutput, verouterr := client.ReadPagedResponse(1*time.Second, morePatterns)
			if verouterr != nil {
				return detail, fmt.Errorf("执行(display version)命令失败: %v", verouterr)
			}
			fillHuaweiDeviceDetail(&detail, string(veroutput))
			softwareVersion := detail.SoftwareVersion
			switch {
			case strings.Contains(softwareVersion, "7.1.070, Release 2609"):
				var output string
				cmdlist := append([]string{
					"display interface brief",
					"display transceiver diagnosis interface",
					"display port trunk",
					"display ip routing-table",
				}, deviceMonitoringCommands(csname)...)
				for _, cmdstr := range cmdlist {
					client.SendCommand(cmdstr)
					curoutput, curerr := client.ReadPagedResponse(1500*time.Millisecond, morePatterns)
					if curerr != nil {
						continue
					}
					output += curoutput
				}
				detail.Interfaces = ParseH3CInterfaceInfo(output)
				fillRoutingTableAfterCollection(&detail, csname, output)
				fillHuaweiDeviceDetail(&detail, output)
				return detail, nil
			default:
				return detail, fmt.Errorf("未识别到H3C软件版本")
			}
		case "huawei":
			// 处理华为交换机的特定逻辑
			client.SendCommand("display version")
			veroutput, verouterr := client.ReadPagedResponse(1*time.Second, morePatterns)
			if verouterr != nil {
				return detail, fmt.Errorf("执行(display version)命令失败: %v", verouterr)
			}
			fillHuaweiDeviceDetail(&detail, string(veroutput))
			softwareVersion := detail.SoftwareVersion
			switch {
			case strings.Contains(softwareVersion, "SPC800"):
				var output string
				cmdlist := append([]string{"display interface transceiver verbose", "display interface brief", "display port vlan", "display ip routing-table"}, deviceMonitoringCommands(csname)...)
				for _, cmdstr := range cmdlist {
					client.SendCommand(cmdstr)
					curoutput, curerr := client.ReadPagedResponse(1*time.Second, morePatterns)
					if curerr != nil {
						continue
					}
					output += curoutput
				}
				detail.Interfaces = ParseHuaweiInterfaceInfo(output)
				fillRoutingTableAfterCollection(&detail, csname, output)
				fillHuaweiDeviceDetail(&detail, output)
				return detail, nil
			case strings.Contains(softwareVersion, "SPC500"):
				var output string
				cmdlist := append([]string{"display transceiver verbose", "display interface brief", "display port vlan", "display ip routing-table"}, deviceMonitoringCommands(csname)...)
				for _, cmdstr := range cmdlist {
					client.SendCommand(cmdstr)
					curoutput, curerr := client.ReadPagedResponse(1*time.Second, morePatterns)
					if curerr != nil {
						continue
					}
					output += curoutput
				}
				detail.Interfaces = ParseHuaweiInterfaceInfo(output)
				fillRoutingTableAfterCollection(&detail, csname, output)
				fillHuaweiDeviceDetail(&detail, output)
				return detail, nil
			default:
				return detail, fmt.Errorf("此交换机类型未做匹配")
			}
		default:
			return detail, fmt.Errorf("不支持的交换机类型")
		}
	case 3: //SSH连接交换机，获取设备信息
		// 配置SSH认证
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
		s := SwitchSSHConfig{
			Host:          req.Hostip,   // 交换机IP
			Port:          req.Port,     // SSH端口，通常是22
			Username:      req.Username, // SSH用户名
			Password:      req.Password, // SSH密码
			kexAlgorithms: kexitems,
		}
		csname := GetSwitchMaker(szMaker)
		switch csname {
		case "centec":
			client, err := m.ConnectWithLoginInfo(s.Host, s.Port, s.Username, s.Password, s.kexAlgorithms, SwitchLoginInfo{
				Username: s.Username,
				Password: s.Password,
				Maker:    szMaker,
			})
			if err != nil {
				return detail, fmt.Errorf("执行命令失败: %v,请重新连接", err)
			}
			defer client.Close()

			info, versionOutput, err := queryCentecInterfaceInfoBySSH(client)
			if err != nil {
				return detail, err
			}
			detail.Interfaces = info
			fillCentecDeviceDetail(&detail, versionOutput)
			return detail, nil
		case "ruijie":
			client, err := m.ConnectWithLoginInfo(s.Host, s.Port, s.Username, s.Password, s.kexAlgorithms, SwitchLoginInfo{
				Username: s.Username,
				Password: s.Password,
				Maker:    szMaker,
			})
			if err != nil {
				return detail, fmt.Errorf("执行命令失败: %v,请重新连接", err)
			}
			defer client.Close()

			info, versionOutput, err := queryRuijieInterfaceInfoBySSH(client)
			if err != nil {
				return detail, err
			}
			detail.Interfaces = info
			fillRuijieDeviceDetail(&detail, versionOutput)
			return detail, nil
		case "h3c":
			// 处理华三交换机的特定逻辑
			verstr := []string{"display version"}
			client, err := m.ConnectWithLoginInfo(s.Host, s.Port, s.Username, s.Password, s.kexAlgorithms, SwitchLoginInfo{
				Username: s.Username,
				Password: s.Password,
				Maker:    szMaker,
			})
			if err != nil {
				return detail, fmt.Errorf("执行命令失败: %v,请重新连接", err)
			}
			defer client.Close()
			if cachedVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip); err == nil {
				if queryCmds, ok := buildCachedSwitchQueryCommands(csname, cachedVersion); ok {
					queryCmds = append(queryCmds, deviceMonitoringCommands(csname)...)
					if output, outerr := client.RunDeviceCollectionInteractive(queryCmds); outerr == nil {
						fillHuaweiDeviceDetail(&detail, output)
						detail.Interfaces = ParseH3CInterfaceInfo(output)
						fillRoutingTableAfterCollection(&detail, csname, output)
						if detail.SoftwareVersion != "" && len(detail.Interfaces) > 0 {
							return detail, nil
						}
					}
				}
			}
			veroutput, verouterr := client.RunDeviceCollectionInteractive(verstr)
			if verouterr != nil {
				return detail, fmt.Errorf("执行(display version)命令失败: %v", verouterr)
			}
			fillHuaweiDeviceDetail(&detail, string(veroutput))
			softwareVersion := detail.SoftwareVersion
			switch {
			case strings.Contains(softwareVersion, "7.1.070, Release 2609"):
				cmdstr := append([]string{
					"display transceiver diagnosis interface",
					"display interface brief",
					"display port trunk",
					"display ip routing-table",
				}, deviceMonitoringCommands(csname)...)
				output, outerr := client.RunDeviceCollectionInteractive(cmdstr)
				if outerr != nil {
					return detail, fmt.Errorf("执行命令失败: %v", outerr)
				}
				detail.Interfaces = ParseH3CInterfaceInfo(string(output))
				fillRoutingTableAfterCollection(&detail, csname, string(output))
				fillHuaweiDeviceDetail(&detail, string(output))
				return detail, nil
			default:
				return detail, fmt.Errorf("未识别到H3C软件版本")
			}
		case "huawei":
			// 处理华为交换机的特定逻辑
			//display device manu 显示详细的制造信息,如SN，如果多行则取第一行数据
			client, err := m.ConnectWithLoginInfo(s.Host, s.Port, s.Username, s.Password, s.kexAlgorithms, SwitchLoginInfo{
				Username: s.Username,
				Password: s.Password,
				Maker:    szMaker,
			})
			if err != nil {
				return detail, fmt.Errorf("执行命令失败: %v,请重新连接", err)
			}
			defer client.Close()
			if cachedVersion, err := m.loadSwitchPortSoftwareVersion(req.Hostip); err == nil {
				if queryCmds, ok := buildCachedSwitchQueryCommands(csname, cachedVersion); ok {
					queryCmds = append(queryCmds, deviceMonitoringCommands(csname)...)
					if output, outerr := client.RunDeviceCollectionInteractive(queryCmds); outerr == nil {
						fillHuaweiDeviceDetail(&detail, output)
						detail.Interfaces = ParseHuaweiInterfaceInfo(output)
						fillRoutingTableAfterCollection(&detail, csname, output)
						if detail.SoftwareVersion != "" && len(detail.Interfaces) > 0 {
							return detail, nil
						}
					}
				}
			}
			// Huawei may close the transport after a terminal "quit". Keep version
			// detection and inventory collection in one interactive Shell instead of
			// opening a second session after display version.
			collectionCmds := append([]string{
				"display version",
				"display interface transceiver verbose",
				"display transceiver verbose",
				"display interface brief",
				"display port vlan",
				"display ip routing-table",
			}, deviceMonitoringCommands(csname)...)
			output, outerr := client.RunDeviceCollectionInteractive(collectionCmds)
			if outerr != nil {
				return detail, fmt.Errorf("执行华为设备采集命令失败: %v", outerr)
			}
			fillHuaweiDeviceDetail(&detail, output)
			softwareVersion := detail.SoftwareVersion
			switch {
			case strings.Contains(softwareVersion, "SPC800"), strings.Contains(softwareVersion, "SPC500"):
				detail.Interfaces = ParseHuaweiInterfaceInfo(output)
				fillRoutingTableAfterCollection(&detail, csname, output)
				return detail, nil
			default:
				return detail, fmt.Errorf("此交换机类型未做匹配")
			}
		default:
			return detail, fmt.Errorf("不支持的交换机类型")
		}
	default:
		return detail, fmt.Errorf("未识别的连接方式")
	}
}

func fillRoutingTableAfterCollection(detail *SwitchDeviceDetail, maker, output string) {
	if output == "" {
		return
	}
	switch maker {
	case "huawei", "h3c":
		detail.RoutingTable = ParseRoutingTables(output)
	}
}

// 缓存版本快路径：根据本地缓存的软件版本直接选择命令组，减少一次版本探测往返。

// 缓存版本快路径：根据本地缓存的软件版本直接选择命令组，减少一次版本探测往返。
func buildCachedSwitchQueryCommands(maker, softwareVersion string) ([]string, bool) {
	softwareVersion = strings.TrimSpace(softwareVersion)
	switch maker {
	case "huawei":
		switch huaweiSoftwareVersionBranch(softwareVersion) {
		case "SPC800":
			return []string{
				"display version",
				"display interface transceiver verbose",
				"display interface brief",
				"display port vlan",
				"display ip routing-table",
			}, true
		case "SPC500":
			return []string{
				"display version",
				"display transceiver verbose",
				"display interface brief",
				"display port vlan",
				"display ip routing-table",
			}, true
		}
	case "h3c":
		if strings.Contains(softwareVersion, "7.1.070, Release 2609") {
			return []string{
				"display version",
				"display transceiver diagnosis interface",
				"display interface brief",
				"display port trunk",
				"display ip routing-table",
			}, true
		}
	}
	return nil, false
}

// extractUptime 通用运行时长提取：匹配 "uptime is ..." 行(华为/H3C/锐捷/盛科通用)

// extractUptime 通用运行时长提取：匹配 "uptime is ..." 行(华为/H3C/锐捷/盛科通用)
func extractUptime(versionOutput string) string {
	matches := regexp.MustCompile(`(?i)uptime\s+is\s+(.+)`).FindStringSubmatch(versionOutput)
	if len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// fillHuaweiDeviceDetail 从 display version 输出填充华为/H3C 的设备级信息

// fillHuaweiDeviceDetail 从 display version 输出填充华为/H3C 的设备级信息
func fillHuaweiDeviceDetail(detail *SwitchDeviceDetail, versionOutput string) {
	versionInfo := ParseHuaweiVersionInfo(versionOutput)
	if value := versionInfo["deviceName"]; value != "" {
		detail.DeviceName = value
	}
	if value := versionInfo["softwareVersion"]; value != "" {
		detail.SoftwareVersion = value
	}
	if uptime := versionInfo["uptime"]; uptime != "" {
		detail.Uptime = uptime
	} else if uptime := extractUptime(versionOutput); uptime != "" {
		detail.Uptime = uptime
	}
	fillDeviceMonitoringDetail(detail, "huawei", versionOutput)
}

// fillRuijieDeviceDetail 从 show version 输出填充锐捷的设备级信息

// fillRuijieDeviceDetail 从 show version 输出填充锐捷的设备级信息
func fillRuijieDeviceDetail(detail *SwitchDeviceDetail, versionOutput string) {
	versionInfo := ParseRuijieVersionInfo(versionOutput)
	if value := versionInfo["deviceName"]; value != "" {
		detail.DeviceName = value
	}
	if value := versionInfo["softwareVersion"]; value != "" {
		detail.SoftwareVersion = value
	}
	if uptime := versionInfo["uptime"]; uptime != "" {
		detail.Uptime = uptime
	} else if uptime := extractUptime(versionOutput); uptime != "" {
		detail.Uptime = uptime
	}
	fillDeviceMonitoringDetail(detail, "ruijie", versionOutput)
}

// fillDeviceMonitoringDetail 解析华为、H3C、锐捷的设备级只读监控输出。
// 命令在不同型号上可用性不同，因此解析不到数据时保留零值，不影响设备采集。

// fillDeviceMonitoringDetail 解析华为、H3C、锐捷的设备级只读监控输出。
// 命令在不同型号上可用性不同，因此解析不到数据时保留零值，不影响设备采集。
func fillDeviceMonitoringDetail(detail *SwitchDeviceDetail, maker, output string) {
	normalizedMaker := GetSwitchMaker(maker)
	if normalizedMaker != "huawei" && normalizedMaker != "h3c" && normalizedMaker != "ruijie" {
		return
	}

	if match := regexp.MustCompile(`(?im)^\s*CPU Usage\s*:\s*([0-9.]+)%`).FindStringSubmatch(output); len(match) == 2 {
		detail.CPUUsage, _ = strconv.ParseFloat(match[1], 64)
	} else if match := regexp.MustCompile(`(?i)CPU utilization (?:for|in) five seconds:\s*([0-9.]+)%`).FindStringSubmatch(output); len(match) == 2 {
		detail.CPUUsage, _ = strconv.ParseFloat(match[1], 64)
	} else if match := regexp.MustCompile(`(?im)^\s*([0-9.]+)%\s+in last 5 seconds`).FindStringSubmatch(output); len(match) == 2 {
		detail.CPUUsage, _ = strconv.ParseFloat(match[1], 64)
	} else if match := regexp.MustCompile(`(?im)CPU(?:\s+usage|\s+utilization)?\s*[:=]\s*([0-9.]+)%`).FindStringSubmatch(output); len(match) == 2 {
		detail.CPUUsage, _ = strconv.ParseFloat(match[1], 64)
	}

	if match := regexp.MustCompile(`(?im)^\s*System Memory:\s*\d+\s*(?:KBytes|KB|MB|GB|bytes?)\s+total,\s*(\d+)\s*(KBytes|KB|MB|GB|bytes?)\s+used\b`).FindStringSubmatch(output); len(match) == 3 {
		detail.MemoryAllocated = memoryValueInBytes(match[1], match[2])
	} else if match := regexp.MustCompile(`(?im)^\s*Total Memory Used Is:\s*(\d+)\s+bytes`).FindStringSubmatch(output); len(match) == 2 {
		detail.MemoryAllocated, _ = strconv.ParseInt(match[1], 10, 64)
	} else if match := regexp.MustCompile(`(?im)^\s*Total Memory Used:\s*(\d+)\s*(Kbytes|bytes|KB|MB|GB)\b`).FindStringSubmatch(output); len(match) == 3 {
		detail.MemoryAllocated = memoryValueInBytes(match[1], match[2])
	} else if match := regexp.MustCompile(`(?im)\b(?:Memory Used|Used Memory|Memory usage)\s*[:=]\s*(\d+)\s*(?:bytes?|KB|MB|GB)?`).FindStringSubmatch(output); len(match) == 2 {
		detail.MemoryAllocated, _ = strconv.ParseInt(match[1], 10, 64)
	}
	if match := regexp.MustCompile(`(?im)^\s*System uptime\s*:\s*(.+?)\s*$`).FindStringSubmatch(output); len(match) == 2 {
		detail.Uptime = strings.TrimSpace(match[1])
	}

	for _, match := range regexp.MustCompile(`(?im)^\s*(?:\d+\s+)?(?:hotspot\s+)?\d*\s*([0-9.]+)\s+(?:0|[-0-9.]+)\s+(?:\d+|NA)\s+(?:\d+|NA)\s+(?:\d+|NA)\s*$`).FindAllStringSubmatch(output, -1) {
		temperature, err := strconv.ParseFloat(match[1], 64)
		if err == nil && temperature > detail.DeviceTemperature {
			detail.DeviceTemperature = temperature
		}
	}
	for _, match := range regexp.MustCompile(`(?im)(?:temperature|temp(?:erature)?)\D{0,32}([0-9.]+)\s*(?:C|degree)`).FindAllStringSubmatch(output, -1) {
		temperature, err := strconv.ParseFloat(match[1], 64)
		if err == nil && temperature > detail.DeviceTemperature {
			detail.DeviceTemperature = temperature
		}
	}
	for _, match := range regexp.MustCompile(`(?im)^\s*(?:\d+\s+)?--\s+(\S+)\s+\S+\s+\d+\s+\d+\s+([0-9.]+)\s*$`).FindAllStringSubmatch(output, -1) {
		temperature, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		detail.TemperatureSensors = append(detail.TemperatureSensors, SwitchTemperatureSensor{Position: match[1], Temperature: temperature})
		if temperature > detail.DeviceTemperature {
			detail.DeviceTemperature = temperature
		}
	}
	if normalizedMaker == "ruijie" {
		for _, sensor := range parseRuijieTemperatureSensors(output) {
			detail.TemperatureSensors = append(detail.TemperatureSensors, sensor)
			if sensor.Temperature > detail.DeviceTemperature {
				detail.DeviceTemperature = sensor.Temperature
			}
		}
	}

	for _, match := range regexp.MustCompile(`(?im)^\s*fan\s*(\d+)\D{0,48}?([0-9.]+)\s*%.*$`).FindAllStringSubmatch(output, -1) {
		speed, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		detail.Fans = append(detail.Fans, SwitchFanInfo{ID: match[1], Speed: speed})
		detail.FanSpeedRatio += speed
	}
	for _, match := range regexp.MustCompile(`(?im)^\s*(?:\d+\s+)?(FAN\d+)\s+\[[^\]]+\]\s+(\S+)\s+([0-9.]+)%\([^)]*\)\s+(\S+)`).FindAllStringSubmatch(output, -1) {
		speed, err := strconv.ParseFloat(match[3], 64)
		if err != nil {
			continue
		}
		detail.Fans = append(detail.Fans, SwitchFanInfo{ID: match[1], Status: match[2], Speed: speed, Mode: match[4]})
		detail.FanSpeedRatio += speed
	}
	if len(detail.Fans) > 0 {
		detail.FanSpeedRatio = roundToTwoDecimalPlaces(detail.FanSpeedRatio / float64(len(detail.Fans)))
	}
	if normalizedMaker == "ruijie" {
		detail.Fans = append(detail.Fans, parseRuijieFans(output)...)
	}
}

func memoryValueInBytes(value, unit string) int64 {
	memory, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(unit) {
	case "kbytes", "kb":
		return memory * 1024
	case "mb":
		return memory * 1024 * 1024
	case "gb":
		return memory * 1024 * 1024 * 1024
	default:
		return memory
	}
}

func parseRuijieTemperatureSensors(output string) []SwitchTemperatureSensor {
	lines := strings.Split(normalizeCLIOutput(output), "\n")
	headerPattern := regexp.MustCompile(`(?i)Temperature\s+name\s+Current\(C\)\s+Status`)
	rowPattern := regexp.MustCompile(`^\s*(?:(\d+)\s+\S+\s+)?(\S+)\s+(-?[0-9]+(?:\.[0-9]+)?)\s+(\S+)\s*$`)
	inTable := false
	currentIndex := ""
	var sensors []SwitchTemperatureSensor
	for _, line := range lines {
		if headerPattern.MatchString(line) {
			inTable = true
			continue
		}
		if !inTable || strings.TrimSpace(line) == "" || isCLISeparatorLine(line) {
			continue
		}
		match := rowPattern.FindStringSubmatch(line)
		if len(match) != 5 {
			if len(sensors) > 0 {
				break
			}
			continue
		}
		temperature, err := strconv.ParseFloat(match[3], 64)
		if err != nil {
			continue
		}
		if match[1] != "" {
			currentIndex = match[1]
		}
		sensors = append(sensors, SwitchTemperatureSensor{Index: currentIndex, Position: match[2], Temperature: temperature, Status: match[4]})
	}
	return sensors
}

func parseRuijieFans(output string) []SwitchFanInfo {
	lines := strings.Split(normalizeCLIOutput(output), "\n")
	headerPattern := regexp.MustCompile(`(?i)Fan\s+id\s+Type\s+Status\s+Hardware\s+Version\s+Serial\s+Number`)
	rowPattern := regexp.MustCompile(`(?i)^\s*(\d+)\s+(.+?)\s+(ok|normal|fault|failed|absent|not\s+present)\s+(\S+)\s+(\S+)\s*$`)
	inTable := false
	var fans []SwitchFanInfo
	for _, line := range lines {
		if headerPattern.MatchString(line) {
			inTable = true
			continue
		}
		if !inTable || strings.TrimSpace(line) == "" || isCLISeparatorLine(line) {
			continue
		}
		match := rowPattern.FindStringSubmatch(line)
		if len(match) != 6 {
			if len(fans) > 0 {
				break
			}
			continue
		}
		fans = append(fans, SwitchFanInfo{
			ID:              match[1],
			Type:            strings.TrimSpace(match[2]),
			Status:          match[3],
			HardwareVersion: match[4],
			SerialNumber:    match[5],
		})
	}
	return fans
}

func isCLISeparatorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && strings.Trim(trimmed, "-=") == ""
}

func deviceMonitoringCommands(maker string) []string {
	switch GetSwitchMaker(maker) {
	case "huawei":
		return []string{
			"display cpu", "display memory", "display device temperature all", "display device fan",
			"display cpu-usage", "display memory-usage", "display temperature slot 0", "display fan",
		}
	case "h3c":
		return []string{"display cpu-usage", "display memory", "display environment", "display fan"}
	case "ruijie":
		return []string{"show cpu", "show memory", "show temperature", "show fan"}
	default:
		return nil
	}
}

func collectMonitoringOutputByTelnet(client *TelnetClient, maker string, morePatterns []string) string {
	var output strings.Builder
	for _, command := range deviceMonitoringCommands(maker) {
		if value, err := runTelnetCommandWithPaging(client, command, morePatterns, 3*time.Second); err == nil {
			output.WriteString(value)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func collectMonitoringOutputBySSH(client *Client, maker string) string {
	commands := deviceMonitoringCommands(maker)
	if len(commands) == 0 {
		return ""
	}
	output, err := client.RunInteractiveWithOptions(commands, InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(GetSwitchMaker(maker)),
		PromptSuffixes: []string{">", "#", "]"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		ExitCommand:    switchInteractiveExitCommandForMaker(maker),
		CommandDelay:   250 * time.Millisecond,
		ReadTimeout:    3 * time.Second,
		AutoExit:       true,
	})
	if err != nil {
		return ""
	}
	return output
}

// collectMonitoringOutputBySSHWithinBounds keeps optional monitoring from delaying
// the synchronous device-information response when a device CLI command stalls.

// collectMonitoringOutputBySSHWithinBounds keeps optional monitoring from delaying
// the synchronous device-information response when a device CLI command stalls.
func collectMonitoringOutputBySSHWithinBounds(client *Client, maker string, timeout time.Duration) string {
	return collectMonitoringOutputWithinBounds(timeout, func() string {
		return collectMonitoringOutputBySSH(client, maker)
	})
}

func collectMonitoringOutputWithinBounds(timeout time.Duration, collect func() string) string {
	if timeout <= 0 || collect == nil {
		return ""
	}
	result := make(chan string, 1)
	go func() {
		result <- collect()
	}()

	select {
	case output := <-result:
		return output
	case <-time.After(timeout):
		return ""
	}
}

// fillCentecDeviceDetail 解析 Centec 的版本和设备级监控输出。

// fillCentecDeviceDetail 解析 Centec 的版本和设备级监控输出。
func fillCentecDeviceDetail(detail *SwitchDeviceDetail, output string) {
	detail.Uptime = extractUptime(output)
	if match := regexp.MustCompile(`(?im)^CentecOS Software,\s*([^,]+),\s*Version\s*([^\s]+)`).FindStringSubmatch(output); len(match) == 3 {
		detail.DeviceName = strings.TrimSpace(match[1])
		detail.SoftwareVersion = strings.TrimSpace(match[2])
	}
	if match := regexp.MustCompile(`(?i)CPU utilization for five seconds:\s*([0-9.]+)%\s*/\s*([0-9.]+)%`).FindStringSubmatch(output); len(match) == 3 {
		detail.CPUUsage, _ = strconv.ParseFloat(match[1], 64)
		detail.CPUInterrupt, _ = strconv.ParseFloat(match[2], 64)
	}

	memoryPattern := regexp.MustCompile(`(?im)^\s*Allocated memory size:\s*(\d+)\s*$`)
	for _, match := range memoryPattern.FindAllStringSubmatch(output, -1) {
		allocated, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil {
			detail.MemoryAllocated += allocated
		}
	}

	environmentStart := strings.Index(strings.ToLower(output), "fan module:")
	if environmentStart < 0 {
		return
	}
	environment := output[environmentStart:]
	fanPattern := regexp.MustCompile(`(?m)^\s*(\d+)\s+(\d+-\d+)\s+(\S+)\s+([0-9.]+)%\s+(\S+)\s*$`)
	for _, match := range fanPattern.FindAllStringSubmatch(environment, -1) {
		speed, err := strconv.ParseFloat(match[4], 64)
		if err != nil {
			continue
		}
		detail.Fans = append(detail.Fans, SwitchFanInfo{ID: match[1], Index: match[2], Status: match[3], Speed: speed, Mode: match[5]})
		detail.FanSpeedRatio += speed
	}
	if len(detail.Fans) > 0 {
		detail.FanSpeedRatio = roundToTwoDecimalPlaces(detail.FanSpeedRatio / float64(len(detail.Fans)))
	}

	sensorStart := strings.Index(strings.ToLower(environment), "sensor status")
	if sensorStart < 0 {
		return
	}
	sensorPattern := regexp.MustCompile(`(?m)^\s*(\d+)\s+(-?[0-9.]+)\s+-?[0-9.]+\s+-?[0-9.]+\s+-?[0-9.]+\s+(\S+)\s*$`)
	for _, match := range sensorPattern.FindAllStringSubmatch(environment[sensorStart:], -1) {
		temperature, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		detail.TemperatureSensors = append(detail.TemperatureSensors, SwitchTemperatureSensor{Index: match[1], Temperature: temperature, Position: match[3]})
		if temperature > detail.DeviceTemperature {
			detail.DeviceTemperature = temperature
		}
	}
}

func collectCentecMonitoringOutputByTelnet(client *TelnetClient, morePatterns []string) string {
	var output strings.Builder
	for _, command := range []string{"show process", "show memory summary", "show environment"} {
		if value, err := runTelnetCommandWithPaging(client, command, morePatterns, 3*time.Second); err == nil {
			output.WriteString(value)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func collectCentecMonitoringOutputBySSH(client *Client) string {
	var output strings.Builder
	for _, command := range []string{"show process", "show memory summary", "show environment"} {
		if value, err := client.RunInteractive([]string{command}); err == nil {
			output.WriteString(value)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

// VLAN 信息模块：负责 VLAN 列表、分类器和端口 VLAN 管理接口。

// VLAN 信息模块：负责 VLAN 列表、分类器和端口 VLAN 管理接口。
func (m *Module) handleVlanInformation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	hostInfo := r.FormValue("hostInfo")
	var req SwitchRequest
	if err := m.decodeEncryptedSwitchRequest(hostInfo, &req); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	if req.Port == 0 {
		req.Port = 80
	}
	if req.Format == "" {
		req.Format = "text"
	}
	nType, _ := strconv.Atoi(r.FormValue("nType"))
	nAct, _ := strconv.Atoi(r.FormValue("nAct"))
	switch nType {
	case 0: //查询VLAN列表
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				"show vlan all",
				"show vlan-security",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			//查询VLAN列表
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			var result []VLANInfo
			for index, info := range infos {
				sd := info["sourceDetails"]
				if index == 0 {
					//VLAN列表
					sdlist := strings.Split(sd.(string), "\n")
					headerDone := false
					for _, line := range sdlist {
						// 跳过空行
						if len(strings.TrimSpace(line)) == 0 {
							continue
						}
						// 跳过头部，直到遇到 === 分隔线
						if !headerDone {
							if strings.HasPrefix(strings.TrimSpace(line), "=====") {
								headerDone = true
							}
							continue
						}
						// 判断是否为续行（成员端口跨行）：行首为空白则为续行
						if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
							// 续行，追加到最后一个 VLAN 的成员端口
							if len(result) > 0 {
								ports := strings.TrimSpace(line)
								if ports != "" {
									result[len(result)-1].MemberPort += " " + ports
								}
							}
							continue
						}
						vlan := VLANInfo{}
						linelist := regexp.MustCompile(`\s+`).Split(line, -1)
						if len(linelist) >= 1 {
							vlan.VlanId = strings.TrimSpace(linelist[0])
						}
						if len(linelist) >= 2 {
							vlan.Name = strings.TrimSpace(linelist[1])
						}
						if len(linelist) >= 3 {
							vlan.Status = strings.TrimSpace(linelist[2])
							vlan.MacLearning = "Enable"
							vlan.MacLimitAction = "Forward"
							vlan.MacLimitMaximum = 0
						}
						if len(linelist) >= 4 {
							vlan.STPID = strings.TrimSpace(linelist[3])
						}
						if len(linelist) >= 5 {
							vlan.MemberPort = strings.TrimSpace(strings.Join(linelist[4:], " "))
						}
						if vlan.VlanId != "" {
							result = append(result, vlan)
						}
					}
				} else if index == 1 {
					//VLAN安全列表
					sdlist := strings.Split(sd.(string), "\n")
					headerDone := false
					for _, line := range sdlist {
						line = strings.TrimSpace(line)
						// 跳过空行
						if len(strings.TrimSpace(line)) == 0 {
							continue
						}
						// 跳过头部，直到遇到 === 分隔线
						if !headerDone {
							if strings.HasPrefix(strings.TrimSpace(line), "--------") {
								headerDone = true
							}
							continue
						}
						fields := regexp.MustCompile(`\s+`).Split(line, -1)
						if len(fields) >= 4 {
							vlanId := strings.TrimSpace(fields[0])
							macLearning := strings.TrimSpace(fields[1])
							macLimitMaximum := strings.TrimSpace(fields[2])
							macAction := strings.TrimSpace(fields[4])
							for i, vlan := range result {
								if vlan.VlanId == vlanId {
									result[i].MacLearning = macLearning
									result[i].MacLimitMaximum, _ = strconv.Atoi(macLimitMaximum)
									result[i].MacLimitAction = macAction
									break
								}
							}
						}
					}
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
		case a == 1 || a == 3:
			//添加/删除VLAN
			saveData := r.FormValue("saveData")
			var vlanreq VLANInfo
			if err := json.Unmarshal([]byte(saveData), &vlanreq); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			var upvlanstr string
			if a == 1 {
				upvlanstr = fmt.Sprintf("vlan %s", vlanreq.VlanId)
				if vlanreq.Name != "" {
					upvlanstr += fmt.Sprintf(" name %s", vlanreq.Name)
				}
			} else {
				upvlanstr = fmt.Sprintf("no vlan %s", vlanreq.VlanId)
			}
			cmds := []string{
				"configure terminal",
				"vlan database",
				upvlanstr,
				"exit",
				"exit",
				"write memory",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := fmt.Sprintf("配置错误(Invalid parameter:%s, please check again.)", upvlanstr) //info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		case a == 2:
			//修改VLAN
			saveData := r.FormValue("saveData")
			var vlanreq VLANInfo
			if err := json.Unmarshal([]byte(saveData), &vlanreq); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			cmds := []string{
				"configure terminal",
				"vlan database",
			}
			cmds = append(cmds, fmt.Sprintf("vlan %s state %s", vlanreq.VlanId, vlanreq.Status))
			if vlanreq.MacLimitMaximum == 0 {
				cmds = append(cmds, fmt.Sprintf("no vlan %s mac-limit maximum", vlanreq.VlanId))
			} else {
				cmds = append(cmds, fmt.Sprintf("vlan %s mac-limit maximum %d", vlanreq.VlanId, vlanreq.MacLimitMaximum))
			}
			cmds = append(cmds, fmt.Sprintf("vlan %s mac-limit action %s", vlanreq.VlanId, vlanreq.MacLimitAction))
			cmds = append(cmds, fmt.Sprintf("vlan %s mac learning %s", vlanreq.VlanId, vlanreq.MacLearning))
			cmds = append(cmds, "end")
			cmds = append(cmds, "write memory")
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	case 1: //查询VLAN IF端口列表
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				"show ip interface brief",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			//查询VLAN列表
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			var vlanifnames []string
			for _, info := range infos {
				sd := info["sourceDetails"]
				lines := strings.Split(sd.(string), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if len(line) > 0 {
						if strings.HasPrefix(strings.ToLower(line), "vlan") {
							fields := regexp.MustCompile(`\s+`).Split(line, -1)
							if len(fields) >= 1 {
								vlanifnames = append(vlanifnames, strings.TrimSpace(fields[0]))
							}
						}
					} else {
						continue
					}
				}
			}
			if len(vlanifnames) == 0 {
				writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: []string{}})
				return
			} else {
				var vlanifs []InterfaceVlanif
				for _, name := range vlanifnames {
					cmdstr := fmt.Sprintf("show interface %s", name)
					cmds := []string{
						cmdstr,
					}
					rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
						Username: req.Username,
						Password: req.Password,
						Maker:    req.Maker,
					})
					if err != nil {
						continue
					}
					if rpcResp.Error != nil {
						continue
					}

					vlanifinfos := []map[string]interface{}{}
					if err := json.Unmarshal(rpcResp.Result, &vlanifinfos); err != nil {
						continue
					}
					for _, info := range vlanifinfos {
						sd := info["sourceDetails"]
						lines := sd.(string)
						vlanifdata := ParseInterfaceVlanif(lines)
						if vlanifdata != nil {
							vlanifs = append(vlanifs, *vlanifdata)
						}
					}
				}
				writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: vlanifs})
			}
		case a == 1 || a == 2 || a == 3:
			//添加/删除/修改VLAN IF接口
			saveData := r.FormValue("saveData")
			var vlanifreq InterfaceVlanif
			if err := json.Unmarshal([]byte(saveData), &vlanifreq); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			cmds := []string{
				"configure terminal",
			}
			switch a {
			case 1:
				cmds = append(cmds, fmt.Sprintf("interface vlan %s", vlanifreq.VlanId))
				cmds = append(cmds, fmt.Sprintf("ip address %s %s", vlanifreq.IPAddress, vlanifreq.SubnetMask))
				cmds = append(cmds, "no shutdown")
			case 2:
				cmds = append(cmds, fmt.Sprintf("interface vlan %s", vlanifreq.VlanId))
				cmds = append(cmds, fmt.Sprintf("ip address %s %s", vlanifreq.IPAddress, vlanifreq.SubnetMask))
				cmds = append(cmds, "no shutdown")
			case 3:
				cmds = append(cmds, fmt.Sprintf("no interface vlan %s", vlanifreq.VlanId))
			}
			cmds = append(cmds, "end")
			cmds = append(cmds, "write memory")
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	case 2: //查询Access/Trunk端口列表
		//TODO
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				"show interface switchport", //检查配置，显示交换机接口的信息
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			var ethVlans []InterfaceVlanif
			for _, info := range infos {
				sd := info["sourceDetails"]
				lines := strings.Split(sd.(string), "Interface")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if len(line) > 0 {
						curinfos := strings.Split(line, "\n")
						if len(curinfos) > 0 {
							var ethname, ethmode, joinvlan string
							pvid := 1
							for _, curline := range curinfos {
								curline = strings.TrimSpace(curline)
								if strings.Contains(strings.ToLower(curline), "name") && strings.Contains(curline, ":") {
									ethname = strings.TrimSpace(strings.Split(curline, ":")[1])
								}
								if strings.Contains(strings.ToLower(curline), "switchport mode") && strings.Contains(curline, ":") {
									ethmode = strings.TrimSpace(strings.Split(curline, ":")[1])
								}
								if strings.Contains(strings.ToLower(curline), "default vlan") && strings.Contains(curline, ":") {
									pvidstr := strings.TrimSpace(strings.Split(curline, ":")[1])
									if pid, err := strconv.Atoi(pvidstr); err == nil {
										pvid = pid
									}
								}
								if strings.Contains(strings.ToLower(curline), "configured vlans") && strings.Contains(curline, ":") {
									joinvlan = strings.TrimSpace(strings.Split(curline, ":")[1])
								}
							}
							if ethname != "" {
								ethport := &InterfaceVlanif{
									EthName:     strings.TrimSpace(ethname),
									EthMode:     ethmode,
									EthPVID:     pvid,
									EthJoinVlan: joinvlan,
								}
								ethVlans = append(ethVlans, *ethport)
							}
						}
					} else {
						continue
					}
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: ethVlans})
		case a == 2:
			//修改Access/Trunk端口
			saveData := r.FormValue("saveData")
			var vr []InterfaceVlanif
			if err := json.Unmarshal([]byte(saveData), &vr); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			for _, vrobj := range vr {
				ethname := vrobj.EthName
				//ethmode := vrobj.EthMode
				ethpvid := vrobj.EthPVID
				ethjoinvlan := vrobj.EthJoinVlan
				oldpvid := vrobj.OldEthPVID
				oldjoinvlan := vrobj.OldEthJoinVlan
				if ethpvid != oldpvid || ethjoinvlan != oldjoinvlan {
					cmds := []string{
						"configure terminal",
					}
					cmds = append(cmds, fmt.Sprintf("interface %s", ethname))
					if ethpvid == 0 {
						ethpvid = 1
					}
					if ethpvid != oldpvid {
						if ethpvid == 1 {
							cmds = append(cmds, "no switchport access vlan")
						} else {
							cmds = append(cmds, fmt.Sprintf("switchport access vlan %d", ethpvid))
						}
					}
					if len(ethjoinvlan) == 0 {
						ethjoinvlan = "1"
					}
					if len(oldjoinvlan) == 0 {
						oldjoinvlan = "1"
					}
					if ethjoinvlan != oldjoinvlan {
						removevlanlist, _ := parseRange(oldjoinvlan, ethpvid)
						removevlans := compressRanges(removevlanlist)
						if len(removevlans) > 0 {
							cmds = append(cmds, fmt.Sprintf("switchport access allowed vlan remove %s", removevlans))
						}
						if ethjoinvlan != "1" {
							cmds = append(cmds, fmt.Sprintf("switchport access allowed vlan add %s", ethjoinvlan))
						}
					}
					cmds = append(cmds, "end")
					cmds = append(cmds, "write memory")
					rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
						Username: req.Username,
						Password: req.Password,
						Maker:    req.Maker,
					})
					if err != nil {
						continue
					}
					if rpcResp.Error != nil {
						continue
					}
					infos := []map[string]interface{}{}
					if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
						continue
					}
					for _, info := range infos {
						if _, ok := info["errorCode"]; ok {
							continue
						}
					}
				} else {
					continue
				}
			}
			//TODO 目前只处理Access模式的修改，Trunk模式的修改较复杂，后续再完善
			//TODO 修改接口时需要考虑原有配置的影响，比如原来是Access模式，修改为Trunk模式时，需要先删除原有的Access配置，再添加Trunk配置；反之亦然；还需要考虑接口原来加入的VLAN和PVID配置，避免修改后接口不通
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Type"})
		return
	}
}

func (m *Module) handleVlanClassifier(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	hostInfo := r.FormValue("hostInfo")
	var req SwitchRequest
	if err := m.decodeEncryptedSwitchRequest(hostInfo, &req); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	if req.Port == 0 {
		req.Port = 80
	}
	if req.Format == "" {
		req.Format = "text"
	}
	nType, _ := strconv.Atoi(r.FormValue("nType"))
	nAct, _ := strconv.Atoi(r.FormValue("nAct"))
	switch nType {
	case 0: //查询VLAN分类规则列表
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				"show vlan classifier rule",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			//查询VLAN列表
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			var vc []VLANClassific
			for _, info := range infos {
				if len(info) == 0 {
					writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: []VLANClassific{}})
					return
				}
				sd := info["sourceDetails"]
				sdlist := strings.Split(sd.(string), "\n")
				for _, line := range sdlist {
					line = strings.TrimSpace(line)
					// 跳过空行
					if len(strings.TrimSpace(line)) == 0 {
						continue
					}
					fields := regexp.MustCompile(`\s+`).Split(line, -1)
					if len(fields) >= 8 {
						ruleId, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
						ruleType := strings.TrimSpace(fields[4])
						ruleContent := strings.TrimSpace(fields[5])
						vlanId, _ := strconv.Atoi(strings.TrimSpace(fields[7]))
						curvc := VLANClassific{
							RuleId:      ruleId,
							RuleType:    ruleType,
							RuleContent: ruleContent,
							VlanId:      vlanId,
						}
						vc = append(vc, curvc)
					}
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: vc})
		case a == 1 || a == 3:
			//添加/删除VLAN
			saveData := r.FormValue("saveData")
			var vc VLANClassific
			if err := json.Unmarshal([]byte(saveData), &vc); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			var upstr string
			if a == 1 {
				upstr = fmt.Sprintf("vlan classifier rule %d %s %s vlan %d", vc.RuleId, vc.RuleType, vc.RuleContent, vc.VlanId)
			} else {
				//TODO 删除规则时需要考虑规则ID不存在的情况，目前直接执行命令，后续可以先查询规则列表，如果规则ID不存在，则返回错误提示；如果规则ID存在，则执行删除命令
				//TODO 暂不支持批量删除规则，如果需要批量删除，可以在前端循环调用删除接口，或者在后端增加批量删除接口，接受一个规则ID列表，循环执行删除命令
				upstr = fmt.Sprintf("no vlan classifier rule %d", vc.RuleId)
			}
			cmds := []string{
				"configure terminal",
				upstr,
				"end",
				"write memory",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	case 1: //查询VLAN分类组列表
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				" show vlan classifier group",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			//查询VLAN列表
			infos := []map[string]interface{}{}
			var vc []VLANClassific
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if len(info) == 0 {
					writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: []VLANClassific{}})
					return
				}
				sd := info["sourceDetails"]
				sdlist := strings.Split(sd.(string), "\n")
				for _, line := range sdlist {
					line = strings.TrimSpace(line)
					// 跳过空行
					if len(strings.TrimSpace(line)) == 0 {
						continue
					}
					fields := regexp.MustCompile(`\s+`).Split(line, -1)
					if len(fields) >= 7 {
						groupId, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
						ruleId, _ := strconv.Atoi(strings.TrimSpace(fields[6]))
						curvc := VLANClassific{
							RuleId:  ruleId,
							GroupId: groupId,
						}
						vc = append(vc, curvc)
					}
				}

			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: vc})
		case a == 1 || a == 3:
			//添加/删除VLAN分类组
			saveData := r.FormValue("saveData")
			var vc VLANClassific
			if err := json.Unmarshal([]byte(saveData), &vc); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			var upstr string
			if a == 1 {
				// vlan classifier group 31 add rule 1
				upstr = fmt.Sprintf("vlan classifier group %d add rule %d", vc.GroupId, vc.RuleId)
			} else {
				//TODO 暂不支持批量删除规则，如果需要批量删除，可以在前端循环调用删除接口，或者在后端增加批量删除接口，接受一个规则ID列表，循环执行删除命令
				upstr = fmt.Sprintf("no vlan classifier group %d", vc.GroupId)
			}
			cmds := []string{
				"configure terminal",
				upstr,
				"end",
				"write memory",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	case 2: //查询VLAN分类用途列表
		switch a := nAct; {
		case a == 0:
			cmds := []string{
				"show vlan classifier interface group",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}

			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			//查询VLAN列表
			infos := []map[string]interface{}{}
			var vc []VLANClassific
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if len(info) == 0 {
					writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: []VLANClassific{}})
					return
				}
				sd := info["sourceDetails"]
				sdlist := strings.Split(sd.(string), "\n")
				for _, line := range sdlist {
					line = strings.TrimSpace(line)
					// 跳过空行
					if len(strings.TrimSpace(line)) == 0 {
						continue
					}
					fields := regexp.MustCompile(`\s+`).Split(line, -1)
					if len(fields) >= 9 {
						groupId, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
						ethPort := strings.TrimSpace(fields[6])
						ruleType := strings.TrimSpace(fields[8])
						curvc := VLANClassific{
							GroupId:  groupId,
							EthPort:  ethPort,
							RuleType: ruleType,
						}
						vc = append(vc, curvc)
					}
				}

			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: vc})
		case a == 1 || a == 3:
			//添加/删除VLAN分类组
			saveData := r.FormValue("saveData")
			var vc VLANClassific
			if err := json.Unmarshal([]byte(saveData), &vc); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			var upstr string
			if a == 1 {
				//vlan classifier activate 31 based protocol
				upstr = fmt.Sprintf("vlan classifier activate %d based %s", vc.GroupId, vc.RuleType)
			} else {
				//TODO 暂不支持批量删除规则，如果需要批量删除，可以在前端循环调用删除接口，或者在后端增加批量删除接口，接受一个规则ID列表，循环执行删除命令
				upstr = "no vlan classifier activate"
			}
			cmds := []string{
				"configure terminal",
				fmt.Sprintf("interface %s", vc.EthPort),
				upstr,
				"end",
				"write memory",
			}
			rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
				Username: req.Username,
				Password: req.Password,
				Maker:    req.Maker,
			})
			if err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			if rpcResp.Error != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message})
				return
			}
			infos := []map[string]interface{}{}
			if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
				writeJSON(w, http.StatusBadGateway, APIResponse{Code: -1, Message: "rpc error: " + err.Error()})
				return
			}
			for _, info := range infos {
				if val, ok := info["errorCode"]; ok {
					desc := info["errorDesc"].(string)
					writeJSON(w, http.StatusBadGateway, APIResponse{Code: int(val.(float64)), Message: desc})
					return
				}
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
			return
		}
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Type"})
		return
	}
}

// TODO 交换机接口设计结束

// serverPathPattern 匹配服务端绝对路径（以常见系统目录开头），用于避免把服务器目录信息返回给前端。
// 只匹配目录前缀，避免误伤类似 Ten-GigabitEthernet1/0/30 的正常文本。

// 端口 VLAN 执行模块：按厂商和连接方式执行端口 VLAN 查询与配置。
func (m *Module) handleSwitchVlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}

	nAct, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct: " + err.Error()})
		return
	}
	saveData := r.FormValue("saveData")
	var req SwitchRequest
	if strings.TrimSpace(saveData) != "" {
		if err := m.decodeEncryptedSwitchRequest(saveData, &req); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "传参错误" + err.Error()})
			return
		}
	} else {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "传参不能为空"})
		return
	}
	if nAct == 1 && GetSwitchMaker(req.Maker) == "centec" {
		m.handleCentecVLANList(w, req)
		return
	}
	if nAct == 4 && GetSwitchMaker(req.Maker) == "centec" {
		m.handleCentecVLANCreate(w, req)
		return
	}
	if nAct == 5 && GetSwitchMaker(req.Maker) == "centec" {
		m.handleCentecVLANDelete(w, req)
		return
	}
	if nAct == 6 && GetSwitchMaker(req.Maker) == "centec" {
		m.handleCentecVLANNameUpdate(w, req)
		return
	}
	if GetSwitchMaker(req.Maker) == "huawei" && (nAct == 1 || nAct == 4 || nAct == 5 || nAct == 6) {
		m.handleHuaweiDeviceVLAN(w, req, nAct)
		return
	}
	if GetSwitchMaker(req.Maker) == "h3c" && (nAct == 1 || nAct == 4 || nAct == 5 || nAct == 6) {
		m.handleH3CDeviceVLAN(w, req, nAct)
		return
	}
	if GetSwitchMaker(req.Maker) == "ruijie" && (nAct == 1 || nAct == 4 || nAct == 5 || nAct == 6) {
		m.handleRuijieDeviceVLAN(w, req, nAct)
		return
	}

	var vlanReq SwitchPortVlanRequest
	if nAct != 0 {
		if err := json.Unmarshal([]byte(saveData), &vlanReq); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid saveData: " + err.Error()})
			return
		}
	}
	if nAct == 0 {
		if err := json.Unmarshal([]byte(saveData), &vlanReq); err != nil {
			vlanReq.Port = strings.TrimSpace(r.FormValue("port"))
		}
	}
	if strings.TrimSpace(vlanReq.Port) == "" {
		vlanReq.Port = strings.TrimSpace(req.Interface)
	}
	if strings.TrimSpace(req.Vlan) != "" {
		if vlanReq.PVID == 0 {
			if pid, err := strconv.ParseInt(strings.TrimSpace(req.Vlan), 10, 64); err == nil {
				vlanReq.PVID = pid
			}
		}
		if strings.TrimSpace(vlanReq.TrunkVLANList) == "" {
			vlanReq.TrunkVLANList = strings.TrimSpace(req.Vlan)
		}
	}
	vlanReq.Port = normalizeSwitchPortName(vlanReq.Port)
	if nAct == 1 || nAct == 2 || nAct == 3 {
		applySwitchVlanAggregationTarget(&vlanReq)
	}

	csname := GetSwitchMaker(req.Maker)
	if vlanReq.Port == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "port is required"})
		return
	}

	switch csname {
	case "huawei":
		switch req.Type {
		case 2:
			m.handleSwitchVlanByTelnet(w, req, nAct, vlanReq)
		case 3:
			m.handleSwitchVlanBySSH(w, req, nAct, vlanReq)
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持Telnet或SSH方式"})
		}
	case "centec":
		switch req.Type {
		case 1:
			m.handleCentecSwitchVlanByRPC(w, req, nAct, vlanReq)
		case 2:
			m.handleCentecSwitchVlanByTelnet(w, req, nAct, vlanReq)
		case 3:
			m.handleCentecSwitchVlanBySSH(w, req, nAct, vlanReq)
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持RPC、Telnet或SSH方式"})
		}
	case "h3c":
		m.handleH3CSwitchVlan(w, req, nAct, vlanReq)
	case "ruijie":
		m.handleRuijieSwitchVlan(w, req, nAct, vlanReq)
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持华为、华三、盛科或锐捷交换机"})
	}
}

func (m *Module) handleCentecVLANList(w http.ResponseWriter, req SwitchRequest) {
	if strings.TrimSpace(req.Hostip) == "" || strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "交换机地址和登录账号不能为空"})
		return
	}
	if req.Format == "" {
		req.Format = "text"
	}

	var (
		output string
		err    error
	)
	switch req.Type {
	case 1:
		if req.Port == 0 {
			req.Port = 80
		}
		resp, rpcErr := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{"show vlan all"}, req.Format, SwitchLoginInfo{
			Username: req.Username,
			Password: req.Password,
			Maker:    req.Maker,
		})
		if rpcErr != nil {
			err = fmt.Errorf("rpc error: %v", rpcErr)
		} else if resp.Error != nil {
			err = fmt.Errorf("%s", resp.Error.Message)
		} else {
			output, err = extractSingleRPCSourceDetails(resp.Result)
		}
	case 2:
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			err = client.SendCommand("show vlan all")
			if err == nil {
				output, err = client.ReadPagedResponse(2*time.Second, []string{"---- More ----", "--More--", "More"})
			}
		}
	case 3:
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			output, err = client.RunInteractive([]string{"show vlan all"})
		}
	default:
		err = fmt.Errorf("当前仅支持RPC、Telnet或SSH方式")
	}
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "查询VLAN失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseCentecVLANAll(output)})
}

func (m *Module) handleCentecVLANCreate(w http.ResponseWriter, req SwitchRequest) {
	m.handleCentecVLANChange(w, req, false)
}

func (m *Module) handleCentecVLANDelete(w http.ResponseWriter, req SwitchRequest) {
	m.handleCentecVLANChange(w, req, true)
}

func (m *Module) handleCentecVLANNameUpdate(w http.ResponseWriter, req SwitchRequest) {
	if strings.TrimSpace(req.Hostip) == "" || strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "交换机地址和登录账号不能为空"})
		return
	}
	cmds, err := buildCentecVLANNameUpdateCommands(req.Vlan, req.VlanName)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
		return
	}
	if req.Format == "" {
		req.Format = "text"
	}
	switch req.Type {
	case 1:
		if req.Port == 0 {
			req.Port = 80
		}
		err = m.executeCentecRPCCommands(req, cmds)
	case 2:
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			var output strings.Builder
			for _, cmd := range cmds {
				if err = client.SendCommand(cmd); err != nil {
					break
				}
				var current string
				current, err = client.ReadPagedResponse(1200*time.Millisecond, []string{"---- More ----", "--More--", "More"})
				if err != nil {
					break
				}
				if err = appendVLANCommandOutput(&output, cmd, current); err != nil {
					break
				}
			}
			if err == nil && hasCentecCommandError(output.String()) {
				err = fmt.Errorf("更新VLAN名称失败")
			}
		}
	case 3:
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			var output string
			output, err = client.RunInteractiveWithOptions(cmds, InteractiveOptions{PromptSuffixes: []string{"#", ">"}, MorePatterns: []string{"---- More ----", "--More--", "More"}, CommandDelay: 1200, ReadTimeout: 1500 * time.Millisecond, AutoExit: true, StopOnErrorOutput: true})
			if err == nil && hasCentecCommandError(output) {
				err = fmt.Errorf("更新VLAN名称失败")
			}
		}
	default:
		err = fmt.Errorf("当前仅支持RPC、Telnet或SSH方式")
	}
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func buildCentecVLANNameUpdateCommands(vlan, name string) ([]string, error) {
	vlan = strings.TrimSpace(vlan)
	id, err := strconv.Atoi(vlan)
	if err != nil || id < 1 || id > 4094 || strings.ContainsAny(vlan, ",-") {
		return nil, fmt.Errorf("szVlan 必须是 1-4094 的单个 VLAN ID")
	}
	name = strings.TrimSpace(name)
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`).MatchString(name) {
		return nil, fmt.Errorf("szVlanName 仅支持 1-32 位字母、数字、点、下划线或连字符")
	}
	return []string{"configure terminal", "vlan database", fmt.Sprintf("vlan %d name %s state enable", id, name), "exit", "exit", "write memory"}, nil
}

func (m *Module) handleCentecVLANChange(w http.ResponseWriter, req SwitchRequest, remove bool) {
	action := "新增"
	if remove {
		action = "删除"
	}
	if strings.TrimSpace(req.Hostip) == "" || strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "交换机地址和登录账号不能为空"})
		return
	}
	cmds, err := buildCentecVLANChangeCommands(req.Vlan, remove)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
		return
	}
	if req.Format == "" {
		req.Format = "text"
	}

	switch req.Type {
	case 1:
		if req.Port == 0 {
			req.Port = 80
		}
		err = m.executeCentecRPCCommands(req, cmds)
	case 2:
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			var output strings.Builder
			for _, cmd := range cmds {
				if err = client.SendCommand(cmd); err != nil {
					break
				}
				var current string
				current, err = client.ReadPagedResponse(1200*time.Millisecond, []string{"---- More ----", "--More--", "More"})
				if err != nil {
					break
				}
				if err = appendVLANCommandOutput(&output, cmd, current); err != nil {
					break
				}
			}
			if err == nil && hasCentecCommandError(output.String()) {
				err = fmt.Errorf("%sVLAN失败", action)
			}
		}
	case 3:
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			var output string
			output, err = client.RunInteractiveWithOptions(cmds, InteractiveOptions{
				PromptSuffixes:    []string{"#", ">"},
				MorePatterns:      []string{"---- More ----", "--More--", "More"},
				CommandDelay:      1200,
				ReadTimeout:       1500 * time.Millisecond,
				AutoExit:          true,
				StopOnErrorOutput: true,
			})
			if err == nil && hasCentecCommandError(output) {
				err = fmt.Errorf("%sVLAN失败", action)
			}
		}
	default:
		err = fmt.Errorf("当前仅支持RPC、Telnet或SSH方式")
	}
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func buildCentecVLANCreateCommands(vlans string) ([]string, error) {
	return buildCentecVLANChangeCommands(vlans, false)
}

func buildCentecVLANDeleteCommands(vlans string) ([]string, error) {
	return buildCentecVLANChangeCommands(vlans, true)
}

func buildCentecVLANChangeCommands(vlans string, remove bool) ([]string, error) {
	if strings.TrimSpace(vlans) == "" {
		return nil, fmt.Errorf("szVlan 为必填字段")
	}
	parts := strings.Split(vlans, ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("szVlan 格式无效")
		}
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("szVlan 格式无效: %s", part)
		}
		start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
		if err != nil || start < 1 || start > 4094 {
			return nil, fmt.Errorf("VLAN ID 必须在 1-4094 之间: %s", part)
		}
		if len(bounds) == 2 {
			end, endErr := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if endErr != nil || end < start || end > 4094 {
				return nil, fmt.Errorf("VLAN 范围无效: %s", part)
			}
			normalized = append(normalized, fmt.Sprintf("%d-%d", start, end))
			continue
		}
		normalized = append(normalized, strconv.Itoa(start))
	}
	command := "vlan " + strings.Join(normalized, ",")
	if remove {
		command = "no " + command
	}
	return []string{"configure terminal", "vlan database", command, "exit", "exit", "write memory"}, nil
}

func extractSingleRPCSourceDetails(raw json.RawMessage) (string, error) {
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", fmt.Errorf("rpc error: %v", err)
	}
	for _, entry := range entries {
		if code, ok := entry["errorCode"]; ok {
			return "", fmt.Errorf("rpc command error: %v", code)
		}
		if source, ok := entry["sourceDetails"].(string); ok && strings.TrimSpace(source) != "" {
			return source, nil
		}
	}
	return "", fmt.Errorf("rpc response missing sourceDetails")
}

func parseCentecVLANAll(output string) []VLANInfo {
	rows := make([]VLANInfo, 0)
	headerDone := false
	rowPattern := regexp.MustCompile(`^\s*(\d+)\s+(\S+)\s+(\S+)\s+(\S+)(?:\s+(.*))?$`)
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !headerDone {
			if strings.HasPrefix(trimmed, "=======") {
				headerDone = true
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "switch#") {
			break
		}
		if match := rowPattern.FindStringSubmatch(line); len(match) == 6 {
			rows = append(rows, VLANInfo{
				VlanId:     match[1],
				Name:       match[2],
				Status:     match[3],
				STPID:      match[4],
				MemberPort: strings.TrimSpace(match[5]),
			})
			continue
		}
		if len(rows) > 0 && (len(line) > 0 && (line[0] == ' ' || line[0] == '\t')) {
			members := strings.TrimSpace(line)
			if members != "" {
				rows[len(rows)-1].MemberPort = strings.TrimSpace(rows[len(rows)-1].MemberPort + " " + members)
			}
		}
	}
	return rows
}

func (m *Module) getTerminalMAC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}

	saveData := r.FormValue("saveData")
	if strings.TrimSpace(saveData) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "传参不能为空"})
		return
	}

	var req SwitchRequest
	if err := m.decodeEncryptedSwitchRequest(saveData, &req); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid saveData: " + err.Error()})
		return
	}

	if strings.TrimSpace(req.Hostip) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "szHostip is required"})
		return
	}
	if strings.TrimSpace(req.Vlan) == "" && strings.TrimSpace(req.Interface) == "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "szVlan or szInterface is required"})
		return
	}

	switch GetSwitchMaker(req.Maker) {
	case "huawei", "h3c", "ruijie":
		switch req.Type {
		case 2:
			m.handleTerminalMACByTelnet(w, req)
		case 3:
			m.handleTerminalMACBySSH(w, req)
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持TELNET或SSH方式"})
		}
	case "centec":
		switch req.Type {
		case 1:
			m.handleCentecTerminalMACByRPC(w, req)
		case 2:
			m.handleCentecTerminalMACByTelnet(w, req)
		case 3:
			m.handleCentecTerminalMACBySSH(w, req)
		default:
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持RPC、TELNET或SSH方式"})
		}
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "当前仅支持华为、华三、盛科或锐捷交换机"})
	}
}

func (m *Module) handleRuijieDeviceVLAN(w http.ResponseWriter, req SwitchRequest, nAct int) {
	if req.Type != 2 && req.Type != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "锐捷设备 VLAN 管理当前仅支持 Telnet 或 SSH"})
		return
	}
	commands := []string{"show vlan"}
	if nAct != 1 {
		var err error
		commands, err = buildRuijieDeviceVLANCommands(nAct, req.Vlan, req.VlanName)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
	}

	output, err := m.runRuijieVLANCommands(req, commands, nAct != 1)
	if err != nil {
		message := "锐捷 VLAN 查询失败: " + err.Error()
		if nAct != 1 {
			message = vlanSettingFailureMessage(err)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
		return
	}
	if nAct == 1 {
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseRuijieVLANDisplay(output)})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func (m *Module) runRuijieVLANCommands(req SwitchRequest, commands []string, mutation bool) (string, error) {
	morePatterns := []string{"---- More ----", "--More--", "More"}
	var output string
	var err error
	if req.Type == 2 {
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			if mutation {
				output, err = runHuaweiConfigCommandsByTelnet(client, commands, 1200*time.Millisecond, morePatterns)
			} else {
				output, err = runTelnetCommandWithPaging(client, commands[0], morePatterns, 2*time.Second)
			}
		}
	} else {
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			return "", connectErr
		}
		defer client.Close()
		options := InteractiveOptions{
			PromptSuffixes:    []string{"#", ">"},
			MorePatterns:      morePatterns,
			CommandDelay:      1200 * time.Millisecond,
			ReadTimeout:       1500 * time.Millisecond,
			AutoConfirm:       mutation,
			AutoExit:          false,
			StopOnErrorOutput: mutation,
		}
		output, err = client.RunInteractiveWithOptions(commands, options)
	}
	if err != nil {
		return output, err
	}
	if mutation {
		if detail := matchSwitchCollectionCommandError(output); detail != "" {
			return output, fmt.Errorf("%s", detail)
		}
	}
	return output, nil
}

func buildRuijieDeviceVLANCommands(nAct int, vlans, name string) ([]string, error) {
	ids, err := expandRuijieVLANIDs(vlans)
	if err != nil {
		return nil, err
	}
	commands := []string{"configure terminal"}
	switch nAct {
	case 4:
		for _, id := range ids {
			commands = append(commands, fmt.Sprintf("vlan %d", id), "exit")
		}
		return append(commands, "exit", "write"), nil
	case 5:
		for _, id := range ids {
			commands = append(commands, fmt.Sprintf("no vlan %d", id))
		}
		return append(commands, "exit", "write"), nil
	case 6:
		if len(ids) != 1 {
			return nil, fmt.Errorf("修改 VLAN 名称需要单个 VLAN ID 和名称")
		}
		name = strings.TrimSpace(name)
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`).MatchString(name) {
			return nil, fmt.Errorf("szVlanName 仅支持 1-32 位字母、数字、点、下划线或连字符")
		}
		return append(commands,
			fmt.Sprintf("vlan %d", ids[0]),
			"name "+name,
			"exit",
			"exit",
			"write",
		), nil
	default:
		return nil, fmt.Errorf("不支持的 VLAN 操作")
	}
}

func expandRuijieVLANIDs(value string) ([]int, error) {
	if !validateVLANList(value) {
		return nil, fmt.Errorf("VLAN ID 必须为 1-4094 的单个数字或逗号/范围组合")
	}
	ids := make([]int, 0)
	seen := make(map[int]struct{})
	for _, item := range strings.Split(strings.ReplaceAll(value, " ", ""), ",") {
		bounds := strings.SplitN(item, "-", 2)
		start, _ := strconv.Atoi(bounds[0])
		end := start
		if len(bounds) == 2 {
			end, _ = strconv.Atoi(bounds[1])
		}
		for id := start; id <= end; id++ {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func parseRuijieVLANDisplay(output string) []VLANInfo {
	rows := make([]VLANInfo, 0)
	rowPattern := regexp.MustCompile(`(?i)^\s*(\d{1,4}(?:-\d{1,4})?)\s+(\S+)\s+(STATIC|DYNAMIC|VXLAN|ACTIVE|INACTIVE|ENABLED|DISABLED)\s*(.*)$`)
	vlanIDPattern := regexp.MustCompile(`(?i)^VLAN\s+(?:ID|Identifier)\s*:\s*(\d{1,4})\s*$`)
	namePattern := regexp.MustCompile(`(?i)^Name\s*:\s*(.+)$`)
	statusPattern := regexp.MustCompile(`(?i)^Status\s*:\s*(.+)$`)
	memberPattern := regexp.MustCompile(`(?i)^(?:Member\s+ports?|Ports?|Tagged\s+ports?|Untagged\s+ports?)\s*:\s*(.*)$`)
	var current *VLANInfo
	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "===") {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "show vlan") || (strings.Contains(lower, "vlan") && strings.Contains(lower, "name") && strings.Contains(lower, "status")) {
			continue
		}
		if strings.HasSuffix(trimmed, "#") || strings.HasSuffix(trimmed, ">") {
			continue
		}
		if match := rowPattern.FindStringSubmatch(line); len(match) == 5 {
			if !validateVLANList(match[1]) {
				continue
			}
			mode := 0
			if strings.Contains(match[1], "-") {
				mode = 1
			}
			rows = append(rows, VLANInfo{
				Mode:       mode,
				VlanId:     match[1],
				Name:       match[2],
				Status:     strings.ToUpper(match[3]),
				MemberPort: strings.TrimSpace(match[4]),
			})
			current = &rows[len(rows)-1]
			continue
		}
		if match := vlanIDPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			rows = append(rows, VLANInfo{VlanId: match[1]})
			current = &rows[len(rows)-1]
			continue
		}
		if current == nil {
			continue
		}
		if match := namePattern.FindStringSubmatch(trimmed); len(match) == 2 {
			current.Name = strings.TrimSpace(match[1])
			continue
		}
		if match := statusPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			current.Status = strings.TrimSpace(match[1])
			continue
		}
		if match := memberPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			current.MemberPort = strings.TrimSpace(strings.TrimSpace(current.MemberPort + " " + match[1]))
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			current.MemberPort = strings.TrimSpace(current.MemberPort + " " + trimmed)
		}
	}
	return rows
}

func (m *Module) handleRuijieSwitchVlan(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Type != 2 && req.Type != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "锐捷接口 VLAN 管理当前仅支持 Telnet 或 SSH"})
		return
	}
	if nAct != 0 && nAct != 1 && nAct != 2 && nAct != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
		return
	}

	var commands []string
	if nAct == 0 {
		commands = []string{"show interface switchport"}
	} else {
		var err error
		if nAct == 3 {
			commands, err = buildRuijieSwitchVLANDeleteCommands(vlanReq)
		} else {
			commands, err = buildRuijieSwitchVLANCommands(vlanReq)
		}
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
	}

	morePatterns := []string{"---- More ----", "--More--", "More"}
	var output string
	var err error
	if req.Type == 2 {
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			if nAct == 0 {
				output, err = runTelnetCommandWithPaging(client, commands[0], morePatterns, 2*time.Second)
			} else {
				output, err = runHuaweiConfigCommandsByTelnet(client, commands, 1200*time.Millisecond, morePatterns)
			}
		}
	} else {
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			options := InteractiveOptions{
				PromptSuffixes: []string{"#", ">"},
				MorePatterns:   morePatterns,
				CommandDelay:   1200 * time.Millisecond,
				ReadTimeout:    1500 * time.Millisecond,
				AutoExit:       false,
			}
			if nAct != 0 {
				options.AutoConfirm = true
				options.StopOnErrorOutput = true
			}
			output, err = client.RunInteractiveWithOptions(commands, options)
		}
	}
	if err != nil {
		message := "锐捷接口 VLAN 查询失败: " + err.Error()
		if nAct != 0 {
			message = vlanSettingFailureMessage(err)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
		return
	}
	if nAct == 0 {
		if info, ok := findSwitchPortVlan(ParseRuijieSwitchportVlan(output), vlanReq.Port); ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: info})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
		return
	}
	if detail := matchSwitchCollectionCommandError(output); detail != "" {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(fmt.Errorf("%s", detail))})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func buildRuijieSwitchVLANCommands(req SwitchPortVlanRequest) ([]string, error) {
	return buildRuijieSwitchVLANMembershipCommands(req, false)
}

func buildRuijieSwitchVLANDeleteCommands(req SwitchPortVlanRequest) ([]string, error) {
	return buildRuijieSwitchVLANMembershipCommands(req, true)
}

func buildRuijieSwitchVLANMembershipCommands(req SwitchPortVlanRequest, remove bool) ([]string, error) {
	port := formatRuijiePortNameForCommand(req.Port)
	if port == "" {
		return nil, fmt.Errorf("port is required")
	}
	if req.PVID < 1 || req.PVID > 4094 {
		return nil, fmt.Errorf("PVID 必须为 1-4094")
	}
	membershipCommand := fmt.Sprintf("add interface %s", port)
	if remove {
		membershipCommand = "no " + membershipCommand
	}
	return []string{
		"configure terminal",
		fmt.Sprintf("vlan %d", req.PVID),
		membershipCommand,
		"exit",
		"exit",
		"write",
	}, nil
}

func formatRuijiePortNameForCommand(port string) string {
	port = normalizeRuijiePortName(port)
	if portType, portNumber, ok := splitSwitchPortTypeAndNumber(port); ok {
		return portType + " " + portNumber
	}
	return port
}

// H3C VLAN management uses Comware-specific commands while retaining the
// same action contract as Huawei devices.

// H3C VLAN management uses Comware-specific commands while retaining the
// same action contract as Huawei devices.
func (m *Module) handleH3CSwitchVlan(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Type != 2 && req.Type != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "H3C 接口 VLAN 管理当前仅支持 Telnet 或 SSH"})
		return
	}
	var output string
	var err error
	morePatterns := []string{"---- More ----", "--More--", "More"}
	if req.Type == 2 {
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			if nAct == 0 {
				output, err = runTelnetCommandWithPaging(client, h3cDisplayInterfaceCommand(vlanReq.Port), morePatterns, 2*time.Second)
			} else {
				cmds, buildErr := buildH3CSwitchVLANCommands(nAct, vlanReq, SwitchVlanMapping{})
				if buildErr != nil {
					err = buildErr
				} else {
					output, err = runHuaweiConfigCommandsByTelnet(client, cmds, 1200*time.Millisecond, morePatterns)
				}
			}
		}
	} else {
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			if nAct == 0 {
				output, err = client.RunInteractiveWithOptions([]string{h3cDisplayInterfaceCommand(vlanReq.Port)}, h3cSSHInteractiveOptions(false))
			} else {
				cmds, buildErr := buildH3CSwitchVLANCommands(nAct, vlanReq, SwitchVlanMapping{})
				if buildErr != nil {
					err = buildErr
				} else {
					output, err = client.RunInteractiveWithOptions(cmds, h3cSSHInteractiveOptions(true))
				}
			}
		}
	}
	if err != nil {
		message := "H3C 接口 VLAN 操作失败: " + err.Error()
		if nAct != 0 {
			message = vlanSettingFailureMessage(err)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
		return
	}
	if nAct == 0 {
		if info, ok := findH3CInterfaceDetailVlan(output, vlanReq.Port); ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: info})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
		return
	}
	if hasHuaweiCommandError(output) {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "H3C 接口 VLAN 操作失败", Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func (m *Module) handleSwitchVlanBySSH(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Port == 0 {
		req.Port = 22
	}
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
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error() + ",请重新连接"})
		return
	}
	defer client.Close()

	switch nAct {
	case 0:
		output, err := client.RunInteractive([]string{"display port vlan"})
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error()})
			return
		}
		vlanMap := ParseHuaweiPortVlan(output)
		if info, ok := findSwitchPortVlan(vlanMap, vlanReq.Port); ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: info})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
	case 1, 2:
		current := SwitchVlanMapping{}
		if nAct == 2 {
			current, _ = m.queryHuaweiSwitchPortVlanBySSH(client, vlanReq.Port)
		}
		cmds, err := buildHuaweiSwitchVlanUpsertCommandsForCurrent(vlanReq, current)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		output, err := runHuaweiConfigCommandsBySSH(client, cmds)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasHuaweiCommandError(output) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN配置失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	case 3:
		current, _ := m.queryHuaweiSwitchPortVlanBySSH(client, vlanReq.Port)
		cmds := buildHuaweiSwitchVlanDeleteCommands(current)
		output, err := runHuaweiConfigCommandsBySSH(client, cmds)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasHuaweiCommandError(output) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN删除失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

// handleHuaweiDeviceVLAN handles the device-level VLAN list and batch mutations.

// handleHuaweiDeviceVLAN handles the device-level VLAN list and batch mutations.
func (m *Module) handleHuaweiDeviceVLAN(w http.ResponseWriter, req SwitchRequest, nAct int) {
	if req.Type != 2 && req.Type != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "华为设备级 VLAN 管理当前仅支持 Telnet 或 SSH"})
		return
	}
	morePatterns := []string{"---- More ----", "--More--", "More"}
	if nAct == 1 {
		var output string
		var runErr error
		if req.Type == 2 {
			if req.Port == 0 {
				req.Port = 23
			}
			client := &TelnetClient{
				owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
			if runErr = client.SwitchConnect(req.Username, req.Password); runErr == nil {
				defer client.Conn.Close()
				if runErr = client.SendCommand("display vlan"); runErr == nil {
					output, runErr = client.ReadPagedResponse(2*time.Second, morePatterns)
				}
			}
		} else {
			if req.Port == 0 {
				req.Port = 22
			}
			client, connectErr := m.ConnectWithLoginInfo(req.Hostip, req.Port, req.Username, req.Password, []string{"mlkem768x25519-sha256", "curve25519-sha256", "diffie-hellman-group14-sha1"}, SwitchLoginInfo{Username: req.Username, Password: req.Password, Maker: req.Maker})
			if connectErr != nil {
				runErr = connectErr
			} else {
				defer client.Close()
				output, runErr = client.RunInteractive([]string{"display vlan"})
			}
		}
		if runErr != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + runErr.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseHuaweiVLANDisplay(output)})
		return
	}
	cmds, buildErr := buildHuaweiDeviceVLANCommands(nAct, req.Vlan, req.VlanName)
	if buildErr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: buildErr.Error()})
		return
	}
	var output string
	var runErr error
	if req.Type == 2 {
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if runErr = client.SwitchConnect(req.Username, req.Password); runErr == nil {
			defer client.Conn.Close()
			output, runErr = runHuaweiConfigCommandsByTelnet(client, cmds, 1200*time.Millisecond, morePatterns)
		}
	} else {
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.ConnectWithLoginInfo(req.Hostip, req.Port, req.Username, req.Password, []string{"mlkem768x25519-sha256", "curve25519-sha256", "diffie-hellman-group14-sha1"}, SwitchLoginInfo{Username: req.Username, Password: req.Password, Maker: req.Maker})
		if connectErr != nil {
			runErr = connectErr
		} else {
			defer client.Close()
			output, runErr = client.RunInteractiveWithOptions(cmds, InteractiveOptions{
				MorePatterns:      morePatterns,
				PromptSuffixes:    []string{">", "#", "]"},
				CommandDelay:      300 * time.Millisecond,
				ReadTimeout:       3 * time.Second,
				WaitAfterCommands: 200 * time.Millisecond,
				AutoConfirm:       true,
				StopOnErrorOutput: true,
			})
		}
	}
	if runErr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(runErr)})
		return
	}
	if hasHuaweiCommandError(output) {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "VLAN 操作失败", Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func (m *Module) handleH3CDeviceVLAN(w http.ResponseWriter, req SwitchRequest, nAct int) {
	if req.Type != 2 && req.Type != 3 {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "H3C 设备级 VLAN 管理当前仅支持 Telnet 或 SSH"})
		return
	}
	commands := h3cDeviceVLANListCommands()
	if nAct != 1 {
		var err error
		commands, err = buildH3CDeviceVLANCommands(nAct, req.Vlan, req.VlanName)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
	}
	morePatterns := []string{"---- More ----", "--More--", "More"}
	var output string
	var err error
	if req.Type == 2 {
		if req.Port == 0 {
			req.Port = 23
		}
		client := &TelnetClient{
			owner: m, Host: req.Hostip, Port: req.Port, Maker: req.Maker, Timeout: 30 * time.Second}
		if err = client.SwitchConnect(req.Username, req.Password); err == nil {
			defer client.Conn.Close()
			if nAct == 1 {
				output, err = runHuaweiConfigCommandsByTelnet(client, commands, 1200*time.Millisecond, morePatterns)
			} else {
				output, err = runHuaweiConfigCommandsByTelnet(client, commands, 1200*time.Millisecond, morePatterns)
			}
		}
	} else {
		if req.Port == 0 {
			req.Port = 22
		}
		client, connectErr := m.connectSwitchSSHClient(req)
		if connectErr != nil {
			err = connectErr
		} else {
			defer client.Close()
			if nAct == 1 {
				output, err = client.RunInteractiveWithOptions(commands, h3cDeviceVLANListOptions())
			} else {
				output, err = client.RunInteractiveWithOptions(commands, h3cSSHInteractiveOptions(true))
			}
		}
	}
	if err != nil {
		message := "H3C VLAN 操作失败: " + err.Error()
		if nAct != 1 {
			message = vlanSettingFailureMessage(err)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: message})
		return
	}
	if hasHuaweiCommandError(output) {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "H3C VLAN 操作失败", Data: output})
		return
	}
	if nAct == 1 {
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: parseH3CVLANDisplay(output)})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
}

func h3cDeviceVLANListCommands() []string {
	return []string{"system-view", "display vlan brief"}
}

func h3cDeviceVLANListOptions() InteractiveOptions {
	return h3cSSHInteractiveOptions(false)
}

func h3cSSHInteractiveOptions(autoConfirm bool) InteractiveOptions {
	return InteractiveOptions{
		MorePatterns:      []string{"---- More ----", "--More--", "More"},
		PromptSuffixes:    []string{">", "#", "]"},
		CommandDelay:      300 * time.Millisecond,
		ReadTimeout:       3 * time.Second,
		AutoConfirm:       autoConfirm,
		StopOnErrorOutput: autoConfirm,
		// One quit only returns [H3C] to <H3C>, then session.Wait blocks until timeout.
		// Close the local channel after the output reaches the prompt instead.
		AutoExit: false,
	}
}

func buildH3CDeviceVLANCommands(nAct int, vlans, name string) ([]string, error) {
	if !validateVLANList(vlans) {
		return nil, fmt.Errorf("VLAN ID 必须为 1-4094 的单个数字或逗号/范围组合")
	}
	values := strings.ReplaceAll(normalizeVLANBatch(vlans), " to ", " to ")
	if nAct == 4 {
		return []string{"system-view", "vlan " + values, "quit", "save force"}, nil
	}
	if nAct == 5 {
		return []string{"system-view", "undo vlan " + values, "quit", "save force"}, nil
	}
	if nAct == 6 {
		if strings.ContainsAny(vlans, ",- ") || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("修改 VLAN 名称需要单个 VLAN ID 和名称")
		}
		return []string{"system-view", "vlan " + strings.TrimSpace(vlans), "name " + strings.TrimSpace(name), "quit", "quit", "save force"}, nil
	}
	return nil, fmt.Errorf("不支持的 VLAN 操作")
}

func parseH3CVLANDisplay(output string) []VLANInfo {
	rows := []VLANInfo{}
	var current *VLANInfo
	inBriefTable := false
	briefPortColumn := -1
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToLower(line), "vlan id") && strings.Contains(strings.ToLower(line), "name") {
			inBriefTable = true
			briefPortColumn = strings.Index(strings.ToLower(raw), "port")
			continue
		}
		if inBriefTable {
			// Comware prompts such as [H3C] are emitted after the final VLAN row;
			// they are not continuation member-port lines.
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				continue
			}
			if match := regexp.MustCompile(`^\s*(\d+)\s+`).FindStringSubmatch(raw); len(match) == 2 {
				if id, parseErr := strconv.Atoi(match[1]); parseErr == nil && id >= 1 && id <= 4094 {
					nameAndPort := raw[len(match[0]):]
					name := strings.TrimSpace(nameAndPort)
					memberPort := ""
					if briefPortColumn > len(match[0]) && len(raw) > briefPortColumn {
						name = strings.TrimSpace(raw[len(match[0]):briefPortColumn])
						memberPort = strings.TrimSpace(raw[briefPortColumn:])
					}
					rows = append(rows, VLANInfo{VlanId: match[1], Name: name, MemberPort: memberPort})
					current = &rows[len(rows)-1]
					continue
				}
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if id, parseErr := strconv.Atoi(fields[0]); parseErr == nil && id >= 1 && id <= 4094 {
					name := fields[1]
					memberPorts := strings.Join(fields[2:], " ")
					rows = append(rows, VLANInfo{VlanId: fields[0], Name: name, MemberPort: memberPorts})
					current = &rows[len(rows)-1]
					continue
				}
			}
			if current != nil && line != "" && !strings.HasPrefix(line, "<") {
				if current.MemberPort != "" {
					current.MemberPort += " "
				}
				current.MemberPort += line
			}
			continue
		}
		if match := regexp.MustCompile(`(?i)^VLAN\s+ID\s*:\s*(\d+)`).FindStringSubmatch(line); len(match) == 2 {
			rows = append(rows, VLANInfo{VlanId: match[1]})
			current = &rows[len(rows)-1]
			continue
		}
		if current != nil {
			if match := regexp.MustCompile(`(?i)^(?:Name|Description)\s*:\s*(.+)$`).FindStringSubmatch(line); len(match) == 2 {
				current.Name = strings.TrimSpace(match[1])
			}
		}
	}
	return rows
}

func buildHuaweiDeviceVLANCommands(nAct int, vlans, name string) ([]string, error) {
	if !validateVLANList(vlans) {
		return nil, fmt.Errorf("VLAN ID 必须为 1-4094 的单个数字或逗号/范围组合")
	}
	if nAct == 4 {
		vlanCommand := "vlan " + strings.TrimSpace(vlans)
		if strings.Contains(vlans, ",") || strings.Contains(vlans, "-") {
			vlanCommand = "vlan batch " + normalizeVLANBatch(vlans)
			return []string{"system-view", vlanCommand, "commit", "quit", "save"}, nil
		}
		return []string{"system-view", vlanCommand, "commit", "quit", "quit", "save"}, nil
	}
	if nAct == 5 {
		vlanCommand := "undo vlan " + strings.TrimSpace(vlans)
		if strings.Contains(vlans, ",") || strings.Contains(vlans, "-") {
			vlanCommand = "undo vlan batch " + normalizeVLANBatch(vlans)
		}
		return []string{"system-view", vlanCommand, "commit", "quit", "save"}, nil
	}
	if nAct == 6 {
		if strings.Contains(vlans, ",") || strings.Contains(vlans, "-") || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("修改 VLAN 名称需要单个 VLAN ID 和名称")
		}
		return []string{"system-view", "vlan " + strings.TrimSpace(vlans), "description " + strings.TrimSpace(name), "quit", "quit", "save"}, nil
	}
	return nil, fmt.Errorf("不支持的 VLAN 操作")
}

func normalizeVLANBatch(value string) string {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), ",")
	formatted := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			formatted = append(formatted, rangeParts[0]+" to "+rangeParts[1])
		} else if part != "" {
			formatted = append(formatted, part)
		}
	}
	return strings.Join(formatted, " ")
}

func validateVLANList(value string) bool {
	for _, part := range strings.Split(strings.ReplaceAll(value, " ", ""), ",") {
		bits := strings.Split(part, "-")
		if len(bits) > 2 || part == "" {
			return false
		}
		start, err := strconv.Atoi(bits[0])
		if err != nil || start < 1 || start > 4094 {
			return false
		}
		end := start
		if len(bits) == 2 {
			end, err = strconv.Atoi(bits[1])
			if err != nil || end < start || end > 4094 {
				return false
			}
		}
	}
	return strings.TrimSpace(value) != ""
}

func parseHuaweiVLANDisplay(output string) []VLANInfo {
	rows := []VLANInfo{}
	byID := map[string]int{}
	detailPattern := regexp.MustCompile(`^\s*(\d+)\s+\S+\s+(\S+)\s+\S+\s+\S+\s+\S+\s+\S+\s+\S+\s+\S+\s*(.*)$`)
	inPorts := false
	inDesc := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "VID") && strings.Contains(trim, "Ports") {
			inPorts = true
			inDesc = false
			continue
		}
		if strings.HasPrefix(trim, "VID  Type") {
			inPorts = false
			inDesc = true
			continue
		}
		if !inPorts && !inDesc {
			continue
		}
		if strings.HasPrefix(trim, "---") || trim == "" || strings.HasPrefix(trim, "<") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) == 0 || !regexp.MustCompile(`^\d+$`).MatchString(fields[0]) {
			if inPorts && len(rows) > 0 && strings.TrimSpace(line) != "" {
				rows[len(rows)-1].MemberPort = strings.TrimSpace(rows[len(rows)-1].MemberPort + " " + trim)
			}
			continue
		}
		id := fields[0]
		if inDesc {
			match := detailPattern.FindStringSubmatch(line)
			if len(match) != 4 {
				continue
			}
			if index, ok := byID[match[1]]; ok {
				rows[index].Status = match[2]
				desc := strings.TrimSpace(match[3])
				if rows[index].Name == "" {
					rows[index].Name = desc
				}
			}
			continue
		}
		row := VLANInfo{VlanId: id, MemberPort: strings.TrimSpace(strings.TrimPrefix(trim, id))}
		rows = append(rows, row)
		byID[id] = len(rows) - 1
	}
	return rows
}

func (m *Module) handleSwitchVlanByTelnet(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Port == 0 {
		req.Port = 23
	}
	client := &TelnetClient{
		owner:   m,
		Host:    req.Hostip,
		Port:    req.Port,
		Maker:   req.Maker,
		Timeout: 30 * time.Second,
	}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Connection failed: " + err.Error()})
		return
	}
	defer client.Conn.Close()

	morePatterns := []string{"---- More ----", "--More--", "More"}
	switch nAct {
	case 0:
		client.SendCommand("display port vlan")
		output, err := client.ReadPagedResponse(1*time.Second, morePatterns)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error()})
			return
		}
		vlanMap := ParseHuaweiPortVlan(output)
		if info, ok := findSwitchPortVlan(vlanMap, vlanReq.Port); ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: info})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
	case 1, 2:
		current := SwitchVlanMapping{}
		if nAct == 2 {
			current, _ = m.queryHuaweiSwitchPortVlanByTelnet(client, vlanReq.Port, morePatterns)
		}
		cmds, err := buildHuaweiSwitchVlanUpsertCommandsForCurrent(vlanReq, current)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		output, err := runHuaweiConfigCommandsByTelnet(client, cmds, 1200*time.Millisecond, morePatterns)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasHuaweiCommandError(output) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN配置失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	case 3:
		current, _ := m.queryHuaweiSwitchPortVlanByTelnet(client, vlanReq.Port, morePatterns)
		cmds := buildHuaweiSwitchVlanDeleteCommands(current)
		output, err := runHuaweiConfigCommandsByTelnet(client, cmds, 1200*time.Millisecond, morePatterns)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasHuaweiCommandError(output) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN删除失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) handleCentecSwitchVlanByRPC(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Port == 0 {
		req.Port = 80
	}
	if req.Format == "" {
		req.Format = "text"
	}
	switch nAct {
	case 0:
		current, ok, err := m.queryCentecSwitchPortVlanByRPC(req, vlanReq.Port)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
			return
		}
		if aggregate, err := m.queryCentecChannelGroupByRPC(req, vlanReq.Port); err == nil {
			current.AggregationPort = aggregate
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: current})
	case 1, 2:
		if err := m.resolveCentecVlanTargetByRPC(&vlanReq, req); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		cmds, err := buildCentecSwitchVlanUpsertCommands(vlanReq)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		if err := m.executeCentecRPCCommands(req, cmds); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	case 3:
		if err := m.resolveCentecVlanTargetByRPC(&vlanReq, req); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		current, _, err := m.queryCentecSwitchPortVlanByRPC(req, vlanReq.Port)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		cmds := buildCentecSwitchVlanDeleteCommands(current)
		if err := m.executeCentecRPCCommands(req, cmds); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) handleCentecSwitchVlanBySSH(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Port == 0 {
		req.Port = 22
	}
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
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error() + ",请重新连接"})
		return
	}
	defer client.Close()
	options := InteractiveOptions{
		PromptSuffixes: []string{"#", ">"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   1200,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       true,
	}

	switch nAct {
	case 0:
		current, ok := queryCentecSwitchPortVlanBySSHWithOptions(client, vlanReq.Port, options)
		if !ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
			return
		}
		if aggregate, err := queryCentecChannelGroupBySSH(client, vlanReq.Port, options); err == nil {
			current.AggregationPort = aggregate
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: current})
	case 1, 2:
		if err := resolveCentecVlanTargetBySSH(&vlanReq, client, options); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		cmds, err := buildCentecSwitchVlanUpsertCommands(vlanReq)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		configOptions := options
		configOptions.StopOnErrorOutput = true
		output, err := client.RunInteractiveWithOptions(cmds, configOptions)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasCentecCommandError(output) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN配置失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	case 3:
		if err := resolveCentecVlanTargetBySSH(&vlanReq, client, options); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		current, _ := queryCentecSwitchPortVlanBySSHWithOptions(client, vlanReq.Port, options)
		cmds := buildCentecSwitchVlanDeleteCommands(current)
		configOptions := options
		configOptions.StopOnErrorOutput = true
		output, err := client.RunInteractiveWithOptions(cmds, configOptions)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err)})
			return
		}
		if hasCentecCommandError(output) {
			if cleared, ok := queryCentecSwitchPortVlanBySSHWithOptions(client, vlanReq.Port, options); ok && isCentecSwitchVlanCleared(cleared) {
				writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
				return
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN删除失败", Data: output})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) handleCentecSwitchVlanByTelnet(w http.ResponseWriter, req SwitchRequest, nAct int, vlanReq SwitchPortVlanRequest) {
	if req.Port == 0 {
		req.Port = 23
	}
	client := &TelnetClient{
		owner:   m,
		Host:    req.Hostip,
		Port:    req.Port,
		Maker:   req.Maker,
		Timeout: 30 * time.Second,
	}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Connection failed: " + err.Error()})
		return
	}
	defer client.Conn.Close()

	morePatterns := []string{"---- More ----", "--More--", "More"}
	switch nAct {
	case 0:
		current, ok := queryCentecSwitchPortVlanByTelnet(client, vlanReq.Port, morePatterns)
		if !ok {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "未找到接口VLAN信息", Data: SwitchVlanMapping{Port: vlanReq.Port}})
			return
		}
		if aggregate, err := queryCentecChannelGroupByTelnet(client, vlanReq.Port, morePatterns); err == nil {
			current.AggregationPort = aggregate
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: current})
	case 1, 2:
		if err := resolveCentecVlanTargetByTelnet(&vlanReq, client, morePatterns); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		cmds, err := buildCentecSwitchVlanUpsertCommands(vlanReq)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		var output strings.Builder
		for _, cmd := range cmds {
			if err := client.SendCommand(cmd); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "发送命令失败: " + err.Error()})
				return
			}
			cur, err := client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
			if err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error()})
				return
			}
			if err := appendVLANCommandOutput(&output, cmd, cur); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err), Data: output.String()})
				return
			}
		}
		if hasCentecCommandError(output.String()) {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN配置失败", Data: output.String()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	case 3:
		if err := resolveCentecVlanTargetByTelnet(&vlanReq, client, morePatterns); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "获取接口聚合信息失败", Data: err.Error()})
			return
		}
		current, _ := queryCentecSwitchPortVlanByTelnet(client, vlanReq.Port, morePatterns)
		cmds := buildCentecSwitchVlanDeleteCommands(current)
		var output strings.Builder
		for _, cmd := range cmds {
			if err := client.SendCommand(cmd); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "发送命令失败: " + err.Error()})
				return
			}
			cur, err := client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
			if err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error()})
				return
			}
			if err := appendVLANCommandOutput(&output, cmd, cur); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: vlanSettingFailureMessage(err), Data: output.String()})
				return
			}
		}
		if hasCentecCommandError(output.String()) {
			if cleared, ok := queryCentecSwitchPortVlanByTelnet(client, vlanReq.Port, morePatterns); ok && isCentecSwitchVlanCleared(cleared) {
				writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
				return
			}
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "接口VLAN删除失败", Data: output.String()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

// 厂商适配模块：封装不同厂商在 Telnet / SSH / RPC 下的接口采集差异。

// 厂商适配模块：封装不同厂商在 Telnet / SSH / RPC 下的接口采集差异。
func queryCentecInterfaceInfoByTelnet(client *TelnetClient, morePatterns []string) ([]SwitchInterfaceInfo, string, error) {
	versionOutput, err := runTelnetCommandWithPaging(client, "show version", morePatterns, 1500*time.Millisecond)
	if err != nil {
		return nil, "", fmt.Errorf("执行(show version)命令失败: %v", err)
	}
	if needsCentecSecurityPromptAnswer(versionOutput) {
		if err := client.SendCommand("no"); err != nil {
			return nil, versionOutput, fmt.Errorf("回复设备安全提示失败: %v", err)
		}
		if _, err := client.ReadPagedResponse(1500*time.Millisecond, morePatterns); err != nil {
			return nil, versionOutput, fmt.Errorf("回复设备安全提示后读取命令提示失败: %v", err)
		}
		versionOutput, err = runTelnetCommandWithPaging(client, "show version", morePatterns, 1500*time.Millisecond)
		if err != nil {
			return nil, "", fmt.Errorf("重试(show version)命令失败: %v", err)
		}
	}
	log.Printf("盛科设备:\n%s", versionOutput)
	if !isSupportedCentecVersion(versionOutput) {
		log.Printf("盛科设备版本识别失败: 未匹配关键字 centec、vsos、cnos、e530、s530、v350、e580")
		log.Printf("盛科设备版本未识别，继续采集接口信息")
	}

	detailOutput, err := runTelnetCommandWithPaging(client, "show transceiver detail", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show transceiver detail)命令失败: %v", err)
	}
	statusOutput, err := runTelnetCommandWithPaging(client, "show interface status", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface status)命令失败: %v", err)
	}
	switchportOutput, err := runTelnetCommandWithPaging(client, "show interface switchport", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface switchport)命令失败: %v", err)
	}

	info, err := buildCentecInterfaceInfo(detailOutput, statusOutput, switchportOutput)
	return info, versionOutput + "\n" + collectCentecMonitoringOutputByTelnet(client, morePatterns), err
}

func needsCentecSecurityPromptAnswer(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "change now?") ||
		strings.Contains(lower, "please answer 'yes' or 'no'")
}

func queryRuijieInterfaceInfoByTelnet(client *TelnetClient, morePatterns []string) ([]SwitchInterfaceInfo, string, error) {
	versionOutput, err := runTelnetCommandWithPaging(client, "show version", morePatterns, 1500*time.Millisecond)
	if err != nil {
		return nil, "", fmt.Errorf("执行(show version)命令失败: %v", err)
	}
	versionInfo := ParseRuijieVersionInfo(versionOutput)
	if versionInfo["deviceName"] == "" && versionInfo["softwareVersion"] == "" {
		return nil, versionOutput, fmt.Errorf("未识别到锐捷交换机版本信息")
	}

	transceiverOutput, err := runTelnetCommandWithPaging(client, "show interface transceiver diagnosis", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface transceiver diagnosis)命令失败: %v", err)
	}
	statusOutput, err := runTelnetCommandWithPaging(client, "show interface status", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface status)命令失败: %v", err)
	}
	switchportOutput, err := runTelnetCommandWithPaging(client, "show interface switchport", morePatterns, 2*time.Second)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface switchport)命令失败: %v", err)
	}

	info, err := buildRuijieInterfaceInfo(statusOutput, transceiverOutput, switchportOutput)
	return info, versionOutput + "\n" + collectMonitoringOutputByTelnet(client, "ruijie", morePatterns), err
}

func runTelnetCommandWithPaging(client *TelnetClient, cmd string, morePatterns []string, timeout time.Duration) (string, error) {
	if err := client.SendCommand(cmd); err != nil {
		return "", err
	}
	return client.ReadPagedResponse(timeout, morePatterns)
}

func queryCentecInterfaceInfoBySSH(client *Client) ([]SwitchInterfaceInfo, string, error) {
	versionOutput, err := client.RunInteractive([]string{"show version"})
	if err != nil {
		return nil, "", fmt.Errorf("执行(show version)命令失败: %v", err)
	}
	if !isSupportedCentecVersion(versionOutput) {
		log.Printf("盛科设备 SSH 版本未识别，继续采集接口信息; versionOutput=%q", versionOutput)
	}

	detailOutput, err := client.RunInteractive([]string{"show transceiver detail"})
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show transceiver detail)命令失败: %v", err)
	}
	statusOutput, err := client.RunInteractive([]string{"show interface status"})
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface status)命令失败: %v", err)
	}
	switchportOutput, err := client.RunInteractive([]string{"show interface switchport"})
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface switchport)命令失败: %v", err)
	}

	info, err := buildCentecInterfaceInfo(detailOutput, statusOutput, switchportOutput)
	return info, versionOutput + "\n" + collectCentecMonitoringOutputBySSH(client), err
}

func queryRuijieInterfaceInfoBySSH(client *Client) ([]SwitchInterfaceInfo, string, error) {
	options := InteractiveOptions{
		PreCommands:    []string{"terminal length 0"},
		PromptSuffixes: []string{">", "#"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   1200,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       false,
	}
	versionOutput, err := client.RunInteractiveWithOptions([]string{"show version"}, options)
	if err != nil {
		return nil, "", fmt.Errorf("执行(show version)命令失败: %v", err)
	}
	versionInfo := ParseRuijieVersionInfo(versionOutput)
	if versionInfo["deviceName"] == "" && versionInfo["softwareVersion"] == "" {
		return nil, versionOutput, fmt.Errorf("未识别到锐捷交换机版本信息")
	}

	transceiverOutput, err := client.RunInteractiveWithOptions([]string{"show interface transceiver diagnosis"}, options)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface transceiver diagnosis)命令失败: %v", err)
	}
	statusOutput, err := client.RunInteractiveWithOptions([]string{"show interface status"}, options)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface status)命令失败: %v", err)
	}
	switchportOutput, err := client.RunInteractiveWithOptions([]string{"show interface switchport"}, options)
	if err != nil {
		return nil, versionOutput, fmt.Errorf("执行(show interface switchport)命令失败: %v", err)
	}

	info, err := buildRuijieInterfaceInfo(statusOutput, transceiverOutput, switchportOutput)
	return info, versionOutput + "\n" + collectMonitoringOutputBySSHWithinBounds(client, "ruijie", 8*time.Second), err
}

func (m *Module) queryCentecInterfaceInfoByRPC(req SwitchRequest) ([]SwitchInterfaceInfo, string, error) {
	cmds := []string{
		"show version",
		"show transceiver detail",
		"show interface status",
		"show interface switchport",
	}
	rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return nil, "", fmt.Errorf("rpc error: %v", err)
	}
	if rpcResp.Error != nil {
		return nil, "", fmt.Errorf("%s", rpcResp.Error.Message)
	}

	sections, err := extractRPCSourceDetails(rpcResp.Result)
	if err != nil {
		return nil, "", err
	}
	versionOutput := sections["show version"]
	if !isSupportedCentecVersion(versionOutput) {
		log.Printf("盛科设备 RPC 版本未识别，继续采集接口信息; versionOutput=%q", versionOutput)
	}
	info, err := buildCentecInterfaceInfo(
		sections["show transceiver detail"],
		sections["show interface status"],
		sections["show interface switchport"],
	)
	return info, versionOutput + "\n" + m.collectCentecMonitoringOutputByRPC(req), err
}

func (m *Module) collectCentecMonitoringOutputByRPC(req SwitchRequest) string {
	var output strings.Builder
	for _, command := range []string{"show process", "show memory summary", "show environment"} {
		response, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{command}, req.Format, SwitchLoginInfo{
			Username: req.Username,
			Password: req.Password,
			Maker:    req.Maker,
		})
		if err != nil || response.Error != nil {
			continue
		}
		var entries []map[string]interface{}
		if err := json.Unmarshal(response.Result, &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			if source, ok := entry["sourceDetails"].(string); ok {
				output.WriteString(source)
				output.WriteByte('\n')
			}
		}
	}
	return output.String()
}

func buildCentecInterfaceInfo(detailOutput, statusOutput, switchportOutput string) ([]SwitchInterfaceInfo, error) {
	transceiverInfos := ParseTransceiverInfo(detailOutput)
	interfaceInfo, err := ParseInterfaceInfo("centec", statusOutput, transceiverInfos)
	if err != nil {
		return nil, fmt.Errorf("解析接口信息失败: %v", err)
	}
	mergeCentecInterfaceVLANInfo(interfaceInfo, ParseCentecSwitchportVlan(switchportOutput))
	return interfaceInfo, nil
}

func extractRPCSourceDetails(raw json.RawMessage) (map[string]string, error) {
	infos := []map[string]interface{}{}
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("rpc error: %v", err)
	}

	result := make(map[string]string)
	for _, info := range infos {
		if val, ok := info["errorCode"]; ok {
			desc := ""
			if str, ok := info["errorDesc"].(string); ok {
				desc = str
			}
			return nil, fmt.Errorf("rpc command error: %v %s", val, strings.TrimSpace(desc))
		}
		source, _ := info["sourceDetails"].(string)
		switch {
		case strings.Contains(source, "The current running image is") || strings.Contains(source, "Hardware Type is"):
			result["show version"] = source
		case strings.Contains(source, "transceiver info:") || (strings.Contains(source, "Temperature") && strings.Contains(source, "Voltage")):
			result["show transceiver detail"] = source
		case strings.Contains(source, "Port             Status") || (strings.Contains(source, "Duplex") && strings.Contains(source, "Speed") && strings.Contains(source, "Mode")):
			result["show interface status"] = source
		case strings.Contains(source, "Interface name") && strings.Contains(source, "Switchport mode"):
			result["show interface switchport"] = source
		}
	}
	return result, nil
}

func isSupportedCentecVersion(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "centec") ||
		strings.Contains(lower, "vsos") ||
		strings.Contains(lower, "cnos") ||
		strings.Contains(lower, "e530") ||
		strings.Contains(lower, "s530") ||
		strings.Contains(lower, "v350") ||
		strings.Contains(lower, "e580")
}

func mergeCentecInterfaceVLANInfo(interfaces []SwitchInterfaceInfo, vlanMap map[string]SwitchVlanMapping) {
	for i := range interfaces {
		if vlanInfo, ok := vlanMap[interfaces[i].Port]; ok {
			interfaces[i].VlanMapping = vlanInfo
		}
	}
}

func buildHuaweiSwitchVlanUpsertCommands(req SwitchPortVlanRequest) ([]string, error) {
	return buildHuaweiSwitchVlanUpsertCommandsForCurrent(req, SwitchVlanMapping{})
}

func buildH3CSwitchVLANCommands(nAct int, req SwitchPortVlanRequest, current SwitchVlanMapping) ([]string, error) {
	port := normalizeSwitchPortName(req.Port)
	if port == "" {
		return nil, fmt.Errorf("port is required")
	}
	if nAct == 1 || nAct == 2 {
		if req.PVID < 1 || req.PVID > 4094 {
			return nil, fmt.Errorf("PVID 必须为 1-4094")
		}
		return []string{
			"system-view",
			fmt.Sprintf("vlan %d", req.PVID),
			h3cConfigPortCommand(port),
			"quit",
			"quit",
			"save force",
		}, nil
	}
	if nAct == 3 {
		if req.PVID < 1 || req.PVID > 4094 {
			return nil, fmt.Errorf("PVID 必须为 1-4094")
		}
		return []string{
			"system-view",
			fmt.Sprintf("vlan %d", req.PVID),
			"undo " + h3cConfigPortCommand(port),
			"quit",
			"quit",
			"save force",
		}, nil
	}
	linkType := strings.ToLower(strings.TrimSpace(req.LinkType))
	if linkType != "access" && linkType != "trunk" && linkType != "hybrid" {
		return nil, fmt.Errorf("unsupported linkType: %s", req.LinkType)
	}
	if req.PVID < 1 || req.PVID > 4094 {
		return nil, fmt.Errorf("PVID 必须为 1-4094")
	}
	vlans := buildHuaweiVlanBatchArg(req)
	cmds := []string{"system-view"}
	if vlans != "" {
		cmds = append(cmds, "vlan "+vlans, "quit")
	}
	cmds = append(cmds, "interface "+port)
	if strings.TrimSpace(current.LinkType) != "" && !strings.EqualFold(current.LinkType, linkType) {
		switch strings.ToLower(current.LinkType) {
		case "trunk":
			cmds = append(cmds, "undo port trunk permit vlan all", "undo port trunk pvid vlan")
		case "hybrid":
			cmds = append(cmds, "undo port hybrid vlan all")
		}
	}
	cmds = append(cmds, "port link-type "+linkType)
	switch linkType {
	case "access":
		cmds = append(cmds, fmt.Sprintf("port access vlan %d", req.PVID))
	case "trunk":
		cmds = append(cmds, fmt.Sprintf("port trunk pvid vlan %d", req.PVID))
		if strings.TrimSpace(req.TrunkVLANList) != "" {
			cmds = append(cmds, "port trunk permit vlan "+buildHuaweiVlanBatchArg(req))
		}
	case "hybrid":
		cmds = append(cmds, fmt.Sprintf("port hybrid pvid vlan %d", req.PVID))
		if strings.TrimSpace(req.TrunkVLANList) != "" {
			cmds = append(cmds, "port hybrid vlan "+buildHuaweiVlanBatchArg(req)+" tagged")
		}
	}
	return append(cmds, "quit", "quit", "save force"), nil
}

func h3cConfigPortCommand(port string) string {
	port = normalizeSwitchPortName(port)
	patterns := []struct {
		prefix string
		alias  string
	}{
		{"Ten-GigabitEthernet", "XGE"},
		{"10GE", "XGE"},
		{"XGE", "XGE"},
		{"FortyGigE", "FGE"},
		{"40GE", "FGE"},
		{"FGE", "FGE"},
		{"GigabitEthernet", "GE"},
		{"GE", "GE"},
	}
	for _, item := range patterns {
		if len(port) > len(item.prefix) && strings.HasPrefix(strings.ToLower(port), strings.ToLower(item.prefix)) {
			return "port " + item.alias + " " + port[len(item.prefix):]
		}
	}
	return "port " + port
}

// buildHuaweiSwitchVlanUpsertCommandsForCurrent clears the incompatible
// VLAN attributes before changing a Huawei port's link type.  SPC800 rejects
// a mode transition while the old mode's attributes remain on the interface.

// buildHuaweiSwitchVlanUpsertCommandsForCurrent clears the incompatible
// VLAN attributes before changing a Huawei port's link type.  SPC800 rejects
// a mode transition while the old mode's attributes remain on the interface.
func buildHuaweiSwitchVlanUpsertCommandsForCurrent(req SwitchPortVlanRequest, current SwitchVlanMapping) ([]string, error) {
	port := normalizeSwitchPortName(req.Port)
	if port == "" {
		return nil, fmt.Errorf("port is required")
	}
	linkType := strings.ToLower(strings.TrimSpace(req.LinkType))
	if linkType == "" {
		linkType = "access"
	}
	if req.PVID <= 0 {
		req.PVID = 1
	}
	cmds := []string{"system-view"}
	vlanBatch := buildHuaweiVlanBatchArg(req)
	if vlanBatch != "" {
		cmds = append(cmds, fmt.Sprintf("vlan batch %s", vlanBatch))
	}
	cmds = append(cmds,
		fmt.Sprintf("interface %s", port),
	)
	currentType := strings.ToLower(strings.TrimSpace(current.LinkType))
	if currentType != "" && currentType != linkType {
		cmds = append(cmds, buildHuaweiSwitchVlanTransitionCleanupCommands(currentType)...)
	}
	cmds = append(cmds, fmt.Sprintf("port link-type %s", linkType))
	switch linkType {
	case "access":
		cmds = append(cmds, fmt.Sprintf("port default vlan %d", req.PVID))
	case "trunk":
		cmds = append(cmds, fmt.Sprintf("port trunk pvid vlan %d", req.PVID))
		if strings.TrimSpace(req.TrunkVLANList) != "" && req.TrunkVLANList != "-" {
			cmds = append(cmds, fmt.Sprintf("port trunk allow-pass vlan %s", strings.TrimSpace(req.TrunkVLANList)))
		}
	case "hybrid":
		cmds = append(cmds, fmt.Sprintf("port hybrid pvid vlan %d", req.PVID))
		if strings.TrimSpace(req.TrunkVLANList) != "" && req.TrunkVLANList != "-" {
			cmds = append(cmds, fmt.Sprintf("port hybrid tagged vlan %s", strings.TrimSpace(req.TrunkVLANList)))
		}
	default:
		return nil, fmt.Errorf("unsupported linkType: %s", req.LinkType)
	}
	cmds = append(cmds, buildHuaweiConfigFinalizeCommands()...)
	return cmds, nil
}

func buildHuaweiSwitchVlanTransitionCleanupCommands(linkType string) []string {
	switch strings.ToLower(strings.TrimSpace(linkType)) {
	case "trunk":
		return []string{"undo port trunk allow-pass vlan all", "undo port trunk pvid vlan"}
	case "hybrid":
		return []string{"undo port hybrid tagged vlan all", "undo port hybrid untagged vlan all", "undo port hybrid pvid vlan"}
	case "access":
		return []string{"undo port default vlan"}
	default:
		return nil
	}
}

func buildHuaweiVlanBatchArg(req SwitchPortVlanRequest) string {
	seen := map[string]struct{}{}
	ordered := make([]string, 0, 2)
	trunkList := strings.TrimSpace(req.TrunkVLANList)
	add := func(val string) {
		val = strings.TrimSpace(val)
		if val == "" || val == "-" || val == "--" {
			return
		}
		if _, ok := seen[val]; ok {
			return
		}
		seen[val] = struct{}{}
		ordered = append(ordered, val)
	}
	if req.PVID > 1 {
		pvidStr := strconv.FormatInt(req.PVID, 10)
		if trunkList == "" || (!strings.Contains(trunkList, pvidStr+" ") && !strings.Contains(trunkList, " "+pvidStr) && trunkList != pvidStr) {
			add(pvidStr)
		}
	}
	add(trunkList)
	return strings.Join(ordered, " ")
}

func buildHuaweiSwitchVlanDeleteCommands(current SwitchVlanMapping) []string {
	port := normalizeSwitchPortName(current.Port)
	if port == "" {
		port = normalizeSwitchPortName(current.Port)
	}
	cmds := []string{
		"system-view",
		fmt.Sprintf("interface %s", port),
	}
	switch strings.ToLower(strings.TrimSpace(current.LinkType)) {
	case "trunk":
		cmds = append(cmds,
			"undo port trunk allow-pass vlan all",
			"undo port trunk pvid vlan",
			"port link-type access",
			"undo port default vlan",
		)
	case "hybrid":
		cmds = append(cmds,
			"undo port hybrid tagged vlan all",
			"undo port hybrid untagged vlan all",
			"undo port hybrid pvid vlan",
			"port link-type access",
			"undo port default vlan",
		)
	default:
		cmds = append(cmds,
			"port link-type access",
			"undo port default vlan",
		)
	}
	cmds = append(cmds, buildHuaweiConfigFinalizeCommands()...)
	return cmds
}

func buildHuaweiConfigFinalizeCommands() []string {
	return []string{
		"commit",
		"quit",
		"quit",
		"save",
	}
}

func runHuaweiConfigCommandsBySSH(client *Client, commands []string) (string, error) {
	return client.RunInteractiveWithOptions(commands, InteractiveOptions{
		PreCommands:       []string{"screen-length 0 temporary"},
		MorePatterns:      []string{"---- More ----", "--More--", "More"},
		PromptSuffixes:    []string{">", "#", "]"},
		CommandDelay:      1200 * time.Millisecond,
		ReadTimeout:       1500 * time.Millisecond,
		AutoConfirm:       true,
		AutoExit:          true,
		StopOnErrorOutput: true,
	})
}

func runHuaweiConfigCommandsByTelnet(client *TelnetClient, commands []string, timeout time.Duration, morePatterns []string) (string, error) {
	var output strings.Builder
	for _, command := range commands {
		if err := client.SendCommand(command); err != nil {
			return output.String(), fmt.Errorf("发送命令失败: %w", err)
		}
		response, err := client.ReadPagedResponse(timeout, morePatterns)
		if err != nil {
			return output.String(), fmt.Errorf("执行命令失败: %w", err)
		}
		output.WriteString(response)
		if detail := matchCommandFailureOutput(command, response); detail != "" {
			return output.String(), fmt.Errorf("command %q: %s", command, detail)
		}
		if !hasInteractiveConfirmationPrompt(response) {
			continue
		}
		if err := client.SendCommand("y"); err != nil {
			return output.String(), fmt.Errorf("发送确认命令失败: %w", err)
		}
		confirmationResponse, err := client.ReadPagedResponse(timeout, morePatterns)
		if err != nil {
			return output.String(), fmt.Errorf("读取确认响应失败: %w", err)
		}
		output.WriteString(confirmationResponse)
		if detail := matchCommandFailureOutput(command, confirmationResponse); detail != "" {
			return output.String(), fmt.Errorf("command %q: %s", command, detail)
		}
	}
	return output.String(), nil
}

func appendVLANCommandOutput(output *strings.Builder, command, response string) error {
	output.WriteString(response)
	if detail := matchCommandFailureOutput(command, response); detail != "" {
		return fmt.Errorf("command %q: %s", command, detail)
	}
	return nil
}

func vlanSettingFailureMessage(err error) string {
	return "VLAN 设置失败: " + err.Error()
}

func (m *Module) queryHuaweiSwitchPortVlanBySSH(client *Client, port string) (SwitchVlanMapping, bool) {
	output, err := client.RunInteractive([]string{"display port vlan"})
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findSwitchPortVlan(ParseHuaweiPortVlan(output), port)
}

func (m *Module) queryHuaweiSwitchPortVlanByTelnet(client *TelnetClient, port string, morePatterns []string) (SwitchVlanMapping, bool) {
	if err := client.SendCommand("display port vlan"); err != nil {
		return SwitchVlanMapping{}, false
	}
	output, err := client.ReadPagedResponse(1*time.Second, morePatterns)
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findSwitchPortVlan(ParseHuaweiPortVlan(output), port)
}

func queryH3CSwitchPortVlanBySSH(client *Client, port string) (SwitchVlanMapping, bool) {
	// Do not use RunInteractive here: its default options send "quit" after
	// the read, and this H3C release closes the entire SSH transport on quit.
	// The caller reuses that transport immediately for the VLAN configuration.
	output, err := client.RunInteractiveWithOptions([]string{h3cDisplayInterfaceCommand(port)}, h3cSSHInteractiveOptions(false))
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findH3CInterfaceDetailVlan(output, port)
}

func queryH3CSwitchPortVlanByTelnet(client *TelnetClient, port string, morePatterns []string) (SwitchVlanMapping, bool) {
	output, err := runTelnetCommandWithPaging(client, h3cDisplayInterfaceCommand(port), morePatterns, 2*time.Second)
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findH3CInterfaceDetailVlan(output, port)
}

func h3cDisplayInterfaceCommand(port string) string {
	port = normalizeSwitchPortName(port)
	for _, prefix := range []string{
		"HundredGigE", "Twenty-FiveGigE", "Ten-GigabitEthernet", "FortyGigE", "GigabitEthernet", "M-GigabitEthernet",
		"100GE", "40GE", "25GE", "10GE", "XGE", "FGE", "MGE", "GE",
	} {
		if len(port) > len(prefix) && strings.HasPrefix(strings.ToLower(port), strings.ToLower(prefix)) {
			return "display interface " + prefix + " " + port[len(prefix):]
		}
	}
	return "display interface " + port
}

func findH3CInterfaceDetailVlan(output, port string) (SwitchVlanMapping, bool) {
	want := normalizeH3CPortName(port)
	if info, ok := ParseH3CInterfaceDetailInfo(output)[want]; ok {
		return info.VlanMapping, true
	}
	return SwitchVlanMapping{}, false
}

func findSwitchPortVlan(vlanMap map[string]SwitchVlanMapping, port string) (SwitchVlanMapping, bool) {
	port = normalizeSwitchPortName(port)
	for key, val := range vlanMap {
		if normalizeSwitchPortName(key) == port || normalizeSwitchPortName(val.Port) == port {
			return val, true
		}
	}
	return SwitchVlanMapping{}, false
}

func hasHuaweiCommandError(output string) bool {
	return matchHuaweiCommandErrorPattern(output) != ""
}

func matchHuaweiCommandErrorPattern(output string) string {
	lower := strings.ToLower(output)
	errorPatterns := []string{
		"error:",
		" failed.",
		" failed ",
		"command failed",
		"operation failed",
		"failed to",
		"unrecognized command",
		"incomplete command",
		"too many parameters",
		"wrong parameter",
	}
	for _, pattern := range errorPatterns {
		if strings.Contains(lower, pattern) {
			return pattern
		}
	}
	return ""
}

func buildCentecSwitchVlanUpsertCommands(req SwitchPortVlanRequest) ([]string, error) {
	port := normalizeSwitchPortName(req.Port)
	if port == "" {
		return nil, fmt.Errorf("port is required")
	}
	linkType := strings.ToLower(strings.TrimSpace(req.LinkType))
	if linkType == "" {
		linkType = "access"
	}
	if req.PVID <= 0 {
		req.PVID = 1
	}
	cmds := []string{
		"configure terminal",
		fmt.Sprintf("interface %s", port),
	}
	switch linkType {
	case "access":
		cmds = append(cmds, "switchport mode access")
		if req.PVID == 1 {
			cmds = append(cmds, "no switchport access vlan")
		} else {
			cmds = append(cmds, fmt.Sprintf("switchport access vlan %d", req.PVID))
		}
	case "trunk":
		cmds = append(cmds, "switchport mode trunk")
		if req.PVID == 1 {
			cmds = append(cmds, "no switchport trunk native vlan")
		} else {
			cmds = append(cmds, fmt.Sprintf("switchport trunk native vlan %d", req.PVID))
		}
		allowed := normalizeCentecAllowedVlanList(req.TrunkVLANList, req.PVID)
		if allowed == "" {
			allowed = strconv.FormatInt(req.PVID, 10)
		}
		cmds = append(cmds, "switchport trunk allowed vlan none")
		cmds = append(cmds, fmt.Sprintf("switchport trunk allowed vlan add %s", allowed))
	default:
		return nil, fmt.Errorf("unsupported linkType: %s", req.LinkType)
	}
	cmds = append(cmds, "end", "write memory")
	return cmds, nil
}

func buildCentecSwitchVlanDeleteCommands(current SwitchVlanMapping) []string {
	port := normalizeSwitchPortName(current.Port)
	cmds := []string{
		"configure terminal",
		fmt.Sprintf("interface %s", port),
	}
	if strings.EqualFold(strings.TrimSpace(current.LinkType), "trunk") {
		// A mode switch alone does not remove the aggregate port's trunk VLAN list.
		cmds = append(cmds,
			"switchport mode trunk",
			"switchport trunk allowed vlan none",
			"no switchport trunk native vlan",
		)
	}
	cmds = append(cmds,
		"switchport mode access",
		"no switchport access vlan",
	)
	// Switching an aggregate port from trunk to access does not always discard
	// its configured VLANs. Remove the pre-delete list explicitly in access view.
	if allowed := centecAllowedVLANsToRemove(current.TrunkVLANList, current.PVID); allowed != "" {
		cmds = append(cmds, fmt.Sprintf("switchport access allowed vlan remove %s", allowed))
	}
	cmds = append(cmds, "end", "write memory")
	return cmds
}

func isCentecSwitchVlanCleared(mapping SwitchVlanMapping) bool {
	if strings.EqualFold(strings.TrimSpace(mapping.LinkType), "trunk") || mapping.PVID > 1 {
		return false
	}
	allowed := strings.TrimSpace(strings.ToLower(mapping.TrunkVLANList))
	return allowed == "" || allowed == "-" || allowed == "--" || allowed == "1"
}

func centecAllowedVLANsToRemove(raw string, pvid int64) string {
	allowed := normalizeCentecAllowedVlanList(raw, pvid)
	vlans, err := parseRange(allowed, 1)
	if err != nil {
		return ""
	}
	seen := make(map[int]struct{}, len(vlans))
	unique := make([]int, 0, len(vlans))
	for _, vlan := range vlans {
		if _, ok := seen[vlan]; ok {
			continue
		}
		seen[vlan] = struct{}{}
		unique = append(unique, vlan)
	}
	sort.Ints(unique)
	return compressRanges(unique)
}

func normalizeCentecAllowedVlanList(raw string, pvid int64) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "--" {
		if pvid > 0 {
			return strconv.FormatInt(pvid, 10)
		}
		return ""
	}
	pvidStr := strconv.FormatInt(pvid, 10)
	if raw == "ALL" {
		return pvidStr
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts)+1)
	ordered := make([]string, 0, len(parts)+1)
	add := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" {
			return
		}
		if _, ok := seen[item]; ok {
			return
		}
		seen[item] = struct{}{}
		ordered = append(ordered, item)
	}
	if pvid > 0 {
		add(pvidStr)
	}
	for _, item := range parts {
		add(item)
	}
	return strings.Join(ordered, ",")
}

func queryCentecSwitchPortVlanBySSH(client *Client, port string) (SwitchVlanMapping, bool) {
	output, err := client.RunInteractive([]string{"show interface switchport"})
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findSwitchPortVlan(ParseCentecSwitchportVlan(output), port)
}

func queryCentecSwitchPortVlanBySSHWithOptions(client *Client, port string, options InteractiveOptions) (SwitchVlanMapping, bool) {
	output, err := client.RunInteractiveWithOptions([]string{"show interface switchport"}, options)
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findSwitchPortVlan(ParseCentecSwitchportVlan(output), port)
}

func queryCentecSwitchPortVlanByTelnet(client *TelnetClient, port string, morePatterns []string) (SwitchVlanMapping, bool) {
	output, err := runTelnetCommandWithPaging(client, "show interface switchport", morePatterns, 2*time.Second)
	if err != nil {
		return SwitchVlanMapping{}, false
	}
	return findSwitchPortVlan(ParseCentecSwitchportVlan(output), port)
}

func (m *Module) queryCentecSwitchPortVlanByRPC(req SwitchRequest, port string) (SwitchVlanMapping, bool, error) {
	cmds := []string{"show interface switchport"}
	rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return SwitchVlanMapping{}, false, fmt.Errorf("rpc error: %v", err)
	}
	if rpcResp.Error != nil {
		return SwitchVlanMapping{}, false, fmt.Errorf("%s", rpcResp.Error.Message)
	}
	sections, err := extractRPCSourceDetails(rpcResp.Result)
	if err != nil {
		return SwitchVlanMapping{}, false, err
	}
	info, ok := findSwitchPortVlan(ParseCentecSwitchportVlan(sections["show interface switchport"]), port)
	return info, ok, nil
}

func (m *Module) resolveCentecVlanTargetByRPC(vlanReq *SwitchPortVlanRequest, req SwitchRequest) error {
	if strings.TrimSpace(vlanReq.AggregationPort) != "" {
		applySwitchVlanAggregationTarget(vlanReq)
		return nil
	}
	aggregate, err := m.queryCentecChannelGroupByRPC(req, vlanReq.Port)
	if err != nil {
		return err
	}
	if aggregate != "" {
		vlanReq.Port = aggregate
	}
	return nil
}

func (m *Module) queryCentecChannelGroupByRPC(req SwitchRequest, port string) (string, error) {
	output, err := m.runCentecChannelGroupRPCCommand(req, "show channel-group port "+port)
	if err != nil {
		return "", err
	}
	if aggregate := parseCentecChannelGroupPortName(output, port); aggregate != "" {
		return aggregate, nil
	}

	output, err = m.runCentecChannelGroupRPCCommand(req, "show channel-group port")
	if err != nil {
		return "", err
	}
	return parseCentecChannelGroupPortName(output, port), nil
}

func (m *Module) runCentecChannelGroupRPCCommand(req SwitchRequest, command string) (string, error) {
	rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{command}, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return "", fmt.Errorf("rpc error: %v", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("%s", rpcResp.Error.Message)
	}
	output, err := extractRPCCommandOutput(rpcResp.Result)
	if err != nil {
		return "", err
	}
	return output, nil
}

// extractRPCCommandOutput returns the sourceDetails of a single-command RPC response.
// Unlike extractRPCSourceDetails, it does not depend on a fixed command-output signature.

// extractRPCCommandOutput returns the sourceDetails of a single-command RPC response.
// Unlike extractRPCSourceDetails, it does not depend on a fixed command-output signature.
func extractRPCCommandOutput(raw json.RawMessage) (string, error) {
	infos := []map[string]interface{}{}
	if err := json.Unmarshal(raw, &infos); err != nil {
		return "", fmt.Errorf("rpc error: %v", err)
	}
	for _, info := range infos {
		if val, ok := info["errorCode"]; ok {
			desc, _ := info["errorDesc"].(string)
			return "", fmt.Errorf("rpc command error: %v %s", val, strings.TrimSpace(desc))
		}
		if source, ok := info["sourceDetails"].(string); ok {
			return source, nil
		}
	}
	return "", nil
}

func resolveCentecVlanTargetBySSH(vlanReq *SwitchPortVlanRequest, client *Client, options InteractiveOptions) error {
	if strings.TrimSpace(vlanReq.AggregationPort) != "" {
		applySwitchVlanAggregationTarget(vlanReq)
		return nil
	}
	aggregate, err := queryCentecChannelGroupBySSH(client, vlanReq.Port, options)
	if err != nil {
		return err
	}
	if aggregate != "" {
		vlanReq.Port = aggregate
	}
	return nil
}

func queryCentecChannelGroupBySSH(client *Client, port string, options InteractiveOptions) (string, error) {
	output, err := client.RunInteractiveWithOptions([]string{"show channel-group port " + port}, options)
	if err != nil {
		return "", err
	}
	if aggregate := parseCentecChannelGroupPortName(output, port); aggregate != "" {
		return aggregate, nil
	}
	output, err = client.RunInteractiveWithOptions([]string{"show channel-group port"}, options)
	if err != nil {
		return "", err
	}
	return parseCentecChannelGroupPortName(output, port), nil
}

func resolveCentecVlanTargetByTelnet(vlanReq *SwitchPortVlanRequest, client *TelnetClient, morePatterns []string) error {
	if strings.TrimSpace(vlanReq.AggregationPort) != "" {
		applySwitchVlanAggregationTarget(vlanReq)
		return nil
	}
	aggregate, err := queryCentecChannelGroupByTelnet(client, vlanReq.Port, morePatterns)
	if err != nil {
		return err
	}
	if aggregate != "" {
		vlanReq.Port = aggregate
	}
	return nil
}

func queryCentecChannelGroupByTelnet(client *TelnetClient, port string, morePatterns []string) (string, error) {
	output, err := runTelnetCommandWithPaging(client, "show channel-group port "+port, morePatterns, 2*time.Second)
	if err != nil {
		return "", err
	}
	if aggregate := parseCentecChannelGroupPortName(output, port); aggregate != "" {
		return aggregate, nil
	}
	output, err = runTelnetCommandWithPaging(client, "show channel-group port", morePatterns, 2*time.Second)
	if err != nil {
		return "", err
	}
	return parseCentecChannelGroupPortName(output, port), nil
}

func updateCentecVlanTarget(vlanReq *SwitchPortVlanRequest, output string) {
	if aggregate := parseCentecChannelGroupName(output); aggregate != "" {
		vlanReq.Port = aggregate
	}
}

func applySwitchVlanAggregationTarget(vlanReq *SwitchPortVlanRequest) {
	if vlanReq == nil {
		return
	}
	if aggregate := normalizeSwitchPortName(vlanReq.AggregationPort); aggregate != "" {
		vlanReq.Port = aggregate
	}
}

func parseCentecChannelGroupName(output string) string {
	nameValuePattern := regexp.MustCompile(`(?i)\bport[- ]?channel\s*:\s*([^\s]+)`)
	if match := nameValuePattern.FindStringSubmatch(output); len(match) == 2 {
		return normalizeSwitchPortName(match[1])
	}
	namePattern := regexp.MustCompile(`(?i)\b(?:interface\s+)?(port[- ]?channel\s*\d+(?:[/-]\d+)*)\b`)
	if match := namePattern.FindStringSubmatch(output); len(match) == 2 {
		return strings.ReplaceAll(strings.ReplaceAll(match[1], " ", ""), "Port-", "port-")
	}
	numberPattern := regexp.MustCompile(`(?im)^\s*(?:channel[- ]?group|port[- ]?channel)\s*[:#]?\s*(\d+)\b`)
	if match := numberPattern.FindStringSubmatch(output); len(match) == 2 {
		return "port-channel" + match[1]
	}
	return ""
}

// parseCentecChannelGroupPortName extracts a member's aggregate from the
// complete channel-group listing. A single-port query can echo the member
// itself as Port-channel, which must not be treated as an aggregate target.

// parseCentecChannelGroupPortName extracts a member's aggregate from the
// complete channel-group listing. A single-port query can echo the member
// itself as Port-channel, which must not be treated as an aggregate target.
func parseCentecChannelGroupPortName(output, memberPort string) string {
	memberPort = normalizeSwitchPortName(memberPort)
	activeMember := false
	foundPortHeader := false
	portHeaderPattern := regexp.MustCompile(`(?i)^\s*port\s*:\s*(\S+)\s*$`)
	for _, line := range strings.Split(output, "\n") {
		if match := portHeaderPattern.FindStringSubmatch(strings.TrimSpace(line)); len(match) == 2 {
			foundPortHeader = true
			activeMember = strings.EqualFold(normalizeSwitchPortName(match[1]), memberPort)
			continue
		}
		if activeMember {
			if aggregate := parseCentecChannelGroupName(line); aggregate != "" && !strings.EqualFold(aggregate, memberPort) {
				return aggregate
			}
		}
	}
	if !foundPortHeader {
		if aggregate := parseCentecChannelGroupName(output); aggregate != "" && !strings.EqualFold(aggregate, memberPort) {
			return aggregate
		}
	}
	return ""
}

func (m *Module) executeCentecRPCCommands(req SwitchRequest, cmds []string) error {
	rpcResp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
		Username: req.Username,
		Password: req.Password,
		Maker:    req.Maker,
	})
	if err != nil {
		return fmt.Errorf("rpc error: %v", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	infos := []map[string]interface{}{}
	if err := json.Unmarshal(rpcResp.Result, &infos); err != nil {
		return fmt.Errorf("rpc error: %v", err)
	}
	for index, info := range infos {
		command := ""
		if index < len(cmds) {
			command = cmds[index]
		}
		if val, ok := info["errorCode"]; ok {
			desc := ""
			if str, ok := info["errorDesc"].(string); ok {
				desc = strings.TrimSpace(str)
			}
			return fmt.Errorf("rpc command error: command=%q error=%v %s", command, val, desc)
		}
		if response, ok := info["sourceDetails"].(string); ok {
			if detail := matchCommandFailureOutput(command, response); detail != "" {
				return fmt.Errorf("command %q: %s", command, detail)
			}
		}
	}
	return nil
}

func hasCentecCommandError(output string) bool {
	lower := strings.ToLower(output)
	errorPatterns := []string{
		"error:",
		"invalid",
		"incomplete command",
		"unrecognized command",
		"failed",
		"ambiguous command",
	}
	for _, pattern := range errorPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// 终端 MAC 模块：根据端口和 VLAN 查询终端接入信息。

// 终端 MAC 模块：根据端口和 VLAN 查询终端接入信息。
func (m *Module) handleTerminalMACBySSH(w http.ResponseWriter, req SwitchRequest) {
	if req.Port == 0 {
		req.Port = 22
	}
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
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error() + ",请重新连接"})
		return
	}
	defer client.Close()

	output, result, errMsg, ok := queryHuaweiTerminalMACBySSH(client, req)
	if !ok {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: errMsg, Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
}

func (m *Module) handleTerminalMACByTelnet(w http.ResponseWriter, req SwitchRequest) {
	if req.Port == 0 {
		req.Port = 23
	}
	client := &TelnetClient{
		owner:   m,
		Host:    req.Hostip,
		Port:    req.Port,
		Maker:   req.Maker,
		Timeout: 30 * time.Second,
	}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Connection failed: " + err.Error()})
		return
	}
	defer client.Conn.Close()

	morePatterns := []string{"---- More ----", "--More--", "More"}
	output, result, errMsg, ok := queryHuaweiTerminalMACByTelnet(client, req, morePatterns)
	if !ok {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: errMsg, Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
}

func queryHuaweiTerminalMACBySSH(client *Client, req SwitchRequest) (string, []TerminalMACInfo, string, bool) {
	commands := buildTerminalMACCommandsForMaker(req)
	options := InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(GetSwitchMaker(req.Maker)),
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		PromptSuffixes: []string{">", "#", "]"},
		ExitCommand:    switchInteractiveExitCommandForMaker(req.Maker),
		CommandDelay:   1200,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       true,
	}
	var lastOutput string
	var lastErrMsg string
	for _, cmd := range commands {
		output, err := client.RunInteractiveWithOptions([]string{cmd}, options)
		lastOutput = output
		if err != nil {
			lastErrMsg = "执行命令失败: " + err.Error()
			continue
		}
		if hasHuaweiCommandError(output) {
			lastErrMsg = "获取终端MAC失败"
			continue
		}
		return output, parseTerminalMACForMaker(output, req), "", true
	}
	return lastOutput, nil, lastErrMsg, false
}

func queryHuaweiTerminalMACByTelnet(client *TelnetClient, req SwitchRequest, morePatterns []string) (string, []TerminalMACInfo, string, bool) {
	commands := buildTerminalMACCommandsForMaker(req)
	var lastOutput string
	var lastErrMsg string
	for _, cmd := range commands {
		if err := client.SendCommand(cmd); err != nil {
			lastErrMsg = "发送命令失败: " + err.Error()
			continue
		}
		output, err := client.ReadPagedResponse(1200*time.Millisecond, morePatterns)
		lastOutput = output
		if err != nil {
			lastErrMsg = "执行命令失败: " + err.Error()
			continue
		}
		if hasHuaweiCommandError(output) {
			lastErrMsg = "获取终端MAC失败"
			continue
		}
		return output, parseTerminalMACForMaker(output, req), "", true
	}
	return lastOutput, nil, lastErrMsg, false
}

func (m *Module) handleCentecTerminalMACByRPC(w http.ResponseWriter, req SwitchRequest) {
	if req.Port == 0 {
		req.Port = 80
	}
	if req.Format == "" {
		req.Format = "text"
	}

	output, result, errMsg, ok := m.queryCentecTerminalMACByRPC(req)
	if !ok {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: errMsg, Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
}

func (m *Module) handleCentecTerminalMACBySSH(w http.ResponseWriter, req SwitchRequest) {
	if req.Port == 0 {
		req.Port = 22
	}
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
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "执行命令失败: " + err.Error() + ",请重新连接"})
		return
	}
	defer client.Close()

	output, result, errMsg, ok := queryCentecTerminalMACBySSH(client, req)
	if !ok {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: errMsg, Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
}

func (m *Module) handleCentecTerminalMACByTelnet(w http.ResponseWriter, req SwitchRequest) {
	if req.Port == 0 {
		req.Port = 23
	}
	client := &TelnetClient{
		owner:   m,
		Host:    req.Hostip,
		Port:    req.Port,
		Maker:   req.Maker,
		Timeout: 30 * time.Second,
	}
	if err := client.SwitchConnect(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Connection failed: " + err.Error()})
		return
	}
	defer client.Conn.Close()

	output, result, errMsg, ok := queryCentecTerminalMACByTelnet(client, req, []string{"---- More ----", "--More--", "More"})
	if !ok {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: errMsg, Data: output})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: result})
}

func (m *Module) queryCentecTerminalMACByRPC(req SwitchRequest) (string, []TerminalMACInfo, string, bool) {
	var lastOutput string
	var lastErrMsg string
	for _, cmd := range buildCentecTerminalMACCommands(req) {
		resp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, []string{cmd}, req.Format, SwitchLoginInfo{
			Username: req.Username,
			Password: req.Password,
			Maker:    req.Maker,
		})
		if err != nil {
			lastErrMsg = "执行命令失败: " + err.Error()
			continue
		}
		if resp.Error != nil {
			lastErrMsg = resp.Error.Message
			continue
		}
		output, err := extractRPCTextOutput(resp.Result)
		lastOutput = output
		if err != nil {
			lastErrMsg = "解析命令输出失败: " + err.Error()
			continue
		}
		if hasCentecCommandError(output) {
			lastErrMsg = "获取终端MAC失败"
			continue
		}
		return output, ParseCentecTerminalMAC(output, req), "", true
	}
	return lastOutput, nil, firstNonEmpty(lastErrMsg, "获取终端MAC失败"), false
}

func queryCentecTerminalMACBySSH(client *Client, req SwitchRequest) (string, []TerminalMACInfo, string, bool) {
	options := InteractiveOptions{
		PromptSuffixes: []string{"#", ">"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		CommandDelay:   1200,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       true,
	}
	var lastOutput string
	var lastErrMsg string
	for _, cmd := range buildCentecTerminalMACCommands(req) {
		output, err := client.RunInteractiveWithOptions([]string{cmd}, options)
		lastOutput = output
		if err != nil {
			lastErrMsg = "执行命令失败: " + err.Error()
			continue
		}
		if hasCentecCommandError(output) {
			lastErrMsg = "获取终端MAC失败"
			continue
		}
		return output, ParseCentecTerminalMAC(output, req), "", true
	}
	return lastOutput, nil, firstNonEmpty(lastErrMsg, "获取终端MAC失败"), false
}

func queryCentecTerminalMACByTelnet(client *TelnetClient, req SwitchRequest, morePatterns []string) (string, []TerminalMACInfo, string, bool) {
	var lastOutput string
	var lastErrMsg string
	for _, cmd := range buildCentecTerminalMACCommands(req) {
		output, err := runTelnetCommandWithPaging(client, cmd, morePatterns, 2*time.Second)
		lastOutput = output
		if err != nil {
			lastErrMsg = "执行命令失败: " + err.Error()
			continue
		}
		if hasCentecCommandError(output) {
			lastErrMsg = "获取终端MAC失败"
			continue
		}
		return output, ParseCentecTerminalMAC(output, req), "", true
	}
	return lastOutput, nil, firstNonEmpty(lastErrMsg, "获取终端MAC失败"), false
}

func buildCentecTerminalMACCommands(req SwitchRequest) []string {
	if iface := normalizeSwitchPortName(req.Interface); iface != "" {
		return []string{
			fmt.Sprintf("show mac address-table interface %s", iface),
			"show mac address-table",
		}
	}
	return []string{
		fmt.Sprintf("show mac address-table vlan %s", strings.TrimSpace(req.Vlan)),
		"show mac address-table",
	}
}

func buildHuaweiTerminalMACCommands(req SwitchRequest) []string {
	iface := strings.TrimSpace(req.Interface)
	if iface != "" {
		iface = normalizeSwitchPortName(iface)
		portType, portNumber, ok := splitSwitchPortTypeAndNumber(iface)
		if ok {
			return []string{
				fmt.Sprintf("display mac-address interface %s %s", portType, portNumber),
				fmt.Sprintf("display mac-address %s %s", portType, portNumber),
			}
		}
		return []string{
			fmt.Sprintf("display mac-address interface %s", iface),
			fmt.Sprintf("display mac-address %s", iface),
		}
	}
	return []string{fmt.Sprintf("display mac-address vlan %s", strings.TrimSpace(req.Vlan))}
}

func buildTerminalMACCommandsForMaker(req SwitchRequest) []string {
	switch GetSwitchMaker(req.Maker) {
	case "h3c":
		return buildH3CTerminalMACCommands(req)
	case "ruijie":
		return buildRuijieTerminalMACCommands(req)
	default:
		return buildHuaweiTerminalMACCommands(req)
	}
}

func buildRuijieTerminalMACCommands(req SwitchRequest) []string {
	iface := normalizeRuijiePortName(req.Interface)
	if iface != "" {
		if portType, portNumber, ok := splitSwitchPortTypeAndNumber(iface); ok {
			return []string{fmt.Sprintf("show mac-address-table interface %s %s", portType, portNumber)}
		}
		return []string{fmt.Sprintf("show mac-address-table interface %s", iface)}
	}
	return []string{fmt.Sprintf("show mac-address-table vlan %s", strings.TrimSpace(req.Vlan))}
}

func normalizeRuijiePortName(port string) string {
	port = normalizeSwitchPortName(port)
	if port == "" {
		return ""
	}
	aliases := []struct {
		pattern *regexp.Regexp
		target  string
	}{
		{regexp.MustCompile(`(?i)^gi(\d+(?:/\d+)+)$`), `GigabitEthernet$1`},
		{regexp.MustCompile(`(?i)^te(\d+(?:/\d+)+)$`), `TenGigabitEthernet$1`},
		{regexp.MustCompile(`(?i)^gigabitethernet(\d+(?:/\d+)+)$`), `GigabitEthernet$1`},
		{regexp.MustCompile(`(?i)^tengigabitethernet(\d+(?:/\d+)+)$`), `TenGigabitEthernet$1`},
	}
	for _, alias := range aliases {
		if alias.pattern.MatchString(port) {
			return alias.pattern.ReplaceAllString(port, alias.target)
		}
	}
	return port
}

func parseTerminalMACForMaker(output string, req SwitchRequest) []TerminalMACInfo {
	if GetSwitchMaker(req.Maker) == "ruijie" {
		return ParseRuijieTerminalMAC(output, req)
	}
	return ParseHuaweiTerminalMAC(output, strings.TrimSpace(req.Vlan))
}

// H3C Comware uses the same display mac-address family but requires the full interface type.

// H3C Comware uses the same display mac-address family but requires the full interface type.
func buildH3CTerminalMACCommands(req SwitchRequest) []string {
	iface := normalizeH3CPortName(req.Interface)
	if iface == "" {
		return []string{fmt.Sprintf("display mac-address vlan %s", strings.TrimSpace(req.Vlan))}
	}
	if portType, portNumber, ok := splitSwitchPortTypeAndNumber(iface); ok {
		return []string{
			fmt.Sprintf("display mac-address interface %s %s", portType, portNumber),
			fmt.Sprintf("display mac-address %s %s", portType, portNumber),
		}
	}
	return []string{
		fmt.Sprintf("display mac-address interface %s", iface),
		fmt.Sprintf("display mac-address %s", iface),
	}
}

func splitSwitchPortTypeAndNumber(port string) (string, string, bool) {
	port = normalizeSwitchPortName(port)
	if port == "" {
		return "", "", false
	}
	matches := regexp.MustCompile(`^(.+?)(\d+(?:/\d+)+)$`).FindStringSubmatch(port)
	if len(matches) != 3 {
		return "", "", false
	}
	if !regexp.MustCompile(`[A-Za-z]`).MatchString(matches[1]) {
		return "", "", false
	}
	return matches[1], matches[2], true
}

func ParseHuaweiTerminalMAC(output, vlan string) []TerminalMACInfo {
	lines := strings.Split(output, "\n")
	results := make([]TerminalMACInfo, 0)
	seen := make(map[string]struct{})
	vlan = strings.TrimSpace(vlan)
	macPattern := regexp.MustCompile(`(?i)([0-9a-f]{4}[-.][0-9a-f]{4}[-.][0-9a-f]{4}|[0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2})`)

	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "mac address table") || strings.Contains(lower, "total") || strings.Contains(lower, "vlan/") {
			continue
		}
		matches := macPattern.FindStringSubmatch(raw)
		if len(matches) < 2 {
			continue
		}

		fields := strings.Fields(raw)
		info := TerminalMACInfo{
			MAC:      normalizeTerminalMAC(matches[1]),
			VLAN:     vlan,
			RawEntry: raw,
		}
		for _, field := range fields {
			trimmed := strings.TrimSpace(field)
			if trimmed == "" {
				continue
			}
			if info.VLAN == "" && regexp.MustCompile(`^\d+$`).MatchString(trimmed) {
				info.VLAN = trimmed
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch upper {
			case "DYNAMIC", "STATIC", "BLACKHOLE", "SECURE", "STICKY":
				info.Type = strings.ToLower(trimmed)
			}
			if info.Port == "" && looksLikeSwitchPort(trimmed) {
				info.Port = normalizeSwitchPortName(trimmed)
			}
		}
		key := info.VLAN + "|" + info.MAC + "|" + info.Port
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, info)
	}
	return results
}

func ParseRuijieTerminalMAC(output string, req SwitchRequest) []TerminalMACInfo {
	lines := strings.Split(output, "\n")
	results := make([]TerminalMACInfo, 0)
	seen := make(map[string]struct{})
	wantPort := normalizeRuijiePortName(req.Interface)
	wantVLAN := strings.TrimSpace(req.Vlan)
	macPattern := regexp.MustCompile(`(?i)([0-9a-f]{4}[-.][0-9a-f]{4}[-.][0-9a-f]{4}|[0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2})`)
	portPattern := regexp.MustCompile(`(?i)\b((?:fastethernet|gigabitethernet|tengigabitethernet|ten-gigabitethernet|twenty-fivegige|fortygige|hundredgige|ethernet|gi|te|ge|xge)\s*\d+(?:/\d+){1,3})\b`)

	for _, line := range lines {
		raw := strings.TrimSpace(line)
		matches := macPattern.FindStringSubmatchIndex(raw)
		if len(matches) < 4 {
			continue
		}

		info := TerminalMACInfo{
			MAC:      normalizeTerminalMAC(raw[matches[2]:matches[3]]),
			RawEntry: raw,
		}
		for _, field := range strings.Fields(raw) {
			field = strings.Trim(field, ",;")
			if info.VLAN == "" && regexp.MustCompile(`^\d+$`).MatchString(field) {
				info.VLAN = field
				continue
			}
			switch strings.ToUpper(field) {
			case "DYNAMIC", "STATIC", "BLACKHOLE", "SECURE", "STICKY":
				info.Type = strings.ToLower(field)
			}
		}
		if portMatches := portPattern.FindStringSubmatch(raw[matches[3]:]); len(portMatches) == 2 {
			info.Port = normalizeRuijiePortName(portMatches[1])
		}
		if wantVLAN != "" && info.VLAN != wantVLAN {
			continue
		}
		if wantPort != "" && !strings.EqualFold(info.Port, wantPort) {
			continue
		}
		key := info.VLAN + "|" + info.MAC + "|" + info.Port
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, info)
	}
	return results
}

func ParseCentecTerminalMAC(output string, req SwitchRequest) []TerminalMACInfo {
	lines := strings.Split(output, "\n")
	results := make([]TerminalMACInfo, 0)
	seen := make(map[string]struct{})
	wantPort := normalizeSwitchPortName(req.Interface)
	wantVLAN := strings.TrimSpace(req.Vlan)
	macPattern := regexp.MustCompile(`(?i)([0-9a-f]{4}[-.][0-9a-f]{4}[-.][0-9a-f]{4}|[0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2}[:-][0-9a-f]{2})`)

	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "mac address") || strings.Contains(lower, "mac-address") || strings.Contains(lower, "total") {
			continue
		}
		matches := macPattern.FindStringSubmatch(raw)
		if len(matches) < 2 {
			continue
		}

		info := TerminalMACInfo{MAC: normalizeTerminalMAC(matches[1]), RawEntry: raw}
		for _, field := range strings.Fields(raw) {
			trimmed := strings.Trim(strings.TrimSpace(field), ",;")
			if trimmed == "" {
				continue
			}
			if info.VLAN == "" && regexp.MustCompile(`^\d+$`).MatchString(trimmed) {
				info.VLAN = trimmed
				continue
			}
			switch strings.ToUpper(trimmed) {
			case "DYNAMIC", "STATIC", "BLACKHOLE", "SECURE", "STICKY":
				info.Type = strings.ToLower(trimmed)
			}
			if info.Port == "" && looksLikeSwitchPort(trimmed) {
				info.Port = normalizeSwitchPortName(trimmed)
			}
		}
		if wantVLAN != "" && info.VLAN != wantVLAN {
			continue
		}
		if wantPort != "" && !switchPortNamesEqual(info.Port, wantPort) {
			continue
		}
		key := info.VLAN + "|" + info.MAC + "|" + info.Port
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, info)
	}
	return results
}

func normalizeTerminalMAC(mac string) string {
	mac = strings.TrimSpace(strings.ToLower(mac))
	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")
	if len(mac) != 12 {
		return strings.TrimSpace(strings.ToLower(mac))
	}
	return fmt.Sprintf("%s-%s-%s", mac[0:4], mac[4:8], mac[8:12])
}

func looksLikeSwitchPort(val string) bool {
	val = normalizeSwitchPortName(val)
	if val == "" {
		return false
	}
	portPrefixes := []string{
		"GE", "XGE", "10GE", "25GE", "40GE", "100GE",
		"GIGABITETHERNET", "TEN-GIGABITETHERNET", "METH", "ETH-", "ETH-TRUNK", "PORT-CHANNEL",
	}
	upper := strings.ToUpper(val)
	for _, prefix := range portPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return regexp.MustCompile(`^[A-Z-]+[0-9]+(/[0-9]+)+$`).MatchString(upper)
}
