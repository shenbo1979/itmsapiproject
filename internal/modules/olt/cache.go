package olt

import (
	"strings"
	"time"
)

// 当前配置缓存的后台刷新与合并（老实现 mergeOltBoardRefresh / updateOltCurrentConfigWithBoardRefresh /
// refreshOltCurrentConfigInBackground / tryStart+finishOltCurrentConfigRefresh 的等价搬运）。

// mergeCurrentConfigBoardRefresh 把一次板卡刷新合并进缓存（老实现 mergeOltBoardRefresh）。
// fullRefresh=true 时整体替换板卡列表；否则按板卡 Id 就地替换/追加，只更新刷新时间。
func mergeCurrentConfigBoardRefresh(cfg *OltCurrentConfig, boardinfos []OltBoardInfo, slotidRefreshTime string,
	ontOnlines, ontTotal int, fullRefresh bool) {
	if cfg == nil {
		return
	}
	if fullRefresh {
		cfg.OltBoardInfo = append([]OltBoardInfo(nil), boardinfos...)
		cfg.SlotidRefreshTime = slotidRefreshTime
		cfg.OntOnlines = ontOnlines
		cfg.OntTotal = ontTotal
		return
	}
	if len(boardinfos) == 0 {
		return
	}
	currentByID := make(map[string]int, len(cfg.OltBoardInfo))
	for index := range cfg.OltBoardInfo {
		currentByID[cfg.OltBoardInfo[index].Id] = index
	}
	for _, boardinfo := range boardinfos {
		if index, ok := currentByID[boardinfo.Id]; ok {
			cfg.OltBoardInfo[index] = boardinfo
			continue
		}
		cfg.OltBoardInfo = append(cfg.OltBoardInfo, boardinfo)
	}
	cfg.SlotidRefreshTime = slotidRefreshTime
}

// updateCurrentConfigWithBoardRefresh 读缓存 → 合并本次刷新 → 落盘（老实现同名函数）。
// 缓存文件不存在时按空缓存处理（等于新建）。
func (m *Module) updateCurrentConfigWithBoardRefresh(ip string, boardinfos []OltBoardInfo, slotidRefreshTime string,
	ontOnlines, ontTotal int, fullRefresh bool) error {
	cfg, ok := m.loadCurrentConfig(ip)
	if !ok {
		cfg = OltCurrentConfig{}
	}
	mergeCurrentConfigBoardRefresh(&cfg, boardinfos, slotidRefreshTime, ontOnlines, ontTotal, fullRefresh)
	if err := m.saveCurrentConfig(ip, cfg); err != nil {
		return err
	}
	// 同步入库（单槽刷新按设备整份写表，与文件缓存保持一致）。
	if saveErr := m.store.SaveDeviceSnapshot(ip, cfg, "", "", time.Now().Format(oltTimeLayout)); saveErr != nil {
		m.logf("单槽刷新入库失败(%s): %v", ip, saveErr)
	}
	return nil
}

// refreshCurrentConfigInBackground 命中缓存时的后台刷新（老实现同名函数）：
// 同一台 OLT 同时只允许一个后台刷新在跑，避免并发登录占满设备会话。
func (m *Module) refreshCurrentConfigInBackground(cp ConnectParameters) {
	if !m.tryStartConfigRefresh(cp.IP) {
		return
	}
	go func(cp ConnectParameters) {
		defer m.finishConfigRefresh(cp.IP)
		var cached *OltCurrentConfig
		if current, ok := m.loadCurrentConfig(cp.IP); ok && hasUsableOltCurrentConfig(current) {
			cached = &current
		}
		if _, err := m.collectCurrentConfig(cp, newOltTrace(false), cached, false); err != nil {
			m.logf("后台刷新 OLT 当前配置失败(%s): %v", cp.IP, err)
		}
	}(cp)
}

// tryStartConfigRefresh 试图占用某台 OLT 的后台刷新位（老实现 tryStartOltCurrentConfigRefresh）。
func (m *Module) tryStartConfigRefresh(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	m.configRefresh.Lock()
	defer m.configRefresh.Unlock()
	if m.configRefresh.inFlight[ip] {
		return false
	}
	m.configRefresh.inFlight[ip] = true
	return true
}

// finishConfigRefresh 释放后台刷新位（老实现 finishOltCurrentConfigRefresh）。
func (m *Module) finishConfigRefresh(ip string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	m.configRefresh.Lock()
	delete(m.configRefresh.inFlight, ip)
	m.configRefresh.Unlock()
}
