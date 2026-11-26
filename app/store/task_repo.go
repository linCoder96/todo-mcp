package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound 是 id 不存在时的业务错误（GUI/MCP 均映射为中文 message）。
var ErrNotFound = errors.New("未找到任务")

func notFound(id string) error {
	return fmt.Errorf("%w %s", ErrNotFound, id)
}

// TaskRepo 是 tasks 表的唯一数据访问实现（design-viva.md §5.2 第 6 项）。
// GUI 与 MCP 两个进程各自持有一个 TaskRepo，共享同一份方法与语义。
type TaskRepo struct {
	db *sql.DB
	// 图片目录推导结果（images/<task_id>/，见 image.go）的缓存与错误。
	imagesOnce    sync.Once
	imagesDirPath string
	imagesErr     error
	// 文件目录推导结果（files/<task_id>/，见 file.go）的缓存与错误。
	filesOnce    sync.Once
	filesDirPath string
	filesErr     error
}

func NewTaskRepo(db *sql.DB) *TaskRepo {
	return &TaskRepo{db: db}
}

// TaskInput 是 Create 的入参；时间戳、status、completed_at 由本层生成。
type TaskInput struct {
	Title       string
	Description *string
	Priority    int
	DueAt       *int64
	// Values 是创建时的自定义字段值（协议形状，键=字段 id，见 value.go 头注释）。
	Values map[string]json.RawMessage
}

const taskColumns = `id, title, description, priority, due_at, status, archived_at, completed_at, created_at, updated_at`

// scanner 同时适配 *sql.Row 与 *sql.Rows。
type scanner interface {
	Scan(dest ...any) error
}

// scanTask 把一行扫描为 Task；可空列用 sql.Null* 承接 NULL。
// 注意：不加载自定义字段值与图片（调用方经 loadDetailsFor 补齐）。
func scanTask(row scanner) (*Task, error) {
	task := &Task{}
	var description sql.NullString
	var dueAt, archivedAt, completedAt sql.NullInt64
	err := row.Scan(
		&task.ID, &task.Title, &description, &task.Priority, &dueAt,
		&task.Status, &archivedAt, &completedAt, &task.CreatedAt, &task.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if description.Valid {
		task.Description = &description.String
	}
	if dueAt.Valid {
		task.DueAt = &dueAt.Int64
	}
	if archivedAt.Valid {
		task.ArchivedAt = &archivedAt.Int64
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Int64
	}
	return task, nil
}

func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("任务标题不能为空白")
	}
	if len([]rune(title)) > 500 {
		return fmt.Errorf("任务标题不能超过 500 字符")
	}
	return nil
}

func validatePriority(priority int) error {
	if priority < 0 || priority > 3 {
		return fmt.Errorf("优先级必须是 0=无、1=低、2=中、3=高之一")
	}
	return nil
}

// Create 新建任务：UUIDv7 作 id（字典序≈创建序，design-viva.md §5.2 第 5 项）。
func (r *TaskRepo) Create(in TaskInput) (*Task, error) {
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	if err := validatePriority(in.Priority); err != nil {
		return nil, err
	}

	identifier, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成 UUIDv7 失败: %w", err)
	}
	now := time.Now().UnixMilli()
	// 字段表在事务外查好（SetMaxOpenConns(1)：事务独占唯一连接，事务内
	// 不能再走 r.db 查询，否则死锁）。
	fields, err := r.fieldsByName()
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO tasks (id, title, description, priority, due_at, status, completed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
		identifier.String(), in.Title, in.Description, in.Priority, in.DueAt, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}
	if err := writeTaskValues(tx, identifier.String(), fields, in.Values); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(identifier.String())
}

