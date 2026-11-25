package gui

import (
	"encoding/json"
	"strconv"
	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// 表格列（参照多维表格第一版：完成/待办事项/自定义字段…/优先级/截止时间/时间/操作）。
// 列键同时也是 hiddenCols/colStates 的键。
const (
	gridGap      float32 = 8
	gridPadX     float32 = 8
	splitThick           = 6 // 列头分隔条占位（列距 = 列宽 + 6，行内同步）
	colDone              = "done"
	colTitle             = "title"
	colPriority          = "priority"
	colDue               = "due"
	colRemaining         = "remaining"
	colAction            = "action"
	popoverWidth         = 240
)

// defaultColWidth 各列默认宽；自定义字段列统一 colFieldWidth。
func defaultColWidth(key string) float32 {
	switch key {
	case colDone:
		return 52
	case colTitle:
		return 240
	case colPriority:
		return 76
	case colDue:
		return 128
	case colRemaining:
		return 84
	case colAction:
		return 64
	default: // 自定义字段
		return 128
	}
}

// colKeyOfField 自定义字段列键 = 字段名。
func (m *model) colWidth(key string) float32 {
	if state, ok := m.colStates[key]; ok {
		return state.Pos
	}
	return defaultColWidth(key)
}

// gridCol 一列的键、宽与头。
type gridCol struct {
	key   string
	label string
}

// gridCols 当前列序列（已滤掉隐藏列；字段列序 = m.fields 顺序，可经列设置调整）。
// 完成/待办事项列固定：不受 hiddenCols 影响，列设置面板也不提供其勾选框。
func (m *model) gridCols() []gridCol {
	cols := make([]gridCol, 0, len(m.fields)+6)
	cols = append(cols, gridCol{colDone, "完成"})
	cols = append(cols, gridCol{colTitle, "待办事项"})
	for _, field := range m.fields {
		if !m.hiddenCols[field.Name] {
			cols = append(cols, gridCol{field.Name, field.Name})
		}
	}
	if !m.hiddenCols[colPriority] {
		cols = append(cols, gridCol{colPriority, "优先级"})
	}
	if !m.hiddenCols[colDue] {
		cols = append(cols, gridCol{colDue, "截止时间"})
	}
	if !m.hiddenCols[colRemaining] {
		cols = append(cols, gridCol{colRemaining, "时间"})
	}
	cols = append(cols, gridCol{colAction, "操作"}) // 操作列固定，保证编辑/删除入口
	return cols
}

// layoutRowWidth 行/列头总宽 = 左右内边距 + 各列(宽+分隔条) − 末列分隔条。
// 每次重绘现算（纯算术无分配），列宽拖拽与显隐即时生效。
func (m *model) layoutRowWidth() float32 {
	cols := m.gridCols()
	total := gridPadX * 2
	for i, col := range cols {
		total += m.colWidth(col.key)
		if i < len(cols)-1 {
			total += splitThick
		}
	}
	return total
}

// gridCell 定宽单元格容器：行内网格对齐的关键（内容宽度不定，容器定宽）。
// trailing 为列距补偿（末列 0，其余 = splitThick，与列头的分隔条占位对齐）。
func (m *model) gridCell(c ui.C, key string, trailing float32, content func()) {
	ui.Row(c).Width(m.colWidth(key) + trailing).Children(content)
}

// page 每次重绘只读 model 并在控件声明处处理点击；页面内无 goroutine、
// 定时器或轮询（官方 skill §2/§7.2 硬规则）。
//
// 布局（2026-11-25 表格视图，参照多维表格）：头部（标题+状态摘要）→ 快捷新建
// → Tab → 工具栏（字段/筛选/分组/排序，后三者为 Popover 面板）→ 错误条
// → 列头行 → 表格（横向随列表滚动，ScrollWith）→ 计数。
func (m *model) page(c ui.C) {
	m.setRowMeta(c)
	theme := c.Theme()
	ui.Column(c).Fill().Pad(16).Gap(8).Children(func() {
		// 头部：标题 + 状态摘要（refresh 时缓存，非每帧计算）
		ui.Row(c).Gap(12).Items(ui.Center).Children(func() {
			ui.Text(c, "待办").FontSize(20).Weight(gfx.WeightSemibold)
			if m.statusLine != "" {
				ui.Text(c, m.statusLine).FontSize(theme.Metrics.FontSizeS).Muted()
			}
		})

		// 快捷新建
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if ui.TextField(c, &m.quickAdd, "快捷新建", "输入任务标题，回车即新建").Grow(1).Submitted() {
				m.quickAddSubmit()
			}
			if ui.Button(c, "新建").Small().Clicked() {
				m.quickAddSubmit()
			}
		})

		// 状态筛选 Tab
		if ui.Tabs(c, &m.tab, filterTabs, nil).FillWidth().Changed() {
			m.errorText = ""
			m.applyQueryChange()
			m.confirmID = ""
			m.editingID = ""
		}

		// 工具栏：字段管理 + 筛选/分组/排序（Popover 面板，参照多维表格）
		m.fieldToolbar(c)

		if m.errorText != "" {
			ui.Text(c, m.errorText).Color(theme.Danger).FontSize(theme.Metrics.FontSizeS)
		}

		if len(m.tasks) == 0 {
			m.emptyState(c)
		} else {
			// 行宽随列宽拖拽/列显隐即时变化：每次重绘现算并写入列表状态
			rowWidth := m.layoutRowWidth()
			m.list.RowWidth = rowWidth
			// 列头行：横向跟随列表滚动（ScrollWith）；列间分隔条可拖拽调宽。
			// 不用 Items(Center)：分隔条固有高度 0，居中会让命中区变成 6×0。
			ui.Row(c).Height(28).Clip().FillWidth().Children(func() {
				ui.Row(c).Width(rowWidth).ScrollWith(&m.list).PadX(gridPadX).Children(func() {
					cols := m.gridCols()
					for i, col := range cols {
						ui.Text(c, col.label).Width(m.colWidth(col.key)).FontSize(theme.Metrics.FontSizeS).Muted().Self(ui.Center)
						if i < len(cols)-1 {
							m.splitterOf(col.key)
							ui.Splitter(c, m.colStates[col.key]).SplitterThickness(splitThick).Mins(48, 48)
						}
					}
				})
			})
			// 任务表格（分组模式下 groupHeaders 标记组头；行宽由 RowWidth 保证）
			ui.List(c, &m.list, len(m.tasks), func(row ui.ListRow) {
				m.taskRow(c, row)
			}).Grow(1).FillWidth().Label("任务列表")
		}

		// 底部计数
		if m.countLine != "" {
			ui.Text(c, m.countLine).FontSize(theme.Metrics.FontSizeS).Muted()
		}
	})

	ui.Modal(c, &m.fieldModalOpen, "字段",
		func() { m.fieldModalBody(c) },
		func() { m.fieldModalFooter(c) })
}

