package switchmod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"itmsapiproject/internal/core"
)

// 设备凭据与登录记录的加解密：
//
//   - 密钥来自平台层注册信息写入的 aeskey.key（core.LoadAESKey 解析）；
//   - 线格式与老工程完全一致（IV||密文||tag 的 base64，见 core.EncryptSecret），
//     因此老库中已有的密文（szRpcUsername/szRpcPassword）在本模块可直接解密；
//   - 登录记录文件 /home/switchfile/loginfile.json 的结构与升级/回滚语义也保持不变。
const (
	// defaultLoginFilePath 是交换机登录记录文件（老实现同名常量）。
	defaultLoginFilePath = "/home/switchfile/loginfile.json"
)

// SwitchLoginRecord 是登录记录文件中的一行（字段名与老实现一致）。
type SwitchLoginRecord struct {
	Hostip        string `json:"szHostip"`
	Port          int64  `json:"nPort"`
	Type          int64  `json:"nType"`
	Username      string `json:"szUsername,omitempty"`
	Password      string `json:"szPassword,omitempty"`
	Maker         string `json:"szMaker,omitempty"`
	LastLoginTime string `json:"lastLoginTime"`
}

// CredentialCipher 负责设备凭据的加解密与 AES 密钥轮换。
type CredentialCipher struct {
	keyPath   string
	loginPath string
	// mu 保护“轮换 + 读写凭据文件”的临界区（老实现用包级 switchLoginFileMu）。
	mu sync.Mutex
}

// NewCredentialCipher 创建凭据加解密器；keyPath 为空时使用老实现的默认路径。
func NewCredentialCipher(keyPath, loginFilePath string) *CredentialCipher {
	if strings.TrimSpace(keyPath) == "" {
		keyPath = "/home/switchfile/aeskey.key"
	}
	if strings.TrimSpace(loginFilePath) == "" {
		loginFilePath = defaultLoginFilePath
	}
	return &CredentialCipher{keyPath: keyPath, loginPath: loginFilePath}
}

// loadKey 读取当前 AES 密钥（每次读取，保证轮换后立即生效）。
func (c *CredentialCipher) loadKey() ([]byte, error) {
	return core.LoadAESKey(c.keyPath)
}

// encryptCredential 加密单个凭据；空值原样返回。
func (c *CredentialCipher) encryptCredential(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	key, err := c.loadKey()
	if err != nil {
		return "", err
	}
	return core.EncryptSecret(value, key)
}

// decryptCredential 解密单个凭据；密文非法时返回错误（调用方决定是否降级为原文）。
func (c *CredentialCipher) decryptCredential(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	key, err := c.loadKey()
	if err != nil {
		return "", err
	}
	return core.DecryptSecret(value, key)
}

// encryptDeviceInformationCredentials 加密设备账号密码，与老实现 encryptDeviceInformationCredentials 等价。
func (s *Store) encryptDeviceInformationCredentials(username, password string) (string, string, error) {
	encryptedUsername, err := s.encryptDeviceInformationCredential(username)
	if err != nil {
		return "", "", fmt.Errorf("加密设备账号失败: %w", err)
	}
	encryptedPassword, err := s.encryptDeviceInformationCredential(password)
	if err != nil {
		return "", "", fmt.Errorf("加密设备密码失败: %w", err)
	}
	return encryptedUsername, encryptedPassword, nil
}

// encryptDeviceInformationCredential 与老实现一致：已经是合法密文时原样保留，
// 避免把客户端回传的密文再包一层 AES-GCM 导致无法解密。
func (s *Store) encryptDeviceInformationCredential(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	if s.cipher == nil {
		return strings.TrimSpace(value), nil
	}
	if _, err := s.cipher.decryptCredential(value); err == nil {
		return strings.TrimSpace(value), nil
	}
	return s.cipher.encryptCredential(value)
}

// decryptDeviceInformationCredentials 就地解密设备账号密码；解密失败时保留原值（老实现行为）。
func (s *Store) decryptDeviceInformationCredentials(di *SwitchDeviceInformation) {
	if di == nil || s.cipher == nil {
		return
	}
	if plaintext, err := s.cipher.decryptCredential(di.RpcUsername); err == nil {
		di.RpcUsername = plaintext
	}
	if plaintext, err := s.cipher.decryptCredential(di.RpcPassword); err == nil {
		di.RpcPassword = plaintext
	}
}

// ── 登录记录文件 ──

// loadLoginRecords 读取登录记录文件（不存在时返回空列表）。
func (c *CredentialCipher) loadLoginRecords() ([]SwitchLoginRecord, error) {
	content, err := os.ReadFile(c.loginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []SwitchLoginRecord{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return []SwitchLoginRecord{}, nil
	}
	var records []SwitchLoginRecord
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// upsertLoginRecord 写入/更新一条登录记录（按 Hostip 去重并排序，权限 0644，与老实现一致）。
func (c *CredentialCipher) upsertLoginRecord(record SwitchLoginRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(c.loginPath), 0755); err != nil {
		return err
	}
	records, err := c.loadLoginRecords()
	if err != nil {
		return err
	}
	updated := false
	for i := range records {
		if records[i].Hostip != record.Hostip {
			continue
		}
		records[i] = record
		updated = true
		break
	}
	if !updated {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Hostip < records[j].Hostip })
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tempPath := c.loginPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tempPath, c.loginPath)
}

// RecordLoginConnection 记录一次成功登录（账号密码加密后写入登录记录文件）。
func (c *CredentialCipher) RecordLoginConnection(host string, port, connectionType int64, username, password, maker string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	encryptedUsername, err := c.encryptCredential(strings.TrimSpace(username))
	if err != nil {
		return err
	}
	encryptedPassword, err := c.encryptCredential(strings.TrimSpace(password))
	if err != nil {
		return err
	}
	return c.upsertLoginRecord(SwitchLoginRecord{
		Hostip:        host,
		Port:          port,
		Type:          connectionType,
		Username:      encryptedUsername,
		Password:      encryptedPassword,
		Maker:         strings.TrimSpace(maker),
		LastLoginTime: time.Now().Format(time.RFC3339),
	})
}

// ── 老实现 switch_login_file.go 中自由函数的等价实现（供搬运过来的链路调用）──

// decodeSwitchCiphertext 按可移植格式解码 base64 密文（老实现同名函数）。
func (m *Module) decodeSwitchCiphertext(value string) ([]byte, error) {
	return core.DecodeCiphertext(value)
}

// decryptSwitchLoginSecret 用当前 AES 密钥解密凭据（老实现同名函数）。
func (m *Module) decryptSwitchLoginSecret(value string) (string, error) {
	if m == nil || m.cipher == nil {
		return "", fmt.Errorf("switch 模块未完成初始化")
	}
	return m.cipher.decryptCredential(value)
}

// encryptSwitchLoginSecret 用当前 AES 密钥加密凭据（老实现同名函数）。
func (m *Module) encryptSwitchLoginSecret(value string) (string, error) {
	if m == nil || m.cipher == nil {
		return "", fmt.Errorf("switch 模块未完成初始化")
	}
	return m.cipher.encryptCredential(value)
}
