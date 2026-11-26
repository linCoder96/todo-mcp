// 图片附件的 GUI 测试（2026-11-26）：徽标、详情抽屉、粘贴/对话框/拖放添加、删除。
package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/gfx"
)

// guiTestPNG 生成一张 32x24 的渐变 PNG；返回字节与磁盘文件路径（落盘的用于
// 对话框/拖放路径，字节用于剪贴板）。
func guiTestPNG(t *testing.T, dir, name string) ([]byte, string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 10), B: 160, A: 255})
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
	return buf.Bytes(), path
}

// waitThumbs 抽屉的缩略图 Effect 是异步的：泵几帧直到 Take 落地。
func waitThumbs(t *testing.T, m *model, driver interface{ Pump() error }) {
	t.Helper()
	for range 200 {
		if len(m.thumbs) > 0 {
			return
		}
		if err := driver.Pump(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // Effect 解码 goroutine 的落地窗口
	}
}

// TestImageBadgeAndDrawerDelete：徽标显示与计数、抽屉打开、删除按钮。
func TestImageBadgeAndDrawerDelete(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "带图任务"})
	if err != nil {
		t.Fatal(err)
	}
	_, pngPath := guiTestPNG(t, t.TempDir(), "截图.png")
	for range 2 {
		if _, err := repo.AddImageFile(task.ID, pngPath); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	if !shows(driver, "图 2") {
		t.Fatalf("行内应显示图片徽标：%v", labelValues(driver))
	}
	click(t, driver, "任务图片:带图任务")
	if !m.detailOpen {
		t.Fatal("点徽标应打开详情抽屉")
	}
	waitThumbs(t, m, driver)
	// 缩略图文件名是普通 Text，不进可达名快照；删除按钮可达名含文件名
	if !shows(driver, "删除图片 截图.png") {
		t.Fatalf("抽屉应显示缩略图与删除按钮：%v", labelValues(driver))
	}
	click(t, driver, "删除图片 截图.png")
	waitThumbs(t, m, driver)
	if count, _ := repo.ImageCount(task.ID); count < 1 || count > 2 {
		t.Fatalf("删除一张后应剩 1 张（异步期间 1~2），得到 %d", count)
	}
	if !shows(driver, "图 ") {
		t.Fatalf("删除后行内徽标应更新：%v", labelValues(driver))
	}
}

// TestImageDrawerAddViaClipboardAndPicker：剪贴板粘贴与文件对话框两条添加路径。
func TestImageDrawerAddViaClipboardAndPicker(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "加图任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver, invalidator := openWithInvalidator(t, m)
	m.openDetails(m.tasks[0].Task) // 无图任务没有徽标，直接打开抽屉
	invalidator.Invalidate()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}

	// 剪贴板粘贴
	pngBytes, _ := guiTestPNG(t, t.TempDir(), "粘贴.png")
	if err := driver.SetClipboardImage(pngBytes); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "从剪贴板粘贴")
	if count, _ := repo.ImageCount(task.ID); count != 1 {
		t.Fatalf("粘贴后应挂上 1 张，得到 %d（错误 %q）", count, m.errorText)
	}

	// 文件对话框（QueueOpenFiles 预置结果）
	_, pngPath := guiTestPNG(t, t.TempDir(), "选的.png")
	if err := driver.QueueOpenFiles([]string{pngPath}, nil); err != nil {
		t.Fatal(err)
	}
	click(t, driver, "添加图片")
	if count, _ := repo.ImageCount(task.ID); count != 2 {
		t.Fatalf("对话框选择后应共 2 张，得到 %d（错误 %q）", count, m.errorText)
	}
	waitThumbs(t, m, driver)
	if !shows(driver, "图 2") {
		t.Fatalf("行内徽标应为 图 2：%v", labelValues(driver))
	}
}

// TestImageDrawerDrop：拖放到抽屉虚线框挂图；拖到别处不认领。
func TestImageDrawerDrop(t *testing.T) {
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

	_, pngPath := guiTestPNG(t, t.TempDir(), "拖的.png")
	// 抽屉已声明拖放区，按它的身份投递 drop
	var cx, cy float32
	found := false
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == "拖放附件到此处" {
			cx = hit.Bounds.X + hit.Bounds.Width/2
			cy = hit.Bounds.Y + hit.Bounds.Height/2
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("抽屉里找不到拖放区：%v", hitLabels(driver))
	}
	// driver.Drop 按坐标 HitTest 定目标（event.Target 由框架覆盖）
	accepted, err := driver.Drop(gfx.FileDropEvent{
		Phase: gfx.FileDropDrop,
		X:     cx,
		Y:     cy,
		Paths: []string{pngPath},
	})
	if err != nil || !accepted {
		t.Fatalf("拖放应被认领：accepted=%v err=%v", accepted, err)
	}
	if count, _ := repo.ImageCount(task.ID); count != 1 {
		t.Fatalf("拖放后应挂上 1 张，得到 %d（错误 %q）", count, m.errorText)
	}

	// 拖到无主位置：不认领（系统显示不可拖入）
	stray, err := driver.Drop(gfx.FileDropEvent{
		Phase: gfx.FileDropDrop,
		X:     10,
		Y:     10,
		Paths: []string{pngPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stray {
		t.Fatal("拖放区之外不应认领")
	}
}

// TestDrawerClosesWhenTaskDeleted：任务被删后抽屉自动收起（refresh → syncDetail）。
func TestDrawerClosesWhenTaskDeleted(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "将删任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	m.openDetails(m.tasks[0].Task)
	if !m.detailOpen {
		t.Fatal("抽屉应打开")
	}

	if _, err := repo.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	m.applyQueryChange() // 任意一次重查（等价于用户点了别的操作）
	if m.detailOpen {
		t.Fatal("任务删除后抽屉应收起")
	}
	_ = driver
}
