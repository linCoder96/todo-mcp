// 字段 CRUD、自定义值读写、增强查询（筛选/排序/分组）的测试。
package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestField 创建选择类字段（选项固定两用：单选/多选）。
func newSelectField(t *testing.T, repo *TaskRepo, name string, multi bool) *Field {
	t.Helper()
	typ := FieldTypeSingleSelect
	if multi {
		typ = FieldTypeMultiSelect
	}
	field, err := repo.CreateField(FieldInput{
		Name:    name,
		Type:    typ,
		Options: []string{"红", "绿", "蓝"},
	})
	if err != nil {
		t.Fatalf("创建字段失败: %v", err)
	}
	return field
}

func TestFieldCRUDValidationAndLimits(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	// 创建文本字段
	text, err := repo.CreateField(FieldInput{Name: "备注", Type: FieldTypeText})
	if err != nil || text.ID == "" || text.Type != FieldTypeText || text.Display != DisplayPlain {
		t.Fatalf("创建文本字段失败: %+v err=%v", text, err)
	}

	// 数字字段 display
	num, err := repo.CreateField(FieldInput{Name: "进度", Type: FieldTypeNumber, Display: DisplayPercent})
	if err != nil || num.Display != DisplayPercent {
		t.Fatalf("创建数字字段失败: %+v err=%v", num, err)
	}

	// 单选/多选 options 校验
	if _, err := repo.CreateField(FieldInput{Name: "空选项", Type: FieldTypeSingleSelect, Options: nil}); err == nil {
		t.Fatal("单选字段无选项应拒绝")
	}
	if _, err := repo.CreateField(FieldInput{Name: "坏选项", Type: FieldTypeMultiSelect, Options: []string{"", "x"}}); err == nil {
		t.Fatal("含空白选项应拒绝")
	}

	// 重名拒绝
	if _, err := repo.CreateField(FieldInput{Name: "备注", Type: FieldTypeText}); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重名应拒绝，实际 %v", err)
	}

	// 内置字段名冲突
	if _, err := repo.CreateField(FieldInput{Name: "title", Type: FieldTypeText}); err == nil || !strings.Contains(err.Error(), "内置字段") {
		t.Fatalf("内置字段名冲突应拒绝，实际 %v", err)
	}

	// 删除：级联验证
	selectField := newSelectField(t, repo, "级联字段", false)
	task, err := repo.Create(TaskInput{
		Title:  "待删字段任务",
		Values: map[string]json.RawMessage{selectField.Name: json.RawMessage(`"红"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RemoveField(selectField.ID); err != nil {
		t.Fatalf("删除字段失败: %v", err)
	}
	reloaded, err := repo.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Values[selectField.Name]; ok {
		t.Fatal("删除字段后任务值应级联清空")
	}
	// 内置字段不可删
	if _, err := repo.RemoveField("title"); err == nil || !strings.Contains(err.Error(), "内置字段") {
		t.Fatalf("内置字段删除应拒绝，实际 %v", err)
	}
	// 不存在的字段
	if _, err := repo.RemoveField("ghost-field-id"); err == nil || !strings.Contains(err.Error(), "未找到字段") {
		t.Fatalf("删不存在字段应报未找到，实际 %v", err)
	}
	// 上限：当前已有 2 个字段（备注、进度），position 自动 = max+1（从 3 起）。
	// 创建到第 MaxFields 个时到达上限。
	position := 3
	for position <= MaxFields {
		field, err := repo.CreateField(FieldInput{
			Name: "字段" + string(rune('A'+position)),
			Type: FieldTypeText,
		})
		if err != nil {
			t.Fatalf("批量创建字段失败: %v", err)
		}
		if field.Position != position {
			t.Fatalf("position 应自动递增: got %d want %d", field.Position, position)
		}
		position++
	}
	// 此时已有 MaxFields 个，超限拒绝
	if _, err := repo.CreateField(FieldInput{Name: "超限字段", Type: FieldTypeText}); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超上限应拒绝，实际 %v", err)
	}

	// 更新：改名、改 display、改 options、改 position
	updated, err := repo.UpdateField(text.ID, FieldPatch{
		Name:     new(new("备注2")),
		Display:  new(new(DisplayPlain)),
		Position: new(new(5)),
	})
	if err != nil || updated.Name != "备注2" || updated.Position != 5 {
		t.Fatalf("更新字段失败: %+v err=%v", updated, err)
	}
	// 改回原名「备注」应成功（此时该名已释放）；改成另一存在字段名「进度」应拒绝。
	if _, err := repo.UpdateField(text.ID, FieldPatch{Name: new(new("备注"))}); err != nil {
		t.Fatalf("改回已释放的原名应成功: %v", err)
	}
	if _, err := repo.UpdateField(text.ID, FieldPatch{Name: new(new("进度"))}); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("改成已有字段名应拒绝，实际 %v", err)
	}

	// 非选择类不能改 options
	if _, err := repo.UpdateField(num.ID, FieldPatch{Options: new(new([]string{"a"}))}); err == nil {
		t.Fatal("非选择类修改 options 应拒绝")
	}

}

func TestTaskValuesRoundTripAndValidation(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	textField, err := repo.CreateField(FieldInput{Name: "备注", Type: FieldTypeText})
	if err != nil {
		t.Fatal(err)
	}
	numField, err := repo.CreateField(FieldInput{Name: "进度", Type: FieldTypeNumber, Display: DisplayPercent})
	if err != nil {
		t.Fatal(err)
	}
	dateField, err := repo.CreateField(FieldInput{Name: "交付日", Type: FieldTypeDate})
	if err != nil {
		t.Fatal(err)
	}
	singleField := newSelectField(t, repo, "优先级选择", false)
	multiField := newSelectField(t, repo, "标签", true)

	// 各类型值写入（协议形状）
	created, err := repo.Create(TaskInput{
		Title: "带字段任务",
		Values: map[string]json.RawMessage{
			textField.Name:   json.RawMessage(`"详细备注"`),
			numField.Name:    json.RawMessage(`87.5`),
			dateField.Name:   json.RawMessage(`"2026-12-01T00:00:00Z"`),
			singleField.Name: json.RawMessage(`"绿"`),
			multiField.Name:  json.RawMessage(`["红","蓝","红"]`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 读出：数值、日期归一化、多选去重
	reloaded, err := repo.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.Values[textField.Name]) != `"详细备注"` {
		t.Fatalf("文本值错误: %s", reloaded.Values[textField.Name])
	}
	if string(reloaded.Values[numField.Name]) != `87.5` {
		t.Fatalf("数值错误: %s", reloaded.Values[numField.Name])
	}
	var dateStr string
	if err := json.Unmarshal(reloaded.Values[dateField.Name], &dateStr); err != nil || dateStr != "2026-12-01T00:00:00Z" {
		t.Fatalf("日期值错误: %s err=%v", reloaded.Values[dateField.Name], err)
	}
	var multi []string
	if err := json.Unmarshal(reloaded.Values[multiField.Name], &multi); err != nil || len(multi) != 2 || multi[0] != "红" || multi[1] != "蓝" {
		t.Fatalf("多选去重错误: %v err=%v", multi, err)
	}

	// 非法值校验
	badValues := map[string]json.RawMessage{
		singleField.Name: json.RawMessage(`"黄"`), // 不在选项
	}
	if _, err := repo.Create(TaskInput{Title: "坏值", Values: badValues}); err == nil || !strings.Contains(err.Error(), "不在可选集") {
		t.Fatalf("非法单选值应拒绝，实际 %v", err)
	}
	if _, err := repo.Create(TaskInput{Title: "坏日期", Values: map[string]json.RawMessage{dateField.Name: json.RawMessage(`"2026-13-40"`)}}); err == nil {
		t.Fatal("非法日期应拒绝")
	}
	if _, err := repo.Create(TaskInput{Title: "不存在字段", Values: map[string]json.RawMessage{"no-such": json.RawMessage(`"x"`)}}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("不存在的字段 id 应拒绝，实际 %v", err)
	}
}

func TestTaskUpdateValuesThreeStates(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	textField, err := repo.CreateField(FieldInput{Name: "备注", Type: FieldTypeText})
	if err != nil {
		t.Fatal(err)
	}
	singleField := newSelectField(t, repo, "级别", false)

	created, err := repo.Create(TaskInput{
		Title: "三态任务",
		Values: map[string]json.RawMessage{
			textField.Name:   json.RawMessage(`"原备注"`),
			singleField.Name: json.RawMessage(`"红"`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 缺席 → 不动
	var absent TaskPatch
	if err := json.Unmarshal([]byte(`{"title":"改标题"}`), &absent); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Update(created.ID, absent)
	if err != nil || string(after.Values[textField.Name]) != `"原备注"` {
		t.Fatalf("替换标题后字段值应保留: %+v err=%v", after, err)
	}

	// JSON 显式 null 清空单字段
	var clearOne TaskPatch
	if err := json.Unmarshal([]byte(`{"values":{"`+textField.Name+`":null}}`), &clearOne); err != nil {
		t.Fatal(err)
	}
	after, err = repo.Update(created.ID, clearOne)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Values[textField.Name]; ok {
		t.Fatal("显式 null 应清空字段值")
	}

	// 整个 values = null 清空全部
	var clearAll TaskPatch
	if err := json.Unmarshal([]byte(`{"values":null}`), &clearAll); err != nil {
		t.Fatal(err)
	}
	after, err = repo.Update(created.ID, clearAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Values) != 0 {
		t.Fatalf("values=null 应清空全部: %+v", after.Values)
	}

	// 有值 → 设置
	var setValue TaskPatch
	if err := json.Unmarshal([]byte(`{"values":{"`+singleField.Name+`":"蓝"}}`), &setValue); err != nil {
		t.Fatal(err)
	}
	after, err = repo.Update(created.ID, setValue)
	if err != nil || string(after.Values[singleField.Name]) != `"蓝"` {
		t.Fatalf("设置字段值失败: %+v err=%v", after, err)
	}
}

func TestQueryFilterSortGroup(t *testing.T) {
	db, _ := newTestDB(t)
	repo := NewTaskRepo(db)

	numField, err := repo.CreateField(FieldInput{Name: "评分", Type: FieldTypeNumber})
	if err != nil {
		t.Fatal(err)
	}
	selectField := newSelectField(t, repo, "分类", false)

	// 造 4 条任务：评分 3/8/5/无；分类 红/绿/红/无
	seeds := []struct {
		title string
		num   string // JSON 数字或空（无值）
		sel   string // JSON 字符串或空
	}{
		{"A-红3", `3`, `"红"`},
		{"B-绿8", `8`, `"绿"`},
		{"C-红5", `5`, `"红"`},
		{"D-无值", ``, ``},
	}
	for _, seed := range seeds {
		values := map[string]json.RawMessage{}
		if seed.num != "" {
			values[numField.Name] = json.RawMessage(seed.num)
		}
		if seed.sel != "" {
			values[selectField.Name] = json.RawMessage(seed.sel)
		}
		if _, err := repo.Create(TaskInput{Title: seed.title, Values: values}); err != nil {
			t.Fatal(err)
		}
	}

	// 数字筛选 gt/lt
	res, err := repo.Query(ListQuery{Filters: []FilterCond{{Field: numField.Name, Op: OpGt, Value: json.RawMessage(`4`)}}})
	if err != nil || res.Total != 2 {
		t.Fatalf("gt 筛选失败: total=%d err=%v", res.Total, err)
	}

	// 单选 is 筛选
	res, err = repo.Query(ListQuery{Filters: []FilterCond{{Field: selectField.Name, Op: OpIs, Value: json.RawMessage(`"红"`)}}})
	if err != nil || res.Total != 2 {
		t.Fatalf("is 筛选失败: total=%d err=%v", res.Total, err)
	}

	// AND 复合：分类=红 且 评分>4
	res, err = repo.Query(ListQuery{Filters: []FilterCond{
		{Field: selectField.Name, Op: OpIs, Value: json.RawMessage(`"红"`)},
		{Field: numField.Name, Op: OpGt, Value: json.RawMessage(`4`)},
	}})
	if err != nil || res.Total != 1 || res.Tasks[0].Title != "C-红5" {
		t.Fatalf("AND 筛选失败: total=%d, first=%q, err=%v", res.Total, res.Tasks[0].Title, err)
	}

	// not_empty 筛选
	res, err = repo.Query(ListQuery{Filters: []FilterCond{{Field: numField.Name, Op: OpNotEmpty}}})
	if err != nil || res.Total != 3 {
		t.Fatalf("not_empty 筛选失败: total=%d err=%v", res.Total, err)
	}

	// 数字字段排序 desc（无值 last）
	res, err = repo.Query(ListQuery{Sort: []SortKey{{Field: numField.Name, Desc: true}}, Limit: 200})
	if err != nil || len(res.Tasks) != 4 {
		t.Fatalf("排序查询失败: %d err=%v", len(res.Tasks), err)
	}
	wantDesc := []string{"B-绿8", "C-红5", "A-红3", "D-无值"}
	for i, title := range wantDesc {
		if res.Tasks[i].Title != title {
			t.Fatalf("降序错误: %q 应为 %q", res.Tasks[i].Title, title)
		}
	}

	// 分组模式：按单选分组
	res, err = repo.Query(ListQuery{GroupBy: selectField.Name})
	if err != nil || res.Groups == nil {
		t.Fatalf("分组查询失败: %+v err=%v", res, err)
	}
	if len(res.Groups) != 3 {
		t.Fatalf("应分 3 组，实际 %d", len(res.Groups))
	}
	// 组序 = 按 id 首次出现（默认排序下有值在前）
	if res.Groups[0].Label != "红" || len(res.Groups[0].Tasks) != 2 {
		t.Fatalf("红组错误: %+v", res.Groups[0])
	}
	if res.Groups[2].Label != "未设置" {
		t.Fatalf("未设置组应排最后: %+v", res.Groups)
	}

	// 多选不可排序/分组
	multiField := newSelectField(t, repo, "多选字段", true)
	if _, err := repo.Query(ListQuery{GroupBy: multiField.Name}); err == nil {
		t.Fatal("多选分组应拒绝")
	}
	if _, err := repo.Query(ListQuery{Sort: []SortKey{{Field: multiField.Name}}}); err == nil {
		t.Fatal("多选排序应拒绝")
	}

	// 内置字段筛选/排序
	res, err = repo.Query(ListQuery{Filters: []FilterCond{{Field: "title", Op: OpContains, Value: json.RawMessage(`"红"`)}}})
	if err != nil || res.Total != 2 {
		t.Fatalf("内置标题 contains 筛选失败: total=%d err=%v", res.Total, err)
	}

	// 不存在的字段
	if _, err := repo.Query(ListQuery{Filters: []FilterCond{{Field: "nope", Op: OpEq, Value: json.RawMessage(`1`)}}}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("不存在字段筛选应拒绝，实际 %v", err)
	}
}
