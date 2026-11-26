// GUI 层自定义字段、筛选、排序、分组的 model 状态与数据加载。
// 与 gui/model.go 的 model 同一个类型；在此文件集中放新功能的逻辑。
package gui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"todo-mcp/app/store"
)

// filterOpChoices 是 GUI 筛选操作符的可选集（按字段类型在 page 层收窄）。
var filterOpChoices = []store.FilterOp{
	store.OpEq, store.OpContains, store.OpGt, store.OpLt,
	store.OpIs, store.OpHas, store.OpBefore, store.OpAfter,
	store.OpEmpty, store.OpNotEmpty,
}

func filterOpLabel(op store.FilterOp) string {
	switch op {
	case store.OpEq:
		return "等于"
	case store.OpContains:
		return "包含"
	case store.OpGt:
		return "大于"
	case store.OpLt:
		return "小于"
	case store.OpIs:
		return "是"
	case store.OpHas:
		return "包含选项"
	case store.OpBefore:
		return "早于"
	case store.OpAfter:
		return "晚于"
	case store.OpEmpty:
		return "为空"
	case store.OpNotEmpty:
		return "非空"
	}
	return string(op)
}

// 字段类型网格（参考图 8 类）：存储类型 + 显示样式，8 个选项一一映射。
type fieldTypeChoice struct {
	label   string
	typeKey store.FieldType
	display store.FieldDisplay
}

var fieldTypeGrid = []fieldTypeChoice{
	{"文本", store.FieldTypeText, store.DisplayPlain},
	{"单选", store.FieldTypeSingleSelect, store.DisplayPlain},
	{"多选", store.FieldTypeMultiSelect, store.DisplayPlain},
	{"数字", store.FieldTypeNumber, store.DisplayPlain},
	{"百分数", store.FieldTypeNumber, store.DisplayPercent},
	{"货币", store.FieldTypeNumber, store.DisplayCurrency},
	{"进度", store.FieldTypeNumber, store.DisplayProgress},
	{"日期", store.FieldTypeDate, store.DisplayPlain},
}

func fieldTypeGridLabel(choice fieldTypeChoice) string { return choice.label }

// fieldTypeChoiceOf 反查某字段在网格中的索引（默认 0=文本）。
func fieldTypeChoiceOf(typeKey store.FieldType, display store.FieldDisplay) int {
	for i, choice := range fieldTypeGrid {
		if choice.typeKey == typeKey && choice.display == display {
			return i
		}
	}
	return 0
}

// isSelectType 判断字段是否为选择类。
func isSelectType(typeKey store.FieldType) bool {
	return typeKey == store.FieldTypeSingleSelect || typeKey == store.FieldTypeMultiSelect
}

// refreshFields 重新加载自定义字段列表并刷新任务（字段重命名后行内显示同步）。
func (m *model) refreshFields() error {
	fields, err := m.repo.ListFields()
	if err != nil {
		return err
	}
	m.fields = fields
	return nil
}

// buildQuery 根据 model 的 tab / 筛选条件 / 排序 / 分组状态构造 store.ListQuery。
func (m *model) buildQuery() store.ListQuery {
	query := store.ListQuery{Limit: 1000}
	base := m.filter() // 沿用 Tab 的状态筛选（保留旧行为）
	query.Status = base.Status
	query.Priority = base.Priority
	query.Archived = base.Archived

	// 多条件筛选（AND；协议上限 10 条，超限截断，2026-11-25 ②）
	if len(m.filterConds) > 0 {
		conds := m.filterConds
		if len(conds) > 10 {
			conds = conds[:10]
		}
		query.Filters = conds
	}
	if m.sortField != "" {
		query.Sort = []store.SortKey{{Field: m.sortField, Desc: m.sortDesc}}
	}
	if m.groupBy != "" {
		query.GroupBy = m.groupBy
	}
	return query
}

// encodeCustomValue 把编辑器文本按字段类型编码为协议 JSON 值（2026-11-25 ①）。
// 文本/单选 → JSON 字符串；多选 → 逗号分隔转数组；数字 → JSON number；
// 日期 → 本地时间解析后归一化 RFC3339。
func encodeCustomValue(field *store.Field, text string) (json.RawMessage, error) {
	text = strings.TrimSpace(text)
	switch field.Type {
	case store.FieldTypeNumber:
		num, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("需要数字，收到 %q", text)
		}
		return json.Marshal(num)
	case store.FieldTypeMultiSelect:
		parts := make([]string, 0, 4)
		for _, item := range strings.Split(text, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("至少需要一个选项（逗号分隔）")
		}
		return json.Marshal(parts)
	case store.FieldTypeDate:
		local, err := time.ParseInLocation(localTimeLayout, text, time.Local)
		if err != nil {
			return nil, fmt.Errorf("日期格式应为 %s", localTimeLayout)
		}
		return json.Marshal(local.UTC().Format(time.RFC3339))
	default: // 文本 / 单选
		return json.Marshal(text)
	}
}

