// 由 tools/port_switch_batch2a.py 从 switchapi/switch_sniffer.go 整体搬运（采集/嗅探配置与后台任务）
package switchmod

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 说明：老实现把嗅探目录/配置路径/互斥锁/管理器都放在包级（m.snifferDir 等），
// 新模块改为 Module 字段（见 module.go 的 snifferDir/snifferConfigPath/snifferMu/snifferManager），
// 由 Migrate 注入，避免包级可变状态。

// 采集配置模块：描述嗅探任务、定时采集任务及其运行期状态。
const (
	switchCollectionShardDateLayout    = "2006-01-02"
	switchCollectionShardFileExtension = ".jsonl"
	switchCollectionScanBufferSize     = 32 * 1024 * 1024
)

type SwitchSnifferConfig struct {
	Enable         int    `json:"nEnable"`
	Type           int    `json:"nType"`
	DailyTime      string `json:"szDailyTime"`
	IntervalSecond int    `json:"nIntervalSecond"`
}

type SwitchSnifferManager struct {
	// owner 指向所属模块：管理器自身只持有任务状态，模块级资源（目录/锁/数据层）通过 owner 访问。
	owner       *Module
	taskManager *DynamicTaskManager
	taskID      string
}

type SwitchCollectionSettings struct {
	Enable          int `json:"nEnable"`
	IntervalSecond  int `json:"nIntervalSecond"`
	RetentionPeriod int `json:"nRetentionPeriod"`
}

type SwitchCollectionManager struct {
	// owner 指向所属模块（同 SwitchSnifferManager）。
	owner       *Module
	taskManager *DynamicTaskManager
	taskID      string
}

type SwitchCollectionInterfaceStat struct {
	ID           int    `json:"ID"`
	Interface    string `json:"szInterface"`
	InBroadcast  int64  `json:"nInBroadcast"`
	OutBroadcast int64  `json:"nOutBroadcast"`
	InDiscard    int64  `json:"nInDiscard"`
	OutDiscard   int64  `json:"nOutDiscard"`
	InPause      int64  `json:"nInPause"`
	OutPause     int64  `json:"nOutPause"`
	InError      int64  `json:"nInError"`
	OutError     int64  `json:"nOutError"`
	InCRC        int64  `json:"nInCRC"`
	OutCRC       int64  `json:"-"`
	InBytes      int64  `json:"nInBytes"`
	OutBytes     int64  `json:"nOutBytes"`
	InRate       int64  `json:"nInRate"`
	OutRate      int64  `json:"nOutRate"`
}

type SwitchCollectionSnapshot struct {
	CollectedAt string                          `json:"dtCollectedAt"`
	Interfaces  []SwitchCollectionInterfaceStat `json:"interfaces"`
}

type SwitchCollectionHostFile struct {
	Hostip          string                     `json:"szHostip"`
	RetentionPeriod int                        `json:"nRetentionPeriod"`
	Records         []SwitchCollectionSnapshot `json:"records"`
}

type SwitchCollectionRunResult struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

