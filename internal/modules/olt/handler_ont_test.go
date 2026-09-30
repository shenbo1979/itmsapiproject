package olt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSlotCache 写一个槽位 ONT 缓存文件（每行一个 JSON，与老实现同格式）。
func writeSlotCache(t *testing.T, dir, boardID string, items ...OntListResponse) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("建缓存目录失败: %v", err)
	}
	var builder strings.Builder
	for _, item := range items {
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("序列化 ONT 失败: %v", err)
		}
		builder.Write(payload)
		builder.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, boardID+".txt"), []byte(builder.String()), 0644); err != nil {
		t.Fatalf("写缓存文件失败: %v", err)
	}
}

// callONTList 以指定方法调用 handleONTListAndStatus（GET 时参数走 query，与老前端 axios params 一致）。
func callONTList(t *testing.T, module *Module, method string, form url.Values) map[string]any {
	t.Helper()
	target := "/itms/handleONTListAndStatus/"
	var request *http.Request
	if method == http.MethodGet {
		request = httptest.NewRequest(method, target+"?"+form.Encode(), nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	recorder := httptest.NewRecorder()
	module.handleONTListAndStatus(recorder, request)
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, recorder.Body.String())
	}
	return payload
}

// TestHandleONTListAndStatusCacheRead 覆盖 Act=0：读槽位缓存（新目录优先 + 老目录兜底）、
// 过滤、分页，以及顶层 count/nPage/nLimit 与老口径一致。
func TestHandleONTListAndStatusCacheRead(t *testing.T) {
	module, fileDir, legacyDir := newDeviceTestModule(t)
	const ip = "192.0.2.20"
	connData := `{"IP":"` + ip + `"}`

	writeSlotCache(t, filepath.Join(fileDir, ip, "slot_ont_file"), "1",
		OntListResponse{PonType: "GPON", FSP: "0/1/0", OntId: "0", RunState: "online", SerialNumber: "SN-1", Desc: "ont-a"},
		OntListResponse{PonType: "GPON", FSP: "0/1/0", OntId: "1", RunState: "offline", SerialNumber: "SN-2", Desc: "ont-b"},
	)
	// 2 号槽位只在老目录里（模拟过渡期老控制台写的缓存）
	writeSlotCache(t, filepath.Join(legacyDir, ip, "slot_ont_file"), "2",
		OntListResponse{PonType: "GPON", FSP: "0/2/0", OntId: "0", RunState: "online", SerialNumber: "SN-3", Desc: "ont-c"},
	)

	// 全部槽位：1 号（新目录）+ 2 号（老目录兜底）= 3 条
	all := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"Page":1,"Limit":50}`},
	})
	if all["code"].(float64) != 0 || all["count"].(float64) != 3 {
		t.Fatalf("读取全部槽位失败: %v", all)
	}
	// 老口径：nLimit 是**请求里的 Limit**（不像 handleOltLogList 会按 count 收敛）。
	if all["nLimit"].(float64) != 50 || len(all["data"].([]any)) != 3 {
		t.Fatalf("nLimit/data = %v/%d, want 50/3", all["nLimit"], len(all["data"].([]any)))
	}

	// 指定槽位 1：只返回 2 条
	board := callONTList(t, module, http.MethodGet, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"SqlBoardId":"1","Page":1,"Limit":50}`},
	})
	if board["count"].(float64) != 2 {
		t.Fatalf("槽位 1 条数 = %v, want 2", board["count"])
	}
	firstItem := board["data"].([]any)[0].(map[string]any)
	if got := firstItem["FSP"]; got != "0/1/0" {
		t.Fatalf("首条 FSP = %v", got)
	}

	// 在线过滤（老口径：RunState 小写比较）
	online := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"SqlOnline":"online","Page":1,"Limit":50}`},
	})
	if online["count"].(float64) != 2 {
		t.Fatalf("在线条数 = %v, want 2", online["count"])
	}

	// 关键字过滤（命中年任意展示字段）
	keyword := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"SqlStr":"ont-c","Page":1,"Limit":50}`},
	})
	if keyword["count"].(float64) != 1 {
		t.Fatalf("关键字命中 = %v, want 1", keyword["count"])
	}

	// 分页：Limit=1、Page=2 → data 1 条；nLimit 仍为请求值 1（老口径）
	page := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"Page":2,"Limit":1}`},
	})
	if page["nPage"].(float64) != 2 || page["nLimit"].(float64) != 1 || len(page["data"].([]any)) != 1 {
		t.Fatalf("分页口径不符: %v", page)
	}

	// 空结果：老口径 count 带 omitempty（0 时字段缺省）、nPage 收敛为 0 也被 omitempty 省略
	empty := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"SqlStr":"不存在的关键字","Page":1,"Limit":50}`},
	})
	if _, exists := empty["count"]; exists {
		t.Fatalf("空结果不应带 count 字段: %v", empty)
	}
	if empty["code"].(float64) != 0 {
		t.Fatalf("空结果 code = %v, want 0", empty["code"])
	}
}

