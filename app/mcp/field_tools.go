// 自定义字段的四个 MCP 工具（create_field / list_fields / update_field /
// remove_field）与 list_tasks 的字段筛选/排序/分组透传。
// 校验走 addTypedTool 的 validate 钩子 + store 层权威校验；错误的出口与
// 既有五个工具一致（invalid_params / SetError）。
package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"todo-mcp/app/store"
)

// createFieldSchema 对应 create_field 入参。
const createFieldSchema = `{
  "type": "object",
  "properties": {
    "name":      { "type": "string", "minLength": 1, "maxLength": 50, "description": "字段名称（必填，全局唯一）" },
    "type":      { "type": "string", "enum": ["text", "single_select", "multi_select", "number", "date"], "description": "字段类型（创建后不可变）" },
    "display":   { "type": "string", "enum": ["plain", "percent", "currency", "progress"], "default": "plain", "description": "number 字段的显示样式：plain/percent/currency/progress" },
    "options":   { "type": "array", "items": { "type": "string", "maxLength": 50 }, "maxItems": 100, "description": "选择类字段的选项列表（single_select/multi_select 必填）" },
    "position":  { "type": "integer", "default": 0, "description": "显示顺序（0=自动追加到末尾）" }
  },
  "required": ["name", "type"],
  "additionalProperties": false
}`

// listFieldsSchema 对应 list_fields 入参（无参数）。
const listFieldsSchema = `{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`

// updateFieldSchema 对应 update_field 入参。
const updateFieldSchema = `{
  "type": "object",
  "properties": {
    "id":       { "type": "string", "description": "字段 id（必填）" },
    "name":     { "type": "string", "minLength": 1, "maxLength": 50, "description": "新字段名（可选）" },
    "display":  { "type": "string", "enum": ["plain", "percent", "currency", "progress"], "description": "number 字段的新显示样式（可选）" },
    "options":  { "type": "array", "items": { "type": "string", "maxLength": 50 }, "maxItems": 100, "description": "选择类字段的新选项（可选，全量替换）" },
    "position": { "type": "integer", "description": "新显示顺序（可选）" }
  },
  "required": ["id"],
  "additionalProperties": false
}`

// fieldIDInputSchema 对应 remove_field 入参。
const fieldIDInputSchema = `{
  "type": "object",
  "properties": { "id": { "type": "string", "description": "字段 id（必填）" } },
  "required": ["id"],
  "additionalProperties": false
}`

// 常见错误映射（与 businessError 同构的字段版）。
func fieldBusinessError(action string, err error) error {
	return businessError(action, err)
}

