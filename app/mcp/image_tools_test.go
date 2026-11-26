// 图片工具的 schema 与出参形状测试（2026-11-26）。
package mcp

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"
)

func TestFileSchemasParse(t *testing.T) {
	for _, schema := range []string{attachFileSchema, attachFileRefSchema, removeFileSchema} {
		resolved := resolveSchema(schema)
		if resolved == nil {
			t.Fatal("schema 解析返回 nil")
		}
	}
}

func TestTaskOutFilesShape(t *testing.T) {
	// 有文件：逐项带 id/file_name/kind/src_path(copy 不带)/size
	task := &store.Task{
		ID: "t1", Title: "任务", Status: 0,
		CreatedAt: 1795500000000, UpdatedAt: 1795500000000,
		Files: []store.TaskFile{{
			ID: "f1", TaskID: "t1", FileName: "文档.docx", Kind: store.FileCopy,
			StoredName: "f1.docx", Size: 2345, Position: 0, CreatedAt: 1795500001000,
		}, {
			ID: "f2", TaskID: "t1", FileName: "表格.xlsx", Kind: store.FileRef,
			SrcPath: `D:\docs\表格.xlsx`, Size: 100, Position: 1, CreatedAt: 1795500002000,
		}},
	}
	encoded, err := json.Marshal(newTaskOut(task))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Files []struct {
			ID       string `json:"id"`
			FileName string `json:"file_name"`
			Kind     string `json:"kind"`
			SrcPath  string `json:"src_path"`
			Size     int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 2 {
		t.Fatalf("files 出参应 2 项: %s", encoded)
	}
	if out.Files[0].Kind != "copy" || out.Files[0].SrcPath != "" || out.Files[0].Size != 2345 {
		t.Fatalf("copy 项形状错误: %+v", out.Files[0])
	}
	if out.Files[1].Kind != "ref" || out.Files[1].SrcPath != `D:\docs\表格.xlsx` {
		t.Fatalf("ref 项应带源路径: %+v", out.Files[1])
	}

	// 无文件：空数组（非 null），与 images/values 口径一致
	empty, err := json.Marshal(newTaskOut(&store.Task{ID: "t2", Title: "无文件", Status: 0,
		CreatedAt: 1795500000000, UpdatedAt: 1795500000000}))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(empty, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Files == nil {
		t.Fatalf("无文件时 files 应为空数组而非 null: %s", empty)
	}
}

func TestTaskOutImagesShape(t *testing.T) {
	// 有图：逐项带 id/file_name/mime/size/created_at
	task := &store.Task{
		ID: "t1", Title: "任务", Status: 0,
		CreatedAt: 1795500000000, UpdatedAt: 1795500000000,
		Images: []store.TaskImage{{
			ID: "img1", TaskID: "t1", FileName: "截图.png", StoredName: "img1.png",
			Mime: "image/png", Size: 1234, Position: 0, CreatedAt: 1795500001000,
		}},
	}
	encoded, err := json.Marshal(newTaskOut(task))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Images []struct {
			ID        string `json:"id"`
			FileName  string `json:"file_name"`
			Mime      string `json:"mime"`
			Size      int64  `json:"size"`
			CreatedAt string `json:"created_at"`
		} `json:"images"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Images) != 1 || out.Images[0].ID != "img1" || out.Images[0].FileName != "截图.png" ||
		out.Images[0].Mime != "image/png" || out.Images[0].Size != 1234 || out.Images[0].CreatedAt == "" {
		t.Fatalf("images 出参形状错误: %s", encoded)
	}

	// 无图：空数组（非 null），与 values 的口径一致
	empty, err := json.Marshal(newTaskOut(&store.Task{ID: "t2", Title: "无图", Status: 0,
		CreatedAt: 1795500000000, UpdatedAt: 1795500000000}))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(empty) || string(empty) == "" {
		t.Fatal("空任务出参非法")
	}
	var probe struct {
		Images []json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(empty, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Images == nil {
		t.Fatalf("无图时 images 应为空数组而非 null: %s", empty)
	}
}
