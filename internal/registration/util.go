package registration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"itmsapiproject/internal/core"
)

// maxClockSkew 是浏览器时间与服务器时间的允许偏差：超过则拒绝保存，避免用错误的本地时间注册。
const maxClockSkew = 24 * time.Hour

// writeFileAtomically 先写同目录临时文件再 rename，避免半截文件被读到。
func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".registration-*")
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

// parseRegistrationDate 解析注册日期字段。
//
// tbValidation.RegistrationDate 是 DATETIME 列，SQLite 驱动会把写入时的本地时间回读为
// RFC3339（末尾 Z，数值仍是写入时的本地时间）。这里去掉末尾的 Z 后按本地时间解析，
// 保证注册日期的时刻在数据库往返后不变（否则会整体偏移本地时区，导致剩余天数多一天）。
func parseRegistrationDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "Z")
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		time.RFC3339,
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported registration date")
}

// readMachineID 读取本机机器码：优先 /etc/machine-id、/var/lib/dbus/machine-id，最后回退主机名。
func readMachineID() (string, error) {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		content, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(content)) != "" {
			return strings.TrimSpace(string(content)), nil
		}
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "", fmt.Errorf("无法获取本机机器码")
	}
	return strings.TrimSpace(hostname), nil
}

// remainingDays 把剩余时长向上取整为天数，与到期时间展示口径一致。
func remainingDays(remaining time.Duration) int {
	if remaining <= 0 {
		return 0
	}
	return int((remaining + 24*time.Hour - 1) / (24 * time.Hour))
}

// validateBrowserTime 校验前端提交的浏览器时间（毫秒）与服务器时间的偏差。
func validateBrowserTime(browserTimeMillis int64, serverTime time.Time) error {
	if browserTimeMillis <= 0 {
		return fmt.Errorf("无法获取浏览器时间，请刷新页面后重试")
	}
	browserTime := time.UnixMilli(browserTimeMillis)
	difference := serverTime.Sub(browserTime)
	if difference < 0 {
		difference = -difference
	}
	if difference >= maxClockSkew {
		return fmt.Errorf("浏览器时间与服务器时间相差已达到 1 天，请校准设备和服务器时间后再保存")
	}
	return nil
}

// sanitizeClientMessage 去除返回给前端的消息中的服务端目录信息（实现集中在 core）。
func sanitizeClientMessage(message string) string {
	return core.SanitizeClientMessage(message)
}

// decodeFormJSON 解析老前端的 saveData 字段（JSON 字符串，query 或 form 传参均可）。
func decodeFormJSON(r *http.Request, field string, target any) error {
	if r == nil {
		return fmt.Errorf("request is nil")
	}
	raw := strings.TrimSpace(r.FormValue(field))
	if raw == "" {
		return fmt.Errorf("%s is required", field)
	}
	if target == nil {
		return fmt.Errorf("decode target is nil")
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid %s: %w", field, err)
	}
	return nil
}
