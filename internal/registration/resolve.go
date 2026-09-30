package registration

import (
	"fmt"
	"strings"
	"time"
)

// resolved 描述一次注册/续期最终采用的本机注册信息。
type resolved struct {
	serialNumber     string
	machineID        string
	randomCode       string
	registrationDate time.Time
	validDuration    int
	remainingTime    int
	expiresAt        time.Time
}

// payload 返回按本次注册结果签发本机注册码所需的载荷。
func (r resolved) payload() keyPayload {
	return keyPayload{
		Version:     codeVersion,
		MachineCode: r.machineID,
		RandomCode:  r.randomCode,
		ValidDays:   r.validDuration,
	}
}

// license 返回本次注册结果对应的授权状态（到期时间与时间锚点的权威记录）。
func (r resolved) license(anchor time.Time) licenseState {
	return licenseState{
		Version:       licenseStateVersion,
		MachineID:     r.machineID,
		SerialNumber:  r.serialNumber,
		ValidDuration: r.validDuration,
		RemainingTime: r.remainingTime,
		RegisteredAt:  r.registrationDate.Unix(),
		ExpiresAt:     r.expiresAt.Unix(),
		AnchorAt:      anchor.Unix(),
	}
}

// resolveFirstRegistration 处理“tbValidation 表中无记录”时的首次注册，按以下优先级：
//  1. 提交了本机注册码：直接按注册码注册（全新机器可直接填注册码激活），
//     有效时长=注册码的有效时长、RemainingTime=有效时长、到期时刻=当前时间+有效时长；
//  2. verify_info.json 存在：使用其中的 RegistTime/AuthExpire（有效时长=AuthExpire-RegistTime）
//     生成注册码，SerialNumber=IdKey、RemainingTime=AuthExpire-当前时间、到期时刻=AuthExpire；
//  3. 都没有：使用机器码、随机码与初始 30 天有效期生成注册码，
//     SerialNumber=注册码、RemainingTime=30 天、到期时刻=当前时间+30 天。
//
// 三种情况都会按本机机器码、随机码与有效时长生成/更新注册码文件（itmsapikey.key）。
func (s *Service) resolveFirstRegistration(submitted Record, now time.Time) (resolved, *keyFileChange, error) {
	code, err := machineCode()
	if err != nil {
		return resolved{}, nil, err
	}
	random := strings.TrimSpace(submitted.RandomCode)
	if !isAlnum(random, randomCodeLen) {
		random, err = generateRandomCode()
		if err != nil {
			return resolved{}, nil, err
		}
	}

	// 1. 提交了本机注册码：优先按注册码注册。
	submittedPayload, hasCode, err := s.submittedLocalCodePayload(submitted.SerialNumber)
	if err != nil {
		return resolved{}, nil, err
	}
	if hasCode {
		result := resolved{
			machineID:        submittedPayload.MachineCode,
			randomCode:       submittedPayload.RandomCode,
			registrationDate: now,
			validDuration:    submittedPayload.ValidDays,
			remainingTime:    submittedPayload.ValidDays,
			expiresAt:        now.AddDate(0, 0, submittedPayload.ValidDays),
		}
		serial, change, codeErr := s.ensureKeyCode(result.payload())
		if codeErr != nil {
			return resolved{}, change, codeErr
		}
		result.serialNumber = serial
		return result, change, nil
	}

	result := resolved{
		machineID:        code,
		randomCode:       random,
		registrationDate: now,
		validDuration:    defaultDuration,
		remainingTime:    defaultDuration,
		expiresAt:        now.AddDate(0, 0, defaultDuration),
	}
	defaults, found, err := s.loadVerifyDefaults()
	if err != nil {
		return resolved{}, nil, err
	}
	if found {
		// 2. 情况2：以云平台的 RegistTime/AuthExpire 为准，到期时刻取 AuthExpire。
		result.registrationDate = defaults.registrationDate
		result.validDuration = defaults.validDuration
		result.remainingTime = remainingDays(defaults.expiresAt.Sub(now))
		result.expiresAt = defaults.expiresAt
	}

	serial, change, err := s.ensureKeyCode(result.payload())
	if err != nil {
		return resolved{}, change, err
	}
	if found {
		// 情况2 的 SerialNumber 取 verify_info.json 中的 IdKey，注册码只落在 itmsapikey.key。
		result.serialNumber = defaults.serialNumber
	} else {
		// 情况3 的 SerialNumber 即生成的注册码。
		result.serialNumber = serial
	}
	return result, change, nil
}

