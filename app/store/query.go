// 增强查询：字段筛选（AND）、排序、分组（design 2026-11-24-custom-fields §3）。
//
// 安全边界：所有 SQL 片段均由字段名白名单 + 枚举操作符映射而来，用户输入
// 永不进入 SQL 文本（值一律参数绑定）。内置字段映射到 tasks 列，自定义字段
// 用 task_field_values 的 EXISTS 子查询（值参数绑定）。
package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FilterOp 是字段筛选操作符（按类型约束，见 validateFilterOp）。
type FilterOp string

const (
	OpEq       FilterOp = "eq"        // 文本/数字：等于
	OpContains FilterOp = "contains"  // 文本：包含
	OpGt       FilterOp = "gt"        // 数字：大于
	OpLt       FilterOp = "lt"        // 数字：小于
	OpIs       FilterOp = "is"        // 单选：等于
	OpHas      FilterOp = "has"       // 多选：包含某选项
	OpBefore   FilterOp = "before"    // 日期：早于
	OpAfter    FilterOp = "after"     // 日期：晚于
	OpEmpty    FilterOp = "empty"     // 为空
	OpNotEmpty FilterOp = "not_empty" // 非空
)

// FilterCond 是单个字段筛选条件。
type FilterCond struct {
	Field string          `json:"field"` // 内置字段名或自定义字段 id
	Op    FilterOp        `json:"op"`
	Value json.RawMessage `json:"value,omitempty"` // 协议形状（empty/not_empty 可缺省）
}

// SortKey 是单个排序键。
type SortKey struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// ListQuery 是增强查询入参。
// Archived 三态（2026-11-26 归档）：nil = 未归档（默认，常规视图不含归档任务）；
// &false = 只要未归档；&true = 只要已归档。
type ListQuery struct {
	Status   *int         // 原有筛选（保留）
	Priority *int         // 原有筛选（保留）
	Archived *bool        // 归档筛选（2026-11-26）
	Filters  []FilterCond // AND 语义
	Sort     []SortKey    // 空 = 默认固定排序
	GroupBy  string       // 分组字段；空 = 平铺
	Limit    int          // 钳制 [1,200]；分组模式忽略（上限 1000）
	Offset   int
}

// ListResult 是增强查询结果；分组模式 Tasks 为空、Groups 有值，反则反之。
type ListResult struct {
	Total  int
	Tasks  []*Task
	Groups []GroupKey
}

// builtinColumn 返回内置字段对应的 tasks 列与类型；非内置返回 ok=false。
func builtinColumn(fieldID string) (column string, kind FieldType) {
	switch fieldID {
	case "title":
		return "title", FieldTypeText
	case "description":
		return "description", FieldTypeText
	case "priority":
		return "priority", FieldTypeNumber
	case "due_at":
		return "due_at", FieldTypeDate
	case "status":
		return "status", FieldTypeNumber
	case "created_at":
		return "created_at", FieldTypeDate
	case "archived_at":
		return "archived_at", FieldTypeDate
	case "completed_at":
		return "completed_at", FieldTypeDate
	}
	return "", ""
}

// validateQuery 校验增强查询：字段存在性、操作符与类型匹配、上限。
func (r *TaskRepo) validateQuery(q ListQuery) error {
	if len(q.Filters) > MaxFilterCondCount {
		return fmt.Errorf("筛选条件不能超过 %d 条", MaxFilterCondCount)
	}
	if len(q.Sort) > MaxSortKeyCount {
		return fmt.Errorf("排序键不能超过 %d 个", MaxSortKeyCount)
	}

	custom, err := r.fieldsByName()
	if err != nil {
		return err
	}
	fieldOf := func(id string) (*Field, bool) {
		if field, ok := custom[id]; ok {
			return field, true
		}
		if column, kind := builtinColumn(id); column != "" {
			return &Field{ID: id, Type: kind}, true
		}
		return nil, false
	}

	for _, cond := range q.Filters {
		field, ok := fieldOf(cond.Field)
		if !ok {
			return fmt.Errorf("筛选字段 %q 不存在", cond.Field)
		}
		if err := validateFilterOp(field, cond.Op); err != nil {
			return fmt.Errorf("筛选字段 %q: %w", cond.Field, err)
		}
	}
	for _, key := range q.Sort {
		field, ok := fieldOf(key.Field)
		if !ok {
			return fmt.Errorf("排序字段 %q 不存在", key.Field)
		}
		if field.Type == FieldTypeMultiSelect {
			return fmt.Errorf("多选字段 %q 不能作为排序键", key.Field)
		}
	}
	if q.GroupBy != "" {
		field, ok := fieldOf(q.GroupBy)
		if !ok {
			return fmt.Errorf("分组字段 %q 不存在", q.GroupBy)
		}
		if field.Type == FieldTypeMultiSelect {
			return fmt.Errorf("多选字段 %q 不能作为分组键", q.GroupBy)
		}
	}
	return nil
}

