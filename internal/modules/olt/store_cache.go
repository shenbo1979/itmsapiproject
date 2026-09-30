package olt

import (
	"database/sql"
	"fmt"
	"strings"
)

// 文件数据对应的表结构（一模块一库 olt.db；表名按 docs/02 §2 规范 tbOlt*，列名按文件实际字段）。
var cacheSchemaDDL = []string{
	// 设备快照：原 currentconfiguration.json 的 17 个标量字段 + 来源文件信息。
	`CREATE TABLE IF NOT EXISTS "tbOltDeviceSnapshot" (
		"HostIP" TEXT PRIMARY KEY,
		"ConnectTime" TEXT DEFAULT '', "OLTRunTime" TEXT DEFAULT '', "RebootTime" TEXT DEFAULT '',
		"Version" TEXT DEFAULT '', "Patch" TEXT DEFAULT '', "Product" TEXT DEFAULT '',
		"Frame" TEXT DEFAULT '', "FrameType" TEXT DEFAULT '',
		"OntOnlineCount" INTEGER DEFAULT 0, "FrameTotal" INTEGER DEFAULT 0, "FrameCount" INTEGER DEFAULT 0,
		"DefaultRoute" TEXT DEFAULT '', "SlotidRefreshTime" TEXT DEFAULT '', "FrameDesc" TEXT DEFAULT '',
		"OntOnlines" INTEGER DEFAULT 0, "OntTotal" INTEGER DEFAULT 0,
		"SourceFile" TEXT DEFAULT '', "SourceMTime" TEXT DEFAULT '', "UpdatedAt" TEXT NOT NULL)`,
	// 板卡：原 OltBoardInfo（嵌套在快照里）。
	`CREATE TABLE IF NOT EXISTS "tbOltDeviceBoard" (
		"HostIP" TEXT NOT NULL, "BoardId" TEXT NOT NULL, "Ordinal" INTEGER DEFAULT 0,
		"Name" TEXT DEFAULT '', "Status" TEXT DEFAULT '', "Online" INTEGER DEFAULT 0, "Type" TEXT DEFAULT '',
		"Desc" TEXT DEFAULT '', "Temperature" TEXT DEFAULT '', "PortNum" INTEGER DEFAULT 0, "PonType" TEXT DEFAULT '',
		PRIMARY KEY ("HostIP","BoardId"))`,
	// PON 口：原 OltBoardInfo.PonInfos。
	`CREATE TABLE IF NOT EXISTS "tbOltDevicePonPort" (
		"HostIP" TEXT NOT NULL, "FSP" TEXT NOT NULL, "BoardId" TEXT DEFAULT '', "PortId" TEXT DEFAULT '',
		"Type" TEXT DEFAULT '', "OntTotal" INTEGER DEFAULT 0, "OntOnline" INTEGER DEFAULT 0, "PortStatus" TEXT DEFAULT '',
		PRIMARY KEY ("HostIP","FSP"))`,
	// GE 口：原 OltBoardInfo.GeInfos。
	`CREATE TABLE IF NOT EXISTS "tbOltDeviceGePort" (
		"HostIP" TEXT NOT NULL, "FSP" TEXT NOT NULL, "BoardId" TEXT DEFAULT '', "PortId" TEXT DEFAULT '',
		"Type" TEXT DEFAULT '', "NativeVLAN" TEXT DEFAULT '', "Speed" TEXT DEFAULT '', "Duplex" TEXT DEFAULT '', "Link" TEXT DEFAULT '',
		PRIMARY KEY ("HostIP","FSP"))`,
	// ONT 缓存：原 slot_ont_file/<槽位>.txt 每行的 17 个 JSON 字段（老 JSON 的 IP → OntIP 列）。
	`CREATE TABLE IF NOT EXISTS "tbOltOntCache" (
		"HostIP" TEXT NOT NULL, "SlotID" TEXT NOT NULL, "FSP" TEXT NOT NULL, "OntId" TEXT NOT NULL,
		"PonType" TEXT DEFAULT '', "Code" TEXT DEFAULT '', "SerialNumber" TEXT DEFAULT '', "ControlFlag" TEXT DEFAULT '',
		"RunState" TEXT DEFAULT '', "ConfigState" TEXT DEFAULT '', "MatchState" TEXT DEFAULT '', "ProtectSide" TEXT DEFAULT '',
		"AuthType" TEXT DEFAULT '', "Protocol" TEXT DEFAULT '', "LineProfile" TEXT DEFAULT '', "SrvProfile" TEXT DEFAULT '',
		"Desc" TEXT DEFAULT '', "Mac" TEXT DEFAULT '', "OntIP" TEXT DEFAULT '',
		"SourceFile" TEXT DEFAULT '', "SourceMTime" TEXT DEFAULT '', "UpdatedAt" TEXT NOT NULL,
		PRIMARY KEY ("HostIP","FSP","OntId"))`,
	`CREATE INDEX IF NOT EXISTS "idxOltOntCacheSlot" ON "tbOltOntCache" ("HostIP","SlotID")`,
	// 文本配置缓存：savedconfiguration.txt / olt_cur_cfg.txt（整篇文本）。
	`CREATE TABLE IF NOT EXISTS "tbOltConfigCache" (
		"HostIP" TEXT NOT NULL, "Kind" TEXT NOT NULL, "Content" TEXT DEFAULT '',
		"ByteSize" INTEGER DEFAULT 0, "SourceFile" TEXT DEFAULT '', "SourceMTime" TEXT DEFAULT '', "UpdatedAt" TEXT NOT NULL,
		PRIMARY KEY ("HostIP","Kind"))`,
	// 自动注册配置：原 ontautomaticcfg.json（口令列加密存放）。
	`CREATE TABLE IF NOT EXISTS "tbOltAutoRegConfig" (
		"HostIP" TEXT PRIMARY KEY, "Type" INTEGER DEFAULT 0, "Port" TEXT DEFAULT '',
		"Username" TEXT DEFAULT '', "Password" TEXT DEFAULT '',
		"Registration" INTEGER DEFAULT 0, "Relocation" INTEGER DEFAULT 0, "TemplateId" INTEGER DEFAULT 0,
		"ScanInterval" INTEGER DEFAULT 30,
		"SourceFile" TEXT DEFAULT '', "SourceMTime" TEXT DEFAULT '', "UpdatedAt" TEXT NOT NULL)`,
}

