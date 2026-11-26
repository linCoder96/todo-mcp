// 单元格直接编辑（2026-11-25）：点自定义字段单元格原地改，Enter 提交、Esc 取消。
// 清空文本后提交 = 删除该字段的值（协议里该键显式 null）。
package gui

import (
	"encoding/json"
	"fmt"
	"strings"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
)

// taskOf 按 id 在当前列表里找任务。
func (m *model) taskOf(id string) *store.Task {
	for _, view := range m.tasks {
		if view.Task != nil && view.ID == id {
			return view.Task
		}
	}
	return nil
}

// cellEditing 该任务的该字段单元格是否处于编辑态。
func (m *model) cellEditing(taskID, field string) bool {
	return m.cellTask == taskID && m.cellField == field
}

// beginCellEdit 进入单元格编辑态，缓冲预填当前值的文本形态。
// 与编辑弹窗互斥：先关掉可能开着的弹窗，避免两处缓冲打架。
func (m *model) beginCellEdit(task *store.Task, field *store.Field) {
	m.endEdit()
	m.cellTask, m.cellField = task.ID, field.Name
	m.cellValue = decodeCustomText(field, task.Values[field.Name])
	m.confirmID = ""
}

// cancelCellEdit 放弃单元格编辑，不落库。
func (m *model) cancelCellEdit() {
	m.cellTask, m.cellField, m.cellValue = "", "", ""
}

// commitCellEdit 提交单元格：有值=设置，清空=删除该字段的值。
// 值不合法时不落库、保留编辑态并显示原因。
func (m *model) commitCellEdit() {
	field := m.fieldOf(m.cellField)
	task := m.taskOf(m.cellTask)
	if field == nil || task == nil {
		m.cancelCellEdit()
		return
	}
	text := strings.TrimSpace(m.cellValue)
	if text == "" && len(task.Values[field.Name]) == 0 {
		m.cancelCellEdit() // 本来就没有值，无需写库
		return
	}
	values := map[string]json.RawMessage{}
	if text == "" {
		values[field.Name] = json.RawMessage(`null`)
	} else {
		raw, err := encodeCustomValue(field, text)
		if err != nil {
			m.errorText = fmt.Sprintf("字段 %s：%v", field.Name, err)
			return
		}
		values[field.Name] = raw
	}
	id, patch := task.ID, store.TaskPatch{Values: new(new(values))}
	m.cancelCellEdit()
	m.apply(func() error {
		_, err := m.repo.Update(id, patch)
		return err
	})
}

// beginRemarkCellEdit 进入备注单元格编辑态（描述列，非自定义字段值表）。
// 单元格键与自定义字段共用 cellTask/cellField/cellValue 缓冲，互斥复用。
func (m *model) beginRemarkCellEdit(task *store.Task) {
	m.cancelCellEdit()
	m.cellTask, m.cellField = task.ID, "description"
	m.cellValue = ""
	if task.Description != nil {
		m.cellValue = *task.Description
	}
	m.confirmID = ""
}

// commitRemarkCell 提交备注单元格：有值 = 设置，清空 = 显式清空 description 列
// （TaskPatch 双层 Option 的内层 nil 语义）；无变化不落库。
func (m *model) commitRemarkCell() {
	task := m.taskOf(m.cellTask)
	if task == nil {
		m.cancelCellEdit()
		return
	}
	text := strings.TrimSpace(m.cellValue)
	original := ""
	if task.Description != nil {
		original = *task.Description
	}
	if text == original {
		m.cancelCellEdit() // 无变化
		return
	}
	var desc **string
	if text == "" {
		desc = new((*string)(nil)) // 显式清空
	} else {
		desc = new(new(text))
	}
	id, patch := task.ID, store.TaskPatch{Description: desc}
	m.cancelCellEdit()
	m.apply(func() error {
		_, err := m.repo.Update(id, patch)
		return err
	})
}

// Key 是 app.Config.OnKey：接收页面没有处理的按键。这里只在单元格编辑态响应
// Esc（放弃修改）。
func (m *model) Key(event gfx.KeyEvent) gfx.EventResult {
	if event.Key == gfx.KeyEscape && !event.Release && m.cellTask != "" {
		m.cancelCellEdit()
		return gfx.EventResult{Handled: true, Repaint: true}
	}
	return gfx.EventResult{}
}

// selectChoiceLabel 单选下拉的选项文案：空串是「未设置」一项，选它即清除该值。
func selectChoiceLabel(choice string) string {
	if choice == "" {
		return "未设置"
	}
	return choice
}
