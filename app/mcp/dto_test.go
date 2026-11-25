package mcp

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"
)

// 三态语义是 update_task 的核心契约（design.md §4.3），必须在包内自证。
func TestUpdateInputTriState(t *testing.T) {
	var absent updateInput
	if err := json.Unmarshal([]byte(`{"id":"a"}`), &absent); err != nil {
		t.Fatalf("absent: %v", err)
	}
	if absent.Patch.Title != nil || absent.Patch.Description != nil ||
		absent.Patch.Priority != nil || absent.Patch.DueAt != nil {
		t.Fatalf("absent: 全部字段应为外层 nil: %+v", absent.Patch)
	}

	var nulls updateInput
	if err := json.Unmarshal([]byte(`{"id":"a","description":null,"due_at":null}`), &nulls); err != nil {
		t.Fatalf("null: %v", err)
	}
	if nulls.Patch.Description == nil || *nulls.Patch.Description != nil {
		t.Fatalf("description 应为外层非 nil、内层 nil: %+v", nulls.Patch.Description)
	}
	if nulls.Patch.DueAt == nil || *nulls.Patch.DueAt != nil {
		t.Fatalf("due_at 应为外层非 nil、内层 nil: %+v", nulls.Patch.DueAt)
	}

	var values updateInput
	if err := json.Unmarshal([]byte(`{"id":"a","title":"新标题","priority":2,"due_at":"2026-11-25T01:00:00+08:00"}`), &values); err != nil {
		t.Fatalf("value: %v", err)
	}
	if values.Patch.Title == nil || *values.Patch.Title == nil || **values.Patch.Title != "新标题" {
		t.Fatalf("title 三层取值错误: %+v", values.Patch.Title)
	}
	if values.Patch.Priority == nil || **values.Patch.Priority != 2 {
		t.Fatalf("priority 取值错误: %+v", values.Patch.Priority)
	}
	if values.Patch.DueAt == nil || **values.Patch.DueAt != "2026-11-25T01:00:00+08:00" {
		t.Fatalf("due_at 取值错误: %+v", values.Patch.DueAt)
	}
}

// 出参时间统一 UTC RFC3339Nano（毫秒保真），null 字段保持 null。
func TestTaskOutTimeFormat(t *testing.T) {
	description := "详情"
	task := &store.Task{
		ID:          "01a",
		Title:       "标题",
		Description: &description,
		Priority:    2,
		DueAt:       new(int64(1774261200123)),
		Status:      1,
		CompletedAt: new(int64(1774261200456)),
		CreatedAt:   1774261200789,
		UpdatedAt:   1774261200789,
	}
	out := newTaskOut(task)
	if out.DueAt == nil || *out.DueAt != "2026-03-23T10:20:00.123Z" {
		got := "<nil>"
		if out.DueAt != nil {
			got = *out.DueAt
		}
		t.Fatalf("due_at = %s, want 2026-03-23T10:20:00.123Z", got)
	}
	if out.CompletedAt == nil || *out.CompletedAt != "2026-03-23T10:20:00.456Z" {
		got := "<nil>"
		if out.CompletedAt != nil {
			got = *out.CompletedAt
		}
		t.Fatalf("completed_at = %s", got)
	}
	if out.CreatedAt != "2026-03-23T10:20:00.789Z" {
		t.Fatalf("created_at = %s", out.CreatedAt)
	}

	empty := newTaskOut(&store.Task{ID: "02a", Title: "t"})
	if empty.Description != nil || empty.DueAt != nil || empty.CompletedAt != nil {
		t.Fatalf("空字段应为 null: %+v", empty)
	}
}

// list_tasks 的协议校验副本必须保留类型约束、去掉数值边界（钳制在代码层）。
func TestListValidationSchemaStripsNumericBounds(t *testing.T) {
	raw := listValidationSchema()
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatalf("校验 schema 非法 JSON: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"limit", "offset"} {
		property := properties[name].(map[string]any)
		if _, ok := property["minimum"]; ok {
			t.Fatalf("%s 校验副本不应含 minimum", name)
		}
		if _, ok := property["maximum"]; ok {
			t.Fatalf("%s 校验副本不应含 maximum", name)
		}
		if property["type"] != "integer" {
			t.Fatalf("%s 校验副本应保留 integer 类型", name)
		}
	}
}

// advertise 的 schema 仍带数值边界与 default，供 agent 阅读。
func TestListAdvertisedSchemaKeepsBounds(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(listTasksSchema), &schema); err != nil {
		t.Fatalf("advertise schema 非法 JSON: %v", err)
	}
	limit := schema["properties"].(map[string]any)["limit"].(map[string]any)
	if limit["maximum"] != float64(maxLimit) || limit["default"] != float64(50) {
		t.Fatalf("advertise limit 边界/默认值缺失: %+v", limit)
	}
}
