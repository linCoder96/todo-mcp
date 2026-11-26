// 列设置面板字段行拖拽排序：行身份、_drop 回调与重排落库。
//
// 交互：字段行中部「≡」把手按下即拖，松开在另一字段行上完成换位；
// 勾选框与「…」菜单按钮是行内子控件，按它们走各自点击、不触发拖拽。
// Popover 面板与页面同 Frame，DragState 由 Page 统一路由。
package gui

import (
	"github.com/vivaui/viva/gfx"

	"todo-mcp/app/store"
)

// fieldDragRow 字段行的声明身份（绘制时写入，drop 时按身份查回字段）。
type fieldDragRow struct {
	id    gfx.ID // 整行 Hit 身份：既是拖拽源，也是放置目标
	field *store.Field
}

// beginFieldDrag 每次列设置面板重绘时重置身份表；同一帧内声明完所有字段行。
func (m *model) beginFieldDrag() {
	m.fieldDragRows = m.fieldDragRows[:0]
}

// fieldDragIndexOf 按身份找字段行下标；-1 表示不是本面板声明的身份。
func (m *model) fieldDragIndexOf(id gfx.ID) int {
	for i := range m.fieldDragRows {
		if id == m.fieldDragRows[i].id {
			return i
		}
	}
	return -1
}

// acceptFieldDrop 只接受自定义字段行之间换位（不接受拖到自身或面板外）。
func (m *model) acceptFieldDrop(source, target gfx.ID) bool {
	from, to := m.fieldDragIndexOf(source), m.fieldDragIndexOf(target)
	return from >= 0 && to >= 0 && from != to
}

// dropFieldDrag 松开：把源字段行移动到目标位置（remove + insert），落库后重查。
// 回调存为 model 方法值，只建一次，避免每帧声明目标时分配闭包。
func (m *model) dropFieldDrag(source, target gfx.ID) {
	from, to := m.fieldDragIndexOf(source), m.fieldDragIndexOf(target)
	if from < 0 || to < 0 || from == to {
		return
	}
	// 身份表按 m.fields 顺序声明，下标即列序（隐藏字段也有行）。
	order := make([]*store.Field, 0, len(m.fields))
	for i, field := range m.fields {
		if i != from {
			order = append(order, field)
		}
	}
	if to < 0 || to > len(order) {
		return
	}
	order = append(order, nil)
	copy(order[to+1:], order[to:])
	order[to] = m.fields[from]
	m.reorderFields(order)
}

// reorderFields 把字段按给定顺序写入 position 0..n-1（逐条小事务），再重查视图。
func (m *model) reorderFields(order []*store.Field) {
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

// fieldDragTargetRow 判断某行身份是否正是当前拖拽悬停的目标（供绘制高亮）。
func (m *model) fieldDragTargetRow(id gfx.ID) bool {
	return m.fieldDrag.Dragging() && m.fieldDrag.Target() == id
}
