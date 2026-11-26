// 自定义字段的存储模型、校验与 CRUD（design 2026-11-24-custom-fields §4）。
// 值表 EAV 采用 text_value / num_value 双列：text/single_select/multi_select
// 落 text_value（multi_select 为 JSON 数组），number/date 落 num_value
// （date 为 UTC epoch 毫秒，与 due_at 口径一致），保证数字排序天然正确。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FieldType 是字段的存储类型（创建后不可变）。
type FieldType string

const (
	FieldTypeText         FieldType = "text"
	FieldTypeSingleSelect FieldType = "single_select"
	FieldTypeMultiSelect  FieldType = "multi_select"
	FieldTypeNumber       FieldType = "number"
	FieldTypeDate         FieldType = "date"
)

// FieldDisplay 是 number 字段的显示样式，仅影响渲染不影响存储。
type FieldDisplay string

const (
	DisplayPlain    FieldDisplay = "plain"
	DisplayPercent  FieldDisplay = "percent"
	DisplayCurrency FieldDisplay = "currency"
	DisplayProgress FieldDisplay = "progress"
)

// optionsJSON 缓存 fields.options 的解析结果（choices 或 nil）。
type optionsJSON struct {
	choices []string
}

// Field 是 fields 表的存储层模型。
type Field struct {
	ID        string
	Name      string
	Type      FieldType
	Display   FieldDisplay
	Options   *optionsJSON // nil = 非选择类
	Position  int
	CreatedAt int64
	UpdatedAt int64
}

// Choices 返回选择类字段的选项；非选择类返回 nil。
func (f *Field) Choices() []string {
	if f.Options == nil {
		return nil
	}
	return f.Options.choices
}

// builtinFields 是 list_fields 返回的内置字段（不可删改、不可作为自定义值键）。
var builtinFields = []Field{
	{ID: "title", Name: "标题", Type: FieldTypeText, Display: DisplayPlain},
	{ID: "description", Name: "备注", Type: FieldTypeText, Display: DisplayPlain},
	{ID: "priority", Name: "优先级", Type: FieldTypeSingleSelect, Display: DisplayPlain},
	{ID: "due_at", Name: "到期时间", Type: FieldTypeDate, Display: DisplayPlain},
	{ID: "status", Name: "状态", Type: FieldTypeSingleSelect, Display: DisplayPlain},
	{ID: "archived_at", Name: "归档时间", Type: FieldTypeDate, Display: DisplayPlain},
	{ID: "completed_at", Name: "完成时间", Type: FieldTypeDate, Display: DisplayPlain},
}

// BuiltinFieldID 判断某字段键是否为内置字段（键值空间：内置名 + 自定义 UUID）。
func BuiltinFieldID(id string) bool {
	for i := range builtinFields {
		if builtinFields[i].ID == id {
			return true
		}
	}
	return false
}

// FieldInput 是 CreateField 的入参。
type FieldInput struct {
	Name     string
	Type     FieldType
	Display  FieldDisplay
	Options  []string // 选择类；其它类型忽略
	Position int
}

// FieldPatch 是 UpdateField 的可改字段（双层 Option 三态，与 TaskPatch 同构）。
// 仅 name / display / options（选择类）/ position 可改；type 创建后不可变。
type FieldPatch struct {
	Name     **string       `json:"name,omitempty"`
	Display  **FieldDisplay `json:"display,omitempty"`
	Options  **[]string     `json:"options,omitempty"` // 显式 null 对非选择类禁止
	Position **int          `json:"position,omitempty"`
}

const fieldColumns = `id, name, type, display, options, position, created_at, updated_at`
const tfvColumns = `task_id, field_id, text_value, num_value`

// 上限（design 2026-11-24-custom-fields §4.1）。
const (
	MaxFields          = 20
	MaxFieldNameLen    = 50
	MaxOptions         = 100
	MaxOptionLen       = 50
	MaxFieldValueLen   = 500
	MaxFilterCondCount = 10
	MaxSortKeyCount    = 3
)

// ErrFieldNotFound 是字段不存在时的业务错误。
var ErrFieldNotFound = errors.New("未找到字段")

func fieldNotFound(id string) error {
	return fmt.Errorf("%w %s", ErrFieldNotFound, id)
}

// scanField 把一行扫描为 *Field；options 列 NULL 表示非选择类。
func scanField(row scanner) (*Field, error) {
	field := &Field{}
	var options sql.NullString
	var err error
	err = row.Scan(&field.ID, &field.Name, &field.Type, &field.Display, &options,
		&field.Position, &field.CreatedAt, &field.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFieldNotFound
		}
		return nil, err
	}
	if options.Valid {
		field.Options = &optionsJSON{}
		if err := json.Unmarshal([]byte(options.String), &field.Options.choices); err != nil {
			return nil, fmt.Errorf("解析字段选项失败: %w", err)
		}
	}
	return field, nil
}

