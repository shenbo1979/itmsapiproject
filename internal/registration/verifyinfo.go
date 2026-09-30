package registration

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// defaultDuration 是首次注册（无云平台授权、无注册码）时的试用有效时长（天）。
const defaultDuration = 30

// verifyDefaults 是云平台授权窗口（verify_info.json）解析出的注册默认值。
type verifyDefaults struct {
	registrationDate time.Time
	expiresAt        time.Time
	validDuration    int
	serialNumber     string
}

// verifyInfo 对应云平台写入的授权文件结构，字段可能是字符串或数字（时间戳）。
type verifyInfo struct {
	RegistTime json.RawMessage `json:"RegistTime"`
	AuthExpire json.RawMessage `json:"AuthExpire"`
	IdKey      json.RawMessage `json:"IdKey"`
}

// loadVerifyDefaults 读取云平台授权窗口；文件不存在时 found=false（表示无云授权）。
func (s *Service) loadVerifyDefaults() (verifyDefaults, bool, error) {
	path := s.cfg.VerifyInfoPath
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return verifyDefaults{}, false, nil
	}
	if err != nil {
		return verifyDefaults{}, false, fmt.Errorf("读取原版本注册信息失败: %w", err)
	}

	var info verifyInfo
	if err := json.Unmarshal(content, &info); err != nil {
		return verifyDefaults{}, false, fmt.Errorf("解析原版本注册信息失败: %w", err)
	}
	registrationDate, err := parseVerifyInfoTime(info.RegistTime)
	if err != nil {
		return verifyDefaults{}, false, fmt.Errorf("原版本 RegistTime 无效: %w", err)
	}
	expiresAt, err := parseVerifyInfoTime(info.AuthExpire)
	if err != nil {
		return verifyDefaults{}, false, fmt.Errorf("原版本 AuthExpire 无效: %w", err)
	}
	if !expiresAt.After(registrationDate) {
		return verifyDefaults{}, false, fmt.Errorf("原版本 AuthExpire 必须晚于 RegistTime")
	}
	validDuration := int(math.Ceil(expiresAt.Sub(registrationDate).Hours() / 24))
	if validDuration < 1 {
		return verifyDefaults{}, false, fmt.Errorf("原版本有效时长必须大于 0")
	}
	serialNumber, err := parseVerifyInfoString(info.IdKey)
	if err != nil {
		return verifyDefaults{}, false, fmt.Errorf("原版本 IdKey 无效: %w", err)
	}
	if utf8.RuneCountInString(serialNumber) > maxRegistrationField {
		return verifyDefaults{}, false, fmt.Errorf("原版本 IdKey 长度不能超过 %d 个字符", maxRegistrationField)
	}
	return verifyDefaults{
		registrationDate: registrationDate,
		expiresAt:        expiresAt,
		validDuration:    validDuration,
		serialNumber:     serialNumber,
	}, true, nil
}

func parseVerifyInfoTime(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, fmt.Errorf("字段不能为空")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if parsed, parseErr := parseRegistrationDate(text); parseErr == nil {
			return parsed, nil
		}
		return parseVerifyInfoTimestamp(text)
	}
	return parseVerifyInfoTimestamp(strings.TrimSpace(string(raw)))
}

func parseVerifyInfoTimestamp(value string) (time.Time, error) {
	timestamp, err := strconv.ParseInt(value, 10, 64)
	if err != nil || timestamp <= 0 {
		return time.Time{}, fmt.Errorf("不支持的时间格式")
	}
	if timestamp >= 1_000_000_000_000 {
		return time.UnixMilli(timestamp).In(time.Local), nil
	}
	return time.Unix(timestamp, 0).In(time.Local), nil
}

func parseVerifyInfoString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("字段不能为空")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// newRecordDefaults 返回“未注册时给前端回显的草稿记录”：
// 机器码/随机码/AES 密钥取自本机（AES 密钥文件缺失时临时生成一个），有效时长默认 30 天；
// 若存在云平台授权窗口则按窗口回显，否则回显注册码文件（itmsapikey.key）中保存的信息。
func (s *Service) newRecordDefaults() (Record, error) {
	machineID, err := readMachineID()
	if err != nil {
		return Record{}, err
	}
	randomCode, err := generateRandomCode()
	if err != nil {
		return Record{}, err
	}
	aesKey, found, err := s.readAESKeyFile()
	if err != nil {
		return Record{}, err
	}
	if !found {
		aesKey, err = generateAESKey()
		if err != nil {
			return Record{}, err
		}
	}
	registrationDate := s.now()
	validDuration := defaultDuration
	remaining := defaultDuration
	serialNumber := ""
	defaults, found, err := s.loadVerifyDefaults()
	if err != nil {
		return Record{}, err
	}
	if found {
		registrationDate = defaults.registrationDate
		validDuration = defaults.validDuration
		remaining = remainingDays(defaults.expiresAt.Sub(s.now()))
		serialNumber = defaults.serialNumber
	} else if state, stateErr := s.loadKeyState(); stateErr != nil {
		return Record{}, stateErr
	} else if state.Existed {
		// 无云平台授权时回显注册码文件中保存的本机注册信息。
		serialNumber = state.Code
		machineID = state.MachineCode
		randomCode = state.RandomCode
		validDuration = state.ValidDays
		remaining = state.ValidDays
		if !state.ModifiedTime.IsZero() {
			registrationDate = state.ModifiedTime
		}
	}
	return Record{
		MachineID:        machineID,
		SerialNumber:     serialNumber,
		AESKey:           aesKey,
		RandomCode:       randomCode,
		RegistrationDate: registrationDate.Format("2006-01-02 15:04:05"),
		ValidDuration:    validDuration,
		RemainingTime:    remaining,
	}, nil
}
