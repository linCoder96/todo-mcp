// 图片附件（2026-11-26）：行内徽标数据 + 任务详情抽屉。
// 缩略图解码在 ui.Effect 里异步跑（页面函数不落盘 IO）；解码目标 ≤thumbMaxDim
// 设备像素，避免大图整张进显存。单张损坏跳过，不拖垮整个抽屉。
package gui

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"todo-mcp/app/store"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// thumbMaxDim 缩略图解码目标的最大边（设备像素；2x 缩放下显示 ≤140 逻辑像素）。
const thumbMaxDim = 280

// thumb 一张加载好的缩略图：元数据 + 预乘 BGRA 位图 + 显示尺寸（逻辑像素）。
type thumb struct {
	image  store.TaskImage
	bitmap gfx.Bitmap
	width  float32
	height float32
}

// detailKey 是缩略图 Effect 的键：任务 id + 代数。附件变化时代数 +1，
// Effect 视为新键，取消旧加载、重新读取（AGENTS「Background work」）。
type detailKey struct {
	task string
	gen  int
}

// newThumbLoad 每个模型一个 Effect 状态（newModel 创建；run 闭包捕获 repo，
// 只做文件读取与像素运算，不碰任何 UI 状态）。NewEffectKeyed 把键传给 run。
func newThumbLoad(repo *store.TaskRepo) *ui.EffectState[[]thumb, detailKey] {
	return ui.NewEffectKeyed[[]thumb, detailKey](func(ctx context.Context, key detailKey) ([]thumb, error) {
		return loadThumbs(ctx, repo, key.task)
	})
}

// loadThumbs 读取任务的全部图片并解码为缩略图；单张失败跳过。
func loadThumbs(ctx context.Context, repo *store.TaskRepo, taskID string) ([]thumb, error) {
	images, err := repo.ListImages(taskID)
	if err != nil {
		return nil, err
	}
	thumbs := make([]thumb, 0, len(images))
	for _, img := range images {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		path, err := repo.ImagePath(taskID, img.StoredName)
		if err != nil {
			continue
		}
		bitmap, width, height, err := decodeThumbnail(path)
		if err != nil {
			continue
		}
		thumbs = append(thumbs, thumb{image: *img, bitmap: bitmap, width: width, height: height})
	}
	return thumbs, nil
}

// decodeThumbnail 解码图片文件 → 等比缩小（块平均）→ 预乘 BGRA（gfx.Bitmap 口径，
// 见 FileIcon 的构造方式）。返回位图与显示尺寸（逻辑像素，≤ thumbMaxDim/2）。
func decodeThumbnail(path string) (gfx.Bitmap, float32, float32, error) {
	file, err := os.Open(path)
	if err != nil {
		return gfx.Bitmap{}, 0, 0, err
	}
	decoded, _, err := image.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return gfx.Bitmap{}, 0, 0, err
	}
	if closeErr != nil {
		return gfx.Bitmap{}, 0, 0, closeErr
	}

	src := toNRGBA(decoded)
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dstW, dstH := srcW, srcH
	if max(srcW, srcH) > thumbMaxDim {
		dstW = max(1, srcW*thumbMaxDim/max(srcW, srcH))
		dstH = max(1, srcH*thumbMaxDim/max(srcW, srcH))
	}
	pix := make([]byte, dstW*dstH*4)
	for ty := 0; ty < dstH; ty++ {
		sy0, sy1 := block(ty, srcH, dstH)
		for tx := 0; tx < dstW; tx++ {
			sx0, sx1 := block(tx, srcW, dstW)
			var r, g, b, a, n uint32
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := src.NRGBAAt(sx, sy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
					a += uint32(c.A)
					n++
				}
			}
			i := (ty*dstW + tx) * 4
			alpha := a / n
			pix[i] = premul(b, alpha, n)
			pix[i+1] = premul(g, alpha, n)
			pix[i+2] = premul(r, alpha, n)
			pix[i+3] = byte(alpha)
		}
	}
	bitmap := gfx.Bitmap{Pix: pix, Width: dstW, Height: dstH, Stride: dstW * 4}
	return bitmap, float32(dstW) / 2, float32(dstH) / 2, nil
}

// block 给出目标像素 i 对应的源像素区间 [lo, hi)（块平均的块）。
func block(i, src, dst int) (int, int) {
	lo := i * src / dst
	hi := (i + 1) * src / dst
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// premul 把源块平均后的通道值预乘到 alpha 上（gfx.Bitmap 要预乘 BGRA）。
func premul(sum, alpha, n uint32) byte {
	value := sum / n
	return byte((value*alpha + 127) / 255)
}

// toNRGBA 把任意解码结果统一为 *image.NRGBA（非预乘 RGBA），便于块平均。
func toNRGBA(img image.Image) *image.NRGBA {
	if nrgba, ok := img.(*image.NRGBA); ok {
		return nrgba
	}
	bounds := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Src)
	return dst
}

