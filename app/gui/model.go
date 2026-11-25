package gui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// 筛选 tab（design.md §5 第 6 项 / design-viva.md §7.1）。
var filterTabs = []string{"全部", "进行中", "已完成"}

// 优先级文案（0=无 1=低 2=中 3=高），与 MCP 出参一致。
var priorityLabels = [...]string{"无", "低", "中", "高"}

// 到期输入/展示格式：Viva 无 HTML datetime-local，按 §7.1 用 TextField 解析。
const localTimeLayout = "2006-01-02 15:04"

// taskView 把每次重查时算好的展示文本缓存下来，避免每次重绘重复格式化
// （官方 skill §2：声明组件与布局不得每次绘制分配）。
type taskView struct {
	*store.Task
	dueLabel      string
	dueToday      bool // 今天到期且未完成（行内唯一的强调色时刻）
	priorityLabel string
	overdue       bool
	groupHeader   bool   // true = 分组标题行（Task 为 nil）
	groupLabel    string // 组头文案
}

// model 只属于窗口线程；页面每次重绘只读它并就地处理点击。
type model struct {
	repo *store.TaskRepo

	tasks []taskView
	list  ui.ListState

	tab        string
	quickAdd   string
	errorText  string
	statusLine string // 头部状态摘要（refresh 时缓存，禁止每帧分配）
	countLine  string // 列表计数行（refresh 时缓存）

	confirmID  string // 删除原位二次确认（design.md §5 第 4 项）
	editingID  string // 行内编辑展开的任务 id（design.md §5 第 5 项）
	editTitle  string
	editDue    string
	editPrio   int
	editValues map[string]string // 行内编辑的自定义字段值缓冲（键=字段名，文本形态）

	// 自定义字段（2026-11-24）
	fields       []*store.Field
	filterConds  []store.FilterCond // 生效中的筛选条件（AND，≤10；2026-11-25 多条件）
	draftField   string             // 筛选面板的待添加条件缓冲
	draftOp      store.FilterOp
	draftValue   string
	sortField    string // 排序字段名；空 = 固定排序
	sortDesc     bool
	groupBy      string // 分组字段名；空 = 平铺
	groupHeaders []bool // 与 tasks 等长；true = 组头行

	// 表格视图（2026-11-25，参照多维表格）：工具栏按钮 + Popover 面板
	filterOpen  bool
	sortOpen    bool
	groupOpen   bool
	colsOpen    bool   // 列设置面板
	filterLabel string // 工具栏按钮文案（含计数，refresh 时缓存）
	sortLabel   string
	groupLabel  string

	// 列设置（2026-11-25 ③）：隐藏列 + 列宽（SplitterState.Pos 即列宽，按列键持有）
	hiddenCols map[string]bool
	colStates  map[string]*ui.SplitterState // 键=列键；缺失时用默认宽

	// 主题（2026-11-25 ④）：跟随系统明暗；PageConfig.Theme 指向它
	theme gfx.Theme

	// 字段管理弹窗状态
	fieldModalOpen bool
	fieldEditID    string // 空 = 新建
	fieldName      string
	fieldOptions   string // 逗号分隔
	fieldTypeIndex int
	confirmFieldID string // 字段列删除二次确认（与任务删除 confirmID 同构）

	// pageTheme 缓存当前页面主题指针（page 每次重绘入口经 setRowMeta 刷新），
	// 供行内声明函数取主题色，避免每行重读 c.Theme() 的包装成本。
	pageTheme *gfx.Theme
}

// newModel 建模型并立即做一次全量重查（GUI 与 MCP 共用同一 repo 语义）。
func newModel(repo *store.TaskRepo) *model {
	m := &model{
		repo:       repo,
		tab:        filterTabs[0],
		theme:      gfx.LightTheme(),
		hiddenCols: map[string]bool{},
		colStates:  map[string]*ui.SplitterState{},
	}
	m.list.Key = func(index int) int {
		view := m.tasks[index]
		if view.groupHeader {
			return stableRowKey("group:" + view.groupLabel)
		}
		return stableRowKey(view.ID)
	}
	m.list.Label = func(index int) string {
		view := m.tasks[index]
		return view.Title
	}
	m.list.Header = func(index int) bool {
		return index < len(m.groupHeaders) && m.groupHeaders[index]
	}
	if err := m.refresh(); err != nil {
		m.errorText = err.Error()
	}
	return m
}

