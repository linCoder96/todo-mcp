// Package mcp 的工具注册与 handler。
//
// 这里只用 SDK 的低层 server.AddTool：go-sdk v1.8.0 的泛型 AddTool 会把
// input schema 校验失败打包成 CallToolResult{IsError:true}（mcp/server.go
// toolForErr 中的 applySchema 分支），而 design.md §4 要求这类参数错误是
// JSON-RPC invalid_params。故用显式 InputSchema（与 §4 逐字一致）+ 自校验，
// 参数错误返回真实 *jsonrpc.Error(-32602)；业务失败仍由 SetError 变成
// isError=true 的工具结果。
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"todo-mcp/app/store"
)

// 工具的 input schema（前五个逐字对齐 design.md §4.1~§4.5；archive/unarchive
// 为 2026-11-26 归档功能新增，与 idSchema 同构）。
const createTaskSchema = `{
  "type": "object",
  "properties": {
    "title":       { "type": "string", "minLength": 1, "maxLength": 500, "description": "任务标题（必填）" },
    "description": { "type": "string", "maxLength": 5000, "description": "任务详情（可选）" },
    "priority":    { "type": "integer", "enum": [0, 1, 2, 3], "default": 0, "description": "优先级：0=无 1=低 2=中 3=高" },
    "due_at":      { "type": "string", "format": "date-time", "description": "到期时间，RFC3339（可选）" },
    "values":      { "type": "object", "additionalProperties": true, "description": "自定义字段值：键=字段 id，值按字段类型（文本=string、数字=number、日期=RFC3339、单选=string、多选=string 数组），见 list_fields" }
  },
  "required": ["title"],
  "additionalProperties": false
}`

const listTasksSchema = `{
  "type": "object",
  "properties": {
    "status":   { "type": "integer", "enum": [0, 1], "description": "筛选状态：0=进行中 1=已完成（缺省=不过滤）" },
    "archived": { "type": "boolean", "description": "归档筛选（2026-11-26 归档功能）：true=只看已归档；缺省/false=只看未归档（归档任务默认不出现在列表里）" },
    "limit":    { "type": "integer", "minimum": 1, "maximum": 200, "default": 50, "description": "分页大小，上限 200" },
    "offset":   { "type": "integer", "minimum": 0, "default": 0, "description": "分页偏移" },
    "filter":   { "type": "array", "maxItems": 10, "items": {
                   "type": "object",
                   "properties": {
                     "field": { "type": "string", "description": "字段：内置名（title/description/priority/due_at/status/created_at/completed_at）或自定义字段 id" },
                     "op":    { "type": "string", "enum": ["eq", "contains", "gt", "lt", "is", "has", "before", "after", "empty", "not_empty"], "description": "操作符，按字段类型限制（见 list_fields）" },
                     "value": { "description": "比较值：文本/选项=string，数字=number，日期=RFC3339；empty/not_empty 省略" }
                   },
                   "required": ["field", "op"],
                   "additionalProperties": false
                 }, "description": "字段筛选条件（AND 语义）" },
    "sort":     { "type": "array", "maxItems": 3, "items": {
                   "type": "object",
                   "properties": {
                     "field": { "type": "string", "description": "排序字段（同 filter.field 语义）" },
                     "desc":  { "type": "boolean", "default": false, "description": "true=降序" }
                   },
                   "required": ["field"],
                   "additionalProperties": false
                 }, "description": "排序键（缺省=固定排序）" },
    "group_by": { "type": "string", "description": "分组字段；设置后返回 groups（此时 limit/offset 不生效）" }
  },
  "additionalProperties": false
}`

const updateTaskSchema = `{
  "type": "object",
  "properties": {
    "id":          { "type": "string", "description": "任务 id（必填）" },
    "title":       { "type": "string", "minLength": 1, "maxLength": 500, "description": "新标题（可选）" },
    "description": { "type": ["string", "null"], "maxLength": 5000, "description": "新详情；传 null 清空（可选）" },
    "priority":    { "type": "integer", "enum": [0, 1, 2, 3], "description": "新优先级（可选）" },
    "due_at":      { "type": ["string", "null"], "format": "date-time", "description": "新到期时间；传 null 清除到期（可选）" },
    "values":      { "type": ["object", "null"], "additionalProperties": true, "description": "自定义字段值：键=字段 id，值按字段类型；传 null 清空某字段值（可选）" }
  },
  "required": ["id"],
  "additionalProperties": false
}`