// splitterOf 确保列键的分隔条状态存在（Pos 即列宽，拖拽实时写回）。
func (m *model) splitterOf(key string) {
	if _, ok := m.colStates[key]; !ok {
		m.colStates[key] = &ui.SplitterState{Pos: defaultColWidth(key)}
	}
}

// allGridCols 全部列（含隐藏的），供列设置面板勾选。
func (m *model) allGridCols() []gridCol {
	cols := make([]gridCol, 0, len(m.fields)+6)
	cols = append(cols, gridCol{colDone, "完成"}, gridCol{colTitle, "待办事项"})
	for _, field := range m.fields {
		cols = append(cols, gridCol{field.Name, field.Name})
	}
	cols = append(cols,
		gridCol{colPriority, "优先级"},
		gridCol{colDue, "截止时间"},
		gridCol{colRemaining, "时间"},
		gridCol{colAction, "操作"})
	return cols
}

// emptyState 空列表提示：空状态是行动邀请（frontend-design skill），
// 按当前 tab 给出不同引导文案；「新建」按钮与顶部快捷新建同一动作。
func (m *model) emptyState(c ui.C) {
	emptyText := "在上方输入标题，回车即可新建第一条任务"
	switch m.tab {
	case filterTabs[1]:
		emptyText = "没有进行中的任务；新建一条，或切回「全部」查看已完成项"
	case filterTabs[2]:
		emptyText = "还没有完成任何任务；勾选一条任务后它会出现在这里"
	}
	ui.Column(c).FillWidth().FillHeight().Center().Gap(8).Children(func() {
		ui.Text(c, "暂无任务").FontSize(16).Weight(gfx.WeightMedium)
		ui.Text(c, emptyText).FontSize(12).Muted().MaxWidth(360)
		if ui.Button(c, "新建").Outline().Small().Clicked() {
			m.quickAddSubmit()
		}
	})
}

