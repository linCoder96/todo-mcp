// 图片附件（2026-11-26）：元数据入库（task_images，v3），文件本体在数据库目录
// 的 images/<task_id>/ 下。图片目录由本仓库连接推导（PRAGMA database_list），
// 因此 GUI 与 MCP 两个进程对同一数据库文件推导出同一目录，无需额外配置。
package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// 附件上限。
const (
	MaxImageBytes    = 20 << 20 // 单张 20 MiB
	MaxImagesPerTask = 20       // 每任务 20 张
)

// TaskImage 是 task_images 表的一行；Size 为文件字节数。
type TaskImage struct {
	ID         string
	TaskID     string
	FileName   string
	StoredName string
	Mime       string
	Size       int64
	Position   int
	CreatedAt  int64
}

// imagesDir 推导并缓存数据库文件配套的图片目录（PRAGMA 只读一次；
// GUI 打开详情抽屉时会连续用到，且与 MCP 进程对同一数据库文件推导出同一目录）。
func (r *TaskRepo) imagesDir() (string, error) {
	r.imagesOnce.Do(func() {
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
			r.imagesErr = fmt.Errorf("数据库未关联文件（内存库等），图片功能不可用")
			return
		}
		r.imagesDirPath = filepath.Join(filepath.Dir(file), "images")
	})
	return r.imagesDirPath, r.imagesErr
}

// ImagesDirFor 给出某数据库文件配套的图片目录（测试与文档用）。
func ImagesDirFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "images")
}

// sniffer MIME：只收 stdlib 能解码的三种（webp 无法解码，明确拒绝）。
func sniffImage(data []byte) (mime string, ext string, err error) {
	switch kind := http.DetectContentType(data); kind {
	case "image/png":
		return kind, ".png", nil
	case "image/jpeg":
		return kind, ".jpg", nil
	case "image/gif":
		return kind, ".gif", nil
	default:
		return "", "", fmt.Errorf("不支持的图片格式 %q；支持 png / jpg / gif", kind)
	}
}

// AddImageFile 从磁盘文件添加一张图片到任务：校验（存在、是图片、大小）→
// 拷入 images/<task_id>/<id>.<ext> → 插入元数据行。
func (r *TaskRepo) AddImageFile(taskID, path string) (*TaskImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	name := filepath.Base(path)
	return r.addImage(taskID, name, data)
}

// AddImageBytes 添加内存里的图片数据（GUI 剪贴板粘贴用）。
func (r *TaskRepo) AddImageBytes(taskID, name string, data []byte) (*TaskImage, error) {
	return r.addImage(taskID, name, data)
}

func (r *TaskRepo) addImage(taskID, name string, data []byte) (*TaskImage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("图片超过 %d MiB 上限", MaxImageBytes>>20)
	}
	mime, ext, err := sniffImage(data)
	if err != nil {
		return nil, err
	}
	if _, err := r.Get(taskID); err != nil {
		return nil, fmt.Errorf("任务不存在: %w", err)
	}
	dir, err := r.imagesDir()
	if err != nil {
		return nil, err
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM task_images WHERE task_id = ?`, taskID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxImagesPerTask {
		return nil, fmt.Errorf("每个任务最多 %d 张图片", MaxImagesPerTask)
	}

	identifier, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成 UUIDv7 失败: %w", err)
	}
	img := &TaskImage{
		ID:       identifier.String(),
		TaskID:   taskID,
		FileName: name,
		Mime:     mime,
		Size:     int64(len(data)),
		Position: count,
	}
	img.CreatedAt = time.Now().UnixMilli()
	img.StoredName = img.ID + ext

	target := filepath.Join(dir, taskID)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, fmt.Errorf("创建图片目录失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(target, img.StoredName), data, 0o644); err != nil {
		return nil, fmt.Errorf("写入图片文件失败: %w", err)
	}
	if _, err := r.db.Exec(
		`INSERT INTO task_images (id, task_id, file_name, stored_name, mime, size_bytes, position, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		img.ID, img.TaskID, img.FileName, img.StoredName, img.Mime, img.Size, img.Position, img.CreatedAt,
	); err != nil {
		_ = os.Remove(filepath.Join(target, img.StoredName)) // 入库失败不残留孤儿文件
		return nil, err
	}
	return img, nil
}