// decodeCustomText 把协议 JSON 值解码为编辑器文本（encodeCustomValue 的逆，①）。
func decodeCustomText(field *store.Field, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch field.Type {
	case store.FieldTypeNumber:
		var num float64
		if err := json.Unmarshal(raw, &num); err == nil {
			return strconv.FormatFloat(num, 'f', -1, 64)
		}
	case store.FieldTypeMultiSelect:
		var items []string
		if err := json.Unmarshal(raw, &items); err == nil {
			return strings.Join(items, ",")
		}
	case store.FieldTypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			if ms, err := time.Parse(time.RFC3339, text); err == nil {
				return ms.In(time.Local).Format(localTimeLayout)
			}
			return text
		}
	default:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	return string(raw)
}

// addFilterCond 把面板缓冲区里的条件追加进生效条件列表（≤10，②）。
// 需要值的条件必须已填值并按字段类型校验/编码；为空/非空直接生效。
func (m *model) addFilterCond() bool {
	if m.draftField == "" || m.draftOp == "" || len(m.filterConds) >= 10 {
		return false
	}
	cond := store.FilterCond{Field: m.draftField, Op: m.draftOp}
	if m.draftOp != store.OpEmpty && m.draftOp != store.OpNotEmpty {
		if strings.TrimSpace(m.draftValue) == "" {
			return false
		}
		raw, err := m.encodeCondValue(m.draftField, m.draftValue)
		if err != nil {
			m.errorText = err.Error()
			return false
		}
		cond.Value = raw
	}
	m.filterConds = append(m.filterConds, cond)
	m.draftField, m.draftOp, m.draftValue = "", "", ""
	return true
}

// encodeCondValue 条件值按字段类型编码（②）：自定义字段复用 encodeCustomValue；
// 内置日期字段归一化 RFC3339（store 的日期比较要求）；内置数字字段编码为 JSON number。
func (m *model) encodeCondValue(fieldKey, text string) (json.RawMessage, error) {
	if field := m.fieldOf(fieldKey); field != nil {
		return encodeCustomValue(field, text)
	}
	switch m.fieldTypeOf(fieldKey) {
	case store.FieldTypeDate:
		local, err := time.ParseInLocation(localTimeLayout, strings.TrimSpace(text), time.Local)
		if err != nil {
			return nil, fmt.Errorf("日期格式应为 %s", localTimeLayout)
		}
		return json.Marshal(local.UTC().Format(time.RFC3339))
	case store.FieldTypeNumber:
		num, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, fmt.Errorf("需要数字，收到 %q", text)
		}
		return json.Marshal(num)
	}
	return json.Marshal(text)
}

// removeFilterCond 移除第 i 条生效条件（②）。
func (m *model) removeFilterCond(index int) {
	if index < 0 || index >= len(m.filterConds) {
		return
	}
	m.filterConds = append(m.filterConds[:index], m.filterConds[index+1:]...)
}

// moveField 调整自定义字段列序（落库 position，③）；delta = -1 上移 / +1 下移。
func (m *model) moveField(id string, delta int) {
	index := -1
	for i, field := range m.fields {
		if field.ID == id {
			index = i
			break
		}
	}
	target := index + delta
	if index < 0 || target < 0 || target >= len(m.fields) {
		return
	}
	// 交换两者的 position（按当前顺序重排 0..n-1，两笔小事务）
	order := make([]*store.Field, len(m.fields))
	copy(order, m.fields)
	order[index], order[target] = order[target], order[index]
	for i, field := range order {
		position := i
		patch := store.FieldPatch{Position: new(new(position))}
		if _, err := m.repo.UpdateField(field.ID, patch); err != nil {
			m.errorText = err.Error()
			return
		}
	}
	m.applyQueryChange()
}

