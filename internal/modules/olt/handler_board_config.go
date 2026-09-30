package olt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 本文件搬运 handleBoardAndSlotInfo 与 handleCurrentConfiguration（均为只读）。

// handleBoardAndSlotInfo 搬运自老实现（olthandler.go 1482 行起"初始板卡及插槽相关信息"）。
//
// 契约：POST + `connData` + `saveData{SqlId/SlotId}` + 可选 `refresh=1`、`trace=1`；
//   - saveData.SlotId 为空 → 返回全部板卡（命中缓存且未 refresh 时先返回缓存并后台刷新）；
//   - saveData.SlotId 非空 → 只查该槽位（命中缓存且未 refresh 时直接返回缓存里那一块）；
//   - 单槽查询成功后把该槽位合并回 currentconfiguration.json（老实现 updateOltCurrentConfigWithBoardRefresh）。
//
// 响应除 data 外还带顶层 OntOnlines / OntTotal / SlotidRefreshTime（老前端用于总览统计）。
func (m *Module) handleBoardAndSlotInfo(w http.ResponseWriter, r *http.Request) {
	trace := newOltTrace(r.FormValue("trace") == "1")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	connData := r.FormValue("connData")
	var cp ConnectParameters
	cp, err := decodeConnectParameters(connData)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	saveData := r.FormValue("saveData")
	var boardSlot BoardSlotInfo
	if err := json.Unmarshal([]byte(saveData), &boardSlot); err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	respond := func(code int, message string, boardinfos []OltBoardInfo, ontOnlines, ontTotal int, slotidRefreshTime string) {
		if elapsed := trace.totalElapsed(); elapsed >= 3*time.Second {
			m.logf("handleBoardAndSlotInfo 慢请求 ip=%s slot=%s trace=%t 耗时=%s 阶段=%s",
				cp.IP, strings.TrimSpace(boardSlot.SlotId), trace.enabled, elapsed, trace.summary())
		}
		writeJSON(w, http.StatusOK, APIResponse{
			Code:              code,
			Message:           message,
			Data:              writeTraceData(boardinfos, trace),
			OntOnlines:        ontOnlines,
			OntTotal:          ontTotal,
			SlotidRefreshTime: slotidRefreshTime,
		})
	}

	forceRefresh := r.FormValue("refresh") == "1"
	var cachedCfg *OltCurrentConfig
	if cached, ok := m.loadCurrentConfig(cp.IP); ok && hasUsableOltCurrentConfig(cached) {
		cachedCfg = &cached
	}

	slotID := strings.TrimSpace(boardSlot.SlotId)
	if slotID == "" {
		if cachedCfg != nil && !forceRefresh {
			m.refreshCurrentConfigInBackground(cp)
			respond(0, "success", append([]OltBoardInfo(nil), cachedCfg.OltBoardInfo...),
				cachedCfg.OntOnlines, cachedCfg.OntTotal, cachedCfg.SlotidRefreshTime)
			return
		}
		oltconfig, err := m.collectCurrentConfig(cp, trace, cachedCfg, true)
		if err != nil {
			if isOltConnectionError(err) {
				respondConnectionFailed(w, err)
				return
			}
			respond(-1, err.Error(), nil, 0, 0, "")
			return
		}
		respond(0, "success", oltconfig.OltBoardInfo, oltconfig.OntOnlines, oltconfig.OntTotal, oltconfig.SlotidRefreshTime)
		return
	}

	var cachedBoard *OltBoardInfo
	if cachedCfg != nil {
		if boardinfo, ok := findOltBoardInfoByID(cachedCfg.OltBoardInfo, slotID); ok {
			cachedBoard = &boardinfo
			if !forceRefresh {
				m.refreshCurrentConfigInBackground(cp)
				boardinfos := []OltBoardInfo{boardinfo}
				configuredTotals := m.loadConfiguredONTTotals(cp.IP)
				ontOnlines, ontTotal := applyOLTConfiguredONTTotals(boardinfos, configuredTotals)
				respond(0, "success", boardinfos, ontOnlines, ontTotal, cachedCfg.SlotidRefreshTime)
				return
			}
		}
	}

	boardinfo, err := m.collectSingleBoardInfo(cp, slotID, cachedBoard)
	if err != nil {
		if isOltConnectionError(err) {
			respondConnectionFailed(w, err)
			return
		}
		respond(-1, err.Error(), nil, 0, 0, "")
		return
	}
	boardinfos := []OltBoardInfo{boardinfo}
	configuredTotals := m.loadConfiguredONTTotals(cp.IP)
	ontOnlines, ontTotal := applyOLTConfiguredONTTotals(boardinfos, configuredTotals)
	slotidRefreshTime := time.Now().Format(oltTimeLayout)
	if err := m.updateCurrentConfigWithBoardRefresh(cp.IP, boardinfos, slotidRefreshTime, ontOnlines, ontTotal, false); err != nil {
		m.logf("保存OLT刷新信息失败: %v", err)
	}
	respond(0, "success", boardinfos, ontOnlines, ontTotal, slotidRefreshTime)
}

// handleCurrentConfiguration 搬运自老实现（olthandler.go 1772 行起"OLT 当前配置信息"）。
//
// Act=0：读已保存配置文本（没有则连设备采集一次）；
// Act=1：强制连设备采集 `display current-configuration` 并写入本模块文件目录。
// 两种 Act 都返回同一份文本（data 为字符串），与老实现一致。
func (m *Module) handleCurrentConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	handleact, acterr := strconv.Atoi(r.FormValue("Act"))
	if acterr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + acterr.Error()})
		return
	}
	connData := r.FormValue("connData")
	var cp ConnectParameters
	cp, err := decodeConnectParameters(connData)
	if err != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
		return
	}
	switch act := handleact; {
	case act == 0 || act == 1:
		// 读已保存配置：**表优先、文件兜底**；Act=1（或表/文件都没有时）连设备刷新。
		configuration := ""
		if content, ok, err := m.store.LoadConfigText(cp.IP, ConfigKindSaved); err == nil && ok {
			configuration = content
		} else {
			configuration = m.loadSavedConfiguration(cp.IP)
		}
		if act == 1 || strings.TrimSpace(configuration) == "" {
			refreshed, err := m.refreshSavedConfiguration(cp)
			if err != nil {
				respondConnectionFailed(w, fmt.Errorf("%w", err))
				return
			}
			configuration = refreshed
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: configuration})
		return
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}