// validateFilterOp 校验操作符与字段类型匹配。
func validateFilterOp(field *Field, op FilterOp) error {
	contains := func(set ...FilterOp) bool {
		for _, item := range set {
			if op == item {
				return true
			}
		}
		return false
	}
	switch field.Type {
	case FieldTypeText:
		if !contains(OpEq, OpContains, OpEmpty, OpNotEmpty) {
			return fmt.Errorf("文本字段仅支持 eq/contains/empty/not_empty，不支持 %q", op)
		}
	case FieldTypeNumber:
		if !contains(OpEq, OpGt, OpLt, OpEmpty, OpNotEmpty) {
			return fmt.Errorf("数字字段仅支持 eq/gt/lt/empty/not_empty，不支持 %q", op)
		}
	case FieldTypeSingleSelect:
		if !contains(OpIs, OpEmpty, OpNotEmpty) {
			return fmt.Errorf("单选字段仅支持 is/empty/not_empty，不支持 %q", op)
		}
	case FieldTypeMultiSelect:
		if !contains(OpHas, OpEmpty, OpNotEmpty) {
			return fmt.Errorf("多选字段仅支持 has/empty/not_empty，不支持 %q", op)
		}
	case FieldTypeDate:
		if !contains(OpBefore, OpAfter, OpEmpty, OpNotEmpty) {
			return fmt.Errorf("日期字段仅支持 before/after/empty/not_empty，不支持 %q", op)
		}
	}
	return nil
}

// parseFilterValue 把协议的 Value 归一化：
//   - 数字类（数字字段/单选 is/多选 has 之外的对比值）：JSON number → float64
//   - 日期：RFC3339 → epoch 毫秒 float64
//   - 文本类：JSON string
func parseFilterValue(field *Field, op FilterOp, raw json.RawMessage) (any, string, error) {
	if op == OpEmpty || op == OpNotEmpty {
		return nil, "", nil
	}
	if len(raw) == 0 || isNull(raw) {
		return nil, "", fmt.Errorf("操作符 %q 需要 value", op)
	}
	// 文本与选择类：值本身即字符串（数字字段的 op 值也是 JSON number）
	if field.Type == FieldTypeText || field.Type == FieldTypeSingleSelect || field.Type == FieldTypeMultiSelect {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, "", fmt.Errorf("值必须是字符串")
		}
		return text, "text", nil
	}
	if field.Type == FieldTypeDate {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, "", fmt.Errorf("日期值必须是字符串")
		}
		ms, err := rfc3339ToMs(text)
		if err != nil {
			return nil, "", fmt.Errorf("日期值格式错误: %w", err)
		}
		return float64(ms), "num", nil
	}
	// 数字字段
	var num float64
	if err := json.Unmarshal(raw, &num); err != nil {
		return nil, "", fmt.Errorf("数字值格式错误")
	}
	return num, "num", nil
}

