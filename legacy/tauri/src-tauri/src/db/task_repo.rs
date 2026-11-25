//! `tasks` 表数据访问唯一入口（design.md §6 模块职责边界）：
//! GUI commands 与 MCP tools 都只做"协议适配 + 参数校验"，本模块是唯一
//! 出现 SQL 的地方；两模式共用同一份语义与校验规则。
//!
//! 写操作一律包在短事务内（design.md §1.2 配套约定）：用
//! [`rusqlite::Connection::unchecked_transaction`]，语义等同 `transaction`，
//! 但接受 `&Connection`，便于上层以 `Mutex<Connection>` 持有长连接。

use std::fmt;

use rusqlite::{params, params_from_iter, Connection, OptionalExtension, Row};
use uuid::Uuid;

/// 所有列按顺序展开，SELECT/行映射共用，避免字段漂移。
const TASK_COLUMNS: &str =
    "id, title, description, priority, due_at, status, completed_at, created_at, updated_at";

/// 任务完整模型（存储层表示：时间为 UTC epoch 毫秒，id 为 UUIDv7 文本）。
///
/// 对外的 RFC3339 字符串转换属于协议层（`mcp/dto.rs` / GUI commands），
/// 本模块只负责存储表示。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Task {
    pub id: String,
    pub title: String,
    pub description: Option<String>,
    /// 0=无 1=低 2=中 3=高
    pub priority: i64,
    /// UTC epoch 毫秒；None = 无到期
    pub due_at: Option<i64>,
    /// 0=进行中 1=已完成
    pub status: i64,
    /// UTC epoch 毫秒；未完成恒为 None（见 DDL 行级不变式）
    pub completed_at: Option<i64>,
    pub created_at: i64,
    pub updated_at: i64,
}

/// `create_task` 入参：status / completed_at / 时间戳由本模块生成，不开放。
#[derive(Debug, Clone)]
pub struct NewTask {
    pub title: String,
    pub description: Option<String>,
    pub priority: i64,
    pub due_at: Option<i64>,
}

/// `update_task` 部分更新入参（design.md §4.3 双层 Option 语义）：
///
/// - 外层 `None`：字段**缺席**，保持原值；
/// - `Some(None)`：字段**显式置空**，把该列清为 NULL；
/// - `Some(Some(value))`：字段显式赋值。
///
/// serde 对 `Option<Option<T>>` 原生支持这一区分，协议层解包后直接传入。
#[derive(Debug, Clone, Default)]
pub struct TaskPatch {
    pub title: Option<String>,
    pub description: Option<Option<String>>,
    pub priority: Option<i64>,
    pub due_at: Option<Option<i64>>,
}

/// `list_tasks` 查询条件；`limit` 在代码层钳制（design.md §4.2）。
#[derive(Debug, Clone, Copy)]
pub struct TaskQuery {
    pub status: Option<i64>,
    pub priority: Option<i64>,
    pub limit: u32,
    pub offset: u32,
}

/// `list_tasks` 出参：`total` 是当前筛选条件下的总数（供 agent 判断是否还有下一页）。
#[derive(Debug, Clone)]
pub struct TaskPage {
    pub total: u64,
    pub tasks: Vec<Task>,
}

/// repo 层错误：业务失败（不存在 / 入参非法）与底层 SQLite 失败分层。
#[derive(Debug)]
pub enum RepoError {
    /// 任务 id 不存在（update/complete/remove 不幂等，明确报错）
    NotFound(String),
    /// 应用层校验失败（如空白标题、优先级越界）
    Validation(String),
    /// SQLite 访问失败
    Db(rusqlite::Error),
}

impl fmt::Display for RepoError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RepoError::NotFound(id) => write!(f, "未找到任务 {id}"),
            RepoError::Validation(message) => write!(f, "{message}"),
            RepoError::Db(error) => write!(f, "数据库访问失败: {error}"),
        }
    }
}

impl std::error::Error for RepoError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            RepoError::Db(error) => Some(error),
            _ => None,
        }
    }
}

impl From<rusqlite::Error> for RepoError {
    fn from(error: rusqlite::Error) -> Self {
        RepoError::Db(error)
    }
}

/// 标题校验：design.md §3.1/§4.1，1~500 字符且不得为纯空白。
/// `description` 的 5000 字符上限属于 JSON Schema 层约束，不在存储层重复实现。
fn validate_title(title: &str) -> Result<(), RepoError> {
    if title.trim().is_empty() {
        return Err(RepoError::Validation("任务标题不能为空白".to_string()));
    }
    if title.chars().count() > 500 {
        return Err(RepoError::Validation(
            "任务标题不能超过 500 字符".to_string(),
        ));
    }
    Ok(())
}

