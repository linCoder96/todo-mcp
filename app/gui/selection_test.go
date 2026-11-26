// GUI 层行多选测试（2026-11-26）：序号列选择复选框、列头全选、工具栏批量
// 操作（完成/归档/删除/清除选择）、选择随视图变化被清理、序号渲染。
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"todo-mcp/app/store"
)

func TestRowSelectionAndBatchComplete(t *testing.T) {
	m, repo := newTestModel(t)
	for _, title := range []string{"多选任务甲", "多选任务乙", "多选任务丙"} {
		if _, err := repo.Create(store.TaskInput{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 无选择时工具栏不出现批量操作
	if shows(driver, "批量完成") {
		t.Fatalf("无选择时不应出现批量操作：%v", labelValues(driver))
	}
	// 勾选两行的序号列复选框
	clickCheckbox(t, driver, "选择:多选任务甲")
	clickCheckbox(t, driver, "选择:多选任务乙")
	if !shows(driver, "已选 2 项") {
		t.Fatalf("勾选两行应显示计数：%v", labelValues(driver))
	}
	click(t, driver, "批量完成")

	for _, title := range []string{"多选任务甲", "多选任务乙"} {
		task := m.taskOf(taskIDByTitle(t, repo, title))
		if task.Status != 1 {
			t.Fatalf("批量完成应勾掉所选任务 %s：%+v", title, task)
		}
	}
	if task := m.taskOf(taskIDByTitle(t, repo, "多选任务丙")); task.Status != 0 {
		t.Fatal("批量完成不应动未选中的行")
	}
	// 操作后选择被清掉，批量操作区随之消失
	if shows(driver, "已选") || m.chosen.Len() != 0 {
		t.Fatalf("批量完成后应清除选择：chosen=%d labels=%v", m.chosen.Len(), labelValues(driver))
	}
	assertLayoutClean(t, driver)
}

func TestSelectAllAndBatchDelete(t *testing.T) {
	m, repo := newTestModel(t)
	for _, title := range []string{"全选删除甲", "全选删除乙"} {
		if _, err := repo.Create(store.TaskInput{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 列头「全选」勾上 → 全部选中
	clickCheckbox(t, driver, "全选")
	if !shows(driver, "已选 2 项") {
		t.Fatalf("全选应选中所有行：%v", labelValues(driver))
	}
	// 批量删除两次确认：第一次只亮确认态
	click(t, driver, "批量删除")
	if !shows(driver, "确认批量删除？") {
		t.Fatalf("第一次点击批量删除应出现二次确认：%v", labelValues(driver))
	}
	if total, _ := repo.Count(store.TaskFilter{}); total != 2 {
		t.Fatal("第一次点击批量删除不应真正删除")
	}
	click(t, driver, "确认批量删除？")
	if total, _ := repo.Count(store.TaskFilter{}); total != 0 {
		t.Fatalf("确认后应删除全部：%d", total)
	}
}

func TestBatchArchiveSelected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "todo.db")
	m, repo := newTestModelAt(t, dbPath)
	// 一条已完成（可归档）+ 一条进行中（批量归档应跳过）
	done, err := repo.Create(store.TaskInput{Title: "批量归档已完成"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(done.ID); err != nil {
		t.Fatal(err)
	}
	active, err := repo.Create(store.TaskInput{Title: "批量归档进行中"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	clickCheckbox(t, driver, "选择:批量归档已完成")
	clickCheckbox(t, driver, "选择:批量归档进行中")
	click(t, driver, "批量归档")

	stored, err := repo.Get(done.ID)
	if err != nil || stored.ArchivedAt == nil {
		t.Fatalf("批量归档应归档已完成行：%+v err=%v", stored, err)
	}
	if stored, err = repo.Get(active.ID); err != nil || stored.ArchivedAt != nil {
		t.Fatalf("批量归档不应动进行中的行：%+v err=%v", stored, err)
	}
	// 与单行归档同口径：归档后重写知识文档
	data, err := os.ReadFile(archiveDocPathFor(dbPath))
	if err != nil {
		t.Fatalf("批量归档应生成知识文档: %v", err)
	}
	if !strings.Contains(string(data), "## 批量归档已完成") {
		t.Fatalf("知识文档应含批量归档的任务：\n%s", string(data))
	}
}

func TestSelectionPrunedOnTabSwitch(t *testing.T) {
	m, repo := newTestModel(t)
	done, err := repo.Create(store.TaskInput{Title: "已完成的行"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(done.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	clickCheckbox(t, driver, "选择:已完成的行")
	if m.chosen.Len() != 1 {
		t.Fatalf("勾选后应有选择：%d", m.chosen.Len())
	}
	// 切到「进行中」：已完成行不可见，选择被清理，批量操作区消失
	click(t, driver, "进行中")
	if m.chosen.Len() != 0 {
		t.Fatalf("切 tab 后应清理不可见行的选择：%d", m.chosen.Len())
	}
	if shows(driver, "已选") {
		t.Fatalf("清理后不应再显示批量操作：%v", labelValues(driver))
	}
}

func TestClearSelectionButton(t *testing.T) {
	m, repo := newTestModel(t)
	// 标题避开按钮文案的子串（shows 是子串匹配）
	if _, err := repo.Create(store.TaskInput{Title: "选择清理任务"}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	clickCheckbox(t, driver, "选择:选择清理任务")
	click(t, driver, "清除选择")
	if m.chosen.Len() != 0 {
		t.Fatalf("清除选择应清空选择集：%d", m.chosen.Len())
	}
	if shows(driver, "已选 1 项") {
		t.Fatalf("清除后批量操作区应消失：%v", labelValues(driver))
	}
}

func TestRowNumbersRenderInOrder(t *testing.T) {
	m, repo := newTestModel(t)
	for _, title := range []string{"序号甲", "序号乙", "序号丙"} {
		if _, err := repo.Create(store.TaskInput{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 序号列头 + 每行序号 1..3（当前视图顺序，无分组时 = 创建顺序）
	if !shows(driver, "序号") {
		t.Fatalf("应有序号列头：%v", labelValues(driver))
	}
	for _, num := range []string{"1", "2", "3"} {
		if !shows(driver, num) {
			t.Fatalf("缺少序号 %s：%v", num, labelValues(driver))
		}
	}
	// 序号在视图里的相对顺序：甲=1 乙=2 丙=3
	values := labelValues(driver)
	indexOf := func(text string) int {
		for i, value := range values {
			if value == text {
				return i
			}
		}
		return -1
	}
	if !(indexOf("1") < indexOf("2") && indexOf("2") < indexOf("3")) {
		t.Fatalf("序号应随行顺序渲染：%v", values)
	}
	assertLayoutClean(t, driver)
}

// taskIDByTitle 从 repo 反查任务 id（按标题唯一创建的测试数据）。
func taskIDByTitle(t *testing.T, repo *store.TaskRepo, title string) string {
	t.Helper()
	tasks, err := repo.List(store.TaskFilter{}, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Title == title {
			return task.ID
		}
	}
	t.Fatalf("找不到标题为 %q 的任务", title)
	return ""
}
