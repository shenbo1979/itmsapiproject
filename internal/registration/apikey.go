package registration

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

// 本机注册码（itmsapikey.key）与主注册服务 /txapikeygen 使用完全相同的算法与密钥派生方式：
//   - 密钥：REGISTRATION_SECRET 环境变量或共享密钥文件内容（两端必须一致），都没有时使用内置共享密钥
//     defaultSecret，经 scrypt(secret, "txapikeygen-registration-v1", 32) 派生；
//   - 明文：{"v":1,"machineCode":32位字母或数字,"randomCode":18位字母或数字,"validDays":天数}；
//   - 注册码：TXR1.<base64url(iv)>.<base64url(tag)>.<base64url(密文)>（AES-256-GCM）。
//
// 签发（加密）时只使用共享密钥，不使用设备本地的历史密钥文件；解密时依次尝试共享密钥与历史密钥，
// 因此旧版本用历史密钥签发的注册码仍然可读；若文件里的注册码是历史密钥签发的，
// 会自动改用共享密钥重新签发并覆盖（见 loadKeyState），保证主注册服务也能解密。
const (
	// defaultSecret 是主注册服务（/txapikeygen）与客户机共用的内置注册码密钥，两端一致保证双向解密。
	defaultSecret = "SXZitmsRegistrationSharedKeyV1-20260917-9f2c7a4e"

	secretEnv       = "REGISTRATION_SECRET"
	legacySecretEnv = "REGISTRATION_LEGACY_SECRETS"

	legacySecretSuffix = ".legacy"
	secretSalt         = "txapikeygen-registration-v1"
	codePrefix         = "TXR1"
	codeVersion        = 1
	ivSize             = 12
	tagSize            = 16
	machineCodeLen     = 32
	randomCodeLen      = 18
	maxValidDays       = 36500
	// serialNumberLen 是序列号长度：前端提交的注册信息长度等于该值时按序列号处理
	// （与 /dev/shm/verify_info.json 中的 IdKey 匹配），为空或大于该值时按注册码处理，
	// 小于该值时直接判为错误的注册码。
	serialNumberLen      = 24
	maxRegistrationField = 256
)

// secretSource 描述一个用于解密注册码的候选密钥及其来源。
type secretSource struct {
	Source string // 来源说明，用于日志、错误提示与页面展示
	Secret string // scrypt 派生密钥时使用的密钥原文
	Shared bool   // 是否为主注册服务与本机共用的密钥（签发新注册码使用的密钥）
}

// keyPayload 对应 /txapikeygen 写入注册码的明文内容。
type keyPayload struct {
	Version     int    `json:"v"`
	MachineCode string `json:"machineCode"`
	RandomCode  string `json:"randomCode"`
	ValidDays   int    `json:"validDays"`
}

// keyState 描述 itmsapikey.key 中保存的本机注册信息。
type keyState struct {
	Code         string
	MachineCode  string
	RandomCode   string
	ValidDays    int
	Existed      bool
	ModifiedTime time.Time
}

// secretLegacyFilePath 返回主注册服务归档的历史密钥文件位置（替换密钥时旧密钥会追加到该文件）。
func (s *Service) secretLegacyFilePath() string {
	return s.cfg.SecretPath + legacySecretSuffix
}

// fallbackSecretPath 返回设备本地历史密钥文件的兜底位置，与 itmsapikey.key 同目录。
// 该文件由旧版本客户端自动生成，只作为历史密钥参与解密，不再用于签发新注册码。
func (s *Service) fallbackSecretPath() string {
	return filepath.Join(filepath.Dir(s.cfg.KeyPath), ".registration-secret")
}

// readSecretFile 读取密钥文件内容；文件不存在或为空时返回空字符串。
func readSecretFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

// readLegacySecretFile 读取历史密钥归档文件，每行一个密钥，# 开头为注释行。
func readLegacySecretFile(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var secrets []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		secrets = append(secrets, line)
	}
	return secrets, nil
}

