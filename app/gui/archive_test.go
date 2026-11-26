// GUI 层归档功能测试（2026-11-26）：已归档 tab 筛选、行内归档/取消归档按钮、
// 知识文档入口。数据流转经 driver 驱动真实页面验证。
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"todo-mcp/app/store"
)

func archiveDocPathFor(dbPath string) string {
	return store.ArchiveDocPath(dbPath)
}

func TestArchiveTabShowsArchivedOnly(t *testing.T) {
	m, repo := newTestModel(t)
	done, err := repo.Create(store.TaskInput{Title: "归档展示任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Archive(done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(store.TaskInput{Title: "未归档任务"}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 默认「全部」tab：归档任务隐藏
	if shows(driver, "归档展示任务") {
		t.Fatalf("全部 tab 不应显示归档任务：%v", labelValues(driver))
	}
	// 切到已归档 tab：只显示归档任务
	click(t, driver, "已归档")
	if !shows(driver, "归档展示任务") {
		t.Fatalf("已归档 tab 应显示归档任务：%v", labelValues(driver))
	}
	if shows(driver, "未归档任务") {
		t.Fatalf("已归档 tab 不应显示未归档任务：%v", labelValues(driver))
	}
	// 已归档行有「取消归档」入口
	if !shows(driver, "取消归档") {
		t.Fatalf("已归档行应有取消归档按钮：%v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}

func TestArchiveButtonArchivesAndWritesDoc(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "todo.db")
	m, repo := newTestModelAt(t, dbPath)
	task, err := repo.Create(store.TaskInput{Title: "待归档任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 已完成行有「归档」按钮
	if !shows(driver, "归档") {
		t.Fatalf("已完成行应有归档按钮：%v", labelValues(driver))
	}
	click(t, driver, "归档")

	// 归档落库：默认列表里该任务消失（点击后重查由 apply 完成）
	stored, err := repo.Get(task.ID)
	if err != nil || stored.ArchivedAt == nil {
		t.Fatalf("点击归档应落库: %+v err=%v", stored, err)
	}
	if shows(driver, "待归档任务") {
		t.Fatalf("归档后任务应从当前视图消失：%v", labelValues(driver))
	}

	// 知识文档已生成且含该任务
	data, err := os.ReadFile(archiveDocPathFor(dbPath))
	if err != nil {
		t.Fatalf("归档动作应生成知识文档: %v", err)
	}
	if !strings.Contains(string(data), "## 待归档任务") {
		t.Fatalf("文档应含归档任务标题：\n%s", string(data))
	}
}

func TestUnarchiveFromArchivedTab(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "待取消归档"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Archive(task.ID); err != nil {
		t.Fatal(err)
	}
	m.tab = filterTabs[3] // 已归档
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "取消归档")
	stored, err := repo.Get(task.ID)
	if err != nil || stored.ArchivedAt != nil || stored.Status != 1 {
		t.Fatalf("取消归档应落库且保持已完成: %+v err=%v", stored, err)
	}
	if shows(driver, "待取消归档") {
		t.Fatalf("取消归档后任务应从已归档视图消失：%v", labelValues(driver))
	}
}

func TestArchiveEmptyStateShowsDocButton(t *testing.T) {
	m, _ := newTestModel(t)
	m.tab = filterTabs[3]
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	if !shows(driver, "生成知识文档") {
		t.Fatalf("已归档空列表应有生成知识文档入口：%v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}
