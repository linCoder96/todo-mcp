// Package mcp 的 DTO（design.md §4 / design-viva.md §6.2 / 2026-11-24 自定义字段）。
// 入参只描述协议形状；业务校验在 tools.go，SQL 只存在于 app/store。
package mcp

import (
	"encoding/json"
	"strings"
	"time"

	"todo-mcp/app/store"
)

// createInput 对应 create_task 入参；values 为自定义字段值（对象键=字段 id）。
type createInput struct {
	Title       string                     `json:"title"`
	Description *string                    `json:"description,omitempty"`
	Priority    int                        `json:"priority,omitempty"`
	DueAt       *string                    `json:"due_at,omitempty"`
	Values      map[string]json.RawMessage `json:"values,omitempty"`
}

// listInput 对应 list_tasks 入参；缺省 limit/offset 由 handler 填默认值。
// filter/sort/group_by 直接复用 store 的类型（协议形状一致）。
type listInput struct {
	Status   *int             `json:"status,omitempty"`
	Priority *int             `json:"priority,omitempty"`
	Limit    *int             `json:"limit,omitempty"`
	Offset   *int             `json:"offset,omitempty"`
	Filter   []store.FilterCond `json:"filter,omitempty"`
	Sort     []store.SortKey  `json:"sort,omitempty"`
	GroupBy  *string          `json:"group_by,omitempty"`
}

// updateInput 必须自定义解码：store.TaskPatch 有自己的 UnmarshalJSON，
// 直接嵌入会让外层结构也继承它而丢掉 id。这里先用 map 取 id，再把完整
// JSON 交给 TaskPatch，复用其“缺席 / 显式 null / 新值”三态语义。
type updateInput struct {
	ID    string
	Patch store.TaskPatch
}

func (in *updateInput) UnmarshalJSON(data []byte) error {
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	var patch store.TaskPatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return err
	}
	in.ID = envelope.ID
	in.Patch = patch
	return nil
}

// idInput 对应 complete_task / remove_task 入参。
type idInput struct {
	ID string `json:"id"`
}

// taskOut 是任务对象出参；时间与可空字段统一为 string|null。
// Values 是自定义字段值（协议形状原样透传）；无值时空对象（非 null）。
type taskOut struct {
	ID          string                     `json:"id"`
	Title       string                     `json:"title"`
	Description *string                    `json:"description"`
	Priority    int                        `json:"priority"`
	DueAt       *string                    `json:"due_at"`
	Status      int                        `json:"status"`
	CompletedAt *string                    `json:"completed_at"`
	CreatedAt   string                     `json:"created_at"`
	UpdatedAt   string                     `json:"updated_at"`
	Values      map[string]json.RawMessage `json:"values"`
}

// listOut 对应 list_tasks 出参；total 是当前筛选条件下的总数。
// 分组模式 Tasks 为空、Groups 有值；平铺反之。
type listOut struct {
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
	Tasks  []taskOut  `json:"tasks"`
	Groups []groupOut `json:"groups,omitempty"`
}

// groupOut 是分组模式下的一个组。
type groupOut struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Total int       `json:"total"`
	Tasks []taskOut `json:"tasks"`
}

// newTaskOut 把 store 层的 epoch 毫秒时间转成 UTC RFC3339Nano 字符串。
func newTaskOut(task *store.Task) taskOut {
	values := task.Values
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	return taskOut{
		ID:          task.ID,
		Title:       task.Title,
		Description: task.Description,
		Priority:    task.Priority,
		DueAt:       millisToRFC3339(task.DueAt),
		Status:      task.Status,
		CompletedAt: millisToRFC3339(task.CompletedAt),
		CreatedAt:   formatMillis(task.CreatedAt),
		UpdatedAt:   formatMillis(task.UpdatedAt),
		Values:      values,
	}
}

func newTaskOuts(tasks []*store.Task) []taskOut {
	out := make([]taskOut, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, newTaskOut(task))
	}
	return out
}

// fieldOut 是字段对象出参（内置 + 自定义统一形状）。
type fieldOut struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Display  string   `json:"display"`
	Options  []string `json:"options,omitempty"`
	Position int      `json:"position"`
	Builtin  bool     `json:"builtin"`
}

// listFieldsOut 对应 list_fields 出参。
type listFieldsOut struct {
	Fields []fieldOut `json:"fields"`
}

// createFieldInput 对应 create_field 入参。
type createFieldInput struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Display  *string  `json:"display,omitempty"`
	Options  []string `json:"options,omitempty"`
	Position *int     `json:"position,omitempty"`
}

// updateFieldInput 对应 update_field 入参。
type updateFieldInput struct {
	ID       string   `json:"id"`
	Name     *string  `json:"name,omitempty"`
	Display  *string  `json:"display,omitempty"`
	Options  []string `json:"options,omitempty"` // nil=不改；传空对象数组不可能（[]string(nil)），需显式三态
	Position *int     `json:"position,omitempty"`
}

// newFieldOut 映射 store.Field 为协议出参。
func newFieldOut(field *store.Field, builtin bool) fieldOut {
	out := fieldOut{
		ID:       field.ID,
		Name:     field.Name,
		Type:     string(field.Type),
		Display:  string(field.Display),
		Position: field.Position,
		Builtin:  builtin,
	}
	if field.Type == store.FieldTypeSingleSelect || field.Type == store.FieldTypeMultiSelect {
		out.Options = field.Choices()
	}
	return out
}

// newFieldOuts 映射全部（含内置）。
func newFieldOuts(fields []*store.Field) []fieldOut {
	builtins := make([]fieldOut, 0, len(builtinFieldMeta))
	for _, meta := range builtinFieldMeta {
		builtins = append(builtins, newFieldOut(meta, true))
	}
	out := make([]fieldOut, 0, len(fields)+len(builtins))
	out = append(out, builtins...)
	for _, field := range fields {
		out = append(out, newFieldOut(field, false))
	}
	return out
}

// builtinFieldMeta 是内置字段的元数据（list_fields 出参用；store 的 builtinFields
// 不导出字段细节，这里声明协议侧副本，Type 用 store.FieldType 保证一致）。
var builtinFieldMeta = []*store.Field{
	{ID: "title", Name: "标题", Type: store.FieldTypeText, Display: store.DisplayPlain},
	{ID: "priority", Name: "优先级", Type: store.FieldTypeNumber, Display: store.DisplayPlain},
	{ID: "due_at", Name: "到期时间", Type: store.FieldTypeDate, Display: store.DisplayPlain},
	{ID: "status", Name: "状态", Type: store.FieldTypeNumber, Display: store.DisplayPlain},
	{ID: "created_at", Name: "创建时间", Type: store.FieldTypeDate, Display: store.DisplayPlain},
}

func millisToRFC3339(millis *int64) *string {
	if millis == nil {
		return nil
	}
	return new(time.UnixMilli(*millis).UTC().Format(time.RFC3339Nano))
}

func formatMillis(millis int64) string {
	return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)
}

// parseRFC3339 接受任意合法 RFC3339（含偏移），归一化为 UTC epoch 毫秒。
func parseRFC3339(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, err
	}
	return parsed.UnixMilli(), nil
}

// blankTitle / tooLong 是 schema minLength/maxLength 之外的权威应用校验：
// minLength=1 不拒绝纯空白，且 handler 不能假设客户端按 schema 计数字符。
func blankTitle(title string) bool {
	return strings.TrimSpace(title) == ""
}

func tooLong(value string, limit int) bool {
	return len([]rune(value)) > limit
}