// GUI 层自定义字段流程测试（driver 驱动真实页面，无窗口）。
package gui

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
)

// waitRefresh 在点击后让 driver 处理一帧（Viva 点击即同步重绘，通常无需额外帧）。
func waitRefresh(t *testing.T, driver *app.Driver) {
	t.Helper()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
}

// TestFieldModalCreateAndValueDisplay：新建字段 + 任务值行内显示。
func TestFieldModalCreateAndValueDisplay(t *testing.T) {
	m, repo := newTestModel(t)
	// 先建一个带「分类」字段值的任务
	field, err := repo.CreateField(store.FieldInput{
		Name: "分类", Type: store.FieldTypeSingleSelect, Options: []string{"A", "B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(store.TaskInput{
		Title:  "带字段任务",
		Values: map[string]json.RawMessage{field.Name: json.RawMessage(`"A"`)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 行内应显示字段值（表格视图：列头「分类」+ 单元格「A」）
	if !shows(driver, "分类") || !shows(driver, "A") {
		t.Fatalf("字段值未显示: %v", labelValues(driver))
	}

	// 打开字段弹窗，应显示类型网格
	click(t, driver, "字段")
	if !shows(driver, "选择字段类型") {
		t.Fatalf("字段弹窗未打开: %v", labelValues(driver))
	}

	// 输入字段名、选「文本」类型（默认即文本）、保存
	typeInto(t, driver, "字段名称", "备注")
	click(t, driver, "创建字段")
	waitRefresh(t, driver)

	// 折叠的 Select 只绘当前项「无」，选项名不在已绘制标签中；
	// 直接验证 model 状态（newField 内 refreshFields 已同步）。
	if len(m.fields) != 2 || m.fields[1].Name != "备注" {
		t.Fatalf("新字段未生效: %+v", m.fields)
	}
	assertLayoutClean(t, driver)
}

// TestFieldFilterSortGroup：通过 model 状态设置筛选/排序/分组后验证数据。
func TestFieldFilterSortGroup(t *testing.T) {
	m, repo := newTestModel(t)

	field, err := repo.CreateField(store.FieldInput{Name: "评分", Type: store.FieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		title string
		score string
	}{{"甲", "90"}, {"乙", "60"}} {
		if _, err := repo.Create(store.TaskInput{
			Title:  seed.title,
			Values: map[string]json.RawMessage{field.Name: json.RawMessage(seed.score)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}

	// 筛选：评分 > 70（2026-11-25 多条件：单条件即长度 1 的 filterConds）
	m.filterConds = []store.FilterCond{{Field: "评分", Op: store.OpGt, Value: json.RawMessage("70")}}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 1 || m.tasks[0].Title != "甲" {
		t.Fatalf("字段筛选错误: %+v", m.tasks)
	}

	// 排序：评分降序
	m.filterConds = nil
	m.sortField = "评分"
	m.sortDesc = true
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 2 || m.tasks[0].Title != "甲" || m.tasks[1].Title != "乙" {
		t.Fatalf("字段排序错误: %v %v", m.tasks[0].Title, m.tasks[1].Title)
	}

	// 分组：按评分分组，首行标记组头
	m.sortField = ""
	m.groupBy = "评分"
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 2 || !m.groupHeaders[0] || !m.groupHeaders[1] {
		t.Fatalf("分组标记错误: headers=%v", m.groupHeaders)
	}
	driver := open(t, m)
	if !shows(driver, "90") || !shows(driver, "60") {
		t.Fatalf("组头未显示: %v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}

// TestTaskValuesSurviveUpdate：行内编辑基础字段不应丢失自定义字段值。
func TestTaskValuesSurviveUpdate(t *testing.T) {
	m, repo := newTestModel(t)
	field, err := repo.CreateField(store.FieldInput{Name: "备注", Type: store.FieldTypeText})
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(store.TaskInput{
		Title:  "任务",
		Values: map[string]json.RawMessage{field.Name: json.RawMessage(`"重要"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	m.beginEdit(m.tasks[0].Task)
	m.editTitle = "改标题"
	m.saveEdit(task.ID)

	reloaded, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Title != "改标题" || string(reloaded.Values["备注"]) != `"重要"` {
		t.Fatalf("更新后自定义值丢失: %+v", reloaded)
	}
}

// TestInlineEditCustomFieldValue：行内编辑自定义字段值并落库（2026-11-25 ①）。
func TestInlineEditCustomFieldValue(t *testing.T) {
	m, repo := newTestModel(t)
	number, err := repo.CreateField(store.FieldInput{Name: "评分", Type: store.FieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	single, err := repo.CreateField(store.FieldInput{
		Name: "分类", Type: store.FieldTypeSingleSelect, Options: []string{"工作", "生活"},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(store.TaskInput{Title: "带字段任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}

	// 展开编辑：预填缓冲应包含全部字段（未填值为空串）
	m.beginEdit(m.tasks[0].Task)
	if _, ok := m.editValues["评分"]; !ok {
		t.Fatal("beginEdit 未初始化字段值缓冲")
	}
	// 填数字与单选，保存
	m.editValues["评分"] = "92.5"
	m.editValues["分类"] = "工作"
	m.saveEdit(task.ID)
	if m.errorText != "" {
		t.Fatalf("保存报错: %s", m.errorText)
	}

	reloaded, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.Values["评分"]) != "92.5" {
		t.Fatalf("数字值未落库: %s", reloaded.Values["评分"])
	}
	if string(reloaded.Values["分类"]) != `"工作"` {
		t.Fatalf("单选值未落库: %s", reloaded.Values["分类"])
	}
	_ = number
	_ = single

	// 非法数字应整体拒绝且不清空已有值
	m.beginEdit(m.tasks[0].Task)
	m.editValues["评分"] = "abc"
	m.saveEdit(task.ID)
	if m.errorText == "" {
		t.Fatal("非法数字应报错")
	}
	reloaded, err = repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.Values["评分"]) != "92.5" {
		t.Fatalf("校验失败时不应改动已有值: %s", reloaded.Values["评分"])
	}
}

// TestFieldColumnDelete：列设置面板删除自定义字段列（二次确认）→ 字段及其值级联删除。
func TestFieldColumnDelete(t *testing.T) {
	m, repo := newTestModel(t)
	field, err := repo.CreateField(store.FieldInput{
		Name: "分类", Type: store.FieldTypeSingleSelect, Options: []string{"A", "B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(store.TaskInput{
		Title:  "带值任务",
		Values: map[string]json.RawMessage{field.Name: json.RawMessage(`"A"`)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if len(m.fields) != 1 {
		t.Fatalf("应只有 1 个自定义字段: %+v", m.fields)
	}
	driver := open(t, m)

	// 打开列设置面板：≥1 个字段时「字段列序」区应显示（删除按钮在此）
	click(t, driver, "列设置")
	if !shows(driver, "字段列序") {
		t.Fatalf("字段列序区未显示（≥1 字段应始终显示）: %v", labelValues(driver))
	}

	// 第一次点击删除：仅进入确认态，不删除
	click(t, driver, "删除 分类")
	if len(m.fields) != 1 {
		t.Fatalf("第一次点击不应删除字段: %+v", m.fields)
	}
	if !shows(driver, "确认删除？") {
		t.Fatalf("第一次点击应出现二次确认: %v", labelValues(driver))
	}

	// 第二次点击：真正删除，级联删值
	click(t, driver, "确认删除？")
	waitRefresh(t, driver)
	if len(m.fields) != 0 {
		t.Fatalf("第二次点击应删除字段: %+v", m.fields)
	}
	// 任务应仍在，但其字段值被级联清空
	if len(m.tasks) != 1 {
		t.Fatalf("删除字段不应删除任务: %v", m.tasks)
	}
	reloaded, err := repo.Get(m.tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Values["分类"]; ok {
		t.Fatalf("字段删除后其值应级联清空: %v", reloaded.Values)
	}
	assertLayoutClean(t, driver)
}
