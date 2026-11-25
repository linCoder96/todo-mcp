package store

import (
	"database/sql"
	"fmt"
)

// v1 表结构与索引（design-viva.md §5.1 内嵌 DDL，以 user_version 为门闸）。
//
// 与文档逐字文本的唯一差异：原文本把“未完成不得有完成时间”的 CHECK 作为
// 表级约束夹在两列定义之间，SQLite 语法不接受（表级约束必须在所有列定义
// 之后）。这里把它改写为 completed_at 列的列级 CHECK，语义完全不变。
const schemaV1 = `
CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT    NOT NULL PRIMARY KEY,        -- UUIDv7，字典序≈创建时间序，可作排序 tiebreak
    title        TEXT    NOT NULL,                    -- 1~500 字符（应用层校验）
    description  TEXT,                                -- NULL = 无描述
    priority     INTEGER NOT NULL DEFAULT 0
                 CHECK (priority IN (0, 1, 2, 3)),   -- 0=无 1=低 2=中 3=高
    due_at       INTEGER,                             -- UTC epoch 毫秒；NULL = 无到期
    status       INTEGER NOT NULL DEFAULT 0
                 CHECK (status IN (0, 1)),            -- 0=进行中 1=已完成
    completed_at INTEGER
                 CHECK (status = 1 OR completed_at IS NULL), -- 行级不变式
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_due_at ON tasks (due_at);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status);
`

// v2（2026-11-24，自定义字段）：fields 元数据表 + task_field_values 值表（EAV）。
// - 外键不带 ON DELETE：本包未开 PRAGMA foreign_keys（单连接 + WAL 惯例），
//   级联删除由应用层在 Remove / RemoveField 事务内显式执行，避免惰性约束误导。
// - options 存 JSON 数组文本；multi_select 的 text_value 也存 JSON 数组文本。
// - 唯一索引 uq_fields_name：字段名全局唯一（应用层先查后插给友好中文错误，
//   索引兜底并发重复）。
const schemaV2 = `
CREATE TABLE IF NOT EXISTS fields (
    id         TEXT    NOT NULL PRIMARY KEY,   -- UUIDv7
    name       TEXT    NOT NULL,               -- 1~50 字符（应用层校验）
    type       TEXT    NOT NULL
               CHECK (type IN ('text', 'single_select', 'multi_select', 'number', 'date')),
    display    TEXT    NOT NULL DEFAULT 'plain'
               CHECK (display IN ('plain', 'percent', 'currency', 'progress')),
    options    TEXT,                           -- 选择类 JSON 数组；其它类型 NULL
    position   INTEGER NOT NULL DEFAULT 0,     -- 显示顺序，小在前
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_fields_name ON fields (name);
CREATE INDEX IF NOT EXISTS idx_fields_position ON fields (position);

CREATE TABLE IF NOT EXISTS task_field_values (
    task_id    TEXT NOT NULL,
    field_id   TEXT NOT NULL,
    text_value TEXT,                           -- text/single_select/multi_select(JSON 数组)
    num_value  REAL,                           -- number 原值；date 为 epoch 毫秒
    PRIMARY KEY (task_id, field_id)
);
CREATE INDEX IF NOT EXISTS idx_tfv_field_text ON task_field_values (field_id, text_value);
CREATE INDEX IF NOT EXISTS idx_tfv_field_num  ON task_field_values (field_id, num_value);
`

// 当前最新 schema 版本。
const latestVersion = 2

// migrate 以 PRAGMA user_version 门闸逐级执行迁移；可重复调用（幂等）。
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取 user_version 失败: %w", err)
	}
	if version >= latestVersion {
		return nil
	}
	if version < 1 {
		if _, err := db.Exec(schemaV1); err != nil {
			return fmt.Errorf("执行 v1 迁移失败: %w", err)
		}
	}
	if version < 2 {
		if _, err := db.Exec(schemaV2); err != nil {
			return fmt.Errorf("执行 v2 迁移失败: %w", err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", latestVersion)); err != nil {
		return fmt.Errorf("写入 user_version 失败: %w", err)
	}
	return nil
}
