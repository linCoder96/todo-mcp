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
	gridGap        float32 = 8
	gridPadX       float32 = 8
	splitThick             = 6 // 列头分隔条占位（列距 = 列宽 + 6，行内同步）
	colIndex               = "index"
	colDone                = "done"
	colTitle               = "title"
	colRemark              = "description"
	colPriority            = "priority"
	colDue                 = "due"
	colRemaining           = "remaining"
	colCreatedAt           = "created_at"
	colArchivedAt          = "archived_at"
	colCompletedAt         = "completed_at"
	colAction              = "action"
	popoverWidth           = 240
)

// defaultColWidth 各列默认宽；自定义字段列统一 colFieldWidth。
func defaultColWidth(key string) float32 {
	switch key {
	case colIndex:
		return 64 // 选择复选框 + 行号（列头还需容纳「序号」标题）
	case colDone:
		return 52
	case colTitle:
		return 180
	case colRemark:
		return 104
	case colPriority:
		return 64
	case colDue:
		return 116
	case colCreatedAt, colArchivedAt, colCompletedAt:
		return 128
	case colRemaining:
		return 84
	case colAction:
		return 120
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
// 序号/完成/待办事项/操作列固定：不受 hiddenCols 影响，列设置面板也不提供其勾选框。
func (m *model) gridCols() []gridCol {
	cols := make([]gridCol, 0, len(m.fields)+8)
	cols = append(cols, gridCol{colIndex, "序号"})
	cols = append(cols, gridCol{colDone, "完成"})
	cols = append(cols, gridCol{colTitle, "待办事项"})
	if !m.hiddenCols[colRemark] {
		cols = append(cols, gridCol{colRemark, "备注"})
	}
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
	if !m.hiddenCols[colCreatedAt] {
		cols = append(cols, gridCol{colCreatedAt, "创建时间"})
	}
	if !m.hiddenCols[colArchivedAt] {
		cols = append(cols, gridCol{colArchivedAt, "归档时间"})
	}
	if !m.hiddenCols[colCompletedAt] {
		cols = append(cols, gridCol{colCompletedAt, "完成时间"})
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
// 非末列在列距中线画 1px 竖线，与列头分隔条里的竖线位置一致（2026-11-26 表格线条）。
func (m *model) gridCell(c ui.C, key string, trailing float32, content func()) {
	cell := ui.Row(c).Width(m.colWidth(key) + trailing)
	if trailing > 0 {
		cell = cell.Draw(m.cellVline)
	}
	cell.Children(content)
}

// page 每次重绘只读 model 并在控件声明处处理点击；页面内无 goroutine、
// 定时器或轮询（官方 skill §2/§7.2 硬规则）。
//
// 布局（2026-11-25 表格视图，参照多维表格）：头部（标题+状态摘要）→ 快捷新建
// → Tab → 工具栏（列设置/筛选/分组/排序，后三者为 Popover 面板）→ 错误条
// → 列头行 → 表格（横向随列表滚动，ScrollWith）→ 计数。
func (m *model) page(c ui.C) {
	m.setRowMeta(c)
	ui.Row(c).Fill().Children(func() {
		// 左侧竖排导航页签：原功能归入「任务清单」，新增「仪表盘」
		// 宽度自适应：竖排页签自然宽 = 文字 + triggerPadding + 指示线，
		// 写死会在真实字体下裁字（headless CJK 测量偏小测不出来）
		ui.Column(c).Pad(8).Gap(8).Children(func() {
			if ui.Tabs(c, &m.section, navSections, nil).Line().Vertical().Changed() {
				m.errorText = ""
				m.endEdit()
			}
		})
		// 导航与内容区的竖分隔线（2026-11-26 表格与布局线条）：Row 里是竖线
		ui.Divider(c)
		ui.Column(c).Grow(1).Fill().Children(func() {
			if m.section == navSections[1] {
				m.dashboardPage(c)
			} else {
				m.taskListPage(c)
			}
		})
	})

	ui.Modal(c, &m.fieldModalOpen, "字段",
		func() { m.fieldModalBody(c) },
		func() { m.fieldModalFooter(c) })

	// 任务编辑弹窗（2026-11-26）：点标题打开；主体与底部按钮见 editorModal 系列。
	// 弹窗打开条件是 editModalOpen（Modal 由 ui 写 false：Esc/遮罩关闭）；
	// editingID 只是记录编辑的是哪个任务，关闭时由 endEdit 一并清掉。
	if m.editModalOpen && m.taskOf(m.editingID) != nil {
		ui.Modal(c, &m.editModalOpen, "编辑任务",
			func() { m.editorModalBody(c) },
			func() { m.editorModalFooter(c) })
	}
	// 用户用 Esc/遮罩关闭弹窗时 ui.Modal 只写了 editModalOpen=false，
	// 这里顺手清掉 editingID，避免残留（endEdit 幂等）。
	if !m.editModalOpen && m.editingID != "" {
		m.endEdit()
	}
	// 任务详情抽屉（附件：图片 + 文件）：入口是行内「图 N」「文 N」徽标
	m.detailDrawer(c)

	// 操作反馈 Toast（复制引用地址等）：整个页面最后声明一次
	ui.Toasts(c, &m.toaster)
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
	cols = append(cols, gridCol{colRemark, "备注"})
	for _, field := range m.fields {
		cols = append(cols, gridCol{field.Name, field.Name})
	}
	cols = append(cols,
		gridCol{colPriority, "优先级"},
		gridCol{colDue, "截止时间"},
		gridCol{colRemaining, "时间"},
		gridCol{colCreatedAt, "创建时间"},
		gridCol{colCompletedAt, "完成时间"},
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
	case filterTabs[3]:
		emptyText = "还没有归档任务；在「已完成」里点「归档」，任务会移到这里并整理进知识文档"
	}
	ui.Column(c).FillWidth().FillHeight().Center().Gap(8).Children(func() {
		ui.Text(c, "暂无任务").FontSize(16).Weight(gfx.WeightMedium)
		ui.Text(c, emptyText).FontSize(12).Muted().MaxWidth(360)
		if m.tab == filterTabs[3] {
			// 已归档空列表：行动入口是生成/打开知识文档，不是新建任务
			if ui.Button(c, "生成知识文档").Outline().Small().Clicked() {
				m.openArchiveDoc()
			}
			return
		}
		if ui.Button(c, "新建").Outline().Small().Clicked() {
			m.quickAddSubmit()
		}
	})
}

// fieldToolbar 工具栏：列设置（列显隐 + 字段管理入口）+ 筛选/排序/分组 Popover
// 按钮（带计数，参照多维表格「筛选 1」）。打开一个面板时收起其它。
// 字段的新建/修改/删除都在列设置面板内（底部「新增字段」与字段行「…」菜单），
// 不再单独提供「字段」按钮。右端是行多选的批量操作区（有选中行才出现）。
func (m *model) fieldToolbar(c ui.C) {
	ui.Row(c).FillWidth().Gap(8).Items(ui.Center).Children(func() {
		colsBtn := ui.Button(c, "列设置").Small().Ghost()
		if colsBtn.Clicked() {
			m.colsOpen = true
			m.filterOpen, m.sortOpen, m.groupOpen = false, false, false
		}
		ui.PopoverWith(c, colsBtn, ui.PopoverSpec{Width: 448}, &m.colsOpen, func() { m.colsPanel(c) })

		filterBtn := ui.Button(c, m.filterLabel).Small().Ghost()
		if filterBtn.Clicked() {
			m.filterOpen = true
			m.sortOpen, m.groupOpen, m.colsOpen = false, false, false
		}
		ui.Popover(c, filterBtn, &m.filterOpen, func() { m.filterPanel(c) })

		sortBtn := ui.Button(c, m.sortLabel).Small().Ghost()
		if sortBtn.Clicked() {
			m.sortOpen = true
			m.filterOpen, m.groupOpen, m.colsOpen = false, false, false
		}
		ui.Popover(c, sortBtn, &m.sortOpen, func() { m.sortPanel(c) })

		groupBtn := ui.Button(c, m.groupLabel).Small().Ghost()
		if groupBtn.Clicked() {
			m.groupOpen = true
			m.filterOpen, m.sortOpen, m.colsOpen = false, false, false
		}
		ui.Popover(c, groupBtn, &m.groupOpen, func() { m.groupPanel(c) })

		// 行多选的批量操作区（2026-11-26）：Spacer 推到右端，无选择时不占内容
		ui.Spacer(c)
		m.selectionToolbar(c)
	})
}

// colsPanel 列设置面板：显示/隐藏列 + 自定义字段行拖拽排序（中部「≡」把手）
// + 「…」菜单（上移/下移/修改/删除二次确认）+ 底部「新增字段」。
// 勾选框与「…」按钮是行内子控件，按它们走各自点击、不触发行拖拽；列宽在列头拖。
func (m *model) colsPanel(c ui.C) {
	m.beginFieldDrag()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "显示列").FontSize(12).Muted()
		ui.Column(c).Gap(4).Children(func() {
			for _, col := range m.allGridCols() {
				if col.key == colDone || col.key == colTitle || col.key == colAction {
					continue // 完成/待办事项/操作列固定
				}
				checked := !m.hiddenCols[col.key]
				field := m.fieldOf(col.key)
				keyC := c.Key(col.key)
				rowID := ui.ID(keyC)
				row := ui.Row(keyC).FillWidth().Gap(4).Items(ui.Center).
					Hit(rowID, "字段行:"+col.label)
				if field != nil {
					// 同一行既是拖拽源又是放置目标；身份表按 m.fields 顺序写入，
					// drop 时按身份查回源/目标下标。
					m.fieldDragRows = append(m.fieldDragRows, fieldDragRow{id: rowID, field: field})
					row = row.DragSource(&m.fieldDrag).
						DropTarget(&m.fieldDrag, m.fieldDragAccept, m.fieldDragDrop)
					if m.fieldDragTargetRow(rowID) {
						row = row.Background(c.Theme().Accent.WithAlpha(0.18))
					}
				}
				row.Children(func() {
					if ui.Checkbox(c, &checked, col.label).Changed() {
						m.hiddenCols[col.key] = !checked
					}
					if field != nil {
						// 把手无独立 Hit：按住它时事件落在整行身份上，触发拖拽。
						ui.Text(c, "≡").FontSize(12).Muted()
						ui.Spacer(c)
						m.fieldMenuButton(c, field)
					}
				})
			}
		})
		if ui.Button(c, "新增字段").Small().Ghost().Clicked() {
			m.openFieldModal(nil)
		}
	})
}

// fieldMenuButton 自定义字段行的「…」按钮：点击打开该行的菜单。
// 同一时间最多一个菜单打开（menuOpen + menuFieldID 定位所属行）。
func (m *model) fieldMenuButton(c ui.C, field *store.Field) {
	btn := ui.Button(c, "…").Ghost().XSmall()
	if btn.Clicked() {
		m.menuFieldID = field.ID
		m.menuOpen = true
	}
	if !m.menuOpen || m.menuFieldID != field.ID {
		return
	}
	ui.Menu(c, btn, &m.menuOpen, func(menu *ui.MenuItems) {
		index := -1
		for i, f := range m.fields {
			if f.ID == field.ID {
				index = i
				break
			}
		}
		if menu.Item("上移").Disabled(index <= 0).Clicked() {
			m.moveField(field.ID, -1)
		}
		if menu.Item("下移").Disabled(index >= len(m.fields)-1).Clicked() {
			m.moveField(field.ID, 1)
		}
		if menu.Item("修改字段").Clicked() {
			m.openFieldModal(field)
		}
		menu.Separator()
		deleteLabel := "删除"
		if m.confirmFieldID == field.ID {
			deleteLabel = "确认删除？"
		}
		// 删除自定义列：二次确认（第一次置 confirmFieldID，菜单项变「确认删除？」，第二次才删）
		if menu.Item(deleteLabel).Destructive().Clicked() {
			m.deleteField(field.ID)
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

var builtinFieldKeys = []string{"title", "description", "priority", "due_at", "status", "created_at", "completed_at"}

func fieldKeyLabel(key string) string {
	if key == "" {
		return "无"
	}
	switch key {
	case "description":
		return "备注"
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
	case "completed_at":
		return "完成时间"
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
	case "title", "description":
		return store.FieldTypeText
	case "priority", "status":
		return store.FieldTypeNumber
	case "due_at", "created_at", "completed_at":
		return store.FieldTypeDate
	}
	for _, field := range m.fields {
		if field.Name == name {
			return field.Type
		}
	}
	return store.FieldTypeText
}

// fieldModalBody 字段编辑弹窗主体：名称 + 类型下拉 + 选项输入。
func (m *model) fieldModalBody(c ui.C) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.TextField(c, &m.fieldName, "字段名称", "输入字段名称")
		ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
			ui.Text(c, "选择字段类型").FontSize(theme.Metrics.FontSizeS).Muted()
			// 类型改为下拉选择（任务 01a21aa7）：8 个类型收敛为一个 Select，
			// 选中态由触发器文案表达；单选/多选仍按选择追加选项输入框。
			ui.Select(c, &m.fieldTypeSel, fieldTypeGridChoices(), fieldTypeGridLabel).
				Width(160).Label("选择字段类型")
		})
		if isSelectType(m.fieldTypeSel.typeKey) {
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
// 序号列（选择复选框 + 行号）→ 完成列（复选框，标题作可达名）→ 待办事项
// （点击=行内编辑）→ 自定义字段列 → 优先级徽标 → 截止时间 → 时间（逾期/今天）
// → 操作（删除，两次确认）。选中行主题 Selection 填色，逾期任务整行浅红着色
// （参照多维表格填色）；组头行渲染整宽灰底条；行底有 1px 横线（2026-11-26 线条）。
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

			cells := ui.Row(row.C).Items(ui.Center).Draw(m.rowHline) // 行底横线
			if selected := m.chosen.Has(view.rowKey); selected {
				// 选中行：主题 Selection 填色（与框架表格选中一致），优先于逾期着色
				cells = cells.Background(theme.Selection)
			} else if view.overdue {
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
					field := m.fieldOf(key)
					if field == nil {
						m.gridCell(row.C, key, trailing, func() { m.cellContent(row.C, view, key) })
						continue
					}
					m.fieldCell(row.C, view, field, m.colWidth(key)+trailing)
				}
			})
		})
	})
}

