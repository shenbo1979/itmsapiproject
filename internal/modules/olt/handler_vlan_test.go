package olt

import (
	"net/url"
	"testing"
)

// vlanSampleConfig 是一份含四种 VLAN 配置的已保存配置（覆盖三层接口/二层互通/上行透传/静态路由）。
const vlanSampleConfig = `hostname OLT
interface vlanif100
 ip address 192.168.50.1 255.255.255.0
quit
interface vlanif200
 ip address 192.168.60.1 255.255.255.0
quit
[vlan-config]
vlan 46 smart
vlan 100 smart
vlan attrib 200 q-in-q
vlan 300 stacking
port vlan 100 0/3 0
port vlan 200 0/3 1
#
[btv-config]
multicast-vlan 400
#
ip route-static 10.0.0.0 255.255.255.0 192.168.50.254
ip route-static 10.0.0.0 255.255.255.0 192.168.50.254
return
`

// TestParseOLTVLANConfiguration 覆盖四类 VLAN 解析：三层接口、三类 VLAN 模式、上行透传、静态路由去重。
func TestParseOLTVLANConfiguration(t *testing.T) {
	vlanif, normal, tran, routes := parseOLTVLANConfiguration(vlanSampleConfig)

	if len(vlanif) != 2 {
		t.Fatalf("vlanif = %d, want 2 => %v", len(vlanif), vlanif)
	}
	if vlanif[0]["VLANID"] != "100" || vlanif[0]["IPAddress"] != "192.168.50.1" || vlanif[0]["SubnetMask"] != "255.255.255.0" {
		t.Fatalf("vlanif[0] = %v", vlanif[0])
	}

	if len(normal) != 4 {
		t.Fatalf("normalvlan = %d, want 4（三档 + 组播）=> %v", len(normal), normal)
	}
	names := map[string]string{}
	for _, item := range normal {
		names[item["szName"].(string)] = item["szVlan"].(string)
	}
	if names["单层智能VLAN(Smart VLAN)"] != "46,100" {
		t.Fatalf("smart vlan = %q", names["单层智能VLAN(Smart VLAN)"])
	}
	if names["基本/普通 QinQ"] != "200" || names["灵活 QinQ"] != "300" {
		t.Fatalf("qinq/stacking = %q / %q", names["基本/普通 QinQ"], names["灵活 QinQ"])
	}
	if names["组播业务 VLAN"] != "400" {
		t.Fatalf("组播 vlan = %q", names["组播业务 VLAN"])
	}

	if len(tran) != 2 {
		t.Fatalf("tranvlan = %d, want 2 => %v", len(tran), tran)
	}
	portMap := map[string]string{}
	for _, item := range tran {
		portMap[item["szPortInfo"].(string)] = item["szVlan"].(string)
	}
	if portMap["0/3/0"] != "100" || portMap["0/3/1"] != "200" {
		t.Fatalf("tranvlan 端口映射 = %v", portMap)
	}

	// 静态路由去重：同样 key 只保留一条。
	if len(routes) != 1 {
		t.Fatalf("routestatic = %d, want 1（去重）=> %v", len(routes), routes)
	}
	if routes[0]["szNetwork"] != "10.0.0.0 / 255.255.255.0" || routes[0]["szGateway"] != "192.168.50.254" {
		t.Fatalf("static[0] = %v", routes[0])
	}
}

// TestSubStringBetween 覆盖工具函数主要分支（老实现同名函数逐字语义）。
func TestSubStringBetween(t *testing.T) {
	if got := SubStringBetween("Failure: abc\r\nrest", "Failure:", "\r\n", 0); got != " abc" {
		t.Fatalf("SubStringBetween = %q", got)
	}
	if got := SubStringBetween("no match", "xyz", "\r\n", 0); got != "" {
		t.Fatalf("应返回空串，got %q", got)
	}
	if got, ok := extractOLTFailure("Failure: rejected\r\n"); !ok || got != "rejected" {
		t.Fatalf("extractOLTFailure = %q, %v", got, ok)
	}
	if _, ok := extractOLTFailure("success\r\n"); ok {
		t.Fatal("无 Failure 不应判定失败")
	}
}

