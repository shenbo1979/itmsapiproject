package olt

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// schemaVersion 是模块库结构版本（写入 tbSchemaVersion；docs/02 §2 必备表）。
//
// 1 → 2（2026-09-22）：新增文件数据对应的缓存表（tbOltDeviceSnapshot / tbOltDeviceBoard /
// tbOltDevicePonPort / tbOltDeviceGePort / tbOltOntCache / tbOltConfigCache / tbOltAutoRegConfig），
// 用于替代原先写在 /home/olt/<IP>/ 下的 currentconfiguration.json、slot_ont_file/*.txt、
// savedconfiguration.txt、olt_cur_cfg.txt、ontautomaticcfg.json（详见 docs/02 §4.4）。
const schemaVersion = 2

// Store 封装 olt 模块库（一模块一库：默认 <DataDir>/olt.db）。
//
// 老实现用包级 getDB()+sqlx（QueryRows/Exec）访问 8306 中间件；新实现改为模块独占的 SQLite，
// 并用显式列扫描替代 sqlx（不引入新依赖）。**表名按 docs/02 §2 规范为 tbOlt*，列名与老库逐字一致**。
type Store struct {
	db *sql.DB
}

// NewStore 创建模块库访问器。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// DB 暴露底层连接（供一次性语句/迁移使用）。
func (s *Store) DB() *sql.DB { return s.db }

// schemaDDL 是模块库建表语句（IF NOT EXISTS，可反复执行）；类型与老库 DDL 保持一致。
var schemaDDL = []string{
	`CREATE TABLE IF NOT EXISTS "tbOltUser" ("Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "Username" TEXT(50) NOT NULL,
		"Password" TEXT(50) NOT NULL, "Port" INTEGER NOT NULL, "Type" INTEGER NOT NULL, "Comment" TEXT(50),
		"IP" TEXT(50) NOT NULL, "Automatic" INTEGER, "ScanInterval" INTEGER, "TemplateId" INTEGER)`,
	`CREATE TABLE IF NOT EXISTS "tbOltRegProfile" ("Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "Name" TEXT NOT NULL,
		"Host" TEXT NOT NULL, "Authtype" TEXT, "Pontype" TEXT, "Lineprofileid" TEXT, "Srvprofileid" TEXT,
		"Ontdesc" TEXT, "Onutype" TEXT, "Servicevlan" TEXT, "Tr069profile" TEXT, "Iptvvlan" TEXT,
		"Unknownvaln" TEXT, "Tr069order" TEXT, "Desc" TEXT, "Active" TEXT)`,
	`CREATE TABLE IF NOT EXISTS "tbOltBoard" ("Nid" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "Userid" INTEGER NOT NULL,
		"Boardid" INTEGER, "Status" TEXT, "Boardname" TEXT, "Boardtype" TEXT, "Boardport" TEXT)`,
	`CREATE TABLE IF NOT EXISTS "tbOltOnt" ("Nid" INTEGER PRIMARY KEY AUTOINCREMENT, "UserId" TEXT, "FSP" TEXT,
		"ONTID" TEXT, "Runstate" TEXT, "Authtype" TEXT, "SN" TEXT, "Managementmode" TEXT, "addressmask" TEXT,
		"Description" TEXT, "LineprofileID" TEXT, "ServiceprofileID" TEXT, "Boardid" TEXT)`,
	`CREATE TABLE IF NOT EXISTS "tbOltOperationLog" ("nID" INTEGER PRIMARY KEY AUTOINCREMENT,
		"szOltName" VARCHAR(256) DEFAULT '', "szMenu" VARCHAR(128) DEFAULT '', "szAct" VARCHAR(128) DEFAULT '',
		"szOperationIP" VARCHAR(128) DEFAULT '', "szText" TEXT DEFAULT '', "dtCreatTime" datetime DEFAULT '',
		"szRemark" TEXT DEFAULT '')`,
	`CREATE TABLE IF NOT EXISTS "tbSchemaVersion" ("module" TEXT PRIMARY KEY, "nVersion" INTEGER NOT NULL,
		"dtAppliedAt" TEXT NOT NULL)`,
}

// oltLogColumns 是 tbOltOperationLog 的列：同时作为「排序字段白名单」，避免把客户端输入直接拼进 ORDER BY。
var oltLogColumns = []string{"nID", "szOltName", "szMenu", "szAct", "szOperationIP", "szText", "dtCreatTime", "szRemark"}

