package gui

// 仪表盘（2026-11-26，任务「客户端左侧添加页签…再加一个仪表版，支持自定义统计图表展示」）。
// 统计基于全部任务，独立于任务清单的状态筛选；维度可选内置维度或任一自定义字段。
// 图表用 viva 自带 ui/chart.Bar；数据在 refresh 时算好，绘制只读缓存。

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/components"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
	"github.com/vivaui/viva/ui/chart"
)

// 主导航页签（左侧竖排）：原全部功能归入「任务清单」，新增「仪表盘」。
var navSections = []string{"任务清单", "仪表盘"}

// 内置统计维度：状态 / 优先级 / 到期情况；其后追加自定义字段 id。
const (
	dimStatus   = "status"
	dimPriority = "priority"
	dimDue      = "due"
)

var baseDimensions = []string{dimStatus, dimPriority, dimDue}

// dashBucket 是一个分类的计数（labels/values 与图表柱一一对应）。
type dashBucket struct {
	label string
	count int
}

// dashDimensions 当前可选维度：内置三项 + 字段列表（刷新时缓存）。
func (m *model) dashDimensions() []string {
	return m.dashDims
}

// dashDimensionLabel 维度的展示名。
func (m *model) dashDimensionLabel(key string) string {
	switch key {
	case dimStatus:
		return "状态"
	case dimPriority:
		return "优先级"
	case dimDue:
		return "到期情况"
	}
	for _, field := range m.fields {
		if field.ID == key {
			return field.Name
		}
	}
	return key
}

// refreshDashboardMeta 在字段列表变化后调用：更新维度选项，维度失效时回退到状态。
func (m *model) refreshDashboardMeta() {
	dims := make([]string, 0, len(baseDimensions)+len(m.fields))
	dims = append(dims, baseDimensions...)
	for _, field := range m.fields {
		dims = append(dims, field.ID)
	}
	m.dashDims = dims
	valid := false
	for _, key := range dims {
		if key == m.dashDimension {
			valid = true
			break
		}
	}
	if !valid {
		m.dashDimension = dimStatus
	}
}

// refreshDashboard 统计全量任务并写入图表数据。查询失败只记错误，不影响任务清单。
func (m *model) refreshDashboard() {
	tasks, err := m.repo.List(store.TaskFilter{}, 200, 0)
	if err != nil {
		m.dashError = "统计加载失败：" + err.Error()
		return
	}
	m.dashError = ""
	buckets := countByDimension(tasks, m.dashDimension, m.fields)
	labels := make([]string, len(buckets))
	values := make([][]float32, 1)
	values[0] = make([]float32, len(buckets))
	total := 0
	for i, bucket := range buckets {
		labels[i] = bucket.label
		values[0][i] = float32(bucket.count)
		total += bucket.count
	}
	m.dashLabels = labels
	m.dashTotal = total
	m.dashChart.Chart.Series = []chart.Series{{Key: "count", Label: "任务数"}}
	m.dashChart.Chart.Values = values
	m.dashChart.Chart.Grid = components.ChartGridHorizontal
	m.dashChart.Chart.Label = "任务分布"
	m.dashChart.Chart.XAxis.Format = func(dst []byte, v float32) []byte {
		index := int(v)
		if index >= 0 && index < len(m.dashLabels) {
			return append(dst, m.dashLabels[index]...)
		}
		return strconv.AppendInt(dst, int64(index), 10)
	}
}

// countByDimension 按维度把任务计数。内置维度保留固定分类（含 0），
// 自定义字段按取值动态分桶（按数量降序、同数按名称，至多 8 类）。
func countByDimension(tasks []*store.Task, dimension string, fields []*store.Field) []dashBucket {
	switch dimension {
	case dimStatus:
		active, done := 0, 0
		for _, task := range tasks {
			if task.Status == 0 {
				active++
			} else {
				done++
			}
		}
		return []dashBucket{{"进行中", active}, {"已完成", done}}
	case dimPriority:
		counts := [4]int{}
		for _, task := range tasks {
			if task.Priority >= 0 && task.Priority < len(counts) {
				counts[task.Priority]++
			}
		}
		buckets := make([]dashBucket, 0, len(counts))
		for i, count := range counts {
			buckets = append(buckets, dashBucket{priorityLabels[i], count})
		}
		return buckets
	case dimDue:
		return countByDue(tasks)
	}
	for _, field := range fields {
		if field.ID == dimension {
			return countByField(tasks, field)
		}
	}
	return nil
}

// countByDue 到期分布（仅进行中任务参与；已完成单列一类，保证总数守恒）。
func countByDue(tasks []*store.Task) []dashBucket {
	now := time.Now()
	todayEnd := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).UnixMilli()
	ms := now.UnixMilli()
	counts := [5]int{} // 无期限 / 已逾期 / 今天到期 / 未来 / 已完成
	for _, task := range tasks {
		switch {
		case task.Status == 1:
			counts[4]++
		case task.DueAt == nil:
			counts[0]++
		case *task.DueAt < ms:
			counts[1]++
		case *task.DueAt < todayEnd:
			counts[2]++
		default:
			counts[3]++
		}
	}
	labels := [...]string{"无期限", "已逾期", "今天到期", "未来", "已完成"}
	buckets := make([]dashBucket, 0, len(labels))
	for i, count := range counts {
		buckets = append(buckets, dashBucket{labels[i], count})
	}
	return buckets
}

