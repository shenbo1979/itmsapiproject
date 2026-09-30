package olt

import (
	"net/url"
	"testing"
)

// TestHandleDBAProfileRead 覆盖 Act=0：从库里读「已保存配置」→ 解析返回 DBA 模板列表（不连设备）。
func TestHandleDBAProfileRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	host := "192.0.2.77"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, dbaSampleConfig, "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if payload["message"] != "success" {
		t.Fatalf("message = %v", payload["message"])
	}
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 5 {
		t.Fatalf("应解析 5 条 DBA 模板，got %#v", payload["data"])
	}
	first := data[0].(map[string]any)
	if first["Id"].(float64) != 10 || first["Name"] != "DBA Main" || first["Type"] != "type3" {
		t.Fatalf("first = %v", first)
	}
}

// TestHandleDBAProfileAdd 覆盖 Act=1 新增 type3：下发 dba-profile add 并写「新增」日志。
func TestHandleDBAProfileAdd(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"让利宽带","Type":"type3","Assure":20032,"Max":1000000}`)
	code, payload := postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "dba-profile add profile-id 100 profile-name 让利宽带 type3 assure 20032 max 1000000") {
		t.Fatalf("未发送新增命令，收到: %v", server.Commands())
	}
	if got := countDBALogs(t, module, "新增"); got != 1 {
		t.Fatalf("应写一条「新增」日志，got %d", got)
	}
}

// TestHandleDBAProfileDelete 覆盖 Act=2 删除：下发 dba-profile delete 并写「删除」日志。
func TestHandleDBAProfileDelete(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":100}`)
	code, payload := postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "dba-profile delete profile-id 100") {
		t.Fatalf("未发送删除命令，收到: %v", server.Commands())
	}
	if got := countDBALogs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」日志，got %d", got)
	}
}

// TestHandleDBAProfileInvalid 覆盖 Id 越界与非法 Type 时直接拒绝、不连设备。
func TestHandleDBAProfileInvalid(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	// Id 越界（<10）。
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":9,"Name":"x","Type":"type1","Fix":128}`)
	code, payload := postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("Id=9 应拒绝: http=%d payload=%v", code, payload)
	}
	// 缺模板名。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"","Type":"type1","Fix":128}`)
	code, payload = postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("空 Name 应拒绝: http=%d payload=%v", code, payload)
	}
	// 带宽越界（Fix 100 < 128）。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"Ok","Type":"type1","Fix":100}`)
	code, payload = postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("Fix=100 应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("校验失败不应连设备，收到命令: %v", server.Commands())
	}
}

// TestHandleDBAProfileOtherAct 覆盖未识别 Act 返回 invalid Action。
func TestHandleDBAProfileOtherAct(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"192.0.2.78","Port":"23","Username":"u","Password":"p"}`)
	form.Set("saveData", `{"Act":9}`)
	code, payload := postOltForm(t, module.handleDBAProfile, form)
	if code != 200 || payload["code"].(float64) == 0 || payload["message"] != "invalid Action" {
		t.Fatalf("Act=9 应返回 invalid Action: http=%d payload=%v", code, payload)
	}
}

// countDBALogs 统计「DBA模板」菜单下某动作的日志条数。
func countDBALogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.db.QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"=? AND "szAct"=?`, dbaLogMenu, action,
	).Scan(&count); err != nil {
		t.Fatalf("countDBALogs: %v", err)
	}
	return count
}
