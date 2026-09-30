package olt

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHandleConnectOltUserWriteBranch 覆盖 Act=1 新增 / Act=2 修改 / Act=3 删除：
// 口令入库加密（账号保持可读）、端口按整数落库、操作日志落库、必填校验与错误文案。
func TestHandleConnectOltUserWriteBranch(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	keyPath := filepath.Join(t.TempDir(), "aeskey.key")
	if err := os.WriteFile(keyPath, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatalf("写密钥失败: %v", err)
	}
	module.ctx.CredentialKeyPath = keyPath

	// 新增
	_, created := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"1"},
		"saveData": {`{"Username":"olt_new","Password":"secret-1","Port":"23","Type":2,"IP":"192.0.2.31","Comment":"新增的 OLT"}`},
	})
	if created["code"].(float64) != 0 {
		t.Fatalf("Act=1 新增失败: %v", created)
	}

	var (
		storedPassword string
		storedUsername string
		storedPort     int
		storedType     int
		storedComment  string
		newId          int
	)
	if err := module.store.DB().QueryRow(
		`SELECT "Nid","Username","Password","Port","Type","Comment" FROM "tbOltUser" WHERE "Username"='olt_new'`,
	).Scan(&newId, &storedUsername, &storedPassword, &storedPort, &storedType, &storedComment); err != nil {
		t.Fatalf("新增后未查到记录: %v", err)
	}
	if storedUsername != "olt_new" {
		t.Fatalf("账号列应保持可读，实际 = %q", storedUsername)
	}
	if storedPassword == "secret-1" {
		t.Fatal("口令列未加密（仍是明文）")
	}
	if plain := module.decryptOltSecret(storedPassword); plain != "secret-1" {
		t.Fatalf("口令密文无法还原为原文: %q", plain)
	}
	if storedPort != 23 || storedType != 2 || storedComment != "新增的 OLT" {
		t.Fatalf("新增字段落库不符: port=%d type=%d comment=%q", storedPort, storedType, storedComment)
	}
	if got := countOltLogs(t, module, "新增"); got != 1 {
		t.Fatalf("新增未写操作日志（%d 条）", got)
	}

	// 查询回读：口令解密后明文返回
	_, listed := postOltForm(t, module.handleConnectOltUser, url.Values{"Act": {"0"}})
	users, _ := listed["data"].([]any)
	var found map[string]any
	for _, item := range users {
		if entry, _ := item.(map[string]any); entry["Username"] == "olt_new" {
			found = entry
		}
	}
	if found == nil || found["Password"] != "secret-1" {
		t.Fatalf("Act=0 未回读出新用户或口令未解密: %v", listed["data"])
	}

	// 修改（改备注 / 端口 / 连接类型）
	_, updated := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"2"},
		"saveData": {`{"Id":` + strconv.Itoa(newId) + `,"Username":"olt_new","Password":"secret-1","Port":"12112","Type":3,"IP":"192.0.2.31","Comment":"改过的备注"}`},
	})
	if updated["code"].(float64) != 0 {
		t.Fatalf("Act=2 修改失败: %v", updated)
	}
	if err := module.store.DB().QueryRow(
		`SELECT "Port","Type","Comment","Password" FROM "tbOltUser" WHERE "Nid"=?`, newId,
	).Scan(&storedPort, &storedType, &storedComment, &storedPassword); err != nil {
		t.Fatalf("修改后查询失败: %v", err)
	}
	if storedPort != 12112 || storedType != 3 || storedComment != "改过的备注" {
		t.Fatalf("修改未生效: port=%d type=%d comment=%q", storedPort, storedType, storedComment)
	}
	if plain := module.decryptOltSecret(storedPassword); plain != "secret-1" {
		t.Fatalf("修改后口令无法还原: %q", plain)
	}
	if got := countOltLogs(t, module, "修改"); got != 1 {
		t.Fatalf("修改未写操作日志（%d 条）", got)
	}

	// 修改缺 Id：明确报错
	_, invalidUpdate := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"2"},
		"saveData": {`{"Username":"olt_new","Password":"x","IP":"192.0.2.31"}`},
	})
	if invalidUpdate["code"].(float64) != -1 {
		t.Fatalf("Act=2 缺 Id 未报错: %v", invalidUpdate)
	}

	// 删除
	_, removed := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"3"},
		"saveData": {`{"Id":` + strconv.Itoa(newId) + `}`},
	})
	if removed["code"].(float64) != 0 {
		t.Fatalf("Act=3 删除失败: %v", removed)
	}
	var count int
	if err := module.store.DB().QueryRow(`SELECT COUNT(*) FROM "tbOltUser" WHERE "Nid"=?`, newId).Scan(&count); err != nil {
		t.Fatalf("删除后统计失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("删除未生效，仍有 %d 条", count)
	}
	if got := countOltLogs(t, module, "删除"); got != 1 {
		t.Fatalf("删除未写操作日志（%d 条）", got)
	}
}