// fieldToolbar 工具栏：字段管理入口 + 筛选/排序/分组 Popover 按钮（带计数，
// 参照多维表格「筛选 1」）。打开一个面板时收起其它。
func (m *model) fieldToolbar(c ui.C) {
	ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
		if ui.Button(c, "字段").Small().Ghost().Clicked() {
			m.openFieldModal(nil)
		}

		filterBtn := ui.Button(c, m.filterLabel).Small().Ghost()
		if filterBtn.Clicked() {
			m.filterOpen = true
			m.sortOpen, m.groupOpen = false, false
		}
		ui.Popover(c, filterBtn, &m.filterOpen, func() { m.filterPanel(c) })

		sortBtn := ui.Button(c, m.sortLabel).Small().Ghost()
		if sortBtn.Clicked() {
			m.sortOpen = true
			m.filterOpen, m.groupOpen = false, false
		}
		ui.Popover(c, sortBtn, &m.sortOpen, func() { m.sortPanel(c) })

		groupBtn := ui.Button(c, m.groupLabel).Small().Ghost()
		if groupBtn.Clicked() {
			m.groupOpen = true
			m.filterOpen, m.sortOpen = false, false
		}
		ui.Popover(c, groupBtn, &m.groupOpen, func() { m.groupPanel(c) })

		colsBtn := ui.Button(c, "列设置").Small().Ghost()
		if colsBtn.Clicked() {
			m.colsOpen = true
			m.filterOpen, m.sortOpen, m.groupOpen = false, false, false
		}
		ui.PopoverWith(c, colsBtn, ui.PopoverSpec{Width: 448}, &m.colsOpen, func() { m.colsPanel(c) })
	})
}

// colsPanel 列设置面板（2026-11-25 ③）：显示/隐藏列 + 自定义字段列序。
// 列宽在列头分隔条上直接拖拽，不在此面板。
func (m *model) colsPanel(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "显示列").FontSize(12).Muted()
		ui.Column(c).Gap(4).Children(func() {
			for _, col := range m.allGridCols() {
				if col.key == colDone || col.key == colTitle || col.key == colAction {
					continue // 完成/待办事项/操作列固定
				}
				checked := !m.hiddenCols[col.key]
				if ui.Checkbox(c, &checked, col.label).Changed() {
					m.hiddenCols[col.key] = !checked
				}
			}
		})
		if len(m.fields) > 0 {
			ui.Text(c, "字段列序").FontSize(12).Muted()
			ui.Column(c).Gap(4).Children(func() {
				for _, field := range m.fields {
					field := field
					ui.Row(c.Key(field.ID)).Gap(4).Items(ui.Center).Children(func() {
						ui.Text(c, field.Name).Grow(1).Ellipsis().FontSize(12)
						if ui.Button(c, "上移").XSmall().Ghost().Clicked() {
							m.moveField(field.ID, -1)
						}
						if ui.Button(c, "下移").XSmall().Ghost().Clicked() {
							m.moveField(field.ID, 1)
						}
						// 删除自定义列：二次确认（第一次置 confirmFieldID，按钮变「确认删除？」，第二次才删）
						deleteLabel := "删除 " + field.Name
						if m.confirmFieldID == field.ID {
							deleteLabel = "确认删除？"
						}
						if ui.Button(c, deleteLabel).XSmall().Destructive().Clicked() {
							m.deleteField(field.ID)
						}
					})
				}
			})
		}
	})
}

