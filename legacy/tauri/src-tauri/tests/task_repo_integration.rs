//! M2 集成测试：真实临时文件库，不依赖网络、GUI 或真实 `%APPDATA%`。
//! 覆盖 design.md §8 M2 完成标准：CRUD、双层 Option 部分更新、幂等完成、
//! 不存在 id、固定排序分页、双连接交错/并发写零 SQLITE_BUSY。

use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

use rusqlite::params;
use todo_mcp_lib::db::open_at;
use todo_mcp_lib::db::task_repo::{
    self, NewTask, RepoError, Task, TaskPatch, TaskQuery,
};

/// 每个用例独占的临时文件库；退出时清理（含 WAL / SHM 旁文件）。
struct TempDb {
    path: PathBuf,
}

impl TempDb {
    fn new(tag: &str) -> Self {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let dir = std::env::temp_dir().join(format!(
            "todo-mcp-it-{}-{}-{}",
            tag,
            std::process::id(),
            nanos
        ));
        std::fs::create_dir_all(&dir).expect("创建临时目录失败");
        TempDb {
            path: dir.join("todo.db"),
        }
    }

    fn open(&self) -> rusqlite::Connection {
        open_at(&self.path).expect("打开临时库失败")
    }
}

impl Drop for TempDb {
    fn drop(&mut self) {
        if let Some(parent) = self.path.parent() {
            let _ = std::fs::remove_dir_all(parent);
        }
    }
}

fn new_task(title: &str) -> NewTask {
    NewTask {
        title: title.to_string(),
        description: None,
        priority: 0,
        due_at: None,
    }
}

fn all_tasks() -> TaskQuery {
    TaskQuery {
        status: None,
        priority: None,
        limit: 200,
        offset: 0,
    }
}

/// SQLITE_BUSY / SQLITE_LOCKED 均判为写冲突（rusqlite 对外错误文本可能是
/// "database is locked"，故按错误码判断，不依赖文案）。
fn is_busy(error: &rusqlite::Error) -> bool {
    match error.sqlite_error_code() {
        Some(rusqlite::ErrorCode::DatabaseBusy | rusqlite::ErrorCode::DatabaseLocked) => true,
        _ => {
            // 兜底：少数封装层只给文本（"database is locked" / "busy"）
            let text = error.to_string().to_lowercase();
            text.contains("busy") || text.contains("locked")
        }
    }
}

#[test]
fn open_sets_pragmas_and_migrates_schema() {
    let temp = TempDb::new("pragmas");
    let conn = temp.open();

    let journal: String = conn
        .query_row("PRAGMA journal_mode", [], |row| row.get(0))
        .unwrap();
    assert_eq!(journal, "wal", "journal_mode 必须为 WAL");

    let timeout: i64 = conn
        .query_row("PRAGMA busy_timeout", [], |row| row.get(0))
        .unwrap();
    assert_eq!(timeout, 5000, "busy_timeout 必须为 5000 毫秒");

    // PRAGMA synchronous 读取为整数档位：0=OFF 1=NORMAL 2=FULL 3=EXTRA
    let synchronous: i64 = conn
        .query_row("PRAGMA synchronous", [], |row| row.get(0))
        .unwrap();
    assert_eq!(synchronous, 1, "synchronous 必须为 NORMAL");

    let version: i64 = conn
        .query_row("PRAGMA user_version", [], |row| row.get(0))
        .unwrap();
    assert_eq!(version, 1, "user_version 必须为 1");

    let table_count: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'tasks'",
            [],
            |row| row.get(0),
        )
        .unwrap();
    assert_eq!(table_count, 1);

    for index in ["idx_tasks_due_at", "idx_tasks_status"] {
        let index_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?1",
                params![index],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(index_count, 1, "缺少索引 {index}");
    }

    // 重复打开（迁移门闸）必须幂等
    drop(conn);
    let conn = temp.open();
    let version: i64 = conn
        .query_row("PRAGMA user_version", [], |row| row.get(0))
        .unwrap();
    assert_eq!(version, 1);
}

