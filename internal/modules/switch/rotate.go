package switchmod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"itmsapiproject/internal/core"
)

// ── AES 密钥轮换（平台层注册信息里的 szAesKey 变化时由 core.CredentialRotator 回调）──

// loginFileBackup 记录轮换前的登录记录文件，失败时用于回滚。
type loginFileBackup struct {
	path     string
	original []byte
	existed  bool
	replaced []byte
	mode     os.FileMode
}

// rotateDeviceCredentials 在事务内把设备台账里的账号密码从旧密钥改为新密钥。
//
// 与老实现的差异（更安全，且语义等价）：
//   - 老实现在“无法用旧密钥解密”时直接报错，导致整个注册保存失败，重试也无法推进
//     （例如上一次轮换已部分完成）；这里对“既不是旧密钥密文”的值保持原样，
//     保证轮换可重入，同时不会把明文或已换钥的密文破坏掉。
func (s *Store) rotateDeviceCredentials(oldKeyText, newKeyText string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := core.NormalizeAESKey(oldKeyText); err != nil {
		return nil // 旧密钥缺失/非法（例如首次注册）：没有需要轮换的历史凭据
	}
	if _, err := core.NormalizeAESKey(newKeyText); err != nil {
		return fmt.Errorf("新 AES 密钥格式无效: %w", err)
	}

	var tableCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'tbDeviceInformation'`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 连接池只有一条连接，必须无条件回滚（错误路径不回滚会永久占住它）。
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT nId, szRpcUsername, szRpcPassword FROM tbDeviceInformation ORDER BY nId`)
	if err != nil {
		return err
	}
	type credentialRow struct {
		id       int64
		username string
		password string
	}
	var pending []credentialRow
	for rows.Next() {
		var item credentialRow
		if err := rows.Scan(&item.id, &item.username, &item.password); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, item := range pending {
		username, err := reencryptCredential(item.username, oldKeyText, newKeyText)
		if err != nil {
			return fmt.Errorf("设备 %d 的账号无法轮换: %w", item.id, err)
		}
		password, err := reencryptCredential(item.password, oldKeyText, newKeyText)
		if err != nil {
			return fmt.Errorf("设备 %d 的密码无法轮换: %w", item.id, err)
		}
		if username == item.username && password == item.password {
			continue
		}
		if _, err := tx.Exec(`UPDATE tbDeviceInformation SET szRpcUsername = ?, szRpcPassword = ? WHERE nId = ?`,
			username, password, item.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// rotateLoginFile 把登录记录文件里的账号密码从旧密钥改为新密钥；返回可用于回滚的备份。
func (c *CredentialCipher) rotateLoginFile(oldKeyText, newKeyText string) (*loginFileBackup, error) {
	if c == nil {
		return nil, nil
	}
	if _, err := core.NormalizeAESKey(oldKeyText); err != nil {
		return nil, nil // 旧密钥缺失（首次注册）：没有需要轮换的历史登录记录
	}
	if _, err := core.NormalizeAESKey(newKeyText); err != nil {
		return nil, fmt.Errorf("新 AES 密钥格式无效: %w", err)
	}

	content, err := os.ReadFile(c.loginPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取历史登录信息失败: %w", err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, nil
	}
	var records []SwitchLoginRecord
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, fmt.Errorf("解析历史登录信息失败: %w", err)
	}

	changed := false
	for index := range records {
		username, err := reencryptCredential(records[index].Username, oldKeyText, newKeyText)
		if err != nil {
			return nil, fmt.Errorf("历史登录记录 %s 的账号无法轮换: %w", records[index].Hostip, err)
		}
		password, err := reencryptCredential(records[index].Password, oldKeyText, newKeyText)
		if err != nil {
			return nil, fmt.Errorf("历史登录记录 %s 的密码无法轮换: %w", records[index].Hostip, err)
		}
		if username != records[index].Username || password != records[index].Password {
			changed = true
		}
		records[index].Username = username
		records[index].Password = password
	}
	if !changed {
		return nil, nil
	}

	replacement, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成迁移后的历史登录信息失败: %w", err)
	}
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(c.loginPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	backup := &loginFileBackup{path: c.loginPath, original: content, existed: true, replaced: replacement, mode: mode}
	if err := writeFileAtomically(c.loginPath, replacement, mode); err != nil {
		return backup, fmt.Errorf("更新历史登录信息失败: %w", err)
	}
	return backup, nil
}

// restore 回滚登录记录文件。
func (b *loginFileBackup) restore() error {
	if b == nil {
		return nil
	}
	if !b.existed {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomically(b.path, b.original, b.mode)
}

// reencryptCredential 用旧密钥解密、再用新密钥加密。
// 无法用旧密钥解密的值（历史明文、已换钥的密文）原样返回，保证轮换可重入。
func reencryptCredential(value, oldKeyText, newKeyText string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	oldKey, err := core.NormalizeAESKey(oldKeyText)
	if err != nil {
		return value, nil
	}
	if plaintext, err := core.DecryptSecret(value, oldKey); err == nil {
		newKey, keyErr := core.NormalizeAESKey(newKeyText)
		if keyErr != nil {
			return "", keyErr
		}
		return core.EncryptSecret(plaintext, newKey)
	}
	return value, nil
}

// writeFileAtomically 先写临时文件再改名，避免半截文件被读到。
func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".switch-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