// choiceRow 面板里的字段/条件按钮组：选中=实心主色，未选=描边。
// 点击即应用（面板内不放 Select：Popover 嵌套下拉会被 dismiss 层吞点击）。
func choiceRow(c ui.C, choices []string, current string, onPick func(key string)) {
	ui.Flow(c).Gap(6).LineGap(6).Children(func() {
		for _, key := range choices {
			label := key
			active := key == current
			var button ui.Box
			if active {
				button = ui.Button(c, label).XSmall()
			} else {
				button = ui.Button(c, label).XSmall().Outline()
			}
			if button.Clicked() {
				onPick(key)
			}
		}
	})
}

// fieldKeyLabels / fieldKeyOf 在字段键与显示文案间转换（choiceRow 以文案为键）。
func fieldKeyLabels(keys []string) []string {
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, fieldKeyLabel(key))
	}
	return labels
}

func fieldKeyOf(label string) (string, bool) {
	for _, key := range builtinFieldKeys {
		if fieldKeyLabel(key) == label {
			return key, true
		}
	}
	// 自定义字段名与文案一致
	return label, true
}

// condValueText 条件值短文案（chips 显示用）。
func condValueText(cond store.FilterCond) string {
	if cond.Value == nil {
		return ""
	}
	var text string
	if json.Unmarshal(cond.Value, &text) == nil {
		return text
	}
	var num float64
	if json.Unmarshal(cond.Value, &num) == nil {
		return strconv.FormatFloat(num, 'f', -1, 64)
	}
	return string(cond.Value)
}

// filterPanel 筛选面板（2026-11-25 ② 多条件）：生效条件 chips（可单条移除）
// + 待添加条件草稿（字段 → 条件 → 值 → 添加条件）。
func (m *model) filterPanel(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		if len(m.filterConds) > 0 {
			ui.Text(c, "生效条件").FontSize(12).Muted()
			ui.Column(c).Gap(4).Children(func() {
				for i, cond := range m.filterConds {
					i, cond := i, cond
					chip := fieldKeyLabel(cond.Field) + " " + filterOpLabel(cond.Op)
					if text := condValueText(cond); text != "" {
						chip += " " + text
					}
					ui.Row(c).Gap(4).Items(ui.Center).Children(func() {
						ui.Badge(c, chip).Secondary()
						if ui.Button(c, "移除 "+chip).XSmall().Ghost().Clicked() {
							m.removeFilterCond(i)
							m.applyQueryChange()
						}
					})
				}
			})
			if ui.Button(c, "清除筛选").Small().Ghost().Clicked() {
				m.filterConds = nil
				m.applyQueryChange()
			}
		}

		ui.Text(c, "添加条件").FontSize(12).Muted()
		choiceRow(c, fieldKeyLabels(m.fieldKeys()), fieldKeyLabel(m.draftField), func(label string) {
			if key, ok := fieldKeyOf(label); ok && m.draftField != key {
				m.draftField, m.draftOp, m.draftValue = key, "", ""
			}
		})
		if m.draftField != "" {
			ops := m.filterOpsForField()
			labels := make([]string, 0, len(ops))
			for _, op := range ops {
				labels = append(labels, filterOpLabel(op))
			}
			choiceRow(c, labels, filterOpLabel(m.draftOp), func(label string) {
				for _, op := range ops {
					if filterOpLabel(op) == label {
						m.draftOp = op
					}
				}
			})
			if m.draftOp != "" {
				if m.draftOp == store.OpEmpty || m.draftOp == store.OpNotEmpty {
					if ui.Button(c, "添加条件").Small().Clicked() {
						if m.addFilterCond() {
							m.applyQueryChange()
						}
					}
				} else {
					if ui.TextField(c, &m.draftValue, "筛选值", "值").FillWidth().Submitted() {
						if m.addFilterCond() {
							m.applyQueryChange()
						}
					}
					if ui.Button(c, "添加条件").Small().Clicked() {
						if m.addFilterCond() {
							m.applyQueryChange()
						}
					}
				}
			}
		}
	})
}

