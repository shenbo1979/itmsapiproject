package registration

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// checkInterval 是后台注册状态巡检周期（推进时间锚点 + 输出注册状态日志）。
const checkInterval = 24 * time.Hour

// Info 读取注册信息（老 loadRegistrationInfo / 接口 nAct=0）。
//
// 到期时间以签名授权状态文件为准：文件缺失或校验失败都按未注册处理，
// 避免通过修改系统时间或修改 tbValidation 表数据延长授权。
func (s *Service) Info() (Info, error) {
	if s.db == nil {
		return Info{}, fmt.Errorf("平台数据库未初始化")
	}
	if err := s.EnsureTables(); err != nil {
		return Info{}, err
	}

	record, hasRecord, err := s.latestRecord()
	if err != nil {
		return Info{}, err
	}
	if hasRecord {
		if fileKey, found, keyErr := s.readAESKeyFile(); keyErr != nil {
			return Info{}, keyErr
		} else if found {
			record.AESKey = fileKey
		}
		if registrationTime, parseErr := parseRegistrationDate(record.RegistrationDate); parseErr == nil {
			record.RegistrationDate = registrationTime.Format("2006-01-02 15:04:05")
		}
	}

	state, stateExists, stateErr := s.loadLicenseState()
	stateInvalid := stateErr != nil
	if stateInvalid {
		s.logger.Printf("授权状态校验失败，按未注册处理: %v", stateErr)
		stateExists = false
	}
	if stateExists {
		code, machineErr := machineCode()
		if machineErr != nil {
			return Info{}, machineErr
		}
		if state.MachineID != code {
			s.logger.Printf("授权状态中的机器码与本机不一致，按未注册处理: %s != %s", state.MachineID, code)
			stateExists = false
		}
	}
	// 仅当授权状态文件“缺失”时才按表中记录补建（升级兼容）；
	// 文件存在但校验失败说明数据被改动，此时不得回退到可被篡改的 tbValidation，保持未注册。
	if !stateExists && !stateInvalid && hasRecord {
		if migrated, migrateErr := s.licenseStateFromRecord(record); migrateErr == nil {
			if _, writeErr := s.writeLicenseState(migrated); writeErr != nil {
				s.logger.Printf("补建授权状态文件失败: %v", writeErr)
			} else {
				s.logger.Printf("已按 tbValidation 记录补建授权状态文件 %s", s.cfg.StatePath)
			}
			state, stateExists = migrated, true
		} else {
			s.logger.Printf("按 tbValidation 记录补建授权状态失败: %v", migrateErr)
		}
	}

	if !stateExists || !hasRecord {
		draft := record
		if !hasRecord {
			generated, draftErr := s.newRecordDefaults()
			if draftErr != nil {
				return Info{}, draftErr
			}
			draft = generated

		}
		return Info{Registered: false, Record: &draft}, nil
	}

	now := s.now()
	expiresAt := licenseExpiresAt(state)
	return Info{
		Registered:    true,
		Expired:       !licenseEffectiveNow(state, now).Before(expiresAt),
		ExpiresAt:     expiresAt.Format("2006-01-02 15:04:05"),
		RemainingDays: licenseRemainingDays(state, now),
		Record:        &record,
	}, nil
}

