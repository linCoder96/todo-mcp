// 视觉 token 与小的行内样式助手（frontend-design skill 两轮法之 token 系统）。
//
// 设计定位：桌面待办的「工作台」气质——中性灰底 + 主题蓝锚点，把「今天到期/逾期」
// 做成每行唯一的彩色时刻；其余位置全部交给主题 token（light/dark 自动正确）。
// 硬约束（design-viva.md §7.1/§7.2）：无 Transition/动画、无每帧分配、颜色取自 theme。
package gui

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// dueMeta 到期标签的 (颜色, 文案前缀)：逾期红色加「逾期」；今天到期主题蓝加「今天」；
// 其余静默灰色。颜色全部来自主题 token，dark 下自动正确。
func (m *model) dueMeta(overdue, dueToday bool) (gfx.Color, string) {
	theme := m.pageTheme
	switch {
	case overdue:
		return theme.Danger, "逾期 "
	case dueToday:
		return theme.Primary, "今天 "
	default:
		return theme.MutedForeground, ""
	}
}

// priorityBadge 渲染优先级徽标（胶囊，ui.Badge 主题变体）。
// priority==0（无）不渲染；低=灰（Secondary）、中=主题蓝（默认）、高=红（Destructive）。
func (m *model) priorityBadge(c ui.C, priority int) ui.Box {
	switch priority {
	case 3:
		return ui.Badge(c, priorityLabels[priority]).Destructive()
	case 2:
		return ui.Badge(c, priorityLabels[priority])
	default:
		return ui.Badge(c, priorityLabels[priority]).Secondary()
	}
}

// setRowMeta 缓存主题供行内声明使用（page 每次重绘入口调用）。
func (m *model) setRowMeta(c ui.C) {
	m.pageTheme = c.Theme()
	m.pageBounds = c.Bounds()
}
