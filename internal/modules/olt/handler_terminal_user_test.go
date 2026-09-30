package olt

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// TestParseOLTTerminalUsers 覆盖配置解析：命中普通行、跳过口令行、跳过 history_password、
// buildrun_new_password 取下一列、去重、去除引导符。
func TestParseOLTTerminalUsers(t *testing.T) {
	config := strings.Join([]string{
		"  terminal user name admin",
		"  terminal user name olt_operator",
		"  terminal user password admin",
		"  terminal user name history_password",
		"  terminal user name buildrun_new_password admin",
		"  terminal user name admin",
		"  terminal user name \"quoted user\"",
		"  not a terminal line",
	}, "\n")
	got := parseOLTTerminalUsers(config)
	want := []string{"admin", "olt_operator", "quoted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOLTTerminalUsers = %#v, want %#v", got, want)
	}
}

// TestHandleOltTerminalUserRead 覆盖 Act=0：连假 OLT 读配置并解析出用户名列表。
func TestHandleOltTerminalUserRead(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "0")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"oltuser","Password":"secret"}`)
	code, payload := postOltForm(t, module.handleOltTerminalUser, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: http=%d payload=%v", code, payload)
	}
	if got := payload["message"]; got != "sueccess" {
		t.Fatalf("message = %v, want sueccess（老实现笔误逐字保持）", got)
	}
	users, _ := payload["data"].([]any)
	if len(users) != 2 {
		t.Fatalf("用户名数 = %d, want 2（admin/olt_operator，admin 去重）%v", len(users), users)
	}
	if users[0] != "admin" || users[1] != "olt_operator" {
		t.Fatalf("用户名顺序 = %v, want [admin olt_operator]", users)
	}
}

// TestHandleOltTerminalUserAdd 覆盖 Act=1：走 terminal user name 交互序列并写操作日志/刷新配置。
func TestHandleOltTerminalUserAdd(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "1")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"oltuser","Password":"secret"}`)
	form.Set("saveData", `{"TerminalUserName":"newuser","TerminalPassword":"Abc12345"}`)
	code, payload := postOltForm(t, module.handleOltTerminalUser, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=1 失败: http=%d payload=%v", code, payload)
	}
	if sawOltTerminalLogs(t, module, "新增") != 1 {
		t.Fatalf("应写入一条「新增」操作日志")
	}
	commands := server.Commands()
	if !commandsContain(commands, "terminal user name", "newuser") {
		t.Fatalf("未发送新增序列，收到: %v", commands)
	}
}

// TestHandleOltTerminalUserChangePassword 覆盖 Act=2：修改终端用户口令确认 (y/n)。
func TestHandleOltTerminalUserChangePassword(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "2")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"admin","Password":"secret"}`)
	form.Set("saveData", `{"TerminalUserName":"admin","TerminalPassword":"NewPass1"}`)
	code, payload := postOltForm(t, module.handleOltTerminalUser, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=2 失败: http=%d payload=%v", code, payload)
	}
	if sawOltTerminalLogs(t, module, "修改") != 1 {
		t.Fatalf("应写入一条「修改」操作日志")
	}
	if !containsCommand(server.Commands(), "terminal user password") {
		t.Fatalf("未发送修改口令序列，收到: %v", server.Commands())
	}
}

// TestHandleOltTerminalUserDelete 覆盖 Act=3：删除用户，确认 (y/n)。
func TestHandleOltTerminalUserDelete(t *testing.T) {
	module := newTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "3")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"admin","Password":"secret"}`)
	form.Set("saveData", `{"TerminalUserName":"olt_operator"}`)
	code, payload := postOltForm(t, module.handleOltTerminalUser, form)
	if code != 200 || payload["code"].(float64) != 0 {
		t.Fatalf("Act=3 失败: http=%d payload=%v", code, payload)
	}
	if sawOltTerminalLogs(t, module, "删除") != 1 {
		t.Fatalf("应写入一条「删除」操作日志")
	}
	if !containsCommand(server.Commands(), "undo terminal user name") {
		t.Fatalf("未收到删除序列，收到: %v", server.Commands())
	}
}

// TestHandleOltTerminalUserRequiresUserName 覆盖写操作缺账号时直接拒绝（不发命令）。
func TestHandleOltTerminalUserRequiresUserName(t *testing.T) {
	module := newTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	form := url.Values{}
	form.Set("Act", "3")
	form.Set("connData", `{"Type":2,"IP":"`+host+`","Port":"`+port+`","Username":"admin","Password":"secret"}`)
	form.Set("saveData", `{"TerminalUserName":""}`)
	code, payload := postOltForm(t, module.handleOltTerminalUser, form)
	if code != 200 || payload["code"].(float64) == 0 {
		t.Fatalf("缺用户名应拒绝: http=%d payload=%v", code, payload)
	}
	if len(server.Commands()) != 0 {
		t.Fatalf("缺用户名不应连接设备，收到: %v", server.Commands())
	}
}

// countOltTerminalLogs 统计"OLT登录用户"菜单下某动作的日志条数。
func countOltTerminalLogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.DB().QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"='OLT登录用户' AND "szAct"=?`, action,
	).Scan(&count); err != nil {
		t.Fatalf("统计操作日志失败: %v", err)
	}
	return count
}

// sawOltTerminalLogs 是 countOltTerminalLogs 的别名（与 handleConnectOltUser 测试命名区分）。
func sawOltTerminalLogs(t *testing.T, module *Module, action string) int {
	return countOltTerminalLogs(t, module, action)
}

// containsCommand 判断 command 列表里是否按顺序出现给定的连续子序列。
func commandsContain(list []string, sequence ...string) bool {
	if len(sequence) == 0 {
		return false
	}
	pos := 0
	for _, s := range list {
		if s == sequence[pos] {
			pos++
			if pos == len(sequence) {
				return true
			}
		}
	}
	return false
}
