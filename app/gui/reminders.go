// 到期提醒（2026-11-25 ⑤）：后台定时检查进行中任务的截止时间，
// 即将到期（60 分钟内）/新近逾期（24 小时内）经系统通知提醒。
// 同一任务的同一到期时间只提醒一次；应用重启后重新计时。
package gui

import (
	"fmt"
	"log"
	"os"
	"time"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
)

const (
	remindInterval = 30 * time.Second
	remindAhead    = 60 * time.Minute
	remindOverdue  = 24 * time.Hour
)

type reminder struct{ title, body string }

// remindLoop 长活 watcher（AGENTS「Background work」的 goroutine 形态）：
// 自查 repo（database/sql 并发安全），通知经 application.Post 送回事件循环
// 线程发送——SendNotification 需在宿主线程调用。
func (m *model) remindLoop(application *app.Application) {
	notified := map[string]int64{} // 仅本 goroutine 读写，无需加锁
	ticker := time.NewTicker(remindInterval)
	defer ticker.Stop()
	for range ticker.C {
		reminders := m.dueReminders(time.Now(), notified)
		if len(reminders) == 0 {
			continue
		}
		application.Post(func() {
			for _, r := range reminders {
				if err := app.SendNotification(app.Notification{Title: r.title, Body: r.body}); err != nil {
					log.New(os.Stderr, "", 0).Printf("发送通知失败: %v", err)
				}
			}
		})
	}
}

// dueReminders 扫描进行中任务，产出本轮应提醒的条目并记录去重水位。
func (m *model) dueReminders(now time.Time, notified map[string]int64) []reminder {
	tasks, err := m.repo.List(store.TaskFilter{Status: new(0)}, 1000, 0)
	if err != nil {
		return nil // 下一轮重试；不打断 ticker
	}
	reminders := make([]reminder, 0, 2)
	for _, task := range tasks {
		if task.DueAt == nil {
			continue
		}
		due := time.UnixMilli(*task.DueAt)
		until := due.Sub(now)
		var found reminder
		switch {
		case until > 0 && until <= remindAhead:
			found = reminder{"待办即将到期", fmt.Sprintf("《%s》%s 到期", task.Title, due.In(time.Local).Format(localTimeLayout))}
		case until <= 0 && until > -remindOverdue:
			found = reminder{"待办已逾期", fmt.Sprintf("《%s》原定 %s", task.Title, due.In(time.Local).Format(localTimeLayout))}
		}
		if found.title == "" || notified[task.ID] == *task.DueAt {
			continue
		}
		notified[task.ID] = *task.DueAt
		reminders = append(reminders, found)
	}
	return reminders
}