// TestHandleVLANFunctionRead 覆盖 Act=0：预先在库里写入已保存配置 → 直接解析返回四类顶层字段（不连设备）。
func TestHandleVLANFunctionRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	host := "192.0.2.55"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, vlanSampleConfig, "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if got := payload["message"]; got != "success" {
		t.Fatalf("message = %v", got)
	}
	// 四类信息都在顶层字段。
	if vlanif, ok := payload["vlanifdata"].([]any); !ok || len(vlanif) != 2 {
		t.Fatalf("vlanifdata = %v", payload["vlanifdata"])
	}
	if normal, ok := payload["vlandata"].([]any); !ok || len(normal) != 4 {
		t.Fatalf("vlandata = %v", payload["vlandata"])
	}
	if tran, ok := payload["tranvlandata"].([]any); !ok || len(tran) != 2 {
		t.Fatalf("tranvlandata = %v", payload["tranvlandata"])
	}
	if static, ok := payload["routestaticdata"].([]any); !ok || len(static) != 1 {
		t.Fatalf("routestaticdata = %v", payload["routestaticdata"])
	}
	if _, reported := payload["data"]; reported {
		t.Fatalf("四类信息不应放进 data（老壳按顶层取值）: %v", payload["data"])
	}
}

// TestHandleVLANReadViaRefresh 覆盖 Act=0 且库里无配置：连设备 `display current-configuration` 刷新后解析。
func TestHandleVLANReadViaRefresh(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	vlanif, _ := payload["vlanifdata"].([]any)
	found := false
	for _, item := range vlanif {
		if m, ok := item.(map[string]any); ok && m["VLANID"] == "100" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应解析出假 OLT 的三层接口 VLAN。vlanifdata=%v", vlanif)
	}
}

// countVLANLogs 统计「VLAN功能」菜单下某动作的日志条数。
func countVLANLogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.DB().QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"='VLAN功能' AND "szAct"=?`, action,
	).Scan(&count); err != nil {
		t.Fatalf("统计操作日志失败: %v", err)
	}
	return count
}

// TestHandleVLANAddType0 BaseVLAN（新增 vlan 100 smart）并写日志。
func TestHandleVLANAddType0(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Type":0,"VlanType":0,"VlanID":"100","Fsp":"","IP":"","Mask":""}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 Type=0 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "vlan 100 smart") {
		t.Fatalf("未发送 vlan 100 smart，收到: %v", server.Commands())
	}
	if got := countVLANLogs(t, module, "新增"); got != 1 {
		t.Fatalf("应写入一条「新增」操作日志，got %d", got)
	}
}

// TestHandleVLANDeleteType1 删除上行透传（undo port vlan）并记录「删除」日志。
func TestHandleVLANDeleteType1(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Type":1,"VlanID":"100","Fsp":"0/3/0"}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 Type=1 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "undo port vlan 100 0/3 0") {
		t.Fatalf("未发送 undo port vlan，收到: %v", server.Commands())
	}
	if got := countVLANLogs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」操作日志，got %d", got)
	}
}

// TestHandleVLANDeleteType2 删除三层 VLAN 接口（undo interface vlanif）只需 VlanID、不必填 IP/Mask，
// 并记录「删除」日志。回归：此前 validateVLANWrite 对删除也要求 IP+Mask，前端只带 VlanID 被误拒。
func TestHandleVLANDeleteType2(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Type":2,"VlanID":"200","IP":"","Mask":""}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 Type=2 删除三层接口失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "undo interface vlanif200") {
		t.Fatalf("未发送 undo interface vlanif200，收到: %v", server.Commands())
	}
	if got := countVLANLogs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」操作日志，got %d", got)
	}
}

// TestHandleVLANRequiresVlanID 覆盖缺 VlanID 时直接拒绝且不发设备命令。
func TestHandleVLANRequiresVlanID(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Type":0,"VlanID":"","Fsp":"","IP":"","Mask":""}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("缺 VlanID 应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("缺 VlanID 不应连设备，收到: %v", server.Commands())
	}
}

// TestHandleVLANBadFsp 覆盖上行透传 Fsp 非 3 段时拒绝（不发设备命令）。
func TestHandleVLANBadFsp(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Type":1,"VlanID":"100","Fsp":"0/3"}`)
	code, payload := postOltForm(t, module.handleVLANFunctionModule, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("Fsp 非 3 段应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("Fsp 非法不应连设备，收到: %v", server.Commands())
	}
}
