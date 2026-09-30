package olt

import (
	"net/url"
	"strings"
	"testing"
)

// TestHandleTr069ProfileRead 覆盖 Act=0：从库里读「已保存配置」→ 解析返回 TR069 管理模板列表
// （Id/Name/AcsUrl/AcsUserName，**不回密码**，不连设备）。
func TestHandleTr069ProfileRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	host := "192.0.2.79"
	if err := module.store.SaveConfigText(host, ConfigKindSaved, srvProfileSampleConfig, "", "", "2026-09-22 10:00:00"); err != nil {
		t.Fatalf("SaveConfigText: %v", err)
	}
	form := url.Values{}
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"23","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"Act":0}`)
	code, payload := postOltForm(t, module.handleTr069Profile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if payload["message"] != "success" {
		t.Fatalf("message = %v", payload["message"])
	}
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("应解析 2 条 TR069 模板，got %#v", payload["data"])
	}
	first := data[0].(map[string]any)
	if first["Id"].(string) != "1" || first["Name"] != "ACS-A" ||
		first["AcsUrl"] != "http://acs.example.com" || first["AcsUserName"] != "acsuser" {
		t.Fatalf("first = %v", first)
	}
	// 契约：不回密码（无 AcsPassword 字段）。
	if _, has := first["AcsPassword"]; has {
		t.Fatalf("不应回密码字段，got %v", first)
	}
}

// TestHandleTr069ProfileAdd 覆盖 Act=1 新增：下发 ont tr069-server-profile add … 命令并写「新增」日志。
func TestHandleTr069ProfileAdd(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":1,"Id":1,"Name":"ACS-A","AcsUrl":"http://acs.example.com","AcsUserName":"acsuser","AcsPassword":"secret"}`)
	code, payload := postOltForm(t, module.handleTr069Profile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "ont tr069-server-profile add profile-id 1 profile-name ACS-A url http://acs.example.com user acsuser secret auth-realm auth") {
		t.Fatalf("未发送新增命令，收到: %v", server.Commands())
	}
	if got := countTr069Logs(t, module, "新增"); got != 1 {
		t.Fatalf("应写一条「新增」日志，got %d", got)
	}
}

// TestHandleTr069ProfileDelete 覆盖 Act=2 删除：下发 ont tr069-server-profile delete 并写「删除」日志。
func TestHandleTr069ProfileDelete(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	form := url.Values{}
	form.Set("connData", oltConnData(host, port))
	form.Set("saveData", `{"Act":2,"Id":1}`)
	code, payload := postOltForm(t, module.handleTr069Profile, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 失败: http=%d payload=%v", code, payload)
	}
	if !containsCommand(server.Commands(), "ont tr069-server-profile delete profile-id 1") {
		t.Fatalf("未发送删除命令，收到: %v", server.Commands())
	}
	if got := countTr069Logs(t, module, "删除"); got != 1 {
		t.Fatalf("应写一条「删除」日志，got %d", got)
	}
}

// TestHandleTr069ProfileValidate 覆盖校验修正：Id 越界（0 / 33）拒绝；新增时缺 Name/Url/User/Pass 拒绝；
// 删除只需 Id（不校验 Name 等）；校验拒绝不连设备。
func TestHandleTr069ProfileValidate(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()
	cases := []struct {
		save string
		want string
	}{
		{`{"Act":1,"Id":0,"Name":"ACS-A","AcsUrl":"http://acs.example.com","AcsUserName":"acsuser","AcsPassword":"secret"}`, "模板ID"},
		{`{"Act":1,"Id":33,"Name":"ACS-A","AcsUrl":"http://acs.example.com","AcsUserName":"acsuser","AcsPassword":"secret"}`, "模板ID"},
		{`{"Act":1,"Id":1,"Name":"","AcsUrl":"http://acs.example.com","AcsUserName":"acsuser","AcsPassword":"secret"}`, "模板名"},
		{`{"Act":1,"Id":1,"Name":"ACS-A","AcsUrl":"","AcsUserName":"acsuser","AcsPassword":"secret"}`, "URL"},
		{`{"Act":1,"Id":1,"Name":"ACS-A","AcsUrl":"http://acs.example.com","AcsUserName":"","AcsPassword":"secret"}`, "用户名"},
		{`{"Act":1,"Id":1,"Name":"ACS-A","AcsUrl":"http://acs.example.com","AcsUserName":"acsuser","AcsPassword":""}`, "密码"},
	}
	for _, c := range cases {
		form := url.Values{}
		form.Set("connData", oltConnData(host, port))
		form.Set("saveData", c.save)
		code, payload := postOltForm(t, module.handleTr069Profile, form)
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

// countTr069Logs 统计「TR069管理模板」菜单下某动作的日志条数。
func countTr069Logs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.db.QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"=? AND "szAct"=?`, tr069ProfileLogMenu, action,
	).Scan(&count); err != nil {
		t.Fatalf("countTr069Logs: %v", err)
	}
	return count
}