// ListImages 按显示顺序返回任务的图片（无图返回 nil）。
func (r *TaskRepo) ListImages(taskID string) ([]*TaskImage, error) {
	rows, err := r.db.Query(
		`SELECT id, task_id, file_name, stored_name, mime, size_bytes, position, created_at
		   FROM task_images WHERE task_id = ? ORDER BY position, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var images []*TaskImage
	for rows.Next() {
		img := &TaskImage{}
		if err := rows.Scan(&img.ID, &img.TaskID, &img.FileName, &img.StoredName,
			&img.Mime, &img.Size, &img.Position, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// ImageCount 返回任务的图片数（行内徽标用；任务无图返回 0）。
func (r *TaskRepo) ImageCount(taskID string) (int, error) {
	var count int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM task_images WHERE task_id = ?`, taskID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// ImagePath 图片文件的磁盘路径（GUI 读像素用；MCP 出参不含路径——文件在
// 本机数据库目录内，跨进程暴露路径没有意义）。
func (r *TaskRepo) ImagePath(taskID, storedName string) (string, error) {
	dir, err := r.imagesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, taskID, storedName), nil
}

// RemoveImage 删除一张图片：先删元数据行（事务），成功后再删磁盘文件；
// 文件已不在（手工清理过）视为成功。
func (r *TaskRepo) RemoveImage(taskID, imageID string) error {
	var stored string
	if err := r.db.QueryRow(
		`SELECT stored_name FROM task_images WHERE id = ? AND task_id = ?`, imageID, taskID,
	).Scan(&stored); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("图片不存在")
		}
		return err
	}
	if _, err := r.db.Exec(`DELETE FROM task_images WHERE id = ?`, imageID); err != nil {
		return err
	}
	dir, err := r.imagesDir()
	if err != nil {
		return nil // 元数据已删；目录未知则无从删文件，下次启动可清理
	}
	if err := os.Remove(filepath.Join(dir, taskID, stored)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除图片文件失败: %w", err)
	}
	return nil
}

// removeTaskImages 删除任务的全部图片行与文件（RemoveTask 级联用，调用方持事务：
// 行在事务内删，文件在事务提交后由调用方经 returned paths 删除）。
func (r *TaskRepo) removeTaskImageRows(tx *sql.Tx, taskID string) ([]string, error) {
	rows, err := tx.Query(`SELECT stored_name FROM task_images WHERE task_id = ?`, taskID)
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
		stored = append(stored, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM task_images WHERE task_id = ?`, taskID); err != nil {
		return nil, err
	}
	return stored, nil
}

// loadImagesFor 批量聚合任务的图片元数据到 task.Images（无图任务保持 nil）。
func (r *TaskRepo) loadImagesFor(tx queryer, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]any, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	placeholders := bytes.Repeat([]byte("?,"), len(ids))
	rows, err := tx.Query(
		`SELECT id, task_id, file_name, stored_name, mime, size_bytes, position, created_at
		   FROM task_images WHERE task_id IN (`+string(placeholders[:len(placeholders)-1])+`)
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
		img := &TaskImage{}
		var taskID string
		if err := rows.Scan(&img.ID, &taskID, &img.FileName, &img.StoredName,
			&img.Mime, &img.Size, &img.Position, &img.CreatedAt); err != nil {
			return err
		}
		task := index[taskID]
		if task == nil {
			continue
		}
		task.Images = append(task.Images, *img)
	}
	return rows.Err()
}

// loadDetailsFor 批量补全任务的展示细节：自定义字段值 + 图片元数据 + 文件元数据。
// 它是 loadValuesFor 的超集，任务查询路径统一改走这里（图片/文件无任务的查询是
// 一次带索引的空结果，开销可忽略）。
func (r *TaskRepo) loadDetailsFor(tx queryer, tasks []*Task) error {
	if err := r.loadValuesFor(tx, tasks); err != nil {
		return err
	}
	if err := r.loadImagesFor(tx, tasks); err != nil {
		return err
	}
	return r.loadFilesFor(tx, tasks)
}