// secretText 返回签发（加密）注册码时必须使用的共享密钥：
// REGISTRATION_SECRET → 主注册服务密钥文件 → 内置共享密钥。
func (s *Service) secretText() (string, error) {
	if secret := strings.TrimSpace(os.Getenv(secretEnv)); secret != "" {
		return secret, nil
	}
	secret, err := readSecretFile(s.cfg.SecretPath)
	if err != nil {
		return "", fmt.Errorf("读取注册码密钥文件 %s 失败: %w", s.cfg.SecretPath, err)
	}
	if secret != "" {
		return secret, nil
	}
	return defaultSecret, nil
}

// secretSources 返回解密注册码时依次尝试的密钥：共享密钥优先，之后是历史密钥。
func (s *Service) secretSources() ([]secretSource, error) {
	var sources []secretSource
	appendSource := func(source, secret string, shared bool) {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			return
		}
		for _, existing := range sources {
			if existing.Secret == secret {
				return
			}
		}
		sources = append(sources, secretSource{Source: source, Secret: secret, Shared: shared})
	}

	if secret := strings.TrimSpace(os.Getenv(secretEnv)); secret != "" {
		appendSource("环境变量 "+secretEnv, secret, true)
	}
	secret, err := readSecretFile(s.cfg.SecretPath)
	if err != nil {
		return nil, fmt.Errorf("读取注册码密钥文件 %s 失败: %w", s.cfg.SecretPath, err)
	}
	appendSource("密钥文件 "+s.cfg.SecretPath, secret, true)
	appendSource("内置共享密钥", defaultSecret, true)

	if legacy := strings.TrimSpace(os.Getenv(legacySecretEnv)); legacy != "" {
		for _, item := range strings.Split(legacy, ",") {
			appendSource("环境变量 "+legacySecretEnv, item, false)
		}
	}
	legacyPath := s.secretLegacyFilePath()
	legacySecrets, err := readLegacySecretFile(legacyPath)
	if err != nil {
		s.logger.Printf("读取注册码历史密钥文件 %s 失败，已忽略: %v", legacyPath, err)
	} else {
		for _, item := range legacySecrets {
			appendSource("历史密钥文件 "+legacyPath, item, false)
		}
	}
	fallbackPath := s.fallbackSecretPath()
	fallbackSecret, err := readSecretFile(fallbackPath)
	if err != nil {
		s.logger.Printf("读取本机历史注册码密钥文件 %s 失败，已忽略: %v", fallbackPath, err)
	} else {
		appendSource("本机历史密钥文件 "+fallbackPath, fallbackSecret, false)
	}
	return sources, nil
}

// deriveKey 派生与 /txapikeygen 一致的 AES-256 密钥。
func deriveKey(secret string) ([]byte, error) {
	key, err := scrypt.Key([]byte(secret), []byte(secretSalt), 16384, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("派生注册码密钥失败: %w", err)
	}
	return key, nil
}

// encryptionKey 返回签发注册码使用的共享密钥派生结果。
func (s *Service) encryptionKey() ([]byte, error) {
	secret, err := s.secretText()
	if err != nil {
		return nil, err
	}
	return deriveKey(secret)
}

// keyFileChange 记录生成本机注册码前的文件状态，注册保存失败时用于回滚。
type keyFileChange struct {
	Path     string
	Previous []byte
	Existed  bool
}

func isAlnum(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case char >= '0' && char <= '9':
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		default:
			return false
		}
	}
	return true
}

func isBase64URL(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case char >= '0' && char <= '9':
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char == '-' || char == '_':
		default:
			return false
		}
	}
	return true
}

// machineCode 返回符合注册码要求的 32 位机器码：机器码本身满足长度与字符集时直接使用，
// 否则（例如回退为主机名）用 SHA-256 前 16 字节归一化为 32 位十六进制。
func machineCode() (string, error) {
	machineID, err := readMachineID()
	if err != nil {
		return "", err
	}
	if isAlnum(machineID, machineCodeLen) {
		return machineID, nil
	}
	sum := sha256.Sum256([]byte(machineID))
	return hex.EncodeToString(sum[:machineCodeLen/2]), nil
}

