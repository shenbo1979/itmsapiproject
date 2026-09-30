package olt

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// handleOltLogList 搬运自老实现 handleOltLogList（/switchProject/switchmain/olthandler.go 4920 行起）。
//
// 契约逐字保持：POST；参数 `Act`（0 查询 / 1 按 Ids 删除 / 2 清空）+ `reqData`（JSON：
// SqlTime/SqlStr/Ids/Page/Limit/Field/Order）；查询响应把分页字段放在**顶层**
// （count/nPage/nLimit），`data` 为 OltOperationLog 数组。
//
// 与老实现的差异（等价改写，不改契约）：
//   - 表访问由「8306 中间件 + sqlx」改为模块库 olt.db（表 tbOltOperationLog，列名一致）；
//   - 时间范围用绑定参数、排序字段走列名白名单（老实现把客户端字符串直接拼进 SQL）；
//   - Act=1 但 Ids 解析不出有效整数时，保持老行为（失败提示 "Operation failed"）。
func (m *Module) handleOltLogList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Code: -1, Message: "method not allowed"})
		return
	}
	handleact, acterr := strconv.Atoi(r.FormValue("Act"))
	if acterr != nil {
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + acterr.Error()})
		return
	}
	switch act := handleact; {
	case act == 0: // 查询
		reqData := r.FormValue("reqData")
		var ollreq LogListRequest
		if err := json.Unmarshal([]byte(reqData), &ollreq); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
			return
		}
		dataProp := ollreq.Field
		sortDir := ollreq.Order
		page := ollreq.Page
		limit := ollreq.Limit
		if dataProp == "" {
			dataProp = "nID"
		}
		if sortDir == "" {
			sortDir = "DESC"
		}
		if page == 0 {
			page = 1
		}
		if limit == 0 {
			limit = 50
		}
		sqlStr := strings.TrimSpace(ollreq.SqlStr)
		sqlTime := strings.TrimSpace(ollreq.SqlTime)
		var timeFrom, timeTo string
		if len(sqlTime) > 0 {
			timelist := strings.Split(sqlTime, "TO")
			if len(timelist) == 2 {
				timeFrom, timeTo = strings.TrimSpace(timelist[0]), strings.TrimSpace(timelist[1])
			} else {
				timeFrom = strings.TrimSpace(sqlTime)
			}
		}
		logs, err := m.store.QueryOltLogs(timeFrom, timeTo, dataProp, sortDir)
		if err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: []string{}})
			return
		}
		infos := make([]any, 0, len(logs))
		for _, obj := range logs {
			if len(sqlStr) > 0 {
				if strings.Contains(obj.OltName, sqlStr) || strings.Contains(obj.Menu, sqlStr) ||
					strings.Contains(obj.Act, sqlStr) || strings.Contains(obj.OperationIP, sqlStr) ||
					strings.Contains(obj.Text, sqlStr) || strings.Contains(obj.Remark, sqlStr) {
					infos = append(infos, obj)
				}
			} else {
				infos = append(infos, obj)
			}
		}
		// 分页口径与老实现逐行一致（含 count/nPage/nLimit 三个顶层字段）
		count := len(infos)
		var totalPages int
		if count > 0 {
			a, b := count/limit, count%limit
			if b == 0 {
				totalPages = a
			} else {
				totalPages = a + 1
			}
		}
		if page > totalPages {
			page = totalPages
		}
		start := limit*page - limit
		if start < 0 {
			start = 0
		}
		limit = page * limit
		if limit > count {
			limit = count
		}
		infos = infos[start:limit]
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success", Data: infos, Total: count, Page: page, Limit: limit})
		return
	case act == 1 || act == 2: // 删除（1：按 Ids；2：清空）
		var ids []int
		if act == 1 {
			reqData := r.FormValue("reqData")
			var ollreq LogListRequest
			if err := json.Unmarshal([]byte(reqData), &ollreq); err != nil {
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid request: " + err.Error()})
				return
			}
			ids = parseOltLogIDs(strings.TrimSpace(ollreq.Ids))
			if len(ids) == 0 { // 老实现此时会因 `IN ()` 报错并返回该提示
				writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Operation failed"})
				return
			}
		}
		if err := m.store.DeleteOltLogs(ids); err != nil {
			writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "Operation failed"})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{Code: 0, Message: "success"})
		return
	default:
		writeJSON(w, http.StatusOK, APIResponse{Code: -1, Message: "invalid Action"})
		return
	}
}