#[test]
fn create_then_get_roundtrip() {
    let temp = TempDb::new("crud");
    let conn = temp.open();

    let mut input = new_task("写周报");
    input.description = Some("每周五前提交".to_string());
    input.priority = 2;
    input.due_at = Some(1_700_000_000_000);
    let created = task_repo::create(&conn, input).unwrap();

    assert_eq!(created.id.len(), 36); // UUIDv7 文本
    assert_eq!(created.title, "写周报");
    assert_eq!(created.description.as_deref(), Some("每周五前提交"));
    assert_eq!(created.priority, 2);
    assert_eq!(created.due_at, Some(1_700_000_000_000));
    assert_eq!(created.status, 0);
    assert_eq!(created.completed_at, None);
    assert_eq!(created.created_at, created.updated_at);

    let fetched = task_repo::get(&conn, &created.id).unwrap();
    assert_eq!(fetched, created);
}

#[test]
fn create_rejects_invalid_title_and_priority() {
    let temp = TempDb::new("validation");
    let conn = temp.open();

    for title in ["", "   "] {
        let error = task_repo::create(&conn, new_task(title)).unwrap_err();
        assert!(
            matches!(error, RepoError::Validation(_)),
            "空白标题应触发 Validation，实际 {error}"
        );
    }

    let error = task_repo::create(&conn, new_task(&"长".repeat(501))).unwrap_err();
    assert!(matches!(error, RepoError::Validation(_)));

    let mut bad_priority = new_task("合法标题");
    bad_priority.priority = 4;
    let error = task_repo::create(&conn, bad_priority).unwrap_err();
    assert!(matches!(error, RepoError::Validation(_)));

    // 边界：恰好 500 字符、最高优先级允许
    let mut boundary = new_task(&"标".repeat(500));
    boundary.priority = 3;
    let created = task_repo::create(&conn, boundary).unwrap();
    assert_eq!(created.title.chars().count(), 500);
    assert_eq!(created.priority, 3);
}

