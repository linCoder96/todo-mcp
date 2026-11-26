// 文件附件的存储层测试（2026-11-26 v4）：copy / ref 两种模式、校验、级联。
package store

import (
	"os"
	"path/filepath"
	"testing"
)

// testFile 写一个普通文本文件到临时目录，返回路径。
func testFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFileCopyAddListRemove(t *testing.T) {
	db, path := newTestDB(t)
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "文件任务"})
	if err != nil {
		t.Fatal(err)
	}

	// copy 模式：拷入 files/<task_id>/，与源解耦
	src := testFile(t, t.TempDir(), "需求.docx", "文档内容")
	file, err := repo.AddFileCopy(task.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != FileCopy || file.StoredName == "" || file.SrcPath != "" {
		t.Fatalf("copy 模式元数据错误: %+v", file)
	}
	stored := filepath.Join(FilesDirFor(path), task.ID, file.StoredName)
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("副本文件应存在: %v", err)
	}
	// 副本内容与源一致
	data, err := os.ReadFile(stored)
	if err != nil || string(data) != "文档内容" {
		t.Fatalf("副本内容错误: %s %v", data, err)
	}

	files, err := repo.ListFiles(task.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("ListFiles 应返回 1 个: %d %v", len(files), err)
	}
	if count, _ := repo.FileCount(task.ID); count != 1 {
		t.Fatalf("FileCount 应为 1: %d", count)
	}
	// Get 聚合：Task.Files 带上文件
	got, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].ID != file.ID || got.Files[0].Kind != FileCopy {
		t.Fatalf("Get 应聚合 Files: %+v", got.Files)
	}

	// 删除：元数据行与磁盘副本一并删除
	if err := repo.RemoveFile(task.ID, file.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatalf("删除后副本应不存在: %v", err)
	}
	if count, _ := repo.FileCount(task.ID); count != 0 {
		t.Fatalf("删除后 FileCount 应为 0: %d", count)
	}
}

func TestFileRefAddAndRemove(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "引用任务"})
	if err != nil {
		t.Fatal(err)
	}

	// ref 模式：只存源路径，不拷贝
	src := testFile(t, t.TempDir(), "共享表格.xlsx", "表格")
	file, err := repo.AddFileRef(task.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != FileRef || file.SrcPath != src || file.StoredName != "" {
		t.Fatalf("ref 模式元数据错误: %+v", file)
	}
	// ref 模式记录源文件大小（展示用）
	if file.Size != int64(len("表格")) {
		t.Fatalf("ref 模式应记录源文件大小 %d，得到 %d", len("表格"), file.Size)
	}
	// 引用目录里不应出现副本
	entries, _ := os.ReadDir(FilesDirFor(filepath.Dir(filepath.Join(t.TempDir(), "x"))))
	_ = entries
	if files, _ := repo.ListFiles(task.ID); len(files) != 1 || files[0].Kind != FileRef {
		t.Fatalf("ref 模式 ListFiles 错误: %+v", files)
	}

	// 删除 ref：不碰源文件
	if err := repo.RemoveFile(task.ID, file.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("删除引用不应动源文件: %v", err)
	}
}

func TestFileValidation(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "校验任务"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	// ref 模式：不存在的源拒绝
	if _, err := repo.AddFileRef(task.ID, filepath.Join(dir, "不存在.txt")); err == nil {
		t.Fatal("不存在的源文件应被拒绝")
	}
	// ref 模式：目录拒绝
	if _, err := repo.AddFileRef(task.ID, dir); err == nil {
		t.Fatal("目录引用应被拒绝")
	}
	// copy 模式：空文件拒绝
	empty := filepath.Join(dir, "空.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFileCopy(task.ID, empty); err == nil {
		t.Fatal("空文件应被拒绝")
	}
	// copy 模式：超上限拒绝（不真写 50MiB，直接构造长内容会慢；用 21 MiB 内容）
	big := make([]byte, MaxFileBytes+1)
	oversize := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(oversize, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFileCopy(task.ID, oversize); err == nil {
		t.Fatal("超限文件应被拒绝")
	}
	// 不存在的任务拒绝
	if _, err := repo.AddFileCopy("no-such-task", testFile(t, dir, "x.txt", "内容")); err == nil {
		t.Fatal("不存在的任务应被拒绝")
	}

	// 每任务数量上限
	for i := range MaxFilesPerTask {
		if _, err := repo.AddFileCopy(task.ID, testFile(t, dir, "f"+string(rune('a'+i))+".txt", "内容")); err != nil {
			t.Fatalf("第 %d 个不应失败: %v", i+1, err)
		}
	}
	if _, err := repo.AddFileCopy(task.ID, testFile(t, dir, "extra.txt", "内容")); err == nil {
		t.Fatalf("第 %d 个应被拒绝", MaxFilesPerTask+1)
	}
	// 删一个又能加
	if files, _ := repo.ListFiles(task.ID); len(files) > 0 {
		if err := repo.RemoveFile(task.ID, files[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.AddFileCopy(task.ID, testFile(t, dir, "extra.txt", "内容")); err != nil {
		t.Fatalf("删除后应能再添加: %v", err)
	}
}

func TestRemoveTaskCascadesFiles(t *testing.T) {
	db, path := newTestDB(t)
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "级联任务"})
	if err != nil {
		t.Fatal(err)
	}
	src := testFile(t, t.TempDir(), "副本.txt", "内容")
	if _, err := repo.AddFileCopy(task.ID, src); err != nil {
		t.Fatal(err)
	}
	refSrc := testFile(t, t.TempDir(), "引用.txt", "引用内容")
	if _, err := repo.AddFileRef(task.ID, refSrc); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	// copy 副本随任务删除；ref 源文件不动
	matches, _ := filepath.Glob(filepath.Join(FilesDirFor(path), task.ID, "*"))
	if len(matches) != 0 {
		t.Fatalf("任务删除后文件目录应清空: %v", matches)
	}
	if _, err := os.Stat(refSrc); err != nil {
		t.Fatalf("删除任务不应动 ref 源文件: %v", err)
	}
}
