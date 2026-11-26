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
//   - 外键不带 ON DELETE：本包未开 PRAGMA foreign_keys（单连接 + WAL 惯例），
//     级联删除由应用层在 Remove / RemoveField 事务内显式执行，避免惰性约束误导。
//   - options 存 JSON 数组文本；multi_select 的 text_value 也存 JSON 数组文本。
//   - 唯一索引 uq_fields_name：字段名全局唯一（应用层先查后插给友好中文错误，
//     索引兜底并发重复）。
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

// v3（2026-11-26，图片附件）：task_images 元数据表。
//   - 图片文件本体在数据库目录的 images/<task_id>/ 下（ImagesDirFor），库里只存
//     元数据与磁盘文件名（stored_name = 图片 id + 扩展名）；删除任务/图片时应用层
//     在事务外连带删文件（文件系统无事务，先库后文件的顺序见 RemoveImage）。
//   - task_id 无外键：与 v1/v2 同一口径，级联由应用层显式执行。
const schemaV3 = `
CREATE TABLE IF NOT EXISTS task_images (
    id          TEXT    NOT NULL PRIMARY KEY,   -- UUIDv7，同时用作磁盘文件名
    task_id     TEXT    NOT NULL,
    file_name   TEXT    NOT NULL,               -- 原始文件名（展示用）
    stored_name TEXT    NOT NULL,               -- 磁盘文件名 = <id>.<扩展名>
    mime        TEXT    NOT NULL,               -- image/png | image/jpeg | image/gif
    size_bytes  INTEGER NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,     -- 显示顺序，小在前
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_images_task ON task_images (task_id, position);
`

// v4（2026-11-26，文件附件）：task_files 元数据表（与 task_images 同构，两种模式）。
//   - copy：文件拷入数据库目录的 files/<task_id>/ 下（stored_name = <id>.<扩展名>），
//     与源文件解耦，删除附件时连带删文件；
//   - ref：只存源文件绝对路径（src_path），不拷贝文件，kind 仍为 ref。
//   - task_id 无外键：与 v1-v3 同一口径，级联由应用层显式执行。
const schemaV4 = `
CREATE TABLE IF NOT EXISTS task_files (
    id          TEXT    NOT NULL PRIMARY KEY,   -- UUIDv7
    task_id     TEXT    NOT NULL,
    file_name   TEXT    NOT NULL,               -- 原始文件名（展示用）
    kind        TEXT    NOT NULL CHECK (kind IN ('copy', 'ref')),
    stored_name TEXT,                           -- copy 模式 <id>.<扩展名>；ref 模式 NULL
    src_path    TEXT,                           -- ref 模式源文件绝对路径；copy 模式 NULL
    size_bytes  INTEGER NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,     -- 显示顺序，小在前
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_files_task ON task_files (task_id, position);
`


// v5（2026-11-26，视图偏好持久化）：app_prefs 单行表，承载任务清单的
// 状态 tab、筛选条件、排序、分组（ViewPrefs JSON）。单用户桌面应用，
// 主键固定 1；写侧全量覆盖（UPSERT），无历史版本需求。
const schemaV5 = `
CREATE TABLE IF NOT EXISTS app_prefs (
    id    INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    prefs TEXT    NOT NULL
);
`

// v6（2026-11-26，归档与知识文档）：tasks 增加 archived_at 列。
//   - NULL = 未归档（默认，存量任务自动落此值）；非 NULL = 归档时间（UTC 毫秒）。
//   - 「归档必已完成」不变式由应用层保证（Archive 只接受 status=1；Reopen 顺带
//     清空归档），SQLite 的 ALTER TABLE ADD COLUMN 无法写跨列 CHECK。
//   - 已归档任务从全部常规视图隐去（GUI 状态 tab / MCP list_tasks 默认口径，
//     见 query.go 的 Archived 筛选与 TaskFilter.Archived 默认值）。
const schemaV6 = `
ALTER TABLE tasks ADD COLUMN archived_at INTEGER;
`

// 当前最新 schema 版本。
const latestVersion = 6

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
	if version < 3 {
		if _, err := db.Exec(schemaV3); err != nil {
			return fmt.Errorf("执行 v3 迁移失败: %w", err)
		}
	}
	if version < 4 {
		if _, err := db.Exec(schemaV4); err != nil {
			return fmt.Errorf("执行 v4 迁移失败: %w", err)
		}
	}
	if version < 5 {
		if _, err := db.Exec(schemaV5); err != nil {
			return fmt.Errorf("执行 v5 迁移失败: %w", err)
		}
	}
	if version < 6 {
		if _, err := db.Exec(schemaV6); err != nil {
			return fmt.Errorf("执行 v6 迁移失败: %w", err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", latestVersion)); err != nil {
		return fmt.Errorf("写入 user_version 失败: %w", err)
	}
	return nil
}
