// Package store 是 GUI 与 MCP 两模式共享的唯一数据访问层（design-viva.md §3.3）。
// 本包只依赖标准库 database/sql 与纯 Go SQLite 驱动，不依赖任何 GUI API，
// 因此 MCP 进程（无窗口）也能解析同一数据库文件。
package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// Task 是 tasks 表的存储层模型。
//
// 时间列为 UTC epoch 毫秒整数；可空列用指针表达，nil → 序列化为 JSON null。
// 对外 RFC3339 字符串的转换在 dto 层完成（design-viva.md §5.2 第 2/4 项）。
type Task struct {
	ID          string
	Title       string
	Description *string
	Priority    int // 0=无 1=低 2=中 3=高
	DueAt       *int64
	Status      int // 0=进行中 1=已完成
	ArchivedAt  *int64
	CompletedAt *int64
	CreatedAt   int64
	UpdatedAt   int64
	// Values 是自定义字段值的协议形状（键=字段 id，值按字段类型）：
	// text/single_select → string；multi_select → JSON 数组；number → number；
	// date → RFC3339 字符串。由 loadValuesFor 聚合填充；未加载时为 nil。
	Values map[string]json.RawMessage
	// Images 是任务的图片附件元数据（2026-11-26），由 loadImagesFor 聚合填充，
	// 按显示顺序；无图/未加载时为 nil。文件本体在数据库目录 images/<id>/ 下。
	Images []TaskImage
	// Files 是任务的文件附件元数据（2026-11-26，见 file.go），由 loadFilesFor
	// 聚合填充，按显示顺序；无文件/未加载时为 nil。copy 模式文件本体在数据库
	// 目录 files/<id>/ 下；ref 模式只存 src_path 引用（源文件不拷贝）。
	Files []TaskFile
	// ArchivedAt 是归档时间（2026-11-26 归档功能）；NULL = 未归档。归档任务
	// 已从常规列表隐去（query.go 的 Archived 筛选），知识文档按它降序汇总。
}

// TaskPatch 部分更新的双层 Option（design-viva.md §5.2 第 3 项）：
//
//   - 外层 nil：字段缺席 → 保持原值；
//   - 外层非 nil、内层 nil：显式 null → 清空该字段；
//   - 两层均非 nil：赋新值。
//
// 注意：encoding/json 对 **T 的默认行为会把显式 null 直接置外层为 nil，
// 无法与"字段缺席"区分（M2 测试实测确认）。故这里实现自定义 UnmarshalJSON，
// 按 key 存在性区分三态；运行时字段仍保持 **T 形态。
type TaskPatch struct {
	Title       **string `json:"title,omitempty"`
	Description **string `json:"description,omitempty"`
	Priority    **int    `json:"priority,omitempty"`
	DueAt       **string `json:"due_at,omitempty"`
	// Values 是自定义字段值补丁（键=字段 id）：
	//   整个 values 缺席 → 全部自定义值不动；值为 null 的键 → 清空该字段；有值 → 设置。
	Values **map[string]json.RawMessage `json:"values,omitempty"`
}

// decodePatchField 按 key 存在性解码单个补丁字段：
// 缺席 → nil；显式 null → 外层非 nil、内层 nil；有值 → 两层均非 nil。
func decodePatchField[T any](raw json.RawMessage) (**T, error) {
	if raw == nil {
		return nil, nil
	}
	if string(raw) == "null" {
		return new(*T), nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return new(new(value)), nil
}

func (patch *TaskPatch) UnmarshalJSON(data []byte) error {
	var raw struct {
		Title       json.RawMessage `json:"title"`
		Description json.RawMessage `json:"description"`
		Priority    json.RawMessage `json:"priority"`
		DueAt       json.RawMessage `json:"due_at"`
		Values      json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	title, err := decodePatchField[string](raw.Title)
	if err != nil {
		return err
	}
	description, err := decodePatchField[string](raw.Description)
	if err != nil {
		return err
	}
	priority, err := decodePatchField[int](raw.Priority)
	if err != nil {
		return err
	}
	dueAt, err := decodePatchField[string](raw.DueAt)
	if err != nil {
		return err
	}

	// values 三态与其它补丁字段同构：缺席 → 外层 nil；显式 null → 外层非 nil 内层 nil；
	// 有值 → 两层均非 nil（内层 map[string]RawMessage，原始形状透传给 value.go 解析）。
	var values **map[string]json.RawMessage
	if raw.Values != nil {
		if string(raw.Values) == "null" {
			values = new(*map[string]json.RawMessage)
		} else {
			var valueMap map[string]json.RawMessage
			if err := json.Unmarshal(raw.Values, &valueMap); err != nil {
				return fmt.Errorf("values 必须是对象: %w", err)
			}
			values = new(new(valueMap))
		}
	}

	patch.Title = title
	patch.Description = description
	patch.Priority = priority
	patch.DueAt = dueAt
	patch.Values = values
	return nil
}

// TaskFilter 是 List 的筛选条件；nil 字段表示不过滤。
// Archived 三态（2026-11-26 归档）：nil = 未归档（默认口径，常规视图不含
// 归档任务）；&false = 只要未归档；&true = 只要已归档。
type TaskFilter struct {
	Status   *int
	Priority *int
	Archived *bool
}

// msToRFC3339 把 UTC epoch 毫秒转为 RFC3339 UTC 字符串。
func msToRFC3339(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

// rfc3339ToMs 解析任意合法 RFC3339（含时区偏移），归一化为 UTC epoch 毫秒。
func rfc3339ToMs(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, err
	}
	return parsed.UnixMilli(), nil
}