const idSchema = `{
  "type": "object",
  "properties": { "id": { "type": "string", "description": "任务 id（必填）" } },
  "required": ["id"],
  "additionalProperties": false
}`

// maxLimit 与 design.md §4.2 的分页上限一致；store.List 也独立钳制。
const maxLimit = 200

// invalidParams 构造真正的 JSON-RPC invalid_params（-32602）协议错误。
func invalidParams(format string, args ...any) error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: fmt.Sprintf(format, args...),
	}
}

func resolveSchema(raw string) *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		panic(fmt.Errorf("解析工具 schema 失败: %w", err))
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		panic(fmt.Errorf("构建工具 schema 失败: %w", err))
	}
	return resolved
}

// addTypedTool 在本项目内复刻官方泛型 handler 的“校验 → 解码 → 调用 →
// 打包 structuredContent”流程，只把参数错误的出口换成协议错误。
// validation 与 schema 分离：design.md §4.2 要求 limit>200 由代码钳制并回显
// （不报错），因此 advertise 的 schema 保留 maximum/minimum 作为给 agent 的
// 文档提示，协议校验副本则去掉数值边界，钳制交给 handler。
func addTypedTool[In, Out any](
	server *mcp.Server,
	name, description, schema, validation string,
	validate func(In) error,
	run func(context.Context, In) (Out, error),
) {
	resolved := resolveSchema(validation)
	server.AddTool(&mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schema),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var raw json.RawMessage
		if req.Params != nil {
			raw = req.Params.Arguments
		}
		var decoded any = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, invalidParams("参数不是合法 JSON: %v", err)
			}
		}
		if err := resolved.Validate(decoded); err != nil {
			return nil, invalidParams("参数校验失败: %v", err)
		}

		var in In
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, invalidParams("参数解码失败: %v", err)
			}
		}
		if validate != nil {
			if err := validate(in); err != nil {
				return nil, err
			}
		}

		out, err := run(ctx, in)
		if err != nil {
			result := &mcp.CallToolResult{}
			result.SetError(err)
			return result, nil
		}
		data, err := json.Marshal(out)
		if err != nil {
			return nil, fmt.Errorf("序列化工具出参失败: %w", err)
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
			StructuredContent: json.RawMessage(data),
		}, nil
	})
}

// listValidationSchema 生成 list_tasks 的协议校验副本：仅去掉 limit/offset
// 的数值边界（minimum/maximum），保留类型与其它约束；advertise 仍用原文。
func listValidationSchema() string {
	var schema map[string]any
	if err := json.Unmarshal([]byte(listTasksSchema), &schema); err != nil {
		panic(fmt.Errorf("解析 list_tasks schema 失败: %w", err))
	}
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"limit", "offset"} {
		property := properties[name].(map[string]any)
		delete(property, "minimum")
		delete(property, "maximum")
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("序列化 list_tasks 校验 schema 失败: %w", err))
	}
	return string(data)
}

// businessError 把 store 的错误映射为中文工具错误；找不到 id 的错误已由
// store 提供“未找到任务 {id}”，原文返回以免重复包装。
func businessError(action string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return err
	}
	return fmt.Errorf("%s: %w", action, err)
}

