package registration

import (
	"database/sql"
	"fmt"
	"strings"
)

// Record 对应 tbValidation 一行（字段名与老接口逐字一致，禁止改名/改类型）。
type Record struct {
	ID               int64  `json:"nId,omitempty"`
	CompanyName      string `json:"szCompanyName"`
	MachineID        string `json:"szMachineId"`
	SerialNumber     string `json:"szSerialNumber"`
	AESKey           string `json:"szAesKey"`
	RandomCode       string `json:"szRandomCode"`
	RegistrationDate string `json:"dtRegistrationDate"`
	ValidDuration    int    `json:"nValidDuration"`
	RemainingTime    int    `json:"nRemainingTime"`
	Remarks          string `json:"szRemarks"`
	BrowserTime      int64  `json:"nBrowserTime,omitempty"`
}

// Info 是注册信息接口的 data 部分（老接口 RegistrationInfo，字段名与老实现一致）。
type Info struct {
	Registered    bool    `json:"registered"`
	Expired       bool    `json:"expired"`
	ExpiresAt     string  `json:"dtExpiresAt,omitempty"`
	RemainingDays int     `json:"nRemainingDays"`
	Record        *Record `json:"record,omitempty"`
}

// validationTableDDL 与老工程 validation_table.go 完全一致：
// 表名、列名、约束都不变，保证迁移时可以直接整表搬运。
const validationTableDDL = `CREATE TABLE IF NOT EXISTS tbValidation (
	Id INTEGER PRIMARY KEY AUTOINCREMENT,
	CompanyName VARCHAR(256) NOT NULL CHECK(length(CompanyName) BETWEEN 1 AND 256),
	MachineId VARCHAR(256) NOT NULL CHECK(length(MachineId) BETWEEN 1 AND 256),
	SerialNumber VARCHAR(256) NOT NULL CHECK(length(SerialNumber) BETWEEN 0 AND 256),
	AesKey VARCHAR(256) NOT NULL CHECK(length(AesKey) BETWEEN 1 AND 256),
	RandomCode VARCHAR(18) NOT NULL CHECK(length(RandomCode) = 18),
	RegistrationDate DATETIME NOT NULL,
	ValidDuration INTEGER NOT NULL,
	RemainingTime INTEGER NOT NULL DEFAULT 0,
	Remarks VARCHAR(256) NOT NULL CHECK(length(Remarks) BETWEEN 0 AND 256)
)`

// EnsureTables 建表并补齐历史库缺少的列（与老 validation_table.go 的迁移等价）。
func (s *Service) EnsureTables() error {
	if s.db == nil {
		return fmt.Errorf("平台数据库未初始化")
	}
	if _, err := s.db.Exec(validationTableDDL); err != nil {
		return err
	}

	var schema string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'tbValidation'`).Scan(&schema); err != nil {
		return err
	}
	normalizedSchema := strings.ToLower(schema)
	serialRequired := strings.Contains(normalizedSchema, "length(serialnumber) between 1 and 256")
	remarksRequired := strings.Contains(normalizedSchema, "length(remarks) between 1 and 256")
	// RemainingTime 为剩余时长字段（整数，单位与 ValidDuration 一致：天），旧表需要补充该列。
	if !strings.Contains(normalizedSchema, "remainingtime") {
		if _, err := s.db.Exec(`ALTER TABLE tbValidation ADD COLUMN RemainingTime INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !serialRequired && !remarksRequired {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 必须无条件回滚：Commit 成功后 Rollback 是空操作（ErrTxDone）。
	// 连接池只有一条连接，错误路径不回滚会把唯一连接永久占住（老工程死锁事故）。
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE tbValidation RENAME TO tbValidation_required_fields_legacy`); err != nil {
		return err
	}
	if _, err := tx.Exec(validationTableDDL); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO tbValidation (
			Id, CompanyName, MachineId, SerialNumber, AesKey, RandomCode,
			RegistrationDate, ValidDuration, RemainingTime, Remarks
		)
		SELECT Id, CompanyName, MachineId, COALESCE(SerialNumber, ''), AesKey, RandomCode,
		       RegistrationDate, ValidDuration, COALESCE(RemainingTime, 0), COALESCE(Remarks, '')
		FROM tbValidation_required_fields_legacy
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE tbValidation_required_fields_legacy`); err != nil {
		return err
	}
	return tx.Commit()
}

const validationRecordColumns = `Id, CompanyName, MachineId, SerialNumber, AesKey, RandomCode,
		RegistrationDate, ValidDuration, RemainingTime, Remarks`

// latestRecordTx 读取最新一条注册记录（按 Id 倒序，与老实现一致）。
func latestRecordTx(tx *sql.Tx) (Record, bool, error) {
	var record Record
	err := tx.QueryRow(`SELECT `+validationRecordColumns+` FROM tbValidation ORDER BY Id DESC LIMIT 1`).Scan(
		&record.ID, &record.CompanyName, &record.MachineID, &record.SerialNumber,
		&record.AESKey, &record.RandomCode, &record.RegistrationDate,
		&record.ValidDuration, &record.RemainingTime, &record.Remarks,
	)
	switch err {
	case nil:
		return record, true, nil
	case sql.ErrNoRows:
		return Record{}, false, nil
	default:
		return Record{}, false, err
	}
}

// latestRecord 读取最新一条注册记录（非事务版本）。
func (s *Service) latestRecord() (Record, bool, error) {
	var record Record
	err := s.db.QueryRow(`SELECT `+validationRecordColumns+` FROM tbValidation ORDER BY Id DESC LIMIT 1`).Scan(
		&record.ID, &record.CompanyName, &record.MachineID, &record.SerialNumber,
		&record.AESKey, &record.RandomCode, &record.RegistrationDate,
		&record.ValidDuration, &record.RemainingTime, &record.Remarks,
	)
	switch err {
	case nil:
		return record, true, nil
	case sql.ErrNoRows:
		return Record{}, false, nil
	default:
		return Record{}, false, err
	}
}
