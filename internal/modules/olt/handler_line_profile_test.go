package olt

import (
	"net/url"
	"testing"
)

// TestHandleLineProfileRead 覆盖 Act=0：从库里读「已保存配置」→ 解析返回 ONT 线路模板列表（不连设备）。
func TestHandleLineProfileRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	host := "192.0.2.77"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, lineProfileSampleConfig, "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if payload["message"] != "success" {
		t.Fatalf("message = %v", payload["message"])
	}
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("应解析 1 条线路模板，got %#v", payload["data"])
	}
	first := data[0].(map[string]any)
	if first["Id"].(string) != "10" || first["Name"] != "LineA" || first["Type"] != "gpon" {
		t.Fatalf("first = %v", first)
	}
	gem, ok := first["GemMapping"].([]any)
	if !ok || len(gem) != 1 {
		t.Fatalf("GemMapping = %#v", first["GemMapping"])
	}
	gm0 := gem[0].(map[string]any)
	ginfo, ok := gm0["GemInfo"].([]any)
	if !ok || len(ginfo) != 2 {
		t.Fatalf("GemInfo = %#v", gm0["GemInfo"])
	}
}

// TestHandleLineProfileAdd 覆盖 Act=1 新增：下发 ont-lineprofile 及 tr069/tcont/gem/commit/quit，并写「新增」日志。
func TestHandleLineProfileAdd(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData",
		`{"Act":1,"Id":100,"Name":"LineA","DbaProfileId":10,"PonType":"gpon","OpenTr069":1,"Vlan":"100,200/300"}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	cmds := server.Commands()
	if !containsCommand(cmds, "ont-lineprofile gpon profile-id 100 profile-name LineA") {
		t.Fatalf("未发送新增命令，收到: %v", cmds)
	}
	// 编号规则与真机一致：单一 T-CONT=1（只发一次，Vlan 两段共用），GEM 0-based、mapping 段内 0-based。
	if !containsCommand(cmds, "tcont 1 dba-profile-id 10") {
		t.Fatalf("未发送 tcont 子命令，收到: %v", cmds)
	}
	if containsCommand(cmds, "tcont 2 dba-profile-id 10") {
		t.Fatalf("单一 T-CONT=1 不应再发 tcont 2，收到: %v", cmds)
	}
	if !containsCommand(cmds, "gem add 0 eth tcont 1") || !containsCommand(cmds, "gem add 1 eth tcont 1") {
		t.Fatalf("未发送 gem add 子命令，收到: %v", cmds)
	}
	if !containsCommand(cmds, "gem mapping 0 0 vlan 100") ||
		!containsCommand(cmds, "gem mapping 0 1 vlan 200") ||
		!containsCommand(cmds, "gem mapping 1 0 vlan 300") {
		t.Fatalf("未发送 gem mapping 子命令，收到: %v", cmds)
	}
	if !containsCommand(cmds, "commit") || !containsCommand(cmds, "quit") {
		t.Fatalf("未发送 commit/quit 收尾命令，收到: %v", cmds)
	}
	if got := countLineLogs(t, module, "新增"); got != 1 {
		t.Fatalf("应写一条「新增」日志，got %d", got)
	}
}

// TestHandleLineProfileDelete 覆盖 Act=2 删除：下发 undo ont-lineprofile 并写「删除」日志。
func TestHandleLineProfileDelete(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":100,"PonType":"gpon"}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "undo ont-lineprofile gpon profile-id 100") {
		t.Fatalf("未发送删除命令，收到: %v", server.Commands())
	}
	if got := countLineLogs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」日志，got %d", got)
	}
}

// TestHandleLineProfileInvalid 覆盖 Id 越界、空模板名、非法 PON 类型时直接拒绝、不连设备。
func TestHandleLineProfileInvalid(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	// Id 越界（<1 或 >8192）。
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":0,"Name":"x","PonType":"gpon","DbaProfileId":10}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("Id=0 应拒绝: http=%d payload=%v", code, payload)
	}
	// DbaProfileId 未选（0）应拒绝。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"x","PonType":"gpon","DbaProfileId":0}`)
	code, payload = postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("DbaProfileId=0 应拒绝: http=%d payload=%v", code, payload)
	}
	// 空模板名。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"","PonType":"gpon","DbaProfileId":10}`)
	code, payload = postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("空 Name 应拒绝: http=%d payload=%v", code, payload)
	}
	// 非法 PON 类型。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"Ok","PonType":"weird","DbaProfileId":10}`)
	code, payload = postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("非法 PonType 应拒绝: http=%d payload=%v", code, payload)
	}
	// 删除（Act=2）缺 PonType 应拒绝——否则 `undo ont-lineprofile  profile-id <id>` 拼出畸形命令却“假成功”。
	form = url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":100}`)
	code, payload = postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("Act=2 缺 PonType 应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("校验失败不应连设备，收到命令: %v", server.Commands())
	}
}

