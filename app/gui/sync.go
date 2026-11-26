// 外部写入同步（2026-11-25）：MCP 进程往共享的 SQLite 文件写入后，窗口不必
// 先获得焦点就自行重查。
//
// 这是 AGENTS.md「Background work」里的长活 watcher 形态：goroutine 内只读 repo
// （database/sql 并发安全），不碰 model；重查经事件循环线程执行。
package gui

import (
	"time"
)

// syncInterval 探测外部写入的间隔。每次只读一个整数（PRAGMA data_version），
// 对一个常驻托盘的应用可以忽略不计。
const syncInterval = 2 * time.Second

// watchExternalWrites 每 every 读一次数据版本；与上次不同（即 MCP 进程提交过）
// 时调用 changed。changed 运行在本 goroutine 上，应只做投递。stop 关闭后返回；
// 传 nil 表示永不停止（应用进程退出即结束）。
// 首次读取失败时下一轮重试，在拿到第一个读数前不会报告变化。
func (m *model) watchExternalWrites(every time.Duration, stop <-chan struct{}, changed func()) {
	last, err := m.repo.DataVersion()
	have := err == nil
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		version, err := m.repo.DataVersion()
		if err != nil {
			continue // 下一轮重试
		}
		if have && version != last {
			changed()
		}
		last, have = version, true
	}
}

// refreshAfterExternalWrite 外部写入后的重查，由 watcher 经事件循环线程调用
// （与 OnActivate 的聚焦重查同一套逻辑）。调用方负责请求重绘。
func (m *model) refreshAfterExternalWrite() {
	if err := m.refresh(); err != nil {
		m.errorText = err.Error()
	}
}
