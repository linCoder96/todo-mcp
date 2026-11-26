// 归档功能测试（2026-11-26）：Archive/Unarchive 生命周期、默认口径隐藏、
// Reopen 联动退出归档、知识文档生成与幂等覆盖。
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archiveAll 把给定任务全部完成并归档，返回归档后的任务对象。
func archiveDone(t *testing.T, repo *TaskRepo, id string) *Task {
	t.Helper()
	if _, err := repo.Complete(id); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Archive(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestArchiveLifecycleAndDefaultHiding(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	done, err := repo.Create(input("已完成任务"))
	if err != nil {
		t.Fatal(err)
	}
	running, err := repo.Create(input("进行中任务"))
	if err != nil {
		t.Fatal(err)
	}

	// 未完成任务不能归档
	if _, err := repo.Archive(running.ID); err == nil || !strings.Contains(err.Error(), "已完成") {
		t.Fatalf("未完成任务归档应报错，实际 %v", err)
	}

	archived := archiveDone(t, repo, done.ID)
	if archived.ArchivedAt == nil {
		t.Fatal("归档后应有 archived_at")
	}
	// 幂等：重复归档不重写时间戳（指针不同是两次查询各自分配，比毫秒值）
	again, err := repo.Archive(done.ID)
	if err != nil || again.ArchivedAt == nil || *again.ArchivedAt != *archived.ArchivedAt {
		t.Fatalf("归档应幂等: %+v err=%v", again, err)
	}

	// 默认口径（TaskFilter{}）隐藏归档任务
	tasks, err := repo.List(TaskFilter{}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == done.ID {
			t.Fatal("默认列表不应含归档任务")
		}
	}
	// 显式 true 只看归档
	onlyArchived, err := repo.List(TaskFilter{Archived: new(true)}, 100, 0)
	if err != nil || len(onlyArchived) != 1 || onlyArchived[0].ID != done.ID {
		t.Fatalf("归档筛选错误: %+v err=%v", onlyArchived, err)
	}
	// 显式 false 只看未归档
	onlyActive, err := repo.List(TaskFilter{Archived: new(false)}, 100, 0)
	if err != nil || len(onlyActive) != 1 || onlyActive[0].ID != running.ID {
		t.Fatalf("未归档筛选错误: %+v err=%v", onlyActive, err)
	}

	// Unarchive 回到已完成视图
	unarchived, err := repo.Unarchive(done.ID)
	if err != nil || unarchived.ArchivedAt != nil || unarchived.Status != 1 || unarchived.CompletedAt == nil {
		t.Fatalf("取消归档应保持已完成状态: %+v err=%v", unarchived, err)
	}
	// 幂等
	again, err = repo.Unarchive(done.ID)
	if err != nil || again.ArchivedAt != nil {
		t.Fatalf("取消归档应幂等: %+v err=%v", again, err)
	}
	tasks, err = repo.List(TaskFilter{}, 100, 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("取消归档后应回到默认列表: %+v err=%v", tasks, err)
	}
}

func TestReopenArchivedTaskClearsArchive(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	task, err := repo.Create(input("归档后撤销"))
	if err != nil {
		t.Fatal(err)
	}
	archiveDone(t, repo, task.ID)

	reopened, err := repo.Reopen(task.ID)
	if err != nil || reopened.Status != 0 || reopened.CompletedAt != nil || reopened.ArchivedAt != nil {
		t.Fatalf("Reopen 应同时退出归档: %+v err=%v", reopened, err)
	}
	tasks, err := repo.List(TaskFilter{}, 100, 0)
	if err != nil || len(tasks) != 1 || tasks[0].Status != 0 {
		t.Fatalf("撤销后应回到进行中: %+v err=%v", tasks, err)
	}
}

func TestQueryArchivedFilter(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)
	a, err := repo.Create(input("归档甲"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.Create(input("未归档乙"))
	if err != nil {
		t.Fatal(err)
	}
	archiveDone(t, repo, a.ID)

	// 增强查询默认口径同样隐藏归档任务
	result, err := repo.Query(ListQuery{Limit: 100})
	if err != nil || result.Total != 1 || result.Tasks[0].ID != b.ID {
		t.Fatalf("增强查询默认应隐藏归档: %+v err=%v", result, err)
	}
	// true 只看归档
	archivedTrue := true
	result, err = repo.Query(ListQuery{Limit: 100, Archived: &archivedTrue})
	if err != nil || result.Total != 1 || result.Tasks[0].ID != a.ID {
		t.Fatalf("增强查询归档筛选错误: %+v err=%v", result, err)
	}
}

func TestArchiveDocGeneratedAndIdempotent(t *testing.T) {
	db, path := newTestDB(t)
	repo := NewTaskRepo(db)

	// 空归档也生成稳定路径的空文档
	docPath, err := repo.GenerateArchiveDoc()
	if err != nil {
		t.Fatal(err)
	}
	if want := ArchiveDocPath(path); docPath != want {
		t.Fatalf("文档路径错误: %s，期望 %s", docPath, want)
	}
	data, err := os.ReadFile(docPath)
	if err != nil || !strings.Contains(string(data), "暂无已归档任务") {
		t.Fatalf("空文档缺失占位文案: %v", err)
	}

	a, err := repo.Create(input("带备注的归档任务"))
	if err != nil {
		t.Fatal(err)
	}
	remark := "把这条任务的关键结论写进文档"
	if _, err := repo.Update(a.ID, TaskPatch{Description: new(new(remark))}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Archive(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GenerateArchiveDoc(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# 归档任务知识文档", "## 带备注的归档任务", "备注：把这条任务的关键结论写进文档", "完成于", "归档于", "优先级：无"} {
		if !strings.Contains(text, want) {
			t.Fatalf("文档缺少 %q：\n%s", want, text)
		}
	}
	if strings.Contains(text, "未归档乙") {
		t.Fatal("未归档任务不应进入文档")
	}

	// 再归档一条后重写：旧内容替换为新全量（按归档时间降序，新归档在前）
	b, err := repo.Create(input("第二条归档"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Archive(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GenerateArchiveDoc(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	text = string(data)
	if !strings.Contains(text, "## 第二条归档") {
		t.Fatalf("重写后应含新归档任务：\n%s", text)
	}
	if strings.Count(text, "## ") != 2 {
		t.Fatalf("文档应恰好两条归档任务：\n%s", text)
	}

	// 取消归档后重写：只剩一条
	if _, err := repo.Unarchive(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GenerateArchiveDoc(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(docPath)
	if strings.Count(string(data), "## ") != 1 {
		t.Fatalf("取消归档后应只剩一条：\n%s", string(data))
	}
}

func TestArchiveDocSkipsBrokenFieldValue(t *testing.T) {
	db, path := newTestDB(t)
	repo := NewTaskRepo(db)
	field, err := repo.CreateField(FieldInput{Name: "笔记", Type: FieldTypeText})
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(input("带字段任务"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(task.ID); err != nil {
		t.Fatal(err)
	}
	// 空展示文本的字段值（text_value 为空串）不进文档：fieldValueText
	// 返回空串即跳过（文档价值优先于完整快照；校验路径允许空串文本）。
	if _, err := repo.db.Exec(
		`INSERT INTO task_field_values (task_id, field_id, text_value) VALUES (?, ?, '')`,
		task.ID, field.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Archive(task.ID); err != nil {
		t.Fatal(err)
	}
	docPath, err := repo.GenerateArchiveDoc()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(path), "docs", ArchiveDocFileName); docPath != want {
		t.Fatalf("路径错误: %s", docPath)
	}
	data, _ := os.ReadFile(docPath)
	if !strings.Contains(string(data), "## 带字段任务") {
		t.Fatalf("任务主体应在文档中：\n%s", string(data))
	}
	if strings.Contains(string(data), "笔记：") {
		t.Fatalf("空展示文本的字段值应被跳过：\n%s", string(data))
	}
}