// TestHandleLineProfileDeleteOnlyId 覆盖删除分支：删除只需 Id 与 PonType（Name 为空不应拒绝）。
func TestHandleLineProfileDeleteOnlyId(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":100,"PonType":"gpon","Name":""}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2（Name 空）应成功: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "undo ont-lineprofile gpon profile-id 100") {
		t.Fatalf("未发送删除命令，收到: %v", server.Commands())
	}
}

// TestHandleLineProfileOtherAct 覆盖未识别 Act 返回 invalid Action。
func TestHandleLineProfileOtherAct(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"192.0.2.78","Port":"23","Username":"u","Password":"p"}`)
	form.Set("saveData", `{"Act":9}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 || payload["message"] != "invalid Action" {
		t.Fatalf("Act=9 应返回 invalid Action: http=%d payload=%v", code, payload)
	}
}

// countLineLogs 统计「ONT线路模板」菜单下某动作的日志条数。
func countLineLogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.db.QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"=? AND "szAct"=?`, lineProfileLogMenu, action,
	).Scan(&count); err != nil {
		t.Fatalf("countLineLogs: %v", err)
	}
	return count
}

// TestHandleLineProfileAddSingleVlan 覆盖需求：Vlan 非空（无 `/` 无 `,` 的单值）时，
// 恒发 `tcont 1 dba-profile-id <dba>`、`gem add 0 eth tcont 1` 与 `gem mapping 0 0 vlan <vlan>`，
// 而不是跳过 GEM 映射（此前的“什么都不执行直接忽略”）。
func TestHandleLineProfileAddSingleVlan(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"LineA","DbaProfileId":10,"PonType":"gpon","OpenTr069":1,"Vlan":"100"}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 单值 VLAN 失败: http=%d payload=%v", code, payload)
	}
	cmds := server.Commands()
	if !containsCommand(cmds, "tcont 1 dba-profile-id 10") {
		t.Fatalf("Vlan 非空应恒发 tcont 1，收到: %v", cmds)
	}
	if !containsCommand(cmds, "gem add 0 eth tcont 1") {
		t.Fatalf("无 / 时应 gem add 0，收到: %v", cmds)
	}
	if !containsCommand(cmds, "gem mapping 0 0 vlan 100") {
		t.Fatalf("无 , 时应 gem mapping 0 0 vlan 100，收到: %v", cmds)
	}
}

// TestHandleLineProfileAddFilterEmptySeg 覆盖空段/空 VLAN 过滤：`100,/200/` 只对非空段发命令，
// gem 编号不错位（0、1），且不发 `gem add 2`/空 VLAN 的 `gem mapping … vlan ` 畸形命令。
func TestHandleLineProfileAddFilterEmptySeg(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"LineA","DbaProfileId":10,"PonType":"gpon","OpenTr069":1,"Vlan":"100,/200/"}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 空段过滤失败: http=%d payload=%v", code, payload)
	}
	cmds := server.Commands()
	if !containsCommand(cmds, "gem add 0 eth tcont 1") || !containsCommand(cmds, "gem add 1 eth tcont 1") {
		t.Fatalf("空段过滤后非空段 gem 编号应为 0/1，收到: %v", cmds)
	}
	if containsCommand(cmds, "gem add 2 eth tcont 1") {
		t.Fatalf("不应发 gem add 2（空段占位），收到: %v", cmds)
	}
	if containsCommand(cmds, "vlan ") {
		t.Fatalf("不应发空 VLAN 的 gem mapping（vlan 后无值），收到: %v", cmds)
	}
}

// TestHandleLineProfileAddRequiresVlan 覆盖需求：空 Vlan 在写入前被校验拒绝（不连设备、不假成功），
// 杜绝 applyLineProfileMapping 因空 Vlan 跳过 tcont/gem/mapping 后只 commit 的“什么都不执行”。
func TestHandleLineProfileAddRequiresVlan(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":100,"Name":"LineA","DbaProfileId":10,"PonType":"gpon","OpenTr069":1,"Vlan":""}`)
	code, payload := postOltForm(t, module.handleLineProfile, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("空 Vlan 应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("空 Vlan 不应连设备，收到命令: %v", server.Commands())
	}
}
