package switchmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"itmsapiproject/internal/core"
)

// TestMasterDataCRUD 覆盖基础资料四类资源的新增/查询/修改/删除（nAct 0/1/2/3）。
func TestMasterDataCRUD(t *testing.T) {
	module := newTestModule(t)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		target  string
	}{
		{"场景", module.HandleVenue, "/itms/handleSwitchVenue/"},
		{"制造商", module.HandleManufacturer, "/itms/handleSwitchManufacturer/"},
		{"用途", module.HandlePurpose, "/itms/handleSwitchPurpose/"},
		{"协议", module.HandleProtocol, "/itms/handleSwitchProtocol/"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			// 新增
			code, data := postForm(t, item.handler, item.target, "nAct=1&"+saveData(t, map[string]any{"szName": "测试-" + item.name}))
			if code != 0 {
				t.Fatalf("新增 code = %d（data=%s）", code, string(data))
			}
			var created int64
			if err := json.Unmarshal(data, &created); err != nil || created <= 0 {
				t.Fatalf("新增返回 id 异常：%s", string(data))
			}
			// 查询：应包含刚新增的条目（老接口返回数组）
			code, data = postForm(t, item.handler, item.target, "nAct=0")
			if code != 0 {
				t.Fatalf("查询 code = %d", code)
			}
			var list []map[string]any
			if err := json.Unmarshal(data, &list); err != nil {
				t.Fatalf("查询返回不是数组：%s", string(data))
			}
			if len(list) != 1 || list[0]["szName"] != "测试-"+item.name {
				t.Fatalf("查询结果 = %s", string(data))
			}
			// 修改
			code, data = postForm(t, item.handler, item.target, "nAct=2&"+saveData(t, map[string]any{
				"nId": created, "szName": "改名-" + item.name}))
			if code != 0 {
				t.Fatalf("修改 code = %d（data=%s）", code, string(data))
			}
			var affected int64
			_ = json.Unmarshal(data, &affected)
			if affected != 1 {
				t.Fatalf("修改影响行数 = %d; want 1", affected)
			}
			// 删除
			code, data = postForm(t, item.handler, item.target, "nAct=3&"+saveData(t, []int{int(created)}))
			if code != 0 {
				t.Fatalf("删除 code = %d（data=%s）", code, string(data))
			}
			_ = json.Unmarshal(data, &affected)
			if affected != 1 {
				t.Fatalf("删除影响行数 = %d; want 1", affected)
			}
			code, data = postForm(t, item.handler, item.target, "nAct=0")
			if code != 0 || string(data) != "[]" {
				t.Fatalf("删除后列表 = %s; want []", string(data))
			}
		})
	}
}

// TestHandlersRejectBadInput 覆盖参数与方法的兼容语义（老接口：invalid nAct → -1，非 POST → 405）。
func TestHandlersRejectBadInput(t *testing.T) {
	module := newTestModule(t)
	code, _ := postForm(t, module.HandleVenue, "/itms/handleSwitchVenue/", "nAct=9")
	if code != core.CodeFail {
		t.Fatalf("非法 nAct code = %d; want %d", code, core.CodeFail)
	}
	code, _ = postForm(t, module.HandleVenue, "/itms/handleSwitchVenue/", "nAct=abc")
	if code != core.CodeFail {
		t.Fatalf("nAct 非数字 code = %d; want %d", code, core.CodeFail)
	}
	code, _ = postForm(t, module.HandleDeviceLedger, "/itms/handleSwitchInfo/", "nAct=1&saveData={bad json}")
	if code != core.CodeFail {
		t.Fatalf("非法 saveData code = %d; want %d", code, core.CodeFail)
	}
	recorder := httptest.NewRecorder()
	module.HandleVenue(recorder, httptest.NewRequest(http.MethodGet, "/itms/handleSwitchVenue/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 状态码 = %d; want 405", recorder.Code)
	}
}
