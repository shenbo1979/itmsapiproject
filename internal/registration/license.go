package registration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 授权状态文件（方案 A）保存“到期时间”的权威依据：
//   - ExpiresAt 是绝对到期时刻（Unix 秒，UTC），注册/续期时计算写入，续期时在其上叠加新的有效时长；
//   - AnchorAt 是服务观察到过的最大时间（Unix 秒，UTC），用于抵抗系统时间回拨：
//     判定时使用“生效时间 = max(系统时间, AnchorAt)”，把系统时间往回调不会延长授权；
//   - Signature 是 HMAC-SHA256（共享密钥）签名，覆盖以上字段与本机机器码，
//     因此直接修改 tbValidation 表的数据或改动本文件都不会延长授权，校验失败即视为未授权。
//
// tbValidation.RemainingTime 等字段只作为业务数据与页面展示，到期判定以本文件为准。
const licenseStateVersion = 1

// licenseState 是授权状态文件内容（ExpiresAt/AnchorAt 为 Unix 秒，UTC）。
type licenseState struct {
	Version       int    `json:"v"`
	MachineID     string `json:"machineId"`
	SerialNumber  string `json:"serialNumber"`
	ValidDuration int    `json:"validDuration"`
	RemainingTime int    `json:"remainingTime"`
	RegisteredAt  int64  `json:"registeredAt"`
	ExpiresAt     int64  `json:"expiresAt"`
	AnchorAt      int64  `json:"anchorAt"`
	Signature     string `json:"sig,omitempty"`
}

// licenseStateChange 记录写入授权状态文件前的文件状态，注册保存失败时用于回滚。
type licenseStateChange struct {
	Path     string
	Previous []byte
	Existed  bool
}

// licenseCanonicalText 生成参与签名的规范化文本，字段顺序固定，避免 JSON 序列化差异影响校验。
func licenseCanonicalText(state licenseState) string {
	fields := []string{
		strconv.Itoa(state.Version),
		strings.TrimSpace(state.MachineID),
		strings.TrimSpace(state.SerialNumber),
		strconv.Itoa(state.ValidDuration),
		strconv.Itoa(state.RemainingTime),
		strconv.FormatInt(state.RegisteredAt, 10),
		strconv.FormatInt(state.ExpiresAt, 10),
		strconv.FormatInt(state.AnchorAt, 10),
	}
	return strings.Join(fields, "\n")
}

// licenseSignature 使用共享密钥（REGISTRATION_SECRET → 主注册服务密钥文件 → 内置共享密钥）
// 计算授权状态签名，保证签名不可由客户机自行伪造。
func (s *Service) licenseSignature(state licenseState) (string, error) {
	secret, err := s.secretText()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(licenseCanonicalText(state)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// validateLicenseState 校验授权状态版本、必填字段与签名。
func (s *Service) validateLicenseState(state licenseState) error {
	if state.Version != licenseStateVersion {
		return fmt.Errorf("授权状态版本 %d 不受支持", state.Version)
	}
	if strings.TrimSpace(state.MachineID) == "" {
		return fmt.Errorf("授权状态缺少机器码")
	}
	if state.ExpiresAt <= 0 {
		return fmt.Errorf("授权状态缺少到期时间")
	}
	if state.AnchorAt <= 0 {
		return fmt.Errorf("授权状态缺少时间锚点")
	}
	if strings.TrimSpace(state.Signature) == "" {
		return fmt.Errorf("授权状态缺少签名")
	}
	expected, err := s.licenseSignature(state)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(state.Signature))), []byte(expected)) {
		return fmt.Errorf("授权状态签名校验失败，注册信息可能被修改")
	}
	return nil
}

// loadLicenseState 读取并校验授权状态文件：
// 返回的 exists 表示文件是否存在（不存在时由调用方决定是否补建），err 非空表示文件存在但不可信。
func (s *Service) loadLicenseState() (licenseState, bool, error) {
	path := s.cfg.StatePath
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return licenseState{}, false, nil
	}
	if err != nil {
		return licenseState{}, false, fmt.Errorf("读取授权状态文件 %s 失败: %w", path, err)
	}
	var state licenseState
	if err := json.Unmarshal(content, &state); err != nil {
		return licenseState{}, true, fmt.Errorf("解析授权状态文件 %s 失败: %w", path, err)
	}
	if err := s.validateLicenseState(state); err != nil {
		return licenseState{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return state, true, nil
}

// writeLicenseState 写入签名后的授权状态文件，并返回用于回滚的变更记录。
func (s *Service) writeLicenseState(state licenseState) (*licenseStateChange, error) {
	state.Version = licenseStateVersion
	signature, err := s.licenseSignature(state)
	if err != nil {
		return nil, err
	}
	state.Signature = signature
	content, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("序列化授权状态失败: %w", err)
	}

	path := s.cfg.StatePath
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("创建授权状态目录失败: %w", err)
	}
	change := &licenseStateChange{Path: path}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		change.Previous = previous
		change.Existed = true
	}
	if err := writeFileAtomically(path, content, 0600); err != nil {
		return change, fmt.Errorf("写入授权状态文件 %s 失败: %w", path, err)
	}
	return change, nil
}

// restoreLicenseState 在注册保存失败时回滚本次写入的授权状态文件。
func restoreLicenseState(change *licenseStateChange) error {
	if change == nil || strings.TrimSpace(change.Path) == "" {
		return nil
	}
	if change.Existed {
		return writeFileAtomically(change.Path, change.Previous, 0600)
	}
	if err := os.Remove(change.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// licenseEffectiveNow 返回用于判定到期的时间：系统时间与时间锚点的较大值，
// 因此把系统时间往回调不会延长授权（锚点已被签名保护）。
func licenseEffectiveNow(state licenseState, now time.Time) time.Time {
	if state.AnchorAt <= 0 {
		return now
	}
	anchor := time.Unix(state.AnchorAt, 0).In(time.Local)
	if anchor.After(now) {
		return anchor
	}
	return now
}

// licenseExpiresAt 返回授权状态的绝对到期时刻。
func licenseExpiresAt(state licenseState) time.Time {
	return time.Unix(state.ExpiresAt, 0).In(time.Local)
}

// licenseRemainingDays 返回授权状态相对生效时间的剩余天数（向上取整）。
func licenseRemainingDays(state licenseState, now time.Time) int {
	return remainingDays(licenseExpiresAt(state).Sub(licenseEffectiveNow(state, now)))
}

// licenseAnchor 返回本次注册/续期使用的时间锚点：取历史锚点与当前时间的较大值，
// 保证系统时间回拨后锚点仍记录历史最大时间。
func licenseAnchor(previous licenseState, exists bool, now time.Time) time.Time {
	if !exists {
		return now
	}
	return licenseEffectiveNow(previous, now)
}