// cellContent 按列键声明单元格内容（taskRow 的网格分派）。
func (m *model) cellContent(c ui.C, view taskView, key string) {
	theme := m.pageTheme
	switch key {
	case colIndex:
		// 序号 + 行选择（2026-11-26 行多选）：复选框的可见标题就是行号，
		// HitExact 让点击目标覆盖整个复选框槽位（方框+行号都点得到）；
		// 可达名单独给「选择:标题」；点行身空白处仍走列表原生的
		// 单选 / Ctrl 点选 / Shift 连选。
		selected := m.chosen.Has(view.rowKey)
		if ui.Checkbox(c, &selected, view.rowNoLabel).Label("选择:" + view.Title).HitExact().Changed() {
			if selected {
				m.chosen.Add(view.rowKey)
			} else {
				m.chosen.Remove(view.rowKey)
			}
		}
	case colDone:
		checked := view.Status == 1
		// 可见标题单独画在待办事项列；复选框只画方框，
		// 任务标题经 .Label 作为其可达名（测试按该名点击方框）。
		if ui.Checkbox(c, &checked, "").Label(view.Title).Changed() {
			m.toggleComplete(view.Task, checked)
		}
	case colTitle:
		// 标题 + 图片徽标（有图才画；点击徽标打开详情抽屉）
		ui.Row(c).Gap(4).Items(ui.Center).FillWidth().Children(func() {
			title := ui.Text(c, view.Title).Grow(1).Ellipsis()
			if view.Status == 1 {
				title.Muted()
			}
			if title.Hit(ui.ID(c), "编辑任务:"+view.Title).Clicked() {
				m.beginEdit(view.Task)
			}
			if view.imageCount > 0 {
				// 徽标套 Hit 容器而不是对徽标 .Hit：主题化徽标（ui.Badge）被
				// .Hit(id, label) 时 label 会顶掉它画的文本。
				badge := ui.Row(c).Shrink(0).Hit(ui.ID(c), "任务图片:"+view.Title).Children(func() {
					ui.Badge(c, view.imageBadge).Secondary()
				})
				if badge.Clicked() {
					m.openDetails(view.Task)
				}
			}
			if view.fileCount > 0 {
				badge := ui.Row(c).Shrink(0).Hit(ui.ID(c), "任务文件:"+view.Title).Children(func() {
					ui.Badge(c, view.fileBadge).Secondary()
				})
				if badge.Clicked() {
					m.openDetails(view.Task)
				}
			}
		})
	case colRemark:
		if m.cellEditing(view.ID, key) {
			if ui.TextField(c, &m.cellValue, "编辑 备注", "备注，留空清除").Grow(1).AutoFocus().Submitted() {
				m.commitRemarkCell()
			}
			return
		}
		remark := ""
		if view.Description != nil {
			remark = *view.Description
		}
		if remark == "" {
			remark = "—"
		}
		text := ui.Text(c, remark).Grow(1).Ellipsis().FontSize(12).Color(theme.MutedForeground)
		if text.Hit(ui.ID(c), "编辑 备注:"+view.Title).Clicked() {
			m.beginRemarkCellEdit(view.Task)
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
	case colArchivedAt:
		if view.archivedLabel != "" {
			ui.Text(c, view.archivedLabel).Ellipsis().FontSize(12).Color(theme.MutedForeground)
		} else {
			ui.Text(c, "—").FontSize(12).Color(theme.MutedForeground.WithAlpha(0.45))
		}
	case colCreatedAt:
		ui.Text(c, view.createdLabel).Ellipsis().FontSize(12).Color(theme.MutedForeground)
	case colCompletedAt:
		if view.completedLabel != "" {
			ui.Text(c, view.completedLabel).Ellipsis().FontSize(12).Color(theme.MutedForeground)
		} else {
			ui.Text(c, "—").FontSize(12).Color(theme.MutedForeground.WithAlpha(0.45))
		}
	case colRemaining:
		switch {
		case view.overdue:
			ui.Text(c, "已逾期").FontSize(12).Color(theme.Danger)
		case view.dueToday:
			ui.Text(c, "今天到期").FontSize(12).Color(theme.Primary)
		}
	case colAction:
		if view.Status == 1 && view.ArchivedAt == nil {
			// 已完成未归档：归档入口（点击即归档并重写知识文档）
			if ui.Button(c, "归档").Small().Ghost().Clicked() {
				m.archiveTask(view.ID, true)
			}
		}
		if view.ArchivedAt != nil {
			// 已归档：取消归档入口
			if ui.Button(c, "取消归档").Small().Ghost().Clicked() {
				m.archiveTask(view.ID, false)
			}
		}
		deleteLabel := "删除"
		if m.confirmID == view.ID {
			deleteLabel = "确认删除？"
		}
		if ui.Button(c, deleteLabel).Small().Ghost().Clicked() {
			m.deleteTask(view.ID)
		}
	default: // 自定义字段列（键 = 字段名）：见 fieldCell / cellEditor
		field := m.fieldOf(key)
		if field == nil {
			return
		}
		if m.cellEditing(view.ID, key) {
			m.cellEditor(c, field, m.colWidth(key))
			return
		}
		if raw, ok := view.Values[key]; ok {
			if label := fieldValueLabel(field, raw); label != "" {
				ui.Text(c, label).Grow(1).Ellipsis().FontSize(12).Color(theme.MutedForeground)
				return
			}
		}
		// 空单元格：淡破折号占位，整格仍是点击目标
		ui.Text(c, "—").Grow(1).FontSize(12).Color(theme.MutedForeground.WithAlpha(0.45))
	}
}

// fieldCell 自定义字段单元格：整格可点，点击原地进入编辑态。
func (m *model) fieldCell(c ui.C, view taskView, field *store.Field, width float32) {
	cell := ui.Row(c).Items(ui.Center).Width(width)
	if width > m.colWidth(field.Name) {
		cell = cell.Draw(m.cellVline) // 非末列：列间竖线
	}
	if !m.cellEditing(view.ID, field.Name) {
		cell = cell.Hit(ui.ID(c), "编辑 "+field.Name+"："+view.Title).
			Hover(ui.Look{}.Background(m.pageTheme.Hover))
		if cell.Clicked() {
			m.beginCellEdit(view.Task, field)
		}
	}
	cell.Children(func() { m.cellContent(c, view, field.Name) })
}

// cellEditor 单元格编辑态的输入控件：单选下拉即选即存，其余是文本框，回车提交。
// 把文本清空后提交即删除该值；Esc 放弃（见 model.Key）。
func (m *model) cellEditor(c ui.C, field *store.Field, width float32) {
	label := "编辑 " + field.Name
	if field.Type == store.FieldTypeSingleSelect {
		choices := append([]string{""}, field.Choices()...)
		if ui.Select(c, &m.cellValue, choices, selectChoiceLabel).Width(width).Label(label).Changed() {
			m.commitCellEdit()
		}
		return
	}
	placeholder := ""
	switch field.Type {
	case store.FieldTypeNumber:
		placeholder = "数字，留空清除"
	case store.FieldTypeDate:
		placeholder = localTimeLayout + "，留空清除"
	case store.FieldTypeMultiSelect:
		placeholder = "逗号分隔，留空清除"
	}
	if ui.TextField(c, &m.cellValue, label, placeholder).Grow(1).AutoFocus().Submitted() {
		m.commitCellEdit()
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

// editorModalBody 编辑弹窗主体：标题 + 备注 + 优先级/到期 + 创建/完成时间（只读）
// + 自定义字段值（2026-11-26 由行内编辑器改造；Modal 自带 Esc/遮罩关闭）。
func (m *model) editorModalBody(c ui.C) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.TextField(c, &m.editTitle, "标题", "").FillWidth()
		ui.TextField(c, &m.editRemark, "备注", "").FillWidth()
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "优先级").FontSize(12).Muted()
			ui.Select(c, &m.editPrio, priorityChoices, priorityLabel).Width(120).Label("优先级")
			ui.Text(c, "到期时间").FontSize(12).Muted()
			ui.TextField(c, &m.editDue, "到期时间", localTimeLayout).Grow(1)
		})
		// 创建/完成/归档时间为系统自动维护的只读信息（任务时间概念完善需求）
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "创建时间").FontSize(12).Muted()
			ui.TextField(c, &m.editCreatedAt, "创建时间", "").ReadOnly(true).Grow(1)
			ui.Text(c, "完成时间").FontSize(12).Muted()
			completed := m.editCompletedAt
			if completed == "" {
				completed = "未完成"
			}
			ui.TextField(c, &completed, "完成时间", "").ReadOnly(true).Grow(1)
		})
		if m.editArchivedAt != "" {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "归档时间").FontSize(12).Muted()
				ui.TextField(c, &m.editArchivedAt, "归档时间", "").ReadOnly(true).Grow(1)
			})
		}
		if len(m.fields) > 0 {
			ui.Text(c, "把字段清空后保存即删除该值；没有改动的字段不会被覆盖").FontSize(12).Muted()
			ui.Flow(c).Gap(12).LineGap(8).Children(func() {
				for _, field := range m.fields {
					m.fieldEditor(c, field)
				}
			})
		}
	})
}

func (m *model) editorModalFooter(c ui.C) {
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if ui.Button(c, "保存").Small().Clicked() {
			m.saveEdit(m.editingID)
		}
		if ui.Button(c, "取消").Small().Ghost().Clicked() {
			m.endEdit()
		}
	})
}

// fieldEditor 行编辑器里单个自定义字段的输入控件（按类型分派）。
// 直接绑定 model.editValues 里的指针：地址在编辑期间不变，输入状态不会丢。
func (m *model) fieldEditor(c ui.C, field *store.Field) {
	value := m.editValues[field.Name]
	switch field.Type {
	case store.FieldTypeSingleSelect:
		choices := append([]string{""}, field.Choices()...)
		ui.Select(c, value, choices, selectChoiceLabel).Width(150).Label(field.Name)
	case store.FieldTypeDate:
		ui.TextField(c, value, field.Name, localTimeLayout).Width(150)
	case store.FieldTypeMultiSelect:
		ui.TextField(c, value, field.Name, "逗号分隔").Width(150)
	case store.FieldTypeNumber:
		ui.TextField(c, value, field.Name, "数字").Width(110)
	default:
		ui.TextField(c, value, field.Name, "").Width(150)
	}
}