// registerTools 注册全部工具：design.md §4 的五个 + 2026-11-26 归档两个
// （archive_task / unarchive_task），其后是字段/文件/图片工具。
func registerTools(server *mcp.Server, repo *store.TaskRepo) {
	addTypedTool(server, "create_task", "创建一条待办任务", createTaskSchema, createTaskSchema,
		func(in createInput) error {
			if blankTitle(in.Title) {
				return invalidParams("任务标题不能为空白")
			}
			if tooLong(in.Title, 500) {
				return invalidParams("任务标题不能超过 500 字符")
			}
			if in.Description != nil && tooLong(*in.Description, 5000) {
				return invalidParams("任务详情不能超过 5000 字符")
			}
			if in.DueAt != nil {
				if _, err := parseRFC3339(*in.DueAt); err != nil {
					return invalidParams("到期时间必须是合法 RFC3339: %v", err)
				}
			}
			return nil
		},
		func(_ context.Context, in createInput) (taskOut, error) {
			var dueAt *int64
			if in.DueAt != nil {
				millis, err := parseRFC3339(*in.DueAt)
				if err != nil {
					return taskOut{}, invalidParams("到期时间必须是合法 RFC3339: %v", err)
				}
				dueAt = &millis
			}
			task, err := repo.Create(store.TaskInput{
				Title:       in.Title,
				Description: in.Description,
				Priority:    in.Priority,
				DueAt:       dueAt,
				Values:      in.Values,
			})
			if err != nil {
				return taskOut{}, businessError("创建任务失败", err)
			}
			return newTaskOut(task), nil
		})

	addTypedTool(server, "list_tasks",
		"查询任务列表：支持状态/优先级筛选、任意字段筛选（filter）、按字段排序（sort）、按字段分组（group_by）。无 filter/sort/group_by 时行为与固定排序一致（未完成在前、到期升序无到期排后、优先级降序）",
		listTasksSchema,
		listValidationSchema(),
		nil,
		func(_ context.Context, in listInput) (listOut, error) {
			query := toListQuery(in)
			result, err := repo.Query(query)
			if err != nil {
				return listOut{}, businessError("查询任务失败", err)
			}
			return resultToOut(result, query), nil
		})
	addTypedTool(server, "update_task", "部分更新任务：只修改出现的字段，未出现的字段保持原值", updateTaskSchema, updateTaskSchema,
		func(in updateInput) error {
			patch := in.Patch
			if patch.Title != nil {
				if *patch.Title == nil {
					return invalidParams("任务标题不能为 null")
				}
				if blankTitle(**patch.Title) {
					return invalidParams("任务标题不能为空白")
				}
				if tooLong(**patch.Title, 500) {
					return invalidParams("任务标题不能超过 500 字符")
				}
			}
			if patch.Description != nil && *patch.Description != nil && tooLong(**patch.Description, 5000) {
				return invalidParams("任务详情不能超过 5000 字符")
			}
			if patch.Priority != nil {
				if *patch.Priority == nil {
					return invalidParams("优先级不能为 null")
				}
				priority := **patch.Priority
				if priority < 0 || priority > 3 {
					return invalidParams("优先级必须是 0、1、2、3 之一")
				}
			}
			if patch.DueAt != nil && *patch.DueAt != nil {
				if _, err := parseRFC3339(**patch.DueAt); err != nil {
					return invalidParams("到期时间必须是合法 RFC3339: %v", err)
				}
			}
			return nil
		},
		func(_ context.Context, in updateInput) (taskOut, error) {
			task, err := repo.Update(in.ID, in.Patch)
			if err != nil {
				return taskOut{}, businessError("更新任务失败", err)
			}
			return newTaskOut(task), nil
		})

	addTypedTool(server, "complete_task", "将任务标记为已完成（status=1，记录 completed_at）",
		idSchema,
		idSchema,
		nil,
		func(_ context.Context, in idInput) (taskOut, error) {
			task, err := repo.Complete(in.ID)
			if err != nil {
				return taskOut{}, businessError("完成任务失败", err)
			}
			return newTaskOut(task), nil
		})

	addTypedTool(server, "archive_task",
		"归档一条已完成的任务（归档后从常规列表隐去，进入已归档视图；幂等，重复调用不重写时间戳）",
		idSchema,
		idSchema,
		nil,
		func(_ context.Context, in idInput) (taskOut, error) {
			task, err := repo.Archive(in.ID)
			if err != nil {
				return taskOut{}, businessError("归档任务失败", err)
			}
			if _, err := repo.GenerateArchiveDoc(); err != nil {
				return taskOut{}, businessError("生成归档知识文档失败", err)
			}
			return newTaskOut(task), nil
		})

	addTypedTool(server, "unarchive_task",
		"取消归档：任务回到已完成列表（幂等）",
		idSchema,
		idSchema,
		nil,
		func(_ context.Context, in idInput) (taskOut, error) {
			task, err := repo.Unarchive(in.ID)
			if err != nil {
				return taskOut{}, businessError("取消归档失败", err)
			}
			if _, err := repo.GenerateArchiveDoc(); err != nil {
				return taskOut{}, businessError("生成归档知识文档失败", err)
			}
			return newTaskOut(task), nil
		})

	addTypedTool(server, "remove_task", "删除任务（物理删除，不可恢复）",
		idSchema,
		idSchema,
		nil,
		func(_ context.Context, in idInput) (taskOut, error) {
			task, err := repo.Remove(in.ID)
			if err != nil {
				return taskOut{}, businessError("删除任务失败", err)
			}
			return newTaskOut(task), nil
		},
	)

	registerFieldTools(server, repo)
	registerFileTools(server, repo)
	registerImageTools(server, repo)
}