func normalizeKeyPayload(payload keyPayload) (keyPayload, error) {
	if payload.Version != codeVersion {
		return payload, fmt.Errorf("注册码版本 %d 不受支持", payload.Version)
	}
	machine := strings.TrimSpace(payload.MachineCode)
	if !isAlnum(machine, machineCodeLen) {
		return payload, fmt.Errorf("注册码中的机器码必须为 %d 位字母或数字", machineCodeLen)
	}
	random := strings.TrimSpace(payload.RandomCode)
	if !isAlnum(random, randomCodeLen) {
		return payload, fmt.Errorf("注册码中的随机码必须为 %d 位字母或数字", randomCodeLen)
	}
	if payload.ValidDays < 1 || payload.ValidDays > maxValidDays {
		return payload, fmt.Errorf("注册码中的有效时长必须为 1 到 %d 之间的整数天数", maxValidDays)
	}
	payload.MachineCode = machine
	payload.RandomCode = random
	return payload, nil
}

// encodeKey 使用共享密钥生成与 /txapikeygen 完全一致的 TXR1 注册码。
func (s *Service) encodeKey(payload keyPayload) (string, error) {
	key, err := s.encryptionKey()
	if err != nil {
		return "", err
	}
	return encodeKeyWithKey(payload, key)
}

// encodeKeyWithSecret 使用指定密钥签发注册码，仅用于测试与历史密钥兼容性验证。
func encodeKeyWithSecret(payload keyPayload, secret string) (string, error) {
	key, err := deriveKey(secret)
	if err != nil {
		return "", err
	}
	return encodeKeyWithKey(payload, key)
}

// encodeKeyWithKey 使用已派生的 AES-256 密钥签发 TXR1 注册码。
func encodeKeyWithKey(payload keyPayload, key []byte) (string, error) {
	payload, err := normalizeKeyPayload(payload)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化注册码加密失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化注册码加密失败: %w", err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", fmt.Errorf("生成注册码随机数失败: %w", err)
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化注册码内容失败: %w", err)
	}
	sealed := gcm.Seal(nil, iv, plain, nil)
	if len(sealed) <= tagSize {
		return "", fmt.Errorf("生成注册码失败")
	}
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]
	parts := []string{
		codePrefix,
		base64.RawURLEncoding.EncodeToString(iv),
		base64.RawURLEncoding.EncodeToString(tag),
		base64.RawURLEncoding.EncodeToString(ciphertext),
	}
	return strings.Join(parts, "."), nil
}

// parseKey 校验 TXR1 注册码结构并返回 iv、tag 与密文。
func parseKey(code string) ([]byte, []byte, []byte, error) {
	parts := strings.Split(strings.TrimSpace(code), ".")
	if len(parts) != 4 || parts[0] != codePrefix {
		return nil, nil, nil, fmt.Errorf("注册码格式无效")
	}
	decoded := make([][]byte, 0, 3)
	for _, part := range parts[1:] {
		if !isBase64URL(part) {
			return nil, nil, nil, fmt.Errorf("注册码格式无效")
		}
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("注册码格式无效")
		}
		decoded = append(decoded, raw)
	}
	iv, tag, ciphertext := decoded[0], decoded[1], decoded[2]
	if len(iv) != ivSize || len(tag) != tagSize || len(ciphertext) == 0 {
		return nil, nil, nil, fmt.Errorf("注册码格式无效")
	}
	return iv, tag, ciphertext, nil
}

// openKey 使用指定密钥校验并解析注册码明文。
func openKey(iv, tag, ciphertext, key []byte) (keyPayload, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return keyPayload{}, fmt.Errorf("初始化注册码解密失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return keyPayload{}, fmt.Errorf("初始化注册码解密失败: %w", err)
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plain, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return keyPayload{}, fmt.Errorf("注册码无法解密或已被篡改")
	}
	var payload keyPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return keyPayload{}, fmt.Errorf("注册码内容无效: %w", err)
	}
	return normalizeKeyPayload(payload)
}