// buildFilterClauses 组装全部 WHERE 片段与参数。
// 内置字段用 tasks 列；自定义字段用 EXISTS 子查询。返回条件切片与参数。
func (r *TaskRepo) buildFilterClauses(q ListQuery, custom map[string]*Field) ([]string, []any, error) {
	conditions := make([]string, 0, len(q.Filters)+3)
	args := make([]any, 0, len(q.Filters)*2+3)
	// 归档默认口径（2026-11-26）：nil / false = 只要未归档；true = 只要已归档。
	if q.Archived == nil || !*q.Archived {
		conditions = append(conditions, "archived_at IS NULL")
	} else {
		conditions = append(conditions, "archived_at IS NOT NULL")
	}
	if q.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, *q.Status)
	}
	if q.Priority != nil {
		conditions = append(conditions, "priority = ?")
		args = append(args, *q.Priority)
	}
	for _, cond := range q.Filters {
		if field, ok := custom[cond.Field]; ok {
			clause, condArgs, err := customFieldClause(field, cond)
			if err != nil {
				return nil, nil, err
			}
			conditions = append(conditions, clause)
			args = append(args, condArgs...)
			continue
		}
		column, kind := builtinColumn(cond.Field)
		if column == "" {
			return nil, nil, fmt.Errorf("筛选字段 %q 不存在", cond.Field)
		}
		clause, condArgs, err := builtinFieldClause(column, kind, cond)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, clause)
		args = append(args, condArgs...)
	}
	return conditions, args, nil
}

// customFieldClause 生成自定义字段的 EXISTS 子查询。
func customFieldClause(field *Field, cond FilterCond) (string, []any, error) {
	value, kind, err := parseFilterValue(field, cond.Op, cond.Value)
	if err != nil {
		return "", nil, fmt.Errorf("字段 %q: %w", field.Name, err)
	}
	column := textValueColumn
	if kind == "num" {
		column = numValueColumn
	}

	// EXISTS 子查询以 task_id 关联；empty/not_empty 判定值行存在性
	// （数字字段只有 num_value、文本只有 text_value，值行存在即非空）。
	var predicate string
	args := []any{field.ID}
	switch cond.Op {
	case OpEmpty:
		return `NOT EXISTS (SELECT 1 FROM task_field_values v WHERE v.task_id = tasks.id AND v.field_id = ?)`,
			args, nil
	case OpNotEmpty:
		return `EXISTS (SELECT 1 FROM task_field_values v WHERE v.task_id = tasks.id AND v.field_id = ?)`,
			args, nil
	case OpEq, OpIs, OpHas:
		predicate = column + " = ?"
		args = append(args, value)
	case OpContains:
		predicate = column + " LIKE '%' || ? || '%'"
		args = append(args, value)
	case OpGt, OpAfter:
		predicate = column + " > ?"
		args = append(args, value)
	case OpLt, OpBefore:
		predicate = column + " < ?"
		args = append(args, value)
	}
	clause := `EXISTS (SELECT 1 FROM task_field_values v WHERE v.task_id = tasks.id AND v.field_id = ? AND ` + predicate + `)`
	return clause, args, nil
}

// nullWord 把 empty/not_empty 转成 SQL 的 NULL 判断词。
func nullWord(op FilterOp) string {
	if op == OpEmpty {
		return "NULL"
	}
	return "NOT NULL"
}

// builtinFieldClause 生成内置字段的列条件。
func builtinFieldClause(column string, kind FieldType, cond FilterCond) (string, []any, error) {
	field := &Field{ID: column, Type: kind}
	value, valueKind, err := parseFilterValue(field, cond.Op, cond.Value)
	if err != nil {
		return "", nil, err
	}
	switch cond.Op {
	case OpEmpty, OpNotEmpty:
		return column + " IS " + nullWord(cond.Op), nil, nil
	}
	_ = valueKind
	var operator string
	switch cond.Op {
	case OpEq, OpIs:
		operator = "="
	case OpContains:
		operator = "LIKE '%' || ? || '%'"
		return column + " " + operator, []any{value}, nil
	case OpGt, OpAfter:
		operator = ">"
	case OpLt, OpBefore:
		operator = "<"
	case OpHas:
		return "", nil, fmt.Errorf("内置字段不支持 has")
	}
	return column + " " + operator + " ?", []any{value}, nil
}

// value 列名常量（避免魔法值散落）。
const (
	textValueColumn = "v.text_value"
	numValueColumn  = "v.num_value"
)

