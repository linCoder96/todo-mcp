// Package assets 暴露内嵌图标资源，供 GUI 窗口与托盘使用（design-viva.md §8.1）。
package assets

import _ "embed"

// IconPNG 是 assets/icon.png 的原始字节（1024px 蓝色勾选图标）。
//
//go:embed icon.png
var IconPNG []byte
