package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestTaskRepoDataVersion：data_version 只在「其它连接」提交后变化。
// GUI 进程靠这一点区分自己的写入（已在点击处重查）与 MCP 进程的写入（需要重查）。
func TestTaskRepoDataVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.db")
	gui, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer gui.Close()
	// 同一文件上的第二个连接，等价于另一个进程（MCP 模式）。
	mcp, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer mcp.Close()
	guiRepo, mcpRepo := NewTaskRepo(gui), NewTaskRepo(mcp)

	before, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}

	// 本连接提交：不应变化（否则用户每次操作都会多一次重查）。
	if _, err := guiRepo.Create(TaskInput{Title: "GUI 自己建的"}); err != nil {
		t.Fatal(err)
	}
	afterOwn, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterOwn != before {
		t.Fatalf("本连接提交后 data_version 不应变：before=%d after=%d", before, afterOwn)
	}

	// 其它连接提交：应变化（任务）。
	if _, err := mcpRepo.Create(TaskInput{Title: "MCP 建的"}); err != nil {
		t.Fatal(err)
	}
	afterTask, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterTask == afterOwn {
		t.Fatalf("其它连接写入任务后 data_version 应变：still %d", afterTask)
	}

	// 其它连接提交：应变化（字段定义）。
	if _, err := mcpRepo.CreateField(FieldInput{Name: "备注", Type: FieldTypeText}); err != nil {
		t.Fatal(err)
	}
	afterField, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterField == afterTask {
		t.Fatalf("其它连接建字段后 data_version 应变：still %d", afterField)
	}

	// 其它连接提交：应变化（字段值）。
	task, err := mcpRepo.Create(TaskInput{Title: "带值"})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]json.RawMessage{"备注": json.RawMessage(`"重要"`)}
	patch := TaskPatch{Values: new(new(values))}
	if _, err := mcpRepo.Update(task.ID, patch); err != nil {
		t.Fatal(err)
	}
	afterValue, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	if afterValue == afterField {
		t.Fatalf("其它连接写字段值后 data_version 应变：still %d", afterValue)
	}

	// 什么都没发生：读两次结果相同。
	idle, err := guiRepo.DataVersion()
	if err != nil {
		t.Fatal(err)
	}
	if idle != afterValue {
		t.Fatalf("无写入时 data_version 应稳定：%d then %d", afterValue, idle)
	}
}
