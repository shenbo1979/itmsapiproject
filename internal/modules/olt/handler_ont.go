package olt

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// handleONTListAndStatus 搬运自老实现（olthandler.go 3146 行起"ONT配置/状态"）。
//
// 本批（第一批）只迁入 **Act=0：读取槽位 ONT 缓存**，即：
//   - reqData.SqlBoardId 非空 → 只读该槽位缓存文件；
//   - 为空 → 读全部槽位缓存文件（老实现按文件名字典序累加）；
//   - 之后按 SqlStr/SqlOnline/SqlBoardId 过滤、分页，响应带顶层 count/nPage/nLimit。
//
// 老实现声明了 reqData.Field/Order 但**并未真正排序**（DataProp/SortDir 在 Act=0/9 分支里没有使用），
// 新实现保持同口径（不排序），避免界面顺序与老壳不一致。
//
// 其余分支的归属（第二批）：
//   - Act=9：连设备采集 `display ont info 0 <slot> all` + 明细缓存 + 后台补详情；
//   - Act=1..6：重启/激活/禁用/注册等**设备写操作**。
//
// 这些分支本批明确返回"第二批待迁入"提示，不做假成功。
func (m *Module) handleONTListAndStatus(w http.ResponseWriter, r *http.Request) {
	// 老实现这个接口允许 GET/POST/OPTIONS（其余 OLT 接口只允许 POST），这里保持一致。
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST, OPTIONS")
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	trace := newOltTrace(r.FormValue("trace") == "1")
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
	case act == 0:
		m.respondONTListFromCache(w, cp, r.FormValue("reqData"), trace)
		return
	case act == 9 || (act >= 1 && act <= 6):
		writeSecondBatchPending(w, "/itms/handleONTListAndStatus/", "Act="+strconv.Itoa(act)+" 为设备采集/写操作")
		return
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}

// respondONTListFromCache 处理 Act=0：读槽位缓存 → 过滤 → 分页 → 返回。
func (m *Module) respondONTListFromCache(w http.ResponseWriter, cp ConnectParameters, reqData string, trace *oltTrace) {
	respond := func(code int, message string, data any, total, page, limit int) {
		if trace != nil && trace.enabled {
			data = writeTraceData(data, trace)
		}
		writeJSON(w, http.StatusOK, APIResponse{
			Code:    code,
			Message: message,
			Data:    data,
			Total:   total,
			Page:    page,
			Limit:   limit,
		})
	}
	var req OntListRequest
	if err := json.Unmarshal([]byte(reqData), &req); err != nil {
		respond(-1, "invalid request: "+err.Error(), "", 0, 0, 0)
		return
	}

	var items []OntListResponse
	boardID := strings.TrimSpace(req.SqlBoardId)
	// 数据来源：**表优先、老文件兜底**（2026-09-22 起 ONT 缓存已入库表 tbOltOntCache；
	// 过渡期老目录 /home/olt/<IP>/slot_ont_file/*.txt 仍可被读到，见 filecache.go）。
	if cached, err := m.store.ListOntCache(cp.IP, boardID); err == nil && len(cached) > 0 {
		items = cached
	} else if boardID != "" {
		cached, _ := m.loadSlotOntItems(cp.IP, boardID)
		items = cached
	} else {
		all, err := m.listSlotOntItems(cp.IP)
		if err != nil {
			respond(-1, "read cache failed: "+err.Error(), "", 0, 0, 0)
			return
		}
		items = all
	}

	keyword := strings.TrimSpace(req.SqlStr)
	online := strings.TrimSpace(req.SqlOnline)
	filterBoardID := strings.TrimSpace(req.SqlBoardId)
	matched := make([]any, 0, len(items))
	for _, item := range items {
		if matchesONTListFilter(item, keyword, online, filterBoardID) {
			matched = append(matched, item)
		}
	}
	count := len(matched)
	page, limit := req.Page, req.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = 50
	}
	page, start, end := paginateBounds(page, limit, count)
	respond(0, "success", matched[start:end], count, page, limit)
}

// matchesONTListFilter 与老实现同名函数逐条一致（关键字命中任一展示字段即算命中；
// online 按 RunState 小写比较；boardID 取 FSP 的中间段）。
func matchesONTListFilter(item OntListResponse, keyword, online, boardID string) bool {
	if keyword != "" {
		if !(strings.Contains(item.FSP, keyword) ||
			strings.Contains(item.OntId, keyword) ||
			strings.Contains(item.SerialNumber, keyword) ||
			strings.Contains(item.AuthType, keyword) ||
			strings.Contains(item.Protocol, keyword) ||
			strings.Contains(item.LineProfile, keyword) ||
			strings.Contains(item.SrvProfile, keyword) ||
			strings.Contains(item.Desc, keyword) ||
			strings.Contains(item.Mac, keyword) ||
			strings.Contains(item.IP, keyword)) {
			return false
		}
	}
	if online != "" && strings.ToLower(item.RunState) != online {
		return false
	}
	if boardID != "" {
		fspinfo := strings.Split(strings.TrimSpace(item.FSP), "/")
		if len(fspinfo) != 3 || fspinfo[1] != boardID {
			return false
		}
	}
	return true
}