// registerFieldTools 注册四个字段管理工具。
func registerFieldTools(server *mcp.Server, repo *store.TaskRepo) {
	addTypedTool(server, "create_field", "创建自定义字段（类型创建后不可变）",
		createFieldSchema, createFieldSchema,
		func(in createFieldInput) error {
			if blankTitle(in.Name) {
				return invalidParams("字段名称不能为空白")
			}
			if tooLong(in.Name, store.MaxFieldNameLen) {
				return invalidParams("字段名称不能超过 %d 字符", store.MaxFieldNameLen)
			}
			switch in.Type {
			case "text", "date":
			case "single_select", "multi_select":
				if len(in.Options) == 0 {
					return invalidParams("选择类字段至少需要一个选项")
				}
				for _, option := range in.Options {
					if blankTitle(option) {
						return invalidParams("选项不能为空白")
					}
					if tooLong(option, store.MaxOptionLen) {
						return invalidParams("选项不能超过 %d 字符", store.MaxOptionLen)
					}
				}
				if len(in.Options) > store.MaxOptions {
					return invalidParams("选项数量不能超过 %d", store.MaxOptions)
				}
			case "number":
				if in.Display != nil {
					switch *in.Display {
					case "plain", "percent", "currency", "progress":
					default:
						return invalidParams("display 必须是 plain/percent/currency/progress 之一")
					}
				}
			default:
				return invalidParams("type 必须是 text/single_select/multi_select/number/date 之一")
			}
			return nil
		},
		func(_ context.Context, in createFieldInput) (fieldOut, error) {
			display := store.DisplayPlain
			if in.Display != nil {
				display = store.FieldDisplay(*in.Display)
			}
			field, err := repo.CreateField(store.FieldInput{
				Name:     in.Name,
				Type:     store.FieldType(in.Type),
				Display:  display,
				Options:  in.Options,
				Position: derefInt(in.Position),
			})
			if err != nil {
				return fieldOut{}, fieldBusinessError("创建字段失败", err)
			}
			return newFieldOut(field, false), nil
		})

	addTypedTool(server, "list_fields", "列出全部字段（内置 + 自定义；内置字段标记 builtin=true 不可删改）",
		listFieldsSchema, listFieldsSchema,
		nil,
		func(_ context.Context, _ struct{}) (listFieldsOut, error) {
			fields, err := repo.ListFields()
			if err != nil {
				return listFieldsOut{}, fieldBusinessError("查询字段失败", err)
			}
			return listFieldsOut{Fields: newFieldOuts(fields)}, nil
		})

	addTypedTool(server, "update_field", "部分更新字段：只修改出现的字段（name/display/options/position），类型不可变",
		updateFieldSchema, updateFieldSchema,
		func(in updateFieldInput) error {
			if in.Name != nil && blankTitle(*in.Name) {
				return invalidParams("字段名称不能为空白")
			}
			if in.Name != nil && tooLong(*in.Name, store.MaxFieldNameLen) {
				return invalidParams("字段名称不能超过 %d 字符", store.MaxFieldNameLen)
			}
			if in.Display != nil {
				switch *in.Display {
				case "plain", "percent", "currency", "progress":
				default:
					return invalidParams("display 必须是 plain/percent/currency/progress 之一")
				}
			}
			if in.Position != nil && *in.Position < 0 {
				return invalidParams("position 不能为负")
			}
			return nil
		},
		func(_ context.Context, in updateFieldInput) (fieldOut, error) {
			patch := store.FieldPatch{}
			if in.Name != nil {
				trimmed := *in.Name
				patch.Name = new(new(trimmed))
			}
			if in.Display != nil {
				display := store.FieldDisplay(*in.Display)
				patch.Display = new(new(display))
			}
			if in.Options != nil {
				options := in.Options
				patch.Options = new(new(options))
			}
			if in.Position != nil {
				position := *in.Position
				patch.Position = new(new(position))
			}
			field, err := repo.UpdateField(in.ID, patch)
			if err != nil {
				return fieldOut{}, fieldBusinessError("更新字段失败", err)
			}
			return newFieldOut(field, false), nil
		})

	addTypedTool(server, "remove_field", "删除自定义字段（级联删除全部字段值，内置字段不可删）",
		fieldIDInputSchema, fieldIDInputSchema,
		nil,
		func(_ context.Context, in idInput) (fieldOut, error) {
			field, err := repo.RemoveField(in.ID)
			if err != nil {
				return fieldOut{}, fieldBusinessError("删除字段失败", err)
			}
			return newFieldOut(field, false), nil
		})
}

// derefInt 解引用 int 指针；nil → 0。
func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// toListQuery 把 list_tasks 的入参转换为 store.ListQuery（含 limit/offset 钳制）。
func toListQuery(in listInput) store.ListQuery {
	query := store.ListQuery{
		Status:   in.Status,
		Priority: in.Priority,
		Filters:  in.Filter,
		Sort:     in.Sort,
		Limit:    50,
	}
	if in.Limit != nil {
		query.Limit = *in.Limit
	}
	query.Limit = clampInt(query.Limit, 1, maxLimit)
	if in.Offset != nil {
		query.Offset = *in.Offset
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	if in.GroupBy != nil {
		query.GroupBy = *in.GroupBy
	}
	return query
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// resultToOut 把 store.ListResult 转出参；分组模式 Tasks 置空、Groups 填值。
func resultToOut(result *store.ListResult, query store.ListQuery) listOut {
	out := listOut{
		Total:  result.Total,
		Limit:  query.Limit,
		Offset: query.Offset,
		Tasks:  newTaskOuts(result.Tasks),
	}
	if result.Groups != nil {
		groups := make([]groupOut, 0, len(result.Groups))
		for _, group := range result.Groups {
			groups = append(groups, groupOut{
				Key:   group.Key,
				Label: group.Label,
				Total: len(group.Tasks),
				Tasks: newTaskOuts(group.Tasks),
			})
		}
		out.Groups = groups
		out.Tasks = []taskOut{}
	}
	return out
}