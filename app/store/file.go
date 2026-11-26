// 文件附件（2026-11-26）：与图片附件（image.go）同构的通用文件附件，两种模式：
//   - 上传拷贝（copy）：文件拷入数据库目录的 files/<task_id>/ 下，与源文件解耦；
//   - 复制引用地址（ref）：只存源文件路径，不拷贝文件；源文件被移动/删除后不可用。
//
// 元数据入库（task_files，v4），文件目录由本仓库连接推导（PRAGMA database_list），
// 因此 GUI 与 MCP 两个进程对同一数据库文件推导出同一目录，无需额外配置。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 附件上限。
const (
	MaxFileBytes    = 50 << 20 // 单文件 50 MiB
	MaxFilesPerTask = 20       // 每任务 20 个
)

// FileKind 是文件附件的存储模式。
type FileKind string

const (
	FileCopy FileKind = "copy" // 文件已拷贝进应用数据目录
	FileRef  FileKind = "ref"  // 只存源路径引用，文件本体不动
)

// TaskFile 是 task_files 表的一行；Size 为文件字节数。
type TaskFile struct {
	ID         string
	TaskID     string
	FileName   string // 原始文件名（展示用）
	Kind       FileKind
	StoredName string // copy 模式磁盘文件名 <id><ext>；ref 模式空
	SrcPath    string // ref 模式源文件绝对路径；copy 模式空
	Size       int64
	Position   int
	CreatedAt  int64
}

// filesDir 推导并缓存数据库文件配套的文件目录（PRAGMA 只读一次；
// 与 imagesDir 同法，见 image.go）。
func (r *TaskRepo) filesDir() (string, error) {
	r.filesOnce.Do(func() {
		var file string
		rows, err := r.db.Query("PRAGMA database_list")
		if err == nil {
			for rows.Next() {
				var seq int
				var name, f string
				if err := rows.Scan(&seq, &name, &f); err == nil && name == "main" {
					file = f
				}
			}
			rows.Close()
		}
		if file == "" {
			r.filesErr = fmt.Errorf("数据库未关联文件（内存库等），文件附件不可用")
			return
		}
		r.filesDirPath = filepath.Join(filepath.Dir(file), "files")
	})
	return r.filesDirPath, r.filesErr
}

// FilesDirFor 给出某数据库文件配套的文件目录（测试与文档用）。
func FilesDirFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "files")
}

// AddFileCopy 从磁盘文件拷贝一份进应用数据目录（上传模式）：
// 校验（存在、非空、大小）→ 拷入 files/<task_id>/<id><ext> → 插入元数据行。
func (r *TaskRepo) AddFileCopy(taskID, path string) (*TaskFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	name := filepath.Base(path)
	return r.addFile(taskID, name, FileCopy, path, data, int64(len(data)))
}

// AddFileRef 只记录源文件路径（引用模式，不拷贝文件）。
func (r *TaskRepo) AddFileRef(taskID, path string) (*TaskFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("来源文件不存在: %w", err)
		}
		return nil, fmt.Errorf("读取来源文件失败: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("引用地址必须是文件，收到目录 %q", path)
	}
	// 引用不拷贝文件，但记录源文件当前大小（展示用；源文件后续变化不追随）。
	return r.addFile(taskID, filepath.Base(path), FileRef, path, nil, info.Size())
}

