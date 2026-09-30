package olt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// OLT 设备（连接用户）的写操作校验：与前端 web/src/modules/olt.js 同一套规则（见 docs/09 §5.2）。
//
// 规格（业务方 2026-09-22 明确）：
//   - IP：合法的 IPv4 地址或合法域名；
//   - Type：0 = TELNET（默认）/ 1 = SSH（老口径 2 = TELNET / 3 = SSH 继续兼容）；
//   - Username：大小写字母 + 数字 + 下划线，长度 ≤ 32；
//   - Password：大小写字母 + 数字 + 特殊字符组合，长度 ≤ 32；
//   - Port：1 - 65535；
//   - Comment：备注（可选）。
//
// 服务端同样校验的原因：前端校验只是体验，接口仍需自己守住数据质量
// （老实现完全不校验，脏数据会直接入库）。
var (
	// oltUsernamePattern：字母/数字/下划线，1~32 位。
	oltUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	// oltPasswordPattern：字母/数字/常见特殊字符，1~32 位。
	oltPasswordPattern = regexp.MustCompile("^[A-Za-z0-9!@#$%^&*()_+\\-=\\[\\]{};:'\",./<>?\\\\|`~]{1,32}$")
	// oltHasLetterPattern / oltHasDigitOrSpecialPattern：用于口令强度（不能是纯数字或纯特殊字符）。
	oltHasLetterPattern         = regexp.MustCompile(`[A-Za-z]`)
	oltHasDigitOrSpecialPattern = regexp.MustCompile(`[0-9!@#$%^&*()_+\-=\[\]{};:'",./<>?\\|` + "`" + `~]`)
	// oltDomainPattern：至少两段、域名标签只含字母数字与连字符、顶级域为字母（长度 ≥ 2）。
	oltDomainPattern = regexp.MustCompile(`^(?i)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// validateOltHost 校验"IP 或域名"：合法 IPv4 或合法域名。
func validateOltHost(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("IP 或域名不能为空")
	}
	if len(value) > 253 {
		return fmt.Errorf("IP 或域名过长（≤ 253）")
	}
	if isIPv4(value) {
		return nil
	}
	if oltDomainPattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("请输入合法的 IPv4 地址或域名")
}

// isIPv4 判断点分十进制 IPv4（4 段、每段 0~255、不允许前导零）。
func isIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		if len(part) > 1 && part[0] == '0' {
			return false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || number > 255 {
			return false
		}
	}
	return true
}

// validateOltConnectType 校验连接类型：0 TELNET / 1 SSH（兼容老口径 2 TELNET / 3 SSH）。
func validateOltConnectType(value int) error {
	switch value {
	case 0, 1, 2, 3:
		return nil
	default:
		return fmt.Errorf("连接类型只能是 0（TELNET）或 1（SSH）")
	}
}

// validateOltUserWrite 校验写操作（新增/修改）的字段；skipPasswordStrength=true 时只校验长度与字符集
// （用于"修改时口令未改动"的场景，避免老库里不符合新强度要求的口令无法保存）。
func validateOltUserWrite(user OltUser, port int, skipPasswordStrength bool) error {
	if err := validateOltHost(user.IP); err != nil {
		return err
	}
	if err := validateOltConnectType(user.Type); err != nil {
		return err
	}
	if !oltUsernamePattern.MatchString(user.Username) {
		return fmt.Errorf("登录账号只能包含大小写字母、数字与下划线，长度 1-32")
	}
	password := strings.TrimSpace(user.Password)
	if password == "" {
		return fmt.Errorf("登录密码不能为空")
	}
	if !oltPasswordPattern.MatchString(password) {
		return fmt.Errorf("登录密码只能包含大小写字母、数字与特殊字符，长度 1-32")
	}
	if !skipPasswordStrength && (!oltHasLetterPattern.MatchString(password) || !oltHasDigitOrSpecialPattern.MatchString(password)) {
		return fmt.Errorf("登录密码需同时包含字母与（数字或特殊字符）")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口范围 1-65535")
	}
	return nil
}