// submittedLocalCodePayload 解析前端提交的内容是否为本机注册码：
//   - 空或不是 TXR1 注册码（例如序列号）：返回 ok=false，交由云平台授权/试用分支处理；
//   - TXR1 注册码：解密失败或机器码与本机不一致时返回错误，避免把无效注册码当成试用注册。
func (s *Service) submittedLocalCodePayload(value string) (keyPayload, bool, error) {
	value = strings.TrimSpace(value)
	if !isLocalCodeValue(value) {
		return keyPayload{}, false, nil
	}
	payload, _, err := s.decodeKeyWithSource(value)
	if err != nil {
		return keyPayload{}, false, fmt.Errorf("注册码无效: %w", err)
	}
	code, err := machineCode()
	if err != nil {
		return keyPayload{}, false, err
	}
	if payload.MachineCode != code {
		return keyPayload{}, false, fmt.Errorf("注册码中的机器码与本机不一致")
	}
	return payload, true, nil
}

// resolveRenewal 处理注册码记录的续期（表中记录必须是本机注册码，调用方已判定）：
//   - 有效时长=原注册码剩余时长+注册码的有效时长，即到期时刻=原到期时刻+注册码有效时长
//     （原注册码已过期时原剩余时长为 0，以当前时间为起点）；
//   - 按续期后的总有效时长重新签发 itmsapikey.key。
func (s *Service) resolveRenewal(current Record, state licenseState, stateExists bool, submittedCode string, now time.Time) (resolved, *keyFileChange, error) {
	input := strings.TrimSpace(submittedCode)
	payload, _, err := s.decodeKeyWithSource(input)
	if err != nil {
		return resolved{}, nil, fmt.Errorf("注册码无效: %w", err)
	}
	if input == strings.TrimSpace(current.SerialNumber) {
		// 防止把“当前已生效的注册码”再提交一次，导致剩余时长被重复累加。
		return resolved{}, nil, fmt.Errorf("请输入新的注册码：当前注册码已生效，重复提交会重复累加有效期")
	}
	code, err := machineCode()
	if err != nil {
		return resolved{}, nil, err
	}
	if payload.MachineCode != code {
		return resolved{}, nil, fmt.Errorf("注册码中的机器码与本机不一致")
	}

	if !stateExists {
		// 授权状态文件缺失（例如首次升级到本版本）：按表中记录补建，续期仍以历史到期时间为基准。
		migrated, migrateErr := s.licenseStateFromRecord(current)
		if migrateErr != nil {
			return resolved{}, nil, migrateErr
		}
		state = migrated
	}
	effectiveNow := licenseEffectiveNow(state, now)
	baseExpiresAt := licenseExpiresAt(state)
	if baseExpiresAt.Before(effectiveNow) {
		baseExpiresAt = effectiveNow
	}
	expiresAt := baseExpiresAt.AddDate(0, 0, payload.ValidDays)
	remaining := remainingDays(expiresAt.Sub(effectiveNow))

	random := strings.TrimSpace(current.RandomCode)
	if !isAlnum(random, randomCodeLen) {
		random = payload.RandomCode
	}
	result := resolved{
		machineID:        code,
		randomCode:       random,
		registrationDate: now,
		validDuration:    remaining,
		remainingTime:    remaining,
		expiresAt:        expiresAt,
	}
	serial, change, err := s.ensureKeyCode(result.payload())
	if err != nil {
		return resolved{}, change, err
	}
	// 表中记录续期后的本机注册码，与 itmsapikey.key 内容保持一致（按累计有效时长签发）。
	result.serialNumber = serial
	return result, change, nil
}

