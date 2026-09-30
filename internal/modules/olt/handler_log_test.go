package olt

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"itmsapiproject/internal/core"
)

// newTestModule 建一个使用临时库的模块实例（不启动服务），并完成建表。
func newTestModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envDBPath, dir+"/olt.db")
	module := New()
	ctx := &core.Context{Logger: log.New(io.Discard, "", 0), DataDir: dir}
	if err := module.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return module
}

// postOltForm 以老前端的 form 方式调用老接口并解出完整响应（含顶层分页字段）。
func postOltForm(t *testing.T, handler http.HandlerFunc, form url.Values) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/itms/handleOltLogList/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, recorder.Body.String())
	}
	return recorder.Code, payload
}

// seedOltLogs 造 5 条日志（时间 09-21 ~ 09-25，便于验证排序、搜索与时间过滤）。
func seedOltLogs(t *testing.T, module *Module) {
	t.Helper()
	for index, name := range []string{"OLT-A", "OLT-B", "OLT-C", "OLT-D", "OLT-E"} {
		item := OltOperationLog{
			OltName:     name,
			Menu:        "board",
			Act:         "查询",
			OperationIP: fmt.Sprintf("10.0.0.%d", index+1),
			Text:        fmt.Sprintf("t%d", index),
			CreatTime:   fmt.Sprintf("2026-09-%d 10:00:00", 21+index),
		}
		if err := module.store.InsertOltLog(item); err != nil {
			t.Fatalf("InsertOltLog: %v", err)
		}
	}
}