// TestHandleConnectOltUserBatchDelete 覆盖 Act=3 的单条与批量删除：
// {"Id":n}（老契约）、{"Ids":"n,m"}（逗号串）、[n,m]（JSON 数组）、空目标报错，以及每条删除写一条操作日志。
func TestHandleConnectOltUserBatchDelete(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	create := func(name, ip string) int {
		t.Helper()
		_, payload := postOltForm(t, module.handleConnectOltUser, url.Values{
			"Act":      {"1"},
			"saveData": {`{"Username":"` + name + `","Password":"Abc12345","IP":"` + ip + `","Type":0,"Port":"23"}`},
		})
		if payload["code"].(float64) != 0 {
			t.Fatalf("造数据失败(%s): %v", name, payload)
		}
		var id int
		if err := module.store.DB().QueryRow(`SELECT "Nid" FROM "tbOltUser" WHERE "Username"=?`, name).Scan(&id); err != nil {
			t.Fatalf("查询新建 Id 失败: %v", err)
		}
		return id
	}
	count := func() int {
		t.Helper()
		var total int
		if err := module.store.DB().QueryRow(`SELECT COUNT(*) FROM "tbOltUser"`).Scan(&total); err != nil {
			t.Fatalf("统计失败: %v", err)
		}
		return total
	}

	idSingle := create("olt_del_1", "192.0.2.31")
	idBatchA := create("olt_del_2", "192.0.2.32")
	idBatchB := create("olt_del_3", "192.0.2.33")
	idArray := create("olt_del_4", "192.0.2.34")
	if got := count(); got != 4 {
		t.Fatalf("造数据后应有 4 条，实际 %d", got)
	}

	// 单条（老契约）
	if _, payload := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"3"},
		"saveData": {`{"Id":` + strconv.Itoa(idSingle) + `}`},
	}); payload["code"].(float64) != 0 {
		t.Fatalf("单条删除失败: %v", payload)
	}
	if got := count(); got != 3 {
		t.Fatalf("单条删除后应有 3 条，实际 %d", got)
	}

	// 批量（Ids 逗号串，前端「删除 (n)」走这条）
	if _, payload := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"3"},
		"saveData": {`{"Ids":"` + strconv.Itoa(idBatchA) + `,` + strconv.Itoa(idBatchB) + `"}`},
	}); payload["code"].(float64) != 0 {
		t.Fatalf("批量删除失败: %v", payload)
	}
	if got := count(); got != 1 {
		t.Fatalf("批量删除后应有 1 条，实际 %d", got)
	}

	// JSON 数组形态（与 switch 台账 api.deleteDevices(ids) 同风格）
	if _, payload := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"3"},
		"saveData": {"[" + strconv.Itoa(idArray) + "]"},
	}); payload["code"].(float64) != 0 {
		t.Fatalf("数组形态删除失败: %v", payload)
	}
	if got := count(); got != 0 {
		t.Fatalf("数组形态删除后应为空，实际 %d", got)
	}

	// 每次删除都写了操作日志（4 条：单条 1 + 批量 2 + 数组 1）
	if got := countOltLogs(t, module, "删除"); got != 4 {
		t.Fatalf("删除操作日志 = %d 条，want 4", got)
	}

	// 空目标：明确报错（不误报成功）
	if _, payload := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"3"},
		"saveData": {`{"Ids":""}`},
	}); payload["code"].(float64) != -1 {
		t.Fatalf("空目标应报错: %v", payload)
	}
}

