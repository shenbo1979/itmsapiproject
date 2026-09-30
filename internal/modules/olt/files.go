package olt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OLT 的文件类缓存（老实现 OltFilePath = /home/olt）在新工程里的落地口径：
//
//	新目录（可写，本模块独占）：<TXAPI_OLT_FILE_DIR 或 <DataDir>/olt>/<OLTIP>/...
//	老目录（只读兜底）：      <TXAPI_OLT_LEGACY_FILE_DIR 或 /home/olt>/<OLTIP>/...
//
// 读：新目录优先、老目录兜底 —— 过渡期老控制台写出的缓存（currentconfiguration.json /
// savedconfiguration.txt / slot_ont_file/*.txt）照样能被新服务读到，切换当天页面不空。
// 写：只写新目录 —— 老程序零改动（施工边界：不修改、不操作原系统与 /home/** 存量数据）。
//
// docs/09 §6 待确认 #3（文件类数据是否纳入模块库）尚无结论，因此本批先按"文件形态 + 只读老目录"落地，
// 待业务确认后再决定是否入库。
const (
	// envFileDir 可覆盖新工程模块文件目录（默认 <DataDir>/olt）。
	envFileDir = "TXAPI_OLT_FILE_DIR"
	// envLegacyFileDir 可覆盖老实现文件目录（默认 /home/olt），仅用于只读兜底。
	envLegacyFileDir = "TXAPI_OLT_LEGACY_FILE_DIR"
	// defaultLegacyFileDir 是老实现的 OltFilePath（只读参考）。
	defaultLegacyFileDir = "/home/olt"
	// defaultDataDir 与 cmd/txapi 的默认 TXAPI_DATA_DIR 一致（一模块一库所在目录）。
	defaultDataDir = "/home/tx"
)

// fileDir 返回本模块的可写文件目录（新目录）。
func (m *Module) fileDir() string {
	if dir := strings.TrimSpace(os.Getenv(envFileDir)); dir != "" {
		return dir
	}
	base := ""
	if m != nil && m.ctx != nil {
		base = strings.TrimSpace(m.ctx.DataDir)
	}
	if base == "" {
		base = defaultDataDir
	}
	return filepath.Join(base, "olt")
}

// legacyFileDir 返回老实现的文件目录（只读兜底）。
func (m *Module) legacyFileDir() string {
	if dir := strings.TrimSpace(os.Getenv(envLegacyFileDir)); dir != "" {
		return dir
	}
	return defaultLegacyFileDir
}

// deviceDir 返回某台 OLT 在新目录下的子目录。
func (m *Module) deviceDir(ip string) string {
	return filepath.Join(m.fileDir(), strings.TrimSpace(ip))
}

// legacyDeviceDir 返回某台 OLT 在老目录下的子目录（只读）。
func (m *Module) legacyDeviceDir(ip string) string {
	return filepath.Join(m.legacyFileDir(), strings.TrimSpace(ip))
}

// currentConfigPaths 返回「OLT 当前配置缓存」的候选路径（新目录优先、老目录兜底）。
func (m *Module) currentConfigPaths(ip string) []string {
	return []string{
		filepath.Join(m.deviceDir(ip), "currentconfiguration.json"),
		filepath.Join(m.legacyDeviceDir(ip), "currentconfiguration.json"),
	}
}

// savedConfigPaths 返回「OLT 已保存配置」的候选路径（老实现额外支持根目录下的兜底文件）。
func (m *Module) savedConfigPaths(ip string) []string {
	ip = strings.TrimSpace(ip)
	paths := make([]string, 0, 4)
	if ip != "" {
		paths = append(paths, filepath.Join(m.deviceDir(ip), "savedconfiguration.txt"))
	}
	paths = append(paths, filepath.Join(m.fileDir(), "savedconfiguration.txt"))
	if ip != "" {
		paths = append(paths, filepath.Join(m.legacyDeviceDir(ip), "savedconfiguration.txt"))
	}
	paths = append(paths, filepath.Join(m.legacyFileDir(), "savedconfiguration.txt"))
	return paths
}

// slotOntDirs 返回槽位 ONT 缓存目录的候选（新目录优先、老目录兜底）。
func (m *Module) slotOntDirs(ip string) []string {
	return []string{
		filepath.Join(m.deviceDir(ip), "slot_ont_file"),
		filepath.Join(m.legacyDeviceDir(ip), "slot_ont_file"),
	}
}