/// 优先级校验：与 DDL 的 CHECK(priority IN (0,1,2,3)) 同源，提前给出应用层语义。
fn validate_priority(priority: i64) -> Result<(), RepoError> {
    if !(0..=3).contains(&priority) {
        return Err(RepoError::Validation(
            "优先级必须是 0=无、1=低、2=中、3=高之一".to_string(),
        ));
    }
    Ok(())
}

/// 当前 UTC epoch 毫秒（存储层所有时间列统一格式，design.md §3.1）。
fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_millis() as i64)
        .unwrap_or_default()
}

/// 行 → 任务模型（列序与 [`TASK_COLUMNS`] 严格一致）。
fn row_to_task(row: &Row<'_>) -> rusqlite::Result<Task> {
    Ok(Task {
        id: row.get(0)?,
        title: row.get(1)?,
        description: row.get(2)?,
        priority: row.get(3)?,
        due_at: row.get(4)?,
        status: row.get(5)?,
        completed_at: row.get(6)?,
        created_at: row.get(7)?,
        updated_at: row.get(8)?,
    })
}

/// 按 id 查询；不存在时返回 [`RepoError::NotFound`]。传入 `&Transaction`
/// 由 Deref 自动转为 `&Connection`，故读写共用同一查询函数。
fn query_by_id(conn: &Connection, id: &str) -> Result<Task, RepoError> {
    let sql = format!("SELECT {TASK_COLUMNS} FROM tasks WHERE id = ?1");
    conn.query_row(&sql, params![id], row_to_task)
        .optional()?
        .ok_or_else(|| RepoError::NotFound(id.to_string()))
}

/// 创建任务：UUIDv7 作 id（字典序≈创建时间序，design.md §2.4），返回完整对象。
pub fn create(conn: &Connection, input: NewTask) -> Result<Task, RepoError> {
    validate_title(&input.title)?;
    validate_priority(input.priority)?;

    let tx = conn.unchecked_transaction()?;
    let id = Uuid::now_v7().to_string();
    let now = now_ms();
    tx.execute(
        "INSERT INTO tasks (id, title, description, priority, due_at, status, completed_at, created_at, updated_at)
         VALUES (?1, ?2, ?3, ?4, ?5, 0, NULL, ?6, ?6)",
        params![
            id,
            input.title,
            input.description,
            input.priority,
            input.due_at,
            now
        ],
    )?;
    let task = query_by_id(&tx, &id)?;
    tx.commit()?;
    Ok(task)
}

/// 按 id 查询；不存在返回 [`RepoError::NotFound`]。
pub fn get(conn: &Connection, id: &str) -> Result<Task, RepoError> {
    query_by_id(conn, id)
}

/// 部分更新：只改动出现的字段；`description`/`due_at` 显式 null 表示清空。
/// 不提供 `status` 字段——完成状态只能走 [`complete`] 以保证幂等语义（design.md §4.4）。
pub fn update(conn: &Connection, id: &str, patch: TaskPatch) -> Result<Task, RepoError> {
    if let Some(title) = &patch.title {
        validate_title(title)?;
    }
    if let Some(priority) = patch.priority {
        validate_priority(priority)?;
    }

    let tx = conn.unchecked_transaction()?;
    let mut task = query_by_id(&tx, id)?;

    // 空补丁：语义为"全部不动"，直接返回当前对象，连 updated_at 也不变
    if patch.title.is_none()
        && patch.description.is_none()
        && patch.priority.is_none()
        && patch.due_at.is_none()
    {
        tx.commit()?;
        return Ok(task);
    }

    if let Some(title) = patch.title {
        task.title = title;
    }
    // 外层 None = 缺席不动；Some(None) = 清空；Some(Some(v)) = 赋值
    if let Some(description) = patch.description {
        task.description = description;
    }
    if let Some(priority) = patch.priority {
        task.priority = priority;
    }
    if let Some(due_at) = patch.due_at {
        task.due_at = due_at;
    }
    task.updated_at = now_ms();

    tx.execute(
        "UPDATE tasks SET title = ?1, description = ?2, priority = ?3, due_at = ?4, updated_at = ?5
         WHERE id = ?6",
        params![
            task.title,
            task.description,
            task.priority,
            task.due_at,
            task.updated_at,
            id
        ],
    )?;
    tx.commit()?;
    Ok(task)
}

