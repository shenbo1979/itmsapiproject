// 由 tools/port_switch_batch2a.py 从 switchapi/switch_collection_db.go 整体搬运（采集快照数据层，改为 Store 方法）
package switchmod

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type SwitchCollectionDBSnapshot struct {
	ID          int64
	Hostip      string
	CollectedAt string
	DayKey      string
	Data        string
}

type SwitchCollectionDBInterfaceStat struct {
	SnapshotID   int64
	Hostip       string
	CollectedAt  string
	Interface    string
	InBroadcast  int64
	OutBroadcast int64
	InDiscard    int64
	OutDiscard   int64
	InPause      int64
	OutPause     int64
	InError      int64
	OutError     int64
	InCRC        int64
	OutCRC       int64
	InBytes      int64
	OutBytes     int64
	InRate       int64
	OutRate      int64
}

func (s *Store) createSwitchCollectionTables() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	if s.db == nil {
		return fmt.Errorf("sqlite db is not initialized")
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS tbSwitchCollectionSnapshot (
			nId INTEGER PRIMARY KEY AUTOINCREMENT,
			szHostip VARCHAR(100) NOT NULL,
			dtCollectedAt VARCHAR(32) NOT NULL,
			szDayKey VARCHAR(10) NOT NULL,
			szSnapshotJson TEXT NOT NULL,
			UNIQUE(szHostip, dtCollectedAt)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tbSwitchCollectionSnapshot_host_time
			ON tbSwitchCollectionSnapshot(szHostip, dtCollectedAt)`,
		`CREATE TABLE IF NOT EXISTS tbSwitchCollectionInterfaceStat (
			nId INTEGER PRIMARY KEY AUTOINCREMENT,
			nSnapshotId INTEGER NOT NULL,
			szHostip VARCHAR(100) NOT NULL,
			dtCollectedAt VARCHAR(32) NOT NULL,
			szInterface VARCHAR(128) NOT NULL,
			nInBroadcast INTEGER NOT NULL DEFAULT 0,
			nOutBroadcast INTEGER NOT NULL DEFAULT 0,
			nInDiscard INTEGER NOT NULL DEFAULT 0,
			nOutDiscard INTEGER NOT NULL DEFAULT 0,
			nInPause INTEGER NOT NULL DEFAULT 0,
			nOutPause INTEGER NOT NULL DEFAULT 0,
			nInError INTEGER NOT NULL DEFAULT 0,
			nOutError INTEGER NOT NULL DEFAULT 0,
			nInCRC INTEGER NOT NULL DEFAULT 0,
			nOutCRC INTEGER NOT NULL DEFAULT 0,
			nInBytes INTEGER NOT NULL DEFAULT 0,
			nOutBytes INTEGER NOT NULL DEFAULT 0,
			nInRate INTEGER NOT NULL DEFAULT 0,
			nOutRate INTEGER NOT NULL DEFAULT 0,
			UNIQUE(nSnapshotId, szInterface)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tbSwitchCollectionInterfaceStat_host_time_port
			ON tbSwitchCollectionInterfaceStat(szHostip, dtCollectedAt, szInterface)`,
		`CREATE INDEX IF NOT EXISTS idx_tbSwitchCollectionInterfaceStat_host_port_time
			ON tbSwitchCollectionInterfaceStat(szHostip, szInterface, dtCollectedAt)`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureSwitchCollectionTables() error {
	return s.createSwitchCollectionTables()
}

func (s *Store) migrateSwitchCollectionFilesToDB() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return err
	}
	if s.db == nil {
		return fmt.Errorf("sqlite db is not initialized")
	}
	if err := os.MkdirAll(s.legacyImportDir, 0755); err != nil {
		return err
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	entries, err := os.ReadDir(s.legacyImportDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	hostips := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			hostips = append(hostips, entry.Name())
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			hostips = append(hostips, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
	}
	sort.Strings(hostips)

	for _, hostip := range hostips {
		if strings.TrimSpace(hostip) == "" {
			continue
		}
		if err := s.migrateSingleSwitchCollectionHostToDB(hostip); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateSingleSwitchCollectionHostToDB(hostip string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return err
	}
	hasFileSource, err := s.hasSwitchCollectionFileStorage(hostip)
	if err != nil {
		return err
	}
	if !hasFileSource {
		return nil
	}
	if err := s.migrateLegacySwitchCollectionHostFile(hostip); err != nil {
		return err
	}

	records, _, err := s.loadAllSwitchCollectionSnapshotsForHost(hostip)
	if err != nil {
		return err
	}

	normalizedRecords := make([]SwitchCollectionSnapshot, 0, len(records))
	validCollectedAt := make(map[string]struct{}, len(records))
	for _, record := range records {
		record = normalizeSwitchCollectionSnapshot(record)
		ts, err := parseSwitchCollectionTimestamp(record.CollectedAt)
		if err != nil {
			continue
		}
		record.CollectedAt = ts.Format("2006-01-02 15:04:05")
		normalizedRecords = append(normalizedRecords, record)
		validCollectedAt[record.CollectedAt] = struct{}{}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 必须无条件回滚：Commit 成功后 Rollback 是空操作（ErrTxDone）。
	// 只在 err != nil 时回滚的写法不安全——后续 `rows, err := ...` 会遮蔽 err，
	// 一旦语句报错（例如库被其他进程锁住），事务就永远不会回滚，
	// 连接池（MaxOpenConns=1）里的唯一连接会被永久占住，导致整个服务卡死。
	defer tx.Rollback()

	if err := deleteStaleSwitchCollectionSnapshotsTx(tx, hostip, validCollectedAt); err != nil {
		return err
	}
	if len(normalizedRecords) == 0 {
		err = tx.Commit()
		return err
	}

	snapshotStmt, err := tx.Prepare(`
		INSERT INTO tbSwitchCollectionSnapshot(szHostip, dtCollectedAt, szDayKey, szSnapshotJson)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(szHostip, dtCollectedAt) DO UPDATE SET
			szDayKey = excluded.szDayKey,
			szSnapshotJson = excluded.szSnapshotJson
	`)
	if err != nil {
		return err
	}
	defer snapshotStmt.Close()

	querySnapshotIDStmt, err := tx.Prepare(`SELECT nId FROM tbSwitchCollectionSnapshot WHERE szHostip = ? AND dtCollectedAt = ?`)
	if err != nil {
		return err
	}
	defer querySnapshotIDStmt.Close()

	deleteInterfaceStmt, err := tx.Prepare(`DELETE FROM tbSwitchCollectionInterfaceStat WHERE nSnapshotId = ?`)
	if err != nil {
		return err
	}
	defer deleteInterfaceStmt.Close()

	interfaceStmt, err := tx.Prepare(`
		INSERT INTO tbSwitchCollectionInterfaceStat(
			nSnapshotId, szHostip, dtCollectedAt, szInterface,
			nInBroadcast, nOutBroadcast, nInDiscard, nOutDiscard,
			nInPause, nOutPause, nInError, nOutError, nInCRC, nOutCRC,
			nInBytes, nOutBytes, nInRate, nOutRate
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer interfaceStmt.Close()

	for _, record := range normalizedRecords {
		ts, err := parseSwitchCollectionTimestamp(record.CollectedAt)
		if err != nil {
			continue
		}

		rawSnapshot, err := json.Marshal(record)
		if err != nil {
			return err
		}

		if _, err := snapshotStmt.Exec(hostip, record.CollectedAt, switchCollectionDayKey(ts), string(rawSnapshot)); err != nil {
			return err
		}

		var snapshotID int64
		if err := querySnapshotIDStmt.QueryRow(hostip, record.CollectedAt).Scan(&snapshotID); err != nil {
			return err
		}

		if _, err := deleteInterfaceStmt.Exec(snapshotID); err != nil {
			return err
		}

		for _, item := range record.Interfaces {
			if _, err := interfaceStmt.Exec(
				snapshotID, hostip, record.CollectedAt, item.Interface,
				item.InBroadcast, item.OutBroadcast, item.InDiscard, item.OutDiscard,
				item.InPause, item.OutPause, item.InError, item.OutError, item.InCRC, item.OutCRC,
				item.InBytes, item.OutBytes, item.InRate, item.OutRate,
			); err != nil {
				return err
			}
		}
	}

	err = tx.Commit()
	if err != nil {
		return err
	}
	return s.cleanupMigratedSwitchCollectionFiles(hostip)
}

func (s *Store) hasSwitchCollectionFileStorage(hostip string) (bool, error) {
	legacyPath := s.switchCollectionLegacyHostFilePath(hostip)
	_, err := os.Stat(legacyPath)
	if err == nil {
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}

	paths, err := s.listSwitchCollectionShardPaths(hostip)
	if err != nil {
		return false, err
	}
	return len(paths) > 0, nil
}

func (s *Store) cleanupMigratedSwitchCollectionFiles(hostip string) error {
	legacyPath := s.switchCollectionLegacyHostFilePath(hostip)
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	hostDir := s.collectionHostDirPath(hostip)
	if err := os.RemoveAll(hostDir); err != nil {
		return err
	}
	return nil
}

func deleteStaleSwitchCollectionSnapshotsTx(tx *sql.Tx, hostip string, validCollectedAt map[string]struct{}) error {
	rows, err := tx.Query(`SELECT nId, dtCollectedAt FROM tbSwitchCollectionSnapshot WHERE szHostip = ?`, hostip)
	if err != nil {
		return err
	}
	defer rows.Close()

	staleSnapshotIDs := make([]int64, 0)
	for rows.Next() {
		var snapshotID int64
		var collectedAt string
		if err := rows.Scan(&snapshotID, &collectedAt); err != nil {
			return err
		}
		if _, ok := validCollectedAt[collectedAt]; ok {
			continue
		}
		staleSnapshotIDs = append(staleSnapshotIDs, snapshotID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(staleSnapshotIDs) == 0 {
		return nil
	}

	deleteInterfaceStmt, err := tx.Prepare(`DELETE FROM tbSwitchCollectionInterfaceStat WHERE nSnapshotId = ?`)
	if err != nil {
		return err
	}
	defer deleteInterfaceStmt.Close()

	deleteSnapshotStmt, err := tx.Prepare(`DELETE FROM tbSwitchCollectionSnapshot WHERE nId = ?`)
	if err != nil {
		return err
	}
	defer deleteSnapshotStmt.Close()

	for _, snapshotID := range staleSnapshotIDs {
		if _, err := deleteInterfaceStmt.Exec(snapshotID); err != nil {
			return err
		}
		if _, err := deleteSnapshotStmt.Exec(snapshotID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) countSwitchCollectionSnapshotsInDB(hostip string) (int, error) {
	if err := s.ensureDB(); err != nil {
		return 0, err
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return 0, err
	}
	if s.db == nil {
		return 0, fmt.Errorf("sqlite db is not initialized")
	}
	query := `SELECT COUNT(1) FROM tbSwitchCollectionSnapshot`
	args := []any{}
	if strings.TrimSpace(hostip) != "" {
		query += ` WHERE szHostip = ?`
		args = append(args, hostip)
	}

	row := s.db.QueryRow(query, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) saveSwitchCollectionSnapshotToDB(hostip string, snapshot SwitchCollectionSnapshot) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return err
	}
	if s.db == nil {
		return fmt.Errorf("sqlite db is not initialized")
	}

	hostip = strings.TrimSpace(hostip)
	if hostip == "" {
		return fmt.Errorf("hostip is required")
	}

	snapshot = normalizeSwitchCollectionSnapshot(snapshot)
	ts, err := parseSwitchCollectionTimestamp(snapshot.CollectedAt)
	if err != nil {
		return err
	}
	snapshot.CollectedAt = ts.Format("2006-01-02 15:04:05")

	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 无条件回滚，成功后 Commit 的 Rollback 为空操作；详见 migrateSingleSwitchCollectionHostToDB 的说明。
	defer tx.Rollback()

	if _, err = tx.Exec(`
		INSERT INTO tbSwitchCollectionSnapshot(szHostip, dtCollectedAt, szDayKey, szSnapshotJson)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(szHostip, dtCollectedAt) DO UPDATE SET
			szDayKey = excluded.szDayKey,
			szSnapshotJson = excluded.szSnapshotJson
	`, hostip, snapshot.CollectedAt, switchCollectionDayKey(ts), string(rawSnapshot)); err != nil {
		return err
	}

	var snapshotID int64
	if err = tx.QueryRow(`SELECT nId FROM tbSwitchCollectionSnapshot WHERE szHostip = ? AND dtCollectedAt = ?`, hostip, snapshot.CollectedAt).Scan(&snapshotID); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM tbSwitchCollectionInterfaceStat WHERE nSnapshotId = ?`, snapshotID); err != nil {
		return err
	}

	for _, item := range snapshot.Interfaces {
		if _, err = tx.Exec(`
			INSERT INTO tbSwitchCollectionInterfaceStat(
				nSnapshotId, szHostip, dtCollectedAt, szInterface,
				nInBroadcast, nOutBroadcast, nInDiscard, nOutDiscard,
				nInPause, nOutPause, nInError, nOutError, nInCRC, nOutCRC,
				nInBytes, nOutBytes, nInRate, nOutRate
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			snapshotID, hostip, snapshot.CollectedAt, item.Interface,
			item.InBroadcast, item.OutBroadcast, item.InDiscard, item.OutDiscard,
			item.InPause, item.OutPause, item.InError, item.OutError, item.InCRC, item.OutCRC,
			item.InBytes, item.OutBytes, item.InRate, item.OutRate,
		); err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func (s *Store) pruneSwitchCollectionSnapshotsInDB(hostip string, retentionPeriod int, now time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("switch 模块数据库未初始化")
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return err
	}
	if s.db == nil {
		return fmt.Errorf("sqlite db is not initialized")
	}
	if retentionPeriod <= 0 {
		retentionPeriod = 7
	}

	cutoff := now.AddDate(0, 0, -retentionPeriod)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// 无条件回滚，成功后 Commit 的 Rollback 为空操作；详见 migrateSingleSwitchCollectionHostToDB 的说明。
	defer tx.Rollback()

	query := `SELECT nId, dtCollectedAt FROM tbSwitchCollectionSnapshot`
	args := []any{}
	if strings.TrimSpace(hostip) != "" {
		query += ` WHERE szHostip = ?`
		args = append(args, hostip)
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	staleSnapshotIDs := make([]int64, 0)
	for rows.Next() {
		var snapshotID int64
		var collectedAt string
		if err := rows.Scan(&snapshotID, &collectedAt); err != nil {
			return err
		}
		ts, err := parseSwitchCollectionTimestamp(collectedAt)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			staleSnapshotIDs = append(staleSnapshotIDs, snapshotID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	deleteInterfaceStmt, err := tx.Prepare(`DELETE FROM tbSwitchCollectionInterfaceStat WHERE nSnapshotId = ?`)
	if err != nil {
		return err
	}
	defer deleteInterfaceStmt.Close()

	deleteSnapshotStmt, err := tx.Prepare(`DELETE FROM tbSwitchCollectionSnapshot WHERE nId = ?`)
	if err != nil {
		return err
	}
	defer deleteSnapshotStmt.Close()

	for _, snapshotID := range staleSnapshotIDs {
		if _, err := deleteInterfaceStmt.Exec(snapshotID); err != nil {
			return err
		}
		if _, err := deleteSnapshotStmt.Exec(snapshotID); err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func (s *Store) loadAllSwitchCollectionSnapshotsForHostFromDB(hostip string) ([]SwitchCollectionSnapshot, bool, error) {
	return s.loadSwitchCollectionSnapshotsFromDB(hostip, nil, nil)
}

func (s *Store) loadSwitchCollectionSnapshotsInRangeFromDB(hostip string, start, end time.Time) ([]SwitchCollectionSnapshot, error) {
	records, _, err := s.loadSwitchCollectionSnapshotsFromDB(hostip, &start, &end)
	return records, err
}

func (s *Store) loadSwitchCollectionSnapshotsFromDB(hostip string, start, end *time.Time) ([]SwitchCollectionSnapshot, bool, error) {
	if err := s.ensureDB(); err != nil {
		return nil, false, err
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return nil, false, err
	}
	if s.db == nil {
		return nil, false, fmt.Errorf("sqlite db is not initialized")
	}

	hostip = strings.TrimSpace(hostip)
	if hostip == "" {
		return []SwitchCollectionSnapshot{}, false, nil
	}

	query := `SELECT dtCollectedAt, szSnapshotJson FROM tbSwitchCollectionSnapshot WHERE szHostip = ?`
	args := []any{hostip}
	query += ` ORDER BY dtCollectedAt`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	records := make([]SwitchCollectionSnapshot, 0)
	for rows.Next() {
		var collectedAt string
		var rawSnapshot string
		if err := rows.Scan(&collectedAt, &rawSnapshot); err != nil {
			return nil, false, err
		}

		record := SwitchCollectionSnapshot{}
		if strings.TrimSpace(rawSnapshot) != "" {
			if err := json.Unmarshal([]byte(rawSnapshot), &record); err != nil {
				return nil, false, err
			}
		}
		if strings.TrimSpace(record.CollectedAt) == "" {
			record.CollectedAt = collectedAt
		}
		ts, err := parseSwitchCollectionTimestamp(record.CollectedAt)
		if err != nil {
			continue
		}
		if start != nil && ts.Before(*start) {
			continue
		}
		if end != nil && ts.After(*end) {
			continue
		}
		record.CollectedAt = ts.Format("2006-01-02 15:04:05")
		record = normalizeSwitchCollectionSnapshot(record)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	sort.Slice(records, func(i, j int) bool {
		ti, ierr := parseSwitchCollectionTimestamp(records[i].CollectedAt)
		tj, jerr := parseSwitchCollectionTimestamp(records[j].CollectedAt)
		switch {
		case ierr != nil && jerr != nil:
			return records[i].CollectedAt < records[j].CollectedAt
		case ierr != nil:
			return false
		case jerr != nil:
			return true
		default:
			return ti.Before(tj)
		}
	})
	return records, len(records) > 0, nil
}

func (s *Store) loadSwitchCollectionInterfaceStatsFromDB(hostip, port string, start, end time.Time) ([]SwitchCollectionDBInterfaceStat, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return nil, err
	}
	if s.db == nil {
		return nil, fmt.Errorf("sqlite db is not initialized")
	}
	portAliases := buildSwitchPortQueryAliases(port)
	if len(portAliases) == 0 {
		return []SwitchCollectionDBInterfaceStat{}, nil
	}
	placeholders := make([]string, len(portAliases))
	args := make([]any, 0, 3+len(portAliases))
	args = append(args, hostip)
	for i, alias := range portAliases {
		placeholders[i] = "?"
		args = append(args, alias)
	}
	args = append(args, start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"))

	query := `
		SELECT nSnapshotId, szHostip, dtCollectedAt, szInterface,
			nInBroadcast, nOutBroadcast, nInDiscard, nOutDiscard,
			nInPause, nOutPause, nInError, nOutError, nInCRC, nOutCRC,
			nInBytes, nOutBytes, nInRate, nOutRate
		FROM tbSwitchCollectionInterfaceStat
		WHERE szHostip = ? AND szInterface IN (` + strings.Join(placeholders, ",") + `)
			AND dtCollectedAt >= ? AND dtCollectedAt <= ?
		ORDER BY dtCollectedAt
	`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]SwitchCollectionDBInterfaceStat, 0)
	for rows.Next() {
		var item SwitchCollectionDBInterfaceStat
		if err := rows.Scan(
			&item.SnapshotID, &item.Hostip, &item.CollectedAt, &item.Interface,
			&item.InBroadcast, &item.OutBroadcast, &item.InDiscard, &item.OutDiscard,
			&item.InPause, &item.OutPause, &item.InError, &item.OutError, &item.InCRC, &item.OutCRC,
			&item.InBytes, &item.OutBytes, &item.InRate, &item.OutRate,
		); err != nil {
			return nil, err
		}
		collectedAt, err := parseSwitchCollectionTimestamp(item.CollectedAt)
		if err != nil {
			continue
		}
		item.CollectedAt = collectedAt.Format("2006-01-02 15:04:05")
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CollectedAt < result[j].CollectedAt
	})
	return result, nil
}

func buildSwitchPortQueryAliases(port string) []string {
	port = normalizeSwitchPortName(port)
	if port == "" {
		return nil
	}

	seen := map[string]struct{}{}
	aliases := make([]string, 0, 4)
	add := func(val string) {
		val = normalizeSwitchPortName(val)
		if val == "" {
			return
		}
		if _, ok := seen[val]; ok {
			return
		}
		seen[val] = struct{}{}
		aliases = append(aliases, val)
	}

	add(port)
	add(normalizeH3CPortName(port))

	aliasPatterns := []struct {
		pattern *regexp.Regexp
		targets []string
	}{
		{regexp.MustCompile(`^Ten-GigabitEthernet(\d+/\d+/\d+)$`), []string{`XGE$1`, `10GE$1`}},
		{regexp.MustCompile(`^GigabitEthernet(\d+/\d+/\d+)$`), []string{`GE$1`}},
		{regexp.MustCompile(`^M-GigabitEthernet(\d+/\d+/\d+)$`), []string{`MGE$1`}},
		{regexp.MustCompile(`^FortyGigE(\d+/\d+/\d+)$`), []string{`FGE$1`, `40GE$1`}},
		{regexp.MustCompile(`^HundredGigE(\d+/\d+/\d+)$`), []string{`100GE$1`}},
		{regexp.MustCompile(`^Twenty-FiveGigE(\d+/\d+/\d+)$`), []string{`25GE$1`}},
		{regexp.MustCompile(`^Bridge-Aggregation(\d+)$`), []string{`BAGG$1`}},
	}
	for _, item := range aliasPatterns {
		for _, target := range item.targets {
			add(item.pattern.ReplaceAllString(port, target))
		}
	}
	return aliases
}

func (s *Store) loadLatestSwitchCollectionSnapshotTimeFromDB(hostip string) (time.Time, bool, error) {
	if err := s.ensureDB(); err != nil {
		return time.Time{}, false, err
	}
	if err := s.ensureSwitchCollectionTables(); err != nil {
		return time.Time{}, false, err
	}
	if s.db == nil {
		return time.Time{}, false, fmt.Errorf("sqlite db is not initialized")
	}

	rows, err := s.db.Query(`SELECT dtCollectedAt FROM tbSwitchCollectionSnapshot WHERE szHostip = ?`, hostip)
	if err != nil {
		return time.Time{}, false, err
	}
	defer rows.Close()

	var latest time.Time
	found := false
	for rows.Next() {
		var collectedAt string
		if err := rows.Scan(&collectedAt); err != nil {
			return time.Time{}, false, err
		}
		ts, err := parseSwitchCollectionTimestamp(collectedAt)
		if err != nil {
			continue
		}
		if !found || ts.After(latest) {
			latest = ts
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, err
	}
	if !found {
		return time.Time{}, false, nil
	}
	return latest, true, nil
}
