// 任务自定义字段值的解析、读写与聚合（EAV 值表，design 2026-11-24-custom-fields §4）。
//
// 协议值形状（Task.Values，MCP/GUI 统一；类型知识全部收敛在 store）：
//
//	text          → JSON string
//	single_select → JSON string（必须 ∈ options）
//	multi_select  → JSON 数组（每项 ∈ options，去重保序）
//	number        → JSON number（存 REAL 列）
//	date          → JSON string RFC3339（归一化为 UTC epoch 毫秒存 REAL 列）
//	（update 的 values 内某项为 null = 显式清空；键缺席 = 不动）
//
// 内部持久形状 FieldValue 与数据库双列一一对应，不对外暴露。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// queryer 抽象 *sql.DB 与 *sql.Tx 的查询（值聚合在事务内外复用）。
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// FieldValue 是单字段值的持久形状；Text 与 Num 按字段类型二选一：
//   - text/single_select: Text = 单值
//   - multi_select: Text = 选项 JSON 数组文本
//   - number/date: Num（date 为 epoch 毫秒）
type FieldValue struct {
	Text string
	Num  *float64
}

// formatNumber 去掉浮点尾零用作分组键（123.0 → "123"）。
func formatNumber(value float64) string {
	return fmt.Sprintf("%v", value)
}

// ParseFieldValue 把协议 raw JSON 按字段类型解析为 FieldValue。
// raw 为 null 时返回错误（清空语义由 patch 路径单独表达，不由本函数处理）。
func ParseFieldValue(field *Field, raw json.RawMessage) (FieldValue, error) {
	if len(raw) == 0 || isNull(raw) {
		return FieldValue{}, fmt.Errorf("字段 %q 缺少值", field.Name)
	}
	switch field.Type {
	case FieldTypeText, FieldTypeSingleSelect:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return FieldValue{}, fmt.Errorf("字段 %q 需要字符串值", field.Name)
		}
		if field.Type == FieldTypeSingleSelect && !containsString(field.Choices(), text) {
			return FieldValue{}, fmt.Errorf("字段 %q 选项 %q 不在可选集中", field.Name, text)
		}
		return FieldValue{Text: text}, nil

	case FieldTypeMultiSelect:
		var choices []string
		if err := json.Unmarshal(raw, &choices); err != nil {
			return FieldValue{}, fmt.Errorf("字段 %q 需要字符串数组", field.Name)
		}
		if len(choices) == 0 {
			return FieldValue{}, fmt.Errorf("字段 %q 至少需要一个选项", field.Name)
		}
		seen := make(map[string]bool, len(choices))
		cleaned := make([]string, 0, len(choices))
		for _, item := range choices {
			if !containsString(field.Choices(), item) {
				return FieldValue{}, fmt.Errorf("字段 %q 选项 %q 不在可选集中", field.Name, item)
			}
			if !seen[item] {
				seen[item] = true
				cleaned = append(cleaned, item)
			}
		}
		encoded, err := json.Marshal(cleaned)
		if err != nil {
			return FieldValue{}, err
		}
		return FieldValue{Text: string(encoded)}, nil

	case FieldTypeNumber:
		var num float64
		if err := json.Unmarshal(raw, &num); err != nil {
			return FieldValue{}, fmt.Errorf("字段 %q 需要数字", field.Name)
		}
		return FieldValue{Num: &num}, nil

	case FieldTypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return FieldValue{}, fmt.Errorf("字段 %q 需要日期字符串", field.Name)
		}
		ms, err := rfc3339ToMs(text)
		if err != nil {
			return FieldValue{}, fmt.Errorf("字段 %q 日期格式错误: %w", field.Name, err)
		}
		var value float64 = float64(ms)
		return FieldValue{Num: &value}, nil
	}
	return FieldValue{}, fmt.Errorf("未知字段类型 %q", field.Type)
}

// protoValue 把持久形状还原为协议形状 JSON（读侧回报给 MCP/GUI）。
func protoValue(field *Field, value FieldValue) (json.RawMessage, error) {
	switch field.Type {
	case FieldTypeText, FieldTypeSingleSelect:
		return json.Marshal(value.Text)
	case FieldTypeMultiSelect:
		// text 存的就是规范化 JSON 数组文本，原样返回。
		return json.RawMessage(value.Text), nil
	case FieldTypeNumber:
		return json.Marshal(*value.Num)
	case FieldTypeDate:
		ms := int64(*value.Num)
		return json.Marshal(msToRFC3339(ms))
	}
	return nil, fmt.Errorf("未知字段类型 %q", field.Type)
}

func isNull(raw json.RawMessage) bool {
	return string(strings.TrimSpace(string(raw))) == "null"
}

