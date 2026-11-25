//! 内嵌 DDL + `PRAGMA user_version` 门闸迁移（design.md §3，不引入迁移框架）。
//!
//! 演进约定：
//! - 每次打开连接检查 `PRAGMA user_version`，小于最新版本则逐级执行内嵌 DDL；
//! - 追加迁移 = 新增一段 `MIGRATION_VN` 常量 + [`run`] 里一行 `if current < N`，
//!   禁止修改已发布版本的 DDL；
//! - DDL 全部幂等（IF NOT EXISTS），两进程并发首次打开亦安全。

use rusqlite::Connection;

use super::DbError;

/// v1：初版表结构与索引（design.md §3.1 DDL 内嵌）。唯一必要修正：原文本把
/// 行级不变式 CHECK 作为"表级约束"夹在列定义之间，SQLite 语法不接受
/// （表级约束必须位于所有列定义之后），故改为 `completed_at` 的列级
/// CHECK，语义完全不变（未完成不得有完成时间）。
const MIGRATION_V1: &str = "
CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT    NOT NULL PRIMARY KEY,        -- UUIDv7，字典序≈创建时间序，可作排序 tiebreak
    title        TEXT    NOT NULL,                    -- 任务标题，1~500 字符（应用层校验）
    description  TEXT,                                -- 任务详情，可空；NULL = 无描述
    priority     INTEGER NOT NULL DEFAULT 0
                 CHECK (priority IN (0, 1, 2, 3)),   -- 优先级：0=无 1=低 2=中 3=高
    due_at       INTEGER,                             -- 到期时间（UTC epoch 毫秒）；NULL = 无到期
    status       INTEGER NOT NULL DEFAULT 0
                 CHECK (status IN (0, 1)),            -- 状态：0=进行中 1=已完成
    completed_at INTEGER                          -- 完成时间（UTC epoch 毫秒）
                 CHECK (status = 1 OR completed_at IS NULL),  -- 行级不变式：未完成不得有完成时间
    created_at   INTEGER NOT NULL,                    -- 创建时间（UTC epoch 毫秒）
    updated_at   INTEGER NOT NULL                     -- 最后更新时间（UTC epoch 毫秒）
);

-- 索引命名规范：idx_ 前缀 = 普通索引；uk_ 前缀 = 唯一索引（保留前缀，本表 MVP 无唯一索引）
CREATE INDEX IF NOT EXISTS idx_tasks_due_at ON tasks (due_at);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status);
";

/// 库内最新 schema 版本。
const LATEST_VERSION: i64 = 1;

/// 将连接上的库迁移到最新版本；幂等，可重复调用。
pub fn run(conn: &Connection) -> Result<(), DbError> {
    let current: i64 = conn.query_row("PRAGMA user_version", [], |row| row.get(0))?;
    if current >= LATEST_VERSION {
        return Ok(());
    }
    if current < 1 {
        conn.execute_batch(MIGRATION_V1)?;
    }
    // 后续版本在此追加，例如：if current < 2 { conn.execute_batch(MIGRATION_V2)?; }
    conn.pragma_update(None, "user_version", LATEST_VERSION)?;
    Ok(())
}
