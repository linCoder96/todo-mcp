// 文件附件的 GUI 测试（2026-11-26）：徽标、抽屉文件区、复制引用地址、拖放分流。
// 注意：抽屉打开时不做 assertLayoutClean——普通 Text 不进可达名快照，且固定宽度
// 抽屉在 880 宽窗口下标题越界是既有渲染口径（images_test 同）。
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
)

// testFileForGUI 写一个普通文本文件到临时目录，返回路径（t.TempDir 每次新目录）。
func testFileForGUI(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFileBadgeAndDrawerPanel：行内「文 N」徽标、抽屉文件区渲染、引用徽标、删除。
func TestFileBadgeAndDrawerPanel(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "带文件任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFileCopy(task.ID, testFileForGUI(t, "副本.txt", "内容一")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFileRef(task.ID, testFileForGUI(t, "共享.txt", "内容二")); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	if !shows(driver, "文 2") {
		t.Fatalf("行内应显示文件徽标：%v", labelValues(driver))
	}
	click(t, driver, "任务文件:带文件任务")
	if !m.detailOpen {
		t.Fatal("点徽标应打开详情抽屉")
	}
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	if !shows(driver, "打开文件:副本.txt") || !shows(driver, "打开文件:共享.txt") {
		t.Fatalf("抽屉应列出文件行：%v", labelValues(driver))
	}
	if !shows(driver, "引用") {
		t.Fatalf("ref 文件应显示引用徽标：%v", labelValues(driver))
	}
	if !shows(driver, "删除文件:副本.txt") {
		t.Fatalf("抽屉应有删除按钮：%v", labelValues(driver))
	}

	// 删除 copy 文件
	click(t, driver, "删除文件:副本.txt")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	if count, _ := repo.FileCount(task.ID); count != 1 {
		t.Fatalf("删除后应剩 1 个：得到 %d（错误 %q）", count, m.errorText)
	}
}

// TestFileCopyReferencePath：复制引用地址把副本路径写入剪贴板（copy 模式）。
func TestFileCopyReferencePath(t *testing.T) {
	m, repo := newTestModelAt(t, filepath.Join(t.TempDir(), "todo.db"))
	task, err := repo.Create(store.TaskInput{Title: "引用任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFileCopy(task.ID, testFileForGUI(t, "文档.txt", "内容")); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	click(t, driver, "任务文件:引用任务")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "复制引用地址:文档.txt")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	clip := driver.Clipboard()
	// copy 模式的引用地址 = 应用数据目录里的副本（UUID 命名），含 files 层级
	if clip == "" || !strings.Contains(clip, "files") || filepath.Ext(clip) != ".txt" {
		t.Fatalf("剪贴板应为副本路径，得到 %q（错误 %q）", clip, m.errorText)
	}
}

// TestFileDrawerRefFromClipboard：剪贴板里的文件路径 →「从剪贴板引用」存为引用。
func TestFileDrawerRefFromClipboard(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "贴引用任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver, invalidator := openWithInvalidator(t, m)
	m.openDetails(m.tasks[0].Task)
	invalidator.Invalidate()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}

	src := testFileForGUI(t, "引用源.txt", "引用内容")
	driver.SetClipboardFiles([]string{src})
	click(t, driver, "从剪贴板引用")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	files, _ := repo.ListFiles(task.ID)
	if len(files) != 1 || files[0].Kind != store.FileRef || files[0].SrcPath != src {
		t.Fatalf("应存入 1 个引用: %+v（错误 %q）", files, m.errorText)
	}
}

// TestFileDrawerDropNonImage：拖放非图片文件 → 文件附件（copy）；拖放图片仍走图片。
func TestFileDrawerDropNonImage(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "拖放任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver, invalidator := openWithInvalidator(t, m)
	m.openDetails(m.tasks[0].Task)
	invalidator.Invalidate()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}

	var target gfx.ID
	var cx, cy float32
	found := false
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == "拖放附件到此处" {
			target, found = hit.ID, true
			cx = hit.Bounds.X + hit.Bounds.Width/2
			cy = hit.Bounds.Y + hit.Bounds.Height/2
			break
		}
	}
	if !found {
		t.Fatalf("抽屉里找不到拖放区：%v（drawerDropID=%v）", hitLabels(driver), m.drawerDropID)
	}
	txtPath := testFileForGUI(t, "拖的文档.txt", "文档内容")
	accepted, err := driver.Drop(gfx.FileDropEvent{
		Phase: gfx.FileDropDrop, X: cx, Y: cy, Paths: []string{txtPath},
	})
	if err != nil || !accepted {
		t.Fatalf("拖放应被认领：accepted=%v err=%v target=%v model=%v", accepted, err, target, m.drawerDropID)
	}
	if count, _ := repo.FileCount(task.ID); count != 1 {
		t.Fatalf("非图片应存为文件附件: %d（错误 %q）", count, m.errorText)
	}
	// 图片拖放仍走图片附件
	_, pngPath := guiTestPNG(t, t.TempDir(), "拖的图.png")
	accepted, err = driver.Drop(gfx.FileDropEvent{
		Phase: gfx.FileDropDrop, X: cx, Y: cy, Paths: []string{pngPath},
	})
	if err != nil || !accepted {
		t.Fatalf("图片拖放应被认领：accepted=%v err=%v", accepted, err)
	}
	if count, _ := repo.ImageCount(task.ID); count != 1 {
		t.Fatalf("图片应存为图片附件: %d（错误 %q）", count, m.errorText)
	}
}

// TestRefFileOpenGuard：ref 模式点「打开文件」提示引用而非报错。
func TestRefFileOpenGuard(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "守卫任务"})
	if err != nil {
		t.Fatal(err)
	}
	src := testFileForGUI(t, "引用守卫.txt", "内容")
	if _, err := repo.AddFileRef(task.ID, src); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	click(t, driver, "任务文件:守卫任务")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "打开文件:引用守卫.txt")
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	if m.errorText != "" {
		t.Fatalf("ref 模式打开文件不应报错: %q", m.errorText)
	}
	if !shows(driver, "这是引用地址") {
		t.Fatalf("应显示引用提示 toast：%v", labelValues(driver))
	}
}
