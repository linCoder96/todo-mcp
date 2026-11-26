package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "todo.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func input(title string) TaskInput {
	return TaskInput{Title: title}
}

func isBusy(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "busy") || strings.Contains(text, "locked")
}

func TestOpenAtSetsPragmasAndMigrates(t *testing.T) {
	db, path := newTestDB(t)

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q，期望 wal", journal)
	}
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout = %d，期望 5000", timeout)
	}
	var synchronous any
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	switch fmt.Sprint(synchronous) {
	case "1", "normal", "NORMAL":
	default:
		t.Fatalf("synchronous = %v，期望 NORMAL(1)", synchronous)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != latestVersion {
		t.Fatalf("user_version = %d，期望 %d", version, latestVersion)
	}
	for _, object := range []struct {
		kind string
		name string
	}{
		{"table", "tasks"},
		{"index", "idx_tasks_due_at"},
		{"index", "idx_tasks_status"},
		{"table", "fields"},
		{"table", "task_field_values"},
		{"index", "uq_fields_name"},
		{"index", "idx_fields_position"},
		{"index", "idx_tfv_field_text"},
		{"index", "idx_tfv_field_num"},
		{"table", "task_images"},
		{"index", "idx_task_images_task"},
		{"table", "task_files"},
		{"index", "idx_task_files_task"},
		{"table", "app_prefs"},
	} {
		var count int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?",
			object.kind, object.name,
		).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("%s %s 数量 = %d, err = %v", object.kind, object.name, count, err)
		}
	}

	// 重复打开：迁移门闸幂等
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAt(path)
	if err != nil {
		t.Fatalf("重复打开失败: %v", err)
	}
	defer reopened.Close()
	var versionAgain int
	if err := reopened.QueryRow("PRAGMA user_version").Scan(&versionAgain); err != nil || versionAgain != latestVersion {
		t.Fatalf("重复打开后 user_version = %d err=%v", versionAgain, err)
	}
}