// DecodeCreateValues 解析 create 的 values（键=字段名，全 setting，null 视为错误）。
func DecodeCreateValues(fields map[string]*Field, raws map[string]json.RawMessage) (map[string]FieldValue, error) {
	values := make(map[string]FieldValue, len(raws))
	for fieldName, raw := range raws {
		field, ok := fields[fieldName]
		if !ok {
			return nil, fmt.Errorf("字段 %q 不存在", fieldName)
		}
		if isNull(raw) {
			return nil, fmt.Errorf("创建任务时字段 %q 不能传 null", field.Name)
		}
		value, err := ParseFieldValue(field, raw)
		if err != nil {
			return nil, err
		}
		values[field.ID] = value
	}
	return values, nil
}

// DecodeUpdateValues 解析 update 的 values 三态（键=字段名）：
// 返回 map 中键缺席 = 不动；键存在且值为 nil = 清空；有值 = 设置。
// 出参 map 的键用字段 id（写值表），与协议层的字段名键分离。
func DecodeUpdateValues(fields map[string]*Field, raws map[string]json.RawMessage) (map[string]*FieldValue, error) {
	values := make(map[string]*FieldValue, len(raws))
	for fieldName, raw := range raws {
		field, ok := fields[fieldName]
		if !ok {
			return nil, fmt.Errorf("字段 %q 不存在", fieldName)
		}
		if isNull(raw) {
			values[field.ID] = nil
			continue
		}
		value, err := ParseFieldValue(field, raw)
		if err != nil {
			return nil, err
		}
		values[field.ID] = new(value)
	}
	return values, nil
}

// fieldsByName 加载全部自定义字段为 map（键=字段名；值解析/筛选按协议名定位）。
// 字段名全局唯一（DB 唯一约束），内置字段名不可占用。
func (r *TaskRepo) fieldsByName() (map[string]*Field, error) {
	fields, err := r.ListFields()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*Field, len(fields))
	for _, field := range fields {
		byName[field.Name] = field
	}
	return byName, nil
}

// writeTaskValues 在创建事务内把协议形状的 create 值解析并按字段类型写入。
// fields 必须在事务外查好（见 Create：单连接下事务内不能查 r.db，否则死锁）。
func writeTaskValues(tx *sql.Tx, taskID string, fields map[string]*Field, raws map[string]json.RawMessage) error {
	if len(raws) == 0 {
		return nil
	}
	decoded, err := DecodeCreateValues(fields, raws)
	if err != nil {
		return err
	}
	for fieldID, value := range decoded {
		if err := upsertFieldValue(tx, taskID, fieldID, value); err != nil {
			return fmt.Errorf("写入字段 %s 值失败: %w", fieldID, err)
		}
	}
	return nil
}

// deleteAllFieldValues 清空某任务的全部自定义字段值（同事务）。
func (r *TaskRepo) deleteAllFieldValues(tx *sql.Tx, taskID string) error {
	_, err := tx.Exec(`DELETE FROM task_field_values WHERE task_id = ?`, taskID)
	return err
}

// loadValuesFor 批量聚合任务的自定义字段值到 task.Values（协议形状）。
// 无值任务 Values 置为空 map（非 nil），读侧可放心遍历。
func (r *TaskRepo) loadValuesFor(tx queryer, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]any, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
		task.Values = map[string]json.RawMessage{}
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	rows, err := tx.Query(
		`SELECT v.task_id, f.name, v.text_value, v.num_value, f.type
		   FROM task_field_values v JOIN fields f ON f.id = v.field_id
		  WHERE v.task_id IN (` + placeholders + `)`,
		ids...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	index := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		index[task.ID] = task
	}
	var fieldType FieldType
	for rows.Next() {
		var taskID, fieldName string
		var text sql.NullString
		var num sql.NullFloat64
		if err := rows.Scan(&taskID, &fieldName, &text, &num, &fieldType); err != nil {
			return err
		}
		task := index[taskID]
		if task == nil {
			continue
		}
		value := FieldValue{}
		if text.Valid {
			value.Text = text.String
		}
		if num.Valid {
			value.Num = new(num.Float64)
		}
		raw, err := protoValue(&Field{Type: fieldType}, value)
		if err != nil {
			return err
		}
		task.Values[fieldName] = raw
	}
	return rows.Err()
}

// applyCreateValues 在任务创建事务内写入字段值（全 setting）。
func (r *TaskRepo) applyCreateValues(tx *sql.Tx, taskID string, values map[string]FieldValue) error {
	for fieldID, value := range values {
		if err := upsertFieldValue(tx, taskID, fieldID, value); err != nil {
			return fmt.Errorf("写入字段 %s 值失败: %w", fieldID, err)
		}
	}
	return nil
}

