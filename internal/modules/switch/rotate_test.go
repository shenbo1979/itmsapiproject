package switchmod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itmsapiproject/internal/core"
)

// writeKeyFile 写入指定内容的 aeskey.key（轮换用例需要切换密钥）。
func writeKeyFile(t *testing.T, module *Module, key string) {
	t.Helper()
	if err := os.WriteFile(module.ctx.CredentialKeyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestRotateCredentialsReencryptsLedgerAndLoginFile 覆盖 AES 密钥轮换：
// 设备台账与登录记录文件里的凭据都要能用新密钥解开，且轮换可重入（重复调用不破坏数据）。
func TestRotateCredentialsReencryptsLedgerAndLoginFile(t *testing.T) {
	module := newTestModule(t)
	oldKey := testAESKey
	newKey := "0123456789abcdefghijklmnopqrstuv"

	// 准备：一条含凭据的设备台账 + 一条登录记录（都按旧密钥加密）。
	deviceID, err := module.store.CreateDeviceInformation(SwitchDeviceInformation{
		Name: "轮换设备", ManageIP: "192.0.2.20", RpcHostip: "192.0.2.20",
		RpcUsername: "root", RpcPassword: "Passw0rd!", RpcType: 3,
	})
	if err != nil {
		t.Fatalf("CreateDeviceInformation: %v", err)
	}
	if err := module.cipher.upsertLoginRecord(SwitchLoginRecord{
		Hostip: "192.0.2.20", Port: 22, Type: 3,
		Username: mustEncrypt(t, "root", oldKey), Password: mustEncrypt(t, "Passw0rd!", oldKey),
		Maker: "huawei", LastLoginTime: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("upsertLoginRecord: %v", err)
	}

	// 平台层把密钥文件换成新密钥后回调轮换。
	writeKeyFile(t, module, newKey)
	if err := module.RotateCredentials(oldKey, newKey); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	// 台账：能读到明文，且库中密文可用新密钥解开。
	device, err := module.store.GetDeviceInformationByID(deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if device.RpcUsername != "root" || device.RpcPassword != "Passw0rd!" {
		t.Fatalf("轮换后台账无法读出明文: %+v", device)
	}
	var stored string
	if err := module.store.DB().QueryRow(`SELECT szRpcUsername FROM tbDeviceInformation WHERE nId = ?`, deviceID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if got, err := core.DecryptSecret(stored, mustKey(t, newKey)); err != nil || got != "root" {
		t.Fatalf("库中密文未按新密钥重加密: got=%q err=%v", got, err)
	}

	// 登录记录文件：同样能用新密钥解开。
	records, err := module.cipher.loadLoginRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("登录记录条数 = %d; want 1", len(records))
	}
	if got, err := core.DecryptSecret(records[0].Username, mustKey(t, newKey)); err != nil || got != "root" {
		t.Fatalf("登录记录账号未按新密钥重加密: got=%q err=%v", got, err)
	}
	if got, err := core.DecryptSecret(records[0].Password, mustKey(t, newKey)); err != nil || got != "Passw0rd!" {
		t.Fatalf("登录记录密码未按新密钥重加密: got=%q err=%v", got, err)
	}

	// 幂等：再轮换一次（旧密钥参数仍是上一把）不应破坏数据。
	if err := module.RotateCredentials(oldKey, newKey); err != nil {
		t.Fatalf("重复轮换: %v", err)
	}
	again, err := module.store.GetDeviceInformationByID(deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if again.RpcUsername != "root" || again.RpcPassword != "Passw0rd!" {
		t.Fatalf("重复轮换后台账损坏: %+v", again)
	}
}

// TestRotateCredentialsSkipsWithoutOldKey 覆盖首次注册（没有旧密钥）时不轮换、不报错。
func TestRotateCredentialsSkipsWithoutOldKey(t *testing.T) {
	module := newTestModule(t)
	if err := module.RotateCredentials("", testAESKey); err != nil {
		t.Fatalf("首次注册场景应直接跳过: %v", err)
	}
	if err := module.RotateCredentials(testAESKey, testAESKey); err != nil {
		t.Fatalf("密钥未变化应直接跳过: %v", err)
	}
}

// TestReadsCiphertextWrittenInLegacyFormat 验证「老库密文可直接解密」：
// 用老实现同一格式（core.EncryptSecret）写入密文后，模块读取时能还原明文。
func TestReadsCiphertextWrittenInLegacyFormat(t *testing.T) {
	module := newTestModule(t)
	legacyKey, err := core.LoadAESKey(module.ctx.CredentialKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	username, err := core.EncryptSecret("legacy-user", legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	password, err := core.EncryptSecret("legacy-pass", legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.store.DB().Exec(`INSERT INTO tbDeviceInformation
		(szName,szManageIP,nAutoEnable,szRpcHostip,nRpcPort,szRpcUsername,szRpcPassword,nRpcType)
		VALUES (?,?,?,?,?,?,?,?)`, "老库设备", "192.0.2.30", 0, "192.0.2.30", 22, username, password, 3); err != nil {
		t.Fatal(err)
	}
	devices, err := module.store.GetAllDeviceInformation()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].RpcUsername != "legacy-user" || devices[0].RpcPassword != "legacy-pass" {
		t.Fatalf("老库密文未被正确解密: %+v", devices)
	}
}

func mustEncrypt(t *testing.T, plain, keyText string) string {
	t.Helper()
	ciphertext, err := core.EncryptSecret(plain, mustKey(t, keyText))
	if err != nil {
		t.Fatal(err)
	}
	return ciphertext
}

func mustKey(t *testing.T, keyText string) []byte {
	t.Helper()
	key, err := core.NormalizeAESKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestLoginRecordUpsertKeepsSingleRowPerHost 覆盖登录记录按 Hostip 去重与排序（老实现语义）。
func TestLoginRecordUpsertKeepsSingleRowPerHost(t *testing.T) {
	module := newTestModule(t)
	for _, host := range []string{"192.0.2.2", "192.0.2.1", "192.0.2.2"} {
		if err := module.cipher.RecordLoginConnection(host, 22, 3, "admin", "pwd", "huawei"); err != nil {
			t.Fatal(err)
		}
	}
	records, err := module.cipher.loadLoginRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Hostip != "192.0.2.1" || records[1].Hostip != "192.0.2.2" {
		t.Fatalf("登录记录 = %+v; want 去重并按 Hostip 排序", records)
	}
	// 文件顶层是数组（与老实现一致）。
	content, err := os.ReadFile(filepath.Join(filepath.Dir(module.ctx.CredentialKeyPath), "loginfile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded []SwitchLoginRecord
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("登录记录文件不是数组: %v", err)
	}
}
