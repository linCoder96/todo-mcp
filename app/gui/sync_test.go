package gui

import (
	"path/filepath"
	"testing"
	"time"

	"todo-mcp/app/store"
)

// waitForChange 等 watcher 报告一次变化。
func waitForChange(t *testing.T, changed <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatalf("超时：%s", what)
	}
}

// expectNoChange 确认 watcher 在这段时间里没有报告变化。
func expectNoChange(t *testing.T, changed <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-changed:
		t.Fatalf("不应报告变化：%s", what)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestWatchExternalReportsMCPWrites：其它连接（= MCP 进程）写库后 watcher 报告
// 变化；本连接写库不报告（用户自己的操作已在点击处重查过）。
func TestWatchExternalReportsMCPWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.db")
	m, _ := newTestModelAt(t, path)

	// 同一文件上的第二个连接，等价于 MCP 进程。
	external, err := store.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = external.Close() })
	mcpRepo := store.NewTaskRepo(external)

	changed := make(chan struct{}, 16)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		m.watchExternalWrites(20*time.Millisecond, stop, func() { changed <- struct{}{} })
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	// 等 watcher 取到基线读数（首轮读取在启动时，给它几个周期）。
	time.Sleep(150 * time.Millisecond)

	if _, err := mcpRepo.Create(store.TaskInput{Title: "MCP 建的任务"}); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changed, "MCP 写入任务后应报告变化")

	if _, err := m.repo.Create(store.TaskInput{Title: "GUI 自己建的"}); err != nil {
		t.Fatal(err)
	}
	expectNoChange(t, changed, "GUI 自己写库")

	if _, err := mcpRepo.Create(store.TaskInput{Title: "MCP 又建的任务"}); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changed, "MCP 第二次写入后应报告变化")
}

// TestExternalWriteShowsAfterRefresh：watcher 报告之后走的那条路径
// （重查 + 请求重绘）确实让外部写入的任务出现在页面上，窗口不必先获得焦点。
func TestExternalWriteShowsAfterRefresh(t *testing.T) {
	m, repo := newTestModel(t)
	driver, invalidator := openWithInvalidator(t, m)

	if _, err := repo.Create(store.TaskInput{Title: "外部写入的任务"}); err != nil {
		t.Fatal(err)
	}
	if shows(driver, "外部写入的任务") {
		t.Fatal("重查前不应显示外部写入的任务")
	}

	// 与 gui.go 里 application.Post 的内容一致
	m.refreshAfterExternalWrite()
	invalidator.Invalidate()
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	if !shows(driver, "外部写入的任务") {
		t.Fatalf("重查并重绘后应显示外部写入的任务：%v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}
