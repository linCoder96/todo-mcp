//! Tauri IPC 命令薄封装（design.md §6）：`State<Mutex<Connection>>` →
//! [`task_repo`]，出参 serde camelCase（[`TaskDto`]）；本模块只做协议适配
//! 与参数校验，不含 SQL。

use std::sync::{Mutex, MutexGuard};

use rusqlite::Connection;
use tauri::State;

use crate::db::task_repo::{self, NewTask, RepoError, TaskPatch, TaskQuery};
use crate::mcp::dto::{
    rfc3339_to_epoch_ms, CreateTaskInput, TaskDto, TaskPageDto, UpdateTaskInput,
};

/// GUI 进程内共享的单条长连接（design.md §1.2：GUI 一连接 + WAL）。
pub struct DbState(pub Mutex<Connection>);

/// 取连接；Mutex 中毒时给出明确中文错误而非 panic。
fn lock_db<'r>(state: &'r State<'r, DbState>) -> Result<MutexGuard<'r, Connection>, String> {
    state
        .0
        .lock()
        .map_err(|_| "数据库连接锁已损坏，请重启应用".to_string())
}

/// 业务错误映射为中文 message（design.md §4 统一错误语义）。
fn repo_error(error: RepoError) -> String {
    error.to_string()
}

/// 列表查询（筛选 tab + 翻页）。排序与 MCP 完全一致，由 task_repo 固定。
#[tauri::command]
pub fn list_tasks(
    state: State<'_, DbState>,
    status: Option<i64>,
    offset: Option<u32>,
) -> Result<TaskPageDto, String> {
    let conn = lock_db(&state)?;
    let query = TaskQuery {
        status,
        priority: None,
        limit: 200,
        offset: offset.unwrap_or(0),
    };
    let page = task_repo::list(&conn, query).map_err(repo_error)?;
    Ok(TaskPageDto {
        total: page.total,
        tasks: page.tasks.into_iter().map(TaskDto::from).collect(),
    })
}

/// 快捷新建：默认 priority=0、无到期（design.md §5 第 2 项）。
#[tauri::command]
pub fn create_task(
    state: State<'_, DbState>,
    input: CreateTaskInput,
) -> Result<TaskDto, String> {
    let conn = lock_db(&state)?;
    let due_at = match input.due_at {
        Some(text) => Some(rfc3339_to_epoch_ms(&text)?),
        None => None,
    };
    let new_task = NewTask {
        title: input.title,
        description: input.description,
        priority: input.priority.unwrap_or(0),
        due_at,
    };
    task_repo::create(&conn, new_task)
        .map_err(repo_error)
        .map(TaskDto::from)
}

/// 行内编辑保存：双层 Option 区分"缺席不动"与"显式置空"（design.md §4.3）。
#[tauri::command]
pub fn update_task(
    state: State<'_, DbState>,
    input: UpdateTaskInput,
) -> Result<TaskDto, String> {
    let conn = lock_db(&state)?;
    let due_at = match input.due_at {
        None => None,                        // 字段缺席 → 不动
        Some(None) => Some(None),            // 显式 null → 清除到期
        Some(Some(text)) => {
            Some(Some(rfc3339_to_epoch_ms(&text)?))
        }
    };
    let patch = TaskPatch {
        title: input.title,
        description: input.description,
        priority: input.priority,
        due_at,
    };
    task_repo::update(&conn, &input.id, patch)
        .map_err(repo_error)
        .map(TaskDto::from)
}

/// 勾选完成：与 MCP complete_task 同一个 repo 函数（design.md §5 第 3 项）。
#[tauri::command]
pub fn complete_task(state: State<'_, DbState>, id: String) -> Result<TaskDto, String> {
    let conn = lock_db(&state)?;
    task_repo::complete(&conn, &id)
        .map_err(repo_error)
        .map(TaskDto::from)
}

/// 取消勾选：status=0 + completed_at 置空（design.md §5 第 3 项）。
#[tauri::command]
pub fn reopen_task(state: State<'_, DbState>, id: String) -> Result<TaskDto, String> {
    let conn = lock_db(&state)?;
    task_repo::reopen(&conn, &id)
        .map_err(repo_error)
        .map(TaskDto::from)
}

/// 删除：返回被删除任务快照（design.md §4.5）。
#[tauri::command]
pub fn remove_task(state: State<'_, DbState>, id: String) -> Result<TaskDto, String> {
    let conn = lock_db(&state)?;
    task_repo::remove(&conn, &id)
        .map_err(repo_error)
        .map(TaskDto::from)
}
