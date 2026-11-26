// 文件附件（2026-11-26）：与图片附件（images.go）同源的详情抽屉扩展。
// 抽屉从「图片附件」改为「附件」，上半部是缩略图网格，下半部是文件列表：
//   - copy 模式：文件已拷贝进应用数据目录，可「打开文件」；
//   - ref 模式：只存源路径引用，可「复制引用地址」（拷贝到剪贴板）。
//
// 添加方式与图片一致：文件对话框（所有文件）、拖放、剪贴板粘贴的文件。
// 删除用 remove_file（copy 连文件删，ref 只删记录）。
package gui

import (
	"fmt"
	"os"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/ui"
)

// syncDetailFiles 在 refresh 尾部同步抽屉当前任务的文件列表（与 syncDetail 同源：
// 任务被删则清空；任务仍在则取最新文件列表，供抽屉直接绘制，不经异步 Effect——
// 文件没有解码成本，列表是纯元数据，取一次即可）。
func (m *model) syncDetailFiles() {
	if !m.detailOpen {
		return
	}
	files, err := m.repo.ListFiles(m.detailKey.task)
	if err != nil {
		m.errorText = err.Error()
		return
	}
	m.detailFiles = nil
	for _, file := range files {
		m.detailFiles = append(m.detailFiles, *file)
	}
}

// pickFiles 打开系统文件对话框选任意文件（可多选），拷入应用数据目录。
func (m *model) pickFiles() {
	paths, err := app.OpenFiles(app.OpenFileOptions{
		Title:         "添加文件",
		AllowMultiple: true,
	})
	if err != nil {
		m.errorText = fmt.Sprintf("打开文件对话框失败：%v", err)
		return
	}
	m.attachCopyFiles(paths)
}

// attachCopyFiles 把磁盘文件逐份拷贝到抽屉当前任务（upload 模式）。
func (m *model) attachCopyFiles(paths []string) {
	var fail error
	for _, path := range paths {
		if _, err := m.repo.AddFileCopy(m.detailKey.task, path); err != nil && fail == nil {
			fail = err
		}
	}
	if fail != nil {
		m.errorText = fmt.Sprintf("添加文件失败：%v", fail)
	}
	m.afterAttachmentChange(fail)
}

// attachRefFromClipboard 把剪贴板里的文件路径存为引用（复制引用地址）。
func (m *model) attachRefFromClipboard() {
	paths, err := app.ClipboardFiles()
	if err != nil || len(paths) == 0 {
		m.errorText = "剪贴板里没有文件"
		return
	}
	var fail error
	for _, path := range paths {
		if _, err := m.repo.AddFileRef(m.detailKey.task, path); err != nil && fail == nil {
			fail = err
		}
	}
	if fail != nil {
		m.errorText = fmt.Sprintf("添加文件引用失败：%v", fail)
	}
	m.afterAttachmentChange(fail)
}

// removeDetailFile 删除抽屉里的一个文件附件（copy 连文件删，ref 只删记录）。
func (m *model) removeDetailFile(fileID string) {
	if err := m.repo.RemoveFile(m.detailKey.task, fileID); err != nil {
		m.errorText = err.Error()
		return
	}
	m.afterAttachmentChange(nil)
}

// openDetailFile 打开 copy 模式文件的系统默认程序（OpenPath 拒绝可执行文件，
// 反馈给用户）。ref 模式提示用「复制引用地址」。
func (m *model) openDetailFile(file store.TaskFile) {
	if file.Kind != store.FileCopy {
		m.toaster.Show("这是引用地址，不是本地副本", "用「复制引用地址」取路径", ui.ToastInfo)
		return
	}
	path, err := m.repo.FilePath(file.TaskID, file.StoredName)
	if err != nil {
		m.errorText = err.Error()
		return
	}
	if err := app.OpenPath(path); err != nil {
		m.errorText = fmt.Sprintf("打开文件失败：%v", err)
	}
}