// sortPanel 排序面板：字段按钮组（含「无」=默认排序）+ 方向切换。
func (m *model) sortPanel(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "按字段排序").FontSize(12).Muted()
		choiceRow(c, fieldKeyLabels(m.fieldKeys()), fieldKeyLabel(m.sortField), func(label string) {
			if key, ok := fieldKeyOf(label); ok {
				m.sortField = key
				m.applyQueryChange()
				m.sortOpen = false
			}
		})
		if m.sortField != "" {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if ui.Button(c, m.sortDirectionLabel()).Small().Clicked() {
					m.sortDesc = !m.sortDesc
					m.applyQueryChange()
				}
				if ui.Button(c, "清除排序").Small().Ghost().Clicked() {
					m.sortField = ""
					m.applyQueryChange()
					m.sortOpen = false
				}
			})
		}
	})
}

// groupPanel 分组面板：字段按钮组（含「无」=平铺）。
func (m *model) groupPanel(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "按字段分组").FontSize(12).Muted()
		choiceRow(c, fieldKeyLabels(m.groupKeys()), fieldKeyLabel(m.groupBy), func(label string) {
			if key, ok := fieldKeyOf(label); ok {
				m.groupBy = key
				m.applyQueryChange()
				m.groupOpen = false
			}
		})
		if m.groupBy != "" && ui.Button(c, "清除分组").Small().Ghost().Clicked() {
			m.groupBy = ""
			m.applyQueryChange()
			m.groupOpen = false
		}
	})
}

func (m *model) sortDirectionLabel() string {
	if m.sortDesc {
		return "降序"
	}
	return "升序"
}

// fieldKeys / groupKeys 返回可选字段键（内置 + 自定义字段名）。
func (m *model) fieldKeys() []string {
	keys := []string{""}
	keys = append(keys, builtinFieldKeys...)
	for _, field := range m.fields {
		keys = append(keys, field.Name)
	}
	return keys
}

func (m *model) groupKeys() []string {
	keys := []string{""}
	keys = append(keys, builtinFieldKeys...)
	for _, field := range m.fields {
		if field.Type != store.FieldTypeMultiSelect {
			keys = append(keys, field.Name)
		}
	}
	return keys
}

var builtinFieldKeys = []string{"title", "priority", "due_at", "status", "created_at"}

func fieldKeyLabel(key string) string {
	if key == "" {
		return "无"
	}
	switch key {
	case "title":
		return "标题"
	case "priority":
		return "优先级"
	case "due_at":
		return "到期时间"
	case "status":
		return "状态"
	case "created_at":
		return "创建时间"
	}
	return key
}

// filterOpsForField 按字段类型收窄操作符（作用于待添加条件的字段）。
func (m *model) filterOpsForField() []store.FilterOp {
	kind := m.fieldTypeOf(m.draftField)
	ops := make([]store.FilterOp, 0, 4)
	switch kind {
	case store.FieldTypeText:
		ops = append(ops, store.OpEq, store.OpContains)
	case store.FieldTypeNumber:
		ops = append(ops, store.OpEq, store.OpGt, store.OpLt)
	case store.FieldTypeSingleSelect:
		ops = append(ops, store.OpIs)
	case store.FieldTypeMultiSelect:
		ops = append(ops, store.OpHas)
	case store.FieldTypeDate:
		ops = append(ops, store.OpBefore, store.OpAfter)
	}
	return append(ops, store.OpEmpty, store.OpNotEmpty)
}

func (m *model) fieldTypeOf(name string) store.FieldType {
	switch name {
	case "title":
		return store.FieldTypeText
	case "priority", "status":
		return store.FieldTypeNumber
	case "due_at", "created_at":
		return store.FieldTypeDate
	}
	for _, field := range m.fields {
		if field.Name == name {
			return field.Type
		}
	}
	return store.FieldTypeText
}

