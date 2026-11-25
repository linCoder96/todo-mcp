package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// 纯 Go SQLite 驱动（无 cgo）：design-viva.md §2.5/§4.2 明确选定。
	_ "modernc.org/sqlite"
)

// resolveDBPath 解析 %APPDATA%\todo-mcp\todo.db（Windows）。
// 使用标准库 os.UserConfigDir，不依赖任何 GUI API，两种模式结果一致。
func resolveDBPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录: %w", err)
	}
	dir := filepath.Join(base, "todo-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建数据目录失败: %w", err)
	}
	return filepath.Join(dir, "todo.db"), nil
}

// Open 打开生产数据库连接（路径解析 + DSN + 单连接 + 迁移）。
func Open() (*sql.DB, error) {
	path, err := resolveDBPath()
	if err != nil {
		return nil, err
	}
	return OpenAt(path)
}

// OpenAt 打开指定路径的数据库连接，供测试与工具复用同一套参数。
//
// DSN 固定 _busy_timeout=5000 与 _journal_mode=WAL（modernc.org/sqlite 的
// 连接作用域 PRAGMA 必须写进 DSN，design-viva.md §5.2 第 1 项）；
// SetMaxOpenConns(1) 保证进程内只有一条物理连接，PRAGMA 状态一致。
func OpenAt(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}
	// synchronous=NORMAL：WAL 下应用崩溃不丢数据，显著降低写放大。
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置 synchronous 失败: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