// SaveDeviceSnapshot 把一份设备快照（含板卡/端口）写入表：同一事务内先清后写。
// 文件形态本来就是"整份覆盖"，表也按整份覆盖，避免残留已经拔掉的板卡/端口。
func (s *Store) SaveDeviceSnapshot(hostIP string, cfg OltCurrentConfig, sourceFile, sourceMTime, updatedAt string) error {
	hostIP = strings.TrimSpace(hostIP)
	if hostIP == "" {
		return fmt.Errorf("OLT IP is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// Commit 成功后 Rollback 是空操作（core.OpenSQLite 的连接池只有一条连接，出错路径必须回滚）。
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO "tbOltDeviceSnapshot"
		("HostIP","ConnectTime","OLTRunTime","RebootTime","Version","Patch","Product","Frame","FrameType",
		 "OntOnlineCount","FrameTotal","FrameCount","DefaultRoute","SlotidRefreshTime","FrameDesc",
		 "OntOnlines","OntTotal","SourceFile","SourceMTime","UpdatedAt")
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT("HostIP") DO UPDATE SET
		 "ConnectTime"=excluded."ConnectTime","OLTRunTime"=excluded."OLTRunTime","RebootTime"=excluded."RebootTime",
		 "Version"=excluded."Version","Patch"=excluded."Patch","Product"=excluded."Product",
		 "Frame"=excluded."Frame","FrameType"=excluded."FrameType",
		 "OntOnlineCount"=excluded."OntOnlineCount","FrameTotal"=excluded."FrameTotal","FrameCount"=excluded."FrameCount",
		 "DefaultRoute"=excluded."DefaultRoute","SlotidRefreshTime"=excluded."SlotidRefreshTime","FrameDesc"=excluded."FrameDesc",
		 "OntOnlines"=excluded."OntOnlines","OntTotal"=excluded."OntTotal",
		 "SourceFile"=excluded."SourceFile","SourceMTime"=excluded."SourceMTime","UpdatedAt"=excluded."UpdatedAt"`,
		hostIP, cfg.ConnectTime, cfg.OLTRunTime, cfg.RebootTime, cfg.Version, cfg.Patch, cfg.Product,
		cfg.Frame, cfg.FrameType, cfg.OntOnlineCount, cfg.FrameTotal, cfg.FrameCount, cfg.DefaultRoute,
		cfg.SlotidRefreshTime, cfg.FrameDesc, cfg.OntOnlines, cfg.OntTotal, sourceFile, sourceMTime, updatedAt); err != nil {
		return err
	}

	for _, table := range []string{"tbOltDeviceBoard", "tbOltDevicePonPort", "tbOltDeviceGePort"} {
		if _, err := tx.Exec(`DELETE FROM "`+table+`" WHERE "HostIP"=?`, hostIP); err != nil {
			return err
		}
	}
	for index, board := range cfg.OltBoardInfo {
		online := 0
		if board.Online {
			online = 1
		}
		if _, err := tx.Exec(`INSERT INTO "tbOltDeviceBoard"
			("HostIP","BoardId","Ordinal","Name","Status","Online","Type","Desc","Temperature","PortNum","PonType")
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			hostIP, strings.TrimSpace(board.Id), index, board.Name, board.Status, online, board.Type,
			board.Desc, board.Temperature, board.PortNum, board.PonType); err != nil {
			return err
		}
		for _, port := range board.PonInfos {
			if _, err := tx.Exec(`INSERT INTO "tbOltDevicePonPort"
				("HostIP","FSP","BoardId","PortId","Type","OntTotal","OntOnline","PortStatus")
				VALUES (?,?,?,?,?,?,?,?)`,
				hostIP, strings.TrimSpace(port.FSP), strings.TrimSpace(board.Id), port.Id, port.Type,
				port.OntTotal, port.OntOnline, port.PortStatus); err != nil {
				return err
			}
		}
		for _, port := range board.GeInfos {
			if _, err := tx.Exec(`INSERT INTO "tbOltDeviceGePort"
				("HostIP","FSP","BoardId","PortId","Type","NativeVLAN","Speed","Duplex","Link")
				VALUES (?,?,?,?,?,?,?,?,?)`,
				hostIP, strings.TrimSpace(port.FSP), strings.TrimSpace(board.Id), port.Id, port.Type,
				port.NativeVLAN, port.Speed, port.Duplex, port.Link); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// LoadDeviceSnapshot 读回一份设备快照（快照 + 板卡 + 端口重新组装成 OltCurrentConfig）。
// 第二个返回值表示该设备是否有快照行。
func (s *Store) LoadDeviceSnapshot(hostIP string) (OltCurrentConfig, bool, error) {
	var cfg OltCurrentConfig
	var sourceFile, sourceMTime, updatedAt string
	err := s.db.QueryRow(`SELECT "ConnectTime","OLTRunTime","RebootTime","Version","Patch","Product",
			"Frame","FrameType","OntOnlineCount","FrameTotal","FrameCount","DefaultRoute","SlotidRefreshTime",
			"FrameDesc","OntOnlines","OntTotal","SourceFile","SourceMTime","UpdatedAt"
		FROM "tbOltDeviceSnapshot" WHERE "HostIP"=?`, strings.TrimSpace(hostIP)).Scan(
		&cfg.ConnectTime, &cfg.OLTRunTime, &cfg.RebootTime, &cfg.Version, &cfg.Patch, &cfg.Product,
		&cfg.Frame, &cfg.FrameType, &cfg.OntOnlineCount, &cfg.FrameTotal, &cfg.FrameCount, &cfg.DefaultRoute,
		&cfg.SlotidRefreshTime, &cfg.FrameDesc, &cfg.OntOnlines, &cfg.OntTotal,
		&sourceFile, &sourceMTime, &updatedAt)
	if err == sql.ErrNoRows {
		return OltCurrentConfig{}, false, nil
	}
	if err != nil {
		return OltCurrentConfig{}, false, err
	}

	boardRows, err := s.db.Query(`SELECT "BoardId","Name","Status","Online","Type","Desc","Temperature","PortNum","PonType"
		FROM "tbOltDeviceBoard" WHERE "HostIP"=? ORDER BY "Ordinal"`, strings.TrimSpace(hostIP))
	if err != nil {
		return OltCurrentConfig{}, false, err
	}
	defer boardRows.Close()
	for boardRows.Next() {
		var board OltBoardInfo
		var online int
		if err := boardRows.Scan(&board.Id, &board.Name, &board.Status, &online, &board.Type, &board.Desc,
			&board.Temperature, &board.PortNum, &board.PonType); err != nil {
			return OltCurrentConfig{}, false, err
		}
		board.Online = online != 0
		cfg.OltBoardInfo = append(cfg.OltBoardInfo, board)
	}
	if err := boardRows.Err(); err != nil {
		return OltCurrentConfig{}, false, err
	}

	ponByBoard := map[string][]PonInfos{}
	ponRows, err := s.db.Query(`SELECT "BoardId","PortId","FSP","Type","OntTotal","OntOnline","PortStatus"
		FROM "tbOltDevicePonPort" WHERE "HostIP"=? ORDER BY "FSP"`, strings.TrimSpace(hostIP))
	if err != nil {
		return OltCurrentConfig{}, false, err
	}
	defer ponRows.Close()
	for ponRows.Next() {
		var boardID string
		var port PonInfos
		if err := ponRows.Scan(&boardID, &port.Id, &port.FSP, &port.Type, &port.OntTotal, &port.OntOnline, &port.PortStatus); err != nil {
			return OltCurrentConfig{}, false, err
		}
		ponByBoard[boardID] = append(ponByBoard[boardID], port)
	}
	if err := ponRows.Err(); err != nil {
		return OltCurrentConfig{}, false, err
	}

	geByBoard := map[string][]GeInfos{}
	geRows, err := s.db.Query(`SELECT "BoardId","PortId","FSP","Type","NativeVLAN","Speed","Duplex","Link"
		FROM "tbOltDeviceGePort" WHERE "HostIP"=? ORDER BY "FSP"`, strings.TrimSpace(hostIP))
	if err != nil {
		return OltCurrentConfig{}, false, err
	}
	defer geRows.Close()
	for geRows.Next() {
		var boardID string
		var port GeInfos
		if err := geRows.Scan(&boardID, &port.Id, &port.FSP, &port.Type, &port.NativeVLAN, &port.Speed,
			&port.Duplex, &port.Link); err != nil {
			return OltCurrentConfig{}, false, err
		}
		geByBoard[boardID] = append(geByBoard[boardID], port)
	}
	if err := geRows.Err(); err != nil {
		return OltCurrentConfig{}, false, err
	}

	for index := range cfg.OltBoardInfo {
		boardID := cfg.OltBoardInfo[index].Id
		cfg.OltBoardInfo[index].PonInfos = ponByBoard[boardID]
		cfg.OltBoardInfo[index].GeInfos = geByBoard[boardID]
	}
	return cfg, true, nil
}

// ── ONT 缓存（原 slot_ont_file/<槽位>.txt，每行一个 ONT JSON）──

// SaveOntCache 覆盖某个槽位的 ONT 缓存（同一事务先清后写，与文件"整份覆盖"语义一致）。
func (s *Store) SaveOntCache(hostIP, slotID string, items []OntListResponse, sourceFile, sourceMTime, updatedAt string) error {
	hostIP, slotID = strings.TrimSpace(hostIP), strings.TrimSpace(slotID)
	if hostIP == "" || slotID == "" {
		return fmt.Errorf("OLT IP and slot id are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM "tbOltOntCache" WHERE "HostIP"=? AND "SlotID"=?`, hostIP, slotID); err != nil {
		return err
	}
	for _, item := range items {
		row := ontCacheRow(hostIP, slotID, sourceFile, sourceMTime, updatedAt, item)
		// OR REPLACE：同一 ONT（HostIP+FSP+OntId）若在别的槽位文件里也出现过（设备换过槽位），
		// 以最后写入的一份为准，避免整次导入因主键冲突而失败。
		if _, err := tx.Exec(`INSERT OR REPLACE INTO "tbOltOntCache"
			("HostIP","SlotID","FSP","OntId","PonType","Code","SerialNumber","ControlFlag","RunState","ConfigState",
			 "MatchState","ProtectSide","AuthType","Protocol","LineProfile","SrvProfile","Desc","Mac","OntIP",
			 "SourceFile","SourceMTime","UpdatedAt")
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			row.HostIP, row.SlotID, row.FSP, row.OntId, row.PonType, row.Code, row.SerialNumber, row.ControlFlag,
			row.RunState, row.ConfigState, row.MatchState, row.ProtectSide, row.AuthType, row.Protocol,
			row.LineProfile, row.SrvProfile, row.Desc, row.Mac, row.OntIP,
			row.SourceFile, row.SourceMTime, row.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListOntCache 读 ONT 缓存：slotID 为空表示该设备全部槽位（按 FSP、OntId 排序，便于分页/筛选）。
func (s *Store) ListOntCache(hostIP, slotID string) ([]OntListResponse, error) {
	query := `SELECT "SlotID","FSP","OntId","PonType","Code","SerialNumber","ControlFlag","RunState","ConfigState",
			"MatchState","ProtectSide","AuthType","Protocol","LineProfile","SrvProfile","Desc","Mac","OntIP"
		FROM "tbOltOntCache" WHERE "HostIP"=?`
	args := []any{strings.TrimSpace(hostIP)}
	if slot := strings.TrimSpace(slotID); slot != "" {
		query += ` AND "SlotID"=?`
		args = append(args, slot)
	}
	query += ` ORDER BY "SlotID","FSP",CAST("OntId" AS INTEGER),"OntId"`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OntListResponse
	for rows.Next() {
		var slotID string
		var row OltOntCacheRow
		if err := rows.Scan(&slotID, &row.FSP, &row.OntId, &row.PonType, &row.Code, &row.SerialNumber,
			&row.ControlFlag, &row.RunState, &row.ConfigState, &row.MatchState, &row.ProtectSide, &row.AuthType,
			&row.Protocol, &row.LineProfile, &row.SrvProfile, &row.Desc, &row.Mac, &row.OntIP); err != nil {
			return nil, err
		}
		items = append(items, ontItemFromRow(row))
	}
	return items, rows.Err()
}

// ListOntCacheSlots 返回某设备有 ONT 缓存的槽位号（按槽位号升序），供前端槽位下拉使用。
func (s *Store) ListOntCacheSlots(hostIP string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT "SlotID" FROM "tbOltOntCache" WHERE "HostIP"=?
		ORDER BY CAST("SlotID" AS INTEGER), "SlotID"`, strings.TrimSpace(hostIP))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	slots := make([]string, 0, 8)
	for rows.Next() {
		var slot string
		if err := rows.Scan(&slot); err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, rows.Err()
}

// ontCacheRow 把老 JSON 结构（OntListResponse）映射为表行（老 JSON 的 IP → OntIP 列）。
func ontCacheRow(hostIP, slotID, sourceFile, sourceMTime, updatedAt string, item OntListResponse) OltOntCacheRow {
	return OltOntCacheRow{
		HostIP: hostIP, SlotID: slotID, FSP: item.FSP, OntId: item.OntId, PonType: item.PonType, Code: item.Code,
		SerialNumber: item.SerialNumber, ControlFlag: item.ControlFlag, RunState: item.RunState,
		ConfigState: item.ConfigState, MatchState: item.MatchState, ProtectSide: item.ProtectSide,
		AuthType: item.AuthType, Protocol: item.Protocol, LineProfile: item.LineProfile, SrvProfile: item.SrvProfile,
		Desc: item.Desc, Mac: item.Mac, OntIP: item.IP,
		SourceFile: sourceFile, SourceMTime: sourceMTime, UpdatedAt: updatedAt,
	}
}

// ontItemFromRow 把表行还原为老 JSON 结构（接口响应字段名因此保持不变）。
func ontItemFromRow(row OltOntCacheRow) OntListResponse {
	return OntListResponse{
		PonType: row.PonType, FSP: row.FSP, OntId: row.OntId, Code: row.Code, SerialNumber: row.SerialNumber,
		ControlFlag: row.ControlFlag, RunState: row.RunState, ConfigState: row.ConfigState,
		MatchState: row.MatchState, ProtectSide: row.ProtectSide, AuthType: row.AuthType, Protocol: row.Protocol,
		LineProfile: row.LineProfile, SrvProfile: row.SrvProfile, Desc: row.Desc, Mac: row.Mac, IP: row.OntIP,
	}
}

// ── 文本配置缓存（原 savedconfiguration.txt / olt_cur_cfg.txt）──

// SaveConfigText 写入整篇配置文本（Kind 见 ConfigKindSaved / ConfigKindCurrent；按设备+类型覆盖）。
func (s *Store) SaveConfigText(hostIP, kind, content, sourceFile, sourceMTime, updatedAt string) error {
	hostIP, kind = strings.TrimSpace(hostIP), strings.TrimSpace(kind)
	if hostIP == "" || kind == "" {
		return fmt.Errorf("OLT IP and config kind are required")
	}
	_, err := s.db.Exec(`INSERT INTO "tbOltConfigCache"
		("HostIP","Kind","Content","ByteSize","SourceFile","SourceMTime","UpdatedAt")
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT("HostIP","Kind") DO UPDATE SET
		 "Content"=excluded."Content","ByteSize"=excluded."ByteSize",
		 "SourceFile"=excluded."SourceFile","SourceMTime"=excluded."SourceMTime","UpdatedAt"=excluded."UpdatedAt"`,
		hostIP, kind, content, len(content), sourceFile, sourceMTime, updatedAt)
	return err
}

// LoadConfigText 读整篇配置文本；ok=false 表示没有缓存（老实现此时返回空串）。
func (s *Store) LoadConfigText(hostIP, kind string) (string, bool, error) {
	var content string
	err := s.db.QueryRow(`SELECT "Content" FROM "tbOltConfigCache" WHERE "HostIP"=? AND "Kind"=?`,
		strings.TrimSpace(hostIP), strings.TrimSpace(kind)).Scan(&content)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content, strings.TrimSpace(content) != "", nil
}

// ── 自动注册配置（原 ontautomaticcfg.json）──

// SaveAutoRegConfig 写入 ONT 自动注册配置（口令由调用方加密后传入；列名与老 JSON 字段一致）。
func (s *Store) SaveAutoRegConfig(cfg OltAutoRegConfig, sourceFile, sourceMTime, updatedAt string) error {
	hostIP := strings.TrimSpace(cfg.HostIP)
	if hostIP == "" {
		return fmt.Errorf("OLT IP is required")
	}
	_, err := s.db.Exec(`INSERT INTO "tbOltAutoRegConfig"
		("HostIP","Type","Port","Username","Password","Registration","Relocation","TemplateId","ScanInterval",
		 "SourceFile","SourceMTime","UpdatedAt")
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT("HostIP") DO UPDATE SET
		 "Type"=excluded."Type","Port"=excluded."Port","Username"=excluded."Username","Password"=excluded."Password",
		 "Registration"=excluded."Registration","Relocation"=excluded."Relocation",
		 "TemplateId"=excluded."TemplateId","ScanInterval"=excluded."ScanInterval",
		 "SourceFile"=excluded."SourceFile","SourceMTime"=excluded."SourceMTime","UpdatedAt"=excluded."UpdatedAt"`,
		hostIP, cfg.Type, cfg.Port, cfg.Username, cfg.Password, cfg.Registration, cfg.Relocation,
		cfg.TemplateId, cfg.ScanInterval, sourceFile, sourceMTime, updatedAt)
	return err
}

// LoadAutoRegConfig 读自动注册配置；ok=false 表示该设备没有配置（老实现回落默认值 0/0/0/30）。
func (s *Store) LoadAutoRegConfig(hostIP string) (OltAutoRegConfig, bool, error) {
	var cfg OltAutoRegConfig
	cfg.HostIP = strings.TrimSpace(hostIP)
	err := s.db.QueryRow(`SELECT "Type","Port","Username","Password","Registration","Relocation","TemplateId",
			"ScanInterval","SourceFile","SourceMTime","UpdatedAt"
		FROM "tbOltAutoRegConfig" WHERE "HostIP"=?`, cfg.HostIP).Scan(
		&cfg.Type, &cfg.Port, &cfg.Username, &cfg.Password, &cfg.Registration, &cfg.Relocation,
		&cfg.TemplateId, &cfg.ScanInterval, &cfg.SourceFile, &cfg.SourceMTime, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return cfg, false, nil
	}
	if err != nil {
		return OltAutoRegConfig{}, false, err
	}
	return cfg, true, nil
}

// ListAutoRegHosts 返回配置了自动注册的设备 IP（升序），供后台任务重建使用。
func (s *Store) ListAutoRegHosts() ([]string, error) {
	rows, err := s.db.Query(`SELECT "HostIP" FROM "tbOltAutoRegConfig" WHERE "Registration"=1 ORDER BY "HostIP"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := make([]string, 0, 4)
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}
