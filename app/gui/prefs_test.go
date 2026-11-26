// GUI 层视图偏好持久化测试（2026-11-26）：
//   - applyQueryChange 变更排序/分组后落库；
//   - 模拟重启（同库新 model）后偏好恢复，任务列表顺序/分组一致。
package gui

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"
)

// TestQueryChangePersistsPrefs：排序/分组/筛选变更写入 app_prefs。
func TestQueryChangePersistsPrefs(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.CreateField(store.FieldInput{Name: "评分", Type: store.FieldTypeNumber}); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshFields(); err != nil {
		t.Fatal(err)
	}

	m.sortField = "评分"
	m.sortDesc = true
	m.applyQueryChange()

	prefs, err := repo.LoadViewPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs == nil || prefs.SortField != "评分" || !prefs.SortDesc {
		t.Fatalf("排序偏好未落库: %+v", prefs)
	}

	m.groupBy = "评分"
	m.applyQueryChange()
	prefs, _ = repo.LoadViewPrefs()
	if prefs == nil || prefs.GroupBy != "评分" || prefs.SortField != "评分" {
		t.Fatalf("分组偏好未落库或排序被清: %+v", prefs)
	}

	m.filterConds = []store.FilterCond{{Field: "评分", Op: store.OpGt, Value: json.RawMessage("70")}}
	m.applyQueryChange()
	prefs, _ = repo.LoadViewPrefs()
	if prefs == nil || len(prefs.Conds) != 1 || prefs.Conds[0].Field != "评分" {
		t.Fatalf("筛选偏好未落库: %+v", prefs)
	}

	// 清除筛选（工具栏「清除筛选」路径）也要落库
	m.filterConds = nil
	m.applyQueryChange()
	prefs, _ = repo.LoadViewPrefs()
	if prefs == nil || len(prefs.Conds) != 0 {
		t.Fatalf("清除筛选未落库: %+v", prefs)
	}
}

// TestPrefsSurviveRestart：设置排序+分组+tab 后，用同一数据库新建 model
// （等价于重启程序），视图状态应恢复且列表按分组展平渲染。
func TestPrefsSurviveRestart(t *testing.T) {
	_, repo := newTestModel(t)
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

	// 第一会话：设置排序与分组并落库
	first := newModel(repo)
	first.sortField = "评分"
	first.sortDesc = true
	first.groupBy = "评分"
	first.tab = filterTabs[1] // 「进行中」
	first.applyQueryChange()

	// 重启：同库新建 model（newModel 内部 loadViewPrefs）
	second := newModel(repo)
	if second.sortField != "评分" || !second.sortDesc {
		t.Fatalf("重启后排序偏好丢失: %+v", second.sortField)
	}
	if second.groupBy != "评分" {
		t.Fatalf("重启后分组偏好丢失: %q", second.groupBy)
	}
	if second.tab != filterTabs[1] {
		t.Fatalf("重启后状态 tab 丢失: %q", second.tab)
	}
	// 分组展平生效：两条任务都在，首行是组头
	if len(second.tasks) != 2 || !second.groupHeaders[0] {
		t.Fatalf("重启后分组视图未生效: tasks=%d headers=%v", len(second.tasks), second.groupHeaders)
	}
	if second.tasks[0].Title != "甲" || second.tasks[1].Title != "乙" {
		t.Fatalf("重启后排序未生效: %s / %s", second.tasks[0].Title, second.tasks[1].Title)
	}
}

// TestPrefsSanitizedOnRestart：偏好引用的字段被删除后重启，悬挂引用被清理，
// 列表回到可用状态且不再携带失效条件。
func TestPrefsSanitizedOnRestart(t *testing.T) {
	repo1, repo := newTestModel(t)
	_ = repo1
	field, err := repo.CreateField(store.FieldInput{Name: "分类", Type: store.FieldTypeSingleSelect, Options: []string{"A", "B"}})
	if err != nil {
		t.Fatal(err)
	}
	first := newModel(repo)
	first.groupBy = "分类"
	first.applyQueryChange()

	if _, err := repo.RemoveField(field.ID); err != nil {
		t.Fatal(err)
	}
	// 重启：newModel 应恢复为无悬挂引用的视图（分组复位），refresh 不报错
	second := newModel(repo)
	if second.groupBy != "" {
		t.Fatalf("重启后未清理失效分组: %q", second.groupBy)
	}
	if second.errorText != "" {
		t.Fatalf("重启后不应带错误启动: %s", second.errorText)
	}
}