// decodeKeyWithSecret 使用指定密钥解密注册码，仅用于测试与历史密钥兼容性验证。
func decodeKeyWithSecret(code, secret string) (keyPayload, error) {
	iv, tag, ciphertext, err := parseKey(code)
	if err != nil {
		return keyPayload{}, err
	}
	key, err := deriveKey(secret)
	if err != nil {
		return keyPayload{}, err
	}
	return openKey(iv, tag, ciphertext, key)
}

// decodeKey 解密并校验 TXR1 注册码：先共享密钥，再依次尝试历史密钥。
func (s *Service) decodeKey(code string) (keyPayload, error) {
	payload, _, err := s.decodeKeyWithSource(code)
	return payload, err
}

// decodeKeyWithSource 与 decodeKey 相同，并返回解密成功所用的密钥来源。
func (s *Service) decodeKeyWithSource(code string) (keyPayload, secretSource, error) {
	iv, tag, ciphertext, err := parseKey(code)
	if err != nil {
		return keyPayload{}, secretSource{}, err
	}
	sources, err := s.secretSources()
	if err != nil {
		return keyPayload{}, secretSource{}, err
	}
	for _, source := range sources {
		key, deriveErr := deriveKey(source.Secret)
		if deriveErr != nil {
			return keyPayload{}, secretSource{}, deriveErr
		}
		payload, openErr := openKey(iv, tag, ciphertext, key)
		if openErr != nil {
			continue
		}
		return payload, source, nil
	}
	return keyPayload{}, secretSource{},
		fmt.Errorf("注册码无法解密或已被篡改（已尝试 %d 个密钥：%s）", len(sources), secretSourceNames(sources))
}

// secretSourceNames 汇总候选密钥来源，便于在错误提示中排查密钥不一致问题。
func secretSourceNames(sources []secretSource) string {
	if len(sources) == 0 {
		return "无可用密钥"
	}
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Source)
	}
	return strings.Join(names, "、")
}

// readKeyCode 读取 itmsapikey.key 中保存的注册码文本。
func (s *Service) readKeyCode() (string, bool, error) {
	content, err := os.ReadFile(s.cfg.KeyPath)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取注册码文件 %s 失败: %w", s.cfg.KeyPath, err)
	}
	code := strings.TrimSpace(string(content))
	if code == "" {
		return "", false, nil
	}
	return code, true, nil
}

func (s *Service) writeKeyFile(path, code string) error {
	if err := writeFileAtomically(path, []byte(strings.TrimSpace(code)), 0600); err != nil {
		return fmt.Errorf("写入注册码文件 %s 失败: %w", path, err)
	}
	return nil
}

// loadKeyState 读取并解密 itmsapikey.key；文件不存在或为空时 Existed=false。
// 若文件中的注册码是历史密钥签发的（主注册服务无法解密），会自动用共享密钥重新签发并覆盖该文件，
// 同时保留原文件的修改时间，从而保持注册日期不变。
func (s *Service) loadKeyState() (keyState, error) {
	path := s.cfg.KeyPath
	code, exists, err := s.readKeyCode()
	if err != nil || !exists {
		return keyState{}, err
	}
	payload, source, err := s.decodeKeyWithSource(code)
	if err != nil {
		return keyState{}, fmt.Errorf("注册码文件 %s 内容无效: %w", path, err)
	}
	state := keyState{
		Code:        code,
		MachineCode: payload.MachineCode,
		RandomCode:  payload.RandomCode,
		ValidDays:   payload.ValidDays,
		Existed:     true,
	}
	if info, statErr := os.Stat(path); statErr == nil {
		state.ModifiedTime = info.ModTime()
	}
	if source.Shared {
		return state, nil
	}

	// 历史密钥签发的注册码无法在 8096 解密：改用共享密钥重新签发并覆盖文件。
	reissued, reissueErr := s.reissueKeyFile(path, payload, state.ModifiedTime)
	if reissueErr != nil {
		s.logger.Printf("本机注册码由%s签发，使用共享密钥重新签发失败，继续使用原注册码: %v", source.Source, reissueErr)
		return state, nil
	}
	s.logger.Printf("本机注册码原由%s签发，已使用共享密钥重新签发，主注册服务可正常解密", source.Source)
	state.Code = reissued
	return state, nil
}