func TestCreateGetUpdateRemoveCRUD(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	due := int64(1_795_568_400_000)
	created, err := repo.Create(TaskInput{
		Title:       "写周报",
		Description: new("每周五前"),
		Priority:    2,
		DueAt:       &due,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.ID) != 36 || created.Status != 0 || created.CompletedAt != nil {
		t.Fatalf("新建任务默认字段异常: %+v", created)
	}
	if created.Description == nil || *created.Description != "每周五前" || created.DueAt == nil || *created.DueAt != due {
		t.Fatalf("新建任务字段丢失: %+v", created)
	}
	if created.CreatedAt != created.UpdatedAt {
		t.Fatalf("初始 created_at 应等于 updated_at: %+v", created)
	}

	fetched, err := repo.Get(created.ID)
	if err != nil || !reflect.DeepEqual(fetched, created) {
		t.Fatalf("Get 往返不一致: %+v, err=%v", fetched, err)
	}

	snapshot, err := repo.Remove(created.ID)
	if err != nil || !reflect.DeepEqual(snapshot, created) {
		t.Fatalf("Remove 应返回删除前快照: %+v, err=%v", snapshot, err)
	}
	if _, err := repo.Get(created.ID); err == nil || !strings.Contains(err.Error(), "未找到任务") {
		t.Fatalf("删除后应报未找到，实际 %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	for _, title := range []string{"", "   "} {
		if _, err := repo.Create(input(title)); err == nil {
			t.Fatalf("空白标题 %q 应被拒绝", title)
		}
	}
	if _, err := repo.Create(input(strings.Repeat("长", 501))); err == nil {
		t.Fatal("501 字符标题应被拒绝")
	}
	bad := input("合法标题")
	bad.Priority = 4
	if _, err := repo.Create(bad); err == nil {
		t.Fatal("优先级 4 应被拒绝")
	}
	boundary := input(strings.Repeat("标", 500))
	boundary.Priority = 3
	if _, err := repo.Create(boundary); err != nil {
		t.Fatalf("500 字符 + 优先级 3 应允许: %v", err)
	}
}

func TestTaskPatchThreeStates(t *testing.T) {
	// **T 的 JSON 三态语义（design-viva.md §5.2 第 3 项）
	for _, tc := range []struct {
		payload string
		absent  bool
		null    bool
		value   bool
	}{
		{`{}`, true, false, false},
		{`{"title":null}`, false, true, false},
		{`{"title":"新标题"}`, false, false, true},
	} {
		var patch TaskPatch
		if err := json.Unmarshal([]byte(tc.payload), &patch); err != nil {
			t.Fatalf("%s 解码失败: %v", tc.payload, err)
		}
		if tc.absent && patch.Title != nil {
			t.Fatalf("%s：缺席应为 nil", tc.payload)
		}
		if tc.null && (patch.Title == nil || *patch.Title != nil) {
			t.Fatalf("%s：显式 null 应为非 nil 外层 + nil 内层", tc.payload)
		}
		if tc.value && (patch.Title == nil || *patch.Title == nil || **patch.Title != "新标题") {
			t.Fatalf("%s：显式值解码错误", tc.payload)
		}
	}

	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	description := "初始描述"
	due := int64(1_795_568_400_000)
	base, err := repo.Create(TaskInput{
		Title: "初始标题", Description: &description, Priority: 1, DueAt: &due,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 空补丁：全部不动，updated_at 也不变
	after, err := repo.Update(base.ID, TaskPatch{})
	if err != nil || !reflect.DeepEqual(after, base) {
		t.Fatalf("空补丁不应改动任务: %+v err=%v", after, err)
	}

	// 缺席 + 显式值：只改 title
	after, err = repo.Update(base.ID, TaskPatch{Title: new(new("新标题"))})
	if err != nil || after.Title != "新标题" || after.Priority != base.Priority || after.DueAt == nil || *after.DueAt != due {
		t.Fatalf("部分更新语义错误: %+v err=%v", after, err)
	}
	if after.Description == nil || *after.Description != description || after.CreatedAt != base.CreatedAt {
		t.Fatalf("未出现字段应保持原值: %+v", after)
	}

	// 显式 null 清空 description 与 due_at，title 缺席不动
	after, err = repo.Update(base.ID, TaskPatch{
		Description: new((*string)(nil)),
		DueAt:       new((*string)(nil)),
	})
	if err != nil || after.Description != nil || after.DueAt != nil || after.Title != "新标题" {
		t.Fatalf("显式清空语义错误: %+v err=%v", after, err)
	}

	// 重新赋值路径
	after, err = repo.Update(base.ID, TaskPatch{
		Description: new(new("二次描述")),
		DueAt:       new(new("2026-11-26T01:00:00Z")),
		Priority:    new(new(3)),
	})
	if err != nil || after.Description == nil || *after.Description != "二次描述" || after.DueAt == nil || after.Priority != 3 {
		t.Fatalf("显式赋值语义错误: %+v err=%v", after, err)
	}

	// 从库里重读确认不是只改内存
	reloaded, err := repo.Get(base.ID)
	if err != nil || !reflect.DeepEqual(reloaded, after) {
		t.Fatalf("更新未落库: %+v err=%v", reloaded, err)
	}
}

func TestCompleteAndReopenIdempotent(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	created, err := repo.Create(input("完成任务"))
	if err != nil {
		t.Fatal(err)
	}

	done, err := repo.Complete(created.ID)
	if err != nil || done.Status != 1 || done.CompletedAt == nil {
		t.Fatalf("Complete 失败: %+v err=%v", done, err)
	}
	again, err := repo.Complete(created.ID)
	if err != nil || !reflect.DeepEqual(again, done) {
		t.Fatalf("Complete 应幂等: %+v err=%v", again, err)
	}

	reopened, err := repo.Reopen(created.ID)
	if err != nil || reopened.Status != 0 || reopened.CompletedAt != nil {
		t.Fatalf("Reopen 应清空完成状态: %+v err=%v", reopened, err)
	}
	again, err = repo.Reopen(created.ID)
	if err != nil || !reflect.DeepEqual(again, reopened) {
		t.Fatalf("Reopen 应幂等: %+v err=%v", again, err)
	}

	unfinished, err := repo.Create(input("未完成任务"))
	if err != nil || unfinished.CompletedAt != nil || unfinished.Status != 0 {
		t.Fatalf("DDL 行级不变式异常: %+v err=%v", unfinished, err)
	}
}

func TestNotFoundErrors(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	ghost := "01900000-0000-7000-8000-000000000000"
	checks := map[string]func() error{
		"Get":      func() error { _, err := repo.Get(ghost); return err },
		"Update":   func() error { _, err := repo.Update(ghost, TaskPatch{}); return err },
		"Complete": func() error { _, err := repo.Complete(ghost); return err },
		"Reopen":   func() error { _, err := repo.Reopen(ghost); return err },
		"Remove":   func() error { _, err := repo.Remove(ghost); return err },
	}
	for name, call := range checks {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "未找到任务 "+ghost) {
			t.Fatalf("%s 应返回中文未找到错误，实际 %v", name, err)
		}
	}
}

func TestListOrderPaginationFiltersAndClamp(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	seeds := []struct {
		title    string
		priority int
		dueAt    *int64
		complete bool
	}{
		{"任务1", 1, nil, false},
		{"任务2", 0, new(int64(1000)), false},
		{"任务3", 3, new(int64(1000)), false},
		{"任务4", 3, new(int64(2000)), false},
		{"任务5", 3, nil, false},
		{"任务6", 2, new(int64(500)), true},
	}
	for _, seed := range seeds {
		created, err := repo.Create(TaskInput{
			Title: seed.title, Priority: seed.priority, DueAt: seed.dueAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if seed.complete {
			if _, err := repo.Complete(created.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	list, err := repo.List(TaskFilter{}, 50, 0)
	if err != nil || len(list) != 6 {
		t.Fatalf("List 数量 = %d, err = %v", len(list), err)
	}
	got := make([]string, 0, len(list))
	for _, task := range list {
		got = append(got, task.Title)
	}
	want := []string{"任务3", "任务2", "任务4", "任务5", "任务1", "任务6"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("固定排序错误: got %v want %v", got, want)
	}

	page1, err := repo.List(TaskFilter{}, 2, 0)
	if err != nil || len(page1) != 2 || page1[0].Title != "任务3" {
		t.Fatalf("分页第一页错误: %+v err=%v", page1, err)
	}
	page2, err := repo.List(TaskFilter{}, 2, 2)
	if err != nil || len(page2) != 2 || page2[0].Title != "任务4" {
		t.Fatalf("分页第二页错误: %+v err=%v", page2, err)
	}

	active, err := repo.List(TaskFilter{Status: new(0)}, 50, 0)
	if err != nil || len(active) != 5 {
		t.Fatalf("状态筛选错误: %d err=%v", len(active), err)
	}
	high, err := repo.List(TaskFilter{Status: new(0), Priority: new(3)}, 50, 0)
	if err != nil || len(high) != 3 || high[0].Title != "任务3" || high[2].Title != "任务5" {
		t.Fatalf("叠加筛选错误: %+v err=%v", high, err)
	}

	// limit 钳制：>200 → 200；<1 → 1
	for index := range 210 {
		if _, err := repo.Create(input(fmt.Sprintf("批量任务%03d", index))); err != nil {
			t.Fatal(err)
		}
	}
	capped, err := repo.List(TaskFilter{}, 9999, 0)
	if err != nil || len(capped) != 200 {
		t.Fatalf("limit 上限钳制错误: %d err=%v", len(capped), err)
	}
	floor, err := repo.List(TaskFilter{}, 0, 0)
	if err != nil || len(floor) != 1 {
		t.Fatalf("limit 下限钳制错误: %d err=%v", len(floor), err)
	}
	total, err := repo.Count(TaskFilter{})
	if err != nil || total != 216 {
		t.Fatalf("Count = %d err=%v", total, err)
	}
}

// completed_at 作为内置日期字段参与增强查询（任务时间概念完善需求）：
// after/before 筛选、排序、分组与空值（未完成）归入「未设置」组。
func TestQueryCompletedAtFilterSortGroup(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	doneA, err := repo.Create(input("已完成A"))
	if err != nil {
		t.Fatal(err)
	}
	doneB, err := repo.Create(input("已完成B"))
	if err != nil {
		t.Fatal(err)
	}
	openTask, err := repo.Create(input("进行中"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(doneA.ID); err != nil {
		t.Fatal(err)
	}
	// doneB 与 doneA 都完成；同毫秒内两次 Complete 时间戳可能相等，
	// 不做递增断言（依赖时钟精度），只要求落库非空。
	done, err := repo.Complete(doneB.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 重新取已完成A 的落库快照（doneA 变量持有的是未完成时的旧对象）
	snapA, err := repo.Get(doneA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.CompletedAt == nil || snapA.CompletedAt == nil {
		t.Fatalf("完成时间应落库: %v vs %v", done.CompletedAt, snapA.CompletedAt)
	}

	// after 筛选：完成时间晚于「完成时间 + 1 小时」的应为 0 条；
	// before 同阈值应命中全部已完成任务（未完成为 NULL 不参与比较）。
	futureText, _ := json.Marshal(msToRFC3339(*snapA.CompletedAt + 3600_000))
	future := json.RawMessage(futureText)
	after := []FilterCond{{Field: "completed_at", Op: OpAfter, Value: future}}
	result, err := repo.Query(ListQuery{Filters: after, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 0 {
		t.Fatalf("completed_at after 筛选错误: %+v", result.Tasks)
	}
	before := []FilterCond{{Field: "completed_at", Op: OpBefore, Value: future}}
	result, err = repo.Query(ListQuery{Filters: before, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("completed_at before 筛选错误: %+v", result.Tasks)
	}

	// not_empty 筛选：只含已完成任务
	notEmpty := []FilterCond{{Field: "completed_at", Op: OpNotEmpty}}
	result, err = repo.Query(ListQuery{Filters: notEmpty, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("completed_at not_empty 筛选错误: %+v", result.Tasks)
	}

	// 按 completed_at 升序：未完成（NULL）排后
	result, err = repo.Query(ListQuery{Sort: []SortKey{{Field: "completed_at"}}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 3 ||
		result.Tasks[0].ID != doneA.ID || result.Tasks[1].ID != doneB.ID || result.Tasks[2].ID != openTask.ID {
		t.Fatalf("completed_at 排序错误: %+v", result.Tasks)
	}

	// 按 completed_at 分组：同日完成归入同一日期组，未完成归入「未设置」组。
	result, err = repo.Query(ListQuery{GroupBy: "completed_at", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("completed_at 分组数 = %d, want 2", len(result.Groups))
	}
	var unset *GroupKey
	doneCount := 0
	for i := range result.Groups {
		if result.Groups[i].Label == "未设置" {
			unset = &result.Groups[i]
		} else {
			doneCount += len(result.Groups[i].Tasks)
		}
	}
	if unset == nil || len(unset.Tasks) != 1 || unset.Tasks[0].ID != openTask.ID {
		t.Fatalf("未完成任务应归入未设置组: %+v", result.Groups)
	}
	if doneCount != 2 {
		t.Fatalf("已完成分组任务数 = %d, want 2", doneCount)
	}
}
func TestRFC3339Conversion(t *testing.T) {
	const text = "2026-11-25T01:00:00Z"
	millis, err := rfc3339ToMs(text)
	if err != nil {
		t.Fatal(err)
	}
	if millis != 1_795_568_400_000 {
		t.Fatalf("epoch 毫秒 = %d", millis)
	}
	if got := msToRFC3339(millis); got != text {
		t.Fatalf("往返 = %q，期望 %q", got, text)
	}
	// 带时区偏移归一化为同一时刻
	offsetMillis, err := rfc3339ToMs("2026-11-25T09:00:00+08:00")
	if err != nil || offsetMillis != millis {
		t.Fatalf("时区偏移归一化错误: %d err=%v", offsetMillis, err)
	}
	// 毫秒精度往返
	withMS := int64(1_795_568_400_123)
	if got := msToRFC3339(withMS); got != "2026-11-25T01:00:00.123Z" {
		t.Fatalf("毫秒格式 = %q", got)
	}
	if _, err := rfc3339ToMs("2026-11-25"); err == nil {
		t.Fatal("非法时间应报错")
	}
}

func TestConcurrentWritersNoBusy(t *testing.T) {
	dbA, path := newTestDB(t)
	dbB, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbB.Close() })
	repoA, repoB := NewTaskRepo(dbA), NewTaskRepo(dbB)

	// 两连接 busy_timeout 一致，100 个写事务严格交错
	for _, db := range []*sql.DB{dbA, dbB} {
		var timeout int
		if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Fatalf("busy_timeout = %d err=%v", timeout, err)
		}
	}
	busy := 0
	for index := range 100 {
		repo := repoA
		if index%2 == 1 {
			repo = repoB
		}
		if _, err := repo.Create(input(fmt.Sprintf("交错任务%03d", index))); err != nil {
			if isBusy(err) {
				busy++
			} else {
				t.Fatalf("交错写出现其他错误: %v", err)
			}
		}
	}
	if busy != 0 {
		t.Fatalf("交错写出现 %d 次 SQLITE_BUSY", busy)
	}

	// 两 goroutine 各持一条独立连接同时写，真正触发写-写竞争
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for _, tag := range []string{"甲", "乙"} {
		wg.Add(1)
		go func(tag string) {
			defer wg.Done()
			db, err := OpenAt(path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			repo := NewTaskRepo(db)
			for index := range 25 {
				if _, err := repo.Create(input(fmt.Sprintf("并发-%s-%02d", tag, index))); err != nil {
					errs <- err
				}
			}
		}(tag)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发写失败: %v", err)
	}

	total, err := repoA.Count(TaskFilter{})
	if err != nil || total != 150 {
		t.Fatalf("并发写后总数 = %d err=%v，数据丢失", total, err)
	}
	// WAL 提交即可见：另一连接立刻能读到第一连接写入的数据
	all, err := repoB.List(TaskFilter{}, 200, 0)
	if err != nil || len(all) != 150 {
		t.Fatalf("另一连接可见性异常: %d err=%v", len(all), err)
	}
}