/// 标记完成：status=1 + completed_at；**幂等**（design.md §4.4）——
/// 对已完成任务重复调用直接返回当前对象，不报错也不重写时间戳。
pub fn complete(conn: &Connection, id: &str) -> Result<Task, RepoError> {
    let tx = conn.unchecked_transaction()?;
    let task = query_by_id(&tx, id)?;
    if task.status == 1 {
        tx.commit()?;
        return Ok(task);
    }

    let now = now_ms();
    tx.execute(
        "UPDATE tasks SET status = 1, completed_at = ?1, updated_at = ?1 WHERE id = ?2",
        params![now, id],
    )?;
    let mut updated = task;
    updated.status = 1;
    updated.completed_at = Some(now);
    updated.updated_at = now;
    tx.commit()?;
    Ok(updated)
}


/// 撤销完成（design.md §5 第 3 项：GUI 取消勾选）：status=0 + completed_at 置空。
/// 与 [`complete`] 对称，对进行中任务同样幂等（不报错、不重复写库）。
pub fn reopen(conn: &Connection, id: &str) -> Result<Task, RepoError> {
    let tx = conn.unchecked_transaction()?;
    let task = query_by_id(&tx, id)?;
    if task.status == 0 {
        tx.commit()?;
        return Ok(task);
    }

    let now = now_ms();
    tx.execute(
        "UPDATE tasks SET status = 0, completed_at = NULL, updated_at = ?1 WHERE id = ?2",
        params![now, id],
    )?;
    let mut updated = task;
    updated.status = 0;
    updated.completed_at = None;
    updated.updated_at = now;
    tx.commit()?;
    Ok(updated)
}
/// 物理删除（design.md §4.5 不可幂等）：返回被删除任务的完整快照供 agent 汇报。
pub fn remove(conn: &Connection, id: &str) -> Result<Task, RepoError> {
    let tx = conn.unchecked_transaction()?;
    let task = query_by_id(&tx, id)?;
    tx.execute("DELETE FROM tasks WHERE id = ?1", params![id])?;
    tx.commit()?;
    Ok(task)
}

/// 固定排序分页查询（design.md §4.2，无 order 参数）：
/// `ORDER BY status ASC, (due_at IS NULL) ASC, due_at ASC, priority DESC, id ASC`。
/// `limit` 钳制到 [1, 200]，`total` 为当前筛选条件下总数。
pub fn list(conn: &Connection, query: TaskQuery) -> Result<TaskPage, RepoError> {
    let limit = i64::from(query.limit.clamp(1, 200));
    let offset = i64::from(query.offset);

    // 只读事务：COUNT 与 SELECT 取同一快照（WAL 下不阻塞其他连接写入）
    let tx = conn.unchecked_transaction()?;

    // 按出现的筛选项拼 WHERE，保持索引可用；占位符序号随筛选条件推进
    let mut values: Vec<i64> = Vec::new();
    let mut conditions: Vec<String> = Vec::new();
    if let Some(status) = query.status {
        values.push(status);
        conditions.push(format!("status = ?{}", values.len()));
    }
    if let Some(priority) = query.priority {
        values.push(priority);
        conditions.push(format!("priority = ?{}", values.len()));
    }
    let where_sql = if conditions.is_empty() {
        String::new()
    } else {
        format!(" WHERE {}", conditions.join(" AND "))
    };

    let count_sql = format!("SELECT COUNT(*) FROM tasks{where_sql}");
    let total: i64 = tx.query_row(&count_sql, params_from_iter(values.iter().copied()), |row| {
        row.get(0)
    })?;

    let list_sql = format!(
        "SELECT {TASK_COLUMNS} FROM tasks{where_sql} \
         ORDER BY status ASC, (due_at IS NULL) ASC, due_at ASC, priority DESC, id ASC \
         LIMIT ?{} OFFSET ?{}",
        values.len() + 1,
        values.len() + 2
    );
    // stmt 借用 tx 且带 Drop，必须先于 commit 结束借用（独立作用域）
    let mut tasks = Vec::new();
    {
        let mut stmt = tx.prepare(&list_sql)?;
        let mut rows = stmt.query(params_from_iter(
            values.into_iter().chain([limit, offset]),
        ))?;
        while let Some(row) = rows.next()? {
            tasks.push(row_to_task(&row)?);
        }
    }
    tx.commit()?;
    Ok(TaskPage {
        total: total as u64,
        tasks,
    })
}