// TestHandleOltLogListQuery 覆盖 Act=0：顶层分页字段（count/nPage/nLimit）、排序白名单、搜索、时间过滤。
func TestHandleOltLogListQuery(t *testing.T) {
	module := newTestModule(t)
	seedOltLogs(t, module)

	code, payload := postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"Page":1,"Limit":50}`}})
	if code != http.StatusOK || payload["code"].(float64) != 0 {
		t.Fatalf("查询失败: http=%d payload=%v", code, payload)
	}
	if got := payload["count"].(float64); got != 5 {
		t.Fatalf("count = %v, want 5", got)
	}
	if got := payload["nPage"].(float64); got != 1 {
		t.Fatalf("nPage = %v, want 1", got)
	}
	if got := payload["nLimit"].(float64); got != 5 { // 老口径：Limit = Page*Limit 后按 count 收敛
		t.Fatalf("nLimit = %v, want 5", got)
	}
	items := payload["data"].([]any)
	if len(items) != 5 {
		t.Fatalf("data 条数 = %d, want 5", len(items))
	}
	if first := items[0].(map[string]any); first["szOltName"] != "OLT-E" {
		t.Fatalf("默认应为 nID DESC（最新在首），得到 %v", first["szOltName"])
	}
	// 字段名逐字保持（老前端按字面量取值）
	for _, key := range []string{"nID", "szOltName", "szMenu", "szAct", "szOperationIP", "szText", "dtCreatTime", "szRemark"} {
		if _, ok := items[0].(map[string]any)[key]; !ok {
			t.Fatalf("响应缺少字段 %s", key)
		}
	}
	// 时间字段必须按老库存储形态返回（不能变成 RFC3339 —— datetime 列会被驱动转成 time.Time）
	if got := items[0].(map[string]any)["dtCreatTime"]; got != "2026-09-25 10:00:00" {
		t.Fatalf("dtCreatTime = %v, want 2026-09-25 10:00:00（老库文本形态）", got)
	}

	// 分页：Page=2 & Limit=2 → count=5 / nPage=2 / nLimit=4 / 2 条（与老实现逐行一致）
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"Page":2,"Limit":2}`}})
	if payload["count"].(float64) != 5 || payload["nPage"].(float64) != 2 || payload["nLimit"].(float64) != 4 {
		t.Fatalf("分页字段 = %v/%v/%v, want 5/2/4", payload["count"], payload["nPage"], payload["nLimit"])
	}
	if got := len(payload["data"].([]any)); got != 2 {
		t.Fatalf("第 2 页条数 = %d, want 2", got)
	}

	// 排序：Order=ASC；非白名单字段回落 nID（且不得把输入拼进 SQL）
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"Field":"nID","Order":"ASC"}`}})
	if got := payload["data"].([]any)[0].(map[string]any)["szOltName"]; got != "OLT-A" {
		t.Fatalf("ASC 首条 = %v, want OLT-A", got)
	}
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"Field":"nID;DROP TABLE tbOltOperationLog","Order":"ASC"}`}})
	if got := payload["data"].([]any)[0].(map[string]any)["szOltName"]; got != "OLT-A" {
		t.Fatalf("非白名单排序字段应回落 nID：%v", got)
	}

	// 搜索：SqlStr 命中 szOltName
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"SqlStr":"OLT-C"}`}})
	if payload["count"].(float64) != 1 {
		t.Fatalf("搜索 count = %v, want 1", payload["count"])
	}

	// 时间过滤：SqlTime = `from TO to` → 命中 09-22/23/24 三条
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"},
		"reqData": {`{"SqlTime":"2026-09-22 00:00:00 TO 2026-09-24 23:59:59"}`}})
	if got := payload["count"].(float64); got != 3 {
		t.Fatalf("时间过滤 count = %v, want 3", got)
	}
}

// TestHandleOltLogListDelete 覆盖 Act=1（按 Ids 删除）与 Act=2（清空），以及 Ids 为空时的老口径提示。
func TestHandleOltLogListDelete(t *testing.T) {
	module := newTestModule(t)
	seedOltLogs(t, module)

	// Act=1 但 Ids 为空：老实现会因 `IN ()` 报错并返回该提示
	_, payload := postOltForm(t, module.handleOltLogList, url.Values{"Act": {"1"}, "reqData": {`{"Ids":""}`}})
	if payload["code"].(float64) != -1 || payload["message"] != "Operation failed" {
		t.Fatalf("空 Ids 应返回 Operation failed，得到 %v", payload)
	}

	// 取最旧两条的 id 后按 Ids 删除
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{"Field":"nID","Order":"ASC"}`}})
	items := payload["data"].([]any)
	idA := int(items[0].(map[string]any)["nID"].(float64))
	idB := int(items[1].(map[string]any)["nID"].(float64))
	_, payload = postOltForm(t, module.handleOltLogList,
		url.Values{"Act": {"1"}, "reqData": {fmt.Sprintf(`{"Ids":"%d, %d"}`, idA, idB)}})
	if payload["code"].(float64) != 0 || payload["message"] != "success" {
		t.Fatalf("按 Ids 删除失败：%v", payload)
	}
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{}`}})
	if got := payload["count"].(float64); got != 3 {
		t.Fatalf("删除两条后 count = %v, want 3", got)
	}

	// Act=2 清空
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"2"}})
	if payload["code"].(float64) != 0 {
		t.Fatalf("清空失败：%v", payload)
	}
	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {`{}`}})
	// count 带 omitempty：0 时字段缺省（与老实现一致，老前端按 `res.count || 0` 处理）
	if got := payload["count"]; got != nil && got.(float64) != 0 {
		t.Fatalf("清空后 count = %v, want 0（0 时字段缺省）", got)
	}
	if got := len(payload["data"].([]any)); got != 0 {
		t.Fatalf("清空后 data 条数 = %d, want 0", got)
	}
}

// TestHandleOltLogListInvalidInput 覆盖非 POST / Act 非数字 / reqData 非 JSON / Act 越界的老口径响应。
func TestHandleOltLogListInvalidInput(t *testing.T) {
	module := newTestModule(t)

	request := httptest.NewRequest(http.MethodGet, "/itms/handleOltLogList/", nil)
	recorder := httptest.NewRecorder()
	module.handleOltLogList(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("非 POST 应 405，得到 %d", recorder.Code)
	}

	_, payload := postOltForm(t, module.handleOltLogList, url.Values{"Act": {"abc"}})
	if payload["code"].(float64) != -1 || !strings.HasPrefix(payload["message"].(string), "invalid request") {
		t.Fatalf("Act 非数字应 -1 invalid request，得到 %v", payload)
	}

	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"0"}, "reqData": {"not-json"}})
	if payload["code"].(float64) != -1 || !strings.HasPrefix(payload["message"].(string), "invalid request") {
		t.Fatalf("reqData 非 JSON 应 -1 invalid request，得到 %v", payload)
	}

	_, payload = postOltForm(t, module.handleOltLogList, url.Values{"Act": {"9"}})
	if payload["code"].(float64) != -1 || payload["message"] != "invalid Action" {
		t.Fatalf("Act 越界应 -1 invalid Action，得到 %v", payload)
	}
}