// readFirstExistingFile 按候选顺序返回第一个可读且非空的文件内容（含实际命中的路径）。
func readFirstExistingFile(paths []string) (string, string) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		return string(data), path
	}
	return "", ""
}

// loadONTListCacheFile 逐行读取某槽位的 ONT 缓存文件（每行一个 OntListResponse JSON）。
// 与老实现一致：空行跳过、坏行跳过、扫描缓冲放大到 1MB（ONT 明细字段可能很长）。
func loadONTListCacheFile(path string) []OntListResponse {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	results := make([]OntListResponse, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item OntListResponse
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		results = append(results, item)
	}
	if err := scanner.Err(); err != nil {
		return nil
	}
	return results
}

// loadCurrentConfig 读取 OLT 当前配置缓存（新目录优先、老目录兜底）。
// ok=false 表示没有任何可用缓存（老实现此时返回零值结构体，前端按空渲染）。
func (m *Module) loadCurrentConfig(ip string) (OltCurrentConfig, bool) {
	var cfg OltCurrentConfig
	content, _ := readFirstExistingFile(m.currentConfigPaths(ip))
	if strings.TrimSpace(content) == "" {
		return cfg, false
	}
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		m.logf("解析 OLT 当前配置缓存失败(%s): %v", ip, err)
		return OltCurrentConfig{}, false
	}
	return cfg, true
}

// saveCurrentConfig 写入 OLT 当前配置缓存（只写新目录；老目录保持只读）。
func (m *Module) saveCurrentConfig(ip string, cfg OltCurrentConfig) error {
	if strings.TrimSpace(ip) == "" {
		return fmt.Errorf("OLT IP is required")
	}
	dir := m.deviceDir(ip)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "currentconfiguration.json")
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// loadSavedConfiguration 读取「OLT 已保存配置」文本（老实现 oltReadSavedConfiguration 的等价物）。
func (m *Module) loadSavedConfiguration(ip string) string {
	content, _ := readFirstExistingFile(m.savedConfigPaths(ip))
	return content
}

// writeSavedConfiguration 写入「OLT 已保存配置」文本（只写新目录）。
func (m *Module) writeSavedConfiguration(ip, configuration string) error {
	configuration = strings.TrimSpace(configuration)
	if strings.TrimSpace(ip) == "" || configuration == "" {
		return fmt.Errorf("empty OLT configuration")
	}
	dir := m.deviceDir(ip)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "savedconfiguration.txt")
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(configuration), 0644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// loadSlotOntItems 读取某个槽位的 ONT 缓存（新目录优先、老目录兜底）。
// 返回的 ok 表示是否在某个目录下找到了该槽位文件。
func (m *Module) loadSlotOntItems(ip, boardID string) ([]OntListResponse, bool) {
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return nil, false
	}
	for _, dir := range m.slotOntDirs(ip) {
		path := filepath.Join(dir, boardID+".txt")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		return loadONTListCacheFile(path), true
	}
	return nil, false
}

// listSlotOntItems 读取某台 OLT 全部槽位的 ONT 缓存（未指定 SlotId 时使用）。
//
// 与老实现的关系：老实现在**单一目录**里按 os.ReadDir 的文件名字典序累加各槽位文件；
// 新实现保持"按槽位号字典序累加、同一槽位只取一份"的口径，并增加"新目录优先、老目录兜底"：
// 某槽位在新目录没有文件时，用老目录（过渡期老控制台写的缓存）补齐，避免切换当天页面空表。
func (m *Module) listSlotOntItems(ip string) ([]OntListResponse, error) {
	slotIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, dir := range m.slotOntDirs(ip) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
				continue
			}
			slotID := strings.TrimSuffix(entry.Name(), ".txt")
			if seen[slotID] {
				continue
			}
			seen[slotID] = true
			slotIDs = append(slotIDs, slotID)
		}
	}
	sort.Strings(slotIDs)

	results := make([]OntListResponse, 0)
	for _, slotID := range slotIDs {
		items, ok := m.loadSlotOntItems(ip, slotID)
		if !ok {
			continue
		}
		results = append(results, items...)
	}
	return results, nil
}