// buildOrderClause 组装 ORDER BY 片段；空 sort 用默认固定排序。
// 自定义字段排序以 EXISTS 子查询投影列（NULLS LAST），内置字段直接投影。
func (r *TaskRepo) buildOrderClause(q ListQuery, custom map[string]*Field) (string, []any, error) {
	if len(q.Sort) == 0 {
		return "status ASC, (due_at IS NULL) ASC, due_at ASC, priority DESC, id ASC", nil, nil
	}
	parts := make([]string, 0, len(q.Sort)+1)
	args := make([]any, 0, len(q.Sort))
	for _, key := range q.Sort {
		direction := "ASC"
		if key.Desc {
			direction = "DESC"
		}
		if field, ok := custom[key.Field]; ok {
			column := textValueColumn
			// 数字/日期排序用 num_value，其余用 text_value
			if field.Type == FieldTypeNumber || field.Type == FieldTypeDate {
				column = numValueColumn
			}
			args = append(args, field.ID)
			parts = append(parts,
				fmt.Sprintf(`(SELECT %s FROM task_field_values v WHERE v.task_id = tasks.id AND v.field_id = ?) %s NULLS LAST`,
					column, direction))
			continue
		}
		column, _ := builtinColumn(key.Field)
		if column == "" {
			return "", nil, fmt.Errorf("排序字段 %q 不存在", key.Field)
		}
		// 空值统一排后：NULLS LAST（字符串列没有 NULL 时的空串也排在 NULL 之后）
		parts = append(parts, column+" IS NULL ASC", column+" "+direction)
	}
	// 稳定 tiebreak
	parts = append(parts, "id ASC")
	return strings.Join(parts, ", "), args, nil
}

// Query 执行增强查询：
//   - 平铺模式：Count + 分页（limit 钳制 [1,200]），返回 Total/Tasks
//   - 分组模式：取全部匹配任务（上限 1000），按字段分组返回 Groups（Tasks 空）
func (r *TaskRepo) Query(q ListQuery) (*ListResult, error) {
	if err := r.validateQuery(q); err != nil {
		return nil, err
	}
	custom, err := r.fieldsByName()
	if err != nil {
		return nil, err
	}

	conditions, args, err := r.buildFilterClauses(q, custom)
	if err != nil {
		return nil, err
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	// 总数（平铺与分组都需要）
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM tasks`+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	if q.GroupBy != "" {
		return r.queryGrouped(q, custom, where, args, total)
	}
	return r.queryPaged(q, where, args, total)
}

// queryPaged 平铺分页查询。
func (r *TaskRepo) queryPaged(q ListQuery, where string, args []any, total int) (*ListResult, error) {
	limit := q.Limit
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	custom, err := r.fieldsByName()
	if err != nil {
		return nil, err
	}
	order, orderArgs, err := r.buildOrderClause(q, custom)
	if err != nil {
		return nil, err
	}
	queryArgs := append(append([]any{}, args...), orderArgs...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := r.db.Query(
		`SELECT `+taskColumns+` FROM tasks`+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		queryArgs...,
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
	return &ListResult{Total: total, Tasks: tasks}, nil
}

// queryGrouped 分组查询：取全部匹配任务（上限 1000）后内存分组（组内保持排序）。
func (r *TaskRepo) queryGrouped(q ListQuery, custom map[string]*Field, where string, args []any, total int) (*ListResult, error) {
	order, orderArgs, err := r.buildOrderClause(ListQuery{Sort: q.Sort}, custom)
	if err != nil {
		return nil, err
	}
	queryArgs := append(append([]any{}, args...), orderArgs...)
	queryArgs = append(queryArgs, 1000)
	rows, err := r.db.Query(
		`SELECT `+taskColumns+` FROM tasks`+where+` ORDER BY `+order+` LIMIT ?`,
		queryArgs...,
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
	// 分组字段可能是内置字段名（不在 custom map 中），构造对应 Field。
	field := custom[q.GroupBy]
	if field == nil {
		column, kind := builtinColumn(q.GroupBy)
		if column == "" {
			return nil, fmt.Errorf("分组字段 %q 不存在", q.GroupBy)
		}
		field = &Field{ID: q.GroupBy, Name: q.GroupBy, Type: kind}
	}
	groups := GroupByField(tasks, field)
	return &ListResult{Total: total, Groups: groups}, nil
}