var (
	switchCollectionNumberPattern    = `(?:\d{1,3}(?:,\d{3})+|\d+)`
	switchCollectionSectionPattern   = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9-]*\d+(?:/\d+){0,3})\s+current state\s*[:：]\s*(.+?)\s*$`)
	switchCollectionRuijieSectionRe  = regexp.MustCompile(`(?i)^\s*([A-Za-z][A-Za-z0-9-]*\s*\d+(?:/\d+){0,3})\s+is\s+([^,]+)(?:,|$)`)
	switchCollectionStandalonePortRe = regexp.MustCompile(`^(?:Eth-Trunk\d+|Bridge-Aggregation\d+|(?:M-)?(?:Ten-GigabitEthernet|HundredGigE|FortyGigE|Twenty-FiveGigE|GigabitEthernet|XGigabitEthernet|Ethernet|GE|XGE|10GE|25GE|40GE|100GE)\d+(?:/\d+){0,3})$`)
	// Centec emits physical ports as eth-*, while aggregated interfaces use
	// names such as agg1, agg-1, bond2 and bond-2 in the status and detail output.
	switchCollectionCentecPortRe        = regexp.MustCompile(`(?i)^interface(?:\s+name)?\s*[:：]?\s*((?:eth-\d+(?:-\d+){1,3}|(?:agg|bond)-?\d+))\s*:?[[:space:]]*$`)
	switchCollectionCurrentStateRe      = regexp.MustCompile(`(?i)^(?:interface\s+)?current\s+state\s*[:：]`)
	switchCollectionInputLineRe         = regexp.MustCompile(`^input(?:\s*\(.*\))?\s*:`)
	switchCollectionOutputLineRe        = regexp.MustCompile(`^output(?:\s*\(.*\))?\s*:`)
	switchCollectionBroadcastPattern    = regexp.MustCompile(`(?i)\bBroadcast(?:\s+packets)?\s*:\s*(` + switchCollectionNumberPattern + `)|\b(` + switchCollectionNumberPattern + `)\s+broadcasts?\b`)
	switchCollectionDiscardPattern      = regexp.MustCompile(`(?i)\bDiscard(?:\s+packets)?\s*:\s*(` + switchCollectionNumberPattern + `)|\b(` + switchCollectionNumberPattern + `)\s+(?:packets\s+)?discard(?:ed|s)?\b|\b(` + switchCollectionNumberPattern + `)\s+(?:input|output)\s+discard\b|\b(` + switchCollectionNumberPattern + `)\s+drop(?:ped|s)?\b|\bBuffers\s+Purged\s*:\s*(` + switchCollectionNumberPattern + `)`)
	switchCollectionPausePattern        = regexp.MustCompile(`(?i)\bPause(?:\s+(?:packets|frames))?\s*:\s*(` + switchCollectionNumberPattern + `)|\b(` + switchCollectionNumberPattern + `)\s+pause(?:s|\s+(?:packets|frames|input|output))?\b`)
	switchCollectionErrorPattern        = regexp.MustCompile(`(?i)\bErrors?\s*:\s*(` + switchCollectionNumberPattern + `)|\b(` + switchCollectionNumberPattern + `)\s+(?:input|output)\s+errors?\b|\b(` + switchCollectionNumberPattern + `)\s+error packets?\b|\b(` + switchCollectionNumberPattern + `)\s+errors?\b`)
	switchCollectionCRCPattern          = regexp.MustCompile(`(?i)\bCRC\s*:\s*(` + switchCollectionNumberPattern + `)|\b(` + switchCollectionNumberPattern + `)\s+CRC\b`)
	switchCollectionInputBytesRe        = regexp.MustCompile(`(?i)^input(?:\s*\(.*\))?\s*:.*?(` + switchCollectionNumberPattern + `)\s+bytes\b`)
	switchCollectionOutputBytesRe       = regexp.MustCompile(`(?i)^output(?:\s*\(.*\))?\s*:.*?(` + switchCollectionNumberPattern + `)\s+bytes\b`)
	switchCollectionInputBytesAnyRe     = regexp.MustCompile(`(?i)\b(?:` + switchCollectionNumberPattern + `)\s+packets\s+input,\s*(` + switchCollectionNumberPattern + `)\s+bytes\b`)
	switchCollectionOutputBytesAnyRe    = regexp.MustCompile(`(?i)\b(?:` + switchCollectionNumberPattern + `)\s+packets\s+output,\s*(` + switchCollectionNumberPattern + `)\s+bytes\b`)
	switchCollectionInputBitsRateRe     = regexp.MustCompile(`(?i)(?:(?:last\s+)?` + switchCollectionNumberPattern + `\s+seconds\s+)?input(?:\s+rate)?\s*[:：]?\s*(?:` + switchCollectionNumberPattern + `\s+packets/sec(?:,|\s+))?(?:` + switchCollectionNumberPattern + `\s+bytes/sec(?:,|\s+))?(` + switchCollectionNumberPattern + `)\s+bits/sec`)
	switchCollectionInputBytesRateRe    = regexp.MustCompile(`(?i)(?:(?:last\s+)?` + switchCollectionNumberPattern + `\s+seconds\s+)?input(?:\s+rate)?\s*[:：]?\s*(?:` + switchCollectionNumberPattern + `\s+packets/sec(?:,|\s+))?(` + switchCollectionNumberPattern + `)\s+bytes/sec`)
	switchCollectionOutputBitsRateRe    = regexp.MustCompile(`(?i)(?:(?:last\s+)?` + switchCollectionNumberPattern + `\s+seconds\s+)?output(?:\s+rate)?\s*[:：]?\s*(?:` + switchCollectionNumberPattern + `\s+packets/sec(?:,|\s+))?(?:` + switchCollectionNumberPattern + `\s+bytes/sec(?:,|\s+))?(` + switchCollectionNumberPattern + `)\s+bits/sec`)
	switchCollectionOutputBytesRateRe   = regexp.MustCompile(`(?i)(?:(?:last\s+)?` + switchCollectionNumberPattern + `\s+seconds\s+)?output(?:\s+rate)?\s*[:：]?\s*(?:` + switchCollectionNumberPattern + `\s+packets/sec(?:,|\s+))?(` + switchCollectionNumberPattern + `)\s+bytes/sec`)
	switchCollectionInputBitsRateAnyRe  = regexp.MustCompile(`(?i)\binput\b.*?(` + switchCollectionNumberPattern + `)\s+bits/sec`)
	switchCollectionOutputBitsRateAnyRe = regexp.MustCompile(`(?i)\boutput\b.*?(` + switchCollectionNumberPattern + `)\s+bits/sec`)
)

// 配置接口模块：负责嗅探和采集任务的启停、参数落盘与任务重建。
func (m *Module) newSnifferManager() *SwitchSnifferManager {
	manager := &SwitchSnifferManager{
		owner: m, taskManager: NewDynamicTaskManager()}
	manager.taskManager.Start()
	return manager
}

func (m *Module) newCollectionManager() *SwitchCollectionManager {
	manager := &SwitchCollectionManager{
		owner: m, taskManager: NewDynamicTaskManager()}
	manager.taskManager.Start()
	return manager
}

func (m *Module) handleSwitchSnifferConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	act, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct: " + err.Error()})
		return
	}

	switch act {
	case 0:
		config, err := m.loadSwitchSnifferConfig()
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "读取交换机嗅探配置失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: append([]any{}, config)})
	case 1:
		var config SwitchSnifferConfig
		if err := m.decodeFormJSON(r, "saveData", &config); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		normalized, err := normalizeSwitchSnifferConfig(config)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		if err := m.saveSwitchSnifferConfig(normalized); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "保存交换机嗅探配置失败: " + err.Error()})
			return
		}
		m.snifferManager.Apply(normalized)
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: normalized})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) initSwitchSniffer() {
	config, err := m.loadSwitchSnifferConfig()
	if err != nil {
		log.Printf("load switch sniffer config failed: %v", err)
		return
	}
	m.snifferManager.Apply(config)
}

func (m *Module) handleSwitchCollectionSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}

	act, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct: " + err.Error()})
		return
	}

	switch act {
	case 0:
		config, err := m.loadSwitchCollectionSettings()
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "读取交换机采集配置失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: append([]any{}, config)})
	case 1:
		var config SwitchCollectionSettings
		if err := m.decodeFormJSON(r, "saveData", &config); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		normalized, err := normalizeSwitchCollectionSettings(config)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: err.Error()})
			return
		}
		if err := m.saveSwitchCollectionSettings(normalized); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "保存交换机采集配置失败: " + err.Error()})
			return
		}
		m.collectionManager.Apply(normalized)
		if err := m.pruneSwitchCollectionDataDir(normalized.RetentionPeriod, time.Now()); err != nil {
			log.Printf("prune switch collection data after settings save failed: %v", err)
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: normalized})
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid nAct"})
	}
}

func (m *Module) initSwitchCollection() {
	config, err := m.loadSwitchCollectionSettings()
	if err != nil {
		log.Printf("load switch collection settings failed: %v", err)
		return
	}
	m.collectionManager.Apply(config)
	if err := m.pruneSwitchCollectionDataDir(config.RetentionPeriod, time.Now()); err != nil {
		log.Printf("prune switch collection data during init failed: %v", err)
	}
}

func (m *SwitchSnifferManager) Apply(config SwitchSnifferConfig) {
	m.owner.snifferMu.Lock()
	defer m.owner.snifferMu.Unlock()

	if m.taskID != "" {
		m.taskManager.StopTaskByID(m.taskID)
		m.taskID = ""
	}
	if config.Enable == 0 {
		return
	}

	switch config.Type {
	case 1:
		taskID, err := m.taskManager.AddDynamicTask("switch-sniffer-daily", config.DailyTime, 0, func() {
			m.owner.runSwitchSnifferOnce()
		})
		if err == nil {
			m.taskID = taskID
		}
	default:
		job, err := m.taskManager.scheduler.Every(config.IntervalSecond).Second().Tag("switch-sniffer-interval").Do(func() {
			m.owner.runSwitchSnifferOnce()
		})
		if err == nil {
			m.taskID = "switch-sniffer-interval"
			m.taskManager.tasks.Store(m.taskID, job)
		}
	}
}

func (m *SwitchCollectionManager) Apply(config SwitchCollectionSettings) {
	m.owner.collectionMu.Lock()
	defer m.owner.collectionMu.Unlock()

	if m.taskID != "" {
		m.taskManager.StopTaskByID(m.taskID)
		m.taskID = ""
	}
	if config.Enable == 0 {
		return
	}

	job, err := m.taskManager.scheduler.Every(config.IntervalSecond).Second().Tag("switch-collection-interval").Do(func() {
		result := m.owner.runSwitchCollectionOnce(config)
		log.Printf("switch collection finished: total=%d success=%d failed=%d skipped=%d", result.Total, result.Success, result.Failed, result.Skipped)
	})
	if err == nil {
		m.taskID = "switch-collection-interval"
		m.taskManager.tasks.Store(m.taskID, job)
	}
}

func (m *Module) loadSwitchSnifferConfig() (SwitchSnifferConfig, error) {
	config := SwitchSnifferConfig{
		Enable:         0,
		Type:           2,
		DailyTime:      "",
		IntervalSecond: 300,
	}
	if err := os.MkdirAll(m.snifferDir, 0755); err != nil {
		return config, err
	}
	content, err := os.ReadFile(m.snifferConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(content, &config); err != nil {
		return config, err
	}
	return normalizeSwitchSnifferConfig(config)
}

func (m *Module) loadSwitchCollectionSettings() (SwitchCollectionSettings, error) {
	config := SwitchCollectionSettings{
		Enable:          0,
		IntervalSecond:  300,
		RetentionPeriod: 7,
	}
	if err := os.MkdirAll(filepath.Dir(m.collectionSettingsPath), 0755); err != nil {
		return config, err
	}
	content, err := os.ReadFile(m.collectionSettingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(content, &config); err != nil {
		return config, err
	}
	return normalizeSwitchCollectionSettings(config)
}

func (m *Module) saveSwitchSnifferConfig(config SwitchSnifferConfig) error {
	if err := os.MkdirAll(filepath.Dir(m.snifferConfigPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.snifferConfigPath, data, 0644)
}

func (m *Module) saveSwitchCollectionSettings(config SwitchCollectionSettings) error {
	if err := os.MkdirAll(filepath.Dir(m.collectionSettingsPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.collectionSettingsPath, data, 0644)
}

func normalizeSwitchSnifferConfig(config SwitchSnifferConfig) (SwitchSnifferConfig, error) {
	if config.Enable != 0 {
		config.Enable = 1
	}
	if config.Type != 1 {
		config.Type = 2
	}
	config.DailyTime = strings.TrimSpace(config.DailyTime)

	if config.IntervalSecond == 0 {
		config.IntervalSecond = 300
	}
	if config.IntervalSecond < 30 || config.IntervalSecond > 86400 {
		return config, fmt.Errorf("嗅探间隔必须在 30~86400 秒之间")
	}

	if config.Type == 1 {
		if config.DailyTime == "" {
			return config, fmt.Errorf("固定时间嗅探必须设置嗅探时间")
		}
		if _, err := time.Parse("15:04", config.DailyTime); err != nil {
			return config, fmt.Errorf("嗅探时间格式错误，应为 HH:MM")
		}
	}
	return config, nil
}

func normalizeSwitchCollectionSettings(config SwitchCollectionSettings) (SwitchCollectionSettings, error) {
	if config.Enable != 0 {
		config.Enable = 1
	}
	if config.IntervalSecond <= 0 {
		config.IntervalSecond = 300
	}
	if config.RetentionPeriod <= 0 {
		config.RetentionPeriod = 7
	}
	if config.IntervalSecond > 86400 {
		return config, fmt.Errorf("采集间隔不能超过 86400 秒")
	}
	if config.RetentionPeriod > 3650 {
		return config, fmt.Errorf("保存周期不能超过 3650 天")
	}
	return config, nil
}

type SwitchSnifferRunResult struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Offline int `json:"offline"`
	Skipped int `json:"skipped"`
}

func (m *Module) runSwitchSnifferOnce() SwitchSnifferRunResult {
	result := SwitchSnifferRunResult{}
	// 交换机接口的注册信息验证：未注册/已过期时跳过本次嗅探，不操作交换机。
	if err := m.requireRegistration(); err != nil {
		log.Printf("switch sniffer skipped: %v", err)
		return result
	}
	records, err := m.cipher.loadLoginRecords()
	if err != nil {
		log.Printf("switch sniffer load login records failed: %v", err)
		return result
	}
	result.Total = len(records)
	for _, record := range records {
		outcome, err := m.sniffAndUpdateSwitchLoginRecord(record)
		if err != nil {
			log.Printf("switch sniffer update failed for %s: %v", record.Hostip, err)
		}
		switch outcome {
		case "online":
			result.Success++
		case "offline":
			result.Offline++
		default:
			result.Skipped++
		}
	}
	return result
}

func (m *Module) runSwitchCollectionOnce(config SwitchCollectionSettings) SwitchCollectionRunResult {
	m.collectionRunMu.Lock()
	defer m.collectionRunMu.Unlock()

	result := SwitchCollectionRunResult{}
	// 交换机接口的注册信息验证：未注册/已过期时跳过本次采集，不操作交换机。
	if err := m.requireRegistration(); err != nil {
		log.Printf("switch collection skipped: %v", err)
		return result
	}
	records, skipped, err := m.loadOnlineSwitchCollectionRecords()
	if err != nil {
		log.Printf("switch collection load online devices failed: %v", err)
		return result
	}
	result.Total = len(records)
	result.Skipped = skipped
	result.Total += skipped

	now := time.Now()
	retentionPeriod := m.loadCurrentSwitchCollectionRetentionPeriod(config.RetentionPeriod)
	if err := m.pruneSwitchCollectionDataDir(retentionPeriod, now); err != nil {
		log.Printf("prune switch collection data before run failed: %v", err)
	}
	for _, record := range records {
		hostip := strings.TrimSpace(record.Hostip)
		if hostip == "" {
			result.Skipped++
			continue
		}

		interfaces, err := m.collectSwitchCollectionInterfaces(record)
		if err != nil {
			result.Failed++
			log.Printf("switch collection failed for %s: %v", hostip, err)
			continue
		}
		if err := m.appendSwitchCollectionSnapshot(hostip, retentionPeriod, interfaces, now); err != nil {
			result.Failed++
			log.Printf("switch collection save failed for %s: %v", hostip, err)
			continue
		}
		result.Success++
	}
	return result
}

// loadOnlineSwitchCollectionRecords limits collection to ledger devices that
// have been confirmed online and have a saved successful-login record.
func (m *Module) loadOnlineSwitchCollectionRecords() ([]SwitchLoginRecord, int, error) {
	if m.store.db == nil {
		return nil, 0, fmt.Errorf("设备台账数据库未初始化")
	}
	devices, err := m.store.GetAllDeviceInformation()
	if err != nil {
		return nil, 0, err
	}

	onlineHosts := make(map[string]struct{})
	for _, device := range devices {
		if device.AutoEnable == 0 {
			continue
		}
		hostip := strings.TrimSpace(device.ManageIP)
		if hostip != "" {
			onlineHosts[hostip] = struct{}{}
		}
	}
	if len(onlineHosts) == 0 {
		return []SwitchLoginRecord{}, 0, nil
	}

	records, err := m.cipher.loadLoginRecords()
	if err != nil {
		return nil, 0, err
	}
	selected := make([]SwitchLoginRecord, 0, len(onlineHosts))
	seenHosts := make(map[string]struct{})
	for _, record := range records {
		hostip := strings.TrimSpace(record.Hostip)
		if _, online := onlineHosts[hostip]; !online {
			continue
		}
		if _, seen := seenHosts[hostip]; seen {
			continue
		}
		selected = append(selected, record)
		seenHosts[hostip] = struct{}{}
	}
	return selected, len(onlineHosts) - len(selected), nil
}

// 采集执行模块：负责下发 display/show interface 并抽取统一的接口统计字段。
func (m *Module) collectSwitchCollectionInterfaces(record SwitchLoginRecord) ([]SwitchCollectionInterfaceStat, error) {
	req, err := m.buildSwitchRequestFromLoginRecord(record)
	if err != nil {
		return nil, err
	}
	output, err := m.collectSwitchInterfaceOutput(req)
	if err != nil {
		return nil, err
	}
	interfaces := ParseSwitchCollectionInterfaceStats(output)
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("未解析到接口采集数据")
	}
	if err := m.assignSwitchCollectionInterfaceIDsFromDeviceInfo(record.Hostip, interfaces); err != nil {
		log.Printf("switch collection interface ID mapping failed for %s: %v; using collection order", record.Hostip, err)
		assignSwitchCollectionInterfaceIDs(interfaces)
	}
	return interfaces, nil
}

func (m *Module) collectSwitchInterfaceOutput(req SwitchRequest) (string, error) {
	maker := GetSwitchMaker(req.Maker)
	command := switchInterfaceCollectionCommandForMaker(maker)
	if command == "" {
		return "", fmt.Errorf("接口采集暂仅支持华为/H3C/盛科/锐捷")
	}
	if req.Format == "" {
		req.Format = "text"
	}

	switch req.Type {
	case 1:
		return m.collectSwitchInterfaceOutputByRPC(req, command)
	case 2:
		return collectSwitchInterfaceOutputByTelnet(req, command)
	case 3:
		return m.collectSwitchInterfaceOutputBySSH(req, command)
	default:
		return "", fmt.Errorf("未识别的连接方式")
	}
}

func (m *Module) collectSwitchInterfaceOutputByRPC(req SwitchRequest, command string) (string, error) {
	cmds := []string{command}
	resp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
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
	output, err := extractRPCTextOutput(resp.Result)
	if err != nil {
		return "", err
	}
	if pattern := matchSwitchCollectionCommandError(output); pattern != "" {
		return "", fmt.Errorf("display interface 执行失败: %s", pattern)
	}
	return output, nil
}

func collectSwitchInterfaceOutputByTelnet(req SwitchRequest, command string) (string, error) {
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

	output, err := runTelnetCommandWithPaging(client, command, []string{"---- More ----", "--More--", "More"}, 3*time.Second)
	if err != nil {
		return "", err
	}
	if hasHuaweiCommandError(output) {
		return "", fmt.Errorf("display interface 执行失败")
	}
	return output, nil
}

func (m *Module) collectSwitchInterfaceOutputBySSH(req SwitchRequest, command string) (string, error) {
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

	output, err := client.RunInteractiveWithOptions([]string{command}, InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(GetSwitchMaker(req.Maker)),
		PromptSuffixes: []string{">", "#", "]"},
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		ExitCommand:    switchInteractiveExitCommandForMaker(req.Maker),
		CommandDelay:   1200,
		ReadTimeout:    3 * time.Second,
		AutoExit:       true,
	})
	if err != nil {
		return "", err
	}
	if pattern := matchSwitchCollectionCommandError(output); pattern != "" {
		return "", fmt.Errorf("display interface 执行失败: %s", pattern)
	}
	return output, nil
}

func switchCollectionPreCommandsForMaker(maker string) []string {
	switch maker {
	case "huawei":
		return []string{"screen-length 0 temporary"}
	default:
		return nil
	}
}

func matchSwitchCollectionCommandError(output string) string {
	for _, raw := range strings.Split(normalizeCLIOutput(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		if strings.HasPrefix(lower, "error:") {
			switch {
			case strings.Contains(lower, "wrong parameter"),
				strings.Contains(lower, "unrecognized"),
				strings.Contains(lower, "incomplete command"),
				strings.Contains(lower, "too many parameters"),
				strings.Contains(lower, "operation failed"),
				strings.Contains(lower, "command failed"),
				strings.Contains(lower, "failed to"):
				return line
			default:
				continue
			}
		}

		switch {
		case strings.Contains(lower, "unrecognized command"),
			strings.Contains(lower, "wrong parameter found"),
			strings.Contains(lower, "incomplete command"),
			strings.Contains(lower, "too many parameters"),
			strings.Contains(lower, "operation failed"),
			strings.Contains(lower, "command failed"),
			strings.Contains(lower, "failed to"):
			return line
		}
	}
	return ""
}

func switchInterfaceCollectionCommandForMaker(maker string) string {
	switch maker {
	case "huawei", "h3c":
		return "display interface"
	case "centec", "ruijie":
		return "show interface"
	default:
		return ""
	}
}

func extractRPCTextOutput(raw json.RawMessage) (string, error) {
	infos := []map[string]interface{}{}
	if err := json.Unmarshal(raw, &infos); err != nil {
		return "", fmt.Errorf("rpc error: %v", err)
	}

	var output strings.Builder
	for _, info := range infos {
		if val, ok := info["errorCode"]; ok {
			desc := ""
			if str, ok := info["errorDesc"].(string); ok {
				desc = strings.TrimSpace(str)
			}
			return "", fmt.Errorf("rpc command error: %v %s", val, desc)
		}
		source, _ := info["sourceDetails"].(string)
		if strings.TrimSpace(source) == "" {
			continue
		}
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(source)
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", fmt.Errorf("未获取到命令输出")
	}
	return output.String(), nil
}

// 解析模块：从不同厂商接口输出中提取广播、丢弃、速率和字节计数。
func ParseSwitchCollectionInterfaceStats(data string) []SwitchCollectionInterfaceStat {
	data = normalizeCLIOutput(data)
	lines := strings.Split(data, "\n")
	results := make([]SwitchCollectionInterfaceStat, 0)
	var current *SwitchCollectionInterfaceStat
	var pendingPort string
	section := ""

	flush := func() {
		if current == nil || current.Interface == "" {
			current = nil
			return
		}
		results = append(results, *current)
		current = nil
		section = ""
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if matches := switchCollectionSectionPattern.FindStringSubmatch(line); len(matches) == 3 {
			flush()
			current = &SwitchCollectionInterfaceStat{Interface: normalizeSwitchPortName(matches[1])}
			pendingPort = ""
			section = ""
			continue
		}
		if matches := switchCollectionRuijieSectionRe.FindStringSubmatch(line); len(matches) == 3 {
			flush()
			current = &SwitchCollectionInterfaceStat{Interface: normalizeSwitchPortName(matches[1])}
			pendingPort = ""
			section = ""
			continue
		}
		if isSwitchCollectionStandalonePort(line) {
			flush()
			pendingPort = normalizeSwitchPortName(line)
			continue
		}
		if matches := switchCollectionCentecPortRe.FindStringSubmatch(line); len(matches) == 2 {
			flush()
			pendingPort = normalizeSwitchPortName(matches[1])
			continue
		}
		if pendingPort != "" && switchCollectionCurrentStateRe.MatchString(line) {
			current = &SwitchCollectionInterfaceStat{Interface: pendingPort}
			pendingPort = ""
			section = ""
			continue
		}
		if current == nil {
			continue
		}

		switch {
		case isSwitchCollectionInputLine(line):
			section = "input"
		case isSwitchCollectionOutputLine(line):
			section = "output"
		}

		broadcast := sumCollectionPattern(line, switchCollectionBroadcastPattern)
		discard := sumCollectionPattern(line, switchCollectionDiscardPattern)
		pause := sumCollectionPattern(line, switchCollectionPausePattern)
		errCount := sumCollectionPattern(line, switchCollectionErrorPattern)
		crc := sumCollectionPattern(line, switchCollectionCRCPattern)
		inBytes, hasInBytes := firstCollectionInt64(line, switchCollectionInputBytesRe)
		outBytes, hasOutBytes := firstCollectionInt64(line, switchCollectionOutputBytesRe)
		if !hasInBytes {
			inBytes, hasInBytes = firstCollectionInt64(line, switchCollectionInputBytesAnyRe)
		}
		if !hasOutBytes {
			outBytes, hasOutBytes = firstCollectionInt64(line, switchCollectionOutputBytesAnyRe)
		}
		inRate, hasInRate := parseSwitchCollectionRate(line, switchCollectionInputBitsRateRe, switchCollectionInputBytesRateRe)
		outRate, hasOutRate := parseSwitchCollectionRate(line, switchCollectionOutputBitsRateRe, switchCollectionOutputBytesRateRe)
		if !hasInRate {
			inRate, hasInRate = firstCollectionInt64(line, switchCollectionInputBitsRateAnyRe)
		}
		if !hasOutRate {
			outRate, hasOutRate = firstCollectionInt64(line, switchCollectionOutputBitsRateAnyRe)
		}

		switch section {
		case "input":
			current.InBroadcast += broadcast
			current.InDiscard += discard
			current.InPause += pause
			current.InError += errCount
			current.InCRC += crc
		case "output":
			current.OutBroadcast += broadcast
			current.OutDiscard += discard
			current.OutPause += pause
			current.OutError += errCount
			current.OutCRC += crc
		}

		if hasInRate {
			current.InRate = inRate
		}
		if hasOutRate {
			current.OutRate = outRate
		}
		if hasInBytes {
			current.InBytes = inBytes
		}
		if hasOutBytes {
			current.OutBytes = outBytes
		}
	}
	flush()

	sortSwitchCollectionInterfaces(results)
	return results
}

func sortSwitchCollectionInterfaces(interfaces []SwitchCollectionInterfaceStat) {
	sort.Slice(interfaces, func(i, j int) bool {
		return compareSwitchCollectionInterfaceName(interfaces[i].Interface, interfaces[j].Interface) < 0
	})
}

func compareSwitchCollectionInterfaceName(a, b string) int {
	ia := 0
	ib := 0
	for ia < len(a) && ib < len(b) {
		if isASCIIDigit(a[ia]) && isASCIIDigit(b[ib]) {
			ja := ia
			for ja < len(a) && isASCIIDigit(a[ja]) {
				ja++
			}
			jb := ib
			for jb < len(b) && isASCIIDigit(b[jb]) {
				jb++
			}

			na := strings.TrimLeft(a[ia:ja], "0")
			nb := strings.TrimLeft(b[ib:jb], "0")
			if na == "" {
				na = "0"
			}
			if nb == "" {
				nb = "0"
			}
			switch {
			case len(na) < len(nb):
				return -1
			case len(na) > len(nb):
				return 1
			case na < nb:
				return -1
			case na > nb:
				return 1
			}

			ia = ja
			ib = jb
			continue
		}

		ra := a[ia]
		rb := b[ib]
		if ra < rb {
			return -1
		}
		if ra > rb {
			return 1
		}
		ia++
		ib++
	}

	switch {
	case ia == len(a) && ib == len(b):
		return 0
	case ia == len(a):
		return -1
	default:
		return 1
	}
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isSwitchCollectionStandalonePort(line string) bool {
	line = normalizeSwitchPortName(line)
	if line == "" {
		return false
	}
	return switchCollectionStandalonePortRe.MatchString(line)
}

func isSwitchCollectionInputLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return switchCollectionInputLineRe.MatchString(lower) ||
		strings.Contains(lower, "packets input") ||
		strings.Contains(lower, "input rate")
}

func isSwitchCollectionOutputLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return switchCollectionOutputLineRe.MatchString(lower) ||
		strings.Contains(lower, "packets output") ||
		strings.Contains(lower, "output rate")
}

func sumCollectionPattern(line string, pattern *regexp.Regexp) int64 {
	matches := pattern.FindAllStringSubmatch(line, -1)
	var total int64
	for _, match := range matches {
		for _, raw := range match[1:] {
			if raw == "" {
				continue
			}
			value, ok := parseCollectionInt64(raw)
			if !ok {
				continue
			}
			total += value
		}
	}
	return total
}

func parseSwitchCollectionRate(line string, bitsPattern, bytesPattern *regexp.Regexp) (int64, bool) {
	if value, ok := firstCollectionInt64(line, bitsPattern); ok {
		return value, true
	}
	if value, ok := firstCollectionInt64(line, bytesPattern); ok {
		return value * 8, true
	}
	return 0, false
}

func firstCollectionInt64(line string, pattern *regexp.Regexp) (int64, bool) {
	match := pattern.FindStringSubmatch(line)
	if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
		return 0, false
	}
	return parseCollectionInt64(match[1])
}

func parseCollectionInt64(raw string) (int64, bool) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), ",", "")
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func (m *Module) appendSwitchCollectionSnapshot(hostip string, retentionPeriod int, interfaces []SwitchCollectionInterfaceStat, now time.Time) error {
	m.collectionFileMu.Lock()
	defer m.collectionFileMu.Unlock()

	assignSwitchCollectionInterfaceIDs(interfaces)
	sortedInterfaces := append([]SwitchCollectionInterfaceStat(nil), interfaces...)
	sortSwitchCollectionInterfaces(sortedInterfaces)

	if err := m.pruneSwitchCollectionHostStorage(hostip, retentionPeriod, now); err != nil {
		return err
	}

	return m.store.saveSwitchCollectionSnapshotToDB(hostip, SwitchCollectionSnapshot{
		CollectedAt: now.Format("2006-01-02 15:04:05"),
		Interfaces:  sortedInterfaces,
	})
}

func pruneSwitchCollectionSnapshots(records []SwitchCollectionSnapshot, retentionPeriod int, now time.Time) []SwitchCollectionSnapshot {
	if retentionPeriod <= 0 {
		retentionPeriod = 7
	}
	cutoff := now.AddDate(0, 0, -retentionPeriod)
	filtered := make([]SwitchCollectionSnapshot, 0, len(records))
	for _, record := range records {
		if record.Interfaces == nil {
			record.Interfaces = []SwitchCollectionInterfaceStat{}
		}
		sortSwitchCollectionInterfaces(record.Interfaces)
		timestamp, err := parseSwitchCollectionTimestamp(record.CollectedAt)
		if err == nil && timestamp.Before(cutoff) {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func (s *Store) switchCollectionShardFilePath(hostip string, day time.Time) string {
	return filepath.Join(s.collectionHostDirPath(hostip), day.Format(switchCollectionShardDateLayout)+switchCollectionShardFileExtension)
}

func switchCollectionDayKey(ts time.Time) string {
	return ts.Format(switchCollectionShardDateLayout)
}

func normalizeSwitchCollectionSnapshot(snapshot SwitchCollectionSnapshot) SwitchCollectionSnapshot {
	if snapshot.Interfaces == nil {
		snapshot.Interfaces = []SwitchCollectionInterfaceStat{}
	}
	assignSwitchCollectionInterfaceIDs(snapshot.Interfaces)
	sortSwitchCollectionInterfaces(snapshot.Interfaces)
	return snapshot
}

func assignSwitchCollectionInterfaceIDs(interfaces []SwitchCollectionInterfaceStat) {
	for index := range interfaces {
		if interfaces[index].ID == 0 {
			interfaces[index].ID = index + 1
		}
	}
}

func (m *Module) assignSwitchCollectionInterfaceIDsFromDeviceInfo(hostip string, interfaces []SwitchCollectionInterfaceStat) error {
	detail, err := m.loadDeviceInformation(strings.TrimSpace(hostip))
	if err != nil {
		return err
	}
	assignSwitchInterfaceIDs(detail.Interfaces)

	idsByPort := make(map[string]int, len(detail.Interfaces)*2)
	for _, item := range detail.Interfaces {
		if item.ID <= 0 {
			continue
		}
		for _, alias := range buildSwitchPortQueryAliases(item.Port) {
			idsByPort[alias] = item.ID
		}
	}
	for index := range interfaces {
		for _, alias := range buildSwitchPortQueryAliases(interfaces[index].Interface) {
			if id, ok := idsByPort[alias]; ok {
				interfaces[index].ID = id
				break
			}
		}
	}
	assignSwitchCollectionInterfaceIDs(interfaces)
	return nil
}

func parseSwitchCollectionShardDayKey(name string) (string, bool) {
	if filepath.Ext(name) != switchCollectionShardFileExtension {
		return "", false
	}
	day := strings.TrimSuffix(name, switchCollectionShardFileExtension)
	_, err := time.Parse(switchCollectionShardDateLayout, day)
	if err != nil {
		return "", false
	}
	return day, true
}

// 存储模块：负责采集快照、分片文件、缓存设备信息和保留期相关读写。
func (m *Module) loadSwitchCollectionHostFile(path string) (SwitchCollectionHostFile, bool, error) {
	hostip := strings.TrimSpace(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	content := SwitchCollectionHostFile{
		Hostip:  hostip,
		Records: []SwitchCollectionSnapshot{},
	}
	if hostip == "" {
		return content, false, nil
	}

	dbRecords, existed, err := m.store.loadAllSwitchCollectionSnapshotsForHostFromDB(hostip)
	if err != nil {
		return content, false, err
	}
	content.RetentionPeriod = m.loadCurrentSwitchCollectionRetentionPeriod(0)
	content.Records = dbRecords
	return content, existed, nil
}

func (s *Store) listSwitchCollectionShardPaths(hostip string) ([]string, error) {
	entries, err := os.ReadDir(s.collectionHostDirPath(hostip))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := parseSwitchCollectionShardDayKey(entry.Name()); !ok {
			continue
		}
		paths = append(paths, filepath.Join(s.collectionHostDirPath(hostip), entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Store) loadAllSwitchCollectionSnapshotsForHost(hostip string) ([]SwitchCollectionSnapshot, bool, error) {
	paths, err := s.listSwitchCollectionShardPaths(hostip)
	if err != nil {
		return nil, false, err
	}
	if len(paths) == 0 {
		return []SwitchCollectionSnapshot{}, false, nil
	}

	records := make([]SwitchCollectionSnapshot, 0)
	for _, path := range paths {
		snapshots, err := s.loadSwitchCollectionSnapshotsFromShard(path)
		if err != nil {
			return nil, true, err
		}
		records = append(records, snapshots...)
	}
	sort.Slice(records, func(i, j int) bool {
		ti, ierr := parseSwitchCollectionTimestamp(records[i].CollectedAt)
		tj, jerr := parseSwitchCollectionTimestamp(records[j].CollectedAt)
		switch {
		case ierr != nil && jerr != nil:
			return records[i].CollectedAt < records[j].CollectedAt
		case ierr != nil:
			return false
		case jerr != nil:
			return true
		default:
			return ti.Before(tj)
		}
	})
	return records, true, nil
}

func (s *Store) loadSwitchCollectionSnapshotsFromShard(path string) ([]SwitchCollectionSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []SwitchCollectionSnapshot{}, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), switchCollectionScanBufferSize)
	snapshots := make([]SwitchCollectionSnapshot, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var snapshot SwitchCollectionSnapshot
		if err := json.Unmarshal([]byte(line), &snapshot); err != nil {
			return nil, fmt.Errorf("parse switch collection shard %s line %d failed: %w", path, lineNo, err)
		}
		snapshots = append(snapshots, normalizeSwitchCollectionSnapshot(snapshot))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func writeSwitchCollectionShardFile(path string, snapshots []SwitchCollectionSnapshot) error {
	if len(snapshots) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, snapshot := range snapshots {
		data, err := json.Marshal(normalizeSwitchCollectionSnapshot(snapshot))
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmpFile.Write(buf.Bytes()); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *Store) rewriteSwitchCollectionHostShards(hostip string, records []SwitchCollectionSnapshot) error {
	legacyPath := s.switchCollectionLegacyHostFilePath(hostip)
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	hostDir := s.collectionHostDirPath(hostip)
	if err := os.RemoveAll(hostDir); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	grouped := make(map[string][]SwitchCollectionSnapshot)
	for _, record := range records {
		ts, err := parseSwitchCollectionTimestamp(record.CollectedAt)
		if err != nil {
			return err
		}
		dayKey := switchCollectionDayKey(ts)
		grouped[dayKey] = append(grouped[dayKey], normalizeSwitchCollectionSnapshot(record))
	}

	dayKeys := make([]string, 0, len(grouped))
	for dayKey := range grouped {
		dayKeys = append(dayKeys, dayKey)
	}
	sort.Strings(dayKeys)
	for _, dayKey := range dayKeys {
		records := grouped[dayKey]
		sort.Slice(records, func(i, j int) bool {
			ti, _ := parseSwitchCollectionTimestamp(records[i].CollectedAt)
			tj, _ := parseSwitchCollectionTimestamp(records[j].CollectedAt)
			return ti.Before(tj)
		})
		day, _ := time.ParseInLocation(switchCollectionShardDateLayout, dayKey, time.Local)
		if err := writeSwitchCollectionShardFile(s.switchCollectionShardFilePath(hostip, day), records); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateLegacySwitchCollectionHostFile(hostip string) error {
	legacyPath := s.switchCollectionLegacyHostFilePath(hostip)
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return os.Remove(legacyPath)
	}

	paths, err := s.listSwitchCollectionShardPaths(hostip)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		return os.Remove(legacyPath)
	}

	var content SwitchCollectionHostFile
	if err := json.Unmarshal(raw, &content); err != nil {
		return err
	}
	if strings.TrimSpace(content.Hostip) == "" {
		content.Hostip = hostip
	}
	if err := s.rewriteSwitchCollectionHostShards(content.Hostip, content.Records); err != nil {
		return err
	}
	return nil
}

func (m *Module) pruneSwitchCollectionHostStorage(hostip string, retentionPeriod int, now time.Time) error {
	if retentionPeriod <= 0 {
		retentionPeriod = 7
	}
	return m.store.pruneSwitchCollectionSnapshotsInDB(hostip, retentionPeriod, now)
}

func (m *Module) loadCurrentSwitchCollectionRetentionPeriod(fallback int) int {
	retentionPeriod := fallback
	if retentionPeriod <= 0 {
		retentionPeriod = 7
	}

	config, err := m.loadSwitchCollectionSettings()
	if err != nil {
		return retentionPeriod
	}
	if config.RetentionPeriod <= 0 {
		return retentionPeriod
	}
	return config.RetentionPeriod
}

func (m *Module) pruneSwitchCollectionDataDir(retentionPeriod int, now time.Time) error {
	m.collectionFileMu.Lock()
	defer m.collectionFileMu.Unlock()

	if retentionPeriod <= 0 {
		retentionPeriod = 7
	}
	return m.store.pruneSwitchCollectionSnapshotsInDB("", retentionPeriod, now)
}

func parseSwitchCollectionTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	if ts, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("invalid collection timestamp: %s", raw)
}

func (m *Module) sniffAndUpdateSwitchLoginRecord(record SwitchLoginRecord) (string, error) {
	hostip := strings.TrimSpace(record.Hostip)
	if hostip == "" {
		return "skipped", nil
	}

	existing, _ := m.loadDeviceInformation(hostip)
	req, err := m.buildSwitchRequestFromLoginRecord(record)
	if err != nil {
		update := existing
		update.Hostip = hostip
		update.Online = 0
		return m.saveSniffedSwitchStatus(hostip, update, 0)
	}

	versionOutput, err := m.sniffSwitchVersion(req)
	update := existing
	update.Hostip = hostip
	if err != nil {
		update.Online = 0
		return m.saveSniffedSwitchStatus(hostip, update, 0)
	}

	update.Online = 1
	fillSwitchUptimeFromVersionOutput(&update, req.Maker, versionOutput)
	return m.saveSniffedSwitchStatus(hostip, update, 1)
}

func (m *Module) saveSniffedSwitchStatus(hostip string, detail SwitchDeviceDetail, online int64) (string, error) {
	cacheErr := m.saveDeviceInformation(hostip, detail)
	_, statusErr := m.store.UpdateDeviceAutoEnableByManageIP(hostip, online)
	if cacheErr != nil {
		return "skipped", cacheErr
	}
	if statusErr != nil {
		return "skipped", statusErr
	}
	if online == 0 {
		return "offline", nil
	}
	return "online", nil
}

func (m *Module) loadDeviceInformation(hostip string) (SwitchDeviceDetail, error) {
	detail := SwitchDeviceDetail{
		Hostip:     hostip,
		Interfaces: []SwitchInterfaceInfo{},
	}
	content, err := os.ReadFile(filepath.Join(m.deviceFileDir, hostip+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return detail, nil
		}
		return detail, err
	}
	if err := json.Unmarshal(content, &detail); err != nil {
		return detail, err
	}
	if detail.Interfaces == nil {
		detail.Interfaces = []SwitchInterfaceInfo{}
	}
	return detail, nil
}

func (m *Module) buildSwitchRequestFromLoginRecord(record SwitchLoginRecord) (SwitchRequest, error) {
	maker := strings.TrimSpace(record.Maker)
	if GetSwitchMaker(maker) == "" {
		return SwitchRequest{}, fmt.Errorf("交换机厂商为空或不支持")
	}
	username, err := m.decryptSwitchLoginSecret(strings.TrimSpace(record.Username))
	if err != nil {
		return SwitchRequest{}, err
	}
	password, err := m.decryptSwitchLoginSecret(strings.TrimSpace(record.Password))
	if err != nil {
		return SwitchRequest{}, err
	}
	hostip := strings.TrimSpace(record.Hostip)
	req := SwitchRequest{
		Type:     record.Type,
		Hostip:   hostip,
		Port:     record.Port,
		Username: username,
		Password: password,
		Maker:    maker,
		Format:   "text",
	}
	if req.Type == 0 {
		req.Type = 3
	}
	switch req.Type {
	case 1:
		if req.Port == 0 {
			req.Port = 80
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
		return SwitchRequest{}, fmt.Errorf("不支持的连接方式: %d", req.Type)
	}
	if req.Hostip == "" {
		return SwitchRequest{}, fmt.Errorf("设备IP为空")
	}
	return req, nil
}

func (m *Module) sniffSwitchVersion(req SwitchRequest) (string, error) {
	maker := GetSwitchMaker(req.Maker)
	switch req.Type {
	case 1:
		return m.sniffSwitchVersionByRPC(req, maker)
	case 2:
		return sniffSwitchVersionByTelnet(req, maker)
	case 3:
		return m.sniffSwitchVersionBySSH(req, maker)
	default:
		return "", fmt.Errorf("未识别的连接方式")
	}
}

func (m *Module) sniffSwitchVersionByRPC(req SwitchRequest, maker string) (string, error) {
	if maker != "centec" {
		return "", fmt.Errorf("RPC 嗅探暂仅支持盛科交换机")
	}
	cmds := []string{"show version"}
	resp, err := m.ExecuteCmdsWithLoginInfo(req.Hostip, req.Port, cmds, req.Format, SwitchLoginInfo{
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
	infos := []map[string]interface{}{}
	if err := json.Unmarshal(resp.Result, &infos); err != nil {
		return "", err
	}
	sections := make(map[string]string)
	for _, info := range infos {
		source, _ := info["sourceDetails"].(string)
		switch {
		case strings.Contains(source, "The current running image is") || strings.Contains(source, "Hardware Type is"):
			sections["show version"] = source
		}
	}
	versionOutput := sections["show version"]
	if strings.TrimSpace(versionOutput) == "" {
		return "", fmt.Errorf("未获取到 show version 输出")
	}
	return versionOutput, nil
}

func sniffSwitchVersionByTelnet(req SwitchRequest, maker string) (string, error) {
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

	morePatterns := []string{"---- More ----", "--More--", "More"}
	command := switchVersionCommandForMaker(maker)
	if command == "" {
		return "", fmt.Errorf("不支持的交换机类型")
	}
	return runTelnetCommandWithPaging(client, command, morePatterns, 1500*time.Millisecond)
}

func (m *Module) sniffSwitchVersionBySSH(req SwitchRequest, maker string) (string, error) {
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

	command := switchVersionCommandForMaker(maker)
	if command == "" {
		return "", fmt.Errorf("不支持的交换机类型")
	}
	return client.RunInteractiveWithOptions([]string{command}, InteractiveOptions{
		PreCommands:    switchCollectionPreCommandsForMaker(maker),
		MorePatterns:   []string{"---- More ----", "--More--", "More"},
		PromptSuffixes: []string{">", "#", "]"},
		ExitCommand:    switchInteractiveExitCommandForMaker(maker),
		CommandDelay:   1200 * time.Millisecond,
		ReadTimeout:    1500 * time.Millisecond,
		AutoExit:       true,
	})
}

func switchVersionCommandForMaker(maker string) string {
	switch maker {
	case "huawei", "h3c":
		return "display version"
	case "ruijie", "centec":
		return "show version"
	default:
		return ""
	}
}

func fillSwitchUptimeFromVersionOutput(detail *SwitchDeviceDetail, maker, versionOutput string) {
	switch GetSwitchMaker(maker) {
	case "huawei", "h3c":
		fillHuaweiDeviceDetail(detail, versionOutput)
	case "ruijie":
		fillRuijieDeviceDetail(detail, versionOutput)
	case "centec":
		fillCentecDeviceDetail(detail, versionOutput)
	default:
		if uptime := extractUptime(versionOutput); uptime != "" {
			detail.Uptime = uptime
		}
	}
}