// stableRowKey 把 UUIDv7 文本映射为 31 位稳定整数键，令列表行在插入/筛选时
// 保持身份（ui.List 的 Key 回调要求非负 int）。
func stableRowKey(id string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(id))
	return int(hasher.Sum32() & 0x7fffffff)
}

// filter 把当前 tab 映射为 store.List 的筛选条件；nil 字段表示不过滤。
func (m *model) filter() store.TaskFilter {
	switch m.tab {
	case filterTabs[1]:
		return store.TaskFilter{Status: new(0)}
	case filterTabs[2]:
		return store.TaskFilter{Status: new(1)}
	default:
		return store.TaskFilter{}
	}
}

// refresh 全量重查（WAL 下提交即可见）+ 字段列表；支持字段筛选/排序/分组。
// 分组模式下任务列表展平为「组头行 + 组内任务行」，groupHeaders 标记组头。
func (m *model) refresh() error {
	if err := m.refreshFields(); err != nil {
		return err
	}
	query := m.buildQuery()
	result, err := m.repo.Query(query)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	today := time.Now()
	lo := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	hi := lo.Add(24 * time.Hour)
	loMs, hiMs := lo.UnixMilli(), hi.UnixMilli()
	toView := func(task *store.Task) taskView {
		view := taskView{Task: task, priorityLabel: priorityLabels[task.Priority]}
		if task.DueAt != nil {
			view.dueLabel = time.UnixMilli(*task.DueAt).In(time.Local).Format(localTimeLayout)
			view.overdue = task.Status == 0 && *task.DueAt < now
			view.dueToday = task.Status == 0 && *task.DueAt >= loMs && *task.DueAt < hiMs
		}
		return view
	}

	views := make([]taskView, 0)
	headers := make([]bool, 0)
	if result.Groups != nil {
		for _, group := range result.Groups {
			for index, task := range group.Tasks {
				view := toView(task)
				if index == 0 {
					// 组头标记必须同时写入 view.groupHeader（taskRow 渲染组头文本）
					// 和 headers（m.list.Header 粘性组头机制），缺一不可。
					view.groupHeader = true
					view.groupLabel = group.Label
					headers = append(headers, true)
				} else {
					headers = append(headers, false)
				}
				views = append(views, view)
			}
		}
	} else {
		for _, task := range result.Tasks {
			views = append(views, toView(task))
			headers = append(headers, false)
		}
	}
	m.tasks = views
	m.groupHeaders = headers
	m.statusLine = m.buildStatusLine()
	m.countLine = fmt.Sprintf("共 %d 项", len(views))
	m.filterLabel = toolbarCountLabel("筛选", len(m.filterConds))
	m.sortLabel = toolbarLabel("排序", m.sortField != "")
	m.groupLabel = toolbarLabel("分组", m.groupBy != "")
	return nil
}

// toolbarLabel 工具栏按钮文案：激活时附计数（参照多维表格「筛选 1」样式）。
func toolbarLabel(base string, active bool) string {
	if active {
		return base + " 1"
	}
	return base
}

// toolbarCountLabel 同上，但计数为条件/条目数量（0 = 不附）。
func toolbarCountLabel(base string, count int) string {
	if count > 0 {
		return fmt.Sprintf("%s %d", base, count)
	}
	return base
}

// buildStatusLine 头部状态摘要（refresh 时一次算好）：进行中/已完成/今天到期/逾期。
// 仅在有信息量时拼入，用中文顿号分隔（不引入无意义的装饰性分隔符）。
func (m *model) buildStatusLine() string {
	active, done, dueToday, overdue := 0, 0, 0, 0
	for _, view := range m.tasks {
		if view.Task == nil {
			continue
		}
		if view.Status == 0 {
			active++
			if view.dueToday {
				dueToday++
			}
			if view.overdue {
				overdue++
			}
		} else {
			done++
		}
	}
	parts := []string{fmt.Sprintf("%d 项进行中", active), fmt.Sprintf("%d 项已完成", done)}
	if dueToday > 0 {
		parts = append(parts, fmt.Sprintf("%d 项今天到期", dueToday))
	}
	if overdue > 0 {
		parts = append(parts, fmt.Sprintf("%d 项已逾期", overdue))
	}
	if len(m.filterConds) > 0 {
		parts = append(parts, fmt.Sprintf("已按 %d 个条件筛选", len(m.filterConds)))
	}
	return strings.Join(parts, "，")
}