// countByField 自定义字段取值分布：多选字段每个选项各计一次，空值跳过。
func countByField(tasks []*store.Task, field *store.Field) []dashBucket {
	counts := map[string]int{}
	for _, task := range tasks {
		for _, value := range dashFieldValues(field, task.Values[field.ID]) {
			counts[value]++
		}
	}
	buckets := make([]dashBucket, 0, len(counts))
	for label, count := range counts {
		buckets = append(buckets, dashBucket{label, count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].count != buckets[j].count {
			return buckets[i].count > buckets[j].count
		}
		return buckets[i].label < buckets[j].label
	})
	const maxBuckets = 8
	if len(buckets) > maxBuckets {
		other := 0
		for _, bucket := range buckets[maxBuckets:] {
			other += bucket.count
		}
		buckets = append(buckets[:maxBuckets], dashBucket{"其它", other})
	}
	return buckets
}

// dashFieldValues 字段值 → 分类标签列表（多选拆开，其余沿用展示文本）。
func dashFieldValues(field *store.Field, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	if field.Type == store.FieldTypeMultiSelect {
		return decodeMultiValues(raw)
	}
	if text := fieldValueLabel(field, raw); text != "" {
		return []string{text}
	}
	return nil
}

// dashboardPage 仪表盘页：维度下拉 + 柱状图 + 总数摘要。
func (m *model) dashboardPage(c ui.C) {
	theme := c.Theme()
	ui.Column(c).Fill().Pad(16).Gap(12).Children(func() {
		ui.Row(c).Gap(12).Items(ui.Center).Children(func() {
			ui.Text(c, "仪表盘").FontSize(20).Weight(gfx.WeightSemibold)
			ui.Text(c, "统计全部任务").FontSize(theme.Metrics.FontSizeS).Muted()
		})
		ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
			ui.Text(c, "统计维度").FontSize(theme.Metrics.FontSizeS)
			if ui.Select(c, &m.dashDimension, m.dashDimensions(), m.dashDimLabel).Changed() {
				m.refreshDashboard()
			}
		})
		if m.dashError != "" {
			ui.Text(c, m.dashError).Color(theme.Danger).FontSize(theme.Metrics.FontSizeS)
		}
		ui.Text(c, fmt.Sprintf("共统计 %d 项", m.dashTotal)).FontSize(theme.Metrics.FontSizeS).Muted()
		if m.dashTotal == 0 {
			ui.Text(c, "暂无任务数据，去「任务清单」新建一条吧").Muted()
			return
		}
		if len(m.dashChart.Chart.Series) > 0 {
			m.dashChart.Chart.Series[0].Color = theme.Chart[0]
		}
		chart.Bar(c, "任务分布图", &m.dashChart).Grow(1).FillWidth()
	})
}

// taskListPage 原任务清单内容（page 迁移前的主区域），保留原有交互与弹窗入口。
func (m *model) taskListPage(c ui.C) {
	theme := c.Theme()
	ui.Column(c).Fill().Pad(16).Gap(8).Children(func() {
		ui.Row(c).Gap(12).Items(ui.Center).Children(func() {
			ui.Text(c, "任务清单").FontSize(20).Weight(gfx.WeightSemibold)
			if m.statusLine != "" {
				ui.Text(c, m.statusLine).FontSize(theme.Metrics.FontSizeS).Muted()
			}
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if ui.TextField(c, &m.quickAdd, "快捷新建", "输入任务标题，回车即新建").Grow(1).Submitted() {
				m.quickAddSubmit()
			}
			if ui.Button(c, "新建").Small().Clicked() {
				m.quickAddSubmit()
			}
		})
		if ui.Tabs(c, &m.tab, filterTabs, nil).FillWidth().Changed() {
			m.errorText = ""
			m.applyQueryChange()
			m.confirmID = ""
			m.endEdit()
		}
		m.fieldToolbar(c)
		if m.tab == filterTabs[3] {
			// 已归档 tab 的知识文档入口（2026-11-26 归档功能）：
			// 归档任务整理成 Markdown 文档，生成即打开。
			if ui.Button(c, "生成知识文档").Small().Clicked() {
				m.openArchiveDoc()
			}
		}
		if m.errorText != "" {
			ui.Text(c, m.errorText).Color(theme.Danger).FontSize(theme.Metrics.FontSizeS)
		}
		if len(m.tasks) == 0 {
			m.emptyState(c)
		} else {
			rowWidth := m.layoutRowWidth()
			m.list.RowWidth = rowWidth
			// 列头行（2026-11-26 表格线条）：灰底 + 底边 1px 横线，与行底横线、
			// 列间竖线构成网格；Clip 让横向滚动的内容不出头。
			ui.Row(c).Height(28).Clip().FillWidth().Background(theme.Muted).Draw(m.rowHline).Children(func() {
				ui.Row(c).Width(rowWidth).Height(28).ScrollWith(&m.list).PadX(gridPadX).Children(func() {
					cols := m.gridCols()
					for i, col := range cols {
						if col.key == colIndex {
							m.indexHeaderCell(c)
						} else {
							ui.Text(c, col.label).Width(m.colWidth(col.key)).FontSize(theme.Metrics.FontSizeS).Muted().Self(ui.Center)
						}
						if i < len(cols)-1 {
							m.splitterOf(col.key)
							ui.Splitter(c, m.colStates[col.key]).SplitterThickness(splitThick).Mins(48, 48).Draw(m.headVline)
						}
					}
				})
			})
			ui.List(c, &m.list, len(m.tasks), func(row ui.ListRow) {
				m.taskRow(c, row)
			}).Grow(1).FillWidth().Label("任务列表")
		}
		if m.countLine != "" {
			ui.Text(c, m.countLine).FontSize(theme.Metrics.FontSizeS).Muted()
		}
	})
}

// decodeMultiValues 解析多选字段值的 JSON 数组；非法值当作空。
func decodeMultiValues(raw []byte) []string {
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}