// validateChoices 校验选项：非空、数量与单个长度上限。
func validateChoices(choices []string) error {
	if len(choices) == 0 {
		return fmt.Errorf("选择类字段至少需要一个选项")
	}
	if len(choices) > MaxOptions {
		return fmt.Errorf("选项数量不能超过 %d", MaxOptions)
	}
	for _, choice := range choices {
		trimmed := strings.TrimSpace(choice)
		if trimmed == "" {
			return fmt.Errorf("选项不能为空白")
		}
		if len([]rune(trimmed)) > MaxOptionLen {
			return fmt.Errorf("选项不能超过 %d 字符", MaxOptionLen)
		}
	}
	return nil
}

// userFieldCount 返回自定义字段总数（用于上限校验）。
func (r *TaskRepo) userFieldCount() (int, error) {
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM fields`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// validateFieldName 校验字段名：非空白、≤50 字符、未与现有字段重名。
func (r *TaskRepo) validateFieldName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("字段名不能为空白")
	}
	if len([]rune(trimmed)) > MaxFieldNameLen {
		return fmt.Errorf("字段名不能超过 %d 字符", MaxFieldNameLen)
	}
	if BuiltinFieldID(trimmed) {
		return fmt.Errorf("字段名 %q 与内置字段冲突", trimmed)
	}
	var exists int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM fields WHERE name = ?`, trimmed).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("字段名 %q 已存在", trimmed)
	}
	return nil
}

// validateFieldInput 校验 CreateField 入参（全量）。
func (r *TaskRepo) validateFieldInput(in FieldInput) error {
	if err := r.validateFieldName(in.Name); err != nil {
		return err
	}
	switch in.Type {
	case FieldTypeText, FieldTypeDate:
		// 无附加校验
	case FieldTypeSingleSelect, FieldTypeMultiSelect:
		if err := validateChoices(in.Options); err != nil {
			return err
		}
	case FieldTypeNumber:
		switch in.Display {
		case DisplayPlain, DisplayPercent, DisplayCurrency, DisplayProgress:
		default:
			return fmt.Errorf("数字字段 display 必须是 plain/percent/currency/progress 之一")
		}
	default:
		return fmt.Errorf("不支持字段类型 %q", in.Type)
	}
	return nil
}

