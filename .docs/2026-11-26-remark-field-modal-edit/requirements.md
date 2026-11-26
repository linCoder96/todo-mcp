# 固定字段「备注」+ 编辑弹窗化

日期：2026-11-26　状态：已实现

## 实现记录（2026-11-26 完成）

- `store/builtinFields`、`builtinColumn`、`mcp/builtinFieldMeta`、`gui/builtinFieldKeys`、
  `fieldKeyLabel`、`fieldTypeOf`、MCP `list_tasks` schema 一并接入 `description` =「备注」
  （文本类型），筛选/排序/分组面板自动获得该维度。
- GUI 表格新增「备注」列（默认显示，可在列设置隐藏；默认宽 160）。
- 备注单元格：整格点击原地编辑（复刻自定义字段文本单元格路径，`beginRemarkCellEdit` /
  `commitRemarkCell`），回车提交、清空提交 = 清空 description、Esc 取消。
- 编辑改为弹窗：点标题打开 Modal「编辑任务」，字段集 = 标题 + 备注 + 优先级/到期 +
  自定义字段值；保存/取消关闭，Esc/遮罩关闭后 `editingID` 一并清空；行内 `editorRow`
  已删除。
- 编辑弹窗与单元格编辑互斥；任务被删（外部 MCP 并发）时 refresh 自动收起编辑态。
- 测试：新增 `TestRemarkColumnCellEdit`、`TestEditModalRemark`；原行编辑器测试
  `TestRowEditorClearsFieldValue` 的自定义字段改名避让固定「备注」列撞名。
- 验证：`go vet` / `viva vet` 无告警；`store`/`mcp` 全绿；`gui` 仅 3 个图片功能基线失败
  （改动前已存在）；headless 画面确认备注列渲染正常。

## 需求一：添加固定字段「备注」

- 协议已有 `description`（tasks 表列、create/update 均支持），GUI 无展示/编辑入口。
- 方案：把 `description` 作为**固定字段**接入现有字段体系，而不是新建自定义字段：
  - `store.builtinFields` 增加 `{ID: "description", Name: "备注", Type: FieldTypeText}`；`builtinColumn` 映射到 tasks.description。
  - GUI 表格加「备注」列（可隐藏/调宽，同优先级列待遇）；单元格点击原地编辑（复用 cell_edit 文本路径）。
  - 行内编辑器/弹窗编辑器里加备注输入（TextArea）。
  - 筛选/排序自然获得备注维度（builtinColumn 已支持 contains）。
- 不改表结构（列已存在）；MCP 协议不变。

## 需求二：待办事项编辑改为弹窗展示

- 现状：点标题 → 行内展开编辑器（editorRow：标题+优先级+到期+自定义字段）。
- 改为：点标题 → 打开编辑弹窗（Modal），内容沿用 editorRow 的字段集。
- 弹窗 vs 抽屉：选 Modal（与字段管理弹窗一致；编辑是短事务，抽屉留给详情/图片）。
- 关闭联动：保存/取消关闭；Esc 由 Modal 自带；tab 切换收起编辑的逻辑移到关闭回调。

## 未决问题（请用户确认）

1. 备注列默认显示还是默认隐藏？
2. 行内编辑完全移除，还是保留（如双击）？ → 拟完全移除，编辑唯一入口=弹窗
3. 备注是否要进「时间」列旁的固定列序列（列设置里允许隐藏）？
## 共识（2026-11-26 用户确认）

1. 备注列默认**显示**（可在列设置隐藏，同优先级列待遇）。
2. 行内编辑**完全移除**，编辑唯一入口 = 弹窗（点标题打开）。
3. 弹窗里备注用**单行 TextField**（与其它字段一致）。
