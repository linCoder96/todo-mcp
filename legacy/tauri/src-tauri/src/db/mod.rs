//! SQLite 连接管理（design.md §1.2/§6）：
//! - [`resolve_db_path`]：统一解析 `%APPDATA%\todo-mcp\todo.db`，用 `dirs`
//!   crate 而非 Tauri 路径 API（MCP 模式没有 Tauri 运行时），保证两模式
//!   解析结果一致；
//! - [`open`]：打开连接即设 PRAGMA（WAL / busy_timeout=5000 /
//!   synchronous=NORMAL）并执行 user_version 门闸迁移。

pub mod migrate;
pub mod task_repo;

use std::fmt;
use std::path::{Path, PathBuf};

use rusqlite::Connection;

/// db 层统一错误：目录定位失败 / 文件 IO 失败 / SQLite 失败。
#[derive(Debug)]
pub enum DbError {
    /// 无法定位用户数据目录（%APPDATA% 缺失等异常环境）
    DataDirUnavailable,
    /// 建目录等文件系统 IO 失败
    Io(std::io::Error),
    /// SQLite 操作失败
    Sqlite(rusqlite::Error),
}

impl fmt::Display for DbError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            DbError::DataDirUnavailable => write!(f, "无法定位用户数据目录（%APPDATA%）"),
            DbError::Io(error) => write!(f, "数据库目录/文件 IO 失败: {error}"),
            DbError::Sqlite(error) => write!(f, "SQLite 操作失败: {error}"),
        }
    }
}

impl std::error::Error for DbError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            DbError::Io(error) => Some(error),
            DbError::Sqlite(error) => Some(error),
            DbError::DataDirUnavailable => None,
        }
    }
}

impl From<std::io::Error> for DbError {
    fn from(error: std::io::Error) -> Self {
        DbError::Io(error)
    }
}

impl From<rusqlite::Error> for DbError {
    fn from(error: rusqlite::Error) -> Self {
        DbError::Sqlite(error)
    }
}

/// 解析数据库文件路径：`%APPDATA%\todo-mcp\todo.db`；目录不存在则创建。
pub fn resolve_db_path() -> Result<PathBuf, DbError> {
    let base = dirs::data_dir().ok_or(DbError::DataDirUnavailable)?;
    let dir = base.join("todo-mcp");
    std::fs::create_dir_all(&dir)?;
    Ok(dir.join("todo.db"))
}

/// 打开生产数据库连接：路径解析 + PRAGMA + 迁移（GUI 与 MCP 两模式共用）。
pub fn open() -> Result<Connection, DbError> {
    let path = resolve_db_path()?;
    open_at(&path)
}

/// 打开指定路径的数据库连接（测试与工具复用同一套 PRAGMA / 迁移逻辑）。
pub fn open_at(path: &Path) -> Result<Connection, DbError> {
    let conn = Connection::open(path)?;
    apply_pragmas(&conn)?;
    migrate::run(&conn)?;
    Ok(conn)
}

/// 连接级 PRAGMA（design.md §1.2 并发方案，两进程统一）：
/// - `journal_mode=WAL`：读不阻塞写，该属性持久化在 db 文件上；
/// - `busy_timeout=5000`：写-写冲突不再立即返回 SQLITE_BUSY，内部等待至多 5 秒；
/// - `synchronous=NORMAL`：WAL 下应用崩溃不丢数据，换取写放大显著降低。
pub(crate) fn apply_pragmas(conn: &Connection) -> Result<(), DbError> {
    conn.pragma_update(None, "journal_mode", "WAL")?;
    conn.pragma_update(None, "busy_timeout", 5_000_i64)?;
    conn.pragma_update(None, "synchronous", "NORMAL")?;
    Ok(())
}
