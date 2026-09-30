package registration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 测试用共享密钥与 /txapikeygen 服务（REGISTRATION_SECRET=cline-registration-test-secret）一致。
const testSecret = "cline-registration-test-secret"

// testVector 由 /txapikeygen 服务实际生成，用于验证本工程实现与参考实现完全互通。
const testVector = "TXR1.h8mF5AmGTobszmWD.JiPoZ0f-rHMcK0nuI61vDA.KzfTil4veGo_JXwGVzZtky45v_XElzfzdxVsaqLxuQFWDApOH9_Ab4oqABNNOMuvKNaNvilB79uvExA9WRcIi-D4pfR3156JfWdB0GTfTX6UllBqSer4xPjA3E_lRJxs4WYcVk_DlOyy"

// sharedSecretVector 由 /txapikeygen 使用内置共享密钥签发，
// 用于验证未配置密钥的客户机也能解开主注册服务签发的注册码。
const sharedSecretVector = "TXR1.aY7j7Hd3zunPPpHJ.JBJm0hkoN6J_bVeLZlhFyA.xF58NXA7hAaK481d4M0cLIIkmfqbRjD7Wz4nF_BLZMS8iKkP6le1qjE7VJ02kxC9Dm2boF-t4eblFNpS6u-AWBDUL8TAAHyhCb0Tl9-BdbjPL-mmKK3lUahnzJ2EhZ0cIOhUQdqPicy-"

const (
	testMachineCode = "0123456789ABCDEF0123456789ABCDEF"
	testRandomCode  = "ABCDEFGHIJ01234567"
)

// TestKeyCodeMatchesTxapikeygenVector 校验与主注册服务的注册码互通性（黄金向量）。
func TestKeyCodeMatchesTxapikeygenVector(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	payload, err := service.decodeKey(testVector)
	if err != nil {
		t.Fatalf("decode txapikeygen vector: %v", err)
	}
	if payload.Version != 1 || payload.MachineCode != testMachineCode ||
		payload.RandomCode != testRandomCode || payload.ValidDays != 30 {
		t.Fatalf("payload = %+v", payload)
	}

	encoded, err := service.encodeKey(payload)
	if err != nil {
		t.Fatalf("encode registration code: %v", err)
	}
	if !strings.HasPrefix(encoded, codePrefix+".") || len(strings.Split(encoded, ".")) != 4 {
		t.Fatalf("encoded registration code = %q", encoded)
	}
	decoded, err := service.decodeKey(encoded)
	if err != nil {
		t.Fatalf("decode encoded registration code: %v", err)
	}
	if decoded != payload {
		t.Fatalf("round trip payload = %+v; want %+v", decoded, payload)
	}
}

