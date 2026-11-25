package mcp

import (
	"encoding/json"
	"testing"

	"todo-mcp/app/store"
)

func TestFieldSchemasAdvertiseAndValidate(t *testing.T) {
	// 4 个字段工具的 schema 必须可解析（resolveSchema 会在解析失败时 panic）
	for _, schema := range []string{createFieldSchema, listFieldsSchema, updateFieldSchema, fieldIDInputSchema} {
		resolved := resolveSchema(schema)
		if resolved == nil {
			t.Fatal("schema 解析返回 nil")
		}
	}

	// 新 schema 必须声明 values / filter / sort / group_by（否则 addTypedTool 会拒绝参数）
	for name, schema := range map[string]string{
		"create_task": createTaskSchema,
		"update_task": updateTaskSchema,
		"list_tasks":  listTasksSchema,
	} {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
			t.Fatalf("%s schema 非法: %v", name, err)
		}
		props := parsed["properties"].(map[string]any)
		want := map[string][]string{
			"create_task": {"values"},
			"update_task": {"values"},
			"list_tasks":  {"filter", "sort", "group_by"},
		}[name]
		for _, key := range want {
			if _, ok := props[key]; !ok {
				t.Fatalf("%s schema 缺少 %s", name, key)
			}
		}
	}

	// list_tasks 的校验副本仍保留新属性（只去掉 limit/offset 数值边界）
	validation := listValidationSchema()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(validation), &parsed); err != nil {
		t.Fatal(err)
	}
	props := parsed["properties"].(map[string]any)
	for _, key := range []string{"filter", "sort", "group_by"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("list_tasks 校验副本缺少 %s", key)
		}
	}
	limit := props["limit"].(map[string]any)
	if _, ok := limit["maximum"]; ok {
		t.Fatal("校验副本应去掉 limit maximum（钳制在代码层）")
	}
}

func TestBuiltinFieldMetaMatchesStore(t *testing.T) {
	// 内置字段 id 必须与 store.BuiltinFieldID 一致
	for _, meta := range builtinFieldMeta {
		if !store.BuiltinFieldID(meta.ID) {
			t.Fatalf("内置字段 %s 与 store 不一致", meta.ID)
		}
	}
}