func (r *TaskRepo) addFile(taskID, name string, kind FileKind, srcPath string, data []byte, size int64) (*TaskFile, error) {
	if kind == FileCopy && len(data) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	if kind == FileCopy && len(data) > MaxFileBytes {
		return nil, fmt.Errorf("文件超过 %d MiB 上限", MaxFileBytes>>20)
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("文件名不能为空")
	}
	if _, err := r.Get(taskID); err != nil {
		return nil, fmt.Errorf("任务不存在: %w", err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM task_files WHERE task_id = ?`, taskID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxFilesPerTask {
		return nil, fmt.Errorf("每个任务最多 %d 个文件", MaxFilesPerTask)
	}
	identifier, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成 UUIDv7 失败: %w", err)
	}
	file := &TaskFile{
		ID:       identifier.String(),
		TaskID:   taskID,
		FileName: name,
		Kind:     kind,
		Size:     size,
		Position: count,
	}
	if kind == FileRef {
		file.SrcPath = srcPath
	}
	file.CreatedAt = time.Now().UnixMilli()
	var target string
	if kind == FileCopy {
		dir, err := r.filesDir()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(dir, taskID), 0o755); err != nil {
			return nil, fmt.Errorf("创建文件目录失败: %w", err)
		}
		file.StoredName = file.ID + filepath.Ext(name)
		target = filepath.Join(dir, taskID, file.StoredName)
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return nil, fmt.Errorf("写入文件失败: %w", err)
		}
	}
	if _, err := r.db.Exec(
		`INSERT INTO task_files (id, task_id, file_name, kind, stored_name, src_path, size_bytes, position, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		file.ID, file.TaskID, file.FileName, string(file.Kind), file.StoredName, file.SrcPath,
		file.Size, file.Position, file.CreatedAt,
	); err != nil {
		if target != "" {
			_ = os.Remove(target) // 入库失败不残留孤儿文件
		}
		return nil, err
	}
	return file, nil
}

// ListFiles 按显示顺序返回任务的文件（无文件返回 nil）。
func (r *TaskRepo) ListFiles(taskID string) ([]*TaskFile, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, file_name, kind, stored_name, src_path, size_bytes, position, created_at
		   FROM task_files WHERE task_id = ? ORDER BY position, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []*TaskFile
	for rows.Next() {
		file := &TaskFile{}
		if err := rows.Scan(&file.ID, &file.TaskID, &file.FileName, &file.Kind,
			&file.StoredName, &file.SrcPath, &file.Size, &file.Position, &file.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// FileCount 返回任务的文件数（行内徽标用；任务无文件返回 0）。
func (r *TaskRepo) FileCount(taskID string) (int, error) {
	var count int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM task_files WHERE task_id = ?`, taskID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// FilePath 返回 copy 模式文件的磁盘路径（GUI 打开用）；ref 模式无重复文件，
// 返回源路径（SrcPath），方便统一调用。
func (r *TaskRepo) FilePath(taskID, storedName string) (string, error) {
	dir, err := r.filesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, taskID, storedName), nil
}

// RemoveFile 删除一个文件附件：先删元数据行（事务），copy 模式成功后再删磁盘
// 文件；文件已不在（手工清理过）视为成功。ref 模式不碰源文件。
func (r *TaskRepo) RemoveFile(taskID, fileID string) error {
	var stored string
	var kind string
	if err := r.db.QueryRow(
		`SELECT stored_name, kind FROM task_files WHERE id = ? AND task_id = ?`, fileID, taskID,
	).Scan(&stored, &kind); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("文件不存在")
		}
		return err
	}
	if _, err := r.db.Exec(`DELETE FROM task_files WHERE id = ?`, fileID); err != nil {
		return err
	}
	if kind == string(FileCopy) {
		dir, err := r.filesDir()
		if err != nil {
			return nil // 元数据已删；目录未知则无从删文件，下次启动可清理
		}
		if err := os.Remove(filepath.Join(dir, taskID, stored)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除文件失败: %w", err)
		}
	}
	return nil
}

// removeTaskFileRows 删除任务的全部文件行并返回 copy 模式的磁盘文件名
// （RemoveTask 级联用，调用方持事务：行在事务内删，文件在事务提交后删除）。
func (r *TaskRepo) removeTaskFileRows(tx *sql.Tx, taskID string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT stored_name FROM task_files WHERE task_id = ? AND kind = ?`, taskID, string(FileCopy))
	if err != nil {
		return nil, err
	}
	var stored []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		if s != "" {
			stored = append(stored, s)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM task_files WHERE task_id = ?`, taskID); err != nil {
		return nil, err
	}
	return stored, nil
}

// loadFilesFor 批量聚合任务的文件元数据到 task.Files（无文件任务保持 nil）。
func (r *TaskRepo) loadFilesFor(tx queryer, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]any, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	placeholders := strings.Repeat("?,", len(ids))
	rows, err := tx.Query(
		`SELECT id, task_id, file_name, kind, stored_name, src_path, size_bytes, position, created_at
		   FROM task_files WHERE task_id IN (`+strings.TrimSuffix(placeholders, ",")+`)
		  ORDER BY position, id`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		index[task.ID] = task
	}
	for rows.Next() {
		file := &TaskFile{}
		var taskID string
		if err := rows.Scan(&file.ID, &taskID, &file.FileName, &file.Kind,
			&file.StoredName, &file.SrcPath, &file.Size, &file.Position, &file.CreatedAt); err != nil {
			return err
		}
		task := index[taskID]
		if task == nil {
			continue
		}
		task.Files = append(task.Files, *file)
	}
	return rows.Err()
}
