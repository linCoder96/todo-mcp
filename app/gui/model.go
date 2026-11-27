package gui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
	"github.com/vivaui/viva/ui/chart"
)

// 筛选 tab（design.md §5 第 6 项 / design-viva.md §7.1）。
var filterTabs = []string{"全部", "进行中", "已完成", "已归档"}

// 优先级文案（0=无 1=低 2=中 3=高），与 MCP 出参一致。
var priorityLabels = [...]string{"无", "低", "中", "高"}

// 到期输入/展示格式：Viva 无 HTML datetime-local，按 §7.1 用 TextField 解析。
const localTimeLayout = "2006-01-02 15:04"

// taskView 把每次重查时算好的展示文本缓存下来，避免每次重绘重复格式化
// （官方 skill §2：声明组件与布局不得每次绘制分配）。
type taskView struct {
	*store.Task
	rowKey         int    // 行键（= m.list.Key 的返回值；分组首行用组键，选择按它持有）
	rowNo          int    // 序号：当前视图中的任务序（1 起，跨组连续），refresh 时缓存
	rowNoLabel     string // 序号文本（refresh 时缓存，避免每帧分配）
	dueLabel       string
	dueToday       bool   // 今天到期且未完成（行内唯一的强调色时刻）
	createdLabel   string // 创建时间展示文本（refresh 时缓存）
	completedLabel string // 完成时间展示文本（未完成为空）
	archivedLabel  string // 归档时间展示文本（未归档为空）
	priorityLabel  string
	imageCount     int    // 图片附件数（refresh 时缓存）
	imageBadge     string // 图片徽标文案（refresh 时缓存，避免每帧分配）
	fileCount      int    // 文件附件数（refresh 时缓存）
	fileBadge      string // 文件徽标文案（refresh 时缓存）
	overdue        bool
	groupHeader    bool   // true = 分组标题行（Task 为 nil）
	groupLabel     string // 组头文案
}