// reissueKeyFile 使用共享密钥重新签发注册码并覆盖注册码文件，成功后恢复原修改时间，
// 保证注册日期与有效期语义不变。
func (s *Service) reissueKeyFile(path string, payload keyPayload, modifiedTime time.Time) (string, error) {
	code, err := s.encodeKey(payload)
	if err != nil {
		return "", err
	}
	if err := s.writeKeyFile(path, code); err != nil {
		return "", err
	}
	if !modifiedTime.IsZero() {
		if err := os.Chtimes(path, modifiedTime, modifiedTime); err != nil {
			s.logger.Printf("恢复注册码文件 %s 修改时间失败: %v", path, err)
		}
	}
	return code, nil
}

// ensureKeyCode 保证 itmsapikey.key 中保存指定载荷的注册码：
// 文件已存在且机器码、随机码、有效时长都一致时直接复用，否则用共享密钥重新签发后写入，
// 保证主注册服务（8096 /txapikeygen）始终可以解密该注册码。
func (s *Service) ensureKeyCode(payload keyPayload) (string, *keyFileChange, error) {
	normalized, err := normalizeKeyPayload(payload)
	if err != nil {
		return "", nil, err
	}
	if state, stateErr := s.loadKeyState(); stateErr == nil && state.Existed &&
		state.MachineCode == normalized.MachineCode && state.RandomCode == normalized.RandomCode &&
		state.ValidDays == normalized.ValidDays {
		return state.Code, nil, nil
	}
	code, err := s.encodeKey(normalized)
	if err != nil {
		return "", nil, err
	}
	path := s.cfg.KeyPath
	change, err := s.writeKeyCode(path, code, nil)
	if err != nil {
		return "", change, err
	}
	s.logger.Printf("已按 %d 天有效期更新注册码文件 %s", normalized.ValidDays, path)
	return code, change, nil
}

// writeKeyCode 把注册码写入 itmsapikey.key 并返回用于回滚的变更记录；
// 传入已有的 change 时不会覆盖它，保证回滚始终恢复到本次注册保存前的文件状态。
func (s *Service) writeKeyCode(path, code string, change *keyFileChange) (*keyFileChange, error) {
	if change == nil {
		change = &keyFileChange{Path: path}
		if previous, readErr := os.ReadFile(path); readErr == nil {
			change.Previous = previous
			change.Existed = true
		}
	}
	if err := s.writeKeyFile(path, code); err != nil {
		return change, err
	}
	return change, nil
}

// restoreKeyFile 在注册信息保存失败时回滚本次生成的注册码文件。
func (s *Service) restoreKeyFile(change *keyFileChange) error {
	if change == nil || strings.TrimSpace(change.Path) == "" {
		return nil
	}
	if change.Existed {
		return s.writeKeyFile(change.Path, string(change.Previous))
	}
	if err := os.Remove(change.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// isLocalCodeValue 判断记录中的 SerialNumber 是否为本机注册码（TXR1 注册码）。
func isLocalCodeValue(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), codePrefix+".")
}

// generateRandomCode 生成 18 位数字随机码（与老实现一致：每位为 0-9）。
func generateRandomCode() (string, error) {
	raw := make([]byte, randomCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成随机码失败: %w", err)
	}
	for index := range raw {
		raw[index] = '0' + raw[index]%10
	}
	return string(raw), nil
}