// Get 按 id 查询（含自定义字段值）；不存在时返回 ErrNotFound 中文错误。
func (r *TaskRepo) Get(id string) (*Task, error) {
	task, err := scanTask(r.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// listQuery 组装筛选 WHERE 与参数，保持 status/priority 索引可用。
// Archived 三态见 TaskFilter 注释；nil 按未归档处理（归档任务从常规列表隐去）。
func listQuery(filter TaskFilter) (string, []any) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if filter.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, *filter.Status)
	}
	if filter.Priority != nil {
		conditions = append(conditions, "priority = ?")
		args = append(args, *filter.Priority)
	}
	if filter.Archived == nil || !*filter.Archived {
		conditions = append(conditions, "archived_at IS NULL")
	} else {
		conditions = append(conditions, "archived_at IS NOT NULL")
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// List 固定排序分页（design.md §4.2，design-viva.md §1.2 原样继承）：
// ORDER BY status ASC, (due_at IS NULL) ASC, due_at ASC, priority DESC, id ASC。
// limit 在代码层钳制到 [1, 200]，offset 负数按 0 处理。
func (r *TaskRepo) List(filter TaskFilter, limit, offset int) ([]*Task, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	where, args := listQuery(filter)
	args = append(args, limit, offset)
	rows, err := r.db.Query(
		`SELECT `+taskColumns+` FROM tasks`+where+
			` ORDER BY status ASC, (due_at IS NULL) ASC, due_at ASC, priority DESC, id ASC LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]*Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// Count 返回当前筛选条件下总数（MCP list_tasks 出参 total 所需）。
func (r *TaskRepo) Count(filter TaskFilter) (int, error) {
	where, args := listQuery(filter)
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM tasks`+where, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// DataVersion 返回 SQLite 的 PRAGMA data_version：同一连接上先后读到的两个值
// 不同，说明期间有**其它连接**提交过（本连接自己的提交不会改变它）。
// GUI 进程用它探测 MCP 进程对共享数据库文件的写入（任务、字段、字段值都算）。
// 连接被连接池回收重建后数值可能跳变，后果只是一次多余的重查。
func (r *TaskRepo) DataVersion() (int64, error) {
	var version int64
	if err := r.db.QueryRow(`PRAGMA data_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// Update 部分更新：patch 三态由 **T 表达；description/due_at 显式 null 清空。
// 空补丁直接返回当前对象，updated_at 也不变。status 字段刻意不开放。
func (r *TaskRepo) Update(id string, patch TaskPatch) (*Task, error) {
	if patch.Title != nil {
		if *patch.Title == nil {
			return nil, fmt.Errorf("任务标题不能显式置空")
		}
		if err := validateTitle(**patch.Title); err != nil {
			return nil, err
		}
	}
	if patch.Priority != nil {
		if *patch.Priority == nil {
			return nil, fmt.Errorf("优先级不能显式置空")
		}
		if err := validatePriority(**patch.Priority); err != nil {
			return nil, err
		}
	}
	// 字段表在事务外查好（SetMaxOpenConns(1)：事务内查 r.db 会死锁）。
	// 只有 patch.Values 涉及字段时才需要字段表。
	var fields map[string]*Field
	if patch.Values != nil {
		var err error
		fields, err = r.fieldsByName()
		if err != nil {
			return nil, err
		}
	}

	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}

	changed := false
	if patch.Title != nil {
		task.Title = **patch.Title
		changed = true
	}
	if patch.Description != nil {
		task.Description = *patch.Description
		changed = true
	}
	if patch.Priority != nil {
		task.Priority = **patch.Priority
		changed = true
	}
	if patch.DueAt != nil {
		if *patch.DueAt == nil {
			task.DueAt = nil
		} else {
			millis, err := rfc3339ToMs(**patch.DueAt)
			if err != nil {
				return nil, fmt.Errorf("到期时间格式错误: %w", err)
			}
			task.DueAt = &millis
		}
		changed = true
	}

	// 自定义字段值三态：patch.Values 缺席 → 不动；显式 null → 清空全部；
	// 内层某键 null → 清该项；有值 → 设置。解析校验与写入同事务。
	if patch.Values != nil {
		changed = true
		if *patch.Values == nil {
			if err := r.deleteAllFieldValues(tx, id); err != nil {
				return nil, err
			}
		} else {
			decoded, err := DecodeUpdateValues(fields, **patch.Values)
			if err != nil {
				return nil, err
			}
			if err := r.applyUpdateValues(tx, id, decoded); err != nil {
				return nil, err
			}
		}
	}

	if !changed {
		// 先回滚释放唯一连接，再查值（单连接下事务内不可查 r.db）。
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
			return nil, err
		}
		return task, nil
	}

	task.UpdatedAt = time.Now().UnixMilli()
	_, err = tx.Exec(
		`UPDATE tasks SET title = ?, description = ?, priority = ?, due_at = ?, updated_at = ? WHERE id = ?`,
		task.Title, task.Description, task.Priority, task.DueAt, task.UpdatedAt, id,
	)
	if err != nil {
		return nil, fmt.Errorf("更新任务失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// Complete 标记完成：status=1 + completed_at；幂等——已完成任务重复调用
// 直接返回当前对象，不报错也不重写时间戳（design.md §4.4）。
func (r *TaskRepo) Complete(id string) (*Task, error) {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	if task.Status == 1 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
			return nil, err
		}
		return task, nil
	}

	now := time.Now().UnixMilli()
	if _, err := tx.Exec(
		`UPDATE tasks SET status = 1, completed_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	); err != nil {
		return nil, fmt.Errorf("完成任务失败: %w", err)
	}
	task.Status = 1
	task.CompletedAt = &now
	task.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// Reopen 撤销完成（design-viva.md §7.1：GUI 取消勾选独有行为）：
// status=0 + completed_at 置空；对进行中任务同样幂等。
// 已归档任务一并退出归档（维持「归档必已完成」不变式）：archived_at 置空。
func (r *TaskRepo) Reopen(id string) (*Task, error) {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	if task.Status == 0 && task.ArchivedAt == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
			return nil, err
		}
		return task, nil
	}

	now := time.Now().UnixMilli()
	if _, err := tx.Exec(
		`UPDATE tasks SET status = 0, completed_at = NULL, archived_at = NULL, updated_at = ? WHERE id = ?`,
		now, id,
	); err != nil {
		return nil, fmt.Errorf("撤销完成失败: %w", err)
	}
	task.Status = 0
	task.CompletedAt = nil
	task.ArchivedAt = nil
	task.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// Remove 物理删除（不幂等）：返回删除前完整快照供 agent 汇报。
func (r *TaskRepo) Remove(id string) (*Task, error) {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	// 删除前先聚合字段值与图片，让快照完整（删除后值表/图片表已清空）。
	if err := r.loadDetailsFor(tx, []*Task{task}); err != nil {
		return nil, err
	}
	storedFiles, err := r.removeTaskFileRows(tx, id)
	if err != nil {
		return nil, err
	}
	if err := r.deleteFieldValues(tx, id); err != nil {
		return nil, err
	}
	storedImages, err := r.removeTaskImageRows(tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("删除任务失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// 文件系统没有事务：行删净提交后再删图片文件；删不掉的留待人工清理，
	// 不阻塞任务删除。
	if dir, dirErr := r.filesDir(); dirErr == nil {
		for _, stored := range storedFiles {
			_ = os.Remove(filepath.Join(dir, id, stored))
		}
		_ = os.Remove(filepath.Join(dir, id)) // 空目录顺手带走
	}
	if dir, dirErr := r.imagesDir(); dirErr == nil {
		for _, stored := range storedImages {
			_ = os.Remove(filepath.Join(dir, id, stored))
		}
		_ = os.Remove(filepath.Join(dir, id)) // 空目录顺手带走
	}
	return task, nil
}

// Archive 归档任务（2026-11-26 归档功能）：archived_at 记当前时间；幂等——
// 已归档任务重复调用直接返回当前对象，不重写时间戳。仅已完成任务可归档；
// 归档任务从常规列表隐去（TaskFilter.Archived 默认口径）。归档是否成功
// 与知识文档无关：调用方在归档成功后调用 GenerateArchiveDoc 重写文档。
func (r *TaskRepo) Archive(id string) (*Task, error) {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	if task.ArchivedAt != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
			return nil, err
		}
		return task, nil
	}
	if task.Status != 1 {
		return nil, fmt.Errorf("只有已完成的任务才能归档")
	}

	now := time.Now().UnixMilli()
	if _, err := tx.Exec(
		`UPDATE tasks SET archived_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	); err != nil {
		return nil, fmt.Errorf("归档任务失败: %w", err)
	}
	task.ArchivedAt = &now
	task.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// Unarchive 取消归档：archived_at 置空，任务回到「已完成」视图；对未归档
// 任务幂等。不动 status/completed_at（任务保持已完成状态）。
func (r *TaskRepo) Unarchive(id string) (*Task, error) {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	if task.ArchivedAt == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
			return nil, err
		}
		return task, nil
	}

	now := time.Now().UnixMilli()
	if _, err := tx.Exec(
		`UPDATE tasks SET archived_at = NULL, updated_at = ? WHERE id = ?`,
		now, id,
	); err != nil {
		return nil, fmt.Errorf("取消归档失败: %w", err)
	}
	task.ArchivedAt = nil
	task.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := r.loadDetailsFor(r.db, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}