// model 只属于窗口线程；页面每次重绘只读它并就地处理点击。
type model struct {
	repo *store.TaskRepo

	tasks []taskView
	list  ui.ListState

	// 行多选（2026-11-26）：键 = m.list.Key 的行键（分组首行 = 组键）。ui.List
	// 原生手势（Ctrl 点选 / Shift 连选 / Ctrl+A 全选）与序号列复选框共同写入；
	// refresh 时把已不可见行的键清掉，选择始终只数当前视图里的行。
	chosen ui.Selection

	tab        string
	quickAdd   string
	errorText  string
	statusLine string // 头部状态摘要（refresh 时缓存，禁止每帧分配）
	countLine  string // 列表计数行（refresh 时缓存）

	confirmID       string // 删除原位二次确认（design.md §5 第 4 项）
	editingID       string // 编辑弹窗打开的任务 id（2026-11-26 由行内编辑改弹窗）
	editModalOpen   bool   // 编辑弹窗开关（Modal 关闭时写 false）
	editTitle       string
	editRemark      string // 备注（description 列）
	editOrigRemark  string // 打开弹窗时备注原值；保存时比较决定写/清空/不动
	editPrio        int
	editDue         string
	editCreatedAt   string // 创建时间（只读展示，编辑弹窗打开时填充）
	editCompletedAt string // 完成时间（只读展示；未完成为空）
	editArchivedAt  string // 归档时间（只读展示；未归档为空）
	// 行内编辑的自定义字段值缓冲（键=字段名，文本形态）。值用 *string：文本框直接
	// 绑定它，地址在 beginEdit 之间保持不变，输入状态（光标、焦点）才不会丢。
	editValues map[string]*string
	// editOrigins 打开编辑器时的原值；保存时与缓冲比较，决定「不动 / 设置 / 清空」。
	editOrigins map[string]string

	// 单元格直接编辑（2026-11-25）：同一时间至多一个单元格处于编辑态。
	cellTask  string // 编辑中的任务 id；空 = 无
	cellField string // 编辑中的字段名
	cellValue string // 编辑缓冲

	// 图片附件（2026-11-26）：任务详情抽屉 + 缩略图异步加载（见 images.go）
	detailOpen   bool
	detailKey    detailKey        // Effect 键：任务 + 代数
	imageGen     int              // 代数；抽屉打开与附件变化时 +1
	thumbs       []thumb          // 已加载的缩略图（Take 写入）
	drawerDropID gfx.ID           // 抽屉拖放区的身份（FileDrop 认领用）
	detailFiles  []store.TaskFile // 抽屉当前任务的文件列表（refresh 时同步）
	toaster      ui.Toaster       // 操作反馈（复制引用地址等轻提示）
	thumbLoad    *ui.EffectState[[]thumb, detailKey]

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
	menuOpen    bool   // 列设置面板字段「…」菜单打开状态（同一时间最多一个打开）
	menuFieldID string // 当前打开的「…」菜单所属字段ID
	// 列设置面板字段行拖拽：DragState 属于页面，行身份表每次重绘重建。
	fieldDrag       ui.DragState
	fieldDragRows   []fieldDragRow
	fieldDragAccept func(source, target gfx.ID) bool
	fieldDragDrop   func(source, target gfx.ID)
	filterLabel     string // 工具栏按钮文案（含计数，refresh 时缓存）
	sortLabel       string
	groupLabel      string

	// 列设置（2026-11-25 ③）：隐藏列 + 列宽（SplitterState.Pos 即列宽，按列键持有）
	hiddenCols map[string]bool
	colStates  map[string]*ui.SplitterState // 键=列键；缺失时用默认宽

	// 主题（2026-11-25 ④）：跟随系统明暗；PageConfig.Theme 指向它
	theme gfx.Theme

	// 字段管理弹窗状态
	fieldModalOpen bool
	fieldEditID    string // 空 = 新建
	fieldName      string
	fieldOptions   string          // 逗号分隔
	fieldTypeSel   fieldTypeChoice // 字段类型下拉的选中项
	confirmFieldID string          // 字段列删除二次确认（与任务删除 confirmID 同构）

	// pageTheme 缓存当前页面主题指针（page 每次重绘入口经 setRowMeta 刷新），
	// 供行内声明函数取主题色，避免每行重读 c.Theme() 的包装成本。
	pageTheme *gfx.Theme
	// pageBounds 页面布局视口（setRowMeta 刷新）：Drawer 等 overlay 的
	// 定位基准 f.bounds 会被横向滚动行撑宽（RowWidth > 窗口宽），
	// 需要真正的窗口内容区矩形时用它。
	pageBounds gfx.Rect

	// 左侧主导航与仪表盘（2026-11-26）
	section       string // 当前主页签：任务清单 / 仪表盘
	dashDimension string // 仪表盘统计维度
	dashDims      []string
	dashDimLabel  func(string) string
	dashLabels    []string
	dashTotal     int
	dashError     string
	dashChart     chart.BarChartView

	// 表格线条 painter（2026-11-26）：newModel 一次建好，声明处复用，
	// 避免每个单元格每帧新建 Draw 闭包（「分配节俭」规则）。颜色在绘制时
	// 取 d.Theme，light/dark 自动正确。
	rowHline  func(*ui.DrawContext) // 行底 1px 横线（单元格行与列头行共用）
	cellVline func(*ui.DrawContext) // 单元格右缘列间竖线（画在列距中线）
	headVline func(*ui.DrawContext) // 列头分隔条中线竖线（与 cellVline 位置对齐）

	// 选择工具栏（2026-11-26）：计数文案缓存（Sprintf 只在计数变化时发生）
	// 与批量删除二次确认。
	selCount        int    // 上次绘制时的已选计数
	selLine         string // 「已选 N 项」文案缓存
	confirmBatchDel bool   // 批量删除二次确认：第一次点击置 true，第二次才删
}

// newModel 建模型并立即做一次全量重查（GUI 与 MCP 共用同一 repo 语义）。
// 视图偏好（状态 tab / 筛选 / 排序 / 分组）从 app_prefs 恢复，重启不丢；
// 恢复失败按默认视图启动（错误进 errorText，不阻塞建模）。
func newModel(repo *store.TaskRepo) *model {
	m := &model{
		repo:          repo,
		tab:           filterTabs[0],
		section:       navSections[0],
		dashDimension: dimStatus,
		theme:         gfx.LightTheme(),
		hiddenCols: map[string]bool{
			// 创建/完成/归档时间默认隐藏：880px 默认视口放不下全部列（操作列
			// 必须可见）；需要时经列设置勾选显示，编辑弹窗内始终可看。
			colCreatedAt:   true,
			colArchivedAt:  true,
			colCompletedAt: true,
		},
		colStates: map[string]*ui.SplitterState{},
	}
	m.dashDimLabel = m.dashDimensionLabel
	m.fieldDragAccept = m.acceptFieldDrop
	m.fieldDragDrop = m.dropFieldDrag
	m.thumbLoad = newThumbLoad(repo)
	// 行多选：选择集由 list 原生手势与序号列复选框共同读写。
	m.list.Selection = &m.chosen
	// 表格线条 painter（颜色在绘制时取 d.Theme，light/dark 自动正确）。
	m.rowHline = func(d *ui.DrawContext) {
		d.Canvas.Fill(gfx.Rect{X: d.Rect.X, Y: d.Rect.Y + d.Rect.Height - 1,
			Width: d.Rect.Width, Height: 1}, d.Theme.Border)
	}
	m.cellVline = func(d *ui.DrawContext) {
		d.Canvas.Fill(gfx.Rect{X: d.Rect.X + d.Rect.Width - splitThick/2, Y: d.Rect.Y,
			Width: 1, Height: d.Rect.Height}, d.Theme.Border)
	}
	m.headVline = func(d *ui.DrawContext) {
		d.Canvas.Fill(gfx.Rect{X: d.Rect.X + d.Rect.Width/2, Y: d.Rect.Y,
			Width: 1, Height: d.Rect.Height}, d.Theme.Border)
	}
	m.list.Key = func(index int) int {
		return m.tasks[index].rowKey
	}
	m.list.Label = func(index int) string {
		view := m.tasks[index]
		return view.Title
	}
	m.list.Header = func(index int) bool {
		return index < len(m.groupHeaders) && m.groupHeaders[index]
	}
	if err := m.loadViewPrefs(); err != nil {
		m.errorText = err.Error()
	}
	if err := m.refresh(); err != nil {
		m.errorText = err.Error()
	}
	return m
}