// fieldModalBody 字段编辑弹窗主体：名称 + 类型网格 + 选项输入（参考图布局）。
func (m *model) fieldModalBody(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.TextField(c, &m.fieldName, "字段名称", "输入字段名称")
		ui.Paragraph(c, "选择字段类型", 1).Muted()
		ui.Grid(c, 180).FillWidth().Children(func() {
			for index, choice := range fieldTypeGrid {
				// 选中=实心主色，未选=描边；选中态由按钮本身表达，不再画对勾
				if index == m.fieldTypeIndex {
					if ui.Button(c, choice.label).Clicked() {
						m.fieldTypeIndex = index
					}
				} else if ui.Button(c, choice.label).Outline().Clicked() {
					m.fieldTypeIndex = index
				}
			}
		})
		if isSelectType(fieldTypeGrid[m.fieldTypeIndex].typeKey) {
			ui.TextField(c, &m.fieldOptions, "选项", "多个选项用逗号分隔")
		}
	})
}

// fieldModalFooter 弹窗底部：保存/取消/删除。
func (m *model) fieldModalFooter(c ui.C) {
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
		if ui.Button(c, "取消").Clicked() {
			m.closeFieldModal()
		}
		if m.fieldEditID != "" && ui.Button(c, "删除").Destructive().Clicked() {
			m.removeField(m.fieldEditID)
		}
		saveLabel := "创建字段"
		if m.fieldEditID != "" {
			saveLabel = "保存字段"
		}
		if ui.Button(c, saveLabel).Clicked() {
			if m.fieldEditID != "" {
				m.updateField(m.fieldEditID)
			} else {
				m.newField()
			}
		}
	})
}

// taskRow 表格单行：定宽单元格网格（列头经 ScrollWith 对齐）。
// 完成列（复选框，标题作可达名）→ 待办事项（点击=行内编辑）→ 自定义字段列
// → 优先级徽标 → 截止时间 → 时间（逾期/今天）→ 操作（删除，两次确认）。
// 逾期任务整行浅红着色（参照多维表格填色）；组头行渲染整宽灰底条。
func (m *model) taskRow(c ui.C, row ui.ListRow) {
	view := m.tasks[row.Index]
	if view.Task == nil {
		return
	}
	theme := m.pageTheme
	row.Element.FillWidth().PadX(gridPadX).PadY(4).Children(func() {
		ui.Column(row.C).FillWidth().Gap(4).Children(func() {
			if view.groupHeader {
				// 粘性组头（m.list.Header）：灰底条防滚动透出
				ui.Row(row.C).FillWidth().Background(theme.Muted).
					Radius(theme.Metrics.RadiusItem()).PadX(8).PadY(4).Children(func() {
					ui.Text(row.C, view.groupLabel).FontSize(13).Weight(gfx.WeightSemibold).Color(theme.Primary)
				})
			}

			cells := ui.Row(row.C).Items(ui.Center)
			if view.overdue {
				// 逾期整行浅红（alpha 着色取自主题 Danger，dark 下自动正确）
				cells = cells.Background(theme.Danger.WithAlpha(0.06))
			}
			cols := m.gridCols()
			cells.Children(func() {
				for i, col := range cols {
					key := col.key
					trailing := float32(splitThick)
					if i == len(cols)-1 {
						trailing = 0
					}
					m.gridCell(row.C, key, trailing, func() { m.cellContent(row.C, view, key) })
				}
			})

			if m.editingID == view.ID {
				m.editorRow(row.C, view.Task)
			}
		})
	})
}

