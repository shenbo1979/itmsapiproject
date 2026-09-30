package registration

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"itmsapiproject/internal/core"
)

// testNow 是所有用例的“当前时间”，可通过 setTestNow 推进，保证有效期断言稳定可复现。
var testNow = time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local)

// newTestService 在临时目录里起一套隔离的注册服务：状态文件、注册码、AES 密钥、云授权文件、
// 平台数据库全部落在 t.TempDir()，绝不触碰设备的真实注册数据。
func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Dir:    dir,
		Now:    func() time.Time { return testNow },
		Logger: log.New(io.Discard, "", 0),
	}
	db, err := core.OpenSQLite(filepath.Join(dir, "platform.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	service := New(cfg, db)
	if err := service.EnsureTables(); err != nil {
		t.Fatalf("EnsureTables: %v", err)
	}
	return service
}

// setTestNow 推进/回拨用例时间（回拨用于验证时间锚点防护）。
func setTestNow(t *testing.T, value time.Time) {
	t.Helper()
	previous := testNow
	testNow = value
	t.Cleanup(func() { testNow = previous })
}

// saveRecord 保存注册信息并返回保存后的注册状态。
func saveRecord(t *testing.T, service *Service, record Record) Info {
	t.Helper()
	record.BrowserTime = testNow.UnixMilli()
	if err := service.Save(&record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := service.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return info
}

// TestFirstRegistrationWithoutVerifyInfo 覆盖“无云平台授权”的首次注册：
// 使用机器码 + 随机码 + 30 天试用生成注册码，并写出签名授权状态。
func TestFirstRegistrationWithoutVerifyInfo(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	info := saveRecord(t, service, Record{CompanyName: "测试注册", AESKey: strings.Repeat("a", 32)})
	if !info.Registered || info.Expired {
		t.Fatalf("registration info = %+v; want registered & valid", info)
	}
	if info.RemainingDays != defaultDuration {
		t.Fatalf("remaining days = %d; want %d", info.RemainingDays, defaultDuration)
	}
	if info.Record == nil || !strings.HasPrefix(info.Record.SerialNumber, codePrefix+".") {
		t.Fatalf("serial number = %+v; want local registration code", info.Record)
	}
	if strings.TrimSpace(info.Record.MachineID) == "" || len(info.Record.RandomCode) != randomCodeLen {
		t.Fatalf("record = %+v", info.Record)
	}

	state, exists, err := service.loadLicenseState()
	if err != nil || !exists {
		t.Fatalf("load license state: exists=%v err=%v", exists, err)
	}
	if got := licenseExpiresAt(state).Format("2006-01-02 15:04:05"); got != info.ExpiresAt {
		t.Fatalf("license expiresAt = %s; want %s（页面口径必须与签名文件一致）", got, info.ExpiresAt)
	}
	wantExpires := testNow.AddDate(0, 0, defaultDuration).Format("2006-01-02 15:04:05")
	if info.ExpiresAt != wantExpires {
		t.Fatalf("expiresAt = %s; want %s", info.ExpiresAt, wantExpires)
	}
	// 注册码文件必须已写出，且能被共享密钥解开（主注册服务可解密）。
	if _, err := decodeKeyWithSecret(info.Record.SerialNumber, testSecret); err != nil {
		t.Fatalf("注册码文件内容无法用共享密钥解密: %v", err)
	}
}

// TestFirstRegistrationUsesVerifyInfo 覆盖“存在云平台授权窗口”的首次注册：
// 到期时间取 AuthExpire，SerialNumber 取 IdKey。
func TestFirstRegistrationUsesVerifyInfo(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	registrationTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	expiresAt := registrationTime.AddDate(3, 0, 0)
	writeVerifyInfo(t, service, registrationTime, expiresAt, "SN-2026-0001-ABCDEFGH")

	info := saveRecord(t, service, Record{CompanyName: "云授权注册", AESKey: strings.Repeat("b", 32)})
	if !info.Registered {
		t.Fatalf("registration info = %+v; want registered", info)
	}
	if info.Record.SerialNumber != "SN-2026-0001-ABCDEFGH" {
		t.Fatalf("serial number = %q; want verify_info IdKey", info.Record.SerialNumber)
	}
	if want := expiresAt.Format("2006-01-02 15:04:05"); info.ExpiresAt != want {
		t.Fatalf("expiresAt = %s; want %s", info.ExpiresAt, want)
	}
	wantRemaining := remainingDays(expiresAt.Sub(testNow))
	if info.RemainingDays != wantRemaining {
		t.Fatalf("remaining days = %d; want %d", info.RemainingDays, wantRemaining)
	}
}

// TestFirstRegistrationWithSubmittedCode 覆盖“全新机器直接填注册码激活”。
func TestFirstRegistrationWithSubmittedCode(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	code := localCode(t, service, 45)
	info := saveRecord(t, service, Record{
		CompanyName:  "注册码激活",
		AESKey:       strings.Repeat("c", 32),
		SerialNumber: code,
	})
	if !info.Registered || info.RemainingDays != 45 {
		t.Fatalf("registration info = %+v; want 45 remaining days", info)
	}
	if !strings.HasPrefix(info.Record.SerialNumber, codePrefix+".") {
		t.Fatalf("serial number = %q; want 本机注册码", info.Record.SerialNumber)
	}
	// 表中记录的本机注册码必须与注册码文件一致，且能按本机机器码解开。
	payload, err := service.decodeKey(info.Record.SerialNumber)
	if err != nil {
		t.Fatalf("decode stored serial number: %v", err)
	}
	wantMachine, err := machineCode()
	if err != nil {
		t.Fatal(err)
	}
	if payload.ValidDays != 45 || payload.MachineCode != wantMachine {
		t.Fatalf("stored payload = %+v; want validDays=45 machineCode=%s", payload, wantMachine)
	}
}

// writeVerifyInfo 写入云平台授权窗口文件。
func writeVerifyInfo(t *testing.T, service *Service, registrationTime, expiresAt time.Time, idKey string) {
	t.Helper()
	payload := map[string]any{
		"RegistTime": registrationTime.Unix(),
		"AuthExpire": expiresAt.Unix(),
		"IdKey":      idKey,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomically(service.cfg.VerifyInfoPath, content, 0600); err != nil {
		t.Fatal(err)
	}
}

// localCode 生成一枚“本机注册码”（机器码为本机机器码），用于激活/续期用例。
func localCode(t *testing.T, service *Service, validDays int) string {
	t.Helper()
	code, err := machineCode()
	if err != nil {
		t.Fatalf("machineCode: %v", err)
	}
	random, err := generateRandomCode()
	if err != nil {
		t.Fatalf("generateRandomCode: %v", err)
	}
	encoded, err := service.encodeKey(keyPayload{
		Version:     codeVersion,
		MachineCode: code,
		RandomCode:  random,
		ValidDays:   validDays,
	})
	if err != nil {
		t.Fatalf("encodeKey: %v", err)
	}
	return encoded
}

// TestFirstRegistrationRejectsForeignCode 校验机器码不一致的注册码会被拒绝，
// 避免把别家设备的注册码当成本机激活码。
func TestFirstRegistrationRejectsForeignCode(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	foreign, err := service.encodeKey(keyPayload{
		Version:     codeVersion,
		MachineCode: testMachineCode,
		RandomCode:  testRandomCode,
		ValidDays:   30,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := Record{
		CompanyName:  "外来注册码",
		AESKey:       strings.Repeat("d", 32),
		SerialNumber: foreign,
		BrowserTime:  testNow.UnixMilli(),
	}
	if err := service.Save(&record); err == nil {
		t.Fatal("机器码不一致的注册码被接受了")
	}
	info, err := service.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Registered {
		t.Fatalf("registration info = %+v; want still unregistered", info)
	}
}

// TestRenewalAccumulatesRemaining 校验续期是“原剩余 + 注册码有效时长”（累加，不重置）。
func TestRenewalAccumulatesRemaining(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	first := saveRecord(t, service, Record{CompanyName: "续期测试", AESKey: strings.Repeat("e", 32)})
	if first.RemainingDays != defaultDuration {
		t.Fatalf("first remaining days = %d; want %d", first.RemainingDays, defaultDuration)
	}

	renewal := saveRecord(t, service, Record{
		CompanyName:  "续期测试",
		AESKey:       strings.Repeat("e", 32),
		SerialNumber: localCode(t, service, 60),
	})
	if want := defaultDuration + 60; renewal.RemainingDays != want {
		t.Fatalf("renewed remaining days = %d; want %d", renewal.RemainingDays, want)
	}
	wantExpires := testNow.AddDate(0, 0, defaultDuration+60).Format("2006-01-02 15:04:05")
	if renewal.ExpiresAt != wantExpires {
		t.Fatalf("renewed expiresAt = %s; want %s", renewal.ExpiresAt, wantExpires)
	}
}

// TestRenewalRejectsRepeatedCurrentCode 校验重复提交当前已生效的注册码会被拒绝（防止有效期被重复累加）。
func TestRenewalRejectsRepeatedCurrentCode(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	first := saveRecord(t, service, Record{CompanyName: "重复提交", AESKey: strings.Repeat("f", 32)})
	record := Record{
		CompanyName:  "重复提交",
		AESKey:       strings.Repeat("f", 32),
		SerialNumber: first.Record.SerialNumber,
		BrowserTime:  testNow.UnixMilli(),
	}
	if err := service.Save(&record); err == nil {
		t.Fatal("重复提交当前注册码被接受了")
	}
	after, err := service.Info()
	if err != nil {
		t.Fatal(err)
	}
	if after.RemainingDays != first.RemainingDays || after.ExpiresAt != first.ExpiresAt {
		t.Fatalf("重复提交后有效期被改动: %+v -> %+v", first, after)
	}
}

// TestProfileOnlyUpdateKeepsExpiryAndRotatesAESKey 覆盖“只改公司名称/备注与 AES 密钥”分支：
// 授权不变、到期时间不变，AES 密钥文件被替换，并通过 CredentialHook 通知模块轮换凭据。
func TestProfileOnlyUpdateKeepsExpiryAndRotatesAESKey(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	var hookCalls [][2]string
	service.cfg.CredentialHook = func(oldKey, newKey string) error {
		hookCalls = append(hookCalls, [2]string{oldKey, newKey})
		return nil
	}

	oldKey := strings.Repeat("a", 32)
	first := saveRecord(t, service, Record{CompanyName: "资料更新", AESKey: oldKey})
	// 首次注册写 AES 密钥文件时也会通知一次（oldKey 为空），这里只关注轮换调用。
	hookCalls = nil

	newKey := strings.Repeat("z", 32)
	updated := saveRecord(t, service, Record{CompanyName: "资料更新-改名", AESKey: newKey, Remarks: "备注"})
	if updated.ExpiresAt != first.ExpiresAt || updated.RemainingDays != first.RemainingDays {
		t.Fatalf("仅更新资料时授权被改动: %+v -> %+v", first, updated)
	}
	if updated.Record.SerialNumber != first.Record.SerialNumber {
		t.Fatalf("仅更新资料时注册码被改动: %s -> %s", first.Record.SerialNumber, updated.Record.SerialNumber)
	}
	fileKey, found, err := service.readAESKeyFile()
	if err != nil || !found {
		t.Fatalf("readAESKeyFile: found=%v err=%v", found, err)
	}
	if fileKey != newKey {
		t.Fatalf("AES 密钥文件 = %q; want %q", fileKey, newKey)
	}
	if len(hookCalls) != 1 || hookCalls[0][0] != oldKey || hookCalls[0][1] != newKey {
		t.Fatalf("CredentialHook 调用 = %+v; want [%s %s]", hookCalls, oldKey, newKey)
	}
}

// TestTamperedLicenseStateMarksUnregistered 校验手工改动授权状态文件（延长到期时间）会失效，
// 且此后拒绝保存注册信息（fail-closed），不能借用可篡改的表数据延长授权。
func TestTamperedLicenseStateMarksUnregistered(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)
	if info := saveRecord(t, service, Record{CompanyName: "防篡改", AESKey: strings.Repeat("a", 32)}); !info.Registered {
		t.Fatalf("registration info = %+v; want registered", info)
	}

	state, exists, err := service.loadLicenseState()
	if err != nil || !exists {
		t.Fatalf("load license state: exists=%v err=%v", exists, err)
	}
	state.ExpiresAt = licenseExpiresAt(state).AddDate(1, 0, 0).Unix()
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomically(service.cfg.StatePath, content, 0600); err != nil {
		t.Fatal(err)
	}

	info, err := service.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Registered || info.Expired {
		t.Fatalf("registration info = %+v; want unregistered after tampering", info)
	}
	record := Record{
		CompanyName: "防篡改",
		AESKey:      strings.Repeat("a", 32),
		BrowserTime: testNow.UnixMilli(),
	}
	if err := service.Save(&record); err == nil {
		t.Fatal("授权状态被篡改后仍然允许保存注册信息")
	}
}

// TestClockRollbackDoesNotExtendLicense 校验系统时间回拨不会延长授权（时间锚点被签名保护）。
func TestClockRollbackDoesNotExtendLicense(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)
	first := saveRecord(t, service, Record{CompanyName: "时间锚点", AESKey: strings.Repeat("a", 32)})
	stateBefore, exists, err := service.loadLicenseState()
	if err != nil || !exists {
		t.Fatalf("load license state: exists=%v err=%v", exists, err)
	}

	setTestNow(t, testNow.AddDate(0, 0, -10))
	info, err := service.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !info.Registered {
		t.Fatalf("registration info = %+v; want still registered", info)
	}
	if info.RemainingDays != first.RemainingDays {
		t.Fatalf("回拨系统时间后剩余天数被延长: %d -> %d", first.RemainingDays, info.RemainingDays)
	}
	if info.ExpiresAt != first.ExpiresAt {
		t.Fatalf("回拨系统时间后到期时间被改动: %s -> %s", first.ExpiresAt, info.ExpiresAt)
	}
	// 巡检会把锚点推进到历史最大时间并重新签名（下次回拨同样无效）。
	service.CheckExpiration()
	stateAfter, _, err := service.loadLicenseState()
	if err != nil {
		t.Fatalf("load license state: %v", err)
	}
	if stateAfter.AnchorAt < stateBefore.AnchorAt {
		t.Fatalf("锚点被回拨：%d -> %d（必须保持历史最大值）", stateBefore.AnchorAt, stateAfter.AnchorAt)
	}
}

// TestSerialRenewalUsesVerifyInfoWindow 覆盖“表中记录是序列号”的续期：
// 按云平台授权窗口（AuthExpire）刷新到期时间与剩余天数。
func TestSerialRenewalUsesVerifyInfoWindow(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	registrationTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	firstExpire := registrationTime.AddDate(1, 0, 0)
	writeVerifyInfo(t, service, registrationTime, firstExpire, "SN-SERIAL-0001")
	first := saveRecord(t, service, Record{CompanyName: "序列号续期", AESKey: strings.Repeat("a", 32)})
	if first.Record.SerialNumber != "SN-SERIAL-0001" {
		t.Fatalf("serial number = %q; want IdKey", first.Record.SerialNumber)
	}

	// 云平台延期后再保存：到期时间跟随新窗口。
	secondExpire := registrationTime.AddDate(3, 0, 0)
	writeVerifyInfo(t, service, registrationTime, secondExpire, "SN-SERIAL-0001")
	renewed := saveRecord(t, service, Record{CompanyName: "序列号续期", AESKey: strings.Repeat("a", 32)})
	if want := secondExpire.Format("2006-01-02 15:04:05"); renewed.ExpiresAt != want {
		t.Fatalf("expiresAt = %s; want %s", renewed.ExpiresAt, want)
	}
	if want := remainingDays(secondExpire.Sub(testNow)); renewed.RemainingDays != want {
		t.Fatalf("remaining days = %d; want %d", renewed.RemainingDays, want)
	}
}

// TestGuardBlocksBusinessAPIsUntilRegistered 覆盖注册守卫：
// 未注册时业务接口返回 -3（可读提示 + 注册信息），注册接口本身始终可用。
func TestGuardBlocksBusinessAPIsUntilRegistered(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/itms/handleSwitchInfo/", func(w http.ResponseWriter, _ *http.Request) {
		writeLegacy(w, http.StatusOK, core.CodeOK, "success", map[string]any{"list": []string{}})
	})
	mux.HandleFunc(PathRegistrationInfo, service.HandleRegistrationInfo)
	server := httptest.NewServer(service.Guard(mux))
	defer server.Close()

	// 未注册：业务接口被拦截。
	code, data := postLegacy(t, server.URL+"/itms/handleSwitchInfo/", nil)
	if code != core.CodeUnregistered {
		t.Fatalf("未注册时业务接口 code = %d; want %d", code, core.CodeUnregistered)
	}
	if registered, ok := data["registered"].(bool); !ok || registered {
		t.Fatalf("拦截响应应带上注册信息，data = %+v", data)
	}

	// 未注册：注册信息接口仍可读取（返回草稿记录供前端回显）。
	code, data = postLegacy(t, server.URL+PathRegistrationInfo+"?nAct=0", nil)
	if code != core.CodeOK {
		t.Fatalf("注册接口 code = %d; want 0", code)
	}
	if registered, ok := data["registered"].(bool); !ok || registered {
		t.Fatalf("注册接口 data = %+v; want registered=false", data)
	}
	if _, ok := data["record"]; !ok {
		t.Fatalf("注册接口 data = %+v; want 草稿记录", data)
	}

	// 完成注册后：业务接口放行。
	if info := saveRecord(t, service, Record{CompanyName: "守卫测试", AESKey: strings.Repeat("a", 32)}); !info.Registered {
		t.Fatalf("registration info = %+v; want registered", info)
	}
	code, _ = postLegacy(t, server.URL+"/itms/handleSwitchInfo/", nil)
	if code != core.CodeOK {
		t.Fatalf("注册后业务接口 code = %d; want 0", code)
	}
}

// TestHandlerRegistrationInfoRejectsBadInput 覆盖注册接口的参数校验与浏览器时间偏差校验。
func TestHandlerRegistrationInfoRejectsBadInput(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc(PathRegistrationInfo, service.HandleRegistrationInfo)

	record := Record{
		CompanyName: "参数校验",
		AESKey:      strings.Repeat("a", 32),
		BrowserTime: testNow.AddDate(0, 0, 2).UnixMilli(), // 与服务器时间相差 2 天
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	form := "nAct=1&saveData=" + url.QueryEscape(string(raw))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, postFormRequest(PathRegistrationInfo, form))
	payload := decodeResponse(t, recorder)
	if payload.Code != codeFail || !strings.Contains(payload.Message, "浏览器时间") {
		t.Fatalf("浏览器时间偏差未被拒绝: %+v", payload)
	}

	// 非法 nAct。
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, postFormRequest(PathRegistrationInfo, "nAct=9"))
	if payload := decodeResponse(t, recorder); payload.Code != codeFail {
		t.Fatalf("非法 nAct 未被拒绝: %+v", payload)
	}

	// 非 POST。
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, PathRegistrationInfo, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 状态码 = %d; want 405", recorder.Code)
	}
}

// postFormRequest 构造一个带 form Content-Type 的 POST 请求（老前端 form 提交形态）。
func postFormRequest(target, form string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

// postLegacy 以老前端的方式（POST + query 传参）请求接口，返回 code 与 data。
func postLegacy(t *testing.T, url string, body io.Reader) (int, map[string]any) {
	t.Helper()
	response, err := http.Post(url, "application/x-www-form-urlencoded", body)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer response.Body.Close()
	var payload struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return payload.Code, payload.Data
}

// decodeResponse 解析接口响应（同 postLegacy，但不发网络请求）。
func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder) core.APIResponse {
	t.Helper()
	var payload core.APIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, recorder.Body.String())
	}
	return payload
}