// loadViewPrefs 从 store 恢复视图偏好到 model 内存态；无偏好（首启）不动默认值。
func (m *model) loadViewPrefs() error {
	prefs, err := m.repo.LoadViewPrefs()
	if err != nil {
		return err
	}
	if prefs == nil {
		return nil
	}
	// status tab 用文案校验（filterTabs 是界面唯一合法集合），防库值越界。
	for _, tab := range filterTabs {
		if prefs.StatusTab == tab {
			m.tab = prefs.StatusTab
			break
		}
	}
	m.filterConds = prefs.Conds
	m.sortField = prefs.SortField
	m.sortDesc = prefs.SortDesc && prefs.SortField != ""
	m.groupBy = prefs.GroupBy
	return nil
}

// saveViewPrefs 把当前视图状态写进 app_prefs；失败只提示不回滚（下次变更会重写）。
func (m *model) saveViewPrefs() {
	prefs := &store.ViewPrefs{
		StatusTab: m.tab,
		Conds:     m.filterConds,
		SortField: m.sortField,
		SortDesc:  m.sortDesc,
		GroupBy:   m.groupBy,
	}
	if err := m.repo.SaveViewPrefs(prefs); err != nil {
		m.errorText = err.Error()
	}
}

// stableRowKey 把 UUIDv7 文本映射为 31 位稳定整数键，令列表行在插入/筛选时
// 保持身份（ui.List 的 Key 回调要求非负 int）。
func stableRowKey(id string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(id))
	return int(hasher.Sum32() & 0x7fffffff)
}

