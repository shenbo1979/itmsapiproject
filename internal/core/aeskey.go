package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// AES-256 设备凭据密钥的公共实现：注册信息里的 szAesKey 由平台层写入 aeskey.key（默认
// /home/switchfile/aeskey.key），业务模块用同一份实现加解密自己保存的凭据，
// 避免各模块各写一遍格式而产生“能加密不能解密”的偏差。
//
// 线格式（与老工程 switch_login_file.go 完全一致，保证老库密文可直接解密）：
//
//	base64( IV(12) || 密文 || tag(16) )，AES-256-GCM，无 AAD。
const (
	aesKeySize    = 32
	aesGCMIVSize  = 12
	aesGCMTagSize = 16
)

// NormalizeAESKey 校验并解析 AES-256 密钥：32 字节原文、64 位十六进制或 base64（32 字节）。
func NormalizeAESKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case len(raw) == 32:
		return []byte(raw), nil
	case len(raw) == 64:
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == aesKeySize {
			return decoded, nil
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && len(decoded) == aesKeySize {
		return decoded, nil
	}
	return nil, fmt.Errorf("invalid AES-256 key, expected 32-byte raw/base64 or 64-char hex")
}

// LoadAESKey 读取并解析密钥文件；文件不存在或内容非法时返回错误。
func LoadAESKey(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("AES 密钥文件路径不能为空")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 AES 密钥文件 %s 失败: %w", path, err)
	}
	key, err := NormalizeAESKey(string(content))
	if err != nil {
		return nil, fmt.Errorf("AES 密钥文件 %s 内容无效: %w", path, err)
	}
	return key, nil
}

// EncryptSecret 用给定密钥加密明文，返回可移植的 base64 密文；明文为空时返回空串。
func EncryptSecret(plain string, key []byte) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, []byte(plain), nil)
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]

	payload := make([]byte, 0, len(iv)+len(ciphertext)+len(tag))
	payload = append(payload, iv...)
	payload = append(payload, ciphertext...)
	payload = append(payload, tag...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

// DecryptSecret 解开 EncryptSecret 生成的密文；空间不足或校验失败时返回错误。
func DecryptSecret(ciphertext string, key []byte) (string, error) {
	raw, err := decodeCiphertext(ciphertext)
	if err != nil {
		return "", err
	}
	if len(raw) < aesGCMIVSize+aesGCMTagSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ivSize := gcm.NonceSize()
	tagSize := gcm.Overhead()
	if len(raw) < ivSize+tagSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	iv := raw[:ivSize]
	encrypted := raw[ivSize : len(raw)-tagSize]
	tag := raw[len(raw)-tagSize:]
	sealed := make([]byte, 0, len(encrypted)+len(tag))
	sealed = append(sealed, encrypted...)
	sealed = append(sealed, tag...)
	plain, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DecodeCiphertext 按可移植格式解码 base64 密文（兼容空格被替换成 + 的历史写法）。
func DecodeCiphertext(ciphertext string) ([]byte, error) {
	return decodeCiphertext(ciphertext)
}

func decodeCiphertext(ciphertext string) ([]byte, error) {
	normalized := strings.ReplaceAll(ciphertext, " ", "+")
	raw, err := base64.StdEncoding.DecodeString(normalized)
	if err == nil {
		return raw, nil
	}
	return base64.URLEncoding.DecodeString(normalized)
}