// EnsureTables 建表并写入版本记录（幂等）。
func (s *Store) EnsureTables() error {
	// schemaDDL 是老库迁移表；cacheSchemaDDL 是文件数据对应的缓存表（schemaVersion 2 起）。
	for _, ddl := range append(append([]string{}, schemaDDL...), cacheSchemaDDL...) {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("建表失败: %w", err)
		}
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO "tbSchemaVersion" ("module","nVersion","dtAppliedAt") VALUES (?,?,datetime('now','localtime'))`,
		ModuleID, schemaVersion,
	)
	if err != nil {
		return fmt.Errorf("写入版本记录失败: %w", err)
	}
	return nil
}

// QueryOltLogs 按老实现语义查询操作日志：时间范围（可空，`x TO y` 形式）+ 排序。
// 与老实现的差异：时间值用**绑定参数**（老实现是字符串拼接），排序字段走白名单。
//
// 注意 `CAST(... AS TEXT)`：老库该列的声明类型是 `datetime`，go-sqlite3 会把文本值转成 time.Time，
// 扫进 string 时会变成 RFC3339（"2026-08-17T11:29:05Z"），与老前端显示的 "2026-08-17 11:29:05" 不一致
// （见 handler_log_test.go 的回归断言）。强制按 TEXT 取出，保证与老库存储形态逐字一致。
func (s *Store) QueryOltLogs(timeFrom, timeTo, sortField, sortDir string) ([]OltOperationLog, error) {
	wheres, args := oltLogTimeRange(timeFrom, timeTo)
	sqlText := `SELECT "nID","szOltName","szMenu","szAct","szOperationIP","szText",
		CAST("dtCreatTime" AS TEXT),"szRemark" FROM "tbOltOperationLog"`
	if wheres != "" {
		sqlText += " " + wheres
	}
	sqlText += " ORDER BY " + normalizeOltLogSort(sortField) + " " + normalizeOltLogDir(sortDir)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OltOperationLog
	for rows.Next() {
		var item OltOperationLog
		if err := rows.Scan(&item.Id, &item.OltName, &item.Menu, &item.Act, &item.OperationIP,
			&item.Text, &item.CreatTime, &item.Remark); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// InsertOltLog 写入一条操作日志（老实现用 INSERT 语句；写操作类接口会用到）。
func (s *Store) InsertOltLog(item OltOperationLog) error {
	_, err := s.db.Exec(`INSERT INTO "tbOltOperationLog"
		("szOltName","szMenu","szAct","szOperationIP","szText","dtCreatTime","szRemark") VALUES (?,?,?,?,?,?,?)`,
		item.OltName, item.Menu, item.Act, item.OperationIP, item.Text, item.CreatTime, item.Remark)
	return err
}

// DeleteOltLogs 删除指定 id 的日志；ids 为空表示清空全部（对应老实现的 Act=2）。
func (s *Store) DeleteOltLogs(ids []int) error {
	if len(ids) == 0 {
		_, err := s.db.Exec(`DELETE FROM "tbOltOperationLog"`)
		return err
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	_, err := s.db.Exec(`DELETE FROM "tbOltOperationLog" WHERE "nID" IN (`+strings.Join(placeholders, ",")+`)`, args...)
	return err
}

// oltLogTimeRange 把 `x TO y` 形式的旧参数转成 where + 绑定参数（老实现：>= x AND <= y；只有一个值时 >= x）。
func oltLogTimeRange(timeFrom, timeTo string) (string, []any) {
	timeFrom, timeTo = strings.TrimSpace(timeFrom), strings.TrimSpace(timeTo)
	if timeFrom == "" && timeTo == "" {
		return "", nil
	}
	if timeTo == "" {
		return `WHERE "dtCreatTime" >= ?`, []any{timeFrom}
	}
	return `WHERE "dtCreatTime" >= ? AND "dtCreatTime" <= ?`, []any{timeFrom, timeTo}
}

// normalizeOltLogSort 只接受表内列名（老实现直接拼接客户端传入的字段名）。
func normalizeOltLogSort(field string) string {
	field = strings.TrimSpace(field)
	for _, column := range oltLogColumns {
		if strings.EqualFold(field, column) {
			return column
		}
	}
	return "nID"
}

// normalizeOltLogDir 只接受 ASC/DESC（大小写不敏感），其余回落 DESC（与老实现默认值一致）。
func normalizeOltLogDir(dir string) string {
	if strings.EqualFold(strings.TrimSpace(dir), "ASC") {
		return "ASC"
	}
	return "DESC"
}

// parseOltLogIDs 解析 `Ids`（老实现直接拼进 IN 子句；这里按整数解析，非法项忽略）。
func parseOltLogIDs(raw string) []int {
	ids := make([]int, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		ids = append(ids, value)
	}
	return ids
}

// ── OLT 连接用户（老表 oltuser → tbOltUser）──

// oltUserColumns 是 handleConnectOltUser(Act=0) 查询的列。
//
// 老实现只取 7 列（Nid/Username/Password/Port/Type/Comment/IP），因此 Automatic/ScanInterval/TemplateId
// 在响应里恒为 0 —— 老前端无法据此显示"当前连接的是哪台"。新实现补上这 3 列（只增字段、不删不改既有字段，
// 老前端忽略多余字段不受影响），前端据此显示「已连接」（见 web/src/modules/olt.js 与 docs/09 §5.2）。
//
// 注意：这三列在老库里可能是 NULL（历史列，老实现从不写它们），直接 Scan 到 int 会报
// "converting NULL to int"；用 COALESCE 兜成 0，保证老新两代数据都能读。
const oltUserColumns = `"Nid","Username","Password","Port","Type","Comment","IP",` +
	`COALESCE("Automatic",0),COALESCE("ScanInterval",0),COALESCE("TemplateId",0)`

// QueryOltUsers 返回 OLT 连接用户列表（老实现：`ORDER BY Automatic DESC`）。
func (s *Store) QueryOltUsers() ([]OltUser, error) {
	rows, err := s.db.Query(`SELECT ` + oltUserColumns + ` FROM "tbOltUser" ORDER BY "Automatic" DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OltUser
	for rows.Next() {
		var item OltUser
		if err := rows.Scan(&item.Id, &item.Username, &item.Password, &item.Port, &item.Type,
			&item.Comment, &item.IP, &item.Automatic, &item.ScanInterval, &item.TemplateId); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// FindOltUserByIP 按 IP（可选用户名收紧）定位一条 OLT 连接用户，供"connData 缺凭据时回落库"使用。
func (s *Store) FindOltUserByIP(ip, username string) (OltUser, error) {
	query := `SELECT ` + oltUserColumns + ` FROM "tbOltUser" WHERE "IP"=?`
	args := []any{strings.TrimSpace(ip)}
	if strings.TrimSpace(username) != "" {
		query += ` AND "Username"=?`
		args = append(args, strings.TrimSpace(username))
	}
	query += ` ORDER BY "Automatic" DESC LIMIT 1`

	var item OltUser
	err := s.db.QueryRow(query, args...).Scan(&item.Id, &item.Username, &item.Password, &item.Port,
		&item.Type, &item.Comment, &item.IP, &item.Automatic, &item.ScanInterval, &item.TemplateId)
	if err != nil {
		return OltUser{}, err
	}
	return item, nil
}

// FindOltUserByID 按主键定位一条 OLT 连接用户（编辑场景：判断口令是否改动、按需跳过强度校验）。
func (s *Store) FindOltUserByID(id int) (OltUser, error) {
	query := `SELECT ` + oltUserColumns + ` FROM "tbOltUser" WHERE "Nid"=?`
	var item OltUser
	err := s.db.QueryRow(query, id).Scan(&item.Id, &item.Username, &item.Password, &item.Port,
		&item.Type, &item.Comment, &item.IP, &item.Automatic, &item.ScanInterval, &item.TemplateId)
	if err != nil {
		return OltUser{}, err
	}
	return item, nil
}

// SetOltUserAutomatic 标记"当前登录用户"：仅把该用户置 1、其余置 0（老实现 handleOltConnect Act=1）。
func (s *Store) SetOltUserAutomatic(username string) error {
	username = strings.TrimSpace(username)
	if _, err := s.db.Exec(`UPDATE "tbOltUser" SET "Automatic"=1 WHERE "Username"=?`, username); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE "tbOltUser" SET "Automatic"=0 WHERE "Username"<>?`, username)
	return err
}

// InsertOltUser 新增 OLT 连接用户（老实现 handleConnectOltUser Act=1 的 INSERT）。
// 与老实现的差异：用绑定参数（老实现用 fmt.Sprintf 拼 SQL），列名与取值语义不变。
func (s *Store) InsertOltUser(user OltUser, password string, port int) error {
	_, err := s.db.Exec(`INSERT INTO "tbOltUser" ("Username","Password","Port","Type","IP","Comment")
		VALUES (?,?,?,?,?,?)`,
		strings.TrimSpace(user.Username), password, port, user.Type,
		strings.TrimSpace(user.IP), strings.TrimSpace(user.Comment))
	return err
}

// UpdateOltUser 修改 OLT 连接用户（老实现 Act=2 的 UPDATE ... WHERE Nid）。
func (s *Store) UpdateOltUser(user OltUser, password string, port int) error {
	_, err := s.db.Exec(`UPDATE "tbOltUser" SET "Username"=?,"Password"=?,"Port"=?,"Type"=?,"IP"=?,"Comment"=?
		WHERE "Nid"=?`,
		strings.TrimSpace(user.Username), password, port, user.Type,
		strings.TrimSpace(user.IP), strings.TrimSpace(user.Comment), user.Id)
	return err
}

// DeleteOltUser 删除单个 OLT 连接用户（老实现 Act=3 的 DELETE ... WHERE Nid）。
func (s *Store) DeleteOltUser(id int) error {
	return s.DeleteOltUsers([]int{id})
}

// DeleteOltUsers 批量删除 OLT 连接用户（单条即 len(ids)==1）；一条 SQL + 绑定参数，
// 供前端台账的「删除 (n)」批处理使用。
func (s *Store) DeleteOltUsers(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	_, err := s.db.Exec(`DELETE FROM "tbOltUser" WHERE "Nid" IN (`+strings.Join(placeholders, ",")+`)`, args...)
	return err
}
