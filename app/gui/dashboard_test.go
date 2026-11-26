package gui

// 仪表盘测试：统计维度的纯单元覆盖 + 左侧导航切换与图表的驱动验证。

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"
)

func TestCountByDimension(t *testing.T) {
	done := 1
	dueA, dueB := int64(1000), int64(9999999999999)
	tasks := []*store.Task{
		{Title: "A", Priority: 3, Status: 0, DueAt: &dueA},
		{Title: "B", Priority: 2, Status: 0, DueAt: &dueB},
		{Title: "C", Priority: 0, Status: done},
	}

	status := countByDimension(tasks, dimStatus, nil)
	if len(status) != 2 || status[0].count != 2 || status[1].count != 1 {
		t.Fatalf("状态统计错误：%v", status)
	}
	priority := countByDimension(tasks, dimPriority, nil)
	if priority[3].label != "高" || priority[3].count != 1 || priority[0].count != 1 {
		t.Fatalf("优先级统计错误：%v", priority)
	}
	due := countByDimension(tasks, dimDue, nil)
	if due[1].count != 1 || due[3].count != 1 || due[4].count != 1 {
		t.Fatalf("到期统计错误：%v", due)
	}
}

func TestCountByCustomField(t *testing.T) {
	field := &store.Field{ID: "cat", Name: "分类", Type: store.FieldTypeSingleSelect}
	tasks := []*store.Task{
		{Title: "A", Values: map[string]json.RawMessage{"cat": json.RawMessage(`"工作"`)}},
		{Title: "B", Values: map[string]json.RawMessage{"cat": json.RawMessage(`"工作"`)}},
		{Title: "C", Values: map[string]json.RawMessage{"cat": json.RawMessage(`"生活"`)}},
		{Title: "D"},
	}
	buckets := countByDimension(tasks, field.ID, []*store.Field{field})
	if len(buckets) != 2 || buckets[0].label != "工作" || buckets[0].count != 2 || buckets[1].count != 1 {
		t.Fatalf("字段统计错误：%v", buckets)
	}
}

func TestDashboardSectionSwitch(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.Create(store.TaskInput{Title: "统计任务A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(store.TaskInput{Title: "统计任务B", Priority: 3}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 初始在任务清单：左侧页签存在，任务列表正常。
	if m.section != navSections[0] || !shows(driver, "统计任务A") || !shows(driver, "新建") {
		t.Fatalf("初始应在任务清单：section=%q labels=%v", m.section, labelValues(driver))
	}

	click(t, driver, "仪表盘")
	if m.section != navSections[1] || !shows(driver, "统计维度") || !shows(driver, "共统计 2 项") {
		t.Fatalf("仪表盘应显示统计入口与总数：section=%q labels=%v", m.section, labelValues(driver))
	}
	if shows(driver, "新建") {
		t.Fatalf("仪表盘不应显示任务清单的快捷新建：labels=%v", labelValues(driver))
	}

	// 切回任务清单，原功能完整保留。
	click(t, driver, "任务清单")
	if m.section != navSections[0] || !shows(driver, "统计任务B") || !shows(driver, "新建") {
		t.Fatalf("切回任务清单应恢复原界面：section=%q labels=%v", m.section, labelValues(driver))
	}
	assertLayoutClean(t, driver)
}