// CreateField 新建字段：UUIDv7 作 id，position 缺省取当前最大 + 1。
func (r *TaskRepo) CreateField(in FieldInput) (*Field, error) {
	// 空 display 归一化为 plain（CHECK 约束只接受显式枚举值）。
	if in.Display == "" {
		in.Display = DisplayPlain
	}
	if err := r.validateFieldInput(in); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)

	// 数量上限、重名、position 全部在事务外查询（SetMaxOpenConns(1) 下单连接
	// 事务内无法再查 r.db——会自锁）。唯一约束索引兜底并发重名。
	fields, err := r.ListFields()
	if err != nil {
		return nil, err
	}
	if len(fields) >= MaxFields {
		return nil, fmt.Errorf("自定义字段数量已达上限 %d", MaxFields)
	}
	for _, field := range fields {
		if field.Name == name {
			return nil, fmt.Errorf("字段名 %q 已存在", name)
		}
	}
	position := in.Position
	if position == 0 {
		position = len(fields) + 1
	}

	identifier, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("生成 UUIDv7 失败: %w", err)
	}
	now := time.Now().UnixMilli()

	var options any
	switch in.Type {
	case FieldTypeSingleSelect, FieldTypeMultiSelect:
		encoded, err := json.Marshal(in.Options)
		if err != nil {
			return nil, err
		}
		options = string(encoded)
	}
	_, err = r.db.Exec(
		`INSERT INTO fields (id, name, type, display, options, position, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		identifier.String(), name, in.Type, in.Display, options, position, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("字段名 %q 已存在", name)
		}
		return nil, fmt.Errorf("创建字段失败: %w", err)
	}
	return r.GetField(identifier.String())
}

// GetField 按 id 查询字段；不存在返回 ErrFieldNotFound。
func (r *TaskRepo) GetField(id string) (*Field, error) {
	field, err := scanField(r.db.QueryRow(`SELECT `+fieldColumns+` FROM fields WHERE id = ?`, id))
	if errors.Is(err, ErrFieldNotFound) {
		return nil, fieldNotFound(id)
	}
	return field, err
}

// ListFields 返回全部自定义字段，按 position ASC, created_at ASC 排序。
func (r *TaskRepo) ListFields() ([]*Field, error) {
	rows, err := r.db.Query(`SELECT ` + fieldColumns + ` FROM fields ORDER BY position ASC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make([]*Field, 0)
	for rows.Next() {
		field, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

// UpdateField 部分更新字段；仅 name/display/options/position 可改。
func (r *TaskRepo) UpdateField(id string, patch FieldPatch) (*Field, error) {
	// 无事务：单连接下事务内不可查 r.db（自锁），而更新需要先读旧字段、
	// 校验重名（查库）。单条 UPDATE 本身原子，无需事务包裹。
	field, err := scanField(r.db.QueryRow(`SELECT `+fieldColumns+` FROM fields WHERE id = ?`, id))
	if errors.Is(err, ErrFieldNotFound) {
		return nil, fieldNotFound(id)
	}
	if err != nil {
		return nil, err
	}

	changed := false
	if patch.Name != nil {
		if *patch.Name == nil {
			return nil, fmt.Errorf("字段名不能显式置空")
		}
		trimmed := strings.TrimSpace(**patch.Name)
		if strings.TrimSpace(trimmed) == "" {
			return nil, fmt.Errorf("字段名不能为空白")
		}
		if len([]rune(trimmed)) > MaxFieldNameLen {
			return nil, fmt.Errorf("字段名不能超过 %d 字符", MaxFieldNameLen)
		}
		if BuiltinFieldID(trimmed) {
			return nil, fmt.Errorf("字段名 %q 与内置字段冲突", trimmed)
		}
		// 重名校验需排除自身（允许改名但保持原值）
		if trimmed != field.Name {
			var clash int
			if err := r.db.QueryRow(
				`SELECT COUNT(*) FROM fields WHERE name = ? AND id != ?`, trimmed, id,
			).Scan(&clash); err != nil {
				return nil, err
			}
			if clash > 0 {
				return nil, fmt.Errorf("字段名 %q 已存在", trimmed)
			}
		}
		field.Name = trimmed
		changed = true
	}
	if patch.Display != nil {
		if *patch.Display == nil {
			return nil, fmt.Errorf("display 不能显式置空")
		}
		display := **patch.Display
		switch field.Type {
		case FieldTypeNumber:
			switch display {
			case DisplayPlain, DisplayPercent, DisplayCurrency, DisplayProgress:
			default:
				return nil, fmt.Errorf("数字字段 display 必须是 plain/percent/currency/progress 之一")
			}
		default:
			if display != DisplayPlain {
				return nil, fmt.Errorf("只有数字字段可以设置显示样式")
			}
		}
		field.Display = display
		changed = true
	}
	if patch.Options != nil {
		if *patch.Options == nil {
			return nil, fmt.Errorf("options 不能显式置空；非选择类字段无选项")
		}
		if field.Type != FieldTypeSingleSelect && field.Type != FieldTypeMultiSelect {
			return nil, fmt.Errorf("只有选择类字段可以修改选项")
		}
		if err := validateChoices(**patch.Options); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(**patch.Options)
		if err != nil {
			return nil, err
		}
		encodedStr := string(encoded)
		if field.Options == nil || encodedStr != field.mustEncodeOptions() {
			field.Options = &optionsJSON{choices: **patch.Options}
			changed = true
		}
	}
	if patch.Position != nil {
		if *patch.Position == nil {
			return nil, fmt.Errorf("position 不能显式置空")
		}
		position := **patch.Position
		if position < 0 {
			return nil, fmt.Errorf("position 不能为负")
		}
		field.Position = position
		changed = true
	}

	if !changed {
		return field, nil
	}

	field.UpdatedAt = time.Now().UnixMilli()
	var options any
	if field.Options != nil {
		encoded, _ := json.Marshal(field.Options.choices)
		options = string(encoded)
	}
	_, err = r.db.Exec(
		`UPDATE fields SET name = ?, display = ?, options = ?, position = ?, updated_at = ? WHERE id = ?`,
		field.Name, field.Display, options, field.Position, field.UpdatedAt, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("字段名 %q 已存在", field.Name)
		}
		return nil, fmt.Errorf("更新字段失败: %w", err)
	}
	return field, nil
}

// mustEncodeOptions 编码当前选项用于比较（仅 Options 比较分支调用）。
func (f *Field) mustEncodeOptions() string {
	encoded, _ := json.Marshal(f.Options.choices)
	return string(encoded)
}

// RemoveField 物理删除字段并级联物理删除其全部值（与 tasks 删除口径一致）。
func (r *TaskRepo) RemoveField(id string) (*Field, error) {
	if BuiltinFieldID(id) {
		return nil, fmt.Errorf("内置字段不可删除")
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	field, err := scanField(tx.QueryRow(`SELECT `+fieldColumns+` FROM fields WHERE id = ?`, id))
	if errors.Is(err, ErrFieldNotFound) {
		return nil, fieldNotFound(id)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM task_field_values WHERE field_id = ?`, id); err != nil {
		return nil, fmt.Errorf("删除字段值失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM fields WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("删除字段失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return field, nil
}

// isUniqueViolation 判断现代 sqlite 的唯一约束冲突错误。
func isUniqueViolation(err error) bool {
	// modernc.org/sqlite 错误含 "constraint failed: UNIQUE constraint failed"
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
