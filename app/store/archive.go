// 归档知识文档（2026-11-26 归档功能）：把全部已归档任务整理成一份 Markdown，
// 写入数据库目录的 docs/ 下（与 images/files 同法由 PRAGMA 推导，GUI 与 MCP
// 两个进程对同一数据库文件得到同一路径）。固定文件名全量覆盖：文档是归档
// 数据的派生物（幂等重算），不是增量日志。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArchiveDocFileName 归档知识文档的固定文件名。
const ArchiveDocFileName = "归档知识文档.md"

// archivePriorityLabels 优先级码值文案（0=无 1=低 2=中 3=高），与 GUI/MCP 一致。
var archivePriorityLabels = [...]string{"无", "低", "中", "高"}

// docsDir 推导并缓存数据库文件配套的文档目录 docs/（与 imagesDir 同法，
// 见 image.go；本功能写入频率低，不做一次成功后的强缓存，推导失败每次重试）。
func (r *TaskRepo) docsDir() (string, error) {
	var file string
	rows, err := r.db.Query("PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("推导文档目录失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, f string
		if err := rows.Scan(&seq, &name, &f); err == nil && name == "main" {
			file = f
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if file == "" {
		return "", fmt.Errorf("数据库未关联文件（内存库等），知识文档不可用")
	}
	return filepath.Join(filepath.Dir(file), "docs"), nil
}

// ArchiveDocPath 给出某数据库文件配套的知识文档路径（测试与 GUI 打开用）。
func ArchiveDocPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "docs", ArchiveDocFileName)
}

// GenerateArchiveDoc 把全部已归档任务整理成知识文档并覆盖写入 docs/ 下。
// 返回文档绝对路径。无归档任务时写一份只有标题的空文档（保持路径稳定，
// GUI「打开文档」不因空列表而失效）。字段值聚合失败不阻塞生成：该任务按
// 无自定义字段渲染，文档价值优先于完整快照。
func (r *TaskRepo) GenerateArchiveDoc() (string, error) {
	dir, err := r.docsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建文档目录失败: %w", err)
	}

	filter := true
	tasks, err := r.List(TaskFilter{Archived: &filter}, 1_000_000, 0)
	if err != nil {
		return "", fmt.Errorf("读取归档任务失败: %w", err)
	}
	// 归档时间降序（最新整理的在前），同刻按标题稳定。
	sort.Slice(tasks, func(i, j int) bool {
		li, lj := *tasks[i].ArchivedAt, *tasks[j].ArchivedAt
		if li != lj {
			return li > lj
		}
		return tasks[i].Title < tasks[j].Title
	})

	var b strings.Builder
	b.WriteString("# 归档任务知识文档\n\n")
	b.WriteString("> 本文档由待办应用自动生成：内容为全部已归档任务的汇总（归档时间降序）。\n")
	b.WriteString("> 重新归档 / 取消归档任意任务后自动重写，请勿手工编辑。\n\n")
	if len(tasks) == 0 {
		b.WriteString("暂无已归档任务。\n")
	} else {
		fmt.Fprintf(&b, "共 %d 条，生成于 %s。\n\n", len(tasks),
			time.Now().In(time.Local).Format("2006-01-02 15:04"))
		for _, task := range tasks {
			r.writeArchiveDocTask(&b, task)
		}
	}

	path := filepath.Join(dir, ArchiveDocFileName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入知识文档失败: %w", err)
	}
	return path, nil
}

// writeArchiveDocTask 渲染单个归档任务小节：标题（二级）+ 信息行 + 可选备注
// + 自定义字段。时间统一本地时区展示。
func (r *TaskRepo) writeArchiveDocTask(b *strings.Builder, task *Task) {
	fmt.Fprintf(b, "## %s\n\n", task.Title)
	info := []string{
		"完成于 " + time.UnixMilli(*task.CompletedAt).In(time.Local).Format("2006-01-02 15:04"),
		"归档于 " + time.UnixMilli(*task.ArchivedAt).In(time.Local).Format("2006-01-02 15:04"),
		"优先级：" + archivePriorityLabels[task.Priority],
	}
	if task.DueAt != nil {
		info = append(info, "截止 "+time.UnixMilli(*task.DueAt).In(time.Local).Format("2006-01-02 15:04"))
	}
	b.WriteString("- " + strings.Join(info, "，") + "\n")
	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		b.WriteString("- 备注：" + strings.TrimSpace(*task.Description) + "\n")
	}
	if len(task.Values) > 0 {
		labels := r.fieldValueLabels(task)
		for _, label := range labels {
			b.WriteString("- " + label + "\n")
		}
	}
	b.WriteString("\n")
}

// fieldValueText 把协议形状的字段值 JSON 渲染为展示文本（按字段类型）；
// 多选拼顿号，日期转本地日期，解析失败返回空串（该行不进文档）。
func fieldValueText(field *Field, raw json.RawMessage) string {
	switch field.Type {
	case FieldTypeNumber:
		var num float64
		if err := json.Unmarshal(raw, &num); err == nil {
			return formatNumber(num)
		}
	case FieldTypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			if ms, err := rfc3339ToMs(text); err == nil {
				return time.UnixMilli(ms).In(time.Local).Format("2006-01-02")
			}
		}
	case FieldTypeMultiSelect:
		var choices []string
		if err := json.Unmarshal(raw, &choices); err == nil {
			return strings.Join(choices, "、")
		}
	default: // text / single_select
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	return ""
}

// fieldValueLabels 把任务的自定义字段值渲染为「字段名：展示文本」行，按
// fields 表顺序（fieldsByName 已按 position 排序），值取不到展示文本时跳过。
// 需要 repo 查字段表，挂在 TaskRepo 上（writeArchiveDocTask 由
// GenerateArchiveDoc 持 repo 调用链传入）。
func (r *TaskRepo) fieldValueLabels(task *Task) []string {
	fields, err := r.fieldsByName()
	if err != nil {
		return nil
	}
	labels := make([]string, 0, len(task.Values))
	for _, field := range fields {
		raw, ok := task.Values[field.Name]
		if !ok {
			continue
		}
		if text := fieldValueText(field, raw); text != "" {
			labels = append(labels, field.Name+"："+text)
		}
	}
	return labels
}
