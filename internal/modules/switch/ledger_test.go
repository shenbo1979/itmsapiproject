package switchmod

import (
	"encoding/json"
	"testing"

	"itmsapiproject/internal/core"
)

// TestDeviceLedgerEncryptsCredentials 覆盖设备台账 CRUD 与账号密码的加密存储（与老库格式互通）。
func TestDeviceLedgerEncryptsCredentials(t *testing.T) {
	module := newTestModule(t)
	const plainUsername = "admin"
	const plainPassword = "Switch@123"

	code, data := postForm(t, module.HandleDeviceLedger, "/itms/handleSwitchInfo/", "nAct=1&"+saveData(t, SwitchDeviceInformation{
		Name: "测试设备", ManageIP: "192.0.2.10", RpcHostip: "192.0.2.10", RpcPort: 22,
		RpcUsername: plainUsername, RpcPassword: plainPassword, RpcType: 0,
	}))
	if code != 0 {
		t.Fatalf("新增设备 code = %d（data=%s）", code, string(data))
	}
	var id int64
	if err := json.Unmarshal(data, &id); err != nil || id <= 0 {
		t.Fatalf("新增设备返回 id 异常：%s", string(data))
	}

	// 库中必须是密文，且能被同一密钥解开（与老库可移植格式互通）。
	var storedUsername, storedPassword string
	var storedType int64
	if err := module.store.DB().QueryRow(
		`SELECT szRpcUsername, szRpcPassword, nRpcType FROM tbDeviceInformation WHERE nId = ?`, id).
		Scan(&storedUsername, &storedPassword, &storedType); err != nil {
		t.Fatal(err)
	}
	if storedUsername == plainUsername || storedPassword == plainPassword {
		t.Fatal("设备账号密码以明文落库了")
	}
	key, err := core.LoadAESKey(module.ctx.CredentialKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := core.DecryptSecret(storedUsername, key); err != nil || got != plainUsername {
		t.Fatalf("库中账号无法解密: got=%q err=%v", got, err)
	}
	if got, err := core.DecryptSecret(storedPassword, key); err != nil || got != plainPassword {
		t.Fatalf("库中密码无法解密: got=%q err=%v", got, err)
	}
	if storedType != 3 {
		t.Fatalf("nRpcType = %d; want 3（nAct=1 时 0 按 SSH 落库）", storedType)
	}

	// 列表接口返回明文（与老实现一致）。
	code, data = postForm(t, module.HandleDeviceLedger, "/itms/handleSwitchInfo/", "nAct=0")
	if code != 0 {
		t.Fatalf("查询设备 code = %d", code)
	}
	var devices []SwitchDeviceInformation
	if err := json.Unmarshal(data, &devices); err != nil {
		t.Fatalf("查询返回解析失败：%s", string(data))
	}
	if len(devices) != 1 || devices[0].RpcUsername != plainUsername || devices[0].RpcPassword != plainPassword {
		t.Fatalf("设备列表未返回明文凭据: %+v", devices)
	}

	// 更新：回传密文时应原样保留，避免二次加密导致无法解密。
	code, data = postForm(t, module.HandleDeviceLedger, "/itms/handleSwitchInfo/", "nAct=2&"+saveData(t, SwitchDeviceInformation{
		Id: id, Name: "测试设备-改名", ManageIP: "192.0.2.10", RpcType: 3,
		RpcUsername: storedUsername, RpcPassword: storedPassword,
	}))
	if code != 0 {
		t.Fatalf("修改设备 code = %d（data=%s）", code, string(data))
	}
	if err := module.store.DB().QueryRow(`SELECT szRpcUsername FROM tbDeviceInformation WHERE nId = ?`, id).
		Scan(&storedUsername); err != nil {
		t.Fatal(err)
	}
	if got, err := core.DecryptSecret(storedUsername, key); err != nil || got != plainUsername {
		t.Fatalf("回传密文被二次加密了: got=%q err=%v", got, err)
	}

	// 删除。
	code, data = postForm(t, module.HandleDeviceLedger, "/itms/handleSwitchInfo/", "nAct=3&"+saveData(t, []int{int(id)}))
	if code != 0 {
		t.Fatalf("删除设备 code = %d（data=%s）", code, string(data))
	}
	remaining, err := module.store.GetAllDeviceInformation()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("删除后仍有 %d 条设备", len(remaining))
	}
}

// TestModelCRUD 覆盖型号（含 8 个字段的修改）。
func TestModelCRUD(t *testing.T) {
	module := newTestModule(t)

	code, data := postForm(t, module.HandleModel, "/itms/handleSwitchModel/", "nAct=1&"+saveData(t, SwitchModel{
		Name: "S5720-28X", VenueId: 1, PurposeId: 2, ProtocolId: 3, EthPort: 24,
		ManufacturerId: 4, CreationTime: "2026-09-21 10:00:00", Remarks: "接入层",
	}))
	if code != 0 {
		t.Fatalf("新增型号 code = %d（data=%s）", code, string(data))
	}
	var id int64
	_ = json.Unmarshal(data, &id)

	code, data = postForm(t, module.HandleModel, "/itms/handleSwitchModel/", "nAct=0")
	if code != 0 {
		t.Fatalf("查询型号 code = %d", code)
	}
	var models []SwitchModel
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("解析型号列表失败：%s", string(data))
	}
	if len(models) != 1 || models[0].EthPort != 24 || models[0].ManufacturerId != 4 {
		t.Fatalf("型号列表 = %+v", models)
	}

	code, _ = postForm(t, module.HandleModel, "/itms/handleSwitchModel/", "nAct=2&"+saveData(t, SwitchModel{
		Id: id, Name: "S5720-28X-A", VenueId: 1, PurposeId: 2, ProtocolId: 3, EthPort: 28,
		ManufacturerId: 4, Remarks: "接入层-改",
	}))
	if code != 0 {
		t.Fatalf("修改型号 code = %d", code)
	}
	updated, err := module.store.GetModelByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "S5720-28X-A" || updated.EthPort != 28 || updated.Remarks != "接入层-改" {
		t.Fatalf("型号修改结果 = %+v", updated)
	}

	code, _ = postForm(t, module.HandleModel, "/itms/handleSwitchModel/", "nAct=3&"+saveData(t, []int{int(id)}))
	if code != 0 {
		t.Fatalf("删除型号 code = %d", code)
	}
}