// fieldFilterValue 把 GUI 文本框输入按操作符包装为协议 JSON 值。
func fieldFilterValue(op store.FilterOp, text string) string {
	if op == store.OpGt || op == store.OpLt || op == store.OpEq {
		// 数字类：尝试解析；失败则按字符串处理（文本字段 eq）
		var num float64
		if _, err := fmt.Sscanf(text, "%f", &num); err == nil && text != "" {
			return text
		}
	}
	// 日期/文本/选项：JSON 字符串
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

// applyQueryChange 在工具栏变更筛选/排序/分组后重查，并把变更持久化到
// app_prefs（重启后恢复，任务「筛选排序分组没有记住偏好」）。
func (m *model) applyQueryChange() {
	m.errorText = ""
	m.saveViewPrefs()
	if err := m.refresh(); err != nil {
		m.errorText = err.Error()
	}
}

// newField 从编辑缓冲创建字段（page 层在点击保存时调用）。
func (m *model) newField() {
	choice := fieldTypeGrid[m.fieldTypeIndex]
	input := store.FieldInput{
		Name:     strings.TrimSpace(m.fieldName),
		Type:     choice.typeKey,
		Display:  choice.display,
		Position: 0,
	}
	if isSelectType(choice.typeKey) {
		// 选项按逗号分隔，逐项 trim；空项过滤。
		for _, option := range strings.Split(m.fieldOptions, ",") {
			if trimmed := strings.TrimSpace(option); trimmed != "" {
				input.Options = append(input.Options, trimmed)
			}
		}
	}
	if _, err := m.repo.CreateField(input); err != nil {
		m.errorText = err.Error()
		return
	}
	m.errorText = ""
	m.closeFieldModal()
	m.refreshFields()
}

// updateField 从编辑缓冲更新字段（只改可改项）。
func (m *model) updateField(id string) {
	patch := store.FieldPatch{
		Name: new(new(strings.TrimSpace(m.fieldName))),
	}
	choice := fieldTypeGrid[m.fieldTypeIndex]
	if choice.typeKey == store.FieldTypeNumber {
		patch.Display = new(new(choice.display))
	}
	if isSelectType(choice.typeKey) {
		options := make([]string, 0)
		for _, option := range strings.Split(m.fieldOptions, ",") {
			if trimmed := strings.TrimSpace(option); trimmed != "" {
				options = append(options, trimmed)
			}
		}
		patch.Options = new(new(options))
	}
	if _, err := m.repo.UpdateField(id, patch); err != nil {
		m.errorText = err.Error()
		return
	}
	m.errorText = ""
	m.closeFieldModal()
	m.refreshFields()
}

// removeField 删除字段并清理相关选择状态。
func (m *model) removeField(id string) {
	if _, err := m.repo.RemoveField(id); err != nil {
		m.errorText = err.Error()
		return
	}
	name := ""
	for _, field := range m.fields {
		if field.ID == id {
			name = field.Name
			break
		}
	}
	// 清理指向该字段的生效筛选条件
	conds := m.filterConds[:0]
	for _, cond := range m.filterConds {
		if cond.Field != name {
			conds = append(conds, cond)
		}
	}
	m.filterConds = conds
	if m.sortField == name {
		m.sortField = ""
	}
	if m.groupBy == name {
		m.groupBy = ""
	}
	m.errorText = ""
	m.closeFieldModal()
	m.applyQueryChange()
	m.refreshFields()
}

// openFieldModal 打开字段编辑弹窗（新建或编辑）。
func (m *model) openFieldModal(field *store.Field) {
	m.fieldModalOpen = true
	m.fieldEditID = ""
	m.fieldName = ""
	m.fieldOptions = ""
	m.fieldTypeIndex = 0
	if field != nil {
		m.fieldEditID = field.ID
		m.fieldName = field.Name
		m.fieldTypeIndex = fieldTypeChoiceOf(field.Type, field.Display)
		m.fieldOptions = strings.Join(field.Choices(), ",")
	}
}

func (m *model) closeFieldModal() {
	m.fieldModalOpen = false
}

// deleteField 删除自定义字段列：第一次点击置 confirmFieldID 进入确认态，
// 第二次点击才物理删除并级联清理字段值（与任务删除 deleteTask 的二次确认一致）。
func (m *model) deleteField(id string) {
	if m.confirmFieldID != id {
		m.confirmFieldID = id
		return
	}
	m.confirmFieldID = ""
	m.removeField(id)
}

// fieldValueLabel 把协议值格式化为行内展示文本（按字段类型）。
func fieldValueLabel(field *store.Field, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch field.Type {
	case store.FieldTypeText, store.FieldTypeSingleSelect:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	case store.FieldTypeMultiSelect:
		var items []string
		if err := json.Unmarshal(raw, &items); err == nil {
			return strings.Join(items, "、")
		}
	case store.FieldTypeNumber:
		var num float64
		if err := json.Unmarshal(raw, &num); err == nil {
			switch field.Display {
			case store.DisplayPercent:
				return fmt.Sprintf("%.1f%%", num)
			case store.DisplayCurrency:
				return fmt.Sprintf("¥%.2f", num)
			case store.DisplayProgress:
				return fmt.Sprintf("进度 %.0f%%", num)
			default:
				return fmt.Sprintf("%v", num)
			}
		}
	case store.FieldTypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			if ms, err := time.Parse(time.RFC3339, text); err == nil {
				return ms.In(time.Local).Format("2006-01-02")
			}
			return text
		}
	}
	return string(raw)
}