// apply 在页面声明处统一执行写操作与重查；错误显示为中文文本，不弹窗。
func (m *model) apply(operation func() error) {
	m.errorText = ""
	if err := operation(); err != nil {
		m.errorText = err.Error()
	}
	if err := m.refresh(); err != nil {
		m.errorText = err.Error()
	}
}

// quickAddSubmit 快捷新建：回车即创建，默认 priority=0、无到期（design.md §5 第 2 项）。
func (m *model) quickAddSubmit() {
	title := strings.TrimSpace(m.quickAdd)
	if title == "" {
		return
	}
	m.apply(func() error {
		_, err := m.repo.Create(store.TaskInput{Title: title})
		if err == nil {
			m.quickAdd = ""
		}
		return err
	})
}

// toggleComplete 勾选完成 / 取消勾选（取消走 store.Reopen，design-viva.md §7.1）。
func (m *model) toggleComplete(task *store.Task, checked bool) {
	m.apply(func() error {
		if checked {
			_, err := m.repo.Complete(task.ID)
			return err
		}
		_, err := m.repo.Reopen(task.ID)
		return err
	})
}

// deleteTask 删除：第一次点击置 confirmID，第二次才物理删除。
func (m *model) deleteTask(id string) {
	if m.confirmID != id {
		m.confirmID = id
		return
	}
	m.confirmID = ""
	m.apply(func() error {
		_, err := m.repo.Remove(id)
		return err
	})
}

// beginEdit 展开行内编辑并把当前值写入编辑缓冲区（含自定义字段值，2026-11-25 ①）。
func (m *model) beginEdit(task *store.Task) {
	m.editingID = task.ID
	m.editTitle = task.Title
	m.editPrio = task.Priority
	m.editDue = ""
	if task.DueAt != nil {
		m.editDue = time.UnixMilli(*task.DueAt).In(time.Local).Format(localTimeLayout)
	}
	m.editValues = make(map[string]string, len(m.fields))
	for _, field := range m.fields {
		if raw, ok := task.Values[field.Name]; ok {
			m.editValues[field.Name] = decodeCustomText(field, raw)
		} else {
			m.editValues[field.Name] = ""
		}
	}
	m.confirmID = ""
}

// endEdit 收起行内编辑。
func (m *model) endEdit() {
	m.editingID = ""
}

// saveEdit 保存标题/优先级/到期/自定义字段值。到期按本地时间解析后归一化为
// UTC epoch 毫秒，清空输入表示显式置空（TaskPatch 双层 Option 三态，
// design-viva.md §5.2）；自定义字段值非空才写入（空 = 不动，避免误清，
// 2026-11-25 ①）。
func (m *model) saveEdit(id string) {
	title := strings.TrimSpace(m.editTitle)
	if title == "" {
		m.errorText = "任务标题不能为空白"
		return
	}
	patch := store.TaskPatch{
		Title:    new(new(title)),
		Priority: new(new(m.editPrio)),
		DueAt:    new((*string)(nil)),
	}
	if text := strings.TrimSpace(m.editDue); text != "" {
		local, err := time.ParseInLocation(localTimeLayout, text, time.Local)
		if err != nil {
			m.errorText = fmt.Sprintf("到期时间格式应为 %s：%v", localTimeLayout, err)
			return
		}
		rfc3339 := local.UTC().Format(time.RFC3339)
		patch.DueAt = new(new(rfc3339))
	}
	// 自定义字段值：先全部校验再落库（任一解析失败则整单不动）
	values := make(map[string]json.RawMessage, len(m.fields))
	for _, field := range m.fields {
		text, ok := m.editValues[field.Name]
		if !ok || strings.TrimSpace(text) == "" {
			continue // 空 = 不动
		}
		raw, err := encodeCustomValue(field, text)
		if err != nil {
			m.errorText = fmt.Sprintf("字段 %s：%v", field.Name, err)
			return
		}
		values[field.Name] = raw
	}
	if len(values) > 0 {
		patch.Values = new(new(values))
	}
	m.editingID = ""
	m.apply(func() error {
		_, err := m.repo.Update(id, patch)
		return err
	})
}

// priorityChoices / priorityLabel 为 ui.Select 的静态参数（comparable 值 + 文案函数）。
var priorityChoices = []int{0, 1, 2, 3}

func priorityLabel(priority int) string {
	return priorityLabels[priority]
}
