# 需求：自定义字段 + 按字段筛选 / 分组 / 排序

> 状态：**已完成**（2026-11-24，全链路 store → MCP → GUI 实现并测试通过）。
> 参考交互图：用户提供的字段配置面板（字段名称、AI 字段入口、字段类型网格：文本/单选/多选/数字/百分数/货币/进度/日期）。

## 实现产物（2026-11-24）

- store：`app/store/migrate.go`（v2 EAV 表）、`field.go`（字段 CRUD）、`value.go`（值解析/聚合/分组）、`query.go`（筛选/排序/分组查询）；schema `user_version=2`。
- MCP：`app/mcp/field_tools.go`（4 个新工具）、`dto.go`（taskOut.values / listOut.groups）、`tools.go`（create/update_task 的 values、list_tasks 的 filter/sort/group_by）；共 **9 个工具**。
- GUI：`app/gui/fields_model.go`、`page.go`；字段管理弹窗（8 类网格）、行内值显示、筛选/排序/分组工具栏；列表用 Viva 原生 Header 渲染组头。
- 测试：`app/store/field_test.go`、`app/mcp/fields_test.go`、`app/gui/fields_test.go`；`go test ./...` 全绿。

**协议键决策（实施中确定）**：`values` / `filter.field` / `sort.field` / `group_by` 的键统一用**字段名**（内置字段名 + 自定义字段名，全局唯一），而非 UUID —— 对 agent 和 GUI 都更直观；内置字段名（title/priority/due_at/status/created_at）为保留键。
## 1. 需求要点

给 todo-mcp（Viva GUI + MCP stdio 双模式）增加多维表格式的自定义字段能力：

1. **自定义新增字段**：用户定义字段（名称 + 类型），任务可填写字段值。
2. **按字段筛选**：列表按任意字段条件过滤。
3. **分组**：列表按某字段分组展示（组内维持排序）。
4. **排序**：按字段升/降序；未指定时回退现有固定排序（status ASC, due_at NULLS FIRST, due_at ASC, priority DESC, id ASC），保证现有行为不破坏。

## 2. 现状约束（已确认）

- 存储：SQLite 单文件（modernc.org/sqlite 纯 Go 驱动），`tasks` 表 v1 schema，`PRAGMA user_version` 门闸迁移，迁移幂等。
- MCP：5 工具（create_task / list_tasks / update_task / complete_task / remove_task），出参时间 UTC RFC3339。
- GUI：快捷新建、三 Tab 筛选（全部/进行中/已完成）、行内编辑、删除二次确认；单实例 + 托盘。
- 分页：单页上限 200，GUI 全量翻页。

## 3. API 面（已定）

- 新 MCP 工具：`create_field` / `list_fields` / `update_field` / `remove_field`（命名沿用现有动词_名词惯例）。
- `create_task` / `update_task` / `taskOut` 增加 `values`（对象键=字段 id）；三态语义与 TaskPatch 一致（键缺席=不动、显式 null=清值、有值=设置）。
- `list_tasks` 增加可选参数：`filter`（[{field, op, value}] AND 语义）、`sort`（[{field, order}]）、`group_by`（字段键）；缺省行为与现状完全一致（向后兼容，现有 agent 不受影响）。
- `list_fields` 同时返回内置字段（title/priority/due_at/status/created_at，builtin 标记、不可删改）与自定义字段，使筛选/排序/分组在 agent 视角「一切皆字段」。
- 筛选 op 按类型定义：文本 eq/contains；数字类 eq/gt/lt；日期 before/after；单选 is；多选 has；通用 empty/not_empty。
- GUI：字段管理入口 + 字段编辑弹窗（仿参考图：名称 + 8 类类型网格）、行内显示字段值、工具栏筛选/分组/排序控件。

## 4. 已定决策（2026-11-24 用户确认）

1. **存储 = EAV 值表**：`fields` 元数据表 + `task_field_values(task_id, field_id, text_value, num_value)`。
   列映射：text/single_select → text_value；multi_select → text_value 存 JSON 数组；number → num_value；date → num_value 存 epoch 毫秒（与 due_at 口径一致，排序天然正确）。
2. **类型 = 8 类全做**：text、single_select、multi_select、number、date 为 5 个存储类型；百分数/货币/进度为 number 的 `display` 属性（plain/percent/currency/progress），仅影响渲染。
3. **AI 字段排除**：无 LLM 接入，不做假占位。
4. **范围 = 全链路一次做完**：store → MCP → GUI，含测试，每步保持绿。

### 4.1 随决策确定的细则

- 字段类型创建后不可变（避免值迁移）；`update_field` 仅允许改 name / display（number）/ options（选择类）/ position。
- `remove_field` 物理删除字段并级联物理删值（与 tasks 的物理删除口径一致）。
- 选择类 `options` 全量替换；已存值若不在新选项集中保留原值不清理（飞书口径）。
- `multi_select` 不作为排序/分组键（值多义），仅支持筛选 has/not_has；`group_by` 对单选无此限制。
- `group_by` 时返回 groups 数组（key/label/total + 组内 tasks），tasks 平铺为空；上限 1000 行，此时 limit/offset 不生效（文档注明）。
- 值校验：单选值必须在 options 内；多选值数组每项必须在 options 内；number 接受 JSON number；date 入参 RFC3339 归一化毫秒。
- 上限：字段 20 个、字段名 50 字符、选项 100 个/每个 50 字符、文本值 500 字符、filter 条件 10 条、sort 键 3 个。

## 5. 未决问题

（无）