// resolveSerialRenewal 处理“表中记录是序列号”的续期：
// 使用 verify_info.json 中的 RegistTime/AuthExpire（有效时长=AuthExpire-RegistTime）刷新授权窗口，
// 到期时刻取 AuthExpire、剩余时长=AuthExpire-当前时间，并按该有效时长重新签发 itmsapikey.key。
func (s *Service) resolveSerialRenewal(current Record, now time.Time) (resolved, *keyFileChange, error) {
	defaults, found, err := s.loadVerifyDefaults()
	if err != nil {
		return resolved{}, nil, err
	}
	if !found {
		return resolved{}, nil, fmt.Errorf("缺少云平台授权信息，无法保存注册信息")
	}
	code, err := machineCode()
	if err != nil {
		return resolved{}, nil, err
	}
	random := strings.TrimSpace(current.RandomCode)
	if !isAlnum(random, randomCodeLen) {
		random, err = generateRandomCode()
		if err != nil {
			return resolved{}, nil, err
		}
	}

	result := resolved{
		serialNumber:     defaults.serialNumber,
		machineID:        code,
		randomCode:       random,
		registrationDate: defaults.registrationDate,
		validDuration:    defaults.validDuration,
		remainingTime:    remainingDays(defaults.expiresAt.Sub(now)),
		expiresAt:        defaults.expiresAt,
	}
	// 按云平台窗口重新签发 itmsapikey.key；SerialNumber 保持为 verify_info.json 中的 IdKey。
	_, change, err := s.ensureKeyCode(result.payload())
	if err != nil {
		return resolved{}, change, err
	}
	return result, change, nil
}

// resolveProfileOnly 返回“只更新公司名称/备注与 AES 密钥”时使用的注册信息：
// 注册码、机器码、随机码保持表中现有值；注册日期、有效时长与剩余时长优先取签名授权状态（权威来源），
// 因此表中系统字段被改动时会被顺带修正，且不会改变实际到期时间。
func resolveProfileOnly(current Record, state licenseState, stateExists bool, now time.Time) resolved {
	registrationDate, err := parseRegistrationDate(current.RegistrationDate)
	if err != nil {
		registrationDate = now
	}
	result := resolved{
		serialNumber:     strings.TrimSpace(current.SerialNumber),
		machineID:        strings.TrimSpace(current.MachineID),
		randomCode:       strings.TrimSpace(current.RandomCode),
		registrationDate: registrationDate,
		validDuration:    current.ValidDuration,
		remainingTime:    current.RemainingTime,
		expiresAt:        registrationDate.AddDate(0, 0, current.ValidDuration),
	}
	if stateExists {
		if state.RegisteredAt > 0 {
			result.registrationDate = time.Unix(state.RegisteredAt, 0).In(time.Local)
		}
		if state.ValidDuration > 0 {
			result.validDuration = state.ValidDuration
		}
		result.remainingTime = licenseRemainingDays(state, now)
		result.expiresAt = licenseExpiresAt(state)
	}
	return result
}

// licenseStateFromRecord 按 tbValidation 中的记录补建授权状态，
// 用于“已有注册信息但还没有授权状态文件”的场景，保证升级后原有注册与到期时间不被重置。
func (s *Service) licenseStateFromRecord(current Record) (licenseState, error) {
	registrationDate, err := parseRegistrationDate(current.RegistrationDate)
	if err != nil {
		return licenseState{}, fmt.Errorf("注册日期格式无效")
	}
	validDuration := current.ValidDuration
	if validDuration < 1 {
		validDuration = defaultDuration
	}
	remaining := current.RemainingTime
	if remaining < 1 {
		remaining = remainingDays(registrationDate.AddDate(0, 0, validDuration).Sub(s.now()))
	}
	return licenseState{
		Version:       licenseStateVersion,
		MachineID:     strings.TrimSpace(current.MachineID),
		SerialNumber:  strings.TrimSpace(current.SerialNumber),
		ValidDuration: validDuration,
		RemainingTime: remaining,
		RegisteredAt:  registrationDate.Unix(),
		ExpiresAt:     registrationDate.AddDate(0, 0, validDuration).Unix(),
		AnchorAt:      s.now().Unix(),
	}, nil
}
