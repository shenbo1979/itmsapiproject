package switchmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"itmsapiproject/internal/core"
)

// 第二批（SSH/RPC + 采集）的用例：配置读写、采集表、缓存读取、注册校验前置、参数校验。

// failingRegistration 模拟"未注册/已过期"的注册校验（用于验证设备操作前置校验）。
type failingRegistration struct{}

func (failingRegistration) Require() error { return errRegistrationForTest }

var errRegistrationForTest = &testError{"系统尚未注册"}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }

func TestSnifferAndCollectionSettingsRoundTrip(t *testing.T) {
	module := newTestModule(t)

	// 采集设置：保存后读回一致，并落盘到模块目录。
	record := SwitchCollectionSettings{Enable: 1, IntervalSecond: 120, RetentionPeriod: 5}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := postForm(t, module.handleSwitchCollectionSettings, "/itms/handleSwitchCollectionSettings/",
		"nAct=1&saveData="+escapeFormValue(string(raw)))
	if code != 0 {
		t.Fatalf("保存采集设置 code = %d", code)
	}
	code, data := postForm(t, module.handleSwitchCollectionSettings, "/itms/handleSwitchCollectionSettings/", "nAct=0")
	if code != 0 {
		t.Fatalf("读取采集设置 code = %d", code)
	}
	var list []SwitchCollectionSettings
	if err := json.Unmarshal(data, &list); err != nil || len(list) != 1 {
		t.Fatalf("采集设置读回失败：%s", string(data))
	}
	if list[0] != record {
		t.Fatalf("采集设置 = %+v; want %+v", list[0], record)
	}
	if _, err := os.Stat(module.collectionSettingsPath); err != nil {
		t.Fatalf("采集设置未落盘到 %s: %v", module.collectionSettingsPath, err)
	}

	// 嗅探设置：同样支持保存与读回。
	sniffer := SwitchSnifferConfig{Enable: 1, Type: 2, IntervalSecond: 60}
	rawSniffer, err := json.Marshal(sniffer)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = postForm(t, module.handleSwitchSnifferConfig, "/itms/handleSwitchSnifferConfig/",
		"nAct=1&saveData="+escapeFormValue(string(rawSniffer)))
	if code != 0 {
		t.Fatalf("保存嗅探设置 code = %d", code)
	}
	code, data = postForm(t, module.handleSwitchSnifferConfig, "/itms/handleSwitchSnifferConfig/", "nAct=0")
	if code != 0 {
		t.Fatalf("读取嗅探设置 code = %d", code)
	}
	var snifferList []SwitchSnifferConfig
	if err := json.Unmarshal(data, &snifferList); err != nil || len(snifferList) != 1 {
		t.Fatalf("嗅探设置读回失败：%s", string(data))
	}
	if snifferList[0].Enable != 1 || snifferList[0].IntervalSecond != 60 {
		t.Fatalf("嗅探设置 = %+v", snifferList[0])
	}
}

func escapeFormValue(value string) string {
	replacer := strings.NewReplacer("+", "%2B", "&", "%26", "%", "%25", "=", "%3D")
	return replacer.Replace(value)
}

func TestCollectionTablesCreatedOnMigrate(t *testing.T) {
	module := newTestModule(t)
	var count int
	err := module.store.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'tbSwitchCollection%'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("采集相关表 = %d; want 2（Snapshot + InterfaceStat）", count)
	}
}

func TestDeviceInformationReadsLocalCache(t *testing.T) {
	module := newTestModule(t)
	if err := os.MkdirAll(module.deviceFileDir, 0755); err != nil {
		t.Fatal(err)
	}
	cached := SwitchDeviceDetail{
		Hostip:     "192.0.2.10",
		Online:     1,
		DeviceName: "cache-test",
		Interfaces: []SwitchInterfaceInfo{{Port: "GE0/0/1", Status: "up"}},
	}
	content, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module.deviceFileDir, "192.0.2.10.json"), content, 0644); err != nil {
		t.Fatal(err)
	}

	code, data := postForm(t, module.handleDeviceInformation, "/itms/handleDeviceInformation/", "nAct=0&szLinkIp=192.0.2.10")
	if code != 0 {
		t.Fatalf("读取设备缓存 code = %d", code)
	}
	if !strings.Contains(string(data), "cache-test") {
		t.Fatalf("设备缓存内容 = %s", string(data))
	}

	// 全部缓存列表（不带 szLinkIp）。
	code, data = postForm(t, module.handleDeviceInformation, "/itms/handleDeviceInformation/", "nAct=0")
	if code != 0 || !strings.Contains(string(data), "192.0.2.10") {
		t.Fatalf("设备缓存列表 = %s（code=%d）", string(data), code)
	}
}

func TestDeviceOperationsRequireRegistration(t *testing.T) {
	module := newTestModule(t)

	// 未注入注册校验时不做拦截（与老实现"守卫在入口"一致）。
	// 注入"未注册"的校验器：登录式连接必须在发起网络请求前被拒绝。
	module.ctx.Registration = failingRegistration{}
	if _, err := module.ConnectWithLoginInfo("192.0.2.9", 22, "u", "p", nil,
		SwitchLoginInfo{Username: "u", Password: "p"}); err == nil {
		t.Fatal("未注册时不应允许连接设备")
	} else if !strings.Contains(err.Error(), "尚未注册") {
		t.Fatalf("错误信息 = %v; want 注册相关错误", err)
	}
}

func TestPortDetailValidatesRequest(t *testing.T) {
	module := newTestModule(t)
	code, data := postForm(t, module.handleSwitchPortDetail, "/itms/handleSwitchPortDetail/", "nAct=0")
	if code != core.CodeFail {
		t.Fatalf("缺少参数时 code = %d; want %d", code, core.CodeFail)
	}
	if data != nil && !strings.Contains(string(data), "required") && !strings.Contains(string(data), "invalid") {
		t.Fatalf("缺少参数时响应 = %s", string(data))
	}

	recorder := httptest.NewRecorder()
	module.handleSwitchPortDetail(recorder, httptest.NewRequest(http.MethodGet, "/itms/handleSwitchPortDetail/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 状态码 = %d; want 405", recorder.Code)
	}
}
