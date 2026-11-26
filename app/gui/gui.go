// Package gui 实现 GUI 模式装配（design-viva.md §7/§8.1）。
//
// 重要：本包没有包级 init() 副作用——窗口、GPU、托盘、goroutine 一律只在
// Run() 内部创建，保证 --mcp 进程不因链接了本包而初始化任何 GUI 资源
// （design-viva.md §3.2 / §11 风险应对）。
package gui

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"os"
	"sync"

	"todo-mcp/app/store"
	"todo-mcp/assets"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/app/singleinstance"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// scaleRGBA 用标准库做最近邻缩放（不引入 x/image 依赖，design-viva.md §4.2）。
func scaleRGBA(source image.Image, size int) *image.RGBA {
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	bounds := source.Bounds()
	for y := range size {
		sourceY := bounds.Min.Y + y*bounds.Dy()/size
		for x := range size {
			sourceX := bounds.Min.X + x*bounds.Dx()/size
			scaled.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return scaled
}

// loadIcon 把内嵌 PNG 解码并缩放为窗口/托盘共用的 WindowIcon。
func loadIcon() app.WindowIcon {
	decoded, err := png.Decode(bytes.NewReader(assets.IconPNG))
	if err != nil {
		log.Printf("图标解码失败: %v", err)
		return app.WindowIcon{}
	}
	const size = 512
	return app.WindowIcon{Size: size, RGBA: scaleRGBA(decoded, size).Pix}
}

// Run 启动 GUI：单实例防多开 + 主窗口 + 托盘（显示/隐藏/退出）+
// 关闭窗口=隐藏到托盘。仅托盘菜单"退出"调 application.Quit 真正结束进程。
func Run() {
	// singleinstance 必须是 GUI 分支的第一步（design-viva.md §7.1）。
	// onLaunch 在独立 goroutine 上执行，用 application.Post 送回事件循环线程。
	var mu sync.Mutex
	var focus func()
	claim, primary, err := singleinstance.SingleInstance("com.todomcp.app", os.Args[1:], func(_ []string) {
		mu.Lock()
		post := focus
		mu.Unlock()
		if post != nil {
			post()
		}
	})
	if err != nil {
		log.Printf("单实例注册失败，继续启动: %v", err)
	}
	defer claim.Close()
	if !primary {
		return
	}

	icon := loadIcon()

	// GUI 进程内唯一长连接；WAL / busy_timeout / 单连接由 store.Open 统一配置。
	db, err := store.Open()
	if err != nil {
		log.Printf("打开数据库失败: %v", err)
		return
	}
	defer db.Close()
	viewModel := newModel(store.NewTaskRepo(db))
	// 聚焦/自身写入后重查并请求重绘（design-viva.md §3.3 第 5 项）
	invalidator := &app.Invalidator{}

	err = app.RunApplication(func(application *app.Application) error {
		var window *app.Window

		// 深色主题跟随系统（2026-11-25 ④）：初值按当前外观，切换回调实时换主题。
		if app.SystemAppearance() == app.AppearanceDark {
			viewModel.theme = gfx.DarkTheme()
		}
		app.WatchSystemAppearance(func() {
			if app.SystemAppearance() == app.AppearanceDark {
				viewModel.theme = gfx.DarkTheme()
			} else {
				viewModel.theme = gfx.LightTheme()
			}
			invalidator.Invalidate()
		})

		config := app.Config{
			Title:       "待办",
			Width:       880,
			Height:      640,
			MinWidth:    480,
			MinHeight:   360,
			Icon:        icon,
			Invalidator: invalidator,
			OnActivate: func() {
				if err := viewModel.refresh(); err != nil {
					viewModel.errorText = err.Error()
				}
				invalidator.Invalidate()
			},
			OnCloseRequest: func(_ func()) {
				// 刻意不调用 allow：关闭按钮 = 隐藏到托盘（design-viva.md §7.1）
				if window != nil {
					window.Hide()
				}
			},
			// 页面没有处理的按键：单元格编辑态下 Esc 放弃修改
			OnKey: viewModel.Key,
			// Ctrl+V 往详情抽屉粘贴图片（聚焦的文本框自己消费剪贴板）
			OnShortcut: viewModel.Shortcut,
			// 系统拖文件：只有详情抽屉的虚线框认领（见 images.go dropZone）
			OnFileDrop: viewModel.FileDrop,
		}
		created, err := ui.OpenWindow(application, config, ui.PageConfig{View: viewModel.page, Theme: &viewModel.theme})
		if err != nil {
			return err
		}
		window = created

		mu.Lock()
		focus = func() {
			application.Post(func() { window.Show() })
		}
		mu.Unlock()

		// 到期提醒（2026-11-25 ⑤）：后台定时检查，通知经 application.Post
		// 回事件循环线程发送（SendNotification 需在宿主线程）。
		go viewModel.remindLoop(application)

		// 外部写入同步（2026-11-25）：MCP 进程写库后，重查经 application.Post
		// 回事件循环线程执行（model 只属于窗口线程）。
		go viewModel.watchExternalWrites(syncInterval, nil, func() {
			application.Post(func() {
				viewModel.refreshAfterExternalWrite()
				invalidator.Invalidate()
			})
		})

		// 托盘：左键切换显示/隐藏，右键原生菜单三项（design.md §5 第 7 项）
		_, err = application.AddStatusItem(app.StatusItemConfig{
			Tooltip: "待办",
			Icon:    icon.StatusIcon(32),
			OnClick: func(click app.StatusClick) {
				if click.PanelDismissed {
					return
				}
				if window.Hidden() {
					window.Show()
				} else {
					window.Hide()
				}
			},
			Menu: func() []app.MenuItem {
				return []app.MenuItem{
					{Title: "显示主窗口", OnSelect: window.Show, Disabled: !window.Hidden()},
					{Title: "隐藏主窗口", OnSelect: window.Hide, Disabled: window.Hidden()},
					{Separator: true},
					{Title: "退出", OnSelect: application.Quit},
				}
			},
		})
		return err
	})
	if err != nil {
		log.Printf("GUI 启动失败: %v", err)
	}
}
