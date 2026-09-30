package core

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// SQLite 驱动（CGO）；与老工程使用同一版本，保证库文件格式与 PRAGMA 行为一致。
	_ "github.com/mattn/go-sqlite3"
)

// OpenSQLite 打开（必要时创建）一个 SQLite 库，并统一收敛并发写法。
//
// 依据 docs/03-data-authority.md 第 3 节结论 3（老系统 `/home/switch_rpc_api.db` 被两个进程共写，
// 2026-09-20 上午发生写锁争用 + 事务泄漏导致整站假死），统一工程按“一模块一库 + 单写者”落地：
//
//   - journal_mode=WAL：读不阻塞写，避免“读注册状态被写锁卡死”；
//   - busy_timeout=5000：等锁上限（毫秒），超时快速失败而不是无限挂起；
//   - _txlock=immediate：写事务一开始就拿写锁，避免后升锁导致的 SQLITE_BUSY/死锁；
//   - MaxOpenConns(1) + MaxIdleConns(1)：单进程内串行访问，连接池只有一条连接；
//   - 目录自动创建（0750），文件由 SQLite 以 0600 创建。
//
// 注意：连接池只有一条连接，事务出错路径必须回滚，否则唯一连接被永久占住
// （老工程的死锁事故与回归测试：/switchapi/switch_collection_db_deadlock_test.go）。
// 本工程统一使用 `defer tx.Rollback()`（Commit 成功后的 Rollback 是空操作）。
func OpenSQLite(path string) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("sqlite 数据库路径不能为空")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("创建数据库目录 %s 失败: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on&_txlock=immediate",
		filepath.ToSlash(path))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s 失败: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库 %s 失败: %w", path, err)
	}
	return db, nil
}
