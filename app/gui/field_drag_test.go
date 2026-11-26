// 列设置面板字段行拖拽排序：driver 驱动真实指针事件，无窗口。
package gui

import (
	"testing"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
)

// fieldRowHit 找列设置面板中目标字段行的 Hit（label = "字段行:"+字段名）。
func fieldRowHit(t *testing.T, driver *app.Driver, name string) gfx.Rect {
	t.Helper()
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == "字段行:"+name {
			return hit.Bounds
		}
	}
	t.Fatalf("找不到字段行 %q（hits: %v）", name, hitLabels(driver))
	return gfx.Rect{}
}

// TestFieldDragReorder：在列设置面板把字段从中间行拖到末行，
// 断言内存列序、库内 position 与表格列头顺序同步更新。
func TestFieldDragReorder(t *testing.T) {
	m, repo := newTestModel(t)
	for _, name := range []string{"甲字段", "乙字段", "丙字段"} {
		if _, err := repo.CreateField(store.FieldInput{Name: name, Type: store.FieldTypeText}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	click(t, driver, "列设置")
	if !shows(driver, "显示列") {
		t.Fatalf("列设置面板未打开: %v", labelValues(driver))
	}

	// 断言初始身份表：按顺序建好三行（隐藏列也有身份）。
	if len(m.fieldDragRows) != 3 {
		t.Fatalf("字段行身份表应有 3 行: %d", len(m.fieldDragRows))
	}

	// 从「甲字段」行（中部把手处）按下，拖到「丙字段」行松开。
	from := fieldRowHit(t, driver, "甲字段")
	to := fieldRowHit(t, driver, "丙字段")
	sx, sy := from.X+from.Width/2, from.Y+from.Height/2
	tx, ty := to.X+to.Width/2, to.Y+to.Height/2
	if err := driver.Press(sx, sy, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	if err := driver.Move(tx, ty); err != nil {
		t.Fatal(err)
	}
	if !m.fieldDrag.Dragging() {
		t.Fatalf("按下移动后应进入拖拽态: source=%v target=%v", m.fieldDrag.Source(), m.fieldDrag.Target())
	}
	if err := driver.Release(tx, ty, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	waitRefresh(t, driver)

	// remove + insert：甲拖到丙位置 → 乙、丙、甲。
	want := []string{"乙字段", "丙字段", "甲字段"}
	if len(m.fields) != len(want) {
		t.Fatalf("字段数变化: %d", len(m.fields))
	}
	for i, name := range want {
		if m.fields[i].Name != name {
			t.Fatalf("内存列序第 %d 位 = %q, want %q（实际: %v）", i, m.fields[i].Name, name, fieldNames(m.fields))
		}
	}

	// 库内 position 应与新列序一致（重启后仍保持）。
	stored, err := repo.ListFields()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(want) {
		t.Fatalf("库内字段数变化: %d", len(stored))
	}
	for i, name := range want {
		if stored[i].Name != name || stored[i].Position != i {
			t.Fatalf("库内第 %d 位 = %q(position=%d), want %q/%d", i, stored[i].Name, stored[i].Position, name, i)
		}
	}
	assertLayoutClean(t, driver)
}

// fieldNames 仅用于失败信息：按序列出字段名。
func fieldNames(fields []*store.Field) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}

// TestFieldDragRejectsSelf：把字段拖到自身行（或面板外）不应改变列序。
func TestFieldDragRejectsSelf(t *testing.T) {
	m, repo := newTestModel(t)
	for _, name := range []string{"甲字段", "乙字段"} {
		if _, err := repo.CreateField(store.FieldInput{Name: name, Type: store.FieldTypeText}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	driver := open(t, m)
	click(t, driver, "列设置")

	bounds := fieldRowHit(t, driver, "甲字段")
	x, y := bounds.X+bounds.Width/2, bounds.Y+bounds.Height/2
	if err := driver.Press(x, y, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	// 行内小幅移动（超过 4px 阈值但不离开本行）→ 拖拽生效，目标仍是自身。
	if err := driver.Move(x+12, y); err != nil {
		t.Fatal(err)
	}
	if !m.fieldDrag.Dragging() {
		t.Fatal("行内移动应进入拖拽态")
	}
	if err := driver.Release(x+12, y, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	waitRefresh(t, driver)

	if len(m.fields) != 2 || m.fields[0].Name != "甲字段" || m.fields[1].Name != "乙字段" {
		t.Fatalf("拖到自身不应改变列序: %v", fieldNames(m.fields))
	}
	assertLayoutClean(t, driver)
}
