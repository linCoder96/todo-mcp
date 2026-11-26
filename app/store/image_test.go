// 图片附件的存储层测试（2026-11-26 v3）。
package store

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// testPNG 生成一张 8x6 的 PNG 写到临时文件，返回路径。
func testPNG(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 40), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageAddListCountRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "带图任务"})
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()

	// 添加两张（一张按文件、一张按字节）
	first, err := repo.AddImageFile(task.ID, testPNG(t, src, "截图.png"))
	if err != nil {
		t.Fatal(err)
	}
	pngBytes, err := os.ReadFile(testPNG(t, src, "粘贴.png"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.AddImageBytes(task.ID, "粘贴.png", pngBytes)
	if err != nil {
		t.Fatal(err)
	}
	if first.Position != 0 || second.Position != 1 {
		t.Fatalf("position 应为 0,1，得到 %d,%d", first.Position, second.Position)
	}

	// 文件落在数据库目录 images/<task_id>/ 下
	dir := ImagesDirFor(path)
	stored, err := os.ReadDir(filepath.Join(dir, task.ID))
	if err != nil || len(stored) != 2 {
		t.Fatalf("图片目录应有 2 个文件：err=%v n=%d", err, len(stored))
	}

	// ListImages 顺序、ImageCount、Task 聚合
	images, err := repo.ListImages(task.ID)
	if err != nil || len(images) != 2 {
		t.Fatalf("ListImages = %d err=%v", len(images), err)
	}
	if images[0].ID != first.ID || images[1].ID != second.ID {
		t.Fatal("ListImages 未按 position 排序")
	}
	if count, _ := repo.ImageCount(task.ID); count != 2 {
		t.Fatalf("ImageCount = %d，期望 2", count)
	}
	loaded, err := repo.Get(task.ID)
	if err != nil || len(loaded.Images) != 2 {
		t.Fatalf("Get 应聚合图片：n=%d err=%v", len(loaded.Images), err)
	}
	if loaded.Images[1].FileName != "粘贴.png" {
		t.Fatalf("FileName 聚合错误: %+v", loaded.Images[1])
	}

	// RemoveImage：行 + 文件都删
	if err := repo.RemoveImage(task.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if count, _ := repo.ImageCount(task.ID); count != 1 {
		t.Fatalf("删除后 ImageCount = %d，期望 1", count)
	}
	if _, err := os.Stat(filepath.Join(dir, task.ID, first.StoredName)); !os.IsNotExist(err) {
		t.Fatal("删除后图片文件应不存在")
	}
}

func TestImageValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "校验任务"})
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()

	// 非图片格式拒绝
	notImage := filepath.Join(src, "note.txt")
	if err := os.WriteFile(notImage, []byte("这不是图片"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddImageFile(task.ID, notImage); err == nil {
		t.Fatal("文本文件应被拒绝")
	}

	// 超过单张上限拒绝（PNG 头 + 垫够 20 MiB+1）
	var oversize bytes.Buffer
	if err := png.Encode(&oversize, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	oversize.Write(make([]byte, MaxImageBytes+1))
	if _, err := repo.AddImageBytes(task.ID, "big.png", oversize.Bytes()); err == nil {
		t.Fatal("超限图片应被拒绝")
	}

	// 不存在的任务拒绝
	if _, err := repo.AddImageFile("no-such-task", testPNG(t, src, "x.png")); err == nil {
		t.Fatal("不存在的任务应被拒绝")
	}

	// 每任务数量上限：加满 20 张后拒绝
	for i := 0; i < MaxImagesPerTask; i++ {
		if _, err := repo.AddImageFile(task.ID, testPNG(t, src, "p.png")); err != nil {
			t.Fatalf("第 %d 张不应失败: %v", i+1, err)
		}
	}
	if _, err := repo.AddImageFile(task.ID, testPNG(t, src, "p.png")); err == nil {
		t.Fatalf("第 %d 张应被拒绝", MaxImagesPerTask+1)
	}
	// 删一张又能加
	if images, _ := repo.ListImages(task.ID); len(images) > 0 {
		if err := repo.RemoveImage(task.ID, images[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AddImageFile(task.ID, testPNG(t, src, "p.png")); err != nil {
			t.Fatal("删除一张后应能再次添加")
		}
	}
}

func TestRemoveTaskCascadesImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepo(db)
	task, err := repo.Create(TaskInput{Title: "将被删除"})
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := repo.AddImageFile(task.ID, testPNG(t, src, "p.png")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := repo.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	dir := ImagesDirFor(path)
	// 行已级联删除
	if count, _ := repo.ImageCount(task.ID); count != 0 {
		t.Fatalf("删除任务后图片行应清空，还有 %d", count)
	}
	// 文件与目录一并删除
	if entries, err := os.ReadDir(filepath.Join(dir, task.ID)); err == nil && len(entries) > 0 {
		t.Fatalf("删除任务后图片文件应清空，还有 %d 个", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, task.ID)); !os.IsNotExist(err) {
		t.Fatal("删除任务后任务图片目录应不存在")
	}
}
