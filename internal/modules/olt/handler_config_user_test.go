package olt

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"itmsapiproject/internal/core"
)

// TestHandleCurrentConfigurationReadsRefreshesAndFallsBack 覆盖 handleCurrentConfiguration：
// Act=1 连设备采集并落盘 → Act=0 读本模块文件 →（删掉本模块文件后）Act=0 回落到老目录只读兜底。
func TestHandleCurrentConfigurationReadsRefreshesAndFallsBack(t *testing.T) {
	module, fileDir, legacyDir := newDeviceTestModule(t)
	server := startFakeOLT(t)
	host, port := server.HostPort()

	_, refreshed := postOltForm(t, module.handleCurrentConfiguration, url.Values{
		"Act":      {"1"},
		"connData": {oltConnData(host, port)},
	})
	if refreshed["code"].(float64) != 0 {
		t.Fatalf("Act=1 采集失败: %v", refreshed)
	}
	text, _ := refreshed["data"].(string)
	if !strings.Contains(text, "hostname OLT-TEST") {
		t.Fatalf("配置文本缺失内容: %q", text)
	}
	// 老口径：只裁掉回显头（Command: 与命令回显行），配置正文（含结尾 return 行）原样保留。
	if strings.Contains(text, "Command:") {
		t.Fatalf("配置文本未裁掉回显头: %q", text)
	}
	savedPath := filepath.Join(fileDir, host, "savedconfiguration.txt")
	if _, err := os.Stat(savedPath); err != nil {
		t.Fatalf("已保存配置未落盘(%s): %v", savedPath, err)
	}

	// Act=0：设备已关，必须命中文件
	server.Close()
	_, cached := postOltForm(t, module.handleCurrentConfiguration, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if cached["code"].(float64) != 0 || cached["data"] != text {
		t.Fatalf("Act=0 未命中缓存: %v", cached)
	}

	// 删掉本模块文件后，Act=0 应回落到老目录（模拟"老控制台写的缓存，新服务也能读到"）：
	// 先把表里那一行也清掉（表优先，否则会先命中表），再验证文件兜底。
	if _, err := module.store.DB().Exec(`DELETE FROM "tbOltConfigCache" WHERE "HostIP"=? AND "Kind"=?`,
		host, ConfigKindSaved); err != nil {
		t.Fatalf("清表失败: %v", err)
	}
	if err := os.Remove(savedPath); err != nil {
		t.Fatalf("删除本模块缓存失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(legacyDir, host), 0755); err != nil {
		t.Fatalf("建老目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, host, "savedconfiguration.txt"),
		[]byte("hostname LEGACY-OLT"), 0644); err != nil {
		t.Fatalf("写老目录缓存失败: %v", err)
	}
	_, fallback := postOltForm(t, module.handleCurrentConfiguration, url.Values{
		"Act":      {"0"},
		"connData": {`{"IP":"` + host + `"}`},
	})
	if fallback["data"] != "hostname LEGACY-OLT" {
		t.Fatalf("未回落到老目录缓存: %v", fallback)
	}
}

// TestHandleConnectOltUserQueryAndPendingWrite 覆盖 handleConnectOltUser：
// Act=0 查询（口令密文优先解密、明文兜底、按 Automatic 倒序）；Act=1 返回"第二批待迁入"。
func TestHandleConnectOltUserQueryAndPendingWrite(t *testing.T) {
	module, _, _ := newDeviceTestModule(t)

	keyPath := filepath.Join(t.TempDir(), "aeskey.key")
	if err := os.WriteFile(keyPath, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatalf("写密钥失败: %v", err)
	}
	module.ctx.CredentialKeyPath = keyPath
	key, err := core.LoadAESKey(keyPath)
	if err != nil {
		t.Fatalf("LoadAESKey: %v", err)
	}
	cipherPassword, err := core.EncryptSecret("cipher-pass", key)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	if _, err := module.store.DB().Exec(
		`INSERT INTO "tbOltUser" ("Username","Password","Port","Type","Comment","IP","Automatic") VALUES
		 ('plainuser','plain-pass',23,2,'老库存量明文','192.0.2.10',1),
		 ('cipheruser',?,23,3,'迁移后密文','192.0.2.11',0)`, cipherPassword); err != nil {
		t.Fatalf("插入 OLT 用户失败: %v", err)
	}

	_, payload := postOltForm(t, module.handleConnectOltUser, url.Values{"Act": {"0"}})
	if payload["code"].(float64) != 0 {
		t.Fatalf("Act=0 失败: %v", payload)
	}
	users, _ := payload["data"].([]any)
	if len(users) != 2 {
		t.Fatalf("用户数 = %d, want 2", len(users))
	}
	// 老口径 ORDER BY Automatic DESC：Automatic=1 的排在前面
	first := users[0].(map[string]any)
	if first["Username"] != "plainuser" || first["Password"] != "plain-pass" {
		t.Fatalf("首条用户 = %v（明文兜底失败或排序变了）", first)
	}
	second := users[1].(map[string]any)
	if second["Username"] != "cipheruser" || second["Password"] != "cipher-pass" {
		t.Fatalf("密文用户未解密: %v", second)
	}

	// Act=1 缺必填项：明确报错（老实现会写入空账号）
	_, invalid := postOltForm(t, module.handleConnectOltUser, url.Values{"Act": {"1"}, "saveData": {`{"Username":"only_name"}`}})
	if invalid["code"].(float64) != -1 || !strings.Contains(invalid["message"].(string), "IP") {
		t.Fatalf("Act=1 缺 IP 未报错: %v", invalid)
	}
}
