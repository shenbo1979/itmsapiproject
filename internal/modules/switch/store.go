package switchmod

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// 表结构与现网库完全一致（DDL 取自 /home/switch_rpc_api.db 的实际 schema，见 docs/06）。
// 说明：SQLite 不强制 VARCHAR 长度，这里保留原长度声明便于与老库 diffs 对齐。
const (
	venueTableDDL = `CREATE TABLE IF NOT EXISTS tbVenue (nId INTEGER PRIMARY KEY AUTOINCREMENT, szName VARCHAR(100) NOT NULL)`
	nameOnlyDDL   = `(nId INTEGER PRIMARY KEY AUTOINCREMENT, szName VARCHAR(100) NOT NULL)`
	modelTableDDL = `CREATE TABLE IF NOT EXISTS tbModel (
		nId INTEGER PRIMARY KEY AUTOINCREMENT,
		szName VARCHAR(100) NOT NULL,
		nVenueId INTEGER,
		nManufacturerId INTEGER,
		nPurposeId INTEGER,
		nProtocolId INTEGER,
		nEthPort INTEGER,
		szCreationTime VARCHAR(100),
		szRemarks TEXT
	)`
	deviceTableDDL = `CREATE TABLE IF NOT EXISTS tbDeviceInformation (
		nId INTEGER PRIMARY KEY AUTOINCREMENT,
		szName VARCHAR(100) NOT NULL,
		szDevMac VARCHAR(100),
		nModelId INTEGER,
		nPurposeId INTEGER,
		szManageIP VARCHAR(100),
		szGateway VARCHAR(100),
		szSubnetMask VARCHAR(100),
		szServerIP VARCHAR(100),
		nAutoEnable INTEGER,
		szDevPassword VARCHAR(64),
		szRemarks TEXT,
		szRpcHostip VARCHAR(100),
		nRpcPort INTEGER,
		szRpcUsername VARCHAR(512),
		szRpcPassword VARCHAR(512),
		nRpcType INTEGER DEFAULT 3
	)`
)

// Store 是 switch 模块的数据访问层：持有本模块独占的 SQLite 连接。
// 老实现用的包级全局 GetDB() 已被移除（见 docs/00 的事故结论），改由 Context 注入。
type Store struct {
	db     *sql.DB
	cipher *CredentialCipher
	// legacyImportDir 是老版本采集文件目录（迁移老数据用，默认 <DataDir>/collectionfile）。
	legacyImportDir string
	// deviceFileDir 是设备采集缓存目录（老数据迁移/读取用，默认 <DataDir>/devicefile）。
	deviceFileDir string
	// fileMu 串行化采集文件的读写（老实现为包级 switchCollectionFileMu）。
	fileMu sync.Mutex
}

// ensureDB 确认模块库已打开（老实现的 InitDB 检查在新模块里等价于此）。
func (s *Store) ensureDB() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	return nil
}

// collectionHostDirPath 返回某台设备的历史采集文件目录（老实现 switchCollectionHostDirPath）。
func (s *Store) collectionHostDirPath(hostip string) string {
	return filepath.Join(s.legacyImportDir, hostip)
}

// switchCollectionLegacyHostFilePath 返回历史采集文件路径（老实现同名函数）。
func (s *Store) switchCollectionLegacyHostFilePath(hostip string) string {
	return filepath.Join(s.legacyImportDir, hostip+".json")
}

// NewStore 包装一个已打开的模块库连接与凭据加解密器。
func NewStore(db *sql.DB, cipher *CredentialCipher) *Store {
	return &Store{db: db, cipher: cipher}
}

// DB 返回底层连接（供模块内其他组件，如采集任务使用）。
func (s *Store) DB() *sql.DB { return s.db }

// EnsureTables 建表（幂等）。
func (s *Store) EnsureTables() error {
	if s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	statements := []string{
		venueTableDDL,
		`CREATE TABLE IF NOT EXISTS tbManufacturer ` + nameOnlyDDL,
		`CREATE TABLE IF NOT EXISTS tbPurpose ` + nameOnlyDDL,
		`CREATE TABLE IF NOT EXISTS tbProtocol ` + nameOnlyDDL,
		modelTableDDL,
		deviceTableDDL,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("创建交换机表失败: %w", err)
		}
	}
	return nil
}