// copyFilePath 把文件的可用绝对路径复制到剪贴板：copy 模式 = 应用数据目录里的
// 副本；ref 模式 = 源路径。返回给 toast 显示。
func (m *model) copyFilePath(file store.TaskFile) string {
	path := file.SrcPath
	if file.Kind == store.FileCopy {
		var err error
		path, err = m.repo.FilePath(file.TaskID, file.StoredName)
		if err != nil {
			m.errorText = err.Error()
			return ""
		}
	}
	if err := app.SetClipboardText(path); err != nil {
		m.errorText = fmt.Sprintf("复制失败：%v", err)
		return ""
	}
	return path
}

// fileExists 判断文件是否存在（ref 引用有效性检查）。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// fileAccessible 判断文件是否可用：copy 目录文件存在，或 ref 源路径存在。
func (m *model) fileAccessible(file store.TaskFile) bool {
	path := file.SrcPath
	if file.Kind == store.FileCopy {
		var err error
		path, err = m.repo.FilePath(file.TaskID, file.StoredName)
		if err != nil {
			return false
		}
	}
	return fileExists(path)
}

// fileSizeLabel 文件大小展示文案。
func fileSizeLabel(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// fileDisplay 文件行主文案：ref 模式加「（引用）」后缀；copy 模式即文件名。
func fileDisplay(file store.TaskFile) string {
	if file.Kind == store.FileRef {
		return file.FileName + "（引用）"
	}
	return file.FileName
}

// filesPanel 抽屉内的文件区：每个文件一张两行卡片——
//   首行：引用徽标 + 文件名（Grow+Ellipsis，不再被按钮挤没）+ 大小；
//   次行：操作按钮（Flow 自动换行，抽屉 400 宽装不下三个长按钮也不会裁切）。
func (m *model) filesPanel(c ui.C) {
	theme := m.pageTheme
	if len(m.detailFiles) == 0 {
		ui.Text(c, "还没有文件").FontSize(theme.Metrics.FontSizeS).Muted()
		return
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		for i := range m.detailFiles {
			file := m.detailFiles[i] // 按值捕获：c.After 闭包不依赖切片存续
			ui.Column(c.Key(file.ID)).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Gap(6).Items(ui.Center).Children(func() {
					if file.Kind == store.FileRef {
						// 引用标记徽标：Badge 文本即可达名
						ui.Badge(c, "引用").Secondary()
					}
					name := ui.Text(c, fileDisplay(file)).Grow(1).Ellipsis().FontSize(theme.Metrics.FontSizeS)
					if !m.fileAccessible(file) {
						name.Muted()
					}
					ui.Text(c, fileSizeLabel(file.Size)).Shrink(0).FontSize(theme.Metrics.FontSizeS).Muted()
				})
				ui.Flow(c).Gap(6).Children(func() {
					if ui.Button(c, "打开文件:"+file.FileName).XSmall().Ghost().Clicked() {
						c.After(func() { m.openDetailFile(file) })
					}
					if ui.Button(c, "复制引用地址:"+file.FileName).XSmall().Ghost().Clicked() {
						c.After(func() {
							if path := m.copyFilePath(file); path != "" {
								m.toaster.Show("已复制引用地址", path, ui.ToastSuccess)
							}
						})
					}
					if ui.Button(c, "删除文件:"+file.FileName).XSmall().Ghost().Destructive().Clicked() {
						c.After(func() { m.removeDetailFile(file.ID) })
					}
				})
			})
		}
	})
}

// detailFooter 追加文件操作按钮（添加文件 / 从剪贴板加入文件引用）。
func (m *model) detailFooterFiles(c ui.C) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "添加文件").Small().Clicked() {
			m.pickFiles()
		}
		if ui.Button(c, "从剪贴板引用").Small().Clicked() {
			m.attachRefFromClipboard()
		}
	})
}