// TestValidateOltUserWrite 覆盖业务方规格的字段校验：
// IP/域名、连接类型（0 TELNET / 1 SSH）、账号（字母数字下划线 ≤32）、口令（字母+数字或特殊字符 ≤32）、端口 1-65535。
func TestValidateOltUserWrite(t *testing.T) {
	longName := strings.Repeat("a", 33)
	longPassword := strings.Repeat("a", 32) + "1"
	cases := []struct {
		name    string
		user    OltUser
		port    int
		wantErr bool
	}{
		{"IPv4 + TELNET + 规范账号口令", OltUser{IP: "192.0.2.10", Username: "olt_user1", Password: "Abc12345", Type: 0}, 23, false},
		{"域名 + SSH", OltUser{IP: "olt.example.com", Username: "oltuser", Password: "Abc12345", Type: 1}, 22, false},
		{"老口径 Type=3 兼容", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "Abc12345", Type: 3}, 23, false},
		{"非法 IPv4", OltUser{IP: "192.0.2.999", Username: "oltuser", Password: "Abc12345", Type: 0}, 23, true},
		{"非法域名（下划线）", OltUser{IP: "olt_example", Username: "oltuser", Password: "Abc12345", Type: 0}, 23, true},
		{"单段主机名不算域名", OltUser{IP: "oltserver", Username: "oltuser", Password: "Abc12345", Type: 0}, 23, true},
		{"账号含连字符", OltUser{IP: "192.0.2.10", Username: "olt-user", Password: "Abc12345", Type: 0}, 23, true},
		{"账号超长", OltUser{IP: "192.0.2.10", Username: longName, Password: "Abc12345", Type: 0}, 23, true},
		{"口令纯字母", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "abcdefgh", Type: 0}, 23, true},
		{"口令纯数字", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "12345678", Type: 0}, 23, true},
		{"口令含特殊字符", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "Abc123!@#", Type: 0}, 23, false},
		{"口令超长", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: longPassword, Type: 0}, 23, true},
		{"口令含中文", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "口令12345", Type: 0}, 23, true},
		{"端口为 0", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "Abc12345", Type: 0}, 0, true},
		{"端口超上限", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "Abc12345", Type: 0}, 65536, true},
		{"连接类型非法", OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "Abc12345", Type: 9}, 23, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := validateOltUserWrite(item.user, item.port, false)
			if item.wantErr && err == nil {
				t.Fatal("期望校验失败但通过了")
			}
			if !item.wantErr && err != nil {
				t.Fatalf("期望通过但失败: %v", err)
			}
		})
	}

	// 跳过强度校验（编辑时口令未改动）：纯字母口令允许保存，长度与字符集仍校验。
	legacy := OltUser{IP: "192.0.2.10", Username: "oltuser", Password: "legacypass", Type: 0}
	if err := validateOltUserWrite(legacy, 23, true); err != nil {
		t.Fatalf("skip=true 时应放行存量口令: %v", err)
	}
	legacy.Password = longPassword
	if err := validateOltUserWrite(legacy, 23, true); err == nil {
		t.Fatal("skip=true 时长度仍应校验")
	}
}

// TestHandleConnectOltUserRejectsInvalidFields 覆盖接口层的字段校验（写操作返回 -1 与可读提示）。
func TestHandleConnectOltUserRejectsInvalidFields(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)
	cases := []struct {
		name     string
		saveData string
		wantWord string
	}{
		{"非法 IP", `{"Username":"oltuser","Password":"Abc12345","IP":"192.0.2.999","Type":0,"Port":"23"}`, "IPv4"},
		{"账号非法", `{"Username":"olt-user","Password":"Abc12345","IP":"192.0.2.10","Type":0,"Port":"23"}`, "登录账号"},
		{"口令强度不足", `{"Username":"oltuser","Password":"abcdefgh","IP":"192.0.2.10","Type":0,"Port":"23"}`, "密码"},
		{"端口非法", `{"Username":"oltuser","Password":"Abc12345","IP":"192.0.2.10","Type":0,"Port":"70000"}`, "端口"},
		{"连接类型非法", `{"Username":"oltuser","Password":"Abc12345","IP":"192.0.2.10","Type":9,"Port":"23"}`, "连接类型"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, payload := postOltForm(t, module.handleConnectOltUser, url.Values{"Act": {"1"}, "saveData": {item.saveData}})
			if payload["code"].(float64) != -1 || !strings.Contains(payload["message"].(string), item.wantWord) {
				t.Fatalf("未按规格拒绝: %v", payload)
			}
		})
	}

	// 端口留空按默认 23 保存成功
	_, created := postOltForm(t, module.handleConnectOltUser, url.Values{
		"Act":      {"1"},
		"saveData": {`{"Username":"olt_default","Password":"Abc12345","IP":"192.0.2.20","Type":0,"Port":"","Comment":"默认端口"}`},
	})
	if created["code"].(float64) != 0 {
		t.Fatalf("空端口应回落 23 并保存成功: %v", created)
	}
	var port int
	if err := module.store.DB().QueryRow(`SELECT "Port" FROM "tbOltUser" WHERE "Username"='olt_default'`).Scan(&port); err != nil {
		t.Fatalf("查询默认端口失败: %v", err)
	}
	if port != 23 {
		t.Fatalf("默认端口 = %d, want 23", port)
	}
}

// countOltLogs 统计"OLT连接用户"菜单下某个动作的日志条数。
func countOltLogs(t *testing.T, module *Module, action string) int {
	t.Helper()
	var count int
	if err := module.store.DB().QueryRow(
		`SELECT COUNT(*) FROM "tbOltOperationLog" WHERE "szMenu"='OLT连接用户' AND "szAct"=?`, action,
	).Scan(&count); err != nil {
		t.Fatalf("统计操作日志失败: %v", err)
	}
	return count
}