// Save 保存注册信息（老 saveRegistrationInfo / 接口 nAct=1）。
//
// 四个分支（与老实现一致）：
//  1. 表中无记录 → 首次注册（提交本机注册码 → 云平台授权 → 30 天试用）；
//  2. 表中是本机注册码且未提交新注册码 → 只更新公司名称/备注与 AES 密钥，授权不变；
//  3. 表中是本机注册码且提交了新注册码 → 续期，剩余时长累加；
//  4. 表中是序列号 → 按云平台授权窗口刷新。
//
// 写入顺序（跨库无法用单事务，按“先让模块换密钥、再落平台文件、最后提交”的顺序保证失败可回退）：
// 模块凭据轮换 → AES 密钥文件 → 授权状态文件 → 提交 tbValidation；提交失败时回滚平台文件并记录日志。
func (s *Service) Save(record *Record) error {
	if record == nil {
		return fmt.Errorf("registration record is nil")
	}
	if s.db == nil {
		return fmt.Errorf("平台数据库未初始化")
	}

	// 同一进程内串行化“读-改-写”，避免并发注册导致注册码 / 授权状态互相覆盖。
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	if err := s.EnsureTables(); err != nil {
		return err
	}
	fileKey, fileKeyExists, err := s.readAESKeyFile()
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 必须无条件回滚（连接池只有一条连接，错误路径不回滚会永久占住它）。
	defer tx.Rollback()

	current, hasCurrent, err := latestRecordTx(tx)
	if err != nil {
		return err
	}

	// 授权状态文件（签名）保存到期时间与时间锚点，是续期累加与到期判定的基准。
	license, licenseExists, licenseErr := s.loadLicenseState()
	if licenseErr != nil {
		// 状态文件被修改：fail-closed，拒绝注册/续期，避免借用可被篡改的表数据延长授权；
		// 详细原因（含文件路径）只写服务端日志，不返回给前端。
		s.logger.Printf("授权状态校验失败，拒绝注册/续期: %v", licenseErr)
		return fmt.Errorf("授权状态校验失败（注册信息可能被修改），请重新注册")
	}

	now := s.now()
	var result resolved
	var keyChange *keyFileChange
	updateLicense := true
	switch {
	case !hasCurrent:
		result, keyChange, err = s.resolveFirstRegistration(*record, now)
	case isLocalCodeValue(current.SerialNumber) && strings.TrimSpace(record.SerialNumber) == "":
		result = resolveProfileOnly(current, license, licenseExists, now)
		updateLicense = false
	case isLocalCodeValue(current.SerialNumber):
		result, keyChange, err = s.resolveRenewal(current, license, licenseExists, record.SerialNumber, now)
	default:
		result, keyChange, err = s.resolveSerialRenewal(current, now)
	}
	if err != nil {
		return err
	}
	keyCommitted := false
	defer func() {
		if keyCommitted || keyChange == nil {
			return
		}
		if restoreErr := s.restoreKeyFile(keyChange); restoreErr != nil {
			s.logger.Printf("回滚本机注册码文件失败: %v", restoreErr)
		}
	}()

	if hasCurrent {
		record.ID = current.ID
	}
	record.SerialNumber = result.serialNumber
	record.MachineID = result.machineID
	record.RandomCode = result.randomCode
	record.RegistrationDate = result.registrationDate.Format("2006-01-02 15:04:05")
	record.ValidDuration = result.validDuration
	record.RemainingTime = result.remainingTime
	if strings.TrimSpace(record.AESKey) == "" {
		if fileKeyExists {
			record.AESKey = fileKey
		} else {
			record.AESKey, err = generateAESKey()
			if err != nil {
				return err
			}
		}
	}
	*record, err = normalizeRecord(*record)
	if err != nil {
		return err
	}

	if record.ID != 0 {
		_, err = tx.Exec(`
			UPDATE tbValidation
			SET CompanyName = ?, MachineId = ?, SerialNumber = ?, AesKey = ?,
			    RandomCode = ?, RegistrationDate = ?, ValidDuration = ?, RemainingTime = ?, Remarks = ?
			WHERE Id = ?
		`, record.CompanyName, record.MachineID, record.SerialNumber, record.AESKey,
			record.RandomCode, record.RegistrationDate, record.ValidDuration, record.RemainingTime, record.Remarks, record.ID)
	} else {
		var insertResult sqlResult
		insertResult, err = tx.Exec(`
			INSERT INTO tbValidation (
				CompanyName, MachineId, SerialNumber, AesKey, RandomCode,
				RegistrationDate, ValidDuration, RemainingTime, Remarks
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.CompanyName, record.MachineID, record.SerialNumber, record.AESKey,
			record.RandomCode, record.RegistrationDate, record.ValidDuration, record.RemainingTime, record.Remarks)
		if err == nil {
			record.ID, err = insertResult.LastInsertId()
		}
	}
	if err != nil {
		return err
	}

	// AES 密钥变化时先让模块用自己的旧/新密钥完成凭据轮换：
	// 失败则整体放弃本次保存（此时尚未写入任何平台文件，状态可回退），
	// 避免出现“密钥文件已换、模块凭据还是旧密钥”的不可解密状态。
	keyChanged := !fileKeyExists || fileKey != record.AESKey
	if keyChanged {
		oldKey := fileKey
		if !fileKeyExists {
			oldKey = current.AESKey
		}
		if hookErr := s.rotateCredentials(oldKey, record.AESKey); hookErr != nil {
			return fmt.Errorf("更新 AES 密钥失败: %w", hookErr)
		}
		if err := s.writeAESKeyFile(record.AESKey); err != nil {
			return err
		}
	}

	// 授权状态文件先落盘，提交失败时与注册码文件一起回滚；
	// 仅更新注册资料（公司名称/备注、AES 密钥）的分支不改变授权，因此不重写状态文件。
	var licenseChange *licenseStateChange
	if updateLicense {
		licenseChange, err = s.writeLicenseState(result.license(licenseAnchor(license, licenseExists, now)))
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		var restoreErrors []string
		if licenseChange != nil {
			if restoreErr := restoreLicenseState(licenseChange); restoreErr != nil {
				restoreErrors = append(restoreErrors, "恢复授权状态文件失败: "+restoreErr.Error())
			}
		}
		if keyChanged {
			if restoreErr := s.restoreAESKeyFile(fileKey, fileKeyExists); restoreErr != nil {
				restoreErrors = append(restoreErrors, "恢复 AES 密钥文件失败: "+restoreErr.Error())
			}
		}
		if len(restoreErrors) > 0 {
			return fmt.Errorf("提交注册信息失败: %v；%s", err, strings.Join(restoreErrors, "；"))
		}
		return err
	}
	keyCommitted = true
	return nil
}

// sqlResult 是 tx.Exec 的返回值（便于在分支里取 LastInsertId）。
type sqlResult interface {
	LastInsertId() (int64, error)
}

// normalizeRecord 校验并规范化提交的注册信息（与老 normalizeValidationRecord 等价）。
func normalizeRecord(record Record) (Record, error) {
	record.CompanyName = strings.TrimSpace(record.CompanyName)
	record.MachineID = strings.TrimSpace(record.MachineID)
	record.SerialNumber = strings.TrimSpace(record.SerialNumber)
	record.AESKey = strings.TrimSpace(record.AESKey)
	record.RandomCode = strings.TrimSpace(record.RandomCode)
	record.RegistrationDate = strings.TrimSpace(record.RegistrationDate)
	record.Remarks = strings.TrimSpace(record.Remarks)

	required := []struct {
		label string
		value string
	}{
		{"公司名称", record.CompanyName},
		{"机器码", record.MachineID},
		{"AES 密钥", record.AESKey},
	}
	for _, field := range required {
		length := utf8.RuneCountInString(field.value)
		if length < 1 || length > maxRegistrationField {
			return record, fmt.Errorf("%s长度必须为 1-%d 个字符", field.label, maxRegistrationField)
		}
	}
	if _, err := normalizeAESKey(record.AESKey); err != nil {
		return record, fmt.Errorf("AES 密钥格式无效: %w", err)
	}
	optional := []struct {
		label string
		value string
	}{
		{"序列号", record.SerialNumber},
		{"备注", record.Remarks},
	}
	for _, field := range optional {
		if utf8.RuneCountInString(field.value) > maxRegistrationField {
			return record, fmt.Errorf("%s长度不能超过 %d 个字符", field.label, maxRegistrationField)
		}
	}
	if utf8.RuneCountInString(record.RandomCode) != randomCodeLen {
		return record, fmt.Errorf("随机码必须为 %d 个字符", randomCodeLen)
	}
	if record.ValidDuration < 1 {
		return record, fmt.Errorf("有效时长必须大于 0")
	}
	if record.RemainingTime < 0 {
		record.RemainingTime = 0
	}

	registrationTime, err := parseRegistrationDate(record.RegistrationDate)
	if err != nil {
		return record, fmt.Errorf("注册日期格式无效")
	}
	record.RegistrationDate = registrationTime.Format("2006-01-02 15:04:05")
	return record, nil
}

// rotateCredentials 调用平台层注入的凭据轮换钩子（各业务模块用新密钥重新加密自己的凭据）。
func (s *Service) rotateCredentials(oldKey, newKey string) error {
	if s.cfg.CredentialHook == nil {
		return nil
	}
	return s.cfg.CredentialHook(oldKey, newKey)
}

// advanceAnchor 把时间锚点推进到当前（更大的）时间并重新签名写回。
// 启动与每日检查时调用，保证系统时间回拨后锚点仍记录历史最大时间。
func (s *Service) advanceAnchor(now time.Time) {
	state, exists, err := s.loadLicenseState()
	if err != nil {
		s.logger.Printf("刷新授权时间锚点失败，注册信息可能已被修改: %v", err)
		return
	}
	if !exists || now.Unix() <= state.AnchorAt {
		return
	}
	state.AnchorAt = now.Unix()
	if _, err := s.writeLicenseState(state); err != nil {
		s.logger.Printf("刷新授权时间锚点失败: %v", err)
	}
}

// CheckExpiration 推进时间锚点并输出当前注册状态日志（老 checkRegistrationExpiration）。
func (s *Service) CheckExpiration() {
	now := s.now()
	s.advanceAnchor(now)
	info, err := s.Info()
	if err != nil {
		s.logger.Printf("计算注册有效期失败: %v", err)
		return
	}
	if !info.Registered {
		s.logger.Printf("注册状态: 未注册")
		return
	}
	if info.Expired {
		s.logger.Printf("注册状态: 已过期, 到期时间=%s", info.ExpiresAt)
		return
	}
	s.logger.Printf("注册状态: 有效, 到期时间=%s, 剩余天数=%d", info.ExpiresAt, info.RemainingDays)
}

// StartDailyCheck 启动后台巡检（启动时先查一次，之后每 24 小时一次）。
func (s *Service) StartDailyCheck(stop <-chan struct{}) {
	s.CheckExpiration()
	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.CheckExpiration()
			}
		}
	}()
}
