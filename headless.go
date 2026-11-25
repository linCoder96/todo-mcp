//go:build vivahead

package main

// 无头检查入口（AGENTS.md「Check it without a window」）：带 vivahead 标签构建、
// 以 VIVA_HEADLESS=1 运行时，窗口改为在 stdin 上响应 layout/tree/shot/expect 命令。
// 仅用于开发期核对画面，正式构建（不带标签）不包含本文件。
import _ "github.com/vivaui/viva/headless/auto"
