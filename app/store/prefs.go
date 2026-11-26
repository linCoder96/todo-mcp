// 视图偏好持久化（2026-11-26）：筛选条件、排序、分组、状态 tab 的保存与恢复。
//
// 单行表（id = 1，单用户桌面应用），JSON 列承载结构化偏好；写侧全量覆盖
// （UPSERT），读侧行缺失 = 无偏好（返回零值，由调用方用默认值兜底）。
// FilterCond 的 Value 是协议形状 JSON，原样内嵌进 prefs JSON，无二次编码。
//
// 与字段删除的联动：偏好里引用的字段被删除后，字段名查询会报「字段不存在」，
// 因此 LoadViewPrefs 会把指向已删字段的筛选条件剔除、排序/分组复位，
// 避免重启后页面直接报错（GUI 侧无需再做兼容分支）。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// ViewPrefs 是任务清单视图的用户偏好（GUI 内存态 filterConds/sortField/
// sortDesc/groupBy/tab 的持久化镜像）。
type ViewPrefs struct {
	StatusTab string       `json:"status_tab"` // "" = 全部；其余为 filterTabs 文案
	Conds     []FilterCond `json:"conds"`      // 生效筛选条件（AND，≤10）
	SortField string       `json:"sort_field"` // 空 = 默认排序
	SortDesc  bool         `json:"sort_desc"`  // 仅 SortField 非空时有意义
	GroupBy   string       `json:"group_by"`   // 空 = 平铺
}

const prefsID = 1 // 单行表固定主键

// LoadViewPrefs 读取视图偏好；无行或 JSON 解析失败返回 nil（视为无偏好）。
// 字段联动清理：指向已不存在字段的条件剔除，排序/分组字段失效则复位为空。
func (r *TaskRepo) LoadViewPrefs() (*ViewPrefs, error) {
	var raw sql.NullString
	err := r.db.QueryRow(`SELECT prefs FROM app_prefs WHERE id = ?`, prefsID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取视图偏好失败: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	prefs := &ViewPrefs{}
	if err := json.Unmarshal([]byte(raw.String), prefs); err != nil {
		// 偏好损坏不应阻塞启动：丢弃并按无偏好处理。
		return nil, nil
	}
	prefs.sanitizeAgainstFields(r)
	return prefs, nil
}

// sanitizeAgainstFields 把引用了已删字段的偏好项清理掉（加载时调用）。
func (p *ViewPrefs) sanitizeAgainstFields(r *TaskRepo) {
	custom, err := r.fieldsByName()
	if err != nil {
		// 字段加载失败时按「全部存活」处理：宁可保留可能过期的偏好，
		// 不因清理逻辑吞掉加载错误（查询时 validateQuery 会给出真实报错）。
		return
	}
	alive := func(name string) bool {
		if name == "" {
			return false
		}
		if _, ok := custom[name]; ok {
			return true
		}
		_, kind := builtinColumn(name)
		return kind != ""
	}
	conds := make([]FilterCond, 0, len(p.Conds))
	for _, cond := range p.Conds {
		if alive(cond.Field) {
			conds = append(conds, cond)
		}
	}
	p.Conds = conds
	if p.SortField != "" && !alive(p.SortField) {
		p.SortField = ""
		p.SortDesc = false
	}
	if p.GroupBy != "" && !alive(p.GroupBy) {
		p.GroupBy = ""
	}
}

// SaveViewPrefs 全量覆盖写入视图偏好（UPSERT，可重复调用）。
func (r *TaskRepo) SaveViewPrefs(prefs *ViewPrefs) error {
	if prefs == nil {
		return fmt.Errorf("视图偏好为空")
	}
	data, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("序列化视图偏好失败: %w", err)
	}
	_, err = r.db.ExecContext(context.Background(),
		`INSERT INTO app_prefs (id, prefs) VALUES (?, ?)
		 ON CONFLICT(id) DO UPDATE SET prefs = excluded.prefs`,
		prefsID, string(data))
	if err != nil {
		return fmt.Errorf("保存视图偏好失败: %w", err)
	}
	return nil
}
