// 视图偏好持久化（2026-11-26）的回归测试：
//   - store 层：app_prefs v5 迁移、Save/Load 往返、损坏 JSON 容错、
//     引用已删字段的清理（筛选剔除、排序/分组复位）。
//   - GUI 层：applyQueryChange 落库、newModel 重启恢复（重启不丢偏好）。
package store

import (
	"encoding/json"
	"testing"
)

// TestViewPrefsSaveLoadRoundTrip：保存后从同库重读，内容一致（重启恢复的语义基础）。
func TestViewPrefsSaveLoadRoundTrip(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	prefs := &ViewPrefs{
		StatusTab: "进行中",
		Conds: []FilterCond{
			{Field: "priority", Op: OpGt, Value: json.RawMessage("2")},
		},
		SortField: "due_at",
		SortDesc:  true,
		GroupBy:   "status",
	}
	if err := repo.SaveViewPrefs(prefs); err != nil {
		t.Fatalf("保存偏好失败: %v", err)
	}
	got, err := repo.LoadViewPrefs()
	if err != nil {
		t.Fatalf("读取偏好失败: %v", err)
	}
	if got == nil || got.StatusTab != "进行中" || got.SortField != "due_at" ||
		!got.SortDesc || got.GroupBy != "status" || len(got.Conds) != 1 ||
		got.Conds[0].Field != "priority" || got.Conds[0].Op != OpGt ||
		string(got.Conds[0].Value) != "2" {
		t.Fatalf("往返不一致: %+v", got)
	}

	// 覆盖写：第二次保存生效
	prefs2 := &ViewPrefs{StatusTab: "已完成"}
	if err := repo.SaveViewPrefs(prefs2); err != nil {
		t.Fatalf("覆盖保存失败: %v", err)
	}
	got2, _ := repo.LoadViewPrefs()
	if got2 == nil || got2.StatusTab != "已完成" || len(got2.Conds) != 0 || got2.SortField != "" {
		t.Fatalf("覆盖写未生效: %+v", got2)
	}
}

// TestViewPrefsNilWhenAbsent：无偏好行时 Load 返回 nil（首启按默认视图）。
func TestViewPrefsNilWhenAbsent(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	got, err := repo.LoadViewPrefs()
	if err != nil {
		t.Fatalf("空库读取应成功: %v", err)
	}
	if got != nil {
		t.Fatalf("无偏好应返回 nil: %+v", got)
	}
}

// TestViewPrefsCorruptJSON：库中 JSON 损坏时返回 nil，不阻塞启动。
func TestViewPrefsCorruptJSON(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	if _, err := db.Exec(`INSERT INTO app_prefs (id, prefs) VALUES (1, '{broken')`); err != nil {
		t.Fatalf("写入损坏 JSON 失败: %v", err)
	}
	got, err := repo.LoadViewPrefs()
	if err != nil || got != nil {
		t.Fatalf("损坏 JSON 应按无偏好处理（nil, nil），实际 %+v err=%v", got, err)
	}
}

// TestViewPrefsSanitizeDeletedField：引用已删字段的偏好被清理——
// 筛选条件剔除、排序/分组复位，重启后页面不再报「字段不存在」。
func TestViewPrefsSanitizeDeletedField(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	field, err := repo.CreateField(FieldInput{Name: "评分", Type: FieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	prefs := &ViewPrefs{
		Conds: []FilterCond{
			{Field: "priority", Op: OpEq, Value: json.RawMessage("1")}, // 内置，保留
			{Field: "评分", Op: OpGt, Value: json.RawMessage("70")},      // 自定义，保留
		},
		SortField: "评分",
		SortDesc:  true,
		GroupBy:   "评分",
	}
	if err := repo.SaveViewPrefs(prefs); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RemoveField(field.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.LoadViewPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Conds) != 1 || got.Conds[0].Field != "priority" {
		t.Fatalf("已删字段的筛选条件应被剔除: %+v", got)
	}
	if got.SortField != "" || got.SortDesc || got.GroupBy != "" {
		t.Fatalf("已删字段的排序/分组应复位: %+v", got)
	}
}

// TestViewPrefsMigrationV5：v4 库经 migrate 后 user_version=6（v5 偏好 + v6 归档列）
// 且 app_prefs 可用。
func TestViewPrefsMigrationV5(t *testing.T) {
	db, _ := newTestDB(t) // OpenAt 内部已跑 migrate，直接验证版本与读写
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != latestVersion {
		t.Fatalf("user_version 应为 %d，实际 %d err=%v", latestVersion, version, err)
	}
	repo := NewTaskRepo(db)
	if err := repo.SaveViewPrefs(&ViewPrefs{StatusTab: "全部"}); err != nil {
		t.Fatalf("v5 表写入失败: %v", err)
	}
	if got, _ := repo.LoadViewPrefs(); got == nil || got.StatusTab != "全部" {
		t.Fatalf("v5 表读回失败: %+v", got)
	}
}