// TestHandleONTListAndStatusPrefersTableCache 覆盖"表优先、文件兜底"：
// 表里有 ONT 缓存时 Act=0 直接读表（即使磁盘上还有老缓存文件），且不再读文件。
func TestHandleONTListAndStatusPrefersTableCache(t *testing.T) {
	module, fileDir, _ := newDeviceTestModule(t)
	const ip = "192.0.2.90"
	connData := `{"IP":"` + ip + `"}`
	// 文件里放一条"过期"数据（若实现优先读文件就会返回它）
	writeSlotCache(t, filepath.Join(fileDir, ip, "slot_ont_file"), "4",
		OntListResponse{PonType: "gpon", FSP: "0/4/9", OntId: "9", RunState: "offline", Desc: "stale-file"},
	)
	// 表里放两条新数据
	if err := module.store.SaveOntCache(ip, "4", []OntListResponse{
		{PonType: "gpon", FSP: "0/4/5", OntId: "0", RunState: "online", SerialNumber: "FROM-TABLE-1"},
		{PonType: "gpon", FSP: "0/4/5", OntId: "1", RunState: "online", SerialNumber: "FROM-TABLE-2"},
	}, "/legacy/4.txt", "2026-09-01 15:40:22", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveOntCache: %v", err)
	}

	payload := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"SqlBoardId":"4","Page":1,"Limit":50}`},
	})
	if payload["code"].(float64) != 0 || payload["count"].(float64) != 2 {
		t.Fatalf("表优先读取失败: %v", payload)
	}
	first := payload["data"].([]any)[0].(map[string]any)
	if first["SerialNumber"] != "FROM-TABLE-1" {
		t.Fatalf("返回的不是表里的数据: %v", first)
	}

	// 未指定槽位时同样优先读表（文件里的 stale-file 不应出现）
	all := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {connData},
		"reqData":  {`{"Page":1,"Limit":50}`},
	})
	if all["count"].(float64) != 2 || strings.Contains(all["data"].([]any)[0].(map[string]any)["Desc"].(string), "stale") {
		t.Fatalf("全部槽位读取未优先用表: %v", all)
	}
}

// TestHandleONTListAndStatusFallsBackToFileCache 覆盖表为空时的文件兜底（过渡期老缓存仍可读）。
func TestHandleONTListAndStatusFallsBackToFileCache(t *testing.T) {
	module, fileDir, _ := newDeviceTestModule(t)
	const ip = "192.0.2.91"
	writeSlotCache(t, filepath.Join(fileDir, ip, "slot_ont_file"), "4",
		OntListResponse{PonType: "gpon", FSP: "0/4/5", OntId: "0", RunState: "online", Desc: "from-file"},
	)
	payload := callONTList(t, module, http.MethodPost, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + ip + `"}`},
		"reqData":  {`{"Page":1,"Limit":50}`},
	})
	if payload["code"].(float64) != 0 || payload["count"].(float64) != 1 {
		t.Fatalf("文件兜底失败: %v", payload)
	}
	if got := payload["data"].([]any)[0].(map[string]any)["Desc"]; got != "from-file" {
		t.Fatalf("兜底数据不符: %v", got)
	}
}

// TestHandleONTListAndStatusPendingActs 覆盖第二批分支（Act=9 采集 / Act=1..6 写操作）的明确提示。
func TestHandleONTListAndStatusPendingActs(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	for _, act := range []string{"9", "1", "2", "3", "4", "5", "6"} {
		payload := callONTList(t, module, http.MethodPost, url.Values{
			"Act":      {act},
			"connData": {`{"IP":"192.0.2.20"}`},
			"reqData":  {`{"Page":1,"Limit":50}`},
		})
		if payload["code"].(float64) != -1 || !strings.Contains(payload["message"].(string), "第二批") {
			t.Fatalf("Act=%s 未返回第二批待迁入提示: %v", act, payload)
		}
	}

	// OPTIONS 预检（老实现这个接口允许跨域预检）
	request := httptest.NewRequest(http.MethodOptions, "/itms/handleONTListAndStatus/", nil)
	recorder := httptest.NewRecorder()
	module.handleONTListAndStatus(recorder, request)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Allow") != "GET, POST, OPTIONS" {
		t.Fatalf("OPTIONS 预检响应 = %d/%s", recorder.Code, recorder.Header().Get("Allow"))
	}
}