// cellContent 按列键声明单元格内容（taskRow 的网格分派）。
func (m *model) cellContent(c ui.C, view taskView, key string) {
	theme := m.pageTheme
	switch key {
	case colDone:
		checked := view.Status == 1
		// 可见标题单独画在待办事项列；复选框只画方框，
		// 任务标题经 .Label 作为其可达名（测试按该名点击方框）。
		if ui.Checkbox(c, &checked, "").Label(view.Title).Changed() {
			m.toggleComplete(view.Task, checked)
		}
	case colTitle:
		title := ui.Text(c, view.Title).Width(m.colWidth(key) - 8).Ellipsis()
		if view.Status == 1 {
			title.Muted()
		}
		if title.Hit(ui.ID(c), "编辑任务:"+view.Title).Clicked() {
			m.beginEdit(view.Task)
		}
	case colPriority:
		if view.Priority > 0 {
			m.priorityBadge(c, view.Priority)
		}
	case colDue:
		if view.dueLabel != "" {
			due := ui.Text(c, view.dueLabel).Ellipsis().FontSize(12)
			switch {
			case view.overdue:
				due.Color(theme.Danger)
			case view.dueToday:
				due.Color(theme.Primary)
			default:
				due.Muted()
			}
		}
	case colRemaining:
		switch {
		case view.overdue:
			ui.Text(c, "已逾期").FontSize(12).Color(theme.Danger)
		case view.dueToday:
			ui.Text(c, "今天到期").FontSize(12).Color(theme.Primary)
		}
	case colAction:
		deleteLabel := "删除"
		if m.confirmID == view.ID {
			deleteLabel = "确认删除？"
		}
		if ui.Button(c, deleteLabel).Small().Ghost().Clicked() {
			m.deleteTask(view.ID)
		}
	default: // 自定义字段列（键 = 字段名）
		if field := m.fieldOf(key); field != nil {
			if raw, ok := view.Values[key]; ok {
				if label := fieldValueLabel(field, raw); label != "" {
					ui.Text(c, label).Ellipsis().FontSize(12).Color(theme.MutedForeground)
				}
			}
		}
	}
}

// fieldOf 按字段名查字段定义。
func (m *model) fieldOf(name string) *store.Field {
	for _, field := range m.fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

// editorRow 行内编辑：基础字段 + 自定义字段值（2026-11-25 ①）。
func (m *model) editorRow(c ui.C, task *store.Task) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextField(c, &m.editTitle, "标题", "").Width(280)
			ui.Text(c, "优先级").FontSize(12).Muted()
			ui.Select(c, &m.editPrio, priorityChoices, priorityLabel).Width(120).Label("优先级")
			ui.TextField(c, &m.editDue, "到期时间", localTimeLayout).Width(170)
			if ui.Button(c, "保存").Small().Clicked() {
				m.saveEdit(task.ID)
			}
			if ui.Button(c, "取消").Small().Ghost().Clicked() {
				m.endEdit()
			}
		})
		if len(m.fields) > 0 {
			ui.Flow(c).Gap(12).LineGap(8).Children(func() {
				for _, field := range m.fields {
					m.fieldEditor(c, field)
				}
			})
		}
	})
}

// fieldEditor 单个自定义字段的编辑控件（按类型分派；绑定 editValues 缓冲，
// 以副本 + Changed 写回——map 元素不可取址）。
func (m *model) fieldEditor(c ui.C, field *store.Field) {
	value := m.editValues[field.Name]
	switch field.Type {
	case store.FieldTypeSingleSelect:
		choices := append([]string{""}, field.Choices()...)
		sel := ui.Select(c, &value, choices, nil).Width(150).Label(field.Name)
		if sel.Changed() {
			m.editValues[field.Name] = value
		}
	case store.FieldTypeDate:
		if editor := ui.TextField(c, &value, field.Name, localTimeLayout).Width(150); editor.Changed() {
			m.editValues[field.Name] = value
		}
	case store.FieldTypeMultiSelect:
		if editor := ui.TextField(c, &value, field.Name, "逗号分隔").Width(150); editor.Changed() {
			m.editValues[field.Name] = value
		}
	case store.FieldTypeNumber:
		if editor := ui.TextField(c, &value, field.Name, "数字").Width(110); editor.Changed() {
			m.editValues[field.Name] = value
		}
	default:
		if editor := ui.TextField(c, &value, field.Name, "").Width(150); editor.Changed() {
			m.editValues[field.Name] = value
		}
	}
}
