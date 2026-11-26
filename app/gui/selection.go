// 行多选（2026-11-26）：序号列的选择复选框 + 列头全选 + 工具栏右侧的批量操作区。
// 选择集 m.chosen 由三处共同读写：ui.List 原生手势（点行身=单选、Ctrl 点选、
// Shift 连选、Ctrl+A 全选）、每行序号列的复选框、列头「全选」。批量操作在
// 一个 apply 里做完（只重查一次），做完清掉选择——操作后行集合通常会变，
// 残留键只会让「已选 N 项」数到看不见的行。
package gui

import (
	"fmt"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// pruneSelection 把选择集中已不在当前视图里的行键清掉（refresh 末尾调用）。
// 换 tab / 改筛选 / 外部（MCP）删任务都会改变可见行集合；选择键 = 行键
// （分组首行 = 组键），重组分组后键集合变化，旧选择自然被清掉，
// 保证「已选 N 项」永远只数看得见的行。
func (m *model) pruneSelection(views []taskView) {
	if m.chosen.Len() == 0 {
		return
	}
	visible := make(map[int]struct{}, len(views))
	for i := range views {
		visible[views[i].rowKey] = struct{}{}
	}
	for key := range m.chosen.All() {
		if _, ok := visible[key]; !ok {
			m.chosen.Remove(key)
		}
	}
}

// selectionCounts 数当前视图里被选中的行，并按批量操作的适用性分类：
// active=未完成（可批量完成）、done=已完成未归档（可批量归档）、
// archived=已归档（可批量取消归档）。纯读 m.tasks 与 chosen，无分配。
func (m *model) selectionCounts() (count, active, done, archived int) {
	for i := range m.tasks {
		view := &m.tasks[i]
		if view.Task == nil || !m.chosen.Has(view.rowKey) {
			continue
		}
		count++
		switch {
		case view.ArchivedAt != nil:
			archived++
		case view.Status == 0:
			active++
		default:
			done++
		}
	}
	return count, active, done, archived
}

// indexHeaderCell 序号列头：定宽容器内的「全选」复选框（可见标题=列名「序号」）。
// 容器必须固定列宽（与行的 gridCell 同法），否则复选框自然宽会把整个列头
// 推宽、与行错位。全选框不区分半选：全部选中=勾，其余=空。
func (m *model) indexHeaderCell(c ui.C) {
	ui.Row(c).Width(m.colWidth(colIndex)).Children(func() {
		all := m.chosen.Len() == len(m.tasks) && len(m.tasks) > 0
		if ui.Checkbox(c, &all, "序号").Label("全选").HitExact().Changed() {
			if all {
				m.selectAllRows()
			} else {
				m.chosen.Clear()
			}
		}
	})
}

// selectionToolbar 工具栏右端的批量操作区：有选中行才出现。
// 「已选 N 项」文案缓存（Sprintf 只在计数变化时发生），选择变化时
// 批量删除的确认态重新计。按钮只按当前选择里有的行类别出现。
func (m *model) selectionToolbar(c ui.C) {
	count, active, done, archived := m.selectionCounts()
	if count == 0 {
		return
	}
	if count != m.selCount {
		m.selCount, m.selLine = count, fmt.Sprintf("已选 %d 项", count)
		m.confirmBatchDel = false
	}
	theme := c.Theme()
	ui.Text(c, m.selLine).FontSize(theme.Metrics.FontSizeS).Weight(gfx.WeightMedium).Color(theme.Primary)
	if active > 0 {
		if ui.Button(c, "批量完成").Small().Ghost().Clicked() {
			m.completeSelected()
		}
	}
	if done > 0 {
		if ui.Button(c, "批量归档").Small().Ghost().Clicked() {
			m.archiveSelected(true)
		}
	}
	if archived > 0 {
		if ui.Button(c, "批量取消归档").Small().Ghost().Clicked() {
			m.archiveSelected(false)
		}
	}
	deleteLabel := "批量删除"
	if m.confirmBatchDel {
		deleteLabel = "确认批量删除？"
	}
	deleteBtn := ui.Button(c, deleteLabel).Small()
	if m.confirmBatchDel {
		deleteBtn = deleteBtn.Destructive()
	} else {
		deleteBtn = deleteBtn.Ghost()
	}
	if deleteBtn.Clicked() {
		m.deleteSelected()
	}
	if ui.Button(c, "清除选择").Small().Ghost().Clicked() {
		m.chosen.Clear()
	}
}

// selectAllRows 全选当前视图的任务行（列头「全选」复选框勾上时）。
func (m *model) selectAllRows() {
	for i := range m.tasks {
		if view := &m.tasks[i]; view.Task != nil {
			m.chosen.Add(view.rowKey)
		}
	}
}

// selectedTasks 按视图顺序返回选中的任务快照，供批量操作在一个 apply 内消费。
func (m *model) selectedTasks() []*store.Task {
	tasks := make([]*store.Task, 0, m.chosen.Len())
	for i := range m.tasks {
		if view := &m.tasks[i]; view.Task != nil && m.chosen.Has(view.rowKey) {
			tasks = append(tasks, view.Task)
		}
	}
	return tasks
}

// applyBatchSelection 对选中的任务逐个执行 op，全部做完后重查一次；
// regenDoc = 归档类操作结束后重写知识文档（与单行归档同口径）。
// 先清掉选择再执行，避免重查期间 prune 与批量操作抢选择集。
func (m *model) applyBatchSelection(op func(task *store.Task) error, regenDoc bool) {
	tasks := m.selectedTasks()
	m.chosen.Clear()
	m.confirmBatchDel = false
	m.apply(func() error {
		for _, task := range tasks {
			if err := op(task); err != nil {
				return err
			}
		}
		if regenDoc {
			if _, err := m.repo.GenerateArchiveDoc(); err != nil {
				return err
			}
		}
		return nil
	})
}

// completeSelected 批量完成：只动快照里未完成的行（已完成的跳过，不报错）。
func (m *model) completeSelected() {
	m.applyBatchSelection(func(task *store.Task) error {
		if task.Status != 0 {
			return nil
		}
		_, err := m.repo.Complete(task.ID)
		return err
	}, false)
}

// archiveSelected 批量归档 / 批量取消归档：只动快照里适用的行，结束后重写知识文档。
func (m *model) archiveSelected(archive bool) {
	m.applyBatchSelection(func(task *store.Task) error {
		if archive {
			if task.Status != 1 || task.ArchivedAt != nil {
				return nil
			}
			_, err := m.repo.Archive(task.ID)
			return err
		}
		if task.ArchivedAt == nil {
			return nil
		}
		_, err := m.repo.Unarchive(task.ID)
		return err
	}, true)
}

// deleteSelected 批量删除：两次确认（与单行删除同口径），第一次只亮「确认批量删除？」。
func (m *model) deleteSelected() {
	if !m.confirmBatchDel {
		m.confirmBatchDel = true
		return
	}
	m.applyBatchSelection(func(task *store.Task) error {
		_, err := m.repo.Remove(task.ID)
		return err
	}, false)
}