// listNameOnly 查询基础资料全表（按 nId 升序，与老实现一致）。
func (s *Store) listNameOnly(table string) ([]nameRecord, error) {
	rows, err := s.db.Query(fmt.Sprintf("SELECT nId, szName FROM %s ORDER BY nId", table))
	if err != nil {
		return nil, fmt.Errorf("查询失败: %v", err)
	}
	defer rows.Close()

	records := []nameRecord{}
	for rows.Next() {
		var record nameRecord
		if err := rows.Scan(&record.Id, &record.Name); err != nil {
			return nil, fmt.Errorf("扫描数据失败: %v", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代出错: %v", err)
	}
	return records, nil
}

// getNameOnly 按主键查询基础资料。
func (s *Store) getNameOnly(table string, id int64) (nameRecord, error) {
	var record nameRecord
	row := s.db.QueryRow(fmt.Sprintf("SELECT nId, szName FROM %s WHERE nId = ?", table), id)
	if err := row.Scan(&record.Id, &record.Name); err != nil {
		if err == sql.ErrNoRows {
			return record, fmt.Errorf("ID %d 不存在", id)
		}
		return record, fmt.Errorf("查询失败: %v", err)
	}
	return record, nil
}

// nameRecord 是「只有 nId/szName 两列」的基础资料行。
type nameRecord struct {
	Id   int64
	Name string
}

// ── 场景 ──

// CreateVenue 新增场景。
func (s *Store) CreateVenue(venue SwitchVenue) (int64, error) {
	return s.createNameOnly("tbVenue", venue.Name)
}

// GetAllVenues 查询场景列表。
func (s *Store) GetAllVenues() ([]SwitchVenue, error) {
	records, err := s.listNameOnly("tbVenue")
	if err != nil {
		return nil, err
	}
	venues := make([]SwitchVenue, 0, len(records))
	for _, record := range records {
		venues = append(venues, SwitchVenue{Id: record.Id, Name: record.Name})
	}
	return venues, nil
}

// GetVenueByID 查询单个场景。
func (s *Store) GetVenueByID(id int64) (SwitchVenue, error) {
	record, err := s.getNameOnly("tbVenue", id)
	if err != nil {
		return SwitchVenue{}, err
	}
	return SwitchVenue{Id: record.Id, Name: record.Name}, nil
}

// UpdateVenue 修改场景名称。
func (s *Store) UpdateVenue(id int64, newName string) (int64, error) {
	return s.updateNameOnly("tbVenue", id, newName)
}

// DeleteVenue 批量删除场景。
func (s *Store) DeleteVenue(ids []int) (int64, error) {
	return s.deleteByIDs("tbVenue", ids)
}

// ── 制造商 ──

// CreateManufacturer 新增制造商。
func (s *Store) CreateManufacturer(manufacturer SwitchManufacturer) (int64, error) {
	return s.createNameOnly("tbManufacturer", manufacturer.Name)
}

// GetAllManufacturers 查询制造商列表。
func (s *Store) GetAllManufacturers() ([]SwitchManufacturer, error) {
	records, err := s.listNameOnly("tbManufacturer")
	if err != nil {
		return nil, err
	}
	manufacturers := make([]SwitchManufacturer, 0, len(records))
	for _, record := range records {
		manufacturers = append(manufacturers, SwitchManufacturer{Id: record.Id, Name: record.Name})
	}
	return manufacturers, nil
}

// GetManufacturerByID 查询单个制造商。
func (s *Store) GetManufacturerByID(id int64) (SwitchManufacturer, error) {
	record, err := s.getNameOnly("tbManufacturer", id)
	if err != nil {
		return SwitchManufacturer{}, err
	}
	return SwitchManufacturer{Id: record.Id, Name: record.Name}, nil
}

// UpdateManufacturer 修改制造商名称。
func (s *Store) UpdateManufacturer(id int64, newName string) (int64, error) {
	return s.updateNameOnly("tbManufacturer", id, newName)
}

// DeleteManufacturer 批量删除制造商。
func (s *Store) DeleteManufacturer(ids []int) (int64, error) {
	return s.deleteByIDs("tbManufacturer", ids)
}

// ── 用途 ──

// CreatePurpose 新增用途。
func (s *Store) CreatePurpose(purpose SwitchPurpose) (int64, error) {
	return s.createNameOnly("tbPurpose", purpose.Name)
}

// GetAllPurposes 查询用途列表。
func (s *Store) GetAllPurposes() ([]SwitchPurpose, error) {
	records, err := s.listNameOnly("tbPurpose")
	if err != nil {
		return nil, err
	}
	purposes := make([]SwitchPurpose, 0, len(records))
	for _, record := range records {
		purposes = append(purposes, SwitchPurpose{Id: record.Id, Name: record.Name})
	}
	return purposes, nil
}

// GetPurposeByID 查询单个用途。
func (s *Store) GetPurposeByID(id int64) (SwitchPurpose, error) {
	record, err := s.getNameOnly("tbPurpose", id)
	if err != nil {
		return SwitchPurpose{}, err
	}
	return SwitchPurpose{Id: record.Id, Name: record.Name}, nil
}

// UpdatePurpose 修改用途名称。
func (s *Store) UpdatePurpose(id int64, newName string) (int64, error) {
	return s.updateNameOnly("tbPurpose", id, newName)
}

// DeletePurpose 批量删除用途。
func (s *Store) DeletePurpose(ids []int) (int64, error) {
	return s.deleteByIDs("tbPurpose", ids)
}

// ── 协议 ──

// CreateProtocol 新增协议。
func (s *Store) CreateProtocol(protocol SwitchProtocol) (int64, error) {
	return s.createNameOnly("tbProtocol", protocol.Name)
}

// GetAllProtocols 查询协议列表。
func (s *Store) GetAllProtocols() ([]SwitchProtocol, error) {
	records, err := s.listNameOnly("tbProtocol")
	if err != nil {
		return nil, err
	}
	protocols := make([]SwitchProtocol, 0, len(records))
	for _, record := range records {
		protocols = append(protocols, SwitchProtocol{Id: record.Id, Name: record.Name})
	}
	return protocols, nil
}

// GetProtocolByID 查询单个协议。
func (s *Store) GetProtocolByID(id int64) (SwitchProtocol, error) {
	record, err := s.getNameOnly("tbProtocol", id)
	if err != nil {
		return SwitchProtocol{}, err
	}
	return SwitchProtocol{Id: record.Id, Name: record.Name}, nil
}

// UpdateProtocol 修改协议名称。
func (s *Store) UpdateProtocol(id int64, newName string) (int64, error) {
	return s.updateNameOnly("tbProtocol", id, newName)
}

// DeleteProtocol 批量删除协议。
func (s *Store) DeleteProtocol(ids []int) (int64, error) {
	return s.deleteByIDs("tbProtocol", ids)
}

// createNameOnly 插入一行「只有名称」的基础资料，返回自增 ID。
func (s *Store) createNameOnly(table, name string) (int64, error) {
	result, err := s.db.Exec(fmt.Sprintf("INSERT INTO %s (szName) VALUES (?)", table), name)
	if err != nil {
		return 0, fmt.Errorf("插入失败: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取ID失败: %v", err)
	}
	return id, nil
}

// updateNameOnly 修改基础资料名称，返回受影响行数。
func (s *Store) updateNameOnly(table string, id int64, newName string) (int64, error) {
	result, err := s.db.Exec(fmt.Sprintf("UPDATE %s SET szName = ? WHERE nId = ?", table), newName, id)
	if err != nil {
		return 0, fmt.Errorf("更新失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	return rowsAffected, nil
}

// ── 型号 ──

// CreateModel 新增型号。
func (s *Store) CreateModel(m SwitchModel) (int64, error) {
	query := "INSERT INTO tbModel (szName,nVenueId,nPurposeId,nProtocolId,nEthPort,szCreationTime,szRemarks,nManufacturerId) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	result, err := s.db.Exec(query, m.Name, m.VenueId, m.PurposeId, m.ProtocolId, m.EthPort, m.CreationTime, m.Remarks, m.ManufacturerId)
	if err != nil {
		return 0, fmt.Errorf("插入失败: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取ID失败: %v", err)
	}
	return id, nil
}

const modelColumns = "nId,COALESCE(szName,''),COALESCE(nVenueId,0),COALESCE(nPurposeId,0),COALESCE(nProtocolId,0)," +
	"COALESCE(nEthPort,0),COALESCE(szCreationTime,''),COALESCE(szRemarks,''),COALESCE(nManufacturerId,0)"

// GetModelByID 查询单个型号。
func (s *Store) GetModelByID(id int64) (SwitchModel, error) {
	var m SwitchModel
	err := s.db.QueryRow("SELECT "+modelColumns+" FROM tbModel WHERE nId = ?", id).Scan(&m.Id, &m.Name,
		&m.VenueId, &m.PurposeId, &m.ProtocolId, &m.EthPort, &m.CreationTime, &m.Remarks, &m.ManufacturerId)
	if err != nil {
		if err == sql.ErrNoRows {
			return m, fmt.Errorf("型号 ID %d 不存在", id)
		}
		return m, fmt.Errorf("查询失败: %v", err)
	}
	return m, nil
}

// GetAllModels 查询型号列表。
func (s *Store) GetAllModels() ([]SwitchModel, error) {
	rows, err := s.db.Query("SELECT " + modelColumns + " FROM tbModel ORDER BY nId")
	if err != nil {
		return nil, fmt.Errorf("查询失败: %v", err)
	}
	defer rows.Close()

	models := []SwitchModel{}
	for rows.Next() {
		var m SwitchModel
		if err := rows.Scan(&m.Id, &m.Name, &m.VenueId, &m.PurposeId, &m.ProtocolId,
			&m.EthPort, &m.CreationTime, &m.Remarks, &m.ManufacturerId); err != nil {
			return nil, fmt.Errorf("扫描数据失败: %v", err)
		}
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代出错: %v", err)
	}
	return models, nil
}

// UpdateModel 修改型号。
func (s *Store) UpdateModel(id int64, newName string, venueId, purposeId, protocolId, ethPort int64, remarks string, manufacturerId int64) (int64, error) {
	query := "UPDATE tbModel SET szName = ?,nVenueId = ?,nPurposeId = ?,nProtocolId = ?,nEthPort = ?,szRemarks = ?,nManufacturerId = ? WHERE nId = ?"
	result, err := s.db.Exec(query, newName, venueId, purposeId, protocolId, ethPort, remarks, manufacturerId, id)
	if err != nil {
		return 0, fmt.Errorf("更新失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	return rowsAffected, nil
}

// DeleteModel 批量删除型号。
func (s *Store) DeleteModel(ids []int) (int64, error) {
	return s.deleteByIDs("tbModel", ids)
}

// ── 设备台账 ──

// CreateDeviceInformation 新增设备台账（账号密码以 AES-GCM 密文入库）。
func (s *Store) CreateDeviceInformation(di SwitchDeviceInformation) (int64, error) {
	encryptedUsername, encryptedPassword, err := s.encryptDeviceInformationCredentials(di.RpcUsername, di.RpcPassword)
	if err != nil {
		return 0, err
	}
	query := "INSERT INTO tbDeviceInformation (szName,szDevMac,nModelId,nPurposeId,szManageIP,szGateway,szSubnetMask,szServerIP,nAutoEnable,szDevPassword,szRemarks,szRpcHostip,nRpcPort,szRpcUsername,szRpcPassword,nRpcType) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	result, err := s.db.Exec(query, di.Name, di.DevMac, di.ModelId, di.PurposeId, di.ManageIP, di.Gateway,
		di.SubnetMask, di.ServerIP, di.AutoEnable, di.DevPassword, di.Remarks, di.RpcHostip, di.RpcPort,
		encryptedUsername, encryptedPassword, di.RpcType)
	if err != nil {
		return 0, fmt.Errorf("插入失败: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取ID失败: %v", err)
	}
	return id, nil
}

// deviceColumns 统一用 COALESCE 兜底：老库/老前端写入的历史行可能含 NULL，
// 老实现的 Scan 到 string 会直接报错（升级时会导致台账打不开），这里收敛为 0 / 空串，
// JSON 字段名与类型不变（仍然是 szName 这类字符串字段）。
const deviceColumns = "nId,COALESCE(szName,''),COALESCE(szDevMac,''),COALESCE(nModelId,0),COALESCE(nPurposeId,0)," +
	"COALESCE(szManageIP,''),COALESCE(szGateway,''),COALESCE(szSubnetMask,''),COALESCE(szServerIP,'')," +
	"COALESCE(nAutoEnable,0),COALESCE(szDevPassword,''),COALESCE(szRemarks,''),COALESCE(szRpcHostip,'')," +
	"COALESCE(nRpcPort,0),COALESCE(szRpcUsername,''),COALESCE(szRpcPassword,''),COALESCE(nRpcType,3)"

func (s *Store) scanDeviceInformation(scanner interface{ Scan(...any) error }) (SwitchDeviceInformation, error) {
	var di SwitchDeviceInformation
	if err := scanner.Scan(&di.Id, &di.Name, &di.DevMac, &di.ModelId, &di.PurposeId, &di.ManageIP,
		&di.Gateway, &di.SubnetMask, &di.ServerIP, &di.AutoEnable, &di.DevPassword, &di.Remarks,
		&di.RpcHostip, &di.RpcPort, &di.RpcUsername, &di.RpcPassword, &di.RpcType); err != nil {
		return di, err
	}
	s.decryptDeviceInformationCredentials(&di)
	return di, nil
}

// GetDeviceInformationByID 查询单个设备台账（账号密码解密后返回）。
func (s *Store) GetDeviceInformationByID(id int64) (SwitchDeviceInformation, error) {
	row := s.db.QueryRow("SELECT "+deviceColumns+" FROM tbDeviceInformation WHERE nId = ?", id)
	di, err := s.scanDeviceInformation(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return di, fmt.Errorf("设备信息 ID %d 不存在", id)
		}
		return di, fmt.Errorf("查询失败: %v", err)
	}
	return di, nil
}

// GetAllDeviceInformation 查询设备台账列表（账号密码解密后返回）。
func (s *Store) GetAllDeviceInformation() ([]SwitchDeviceInformation, error) {
	rows, err := s.db.Query("SELECT " + deviceColumns + " FROM tbDeviceInformation ORDER BY nId")
	if err != nil {
		return nil, fmt.Errorf("查询失败: %v", err)
	}
	defer rows.Close()

	devices := []SwitchDeviceInformation{}
	for rows.Next() {
		di, scanErr := s.scanDeviceInformation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("扫描数据失败: %v", scanErr)
		}
		devices = append(devices, di)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代出错: %v", err)
	}
	return devices, nil
}

// UpdateDeviceAutoEnableByManageIP 按管理 IP 同步设备在线状态（1 在线 / 0 离线）。
func (s *Store) UpdateDeviceAutoEnableByManageIP(manageIP string, autoEnable int64) (int64, error) {
	manageIP = strings.TrimSpace(manageIP)
	if manageIP == "" {
		return 0, fmt.Errorf("管理IP不能为空")
	}
	if autoEnable != 0 {
		autoEnable = 1
	}
	result, err := s.db.Exec("UPDATE tbDeviceInformation SET nAutoEnable = ? WHERE TRIM(szManageIP) = ?", autoEnable, manageIP)
	if err != nil {
		return 0, fmt.Errorf("更新设备在线状态失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	return rowsAffected, nil
}

// UpdateDeviceAutoEnableByID 按主键同步设备在线状态。
func (s *Store) UpdateDeviceAutoEnableByID(id, autoEnable int64) (int64, error) {
	if id <= 0 {
		return 0, fmt.Errorf("设备ID无效")
	}
	if autoEnable != 0 {
		autoEnable = 1
	}
	result, err := s.db.Exec("UPDATE tbDeviceInformation SET nAutoEnable = ? WHERE nId = ?", autoEnable, id)
	if err != nil {
		return 0, fmt.Errorf("更新设备在线状态失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	if rowsAffected == 0 {
		return 0, fmt.Errorf("设备信息 ID %d 不存在", id)
	}
	return rowsAffected, nil
}

// UpdateDeviceInformation 修改设备台账（账号密码重新加密入库）。
func (s *Store) UpdateDeviceInformation(id int64, newName, newDevMac, newManageIP, newGateway, newSubnetMask, newServerIP, newDevPassword string, newModelId, newPurposeId, newAutoEnable int64, remarks, newRpcHostip string, newRpcPort int64, newRpcUsername, newRpcPassword string, newRpcType int64) (int64, error) {
	encryptedUsername, encryptedPassword, err := s.encryptDeviceInformationCredentials(newRpcUsername, newRpcPassword)
	if err != nil {
		return 0, err
	}
	query := "UPDATE tbDeviceInformation SET szName = ?,szDevMac = ?,nModelId = ?,nPurposeId = ?,szManageIP = ?,szGateway = ?,szSubnetMask = ?,szServerIP = ?,nAutoEnable = ?,szDevPassword = ?,szRemarks = ?,szRpcHostip = ?,nRpcPort = ?,szRpcUsername = ?,szRpcPassword = ?,nRpcType = ? WHERE nId = ?"
	result, err := s.db.Exec(query, newName, newDevMac, newModelId, newPurposeId, newManageIP, newGateway,
		newSubnetMask, newServerIP, newAutoEnable, newDevPassword, remarks, newRpcHostip, newRpcPort,
		encryptedUsername, encryptedPassword, newRpcType, id)
	if err != nil {
		return 0, fmt.Errorf("更新失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	return rowsAffected, nil
}

// DeleteDeviceInformation 批量删除设备台账。
func (s *Store) DeleteDeviceInformation(ids []int) (int64, error) {
	return s.deleteByIDs("tbDeviceInformation", ids)
}
func (s *Store) deleteByIDs(table string, ids []int) (int64, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE nId IN (%s)", table, strings.Join(placeholders, ","))
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("删除失败: %v", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取影响行数失败: %v", err)
	}
	return rowsAffected, nil
}