// ---- 模型动作（都在窗口线程上被调用）----

// openDetails 打开任务详情抽屉；缩略图交给 Effect 按新键加载。
func (m *model) openDetails(task *store.Task) {
	m.imageGen++
	m.detailKey = detailKey{task: task.ID, gen: m.imageGen}
	m.detailOpen = true
	m.thumbs = nil
	m.detailFiles = nil
	m.confirmID = ""
	m.syncDetailFiles()
}

// syncDetail 在每次 refresh 尾部调用：任务被删则收起抽屉；抽屉打开且图片
// 数与已加载缩略图不一致时 bump 代数（Effect 视为新键重载）——抽屉内添加/
// 删除与 MCP 外部写入都经这条路径刷新。
func (m *model) syncDetail() {
	if !m.detailOpen {
		return
	}
	if _, err := m.repo.Get(m.detailKey.task); err != nil {
		m.detailOpen = false
		m.thumbs = nil
		m.detailFiles = nil
		return
	}
	count := -1
	for i := range m.tasks {
		if m.tasks[i].Task != nil && m.tasks[i].ID == m.detailKey.task {
			count = len(m.tasks[i].Images)
			break
		}
	}
	if count >= 0 && count != len(m.thumbs) {
		m.imageGen++
		m.detailKey.gen = m.imageGen
	}
	m.syncDetailFiles()
}

// afterImageChange 添加/删除图片后的统一收尾：重查（syncDetail 会 bump 代数）。
func (m *model) afterImageChange(failErr error) {
	if failErr == nil {
		m.errorText = ""
	}
	if err := m.refresh(); err != nil && m.errorText == "" {
		m.errorText = err.Error()
	}
}

// attachFiles 把拖入/选中的文件挂到抽屉当前任务：可解码的图片走图片附件，
// 其它文件拷入应用数据目录做文件附件；逐张尝试，单张失败记首个错误。
func (m *model) attachFiles(paths []string) {
	var fail error
	for _, path := range paths {
		if _, err := m.repo.AddImageFile(m.detailKey.task, path); err == nil {
			continue
		}
		if _, err := m.repo.AddFileCopy(m.detailKey.task, path); err != nil && fail == nil {
			fail = err
		}
	}
	if fail != nil {
		m.errorText = fmt.Sprintf("添加附件失败：%v", fail)
	}
	m.afterAttachmentChange(fail)
}

// afterAttachmentChange 附件变化统一收尾：重查（图片代数会 bump，文件列表同步）。
func (m *model) afterAttachmentChange(failErr error) {
	if failErr == nil {
		m.errorText = ""
	}
	m.syncDetailFiles()
	if err := m.refresh(); err != nil && m.errorText == "" {
		m.errorText = err.Error()
	}
}

// pasteFromClipboard 把剪贴板里的图片挂到抽屉当前任务（按钮与 Ctrl+V 共用）。
func (m *model) pasteFromClipboard() {
	png, err := app.ClipboardImagePNG()
	if err != nil || len(png) == 0 {
		m.errorText = "剪贴板里没有图片"
		return
	}
	if _, err := m.repo.AddImageBytes(m.detailKey.task, "剪贴板.png", png); err != nil {
		m.errorText = err.Error()
		return
	}
	m.afterImageChange(nil)
}

// pickImages 打开系统文件对话框选图片（可多选）。
func (m *model) pickImages() {
	paths, err := app.OpenFiles(app.OpenFileOptions{
		Title:         "添加图片",
		AllowMultiple: true,
		Filters: []app.FileFilter{
			{Name: "PNG 图片", Pattern: "*.png"},
			{Name: "JPEG 图片", Pattern: "*.jpg;*.jpeg"},
			{Name: "GIF 图片", Pattern: "*.gif"},
		},
	})
	if err != nil {
		m.errorText = fmt.Sprintf("打开文件对话框失败：%v", err)
		return
	}
	m.attachFiles(paths)
}

// removeThumbImage 删除抽屉里的一张图片。
func (m *model) removeThumbImage(imageID string) {
	if err := m.repo.RemoveImage(m.detailKey.task, imageID); err != nil {
		m.errorText = err.Error()
		return
	}
	m.afterImageChange(nil)
}

