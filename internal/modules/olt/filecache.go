package olt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 文件→表 导入器（2026-09-22）。
//
// 背景：老系统把 OLT 的采集结果与配置写在 /home/olt/<IP>/ 下的 JSON/文本文件里
// （见 models_cache.go 顶部映射表）。这些数据都是结构化的，改造为入库表后可分页/筛选/并发读写更安全。
//
// 边界与语义：
//   - 老目录（/home/olt）**只读**（施工边界），导入只 SELECT/ReadFile，绝不改写；
//   - 导入是**幂等**的：按主键覆盖（快照/ONT 缓存按"整份覆盖"清后写），重复执行结果一致；
//   - 只在缓存表为空时自动执行（模块启动时），也可由工具/测试显式调用；
//   - 口令类字段（ontautomaticcfg.json 的 Password）入库前按"密文优先、明文兜底"加密（与 oltuser 同口径）。
func (m *Module) importFileCache() (OltFileImportResult, error) {
	result := OltFileImportResult{}
	now := time.Now().Format(oltTimeLayout)
	for _, hostIP := range m.cachedDeviceHosts() {
		// 1) 设备快照：currentconfiguration.json
		if path, content, ok := m.readCachedFile(m.currentConfigPaths(hostIP)); ok {
			var cfg OltCurrentConfig
			if err := json.Unmarshal([]byte(content), &cfg); err != nil {
				m.logf("导入 OLT 当前配置失败(%s): %v", hostIP, err)
				result.Skipped++
			} else if err := m.store.SaveDeviceSnapshot(hostIP, cfg, path, fileMTime(path), now); err != nil {
				return result, err
			} else {
				result.Devices++
				result.Boards += len(cfg.OltBoardInfo)
				for _, board := range cfg.OltBoardInfo {
					result.PonPorts += len(board.PonInfos)
					result.GePorts += len(board.GeInfos)
				}
			}
		}

		// 2) ONT 缓存：slot_ont_file/<槽位>.txt（一行一个 ONT JSON）
		for _, dir := range m.slotOntDirs(hostIP) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
					continue
				}
				path := filepath.Join(dir, entry.Name())
				slotID := strings.TrimSuffix(entry.Name(), ".txt")
				items := loadONTListCacheFile(path)
				if len(items) == 0 {
					result.Skipped++
					continue
				}
				if err := m.store.SaveOntCache(hostIP, slotID, items, path, fileMTime(path), now); err != nil {
					return result, err
				}
				result.OntItems += len(items)
			}
		}

		// 3) 文本配置：savedconfiguration.txt / olt_cur_cfg.txt
		if path, content, ok := m.readCachedFile(m.savedConfigPaths(hostIP)); ok {
			if err := m.store.SaveConfigText(hostIP, ConfigKindSaved, content, path, fileMTime(path), now); err != nil {
				return result, err
			}
			result.ConfigTexts++
		}
		currentCfgPath := filepath.Join(m.deviceDir(hostIP), "olt_cur_cfg.txt")
		if path, content, ok := m.readCachedFile([]string{
			currentCfgPath,
			filepath.Join(m.legacyDeviceDir(hostIP), "olt_cur_cfg.txt"),
		}); ok {
			if err := m.store.SaveConfigText(hostIP, ConfigKindCurrent, content, path, fileMTime(path), now); err != nil {
				return result, err
			}
			result.ConfigTexts++
		}

		// 4) 自动注册配置：ontautomaticcfg.json（口令加密后入库）
		autoRegPaths := []string{
			filepath.Join(m.deviceDir(hostIP), "ontautomaticcfg.json"),
			filepath.Join(m.legacyDeviceDir(hostIP), "ontautomaticcfg.json"),
		}
		if path, content, ok := m.readCachedFile(autoRegPaths); ok {
			var cfg OltAutoRegConfig
			if err := json.Unmarshal([]byte(content), &cfg); err != nil {
				m.logf("导入 OLT 自动注册配置失败(%s): %v", hostIP, err)
				result.Skipped++
			} else {
				cfg.HostIP = hostIP
				cfg.Password = m.encryptOltSecret(cfg.Password)
				if err := m.store.SaveAutoRegConfig(cfg, path, fileMTime(path), now); err != nil {
					return result, err
				}
				result.AutoRegConfs++
			}
		}
	}
	return result, nil
}

// importFileCacheIfEmpty 仅在缓存表为空时导入（模块启动调用；避免每次启动都重扫文件）。
func (m *Module) importFileCacheIfEmpty() (OltFileImportResult, error) {
	var count int
	if err := m.store.db.QueryRow(`SELECT
			(SELECT COUNT(*) FROM "tbOltDeviceSnapshot") + (SELECT COUNT(*) FROM "tbOltOntCache") +
			(SELECT COUNT(*) FROM "tbOltConfigCache") + (SELECT COUNT(*) FROM "tbOltAutoRegConfig")`).Scan(&count); err != nil {
		return OltFileImportResult{}, err
	}
	if count > 0 {
		return OltFileImportResult{}, nil
	}
	return m.importFileCache()
}

// cachedDeviceHosts 汇总"新目录 + 老目录"里出现过的设备（目录名即设备 IP/域名），按名称排序。
func (m *Module) cachedDeviceHosts() []string {
	seen := map[string]bool{}
	for _, base := range []string{m.fileDir(), m.legacyFileDir()} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := strings.TrimSpace(entry.Name())
			if name != "" {
				seen[name] = true
			}
		}
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// readCachedFile 按候选顺序读取第一个可读且非空的缓存文件（返回内容与实际命中的路径）。
func (m *Module) readCachedFile(paths []string) (string, string, bool) {
	content, path := readFirstExistingFile(paths)
	if strings.TrimSpace(content) == "" {
		return "", "", false
	}
	return path, content, true
}

// fileMTime 返回文件修改时间（无法读取时返回空串），用于记录数据来源时间。
func fileMTime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().Format(oltTimeLayout)
}
