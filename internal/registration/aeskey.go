package registration

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"itmsapiproject/internal/core"
)

// AES 密钥（注册信息里的 szAesKey）用于各业务模块加密设备凭据（交换机 RPC 账号密码、
// 登录记录等）。注册信息保存时写入 cfg.AESKeyPath（默认 /home/switchfile/aeskey.key），
// 模块统一从该文件读取，避免像老实现那样多个包各持一份路径常量。

// normalizeAESKey 校验 AES-256 密钥格式：32 字节原文、64 位十六进制或 base64（32 字节）。
// 实现集中在 core（core.NormalizeAESKey），保证平台层与业务模块对密钥格式的理解完全一致。
func normalizeAESKey(raw string) ([]byte, error) {
	return core.NormalizeAESKey(raw)
}

// generateAESKey 生成 16 字节随机数并以 32 位十六进制字符串返回（与老实现一致的字符集与长度）。
func generateAESKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 AES 密钥失败: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// readAESKeyFile 读取 AES 密钥文件；文件不存在或为空时 found=false。
func (s *Service) readAESKeyFile() (string, bool, error) {
	content, err := os.ReadFile(s.cfg.AESKeyPath)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取 AES 密钥文件 %s 失败: %w", s.cfg.AESKeyPath, err)
	}
	key := strings.TrimSpace(string(content))
	if key == "" {
		return "", false, nil
	}
	return key, true, nil
}

// writeAESKeyFile 原子写入 AES 密钥文件（0600）。
func (s *Service) writeAESKeyFile(key string) error {
	path := s.cfg.AESKeyPath
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建 AES 密钥目录失败: %w", err)
	}
	if err := writeFileAtomically(path, []byte(strings.TrimSpace(key)), 0600); err != nil {
		return fmt.Errorf("写入 AES 密钥文件 %s 失败: %w", path, err)
	}
	return nil
}

// restoreAESKeyFile 在注册保存失败时回滚 AES 密钥文件（existed=false 表示本次新建，删除即可）。
func (s *Service) restoreAESKeyFile(key string, existed bool) error {
	if existed {
		return s.writeAESKeyFile(key)
	}
	if err := os.Remove(s.cfg.AESKeyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