// Shortcut 处理 Ctrl+V 粘贴（app.Config.OnShortcut；无文本框聚焦时才轮到这里，
// 聚焦的文本框自己消费剪贴板）。
func (m *model) Shortcut(event gfx.KeyEvent) gfx.EventResult {
	if !m.detailOpen || event.Release {
		return gfx.EventResult{}
	}
	if event.Modifiers&gfx.ModifierControl == 0 || event.Text != "v" {
		return gfx.EventResult{}
	}
	m.pasteFromClipboard()
	return gfx.EventResult{Handled: true, Repaint: true}
}

// FileDrop 详情抽屉拖放区接收文件（app.Config.OnFileDrop）；其余位置不接管，
// 系统显示不可拖入。
func (m *model) FileDrop(event gfx.FileDropEvent) gfx.EventResult {
	if !m.detailOpen || event.Target != m.drawerDropID {
		return gfx.EventResult{}
	}
	switch event.Phase {
	case gfx.FileDropOver, gfx.FileDropLeave:
		return gfx.EventResult{Handled: true}
	case gfx.FileDropDrop:
		m.attachFiles(event.Paths)
		return gfx.EventResult{Handled: true, Repaint: true}
	}
	return gfx.EventResult{}
}

// ---- 抽屉声明 ----

// detailDrawer 任务详情抽屉（附件：图片 + 文件），底部添加。
func (m *model) detailDrawer(c ui.C) {
	title := "附件"
	if task := m.taskOf(m.detailKey.task); task != nil {
		title = task.Title
	}
	ui.Drawer(c, &m.detailOpen, title, ui.DrawerSpec{Side: ui.DrawerRight, Size: 400},
		func() { m.detailBody(c) },
		func() { m.detailFooter(c) })
}

func (m *model) detailBody(c ui.C) {
	// 声明异步加载（抽屉开着就声明；换任务/附件变化 = 换键重载）
	ui.Effect(c, m.thumbLoad, m.detailKey)
	if thumbs, err, ok := m.thumbLoad.Take(); ok {
		if err != nil {
			m.errorText = err.Error()
		} else {
			m.thumbs = thumbs
		}
	}
	theme := m.pageTheme
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			if len(m.thumbs) == 0 {
				ui.Text(c, "还没有图片").FontSize(14).Weight(gfx.WeightMedium)
				ui.Text(c, "用下方按钮添加，或把图片文件拖进虚线框").FontSize(theme.Metrics.FontSizeS).Muted()
			}
			if len(m.thumbs) > 0 {
				ui.Flow(c).Gap(8).LineGap(12).Children(func() {
					for i := range m.thumbs {
						m.thumbCard(c, &m.thumbs[i])
					}
				})
			}
			m.dropZone(c)
			ui.Divider(c)
			ui.Text(c, "文件").FontSize(12).Muted()
			m.filesPanel(c)
		})
	})
}

func (m *model) thumbCard(c ui.C, th *thumb) {
	theme := m.pageTheme
	ui.Column(c).Gap(4).Width(th.width).Children(func() {
		ui.Image(c, th.bitmap).Width(th.width).Height(th.height).
			Radius(theme.Metrics.RadiusItem()).Clip()
		ui.Text(c, th.image.FileName).Ellipsis().FontSize(theme.Metrics.FontSizeS).Muted()
		if ui.Button(c, "删除图片 "+th.image.FileName).XSmall().Ghost().Destructive().Clicked() {
			m.removeThumbImage(th.image.ID)
		}
	})
}

// dropZone 拖放目标：身份记到 m.drawerDropID，FileDrop 按它认领。
// 图片文件直接挂图；其它文件拷贝为文件附件（upload 模式）。
func (m *model) dropZone(c ui.C) {
	theme := m.pageTheme
	id := ui.ID(c)
	m.drawerDropID = id
	ui.Row(c).FillWidth().Height(56).Center().
		Background(theme.Muted).Radius(theme.Metrics.RadiusControl).
		Border(1, theme.Border).Dashed().
		Hit(id, "拖放附件到此处").Children(func() {
		ui.Text(c, "拖放图片或文件到此处").FontSize(theme.Metrics.FontSizeS).Muted()
	})
}

func (m *model) detailFooter(c ui.C) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "添加图片").Small().Clicked() {
			m.pickImages()
		}
		if ui.Button(c, "从剪贴板粘贴").Small().Clicked() {
			m.pasteFromClipboard()
		}
		if ui.Button(c, "添加文件").Small().Clicked() {
			m.pickFiles()
		}
		if ui.Button(c, "从剪贴板引用").Small().Clicked() {
			m.attachRefFromClipboard()
		}
	})
}
