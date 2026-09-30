package olt

import (
	"net/url"
	"strings"
	"testing"
)

// TestHandleSrvProfileRead 覆盖 Act=0：从库里读「已保存配置」→ 解析返回 ONT 业务模板列表（不连设备）。
func TestHandleSrvProfileRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	host := "192.0.2.78"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, srvProfileSampleConfig, "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleSrvProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if payload["message"] != "success" {
		t.Fatalf("message = %v", payload["message"])
	}
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("应解析 2 条业务模板，got %#v", payload["data"])
	}
	first := data[0].(map[string]any)
	if first["Id"].(string) != "10" || first["Name"] != "SrvA" || first["Type"] != "gpon" {
		t.Fatalf("first = %v", first)
	}
	// 语义修正断言：Pots=电话口 2、Eths=网口 4。
	if first["Pots"] != "2" || first["Eths"] != "4" {
		t.Fatalf("first Pots/Eths = %v/%v，期望 2/4", first["Pots"], first["Eths"])
	}
	srv, ok := first["SrvVlan"].([]any)
	if !ok || len(srv) != 2 {
		t.Fatalf("SrvVlan = %#v", first["SrvVlan"])
	}
	sv0 := srv[0].(map[string]any)
	if sv0["Eth"] != "1" || sv0["Vlan"] != "100" {
		t.Fatalf("SrvVlan[0] = %v", sv0)
	}
	sv1 := srv[1].(map[string]any)
	if sv1["Eth"] != "2" || sv1["Vlan"] != "200,300" {
		t.Fatalf("SrvVlan[1] = %v", sv1)
	}
}

// TestHandleSrvProfileAdd 覆盖 Act=1 新增：下发 ont-srvprofile + ont-port + 逐条 port vlan eth + commit，
// 并写「新增」日志。
func TestHandleSrvProfileAdd(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"SrvA","PonType":"gpon","Pots":"2","Eths":"4","Vlan":"100,200,300"}`)
	code, payload := postOltForm(t, module.handleSrvProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	cmds := server.Commands()
	if !containsCommand(cmds, "ont-srvprofile gpon profile-id 100 profile-name SrvA") {
		t.Fatalf("未发送新增命令，收到: %v", cmds)
	}
	// Pots=2（电话口）、Eths=4（网口）→ `ont-port pots 2 eth 4`。
	if !containsCommand(cmds, "ont-port pots 2 eth 4") {
		t.Fatalf("未发送 ont-port 数量命令，收到: %v", cmds)
	}
	// 逐条 `port vlan eth <n> <vlan>`（eth 从 1 递增）。
	if !containsCommand(cmds, "port vlan eth 1 100") ||
		!containsCommand(cmds, "port vlan eth 2 200") ||
		!containsCommand(cmds, "port vlan eth 3 300") {
		t.Fatalf("未发送 port vlan eth 子命令，收到: %v", cmds)
	}
	if !containsCommand(cmds, "commit") {
		t.Fatalf("未发送 commit 收尾命令，收到: %v", cmds)
	}
	if got := countSrvLogs(t, module, "新增"); got != 1 {
		t.Fatalf("应写一条「新增」日志，got %d", got)
	}
}

// TestHandleSrvProfileAddAdaptive 覆盖 pots/eth 组合的 adaptive 拼装：仅网口（pots 空→0）时发
// `ont-port pots adaptive eth <M>`；都空时发 `ont-port pots adaptive eth adaptive`。
func TestHandleSrvProfileAddAdaptive(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"SrvA","PonType":"gpon","Pots":"","Eths":"4","Vlan":"100"}`)
	code, payload := postOltForm(t, module.handleSrvProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 仅网口失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "ont-port pots adaptive eth 4") {
		t.Fatalf("仅网口应发 adaptive pots，收到: %v", server.Commands())
	}

	server2 := startFakeOLT(t)
	host2, port2 := server2.HostPort()
	form2 := url.Values{}
	form2.Set("connData", oltConnData(host2, port2))
	form2.Set("saveData", `{"Act":1,"Id":100,"Name":"SrvA","PonType":"epon","Pots":"","Eths":"","Vlan":"100"}`)
	code2, payload2 := postOltForm(t, module.handleSrvProfile, form2)
	if code2 != 200 || payload2["code"].(float64) != 0 {
		t.Fatalf("Act=1 都空失败: http=%d payload=%v", code2, payload2)
	}
	if !containsCommand(server2.Commands(), "ont-port pots adaptive eth adaptive") {
		t.Fatalf("都空应发双 adaptive，收到: %v", server2.Commands())
	}
}

// TestHandleSrvProfileDelete 覆盖 Act=2 删除：下发 undo ont-srvprofile 并写「删除」日志。
func TestHandleSrvProfileDelete(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":100,"PonType":"gpon"}`)
	code, payload := postOltForm(t, module.handleSrvProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "undo ont-srvprofile gpon profile-id 100") {
		t.Fatalf("未发送删除命令，收到: %v", server.Commands())
	}
	if got := countSrvLogs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」日志，got %d", got)
	}
}

// TestHandleSrvProfileValidate 覆盖校验修正：Id 越界（0 / 8193）拒绝；缺 PonType 删除也拒绝；
// 空业务 VLAN 拒绝（不连设备）。
func TestHandleSrvProfileValidate(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	cases := []struct {
		save string
		want string
	}{
		{`{"Act":1,"Id":0,"Name":"SrvA","PonType":"gpon","Pots":"2","Eths":"4","Vlan":"100"}`, "模板ID"},
		{`{"Act":1,"Id":8193,"Name":"SrvA","PonType":"gpon","Pots":"2","Eths":"4","Vlan":"100"}`, "模板ID"},
		{`{"Act":1,"Id":100,"Name":"SrvA","PonType":"gpon","Pots":"2","Eths":"4","Vlan":""}`, "业务VLAN"},
		{`{"Act":2,"Id":100}`, "PON类型"},
	}
	for _, c := range cases {
		form := url.Values{}
		form.Set("connData", oltConnData(host, port))
		form.Set("saveData", c.save)
		code, payload := postOltForm(t, module.handleSrvProfile, form)
		if code != 200 || payload["code"].(float64) == 0 {
			t.Fatalf("saveData=%s 应拒绝: http=%d payload=%v", c.save, code, payload)
		}
		message, _ := payload["message"].(string)
		if !strings.Contains(message, c.want) {
			t.Fatalf("saveData=%s 拒绝消息=%q，期望含 %q", c.save, message, c.want)
		}
	}
	// 校验拒绝不应连设备发命令。
	if len(server.Commands()) != 0 {
		t.Fatalf("校验拒绝不应连设备，收到命令: %v", server.Commands())
	}
}

// TestHandleSrvProfileInvalidAction 覆盖非法 Act（如 9）返回 invalid Action。
func TestHandleSrvProfileInvalidAction(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":9,"Id":100,"Name":"SrvA","PonType":"gpon"}`)
	code, payload := postOltForm(t, module.handleSrvProfile, form)
	if code != 200 || payload["code"].(float64) == 0 || payload["message"] != "invalid Action" {
		t.Fatalf("Act=9 应返回 invalid Action: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("invalid Action 不应连设备，收到命令: %v", server.Commands())
	}
}

// countSrvLogs 统计「ONT业务模板」菜单下某动作的日志条数。
func countSrvLogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.db.QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"=? AND "szAct"=?`, srvProfileLogMenu, action,
	).Scan(&count); err != nil {
		t.Fatalf("countSrvLogs: %v", err)
	}
	return count
}