// filter 把当前 tab 映射为 store.List 的筛选条件；nil 字段表示不过滤。
// 已归档 tab 只看 archived_at 非空的任务；其余三个 tab 只看未归档
// （store 对 Archived=nil 的默认口径就是未归档，这里显式传 false 语义等价，
// 保持代码自说明）。
func (m *model) filter() store.TaskFilter {
	if m.tab == filterTabs[3] {
		return store.TaskFilter{Archived: new(true)}
	}
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
		view.createdLabel = time.UnixMilli(task.CreatedAt).In(time.Local).Format(localTimeLayout)
		if task.CompletedAt != nil {
			view.completedLabel = time.UnixMilli(*task.CompletedAt).In(time.Local).Format(localTimeLayout)
		}
		if task.ArchivedAt != nil {
			view.archivedLabel = time.UnixMilli(*task.ArchivedAt).In(time.Local).Format(localTimeLayout)
		}
		if len(task.Images) > 0 {
			view.imageCount = len(task.Images)
			view.imageBadge = fmt.Sprintf("图 %d", len(task.Images))
		}
		if len(task.Files) > 0 {
			view.fileCount = len(task.Files)
			view.fileBadge = fmt.Sprintf("文 %d", len(task.Files))
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
	// 行键与序号（2026-11-26）：行键与 m.list.Key 一致（分组首行 = 组键），
	// 选择集按它持有；序号按当前视图顺序 1 起编号，分组视图跨组连续。
	rowNo := 0
	for i := range views {
		view := &views[i]
		if view.groupHeader {
			view.rowKey = stableRowKey("group:" + view.groupLabel)
		} else {
			view.rowKey = stableRowKey(view.ID)
		}
		rowNo++
		view.rowNo, view.rowNoLabel = rowNo, strconv.Itoa(rowNo)
	}
	m.pruneSelection(views)
	m.statusLine = m.buildStatusLine()
	m.countLine = fmt.Sprintf("共 %d 项", len(views))
	m.filterLabel = toolbarCountLabel("筛选", len(m.filterConds))
	m.sortLabel = toolbarLabel("排序", m.sortField != "")
	m.groupLabel = toolbarLabel("分组", m.groupBy != "")
	// 编辑弹窗/单元格编辑指向的任务被删（外部 MCP 并发）时收起编辑态
	if m.editingID != "" && m.taskOf(m.editingID) == nil {
		m.endEdit()
	}
	if m.cellTask != "" && m.taskOf(m.cellTask) == nil {
		m.cancelCellEdit()
	}
	m.refreshDashboardMeta()
	m.syncDetail()
	m.refreshDashboard()
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

// archiveTask 归档 / 取消归档（2026-11-26 归档功能）：成功后重写归档知识
// 文档（GenerateArchiveDoc 全量覆盖）；错误沿用 apply 的错误条展示。
func (m *model) archiveTask(id string, archive bool) {
	m.apply(func() error {
		var err error
		if archive {
			_, err = m.repo.Archive(id)
		} else {
			_, err = m.repo.Unarchive(id)
		}
		if err != nil {
			return err
		}
		_, err = m.repo.GenerateArchiveDoc()
		return err
	})
}

// openArchiveDoc 生成并打开归档知识文档（系统默认 Markdown 查看器）。
func (m *model) openArchiveDoc() {
	path, err := m.repo.GenerateArchiveDoc()
	if err != nil {
		m.errorText = err.Error()
		return
	}
	if err := app.OpenPath(path); err != nil {
		m.errorText = fmt.Sprintf("打开知识文档失败：%v", err)
	}
}

// beginEdit 打开任务编辑弹窗并把当前值写入编辑缓冲区（含备注与自定义字段值）。
// 弹窗与单元格编辑互斥：两处缓冲各自独立，同时打开会让旧缓冲覆盖新值。
func (m *model) beginEdit(task *store.Task) {
	m.cancelCellEdit()
	m.editingID = task.ID
	m.editModalOpen = true
	m.editTitle = task.Title
	m.editRemark = ""
	if task.Description != nil {
		m.editRemark = *task.Description
	}
	m.editOrigRemark = m.editRemark
	m.editPrio = task.Priority
	m.editDue = ""
	if task.DueAt != nil {
		m.editDue = time.UnixMilli(*task.DueAt).In(time.Local).Format(localTimeLayout)
	}
	m.editCreatedAt = time.UnixMilli(task.CreatedAt).In(time.Local).Format(localTimeLayout)
	m.editCompletedAt = ""
	if task.CompletedAt != nil {
		m.editCompletedAt = time.UnixMilli(*task.CompletedAt).In(time.Local).Format(localTimeLayout)
	}
	m.editArchivedAt = ""
	if task.ArchivedAt != nil {
		m.editArchivedAt = time.UnixMilli(*task.ArchivedAt).In(time.Local).Format(localTimeLayout)
	}
	m.editValues = make(map[string]*string, len(m.fields))
	m.editOrigins = make(map[string]string, len(m.fields))
	for _, field := range m.fields {
		text := ""
		if raw, ok := task.Values[field.Name]; ok {
			text = decodeCustomText(field, raw)
		}
		m.editValues[field.Name] = new(text)
		m.editOrigins[field.Name] = text
	}
	m.confirmID = ""
}

// endEdit 关闭编辑弹窗（保存成功或取消；Modal 的 Esc/遮罩关闭仅写 editModalOpen）。
func (m *model) endEdit() {
	m.editingID = ""
	m.editModalOpen = false
}

// saveEdit 保存标题/优先级/到期/自定义字段值。到期按本地时间解析后归一化为
// UTC epoch 毫秒，清空输入表示显式置空（TaskPatch 双层 Option 三态，
// design-viva.md §5.2）；自定义字段值与打开编辑器时的原值比较：没改的不写，
// 改成空即清除该值。
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
	remark := strings.TrimSpace(m.editRemark)
	if remark != strings.TrimSpace(m.editOrigRemark) {
		if remark == "" {
			patch.Description = new((*string)(nil)) // 显式清空
		} else {
			patch.Description = new(new(remark))
		}
	}
	// 自定义字段值：与打开编辑器时的原值比较，没改的不写；改成空 = 清空
	// （协议里该键显式 null）。先全部校验再落库，任一失败则整单不动。
	values := make(map[string]json.RawMessage, len(m.fields))
	for _, field := range m.fields {
		text := strings.TrimSpace(*m.editValues[field.Name])
		if text == strings.TrimSpace(m.editOrigins[field.Name]) {
			continue // 未改动
		}
		if text == "" {
			values[field.Name] = json.RawMessage(`null`) // 清空
			continue
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
	m.endEdit()
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