#[test]
fn update_partial_semantics_double_option() {
    let temp = TempDb::new("patch");
    let conn = temp.open();

    let mut input = new_task("初始标题");
    input.description = Some("初始描述".to_string());
    input.priority = 1;
    input.due_at = Some(1_700_000_000_000);
    let base = task_repo::create(&conn, input).unwrap();

    // ① 全部缺席：一字段不动，updated_at 也不变
    let after = task_repo::update(&conn, &base.id, TaskPatch::default()).unwrap();
    assert_eq!(after, base);

    // ② 只出现 title：其余保持，created_at 不变
    let after = task_repo::update(
        &conn,
        &base.id,
        TaskPatch {
            title: Some("新标题".to_string()),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(after.title, "新标题");
    assert_eq!(after.description, base.description);
    assert_eq!(after.priority, base.priority);
    assert_eq!(after.due_at, base.due_at);
    assert_eq!(after.status, base.status);
    assert_eq!(after.created_at, base.created_at);
    assert!(after.updated_at >= base.updated_at);

    // ③ description 显式 null → 清空；title 缺席 → 不动
    let after = task_repo::update(
        &conn,
        &base.id,
        TaskPatch {
            description: Some(None),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(after.description, None);
    assert_eq!(after.title, "新标题");
    assert_eq!(after.priority, base.priority);

    // ④ due_at 显式 null → 清除到期
    let after = task_repo::update(
        &conn,
        &base.id,
        TaskPatch {
            due_at: Some(None),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(after.due_at, None);

    // ⑤ 显式赋值路径：Some(Some(value)) / Some(value)
    let after = task_repo::update(
        &conn,
        &base.id,
        TaskPatch {
            description: Some(Some("二次描述".to_string())),
            due_at: Some(Some(1_800_000_000_000)),
            priority: Some(3),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(after.description.as_deref(), Some("二次描述"));
    assert_eq!(after.due_at, Some(1_800_000_000_000));
    assert_eq!(after.priority, 3);

    // 从库里重读，确认不是只改了内存对象
    assert_eq!(task_repo::get(&conn, &base.id).unwrap(), after);
}

#[test]
fn complete_is_idempotent() {
    let temp = TempDb::new("complete");
    let conn = temp.open();

    let created = task_repo::create(&conn, new_task("完成任务")).unwrap();
    let done = task_repo::complete(&conn, &created.id).unwrap();
    assert_eq!(done.status, 1);
    assert!(done.completed_at.is_some());
    assert!(done.updated_at >= created.updated_at);

    // 重复完成：completed_at / updated_at 都不得变化（幂等，不重复写库）
    let again = task_repo::complete(&conn, &created.id).unwrap();
    assert_eq!(again, done);
    assert_eq!(again.completed_at, done.completed_at);
    assert_eq!(again.updated_at, done.updated_at);

    // DDL 行级不变式：未完成的任务不可能带完成时间
    let unfinished = task_repo::create(&conn, new_task("未完成任务")).unwrap();
    assert_eq!(unfinished.completed_at, None);
    assert_eq!(unfinished.status, 0);
}


#[test]
fn reopen_clears_completion_and_is_idempotent() {
    let temp = TempDb::new("reopen");
    let conn = temp.open();

    let created = task_repo::create(&conn, new_task("可撤销完成")).unwrap();
    let done = task_repo::complete(&conn, &created.id).unwrap();
    let reopened = task_repo::reopen(&conn, &created.id).unwrap();
    assert_eq!(reopened.status, 0);
    assert_eq!(reopened.completed_at, None);
    assert!(reopened.updated_at >= done.updated_at);

    // 对进行中任务重复撤销：不报错、不重写时间戳
    let again = task_repo::reopen(&conn, &created.id).unwrap();
    assert_eq!(again, reopened);
    assert_eq!(again.updated_at, reopened.updated_at);

    // 不存在 id 同样明确报错
    assert!(matches!(
        task_repo::reopen(&conn, "01900000-0000-7000-8000-000000000000"),
        Err(RepoError::NotFound(_))
    ));
}
#[test]
fn missing_id_errors_on_all_operations() {
    let temp = TempDb::new("missing");
    let conn = temp.open();
    let ghost = "01900000-0000-7000-8000-000000000000";

    assert!(matches!(
        task_repo::get(&conn, ghost),
        Err(RepoError::NotFound(_))
    ));
    assert!(matches!(
        task_repo::update(&conn, ghost, TaskPatch::default()),
        Err(RepoError::NotFound(_))
    ));
    assert!(matches!(
        task_repo::complete(&conn, ghost),
        Err(RepoError::NotFound(_))
    ));
    assert!(matches!(
        task_repo::remove(&conn, ghost),
        Err(RepoError::NotFound(_))
    ));

    // 错误文案与 design.md §4 的统一中文语义一致
    assert_eq!(
        task_repo::get(&conn, ghost).unwrap_err().to_string(),
        format!("未找到任务 {ghost}")
    );
}

#[test]
fn remove_returns_snapshot_and_deletes() {
    let temp = TempDb::new("remove");
    let conn = temp.open();

    let created = task_repo::create(&conn, new_task("待删除")).unwrap();
    let snapshot = task_repo::remove(&conn, &created.id).unwrap();
    assert_eq!(snapshot, created);
    assert!(matches!(
        task_repo::get(&conn, &created.id),
        Err(RepoError::NotFound(_))
    ));
}

#[test]
fn list_fixed_order_pagination_and_filters() {
    let temp = TempDb::new("list");
    let conn = temp.open();

    // 造序数据（UUIDv7 单调，创建顺序即 id 字典序）：
    // 任务3(d=1000,p=3) / 任务2(d=1000,p=0) / 任务4(d=2000,p=3) /
    // 任务5(无到期,p=3) / 任务1(无到期,p=1) / 任务6(已完成，排最后)
    let seeds: [(&str, i64, Option<i64>, bool); 6] = [
        ("任务1", 1, None, false),
        ("任务2", 0, Some(1_000), false),
        ("任务3", 3, Some(1_000), false),
        ("任务4", 3, Some(2_000), false),
        ("任务5", 3, None, false),
        ("任务6", 2, Some(500), true),
    ];
    let mut ids = Vec::new();
    for (title, priority, due_at, complete) in seeds {
        let mut input = new_task(title);
        input.priority = priority;
        input.due_at = due_at;
        let created = task_repo::create(&conn, input).unwrap();
        if complete {
            task_repo::complete(&conn, &created.id).unwrap();
        }
        ids.push(created.id);
    }

    let page = task_repo::list(&conn, all_tasks()).unwrap();
    assert_eq!(page.total, 6);
    let titles: Vec<&str> = page.tasks.iter().map(|task| task.title.as_str()).collect();
    assert_eq!(
        titles,
        vec!["任务3", "任务2", "任务4", "任务5", "任务1", "任务6"]
    );

    // 分页：limit + offset，total 始终为筛选条件下总数
    let page1 = task_repo::list(
        &conn,
        TaskQuery {
            status: None,
            priority: None,
            limit: 2,
            offset: 0,
        },
    )
    .unwrap();
    assert_eq!(page1.total, 6);
    assert_eq!(page1.tasks.len(), 2);
    assert_eq!(page1.tasks[0].id, ids[2]);

    let page2 = task_repo::list(
        &conn,
        TaskQuery {
            status: None,
            priority: None,
            limit: 2,
            offset: 2,
        },
    )
    .unwrap();
    assert_eq!(page2.total, 6);
    assert_eq!(page2.tasks.len(), 2);
    assert_eq!(page2.tasks[0].id, ids[3]);
    assert_ne!(page1.tasks[1].id, page2.tasks[0].id);

    // 状态 / 优先级筛选，以及两条件叠加
    let active = task_repo::list(
        &conn,
        TaskQuery {
            status: Some(0),
            ..all_tasks()
        },
    )
    .unwrap();
    assert_eq!(active.total, 5);
    assert!(active.tasks.iter().all(|task| task.status == 0));

    let done = task_repo::list(
        &conn,
        TaskQuery {
            status: Some(1),
            ..all_tasks()
        },
    )
    .unwrap();
    assert_eq!(done.total, 1);

    let high = task_repo::list(
        &conn,
        TaskQuery {
            priority: Some(3),
            ..all_tasks()
        },
    )
    .unwrap();
    assert_eq!(high.total, 3);

    let both = task_repo::list(
        &conn,
        TaskQuery {
            status: Some(0),
            priority: Some(3),
            ..all_tasks()
        },
    )
    .unwrap();
    assert_eq!(both.total, 3);
    assert_eq!(
        both.tasks
            .iter()
            .map(|task| task.title.as_str())
            .collect::<Vec<_>>(),
        vec!["任务3", "任务4", "任务5"]
    );
}

#[test]
fn list_clamps_limit_to_200() {
    let temp = TempDb::new("clamp");
    let conn = temp.open();
    for index in 0..210 {
        task_repo::create(&conn, new_task(&format!("批量任务{index:03}"))).unwrap();
    }

    let capped = task_repo::list(
        &conn,
        TaskQuery {
            status: None,
            priority: None,
            limit: 9999,
            offset: 0,
        },
    )
    .unwrap();
    assert_eq!(capped.tasks.len(), 200, "limit 上限必须钳制为 200");
    assert_eq!(capped.total, 210);

    let floor = task_repo::list(
        &conn,
        TaskQuery {
            status: None,
            priority: None,
            limit: 0,
            offset: 0,
        },
    )
    .unwrap();
    assert_eq!(floor.tasks.len(), 1, "limit 下限必须钳制为 1");

    let tail = task_repo::list(
        &conn,
        TaskQuery {
            status: None,
            priority: None,
            limit: 200,
            offset: 200,
        },
    )
    .unwrap();
    assert_eq!(tail.tasks.len(), 10);
}

#[test]
fn two_connections_interleaved_writes_no_busy() {
    let temp = TempDb::new("concurrent");
    let conn_a = temp.open();
    let conn_b = temp.open();

    for conn in [&conn_a, &conn_b] {
        let timeout: i64 = conn
            .query_row("PRAGMA busy_timeout", [], |row| row.get(0))
            .unwrap();
        assert_eq!(timeout, 5000, "两连接必须统一 busy_timeout");
    }

    // 场景一：100 个写事务严格交错（偶数轮连接①，奇数轮连接②）
    let mut busy_errors = 0;
    let mut other_errors = Vec::new();
    for index in 0..100 {
        let conn = if index % 2 == 0 {
            &conn_a
        } else {
            &conn_b
        };
        match task_repo::create(conn, new_task(&format!("交错任务{index:03}"))) {
            Ok(_) => {}
            Err(RepoError::Db(error)) if is_busy(&error) => busy_errors += 1,
            Err(error) => other_errors.push(error.to_string()),
        }
    }
    assert_eq!(busy_errors, 0, "交错写出现 SQLITE_BUSY");
    assert!(other_errors.is_empty(), "交错写出现其他错误: {other_errors:?}");

    // 两连接各自视角：总数一致（WAL 提交即可见），数据零丢失
    let total_a = task_repo::list(&conn_a, all_tasks()).unwrap().total;
    let total_b = task_repo::list(&conn_b, all_tasks()).unwrap().total;
    assert_eq!(total_a, 100);
    assert_eq!(total_b, 100);

    // 场景二：两线程各持一条独立连接同时写，验证真实写-写冲突由 busy_timeout 吸收
    fn concurrent_writer(
        path: &std::path::Path,
        tag: &'static str,
    ) -> (usize, usize, Vec<String>) {
        let conn = open_at(path).expect("线程内打开连接失败");
        let (mut busy, mut errors) = (0, Vec::new());
        for index in 0..25 {
            match task_repo::create(&conn, new_task(&format!("并发-{tag}-{index:02}"))) {
                Ok(_) => {}
                Err(RepoError::Db(error)) if is_busy(&error) => busy += 1,
                Err(error) => errors.push(error.to_string()),
            }
        }
        (busy, errors.len(), errors)
    }
    let path_a = temp.path.clone();
    let path_b = temp.path.clone();
    let first = std::thread::spawn(move || concurrent_writer(&path_a, "甲"));
    let second = std::thread::spawn(move || concurrent_writer(&path_b, "乙"));
    let (busy_a, errors_a, messages_a) = first.join().expect("线程甲 panic");
    let (busy_b, errors_b, messages_b) = second.join().expect("线程乙 panic");
    assert_eq!(busy_a, 0, "线程甲出现 SQLITE_BUSY: {messages_a:?}");
    assert_eq!(busy_b, 0, "线程乙出现 SQLITE_BUSY: {messages_b:?}");
    assert_eq!(errors_a, 0, "线程甲出现其他错误: {messages_a:?}");
    assert_eq!(errors_b, 0, "线程乙出现其他错误: {messages_b:?}");

    let total_final = task_repo::list(&conn_a, all_tasks()).unwrap().total;
    assert_eq!(total_final, 150, "并发写后数据丢失");
    let sample: Task = task_repo::list(
        &conn_a,
        TaskQuery {
            status: None,
            priority: None,
            limit: 1,
            offset: 149,
        },
    )
    .unwrap()
    .tasks
    .remove(0);
    assert!(sample.title.starts_with("并发-"));
}
