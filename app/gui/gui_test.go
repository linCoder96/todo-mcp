package gui

import (
	"path/filepath"
	"strings"
	"testing"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// newTestModel 用 t.TempDir 的临时库建模型；不依赖真实 %APPDATA% 或窗口。
func newTestModel(t *testing.T) (*model, *store.TaskRepo) {
	t.Helper()
	db, err := store.OpenAt(filepath.Join(t.TempDir(), "todo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewTaskRepo(db)
	return newModel(repo), repo
}

// open 以真实绘制器/路由/焦点规则驱动页面，不打开窗口（官方 skill §5 第一层）。
func open(t *testing.T, m *model) *app.Driver {
	t.Helper()
	page := ui.NewPage(ui.PageConfig{View: m.page})
	invalidator := &app.Invalidator{}
	base := app.Config{
		Title:       "待办",
		Width:       880,
		Height:      640,
		Invalidator: invalidator,
		// 与 gui.go 的 OnActivate 保持一致：聚焦时重查并请求重绘
		OnActivate: func() {
			if err := m.refresh(); err != nil {
				m.errorText = err.Error()
			}
			invalidator.Invalidate()
		},
	}
	driver, err := app.NewDriver(ui.Configure(page, base), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(driver.Close)
	return driver
}

// click 按可访问名点击控件（不按像素）。
func click(t *testing.T, driver *app.Driver, name string) {
	t.Helper()
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == name || strings.HasPrefix(hit.Label, name) {
			bounds := hit.Bounds
			if err := driver.Click(bounds.X+bounds.Width/2, bounds.Y+bounds.Height/2); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("页面上找不到可点击控件 %q（tree: %v）", name, hitLabels(driver))
}

// clickCheckbox 点击复选框方框本身（命中区域左端），避免落在标签文字上。
func clickCheckbox(t *testing.T, driver *app.Driver, label string) {
	t.Helper()
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == label {
			bounds := hit.Bounds
			if err := driver.Click(bounds.X+8, bounds.Y+bounds.Height/2); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("找不到复选框 %q（tree: %v）", label, hitLabels(driver))
}

func hitLabels(driver *app.Driver) []string {
	labels := make([]string, 0)
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label != "" {
			labels = append(labels, hit.Label)
		}
	}
	return labels
}

func labelValues(driver *app.Driver) []string {
	values := make([]string, 0)
	for _, label := range driver.Snapshot().Labels {
		values = append(values, label.Value)
	}
	return values
}

func shows(driver *app.Driver, want string) bool {
	for _, value := range labelValues(driver) {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

// typeInto 先点击控件取得焦点，再输入文本。
func typeInto(t *testing.T, driver *app.Driver, label, text string) {
	t.Helper()
	click(t, driver, label)
	if err := driver.Type(text); err != nil {
		t.Fatal(err)
	}
}

// assertLayoutClean 等价于 headless 的 layout 检查：所有绘制文本非空且落在窗口内。
func assertLayoutClean(t *testing.T, driver *app.Driver) {
	t.Helper()
	frame := driver.Frame()
	for _, label := range driver.Snapshot().Labels {
		if strings.TrimSpace(label.Value) == "" {
			t.Fatal("layout: 存在空白标签")
		}
		b := label.Bounds
		if label.Clipped {
			// 控件内被裁剪的文本以 Clip 为准（headless CJK 字宽偏大，属测量口径）
			b = label.Clip
		}
		if b.X < -1 || b.Y < -1 || b.X+b.Width > frame.Width+1 || b.Y+b.Height > frame.Height+1 {
			t.Fatalf("layout: 文本 %q 越界: %+v, frame=%+v, all=%+v", label.Value, b, frame, driver.Snapshot().Labels)
		}
	}
}

func TestTaskListRendering(t *testing.T) {
	m, repo := newTestModel(t)
	for _, seed := range []struct {
		title    string
		priority int
		done     bool
	}{
		{"进行中任务", 2, false},
		{"已完成任务", 1, true},
		{"高优先级任务", 3, false},
	} {
		task, err := repo.Create(store.TaskInput{Title: seed.title, Priority: seed.priority})
		if err != nil {
			t.Fatal(err)
		}
		if seed.done {
			if _, err := repo.Complete(task.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// tree / expect：列表渲染了三条任务，且固定排序把已完成排最后
	for _, title := range []string{"进行中任务", "已完成任务", "高优先级任务"} {
		if !shows(driver, title) {
			t.Fatalf("tree: 列表缺少 %q，labels=%v", title, labelValues(driver))
		}
	}
	values := labelValues(driver)
	indexOf := func(text string) int {
		for i, value := range values {
			if value == text {
				return i
			}
		}
		return -1
	}
	if indexOf("已完成任务") < indexOf("高优先级任务") {
		t.Fatalf("固定排序错误：已完成任务不应排在进行中之前：%v", values)
	}
	assertLayoutClean(t, driver)
}

func TestQuickAddOnEnter(t *testing.T) {
	m, repo := newTestModel(t)
	driver := open(t, m)

	typeInto(t, driver, "快捷新建", "回车新建的任务")
	if err := driver.Key(gfx.KeyEnter, 0); err != nil {
		t.Fatal(err)
	}
	total, err := repo.Count(store.TaskFilter{})
	if err != nil || total != 1 {
		t.Fatalf("回车新建后任务数 = %d err=%v", total, err)
	}
	if !shows(driver, "回车新建的任务") || m.quickAdd != "" {
		t.Fatalf("新建后应显示任务并清空输入框：labels=%v quickAdd=%q", labelValues(driver), m.quickAdd)
	}
	assertLayoutClean(t, driver)
}

func TestCheckboxCompleteAndReopen(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "勾选任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	clickCheckbox(t, driver, "勾选任务")
	updated, err := repo.Get(task.ID)
	if err != nil || updated.Status != 1 || updated.CompletedAt == nil {
		t.Fatalf("勾选应完成：%+v err=%v", updated, err)
	}

	// 再点一次取消勾选，走 store.Reopen
	clickCheckbox(t, driver, "勾选任务")
	reopened, err := repo.Get(task.ID)
	if err != nil || reopened.Status != 0 || reopened.CompletedAt != nil {
		t.Fatalf("取消勾选应清空完成状态：%+v err=%v", reopened, err)
	}
}

func TestFilterTabs(t *testing.T) {
	m, repo := newTestModel(t)
	done, err := repo.Create(store.TaskInput{Title: "筛选已完成任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Complete(done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(store.TaskInput{Title: "筛选进行中任务"}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "已完成")
	if shows(driver, "筛选进行中任务") || !shows(driver, "筛选已完成任务") {
		t.Fatalf("已完成 tab 筛选错误：labels=%v", labelValues(driver))
	}
	click(t, driver, "全部")
	if !shows(driver, "筛选进行中任务") || !shows(driver, "筛选已完成任务") {
		t.Fatalf("全部 tab 应显示两条：labels=%v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}

func TestDeleteNeedsSecondConfirm(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.Create(store.TaskInput{Title: "待删除任务"}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	click(t, driver, "删除")
	if total, _ := repo.Count(store.TaskFilter{}); total != 1 {
		t.Fatal("第一次点击删除不应真正删除")
	}
	if !shows(driver, "确认删除？") {
		t.Fatalf("第一次点击应出现原位二次确认：labels=%v", labelValues(driver))
	}
	click(t, driver, "确认删除？")
	if total, _ := repo.Count(store.TaskFilter{}); total != 0 {
		t.Fatal("第二次点击应删除任务")
	}
}

func TestInlineEditAndFocusRefresh(t *testing.T) {
	m, repo := newTestModel(t)
	task, err := repo.Create(store.TaskInput{Title: "行内编辑任务"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	// 点击行展开编辑器
	click(t, driver, "编辑任务:行内编辑任务")
	click(t, driver, "标题")
	// 用退格清空原标题，再输入新标题（Driver.Chord 仅终端可用）
	for range len([]rune(m.editTitle)) {
		if err := driver.Key(gfx.KeyBackspace, 0); err != nil {
			t.Fatal(err)
		}
	}
	typeInto(t, driver, "标题", "编辑后的标题")
	typeInto(t, driver, "到期时间", "2026-11-25 09:00")
	click(t, driver, "保存")

	updated, err := repo.Get(task.ID)
	if err != nil || updated.Title != "编辑后的标题" || updated.DueAt == nil {
		t.Fatalf("行内编辑未落库：%+v err=%v", updated, err)
	}

	// 模拟另一进程写入后窗口聚焦：OnActivate 触发重查（MCP→GUI 可见性）
	if _, err := repo.Create(store.TaskInput{Title: "MCP 侧新建的任务"}); err != nil {
		t.Fatal(err)
	}
	if shows(driver, "MCP 侧新建的任务") {
		t.Fatal("聚焦前不应已显示外部写入的任务")
	}
	if err := driver.ActivateWindow(); err != nil {
		t.Fatal(err)
	}
	if err := driver.Pump(); err != nil {
		t.Fatal(err)
	}
	if !shows(driver, "MCP 侧新建的任务") {
		t.Fatalf("聚焦重查失败：labels=%v", labelValues(driver))
	}
}

// TestColsPanelFixedColumns：完成/待办事项列为固定列——列设置面板不提供其勾选框、
// 残留隐藏状态不影响其渲染；其余列（优先级）仍可显隐（2026-11-25 字段管理）。
func TestColsPanelFixedColumns(t *testing.T) {
	m, repo := newTestModel(t)
	if _, err := repo.Create(store.TaskInput{Title: "列管理测试任务", Priority: 2}); err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)

	countExact := func(want string) int {
		n := 0
		for _, value := range labelValues(driver) {
			if value == want {
				n++
			}
		}
		return n
	}
	hasHit := func(want string) bool {
		for _, hit := range driver.Snapshot().Hits {
			if hit.Label == want {
				return true
			}
		}
		return false
	}

	// 基线：固定列与优先级列头齐全
	if countExact("完成") != 1 || countExact("待办事项") != 1 || countExact("优先级") != 1 {
		t.Fatalf("基线列头缺失: %v", labelValues(driver))
	}

	// 残留隐藏状态不能隐藏固定列
	m.hiddenCols[colDone] = true
	m.hiddenCols[colTitle] = true
	waitRefresh(t, driver)
	if countExact("完成") != 1 || countExact("待办事项") != 1 {
		t.Fatalf("固定列应不受隐藏状态影响: %v", labelValues(driver))
	}
	m.hiddenCols[colDone], m.hiddenCols[colTitle] = false, false
	waitRefresh(t, driver)

	// 列设置面板：固定列不出勾选框，优先级列可勾选
	click(t, driver, "列设置")
	if !shows(driver, "显示列") {
		t.Fatalf("列设置面板未打开: %v", labelValues(driver))
	}
	if hasHit("完成") || hasHit("待办事项") || hasHit("操作") {
		t.Fatalf("固定列不应出现在列设置面板: %v", hitLabels(driver))
	}
	if !hasHit("优先级") {
		t.Fatalf("优先级列应可在列设置面板显隐: %v", hitLabels(driver))
	}

	// 隐藏优先级列 → 列头消失；再显示 → 列头恢复
	clickCheckbox(t, driver, "优先级")
	if err := driver.Key(gfx.KeyEscape, 0); err != nil {
		t.Fatal(err)
	}
	waitRefresh(t, driver)
	if countExact("优先级") != 0 {
		t.Fatalf("隐藏优先级列后列头应消失: %v", labelValues(driver))
	}
	if countExact("完成") != 1 {
		t.Fatalf("隐藏优先级列后固定列应仍在: %v", labelValues(driver))
	}
	click(t, driver, "列设置")
	clickCheckbox(t, driver, "优先级")
	if err := driver.Key(gfx.KeyEscape, 0); err != nil {
		t.Fatal(err)
	}
	waitRefresh(t, driver)
	if countExact("优先级") != 1 {
		t.Fatalf("恢复优先级列后列头应出现: %v", labelValues(driver))
	}
	assertLayoutClean(t, driver)
}