// applyUpdateValues 应用字段值三态：nil = 删除该键；有值 = upsert。
func (r *TaskRepo) applyUpdateValues(tx *sql.Tx, taskID string, values map[string]*FieldValue) error {
	for fieldID, value := range values {
		if value == nil {
			if _, err := tx.Exec(
				`DELETE FROM task_field_values WHERE task_id = ? AND field_id = ?`, taskID, fieldID,
			); err != nil {
				return fmt.Errorf("清空字段 %s 值失败: %w", fieldID, err)
			}
			continue
		}
		if err := upsertFieldValue(tx, taskID, fieldID, *value); err != nil {
			return fmt.Errorf("写入字段 %s 值失败: %w", fieldID, err)
		}
	}
	return nil
}

// upsertFieldValue 写一行值（PK 冲突即覆盖）。
func upsertFieldValue(tx *sql.Tx, taskID, fieldID string, value FieldValue) error {
	var text any
	if value.Text != "" {
		text = value.Text
	}
	var num any
	if value.Num != nil {
		num = *value.Num
	}
	_, err := tx.Exec(
		`INSERT INTO task_field_values (task_id, field_id, text_value, num_value) VALUES (?, ?, ?, ?)
		 ON CONFLICT(task_id, field_id) DO UPDATE SET
		   text_value = excluded.text_value, num_value = excluded.num_value`,
		taskID, fieldID, text, num,
	)
	return err
}

// deleteFieldValues 清理某任务的全部字段值（Remove 用，同事务）。
func (r *TaskRepo) deleteFieldValues(tx *sql.Tx, taskID string) error {
	_, err := tx.Exec(`DELETE FROM task_field_values WHERE task_id = ?`, taskID)
	return err
}

// containsString 判断字符串切片是否包含指定项。
func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// GroupKey 是 List 分组展示的一个组（Tasks 为组内任务，保持排序顺序）。
type GroupKey struct {
	Key   string
	Label string
	Tasks []*Task
}

// GroupByField 对（已按排序规则排列的）任务切片按字段分组；组序 = 任务首次出现序。
// 空值任务归入"未设置"组（排最后，与 SQL NULLS LAST 口径一致）。不支持 multi_select
// 分组（值多义）；调用方（list 增强）已在上层拦截，此处防御性按未设置处理。
func GroupByField(tasks []*Task, field *Field) []GroupKey {
	order := make(map[string]int, len(tasks))
	groups := make([]GroupKey, 0)
	for _, task := range tasks {
		key, label := valueGroupKey(task, field)
		index, ok := order[key]
		if !ok {
			index = len(groups)
			order[key] = index
			groups = append(groups, GroupKey{Key: key, Label: label})
		}
		groups[index].Tasks = append(groups[index].Tasks, task)
	}
	return groups
}

// valueGroupKey 计算任务在分组字段下的键与标签；内置字段直接从 Task 列取，
// 自定义字段从 task.Values（键=字段名）取；空值统一 "" / "未设置"。
func valueGroupKey(task *Task, field *Field) (string, string) {
	if _, kind := builtinColumn(field.Name); kind != "" {
		switch field.Name {
		case "status":
			if task.Status == 1 {
				return "1", "已完成"
			}
			return "0", "进行中"
		case "priority":
			label := []string{"无", "低", "中", "高"}
			key := fmt.Sprintf("%d", task.Priority)
			if task.Priority >= 0 && task.Priority < len(label) {
				return key, label[task.Priority]
			}
			return key, key
		case "title":
			if task.Title != "" {
				return task.Title, task.Title
			}
		case "due_at":
			if task.DueAt != nil {
				return msToRFC3339(*task.DueAt), time.UnixMilli(*task.DueAt).In(time.Local).Format("2006-01-02")
			}
		case "created_at":
			return msToRFC3339(task.CreatedAt), time.UnixMilli(task.CreatedAt).In(time.Local).Format("2006-01-02")
		}
		return "", "未设置"
	}
	raw, ok := task.Values[field.Name]
	if !ok {
		return "", "未设置"
	}
	switch field.Type {
	case FieldTypeNumber:
		var num float64
		if err := json.Unmarshal(raw, &num); err == nil {
			key := formatNumber(num)
			return key, key
		}
	case FieldTypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			if ms, err := rfc3339ToMs(text); err == nil {
				key := text
				return key, time.UnixMilli(ms).In(time.Local).Format("2006-01-02")
			}
		}
	case FieldTypeSingleSelect, FieldTypeText:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil && text != "" {
			return text, text
		}
	}
	return "", "未设置"
}