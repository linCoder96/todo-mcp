package gui

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
)

// replaceFocused 把当前聚焦文本框的内容整体替换成 text：光标移到末尾，退格清空，再输入。
// （Driver 的全选和弦仅终端可用，所以按字符数退格。）
func replaceFocused(t *testing.T, driver *app.Driver, runes int, text string) {
	t.Helper()
	if err := driver.Key(gfx.KeyEnd, 0); err != nil {
		t.Fatal(err)
	}
	for range runes {
		if err := driver.Key(gfx.KeyBackspace, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.Type(text); err != nil {
		t.Fatal(err)
	}
}

func press(t *testing.T, driver *app.Driver, key gfx.Key) {
	t.Helper()
	if err := driver.Key(key, 0); err != nil {
		t.Fatal(err)
	}
}

// fieldValue 读库里某字段的原始协议值；ok=false 表示该值已不存在（已清除）。
func fieldValue(t *testing.T, repo *store.TaskRepo, taskID, name string) (string, bool) {
	t.Helper()
	task, err := repo.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := task.Values[name]
	return string(raw), ok
}

// TestInCellEditSetEditClearCancel：点单元格原地编辑自定义字段值——
// 回车设置、回车修改、清空后回车删除该值、Esc 放弃不落库。
func TestInCellEditSetEditClearCancel(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.CreateField(store.FieldInput{Name: "备注", Type: store.FieldTypeText}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(store.TaskInput{Title: "单元格任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver, invalidator := openWithInvalidator(t, m)
	const cell = "编辑 备注：单元格任务"

	// 设置
	click(t, driver, cell)
	if !m.cellEditing(task.ID, "备注") {
		t.Fatal("点击单元格后应进入编辑态")
	}
	replaceFocused(t, driver, 0, "第一条备注")
	press(t, driver, gfx.KeyEnter)
	if got, _ := fieldValue(t, repo, task.ID, "备注"); got != `"第一条备注"` {
		t.Fatalf("回车后应落库，得到 %s（错误信息 %q）", got, m.errorText)
	}
	if m.cellTask != "" {
		t.Fatal("提交后应退出编辑态")
	}

	// 修改
	click(t, driver, cell)
	replaceFocused(t, driver, len([]rune("第一条备注")), "改过的")
	press(t, driver, gfx.KeyEnter)
	if got, _ := fieldValue(t, repo, task.ID, "备注"); got != `"改过的"` {
		t.Fatalf("修改后应为新值，得到 %s", got)
	}
	if !shows(driver, "改过的") {
		t.Fatalf("单元格应显示新值：%v", labelValues(driver))
	}

	// 清空后回车 = 删除该值
	click(t, driver, cell)
	replaceFocused(t, driver, len([]rune("改过的")), "")
	press(t, driver, gfx.KeyEnter)
	if got, ok := fieldValue(t, repo, task.ID, "备注"); ok {
		t.Fatalf("清空后提交应删除该值，库里仍有 %s", got)
	}

	// Esc 放弃：不落库
	click(t, driver, cell)
	replaceFocused(t, driver, 0, "不该保存")
	press(t, driver, gfx.KeyEscape)
	if m.cellTask != "" {
		t.Fatal("Esc 后应退出编辑态")
	}
	if got, ok := fieldValue(t, repo, task.ID, "备注"); ok {
		t.Fatalf("Esc 取消后不应落库，库里却有 %s", got)
	}

	// 不合法的值：不落库，停留在编辑态并说明原因
	if _, err := repo.CreateField(store.FieldInput{Name: "评分", Type: store.FieldTypeNumber}); err != nil {
		t.Fatal(err)
	}
	// 模型在事件之外变了，页面要请求重绘才会出现新列
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	invalidator.Invalidate()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "编辑 评分：单元格任务")
	replaceFocused(t, driver, 0, "abc")
	press(t, driver, gfx.KeyEnter)
	if _, ok := fieldValue(t, repo, task.ID, "评分"); ok {
		t.Fatal("非数字不应落库")
	}
	if !m.cellEditing(task.ID, "评分") {
		t.Fatal("值不合法时应停留在编辑态")
	}
	if m.errorText == "" {
		t.Fatal("值不合法时应显示原因")
	}
	assertLayoutClean(t, driver)
}

// TestInCellEditSingleSelect：单选字段单元格用下拉，选「未设置」即清除。
func TestInCellEditSingleSelect(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.CreateField(store.FieldInput{
		Name: "分类", Type: store.FieldTypeSingleSelect, Options: []string{"工作", "生活"},
	}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(store.TaskInput{Title: "单选任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "编辑 分类：单选任务")
	click(t, driver, "编辑 分类") // 展开下拉
	click(t, driver, "工作")
	if got, _ := fieldValue(t, repo, task.ID, "分类"); got != `"工作"` {
		t.Fatalf("选「工作」后应落库，得到 %s", got)
	}
	if m.cellTask != "" {
		t.Fatal("单选选中后应结束编辑态")
	}

	click(t, driver, "编辑 分类：单选任务")
	click(t, driver, "编辑 分类")
	click(t, driver, "未设置")
	if got, ok := fieldValue(t, repo, task.ID, "分类"); ok {
		t.Fatalf("选「未设置」应清除该值，库里仍有 %s", got)
	}
}

// TestRowEditorClearsFieldValue：行编辑器里把字段清空并保存 = 删除该值；
// 没改动的字段不会被覆盖。
func TestRowEditorClearsFieldValue(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.CreateField(store.FieldInput{Name: "标签", Type: store.FieldTypeText}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(store.TaskInput{
		Title:  "行编辑任务",
		Values: map[string]json.RawMessage{"标签": json.RawMessage(`"重要"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "编辑任务:行编辑任务")
	// 预填的原值应出现在文本框里
	if got := *m.editValues["标签"]; got != "重要" {
		t.Fatalf("编辑器应预填原值，得到 %q", got)
	}
	click(t, driver, "标签")
	replaceFocused(t, driver, len([]rune("重要")), "")
	click(t, driver, "保存")
	if got, ok := fieldValue(t, repo, task.ID, "标签"); ok {
		t.Fatalf("清空并保存后应删除该值，库里仍有 %s", got)
	}

	// 重新写入一个值；打开编辑器什么都不改直接保存，值应保留
	values := map[string]json.RawMessage{"标签": json.RawMessage(`"保留我"`)}
	if _, err := repo.Update(task.ID, store.TaskPatch{Values: new(new(values))}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "编辑任务:行编辑任务")
	click(t, driver, "保存")
	if got, _ := fieldValue(t, repo, task.ID, "标签"); got != `"保留我"` {
		t.Fatalf("未改动的字段不应被覆盖，得到 %s", got)
	}
}

// TestRemarkColumnCellEdit：固定「备注」列显示 description、整格点击原地编辑、
// 回车提交、清空提交 = 清空 description（列数据来自 tasks.description，非自定义值表）。
func TestRemarkColumnCellEdit(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "备注任务", Description: new("初始备注")})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	if !shows(driver, "初始备注") {
		t.Fatalf("备注列应展示 description 内容：%v", labelValues(driver))
	}
	click(t, driver, "编辑 备注:备注任务")
	replaceFocused(t, driver, len([]rune("初始备注")), "第二条备注")
	press(t, driver, gfx.KeyEnter)
	updated, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description == nil || *updated.Description != "第二条备注" {
		t.Fatalf("备注列回车应落库到 description，得到 %+v", updated.Description)
	}
	if !shows(driver, "第二条备注") {
		t.Fatalf("备注列应显示新值：%v", labelValues(driver))
	}

	// 清空提交 = 清空 description 列
	click(t, driver, "编辑 备注:备注任务")
	replaceFocused(t, driver, len([]rune("第二条备注")), "")
	press(t, driver, gfx.KeyEnter)
	updated, err = repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != nil {
		t.Fatalf("清空提交后 description 应为 nil，得到 %+v", updated.Description)
	}
}

// TestEditModalRemark：点标题打开编辑弹窗，弹窗含备注输入框（可编辑 description），
// 保存后落库；Esc 关闭弹窗后 editingID 清空、不再弹。
func TestEditModalRemark(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "弹窗任务", Description: new("弹窗前备注")})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "编辑任务:弹窗任务")
	if !m.editModalOpen || m.editingID != task.ID {
		t.Fatalf("点标题应打开编辑弹窗：open=%v id=%q", m.editModalOpen, m.editingID)
	}
	if got := m.editRemark; got != "弹窗前备注" {
		t.Fatalf("弹窗备注应预填原值，得到 %q", got)
	}
	click(t, driver, "备注")
	replaceFocused(t, driver, len([]rune("弹窗前备注")), "弹窗后备注")
	click(t, driver, "保存")
	updated, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description == nil || *updated.Description != "弹窗后备注" {
		t.Fatalf("弹窗保存应落库 description，得到 %+v", updated.Description)
	}
	if m.editModalOpen || m.editingID != "" {
		t.Fatal("保存后弹窗与编辑态都应关闭")
	}

	// Esc 关闭：不落库、editingID 清空
	click(t, driver, "编辑任务:弹窗任务")
	m.editRemark = "不该保存"
	press(t, driver, gfx.KeyEscape)
	if m.editModalOpen || m.editingID != "" {
		t.Fatalf("Esc 关闭后弹窗与编辑态应清空：open=%v id=%q", m.editModalOpen, m.editingID)
	}
	updated, err = repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description == nil || *updated.Description != "弹窗后备注" {
		t.Fatalf("Esc 关闭不应落库，得到 %+v", updated.Description)
	}
}