// TestKeyCodeRejectsInvalidInput 校验篡改与非法载荷都会被拒绝。
func TestKeyCodeRejectsInvalidInput(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)

	code, err := service.encodeKey(keyPayload{
		Version:     codeVersion,
		MachineCode: testMachineCode,
		RandomCode:  testRandomCode,
		ValidDays:   30,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(code, ".")
	tampered := strings.Join([]string{parts[0], parts[1], parts[2], "AAAA" + parts[3][4:]}, ".")
	if _, err := service.decodeKey(tampered); err == nil {
		t.Fatal("tampered registration code was accepted")
	}
	for _, invalid := range []string{"", "TXR1.abc", "ABC." + parts[1] + "." + parts[2] + "." + parts[3]} {
		if _, err := service.decodeKey(invalid); err == nil {
			t.Fatalf("malformed registration code %q was accepted", invalid)
		}
	}
	for _, invalid := range []keyPayload{
		{Version: 2, MachineCode: testMachineCode, RandomCode: testRandomCode, ValidDays: 30},
		{Version: 1, MachineCode: "short", RandomCode: testRandomCode, ValidDays: 30},
		{Version: 1, MachineCode: testMachineCode, RandomCode: "short", ValidDays: 30},
		{Version: 1, MachineCode: testMachineCode, RandomCode: testRandomCode, ValidDays: 0},
	} {
		if _, err := service.encodeKey(invalid); err == nil {
			t.Fatalf("invalid payload %+v was encoded", invalid)
		}
	}
}

// TestSecretFallsBackToSharedDefaultWithoutSecretFile 校验未配置密钥时使用内置共享密钥，
// 且客户端不会自行生成密钥文件（否则主注册服务将无法解密本机签发的注册码）。
func TestSecretFallsBackToSharedDefaultWithoutSecretFile(t *testing.T) {
	t.Setenv(secretEnv, "")
	service := newTestService(t)

	secret, err := service.secretText()
	if err != nil {
		t.Fatalf("resolve registration secret: %v", err)
	}
	if secret != defaultSecret {
		t.Fatalf("secret = %q; want built-in shared secret", secret)
	}
	if _, err := os.Stat(service.cfg.SecretPath); !os.IsNotExist(err) {
		t.Fatalf("客户端不得自行生成密钥文件，stat error = %v", err)
	}
	if _, err := os.Stat(service.fallbackSecretPath()); !os.IsNotExist(err) {
		t.Fatalf("客户端不得在注册码目录生成密钥文件，stat error = %v", err)
	}

	payload, err := service.decodeKey(sharedSecretVector)
	if err != nil {
		t.Fatalf("decode registration code issued by main service: %v", err)
	}
	if payload.MachineCode != testMachineCode || payload.RandomCode != testRandomCode || payload.ValidDays != 30 {
		t.Fatalf("payload = %+v", payload)
	}
}

// TestSecretIgnoresDeviceLocalLegacyFileForIssuing 校验历史密钥只用于解密、不用于签发，
// 否则客户机会用主注册服务不知道的密钥签发注册码。
func TestSecretIgnoresDeviceLocalLegacyFileForIssuing(t *testing.T) {
	t.Setenv(secretEnv, "")
	service := newTestService(t)
	const legacySecret = "cline-registration-device-local-secret"
	if err := os.WriteFile(service.fallbackSecretPath(), []byte(legacySecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	secret, err := service.secretText()
	if err != nil {
		t.Fatalf("resolve registration secret: %v", err)
	}
	if secret != defaultSecret {
		t.Fatalf("secret = %q; want built-in shared secret（设备本地历史密钥不得用于签发注册码）", secret)
	}

	payload := keyPayload{Version: codeVersion, MachineCode: testMachineCode, RandomCode: testRandomCode, ValidDays: 30}
	legacyCode, err := encodeKeyWithSecret(payload, legacySecret)
	if err != nil {
		t.Fatalf("encode legacy registration code: %v", err)
	}
	decoded, source, err := service.decodeKeyWithSource(legacyCode)
	if err != nil {
		t.Fatalf("decode legacy registration code: %v", err)
	}
	if decoded != payload {
		t.Fatalf("decoded payload = %+v; want %+v", decoded, payload)
	}
	if source.Shared || !strings.Contains(source.Source, service.fallbackSecretPath()) {
		t.Fatalf("legacy registration code must be decoded with the device local secret, source = %+v", source)
	}
	if _, err := decodeKeyWithSecret(legacyCode, defaultSecret); err == nil {
		t.Fatal("legacy registration code must not be decodable with the shared secret alone")
	}
}

// TestDecodeUsesLegacySecretSources 校验环境变量与主注册服务归档的历史密钥都能解密老注册码。
func TestDecodeUsesLegacySecretSources(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)
	const archivedSecret = "cline-registration-archived-secret"
	const envLegacySecret = "cline-registration-env-legacy-secret"
	if err := os.MkdirAll(filepath.Dir(service.secretLegacyFilePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.secretLegacyFilePath(),
		[]byte("# 历史密钥归档\n"+archivedSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(legacySecretEnv, envLegacySecret)

	payload := keyPayload{Version: codeVersion, MachineCode: testMachineCode, RandomCode: testRandomCode, ValidDays: 30}
	for name, secret := range map[string]string{
		"环境变量历史密钥": envLegacySecret,
		"归档历史密钥":   archivedSecret,
	} {
		code, err := encodeKeyWithSecret(payload, secret)
		if err != nil {
			t.Fatalf("%s: encode: %v", name, err)
		}
		decoded, err := service.decodeKey(code)
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if decoded != payload {
			t.Fatalf("%s: decoded payload = %+v", name, decoded)
		}
	}
}

// TestLoadKeyStateReissuesLegacySignedCode 校验历史密钥签发的注册码会被共享密钥重新签发，
// 保证主注册服务（8096）始终可以解密本机注册码，且注册日期（文件修改时间）保持不变。
func TestLoadKeyStateReissuesLegacySignedCode(t *testing.T) {
	t.Setenv(secretEnv, testSecret)
	service := newTestService(t)
	const legacySecret = "cline-registration-reissue-secret"
	t.Setenv(legacySecretEnv, legacySecret)
	code, err := encodeKeyWithSecret(keyPayload{
		Version:     codeVersion,
		MachineCode: testMachineCode,
		RandomCode:  testRandomCode,
		ValidDays:   30,
	}, legacySecret)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(service.cfg.Dir, "itmsapikey.key")
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	state, err := service.loadKeyState()
	if err != nil {
		t.Fatalf("load key state: %v", err)
	}
	if state.Code == code {
		t.Fatal("历史密钥签发的注册码没有被重新签发")
	}
	if _, err := decodeKeyWithSecret(state.Code, testSecret); err != nil {
		t.Fatalf("重新签发后的注册码无法用共享密钥解密: %v", err)
	}
	reissuedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if reissuedInfo.ModTime().Unix() != originalInfo.ModTime().Unix() {
		t.Fatalf("重新签发后修改时间被改动: %v -> %v", originalInfo.ModTime(), reissuedInfo.ModTime())
	}
}
